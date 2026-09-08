package world

// slab_test.go — the thickness a floor or a ceiling has (slab.go).

import (
	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// slabRoom is a 10x10 room, floor at 0 and ceiling at 4, whose four edges are
// all walls. Individual tests turn edges into portals or give a surface a
// thickness.
func slabRoom() (*Chunk, *Sector) {
	ch := &Chunk{
		ID:         "room",
		Points:     []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}},
		PointNames: make([]string, 4),
		Sectors: []Sector{{
			ID:           "hall",
			PointIndices: []int{0, 1, 2, 3},
			EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
			Floor:        Floor{Enabled: true, Height: 0, VertexOffsets: []float32{0, 0, 0, 0}, Alpha: 1},
			Ceiling:      Ceiling{Enabled: true, Height: 4, VertexOffsets: []float32{0, 0, 0, 0}, Alpha: 1},
			MinBound:     mgl32.Vec2{0, 0},
			MaxBound:     mgl32.Vec2{10, 10},
		}},
	}
	ch.RecomputeBounds()
	return ch, &ch.Sectors[0]
}

// ySpan is the lowest and highest Y in a geometry's vertex buffer, which is
// de-indexed [X,Y,Z,U,V] records.
func ySpan(verts []float32) (lo, hi float32, ok bool) {
	if len(verts) < 5 {
		return 0, 0, false
	}
	lo, hi = float32(math.MaxFloat32), -float32(math.MaxFloat32)
	for i := 1; i+3 < len(verts); i += 5 {
		if verts[i] < lo {
			lo = verts[i]
		}
		if verts[i] > hi {
			hi = verts[i]
		}
	}
	return lo, hi, true
}

func loadRoom(t *testing.T, ch *Chunk, s *Sector) {
	t.Helper()
	if err := s.Load(SectorPoints(ch, s)); err != nil {
		t.Fatalf("Sector.Load: %v", err)
	}
}

// The default has to stay the plane every existing level is, or turning the
// feature on would be a change to files nobody edited.
func TestNoSlabByDefault(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	loadRoom(t, ch, s)

	if SlabDrawable(&s.Floor.Slab) {
		t.Error("a floor with no thickness produced slab geometry")
	}
	if SlabDrawable(&s.Ceiling.Slab) {
		t.Error("a ceiling with no thickness produced slab geometry")
	}
	if SlabToRaw(s.Floor.Slab) != nil {
		t.Error("a floor with no thickness wrote a slab block to the level file")
	}
}

// The far face hangs below a floor by exactly the thickness, and above a
// ceiling by exactly the thickness. The sign is the whole difference between
// the two, so it is worth pinning on both.
func TestSlabFarFaceSitsAtTheThickness(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.Floor.Slab.Thickness = 0.25
	s.Ceiling.Slab.Thickness = 0.5
	loadRoom(t, ch, s)

	lo, hi, ok := ySpan(s.Floor.Slab.Geometry.Vertices)
	if !ok {
		t.Fatal("no floor slab geometry was produced")
	}
	if lo != -0.25 || hi != -0.25 {
		t.Errorf("floor underside spans y=%v..%v, want a flat plane at -0.25", lo, hi)
	}

	lo, hi, ok = ySpan(s.Ceiling.Slab.Geometry.Vertices)
	if !ok {
		t.Fatal("no ceiling slab geometry was produced")
	}
	if lo != 4.5 || hi != 4.5 {
		t.Errorf("ceiling top side spans y=%v..%v, want a flat plane at 4.5", lo, hi)
	}
}

// The rim closes the band between the two faces. Four wall edges, two triangles
// each.
func TestSlabRimClosesTheBand(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.Floor.Slab.Thickness = 0.25
	loadRoom(t, ch, s)

	lo, hi, ok := ySpan(s.Floor.Slab.RimGeometry.Vertices)
	if !ok {
		t.Fatal("no rim geometry was produced")
	}
	if lo != -0.25 || hi != 0 {
		t.Errorf("rim spans y=%v..%v, want the slab's own band -0.25..0", lo, hi)
	}
	// Five floats per de-indexed corner, three corners per triangle.
	if tris := len(s.Floor.Slab.RimGeometry.Vertices) / 15; tris != 8 {
		t.Errorf("%d rim triangles, want 8 (two per outline edge)", tris)
	}
}

