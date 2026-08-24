package main

import (
	"foundry/internal/model"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func uvClose(a, b mgl32.Vec2, eps float32) bool { return a.Sub(b).Len() <= eps }

func TestUVTransformOrderMatchesWalls(t *testing.T) {
	// Scale, then rotate, then offset — the same order applyUVTransform uses,
	// so a texture behaves the same on a model face as on a wall.
	x := modelUVTransform{Offset: mgl32.Vec2{1, 2}, Scale: mgl32.Vec2{2, 3}, Rotation: 0}
	if got := x.apply(mgl32.Vec2{1, 1}); !uvClose(got, mgl32.Vec2{3, 5}, 1e-5) {
		t.Errorf("scale+offset gave %v, want (3,5)", got)
	}

	// Cross-check against the engine's own implementation on the same input.
	verts := [][]float32{{0, 0, 0, 1, 1}}
	applyUVTransform(verts, mgl32.Vec2{1, 2}, mgl32.Vec2{2, 3}, 0)
	if verts[0][3] != 3 || verts[0][4] != 5 {
		t.Errorf("applyUVTransform disagrees: %v", verts[0][3:])
	}
}

func TestUVTransformIdentityIsANoop(t *testing.T) {
	x := identityUVTransform()
	if !x.IsIdentity() {
		t.Error("identityUVTransform is not identity")
	}
	in := mgl32.Vec2{0.3, 0.7}
	if got := x.apply(in); !uvClose(got, in, 1e-6) {
		t.Errorf("identity transform moved %v to %v", in, got)
	}
	// A zero scale means "unset", not "collapse to a point" — the same
	// convention Wall.UVScale uses.
	zero := modelUVTransform{}
	if got := zero.apply(in); !uvClose(got, in, 1e-6) {
		t.Errorf("zero scale collapsed %v to %v", in, got)
	}
}

func TestUVEditReappliesFromBaseNotCompounding(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	prevU, prevR := modelUndoStack, modelRedoStack
	modelUndoStack, modelRedoStack = boundedStack[modelSnapshot]{}, boundedStack[modelSnapshot]{}
	t.Cleanup(func() { modelUndoStack, modelRedoStack = prevU, prevR })
	modelUVInvalidate()

	faces := []int{0}
	modelUVSync(faces)

	// Dragging a scale slider re-applies to the captured base each frame. If it
	// compounded, scaling to 2 and then to 3 would land on 6.
	modelUVEdit.Xform.Scale = mgl32.Vec2{2, 2}
	modelUVApply(faces)
	modelUVEdit.Xform.Scale = mgl32.Vec2{3, 3}
	modelUVApply(faces)

	// Corner 2 of the cube's unit UVs is (1,1).
	if got := m.Faces[0].UVs[2]; !uvClose(got, mgl32.Vec2{3, 3}, 1e-5) {
		t.Errorf("UV = %v, want (3,3) — the transform compounded", got)
	}

	// And the whole drag is one undo entry, not one per frame.
	if n := modelUndoStack.len(); n != 1 {
		t.Errorf("undo stack has %d entries for one drag, want 1", n)
	}
}

func TestUVSyncRecapturesOnSelectionChange(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	prevU, prevR := modelUndoStack, modelRedoStack
	modelUndoStack, modelRedoStack = boundedStack[modelSnapshot]{}, boundedStack[modelSnapshot]{}
	t.Cleanup(func() { modelUndoStack, modelRedoStack = prevU, prevR })
	modelUVInvalidate()

	modelUVSync([]int{0})
	modelUVEdit.Xform.Scale = mgl32.Vec2{2, 2}
	modelUVApply([]int{0})

	// Moving to a different face must reset the transform, or the new face
	// would silently inherit the previous face's scale.
	modelUVSync([]int{1})
	if !modelUVEdit.Xform.IsIdentity() {
		t.Errorf("transform survived a selection change: %+v", modelUVEdit.Xform)
	}
	// Face 0's edit is now permanent.
	if got := m.Faces[0].UVs[2]; !uvClose(got, mgl32.Vec2{2, 2}, 1e-5) {
		t.Errorf("face 0 UV = %v, want the applied (2,2)", got)
	}
}
