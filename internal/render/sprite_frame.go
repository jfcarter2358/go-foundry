package render

// sprite_frame.go — what one frame's sprite gathers share.
//
// A frame gathers its sprites many times over: once per shadow cascade, per
// point light's cube face and per virtual-shadow-map rectangle (casters only),
// for the point-shadow and VSM signatures, and for the camera — whose answer
// the cutout pass, inside the opaque block, and the blended passes, after the
// transparent queue, both draw from. Nothing a gather reads moves in between:
// the particles are simulated before DrawWorld and not again until the next
// frame. So for the length of one DrawWorld:
//
//   - each particle layer is FILLED once (particle.System.Fill: the ramps, the
//     sheet cells, the tint), into storage kept from frame to frame, and every
//     gather copies from that;
//   - each emitter's bounds — a walk over every live particle with the size
//     ramp evaluated at each — are measured once, for every pass's cull;
//   - an alpha layer is SORTED back to front once, for the camera, and only
//     for the camera: a depth-only pass keeps the nearest depth whatever order
//     its quads arrive in, so the casters' gathers never sort at all;
//   - the camera's gather is made once and drawn by both of its passes.
//
// Outside a DrawWorld (a test, the editor's particle preview) none of this is
// kept and every gather is made from scratch, as it always was.

import (
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"unsafe"

	"foundry/internal/gfx"

	"foundry/internal/particle"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// spriteFrameCaching switches the per-frame sharing off, for the test that
// checks a frame's gathers come out the same either way.
var spriteFrameCaching = true

// spriteFrameState is one frame's shared gathering state.
type spriteFrameState struct {
	frame    uint64
	emitters []spriteEmitterFrame

	// The camera's gather.
	camValid bool
	camFrame uint64
	camLvl   *world.Level
	camEye   mgl32.Vec3
	camCull  Frustum
	camHas   bool
	cam      []spriteBatch
}

// spriteEmitterFrame is one emitter's share of the frame: its bounds and its
// layers' filled instances. e is the emitter it was made for, so a level whose
// emitters were replaced mid-frame cannot read another's.
type spriteEmitterFrame struct {
	frame  uint64
	e      *world.Emitter
	boxed  bool
	lo, hi mgl32.Vec3
	boxOK  bool
	layers []spriteLayerFill
}

// spriteLayerFill is one layer's instances for the frame.
type spriteLayerFill struct {
	frame uint64
	sys   *particle.System
	insts []particle.Instance
	// sorted says insts are in back-to-front order from sortEye — exactly the
	// order Fill(sortBackToFront) would have left them in — rather than the
	// pool's.
	sorted  bool
	sortEye mgl32.Vec3
	// depth is the farthest instance's squared distance from depthEye, for
	// the batch's own back-to-front order.
	depthOK  bool
	depthEye mgl32.Vec3
	depth    float32
	// keys is the sort's scratch, the layer's own so layers can be sorted
	// side by side (prefillSpriteLayers).
	keys []float32
}

var spriteFrame spriteFrameState

// spriteFrameLive reports whether this gather may share the frame's state.
func spriteFrameLive() bool {
	return spriteFrameCaching && drawFrameOpen
}

// emitterFrame is emitter ei's entry for this frame, reset when it was made in
// another frame or for another emitter.
func (f *spriteFrameState) emitterFrame(ei int, e *world.Emitter) *spriteEmitterFrame {
	if ei >= len(f.emitters) {
		if ei < cap(f.emitters) {
			f.emitters = f.emitters[:ei+1]
		} else {
			f.emitters = append(f.emitters, make([]spriteEmitterFrame, ei+1-len(f.emitters))...)
		}
	}
	ef := &f.emitters[ei]
	if ef.frame != drawFrameNo || ef.e != e {
		ef.frame, ef.e, ef.boxed = drawFrameNo, e, false
	}
	return ef
}

// bounds is the emitter's world box, measured once a frame.
func (ef *spriteEmitterFrame) bounds(lvl *world.Level) (mgl32.Vec3, mgl32.Vec3, bool) {
	if !ef.boxed {
		ef.lo, ef.hi, ef.boxOK = ef.e.WorldAABB(lvl)
		ef.boxed = true
	}
	return ef.lo, ef.hi, ef.boxOK
}

// layer is layer li's fill for this frame, filled (in the pool's order) the
// first time it is asked for.
func (ef *spriteEmitterFrame) layer(li int, sys *particle.System) *spriteLayerFill {
	if li >= len(ef.layers) {
		if li < cap(ef.layers) {
			ef.layers = ef.layers[:li+1]
		} else {
			ef.layers = append(ef.layers, make([]spriteLayerFill, li+1-len(ef.layers))...)
		}
	}
	lf := &ef.layers[li]
	if lf.frame != drawFrameNo || lf.sys != sys {
		lf.frame, lf.sys = drawFrameNo, sys
		lf.fill(sys)
	}
	return lf
}

// fill fills the layer in the pool's order: what Fill leaves before it sorts.
func (lf *spriteLayerFill) fill(sys *particle.System) {
	n := sys.Count()
	if cap(lf.insts) < n {
		lf.insts = make([]particle.Instance, n)
	}
	lf.insts = lf.insts[:n]
	lf.insts = lf.insts[:sys.Fill(lf.insts, mgl32.Vec3{}, false)]
	lf.sorted, lf.depthOK = false, false
}

// sortFrom puts the layer back to front from eye, as Fill(sortBackToFront)
// would: from the pool's order, by the same comparison, through the same
// sort — so the order, ties included, is the one an unshared gather draws.
func (lf *spriteLayerFill) sortFrom(sys *particle.System, eye mgl32.Vec3) {
	if lf.sorted && lf.sortEye == eye {
		return
	}
	if lf.sorted {
		// Sorted for another eye: back to the pool's order first.
		lf.fill(sys)
	}
	if cap(lf.keys) < len(lf.insts) {
		lf.keys = make([]float32, len(lf.insts))
	}
	sortInstancesBackToFront(lf.insts, lf.keys[:len(lf.insts)], eye)
	lf.sorted, lf.sortEye = true, eye
}

// maxDepth is the farthest instance's squared distance from eye.
func (lf *spriteLayerFill) maxDepth(eye mgl32.Vec3) float32 {
	if lf.depthOK && lf.depthEye == eye {
		return lf.depth
	}
	var d float32
	for i := range lf.insts {
		if v := lf.insts[i].Pos.Sub(eye).LenSqr(); v > d {
			d = v
		}
	}
	lf.depth, lf.depthEye, lf.depthOK = d, eye, true
	return d
}

// sortInstancesBackToFront is particle.System.Fill's sort, with each
// instance's distance worked out once rather than at every comparison.
//
// sort.Sort and sort.Slice are the same pattern-defeating quicksort, generated
// from one template, so given the same comparisons they make the same moves:
// this leaves exactly the order sort.Slice with Fill's comparison would. The
// distances are Fill's own expression, so every comparison comes out the same.
// keys is scratch of len(insts).
func sortInstancesBackToFront(insts []particle.Instance, keys []float32, eye mgl32.Vec3) {
	if len(insts) < 2 {
		return
	}
	s := instanceDepthSort{insts: insts, keys: keys[:len(insts)]}
	for i := range insts {
		s.keys[i] = insts[i].Pos.Sub(eye).LenSqr()
	}
	sort.Sort(s)
}

type instanceDepthSort struct {
	insts []particle.Instance
	keys  []float32
}

func (s instanceDepthSort) Len() int           { return len(s.insts) }
func (s instanceDepthSort) Less(a, b int) bool { return s.keys[a] > s.keys[b] }
func (s instanceDepthSort) Swap(a, b int) {
	s.insts[a], s.insts[b] = s.insts[b], s.insts[a]
	s.keys[a], s.keys[b] = s.keys[b], s.keys[a]
}

// The particle simulation's instance and the sprite pass's are the same
// fourteen floats in the same order, so a layer's fill is copied into a batch
// whole rather than field by field.
var _ = [1]struct{}{}[unsafe.Sizeof(particle.Instance{})-unsafe.Sizeof(SpriteInstance{})]
var _ = [1]struct{}{}[unsafe.Sizeof(SpriteInstance{})-spriteInstanceFloats*4]

func asSpriteInstances(insts []particle.Instance) []SpriteInstance {
	if len(insts) == 0 {
		return nil
	}
	return unsafe.Slice((*SpriteInstance)(unsafe.Pointer(&insts[0])), len(insts))
}

// cameraSpriteBatches is the camera's gather, made once a frame and shared by
// the cutout and the blended passes.
//
// Called on the goroutine frame_prep.go starts, and by the passes after they
// have joined it.
func cameraSpriteBatches(lvl *world.Level, cull *Frustum, camPos mgl32.Vec3) []spriteBatch {
	f := &spriteFrame
	if spriteFrameLive() && f.camValid && f.camFrame == drawFrameNo && f.camLvl == lvl && f.camEye == camPos &&
		f.camHas == (cull != nil) && (cull == nil || f.camCull == *cull) {
		return f.cam
	}
	gatherSprites(&f.cam, lvl, cull, camPos, false)
	f.camValid = spriteFrameLive()
	f.camFrame, f.camLvl, f.camEye, f.camHas = drawFrameNo, lvl, camPos, cull != nil
	if cull != nil {
		f.camCull = *cull
	}
	return f.cam
}

// prefillSpriteLayers does, for every emitter in view, what the camera's
// gather will ask of the frame's state — the bounds, each layer's fill, an
// alpha layer's sort and every layer's depth — spread over a few goroutines,
// an emitter to a goroutine at a time. Each emitter's entry is its own, so
// they share nothing; what the gather then finds is what it would have made
// itself, one emitter after another. Only inside a frame (spriteFrameLive).
func prefillSpriteLayers(lvl *world.Level, cull *Frustum, eye mgl32.Vec3) {
	n := len(lvl.Emitters)
	if !spriteFrameLive() || n < spritePrefillMin {
		return
	}
	f := &spriteFrame
	// The table is sized, and each entry claimed for this frame, before the
	// goroutines start: from here on each touches its own entries alone.
	for ei := range lvl.Emitters {
		f.emitterFrame(ei, &lvl.Emitters[ei])
	}
	workers := min(runtime.GOMAXPROCS(0), spritePrefillWorkers, n/spritePrefillMin+1)
	var next atomic.Int64
	var wg sync.WaitGroup
	var panicked atomic.Value
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicked.Store(r)
				}
			}()
			for {
				ei := int(next.Add(1) - 1)
				if ei >= n {
					return
				}
				prefillEmitter(lvl, ei, cull, eye)
			}
		}()
	}
	wg.Wait()
	if r := panicked.Load(); r != nil {
		panic(r)
	}
}

// spritePrefillMin is the fewest emitters worth a goroutine; spritePrefillWorkers
// the most goroutines.
const (
	spritePrefillMin     = 8
	spritePrefillWorkers = 8
)

// prefillEmitter is prefillSpriteLayers for emitter ei: gatherEmitters' own
// tests, and the work they lead to.
func prefillEmitter(lvl *world.Level, ei int, cull *Frustum, eye mgl32.Vec3) {
	e := &lvl.Emitters[ei]
	if !e.Enabled || !e.Loaded() {
		return
	}
	ef := &spriteFrame.emitters[ei]
	if cull != nil {
		if lo, hi, ok := ef.bounds(lvl); ok && !AABBInFrustum(*cull, lo, hi) {
			return
		}
	}
	for li, sys := range e.Systems {
		if sys.Count() == 0 || sys.Layer.Muted || sys.Layer.Render == particle.RenderVolume {
			continue
		}
		lf := ef.layer(li, sys)
		if sys.Layer.DrawBlend() == gfx.SpriteBlendAlpha {
			lf.sortFrom(sys, eye)
		}
		lf.maxDepth(eye)
	}
}
