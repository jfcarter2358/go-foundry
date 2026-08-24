package main

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// quadVerts builds a [X,Y,Z,U,V] triangle-list buffer for an axis-aligned quad
// in the XY plane at the given z, spanning [-half, half] on both axes.
func quadVerts(z, half float32) []float32 {
	a := [3]float32{-half, -half, z}
	b := [3]float32{half, -half, z}
	c := [3]float32{half, half, z}
	d := [3]float32{-half, half, z}
	var out []float32
	for _, tri := range [][3][3]float32{{a, b, c}, {a, c, d}} {
		for _, v := range tri {
			out = append(out, v[0], v[1], v[2], 0, 0)
		}
	}
	return out
}

// setupPickCamera points the camera down -Z from the origin and returns the
// centre pixel, which therefore casts a ray straight down -Z.
func setupPickCamera() (int32, int32) {
	WinWidth, WinHeight = 800, 600
	Projection = mgl32.Perspective(mgl32.DegToRad(45.0), float32(WinWidth)/float32(WinHeight), 0.1, 1000.0)
	Cam.Position = mgl32.Vec3{0, 0, 0}
	Cam.Direction = mgl32.Vec3{0, 0, -1}
	Cam.Matrix = mgl32.LookAtV(Cam.Position, mgl32.Vec3{0, 0, -1}, mgl32.Vec3{0, 1, 0})
	return WinWidth / 2, WinHeight / 2
}

func TestPickGeometryTReturnsNearestTriangle(t *testing.T) {
	origin := mgl32.Vec3{0, 0, 0}
	dir := mgl32.Vec3{0, 0, -1}

	verts := append(quadVerts(-10, 2), quadVerts(-3, 2)...)
	got := pickGeometryT(origin, dir, verts)
	if math.Abs(float64(got-3)) > 1e-3 {
		t.Fatalf("pickGeometryT = %v, want ~3 (the nearer quad)", got)
	}

	miss := pickGeometryT(origin, mgl32.Vec3{0, 0, 1}, verts)
	if miss != float32(math.MaxFloat32) {
		t.Fatalf("ray pointing away hit something: %v", miss)
	}
}

func TestPickModelTMatchesWorldDistance(t *testing.T) {
	// A unit quad at the model origin, scaled 2× and pushed 5 units down -Z:
	// its front face ends up at z = -5, so the hit distance must be 5.
	model := mgl32.Translate3D(0, 0, -5).Mul4(mgl32.Scale3D(2, 2, 2))
	got := pickModelT(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0, -1}, quadVerts(0, 0.5), model)
	if math.Abs(float64(got-5)) > 1e-3 {
		t.Fatalf("pickModelT = %v, want ~5", got)
	}
}

func TestPickModelTDegenerateMatrix(t *testing.T) {
	got := pickModelT(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0, -1}, quadVerts(0, 1), mgl32.Mat4{})
	if got != float32(math.MaxFloat32) {
		t.Fatalf("non-invertible model matrix should miss, got %v", got)
	}
}

// TestPick3DClosestWins covers the rule that a click selects whatever is
// nearest along the ray, in both orders: a wall in front of the spawn marker
// and the marker in front of a wall.
func TestPick3DClosestWins(t *testing.T) {
	mx, my := setupPickCamera()

	newLevel := func(wallZ, spawnZ float32) *Level {
		wall := Wall{Visible: true}
		wall.Geometry.Vertices = quadVerts(wallZ, 4)
		return &Level{
			Chunks: []Chunk{{
				ID: "c0",
				Sectors: []Sector{{
					ID:    "s0",
					Walls: []Wall{wall},
				}},
			}},
			// No uploaded mesh, so picking falls back to the marker's AABB —
			// which is the case that matters here, since the arrow itself is
			// only generated against a GL context.
			Spawn: &SpawnPoint{Position: mgl32.Vec3{0, 0, spawnZ}},
		}
	}

	prevLevel := Editor.level
	defer func() { Editor.level = prevLevel }()

	// Wall at 3, spawn marker at 10 → the wall wins.
	Editor.level = newLevel(-3, -10)
	if hit := pick3D(mx, my); hit.kind != pickWall || hit.idx != 0 || hit.sectorIdx != 0 {
		t.Fatalf("near wall: got kind=%q chunk=%d sector=%d idx=%d, want the wall",
			hit.kind, hit.chunkIdx, hit.sectorIdx, hit.idx)
	}

	// Spawn marker at 3, wall at 10 → the marker wins.
	Editor.level = newLevel(-10, -3)
	if hit := pick3D(mx, my); hit.kind != pickSpawn {
		t.Fatalf("near spawn: got kind=%q, want the spawn point", hit.kind)
	}

	// Nothing along the ray → no selection.
	Editor.level = &Level{}
	if hit := pick3D(mx, my); hit.kind != pickNone {
		t.Fatalf("empty level: got kind=%q, want none", hit.kind)
	}
}

