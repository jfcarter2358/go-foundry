package collision

// slab_solid_test.go — a thick floor is solid (world/slab.go).
//
// Before slabs, what stopped a body walking under a mezzanine was the *ceiling
// of the room down there*. A mezzanine over an open hall therefore had nothing
// underneath it at all: a body could rise straight through the floor above and
// land on top of it. Thickness is what changes that, and only thickness, so
// every test here is written as a pair — the same fixture with and without one.

import (
	"foundry/internal/testenv"
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// mezzanineLevel is a 20x10 hall with floor 0 and no ceiling, and a mezzanine
// covering its western half at floor 4, also with no ceiling. Nothing but the
// mezzanine's own underside can stop a body rising under it.
//
// thickness 0 is the plane the engine has always had; anything else is a slab.
func mezzanineLevel(t *testing.T, thickness float32) *world.Level {
	t.Helper()
	// The slab's solidity reads the collision cache Sector.Load fills from the
	// triangles it built, so these fixtures have to actually build — which
	// needs the upload skipped rather than a GL context.
	testenv.WithoutGeometryUploads(t)
	ch := world.Chunk{
		ID:    "hall",
		Scale: 1,
		Points: []mgl32.Vec2{
			{0, 0}, {20, 0}, {20, 10}, {0, 10}, // hall
			{0, 0}, {10, 0}, {10, 10}, {0, 10}, // mezzanine, western half
		},
		PointNames: make([]string, 8),
	}
	mk := func(id string, idx []int, floorY float32, lo, hi mgl32.Vec2) world.Sector {
		return world.Sector{
			ID:           id,
			PointIndices: idx,
			EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
			Floor:        world.Floor{Enabled: true, Height: floorY, VertexOffsets: []float32{0, 0, 0, 0}},
			Ceiling:      world.Ceiling{Enabled: false, Height: floorY + 12, VertexOffsets: []float32{0, 0, 0, 0}},
			MinBound:     lo, MaxBound: hi,
		}
	}
	hall := mk("hall", []int{0, 1, 2, 3}, 0, mgl32.Vec2{0, 0}, mgl32.Vec2{20, 10})
	mezz := mk("mezz", []int{4, 5, 6, 7}, 4, mgl32.Vec2{0, 0}, mgl32.Vec2{10, 10})
	mezz.Floor.Slab.Thickness = thickness
	ch.Sectors = []world.Sector{hall, mezz}
	ch.RecomputeBounds()
	ch.MaxBound[1] = 20

	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	for si := range lvl.Chunks[0].Sectors {
		if err := world.ReloadSector(&lvl.Chunks[0], &lvl.Chunks[0].Sectors[si]); err != nil {
			t.Fatal(err)
		}
	}
	lvl.Chunks[0].MaxBound[1] = 20
	return lvl
}

// The head-stop itself, read straight off the query every clamp goes through.
func TestThickFloorLowersTheCeilingBeneathIt(t *testing.T) {
	for _, c := range []struct {
		name      string
		thickness float32
		wantOK    bool
		want      float32
	}{
		{"no thickness leaves the hall open to the sky", 0, false, 0},
		{"a thick mezzanine caps the hall under it", 0.5, true, 3.5},
	} {
		lvl := mezzanineLevel(t, c.thickness)
		ch := &lvl.Chunks[0]
		hall := &ch.Sectors[0]

		// Under the mezzanine.
		got, ok := sectorCeilingHeightAt(ch, hall, mgl32.Vec2{5, 5})
		if ok != c.wantOK {
			t.Errorf("%s: ceiling found = %v, want %v", c.name, ok, c.wantOK)
			continue
		}
		if ok && got != c.want {
			t.Errorf("%s: ceiling at %v, want %v", c.name, got, c.want)
		}

		// Beyond it, the hall is still open however thick the mezzanine is —
		// the slab caps its own footprint, not the whole sector.
		if _, ok := sectorCeilingHeightAt(ch, hall, mgl32.Vec2{15, 5}); ok {
			t.Errorf("%s: the hall is capped beyond the mezzanine's footprint", c.name)
		}
	}
}

// A slab's own sector is standing on top of it, so it must never cap itself —
// that would be a body clamped into the floor it is resting on.
func TestASlabDoesNotCapItsOwnSector(t *testing.T) {
	lvl := mezzanineLevel(t, 0.5)
	ch := &lvl.Chunks[0]
	mezz := &ch.Sectors[1]
	if _, ok := sectorCeilingHeightAt(ch, mezz, mgl32.Vec2{5, 5}); ok {
		t.Error("the mezzanine's own slab is acting as its ceiling")
	}
}

// The behaviour that motivates the whole thing: jumping up under a mezzanine.
// Without a thickness the body rises through the floor above and ends up on it
// — the old behaviour, preserved. With one it stays underneath.
func TestJumpingUnderAThickMezzanineIsBlocked(t *testing.T) {
	jump := func(thickness float32) *world.Entity {
		lvl := mezzanineLevel(t, thickness)
		p := newTestPlayer(mgl32.Vec3{5, 1, 5})
		settle(p, lvl, 200)
		for i := 0; i < 400; i++ {
			mv := walkMove(mgl32.Vec3{})
			mv.JumpVelocity = 0.6 // enough to clear 4 units from the hall floor
			mv.Jump = true
			MovePlayer(p, lvl, mv)
		}
		return p
	}

	thin := jump(0)
	if thin.Position.Y() < 4 {
		t.Fatalf("with no thickness the body never got above the mezzanine floor (y=%v); "+
			"the fixture no longer tests what it means to", thin.Position.Y())
	}

	thick := jump(0.5)
	if thick.Position.Y() >= 3.5 {
		t.Errorf("jumped to y=%v under a slab whose underside is at 3.5", thick.Position.Y())
	}
	if thick.CurrentSector == "mezz" {
		t.Error("the body ended up on the mezzanine, having passed through its slab")
	}
}

// A hole cut through the floor is a hole through the whole solid, or a
// stairwell up to a thick mezzanine would be sealed by the thing it climbs to.
func TestAnOpeningGoesThroughTheSlab(t *testing.T) {
	lvl := mezzanineLevel(t, 0.5)
	ch := &lvl.Chunks[0]
	mezz := &ch.Sectors[1]
	mezz.Floor.Openings = []world.Opening{{
		ID:     "well",
		Points: []mgl32.Vec2{{3, 3}, {7, 3}, {7, 7}, {3, 7}},
	}}
	if err := world.ReloadSector(ch, mezz); err != nil {
		t.Fatal(err)
	}

	if _, ok := sectorCeilingHeightAt(ch, &ch.Sectors[0], mgl32.Vec2{5, 5}); ok {
		t.Error("the slab still caps the hall inside the stairwell it has cut through it")
	}
	if _, ok := sectorCeilingHeightAt(ch, &ch.Sectors[0], mgl32.Vec2{8.5, 5}); !ok {
		t.Error("the slab stopped capping the hall outside the opening as well")
	}
}

// A shaped far face is solid at the shape that was drawn, not at the flat plane
// it would have been — the same contract a shaped floor already has with what
// you stand on.
func TestSolidityFollowsAShapedUnderside(t *testing.T) {
	lvl := mezzanineLevel(t, 0.5)
	ch := &lvl.Chunks[0]
	mezz := &ch.Sectors[1]

	pts := world.SectorPoints(ch, mezz)
	mesh := world.SeedSurfaceMesh(mezz.FloorSlabFrame(pts))
	// Push the whole face down a further unit. Interior-free, so every point is
	// a boundary one — and boundary depths are re-derived from the frame, so
	// the shape has to be carried by an interior point to survive the rebuild.
	mesh.Points = append(mesh.Points, world.SurfacePoint{U: 5, V: 5, Depth: -1, Boundary: world.SurfaceFree})
	hub := len(mesh.Points) - 1
	ring := mesh.Faces[0].Verts
	mesh.Faces = nil
	for i := range ring {
		j := (i + 1) % len(ring)
		mesh.Faces = append(mesh.Faces, world.SurfaceFace{Verts: []int{ring[i], ring[j], hub}, Alpha: 1})
	}
	mezz.Floor.Slab.Mesh = mesh
	if err := world.ReloadSector(ch, mezz); err != nil {
		t.Fatal(err)
	}

	got, ok := sectorCeilingHeightAt(ch, &ch.Sectors[0], mgl32.Vec2{5, 5})
	if !ok {
		t.Fatal("a shaped slab stopped capping the hall entirely")
	}
	// The dimple's tip is a unit below the flat underside at 3.5.
	if got > 3.0 {
		t.Errorf("head-stop at %v under a face dipping to 2.5; it is reading the flat plane", got)
	}
}
