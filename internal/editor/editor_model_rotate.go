package editor

// editor_model_rotate.go — the rotate gizmo.
//
// The translate gizmo (editor_model_pick.go) drags along a line; this one drags
// around one. They are separate tools rather than one combined widget because
// their handles would sit on top of each other: a ring passes through the tips
// of the arms, and every click near the end of an axis would be a coin toss.
// modelGizmoTool says which is on screen, the way most modellers do it.
//
// Both share the drag bookkeeping in modelGizmo — the same mouse-down snapshot,
// the same undo entry, the same "apply absolutely from the snapshot, never
// incrementally" rule, which for rotation matters more than for translation:
// composing a rotation per motion event would accumulate drift in the basis and
// slowly shear the selection.

import (
	"foundry/internal/geom"
	"foundry/internal/gizmo"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// modelGizmoToolKind is which gizmo the viewport is showing.
type modelGizmoToolKind int

const (
	modelToolMove modelGizmoToolKind = iota
	modelToolRotate
	modelToolScale
)

// modelGizmoTools is the trio, in the order the toolbar shows them.
var modelGizmoTools = []modelGizmoToolKind{modelToolMove, modelToolRotate, modelToolScale}

func (t modelGizmoToolKind) String() string {
	switch t {
	case modelToolRotate:
		return "Rotate"
	case modelToolScale:
		return "Scale"
	default:
		return "Move"
	}
}

// modelGizmoTool is the active gizmo.
var modelGizmoTool modelGizmoToolKind

// modelSetGizmoTool switches tools, cancelling any drag in progress so a
// half-finished translation cannot be finished as a rotation.
func modelSetGizmoTool(tool modelGizmoToolKind) {
	if modelGizmoTool == tool {
		return
	}
	modelGizmoMouseUp()
	modelGizmoTool = tool
}

const (
	// modelRingSegments is how finely the rings are drawn and picked. 48 is
	// smooth enough that the polyline reads as a circle at gizmo size, and
	// coarse enough that picking three of them stays trivial.
	modelRingSegments = 48

	// modelRotateSnapDeg is the increment rotation snaps to when snap-to-grid
	// is on. The grid step is a distance and means nothing for an angle, so
	// rotation gets its own quantum — 15 degrees, which divides the right
	// angles and the thirds people actually reach for.
	modelRotateSnapDeg = 15
)

// modelRingBasis returns two unit vectors spanning the plane perpendicular to
// axis. Deterministic for a given axis, so the angle measured during a drag and
// the ring drawn on screen agree about where zero is.
func modelRingBasis(axis mgl32.Vec3) (u, v mgl32.Vec3) {
	u = geom.PerpAxis(axis)
	if u.Len() < 1e-6 {
		u = mgl32.Vec3{1, 0, 0}
	}
	u = u.Normalize()
	return u, axis.Cross(u).Normalize()
}

// modelRingPoint is the point at the given angle on the ring of radius r.
func modelRingPoint(origin, u, v mgl32.Vec3, r float32, angle float64) mgl32.Vec3 {
	return origin.
		Add(u.Mul(r * float32(math.Cos(angle)))).
		Add(v.Mul(r * float32(math.Sin(angle))))
}

// modelRotationAngleAt returns the cursor's angle around origin in the plane
// perpendicular to axis, through this window's camera. The measurement itself
// is rotationAngleForRay (editor_rotate_gizmo.go), shared with the level
// editor's rotate gizmo so a ring dragged the same way means the same thing in
// both viewports.
func modelRotationAngleAt(mx, my int32, origin, axis mgl32.Vec3) (float32, bool) {
	w, h, ok := modelViewport()
	if !ok {
		return 0, false
	}
	rayO, rayD := modelCam.ray(mx, my, w, h)
	return rotationAngleForRay(rayO, rayD, origin, axis)
}

// modelPickRotateRing returns the ring a click at (mx, my) grabs, or -1.
func modelPickRotateRing(mx, my int32) int {
	origin, ok := modelGizmoOrigin()
	if !ok {
		return -1
	}
	w, h, vok := modelViewport()
	if !vok {
		return -1
	}
	projView := modelProjView()
	radius := modelGizmoArmLength(origin)

	best := -1
	bestD := gizmoPickPx
	for i, axis := range gizmoAxes {
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

// ---------- the drag ----------

// modelRotateMouseDown starts a rotation if the click landed on a ring.
func modelRotateMouseDown(mx, my int32) bool {
	axis := modelPickRotateRing(mx, my)
	if axis < 0 {
		return false
	}
	origin, ok := modelGizmoOrigin()
	if !ok {
		return false
	}
	angle, ok := modelRotationAngleAt(mx, my, origin, gizmoAxes[axis])
	if !ok {
		return false
	}

	// One undo entry for the whole drag, as the translate gizmo does.
	ModelPushUndo()

	modelGizmo.Dragging = true
	modelGizmo.Tool = modelToolRotate
	modelGizmo.Axis = axis
	modelGizmo.StartAngle = angle
	modelGizmo.StartOrigin = origin
	modelGizmo.StartVerts = map[int]mgl32.Vec3{}

	if modelSelection.Mode == modelSelBone {
		if modelPoseActive() {
			modelPoseGizmoCapture()
			return true
		}
		modelBoneGizmoCapture()
		return true
	}
	for _, v := range modelTransformVerts() {
		if v >= 0 && v < len(modelDoc.Verts) {
			modelGizmo.StartVerts[v] = modelDoc.Verts[v]
		}
	}
	return true
}

// modelRotateMouseMove applies the rotation, always from the mouse-down
// snapshot rather than from the previous frame.
func modelRotateMouseMove(mx, my int32) {
	if !modelGizmo.Dragging || modelGizmo.Tool != modelToolRotate || modelDoc == nil {
		return
	}
	axis := gizmoAxes[modelGizmo.Axis]
	origin := modelGizmo.StartOrigin
	angle, ok := modelRotationAngleAt(mx, my, origin, axis)
	if !ok {
		return
	}
	delta := geom.WrapAngle(angle - modelGizmo.StartAngle)
	if Editor.SnapToGrid {
		delta = snapValue(delta, mgl32.DegToRad(modelRotateSnapDeg))
	}

	if modelSelection.Mode == modelSelBone && modelPoseActive() {
		// Pose rotation pivots at each bone's own head, not at the shared
		// origin — see modelPoseRotateApply — so it wants the angle, not the
		// matrix.
		modelPoseRotateApply(axis, delta)
		return
	}
	rot := mgl32.HomogRotate3D(delta, axis).Mat3()
	if modelSelection.Mode == modelSelBone {
		modelBoneRotateApply(axis, delta, origin, rot)
		return
	}
	for v, start := range modelGizmo.StartVerts {
		modelDoc.SetVertPos(v, origin.Add(rot.Mul3x1(start.Sub(origin))))
	}
	modelAfterMeshMove()
}

// modelBoneRotateApply turns the selected bones about origin.
//
// Rotation always moves the whole bone, whichever joint the gizmo is pointed
// at — spinning a single endpoint about itself would do nothing. The joint
// selector still chooses the *pivot* through modelGizmoOrigin, which is what
// makes "rotate about the head" mean swinging the bone from its joint, the way
// a limb actually moves.
func modelBoneRotateApply(axis mgl32.Vec3, delta float32, origin mgl32.Vec3, rot mgl32.Mat3) {
	if len(modelBoneDrag.Bones) == 0 {
		return
	}
	leads := modelBoneLeads()
	for _, bi := range leads {
		if bi < 0 || bi >= len(modelBoneDrag.Bones) || bi >= len(modelDoc.Bones) {
			continue
		}
		rest := modelBoneDrag.Bones[bi]
		head := origin.Add(rot.Mul3x1(rest.Head.Sub(origin)))
		tail := origin.Add(rot.Mul3x1(rest.Tail.Sub(origin)))
		modelDoc.SetBoneHead(bi, head)
		modelDoc.Bones[bi].Tail = tail

		// Carry the twist. Moving the endpoints captures every part of the
		// rotation except the component along the bone's own axis, which leaves
		// them where they were — that part is the roll. Projecting the rotation
		// onto the bone direction is exact at both ends of the range (a turn
		// about the bone is all twist, one across it is none) and a reasonable
		// blend in between.
		dir := rest.Tail.Sub(rest.Head)
		if dir.Len() > 1e-6 {
			modelDoc.Bones[bi].Roll = rest.Roll + delta*axis.Dot(dir.Normalize())
		}
	}
	modelMirrorBoneLeads(leads, true, true, true)
	modelDoc.SyncConnectedBones()
	if modelBoneDeform {
		modelDoc.SkinFrom(modelBoneDrag.Bones, modelBoneDrag.Verts)
	}
}

// ---------- drawing ----------

// drawModelRotateGizmo draws the three rings, brightening the one under the
// cursor or the one being dragged.
func drawModelRotateGizmo(mx, my int32) {
	origin, ok := modelGizmoOrigin()
	if !ok {
		return
	}
	radius := modelGizmoArmLength(origin)

	hover := -1
	if modelGizmo.Dragging && modelGizmo.Tool == modelToolRotate {
		hover = modelGizmo.Axis
	} else if !modelGizmo.Dragging {
		hover = modelPickRotateRing(mx, my)
	}

	yellow := mgl32.Vec3{1, 1, 0}
	for i, axis := range gizmoAxes {
		col := gizmoAxisColors[i]
		if i == hover {
			col = yellow
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
}
