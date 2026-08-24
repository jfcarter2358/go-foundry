package main

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// solidEntity is a 1×1×1 box at pos, opted into everything and movable — the
// default subject for the tests below.
func solidEntity(id string, pos mgl32.Vec3) Entity {
	return Entity{
		ID:         id,
		Position:   pos,
		Scale:      mgl32.Vec3{1, 1, 1},
		BoundHalfX: 0.5, BoundHalfY: 0.5, BoundHalfZ: 0.5,
		Visible:   true,
		Collision: EntityCollision{World: true, Entities: true, Gravity: true, Movable: true},
	}
}

// withCollisionMasters forces both engine-wide switches on for the duration of
// a test and puts them back afterwards, so a test that flips one cannot leak
// into the next.
func withCollisionMasters(t *testing.T, world, entities bool) {
	t.Helper()
	pw, pe := EntityWorldCollisionEnabled, EntityEntityCollisionEnabled
	EntityWorldCollisionEnabled, EntityEntityCollisionEnabled = world, entities
	t.Cleanup(func() { EntityWorldCollisionEnabled, EntityEntityCollisionEnabled = pw, pe })
}

// ---------- defaults and gating ----------

func TestEntityCollisionDefaultsToInertButMovable(t *testing.T) {
	// An engine update must not silently start shoving a level's props around,
	// so everything physical is opt-in. Movable is the exception: it defaults
	// on so turning a single flag on behaves the obvious way.
	c := DefaultEntityCollision()
	if c.World || c.Entities || c.Gravity {
		t.Errorf("collision defaulted to on: %+v", c)
	}
	if !c.Movable {
		t.Error("movable defaulted to off")
	}
}

func TestCollisionMastersGateThePerEntityFlags(t *testing.T) {
	e := solidEntity("a", mgl32.Vec3{})

	withCollisionMasters(t, true, true)
	if !e.collidesWithWorld() || !e.collidesWithEntities() {
		t.Error("both on: entity should collide")
	}

	EntityWorldCollisionEnabled = false
	if e.collidesWithWorld() {
		t.Error("world master off but entity still collides with world")
	}
	if !e.collidesWithEntities() {
		t.Error("the world master should not gate entity collision")
	}

	EntityWorldCollisionEnabled, EntityEntityCollisionEnabled = true, false
	if e.collidesWithEntities() {
		t.Error("entity master off but entity still collides with entities")
	}

	// An entity with no volume has nothing to collide with, whatever it asks
	// for — and resolving against a zero-sized box would be a divide by nothing.
	EntityEntityCollisionEnabled = true
	e.BoundHalfY = 0
	if e.collidesWithEntities() {
		t.Error("an entity with no bounds reported entity collision")
	}
}

// ---------- gravity ----------

func TestApplyGravityAcceleratesAndCaps(t *testing.T) {
	e := solidEntity("a", mgl32.Vec3{})

	e.applyGravity(0.5)
	if want := -EntityGravityAccel * 0.5; abs32(e.Velocity.Y()-want) > 1e-4 {
		t.Errorf("got %v, want %v", e.Velocity.Y(), want)
	}

	// Terminal velocity: a long fall must not build up enough speed to tunnel
	// through a floor in one step.
	for i := 0; i < 100; i++ {
		e.applyGravity(0.1)
	}
	if e.Velocity.Y() < -EntityMaxFallSpeed-1e-4 {
		t.Errorf("fall speed %v exceeded the cap %v", e.Velocity.Y(), EntityMaxFallSpeed)
	}
}

func TestApplyGravityRespectsItsFlags(t *testing.T) {
	e := solidEntity("a", mgl32.Vec3{})
	e.Collision.Gravity = false
	e.applyGravity(1)
	if e.Velocity.Y() != 0 {
		t.Errorf("gravity off but velocity changed: %v", e.Velocity.Y())
	}

	// Nothing can displace an immovable entity, gravity included.
	e.Collision.Gravity = true
	e.Collision.Movable = false
	e.applyGravity(1)
	if e.Velocity.Y() != 0 {
		t.Errorf("immovable entity fell: %v", e.Velocity.Y())
	}
}

