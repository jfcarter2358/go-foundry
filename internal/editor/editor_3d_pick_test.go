package editor

import (
	"math"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/platform"
	"foundry/internal/testenv"
	"foundry/internal/world"

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

// brushGeomLevel installs a level of one chunk whose brush faces have real
// CPU-side vertex buffers, which is what pick3D casts against.
//
// A picking test needs geometry, not just a brush: Brush.Load is where a face's
// index loop becomes triangles, and testenv.WithoutGeometryUploads is what lets
// that happen with no GL context.
func brushGeomLevel(t *testing.T, brushes ...world.Brush) *world.Chunk {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	ch := world.Chunk{ID: "c0", Brushes: brushes}
	for bi := range ch.Brushes {
		if err := ch.Brushes[bi].Load(); err != nil {
			t.Fatalf("loading brush %q: %v", ch.Brushes[bi].ID, err)
		}
	}
	ch.RecomputeBounds()
	Editor.level = &world.Level{Chunks: []world.Chunk{ch}}
	return &Editor.level.Chunks[0]
}

// TestPick3DClosestWins covers the rule that a click selects whatever is
// nearest along the ray, in both orders: a brush in front of the spawn marker
// and the marker in front of a brush.
func TestPick3DClosestWins(t *testing.T) {
	defer saveEditorState()()
	mx, my := setupPickCamera()

	setup := func(brushZ, spawnZ float32) {
		brushGeomLevel(t, world.NewBoxBrush("wall",
			mgl32.Vec3{0, 0, brushZ}, mgl32.Vec3{4, 4, 0.5}, "concrete"))
		// No uploaded mesh, so picking falls back to the marker's AABB —
		// which is the case that matters here, since the arrow itself is
		// only generated against a GL context.
		Editor.level.Spawn = &world.SpawnPoint{Position: mgl32.Vec3{0, 0, spawnZ}}
	}

	// Brush at 3, spawn marker at 10 → the brush wins.
	setup(-3, -10)
	if hit := pick3D(mx, my); hit.kind != pickBrushFace || hit.brushIdx != 0 {
		t.Fatalf("near brush: got kind=%q chunk=%d brush=%d face=%d, want a brush face",
			hit.kind, hit.chunkIdx, hit.brushIdx, hit.faceIdx)
	}

	// Spawn marker at 3, brush at 10 → the marker wins.
	setup(-10, -3)
	if hit := pick3D(mx, my); hit.kind != pickSpawn {
		t.Fatalf("near spawn: got kind=%q, want the spawn point", hit.kind)
	}

	// Nothing along the ray → no selection.
	Editor.level = &world.Level{}
	if hit := pick3D(mx, my); hit.kind != pickNone {
		t.Fatalf("empty level: got kind=%q, want none", hit.kind)
	}
}

// TestPick3DResolvesTheNearFaceOfASolid checks that a click on a box lands on
// the side facing the camera rather than on the far side behind it.
//
// This is the brush counterpart of the old floor-versus-ceiling test, and it is
// a sharper question than that one was. A sector's floor and ceiling were two
// separate objects; a box's top and bottom are two faces of one solid, both on
// the ray, and only the winding tells them apart. effectivePickT's back-face
// bias is what makes the near one win.
func TestPick3DResolvesTheNearFaceOfASolid(t *testing.T) {
	defer saveEditorState()()
	platform.WinWidth, platform.WinHeight = 800, 600
	gfx.Projection = mgl32.Perspective(mgl32.DegToRad(45.0),
		float32(platform.WinWidth)/float32(platform.WinHeight), 0.1, 1000.0)
	gfx.Cam.Position = mgl32.Vec3{0, 6, 0}

	brushGeomLevel(t, world.NewBoxBrush("block",
		mgl32.Vec3{0, 0, 0}, mgl32.Vec3{4, 2, 4}, "concrete"))

	mx, my := platform.WinWidth/2, platform.WinHeight/2

	// From above, looking down: the top face.
	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position,
		gfx.Cam.Position.Add(mgl32.Vec3{0, -1, 0}), mgl32.Vec3{0, 0, -1})
	hit := pick3D(mx, my)
	if hit.kind != pickBrushFace || hit.faceIdx != boxFaceTop {
		t.Fatalf("looking down: got kind=%q face=%d, want the top face (%d)",
			hit.kind, hit.faceIdx, boxFaceTop)
	}

	// From below, looking up: the bottom face.
	gfx.Cam.Position = mgl32.Vec3{0, -6, 0}
	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position,
		gfx.Cam.Position.Add(mgl32.Vec3{0, 1, 0}), mgl32.Vec3{0, 0, 1})
	hit = pick3D(mx, my)
	if hit.kind != pickBrushFace || hit.faceIdx != boxFaceBottom {
		t.Fatalf("looking up: got kind=%q face=%d, want the bottom face (%d)",
			hit.kind, hit.faceIdx, boxFaceBottom)
	}
}

