package editor

// editor_2d_anchor_test.go — the 2D map is an authoring view of one chunk's
// own layout, so its plane is that chunk's local space: placing the chunk
// somewhere else in the world must not move its layout on the map. See the
// anchor block in editor_2d.go.

import (
	"math"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// anchorTestChunk is a plain square room, placed by the caller.
func anchorTestChunk(origin mgl32.Vec3, rotation, scale float32) *world.Level {
	return &world.Level{Chunks: []world.Chunk{{
		ID:     "room",
		Origin: origin, Rotation: rotation, Scale: scale,
		Points: []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}},
		Sectors: []world.Sector{{
			ID:           "s0",
			PointIndices: []int{0, 1, 2, 3},
			Floor:        world.Floor{Height: 0, VertexOffsets: []float32{0, 0, 0, 0}},
			Ceiling:      world.Ceiling{Height: 8, VertexOffsets: []float32{0, 0, 0, 0}},
		}},
	}}}
}

// withMapView pins a deterministic pan/zoom and active chunk for the duration
// of a test, restoring whatever the editor had.
func withMapView(t *testing.T, lvl *world.Level) {
	t.Helper()
	restore := saveEditorSelection()
	pan, zoom := Editor.Pan2D, Editor.Zoom2D
	t.Cleanup(func() {
		Editor.Pan2D, Editor.Zoom2D = pan, zoom
		restore()
	})
	Editor.level = lvl
	Editor.ActiveChunkIdx = 0
	Editor.Pan2D = mgl32.Vec2{0, 0}
	Editor.Zoom2D = 8
}

// chunkPointsOnScreen is where the active chunk's own points land on the map.
func chunkPointsOnScreen(t *testing.T) []mgl32.Vec2 {
	t.Helper()
	ch := &Editor.level.Chunks[0]
	out := make([]mgl32.Vec2, len(ch.Points))
	for i, p := range ch.Points {
		// Drawn exactly as Editor2DRender draws them: chunk-local carried out
		// to world, then projected.
		w := ch.LocalToWorld(mgl32.Vec3{p.X(), 0, p.Y()})
		sx, sy := worldToScreen(w.X(), w.Z())
		out[i] = mgl32.Vec2{sx, sy}
	}
	return out
}

// The headline invariant the user asked for: dragging a chunk's Origin around
// must not move its layout in the 2D editor.
func TestMapLayoutIsUnchangedByChunkOrigin(t *testing.T) {
	withMapView(t, anchorTestChunk(mgl32.Vec3{}, 0, 1))
	atOrigin := chunkPointsOnScreen(t)

	for _, origin := range []mgl32.Vec3{
		{100, 0, 0}, {0, 0, -250}, {1000, 37, 1000},
	} {
		Editor.level.Chunks[0].Origin = origin
		moved := chunkPointsOnScreen(t)
		for i := range atOrigin {
			if !nearlyEqual(atOrigin[i].X(), moved[i].X(), 1e-3) ||
				!nearlyEqual(atOrigin[i].Y(), moved[i].Y(), 1e-3) {
				t.Fatalf("origin %v moved the map layout: point %d was at %v, now at %v",
					origin, i, atOrigin[i], moved[i])
			}
		}
	}
}

// Rotating or scaling the chunk places it differently in the world too, and is
// equally not a change to the layout being authored.
func TestMapLayoutIsUnchangedByChunkRotationAndScale(t *testing.T) {
	withMapView(t, anchorTestChunk(mgl32.Vec3{}, 0, 1))
	plain := chunkPointsOnScreen(t)

	Editor.level.Chunks[0].Rotation = float32(math.Pi / 3)
	Editor.level.Chunks[0].Scale = 2.5
	Editor.level.Chunks[0].Origin = mgl32.Vec3{-400, 0, 90}
	placed := chunkPointsOnScreen(t)

	for i := range plain {
		if !nearlyEqual(plain[i].X(), placed[i].X(), 1e-3) ||
			!nearlyEqual(plain[i].Y(), placed[i].Y(), 1e-3) {
			t.Fatalf("rotating/scaling/moving the chunk moved the map layout: "+
				"point %d was at %v, now at %v", i, plain[i], placed[i])
		}
	}
}

