package model

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// model_uv_test.go — the ModelDoc-level UV operations.
//
// The interactive slider session that drives them is editor state and lives
// in the root package, with its tests beside it.

// uvClose reports whether two UVs agree to within eps.
func uvClose(a, b mgl32.Vec2, eps float32) bool { return a.Sub(b).Len() <= eps }

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