// ---------- the collision box ----------

func TestCollisionAABBIsTighterThanTheCullingBox(t *testing.T) {
	// Culling has to be conservative and blows a rotated box up to a uniform
	// radius; a resolver wants the exact span, or it would push things apart
	// across a visible gap.
	e := solidEntity("a", mgl32.Vec3{})
	e.Rotation = mgl32.Vec3{0, math.Pi / 4, 0}

	cmin, cmax, ok := e.CollisionAABB()
	if !ok {
		t.Fatal("no collision bounds")
	}
	wmin, wmax, ok := e.WorldAABB()
	if !ok {
		t.Fatal("no culling bounds")
	}

	// A 45° yaw spreads a unit box to half-diagonal √2/2 ≈ 0.707 in X and Z,
	// and leaves Y alone.
	if abs32(cmax.X()-0.7071) > 1e-3 || abs32(cmax.Z()-0.7071) > 1e-3 {
		t.Errorf("XZ span: got %v", cmax)
	}
	if abs32(cmax.Y()-0.5) > 1e-4 {
		t.Errorf("Y should be untouched by a yaw: got %v", cmax.Y())
	}
	// The culling box must still contain the collision box.
	if cmin.Y() < wmin.Y()-1e-4 || cmax.Y() > wmax.Y()+1e-4 {
		t.Error("the collision box escaped the culling box")
	}
	if wmax.Y() <= cmax.Y() {
		t.Error("culling box was not more conservative on Y")
	}
}

func TestCollisionAABBUnrotatedMatchesTheExtents(t *testing.T) {
	e := solidEntity("a", mgl32.Vec3{2, 3, 4})
	e.BoundOffset = mgl32.Vec3{0, 0.5, 0}
	min, max, ok := e.CollisionAABB()
	if !ok {
		t.Fatal("no bounds")
	}
	// Centre is position + offset; the box is the extents about it.
	if !vecNear(min, mgl32.Vec3{1.5, 3, 3.5}, 1e-5) || !vecNear(max, mgl32.Vec3{2.5, 4, 4.5}, 1e-5) {
		t.Errorf("got %v..%v", min, max)
	}
}

// ---------- entity against entity ----------

func TestResolveOverlapSeparatedPairIsUntouched(t *testing.T) {
	a := solidEntity("a", mgl32.Vec3{0, 0, 0})
	b := solidEntity("b", mgl32.Vec3{5, 0, 0})
	if ma, mb := resolveEntityOverlap(&a, &b); ma || mb {
		t.Errorf("a separated pair was moved: %v %v", ma, mb)
	}
	if a.Position != (mgl32.Vec3{0, 0, 0}) || b.Position != (mgl32.Vec3{5, 0, 0}) {
		t.Errorf("positions changed: %v %v", a.Position, b.Position)
	}
}

func TestResolveOverlapSplitsBetweenTwoMovableEntities(t *testing.T) {
	// Boxes 1 unit wide overlapping by 0.4 on X: each backs off 0.2.
	a := solidEntity("a", mgl32.Vec3{0, 0, 0})
	b := solidEntity("b", mgl32.Vec3{0.6, 0, 0})

	ma, mb := resolveEntityOverlap(&a, &b)
	if !ma || !mb {
		t.Fatalf("both should have moved: %v %v", ma, mb)
	}
	if abs32(a.Position.X()-(-0.2)) > 1e-4 {
		t.Errorf("a.x = %v, want -0.2", a.Position.X())
	}
	if abs32(b.Position.X()-0.8) > 1e-4 {
		t.Errorf("b.x = %v, want 0.8", b.Position.X())
	}
	// Exactly touching afterwards, not still overlapping.
	if _, _, again := overlapOf(&a, &b); again {
		t.Error("still overlapping after resolution")
	}
}

