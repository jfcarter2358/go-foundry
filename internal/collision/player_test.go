package collision

// player_test.go — MovePlayer, which used to be inlined in the main loop.
//
// The last test here is the one that matters for what comes next: MovePlayer is
// handed the chunks it resolves against and never asks a level where anything
// is, so the same walk produces the same *relative* result wherever those
// chunks happen to sit. That is what a vehicle interior will rely on.

import (
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// walkMove is the gameplay tuning the shipped build uses, so these tests are
// against the real numbers rather than convenient ones.
func walkMove(intent mgl32.Vec3) PlayerMove {
	return PlayerMove{
		Intent:       intent,
		Gravity:      true,
		DTScale:      1.0,
		GravityAccel: 0.010,
		MaxFallSpeed: 0.50,
		JumpVelocity: 0.20,
	}
}

// settle runs enough frames for a dropped player to come to rest.
func settle(p *world.Entity, lvl *world.Level, n int) (onGround bool) {
	for i := 0; i < n; i++ {
		onGround = MovePlayer(p, lvl, walkMove(mgl32.Vec3{}))
	}
	return onGround
}

func newTestPlayer(pos mgl32.Vec3) *world.Entity { return world.NewPlayer(pos) }

func TestMovePlayerFallsAndLandsOnTheFloor(t *testing.T) {
	lvl := collisionTestLevel() // floors at y=0, ceilings at y=8
	p := newTestPlayer(mgl32.Vec3{5, 6, 5})

	if onGround := MovePlayer(p, lvl, walkMove(mgl32.Vec3{})); onGround {
		t.Error("reported on the ground on the first frame of a 6-unit fall")
	}

	onGround := settle(p, lvl, 400)
	if !onGround {
		t.Fatalf("never landed; ended at y=%v", p.Position.Y())
	}
	// ResolveFloorCollision's floor half treats the position as the body
	// centre, so a resting player sits one half-height above the floor.
	if got, want := p.Position.Y(), float32(0)+p.BoundHalfY; got != want {
		t.Errorf("rested at y=%v, want %v", got, want)
	}
	if p.Velocity[1] != 0 {
		t.Errorf("vertical velocity survived the landing: %v", p.Velocity[1])
	}
}

func TestMovePlayerAppliesIntentHorizontally(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 0.9, 5})
	settle(p, lvl, 5)

	start := p.Position
	MovePlayer(p, lvl, walkMove(mgl32.Vec3{0.5, 0, 0}))

	if got := p.Position.X() - start.X(); got < 0.49 || got > 0.51 {
		t.Errorf("moved %v in X, want ~0.5", got)
	}
	if got := p.Position.Z() - start.Z(); got != 0 {
		t.Errorf("drifted %v in Z with no Z intent", got)
	}
}

func TestMovePlayerJumpLeavesTheGround(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 0.9, 5})
	settle(p, lvl, 5)

	mv := walkMove(mgl32.Vec3{})
	mv.Jump = true
	onGround := MovePlayer(p, lvl, mv)
	if onGround {
		t.Error("still on the ground on the frame the jump was applied")
	}
	if p.Position.Y() <= 0.9 {
		t.Errorf("jump did not lift the player: y=%v", p.Position.Y())
	}

	// And it comes back down.
	if !settle(p, lvl, 400) {
		t.Error("never landed after the jump")
	}
}

func TestMovePlayerFreeFlyIgnoresGravityAndGround(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 4, 5})

	mv := walkMove(mgl32.Vec3{0, 0.25, 0})
	mv.Gravity = false
	onGround := MovePlayer(p, lvl, mv)

	if onGround {
		t.Error("free-fly reported standing on the ground")
	}
	if got := p.Position.Y(); got != 4.25 {
		t.Errorf("free-fly rose to %v, want 4.25 (the intent, and nothing else)", got)
	}
}

// step is the shipped MovementSpeed. The tests below walk at it deliberately:
// the resolvers are position-based rather than swept, so a step large enough to
// clear a wall *and* leave the sector in one frame escapes the level entirely —
// the lookup then finds no sector and nothing resolves. That is a real gap, and
// it is what the swept-collision work exists to close; it is not something this
// extraction changed, so it is not what these tests are guarding.
const step = float32(0.1)

