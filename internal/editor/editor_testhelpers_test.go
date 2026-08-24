package editor

// editor_testhelpers_test.go — small float comparisons the editor's tests share.
//
// internal/world and internal/collision carry the same few lines. A test helper
// cannot cross a package boundary without becoming part of that package's API,
// and a tolerance check is not worth exporting for.

import "github.com/go-gl/mathgl/mgl32"

// vec3Close reports whether two vectors agree to within eps on every axis.
func vec3Close(a, b mgl32.Vec3, eps float32) bool {
	for i := 0; i < 3; i++ {
		d := a[i] - b[i]
		if d < -eps || d > eps {
			return false
		}
	}
	return true
}

// vecNear is vec3Close under the name internal/world's tests use.
func vecNear(a, b mgl32.Vec3, tol float32) bool { return vec3Close(a, b, tol) }

// nearlyEqual reports whether two scalars agree within tol.
func nearlyEqual(a, b, tol float32) bool {
	d := a - b
	return d <= tol && d >= -tol
}
