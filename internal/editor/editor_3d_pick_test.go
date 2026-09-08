package editor

import (
	"foundry/internal/gfx"
	"foundry/internal/platform"
	"foundry/internal/world"
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
	platform.WinWidth, platform.WinHeight = 800, 600
	gfx.Projection = mgl32.Perspective(mgl32.DegToRad(45.0), float32(platform.WinWidth)/float32(platform.WinHeight), 0.1, 1000.0)
	gfx.Cam.Position = mgl32.Vec3{0, 0, 0}
	gfx.Cam.Direction = mgl32.Vec3{0, 0, -1}
	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, mgl32.Vec3{0, 0, -1}, mgl32.Vec3{0, 1, 0})
	return platform.WinWidth / 2, platform.WinHeight / 2
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

	newLevel := func(wallZ, spawnZ float32) *world.Level {
		wall := world.Wall{Visible: true}
		wall.Geometry.Vertices = quadVerts(wallZ, 4)
		return &world.Level{
			Chunks: []world.Chunk{{
				ID: "c0",
				Sectors: []world.Sector{{
					ID:    "s0",
					Walls: []world.Wall{wall},
				}},
			}},
			// No uploaded mesh, so picking falls back to the marker's AABB —
			// which is the case that matters here, since the arrow itself is
			// only generated against a GL context.
			Spawn: &world.SpawnPoint{Position: mgl32.Vec3{0, 0, spawnZ}},
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
	Editor.level = &world.Level{}
	if hit := pick3D(mx, my); hit.kind != pickNone {
		t.Fatalf("empty level: got kind=%q, want none", hit.kind)
	}
}

// TestPick3DFloorAndCeiling checks that a floor and a ceiling are picked
// separately, each resolving to its own sector face.
func TestPick3DFloorAndCeiling(t *testing.T) {
	platform.WinWidth, platform.WinHeight = 800, 600
	gfx.Projection = mgl32.Perspective(mgl32.DegToRad(45.0), float32(platform.WinWidth)/float32(platform.WinHeight), 0.1, 1000.0)
	gfx.Cam.Position = mgl32.Vec3{0, 2, 0}

	sector := world.Sector{ID: "s0"}
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
	Editor.level = &world.Level{Chunks: []world.Chunk{{ID: "c0", Sectors: []world.Sector{sector}}}}

	mx, my := platform.WinWidth/2, platform.WinHeight/2

	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, gfx.Cam.Position.Add(mgl32.Vec3{0, -1, 0}), mgl32.Vec3{0, 0, -1})
	if hit := pick3D(mx, my); hit.kind != pickFloor {
		t.Fatalf("looking down: got kind=%q, want floor", hit.kind)
	}

	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, gfx.Cam.Position.Add(mgl32.Vec3{0, 1, 0}), mgl32.Vec3{0, 0, 1})
	if hit := pick3D(mx, my); hit.kind != pickCeiling {
		t.Fatalf("looking up: got kind=%q, want ceiling", hit.kind)
	}
}

