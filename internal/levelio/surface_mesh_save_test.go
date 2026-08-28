package levelio

// surface_mesh_save_test.go — surface meshes across the YAML round trip, and
// the one-way migration of the fields they replaced.
//
// The migration is the part worth testing hardest. Every level on disk is
// written in `extrusions:` and `terrain_points:`, and the whole claim of this
// change is that those keep working — that a doorway cut two years ago still
// loads as a doorway, and comes back out as a mesh nobody has to think about
// again.

import (
	"foundry/internal/geom"
	"foundry/internal/testenv"
	"foundry/internal/world"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// doorwayExtrusion is a rectangle in the middle of a wall, pushed inward — the
// shape almost every legacy extrusion in a level actually is.
func doorwayExtrusion() world.RawExtrusion {
	rect := []world.RawExtrusionPoint{{U: 3, V: 1}, {U: 7, V: 1}, {U: 7, V: 3}, {U: 3, V: 3}}
	return world.RawExtrusion{
		Outer:   rect,
		Inner:   rect,
		Depth:   -0.5,
		UVScale: geom.Point{X: 1, Y: 1},
	}
}

func TestFlatSurfacesWriteNoMesh(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	s := &ch.Sectors[0]
	if !s.Floor.Mesh.IsEmpty() || !s.Ceiling.Mesh.IsEmpty() {
		t.Error("a plain room loaded with a surface mesh; flat must stay flat")
	}
	for i := range s.Walls {
		if !s.Walls[i].Mesh.IsEmpty() {
			t.Errorf("wall %d loaded with a surface mesh", i)
		}
	}

	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	out, err := yaml.Marshal(back)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// `mesh:` is omitempty, so a level of plain rooms must serialise exactly as
	// it always did. This is what keeps the field free until somebody uses it.
	if strings.Contains(string(out), "mesh:") {
		t.Errorf("a flat chunk wrote a mesh key:\n%s", out)
	}
}

func TestLegacyWallExtrusionMigratesToAMesh(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	raw.Sectors[0].Edges[0].Extrusions = []world.RawExtrusion{doorwayExtrusion()}

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	w := &ch.Sectors[0].Walls[0]
	if w.Mesh.IsEmpty() {
		t.Fatal("the wall's extrusion did not become a mesh")
	}
	// Base face with one hole ring, four sides, one cap.
	if len(w.Mesh.Faces[0].Holes) != 1 {
		t.Errorf("base face has %d hole rings, want 1", len(w.Mesh.Faces[0].Holes))
	}
	if got := len(w.Mesh.Faces); got != 6 {
		t.Errorf("migrated doorway made %d faces, want 6 (base + 4 sides + cap)", got)
	}
	// The recess must have depth, or it migrated as a flat patch.
	deepest := float32(0)
	for _, p := range w.Mesh.Points {
		if p.Depth < deepest {
			deepest = p.Depth
		}
	}
	if deepest > -0.4 {
		t.Errorf("deepest migrated point is at %v, want about -0.5", deepest)
	}
}

func TestMigratedMeshIsWrittenBackAsAMeshAndNotAsExtrusions(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	raw.Sectors[0].Edges[0].Extrusions = []world.RawExtrusion{doorwayExtrusion()}

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	edge := back.Sectors[0].Edges[0]
	if len(edge.Extrusions) != 0 {
		t.Errorf("the writer emitted %d legacy extrusions; the migration is meant to be one-way",
			len(edge.Extrusions))
	}
	if edge.Mesh == nil {
		t.Fatal("the writer emitted no mesh for a shaped wall")
	}
	if len(edge.Mesh.Faces) != 6 {
		t.Errorf("wrote %d faces, want the 6 the migration produced", len(edge.Mesh.Faces))
	}
	if len(edge.Mesh.Points) != len(edge.Mesh.Boundary)*3 {
		t.Errorf("points (%d floats) and boundary (%d entries) are not parallel",
			len(edge.Mesh.Points), len(edge.Mesh.Boundary))
	}
}

func TestSurfaceMeshSurvivesTheRoundTrip(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	raw.Sectors[0].Edges[0].Extrusions = []world.RawExtrusion{doorwayExtrusion()}

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	before := ch.Sectors[0].Walls[0].Mesh.Clone()

	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	// Through actual YAML, not just the raw struct: the flow-style point and
	// index lists are the part most likely to come back a different shape.
	out, err := yaml.Marshal(back)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var reparsed world.RawChunk
	if err := yaml.Unmarshal(out, &reparsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ch2, _, err := loadRawChunk(reparsed)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	after := ch2.Sectors[0].Walls[0].Mesh

	if len(after.Points) != len(before.Points) || len(after.Faces) != len(before.Faces) {
		t.Fatalf("mesh changed shape across the round trip: %d/%d points, %d/%d faces",
			len(after.Points), len(before.Points), len(after.Faces), len(before.Faces))
	}
	for i := range before.Points {
		a, b := before.Points[i], after.Points[i]
		if a.U != b.U || a.V != b.V || a.Depth != b.Depth || a.Boundary != b.Boundary {
			t.Errorf("point %d became %+v, was %+v", i, b, a)
		}
	}
	for i := range before.Faces {
		a, b := before.Faces[i], after.Faces[i]
		if len(a.Verts) != len(b.Verts) || len(a.Holes) != len(b.Holes) || a.Hole != b.Hole {
			t.Errorf("face %d became %+v, was %+v", i, b, a)
		}
	}
}

func TestLegacyTerrainFloorMigratesAndKeepsItsHeight(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	raw.Sectors[0].Floor.Terrain = true
	raw.Sectors[0].Floor.TerrainPoints = []world.RawTerrainPoint{{U: 5, V: 5, Depth: 2}}

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	s := &ch.Sectors[0]
	if s.Floor.Mesh.IsEmpty() {
		t.Fatal("the terrain floor did not become a mesh")
	}
	// The peak has to survive, and it has to be something a body stands on:
	// the collision cache is filled from the same triangles that were drawn.
	if len(s.Floor.CollisionTris) == 0 {
		t.Error("a shaped floor produced no collision triangles")
	}
	h, _, ok := world.InterpolateSurfaceHeight(&ch, s, mgl32.Vec2{5, 5}, true)
	if !ok {
		t.Fatal("no height at the peak")
	}
	if h < 1.9 || h > 2.1 {
		t.Errorf("height at the terrain peak = %v, want about 2", h)
	}
}

func TestAWrittenMeshWinsOverLegacyKeys(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	// A file carrying both — one saved by a new build then hand-merged with an
	// old one. The mesh is the representation the author has been editing, so
	// it is the one that counts.
	raw := squareRawChunk(false)
	raw.Sectors[0].Edges[0].Extrusions = []world.RawExtrusion{doorwayExtrusion()}
	raw.Sectors[0].Edges[0].Mesh = &world.RawSurfaceMesh{
		Points:   []float32{0, 0, 0, 10, 0, 0, 10, 4, 0, 0, 4, 0},
		Boundary: []int{0, 1, 2, 3},
		Faces:    []world.RawSurfaceFace{{Verts: []int{0, 1, 2, 3}}},
	}

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	mesh := &ch.Sectors[0].Walls[0].Mesh
	if len(mesh.Faces) != 1 {
		t.Errorf("loaded %d faces, want the 1 the mesh key describes — the legacy extrusion won",
			len(mesh.Faces))
	}
}

func TestAMalformedFaceIsDroppedRatherThanFailingTheLoad(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	raw.Sectors[0].Floor.Mesh = &world.RawSurfaceMesh{
		Points:   []float32{0, 0, 0, 10, 0, 0, 10, 10, 0, 0, 10, 0},
		Boundary: []int{0, 1, 2, 3},
		Faces: []world.RawSurfaceFace{
			{Verts: []int{0, 1, 2, 3}},
			{Verts: []int{0, 1, 99}}, // indexes past the point pool
		},
	}

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("a level with one bad face refused to load entirely: %v", err)
	}
	if got := len(ch.Sectors[0].Floor.Mesh.Faces); got != 1 {
		t.Errorf("loaded %d faces, want 1 — the out-of-range one should be dropped", got)
	}
}
