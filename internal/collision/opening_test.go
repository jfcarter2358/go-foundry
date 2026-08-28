package collision

// opening_test.go — walking around, and falling through, a hole in a floor.
//
// The geometry half of an opening is tested in the world package. This is the
// half that decides whether it is a stairwell or a decal: the surface has to
// stop clamping bodies inside the hole, and the sector lookup has to hand the
// falling body to whatever is underneath as it drops past.
//
// Those two are separate mechanisms and neither is enough alone. Without the
// clamp change you stand on thin air over the hole; without the vertical sector
// ranking (world/spatial.go) you fall out of the world instead of landing in
// the room below.

import (
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// nearly compares two heights within a hair, for the assertions whose expected
// value is itself a rounded sum.
func nearly(a, b float32) bool { return a-b < 1e-4 && b-a < 1e-4 }

// towerLevel is a hall at y=0 under a mezzanine at y=6, sharing one 20x20
// footprint in a single chunk, with a 4x4 stairwell cut in the mezzanine floor
// around (10,10).
func towerLevel(t *testing.T) *world.Level {
	t.Helper()
	ch := world.Chunk{
		ID:         "tower",
		Scale:      1,
		Points:     []mgl32.Vec2{{0, 0}, {20, 0}, {20, 20}, {0, 20}},
		PointNames: make([]string, 4),
		Layers: []world.ChunkLayer{
			{Name: "Hall", BaseY: 0, CeilY: 5},
			{Name: "Mezzanine", BaseY: 6, CeilY: 12},
		},
	}
	mk := func(id string, layer int, floorY, ceilY float32) world.Sector {
		return world.Sector{
			ID:           id,
			Layer:        layer,
			PointIndices: []int{0, 1, 2, 3},
			EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
			Floor:        world.Floor{Enabled: true, Height: floorY, VertexOffsets: []float32{0, 0, 0, 0}},
			Ceiling:      world.Ceiling{Enabled: true, Height: ceilY, VertexOffsets: []float32{0, 0, 0, 0}},
			MinBound:     mgl32.Vec2{0, 0},
			MaxBound:     mgl32.Vec2{20, 20},
		}
	}
	hall := mk("hall", 0, 0, 5)
	mezz := mk("mezz", 1, 6, 12)
	mezz.Floor.Openings = []world.Opening{{
		ID:     "stairwell",
		Points: []mgl32.Vec2{{8, 8}, {12, 8}, {12, 12}, {8, 12}},
	}}
	ch.Sectors = []world.Sector{hall, mezz}
	ch.RecomputeBounds()

	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	return lvl
}

func TestStandingOnAMezzanineBesideItsStairwell(t *testing.T) {
	lvl := towerLevel(t)
	// Well clear of the hole.
	p := newTestPlayer(mgl32.Vec3{3, 8, 3})

	if !settle(p, lvl, 400) {
		t.Fatalf("never landed on the mezzanine; ended at y=%v", p.Position.Y())
	}
	// Compared with a tolerance rather than exactly: a floor at y=6 makes
	// `6 + BoundHalfY` a rounded sum, unlike the y=0 floors the other resting
	// tests use, where the addition is exact.
	if got, want := p.Position.Y(), float32(6)+p.BoundHalfY; !nearly(got, want) {
		t.Errorf("rested at y=%v, want the mezzanine floor at %v", got, want)
	}
	if p.CurrentSector != "mezz" {
		t.Errorf("resting on the mezzanine but CurrentSector is %q", p.CurrentSector)
	}
}

func TestFallingThroughAStairwellLandsInTheRoomBelow(t *testing.T) {
	lvl := towerLevel(t)
	// Directly over the hole, at mezzanine standing height.
	p := newTestPlayer(mgl32.Vec3{10, 7, 10})

	if !settle(p, lvl, 800) {
		t.Fatalf("never came to rest; ended at y=%v", p.Position.Y())
	}
	if got, want := p.Position.Y(), float32(0)+p.BoundHalfY; got != want {
		t.Errorf("rested at y=%v, want the hall floor at %v — the hole did not let it through",
			got, want)
	}
	if p.CurrentSector != "hall" {
		t.Errorf("landed in the hall but CurrentSector is %q", p.CurrentSector)
	}
}

// Walking off the solid part of the mezzanine and over the hole has to start a
// fall — this is the gesture an author actually cares about, and it exercises
// the clamp and the lookup together rather than dropping a body in pre-placed.
func TestWalkingOverTheHoleStartsAFall(t *testing.T) {
	lvl := towerLevel(t)
	p := newTestPlayer(mgl32.Vec3{4, 7, 10})
	if !settle(p, lvl, 400) {
		t.Fatal("never landed on the mezzanine to start with")
	}
	startY := p.Position.Y()

	// Walk east until past the hole's near edge at x=8.
	for i := 0; i < 60 && p.Position.X() < 10; i++ {
		MovePlayer(p, lvl, walkMove(mgl32.Vec3{0.2, 0, 0}))
	}
	if p.Position.X() < 8 {
		t.Fatalf("never reached the hole; stopped at x=%v", p.Position.X())
	}
	if p.Position.Y() >= startY {
		t.Errorf("walked out over the hole at y=%v and did not start falling (was %v)",
			p.Position.Y(), startY)
	}

	if !settle(p, lvl, 800) {
		t.Fatalf("never came to rest after stepping into the hole; y=%v", p.Position.Y())
	}
	if got, want := p.Position.Y(), float32(0)+p.BoundHalfY; got != want {
		t.Errorf("came to rest at y=%v, want the hall floor at %v", got, want)
	}
}

// The mirror: a ceiling with a hole in it must stop bonking heads, or a stair
// climbing into the storey above hits an invisible lid.
func TestACeilingOpeningDoesNotBonkHeads(t *testing.T) {
	lvl := towerLevel(t)
	hall := &lvl.Chunks[0].Sectors[0]
	hall.Ceiling.Openings = []world.Opening{{
		ID:     "up",
		Points: []mgl32.Vec2{{8, 8}, {12, 8}, {12, 12}, {8, 12}},
	}}

	p := newTestPlayer(mgl32.Vec3{10, 1, 10})
	settle(p, lvl, 200)

	// Jump straight up under the hole. With the ceiling intact this clamps at
	// the hall ceiling; with the hole it is free to rise past it.
	p.Velocity[1] = 0
	top := p.Position.Y()
	for i := 0; i < 120; i++ {
		mv := walkMove(mgl32.Vec3{})
		if i == 0 {
			mv.Jump = true
			mv.JumpVelocity = 1.0
		}
		MovePlayer(p, lvl, mv)
		if p.Position.Y() > top {
			top = p.Position.Y()
		}
	}
	if top <= 5 {
		t.Errorf("rose only to y=%v; the hole in the ceiling at y=5 did not let the body through", top)
	}
}

// A solid ceiling still stops a body — the opening must not have disabled the
// clamp everywhere.
func TestASolidCeilingStillBonksHeads(t *testing.T) {
	lvl := towerLevel(t)
	// Away from the stairwell, under the intact part of the hall ceiling.
	p := newTestPlayer(mgl32.Vec3{3, 1, 3})
	settle(p, lvl, 200)

	top := p.Position.Y()
	for i := 0; i < 120; i++ {
		mv := walkMove(mgl32.Vec3{})
		if i == 0 {
			mv.Jump = true
			mv.JumpVelocity = 1.0
		}
		MovePlayer(p, lvl, mv)
		if p.Position.Y() > top {
			top = p.Position.Y()
		}
	}
	if top >= 5 {
		t.Errorf("rose to y=%v, through a hall ceiling at y=5 that has no hole in it", top)
	}
}