// Across a portal the slab continues into the sector next door, whose own rim
// would be in exactly the same place. Drawing both is a z-fighting seam down
// every doorway.
func TestSlabRimSkipsPortalEdges(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.EdgeKinds[1] = "portal"
	s.Floor.Slab.Thickness = 0.25
	loadRoom(t, ch, s)

	if tris := len(s.Floor.Slab.RimGeometry.Vertices) / 15; tris != 6 {
		t.Errorf("%d rim triangles with one edge a portal, want 6", tris)
	}
}

// RimAcrossPortals is the author's override: with it on, a portal edge gets a
// rim quad like any other, even though the slab continues into the sector
// next door there.
func TestSlabRimAcrossPortalsIncludesPortalEdges(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.EdgeKinds[1] = "portal"
	s.Floor.Slab.Thickness = 0.25
	s.Floor.Slab.RimAcrossPortals = true
	loadRoom(t, ch, s)

	if tris := len(s.Floor.Slab.RimGeometry.Vertices) / 15; tris != 8 {
		t.Errorf("%d rim triangles with RimAcrossPortals on, want 8 (all four edges)", tris)
	}
}

// A slab follows the shape of the surface it hangs off rather than its base
// plane: that is what makes it a slab and not a second floor.
func TestSlabFollowsPerVertexOffsets(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.Floor.VertexOffsets = []float32{0, 0, 2, 0} // one corner raised
	s.Floor.Slab.Thickness = 0.25
	loadRoom(t, ch, s)

	lo, hi, ok := ySpan(s.Floor.Slab.Geometry.Vertices)
	if !ok {
		t.Fatal("no floor slab geometry was produced")
	}
	if lo != -0.25 || hi != 1.75 {
		t.Errorf("underside of a sloped floor spans y=%v..%v, want -0.25..1.75 — "+
			"the floor's own range dropped by the thickness", lo, hi)
	}
}

// Building the far face runs the shared mesh through a second frame. That frame
// keeps the surface's winding precisely so SyncSurfaceBoundary cannot renumber
// the corners of the mesh the two are using; if it ever stops doing so, a
// shaped floor is silently scrambled by having a thickness.
func TestSlabLeavesTheSharedMeshAlone(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.Floor.Mesh = SeedSurfaceMesh(s.FloorFrame(SectorPoints(ch, s)))
	before := s.Floor.Mesh.Clone()

	s.Floor.Slab.Thickness = 0.25
	loadRoom(t, ch, s)

	if len(s.Floor.Mesh.Points) != len(before.Points) {
		t.Fatalf("mesh point count changed from %d to %d",
			len(before.Points), len(s.Floor.Mesh.Points))
	}
	for i, p := range s.Floor.Mesh.Points {
		if p != before.Points[i] {
			t.Errorf("mesh point %d moved from %+v to %+v while its slab was built",
				i, before.Points[i], p)
		}
	}
}

// firstTriangleNormal is the geometric normal of a de-indexed buffer's first
// triangle — which is to say which way that face is wound, since a Geometry's
// vertices are laid out in index order.
func firstTriangleNormal(t *testing.T, verts []float32) mgl32.Vec3 {
	t.Helper()
	if len(verts) < 15 {
		t.Fatalf("expected at least one triangle, got %d floats", len(verts))
	}
	a := mgl32.Vec3{verts[0], verts[1], verts[2]}
	b := mgl32.Vec3{verts[5], verts[6], verts[7]}
	c := mgl32.Vec3{verts[10], verts[11], verts[12]}
	return b.Sub(a).Cross(c.Sub(a))
}