// TestPick3DSkipsNoDrawFaces: a NoDraw face builds no geometry, so there is
// nothing under the cursor to hit. Picking one would select an invisible
// surface out of what looks like empty space.
func TestPick3DSkipsNoDrawFaces(t *testing.T) {
	defer saveEditorState()()
	mx, my := setupPickCamera()

	b := world.NewBoxBrush("block", mgl32.Vec3{0, 0, -5}, mgl32.Vec3{2, 2, 0.5}, "concrete")
	brushGeomLevel(t, b)
	if hit := pick3D(mx, my); hit.kind != pickBrushFace {
		t.Fatalf("with the face drawn: got kind=%q, want a brush face", hit.kind)
	}

	// Every face off, so the whole solid is invisible.
	ch := &Editor.level.Chunks[0]
	for fi := range ch.Brushes[0].Faces {
		ch.Brushes[0].Faces[fi].NoDraw = true
	}
	if err := ch.Brushes[0].Load(); err != nil {
		t.Fatalf("reloading: %v", err)
	}
	if hit := pick3D(mx, my); hit.kind != pickNone {
		t.Fatalf("with every face NoDraw: got kind=%q, want none", hit.kind)
	}
}

// ---------- vertex handles ----------

// TestVertexHandleCycling checks that clicking the same handle box repeatedly
// walks through every vertex under the cursor and wraps around.
//
// Two boxes are placed so that a corner of each projects onto the same pixel,
// which is the case the cycling exists for: a shared corner of two sectors used
// to be the example, and a brush owns its corners outright, so overlapping
// handles now come from two solids meeting rather than from one point named
// twice.
func TestVertexHandleCycling(t *testing.T) {
	defer saveEditorState()()
	platform.WinWidth, platform.WinHeight = 800, 600
	gfx.Projection = mgl32.Perspective(mgl32.DegToRad(45.0),
		float32(platform.WinWidth)/float32(platform.WinHeight), 0.1, 1000.0)
	gfx.Cam.Position = mgl32.Vec3{0, 0, 6}
	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})
	// Wireframe, so the occlusion test answers true without a rendered scene.
	Editor.Wireframe = true

	// Two boxes placed so that one corner of each lies on the same ray from the
	// camera: the eye is at (0,0,6), the near corner is (1,1,1), and the ray
	// through it reaches (1.6,1.6,-2) — which is the far box's own corner. The
	// two therefore project onto a single pixel, which is what cycling is for.
	brushGeomLevel(t,
		world.NewBoxBrush("a", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 1, 1}, "concrete"),
		world.NewBoxBrush("b", mgl32.Vec3{0.6, 0.6, -3}, mgl32.Vec3{1, 1, 1}, "concrete"))
	Editor.ActiveChunkIdx = 0
	setVertexSelection(nil)

	// The pixel the front box's top-right-near corner lands on.
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
	corner, ok := projectToScreen(projView, mgl32.Vec3{1, 1, 1})
	if !ok {
		t.Fatal("the corner did not project onto the screen")
	}
	mx, my := int32(corner.X()), int32(corner.Y())

	inBox := vertexHandlesInBox(mx, my)
	if len(inBox) < 2 {
		t.Fatalf("got %d handles under the cursor, want at least 2 to cycle through", len(inBox))
	}

	// Each click advances to the next candidate, and the last wraps to the
	// first — which is the whole point: a handle hidden behind another can be
	// reached without moving the camera.
	seen := map[vertexSel]bool{}
	for i := 0; i < len(inBox); i++ {
		h, ok := pickVertexHandle(mx, my)
		if !ok {
			t.Fatalf("click %d picked nothing", i)
		}
		seen[h.vertexSel] = true
		selectVertexHandle(h)
	}
	if len(seen) != len(inBox) {
		t.Errorf("cycling reached %d of %d handles under the cursor", len(seen), len(inBox))
	}
	if h, ok := pickVertexHandle(mx, my); !ok || h.vertexSel != inBox[0].vertexSel {
		t.Errorf("the cycle did not wrap back to the first handle")
	}
}

