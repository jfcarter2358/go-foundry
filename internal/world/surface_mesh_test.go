package world

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func testWallFrame() WallSurfaceFrame {
	// A 4-long wall from the origin along +X, floor at 0, ceiling at 3.
	return NewWallSurfaceFrame(mgl32.Vec2{0, 0}, mgl32.Vec2{4, 0}, 0, 0, 3, 3)
}

func testFloorFrame() PlanarSurfaceFrame {
	pts := []mgl32.Vec2{{0, 0}, {4, 0}, {4, 4}, {0, 4}}
	return NewPlanarSurfaceFrame(pts, func(int) float32 { return 0 }, 0, 1, true)
}

// vertsAt collects the world positions in a built buffer, for checking that
// geometry landed where it should without depending on vertex ordering.
func vertsAt(verts [][]float32) []mgl32.Vec3 {
	out := make([]mgl32.Vec3, len(verts))
	for i, v := range verts {
		out[i] = mgl32.Vec3{v[0], v[1], v[2]}
	}
	return out
}

func TestEmptyMeshBuildsTheFlatSurface(t *testing.T) {
	f := testWallFrame()
	var m SurfaceMesh
	verts, idx, subs := BuildSurfaceMesh(f, &m, false, nil)
	if len(subs) != 0 {
		t.Fatalf("flat wall produced %d submeshes, want 0", len(subs))
	}
	if len(idx) != 6 {
		t.Fatalf("flat wall triangulated into %d indices, want 6 (two triangles)", len(idx))
	}
	if len(verts) != 4 {
		t.Fatalf("flat wall emitted %d vertices, want 4", len(verts))
	}
	// Every corner must sit on the wall plane (z == 0 here) and within its
	// height range.
	for _, p := range vertsAt(verts) {
		if math.Abs(float64(p.Z())) > 1e-4 {
			t.Errorf("corner %v left the wall plane", p)
		}
		if p.Y() < -1e-4 || p.Y() > 3+1e-4 {
			t.Errorf("corner %v outside the wall's height range", p)
		}
	}
}

func TestWallOutlineUsesEachEndsOwnHeight(t *testing.T) {
	// A wall between differing floor heights is a trapezoid. The old
	// extrusion builder averaged the two and drew a rectangle.
	f := NewWallSurfaceFrame(mgl32.Vec2{0, 0}, mgl32.Vec2{4, 0}, 0, 2, 3, 3)
	out := f.Outline()
	if got := out[3].Y(); math.Abs(float64(got-3)) > 1e-5 {
		t.Errorf("near corner V = %v, want 3 (ceiling 3 - floor 0)", got)
	}
	if got := out[2].Y(); math.Abs(float64(got-1)) > 1e-5 {
		t.Errorf("far corner V = %v, want 1 (ceiling 3 - floor 2)", got)
	}
}

func TestSeededMeshMatchesTheFlatSurface(t *testing.T) {
	f := testWallFrame()
	seeded := SeedSurfaceMesh(f)
	var empty SurfaceMesh
	a, ai, _ := BuildSurfaceMesh(f, &empty, false, nil)
	b, bi, _ := BuildSurfaceMesh(f, &seeded, false, nil)
	if len(a) != len(b) || len(ai) != len(bi) {
		t.Fatalf("seeded mesh built %d verts/%d idx, flat built %d/%d — they must agree",
			len(b), len(bi), len(a), len(ai))
	}
}

func TestExtrudeAFaceGrowsSidesAndMovesTheCap(t *testing.T) {
	f := testWallFrame()
	m := SeedSurfaceMesh(f)
	caps, sides := m.SurfaceExtrudeFaces([]int{0}, -0.5)
	if len(caps) != 1 || caps[0] != 0 {
		t.Fatalf("extrude returned caps %v, want the face that went in", caps)
	}
	if len(sides) != 4 {
		t.Fatalf("extruding a quad grew %d sides, want 4", len(sides))
	}
	for _, vi := range m.Faces[0].Verts {
		if got := m.Points[vi].Depth; math.Abs(float64(got+0.5)) > 1e-5 {
			t.Errorf("cap point depth = %v, want -0.5", got)
		}
		if m.Points[vi].Boundary != SurfaceFree {
			t.Errorf("cap point still claims outline corner %d; it has left the outline", m.Points[vi].Boundary)
		}
	}
	// The originals stay pinned, which is what keeps the surface attached.
	pinned := 0
	for _, p := range m.Points {
		if p.Boundary >= 0 {
			pinned++
		}
	}
	if pinned != 4 {
		t.Errorf("%d points still bound to the outline, want the original 4", pinned)
	}
}

