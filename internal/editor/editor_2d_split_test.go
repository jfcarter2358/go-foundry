package editor

import (
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/leveltest"
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// makeSharedEdgeAPortal turns the A/B shared edge into a reciprocal portal pair.
func makeSharedEdgeAPortal(ch *world.Chunk) {
	a, b := &ch.Sectors[0], &ch.Sectors[1]

	a.EdgeKinds = []string{"wall", "portal", "wall", "wall"}
	a.Portals = []world.Portal{{
		StartPoint: ch.Points[1], EndPoint: ch.Points[2], ToSector: "B",
		DrawGap: true, GapTexture: "metal", Visible: true, DoubleSided: true, Alpha: 0.75,
	}}
	a.Walls = append(a.Walls[:1], a.Walls[2:]...)

	b.EdgeKinds = []string{"wall", "wall", "wall", "portal"}
	b.Portals = []world.Portal{{
		StartPoint: ch.Points[2], EndPoint: ch.Points[1], ToSector: "A",
		DrawGap: true, GapTexture: "metal", Visible: true, DoubleSided: true, Alpha: 0.75,
	}}
	b.Walls = b.Walls[:3]
}

// sectorEdges returns each sector edge as an ordered pair of chunk point indices.
func sectorEdges(s *world.Sector) [][2]int {
	n := len(s.PointIndices)
	edges := make([][2]int, n)
	for ei := 0; ei < n; ei++ {
		edges[ei] = [2]int{s.PointIndices[ei], s.PointIndices[(ei+1)%n]}
	}
	return edges
}

func TestPlanEdgeSplitSnapsPointOntoEdge(t *testing.T) {
	ch := leveltest.SplitChunk()

	// Click 0.5 units off the shared edge, 2 units up from point 1.
	plan, ok := planEdgeSplit(ch, 10.5, 2, 4)
	if !ok {
		t.Fatal("planEdgeSplit found no edge near the shared edge")
	}
	if want := (mgl32.Vec2{10, 2}); !geom.ApproxEq2(plan.newPt, want) {
		t.Errorf("new point = %v, want %v (snapped onto the edge)", plan.newPt, want)
	}
	if plan.si != 0 || plan.kind != "wall" || plan.kindIdx != 1 {
		t.Errorf("selection = sector %d %s[%d], want sector 0 wall[1]", plan.si, plan.kind, plan.kindIdx)
	}
}

func TestPlanEdgeSplitCoversBothSharingSectors(t *testing.T) {
	ch := leveltest.SplitChunk()

	plan, ok := planEdgeSplit(ch, 10.5, 2, 4)
	if !ok {
		t.Fatal("planEdgeSplit found no edge")
	}
	if len(plan.refs) != 2 {
		t.Fatalf("got %d refs, want 2 (both sectors sharing the edge): %+v", len(plan.refs), plan.refs)
	}

	// A walks the edge forwards (t=0.2 of 10 units); B walks it backwards, so
	// the same world point sits at 1-t along B's edge.
	want := map[int]edgeSplitRef{
		0: {si: 0, ei: 1, t: 0.2},
		1: {si: 1, ei: 3, t: 0.8},
	}
	for _, r := range plan.refs {
		w, okRef := want[r.si]
		if !okRef {
			t.Fatalf("unexpected ref for sector %d", r.si)
		}
		if r.ei != w.ei || absF(r.t-w.t) > 1e-4 {
			t.Errorf("sector %d ref = edge %d t=%.4f, want edge %d t=%.4f", r.si, r.ei, r.t, w.ei, w.t)
		}
	}
}

func TestPlanEdgeSplitRejectsBadClicks(t *testing.T) {
	cases := []struct {
		name      string
		wx, wz    float32
		threshold float32
	}{
		{"far from every edge", 5, 5, 1},
		{"on top of an existing vertex", 10, 0, 4},
		{"a hair past an existing vertex", 10, 0.001, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := leveltest.SplitChunk()
			if _, ok := planEdgeSplit(ch, tc.wx, tc.wz, tc.threshold); ok {
				t.Error("planEdgeSplit accepted the click, want rejection")
			}
		})
	}
}

