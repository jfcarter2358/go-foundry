package collision

// ceiling_entry_test.go — the outside of a ceiling is a wall.
//
// A body walking off a height into the roof of a lower room used to be admitted
// and then rescued by ResolveFloorCollision's ceiling clamp, which yanks it
// down to just under that ceiling in a single frame. That clamp is right for
// what it was written for — a jump inside a room, met going up — but a body
// arriving from above is not bonking its head on anything, and being teleported
// several units down through a solid surface reads as falling through the world.
//
// The shape that produces it is an ordinary authoring mistake: a staircase that
// tops out level with the ceiling of the room it climbs past, with no upper
// storey built yet. That is what these fixtures are.

import (
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// roofLevel is a room 20 wide with floor 0 and ceiling 3, and a platform beside
// it whose floor is at 3 — exactly the room's ceiling height — with no ceiling
// of its own. Walking off the platform into the room means walking onto its roof.
func roofLevel(t *testing.T) *world.Level {
	t.Helper()
	ch := world.Chunk{
		ID:    "tower",
		Scale: 1,
		Points: []mgl32.Vec2{
			{0, 0}, {10, 0}, {10, 10}, {0, 10}, // room
			{10, 0}, {20, 0}, {20, 10}, {10, 10}, // platform
		},
		PointNames: make([]string, 8),
	}
	mk := func(id string, idx []int, floorY float32, ceil bool, lo, hi mgl32.Vec2) world.Sector {
		s := world.Sector{
			ID:           id,
			PointIndices: idx,
			EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
			Floor:        world.Floor{Enabled: true, Height: floorY, VertexOffsets: []float32{0, 0, 0, 0}},
			Ceiling:      world.Ceiling{Enabled: ceil, Height: 3, VertexOffsets: []float32{0, 0, 0, 0}},
			MinBound:     lo, MaxBound: hi,
		}
		return s
	}
	room := mk("room", []int{0, 1, 2, 3}, 0, true, mgl32.Vec2{0, 0}, mgl32.Vec2{10, 10})
	platform := mk("platform", []int{4, 5, 6, 7}, 3, false, mgl32.Vec2{10, 0}, mgl32.Vec2{20, 10})
	platform.Ceiling.Height = 3
	ch.Sectors = []world.Sector{room, platform}
	ch.RecomputeBounds()
	// The platform's walls reach well above its floor, so the chunk's own Y
	// bounds cover a body standing on it.
	if ch.MaxBound.Y() < 7 {
		ch.MaxBound[1] = 7
	}

	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	lvl.Chunks[0].MaxBound[1] = 7
	return lvl
}

func TestWalkingOntoARoofIsBlockedNotTeleported(t *testing.T) {
	lvl := roofLevel(t)
	p := newTestPlayer(mgl32.Vec3{15, 4, 5}) // over the platform
	if !settle(p, lvl, 300) {
		t.Fatalf("never landed on the platform; y=%v", p.Position.Y())
	}
	if p.CurrentSector != "platform" {
		t.Fatalf("settled in %q, want the platform", p.CurrentSector)
	}
	startY := p.Position.Y()

	// Walk west, off the platform and toward the room whose ceiling is level
	// with the platform's floor.
	worst := float32(0)
	for i := 0; i < 200; i++ {
		prev := p.Position.Y()
		MovePlayer(p, lvl, walkMove(mgl32.Vec3{-0.15, 0, 0}))
		if d := prev - p.Position.Y(); d > worst {
			worst = d
		}
	}
	if worst > 0.3 {
		t.Errorf("dropped %.2f in one frame — still teleporting through the roof", worst)
	}
	if p.Position.Y() < startY-0.1 {
		t.Errorf("ended at y=%v, below the platform floor it started on (%v)", p.Position.Y(), startY)
	}
	if p.CurrentSector != "platform" {
		t.Errorf("ended in %q; the body walked onto the room's roof", p.CurrentSector)
	}
}

// The block must not become a general ban on low ceilings: a body already in a
// room may jump into its ceiling and must keep moving horizontally, or any room
// without much headroom would freeze whatever jumped in it.
func TestJumpingIntoACeilingDoesNotFreezeMovement(t *testing.T) {
	lvl := roofLevel(t)
	p := newTestPlayer(mgl32.Vec3{2, 2, 5})
	settle(p, lvl, 200)
	startX := p.Position.X()

	for i := 0; i < 60; i++ {
		mv := walkMove(mgl32.Vec3{0.12, 0, 0})
		if i%10 == 0 {
			mv.Jump = true
		}
		MovePlayer(p, lvl, mv)
	}
	if moved := p.Position.X() - startX; moved < 2 {
		t.Errorf("jump-walking inside the room moved only %.2f — the ceiling froze it", moved)
	}
}

// Entering a low room at floor level is ordinary and must stay allowed.
func TestEnteringALowRoomAtFloorLevelIsAllowed(t *testing.T) {
	lvl := roofLevel(t)
	// Start inside the room, walk across it and back — never leaving floor level.
	p := newTestPlayer(mgl32.Vec3{1, 2, 5})
	settle(p, lvl, 200)
	if p.CurrentSector != "room" {
		t.Fatalf("settled in %q, want the room", p.CurrentSector)
	}
	for i := 0; i < 60; i++ {
		MovePlayer(p, lvl, walkMove(mgl32.Vec3{0.1, 0, 0}))
	}
	if p.Position.X() < 4 {
		t.Errorf("walking across the room was blocked at x=%v", p.Position.X())
	}
}

// A body denied entry keeps whatever vertical motion it had: refusing the
// sideways step must not also stop it falling.
func TestARefusedStepStillFalls(t *testing.T) {
	lvl := roofLevel(t)
	// In the air over the platform, pushing west into the room's roof.
	p := newTestPlayer(mgl32.Vec3{11, 6, 5})
	startY := p.Position.Y()
	for i := 0; i < 20; i++ {
		MovePlayer(p, lvl, walkMove(mgl32.Vec3{-0.15, 0, 0}))
	}
	if p.Position.Y() >= startY {
		t.Errorf("a body pushed against the roof stopped falling: y=%v (was %v)", p.Position.Y(), startY)
	}
}