func TestExtrudeIsRepeatable(t *testing.T) {
	// The whole point of the mesh over Extrusion: a cap can be extruded again.
	f := testWallFrame()
	m := SeedSurfaceMesh(f)
	m.SurfaceExtrudeFaces([]int{0}, -0.3)
	caps, sides := m.SurfaceExtrudeFaces([]int{0}, -0.3)
	if len(caps) != 1 || len(sides) != 4 {
		t.Fatalf("second extrude returned %d caps / %d sides, want 1 / 4", len(caps), len(sides))
	}
	for _, vi := range m.Faces[0].Verts {
		if got := m.Points[vi].Depth; math.Abs(float64(got+0.6)) > 1e-5 {
			t.Errorf("twice-extruded cap depth = %v, want -0.6", got)
		}
	}
}

func TestInsetThenExtrudeThenHoleCutsATunnel(t *testing.T) {
	// The doorway workflow end to end.
	f := testWallFrame()
	m := SeedSurfaceMesh(f)
	inset := m.SurfaceInsetFaces([]int{0}, 0.5)
	if len(inset) != 1 {
		t.Fatalf("inset returned %v, want one face", inset)
	}
	if len(m.Faces) != 5 {
		t.Fatalf("inset left %d faces, want 5 (the inset face plus a ring of 4)", len(m.Faces))
	}
	m.SurfaceExtrudeFaces(inset, -0.4)
	m.Faces[inset[0]].Hole = true

	windows := SurfaceWallWindows(&m)
	if len(windows) != 1 {
		t.Fatalf("wall reports %d pass-through windows, want 1", len(windows))
	}
	if len(windows[0]) != 4 {
		t.Errorf("window ring has %d corners, want 4", len(windows[0]))
	}
	// The hole face must not be drawn.
	verts, idx, _ := BuildSurfaceMesh(f, &m, false, nil)
	if len(idx) == 0 || len(verts) == 0 {
		t.Fatal("wall with a tunnel drew nothing at all")
	}
}

func TestSplitFaceRefusesAnAdjacentPair(t *testing.T) {
	f := testWallFrame()
	m := SeedSurfaceMesh(f)
	if got := m.SurfaceSplitFace(0, 0, 1); got != -1 {
		t.Errorf("splitting between adjacent corners returned %d, want -1", got)
	}
	if got := m.SurfaceSplitFace(0, 0, 2); got < 0 {
		t.Fatalf("splitting a quad on its diagonal returned %d, want a new face", got)
	}
	if len(m.Faces) != 2 {
		t.Fatalf("split left %d faces, want 2", len(m.Faces))
	}
	for _, f := range m.Faces {
		if len(f.Verts) != 3 {
			t.Errorf("split a quad into a %d-gon, want two triangles", len(f.Verts))
		}
	}
}

func TestAddFacePointSitsOnASlopedSurface(t *testing.T) {
	// Clicking on a sloped face must not dimple it.
	pts := []mgl32.Vec2{{0, 0}, {4, 0}, {4, 4}, {0, 4}}
	heights := []float32{0, 0, 2, 2}
	f := NewPlanarSurfaceFrame(pts, func(i int) float32 { return heights[i] }, 0, 1, true)
	m := SeedSurfaceMesh(f)
	np := m.SurfaceAddFacePoint(0, 2, 2)
	if np < 0 {
		t.Fatal("could not place a point on the face")
	}
	if got := m.Points[np].Depth; math.Abs(float64(got-1)) > 1e-4 {
		t.Errorf("point placed mid-slope got depth %v, want 1 (interpolated)", got)
	}
	if len(m.Faces) != 4 {
		t.Errorf("fanning a quad made %d faces, want 4", len(m.Faces))
	}
}

