package editor

// editor_transform_test.go — the seam every gizmo shares: a selected vertex's
// world position (vertexSelPos) and its write-back (writeSelectedVertexWorld,
// in editor_sel_transform.go) have to agree with each other and with the
// chunk's own ModelMatrix, or a chunk with a live Origin/Rotation/Scale would
// edit at the wrong place.

import (
	"math"
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// placedRoomLevel builds a level with one chunk placed away from the world
// origin and rotated a quarter turn, holding a single box brush whose corners
// sit at round chunk-local coordinates.
func placedRoomLevel() *world.Level {
	return &world.Level{Chunks: []world.Chunk{{
		ID:       "room",
		Origin:   mgl32.Vec3{100, 5, -50},
		Rotation: float32(math.Pi / 2),
		Scale:    1,
		Brushes: []world.Brush{
			world.NewBoxBrush("b0", mgl32.Vec3{5, 4, 5}, mgl32.Vec3{5, 4, 5}, "concrete"),
		},
	}}}
}

func TestVertexSelPosMatchesChunkModelMatrix(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	defer saveEditorSelection()()
	Editor.level = placedRoomLevel()

	sel := vertexSel{chunkIdx: 0, brushIdx: 0, vertIdx: 0}
	got, ok := vertexSelPos(sel)
	if !ok {
		t.Fatal("vertexSelPos reported no position for a valid selection")
	}

	ch := &Editor.level.Chunks[0]
	want := ch.LocalToWorld(ch.Brushes[0].Verts[0])
	if !vecNear(got, want, 1e-4) {
		t.Errorf("vertexSelPos = %v, want %v (ch.LocalToWorld of the same local corner)", got, want)
	}
}

func TestSetSelectedVertexWorldWritesBackToLocalSpace(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	defer saveEditorSelection()()
	Editor.level = placedRoomLevel()
	ch := &Editor.level.Chunks[0]

	// Two corners of the box selected — moving them by a world-space delta and
	// writing back must land each at the correct new *local* coordinate once
	// the chunk's rotation is undone.
	Editor.SelectedVertices = []vertexSel{
		{chunkIdx: 0, brushIdx: 0, vertIdx: 0},
		{chunkIdx: 0, brushIdx: 0, vertIdx: 1},
	}
	before0 := ch.Brushes[0].Verts[0]
	before1 := ch.Brushes[0].Verts[1]

	delta := mgl32.Vec3{1, 0, 0} // world +X
	setSelectedVertexWorld(func(i int, sel vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		return cur.Add(delta)
	})

	// A quarter turn about Y takes local +X to world -Z, so world +X comes back
	// as local +Z: each corner should have moved in Z and be unchanged in X.
	want0 := ch.WorldToLocal(ch.LocalToWorld(before0).Add(delta))
	want1 := ch.WorldToLocal(ch.LocalToWorld(before1).Add(delta))
	if got := ch.Brushes[0].Verts[0]; !vecNear(got, want0, 1e-3) {
		t.Errorf("corner 0 = %v, want %v", got, want0)
	}
	if got := ch.Brushes[0].Verts[1]; !vecNear(got, want1, 1e-3) {
		t.Errorf("corner 1 = %v, want %v", got, want1)
	}

	// And reading back through vertexSelPos must reproduce the same world
	// position the write started from, closing the loop.
	got0, _ := vertexSelPos(Editor.SelectedVertices[0])
	want := ch.LocalToWorld(before0).Add(delta)
	if !vecNear(got0, want, 1e-3) {
		t.Errorf("read-back world position = %v, want %v", got0, want)
	}
}

// Snapping is done in the CHUNK's space, not the world's, because that is the
// grid a brush's corners are authored on — and for a rotated chunk the two are
// not the same grid.
func TestSetSelectedVertexLocalsSnapsInChunkSpace(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	defer saveEditorSelection()()
	Editor.level = placedRoomLevel()
	ch := &Editor.level.Chunks[0]
	ch.Brushes[0].Verts[0] = mgl32.Vec3{0.3, 0.7, 1.4}

	Editor.SelectedVertices = []vertexSel{{chunkIdx: 0, brushIdx: 0, vertIdx: 0}}
	setSelectedVertexLocals(func(_ int, _ vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		return snapVec3(cur, 0.5)
	})

	if got := ch.Brushes[0].Verts[0]; !vecNear(got, mgl32.Vec3{0.5, 0.5, 1.5}, 1e-4) {
		t.Errorf("snapped corner = %v, want the chunk-local grid point (0.5, 0.5, 1.5)", got)
	}
}