// overlapOf reports the per-axis overlap of two entities and whether they
// intersect at all — the test's own reading of the boxes, kept separate from
// the resolver so it cannot agree with a bug in it.
func overlapOf(a, b *Entity) (mgl32.Vec3, int, bool) {
	amin, amax, _ := a.CollisionAABB()
	bmin, bmax, _ := b.CollisionAABB()
	var ov mgl32.Vec3
	axis := 0
	for k := 0; k < 3; k++ {
		lo := float32(math.Max(float64(amin[k]), float64(bmin[k])))
		hi := float32(math.Min(float64(amax[k]), float64(bmax[k])))
		ov[k] = hi - lo
		if ov[k] <= 1e-5 {
			return ov, 0, false
		}
		if ov[k] < ov[axis] {
			axis = k
		}
	}
	return ov, axis, true
}

func TestResolveOverlapImmovableEntityDoesNotMove(t *testing.T) {
	a := solidEntity("crate", mgl32.Vec3{0.6, 0, 0})
	b := solidEntity("boulder", mgl32.Vec3{0, 0, 0})
	b.Collision.Movable = false

	ma, mb := resolveEntityOverlap(&a, &b)
	if !ma || mb {
		t.Fatalf("only the crate should move: crate=%v boulder=%v", ma, mb)
	}
	if b.Position != (mgl32.Vec3{0, 0, 0}) {
		t.Errorf("boulder moved: %v", b.Position)
	}
	// The crate takes the whole separation rather than half of it.
	if abs32(a.Position.X()-1.0) > 1e-4 {
		t.Errorf("crate.x = %v, want 1.0", a.Position.X())
	}
}

func TestResolveOverlapTwoImmovableEntitiesAreLeftAlone(t *testing.T) {
	a := solidEntity("a", mgl32.Vec3{0, 0, 0})
	b := solidEntity("b", mgl32.Vec3{0.5, 0, 0})
	a.Collision.Movable, b.Collision.Movable = false, false

	if ma, mb := resolveEntityOverlap(&a, &b); ma || mb {
		t.Errorf("an immovable pair was moved: %v %v", ma, mb)
	}
}

func TestResolveOverlapPushesAlongTheShallowestAxis(t *testing.T) {
	// Deeply overlapped on X and Z, barely on Y: something dropped on top of
	// something else has to settle on it rather than squirt out sideways.
	a := solidEntity("dropped", mgl32.Vec3{0, 0.9, 0})
	b := solidEntity("floorbox", mgl32.Vec3{0, 0, 0})
	b.Collision.Movable = false

	resolveEntityOverlap(&a, &b)

	if abs32(a.Position.X()) > 1e-4 || abs32(a.Position.Z()) > 1e-4 {
		t.Errorf("pushed sideways: %v", a.Position)
	}
	if abs32(a.Position.Y()-1.0) > 1e-4 {
		t.Errorf("y = %v, want to rest at 1.0", a.Position.Y())
	}
}

func TestResolveOverlapCancelsOnlyTheClosingVelocity(t *testing.T) {
	// Landing on something kills the downward speed, or gravity would keep
	// accelerating into it and the entity would jitter.
	a := solidEntity("dropped", mgl32.Vec3{0, 0.9, 0})
	a.Velocity = mgl32.Vec3{3, -8, 0}
	b := solidEntity("floorbox", mgl32.Vec3{0, 0, 0})
	b.Collision.Movable = false

	resolveEntityOverlap(&a, &b)

	if a.Velocity.Y() != 0 {
		t.Errorf("downward velocity survived the landing: %v", a.Velocity.Y())
	}
	// Movement along the other axes is not the engine's business.
	if a.Velocity.X() != 3 {
		t.Errorf("horizontal velocity was cancelled too: %v", a.Velocity.X())
	}
	// Staged, not published: the pair pass must not be able to see its own
	// work, or a stack would resolve differently depending on list order.
	// EntityUpdate commits it once the frame's resolution is finished.
	if !a.Collision.groundedNext {
		t.Error("being pushed up onto something should count as standing on it")
	}
	if a.Collision.OnGround {
		t.Error("the ground flag was published mid-pass")
	}
	a.commitGrounded()
	if !a.Collision.OnGround {
		t.Error("commit did not publish the staged ground flag")
	}

	// An entity already separating must not be stopped, or a stack would stick
	// together instead of settling.
	c := solidEntity("rising", mgl32.Vec3{0, 0.9, 0})
	c.Velocity = mgl32.Vec3{0, 5, 0}
	resolveEntityOverlap(&c, &b)
	if c.Velocity.Y() != 5 {
		t.Errorf("upward velocity was cancelled: %v", c.Velocity.Y())
	}
}

