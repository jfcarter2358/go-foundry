package editor

// editor_rotate_gizmo.go — the 3D viewport's rotate gizmo.
//
// Three rings, one per world axis, drawn at the selection's centre. Dragging a
// ring swings every selected vertex around that centre — around the *shared*
// centre, not each vertex's own position, which is the whole point: rotating a
// point about itself does nothing, and a multi-selection that turns as a rigid
// body is what "rotate the selection" has to mean.
//
// The maths is the model editor's (editor_model_rotate.go) — the same ring
// basis, the same ray-into-the-plane angle measurement, the same fold of the
// ±pi seam — so a ring dragged the same way means the same thing in both
// viewports, right down to which stops snapping lands on. Only the camera and
// the write-back differ, and both live here.

import (
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/world"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// rotationAngleForRay returns the angle of a view ray around origin, measured
// in the plane through origin perpendicular to axis.
//
// The cursor is placed by intersecting the ray with that plane rather than by
// measuring an angle in screen space. Screen space would be simpler and would
// feel wrong the moment a ring is seen at a shallow angle: the same wrist
// movement would produce a different rotation on the near and far sides of the
// ellipse the circle projects to.
//
// ok is false when the ray runs along the plane (edge-on, where the
// intersection is nowhere or wildly unstable), when the plane is behind the
// camera, or when the cursor lands dead on the centre and there is no angle to
// speak of. Refusing leaves the selection where it is, which beats letting it
// spin on numerical noise.
func rotationAngleForRay(rayO, rayD, origin, axis mgl32.Vec3) (float32, bool) {
	denom := rayD.Dot(axis)
	if float32(math.Abs(float64(denom))) < 1e-4 {
		return 0, false
	}
	t := origin.Sub(rayO).Dot(axis) / denom
	if t <= 0 {
		return 0, false
	}
	rel := rayO.Add(rayD.Mul(t)).Sub(origin)
	if rel.Len() < 1e-6 {
		return 0, false
	}
	u, v := modelRingBasis(axis)
	return float32(math.Atan2(float64(rel.Dot(v)), float64(rel.Dot(u)))), true
}

// editorRotationAngleAt is rotationAngleForRay through the main window's camera.
func editorRotationAngleAt(mx, my int32, origin, axis mgl32.Vec3) (float32, bool) {
	rayO, rayD := screenRay(mx, my)
	return rotationAngleForRay(rayO, rayD, origin, axis)
}

// pickRotateRing returns the ring a click at (mx, my) grabs, or -1. Each ring
// is picked as the polyline it is drawn as, so what is grabbed is exactly what
// is on screen.
func pickRotateRing(mx, my int32) int {
	origin, ok := rotateGizmoOrigin()
	if !ok {
		return -1
	}
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
	radius := gizmoArmLength(origin)

	chunkOnly := gizmoChunkRotates()
	best := -1
	bestD := gizmoPickPx
	for i, axis := range gizmoAxes {
		if chunkOnly && i != 1 {
			continue
		}
		u, v := modelRingBasis(axis)
		var prev mgl32.Vec2
		havePrev := false
		for s := 0; s <= modelRingSegments; s++ {
			angle := float64(s) / modelRingSegments * 2 * math.Pi
			sp, onScreen := projectToScreen(projView, modelRingPoint(origin, u, v, radius, angle))
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

// rotateGizmoOrigin returns the pivot the rings sit on: the centre of the
// selected vertices, or the position of whatever single thing is selected.
//
// The two rotate in different senses. A vertex selection has an extent, so
// turning it moves the points themselves; an entity, a volume, the spawn
// marker or a chunk is a single anchor with an orientation, so turning it
// rewrites that orientation and leaves it where it is.
func rotateGizmoOrigin() (mgl32.Vec3, bool) {
	switch gizmoEffectiveKind() {
	case gizmoKindVertices:
		return selectedVerticesCenter()
	case gizmoKindFace:
		// A face turns in the vertex sense, not the entity one: it has an
		// extent made of corners, and swinging them about its own middle is
		// what turning a wall or a floor can mean.
		return selectedFacesCenter()
	case gizmoKindBrush:
		// The vertex sense again, and for a brush it is the only one available:
		// an authored brush has no orientation field to rewrite, so turning one
		// turns its corners about the middle of the selection.
		return selectedBrushesCenter()
	case gizmoKindEntity:
		if e := activeGizmoEntity(); e != nil {
			return e.Position, true
		}
	case gizmoKindVolume:
		if v := activeGizmoVolume(); v != nil {
			return v.Position, true
		}
	case gizmoKindSprite:
		if sp := activeGizmoSprite(); sp != nil {
			return sp.Position, true
		}
	case gizmoKindDecal:
		if d := activeGizmoDecal(); d != nil {
			return d.Position, true
		}
	case gizmoKindScenery:
		if sc := activeGizmoScenery(); sc != nil {
			return sc.Position, true
		}
	case gizmoKindEmitter:
		if em := activeGizmoEmitter(); em != nil {
			return em.Position, true
		}
	case gizmoKindEffect:
		if fx := activeGizmoEffect(); fx != nil {
			return fx.Position, true
		}
	case gizmoKindSound:
		if sd := activeGizmoSound(); sd != nil {
			return sd.Position, true
		}
	case gizmoKindWaypoint:
		if wp := activeGizmoWaypoint(); wp != nil {
			return wp.Position, true
		}
	case gizmoKindSpawn:
		if s := activeGizmoSpawn(); s != nil {
			return s.Position, true
		}
	case gizmoKindChunk:
		if ch := activeGizmoChunk(); ch != nil {
			return ch.LocalToWorld(ch.MinBound.Add(ch.MaxBound).Mul(0.5)), true
		}
	}
	return mgl32.Vec3{}, false
}

// gizmoChunkRotates reports whether the current selection is a chunk — the
// one kind whose rotation is a single Y-axis scalar (Chunk.Rotation; see
// ModelMatrix), not a full Euler triple. The X/Z rings would have nothing to
// write back to, so only Y is offered at all.
func gizmoChunkRotates() bool {
	return gizmoEffectiveKind() == gizmoKindChunk
}

// ---------- the drag ----------

// rotateGizmoMouseDown starts a rotation if the click landed on a ring.
// Returns true when the click was consumed, so it does not also re-select —
// including when the grab cannot start, for the reason translateGizmoMouseDown
// documents: a click aimed at a handle must never throw away the selection the
// handle exists to move.
func rotateGizmoMouseDown(t sdl.MouseButtonEvent) bool {
	if t.Button != sdl.ButtonLeft || gizmoSuppressed() {
		return false
	}
	axis := pickRotateRing(t.X, t.Y)
	if axis < 0 {
		return false
	}
	origin, ok := rotateGizmoOrigin()
	if !ok {
		return true
	}
	angle, ok := editorRotationAngleAt(t.X, t.Y, origin, gizmoAxes[axis])
	if !ok {
		return true
	}

	// One undo entry for the whole drag, not one per motion event.
	// captureGizmoDragStart snapshots whatever the target rotates from — the
	// vertex positions, or the entity's own Euler angles.
	if !captureGizmoDragStart() {
		return true
	}
	Editor.GizmoDragging = true
	Editor.GizmoDragTool = editorToolRotate
	Editor.GizmoDragAxis = axis
	Editor.GizmoDragStartS = angle
	Editor.GizmoDragOrigin = origin
	return true
}

// rotateGizmoMouseMove applies the current cursor position to the drag, always
// from the mouse-down snapshot rather than from the previous frame: composing a
// rotation per motion event accumulates drift in the basis and would slowly
// shear the selection.
func rotateGizmoMouseMove(mx, my int32) {
	axis := Editor.GizmoDragAxis
	if axis < 0 || axis > 2 {
		return
	}
	origin := Editor.GizmoDragOrigin
	angle, ok := editorRotationAngleAt(mx, my, origin, gizmoAxes[axis])
	if !ok {
		return
	}
	delta := geom.WrapAngle(angle - Editor.GizmoDragStartS)
	if Editor.SnapToGrid {
		delta = snapValue(delta, mgl32.DegToRad(modelRotateSnapDeg))
	}
	applyRotationToSelection(gizmoAxes[axis], origin, delta)
}

// applyRotationToSelection swings every selected vertex around origin.
//
// A turn about X or Z moves a vertex's height as well as its footprint, and the
// two are stored separately — the footprint on a chunk point shared with every
// handle at that corner, the height per vertex. setSelectedVertexWorld
// reconciles that: a corner whose floor and ceiling handles are both selected
// gets one averaged footprint rather than whichever handle was written last.
func applyRotationToSelection(axis, origin mgl32.Vec3, delta float32) {
	if wp := activeGizmoWaypoint(); wp != nil {
		// Unwrapped — see eulerAddedAbout for why a waypoint alone keeps
		// counting past 180°.
		wp.Rotation = eulerAddedAbout(Editor.GizmoDragStartRot, axis, delta)
		return
	}
	if e := activeGizmoEntity(); e != nil {
		e.Rotation = eulerRotatedAbout(Editor.GizmoDragStartRot, axis, delta)
		return
	}
	if v := activeGizmoVolume(); v != nil {
		v.Rotation = eulerRotatedAbout(Editor.GizmoDragStartRot, axis, delta)
		return
	}
	if sc := activeGizmoScenery(); sc != nil {
		sc.Rotation = eulerRotatedAbout(Editor.GizmoDragStartRot, axis, delta)
		return
	}
	if em := activeGizmoEmitter(); em != nil {
		// An emitter's rotation aims its emission shape, which is the whole
		// reason the rotate gizmo is offered on something with no visible body.
		em.Rotation = eulerRotatedAbout(Editor.GizmoDragStartRot, axis, delta)
		return
	}
	if fx := activeGizmoEffect(); fx != nil {
		// Turns the fireball's noise, which is what tells two placements of
		// the same explosion apart.
		fx.Rotation = eulerRotatedAbout(Editor.GizmoDragStartRot, axis, delta)
		return
	}
	if sd := activeGizmoSound(); sd != nil {
		// A sound's rotation aims its cone, for the emitter's reason. It means
		// nothing on an omnidirectional one, which is why the panel draws the
		// cone controls before it is worth reaching for this.
		sd.Rotation = eulerRotatedAbout(Editor.GizmoDragStartRot, axis, delta)
		return
	}
	if s := activeGizmoSprite(); s != nil {
		// Meaningful on all three axes only when the sprite is not billboarded;
		// in the other two modes the renderer reads Z alone, as a roll. The
		// drag is allowed either way rather than being blocked, so switching a
		// sprite to Fixed keeps whatever was already dialled in.
		s.Rotation = eulerRotatedAbout(Editor.GizmoDragStartRot, axis, delta)
		return
	}
	if d := activeGizmoDecal(); d != nil {
		// Every axis is meaningful and this is the gizmo that matters most on a
		// decal: turning it aims the projection, so a mark can be swung from one
		// wall onto the next without touching a number.
		d.Rotation = eulerRotatedAbout(Editor.GizmoDragStartRot, axis, delta)
		return
	}
	if sp := activeGizmoSpawn(); sp != nil {
		// The marker's facing is derived from its rotation (SpawnDirection), so
		// turning it here is all it takes for the arrow and the direction the
		// player will look to follow the drag together.
		sp.Rotation = eulerRotatedAbout(Editor.GizmoDragStartRot, axis, delta)
		return
	}
	if ch := activeGizmoChunk(); ch != nil {
		// Only the Y ring is ever offered (gizmoChunkRotates), so axis is
		// always Y here; eulerRotatedAbout still does the wrap correctly.
		ch.Rotation = eulerRotatedAbout(Editor.GizmoDragStartRot, axis, delta).Y()
		editorChunkTransformChanged()
		return
	}
	setSelectedVertexWorld(rotationTransform(axis, origin, delta))
}

// eulerRotatedAbout adds delta about one world axis to an Euler triple.
//
// The gizmo only ever drags a world axis, and the three Euler channels *are*
// the world axes here (see EulerMatrixYXZ), so this is an addition on one
// channel rather than a quaternion composition. Composing properly would let a
// drag about X leak into Y and Z, which is exactly what an author dragging the
// X ring does not want to see happen to the numbers in the panel.
func eulerRotatedAbout(start, axis mgl32.Vec3, delta float32) mgl32.Vec3 {
	out := start
	switch {
	case axis == (mgl32.Vec3{1, 0, 0}):
		out[0] = world.WrapAngle(out[0] + delta)
	case axis == (mgl32.Vec3{0, 1, 0}):
		out[1] = world.WrapAngle(out[1] + delta)
	case axis == (mgl32.Vec3{0, 0, 1}):
		out[2] = world.WrapAngle(out[2] + delta)
	}
	return out
}

// rotationTransform is the position map a rotate drag applies: every selected
// vertex swung about origin from where it was at mouse-down.
func rotationTransform(axis, origin mgl32.Vec3, delta float32) func(int, vertexSel, mgl32.Vec3) mgl32.Vec3 {
	start := Editor.GizmoDragWorld
	return func(i int, sel vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		if i >= len(start) {
			return cur
		}
		return rotatePointAbout(start[i], origin, axis, delta)
	}
}

// ---------- drawing ----------

// renderRotateGizmo draws the three rings, brightening the one under the cursor
// or the one being dragged.
func renderRotateGizmo(mx, my int32) {
	origin, ok := rotateGizmoOrigin()
	if !ok {
		return
	}
	radius := gizmoArmLength(origin)

	hover := -1
	if editorGizmoDragging() {
		hover = Editor.GizmoDragAxis
	} else {
		hover = pickRotateRing(mx, my)
	}

	yellow := mgl32.Vec3{1, 1, 0}
	chunkOnly := gizmoChunkRotates()
	for i, axis := range gizmoAxes {
		if chunkOnly && i != 1 {
			continue
		}
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
	// The pivot itself, so it is obvious the selection turns about its shared
	// centre rather than about any one of the points in it.
	gizmoBox(origin, gizmoTipHalfPx, mgl32.Vec3{0.85, 0.85, 0.9})
}
