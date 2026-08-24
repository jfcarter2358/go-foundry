package main

// editor_scale_gizmo.go — the model editor's scale gizmo, on a model placed
// into a level.
//
// A model imported from the catalog (editor_model_import.go) becomes an Entity
// whose Scale the render path already honours, but the only way to change that
// scale was to type three numbers into the side panel. The model editor has had
// a proper scale widget since editor_model_scale.go, so this is that widget
// again rather than a second one: the arm proportions, the uniform handle, the
// snapping quantum, the floor a drag clamps to and the "apply absolutely from
// the mouse-down snapshot" rule all come straight from there, and the maths is
// literally the same functions.
//
// What differs is what a drag writes. In the model editor a scale moves
// vertices; here the mesh is a shared asset that other placements of the same
// model must not be dragged along with, so the drag writes the entity's Scale
// instead and the mesh on disk is never touched.
//
// The move and scale gizmos are separate tools, toggled from the toolbar or
// with G / C, for the same reason they are in the model editor: their handles
// would otherwise sit on top of each other and every click near an arm would be
// a coin toss.

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// editorGizmoTools are the tools the 3D viewport offers, in toolbar order.
// Rotate is the model editor's alone: an entity's rotation is authored in the
// side panel, and there is no ring widget here to conflict with.
var editorGizmoTools = []modelGizmoToolKind{modelToolMove, modelToolScale}

// editorGizmoTool is the tool the 3D viewport is currently showing.
var editorGizmoTool = modelToolMove

// editorSetGizmoTool switches tools, cancelling any drag in progress so a
// half-finished move cannot be finished as a scale.
func editorSetGizmoTool(tool modelGizmoToolKind) {
	if tool != modelToolMove && tool != modelToolScale || editorGizmoTool == tool {
		return
	}
	editorGizmoMouseUp()
	editorGizmoTool = tool
}

// editorScaleTarget returns the entity the scale gizmo would act on, or nil.
//
// Only entities scale here. A sector's shape is its points and its heights, a
// primitive's is its own geometry — those are edited where they live — so the
// scale tool has nothing to grab unless a placed model is selected.
func editorScaleTarget() *Entity {
	if gizmoEffectiveKind() != gizmoKindEntity {
		return nil
	}
	lvl := Editor.level
	if lvl == nil || Editor.ActiveEntityIdx < 0 || Editor.ActiveEntityIdx >= len(lvl.Entities) {
		return nil
	}
	return &lvl.Entities[Editor.ActiveEntityIdx]
}

// editorScaleGizmoActive reports whether the scale gizmo is what the viewport
// draws and what a click in it grabs. With the tool armed but nothing scalable
// selected, the move gizmo stays on screen rather than leaving the selection
// with no handles at all.
func editorScaleGizmoActive() bool {
	return editorGizmoTool == modelToolScale && editorScaleTarget() != nil
}

// entityScale reads an entity's scale with the zero value read as identity, the
// way Entity.ModelMatrix does — an entity that predates the field, or one
// loaded from a level that never wrote it, scales from 1 rather than from 0.
func entityScale(e *Entity) mgl32.Vec3 {
	if e.Scale == (mgl32.Vec3{}) {
		return mgl32.Vec3{1, 1, 1}
	}
	return e.Scale
}

// editorScaleAxes returns the directions the three axis handles drag along: the
// entity's own axes, in world space.
//
// They are the entity's and not the world's because Entity.ModelMatrix applies
// the scale before the rotation, so a component of Scale stretches the model
// along its own axis however it is turned. Handles drawn on the world axes
// would stretch a rotated model in a direction they do not point.
// There is no world/local switch here, the way the model editor has one
// (editor_model_local_axes.go): a world-axis stretch of a rotated entity is not
// expressible as a TRS scale at all, so the local frame is not a preference
// here but the only frame the value being edited has.
func editorScaleAxes(e *Entity) [3]mgl32.Vec3 {
	if e == nil || e.Rotation == (mgl32.Vec3{}) {
		return worldBasis()
	}
	// Same composition order as ModelMatrix, so column i is where the model's
	// local axis i ends up.
	r := mgl32.HomogRotate3DY(e.Rotation.Y()).
		Mul4(mgl32.HomogRotate3DX(e.Rotation.X())).
		Mul4(mgl32.HomogRotate3DZ(e.Rotation.Z()))
	var out [3]mgl32.Vec3
	for i := 0; i < 3; i++ {
		c := r.Col(i)
		v := mgl32.Vec3{c.X(), c.Y(), c.Z()}
		if v.Len() < 1e-6 {
			return worldBasis()
		}
		out[i] = v.Normalize()
	}
	return out
}

