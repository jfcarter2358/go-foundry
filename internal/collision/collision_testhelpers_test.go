package collision

// collision_testhelpers_test.go — helpers shared by this package's tests.
//
// internal/world's tests carry the same vecNear. A test helper cannot cross a
// package boundary without becoming part of that package's API, and a
// three-line float comparison is not worth exporting for.

import "github.com/go-gl/mathgl/mgl32"

// vecNear reports whether two vectors agree within tol on every axis.
func vecNear(a, b mgl32.Vec3, tol float32) bool {
	d := a.Sub(b)
	return mgl32.Abs(d.X()) <= tol && mgl32.Abs(d.Y()) <= tol && mgl32.Abs(d.Z()) <= tol
}