// The far face looks away from the surface it hangs off. Reversing the winding
// is how it gets there (surfaceBuild.Reverse), and a single-sided slab wound
// the other way round is invisible from the only side anybody sees it from.
//
// Stated against the floor's own facing rather than against world -Y, because
// this is a claim about the two being back to back — which is what it means for
// them to be the two faces of one solid — and not about which way round the
// engine happens to wind a floor.
func TestSlabFarFaceLooksAwayFromItsSurface(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.Floor.Slab.Thickness = 0.25
	loadRoom(t, ch, s)

	front := firstTriangleNormal(t, s.Floor.Geometry.Vertices)
	back := firstTriangleNormal(t, s.Floor.Slab.Geometry.Vertices)
	if front.Dot(back) >= 0 {
		t.Errorf("floor faces %v and its underside faces %v; the two must be back to back",
			front, back)
	}
}

// Thinning a slab back to nothing has to release it, not leave the last build
// on screen — a floor whose thickness a script animates to zero is the case.
func TestSlabThinnedToNothingIsReleased(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.Floor.Slab.Thickness = 0.25
	loadRoom(t, ch, s)
	if !SlabDrawable(&s.Floor.Slab) {
		t.Fatal("no slab geometry to release")
	}

	s.Floor.Slab.Thickness = 0
	loadRoom(t, ch, s)
	if SlabDrawable(&s.Floor.Slab) {
		t.Error("a slab thinned back to zero still has drawable geometry")
	}
}

// The sector's bounds have to cover the slab, or the sector is culled away
// while part of it is on screen — and, worse, while it is still casting, since
// the shadow pass culls against this same box.
func TestLocalAABBCoversTheSlab(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	_, s := slabRoom()
	s.Floor.Slab.Thickness = 0.25
	s.Ceiling.Slab.Thickness = 0.5

	lo, hi := s.LocalAABB()
	if lo.Y() > -0.25 {
		t.Errorf("bounds bottom at y=%v, want at or below the floor slab at -0.25", lo.Y())
	}
	if hi.Y() < 4.5 {
		t.Errorf("bounds top at y=%v, want at or above the ceiling slab at 4.5", hi.Y())
	}
}

// A slab's textures are its own, and "" is inheritance rather than a missing
// texture — so a slab that has only been switched on already matches its floor.
func TestSlabTextureInheritsFromItsSurface(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.Floor.Texture = "stone_floor"
	s.Floor.Slab.Thickness = 0.25
	loadRoom(t, ch, s)

	if got := s.Floor.Slab.Geometry.Texture; got != "stone_floor" {
		t.Errorf("underside texture %q, want the floor's own %q", got, "stone_floor")
	}
	if got := s.Floor.Slab.RimGeometry.Texture; got != "stone_floor" {
		t.Errorf("rim texture %q, want the floor's own %q", got, "stone_floor")
	}

	s.Floor.Slab.Texture = "concrete"
	loadRoom(t, ch, s)
	if got := s.Floor.Slab.Geometry.Texture; got != "concrete" {
		t.Errorf("underside texture %q, want its own %q", got, "concrete")
	}
	if got := s.Floor.Slab.RimGeometry.Texture; got != "concrete" {
		t.Errorf("rim texture %q, want to fall through to the slab's %q", got, "concrete")
	}
}

// Clone drops derived geometry the way it does for skirts and gap fill: a
// cloned chunk that kept the handles would be a second struct owning buffers it
// did not allocate.
func TestCloneDropsDerivedSlabGeometry(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.Floor.Slab.Thickness = 0.25
	s.Ceiling.Slab.Thickness = 0.25
	loadRoom(t, ch, s)

	cp := ch.Clone("copy", "_2")
	if len(cp.Sectors[0].Floor.Slab.Geometry.Vertices) != 0 ||
		len(cp.Sectors[0].Floor.Slab.RimGeometry.Vertices) != 0 {
		t.Error("clone carried the floor slab's derived geometry across")
	}
	if len(cp.Sectors[0].Ceiling.Slab.Geometry.Vertices) != 0 {
		t.Error("clone carried the ceiling slab's derived geometry across")
	}
	if cp.Sectors[0].Floor.Slab.Thickness != 0.25 {
		t.Error("clone dropped the thickness, which is authored rather than derived")
	}
}

