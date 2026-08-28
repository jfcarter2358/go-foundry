package world

// opening_test.go — holes in a floor or ceiling.
//
// The property that matters most is that the three systems reading an opening
// agree: you must not be able to see a hole you cannot fall through, nor fall
// through one you cannot see. Geometry is checked here through the mesh the
// triangulator actually produces; collision has its own tests in the collision
// package, over the same shapes.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// hatchChunk is two sectors stacked on one 20x20 footprint — a hall at y=0 and
// a mezzanine at y=6 — with a 4x4 hole cut in the mezzanine's floor.
func hatchChunk() *Chunk {
	ch := &Chunk{
		ID:         "tower",
		Points:     []mgl32.Vec2{{0, 0}, {20, 0}, {20, 20}, {0, 20}},
		PointNames: make([]string, 4),
		Layers: []ChunkLayer{
			{Name: "Hall", BaseY: 0, CeilY: 5},
			{Name: "Mezzanine", BaseY: 6, CeilY: 10},
		},
	}
	mk := func(id string, layer int, floorY, ceilY float32) Sector {
		return Sector{
			ID:           id,
			Layer:        layer,
			PointIndices: []int{0, 1, 2, 3},
			EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
			Floor:        Floor{Enabled: true, Height: floorY, VertexOffsets: []float32{0, 0, 0, 0}},
			Ceiling:      Ceiling{Enabled: true, Height: ceilY, VertexOffsets: []float32{0, 0, 0, 0}},
			MinBound:     mgl32.Vec2{0, 0},
			MaxBound:     mgl32.Vec2{20, 20},
		}
	}
	hall := mk("hall", 0, 0, 5)
	mezz := mk("mezz", 1, 6, 10)
	mezz.Floor.Openings = []Opening{{
		ID:     "stairwell",
		Points: []mgl32.Vec2{{8, 8}, {12, 8}, {12, 12}, {8, 12}},
	}}
	ch.Sectors = []Sector{hall, mezz}
	ch.RecomputeBounds()
	return ch
}

func TestSurfaceIsOpenOnlyInsideTheHole(t *testing.T) {
	ch := hatchChunk()
	ops := ch.Sectors[1].Floor.Openings

	for _, in := range []mgl32.Vec2{{10, 10}, {8.5, 8.5}, {11.9, 11.9}} {
		if !SurfaceIsOpenAt(ops, in) {
			t.Errorf("%v is inside the hole but reads as solid floor", in)
		}
	}
	for _, out := range []mgl32.Vec2{{2, 2}, {10, 7.9}, {12.1, 10}, {19, 19}} {
		if SurfaceIsOpenAt(ops, out) {
			t.Errorf("%v is outside the hole but reads as open", out)
		}
	}
}

// The hole has to lead somewhere, and it must not answer with the sector it was
// cut in.
func TestOpeningTargetFindsTheSectorBelow(t *testing.T) {
	ch := hatchChunk()
	mezz := &ch.Sectors[1]

	got := OpeningTarget(ch, mezz, &mezz.Floor.Openings[0], true)
	if got == nil {
		t.Fatal("a hole in the mezzanine floor leads nowhere")
	}
	if got.ID != "hall" {
		t.Errorf("the stairwell leads to %q, want the hall under it", got.ID)
	}

	// The mirrored case: a hole in the hall's ceiling leads up.
	hall := &ch.Sectors[0]
	hall.Ceiling.Openings = []Opening{{
		ID:     "up",
		Points: []mgl32.Vec2{{8, 8}, {12, 8}, {12, 12}, {8, 12}},
	}}
	got = OpeningTarget(ch, hall, &hall.Ceiling.Openings[0], false)
	if got == nil || got.ID != "mezz" {
		t.Errorf("a hole in the hall ceiling leads to %v, want the mezzanine", got)
	}
}

func TestOpeningOverNothingLeadsNowhere(t *testing.T) {
	ch := hatchChunk()
	ch.Sectors = ch.Sectors[1:] // drop the hall: the hole now opens onto a void
	mezz := &ch.Sectors[0]

	if got := OpeningTarget(ch, mezz, &mezz.Floor.Openings[0], true); got != nil {
		t.Errorf("a hole over nothing led to %q", got.ID)
	}
}