// TestVertexHandleCycling checks that clicking the same handle box repeatedly
// walks through every vertex under the cursor and wraps around. Two sectors
// share chunk point 0, so its floor handle is really two candidates.
func TestVertexHandleCycling(t *testing.T) {
	platform.WinWidth, platform.WinHeight = 800, 600
	gfx.Projection = mgl32.Perspective(mgl32.DegToRad(45.0), float32(platform.WinWidth)/float32(platform.WinHeight), 0.1, 1000.0)
	gfx.Cam.Position = mgl32.Vec3{0, 1, 6}
	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})

	mkSector := func(id string) world.Sector {
		s := world.Sector{ID: id, PointIndices: []int{0, 1, 2}}
		s.Floor.Enabled = true
		s.Ceiling.Enabled = true
		s.Ceiling.Height = 4 // keeps ceiling handles off the clicked pixel
		return s
	}
	chunk := world.Chunk{
		ID:      "c0",
		Points:  []mgl32.Vec2{{0, 0}, {2, 0}, {0, 2}},
		Sectors: []world.Sector{mkSector("s0"), mkSector("s1")},
	}

	restore := saveEditorSelection()
	defer restore()
	Editor.level = &world.Level{Chunks: []world.Chunk{chunk}}
	Editor.ActiveChunkIdx = 0
	clearVertexSelection()

	// Click dead-centre on the shared point's floor handle.
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
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
	platform.WinWidth, platform.WinHeight = 800, 600
	gfx.Projection = mgl32.Perspective(mgl32.DegToRad(45.0), float32(platform.WinWidth)/float32(platform.WinHeight), 0.1, 1000.0)

	sector := world.Sector{ID: "s0", PointIndices: []int{0}}
	far := vertexHandleMaxDist * 3
	chunk := world.Chunk{ID: "c0", Points: []mgl32.Vec2{{0, -far}}, Sectors: []world.Sector{sector}}

	restore := saveEditorSelection()
	defer restore()
	Editor.level = &world.Level{Chunks: []world.Chunk{chunk}}
	Editor.ActiveChunkIdx = 0
	clearVertexSelection()

	// In range: camera a few units away from the point.
	gfx.Cam.Position = mgl32.Vec3{0, 0, -far + 5}
	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, mgl32.Vec3{0, 0, -far}, mgl32.Vec3{0, 1, 0})
	mx, my := platform.WinWidth/2, platform.WinHeight/2
	if _, ok := pickVertexHandle(mx, my); !ok {
		t.Fatal("vertex within range was not pickable")
	}

	// Out of range: same screen position, camera far away.
	gfx.Cam.Position = mgl32.Vec3{0, 0, 0}
	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, mgl32.Vec3{0, 0, -1}, mgl32.Vec3{0, 1, 0})
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
	chunkSel := Editor.ChunkSelected
	entIdx, volIdx := Editor.ActiveEntityIdx, Editor.ActiveVolumeIdx
	lightKind, lightIdx := Editor.SelectedLightKind, Editor.SelectedLightIdx
	biomeLayer := Editor.BiomeLayerActive
	focus := Editor.PanelFocus
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
		Editor.ChunkSelected = chunkSel
		Editor.ActiveEntityIdx, Editor.ActiveVolumeIdx = entIdx, volIdx
		Editor.SelectedLightKind, Editor.SelectedLightIdx = lightKind, lightIdx
		Editor.BiomeLayerActive = biomeLayer
		Editor.PanelFocus = focus
		editorVisReset()
	}
}

// TestCollectHandlesShowAllIgnoresCursor covers the off-world draw path, which
// asks for every handle while passing a cursor far outside the window so none
// of them reads as hovered. Comparing against vertexHandleShowAllPx as a radius
// rejected all of them instead, which emptied the viewport whenever a camera
// drag carried the pointer over the side panel — turning right, but not left.
func TestCollectHandlesShowAllIgnoresCursor(t *testing.T) {
	setupPickCamera()

	prevLevel, prevWire := Editor.level, Editor.Wireframe
	defer func() { Editor.level = prevLevel; Editor.Wireframe = prevWire }()
	// Wireframe so the occlusion test answers true without needing geometry.
	Editor.Wireframe = true

	// A unit square 5 units down -Z, well inside vertexHandleMaxDist.
	sector := world.Sector{ID: "s0", PointIndices: []int{0, 1, 2, 3}}
	sector.Floor.Height, sector.Ceiling.Height = 0, 2
	Editor.level = &world.Level{Chunks: []world.Chunk{{
		ID: "c0",
		Points: []mgl32.Vec2{
			{-1, -6}, {1, -6}, {1, -4}, {-1, -4},
		},
		Sectors: []world.Sector{sector},
	}}}

	const offWorld = math.MinInt32 / 2
	for _, tc := range []struct {
		name   string
		mx, my int32
	}{
		{"cursor on the geometry", platform.WinWidth / 2, platform.WinHeight / 2},
		{"cursor off the world", offWorld, offWorld},
	} {
		if got := len(collectVertexHandles(tc.mx, tc.my, vertexHandleShowAllPx)); got != 8 {
			t.Errorf("%s: got %d vertex handles, want 8 (4 points × floor+ceiling)", tc.name, got)
		}
		if got := len(collectEdgeHandles(tc.mx, tc.my, vertexHandleShowAllPx)); got == 0 {
			t.Errorf("%s: got no edge handles", tc.name)
		}
	}

	// The cursor-proximity radius still filters: the same off-world cursor
	// reaches nothing when show-all is not asked for.
	if got := len(collectVertexHandles(offWorld, offWorld, vertexHandleShowPx)); got != 0 {
		t.Errorf("proximity mode with an off-world cursor: got %d handles, want 0", got)
	}
}