// TestVertexHandleRangeLimit: a vertex further than vertexHandleMaxDist gets no
// handle, so distant geometry that happens to project under the cursor cannot
// hijack a click meant for what is in front of it.
func TestVertexHandleRangeLimit(t *testing.T) {
	defer saveEditorState()()
	platform.WinWidth, platform.WinHeight = 800, 600
	gfx.Projection = mgl32.Perspective(mgl32.DegToRad(45.0),
		float32(platform.WinWidth)/float32(platform.WinHeight), 0.1, 1000.0)
	Editor.Wireframe = true

	const far = vertexHandleMaxDist * 2
	brushGeomLevel(t, world.NewBoxBrush("far",
		mgl32.Vec3{0, 0, -far}, mgl32.Vec3{1, 1, 1}, "concrete"))
	Editor.ActiveChunkIdx = 0
	setVertexSelection(nil)

	gfx.Cam.Position = mgl32.Vec3{0, 0, 0}
	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, mgl32.Vec3{0, 0, -1}, mgl32.Vec3{0, 1, 0})

	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
	corner, ok := projectToScreen(projView, mgl32.Vec3{1, 1, -far + 1})
	if !ok {
		t.Fatal("the far corner did not project onto the screen")
	}
	if _, ok := pickVertexHandle(int32(corner.X()), int32(corner.Y())); ok {
		t.Fatalf("a vertex %.0f units away was pickable (limit %.0f)", far, vertexHandleMaxDist)
	}
}

// TestCollectHandlesShowAllIgnoresCursor covers the off-world draw path, which
// asks for every handle while passing a cursor far outside the window so none
// of them reads as hovered. Comparing against vertexHandleShowAllPx as a radius
// rejected all of them instead, which emptied the viewport whenever a camera
// drag carried the pointer over the side panel — turning right, but not left.
func TestCollectHandlesShowAllIgnoresCursor(t *testing.T) {
	defer saveEditorState()()
	setupPickCamera()
	// Wireframe so the occlusion test answers true without needing a rendered
	// scene behind it.
	Editor.Wireframe = true

	// One box 5 units down -Z, well inside vertexHandleMaxDist.
	brushGeomLevel(t, world.NewBoxBrush("b",
		mgl32.Vec3{0, 0, -5}, mgl32.Vec3{1, 1, 1}, "concrete"))

	const offWorld = math.MinInt32 / 2
	for _, tc := range []struct {
		name   string
		mx, my int32
	}{
		{"cursor on the geometry", platform.WinWidth / 2, platform.WinHeight / 2},
		{"cursor off the world", offWorld, offWorld},
	} {
		if got := len(collectVertexHandles(tc.mx, tc.my, vertexHandleShowAllPx)); got != 8 {
			t.Errorf("%s: got %d vertex handles, want 8 (a box's corners)", tc.name, got)
		}
		// A box has twelve edges: every face loop contributes four, and each
		// is shared by exactly two faces, so the dedupe halves 24 down to 12.
		if got := len(collectEdgeHandles(tc.mx, tc.my, vertexHandleShowAllPx)); got != 12 {
			t.Errorf("%s: got %d edge handles, want 12 (a box's edges)", tc.name, got)
		}
	}

	// The cursor-proximity radius still filters: the same off-world cursor
	// reaches nothing when show-all is not asked for.
	if got := len(collectVertexHandles(offWorld, offWorld, vertexHandleShowPx)); got != 0 {
		t.Errorf("proximity mode with an off-world cursor: got %d handles, want 0", got)
	}
}

// TestBrushEdgesDedupesSharedEdges: the two faces meeting along an edge report
// the same undirected pair, and a doubled handle would mean every edge of every
// brush was two coincident prisms fighting for the same click.
func TestBrushEdgesDedupesSharedEdges(t *testing.T) {
	b := world.NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete")
	loops := make([][]int, len(b.Faces))
	for fi := range b.Faces {
		loops[fi] = b.Faces[fi].Indices
	}
	edges := brushEdges(0, 0, loops, false)
	if len(edges) != 12 {
		t.Fatalf("got %d edges for a box, want 12", len(edges))
	}
	for _, e := range edges {
		if e.a >= e.b {
			t.Errorf("edge %v is not in canonical (low, high) order", e)
		}
	}
}
