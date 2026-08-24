package main

// model_testhelpers_test.go — small comparisons the editor's tests share.
//
// The ModelDoc *fixtures* live in internal/modeltest, which more than one
// package imports. These two stay here: internal/world and internal/collision
// carry their own copies of the same three lines, because a test helper cannot
// cross a package boundary without becoming part of that package's API, and a
// float comparison is not worth exporting for.

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

// vecNear is vec3Close with the arguments in the order internal/world's tests
// use, kept so tests moved between the two read the same.
func vecNear(a, b mgl32.Vec3, tol float32) bool { return vec3Close(a, b, tol) }

// nearlyEqual reports whether two scalars agree within tol.
func nearlyEqual(a, b, tol float32) bool {
	d := a - b
	return d <= tol && d >= -tol
}