// ---------- the level-wide pass ----------

func TestResolveEntityCollisionsOnlyTouchesOptedInEntities(t *testing.T) {
	withCollisionMasters(t, true, true)

	lvl := &Level{Entities: []Entity{
		solidEntity("a", mgl32.Vec3{0, 0, 0}),
		solidEntity("b", mgl32.Vec3{0.6, 0, 0}),
		solidEntity("ghost", mgl32.Vec3{0.3, 0, 0}),
	}}
	// The ghost sits right between the other two but has opted out, so nothing
	// should notice it.
	lvl.Entities[2].Collision.Entities = false
	ghostPos := lvl.Entities[2].Position

	moved := ResolveEntityCollisions(lvl, nil)

	if len(moved) != 2 || moved[0] != 0 || moved[1] != 1 {
		t.Errorf("moved = %v, want the two opted-in entities", moved)
	}
	if lvl.Entities[2].Position != ghostPos {
		t.Errorf("the opted-out entity moved: %v", lvl.Entities[2].Position)
	}
}

func TestResolveEntityCollisionsRespectsTheMaster(t *testing.T) {
	withCollisionMasters(t, true, false)

	lvl := &Level{Entities: []Entity{
		solidEntity("a", mgl32.Vec3{0, 0, 0}),
		solidEntity("b", mgl32.Vec3{0.6, 0, 0}),
	}}
	if moved := ResolveEntityCollisions(lvl, nil); len(moved) != 0 {
		t.Errorf("master switch off but %v moved", moved)
	}
	if lvl.Entities[0].Position != (mgl32.Vec3{0, 0, 0}) {
		t.Errorf("entity moved with the master off: %v", lvl.Entities[0].Position)
	}
}

func TestResolveEntityCollisionsIncludesThePlayer(t *testing.T) {
	// A solid crate should be solid to walk into.
	withCollisionMasters(t, true, true)

	lvl := &Level{Entities: []Entity{solidEntity("crate", mgl32.Vec3{0, 0, 0})}}
	lvl.Entities[0].Collision.Movable = false

	player := NewPlayer(mgl32.Vec3{0.2, 0, 0})
	moved := ResolveEntityCollisions(lvl, player)

	if len(moved) != 0 {
		t.Errorf("the immovable crate was moved: %v", moved)
	}
	if player.Position.X() == 0.2 {
		t.Error("the player was not pushed out of the crate")
	}
	if lvl.Entities[0].Position != (mgl32.Vec3{0, 0, 0}) {
		t.Errorf("crate moved: %v", lvl.Entities[0].Position)
	}
}

func TestResolveEntityCollisionsIsOrderIndependent(t *testing.T) {
	// The pass runs after everything has integrated, so listing the same two
	// entities the other way round must give the same separation.
	withCollisionMasters(t, true, true)

	fwd := &Level{Entities: []Entity{
		solidEntity("a", mgl32.Vec3{0, 0, 0}),
		solidEntity("b", mgl32.Vec3{0.6, 0, 0}),
	}}
	rev := &Level{Entities: []Entity{
		solidEntity("b", mgl32.Vec3{0.6, 0, 0}),
		solidEntity("a", mgl32.Vec3{0, 0, 0}),
	}}
	ResolveEntityCollisions(fwd, nil)
	ResolveEntityCollisions(rev, nil)

	if !vecNear(fwd.Entities[0].Position, rev.Entities[1].Position, 1e-5) ||
		!vecNear(fwd.Entities[1].Position, rev.Entities[0].Position, 1e-5) {
		t.Errorf("order changed the result: %v/%v vs %v/%v",
			fwd.Entities[0].Position, fwd.Entities[1].Position,
			rev.Entities[1].Position, rev.Entities[0].Position)
	}
}

// ---------- world collision ----------

