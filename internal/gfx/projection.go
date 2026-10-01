package gfx

// projection.go — the camera's lens.
//
// The projection used to be built in two places in the app package, both with
// a hard-coded 45°, and only when the window changed size. A scope zooming in
// needs it to change during play, so the field of view is a variable here and
// the one function that turns it into a matrix is too.

import (
	"foundry/internal/platform"

	"github.com/go-gl/gl/v4.3-core/gl"
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

const projectionNear = float32(0.1)

// DefaultFarPlane is how far the camera sees, in world units, when the level
// has not asked for another distance (world.LevelViewDistance).
const DefaultFarPlane = float32(1000)

// FarPlane is the far clip distance the next SyncProjection builds with. The
// frame loop writes the current level's `view_distance:` here before syncing,
// so a large outdoor level can see further than a corridor needs to.
//
// The near plane stays where it is: the player can stand against a wall, so
// pushing it out would slice that wall open. The cost of a long view is depth
// precision at range, which is why the default is not simply "very far".
var FarPlane = DefaultFarPlane

// What Projection was last built from, so SyncProjection only rebuilds when
// one of them has moved.
var builtFOV, builtAspect, builtFar float32

// RebuildProjection builds Projection from FieldOfView, FarPlane and the window's size,
// and uploads it to the world program — which must be the program in use.
func RebuildProjection() {
	h := platform.WinHeight
	if h < 1 {
		h = 1
	}
	aspect := float32(platform.WinWidth) / float32(h)
	fov := clampFOV(FieldOfView)
	far := farPlane()
	Projection = mgl32.Perspective(mgl32.DegToRad(fov), aspect, projectionNear, far)
	builtFOV, builtAspect, builtFar = fov, aspect, far
	gl.UniformMatrix4fv(ProjectionUniform, 1, false, &Projection[0])
}

// SyncProjection rebuilds the projection when the field of view or far plane has changed
// since it was last built. Called once a frame with the world program bound,
// before anything reads Projection; free when nothing changed.
func SyncProjection() {
	h := platform.WinHeight
	if h < 1 {
		h = 1
	}
	aspect := float32(platform.WinWidth) / float32(h)
	if clampFOV(FieldOfView) == builtFOV && aspect == builtAspect && farPlane() == builtFar {
		return
	}
	RebuildProjection()
}

// farPlane is FarPlane kept strictly beyond the near plane, so a zero or
// garbage value cannot build a degenerate matrix.
func farPlane() float32 {
	if FarPlane <= projectionNear*10 {
		return DefaultFarPlane
	}
	return FarPlane
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

// ---------- the viewmodel's own lens ----------
//
// The hands are not in the world, and they should not be seen through the
// world's lens. Aiming down a rifle's sights narrows FieldOfView to 30° or
// less, and drawing the viewmodel through that same matrix magnifies the arms
// by exactly as much as it magnifies the target: they swell across the side of
// the screen and start pushing through the near plane. Every first-person game
// solves this the same way, by giving the viewmodel a fixed lens of its own.
//
// Nothing about the alignment of a gun's sights depends on which lens is used.
// The ADS pose puts the sight line through the eye point along the view axis,
// and a line through the eye projects to the middle of the screen at any field
// of view — so a script's solved sight offsets stay solved.

// DefaultViewmodelFieldOfView is the vertical field of view, in degrees, the
// viewmodel is drawn with. Wider than the world's default, which is what keeps
// hands held close to the eye from filling the frame.
const DefaultViewmodelFieldOfView = float32(55)

// ViewmodelFieldOfView is the lens RenderViewmodel draws through.
// `@level.prop.viewmodel_fov` is the script's handle on it; ScriptRuntimeReset
// puts the default back, so a scope held when play ends does not follow the
// author into the editor.
var ViewmodelFieldOfView = DefaultViewmodelFieldOfView

// viewmodelNear is nearer than the world's near plane by a factor of ten. The
// world has nothing within 10cm of the eye that is worth the depth precision;
// a pair of hands is *entirely* within that distance, and at the world's own
// near plane the parts of them closest to the camera — a thumb over the sights,
// a raised elbow while reloading — are sliced open.
const viewmodelNear = float32(0.01)

// ViewmodelProjection is the perspective matrix the viewmodel pass draws with:
// its own field of view, its own near plane, the window's real aspect.
//
// Built on demand rather than cached like Projection: it is one matrix a frame,
// against a pass that clears the depth buffer and draws a handful of meshes.
func ViewmodelProjection() mgl32.Mat4 {
	h := platform.WinHeight
	if h < 1 {
		h = 1
	}
	aspect := float32(platform.WinWidth) / float32(h)
	return mgl32.Perspective(mgl32.DegToRad(clampFOV(ViewmodelFieldOfView)), aspect, viewmodelNear, DefaultFarPlane)
}

// UploadProjection sends a projection matrix to the world program, which must
// be the program in use. A pass that draws through a lens of its own uploads
// it, draws, and uploads gfx.Projection back — leaving the uniform holding
// something other than Projection would silently bend every pass after it.
func UploadProjection(m mgl32.Mat4) {
	gl.UniformMatrix4fv(ProjectionUniform, 1, false, &m[0])
}