// The geometry half: the mesh must actually lose its triangles where the hole
// is, or the hole is a lie collision tells and the eye does not.
func TestFloorMeshIsCutWhereTheOpeningIs(t *testing.T) {
	ch := hatchChunk()
	mezz := &ch.Sectors[1]
	points := SectorPoints(ch, mezz)

	frame := NewPlanarSurfaceFrame(points, mezz.Floor.VertexHeight, mezz.Floor.Height, 4.0, true)
	verts, indices, _ := BuildSurfaceMesh(frame, &mezz.Floor.Mesh, false, mezz.Floor.Openings)
	if len(indices) == 0 {
		t.Fatal("the floor triangulated to nothing")
	}

	covered := func(xz mgl32.Vec2) bool {
		for i := 0; i+2 < len(indices); i += 3 {
			var tri [3]mgl32.Vec2
			for k := 0; k < 3; k++ {
				v := verts[indices[i+k]]
				tri[k] = mgl32.Vec2{v[0], v[2]}
			}
			if inside, _, _, _ := pointInTri(xz, tri); inside {
				return true
			}
		}
		return false
	}

	if covered(mgl32.Vec2{10, 10}) {
		t.Error("the middle of the hole is still covered by floor triangles")
	}
	// Away from the bridge seam. EarClipWithHoles joins a hole to the outline
	// by duplicating two vertices and nudging them ~1e-5 apart (geom's
	// bridgeHole), which leaves a hairline crack along the join. That is
	// inherent to bridging and predates openings; every extrusion has carried
	// it. At a hundredth of a millimetre it is far below anything visible, but
	// a sample point sitting exactly on it would land in the crack.
	for _, solid := range []mgl32.Vec2{{2, 2}, {18, 4}, {4, 18}, {10, 3}, {3, 10}, {17, 19}} {
		if !covered(solid) {
			t.Errorf("%v is outside the hole but lost its floor", solid)
		}
	}
}

// A hole's rim sits on the surface it is cut into. On a sloped floor that means
// following the slope — flattening it to the sector's base height would leave a
// step around the hole and a crack where the bridge joins it.
func TestHoleRimFollowsASlopedFloor(t *testing.T) {
	ch := hatchChunk()
	mezz := &ch.Sectors[1]
	// Tilt the floor: two corners 8 units higher than the other two.
	mezz.Floor.VertexOffsets = []float32{0, 0, 8, 8}
	points := SectorPoints(ch, mezz)

	frame := NewPlanarSurfaceFrame(points, mezz.Floor.VertexHeight, mezz.Floor.Height, 4.0, true)
	verts, _, _ := BuildSurfaceMesh(frame, &mezz.Floor.Mesh, false, mezz.Floor.Openings)

	// Every rim vertex must sit at the height the surrounding surface has
	// there, which for this ramp is strictly between the low and high edges.
	for _, v := range verts {
		xz := mgl32.Vec2{v[0], v[2]}
		onRim := false
		for _, p := range mezz.Floor.Openings[0].Points {
			if p.Sub(xz).Len() < 1e-3 {
				onRim = true
				break
			}
		}
		if !onRim {
			continue
		}
		want, _, ok := InterpolateSurfaceHeight(ch, mezz, xz, true)
		if !ok {
			continue
		}
		if diff := v[1] - want; diff > 1e-3 || diff < -1e-3 {
			t.Errorf("rim vertex at %v sits at y=%v, want %v (the sloped surface there)",
				xz, v[1], want)
		}
	}
}

// pointInTri is a local barycentric test, kept here rather than reaching for the
// geom helper so the geometry assertions above do not depend on the same code
// the triangulator does.
func pointInTri(p mgl32.Vec2, tri [3]mgl32.Vec2) (bool, float32, float32, float32) {
	v0 := tri[2].Sub(tri[0])
	v1 := tri[1].Sub(tri[0])
	v2 := p.Sub(tri[0])
	d00 := v0.Dot(v0)
	d01 := v0.Dot(v1)
	d02 := v0.Dot(v2)
	d11 := v1.Dot(v1)
	d12 := v1.Dot(v2)
	denom := d00*d11 - d01*d01
	if denom == 0 {
		return false, 0, 0, 0
	}
	u := (d11*d02 - d01*d12) / denom
	v := (d00*d12 - d01*d02) / denom
	const eps = -1e-6
	return u >= eps && v >= eps && u+v <= 1-eps, 1 - u - v, v, u
}