// collisionTestLevel is splitTestChunk's two 10×10 sectors with their floors at
// y=0 and their ceilings at y=8 — enough for the floor/ceiling clamp and the
// wall push.
//
// The 2D sector bounds are computed here rather than through Sector.Load, which
// also uploads geometry and so needs a GL context. Without them every sector
// AABB is degenerate and the chunk lookup rejects the whole level.
func collisionTestLevel() *Level {
	ch := *splitTestChunk()
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		s.Floor.Enabled = true
		s.Floor.Height = 0
		s.Ceiling.Enabled = true
		s.Ceiling.Height = 8

		pts := SectorPoints(&ch, s)
		s.MinBound, s.MaxBound = pts[0], pts[0]
		for _, p := range pts[1:] {
			s.MinBound = mgl32.Vec2{
				float32(math.Min(float64(s.MinBound.X()), float64(p.X()))),
				float32(math.Min(float64(s.MinBound.Y()), float64(p.Y()))),
			}
			s.MaxBound = mgl32.Vec2{
				float32(math.Max(float64(s.MaxBound.X()), float64(p.X()))),
				float32(math.Max(float64(s.MaxBound.Y()), float64(p.Y()))),
			}
		}
	}
	ch.RecomputeBounds()
	return &Level{Chunks: []Chunk{ch}}
}

func TestResolveEntityWorldRestsOnTheFloor(t *testing.T) {
	lvl := collisionTestLevel()
	// The resolver clamps where the entity already is; it does not integrate.
	// So the subject is one that has just been carried *below* the floor by a
	// step of movement, which is what the clamp exists to undo.
	//
	// Position is the entity's base and the bound offset lifts the volume's
	// centre to the middle of the box — the convention the shipped definitions
	// use. Landing has to put the base on the floor, not the centre.
	e := solidEntity("crate", mgl32.Vec3{5, -0.4, 5})
	e.BoundOffset = mgl32.Vec3{0, 0.5, 0}
	e.Velocity = mgl32.Vec3{0, -4, 0}

	if !resolveEntityWorld(lvl, &e, mgl32.Vec3{5, 0.2, 5}) {
		t.Fatal("expected to land on the floor")
	}
	if abs32(e.Position.Y()) > 1e-4 {
		t.Errorf("base ended at y=%v, want 0", e.Position.Y())
	}
	if e.Velocity.Y() != 0 {
		t.Errorf("landing did not stop the fall: %v", e.Velocity.Y())
	}
}

func TestResolveEntityWorldLeavesSomethingInMidAirAlone(t *testing.T) {
	// The clamp must not snap a hovering entity down: falling is gravity's job,
	// and an entity with gravity off is entitled to stay up there.
	lvl := collisionTestLevel()
	e := solidEntity("hovering", mgl32.Vec3{5, 3, 5})
	before := e.Position

	if resolveEntityWorld(lvl, &e, before) {
		t.Error("reported standing on ground while in mid-air")
	}
	if e.Position != before {
		t.Errorf("moved: %v", e.Position)
	}
}

func TestResolveEntityWorldClampsUnderTheCeiling(t *testing.T) {
	lvl := collisionTestLevel()
	// Ceiling is at 8 and the box is 1 tall, so a centre at 7.7 puts its top
	// 0.2 through it. The centre has to stay inside the chunk's own Y range or
	// the sector lookup would reject it before the clamp got a look.
	e := solidEntity("balloon", mgl32.Vec3{5, 7.7, 5})
	e.Collision.Gravity = false
	e.Velocity = mgl32.Vec3{0, 4, 0}

	resolveEntityWorld(lvl, &e, mgl32.Vec3{5, 7.4, 5})

	// The volume's top ends at the ceiling, symmetrically about its centre —
	// unlike the player's clamp, which is deliberately loose.
	if abs32((e.Position.Y()+0.5)-8) > 1e-4 {
		t.Errorf("top ended at y=%v, want the ceiling at 8", e.Position.Y()+0.5)
	}
	if e.Velocity.Y() != 0 {
		t.Errorf("head-bonk did not stop the rise: %v", e.Velocity.Y())
	}
}

