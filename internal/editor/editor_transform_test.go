package editor

// editor_transform_test.go — the seam every gizmo (2D and 3D) shares: a
// selected vertex's world position (vertexSelPos) and its write-back
// (writeSelectedVertexWorld, in editor_sel_transform.go) have to agree with
// each other and with the chunk's own ModelMatrix, or a chunk with a live
// Origin/Rotation/Scale would edit at the wrong place from either view.

import (
	"math"
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// placedRoomLevel builds a level with one chunk placed away from the world
// origin and rotated, with a single sector so its points have floor/ceiling
// vertex handles to select.
func placedRoomLevel() *world.Level {
	ch := world.Chunk{
		ID:       "room",
		Origin:   mgl32.Vec3{100, 5, -50},
		Rotation: float32(math.Pi / 2),
		Scale:    1,
		Points:   []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}},
		Sectors: []world.Sector{{
			ID:           "s0",
			PointIndices: []int{0, 1, 2, 3},
			Floor:        world.Floor{Enabled: true, Height: 0, VertexOffsets: []float32{0, 0, 0, 0}},
			Ceiling:      world.Ceiling{Enabled: true, Height: 8, VertexOffsets: []float32{0, 0, 0, 0}},
		}},
	}
	return &world.Level{Chunks: []world.Chunk{ch}}
}

func TestVertexSelPosMatchesChunkModelMatrix(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	restore := saveEditorSelection()
	defer restore()
	Editor.level = placedRoomLevel()

	sel := vertexSel{chunkIdx: 0, sectorIdx: 0, vertIdx: 0, pointIdx: 0, isFloor: true}
	got, ok := vertexSelPos(sel)
	if !ok {
		t.Fatal("vertexSelPos reported no position for a valid selection")
	}

	ch := &Editor.level.Chunks[0]
	local := ch.Points[0] // {0,0}, floor height 0
	want := ch.LocalToWorld(mgl32.Vec3{local.X(), 0, local.Y()})
	if !vecNear(got, want, 1e-4) {
		t.Errorf("vertexSelPos = %v, want %v (ch.LocalToWorld of the same local point)", got, want)
	}
}

func TestSetSelectedVertexWorldWritesBackToLocalSpace(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	restore := saveEditorSelection()
	defer restore()
	Editor.level = placedRoomLevel()
	ch := &Editor.level.Chunks[0]

	// Both floor corners at chunk-local (0,0) and (10,0) selected — moving
	// them by a world-space delta and writing back must land each at the
	// correct new *local* coordinate once the chunk's rotation is undone.
	Editor.SelectedVertices = []vertexSel{
		{chunkIdx: 0, sectorIdx: 0, vertIdx: 0, pointIdx: 0, isFloor: true},
		{chunkIdx: 0, sectorIdx: 0, vertIdx: 1, pointIdx: 1, isFloor: true},
	}

	delta := mgl32.Vec3{1, 0, 0} // world +X
	setSelectedVertexWorld(func(i int, sel vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		return cur.Add(delta)
	})

	// Local +Z is world -X's opposite (90° rotation: local +X -> world -Z, so
	// world +X -> local -Z); each point should have moved by that much in Z
	// and be unchanged in X.
	wantP0 := ch.WorldToLocal(mgl32.Vec3{100 + 1, 5, -50})
	wantP1 := ch.WorldToLocal(ch.LocalToWorld(mgl32.Vec3{10, 0, 0}).Add(delta))

	if got := ch.Points[0]; !approxVec2(got, mgl32.Vec2{wantP0.X(), wantP0.Z()}, 1e-3) {
		t.Errorf("point 0 = %v, want %v", got, mgl32.Vec2{wantP0.X(), wantP0.Z()})
	}
	if got := ch.Points[1]; !approxVec2(got, mgl32.Vec2{wantP1.X(), wantP1.Z()}, 1e-3) {
		t.Errorf("point 1 = %v, want %v", got, mgl32.Vec2{wantP1.X(), wantP1.Z()})
	}

	// And reading back through vertexSelPos must reproduce the same world
	// position the write started from, closing the loop.
	got0, _ := vertexSelPos(Editor.SelectedVertices[0])
	want0 := mgl32.Vec3{101, 5, -50}
	if !vecNear(got0, want0, 1e-3) {
		t.Errorf("read-back world position = %v, want %v", got0, want0)
	}
}

func approxVec2(got, want mgl32.Vec2, eps float32) bool {
	dx := got.X() - want.X()
	dy := got.Y() - want.Y()
	return dx < eps && dx > -eps && dy < eps && dy > -eps
}