// ---------- coplanar stacked surfaces ----------

func TestCoplanarCeilingAndFloorAreFound(t *testing.T) {
	ch := hatchChunk()
	// Put the hall's ceiling exactly on the mezzanine's floor, which is what a
	// second storey built straight onto the first looks like.
	ch.Sectors[0].Ceiling.Height = 6

	pairs := CoplanarStackedSurfaces(ch)
	if len(pairs) != 1 {
		t.Fatalf("found %d coplanar pairs, want 1", len(pairs))
	}
	if pairs[0].Lower.ID != "hall" || pairs[0].Upper.ID != "mezz" {
		t.Errorf("pair is %s/%s, want hall/mezz", pairs[0].Lower.ID, pairs[0].Upper.ID)
	}
	if pairs[0].Height != 6 {
		t.Errorf("pair height %v, want 6", pairs[0].Height)
	}
}

// The ordinary arrangement — a slab between the storeys — must not be reported.
func TestSurfacesWithASlabBetweenThemAreNotReported(t *testing.T) {
	ch := hatchChunk() // hall ceiling at 5, mezzanine floor at 6
	if pairs := CoplanarStackedSurfaces(ch); len(pairs) != 0 {
		t.Errorf("a 1-unit slab was reported as coplanar: %d pairs", len(pairs))
	}
}

// Two rooms on different storeys that do not sit over each other share no
// ground, so their surfaces cannot fight however their heights line up.
func TestSectorsSideBySideAreNotReported(t *testing.T) {
	ch := hatchChunk()
	// Move the mezzanine off to one side entirely.
	ch.Points = append(ch.Points, mgl32.Vec2{40, 0}, mgl32.Vec2{60, 0}, mgl32.Vec2{60, 20}, mgl32.Vec2{40, 20})
	ch.Sectors[1].PointIndices = []int{4, 5, 6, 7}
	ch.Sectors[1].MinBound = mgl32.Vec2{40, 0}
	ch.Sectors[1].MaxBound = mgl32.Vec2{60, 20}
	ch.Sectors[0].Ceiling.Height = 6 // same height as the far sector's floor

	if pairs := CoplanarStackedSurfaces(ch); len(pairs) != 0 {
		t.Errorf("sectors that share no ground were reported: %d pairs", len(pairs))
	}
}

func TestSeparatingMovesTheCeilingNotTheFloor(t *testing.T) {
	ch := hatchChunk()
	ch.Sectors[0].Ceiling.Height = 6
	floorBefore := ch.Sectors[1].Floor.Height

	if n := SeparateCoplanarSurfaces(ch); n != 1 {
		t.Fatalf("separated %d surfaces, want 1", n)
	}
	if got, want := ch.Sectors[0].Ceiling.Height, float32(6)-SlabThickness; got != want {
		t.Errorf("the hall ceiling is at %v, want %v", got, want)
	}
	if ch.Sectors[1].Floor.Height != floorBefore {
		t.Errorf("the mezzanine floor moved to %v from %v — walkable surfaces must stay put",
			ch.Sectors[1].Floor.Height, floorBefore)
	}
	if pairs := CoplanarStackedSurfaces(ch); len(pairs) != 0 {
		t.Errorf("still %d coplanar pairs after separating", len(pairs))
	}
}

// A new storey must not be born coplanar with the one below it — this is the
// trap AddLayer used to set, by starting the new band exactly on the old one.
func TestAddLayerLeavesASlab(t *testing.T) {
	ch := hatchChunk()
	ch.Layers = []ChunkLayer{{Name: "Ground", BaseY: 0, CeilY: 4}}
	ch.Sectors = ch.Sectors[:1]
	ch.Sectors[0].Layer = 0
	ch.Sectors[0].Floor.Height, ch.Sectors[0].Ceiling.Height = 0, 4

	i := ch.AddLayer()
	band := ch.LayerAt(i)
	if band.BaseY <= 4 {
		t.Errorf("the new layer's floor starts at %v, on or under the ceiling below it at 4", band.BaseY)
	}
	if got, want := band.BaseY, float32(4)+SlabThickness; got != want {
		t.Errorf("new layer BaseY = %v, want %v", got, want)
	}
}
