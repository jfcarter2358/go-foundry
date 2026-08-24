package main

import (
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
	if !x.isIdentity() {
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
	m := NewCubeDoc()
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
	m := NewCubeDoc()
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
	if !modelUVEdit.Xform.isIdentity() {
		t.Errorf("transform survived a selection change: %+v", modelUVEdit.Xform)
	}
	// Face 0's edit is now permanent.
	if got := m.Faces[0].UVs[2]; !uvClose(got, mgl32.Vec2{2, 2}, 1e-5) {
		t.Errorf("face 0 UV = %v, want the applied (2,2)", got)
	}
}

func TestPlanarUnwrapFitsUnitSquare(t *testing.T) {
	m := NewCubeDoc()
	// Scramble the mapping first so the unwrap has something to repair.
	for i := range m.Faces[0].UVs {
		m.Faces[0].UVs[i] = mgl32.Vec2{7, -3}
	}
	m.ModelPlanarUnwrap([]int{0}, true)

	minU, minV := float32(1e9), float32(1e9)
	maxU, maxV := float32(-1e9), float32(-1e9)
	for _, uv := range m.Faces[0].UVs {
		minU, maxU = min(minU, uv.X()), max(maxU, uv.X())
		minV, maxV = min(minV, uv.Y()), max(maxV, uv.Y())
	}
	if minU < -1e-5 || minV < -1e-5 || maxU > 1+1e-5 || maxV > 1+1e-5 {
		t.Errorf("fitted UVs span %v..%v x %v..%v, want 0..1", minU, maxU, minV, maxV)
	}
	if maxU-minU < 0.99 || maxV-minV < 0.99 {
		t.Errorf("fitted UVs only span %v x %v, want the full square", maxU-minU, maxV-minV)
	}
}

func TestPlanarUnwrapKeepsWorldScale(t *testing.T) {
	// Two faces of different sizes must get proportional UV spans, which is
	// what keeps texel density even across a model.
	m := &ModelDoc{
		Verts: []mgl32.Vec3{
			{0, 0, 0}, {1, 0, 0}, {1, 0, 1}, {0, 0, 1},
			{5, 0, 0}, {7, 0, 0}, {7, 0, 2}, {5, 0, 2},
		},
		Faces: []ModelFace{
			{Verts: []int{0, 1, 2, 3}, UVs: make([]mgl32.Vec2, 4)},
			{Verts: []int{4, 5, 6, 7}, UVs: make([]mgl32.Vec2, 4)},
		},
	}
	m.ModelPlanarUnwrap([]int{0, 1}, false)

	span := func(fi int) float32 {
		minU, maxU := float32(1e9), float32(-1e9)
		for _, uv := range m.Faces[fi].UVs {
			minU, maxU = min(minU, uv.X()), max(maxU, uv.X())
		}
		return maxU - minU
	}
	// The second face is twice as wide, so it must get twice the UV span.
	if ratio := span(1) / span(0); ratio < 1.99 || ratio > 2.01 {
		t.Errorf("UV span ratio = %v, want 2 — world scale was not preserved", ratio)
	}
}

func TestFlipUVMirrorsAboutItsOwnRange(t *testing.T) {
	// Flipping about a hardcoded 0.5 would move UVs that live outside the unit
	// square; mirroring about the face's own range does not.
	m := &ModelDoc{
		Verts: []mgl32.Vec3{{0, 0, 0}, {1, 0, 0}, {1, 0, 1}, {0, 0, 1}},
		Faces: []ModelFace{{
			Verts: []int{0, 1, 2, 3},
			UVs:   []mgl32.Vec2{{2, 0}, {4, 0}, {4, 1}, {2, 1}},
		}},
	}
	m.ModelFlipUV([]int{0}, true, false)
	want := []mgl32.Vec2{{4, 0}, {2, 0}, {2, 1}, {4, 1}}
	for i, uv := range m.Faces[0].UVs {
		if !uvClose(uv, want[i], 1e-5) {
			t.Errorf("corner %d = %v, want %v", i, uv, want[i])
		}
	}

	// Flipping neither axis must do nothing at all.
	before := append([]mgl32.Vec2(nil), m.Faces[0].UVs...)
	m.ModelFlipUV([]int{0}, false, false)
	for i, uv := range m.Faces[0].UVs {
		if !uvClose(uv, before[i], 1e-6) {
			t.Errorf("no-op flip changed corner %d", i)
		}
	}
}