func TestSubdivideRepairsTheNeighbour(t *testing.T) {
	f := testFloorFrame()
	m := SeedSurfaceMesh(f)
	m.SurfaceSplitFace(0, 0, 2) // two triangles sharing an edge
	before := len(m.Faces[1].Verts)
	m.SurfaceSubdivideFaces([]int{0})
	// Face 1 was not selected but shares two edges with face 0; its ring must
	// have grown the midpoints, or the two would crack apart.
	last := len(m.Faces) - 1
	_ = last
	grew := false
	for i := range m.Faces {
		if len(m.Faces[i].Verts) > before {
			grew = true
		}
	}
	if !grew {
		t.Error("no face grew a midpoint; the unselected neighbour was not repaired")
	}
}

func TestMigratedWallExtrusionCutsAHoleAndBuildsAPrism(t *testing.T) {
	f := testWallFrame()
	ext := Extrusion{
		Outer: []ExtrusionPoint{{U: 1, V: 1}, {U: 3, V: 1}, {U: 3, V: 2}, {U: 1, V: 2}},
		Inner: []ExtrusionPoint{{U: 1, V: 1}, {U: 3, V: 1}, {U: 3, V: 2}, {U: 1, V: 2}},
		Depth: -0.5, Visible: true, BackVisible: true, Alpha: 1,
	}
	m := MigrateSurfaceMesh(f, []Extrusion{ext}, nil, false)
	if m.IsEmpty() {
		t.Fatal("migration produced nothing")
	}
	if len(m.Faces[0].Holes) != 1 {
		t.Fatalf("base face has %d holes, want 1", len(m.Faces[0].Holes))
	}
	// base + 4 sides + 1 cap
	if len(m.Faces) != 6 {
		t.Fatalf("migrated extrusion made %d faces, want 6", len(m.Faces))
	}
	cap := m.Faces[len(m.Faces)-1]
	for _, vi := range cap.Verts {
		if got := m.Points[vi].Depth; math.Abs(float64(got+0.5)) > 1e-5 {
			t.Errorf("cap depth = %v, want the extrusion's -0.5", got)
		}
	}
	verts, idx, _ := BuildSurfaceMesh(f, &m, false, nil)
	if len(idx) == 0 {
		t.Fatal("migrated extrusion built no triangles")
	}
	// The recess must actually leave the wall plane.
	off := false
	for _, p := range vertsAt(verts) {
		if math.Abs(float64(p.Z())) > 1e-3 {
			off = true
		}
	}
	if !off {
		t.Error("no vertex left the wall plane; the recess has no depth")
	}
}

func TestMigratedFloorExtrusionSinksAPit(t *testing.T) {
	// A floor Extrusion's positive Depth used to go *down*, while
	// TerrainPoint.Depth on the same surface went up. The migration reconciles
	// them onto the +Y convention.
	f := testFloorFrame()
	ext := Extrusion{
		Outer: []ExtrusionPoint{{U: 1, V: 1}, {U: 3, V: 1}, {U: 3, V: 3}, {U: 1, V: 3}},
		Inner: []ExtrusionPoint{{U: 1, V: 1}, {U: 3, V: 1}, {U: 3, V: 3}, {U: 1, V: 3}},
		Depth: 1, Visible: true, BackVisible: true, Alpha: 1,
	}
	m := MigrateSurfaceMesh(f, []Extrusion{ext}, nil, false)
	capFace := m.Faces[len(m.Faces)-1]
	for _, vi := range capFace.Verts {
		if got := m.Points[vi].Depth; got >= 0 {
			t.Errorf("floor pit cap depth = %v, want negative (below the floor)", got)
		}
	}
}

func TestMigratedTerrainKeepsItsTriangles(t *testing.T) {
	f := testFloorFrame()
	tp := []TerrainPoint{{U: 2, V: 2, Depth: 1}}
	m := MigrateSurfaceMesh(f, nil, tp, true)
	if len(m.Faces) == 0 {
		t.Fatal("terrain migration produced no faces")
	}
	for _, face := range m.Faces {
		if len(face.Verts) != 3 {
			t.Errorf("terrain face has %d corners, want a triangle", len(face.Verts))
		}
	}
	// The peak must survive as a free point at its own depth.
	found := false
	for _, p := range m.Points {
		if p.Boundary == SurfaceFree && math.Abs(float64(p.Depth-1)) < 1e-5 {
			found = true
		}
	}
	if !found {
		t.Error("the terrain point's depth did not survive migration")
	}
}