// TestPick3DFloorAndCeiling checks that a floor and a ceiling are picked
// separately, each resolving to its own sector face.
func TestPick3DFloorAndCeiling(t *testing.T) {
	WinWidth, WinHeight = 800, 600
	Projection = mgl32.Perspective(mgl32.DegToRad(45.0), float32(WinWidth)/float32(WinHeight), 0.1, 1000.0)
	Cam.Position = mgl32.Vec3{0, 2, 0}

	sector := Sector{ID: "s0"}
	sector.Floor.Enabled = true
	sector.Ceiling.Enabled = true
	// Horizontal quads: floor at y=0, ceiling at y=4, both spanning x,z ∈ [-4,4].
	horizQuad := func(y float32) []float32 {
		a := [3]float32{-4, y, -4}
		b := [3]float32{4, y, -4}
		c := [3]float32{4, y, 4}
		d := [3]float32{-4, y, 4}
		var out []float32
		for _, tri := range [][3][3]float32{{a, b, c}, {a, c, d}} {
			for _, v := range tri {
				out = append(out, v[0], v[1], v[2], 0, 0)
			}
		}
		return out
	}
	sector.Floor.Geometry.Vertices = horizQuad(0)
	sector.Ceiling.Geometry.Vertices = horizQuad(4)

	prevLevel := Editor.level
	defer func() { Editor.level = prevLevel }()
	Editor.level = &Level{Chunks: []Chunk{{ID: "c0", Sectors: []Sector{sector}}}}

	mx, my := WinWidth/2, WinHeight/2

	Cam.Matrix = mgl32.LookAtV(Cam.Position, Cam.Position.Add(mgl32.Vec3{0, -1, 0}), mgl32.Vec3{0, 0, -1})
	if hit := pick3D(mx, my); hit.kind != pickFloor {
		t.Fatalf("looking down: got kind=%q, want floor", hit.kind)
	}

	Cam.Matrix = mgl32.LookAtV(Cam.Position, Cam.Position.Add(mgl32.Vec3{0, 1, 0}), mgl32.Vec3{0, 0, 1})
	if hit := pick3D(mx, my); hit.kind != pickCeiling {
		t.Fatalf("looking up: got kind=%q, want ceiling", hit.kind)
	}
}

// TestVertexHandleCycling checks that clicking the same handle box repeatedly
// walks through every vertex under the cursor and wraps around. Two sectors
// share chunk point 0, so its floor handle is really two candidates.
func TestVertexHandleCycling(t *testing.T) {
	WinWidth, WinHeight = 800, 600
	Projection = mgl32.Perspective(mgl32.DegToRad(45.0), float32(WinWidth)/float32(WinHeight), 0.1, 1000.0)
	Cam.Position = mgl32.Vec3{0, 1, 6}
	Cam.Matrix = mgl32.LookAtV(Cam.Position, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})

	mkSector := func(id string) Sector {
		s := Sector{ID: id, PointIndices: []int{0, 1, 2}}
		s.Floor.Enabled = true
		s.Ceiling.Enabled = true
		s.Ceiling.Height = 4 // keeps ceiling handles off the clicked pixel
		return s
	}
	chunk := Chunk{
		ID:      "c0",
		Points:  []mgl32.Vec2{{0, 0}, {2, 0}, {0, 2}},
		Sectors: []Sector{mkSector("s0"), mkSector("s1")},
	}

	restore := saveEditorSelection()
	defer restore()
	Editor.level = &Level{Chunks: []Chunk{chunk}}
	Editor.ActiveChunkIdx = 0
	clearVertexSelection()

	// Click dead-centre on the shared point's floor handle.
	projView := Projection.Mul4(Cam.Matrix)
	screen, ok := projectToScreen(projView, mgl32.Vec3{0, 0, 0})
	if !ok {
		t.Fatal("shared point did not project to the screen")
	}
	mx, my := int32(screen.X()), int32(screen.Y())

	var seen []int
	for i := 0; i < 3; i++ {
		h, ok := pickVertexHandle(mx, my)
		if !ok {
			t.Fatalf("click %d: no vertex handle picked", i)
		}
		if !h.isFloor || h.vertIdx != 0 {
			t.Fatalf("click %d: picked vertIdx=%d isFloor=%v, want vertex 0's floor handle",
				i, h.vertIdx, h.isFloor)
		}
		selectVertexHandle(h)
		seen = append(seen, h.sectorIdx)
	}

	// Two candidates → sector A, sector B, then back to sector A.
	if seen[0] == seen[1] {
		t.Fatalf("clicking again did not advance to the other sector: %v", seen)
	}
	if seen[2] != seen[0] {
		t.Fatalf("cycle did not wrap around: %v", seen)
	}

	// A click well away from any vertex must not pick a handle.
	if _, ok := pickVertexHandle(mx+400, my); ok {
		t.Fatal("picked a vertex handle far from any vertex")
	}

	// Ctrl+click grabs every vertex whose box covers the cursor — here the
	// shared point's floor handle in both sectors.
	clearVertexSelection()
	if !handleVertexClick(mx, my, true) {
		t.Fatal("ctrl+click picked nothing")
	}
	if len(Editor.SelectedVertices) != 2 {
		t.Fatalf("ctrl+click selected %d vertices, want 2", len(Editor.SelectedVertices))
	}
	// A plain click after it collapses back to a single vertex.
	if !handleVertexClick(mx, my, false) {
		t.Fatal("plain click picked nothing")
	}
	if len(Editor.SelectedVertices) != 1 {
		t.Fatalf("plain click selected %d vertices, want 1", len(Editor.SelectedVertices))
	}
}