func TestApplyEdgeSplitWallHalvesInheritProperties(t *testing.T) {
	ch := leveltest.SplitChunk()

	// Give the wall about to be split a thoroughly non-default set of properties.
	orig := world.Wall{
		StartPoint:  ch.Points[1],
		EndPoint:    ch.Points[2],
		Texture:     "rusted_metal",
		Collision:   false,
		Visible:     false,
		Pinned:      true,
		DoubleSided: false,
		Alpha:       0.42,
		UVOffset:    mgl32.Vec2{0.25, 0.5},
		UVScale:     mgl32.Vec2{2, 3},
		UVRotation:  1.25,
		Detail:      world.DetailSpec{Enabled: true},
	}
	ch.Sectors[0].Walls[1] = orig

	plan, ok := planEdgeSplit(ch, 10.5, 2, 4)
	if !ok {
		t.Fatal("planEdgeSplit found no edge")
	}
	newIdx := applyEdgeSplit(ch, plan)

	a := &ch.Sectors[0]
	if len(a.Walls) != 5 {
		t.Fatalf("sector A has %d walls, want 5", len(a.Walls))
	}
	first, second := a.Walls[1], a.Walls[2]

	// The split point ends both the first half and starts the second, so the
	// two halves together span exactly the original edge.
	if !geom.ApproxEq2(first.StartPoint, orig.StartPoint) || !geom.ApproxEq2(first.EndPoint, plan.newPt) {
		t.Errorf("first half = %v→%v, want %v→%v", first.StartPoint, first.EndPoint, orig.StartPoint, plan.newPt)
	}
	if !geom.ApproxEq2(second.StartPoint, plan.newPt) || !geom.ApproxEq2(second.EndPoint, orig.EndPoint) {
		t.Errorf("second half = %v→%v, want %v→%v", second.StartPoint, second.EndPoint, plan.newPt, orig.EndPoint)
	}

	for _, tc := range []struct {
		name string
		w    world.Wall
	}{{"first half", first}, {"second half", second}} {
		got, want := tc.w, orig
		// Endpoints and geometry legitimately differ; everything else must not.
		got.StartPoint, got.EndPoint = want.StartPoint, want.EndPoint
		got.Geometry, want.Geometry = gfx.Geometry{}, gfx.Geometry{}
		got.SubmeshGeometries, want.SubmeshGeometries = nil, nil
		if got.Texture != want.Texture || got.Collision != want.Collision ||
			got.Visible != want.Visible || got.Pinned != want.Pinned ||
			got.DoubleSided != want.DoubleSided ||
			got.Alpha != want.Alpha || got.UVOffset != want.UVOffset ||
			got.UVScale != want.UVScale || got.UVRotation != want.UVRotation ||
			got.Detail.Enabled != want.Detail.Enabled {
			t.Errorf("%s did not inherit the split wall's properties:\n got %+v\nwant %+v", tc.name, got, want)
		}
	}

	// Bookkeeping: the new vertex lands between 1 and 2, and edge kinds stay
	// index-aligned with the point indices.
	wantIndices := []int{0, 1, newIdx, 2, 3}
	if len(a.PointIndices) != len(wantIndices) {
		t.Fatalf("sector A point indices = %v, want %v", a.PointIndices, wantIndices)
	}
	for i, want := range wantIndices {
		if a.PointIndices[i] != want {
			t.Fatalf("sector A point indices = %v, want %v", a.PointIndices, wantIndices)
		}
	}
	if len(a.EdgeKinds) != len(a.PointIndices) {
		t.Errorf("sector A has %d edge kinds for %d points; must stay aligned", len(a.EdgeKinds), len(a.PointIndices))
	}
	if len(a.Floor.VertexOffsets) != len(a.PointIndices) || len(a.Ceiling.VertexOffsets) != len(a.PointIndices) {
		t.Errorf("vertex offsets = floor %d / ceiling %d, want %d each",
			len(a.Floor.VertexOffsets), len(a.Ceiling.VertexOffsets), len(a.PointIndices))
	}
}

func TestApplyEdgeSplitKeepsSharedPortalReciprocal(t *testing.T) {
	ch := leveltest.SplitChunk()
	makeSharedEdgeAPortal(ch)

	plan, ok := planEdgeSplit(ch, 10.5, 2, 4)
	if !ok {
		t.Fatal("planEdgeSplit found no edge")
	}
	applyEdgeSplit(ch, plan)

	a, b := &ch.Sectors[0], &ch.Sectors[1]
	if len(a.Portals) != 2 || len(b.Portals) != 2 {
		t.Fatalf("portals after split = A:%d B:%d, want 2 each", len(a.Portals), len(b.Portals))
	}
	for _, p := range append(append([]world.Portal{}, a.Portals...), b.Portals...) {
		if p.ToSector == "" || !p.DrawGap || p.GapTexture != "metal" || !p.Visible ||
			!p.DoubleSided || p.Alpha != 0.75 {
			t.Errorf("portal half lost properties: %+v", p)
		}
	}

	// InferPortals only keeps a portal while the other sector still has the
	// exact reverse edge, so every portal edge of A must be mirrored in B.
	bEdges := map[[2]int]bool{}
	for _, e := range sectorEdges(b) {
		bEdges[e] = true
	}
	for ei, e := range sectorEdges(a) {
		if ei >= len(a.EdgeKinds) || a.EdgeKinds[ei] != "portal" {
			continue
		}
		if !bEdges[[2]int{e[1], e[0]}] {
			t.Errorf("A portal edge %v has no reverse edge in B (edges: %v)", e, sectorEdges(b))
		}
	}
}

