package render

// frame_prep.go — the frame's CPU preparation, off the main thread.
//
// Until this, nothing the renderer did ran anywhere but the thread holding
// the GL context, so every millisecond of pure CPU work — walking the level's
// sources for live GI, filling and sorting a few thousand particles — was a
// millisecond the GPU waited for its next command. Two of those pieces read
// nothing the passes before them write, and are started on goroutines as soon
// as what they read is settled, then joined where their answer is used:
//
//   - GI's source diff over the level's chunks, entities and placements
//     (giSourceSet.collectWorld): started once the chunks are synced at the
//     top of the frame, joined in UpdateGI after the sun's shadows and the
//     clouds'. The rest of the diff (the foliage's crowns, the sweep, the
//     flattening) and every GL call stay where they were.
//   - the camera's sprite gather (cameraSpriteBatches): every particle
//     layer's fill and back-to-front sort and the batches made of them,
//     started as soon as the camera's frustum is known, joined by the cutout
//     sprite pass after the whole opaque world.
//
// A goroutine is started only when its answer is sure to be asked for — the
// conditions are the consumer's own — so nothing is ever computed for a frame
// and then thrown away half applied. ParallelFramePrep off (or
// FOUNDRY_PARALLEL_PREP=0) does all of it where it always was, in the order it
// always was; the tests draw both ways and compare.

import (
	"sync"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// ParallelFramePrep switches the goroutines; see the file comment.
var ParallelFramePrep = gfx.EnvFlag("FOUNDRY_PARALLEL_PREP", true)

// prepStarted counts the jobs started, for the tests to see both ran.
var prepStarted struct{ gi, sprites int }

// prepJob is one piece of preparation running on a goroutine.
type prepJob struct {
	wg      sync.WaitGroup
	running bool
	// panicked carries a panic on the goroutine to the thread that waits,
	// where the engine's own recovery logs it — a goroutine's panic would
	// otherwise end the process with nothing in the log.
	panicked any
}

func (j *prepJob) start(fn func()) {
	j.running = true
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				j.panicked = r
			}
		}()
		fn()
	}()
}

// wait joins the job, if one is running.
func (j *prepJob) wait() {
	if !j.running {
		return
	}
	j.wg.Wait()
	j.running = false
	if p := j.panicked; p != nil {
		j.panicked = nil
		panic(p)
	}
}

// ---------- GI's source diff ----------

var giPrep struct {
	job   prepJob
	frame uint64
	lvl   *world.Level
	set   *giSourceSet
}

// giSteady is whether UpdateGI will this frame go straight to the source
// diff on the set it already has — the level, the tier and the spacing all as
// they were — which is when the diff can be started ahead of it.
func giSteady(lvl *world.Level, o WorldDraw) bool {
	g := &gi
	if o.skip("gi") || !giWanted(lvl, o) || g.failed || g.field == nil || g.tier != gfx.GIQuality || g.sources == nil {
		return false
	}
	spacing := max(lvl.Lights.Global.GI.ProbeSpacing, world.GIProbeSpacingMin) * g.t.spacing
	return g.lvl == lvl && lvl.Name == g.lvlName && spacing == g.spacing
}

// prepareGI starts the frame's source diff on a goroutine, when it can be.
func prepareGI(lvl *world.Level, o WorldDraw) {
	giPrep.job.wait()
	if !ParallelFramePrep || !giSteady(lvl, o) {
		return
	}
	g := &gi
	// UpdateGI's own setting, made before it is read.
	g.sources.minModel = g.spacing * 0.5
	set := g.sources
	giPrep.frame, giPrep.lvl, giPrep.set = drawFrameNo, lvl, set
	prepStarted.gi++
	giPrep.job.start(func() { set.collectWorld(lvl) })
}

// giCollect is UpdateGI's source diff: the half the goroutine made, joined
// and finished, or the whole of it here.
func giCollect(lvl *world.Level) {
	s := gi.sources
	if giPrep.job.running {
		giPrep.job.wait()
		s.minModel = gi.spacing * 0.5
		if giPrep.frame == drawFrameNo && giPrep.lvl == lvl && giPrep.set == s {
			s.collectFinish(lvl)
			return
		}
		// Made for another set (never, while giSteady is UpdateGI's own
		// test). Left unfinished: a set's next collect carries over the
		// dirt a half one found (finishPending).
	}
	s.minModel = gi.spacing * 0.5
	s.collect(lvl)
}

// ---------- the camera's sprites ----------

var spritePrep struct {
	job   prepJob
	frame uint64
	lvl   *world.Level
	cull  Frustum
	eye   mgl32.Vec3
}

// prepareSprites starts the camera's sprite gather on a goroutine: everything
// cameraSpriteBatches will be asked for by the cutout pass, which is drawn
// with this frustum from this eye unless the pass is skipped.
func prepareSprites(lvl *world.Level, o WorldDraw, frustum Frustum, eye mgl32.Vec3) {
	spritePrep.job.wait()
	if !ParallelFramePrep || !spriteFrameLive() || o.Wireframe || o.skip("sprites") || !spritePassLive(lvl) {
		return
	}
	p := &spritePrep
	p.frame, p.lvl, p.cull, p.eye = drawFrameNo, lvl, frustum, eye
	prepStarted.sprites++
	p.job.start(func() {
		// The particle layers first, side by side; then the batches, made
		// from what they left, in the gather's own order.
		prefillSpriteLayers(lvl, &p.cull, eye)
		cameraSpriteBatches(lvl, &p.cull, eye)
	})
}

// joinSprites waits for the camera's gather, if one was started: called
// before anything else reads the frame's sprite state.
func joinSprites() { spritePrep.job.wait() }

// endFramePrep joins whatever is still running at the end of a frame, so
// nothing reaches into the next. A source diff nobody finished stays half
// done; its set's next collect carries its dirt over (finishPending).
func endFramePrep() {
	spritePrep.job.wait()
	giPrep.job.wait()
}
