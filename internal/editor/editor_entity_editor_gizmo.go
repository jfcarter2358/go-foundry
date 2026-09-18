package editor

// editor_entity_editor_gizmo.go — move/rotate/scale gizmos for the entity
// editor.
//
// The same three tools the 3D view and the model editor carry, on the same
// G/R/C keys and drawn with the same arms, rings and handles: an author who has
// dragged a chunk into place in the level editor already knows how to drag a
// hull into place here. What differs is only what a drag writes to.
//
// What it acts on is whatever the outline has selected, and each kind offers
// exactly the channels it actually has:
//
//	model    offset, a full Euler rotation, a per-axis scale
//	geometry a brush, a face, an edge or a corner — all three tools, all three
//	         axes, written straight onto the vertices. Not a transform at all:
//	         a brush's shape IS its corner list, so moving one is moving them,
//	         and this window borrows the level editor's own write paths
//	         (editor_sel_transform.go) rather than owning a second copy
//	seat     position and a yaw. There is nothing to scale: a seat is a point
//	         and a facing, and its extent is the occupant's, not its own
//
// A tool with nothing to write to shows no handles at all, rather than offering
// a drag that would silently do nothing — the rule the level editor's gizmos
// follow for a chunk (no rotate arms but Y) and a point light (no scale).
//
// Picking and the drag maths are the shared ones (axisParamForRay,
// rotationAngleForRay, scaleFactorBetween), routed through this window's own
// camera the way the model editor routes them through its own — see the header
// of editor_model_pick.go. The one rule every drag here keeps is the one every
// other gizmo in the editor keeps: apply absolutely from the mouse-down
// snapshot, never incrementally, so a long drag stays exact and snapping lands
// on absolute stops.