// TestVertexHandleRangeLimit checks that vertices beyond vertexHandleMaxDist get
// no handle, so distant geometry cannot hijack a click.
func TestVertexHandleRangeLimit(t *testing.T) {
	WinWidth, WinHeight = 800, 600
	Projection = mgl32.Perspective(mgl32.DegToRad(45.0), float32(WinWidth)/float32(WinHeight), 0.1, 1000.0)

	sector := Sector{ID: "s0", PointIndices: []int{0}}
	far := vertexHandleMaxDist * 3
	chunk := Chunk{ID: "c0", Points: []mgl32.Vec2{{0, -far}}, Sectors: []Sector{sector}}

	restore := saveEditorSelection()
	defer restore()
	Editor.level = &Level{Chunks: []Chunk{chunk}}
	Editor.ActiveChunkIdx = 0
	clearVertexSelection()

	// In range: camera a few units away from the point.
	Cam.Position = mgl32.Vec3{0, 0, -far + 5}
	Cam.Matrix = mgl32.LookAtV(Cam.Position, mgl32.Vec3{0, 0, -far}, mgl32.Vec3{0, 1, 0})
	mx, my := WinWidth/2, WinHeight/2
	if _, ok := pickVertexHandle(mx, my); !ok {
		t.Fatal("vertex within range was not pickable")
	}

	// Out of range: same screen position, camera far away.
	Cam.Position = mgl32.Vec3{0, 0, 0}
	Cam.Matrix = mgl32.LookAtV(Cam.Position, mgl32.Vec3{0, 0, -1}, mgl32.Vec3{0, 1, 0})
	if _, ok := pickVertexHandle(mx, my); ok {
		t.Fatalf("vertex %.0f units away was pickable (limit %.0f)", far, vertexHandleMaxDist)
	}
}

// saveEditorSelection snapshots the editor selection state a test mutates and
// returns a function that puts it back.
func saveEditorSelection() func() {
	lvl := Editor.level
	mode := Editor.Mode
	chunkIdx := Editor.ActiveChunkIdx
	sectorIdx := Editor.ActiveSectorIdx
	surface, edgeKind, edgeIdx := Editor.ActiveSurface, Editor.ActiveEdgeKind, Editor.ActiveEdgeIdx
	spawn := Editor.SpawnSelected
	si, vi := Editor.ActiveVertexSI, Editor.ActiveVertexVI
	sels := Editor.SelectedVertices
	faces := Editor.SelectedFaces
	kind := Editor.GizmoKind
	dragging, axis := Editor.GizmoDragging, Editor.GizmoDragAxis
	snap, step := Editor.SnapToGrid, Editor.StepSize
	selMode, wire := Editor.SelMode, Editor.Wireframe
	biomeIdx := Editor.ActiveBiomeSectorIdx
	tool := Editor.GizmoTool
	gizmo2D := Editor.Gizmo2DState
	editFloor := Editor.EditFloorVerts
	dragSI, dragVI := Editor.DragVertexSI, Editor.DragVertexVI
	return func() {
		Editor.level = lvl
		Editor.Mode = mode
		Editor.ActiveChunkIdx = chunkIdx
		Editor.ActiveSectorIdx = sectorIdx
		Editor.ActiveSurface, Editor.ActiveEdgeKind, Editor.ActiveEdgeIdx = surface, edgeKind, edgeIdx
		Editor.SpawnSelected = spawn
		Editor.ActiveVertexSI, Editor.ActiveVertexVI = si, vi
		Editor.SelectedVertices = sels
		Editor.SelectedFaces = faces
		Editor.GizmoKind = kind
		Editor.GizmoDragging, Editor.GizmoDragAxis = dragging, axis
		Editor.SnapToGrid, Editor.StepSize = snap, step
		Editor.SelMode, Editor.Wireframe = selMode, wire
		Editor.ActiveBiomeSectorIdx = biomeIdx
		Editor.GizmoTool = tool
		Editor.Gizmo2DState = gizmo2D
		Editor.EditFloorVerts = editFloor
		Editor.DragVertexSI, Editor.DragVertexVI = dragSI, dragVI
		editorVisReset()
	}
}
