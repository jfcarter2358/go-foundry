package collision

// entity_sleep.go — not re-solving a body that is lying still.
//
// Most of a level's bodies, most of the time, are standing: a guard at his
// post, a crate on a floor, a soldier crouched in cover. Every frame each of
// them still falls a frame's worth, is swept and stepped and pushed back out of
// the floor it is standing on — and lands exactly where it stood. That resolve
// is most of what the entity update costs.
//
// So the motion step is memoised. stepEntityMotion is a pure function of the
// body's own motion state and of the world around it; when a body arrives at a
// frame in exactly the state it arrived in last frame (bit for bit), and
// nothing about the world it could have touched has changed, the step would
// produce exactly what it produced last frame — so that is copied back instead
// of being worked out again. There is no "asleep" state to forget to clear and
// no list of wake-up sources to keep complete: anything that would wake a body
// — a script or a controller writing its velocity or position, a push, an
// impulse, a change of gravity, its shape or its collision flags — changes one
// of its inputs, and a changed input is a miss.
//
// # What counts as an input
//
// The body's own (motionKey): everything stepEntityMotion reads of the entity
// — placement, velocity, spin, scale, bounds, collision flags and footing, the
// controller's step height — plus the gravity it is under (asked afresh every
// frame, so a gravity volume that moved or changed is seen), the frame length
// and the player step height the level falls back on.
//
// The world's (worldMotionVersion), once per pass: which chunks there are, where
// each world-space chunk is placed and how big, whether it is switched on, its
// brushes (the mesh generation, and the placement of every brush that can move),
// its terrain's generation, the spatial hash and the frame set, the scenery
// index, and the engine-wide collision switch. Anything that moves or edits
// world geometry changes it, and every body is re-solved that frame.
//
// The entity solids (entity_brushes.go) are the one part of the world that
// moves all the time — a ship flying is a moving solid — so they are not part
// of the version. Instead a body is only ever memoised while every entity solid
// is out of reach of it, padded by far more than the frame's motion and a
// step-up could carry it (geometriesClear), and that is checked against where
// the solids are this frame on every hit.
//
// # What is left out
//
// Only the simple case is memoised, which is also the common one: a body in the
// world frame, standing (on the ground at the start of the step), unseated,
// with no tween or waypoint path driving it and no step being eased in its
// drawing, whose step left it with no step offset either — so the step played
// no step clip and posted no event. Anything else is solved every frame, as it
// always was.
//
// SleepRestingBodies switches it off, which is what the tests compare against.