// TestPick3DPortalGapFill covers the rule that split portals in two: an open
// portal is a hole and a click goes through it, but the gap fill that closes
// one is drawn geometry and a click lands on it.
//
// The shape this exists for is the vertical door — a sector whose floor is
// raised and ceiling lowered until they meet — whose every visible face is gap
// fill owned by the neighbouring sector's portal. Without this the door could
// be built but never clicked, and its gap texture was reachable only by
// finding the right edge on the 2D map.
func TestPick3DPortalGapFill(t *testing.T) {
	mx, my := setupPickCamera()

	prevLevel := Editor.level
	defer func() { Editor.level = prevLevel }()

	// A portal at z=-3 with a wall 7 units behind it. Whether the click stops
	// at the portal or carries on to the wall is the whole question.
	newLevel := func(withGap bool) *world.Level {
		wall := world.Wall{Visible: true}
		wall.Geometry.Vertices = quadVerts(-10, 4)
		p := world.Portal{ID: "door", ToSector: "s0", DrawGap: true, Visible: true}
		if withGap {
			p.FloorGap.Vertices = quadVerts(-3, 4)
		}
		return &world.Level{
			Chunks: []world.Chunk{{
				ID: "c0",
				Sectors: []world.Sector{{
					ID:      "s0",
					Walls:   []world.Wall{wall},
					Portals: []world.Portal{p},
				}},
			}},
		}
	}

	Editor.level = newLevel(false)
	if hit := pick3D(mx, my); hit.kind != pickWall {
		t.Fatalf("open portal: got kind=%q, want the click to pass through to the wall", hit.kind)
	}

	Editor.level = newLevel(true)
	hit := pick3D(mx, my)
	if hit.kind != pickPortal || hit.sectorIdx != 0 || hit.idx != 0 {
		t.Fatalf("gap fill: got kind=%q sector=%d idx=%d, want portal 0 of sector 0",
			hit.kind, hit.sectorIdx, hit.idx)
	}
	if math.Abs(float64(hit.t-3)) > 1e-3 {
		t.Fatalf("gap fill hit at t=%v, want ~3", hit.t)
	}

	// And it has to arrive as a face selection, or the properties panel never
	// opens on the gap texture the click was aimed at.
	f, ok := faceSelFromHit(hit)
	if !ok || f.kind != "portal" || f.idx != 0 {
		t.Fatalf("faceSelFromHit(%+v) = %+v, %v; want the portal face", hit, f, ok)
	}
	if !faceSelValid(f) {
		t.Fatal("portal face selection does not name live geometry")
	}
}

// TestPick3DPortalCeilingGap is the other half of a shut door: the filler
// hanging from the ceiling, on a portal with no floor riser at all.
func TestPick3DPortalCeilingGap(t *testing.T) {
	mx, my := setupPickCamera()

	prevLevel := Editor.level
	defer func() { Editor.level = prevLevel }()

	p := world.Portal{ID: "door", ToSector: "s0", DrawGap: true, Visible: true}
	p.CeilGap.Vertices = quadVerts(-5, 4)
	Editor.level = &world.Level{
		Chunks: []world.Chunk{{
			ID:      "c0",
			Sectors: []world.Sector{{ID: "s0", Portals: []world.Portal{p}}},
		}},
	}

	if hit := pick3D(mx, my); hit.kind != pickPortal || hit.idx != 0 {
		t.Fatalf("ceiling filler: got kind=%q idx=%d, want portal 0", hit.kind, hit.idx)
	}
}