// The seam between "built" and "drawn": every pass that walks a chunk's
// geometry goes through ChunkDrawGeometries or a loop written against the same
// list, so a slab missing from it is a slab that compiles and never appears.
func TestSlabIsInTheChunkDrawSet(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.Floor.Slab.Thickness = 0.25
	loadRoom(t, ch, s)

	inSet := func(g *gfx.Geometry) bool {
		for _, d := range ChunkDrawGeometries(ch) {
			if d == g {
				return true
			}
		}
		return false
	}
	if !inSet(&s.Floor.Slab.Geometry) {
		t.Error("the floor's underside is not in the chunk's draw set")
	}
	if !inSet(&s.Floor.Slab.RimGeometry) {
		t.Error("the floor slab's rim is not in the chunk's draw set")
	}

	// Hidden drops the far face and keeps the rim — the split every pass makes.
	s.Floor.Slab.Hidden = true
	loadRoom(t, ch, s)
	if inSet(&s.Floor.Slab.Geometry) {
		t.Error("a hidden underside is still drawn")
	}
	if !inSet(&s.Floor.Slab.RimGeometry) {
		t.Error("hiding the underside took its rim with it")
	}
}

// A chunk switched off releases its buffers (chunk_enable.go). A slab left
// holding a VAO there is a leak that streaming would repeat every time the
// chunk came back.
func TestReleaseGeometryFreesTheSlab(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.Floor.Slab.Thickness = 0.25
	s.Ceiling.Slab.Thickness = 0.25
	loadRoom(t, ch, s)

	ch.ReleaseGeometry()
	if SlabDrawable(&s.Floor.Slab) || SlabDrawable(&s.Ceiling.Slab) {
		t.Error("a released chunk still holds slab geometry")
	}
}

// ---------- an independently shaped far face ----------

// The default is the mirror, and that is what makes a slab cheap: shaping a
// floor shapes its underside with it, and neither the level file nor the author
// has to say so.
func TestUnshapedSlabMirrorsItsSurfacesMesh(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.Floor.Slab.Thickness = 0.25

	if got := s.Floor.Slab.ShapeMesh(&s.Floor.Mesh); got != &s.Floor.Mesh {
		t.Error("an unshaped slab does not build from its surface's mesh")
	}
	if s.Floor.Slab.Shaped() {
		t.Error("a slab with no mesh of its own reports as shaped")
	}

	s.Floor.Slab.Mesh = SeedSurfaceMesh(s.FloorSlabFrame(SectorPoints(ch, s)))
	if got := s.Floor.Slab.ShapeMesh(&s.Floor.Mesh); got != &s.Floor.Slab.Mesh {
		t.Error("a shaped slab still builds from its surface's mesh")
	}
	if !s.Floor.Slab.Shaped() {
		t.Error("a slab with a mesh of its own does not report as shaped")
	}
}

// Shaping the far face has to start from what was on screen — the mirror —
// rather than from a flat ring, or entering shaping mode on a carved floor's
// underside reads as the editor throwing the carve away.
func TestSlabShapingSeedsFromTheMirror(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.Floor.Slab.Thickness = 0.25
	pts := SectorPoints(ch, s)

	// A floor with an interior point pushed down: a dimple nothing else has.
	s.Floor.Mesh = SeedSurfaceMesh(s.FloorFrame(pts))
	s.Floor.Mesh.Points = append(s.Floor.Mesh.Points,
		SurfacePoint{U: 5, V: 5, Depth: -1, Boundary: SurfaceFree})

	frame := s.FloorSlabFrame(pts)
	seeded := s.SeedSurfaceMeshFor("floor_slab", -1, frame)
	if len(seeded.Points) != len(s.Floor.Mesh.Points) {
		t.Fatalf("seeded %d points from a %d-point floor mesh; the shape was not carried",
			len(seeded.Points), len(s.Floor.Mesh.Points))
	}
	last := seeded.Points[len(seeded.Points)-1]
	if last.Depth != -1 || last.Boundary != SurfaceFree {
		t.Errorf("the floor's interior point came across as %+v", last)
	}
	// A deep copy, or shaping the underside would edit the floor.
	seeded.Points[0].Depth = 42
	if s.Floor.Mesh.Points[0].Depth == 42 {
		t.Error("the seed shares its point pool with the floor's mesh")
	}

	// A flat surface has nothing to carry, so its slab starts from the outline.
	s.Floor.Mesh = SurfaceMesh{}
	if got := s.SeedSurfaceMeshFor("floor_slab", -1, frame); len(got.Points) != len(pts) {
		t.Errorf("a flat floor's underside seeded %d points, want the %d-corner outline",
			len(got.Points), len(pts))
	}
}

