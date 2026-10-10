package world

// emitter.go — Emitter, a placed instance of a .fpar effect.
//
// The split is the entity one: what an effect IS lives in
// particles/<name>.fpar, and where it is lives in the level that places it.
// Giving every torch in the game a lazier flame is then one file rather than a
// search across every level that ever placed one — the argument DESIGN.md makes
// for entity definitions, and it applies here for the same reason.
//
// So an emitter carries almost nothing of its own: an ID, a transform, what it
// rides, and the handful of knobs worth varying between two placements of the
// same effect (a rate scale, a tint, whether it starts running). Everything
// else is the document's.
//
// Systems is the live simulation, one per layer. Runtime-only and never
// serialized, like Volume.Geometry and Entity.VState: a level file says an
// explosion goes here, not where its four hundred sparks currently are.

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"

	"foundry/internal/particle"

	"github.com/go-gl/mathgl/mgl32"
)

// Emitter is a placed particle effect.
type Emitter struct {
	// ID is unique within a level; a script addresses an emitter by it (`@id`).
	ID string
	// ScriptData carries the tags, attributes and attached script, so `#tag`
	// reaches emitters on the same terms it reaches entities and volumes.
	ScriptData

	// Effect names a document under particles/, without the extension.
	Effect string

	// Position is world-space unless Attach names a carrier, in which case it
	// is that carrier's local space. Rotation is Euler XYZ in radians and turns
	// the emission shape — a cone fires wherever it points.
	Position mgl32.Vec3
	Rotation mgl32.Vec3

	// Enabled is the master switch. A disabled emitter does not simulate at
	// all, which is the difference between it and Emitting.
	Enabled bool
	// Emitting gates continuous release. Off, live particles finish their lives
	// and bursts still fire — which is what `stop_emit` leaves behind.
	Emitting bool
	// RateScale multiplies every layer's rate. 1 is the document's own.
	RateScale float32
	// Tint multiplies every layer's own tint, for varying one placement of a
	// shared effect without a second document.
	Tint mgl32.Vec3

	// Attach names the frame, entity or bone this emitter rides.
	Attach Attach

	// FromEntity is runtime-only and never serialized: the entity whose type
	// hung this emitter on it (entity_emitter.go). Empty for one the level file
	// placed.
	//
	// It plays the part Chunk.FromEntity plays for materialised geometry. Two
	// things read it: LevelToRaw skips these when saving, because they are the
	// entity's content and a level that wrote a copy would freeze it and stop
	// tracking the type; and DropEntityEmitters finds them again when the
	// entity is deleted, renamed or re-stamped.
	FromEntity string

	// OneShot marks an emitter that removes itself once its last particle has
	// died. Set only by the `spawn_effect` verb and never serialized: a level
	// file places lasting emitters, and a script firing one per impact would
	// otherwise fill the level with spent ones.
	OneShot bool

	// Doc is the loaded effect. Nil when the document could not be read, which
	// is a missing asset rather than a broken level — the emitter stays
	// selectable and says so in the panel.
	Doc *particle.Doc
	// Systems is one simulation per layer, parallel to Doc.Layers.
	Systems []*particle.System

	// Waypoints is the waypoint animation this emitter is travelling, or nil.
	// Runtime-only, never serialized; see waypoint_follow.go.
	Waypoints *WaypointPlayback
}

// NewEmitter returns an emitter with the defaults a freshly placed one should
// have: running, unscaled, untinted.
func NewEmitter(id string, pos mgl32.Vec3) Emitter {
	return Emitter{
		ID:        id,
		Position:  pos,
		Enabled:   true,
		Emitting:  true,
		RateScale: 1,
		Tint:      mgl32.Vec3{1, 1, 1},
	}
}

// EffectiveRateScale is the multiplier, never negative — a negative rate would
// emit nothing and read as a bug rather than as a setting.
func (e *Emitter) EffectiveRateScale() float32 {
	if e.RateScale < 0 {
		return 0
	}
	return e.RateScale
}

// EffectiveTint is the tint, with the zero value read as white. An emitter
// built field by field rather than through NewEmitter would otherwise multiply
// every layer by black and emit invisible particles.
func (e *Emitter) EffectiveTint() mgl32.Vec3 {
	if e.Tint == (mgl32.Vec3{}) {
		return mgl32.Vec3{1, 1, 1}
	}
	return e.Tint
}

// tintLayer folds the emitter's tint into a layer's own, which is how one
// placement of a shared effect is recoloured without a second document.
//
// Applied where the rate scale is, on the copy of the layer handed to the
// system, and for the same reason: the layer a system holds is its own, so
// re-deriving it from the document each time is what keeps two syncs from
// compounding.
func (e *Emitter) tintLayer(l particle.Layer) particle.Layer {
	t := e.EffectiveTint()
	l.Tint = mgl32.Vec3{l.Tint.X() * t.X(), l.Tint.Y() * t.Y(), l.Tint.Z() * t.Z()}
	return l
}

