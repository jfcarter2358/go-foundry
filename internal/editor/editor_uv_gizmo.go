package editor

// editor_uv_gizmo.go — UV mode: the move, rotate and scale tools acting on a
// selected face's texture instead of its corners.
//
// U toggles the mode; G, R and C still pick the tool. Each drag writes the
// transform the face is drawn with — the override's while that is on — and
// every selected face follows, each through its own mapping.
//
//	move    two arms along the directions the drawn texture's U and V run, and
//	        a square between them for a free drag. The texel grabbed stays
//	        under the cursor; an arm keeps only its own axis of that change.
//	rotate  one ring in the face's plane. The texture turns about the gizmo's
//	        centre, following the cursor round.
//	scale   two arms along the axes the face's UV scale stretches — the
//	        projection's or unwrap's, which a rotation does not turn — and a
//	        centre box for both. Pulling out makes the texture bigger.
//
// Rotate and scale pin the texel under the centre by moving the offset, so the
// texture turns and grows about the face rather than about its UV origin.
//
// Everything goes through ws() — camera, drag state, undo, change hook — so the
// level editor and the entity editor run this same code in their own viewports.
// The tool is passed in because the two windows keep it in different places.

import (
	"math"

	"foundry/internal/geom"
	"foundry/internal/gizmo"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// The UV gizmo's handles, as GizmoDragAxis holds them. The free handle is the
// move tool's square and the scale tool's centre box; the rotate tool's ring is
// uvHandleU.
const (
	uvHandleU    = 0
	uvHandleV    = 1
	uvHandleFree = 2
)

// uvSquareHalfPx is half the on-screen size of the free-drag square.
const uvSquareHalfPx float32 = 7

// uvModeTooltip explains the UV mode toggle on both windows' toolbars.
const uvModeTooltip = "U — UV mode: the move, rotate and scale tools act on the\n" +
	"selected faces' texture instead of their corners."

// uvDragStart is one selected face's drawn transform at mouse-down, and the UV
// under the gizmo's centre then.
type uvDragStart struct {
	offset, scale mgl32.Vec2
	rotation      float32
	pivotUV       mgl32.Vec2
	ok            bool
}

// uvGizmoFrame is where the UV gizmo sits and which way its handles point, in
// world space: u/v along the drawn texture's axes, su/sv along the scale's.
type uvGizmoFrame struct {
	origin, normal mgl32.Vec3
	u, v           mgl32.Vec3
	su, sv         mgl32.Vec3
}

// uvGizmoTarget resolves a face selection to its chunk, mesh and face, or nils.
func uvGizmoTarget(sel faceSel) (*world.Chunk, *world.BrushMesh, *world.BrushFace) {
	ch, b := faceSelBrush(sel)
	m := selMesh(b, sel.detail)
	f := faceSelFace(sel)
	if ch == nil || m == nil || f == nil {
		return nil, nil, nil
	}
	return ch, m, f
}

// uvFaceNormal is a face's unit normal in its mesh's space.
func uvFaceNormal(m *world.BrushMesh, f *world.BrushFace) (mgl32.Vec3, bool) {
	pts := m.FacePoints(f)
	n := geom.PolygonNormal3D(pts)
	return n, len(pts) >= 3 && n.Len() >= 0.5
}

// uvGizmoFrameNow builds the gizmo's frame on the primary selected face. ok is
// false with no face selected, or on a face whose texture has no extent.
func uvGizmoFrameNow() (uvGizmoFrame, bool) {
	if gizmoEffectiveKind() != gizmoKindFace || ws().placementArmed() {
		return uvGizmoFrame{}, false
	}
	ch, m, f := uvGizmoTarget(primaryFaceSel())
	if f == nil {
		return uvGizmoFrame{}, false
	}
	n, ok := uvFaceNormal(m, f)
	if !ok {
		return uvGizmoFrame{}, false
	}
	drawn, ok1 := m.DrawnUVMap(f)
	base, ok2 := m.BaseUVMap(f)
	if !ok1 || !ok2 {
		return uvGizmoFrame{}, false
	}
	du, dv, ok1 := drawn.Steps(n)
	su, sv, ok2 := base.Steps(n)
	if !ok1 || !ok2 {
		return uvGizmoFrame{}, false
	}
	var c mgl32.Vec3
	pts := m.FacePoints(f)
	for _, p := range pts {
		c = c.Add(p)
	}
	c = c.Mul(1 / float32(len(pts)))
	dir := func(d mgl32.Vec3) mgl32.Vec3 { return ch.LocalToWorldDirection(d).Normalize() }
	return uvGizmoFrame{
		origin: ch.LocalToWorld(c),
		normal: dir(n),
		u:      dir(du), v: dir(dv),
		su: dir(su), sv: dir(sv),
	}, true
}

// uvGizmoPlaneHit is where the cursor's view ray meets the frame's plane. ok is
// false edge-on or behind the camera.
func uvGizmoPlaneHit(fr uvGizmoFrame, mx, my int32) (mgl32.Vec3, bool) {
	rayO, rayD := ws().ray(mx, my)
	denom := rayD.Dot(fr.normal)
	if math.Abs(float64(denom)) < 1e-4 {
		return mgl32.Vec3{}, false
	}
	t := fr.origin.Sub(rayO).Dot(fr.normal) / denom
	if t <= 0 {
		return mgl32.Vec3{}, false
	}
	return rayO.Add(rayD.Mul(t)), true
}

// uvGizmoArms is the tool's two arm directions and their length.
func uvGizmoArms(fr uvGizmoFrame, tool editorGizmoTool) ([2]mgl32.Vec3, float32) {
	if tool == editorToolScale {
		return [2]mgl32.Vec3{fr.su, fr.sv}, gizmoArmLength(fr.origin) * modelScaleArmFrac
	}
	return [2]mgl32.Vec3{fr.u, fr.v}, gizmoArmLength(fr.origin)
}

// pickUVHandle returns the handle of tool's gizmo a click at (mx, my) grabs,
// or -1.
func pickUVHandle(fr uvGizmoFrame, tool editorGizmoTool, mx, my int32) int {
	projView := ws().projView()
	originScreen, ok := projectToScreen(projView, fr.origin)
	if !ok {
		return -1
	}
	cursor := mgl32.Vec2{float32(mx), float32(my)}

	if tool == editorToolRotate {
		u, v := modelRingBasis(fr.normal)
		radius := gizmoArmLength(fr.origin)
		var prev mgl32.Vec2
		havePrev := false
		for s := 0; s <= modelRingSegments; s++ {
			angle := float64(s) / modelRingSegments * 2 * math.Pi
			sp, on := projectToScreen(projView, modelRingPoint(fr.origin, u, v, radius, angle))
			if !on {
				havePrev = false
				continue
			}
			if havePrev && distToSegment2D(cursor.X(), cursor.Y(), prev.X(), prev.Y(), sp.X(), sp.Y()) <= gizmoPickPx {
				return uvHandleU
			}
			prev, havePrev = sp, true
		}
		return -1
	}

	d := cursor.Sub(originScreen)
	if tool == editorToolScale {
		if d.Len() <= modelScaleUniformPx {
			return uvHandleFree
		}
	} else if reach := float64(uvSquareHalfPx + 2); math.Abs(float64(d.X())) <= reach && math.Abs(float64(d.Y())) <= reach {
		return uvHandleFree
	}

	arms, armLen := uvGizmoArms(fr, tool)
	best := -1
	bestD := gizmoPickPx
	for i, dir := range arms {
		tipScreen, ok := projectToScreen(projView, fr.origin.Add(dir.Mul(armLen)))
		if !ok {
			continue
		}
		dist := distToSegment2D(cursor.X(), cursor.Y(),
			originScreen.X(), originScreen.Y(), tipScreen.X(), tipScreen.Y())
		if dist <= bestD {
			bestD = dist
			best = i
		}
	}
	return best
}

// uvGizmoMeasure is what a drag of tool is measured in at (mx, my): nothing
// for move (which works from the plane hit), the angle round the ring for
// rotate, and for scale the distance along the grabbed arm or, for the centre
// box, from the centre on screen.
func uvGizmoMeasure(fr uvGizmoFrame, tool editorGizmoTool, handle int, mx, my int32) (float32, bool) {
	switch tool {
	case editorToolRotate:
		rayO, rayD := ws().ray(mx, my)
		return rotationAngleForRay(rayO, rayD, fr.origin, fr.normal)
	case editorToolScale:
		if handle == uvHandleFree {
			originScreen, ok := projectToScreen(ws().projView(), fr.origin)
			if !ok {
				return 0, false
			}
			return mgl32.Vec2{float32(mx), float32(my)}.Sub(originScreen).Len(), true
		}
		arms, _ := uvGizmoArms(fr, tool)
		rayO, rayD := ws().ray(mx, my)
		return axisParamForRay(rayO, rayD, fr.origin, arms[handle])
	}
	return 0, true
}

// uvGizmoMouseDown starts a UV drag with tool if a left click at (mx, my)
// grabbed a handle, snapshotting every selected face's drawn transform and
// pushing one undo entry for the drag. Returns true when the click was consumed.
func uvGizmoMouseDown(tool editorGizmoTool, mx, my int32) bool {
	fr, ok := uvGizmoFrameNow()
	if !ok {
		return false
	}
	handle := pickUVHandle(fr, tool, mx, my)
	if handle < 0 {
		return false
	}
	hit, ok := uvGizmoPlaneHit(fr, mx, my)
	if !ok && tool == editorToolMove {
		return true
	}
	measure, ok := uvGizmoMeasure(fr, tool, handle, mx, my)
	if !ok || (tool == editorToolScale && geom.Abs32(measure) < scaleRefMin) {
		return true
	}

	w := ws()
	starts := make([]uvDragStart, len(w.SelectedFaces))
	for i, sel := range w.SelectedFaces {
		ch, m, f := uvGizmoTarget(sel)
		if f == nil {
			continue
		}
		uvm, ok := m.DrawnUVMap(f)
		if !ok {
			continue
		}
		offset, scale, rotation := f.DrawUVTransform()
		if scale == (mgl32.Vec2{}) {
			scale = mgl32.Vec2{1, 1}
		}
		starts[i] = uvDragStart{
			offset: offset, scale: scale, rotation: rotation,
			pivotUV: uvm.At(ch.WorldToLocal(fr.origin)), ok: true,
		}
	}
	w.pushUndo()
	w.GizmoDragUVStarts = starts
	w.GizmoDragUV = true
	w.GizmoDragging = true
	w.GizmoDragTool = tool
	w.GizmoDragAxis = handle
	w.GizmoDragOrigin = fr.origin
	w.GizmoDragStartPos = hit
	w.GizmoDragStartS = measure
	return true
}

// uvGizmoEndDrag ends a UV drag in w.
func uvGizmoEndDrag(w *brushWorkspace) {
	if !w.GizmoDragUV {
		return
	}
	w.GizmoDragging = false
	w.GizmoDragUV = false
	w.GizmoDragAxis = -1
	w.GizmoDragUVStarts = nil
}

// uvScaleFactor is the texture's growth for a scale drag measured start at
// mouse-down and now under the cursor: tenths when snapping, never below
// modelScaleMin.
func uvScaleFactor(start, now float32) float32 {
	if geom.Abs32(start) < scaleRefMin {
		return 1
	}
	f := now / start
	if ws().SnapToGrid {
		f = snapValue(f, modelScaleSnapStep)
	}
	return max(f, modelScaleMin)
}

// uvGizmoMouseMove applies the cursor to the UV drag in progress.
func uvGizmoMouseMove(mx, my int32) {
	w := ws()
	if !w.GizmoDragging || !w.GizmoDragUV {
		return
	}
	fr, ok := uvGizmoFrameNow()
	if !ok {
		return
	}
	// Measured against the plane and pivot the drag started on.
	fr.origin = w.GizmoDragOrigin

	tool, handle := w.GizmoDragTool, w.GizmoDragAxis
	var worldDelta mgl32.Vec3
	var angle, factor float32
	switch tool {
	case editorToolMove:
		hit, ok := uvGizmoPlaneHit(fr, mx, my)
		if !ok {
			return
		}
		worldDelta = hit.Sub(w.GizmoDragStartPos)
	case editorToolRotate:
		a, ok := uvGizmoMeasure(fr, tool, handle, mx, my)
		if !ok {
			return
		}
		angle = geom.WrapAngle(a - w.GizmoDragStartS)
		if w.SnapToGrid {
			angle = snapValue(angle, mgl32.DegToRad(modelRotateSnapDeg))
		}
	case editorToolScale:
		// The scale arms are the unscaled mapping's axes, which a scale drag
		// does not turn, so the live frame's arms are the mouse-down ones.
		s, ok := uvGizmoMeasure(fr, tool, handle, mx, my)
		if !ok {
			return
		}
		factor = uvScaleFactor(w.GizmoDragStartS, s)
	}

	type brushKey struct{ chunkIdx, brushIdx int }
	touched := map[brushKey]bool{}
	for i, sel := range w.SelectedFaces {
		if i >= len(w.GizmoDragUVStarts) || !w.GizmoDragUVStarts[i].ok {
			continue
		}
		st := w.GizmoDragUVStarts[i]
		ch, m, f := uvGizmoTarget(sel)
		if f == nil {
			continue
		}
		pivot := ch.WorldToLocal(fr.origin)

		switch tool {
		case editorToolMove:
			f.SetDrawUVTransform(st.offset, st.scale, st.rotation)
			uvm, ok := m.DrawnUVMap(f)
			if !ok {
				continue
			}
			shift := uvm.Shift(ch.WorldToLocalDirection(worldDelta))
			switch handle {
			case uvHandleU:
				shift[1] = 0
			case uvHandleV:
				shift[0] = 0
			}
			f.SetDrawUVTransform(st.offset.Add(shift), st.scale, st.rotation)

		case editorToolRotate:
			// Turning the UVs by +d turns the texture by -d on a face that reads
			// the right way round, and by +d on a mirrored one. The cursor's
			// angle is about the primary face's normal, so a face facing the
			// other way flips too.
			f.SetDrawUVTransform(st.offset, st.scale, st.rotation)
			uvm, ok1 := m.DrawnUVMap(f)
			n, ok2 := uvFaceNormal(m, f)
			if !ok1 || !ok2 {
				continue
			}
			sign := float32(-1)
			if uvm.Handedness(n) < 0 {
				sign = -sign
			}
			if ch.LocalToWorldDirection(n).Dot(fr.normal) < 0 {
				sign = -sign
			}
			f.SetDrawUVTransform(st.offset, st.scale, world.WrapAngle(st.rotation+sign*angle))
			m.PinDrawnUV(f, pivot, st.pivotUV)

		case editorToolScale:
			// A bigger texture is fewer UVs per unit of face.
			scale := st.scale
			if handle != uvHandleV {
				scale[0] /= factor
			}
			if handle != uvHandleU {
				scale[1] /= factor
			}
			f.SetDrawUVTransform(st.offset, scale, st.rotation)
			m.PinDrawnUV(f, pivot, st.pivotUV)
		}
		touched[brushKey{sel.chunkIdx, sel.brushIdx}] = true
	}
	for k := range touched {
		ch, b := faceSelBrush(faceSel{chunkIdx: k.chunkIdx, brushIdx: k.brushIdx})
		if b == nil {
			continue
		}
		_ = b.Load()
		editorChunkGeometryChanged(ch)
	}
	if len(touched) > 0 {
		w.geometryChanged()
	}
}

// renderUVGizmo draws tool's UV handles on the primary selected face. The
// hovered or dragged handle turns yellow.
func renderUVGizmo(tool editorGizmoTool, mx, my int32) {
	fr, ok := uvGizmoFrameNow()
	if !ok {
		return
	}
	w := ws()
	dragging := w.GizmoDragging && w.GizmoDragUV
	hover := -1
	if dragging {
		fr.origin = w.GizmoDragOrigin
		hover = w.GizmoDragAxis
	} else {
		hover = pickUVHandle(fr, tool, mx, my)
	}
	yellow := mgl32.Vec3{1, 1, 0}
	centre := mgl32.Vec3{0.9, 0.9, 0.9}

	if tool == editorToolRotate {
		col := mgl32.Vec3{0.85, 0.6, 1}
		if hover == uvHandleU {
			col = yellow
		}
		u, v := modelRingBasis(fr.normal)
		radius := gizmoArmLength(fr.origin)
		prev := modelRingPoint(fr.origin, u, v, radius, 0)
		for s := 1; s <= modelRingSegments; s++ {
			p := modelRingPoint(fr.origin, u, v, radius, float64(s)/modelRingSegments*2*math.Pi)
			gizmo.GizmoLine(prev, p, col)
			prev = p
		}
		// The texture's own axes inside the ring, so a turn shows as one.
		gizmo.GizmoLine(fr.origin, fr.origin.Add(fr.u.Mul(radius*0.5)), gizmoAxisColors[0])
		gizmo.GizmoLine(fr.origin, fr.origin.Add(fr.v.Mul(radius*0.5)), gizmoAxisColors[1])
		gizmoBox(fr.origin, gizmoTipHalfPx, centre)
		return
	}

	arms, armLen := uvGizmoArms(fr, tool)
	tipPx := gizmoTipHalfPx
	if tool == editorToolScale {
		tipPx = modelScaleTipHalfPx
	}
	for i, dir := range arms {
		col := gizmoAxisColors[i]
		if i == hover {
			col = yellow
		}
		tip := fr.origin.Add(dir.Mul(armLen))
		gizmo.GizmoLine(fr.origin, tip, col)
		gizmoBox(tip, tipPx, col)
	}

	col := centre
	if hover == uvHandleFree {
		col = yellow
	}
	if tool == editorToolScale {
		gizmoBox(fr.origin, gizmoTipHalfPx*1.2, col)
		return
	}
	h := worldSizeForPixels(fr.origin, uvSquareHalfPx)
	a, b := fr.u.Mul(h), fr.v.Mul(h)
	corners := [4]mgl32.Vec3{
		fr.origin.Sub(a).Sub(b), fr.origin.Add(a).Sub(b),
		fr.origin.Add(a).Add(b), fr.origin.Sub(a).Add(b),
	}
	for i := range corners {
		gizmo.GizmoLine(corners[i], corners[(i+1)%4], col)
	}
}