import (
	"hash/fnv"
	"math"
	"sync/atomic"
	"unsafe"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// SleepRestingBodies switches the motion memo on.
var SleepRestingBodies = true

// motionKey is everything a body's motion step reads of the body, and the
// world version it was taken against.
type motionKey struct {
	id                       string
	world                    uint64
	dt, gravity, playerStep  float32
	pos, vel, rot, spin, scl mgl32.Vec3
	stepOffset               float32
	stepAnchor               mgl32.Vec3
	coll                     world.EntityCollision
	boundKind                world.BoundingType
	halfX, halfY, halfZ      float32
	boundOffset              mgl32.Vec3
	ai                       *world.AISpec
	aiEnabled                bool
	aiStep                   float32
}

// motionOut is everything the step writes when it runs in the memoised case.
type motionOut struct {
	pos, vel, rot mgl32.Vec3
	coll          world.EntityCollision
	chunk         string
}

type motionMemo struct {
	ok  bool
	key motionKey
	out motionOut
}

// motionMemos is index-parallel to the level's entities. In the parallel pass
// each slot is touched only by the worker stepping that entity.
var (
	motionMemos    []motionMemo
	motionMemoLvl  *world.Level
	motionMemoHits atomic.Uint64
)

// MotionMemoHits is how many body steps the memo has answered since the
// process started — for tests and the profiler, to see that it is taking.
func MotionMemoHits() uint64 { return motionMemoHits.Load() }

// sleepPass is what one pass of EntityUpdate knows about the world for the memo.
type sleepPass struct {
	on      bool
	version uint64
}

// beginSleepPass sizes the memo for the level and takes the world's version.
// Before the workers start.
func beginSleepPass(lvl *world.Level, motion bool) sleepPass {
	if !SleepRestingBodies || !motion || lvl == nil {
		return sleepPass{}
	}
	if motionMemoLvl != lvl {
		motionMemoLvl = lvl
		motionMemos = motionMemos[:0]
	}
	if n := len(lvl.Entities); len(motionMemos) < n {
		motionMemos = append(motionMemos, make([]motionMemo, n-len(motionMemos))...)
	}
	return sleepPass{on: true, version: worldMotionVersion(lvl)}
}

// stepEntityMotionMemo is stepEntityMotion through the memo.
func stepEntityMotionMemo(lvl *world.Level, e *world.Entity, i int, dt float32, sp sleepPass) {
	if !sp.on || i >= len(motionMemos) {
		stepEntityMotion(lvl, e, dt)
		return
	}
	m := &motionMemos[i]
	key, eligible := motionKeyOf(lvl, e, dt, sp.version)
	if eligible && !geometriesClear(lvl, e, dt) {
		eligible = false
	}
	if eligible && m.ok && m.key == key {
		e.Position, e.Velocity, e.Rotation = m.out.pos, m.out.vel, m.out.rot
		e.Collision = m.out.coll
		e.CurrentChunk = m.out.chunk
		motionMemoHits.Add(1)
		return
	}
	stepEntityMotion(lvl, e, dt)
	m.ok = false
	if eligible && e.StepOffset == 0 {
		m.ok, m.key = true, key
		m.out = motionOut{pos: e.Position, vel: e.Velocity, rot: e.Rotation, coll: e.Collision, chunk: e.CurrentChunk}
	}
}

// motionKeyOf is the body's inputs, and whether it is a body the memo takes.
func motionKeyOf(lvl *world.Level, e *world.Entity, dt float32, version uint64) (motionKey, bool) {
	if e.Frame != "" || e.Seat != "" || e.StepOffset != 0 || !e.Collision.OnGround ||
		e.Waypoints != nil || e.Tweens != (world.Tweens{}) || e.VState != nil || !e.CollidesWithWorld() {
		return motionKey{}, false
	}
	k := motionKey{
		id:          e.ID,
		world:       version,
		dt:          dt,
		gravity:     world.GravityFor(lvl, e),
		playerStep:  lvl.Player.EffectiveStepHeight(),
		pos:         e.Position,
		vel:         e.Velocity,
		rot:         e.Rotation,
		spin:        e.AngularVelocity,
		scl:         e.Scale,
		stepOffset:  e.StepOffset,
		stepAnchor:  e.StepAnchor,
		coll:        e.Collision,
		boundKind:   e.BoundingKind,
		halfX:       e.BoundHalfX,
		halfY:       e.BoundHalfY,
		halfZ:       e.BoundHalfZ,
		boundOffset: e.BoundOffset,
		ai:          e.AI,
	}
	if e.AI != nil {
		k.aiEnabled, k.aiStep = e.AI.Enabled, e.AI.StepHeight
	}
	return k, true
}

// geometriesClear reports whether every entity solid is far out of reach of
// the body this frame: far enough that no sweep step, step-up probe or push
// of this frame's resolve can have come near it.
func geometriesClear(lvl *world.Level, e *world.Entity, dt float32) bool {
	geos := entityGeometries(lvl)
	if len(geos) == 0 {
		return true
	}
	wf := lvl.Frames.FrameFor(e)
	if wf == nil {
		return false
	}
	h := e.BoundHalfExtents()
	r := max(h.X(), h.Z())
	// The body's own reach plus everything this frame could move it through:
	// its velocity over the frame, a step-up's lift, and a generous metre.
	reach := e.Velocity.Len()*dt + 1 + lvl.Player.EffectiveStepHeight()
	if e.AI != nil {
		reach += e.AI.StepHeight
	}
	half := mgl32.Vec3{r + reach, h.Y() + reach, r + reach}
	c := wf.ToWorld(e.BoundCenter())
	for gi := range geos {
		g := &geos[gi]
		if !g.reachOK {
			return false
		}
		if !g.reach.valid() {
			continue
		}
		// The body's box in the solid's chunk space is the world box carried
		// through the frame (rigid) and the chunk's placement inverse, which
		// can turn and scale it: a sphere of the box's half-diagonal, grown by
		// a bound on the inverse's stretch, contains it whatever they do.
		rad := half.Len() * g.invStretch
		local := g.local(g.Frame.FromWorld(c))
		if g.reach.touches(local, mgl32.Vec3{rad, rad, rad}) {
			return false
		}
	}
	return true
}

// worldMotionVersion fingerprints everything about the world, other than the
// entity solids, that a world-space body's motion step reads. See the file
// comment.
func worldMotionVersion(lvl *world.Level) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	u64 := func(v uint64) {
		for i := range buf {
			buf[i] = byte(v >> (8 * i))
		}
		h.Write(buf[:])
	}
	f32 := func(v float32) { u64(uint64(math.Float32bits(v))) }
	ptr := func(p unsafe.Pointer) { u64(uint64(uintptr(p))) }
	b := func(v bool) {
		if v {
			u64(1)
		} else {
			u64(0)
		}
	}
	u64(world.BrushMeshGeneration())
	b(world.EntityWorldCollisionEnabled)
	ptr(unsafe.Pointer(lvl.Spatial))
	ptr(unsafe.Pointer(lvl.Frames))
	ptr(unsafe.Pointer(sceneryIndexFor(lvl)))
	u64(uint64(len(lvl.Chunks)))
	if len(lvl.Chunks) > 0 {
		ptr(unsafe.Pointer(&lvl.Chunks[0]))
	}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if ch.InFrame() {
			// A frame's own chunks are the entity solids' and the riders'
			// business, neither of which the memo takes.
			continue
		}
		b(ch.IsEnabled())
		for _, v := range ch.ModelMatrix() {
			f32(v)
		}
		for _, v := range [6]float32{ch.MinBound[0], ch.MinBound[1], ch.MinBound[2], ch.MaxBound[0], ch.MaxBound[1], ch.MaxBound[2]} {
			f32(v)
		}
		u64(uint64(len(ch.Brushes)))
		if len(ch.Brushes) > 0 {
			ptr(unsafe.Pointer(&ch.Brushes[0]))
		}
		ptr(unsafe.Pointer(ch.Terrain))
		if ch.Terrain != nil {
			u64(ch.Terrain.Generation)
		}
		if pc := brushBoxesFor(ch); pc != nil {
			for _, bi := range pc.loose {
				for _, v := range ch.Brushes[bi].Placement() {
					f32(v)
				}
			}
		}
	}
	return h.Sum64()
}