// Loaded reports whether the emitter has a document to simulate.
func (e *Emitter) Loaded() bool { return e.Doc != nil && len(e.Systems) > 0 }

// Load reads the effect document and builds one system per layer. Safe to call
// repeatedly — it rebuilds from scratch, so it doubles as "reload after the
// document changed on disk".
//
// A missing or malformed document is reported but not fatal: an emitter naming
// an effect that has been deleted should leave a marker to fix, not refuse the
// level.
func (e *Emitter) Load() error {
	e.Doc = nil
	e.Systems = nil
	if e.Effect == "" {
		return nil
	}
	doc, err := particle.LoadParticle(particle.ParticlePath(e.Effect))
	if err != nil {
		return err
	}
	e.SetDoc(doc)
	return nil
}

// SetDoc installs a document and rebuilds the systems from it. Separate from
// Load so the particle editor can push a live document into an emitter it is
// previewing without a round trip through the disk.
func (e *Emitter) SetDoc(doc *particle.Doc) {
	e.Doc = doc
	e.Systems = nil
	if doc == nil {
		return
	}
	e.Systems = make([]*particle.System, len(doc.Layers))
	for i := range doc.Layers {
		l := e.tintLayer(doc.Layers[i])
		l.Rate *= e.EffectiveRateScale()
		// Seeded per layer so two layers of one effect do not draw the same
		// sequence, and per emitter ID so two torches in a room do not flicker
		// in lockstep.
		e.Systems[i] = particle.NewSystem(l, doc.Gravity, doc.Seed+uint32(i)+idHash(e.ID))
		e.Systems[i].Emitting = e.Emitting
	}
}

// SyncSystems pushes the document's current layers back into the live systems,
// preserving what is already alive. The particle editor calls it every frame it
// has changed something, so an author sees a parameter take effect on the
// particles already in flight rather than only on the next ones.
func (e *Emitter) SyncSystems() {
	if e.Doc == nil {
		return
	}
	if len(e.Systems) != len(e.Doc.Layers) {
		e.SetDoc(e.Doc)
		return
	}
	scale := e.EffectiveRateScale()
	for i := range e.Doc.Layers {
		l := e.tintLayer(e.Doc.Layers[i])
		l.Rate *= scale
		e.Systems[i].SetLayer(l, e.Doc.Gravity)
		e.Systems[i].Emitting = e.Emitting
	}
}

// Reset clears every live particle and rewinds every cycle — a script's
// `restart`, and the editor's button.
func (e *Emitter) Reset() {
	for _, s := range e.Systems {
		s.Reset()
	}
}

// Burst fires n particles from every layer at once. With layer empty it reaches
// all of them; naming one targets it, which is how an effect with a flash and a
// smoke plume can have just the flash fired.
func (e *Emitter) Burst(n int, layer string, origin mgl32.Mat4) {
	for i, s := range e.Systems {
		if layer != "" && (e.Doc == nil || i >= len(e.Doc.Layers) || e.Doc.Layers[i].Name != layer) {
			continue
		}
		s.Burst(n, origin)
	}
}

// LiveCount is how many particles the emitter currently has in flight, across
// every layer — what the editor's status line and the debug overlay report.
func (e *Emitter) LiveCount() int {
	n := 0
	for _, s := range e.Systems {
		n += s.Count()
	}
	return n
}

// ModelMatrix builds the emitter's authored transform, before attachment.
func (e *Emitter) ModelMatrix() mgl32.Mat4 {
	T := mgl32.Translate3D(e.Position.X(), e.Position.Y(), e.Position.Z())
	return T.Mul4(EulerMatrixYXZ(e.Rotation))
}

// OriginMatrix is the transform particles are emitted through: the emitter's
// own, carried by whatever it rides.
func (e *Emitter) OriginMatrix(lvl *Level) mgl32.Mat4 {
	if e.Attach.IsZero() {
		return e.ModelMatrix()
	}
	return e.Attach.Matrix(lvl).Mul4(e.ModelMatrix())
}

// WorldPosition is where the emitter currently is, for culling and for the
// editor's marker.
func (e *Emitter) WorldPosition(lvl *Level) mgl32.Vec3 {
	m := e.OriginMatrix(lvl)
	return mgl32.Vec3{m[12], m[13], m[14]}
}

