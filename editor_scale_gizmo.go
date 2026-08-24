package main

// editor_scale_gizmo.go — the 3D viewport's scale gizmo.
//
// Three short arms plus a handle at the centre, all at the selection's centre.
// Dragging one moves every selected vertex toward or away from that centre —
// nothing is resized. A sector vertex has no size to change: it is a corner,
// and the only thing "scale the selection" can mean for a set of corners is how
// far apart they are. An axis arm spreads them along that world axis alone; the
// centre handle spreads them in all three at once.
//
// Handles, snapping and clamping are the model editor's (editor_model_scale.go)
// via scaleFactorBetween, so a handle pulled the same distance means the same
// thing in both viewports.

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// scaleGizmoOrigin returns the centre the selection spreads about: the middle
// of a vertex selection, or an entity's own position.
//
// As with rotate, the two mean different things — a vertex selection spreads
// its points apart, an entity rewrites its Scale and grows in place — and a
// spawn marker and a chunk have no size to change at all.
func scaleGizmoOrigin() (mgl32.Vec3, bool) {
	switch gizmoEffectiveKind() {
	case gizmoKindVertices:
		return selectedVerticesCenter()
	case gizmoKindEntity:
		if e := activeGizmoEntity(); e != nil {
			return e.Position, true
		}
	}
	return mgl32.Vec3{}, false
}

// pickScaleHandle returns the handle a click at (mx, my) grabs: 0..2 for an
// axis, modelScaleUniformAxis for the centre, or -1.
//
// The centre is tested first because all three arms start there, so every click
// near it is within zero pixels of all of them — and spreading in every
// direction is the more common intent when aiming at the middle of the widget.
func pickScaleHandle(mx, my int32) int {
	origin, ok := scaleGizmoOrigin()
	if !ok {
		return -1
	}
	projView := Projection.Mul4(Cam.Matrix)
	originScreen, ok := projectToScreen(projView, origin)
	if !ok {
		return -1
	}
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	if cursor.Sub(originScreen).Len() <= modelScaleUniformPx {
		return modelScaleUniformAxis
	}

	armLen := gizmoArmLength(origin) * modelScaleArmFrac
	best := -1
	bestD := gizmoPickPx
	for i, axis := range gizmoAxes {
		tip, ok := projectToScreen(projView, origin.Add(axis.Mul(armLen)))
		if !ok {
			continue
		}
		d := distToSegment2D(cursor.X(), cursor.Y(),
			originScreen.X(), originScreen.Y(), tip.X(), tip.Y())
		if d <= bestD {
			bestD = d
			best = i
		}
	}
	return best
}

// scaleReference returns the measure a scale factor is a ratio of.
//
// For an axis handle that is the cursor's position along the axis, so pulling
// the handle out from the centre spreads the selection and pushing it in draws
// it together. For the centre handle it is the cursor's distance from the
// origin on screen, since no single world axis describes that drag.
func scaleReference(mx, my int32, origin mgl32.Vec3, handle int) (float32, bool) {
	if handle == modelScaleUniformAxis {
		originScreen, ok := projectToScreen(Projection.Mul4(Cam.Matrix), origin)
		if !ok {
			return 0, false
		}
		return mgl32.Vec2{float32(mx), float32(my)}.Sub(originScreen).Len(), true
	}
	if handle < 0 || handle > 2 {
		return 0, false
	}
	return axisParamAtMouse(origin, gizmoAxes[handle], mx, my)
}

// scaleVector spreads a factor over the axes the handle drives.
func scaleVector(handle int, f float32) mgl32.Vec3 {
	if handle == modelScaleUniformAxis {
		return mgl32.Vec3{f, f, f}
	}
	out := mgl32.Vec3{1, 1, 1}
	if handle >= 0 && handle <= 2 {
		out[handle] = f
	}
	return out
}

// ---------- the drag ----------