func TestMovePlayerIsStoppedByAWall(t *testing.T) {
	lvl := collisionTestLevel() // sectors span x in [0,20], walls on the outside
	p := newTestPlayer(mgl32.Vec3{1, 0.9, 5})
	settle(p, lvl, 5)

	// Walk into the x=0 wall for far longer than it takes to reach it.
	for i := 0; i < 200; i++ {
		MovePlayer(p, lvl, walkMove(mgl32.Vec3{-step, 0, 0}))
	}
	if p.Position.X() < p.BoundHalfX {
		t.Errorf("walked through the wall to x=%v", p.Position.X())
	}
}

func TestMovePlayerRecordsWhereItIs(t *testing.T) {
	lvl := collisionTestLevel()
	// The shared edge starts out a wall on both sides; InferPortals promotes it,
	// which is what makes A and B one walkable space rather than two rooms.
	lvl.Chunks[0].InferPortals()

	p := newTestPlayer(mgl32.Vec3{5, 0.9, 5}) // inside sector A
	settle(p, lvl, 5)
	if p.CurrentSector != "A" {
		t.Errorf("CurrentSector = %q, want A", p.CurrentSector)
	}

	// Walk across the shared edge into B (x from 5 to 15).
	for i := 0; i < 100; i++ {
		MovePlayer(p, lvl, walkMove(mgl32.Vec3{step, 0, 0}))
	}
	if p.CurrentSector != "B" {
		t.Errorf("after walking into B, CurrentSector = %q (x=%v)", p.CurrentSector, p.Position.X())
	}
}

// TestMovePlayerIsIndependentOfWhereTheChunkSits is the invariant a moving
// vehicle interior rests on: MovePlayer resolves against the chunks it is
// handed, in whatever space those chunks are expressed in. Translate the whole
// world and the same walk must produce the same walk.
func TestMovePlayerIsIndependentOfWhereTheChunkSits(t *testing.T) {
	const dx, dz = float32(1000), float32(-750)

	here := collisionTestLevel()
	there := collisionTestLevel()
	shiftChunkXZ(&there.Chunks[0], dx, dz)
	// The spatial hash indexes by position, so moving the geometry invalidates
	// it — the same rebuild the engine does whenever a footprint moves.
	if err := world.RebuildIndexes(there); err != nil {
		t.Fatal(err)
	}

	a := newTestPlayer(mgl32.Vec3{5, 6, 5})
	b := newTestPlayer(mgl32.Vec3{5 + dx, 6, 5 + dz})

	steps := []mgl32.Vec3{
		{0, 0, 0}, {step, 0, 0}, {step, 0, step / 2}, {0, 0, step},
		{-step, 0, step / 2}, {0, 0, 0}, {step, 0, -step},
	}
	for i := 0; i < 120; i++ {
		step := steps[i%len(steps)]
		ga := MovePlayer(a, here, walkMove(step))
		gb := MovePlayer(b, there, walkMove(step))
		if ga != gb {
			t.Fatalf("step %d: ground flags diverged (%v vs %v)", i, ga, gb)
		}
	}

	want := a.Position.Add(mgl32.Vec3{dx, 0, dz})
	if !vecNear(b.Position, want, 1e-3) {
		t.Errorf("the same walk ended at %v in the shifted world, want %v", b.Position, want)
	}
	if a.CurrentSector != b.CurrentSector {
		t.Errorf("ended in different sectors: %q vs %q", a.CurrentSector, b.CurrentSector)
	}
}

// shiftChunkXZ translates a chunk's whole footprint, which for these GL-free
// test chunks means the point pool, the cached wall endpoints and the per-sector
// 2D bounds — everything MovePlayer reads.
func shiftChunkXZ(ch *world.Chunk, dx, dz float32) {
	d2 := mgl32.Vec2{dx, dz}
	for i := range ch.Points {
		ch.Points[i] = ch.Points[i].Add(d2)
	}
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		s.MinBound = s.MinBound.Add(d2)
		s.MaxBound = s.MaxBound.Add(d2)
		for wi := range s.Walls {
			s.Walls[wi].StartPoint = s.Walls[wi].StartPoint.Add(d2)
			s.Walls[wi].EndPoint = s.Walls[wi].EndPoint.Add(d2)
		}
	}
	ch.RecomputeBounds()
}