func TestApplyEdgeSplitOnUnsharedEdgeTouchesOneSector(t *testing.T) {
	ch := leveltest.SplitChunk()

	// Sector A's edge 0 runs (0,0)→(10,0) and belongs to A alone.
	plan, ok := planEdgeSplit(ch, 4, 0.3, 4)
	if !ok {
		t.Fatal("planEdgeSplit found no edge")
	}
	if len(plan.refs) != 1 || plan.refs[0].si != 0 {
		t.Fatalf("refs = %+v, want a single ref for sector 0", plan.refs)
	}
	applyEdgeSplit(ch, plan)

	if got := len(ch.Sectors[0].PointIndices); got != 5 {
		t.Errorf("sector A has %d points, want 5", got)
	}
	if got := len(ch.Sectors[1].PointIndices); got != 4 {
		t.Errorf("sector B has %d points, want 4 (untouched)", got)
	}
}

func TestInsertSectorVertexLerpsVertexOffsets(t *testing.T) {
	ch := leveltest.SplitChunk()
	// Slope the floor along edge 1: vertex 1 sits at 0, vertex 2 at 4.
	ch.Sectors[0].Floor.VertexOffsets = []float32{0, 0, 4, 0}

	plan, ok := planEdgeSplit(ch, 10, 2.5, 4)
	if !ok {
		t.Fatal("planEdgeSplit found no edge")
	}
	applyEdgeSplit(ch, plan)

	// The split sits 25% along the edge, so its offset must land on the slope
	// (0 + 0.25*4 = 1) or the floor surface would kink at the new vertex.
	got := ch.Sectors[0].Floor.VertexOffsets
	if len(got) != 5 {
		t.Fatalf("floor offsets = %v, want 5 entries", got)
	}
	if absF(got[2]-1) > 1e-4 {
		t.Errorf("new vertex floor offset = %v (all: %v), want 1", got[2], got)
	}
}

// wallMeshWithPatches builds a wall mesh carrying one square face per U range,
// so a split can be checked by which faces landed on which side.
func wallMeshWithPatches(ranges [][2]float32) world.SurfaceMesh {
	var m world.SurfaceMesh
	for i, r := range ranges {
		a := m.AddPoint(r[0], 1, 0)
		b := m.AddPoint(r[1], 1, 0)
		c := m.AddPoint(r[1], 2, 0)
		d := m.AddPoint(r[0], 2, 0)
		m.Faces = append(m.Faces, world.SurfaceFace{
			Verts:   []int{a, b, c, d},
			Texture: []string{"left", "right", "straddle"}[i%3],
		})
	}
	return m
}

// faceURange is the U extent of one face of a mesh.
func faceURange(m *world.SurfaceMesh, fi int) (float32, float32) {
	f := m.Faces[fi]
	minU, maxU := m.Points[f.Verts[0]].U, m.Points[f.Verts[0]].U
	for _, v := range f.Verts[1:] {
		u := m.Points[v].U
		if u < minU {
			minU = u
		}
		if u > maxU {
			maxU = u
		}
	}
	return minU, maxU
}