func TestResolveEntityWorldOutsideEverySectorIsLeftAlone(t *testing.T) {
	// An entity over open space is a legitimate thing to be; it must not be
	// yanked to the nearest floor.
	lvl := collisionTestLevel()
	e := solidEntity("adrift", mgl32.Vec3{500, 50, 500})
	before := e.Position

	if resolveEntityWorld(lvl, &e, before) {
		t.Error("reported standing on ground outside every sector")
	}
	if e.Position != before {
		t.Errorf("moved: %v", e.Position)
	}
}

func TestResolveEntityWorldPushesOutOfAWall(t *testing.T) {
	lvl := collisionTestLevel()
	// x=0 is sector A's west wall; a box centred on it must end up inside.
	e := solidEntity("crate", mgl32.Vec3{0, 1, 5})
	e.Collision.Gravity = false

	resolveEntityWorld(lvl, &e, mgl32.Vec3{2, 1, 5})

	if e.Position.X() < 0 {
		t.Errorf("left outside the wall: x=%v", e.Position.X())
	}
}

// ---------- the update loop ----------

func TestEntityUpdateAppliesGravityOnlyWithMotion(t *testing.T) {
	withCollisionMasters(t, true, true)
	lvl := collisionTestLevel()
	lvl.Entities = []Entity{solidEntity("crate", mgl32.Vec3{5, 5, 5})}

	// Editor open: nothing physical happens, so an entity cannot drift off the
	// spot it was just placed on.
	EntityUpdate(lvl, 0.1, false, nil)
	if lvl.Entities[0].Position != (mgl32.Vec3{5, 5, 5}) {
		t.Errorf("moved with motion off: %v", lvl.Entities[0].Position)
	}
	if lvl.Entities[0].Velocity != (mgl32.Vec3{}) {
		t.Errorf("gravity ran with motion off: %v", lvl.Entities[0].Velocity)
	}

	EntityUpdate(lvl, 0.1, true, nil)
	if lvl.Entities[0].Position.Y() >= 5 {
		t.Errorf("did not fall: %v", lvl.Entities[0].Position.Y())
	}
}

func TestEntityUpdateSettlesAFallingEntityOnTheFloor(t *testing.T) {
	withCollisionMasters(t, true, true)
	lvl := collisionTestLevel()
	e := solidEntity("crate", mgl32.Vec3{5, 4, 5})
	e.BoundOffset = mgl32.Vec3{0, 0.5, 0}
	lvl.Entities = []Entity{e}

	for i := 0; i < 240; i++ {
		EntityUpdate(lvl, 1.0/60, true, nil)
	}

	got := &lvl.Entities[0]
	if abs32(got.Position.Y()) > 1e-3 {
		t.Errorf("came to rest at y=%v, want the floor at 0", got.Position.Y())
	}
	if !got.Collision.OnGround {
		t.Error("resting on the floor but not reported as on the ground")
	}
	if got.Velocity.Y() != 0 {
		t.Errorf("still accelerating into the floor: %v", got.Velocity.Y())
	}
}

// A three-box stack, built in both list orders. This is the regression test for
// the bottom-up walk: resolved top-down without it, the topmost box ended every
// frame one gravity-step short of where it belonged and the stack visibly
// sagged. Both orders must settle on exactly 0 / 1 / 2.
func TestEntityUpdateStackSettlesWhateverTheListOrder(t *testing.T) {
	withCollisionMasters(t, true, true)

	for _, topDown := range []bool{false, true} {
		lvl := collisionTestLevel()
		heights := []float32{2, 3.5, 5}
		if topDown {
			heights = []float32{5, 3.5, 2}
		}
		for i, y := range heights {
			e := solidEntity(string(rune('a'+i)), mgl32.Vec3{5, y, 5})
			e.BoundOffset = mgl32.Vec3{0, 0.5, 0}
			lvl.Entities = append(lvl.Entities, e)
		}

		for f := 0; f < 600; f++ {
			EntityUpdate(lvl, 1.0/60, true, nil)
		}

		// Read the stack off by height rather than by index, since the two
		// orders put the same box in different slots.
		got := []float32{}
		for i := range lvl.Entities {
			got = append(got, lvl.Entities[i].Position.Y())
		}
		sortFloats(got)
		want := []float32{0, 1, 2}
		for i := range want {
			if abs32(got[i]-want[i]) > 1e-4 {
				t.Errorf("topDown=%v: settled at %v, want %v", topDown, got, want)
				break
			}
		}
		for i := range lvl.Entities {
			if !lvl.Entities[i].Collision.OnGround {
				t.Errorf("topDown=%v: box %d not reported as on the ground", topDown, i)
			}
		}
	}
}