import (
	"foundry/internal/geom"
	"foundry/internal/gizmo"
	"foundry/internal/world"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// entityGizmoTool is the active tool. Shares editorGizmoTool with the level
// editor rather than defining a parallel enum: they are the same three tools,
// and the labels, keys and tooltips in editor_sel_tool.go are written once.
var entityGizmoTool editorGizmoTool

// entityMouseX/Y is the last cursor position seen in this window, kept because
// hover highlighting happens at draw time and SDL only reports motion as an
// event.
var entityMouseX, entityMouseY int32

// entityGizmo tracks a drag in progress.
//
// StartOrigin is the gizmo's position at mouse-down and every later sample is
// measured against it: axisParamForRay returns a parameter relative to the
// origin it is given, so reading the live origin — which has moved with the
// thing being dragged — would subtract the offset already applied and leave the
// drag trailing the cursor.
var entityGizmo entityGizmoDrag

// entityGizmoDrag is that state, named so a test can reset it in one
// assignment rather than restating the shape of an anonymous struct.
type entityGizmoDrag struct {
	Dragging bool
	Tool     editorGizmoTool
	// Axis indexes gizmoAxes: the arm, the ring's normal, or the scale handle,
	// where modelScaleUniformAxis means the centre one, which is no axis.
	Axis int
	// StartS is the measure the drag is relative to: an axis parameter for
	// move, the angle around the ring for rotate, and for scale the reference
	// the factor is a ratio of.
	StartS      float32
	StartOrigin mgl32.Vec3

	// What the drag offsets, snapshotted at mouse-down. StartRot holds a
	// Y-only rotation in its Y component, so one set of fields serves all three
	// kinds.
	StartPos   mgl32.Vec3
	StartRot   mgl32.Vec3
	StartScale mgl32.Vec3

	// StartWorld is the geometry case's snapshot: every dragged corner's
	// position at mouse-down, parallel with gizmoVertexSels. Non-nil is what
	// says this drag is reshaping brushes rather than writing a transform.
	StartWorld []mgl32.Vec3
}

// ---------- what the gizmo acts on ----------

// entityGizmoTarget is the part of the definition the current selection
// exposes: the channels that exist on it, and nil for the ones that do not.
//
// Resolved fresh every time it is needed rather than held across frames. The
// pointers reach into slices that undo replaces wholesale, so a cached one
// would go on writing into a document that is no longer being edited.
type entityGizmoTarget struct {
	Pos *mgl32.Vec3
	// Rot is a full Euler triple; RotY a single angle about Y. Exactly one is
	// set.
	Rot  *mgl32.Vec3
	RotY *float32
	// Scale is a per-axis factor; ScaleU a uniform one. Both nil for something
	// with no size of its own.
	Scale  *mgl32.Vec3
	ScaleU *float32
	// Waypoint marks a path's point: its rotation keeps counting past half a
	// turn (eulerAddedAbout) and its scale is a brush's (ClampBrushScale).
	Waypoint bool
}

// entityGizmoTargetOf resolves the outline's selection, or reports false when
// it points at nothing.
func entityGizmoTargetOf() (entityGizmoTarget, bool) {
	if entityDef == nil {
		return entityGizmoTarget{}, false
	}
	switch entitySel.Kind {
	case entitySelModel:
		if entitySel.Idx < 0 || entitySel.Idx >= len(entityDef.Models) {
			return entityGizmoTarget{}, false
		}
		m := &entityDef.Models[entitySel.Idx]
		return entityGizmoTarget{Pos: &m.Offset, Rot: &m.Rotation, Scale: &m.Scale}, true

	case entitySelEmitter:
		if entitySel.Idx < 0 || entitySel.Idx >= len(entityDef.Emitters) {
			return entityGizmoTarget{}, false
		}
		m := &entityDef.Emitters[entitySel.Idx]
		// No scale: an effect's size is its document's, and a gizmo that
		// offered a handle writing nothing would read as broken.
		return entityGizmoTarget{Pos: &m.Offset, Rot: &m.Rotation}, true

	case entitySelVolume:
		if entitySel.Idx < 0 || entitySel.Idx >= len(entityDef.Volumes) {
			return entityGizmoTarget{}, false
		}
		m := &entityDef.Volumes[entitySel.Idx]
		// Scale drags the half-extents, the level editor's rule for a volume:
		// a region's size IS its extents, and it has no factor of its own.
		return entityGizmoTarget{Pos: &m.Offset, Rot: &m.Rotation, Scale: &m.Half}, true

	case entitySelSeat:
		if entityDef.Vehicle == nil ||
			entitySel.Idx < 0 || entitySel.Idx >= len(entityDef.Vehicle.Seats) {
			return entityGizmoTarget{}, false
		}
		s := &entityDef.Vehicle.Seats[entitySel.Idx]
		return entityGizmoTarget{Pos: &s.Position, RotY: &s.Yaw}, true

	case entitySelWaypoints:
		// One point, all three channels — each is a real one the brush arriving
		// there takes. A path selected whole has no single place to grab.
		p := entityActiveWaypoint()
		if p == nil {
			return entityGizmoTarget{}, false
		}
		return entityGizmoTarget{Pos: &p.Position, Rot: &p.Rotation, Scale: &p.Scale, Waypoint: true}, true
	}
	return entityGizmoTarget{}, false
}

// ---------- geometry: brushes, faces, edges and corners ----------
//
// A brush has no transform to write. Its shape is its vertex list, so every
// tool here resolves the selection down to the handles underneath it —
// gizmoVertexSels, exactly as the level editor's gizmos do — and writes
// positions. That is why this window needed no new transform maths to gain
// brush editing: rotatePointAbout and scalePointAbout are shared, and so is the
// write-back that reloads the brushes a drag touched.

// entityGeometrySelected reports whether the geometry workspace has something
// the gizmo can drag, which is any brush, face, edge or corner selection.
func entityGeometrySelected() bool { return len(gizmoVertexSels()) > 0 }

// entityGeometryCenter is where the handles sit for a geometry selection: the
// average of the corners it resolves to, in the entity's own space.
func entityGeometryCenter() (mgl32.Vec3, bool) {
	var sum mgl32.Vec3
	n := 0
	for _, sel := range gizmoVertexSels() {
		if p, ok := vertexSelPos(sel); ok {
			sum = sum.Add(p)
			n++
		}
	}
	if n == 0 {
		return mgl32.Vec3{}, false
	}
	return sum.Mul(1 / float32(n)), true
}

// entityGizmoOrigin is where the handles sit: the selection's own authored
// position, which is the thing a move drag rewrites — or, for a geometry
// selection, the centre of the corners it is made of.
func entityGizmoOrigin() (mgl32.Vec3, bool) {
	if entityGeometrySelected() {
		return entityGeometryCenter()
	}
	t, ok := entityGizmoTargetOf()
	if !ok {
		return mgl32.Vec3{}, false
	}
	return *t.Pos, true
}

// entityGizmoRotates reports whether the current selection has a rotation to
// write, and whether it is the Y-only kind.
func entityGizmoRotates() (ok, yOnly bool) {
	if entityGeometrySelected() {
		// Every axis: spreading a set of corners about a ring is defined on all
		// three, and a brush has no authored rotation channel to be limited by.
		return true, false
	}
	t, got := entityGizmoTargetOf()
	if !got {
		return false, false
	}
	return t.Rot != nil || t.RotY != nil, t.RotY != nil
}

// entityGizmoScales reports whether the current selection has a size to write,
// and whether that size is one uniform factor rather than three.
func entityGizmoScales() (ok, uniform bool) {
	if entityGeometrySelected() {
		return true, false
	}
	t, got := entityGizmoTargetOf()
	if !got {
		return false, false
	}
	return t.Scale != nil || t.ScaleU != nil, t.ScaleU != nil
}

// entityGizmoToolTooltip explains what each tool does *here*, where every drag
// rewrites the selection's own transform rather than spreading a set of points
// about a shared centre the way the level editor's do.
func entityGizmoToolTooltip(t editorGizmoTool) string {
	switch t {
	case editorToolRotate:
		return "R \u2014 Drag a ring to turn the selection about its own anchor.\n" +
			"A chunk and a seat turn about Y only; a model attachment\nturns about all three."
	case editorToolScale:
		return "C \u2014 Drag a handle to resize the selection in place.\n" +
			"A model attachment scales per axis or uniformly from the\n" +
			"centre; a chunk only uniformly; a seat has no size."
	default:
		return "G \u2014 Drag an arm to slide the selection along one axis.\n" +
			"Moves the offset the panel shows, so the type keeps its shape."
	}
}

// entitySetGizmoTool switches tools, ending any drag in progress first so a
// half-finished translation cannot be completed as a rotation.
// entityToggleUVMode switches this window's tools between a face's corners and
// its texture, ending any drag in progress first.
func entityToggleUVMode() {
	entityGizmoMouseUp()
	entityWS.UVMode = !entityWS.UVMode
}

func entitySetGizmoTool(t editorGizmoTool) {
	if entityGizmoTool == t {
		return
	}
	entityGizmoMouseUp()
	entityGizmoTool = t
}

// ---------- this window's viewport ----------

func entityViewport() (w, h int32, ok bool) {
	if entityEditorWindow == nil {
		return 0, 0, false
	}
	return entityEditorWindow.W, entityEditorWindow.H, true
}

func entityProjView() mgl32.Mat4 {
	w, h, ok := entityViewport()
	if !ok {
		return mgl32.Ident4()
	}
	return entityCam.proj(w, h).Mul4(entityCam.view())
}

// entityWorldSizeForPixels is worldSizeForPixels against this window's camera;
// the original sizes itself from the main window's Projection and WinHeight.
func entityWorldSizeForPixels(p mgl32.Vec3, halfPx float32) float32 {
	w, h, ok := entityViewport()
	if !ok {
		return 0.05
	}
	return worldSizeForPixelsIn(p, entityCam.position(), halfPx,
		entityCam.proj(w, h).At(1, 1), h)
}

// entityGizmoArmLength keeps the arms a constant on-screen length, so the
// gizmo is the same size whether the type is a crate or a carrier.
func entityGizmoArmLength(origin mgl32.Vec3) float32 {
	return entityWorldSizeForPixels(origin, gizmoArmPx)
}

// entityGizmoBox draws a screen-constant cube at p — gizmoBox against this
// window's camera.
func entityGizmoBox(p mgl32.Vec3, halfPx float32, col mgl32.Vec3) {
	half := entityWorldSizeForPixels(p, halfPx)
	hv := mgl32.Vec3{half, half, half}
	gizmo.GizmoAABB(p.Sub(hv), p.Add(hv), col)
}

// entityCamRay is this window's picking ray at a cursor position.
func entityCamRay(mx, my int32) (origin, dir mgl32.Vec3, ok bool) {
	w, h, vok := entityViewport()
	if !vok {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	o, d := entityCam.ray(mx, my, w, h)
	return o, d, true
}

// ---------- picking ----------

// entityPickMoveAxis returns the arm a click at (mx, my) grabs, or -1.
func entityPickMoveAxis(mx, my int32) int {
	origin, ok := entityGizmoOrigin()
	if !ok {
		return -1
	}
	w, h, vok := entityViewport()
	if !vok {
		return -1
	}
	projView := entityProjView()
	originScreen, ok := projectToScreenIn(projView, origin, w, h)
	if !ok {
		return -1
	}
	armLen := entityGizmoArmLength(origin)

	best := -1
	bestD := gizmoPickPx
	for i, axis := range gizmoAxes {
		tip, ok := projectToScreenIn(projView, origin.Add(axis.Mul(armLen)), w, h)
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

// entityPickRotateRing returns the ring a click grabs, or -1. Each ring is
// picked as the polyline it is drawn as, so what is grabbed is what is on
// screen.
func entityPickRotateRing(mx, my int32) int {
	origin, ok := entityGizmoOrigin()
	if !ok {
		return -1
	}
	rotates, yOnly := entityGizmoRotates()
	if !rotates {
		return -1
	}
	w, h, vok := entityViewport()
	if !vok {
		return -1
	}
	projView := entityProjView()
	radius := entityGizmoArmLength(origin)

	best := -1
	bestD := gizmoPickPx
	for i, axis := range gizmoAxes {
		if yOnly && i != 1 {
			continue
		}
		u, v := modelRingBasis(axis)
		var prev mgl32.Vec2
		havePrev := false
		for s := 0; s <= modelRingSegments; s++ {
			angle := float64(s) / modelRingSegments * 2 * math.Pi
			sp, onScreen := projectToScreenIn(projView, modelRingPoint(origin, u, v, radius, angle), w, h)
			if !onScreen {
				havePrev = false
				continue
			}
			if havePrev {
				d := distToSegment2D(float32(mx), float32(my), prev.X(), prev.Y(), sp.X(), sp.Y())
				if d <= bestD {
					bestD = d
					best = i
				}
			}
			prev, havePrev = sp, true
		}
	}
	return best
}

// entityPickScaleHandle returns the handle a click grabs: 0..2 for an axis,
// modelScaleUniformAxis for the centre, or -1.
//
// The centre is tested first because all three arms start there, so every click
// near it is within zero pixels of all of them — and a uniform drag is the more
// common intent when aiming at the middle of the widget.
func entityPickScaleHandle(mx, my int32) int {
	origin, ok := entityGizmoOrigin()
	if !ok {
		return -1
	}
	scales, uniform := entityGizmoScales()
	if !scales {
		return -1
	}
	w, h, vok := entityViewport()
	if !vok {
		return -1
	}
	projView := entityProjView()
	originScreen, ok := projectToScreenIn(projView, origin, w, h)
	if !ok {
		return -1
	}
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	if cursor.Sub(originScreen).Len() <= modelScaleUniformPx {
		return modelScaleUniformAxis
	}
	if uniform {
		return -1
	}

	armLen := entityGizmoArmLength(origin) * modelScaleArmFrac
	best := -1
	bestD := gizmoPickPx
	for i, axis := range gizmoAxes {
		tip, ok := projectToScreenIn(projView, origin.Add(axis.Mul(armLen)), w, h)
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

// entityRotationAngleAt returns the cursor's angle around origin in the plane
// perpendicular to axis, through this window's camera. The measurement is
// rotationAngleForRay, shared with both other viewports.
func entityRotationAngleAt(mx, my int32, origin, axis mgl32.Vec3) (float32, bool) {
	rayO, rayD, ok := entityCamRay(mx, my)
	if !ok {
		return 0, false
	}
	return rotationAngleForRay(rayO, rayD, origin, axis)
}

// entityScaleReference returns the measure a scale factor is a ratio of: the
// cursor's position along the axis for an arm, and its distance from the centre
// on screen for the uniform handle, since no single world axis describes that
// drag.
func entityScaleReference(mx, my int32, origin mgl32.Vec3, handle int) (float32, bool) {
	w, h, ok := entityViewport()
	if !ok {
		return 0, false
	}
	if handle == modelScaleUniformAxis {
		originScreen, ok := projectToScreenIn(entityProjView(), origin, w, h)
		if !ok {
			return 0, false
		}
		return mgl32.Vec2{float32(mx), float32(my)}.Sub(originScreen).Len(), true
	}
	if handle < 0 || handle > 2 {
		return 0, false
	}
	rayO, rayD := entityCam.ray(mx, my, w, h)
	return axisParamForRay(rayO, rayD, origin, gizmoAxes[handle])
}

// ---------- the drag ----------

// entityGizmoDragging reports whether a drag is in progress. A drag owns the
// mouse until release.
func entityGizmoDragging() bool { return entityGizmo.Dragging }

// entityGizmoMouseDown routes a left click to whichever gizmo is on screen.
// Returns true when the click was consumed.
//
// A handle under the cursor consumes the click even when the drag cannot start
// — an axis pointing almost straight at the camera, say. The handles are drawn
// over everything and sit exactly where the selection is, so a click aimed at
// one must never fall through to whatever the viewport would otherwise do with
// it.
func entityGizmoMouseDown(mx, my int32) bool {
	if entityWS.UVMode {
		// The UV drag keeps its state on the workspace (editor_uv_gizmo.go);
		// entityGizmo only has to say a drag owns the mouse.
		if !uvGizmoMouseDown(entityGizmoTool, mx, my) {
			return false
		}
		if entityWS.GizmoDragging {
			entityGizmo = entityGizmoDrag{Dragging: true, Tool: entityGizmoTool, Axis: entityWS.GizmoDragAxis}
		}
		return true
	}
	switch entityGizmoTool {
	case editorToolRotate:
		return entityRotateMouseDown(mx, my)
	case editorToolScale:
		return entityScaleMouseDown(mx, my)
	}
	return entityMoveMouseDown(mx, my)
}

func entityMoveMouseDown(mx, my int32) bool {
	axis := entityPickMoveAxis(mx, my)
	if axis < 0 {
		return false
	}
	origin, ok := entityGizmoOrigin()
	if !ok {
		return true
	}
	rayO, rayD, ok := entityCamRay(mx, my)
	if !ok {
		return true
	}
	s, ok := axisParamForRay(rayO, rayD, origin, gizmoAxes[axis])
	if !ok {
		return true
	}
	entityGizmoBeginDrag(editorToolMove, axis, s, origin)
	return true
}

func entityRotateMouseDown(mx, my int32) bool {
	axis := entityPickRotateRing(mx, my)
	if axis < 0 {
		return false
	}
	origin, ok := entityGizmoOrigin()
	if !ok {
		return true
	}
	angle, ok := entityRotationAngleAt(mx, my, origin, gizmoAxes[axis])
	if !ok {
		return true
	}
	entityGizmoBeginDrag(editorToolRotate, axis, angle, origin)
	return true
}

func entityScaleMouseDown(mx, my int32) bool {
	handle := entityPickScaleHandle(mx, my)
	if handle < 0 {
		return false
	}
	origin, ok := entityGizmoOrigin()
	if !ok {
		return true
	}
	ref, ok := entityScaleReference(mx, my, origin, handle)
	if !ok {
		return true
	}
	// Too near the centre to be a ratio of anything: the first motion event
	// would multiply by a huge number.
	if geom.Abs32(ref) < scaleRefMin {
		return true
	}
	entityGizmoBeginDrag(editorToolScale, handle, ref, origin)
	return true
}

// entityGizmoBeginDrag snapshots what the drag will offset and pushes one undo
// entry for the whole of it, rather than one per motion event.
func entityGizmoBeginDrag(tool editorGizmoTool, axis int, start float32, origin mgl32.Vec3) {
	if entityGeometrySelected() {
		EntityDefPushUndo()
		entityGizmo = entityGizmoDrag{
			Dragging: true, Tool: tool, Axis: axis, StartS: start,
			StartOrigin: origin,
			StartWorld:  selectedVerticesWorld(),
		}
		return
	}
	t, ok := entityGizmoTargetOf()
	if !ok {
		return
	}
	EntityDefPushUndo()

	entityGizmo.Dragging = true
	entityGizmo.Tool = tool
	entityGizmo.Axis = axis
	entityGizmo.StartS = start
	entityGizmo.StartOrigin = origin
	entityGizmo.StartWorld = nil
	entityGizmo.StartPos = *t.Pos

	// All three channels are snapshotted whichever tool started the drag: it
	// costs three copies, and it keeps one capture path rather than one per
	// tool. A channel the selection does not have stays at its neutral value.
	entityGizmo.StartRot = mgl32.Vec3{}
	switch {
	case t.Rot != nil:
		entityGizmo.StartRot = *t.Rot
	case t.RotY != nil:
		entityGizmo.StartRot = mgl32.Vec3{0, *t.RotY, 0}
	}
	entityGizmo.StartScale = mgl32.Vec3{1, 1, 1}
	switch {
	case t.Scale != nil:
		entityGizmo.StartScale = entityEffectiveScaleOf(*t.Scale)
	case t.ScaleU != nil:
		s := *t.ScaleU
		if s == 0 {
			s = 1
		}
		entityGizmo.StartScale = mgl32.Vec3{s, s, s}
	}
}

// entityEffectiveScaleOf reads an attachment's authored scale, where the zero
// vector means "unscaled" rather than "collapsed to nothing" — EntityModel's
// own EffectiveScale rule, applied to a bare vector so the snapshot does not
// need the attachment.
func entityEffectiveScaleOf(s mgl32.Vec3) mgl32.Vec3 {
	if s == (mgl32.Vec3{}) {
		return mgl32.Vec3{1, 1, 1}
	}
	return s
}

// entityGizmoMouseUp ends a drag.
func entityGizmoMouseUp() {
	entityGizmo.Dragging = false
	entityGizmo.Axis = -1
	entityGizmo.StartWorld = nil
	uvGizmoEndDrag(&entityWS)
}

// entityGizmoMouseMove applies the cursor position to the drag. It dispatches
// on the tool the *drag* belongs to rather than the tool now selected: a tool
// switch ends the drag first, and routing on the live value would otherwise let
// a stale drag be finished by the wrong maths.
func entityGizmoMouseMove(mx, my int32) {
	if !entityGizmo.Dragging {
		return
	}
	if entityWS.GizmoDragUV {
		uvGizmoMouseMove(mx, my)
		return
	}
	switch entityGizmo.Tool {
	case editorToolRotate:
		entityRotateMouseMove(mx, my)
	case editorToolScale:
		entityScaleMouseMove(mx, my)
	default:
		entityMoveMouseMove(mx, my)
	}
}

func entityMoveMouseMove(mx, my int32) {
	axis := entityGizmo.Axis
	if axis < 0 || axis > 2 {
		return
	}
	rayO, rayD, ok := entityCamRay(mx, my)
	if !ok {
		return
	}
	s, ok := axisParamForRay(rayO, rayD, entityGizmo.StartOrigin, gizmoAxes[axis])
	if !ok {
		return
	}
	entityGizmoApplyMove(axis, s-entityGizmo.StartS)
}

func entityRotateMouseMove(mx, my int32) {
	axis := entityGizmo.Axis
	if axis < 0 || axis > 2 {
		return
	}
	angle, ok := entityRotationAngleAt(mx, my, entityGizmo.StartOrigin, gizmoAxes[axis])
	if !ok {
		return
	}
	delta := geom.WrapAngle(angle - entityGizmo.StartS)
	if ws().SnapToGrid {
		// The grid step is a distance and means nothing for an angle, so
		// rotation gets the same 15-degree quantum the other two viewports use.
		delta = snapValue(delta, mgl32.DegToRad(modelRotateSnapDeg))
	}
	entityGizmoApplyRotate(axis, delta)
}

func entityScaleMouseMove(mx, my int32) {
	ref, ok := entityScaleReference(mx, my, entityGizmo.StartOrigin, entityGizmo.Axis)
	if !ok {
		return
	}
	entityGizmoApplyScale(entityGizmo.Axis, scaleFactorBetween(entityGizmo.StartS, ref))
}

// ---------- the write-back ----------

// entityGizmoApplyMove slides the selection along one world axis from where it
// was at mouse-down, snapped to the grid step when snapping is on.
func entityGizmoApplyMove(axis int, delta float32) {
	if axis < 0 || axis > 2 {
		return
	}
	if entityGizmo.StartWorld != nil {
		// Every corner slides by the same amount, from its own mouse-down
		// position — absolutely, so a long drag never accumulates rounding.
		start := entityGizmo.StartWorld
		setSelectedVertexWorld(func(i int, _ vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
			if i >= len(start) {
				return cur
			}
			return axisMoved(start[i], axis, delta)
		})
		entityGeometryDragged()
		return
	}
	t, ok := entityGizmoTargetOf()
	if !ok {
		return
	}
	*t.Pos = axisMoved(entityGizmo.StartPos, axis, delta)
	entityGizmoChanged()
}

// entityGizmoApplyRotate turns the selection about one world axis.
//
// The Euler case adds on a single channel rather than composing quaternions,
// for the reason eulerRotatedAbout gives: the three channels *are* the world
// axes here, and a proper composition would let a drag about X leak into Y and
// Z — exactly what an author dragging the X ring does not want to see happen to
// the numbers in the panel.
func entityGizmoApplyRotate(axis int, delta float32) {
	if axis < 0 || axis > 2 {
		return
	}
	if entityGizmo.StartWorld != nil {
		start := entityGizmo.StartWorld
		center := entityGizmo.StartOrigin
		setSelectedVertexWorld(func(i int, _ vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
			if i >= len(start) {
				return cur
			}
			return rotatePointAbout(start[i], center, gizmoAxes[axis], delta)
		})
		entityGeometryDragged()
		return
	}
	t, ok := entityGizmoTargetOf()
	if !ok {
		return
	}
	switch {
	case t.Rot != nil && t.Waypoint:
		*t.Rot = eulerAddedAbout(entityGizmo.StartRot, gizmoAxes[axis], delta)
	case t.Rot != nil:
		*t.Rot = eulerRotatedAbout(entityGizmo.StartRot, gizmoAxes[axis], delta)
	case t.RotY != nil:
		// Only the Y ring is ever offered for these, so anything else is a
		// stale axis from a selection that has since changed.
		if axis != 1 {
			return
		}
		*t.RotY = world.WrapAngle(entityGizmo.StartRot.Y() + delta)
	default:
		return
	}
	entityGizmoChanged()
}

// entityGizmoApplyScale resizes the selection in place — it grows about its own
// origin rather than moving, which is what scaling a thing with a size of its
// own has to mean.
func entityGizmoApplyScale(handle int, f float32) {
	if entityGizmo.StartWorld != nil {
		start := entityGizmo.StartWorld
		center := entityGizmo.StartOrigin
		factors := scaleVector(handle, f)
		setSelectedVertexWorld(func(i int, _ vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
			if i >= len(start) {
				return cur
			}
			return scalePointAbout(start[i], center, factors)
		})
		entityGeometryDragged()
		return
	}
	t, ok := entityGizmoTargetOf()
	if !ok {
		return
	}
	factors := scaleVector(handle, f)
	s := entityGizmo.StartScale
	switch {
	case t.Scale != nil && t.Waypoint:
		*t.Scale = world.ClampBrushScale(mgl32.Vec3{s.X() * factors.X(), s.Y() * factors.Y(), s.Z() * factors.Z()})
	case t.Scale != nil:
		*t.Scale = mgl32.Vec3{
			world.EntityScaleClamp(s.X() * factors.X()),
			world.EntityScaleClamp(s.Y() * factors.Y()),
			world.EntityScaleClamp(s.Z() * factors.Z()),
		}
	case t.ScaleU != nil:
		// Only the uniform handle is ever offered for these, so the three
		// factors are equal.
		*t.ScaleU = world.EntityScaleClamp(s.X() * factors.X())
	default:
		return
	}
	entityGizmoChanged()
}

// entityGizmoChanged records the edit and moves the preview to match.
//
// The transform-only sync rather than entityPreviewInvalidate: a drag runs this
// on every motion event, and a full rebuild re-reads every chunk file off disk
// and re-uploads every mesh. Nothing a gizmo writes changes the shape of the
// preview, only where its pieces sit.
func entityGizmoChanged() {
	entityMarkDirty()
	entityPreviewApplyTransforms()
}

// entityGeometryDragged is entityGizmoChanged for a drag that reshaped brushes.
//
// setSelectedVertexWorld has already reloaded whatever it touched, so all that
// is left is the chunk's own bounds and the dirty flag. The document copy of
// the brushes is taken at the end of the frame, not here — see
// editor_entity_editor_brushes.go on why that ordering is what makes undo work.
func entityGeometryDragged() {
	entityGeometryEdited()
	entityMarkDirty()
}

// ---------- drawing ----------

// drawEntityGizmo draws the handles for the current tool and selection. Drawn
// with depth testing off, so a handle behind the hull is still one you can see
// and grab.
func drawEntityGizmo(mx, my int32) {
	if entityWS.UVMode {
		renderUVGizmo(entityGizmoTool, mx, my)
		return
	}
	switch entityGizmoTool {
	case editorToolRotate:
		drawEntityRotateGizmo(mx, my)
	case editorToolScale:
		drawEntityScaleGizmo(mx, my)
	default:
		drawEntityMoveGizmo(mx, my)
	}
}

// entityGizmoHover returns the handle to highlight: the one being dragged, or
// the one under the cursor.
func entityGizmoHover(pick func(mx, my int32) int, mx, my int32) int {
	if entityGizmo.Dragging {
		return entityGizmo.Axis
	}
	return pick(mx, my)
}

var entityGizmoHighlight = mgl32.Vec3{1, 1, 0}

func drawEntityMoveGizmo(mx, my int32) {
	origin, ok := entityGizmoOrigin()
	if !ok {
		return
	}
	armLen := entityGizmoArmLength(origin)
	hover := entityGizmoHover(entityPickMoveAxis, mx, my)

	for i, axis := range gizmoAxes {
		col := gizmoAxisColors[i]
		if i == hover {
			col = entityGizmoHighlight
		}
		tip := origin.Add(axis.Mul(armLen))
		gizmo.GizmoLine(origin, tip, col)
		// The grab box at the tip is what makes the arm findable: a one-pixel
		// line is a hard target at any distance.
		entityGizmoBox(tip, gizmoTipHalfPx, col)
	}
}

func drawEntityRotateGizmo(mx, my int32) {
	origin, ok := entityGizmoOrigin()
	if !ok {
		return
	}
	rotates, yOnly := entityGizmoRotates()
	if !rotates {
		return
	}
	radius := entityGizmoArmLength(origin)
	hover := entityGizmoHover(entityPickRotateRing, mx, my)

	for i, axis := range gizmoAxes {
		if yOnly && i != 1 {
			continue
		}
		col := gizmoAxisColors[i]
		if i == hover {
			col = entityGizmoHighlight
		}
		u, v := modelRingBasis(axis)
		prev := modelRingPoint(origin, u, v, radius, 0)
		for s := 1; s <= modelRingSegments; s++ {
			angle := float64(s) / modelRingSegments * 2 * math.Pi
			p := modelRingPoint(origin, u, v, radius, angle)
			gizmo.GizmoLine(prev, p, col)
			prev = p
		}
	}
	// The pivot itself, so it is obvious the thing turns about its own anchor.
	entityGizmoBox(origin, gizmoTipHalfPx, mgl32.Vec3{0.85, 0.85, 0.9})
}

func drawEntityScaleGizmo(mx, my int32) {
	origin, ok := entityGizmoOrigin()
	if !ok {
		return
	}
	scales, uniform := entityGizmoScales()
	if !scales {
		return
	}
	armLen := entityGizmoArmLength(origin) * modelScaleArmFrac
	hover := entityGizmoHover(entityPickScaleHandle, mx, my)

	if !uniform {
		for i, axis := range gizmoAxes {
			col := gizmoAxisColors[i]
			if i == hover {
				col = entityGizmoHighlight
			}
			tip := origin.Add(axis.Mul(armLen))
			gizmo.GizmoLine(origin, tip, col)
			entityGizmoBox(tip, modelScaleTipHalfPx, col)
		}
	}

	// The centre handle. Neutral rather than any axis colour, because it is the
	// one that belongs to none of them.
	col := mgl32.Vec3{0.85, 0.85, 0.9}
	if hover == modelScaleUniformAxis {
		col = entityGizmoHighlight
	}
	entityGizmoBox(origin, gizmoTipHalfPx*1.2, col)
}
