package editor

// editor_3d_worldbounds_test.go — Chunk.MinBound/MaxBound and a brush's own
// LocalAABB are chunk-LOCAL. Anything in the 3D view that draws them, frames
// them, or tests a world-space ray against them has to carry them out through
// the chunk's own placement first, or it describes where the chunk would be if
// it had never been moved.

import (
	"math"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// boundsTestLevel is one chunk holding a single 10x8x10 box brush spanning
// x,z ∈ [-5,5] and y ∈ [0,8], placed by the caller.
func boundsTestLevel(origin mgl32.Vec3, rotation, scale float32) *world.Level {
	return &world.Level{Chunks: []world.Chunk{{
		ID:     "room",
		Origin: origin, Rotation: rotation, Scale: scale,
		Brushes: []world.Brush{
			world.NewBoxBrush("b0", mgl32.Vec3{0, 4, 0}, mgl32.Vec3{5, 4, 5}, "concrete"),
		},
		MinBound: mgl32.Vec3{-5, 0, -5},
		MaxBound: mgl32.Vec3{5, 8, 5},
	}}}
}

// The chunk-highlight box has to sit on the chunk, wherever it has been moved
// to — the reported bug was an outline stuck at the world origin.
func TestChunkWorldOBBFollowsTheChunk(t *testing.T) {
	origin := mgl32.Vec3{120, 4, -60}
	lvl := boundsTestLevel(origin, 0, 1)
	ch := &lvl.Chunks[0]

	center, axes, half := chunkWorldOBB(ch)

	// Local centre is (0,4,0); moving the chunk moves the box with it.
	want := mgl32.Vec3{0, 4, 0}.Add(origin)
	if !vecNear(center, want, 1e-4) {
		t.Errorf("box centre = %v, want %v (the chunk's own centre, moved)", center, want)
	}
	if !vecNear(half, mgl32.Vec3{5, 4, 5}, 1e-4) {
		t.Errorf("half extents = %v, want (5,4,5)", half)
	}
	for i, a := range axes {
		if !nearlyEqual(a.Len(), 1, 1e-4) {
			t.Errorf("axis %d has length %v, want a unit vector", i, a.Len())
		}
	}
}

// A rotated chunk's box must turn with it — that is the whole reason it is an
// oriented box and not a refitted world AABB.
func TestChunkWorldOBBRotatesWithTheChunk(t *testing.T) {
	lvl := boundsTestLevel(mgl32.Vec3{}, float32(math.Pi/2), 1)
	_, axes, half := chunkWorldOBB(&lvl.Chunks[0])

	// A quarter turn about Y takes local +X to world -Z (mgl32's Y rotation).
	if !nearlyEqual(mgl32.Abs(axes[0].Z()), 1, 1e-4) {
		t.Errorf("local +X axis = %v, want it turned onto world Z", axes[0])
	}
	if !nearlyEqual(axes[1].Y(), 1, 1e-4) {
		t.Errorf("local +Y axis = %v, want it still world up", axes[1])
	}
	// Rotation does not change extents.
	if !vecNear(half, mgl32.Vec3{5, 4, 5}, 1e-4) {
		t.Errorf("half extents = %v, want (5,4,5) — rotation must not resize the box", half)
	}
}

// Scale grows the box in world units while the stored local bounds stay put.
func TestChunkWorldOBBScalesWithTheChunk(t *testing.T) {
	lvl := boundsTestLevel(mgl32.Vec3{}, 0, 3)
	center, _, half := chunkWorldOBB(&lvl.Chunks[0])

	if !vecNear(half, mgl32.Vec3{15, 12, 15}, 1e-3) {
		t.Errorf("half extents at scale 3 = %v, want (15,12,15)", half)
	}
	if !vecNear(center, mgl32.Vec3{0, 12, 0}, 1e-3) {
		t.Errorf("centre at scale 3 = %v, want (0,12,0)", center)
	}
}

// "Frame selection" flies the camera to these bounds, so they have to be the
// world ones for both the whole-chunk fallback and the selected-brush case.
func TestSelectionBoundsAreWorldSpace(t *testing.T) {
	origin := mgl32.Vec3{300, 0, 40}
	restore := saveEditorSelection()
	defer restore()
	Editor.level = boundsTestLevel(origin, 0, 1)
	Editor.ActiveChunkIdx = 0
	Editor.SpawnSelected = false
	Editor.SelectedVertices = nil
	Editor.ActiveEntityIdx = -1
	Editor.ActiveVolumeIdx = -1

	// Whole-chunk fallback.
	clearFaceSelection()
	min, max, ok := editorSelectionBounds()
	if !ok {
		t.Fatal("no selection bounds for an active chunk")
	}
	centre := min.Add(max).Mul(0.5)
	if !vecNear(centre, mgl32.Vec3{0, 4, 0}.Add(origin), 1e-3) {
		t.Errorf("chunk framing centre = %v, want the chunk's placed centre %v",
			centre, mgl32.Vec3{0, 4, 0}.Add(origin))
	}

	// Selected brush.
	setFaceSelection([]faceSel{brushFaceSel(0, 0, 0)})
	min, max, ok = editorSelectionBounds()
	if !ok {
		t.Fatal("no selection bounds for a selected brush")
	}
	centre = min.Add(max).Mul(0.5)
	if !vecNear(centre, mgl32.Vec3{0, 4, 0}.Add(origin), 1e-3) {
		t.Errorf("brush framing centre = %v, want the brush's placed centre %v",
			centre, mgl32.Vec3{0, 4, 0}.Add(origin))
	}
}

// The occlusion walk rejects chunks by bounds before it looks at triangles. A
// moved chunk must be reachable at where it is, and not at where it is not.
func TestOcclusionChunkRejectUsesWorldBounds(t *testing.T) {
	origin := mgl32.Vec3{200, 0, 0}
	lvl := boundsTestLevel(origin, 0, 1)
	ch := &lvl.Chunks[0]

	cmin, cmax := ch.WorldBounds()

	// A ray running along +Z at the chunk's own X, aimed straight at it. The
	// chunk's stale LOCAL bounds sit back at the world origin (x in [-5,5]),
	// nowhere near this ray — so the two answers differ, which is the whole
	// point: world bounds find it, local bounds do not.
	eye := mgl32.Vec3{origin.X(), 4, -100}
	dir := mgl32.Vec3{0, 0, 1}
	if !segmentHitsAABB(eye, dir, 200, cmin, cmax) {
		t.Error("a ray aimed at the moved chunk missed its world bounds")
	}
	if segmentHitsAABB(eye, dir, 200, ch.MinBound, ch.MaxBound) {
		t.Error("the chunk's chunk-local bounds answered a world-space ray — " +
			"testing those directly is the bug this guards")
	}

	// The same ray, stopped well short of the chunk.
	if segmentHitsAABB(eye, dir, 50, cmin, cmax) {
		t.Error("a ray stopping short of the chunk still hit its world bounds")
	}
}
