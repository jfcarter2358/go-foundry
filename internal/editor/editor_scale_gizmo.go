package editor

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
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// scaleGizmoOrigin returns the centre the selection spreads about: the middle
// of a vertex selection, or an entity's own position.
//
// As with rotate, the two mean different things — a vertex selection spreads
// its points apart, an entity rewrites its Scale and grows in place — and a
// spawn marker has no size to change at all.
func scaleGizmoOrigin() (mgl32.Vec3, bool) {
	switch gizmoEffectiveKind() {
	case gizmoKindVertices:
		return selectedVerticesCenter()
	case gizmoKindFace:
		// As with rotate: the corners spread about the face's own centre, so a
		// wall gets wider and taller and a floor grows outward. Nothing is
		// resized, because a corner has no size.
		return selectedFacesCenter()
	case gizmoKindBrush:
		// The whole solid spreads about the middle of the selection, which is
		// what "make this pillar taller" means when a brush has no scale field
		// of its own.
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
	case gizmoKindScenery:
		if sc := activeGizmoScenery(); sc != nil {
			return sc.Position, true
		}
	case gizmoKindEmitter:
		// Offered so the gizmo does not vanish when an emitter is selected, but
		// applyScaleToSelection has no branch for one: an emitter's size is its
		// effect's emission shape, edited in the particle editor.
		if em := activeGizmoEmitter(); em != nil {
			return em.Position, true
		}
	case gizmoKindSound:
		// Offered on the same terms and for the same reason: a sound's reach
		// is its two distances, which are numbers in the panel.
		if sd := activeGizmoSound(); sd != nil {
			return sd.Position, true
		}
	case gizmoKindWaypoint:
		if wp := activeGizmoWaypoint(); wp != nil {
			return wp.Position, true
		}
	case gizmoKindChunk:
		if ch := activeGizmoChunk(); ch != nil {
			return ch.LocalToWorld(ch.MinBound.Add(ch.MaxBound).Mul(0.5)), true
		}
	}
	return mgl32.Vec3{}, false
}

// gizmoChunkScales reports whether the current selection is a chunk — the one
// kind whose scale is a single uniform factor (Chunk.Scale; see ModelMatrix),
// not a per-axis Vec3. The three axis arms would have nothing of their own to
// write back to, so only the uniform centre handle is offered at all.
func gizmoChunkScales() bool {
	return gizmoEffectiveKind() == gizmoKindChunk
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
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
	originScreen, ok := projectToScreen(projView, origin)
	if !ok {
		return -1
	}
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	if cursor.Sub(originScreen).Len() <= modelScaleUniformPx {
		return modelScaleUniformAxis
	}
	if gizmoChunkScales() {
		return -1
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
		originScreen, ok := projectToScreen(gfx.Projection.Mul4(gfx.Cam.Matrix), origin)
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
	if geom.Abs32(ref) < scaleRefMin {
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
	if wp := activeGizmoWaypoint(); wp != nil {
		// A real per-axis factor, the scenery case: the brush arriving at the
		// point takes exactly this scale.
		s := Editor.GizmoDragStartScale
		wp.Scale = world.ClampBrushScale(mgl32.Vec3{
			s.X() * factors.X(), s.Y() * factors.Y(), s.Z() * factors.Z(),
		})
		return
	}
	if e := activeGizmoEntity(); e != nil {
		s := Editor.GizmoDragStartScale
		e.Scale = mgl32.Vec3{
			world.EntityScaleClamp(s.X() * factors.X()),
			world.EntityScaleClamp(s.Y() * factors.Y()),
			world.EntityScaleClamp(s.Z() * factors.Z()),
		}
		return
	}
	if sc := activeGizmoScenery(); sc != nil {
		// A real scale factor on the model, so the snapshot is a scale and the
		// drag multiplies it — the entity case above, not the volume one below.
		st := Editor.GizmoDragStartScale
		sc.Scale = mgl32.Vec3{
			world.EntityScaleClamp(st.X() * factors.X()),
			world.EntityScaleClamp(st.Y() * factors.Y()),
			world.EntityScaleClamp(st.Z() * factors.Z()),
		}
		return
	}
	if v := activeGizmoVolume(); v != nil {
		// The snapshot holds the starting half-extents rather than a scale, so
		// dragging resizes the region itself. That does change the cage's shape,
		// which is the one gizmo operation on a volume that needs a rebuild.
		h := Editor.GizmoDragStartScale
		v.HalfX = world.EntityScaleClamp(h.X() * factors.X())
		v.HalfY = world.EntityScaleClamp(h.Y() * factors.Y())
		v.HalfZ = world.EntityScaleClamp(h.Z() * factors.Z())
		_ = v.Load()
		return
	}
	if sp := activeGizmoSprite(); sp != nil {
		// The snapshot holds width and height rather than a scale, so dragging
		// resizes the quad itself. Z is ignored: a quad has no depth, and the
		// Z handle drags nothing rather than silently driving the width.
		h := Editor.GizmoDragStartScale
		sp.Width = world.EntityScaleClamp(h.X() * factors.X())
		sp.Height = world.EntityScaleClamp(h.Y() * factors.Y())
		return
	}
	if ch := activeGizmoChunk(); ch != nil {
		// Only the uniform handle is ever offered (gizmoChunkScales), so
		// factors.X()/.Y()/.Z() are always equal here.
		ch.Scale = world.EntityScaleClamp(Editor.GizmoDragStartScale.X() * factors.X())
		editorChunkTransformChanged()
		return
	}
	setSelectedVertexWorld(scaleTransform(origin, factors))
}

// EntityScaleMin is the smallest magnitude an entity axis can be scaled to.

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
	if !gizmoChunkScales() {
		for i, axis := range gizmoAxes {
			col := gizmoAxisColors[i]
			if i == hover {
				col = yellow
			}
			tip := origin.Add(axis.Mul(armLen))
			gizmo.GizmoLine(origin, tip, col)
			gizmoBox(tip, modelScaleTipHalfPx, col)
		}
	}

	// The centre handle. Neutral rather than any axis colour, because it is the
	// one that belongs to none of them.
	col := mgl32.Vec3{0.85, 0.85, 0.9}
	if hover == modelScaleUniformAxis {
		col = yellow
	}
	gizmoBox(origin, gizmoTipHalfPx*1.2, col)
}