func sortFloats(v []float32) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

func TestEntityUpdateStacksTwoEntities(t *testing.T) {
	// The one test that exercises the whole order: everything falls, then the
	// set is separated, then what was displaced is settled back on the world.
	withCollisionMasters(t, true, true)
	lvl := collisionTestLevel()

	bottom := solidEntity("bottom", mgl32.Vec3{5, 0, 5})
	bottom.BoundOffset = mgl32.Vec3{0, 0.5, 0}
	top := solidEntity("top", mgl32.Vec3{5, 3, 5})
	top.BoundOffset = mgl32.Vec3{0, 0.5, 0}
	lvl.Entities = []Entity{bottom, top}

	for i := 0; i < 300; i++ {
		EntityUpdate(lvl, 1.0/60, true, nil)
	}

	b, tp := &lvl.Entities[0], &lvl.Entities[1]
	if abs32(b.Position.Y()) > 1e-2 {
		t.Errorf("bottom box at y=%v, want the floor at 0", b.Position.Y())
	}
	// Boxes are 1 unit tall, so the upper one's base should sit on the lower
	// one's top.
	if abs32(tp.Position.Y()-1) > 1e-2 {
		t.Errorf("top box at y=%v, want to rest at 1", tp.Position.Y())
	}
	if _, _, overlapping := overlapOf(b, tp); overlapping {
		t.Error("the stacked boxes are still interpenetrating")
	}
}

func TestCanDisplaceRefusesToPushGroundedEntitiesDown(t *testing.T) {
	// The rule that makes a stack settle exactly instead of sagging: half a
	// separation spent pressing a grounded box into the floor is half a
	// separation wasted, and the world resolve undoes it every frame.
	grounded := solidEntity("grounded", mgl32.Vec3{})
	grounded.Collision.OnGround = true

	if canDisplace(&grounded, 1, -1) {
		t.Error("a grounded entity accepted being pushed downward")
	}
	if !canDisplace(&grounded, 1, 1) {
		t.Error("a grounded entity refused being pushed upward")
	}
	// Standing on something says nothing about moving sideways.
	if !canDisplace(&grounded, 0, -1) || !canDisplace(&grounded, 2, -1) {
		t.Error("a grounded entity refused a horizontal push")
	}

	airborne := solidEntity("airborne", mgl32.Vec3{})
	if !canDisplace(&airborne, 1, -1) {
		t.Error("an airborne entity refused being pushed downward")
	}
}

func TestResolveOverlapGivesTheWholePushToTheOneThatCanTakeIt(t *testing.T) {
	// Two movable boxes normally split the separation. When the lower one is
	// resting on the floor, it cannot give way downward, so the upper one takes
	// all of it — and the pair comes apart in one step rather than converging.
	bottom := solidEntity("bottom", mgl32.Vec3{0, 0, 0})
	bottom.Collision.OnGround = true
	top := solidEntity("top", mgl32.Vec3{0, 0.6, 0})

	movedBottom, movedTop := resolveEntityOverlap(&bottom, &top)
	if movedBottom {
		t.Error("the grounded box was pushed down")
	}
	if !movedTop {
		t.Fatal("the upper box was not moved")
	}
	if bottom.Position.Y() != 0 {
		t.Errorf("bottom moved to %v", bottom.Position.Y())
	}
	if abs32(top.Position.Y()-1) > 1e-4 {
		t.Errorf("top at %v, want to rest at 1", top.Position.Y())
	}
	if _, _, overlapping := overlapOf(&bottom, &top); overlapping {
		t.Error("still overlapping after one resolution")
	}
}