// Once shaped, the far face is its own: carving the underside must not move the
// floor above it, which is the whole reason for the field.
func TestShapedSlabDoesNotFollowTheSurface(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.Floor.Slab.Thickness = 0.25
	pts := SectorPoints(ch, s)

	mesh := SeedSurfaceMesh(s.FloorSlabFrame(pts))
	mesh.Points = append(mesh.Points, SurfacePoint{U: 5, V: 5, Depth: -2, Boundary: SurfaceFree})
	hub := len(mesh.Points) - 1
	ring := mesh.Faces[0].Verts
	mesh.Faces = nil
	for i := range ring {
		mesh.Faces = append(mesh.Faces,
			SurfaceFace{Verts: []int{ring[i], ring[(i+1)%len(ring)], hub}, Alpha: 1})
	}
	s.Floor.Slab.Mesh = mesh
	loadRoom(t, ch, s)

	lo, _, ok := ySpan(s.Floor.Slab.Geometry.Vertices)
	if !ok {
		t.Fatal("no slab geometry was produced")
	}
	if lo != -2.25 {
		t.Errorf("shaped underside reaches y=%v, want -2.25 (base -0.25, dimple -2)", lo)
	}
	if flo, fhi, _ := ySpan(s.Floor.Geometry.Vertices); flo != 0 || fhi != 0 {
		t.Errorf("carving the underside moved the floor: it now spans %v..%v", flo, fhi)
	}
	// Bounds have to cover the carve, or the sector is culled with part of it
	// on screen.
	if min, _ := s.LocalAABB(); min.Y() > -2.25 {
		t.Errorf("bounds bottom at %v, want at or below the shaped underside at -2.25", min.Y())
	}
}

// A shaped far face still meets the rim at the outline: boundary points belong
// to the sector, not to the mesh, so shaping is free in the interior and pinned
// at the edge. Without that the rim would hang in the air.
func TestShapedSlabStillMeetsItsRim(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.Floor.Slab.Thickness = 0.25
	pts := SectorPoints(ch, s)
	mesh := SeedSurfaceMesh(s.FloorSlabFrame(pts))
	for i := range mesh.Points {
		mesh.Points[i].Depth = -3 // try to drag the boundary down with it
	}
	s.Floor.Slab.Mesh = mesh
	loadRoom(t, ch, s)

	lo, hi, ok := ySpan(s.Floor.Slab.RimGeometry.Vertices)
	if !ok {
		t.Fatal("no rim geometry was produced")
	}
	if lo != -0.25 || hi != 0 {
		t.Errorf("rim spans y=%v..%v, want the band -0.25..0 the outline pins it to", lo, hi)
	}
}

// The mesh is authored data, so it has to survive everything the numbers do.
func TestShapedSlabMeshRoundTripsAndClones(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, s := slabRoom()
	s.Floor.Slab.Thickness = 0.25
	s.Floor.Slab.Mesh = SeedSurfaceMesh(s.FloorSlabFrame(SectorPoints(ch, s)))
	n := len(s.Floor.Slab.Mesh.Points)

	back := SlabFromRaw(SlabToRaw(s.Floor.Slab))
	if len(back.Mesh.Points) != n {
		t.Errorf("round trip left %d points, want %d", len(back.Mesh.Points), n)
	}

	cp := ch.Clone("copy", "_2")
	cpMesh := &cp.Sectors[0].Floor.Slab.Mesh
	if len(cpMesh.Points) != n {
		t.Fatalf("clone left %d points, want %d", len(cpMesh.Points), n)
	}
	cpMesh.Points[0].Depth = 99
	if s.Floor.Slab.Mesh.Points[0].Depth == 99 {
		t.Error("clone shares its slab mesh's point pool with the original")
	}
}