func TestCollisionMeshAgreesWithTheRenderMesh(t *testing.T) {
	f := testFloorFrame()
	m := SeedSurfaceMesh(f)
	m.SurfaceAddFacePoint(0, 2, 2)
	m.Points[len(m.Points)-1].Depth = 1.5

	_, idx, _ := BuildSurfaceMesh(f, &m, false, nil)
	pts, depths, tris := SurfaceCollisionMesh(f, &m, nil)
	if len(tris) != len(idx) {
		t.Fatalf("collision has %d indices, render has %d — they must cut the same polygons",
			len(tris), len(idx))
	}
	if len(pts) != len(depths) {
		t.Fatalf("collision points and depths disagree: %d vs %d", len(pts), len(depths))
	}
	peak := float32(0)
	for _, d := range depths {
		if d > peak {
			peak = d
		}
	}
	if math.Abs(float64(peak-1.5)) > 1e-5 {
		t.Errorf("collision mesh peak depth = %v, want the 1.5 the point was dragged to", peak)
	}
}

func TestWallMeshHasNoCollisionMesh(t *testing.T) {
	// The wall collision plane stays flat; a wall's mesh contributes windows,
	// not a walkable surface.
	f := testWallFrame()
	m := SeedSurfaceMesh(f)
	pts, _, tris := SurfaceCollisionMesh(f, &m, nil)
	if pts != nil || tris != nil {
		t.Error("a wall produced a collision mesh; wall collision is a segment test")
	}
}

func TestSyncBoundaryFollowsAMovedCorner(t *testing.T) {
	pts := []mgl32.Vec2{{0, 0}, {4, 0}, {4, 4}, {0, 4}}
	heights := []float32{0, 0, 0, 0}
	f := NewPlanarSurfaceFrame(pts, func(i int) float32 { return heights[i] }, 0, 1, true)
	m := SeedSurfaceMesh(f)

	// Drag a sector corner and raise it.
	pts[1] = mgl32.Vec2{6, 0}
	heights[1] = 2
	f2 := NewPlanarSurfaceFrame(pts, func(i int) float32 { return heights[i] }, 0, 1, true)
	SyncSurfaceBoundary(&m, f2)

	var moved *SurfacePoint
	for i := range m.Points {
		if m.Points[i].Boundary == 1 {
			moved = &m.Points[i]
		}
	}
	if moved == nil {
		t.Fatal("no point is bound to corner 1")
	}
	if moved.U != 6 || moved.Depth != 2 {
		t.Errorf("boundary point at (%v,%v) depth %v, want (6,0) depth 2",
			moved.U, moved.V, moved.Depth)
	}
}

func TestRemapByPositionSurvivesAnInsertion(t *testing.T) {
	old := []mgl32.Vec2{{0, 0}, {4, 0}, {4, 4}, {0, 4}}
	// A wall split inserts a corner at index 1; everything after shifts.
	fresh := []mgl32.Vec2{{0, 0}, {2, 0}, {4, 0}, {4, 4}, {0, 4}}
	r := RemapByPosition(old, fresh, 0.01)
	want := []int{0, 2, 3, 4}
	for i, w := range want {
		if r.Corners[i] != w {
			t.Errorf("old corner %d mapped to %d, want %d", i, r.Corners[i], w)
		}
	}
}

func TestRemapDemotesADeletedCorner(t *testing.T) {
	f := testFloorFrame()
	m := SeedSurfaceMesh(f)
	r := SurfaceRemap{Corners: []int{0, -1, 1, 2}, NewCount: 3}
	RemapSurfaceMesh(&m, r)
	for i, p := range m.Points {
		if i == 1 && p.Boundary != SurfaceFree {
			t.Errorf("point on the deleted corner still claims corner %d", p.Boundary)
		}
	}
	if m.Points[2].Boundary != 1 || m.Points[3].Boundary != 2 {
		t.Errorf("later corners not shifted down: got %d, %d", m.Points[2].Boundary, m.Points[3].Boundary)
	}
}

func TestInsertBoundaryCornerSplicesTheFaceEdge(t *testing.T) {
	f := testFloorFrame()
	m := SeedSurfaceMesh(f)
	// Insert a corner at index 1, between old corners 0 and 1.
	InsertSurfaceBoundaryCorner(&m, 0, 1, 1, mgl32.Vec2{2, 0}, 0)
	if len(m.Faces[0].Verts) != 5 {
		t.Fatalf("face ring has %d corners after the insert, want 5 — a T-junction was left behind",
			len(m.Faces[0].Verts))
	}
	// The new point must be bound to the corner it was inserted at, and the
	// old corner 1 must have shifted up to 2.
	found := map[int]bool{}
	for _, p := range m.Points {
		if p.Boundary >= 0 {
			found[p.Boundary] = true
		}
	}
	for i := 0; i < 5; i++ {
		if !found[i] {
			t.Errorf("no point bound to corner %d after the insert", i)
		}
	}
}