// editorPickScaleHandle returns the handle a click grabs: 0..2 for an axis,
// modelScaleUniformAxis for the centre, or -1.
//
// The centre is tested first for the same reason modelPickScaleHandle does it:
// all three arms start there, so every click near the middle is within zero
// pixels of all of them, and uniform scaling is the likelier intent anyway.
func editorPickScaleHandle(mx, my int32) int {
	e := editorScaleTarget()
	if e == nil {
		return -1
	}
	origin, ok := translateGizmoOrigin()
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
	axes := editorScaleAxes(e)
	best := -1
	bestD := gizmoPickPx
	for i, axis := range axes {
		tip, ok := projectToScreen(projView, origin.Add(axis.Mul(armLen)))
		if !ok {
			continue
		}
		d := distToSegment2D(float32(mx), float32(my),
			originScreen.X(), originScreen.Y(), tip.X(), tip.Y())
		if d <= bestD {
			bestD = d
			best = i
		}
	}
	return best
}

// editorScaleReference returns the measure a scale factor is a ratio of — the
// cursor's position along the grabbed axis, or its distance from the centre on
// screen for the uniform handle. Same split as modelScaleReference.
func editorScaleReference(mx, my int32, origin mgl32.Vec3, axes [3]mgl32.Vec3, handle int) (float32, bool) {
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
	rayO, rayD := screenRay(mx, my)
	return axisParamForRay(rayO, rayD, origin, axes[handle])
}

// editorScaleApply returns start scaled by the per-axis factors in f, with no
// component allowed past modelScaleMin. Scaling through zero would mirror the
// model and invert the winding of every face, which is never what a drag that
// only asked for "smaller" meant.
func editorScaleApply(start, f mgl32.Vec3) mgl32.Vec3 {
	out := mgl32.Vec3{start.X() * f.X(), start.Y() * f.Y(), start.Z() * f.Z()}
	for i := 0; i < 3; i++ {
		if out[i] < modelScaleMin {
			out[i] = modelScaleMin
		}
	}
	return out
}

// ---------- the drag ----------

// editorScaleMouseDown starts a scale drag if the click grabbed a handle.
// Returns true when the click was consumed.
//
// A handle under the cursor consumes the click even when the drag cannot start,
// for the same reason the translate gizmo does it: the handles are drawn over
// everything, so a click aimed at one must never fall through to picking and
// throw away the selection it exists to scale.
func editorScaleMouseDown(t sdl.MouseButtonEvent) bool {
	if t.Button != sdl.ButtonLeft || gizmoSuppressed() {
		return false
	}
	e := editorScaleTarget()
	if e == nil {
		return false
	}
	handle := editorPickScaleHandle(t.X, t.Y)
	if handle < 0 {
		return false
	}
	origin, ok := translateGizmoOrigin()
	if !ok {
		return true
	}
	axes := editorScaleAxes(e)
	ref, ok := editorScaleReference(t.X, t.Y, origin, axes, handle)
	if !ok || abs32(ref) < scaleRefMin {
		return true
	}

	// One undo entry for the whole drag, as the translate gizmo does.
	EditorPushUndo()

	Editor.GizmoDragging = true
	Editor.GizmoDragTool = modelToolScale
	Editor.GizmoDragAxis = handle
	Editor.GizmoDragStartS = ref
	Editor.GizmoDragOrigin = origin
	Editor.GizmoScaleAxes = axes
	Editor.GizmoDragStartScale = entityScale(e)
	return true
}