// WorldAABB returns bounds holding every live particle, widened to the emitter
// itself so an emitter with nothing alive yet is still culled sensibly rather
// than being culled away and never starting.
func (e *Emitter) WorldAABB(lvl *Level) (min, max mgl32.Vec3, ok bool) {
	p := e.WorldPosition(lvl)
	min, max, ok = p, p, true
	for _, s := range e.Systems {
		lo, hi, has := s.Bounds()
		if !has {
			continue
		}
		min = mgl32.Vec3{minF32(min.X(), lo.X()), minF32(min.Y(), lo.Y()), minF32(min.Z(), lo.Z())}
		max = mgl32.Vec3{maxF32(max.X(), hi.X()), maxF32(max.Y(), hi.Y()), maxF32(max.Z(), hi.Z())}
	}
	return min, max, ok
}

func minF32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxF32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// idHash is an FNV-1a of the emitter's ID, mixed into its seed so two
// placements of one effect do not run in lockstep.
func idHash(id string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(id); i++ {
		h ^= uint32(id[i])
		h *= 16777619
	}
	return h
}

// ---------- level lookup ----------

// FindEmitter returns the level emitter with the given ID, or nil.
func FindEmitter(lvl *Level, id string) *Emitter {
	if lvl == nil || id == "" {
		return nil
	}
	for i := range lvl.Emitters {
		if lvl.Emitters[i].ID == id {
			return &lvl.Emitters[i]
		}
	}
	return nil
}

// FindEmittersByTag returns every level emitter carrying tag, in level order.
func FindEmittersByTag(lvl *Level, tag string) []*Emitter {
	if lvl == nil || tag == "" {
		return nil
	}
	var out []*Emitter
	for i := range lvl.Emitters {
		if lvl.Emitters[i].HasTag(tag) {
			out = append(out, &lvl.Emitters[i])
		}
	}
	return out
}

// UniqueEmitterID returns base, or base with a numeric suffix that nothing in
// lvl is already using.
func UniqueEmitterID(lvl *Level, base string) string {
	if base == "" {
		base = "emitter"
	}
	return uniqueLevelID(lvl, base)
}

// ---------- simulation ----------

// EmitterUpdate advances every emitter in the level.
//
// Called once a frame from app.go, after RefreshFrames has republished the
// frame transforms an attached emitter reads and before the script tick, so a
// burst a script fires this frame lands at age 0 and draws this frame.
//
// motion is the play gate, matching EntityUpdate's: in the editor an emitter
// still simulates, because an effect that froze the moment you opened the
// editor could not be placed by eye. What it does not do is move anything else.
//
// It reports whether the scene's lights need uploading again this frame, on
// EffectUpdate's terms and for the same reason: an effect whose document sets
// a `light:` block casts a flash while it goes off (emitter_light.go), and a
// light is a uniform rather than something drawn where it stands.
func EmitterUpdate(lvl *Level, dt float32, motion bool) (lightsChanged bool) {
	if lvl == nil {
		return false
	}
	_ = motion
	if !stepEmittersParallel(lvl, dt) {
		for i := range lvl.Emitters {
			stepEmitter(lvl, &lvl.Emitters[i], dt)
		}
	}
	// Whether a one-shot has run its course is its own systems' business, so
	// it is asked after every emitter has stepped rather than as each does.
	reap := false
	for i := range lvl.Emitters {
		if e := &lvl.Emitters[i]; e.Enabled && e.Loaded() && e.OneShot && e.finished() {
			reap = true
			break
		}
	}
	if reap {
		reapOneShots(lvl)
	}
	// Lit this frame, or lit last frame and not this one — the frame after the
	// last flash dies has to upload once more to clear it.
	lit := AnyEmitterFlash(lvl)
	changed := lit || emitterFlashesWereLit
	emitterFlashesWereLit = lit
	return changed
}

// stepEmitter advances one emitter's systems by dt.
func stepEmitter(lvl *Level, e *Emitter, dt float32) {
	if !e.Enabled || !e.Loaded() {
		return
	}
	origin := e.OriginMatrix(lvl)
	for _, s := range e.Systems {
		s.Step(dt, origin)
	}
}

// ---------- stepping on every core ----------
//
// Every particle system carries its own seeded generator and touches nothing
// but its own pool, so the emitters of a level are independent of one another
// and can step at once: the result is the serial loop's to the bit. The two
// things they share are read-only while they step — the level (an attached
// emitter reads where its carrier is) and the collision seam, particle.Collide,
// which reads the level's geometry through caches that collision fills ahead
// of time when told a parallel step is coming (EmitterParallelBegin).

// ParallelEmitters switches the parallel step on; a test turns it off to
// compare against the serial loop.
var ParallelEmitters = true

