package gfx

// projection.go — the camera's lens.
//
// The projection used to be built in two places in the app package, both with
// a hard-coded 45°, and only when the window changed size. A scope zooming in
// needs it to change during play, so the field of view is a variable here and
// the one function that turns it into a matrix is too.

import (
	"foundry/internal/platform"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// DefaultFieldOfView is the vertical field of view, in degrees, the engine
// draws with when nothing has asked for another.
const DefaultFieldOfView = float32(45)

// MinFieldOfView and MaxFieldOfView bound what FieldOfView may be set to. Past
// either end the perspective divide stops describing a lens anyone would use.
const (
	MinFieldOfView = float32(5)
	MaxFieldOfView = float32(120)
)

// FieldOfView is the vertical field of view the next SyncProjection builds
// with. Write it and let the frame loop pick it up; `@level.prop.camera_fov`
// is the script's handle on it.
var FieldOfView = DefaultFieldOfView

const (
	projectionNear = float32(0.1)
	projectionFar  = float32(1000)
)

// What Projection was last built from, so SyncProjection only rebuilds when
// one of them has moved.
var builtFOV, builtAspect float32

// RebuildProjection builds Projection from FieldOfView and the window's size,
// and uploads it to the world program — which must be the program in use.
func RebuildProjection() {
	h := platform.WinHeight
	if h < 1 {
		h = 1
	}
	aspect := float32(platform.WinWidth) / float32(h)
	fov := clampFOV(FieldOfView)
	Projection = mgl32.Perspective(mgl32.DegToRad(fov), aspect, projectionNear, projectionFar)
	builtFOV, builtAspect = fov, aspect
	gl.UniformMatrix4fv(ProjectionUniform, 1, false, &Projection[0])
}

// SyncProjection rebuilds the projection when the field of view has changed
// since it was last built. Called once a frame with the world program bound,
// before anything reads Projection; free when nothing changed.
func SyncProjection() {
	h := platform.WinHeight
	if h < 1 {
		h = 1
	}
	aspect := float32(platform.WinWidth) / float32(h)
	if clampFOV(FieldOfView) == builtFOV && aspect == builtAspect {
		return
	}
	RebuildProjection()
}

func clampFOV(f float32) float32 {
	if f < MinFieldOfView {
		return MinFieldOfView
	}
	if f > MaxFieldOfView {
		return MaxFieldOfView
	}
	return f
}