// scaleGizmoMouseDown starts a scale if the click landed on a handle. As with
// the other two tools, a handle under the cursor consumes the click even when
// the grab is refused, so a near miss never throws the selection away.
func scaleGizmoMouseDown(t sdl.MouseButtonEvent) bool {
	if t.Button != sdl.ButtonLeft || gizmoSuppressed() {
		return false
	}
	handle := pickScaleHandle(t.X, t.Y)
	if handle < 0 {
		return false
	}
	origin, ok := scaleGizmoOrigin()
	if !ok {
		return true
	}
	ref, ok := scaleReference(t.X, t.Y, origin, handle)
	if !ok {
		return true
	}
	// Too near the centre to be a ratio of anything: the first motion event
	// would multiply by a huge number.
	if abs32(ref) < scaleRefMin {
		return true
	}

	// captureGizmoDragStart pushes the undo entry and snapshots whatever the
	// target scales from — the vertex positions, or the entity's own Scale.
	if !captureGizmoDragStart() {
		return true
	}
	Editor.GizmoDragging = true
	Editor.GizmoDragTool = editorToolScale
	Editor.GizmoDragAxis = handle
	Editor.GizmoDragStartS = ref
	Editor.GizmoDragOrigin = origin
	return true
}

// scaleGizmoMouseMove applies the drag, always from the mouse-down snapshot:
// multiplying a factor in on every motion event compounds exponentially.
func scaleGizmoMouseMove(mx, my int32) {
	origin := Editor.GizmoDragOrigin
	ref, ok := scaleReference(mx, my, origin, Editor.GizmoDragAxis)
	if !ok {
		return
	}
	applyScaleToSelection(origin, scaleVector(Editor.GizmoDragAxis,
		scaleFactorBetween(Editor.GizmoDragStartS, ref)))
}

// applyScaleToSelection moves every selected vertex toward or away from origin,
// or resizes the selected entity in place.
func applyScaleToSelection(origin, factors mgl32.Vec3) {
	if e := activeGizmoEntity(); e != nil {
		s := Editor.GizmoDragStartScale
		e.Scale = mgl32.Vec3{
			entityScaleClamp(s.X() * factors.X()),
			entityScaleClamp(s.Y() * factors.Y()),
			entityScaleClamp(s.Z() * factors.Z()),
		}
		return
	}
	setSelectedVertexWorld(scaleTransform(origin, factors))
}

// entityScaleMin is the smallest magnitude an entity axis can be scaled to.
// A zero scale collapses the model to nothing and makes its matrix singular,
// which the picker inverts — so a drag can approach flat but not reach it.
const entityScaleMin = float32(0.001)

// entityScaleClamp keeps a scale factor off zero without forcing its sign: a
// negative scale legitimately mirrors a model.
func entityScaleClamp(v float32) float32 {
	if v > -entityScaleMin && v < entityScaleMin {
		if v < 0 {
			return -entityScaleMin
		}
		return entityScaleMin
	}
	return v
}

// scaleTransform is the position map a scale drag applies: every selected
// vertex pushed out from (or pulled in toward) origin from where it was at
// mouse-down.
func scaleTransform(origin, factors mgl32.Vec3) func(int, vertexSel, mgl32.Vec3) mgl32.Vec3 {
	start := Editor.GizmoDragWorld
	return func(i int, sel vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		if i >= len(start) {
			return cur
		}
		return scalePointAbout(start[i], origin, factors)
	}
}

// ---------- drawing ----------

// renderScaleGizmo draws the three axis handles and the centre handle,
// brightening whichever is under the cursor or being dragged.
func renderScaleGizmo(mx, my int32) {
	origin, ok := scaleGizmoOrigin()
	if !ok {
		return
	}
	armLen := gizmoArmLength(origin) * modelScaleArmFrac

	hover := -1
	if editorGizmoDragging() {
		hover = Editor.GizmoDragAxis
	} else {
		hover = pickScaleHandle(mx, my)
	}

	yellow := mgl32.Vec3{1, 1, 0}
	for i, axis := range gizmoAxes {
		col := gizmoAxisColors[i]
		if i == hover {
			col = yellow
		}
		tip := origin.Add(axis.Mul(armLen))
		GizmoLine(origin, tip, col)
		gizmoBox(tip, modelScaleTipHalfPx, col)
	}

	// The centre handle. Neutral rather than any axis colour, because it is the
	// one that belongs to none of them.
	col := mgl32.Vec3{0.85, 0.85, 0.9}
	if hover == modelScaleUniformAxis {
		col = yellow
	}
	gizmoBox(origin, gizmoTipHalfPx*1.2, col)
}
