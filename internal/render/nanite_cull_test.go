package render

// nanite_cull_test.go — the parts of the GPU cull that are plain arithmetic.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// frustumEye reads where a light looks from off its frustum alone: the
// position for a perspective one (a point light's face), the direction its
// rays run for an orthographic one (a sun cascade).
func TestFrustumEyeIsThePointOrTheDirectionTheViewLooksFrom(t *testing.T) {
	eye := mgl32.Vec3{3, -2, 7}
	target := mgl32.Vec3{-1, 0.5, 2}
	view := mgl32.LookAtV(eye, target, mgl32.Vec3{0, 1, 0})

	got, ok := frustumEye(ExtractFrustum(mgl32.Perspective(mgl32.DegToRad(90), 1, 0.1, 50).Mul4(view)))
	if !ok || got[3] != 1 || got.Vec3().Sub(eye).Len() > 1e-3 {
		t.Errorf("perspective: got %v (ok %v), want the point %v", got, ok, eye)
	}

	got, ok = frustumEye(ExtractFrustum(mgl32.Ortho(-5, 5, -3, 3, 0.1, 40).Mul4(view)))
	want := target.Sub(eye).Normalize()
	if !ok || got[3] != 0 || got.Vec3().Sub(want).Len() > 1e-4 {
		t.Errorf("orthographic: got %v (ok %v), want the direction %v", got, ok, want)
	}

	if _, ok := frustumEye(Frustum{}); ok {
		t.Error("an empty frustum gave an eye")
	}
}