// The chunk's local origin is the map's origin — that is what "the local
// origin of the chunk should be the origin in the 2D editor" means concretely.
func TestChunkLocalOriginIsTheMapOrigin(t *testing.T) {
	withMapView(t, anchorTestChunk(mgl32.Vec3{321, 0, -77}, float32(math.Pi/4), 3))
	ch := &Editor.level.Chunks[0]

	// Where the chunk's own (0,0) lands on screen...
	w := ch.LocalToWorld(mgl32.Vec3{0, 0, 0})
	sx, sy := worldToScreen(w.X(), w.Z())

	// ...must be where the map's own origin lands, with pan at 0 that is the
	// middle of the viewport.
	ox, oy := editor2DView().worldToScreen(0, 0)
	if !nearlyEqual(sx, ox, 1e-3) || !nearlyEqual(sy, oy, 1e-3) {
		t.Errorf("chunk local origin drew at (%v,%v), map origin is (%v,%v)", sx, sy, ox, oy)
	}
}

// screenToWorld and worldToScreen must remain exact inverses through the
// anchor, or a click would not land where the thing under it was drawn.
func TestScreenWorldRoundTripThroughAnchor(t *testing.T) {
	withMapView(t, anchorTestChunk(mgl32.Vec3{50, 0, -20}, float32(math.Pi/6), 2))

	for _, pt := range [][2]int32{{100, 100}, {640, 360}, {17, 903}} {
		wx, wz := screenToWorld(pt[0], pt[1])
		sx, sy := worldToScreen(wx, wz)
		if !nearlyEqual(sx, float32(pt[0]), 1e-2) || !nearlyEqual(sy, float32(pt[1]), 1e-2) {
			t.Errorf("screen %v -> world (%v,%v) -> screen (%v,%v): not a round trip",
				pt, wx, wz, sx, sy)
		}
	}
}

// A click on the map has to resolve to the chunk-local point actually drawn
// there — this is the path every hit test takes (screenToChunkLocal).
func TestScreenToChunkLocalMatchesWhatWasDrawn(t *testing.T) {
	withMapView(t, anchorTestChunk(mgl32.Vec3{-300, 0, 125}, float32(math.Pi/5), 1.75))
	ch := &Editor.level.Chunks[0]

	for i, want := range ch.Points {
		w := ch.LocalToWorld(mgl32.Vec3{want.X(), 0, want.Y()})
		sx, sy := worldToScreen(w.X(), w.Z())
		gotX, gotZ := screenToChunkLocal(ch, int32(math.Round(float64(sx))), int32(math.Round(float64(sy))))
		if !nearlyEqual(gotX, want.X(), 1e-2) || !nearlyEqual(gotZ, want.Y(), 1e-2) {
			t.Errorf("point %d drawn at screen (%v,%v) resolved to local (%v,%v), want (%v,%v)",
				i, sx, sy, gotX, gotZ, want.X(), want.Y())
		}
	}
}

// A click radius is quoted in pixels and has to stay that many pixels on the
// map whatever the chunk's scale — chunkLocalSize(screenToWorldSize(px)) is
// the composition every hit test uses to get there.
func TestClickRadiusIsScaleInvariantOnTheMap(t *testing.T) {
	withMapView(t, anchorTestChunk(mgl32.Vec3{}, 0, 1))
	ch := &Editor.level.Chunks[0]
	want := chunkLocalSize(ch, screenToWorldSize(editor2DSelectRadius, editor2DOrtho()))

	for _, scale := range []float32{0.25, 2, 7.5} {
		ch.Scale = scale
		got := chunkLocalSize(ch, screenToWorldSize(editor2DSelectRadius, editor2DOrtho()))
		if !nearlyEqual(got, want, 1e-4) {
			t.Errorf("scale %v: click radius in local units = %v, want %v (a pixel radius "+
				"must not change meaning when the chunk is scaled)", scale, got, want)
		}
	}
}
