package editor

// editor_model_scale.go — the scale gizmo, the third of the move/rotate/scale
// trio. It follows the same shape as editor_model_rotate.go: its own handles,
// its own drag maths, the same shared bookkeeping in modelGizmo, and the same
// rule that a drag is applied absolutely from the mouse-down snapshot rather
// than composed frame by frame — which matters more here than anywhere else,
// since multiplying a factor in every motion event compounds exponentially.

import (
	"foundry/internal/geom"
	"foundry/internal/gizmo"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	// modelScaleArmFrac shortens the scale arms relative to the move gizmo's,
	// so the two read differently at a glance even before the toolbar is
	// consulted: short arms with a big box mean scale, long arms with a small
	// one mean move.
	modelScaleArmFrac = 0.85
	// modelScaleTipHalfPx is half the on-screen size of an axis grab box.
	modelScaleTipHalfPx = gizmoTipHalfPx * 1.7
	// modelScaleUniformPx is how close to the centre a click counts as grabbing
	// the uniform handle.
	modelScaleUniformPx = float32(12)

	// modelScaleSnapStep is the quantum the scale factor snaps to when
	// snap-to-grid is on. Like rotation's 15 degrees, a distance-based grid step
	// means nothing for a ratio, so scale gets its own: tenths, which puts a
	// half, a double and a triple all exactly on a stop.
	modelScaleSnapStep = 0.1

	// modelScaleMin keeps a drag from collapsing the selection to a point or
	// pulling it through itself. Passing zero would mirror the geometry, which
	// inverts the winding of every face it touches and turns the model inside
	// out — a destructive surprise from a gesture that only asked for "smaller".
	modelScaleMin = 0.01

	// scaleRefMin is the smallest reference a drag can be a ratio of. Below it
	// the mouse-down measure is indistinguishable from zero and the factor it
	// would produce is meaningless, so the grab is refused rather than allowed
	// to fling the selection off to infinity on the first motion event.
	scaleRefMin = 1e-4
)

// modelScaleUniformAxis is the Axis value recorded for a uniform drag. It is
// out of range for gizmoAxes on purpose: nothing can mistake it for X, Y or Z.
const modelScaleUniformAxis = 3