func TestRemoveBoundaryCornerDropsItFromTheRing(t *testing.T) {
	f := testFloorFrame()
	m := SeedSurfaceMesh(f)
	RemoveSurfaceBoundaryCorner(&m, 1)
	if len(m.Faces[0].Verts) != 3 {
		t.Fatalf("face ring has %d corners after the removal, want 3", len(m.Faces[0].Verts))
	}
	for _, p := range m.Points {
		if p.Boundary >= 3 {
			t.Errorf("a point still claims corner %d, past the shrunken outline", p.Boundary)
		}
	}
}

func TestInteriorShapeSurvivesAnOutlineChange(t *testing.T) {
	// The reason remapping exists rather than discarding: an alcove carved into
	// a wall must not vanish because the room's corner was then nudged.
	f := testFloorFrame()
	m := SeedSurfaceMesh(f)
	inset := m.SurfaceInsetFaces([]int{0}, 0.5)
	m.SurfaceExtrudeFaces(inset, -1)
	before := len(m.Faces)

	RemapSurfaceMesh(&m, SurfaceRemap{Corners: []int{0, 1, 2, 3}, NewCount: 4})
	if len(m.Faces) != before {
		t.Errorf("remap changed the face count from %d to %d", before, len(m.Faces))
	}
	deep := 0
	for _, p := range m.Points {
		if p.Depth < -0.5 {
			deep++
		}
	}
	if deep == 0 {
		t.Error("the carved shape's depth did not survive the remap")
	}
}

func TestOpeningIsCutExactlyFromAnUneditedFloor(t *testing.T) {
	f := testFloorFrame()
	var m SurfaceMesh
	op := []Opening{{ID: "o1", Points: []mgl32.Vec2{{1, 1}, {2, 1}, {2, 2}, {1, 2}}}}
	verts, idx, _ := BuildSurfaceMesh(f, &m, false, op)
	if len(idx) == 0 {
		t.Fatal("floor with an opening drew nothing")
	}
	// No triangle centroid may land inside the hole.
	for i := 0; i+2 < len(idx); i += 3 {
		a := mgl32.Vec2{verts[idx[i]][0], verts[idx[i]][2]}
		b := mgl32.Vec2{verts[idx[i+1]][0], verts[idx[i+1]][2]}
		c := mgl32.Vec2{verts[idx[i+2]][0], verts[idx[i+2]][2]}
		mid := a.Add(b).Add(c).Mul(1.0 / 3.0)
		if SurfaceIsOpenAt(op, mid) {
			t.Fatalf("triangle at %v was drawn inside the opening", mid)
		}
	}
}

func TestFaceTextureBecomesItsOwnSubmesh(t *testing.T) {
	f := testWallFrame()
	m := SeedSurfaceMesh(f)
	inset := m.SurfaceInsetFaces([]int{0}, 0.5)
	m.SurfaceExtrudeFaces(inset, -0.5)
	m.Faces[inset[0]].Texture = "metal"
	m.Faces[inset[0]].Alpha = 0.5

	_, _, subs := BuildSurfaceMesh(f, &m, false, nil)
	if len(subs) != 1 {
		t.Fatalf("got %d submeshes, want 1 for the one overriding texture", len(subs))
	}
	if subs[0].Texture != "metal" || subs[0].Alpha != 0.5 {
		t.Errorf("submesh = %q alpha %v, want metal / 0.5", subs[0].Texture, subs[0].Alpha)
	}
}

func TestDoubleSidedEmitsBothWindings(t *testing.T) {
	f := testWallFrame()
	var m SurfaceMesh
	_, single, _ := BuildSurfaceMesh(f, &m, false, nil)
	_, double, _ := BuildSurfaceMesh(f, &m, true, nil)
	if len(double) != 2*len(single) {
		t.Errorf("double-sided emitted %d indices, want twice the %d of single", len(double), len(single))
	}
}