func TestSplitWallDividesItsMeshAtTheSplitPoint(t *testing.T) {
	w := world.Wall{
		StartPoint: mgl32.Vec2{0, 0},
		EndPoint:   mgl32.Vec2{10, 0},
		Mesh:       wallMeshWithPatches([][2]float32{{0.5, 1.5}, {5, 6}}),
	}

	// Split 3 units along a 10-unit wall.
	first, second := splitWall(w, mgl32.Vec2{3, 0}, 0.3)

	if len(first.Mesh.Faces) != 1 {
		t.Fatalf("first half has %d faces, want the one left of the seam", len(first.Mesh.Faces))
	}
	if len(second.Mesh.Faces) != 1 {
		t.Fatalf("second half has %d faces, want the one right of the seam", len(second.Mesh.Faces))
	}
	if got := first.Mesh.Faces[0].Texture; got != "left" {
		t.Errorf("first half kept the %q face, want left", got)
	}
	if got := second.Mesh.Faces[0].Texture; got != "right" {
		t.Errorf("second half kept the %q face, want right", got)
	}

	// A wall's U is measured from its own start point, so the first half keeps
	// its coordinates and the second half's rebase to the seam.
	if lo, hi := faceURange(&first.Mesh, 0); absF(lo-0.5) > 1e-4 || absF(hi-1.5) > 1e-4 {
		t.Errorf("first half face U range = [%v,%v], want [0.5,1.5]", lo, hi)
	}
	if lo, hi := faceURange(&second.Mesh, 0); absF(lo-2) > 1e-4 || absF(hi-3) > 1e-4 {
		t.Errorf("second half face U range = [%v,%v], want [2,3] (rebased to the seam)", lo, hi)
	}
}

func TestSplitWallSendsAStraddlingFaceWhole(t *testing.T) {
	// A shape crossing the seam goes entirely to the side its centre is on.
	// Slicing it would leave two half-doorways in two meshes, neither of which
	// could be dragged as one — see SplitWallSurfaceMesh.
	w := world.Wall{
		StartPoint: mgl32.Vec2{0, 0},
		EndPoint:   mgl32.Vec2{10, 0},
		Mesh:       wallMeshWithPatches([][2]float32{{2, 4}}),
	}
	first, second := splitWall(w, mgl32.Vec2{3, 0}, 0.3)

	total := len(first.Mesh.Faces) + len(second.Mesh.Faces)
	if total != 1 {
		t.Fatalf("the straddling face ended up as %d faces, want exactly 1", total)
	}
	// Centroid U is 3, exactly on the seam, so it goes to the first half.
	if len(first.Mesh.Faces) != 1 {
		t.Error("a face centred on the seam should go to the first half")
	}
}

func TestSplitWallOfAFlatWallStaysFlat(t *testing.T) {
	// A wall nobody has shaped has no mesh at all, and splitting it must not
	// invent one — that is what keeps a level of plain rooms free of meshes
	// that say nothing.
	w := world.Wall{StartPoint: mgl32.Vec2{0, 0}, EndPoint: mgl32.Vec2{10, 0}}
	first, second := splitWall(w, mgl32.Vec2{3, 0}, 0.3)
	if !first.Mesh.IsEmpty() || !second.Mesh.IsEmpty() {
		t.Error("splitting a flat wall produced a mesh")
	}
}

func TestSplitPortalHalvesShareTargetAndSettings(t *testing.T) {
	p := world.Portal{
		StartPoint: mgl32.Vec2{0, 0},
		EndPoint:   mgl32.Vec2{10, 0},
		ToSector:   "hallway",
		DrawGap:    true,
		GapTexture: "trim",
		Visible:    true,
		Alpha:      0.5,
	}
	mid := mgl32.Vec2{4, 0}

	first, second := splitPortal(p, mid)
	if !geom.ApproxEq2(first.EndPoint, mid) || !geom.ApproxEq2(second.StartPoint, mid) {
		t.Errorf("halves do not meet at %v: %v→%v and %v→%v",
			mid, first.StartPoint, first.EndPoint, second.StartPoint, second.EndPoint)
	}
	for name, got := range map[string]world.Portal{"first": first, "second": second} {
		if got.ToSector != p.ToSector || got.DrawGap != p.DrawGap ||
			got.GapTexture != p.GapTexture || got.Visible != p.Visible || got.Alpha != p.Alpha {
			t.Errorf("%s half lost portal settings: %+v", name, got)
		}
	}
}

func TestSegmentParam2D(t *testing.T) {
	cases := []struct {
		name           string
		px, py         float32
		ax, ay, bx, by float32
		want           float32
	}{
		{"midpoint", 5, 3, 0, 0, 10, 0, 0.5},
		{"clamped before start", -4, 0, 0, 0, 10, 0, 0},
		{"clamped past end", 14, 0, 0, 0, 10, 0, 1},
		{"diagonal projection", 2, 0, 0, 0, 4, 4, 0.25},
		{"degenerate segment", 3, 3, 2, 2, 2, 2, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := segmentParam2D(tc.px, tc.py, tc.ax, tc.ay, tc.bx, tc.by)
			if absF(got-tc.want) > 1e-4 {
				t.Errorf("segmentParam2D = %v, want %v", got, tc.want)
			}
		})
	}
}

func absF(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