// editorScaleMouseMove applies the scale, always from the mouse-down snapshot —
// composing a factor per motion event would compound exponentially over a drag.
func editorScaleMouseMove(mx, my int32) {
	if !Editor.GizmoDragging || Editor.GizmoDragTool != modelToolScale {
		return
	}
	e := editorScaleTarget()
	if e == nil {
		return
	}
	ref, ok := editorScaleReference(mx, my, Editor.GizmoDragOrigin,
		Editor.GizmoScaleAxes, Editor.GizmoDragAxis)
	if !ok {
		return
	}
	f := modelScaleVector(Editor.GizmoDragAxis, scaleFactorBetween(Editor.GizmoDragStartS, ref))
	e.Scale = editorScaleApply(Editor.GizmoDragStartScale, f)
}

// ---------- dispatch ----------

// editorGizmoDragging reports whether a gizmo drag — of either tool — is in
// progress. A drag owns the mouse until release.
func editorGizmoDragging() bool { return Editor.GizmoDragging }

// editorGizmoMouseDown routes a click to whichever gizmo is on screen. The
// tools never share a click: the arms of the one that is not drawn must not be
// clickable, so a miss on the scale handles stays a miss rather than falling
// through to a translate arm nobody can see.
func editorGizmoMouseDown(t sdl.MouseButtonEvent) bool {
	if editorScaleGizmoActive() {
		return editorScaleMouseDown(t)
	}
	return translateGizmoMouseDown(t)
}

// editorGizmoMouseMove hands motion to the tool that owns the drag — recorded
// at mouse-down, so switching tools mid-drag cannot reinterpret it.
func editorGizmoMouseMove(mx, my int32) {
	if Editor.GizmoDragTool == modelToolScale {
		editorScaleMouseMove(mx, my)
		return
	}
	translateGizmoMouseMove(mx, my)
}

// editorGizmoMouseUp ends a drag of either tool.
func editorGizmoMouseUp() {
	Editor.GizmoDragTool = modelToolMove
	Editor.GizmoDragStartScale = mgl32.Vec3{}
	translateGizmoMouseUp()
}

// ---------- drawing ----------

// renderEditorGizmo draws whichever gizmo the viewport is showing.
func renderEditorGizmo(mx, my int32) {
	if editorScaleGizmoActive() {
		renderEditorScaleGizmo(mx, my)
		return
	}
	renderTranslateGizmo(mx, my)
}

// renderEditorScaleGizmo draws the three axis handles and the uniform handle at
// the centre, brightening whichever is under the cursor or being dragged.
func renderEditorScaleGizmo(mx, my int32) {
	e := editorScaleTarget()
	if e == nil {
		return
	}
	origin, ok := translateGizmoOrigin()
	if !ok {
		return
	}

	hover := -1
	axes := editorScaleAxes(e)
	if Editor.GizmoDragging && Editor.GizmoDragTool == modelToolScale {
		hover = Editor.GizmoDragAxis
		// The drag's own axes, so the handles stay where they were grabbed.
		axes = Editor.GizmoScaleAxes
	} else if !Editor.GizmoDragging {
		hover = editorPickScaleHandle(mx, my)
	}

	armLen := gizmoArmLength(origin) * modelScaleArmFrac
	yellow := mgl32.Vec3{1, 1, 0}
	for i, axis := range axes {
		col := gizmoAxisColors[i]
		if i == hover {
			col = yellow
		}
		tip := origin.Add(axis.Mul(armLen))
		GizmoLine(origin, tip, col)
		gizmoBox(tip, modelScaleTipHalfPx, col)
	}

	// The uniform handle. Neutral rather than any axis colour, because it is
	// the one that belongs to none of them.
	col := mgl32.Vec3{0.85, 0.85, 0.9}
	if hover == modelScaleUniformAxis {
		col = yellow
	}
	gizmoBox(origin, gizmoTipHalfPx*1.2, col)
}