// modelPickScaleHandle returns the handle a click grabs: 0..2 for an axis,
// modelScaleUniformAxis for the centre, or -1.
//
// The centre is tested first because all three arms start there, so every click
// near it is within zero pixels of all of them. Uniform scaling is also the more
// common intent when aiming at the middle of the widget.
func modelPickScaleHandle(mx, my int32) int {
	origin, ok := modelGizmoOrigin()
	if !ok {
		return -1
	}
	w, h, vok := modelViewport()
	if !vok {
		return -1
	}
	projView := modelProjView()
	originScreen, ok := projectToScreenIn(projView, origin, w, h)
	if !ok {
		return -1
	}
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	if cursor.Sub(originScreen).Len() <= modelScaleUniformPx {
		return modelScaleUniformAxis
	}

	armLen := modelGizmoArmLength(origin) * modelScaleArmFrac
	basis, _ := modelScaleBasis()
	best := -1
	bestD := gizmoPickPx
	for i, axis := range basis {
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

// modelScaleReference returns the measure a scale factor is a ratio of.
//
// For an axis handle that is the cursor's position along the axis, so pulling
// the handle out from the centre grows the selection and pushing it in shrinks
// it. For the uniform handle it is the cursor's distance from the centre on
// screen, since no single world axis describes that drag.
func modelScaleReference(mx, my int32, origin mgl32.Vec3, handle int) (float32, bool) {
	w, h, ok := modelViewport()
	if !ok {
		return 0, false
	}
	if handle == modelScaleUniformAxis {
		originScreen, ok := projectToScreenIn(modelProjView(), origin, w, h)
		if !ok {
			return 0, false
		}
		return mgl32.Vec2{float32(mx), float32(my)}.Sub(originScreen).Len(), true
	}
	if handle < 0 || handle > 2 {
		return 0, false
	}
	rayO, rayD := modelCam.ray(mx, my, w, h)
	basis := modelScaleDragBasis()
	return axisParamForRay(rayO, rayD, origin, basis[handle])
}

// scaleFactorBetween turns the reference taken at mouse-down and the one under
// the cursor now into the factor a scale drag applies: snapped when
// snap-to-grid is on, and never below modelScaleMin.
//
// The level editor's scale gizmo (editor_scale_gizmo.go) runs its drags through
// this too, so a handle pulled the same distance means the same thing in both
// editors — including which stops snapping lands on.
func scaleFactorBetween(start, now float32) float32 {
	// Grabbing where the reference is ~0 — the cursor level with the centre
	// along the axis — would divide by nothing and send the factor to infinity.
	if geom.Abs32(start) < scaleRefMin {
		return 1
	}
	f := now / start
	if Editor.SnapToGrid {
		f = snapValue(f, modelScaleSnapStep)
	}
	if f < modelScaleMin {
		f = modelScaleMin
	}
	return f
}

// modelScaleFactor turns the current reference into a factor against the one
// taken at mouse-down, snapped and clamped.
func modelScaleFactor(now float32) float32 {
	return scaleFactorBetween(modelGizmo.StartS, now)
}

// modelScaleVector spreads a factor over the axes the handle drives.
func modelScaleVector(handle int, f float32) mgl32.Vec3 {
	if handle == modelScaleUniformAxis {
		return mgl32.Vec3{f, f, f}
	}
	out := mgl32.Vec3{1, 1, 1}
	if handle >= 0 && handle <= 2 {
		out[handle] = f
	}
	return out
}

// modelScalePoint scales p about origin by a per-axis factor, in world axes.
func modelScalePoint(p, origin, s mgl32.Vec3) mgl32.Vec3 {
	d := p.Sub(origin)
	return origin.Add(mgl32.Vec3{d.X() * s.X(), d.Y() * s.Y(), d.Z() * s.Z()})
}

// modelScalePointIn scales p about origin by a per-axis factor measured in the
// given orthonormal frame: decompose the offset onto the three axes, stretch
// each component, put it back together. With the world frame it is exactly
// modelScalePoint; with the selection's own it is what makes "wider" mean wider
// across a surface rather than wider along X.
func modelScalePointIn(p, origin, s mgl32.Vec3, basis [3]mgl32.Vec3) mgl32.Vec3 {
	d := p.Sub(origin)
	out := origin
	for i, axis := range basis {
		out = out.Add(axis.Mul(d.Dot(axis) * s[i]))
	}
	return out
}

// ---------- the drag ----------

// modelScaleMouseDown starts a scale if the click landed on a handle.
func modelScaleMouseDown(mx, my int32) bool {
	handle := modelPickScaleHandle(mx, my)
	if handle < 0 {
		return false
	}
	origin, ok := modelGizmoOrigin()
	if !ok {
		return false
	}
	ref, ok := modelScaleReference(mx, my, origin, handle)
	if !ok {
		return false
	}
	// Too near the centre to be a ratio of anything: the first motion event
	// would multiply by a huge number.
	if geom.Abs32(ref) < scaleRefMin {
		return false
	}

	// One undo entry for the whole drag, as the other two tools do.
	ModelPushUndo()

	modelGizmo.Dragging = true
	modelGizmo.Tool = modelToolScale
	modelGizmo.Axis = handle
	modelGizmo.StartS = ref
	modelGizmo.StartOrigin = origin
	modelGizmo.StartBasis, _ = modelScaleBasis()
	modelGizmo.StartVerts = map[int]mgl32.Vec3{}

	if modelSelection.Mode == modelSelBone {
		if modelPoseActive() {
			modelPoseGizmoCapture()
			return true
		}
		modelBoneGizmoCapture()
		return true
	}
	for _, v := range modelSelectedVerts() {
		if v >= 0 && v < len(modelDoc.Verts) {
			modelGizmo.StartVerts[v] = modelDoc.Verts[v]
		}
	}
	return true
}

// modelScaleMouseMove applies the scale, always from the mouse-down snapshot.
func modelScaleMouseMove(mx, my int32) {
	if !modelGizmo.Dragging || modelGizmo.Tool != modelToolScale || modelDoc == nil {
		return
	}
	origin := modelGizmo.StartOrigin
	ref, ok := modelScaleReference(mx, my, origin, modelGizmo.Axis)
	if !ok {
		return
	}
	s := modelScaleVector(modelGizmo.Axis, modelScaleFactor(ref))
	basis := modelScaleDragBasis()

	if modelSelection.Mode == modelSelBone {
		if modelPoseActive() {
			// s is already per-axis in the drag basis, which pose mode pins to
			// the bone's own frame (modelScaleBasis), so it drops straight into
			// the channel.
			modelPoseScaleApply(s)
			return
		}
		modelBoneScaleApply(origin, s, basis)
		return
	}
	for v, start := range modelGizmo.StartVerts {
		modelDoc.SetVertPos(v, modelScalePointIn(start, origin, s, basis))
	}
	modelAfterMeshMove()
}

// modelBoneScaleApply scales the selected bones about origin.
//
// Both endpoints move, so the bone's length changes — which is the whole point:
// scaling a bone is how a limb is made longer, and the length is the scale of
// everything weighted to it. Roll is untouched; a scale carries no twist.
func modelBoneScaleApply(origin mgl32.Vec3, s mgl32.Vec3, basis [3]mgl32.Vec3) {
	if len(modelBoneDrag.Bones) == 0 {
		return
	}
	leads := modelBoneLeads()
	for _, bi := range leads {
		if bi < 0 || bi >= len(modelBoneDrag.Bones) || bi >= len(modelDoc.Bones) {
			continue
		}
		rest := modelBoneDrag.Bones[bi]
		head := modelScalePointIn(rest.Head, origin, s, basis)
		tail := modelScalePointIn(rest.Tail, origin, s, basis)
		// A bone scaled to nothing has no direction and could not be drawn or
		// deform anything, so leave it as it was rather than destroying it.
		if tail.Sub(head).Len() < 1e-6 {
			continue
		}
		modelDoc.SetBoneHead(bi, head)
		modelDoc.Bones[bi].Tail = tail
	}
	modelMirrorBoneLeads(leads, true, true, false)
	modelDoc.SyncConnectedBones()
	if modelBoneDeform {
		modelDoc.SkinFrom(modelBoneDrag.Bones, modelBoneDrag.Verts)
	}
}

// ---------- drawing ----------

// drawModelScaleGizmo draws the three axis handles and the uniform handle at
// the centre, brightening whichever is under the cursor or being dragged.
func drawModelScaleGizmo(mx, my int32) {
	origin, ok := modelGizmoOrigin()
	if !ok {
		return
	}
	armLen := modelGizmoArmLength(origin) * modelScaleArmFrac

	hover := -1
	if modelGizmo.Dragging && modelGizmo.Tool == modelToolScale {
		hover = modelGizmo.Axis
	} else if !modelGizmo.Dragging {
		hover = modelPickScaleHandle(mx, my)
	}

	// The frozen frame while a drag is running, the live one otherwise: the
	// arms must not swing away from under a handle the user is holding.
	basis := modelScaleDragBasis()
	yellow := mgl32.Vec3{1, 1, 0}
	for i, axis := range basis {
		col := gizmoAxisColors[i]
		if i == hover {
			col = yellow
		}
		tip := origin.Add(axis.Mul(armLen))
		gizmo.GizmoLine(origin, tip, col)
		modelGizmoBox(tip, modelScaleTipHalfPx, col)
	}

	// The uniform handle. Neutral rather than any axis colour, because it is
	// the one that belongs to none of them.
	col := mgl32.Vec3{0.85, 0.85, 0.9}
	if hover == modelScaleUniformAxis {
		col = yellow
	}
	modelGizmoBox(origin, gizmoTipHalfPx*1.2, col)
}