// EmitterParallelBegin and EmitterParallelEnd bracket a parallel step. The
// collision package installs them (it sits above this one) so that the
// particle collision seam's lazily built caches are filled and frozen before
// several goroutines query them; nil, nothing needs bracketing.
var EmitterParallelBegin func(lvl *Level)
var EmitterParallelEnd func()

// parallelEmitterMin is how many live particles a frame needs before stepping
// emitters on several goroutines pays for starting them.
const parallelEmitterMin = 512

// stepEmittersParallel steps every emitter on a pool of workers, each taking
// the next unstepped emitter (one emitter's cost ranges from nothing to
// thousands of colliding particles, so a fixed split would leave workers
// idle). It reports false, having done nothing, when the frame is too small to
// be worth it.
func stepEmittersParallel(lvl *Level, dt float32) bool {
	if !ParallelEmitters || len(lvl.Emitters) < 2 || runtime.GOMAXPROCS(0) < 2 {
		return false
	}
	live := 0
	for i := range lvl.Emitters {
		if e := &lvl.Emitters[i]; e.Enabled && e.Loaded() {
			live += e.LiveCount()
		}
	}
	if live < parallelEmitterMin {
		return false
	}
	if EmitterParallelBegin != nil {
		EmitterParallelBegin(lvl)
	}
	if EmitterParallelEnd != nil {
		defer EmitterParallelEnd()
	}
	workers := min(runtime.GOMAXPROCS(0), len(lvl.Emitters))
	var next atomic.Int64
	var wg sync.WaitGroup
	var failure atomic.Value
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					failure.CompareAndSwap(nil, fmt.Sprint(r))
				}
			}()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(lvl.Emitters) {
					return
				}
				stepEmitter(lvl, &lvl.Emitters[i], dt)
			}
		}()
	}
	wg.Wait()
	if r := failure.Load(); r != nil {
		panic(fmt.Sprintf("world: parallel emitter step: %v", r))
	}
	return true
}

// emitterFlashesWereLit remembers whether last frame's upload carried an
// emitter flash. See effect.go's effectLightsWereLit, which it mirrors.
var emitterFlashesWereLit bool

// finished reports whether a one-shot has run its course: nothing alive, and
// nothing still to come.
//
// The second half matters. A one-shot whose first frame has not emitted yet has
// no live particles either, so reaping on "nothing alive" alone would delete
// every spawn_effect on the frame it was created.
func (e *Emitter) finished() bool {
	if e.LiveCount() > 0 {
		return false
	}
	for _, s := range e.Systems {
		if !s.Done() {
			return false
		}
	}
	return true
}

// reapOneShots removes the finished self-destructing emitters. Done in one pass
// after the walk rather than inside it, because deleting from a slice being
// ranged over is how an element gets skipped.
func reapOneShots(lvl *Level) {
	kept := lvl.Emitters[:0]
	for i := range lvl.Emitters {
		e := &lvl.Emitters[i]
		if e.OneShot && e.Loaded() && e.finished() {
			continue
		}
		kept = append(kept, *e)
	}
	lvl.Emitters = kept
}

// ---------- serialization ----------

// EmitterFromRaw converts the YAML form.
func EmitterFromRaw(r RawEmitter) (Emitter, error) {
	data, err := ScriptDataFromRaw(r.RawScriptData, "emitter "+r.ID)
	if err != nil {
		return Emitter{}, err
	}
	return Emitter{
		ID:         r.ID,
		ScriptData: data,
		Effect:     r.Effect,
		Position:   mgl32.Vec3{r.Position.X, r.Position.Y, r.Position.Z},
		Rotation:   mgl32.Vec3{r.Rotation.X, r.Rotation.Y, r.Rotation.Z},
		Enabled:    BoolDefault(r.Enabled, true),
		Emitting:   BoolDefault(r.Emitting, true),
		RateScale:  float32Default(r.RateScale, 1),
		Tint:       colorDefault(r.Tint),
		Attach:     AttachFromRaw(r.Attach),
	}, nil
}

// EmitterToRaw is the inverse.
func EmitterToRaw(e *Emitter) RawEmitter {
	return RawEmitter{
		ID:            e.ID,
		RawScriptData: ScriptDataToRaw(e.ScriptData),
		Effect:        e.Effect,
		Position:      RawVec3{X: e.Position.X(), Y: e.Position.Y(), Z: e.Position.Z()},
		Rotation:      RawVec3{X: e.Rotation.X(), Y: e.Rotation.Y(), Z: e.Rotation.Z()},
		Enabled:       BoolPtrIfFalse(e.Enabled),
		Emitting:      BoolPtrIfFalse(e.Emitting),
		RateScale:     float32PtrIfNot(e.RateScale, 1),
		Tint:          colorPtrIfNotWhite(e.Tint),
		Attach:        AttachToRaw(e.Attach),
	}
}
