package editor

// editor_particle_render.go — the particle editor's 3D preview.
//
// The scene is three things: a grid to judge scale against, an optional ground
// plane so a collision layer has something to bounce off, and the effect itself.
//
// The effect is drawn through render.DrawSpriteInstances — the same instanced
// pass the running engine uses, not a second implementation. That is the whole
// point of the preview: an effect that looks right here looks right in the
// level, because the code that put it on screen was the same code.
//
// GL object ownership: the sprite pass's buffers and programs were created in
// the main window's context and are shared here, because uiwin creates every
// secondary window's context in a share group with the main one (see
// NewUIWindow). Its VAO is NOT shared — no VAO ever is — so this window binds
// one of its own through render.SpriteVAOOverride, exactly as it does for the
// gizmo through GizmoVAOOverride.

import (
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/particle"
	"foundry/internal/render"

	"github.com/go-gl/mathgl/mgl32"
)

var (
	particleGridColor   = mgl32.Vec3{0.28, 0.28, 0.32}
	particleAxisX       = mgl32.Vec3{0.55, 0.25, 0.25}
	particleAxisZ       = mgl32.Vec3{0.25, 0.25, 0.55}
	particleGroundColor = mgl32.Vec3{0.35, 0.35, 0.38}
	particleShapeColor  = mgl32.Vec3{0.3, 1, 0.5}
)

// drawParticleGrid draws a unit grid on the ground plane, with the two axes
// picked out — the scale reference an effect is dialled in against.
func drawParticleGrid() {
	const half = 5
	for i := -half; i <= half; i++ {
		f := float32(i)
		xCol, zCol := particleGridColor, particleGridColor
		if i == 0 {
			xCol, zCol = particleAxisZ, particleAxisX
		}
		gizmo.GizmoLine(mgl32.Vec3{f, 0, -half}, mgl32.Vec3{f, 0, half}, xCol)
		gizmo.GizmoLine(mgl32.Vec3{-half, 0, f}, mgl32.Vec3{half, 0, f}, zCol)
	}
}

// drawParticleGround outlines the collision plane.
//
// It is not merely decoration: particle.Collide is pointed at a flat floor at
// y = 0 while this window is up (see particlePreviewCollider), so a bounce
// layer can be authored without a level open. Drawing the plane is what makes
// that floor visible rather than a surprise.
func drawParticleGround() {
	const half = 5
	c := particleGroundColor
	gizmo.GizmoLine(mgl32.Vec3{-half, 0, -half}, mgl32.Vec3{half, 0, -half}, c)
	gizmo.GizmoLine(mgl32.Vec3{half, 0, -half}, mgl32.Vec3{half, 0, half}, c)
	gizmo.GizmoLine(mgl32.Vec3{half, 0, half}, mgl32.Vec3{-half, 0, half}, c)
	gizmo.GizmoLine(mgl32.Vec3{-half, 0, half}, mgl32.Vec3{-half, 0, -half}, c)
}

// drawParticleShapeOutline outlines the active layer's emission region, which
// is the only way to aim a cone by eye.
func drawParticleShapeOutline() {
	l := particleActive()
	if l == nil {
		return
	}
	id := func(p mgl32.Vec3) mgl32.Vec3 { return p }
	switch l.Shape {
	case particle.ShapeSphere, particle.ShapeHemisphere:
		gizmo.GizmoSphere(mgl32.Vec3{}, l.Radius, particleShapeColor)
	case particle.ShapeDisc:
		ring(id, l.Radius, 0, particleShapeColor, false)
	case particle.ShapeCone:
		ring(id, l.Radius, 0, particleShapeColor, false)
		drawConeOutline(id, l.Radius, l.Angle)
	case particle.ShapeBox:
		drawBoxOutline(id, l.BoxHalf)
	case particle.ShapePoint:
		const t = 0.08
		gizmo.GizmoLine(mgl32.Vec3{-t, 0, 0}, mgl32.Vec3{t, 0, 0}, particleShapeColor)
		gizmo.GizmoLine(mgl32.Vec3{0, -t, 0}, mgl32.Vec3{0, t, 0}, particleShapeColor)
		gizmo.GizmoLine(mgl32.Vec3{0, 0, -t}, mgl32.Vec3{0, 0, t}, particleShapeColor)
	}
}

// particlePreviewFill is the staging buffer the preview fills into, reused so a
// frame of preview allocates nothing.
var particlePreviewFill []particle.Instance

// previewDraw draws every visible layer through the engine's own sprite pass.
//
// Layers are drawn in document order rather than sorted against each other:
// order is the author's, which is what the layer list's move buttons are for,
// and it is the only ordering that makes a smoke plume reliably sit behind the
// flame it rises from.
func previewDraw(projView mgl32.Mat4) {
	if particleDoc == nil || len(particleSystems) == 0 || particleEditorWindow == nil {
		return
	}
	camPos := particleCam.position()

	// This window's own vertex array, for the duration of this frame. A VAO is
	// never shared between GL contexts — the buffers and programs behind it
	// are, which is why only this one line is needed.
	render.SpriteVAOOverride = particleEditorWindow.SpriteVAO
	defer func() { render.SpriteVAOOverride = 0 }()

	for i, s := range particleSystems {
		if !particleLayerVisible(i) || s.Count() == 0 {
			continue
		}
		n := s.Count()
		if cap(particlePreviewFill) < n {
			particlePreviewFill = make([]particle.Instance, n)
		}
		buf := particlePreviewFill[:n]
		l := particleDoc.Layers[i]
		written := s.Fill(buf, camPos, l.Blend == gfx.SpriteBlendAlpha)
		if written == 0 {
			continue
		}
		render.DrawSpriteInstances(buf[:written], l.Texture, l.Blend, l.Billboard, projView, camPos)
	}
}

// ---------- the preview's floor ----------

// particlePreviewGroundY is where the preview's collision plane sits: the
// ground the grid is drawn on.
const particlePreviewGroundY = float32(0)

// withPreviewCollider runs fn with particle.Collide pointed at a flat floor at
// y = 0 instead of at the level.
//
// The seam is a package global, which is what makes this a swap rather than a
// parameter — but the swap is contained to one call and restored on the way
// out, so the main window's next frame sees the level collider it installed.
// Without it a bounce layer could only be authored with a level open, and would
// bounce off whatever happened to be under the emitter in it.
func withPreviewCollider(fn func()) {
	prev := particle.Collide
	particle.Collide = previewGroundCollide
	defer func() { particle.Collide = prev }()
	fn()
}

// previewGroundCollide is a flat floor at particlePreviewGroundY.
func previewGroundCollide(from, to mgl32.Vec3, r float32) (mgl32.Vec3, mgl32.Vec3, bool) {
	if !particleShowGround || to.Y()-r > particlePreviewGroundY {
		return to, mgl32.Vec3{}, false
	}
	return mgl32.Vec3{to.X(), particlePreviewGroundY + r, to.Z()}, mgl32.Vec3{0, 1, 0}, true
}
