package main

// editor_model_pick.go — selection and the translate gizmo for the model editor.
//
// Picking has to be local to this window: screenRay, projectToScreen and
// worldSizeForPixels all read the main window's WinWidth/WinHeight/Projection/Cam
// globals, and the model editor has none of those. Each is used here through the
// viewport-parameterised variant added beside it (projectToScreenIn,
// worldSizeForPixelsIn, axisParamForRay), so the maths is shared with the level
// editor even though the camera is not.

import (
	"foundry/internal/gizmo"
	"foundry/internal/model"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// modelSelMode is what a click selects. Vertices, edges and faces are three
// views of the same mesh, so switching modes converts the current selection
// rather than discarding it.
type modelSelMode int

const (
	modelSelVertex modelSelMode = iota
	modelSelEdge
	modelSelFace
	modelSelBone
)

func (m modelSelMode) String() string {
	switch m {
	case modelSelVertex:
		return "Vertex"
	case modelSelEdge:
		return "Edge"
	case modelSelBone:
		return "Bone"
	default:
		return "Face"
	}
}

// modelSelection is the editor's current selection.
//
// Bones sit alongside the mesh selection rather than replacing it, and are not
// cleared when the mode changes — see the header of editor_model_bone.go for
// why binding vertices to a bone needs both halves alive at once.
type modelSelectionState struct {
	Mode  modelSelMode
	Verts []int
	Edges []model.EdgeKey
	Faces []int

	Bones     []int
	BoneJoint modelBoneJoint // which end of the selected bones the gizmo moves
}

var modelSelection modelSelectionState

const (
	modelVertHandlePx = float32(5)  // half-extent of a vertex handle box
	modelPickPx       = float32(10) // click slop for vertices and edges
)

// The translate gizmo deliberately reuses the 3D view's own constants and
// colours (editor_translate_gizmo.go) rather than defining its own: it is the
// same control, and an arm that is a different length or a different red in the
// two windows reads as a different tool.

var (
	modelSelColor     = mgl32.Vec3{1, 0.75, 0.1}
	modelVertColor    = mgl32.Vec3{0.55, 0.55, 0.62}
	modelFaceSelColor = mgl32.Vec3{1, 0.55, 0.05}
)

// modelGizmo tracks an axis drag in progress.
//
// StartOrigin is the gizmo's position at mouse-down, and every later sample is
// measured against it. It cannot be recomputed per event: axisParamForRay
// returns a parameter *relative to the origin it is given*, so reading the live
// gizmo position — which has moved with the selection — would subtract the
// offset already applied and leave the drag trailing the cursor.
var modelGizmo struct {
	Dragging bool
	Tool     modelGizmoToolKind // which gizmo owns this drag
	// Axis indexes gizmoAxes: the arm, the ring's normal, or the scale handle —
	// where modelScaleUniformAxis means the centre one, which is no axis.
	Axis int
	// StartS is the measure the drag is relative to: an axis parameter for
	// move, and for scale the reference the factor is a ratio of.
	StartS      float32
	StartAngle  float32 // rotate: angle around the ring at mouse-down
	StartOrigin mgl32.Vec3
	// StartBasis is the frame a scale drag measures in, frozen at mouse-down
	// for the same reason StartOrigin is — a local frame is derived from the
	// very geometry the drag is moving.
	StartBasis [3]mgl32.Vec3
	StartVerts map[int]mgl32.Vec3 // vertex positions when the drag began
}

// ---------- selection state ----------

// modelClearSelection drops everything — used when a different document is
// swapped in, where nothing that was selected still exists.
func modelClearSelection() {
	modelClearMeshSelection()
	modelClearBoneSelection()
}

// modelClearMeshSelection drops the vertex/edge/face selection, leaving the
// active bone alone.
func modelClearMeshSelection() {
	modelSelection.Verts = nil
	modelSelection.Edges = nil
	modelSelection.Faces = nil
}

// modelClearBoneSelection drops the bone selection, leaving the mesh selection
// alone.
func modelClearBoneSelection() {
	modelSelection.Bones = nil
	modelSelection.BoneJoint = modelBoneWhole
}

// modelSelectedVerts returns the vertex set the current selection resolves to.
// Every move goes through this, so there is one code path whatever mode the
// user is in.
func modelSelectedVerts() []int {
	if modelDoc == nil {
		return nil
	}
	switch modelSelection.Mode {
	case modelSelVertex:
		return modelSelection.Verts
	case modelSelEdge:
		return modelDoc.VertsOfEdges(modelSelection.Edges)
	case modelSelFace:
		return modelDoc.VertsOfFaces(modelSelection.Faces)
	default:
		// Bone mode moves bones, not vertices; the mesh follows through the
		// skin weights rather than through the gizmo.
		return nil
	}
}

// modelSelectedMeshVerts is the vertex set the *mesh* selection resolves to,
// whatever mode is current. The bone panel assigns to this, so switching to
// Bone mode to pick a bone does not throw away the points being assigned.
func modelSelectedMeshVerts() []int {
	if modelDoc == nil {
		return nil
	}
	switch {
	case len(modelSelection.Verts) > 0:
		return modelSelection.Verts
	case len(modelSelection.Edges) > 0:
		return modelDoc.VertsOfEdges(modelSelection.Edges)
	default:
		return modelDoc.VertsOfFaces(modelSelection.Faces)
	}
}

// modelSetSelMode switches selection mode, carrying the current selection over.
// Going to a coarser mode keeps only what is fully covered — three of a quad's
// four vertices do not make the quad selected.
func modelSetSelMode(mode modelSelMode) {
	if modelDoc == nil || mode == modelSelection.Mode {
		modelSelection.Mode = mode
		return
	}
	// Bone mode shares no unit with the other three — a bone is not a set of
	// vertices — so there is nothing to convert in either direction, and the
	// mesh selection is left exactly as it was for the panel to assign.
	if mode == modelSelBone || modelSelection.Mode == modelSelBone {
		modelSelection.Mode = mode
		return
	}
	verts := modelSelectedVerts()
	inSel := make(map[int]bool, len(verts))
	for _, v := range verts {
		inSel[v] = true
	}

	modelSelection.Mode = mode
	modelSelection.Verts = nil
	modelSelection.Edges = nil
	modelSelection.Faces = nil

	switch mode {
	case modelSelVertex:
		modelSelection.Verts = verts

	case modelSelEdge:
		for _, e := range modelDoc.Edges() {
			if inSel[e.A] && inSel[e.B] {
				modelSelection.Edges = append(modelSelection.Edges, e)
			}
		}

	case modelSelFace:
		for fi := range modelDoc.Faces {
			covered := len(modelDoc.Faces[fi].Verts) > 0
			for _, v := range modelDoc.Faces[fi].Verts {
				if !inSel[v] {
					covered = false
					break
				}
			}
			if covered {
				modelSelection.Faces = append(modelSelection.Faces, fi)
			}
		}
	}
}

// ---------- picking ----------

// modelViewport returns the pop-out's pixel size, or false when it is closed.
func modelViewport() (w, h int32, ok bool) {
	if modelEditorWindow == nil {
		return 0, 0, false
	}
	return modelEditorWindow.W, modelEditorWindow.H, true
}

func modelProjView() mgl32.Mat4 {
	w, h, ok := modelViewport()
	if !ok {
		return mgl32.Ident4()
	}
	return modelCam.proj(w, h).Mul4(modelCam.view())
}

// modelPickFace ray-casts against every face's triangulation and returns the
// nearest hit. Unlike pick3D (editor_3d_pick.go), which reports which *surface*
// was hit, this has to report which face — the face is the unit everything in
// this editor operates on.
func modelPickFace(mx, my int32) (int, bool) {
	w, h, ok := modelViewport()
	if !ok || modelDoc == nil {
		return 0, false
	}
	origin, dir := modelCam.ray(mx, my, w, h)

	best := -1
	bestT := float32(math.MaxFloat32)
	for fi := range modelDoc.Faces {
		f := &modelDoc.Faces[fi]
		for _, tri := range modelDoc.FaceTriangles(fi) {
			a := modelDoc.Verts[f.Verts[tri[0]]]
			b := modelDoc.Verts[f.Verts[tri[1]]]
			c := modelDoc.Verts[f.Verts[tri[2]]]
			hit, t, front := rayTriangleIntersect(origin, dir, a, b, c)
			if !hit {
				continue
			}
			// Faces are routinely viewed from behind while modelling, so back
			// faces are pickable — just nudged behind front ones so a solid's
			// near side wins a tie.
			if et := effectivePickT(t, front); et < bestT {
				bestT = et
				best = fi
			}
		}
	}
	return best, best >= 0
}

// modelPickVertex returns the vertex nearest the cursor in screen space, within
// modelPickPx. Depth breaks ties so the near side of a solid wins.
func modelPickVertex(mx, my int32) (int, bool) {
	w, h, ok := modelViewport()
	if !ok || modelDoc == nil {
		return 0, false
	}
	modelUpdateVisibility()
	projView := modelProjView()
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	camPos := modelCam.position()

	best := -1
	bestD := modelPickPx
	bestDepth := float32(math.MaxFloat32)
	for vi, p := range modelDoc.Verts {
		// In solid shading a vertex behind the surface is not on screen, so it
		// must not be clickable either — otherwise the click grabs geometry the
		// user cannot see. Wireframe reports everything visible.
		if !modelVertexVisible(vi) {
			continue
		}
		sp, ok := projectToScreenIn(projView, p, w, h)
		if !ok {
			continue
		}
		d := sp.Sub(cursor).Len()
		if d > modelPickPx {
			continue
		}
		depth := p.Sub(camPos).Len()
		if d < bestD-1 || (d <= bestD+1 && depth < bestDepth) {
			best, bestD, bestDepth = vi, d, depth
		}
	}
	return best, best >= 0
}

// modelPickEdge returns the edge whose screen-space segment the cursor is
// nearest, within modelPickPx.
func modelPickEdge(mx, my int32) (model.EdgeKey, bool) {
	w, h, ok := modelViewport()
	if !ok || modelDoc == nil {
		return model.EdgeKey{}, false
	}
	modelUpdateVisibility()
	projView := modelProjView()
	camPos := modelCam.position()

	var best model.EdgeKey
	found := false
	bestD := modelPickPx
	bestDepth := float32(math.MaxFloat32)
	for _, e := range modelDoc.Edges() {
		if e.A >= len(modelDoc.Verts) || e.B >= len(modelDoc.Verts) {
			continue
		}
		if !modelEdgeVisible(e) {
			continue
		}
		pa, pb := modelDoc.Verts[e.A], modelDoc.Verts[e.B]
		sa, okA := projectToScreenIn(projView, pa, w, h)
		sb, okB := projectToScreenIn(projView, pb, w, h)
		if !okA || !okB {
			continue
		}
		d := distToSegment2D(float32(mx), float32(my), sa.X(), sa.Y(), sb.X(), sb.Y())
		if d > modelPickPx {
			continue
		}
		depth := pa.Add(pb).Mul(0.5).Sub(camPos).Len()
		if d < bestD-1 || (d <= bestD+1 && depth < bestDepth) {
			best, bestD, bestDepth, found = e, d, depth, true
		}
	}
	return best, found
}

// modelEditorClick resolves a left-click into a selection change. additive is
// ctrl-held. Returns true when the click was consumed.
func modelEditorClick(mx, my int32, additive bool) bool {
	if modelDoc == nil {
		return false
	}
	switch modelSelection.Mode {
	case modelSelVertex:
		vi, ok := modelPickVertex(mx, my)
		if !ok {
			if !additive {
				modelClearMeshSelection()
			}
			return false
		}
		modelSelection.Verts = toggleInt(modelSelection.Verts, vi, additive)

	case modelSelEdge:
		e, ok := modelPickEdge(mx, my)
		if !ok {
			if !additive {
				modelClearMeshSelection()
			}
			return false
		}
		modelSelection.Edges = toggleEdge(modelSelection.Edges, e, additive)

	case modelSelFace:
		fi, ok := modelPickFace(mx, my)
		if !ok {
			if !additive {
				modelClearMeshSelection()
			}
			return false
		}
		modelSelection.Faces = toggleInt(modelSelection.Faces, fi, additive)

	case modelSelBone:
		return modelBoneClick(mx, my, additive)
	}
	return true
}

// toggleInt adds v to sel when additive (removing it if already there, so
// ctrl+click deselects), or replaces the selection outright when not.
func toggleInt(sel []int, v int, additive bool) []int {
	if !additive {
		return []int{v}
	}
	for i, x := range sel {
		if x == v {
			return append(sel[:i:i], sel[i+1:]...)
		}
	}
	return append(sel, v)
}

func toggleEdge(sel []model.EdgeKey, e model.EdgeKey, additive bool) []model.EdgeKey {
	if !additive {
		return []model.EdgeKey{e}
	}
	for i, x := range sel {
		if x == e {
			return append(sel[:i:i], sel[i+1:]...)
		}
	}
	return append(sel, e)
}

// ---------- translate gizmo ----------

// modelGizmoOrigin is the centroid of the selected vertices.
func modelGizmoOrigin() (mgl32.Vec3, bool) {
	if modelSelection.Mode == modelSelBone {
		return modelBoneGizmoOrigin()
	}
	verts := modelSelectedVerts()
	if modelDoc == nil || len(verts) == 0 {
		return mgl32.Vec3{}, false
	}
	var c mgl32.Vec3
	n := 0
	for _, v := range verts {
		if v >= 0 && v < len(modelDoc.Verts) {
			c = c.Add(modelDoc.Verts[v])
			n++
		}
	}
	if n == 0 {
		return mgl32.Vec3{}, false
	}
	return c.Mul(1 / float32(n)), true
}

// modelGizmoArmLength keeps the arms a constant on-screen length.
func modelGizmoArmLength(origin mgl32.Vec3) float32 {
	return modelWorldSizeForPixels(origin, gizmoArmPx)
}

// modelNormalArmColor marks the single normal arm out from the three world
// ones: it is not X, Y or Z, and colouring it like one of them would read as a
// world axis that had wandered off true.
var modelNormalArmColor = mgl32.Vec3{0.2, 0.95, 0.9}

// modelMoveAxes returns the directions the translate gizmo offers and the
// colours to draw them in, indexed together. Normally that is the three world
// axes; while a pending extrude is aimed at its own normal (a second press of
// E) it is that one direction alone, because the whole point of aiming is that
// the other three are not what the user wants to drag along.
func modelMoveAxes() ([]mgl32.Vec3, []mgl32.Vec3) {
	if n, ok := modelExtrudeNormalAxis(); ok {
		return []mgl32.Vec3{n}, []mgl32.Vec3{modelNormalArmColor}
	}
	return gizmoAxes[:], gizmoAxisColors[:]
}

// modelMoveAxisDir is the world direction of move-gizmo arm i, guarding against
// an index left over from a set of arms that is no longer on screen.
func modelMoveAxisDir(i int) (mgl32.Vec3, bool) {
	dirs, _ := modelMoveAxes()
	if i < 0 || i >= len(dirs) {
		return mgl32.Vec3{}, false
	}
	return dirs[i], true
}

// modelPickGizmoAxis returns the axis a click grabs, or -1.
func modelPickGizmoAxis(mx, my int32) int {
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
	armLen := modelGizmoArmLength(origin)

	dirs, _ := modelMoveAxes()
	best := -1
	bestD := gizmoPickPx
	for i, axis := range dirs {
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

// modelGizmoMouseDown starts an axis drag if the click landed on an arm.
// Returns true when the click was consumed by the gizmo.
func modelGizmoMouseDown(mx, my int32) bool {
	switch modelGizmoTool {
	case modelToolRotate:
		return modelRotateMouseDown(mx, my)
	case modelToolScale:
		return modelScaleMouseDown(mx, my)
	}
	axis := modelPickGizmoAxis(mx, my)
	if axis < 0 {
		return false
	}
	origin, ok := modelGizmoOrigin()
	if !ok {
		return false
	}
	w, h, vok := modelViewport()
	if !vok {
		return false
	}
	dir, ok := modelMoveAxisDir(axis)
	if !ok {
		return false
	}
	rayO, rayD := modelCam.ray(mx, my, w, h)
	s, ok := axisParamForRay(rayO, rayD, origin, dir)
	if !ok {
		return false
	}

	// One undo entry for the whole drag, pushed here rather than per motion
	// event — the same rule captureGizmoDragStart follows for the level editor.
	ModelPushUndo()

	modelGizmo.Dragging = true
	modelGizmo.Tool = modelToolMove
	modelGizmo.Axis = axis
	modelGizmo.StartS = s
	modelGizmo.StartOrigin = origin
	modelGizmo.StartVerts = map[int]mgl32.Vec3{}
	if modelSelection.Mode == modelSelBone {
		if modelPoseActive() {
			modelPoseGizmoCapture()
			return true
		}
		// A bone drag has to snapshot the whole mesh, not the selection: the
		// vertices it moves are the ones weighted to the bone, which is not a
		// set the gizmo knows about.
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

// modelGizmoMouseMove applies the drag. Both the reference origin and the
// positions being offset come from the snapshot taken at mouse-down, not from
// the previous frame, so rounding cannot accumulate and snapping is absolute.
func modelGizmoMouseMove(mx, my int32) {
	if !modelGizmo.Dragging || modelDoc == nil {
		return
	}
	switch modelGizmo.Tool {
	case modelToolRotate:
		modelRotateMouseMove(mx, my)
		return
	case modelToolScale:
		modelScaleMouseMove(mx, my)
		return
	}
	w, h, vok := modelViewport()
	if !vok {
		return
	}
	axis, ok := modelMoveAxisDir(modelGizmo.Axis)
	if !ok {
		return
	}
	rayO, rayD := modelCam.ray(mx, my, w, h)
	s, ok := axisParamForRay(rayO, rayD, modelGizmo.StartOrigin, axis)
	if !ok {
		return
	}
	delta := s - modelGizmo.StartS
	if modelSelection.Mode == modelSelBone {
		if modelPoseActive() {
			modelPoseMoveApply(modelGizmo.Axis, delta)
			return
		}
		modelBoneGizmoApply(modelGizmo.Axis, delta)
		return
	}
	if _, normal := modelExtrudeNormalAxis(); normal {
		// Off-axis, so there is no single coordinate to round: the grid step
		// governs the distance travelled instead, which is the depth the user
		// is choosing. Snapping the components would drag the cap off its own
		// normal and shear the sides it just walled in.
		offset := axis.Mul(gizmoSnap(delta))
		for v, start := range modelGizmo.StartVerts {
			modelDoc.SetVertPos(v, start.Add(offset))
		}
		modelAfterMeshMove()
		return
	}
	// Snapped per vertex, from that vertex's own start — the shape of
	// dragVerticesAlongAxis (editor_translate_gizmo.go), and the reason every
	// point that moves lands on a grid line. Snapping the selection's centroid
	// instead only guarantees the *average* is on the grid: drag a face whose
	// corners sit at different offsets and not one of them ends up on it.
	for v, start := range modelGizmo.StartVerts {
		modelDoc.SetVertPos(v, axisMoved(start, modelGizmo.Axis, delta))
	}
	modelAfterMeshMove()
}

func modelGizmoMouseUp() {
	modelGizmo.Dragging = false
	modelGizmo.Tool = modelToolMove
	modelGizmo.StartVerts = nil
	modelBoneGizmoRelease()
	modelPoseGizmoRelease()
}

// modelNudgeSelection moves the selection along one world axis, through the
// same per-element snapping as a gizmo drag so the panel's buttons and the
// mouse agree about where things end up.
//
// Bone mode nudges the selected joints rather than vertices, and goes through
// modelBoneEdit so the points weighted to those bones come along — the rule
// every other bone edit follows.
func modelNudgeSelection(axis int, delta float32) {
	if modelDoc == nil || axis < 0 || axis > 2 || delta == 0 {
		return
	}
	if modelSelection.Mode == modelSelBone {
		if modelPoseActive() {
			ModelPushUndo()
			modelPoseGizmoCapture()
			modelPoseMoveApply(axis, delta)
			modelPoseGizmoRelease()
			return
		}
		modelBoneEdit(func() {
			for _, bi := range modelSelection.Bones {
				modelNudgeBone(bi, axis, delta)
			}
		})
		return
	}
	for _, v := range modelSelectedVerts() {
		if v >= 0 && v < len(modelDoc.Verts) {
			modelDoc.SetVertPos(v, axisMoved(modelDoc.Verts[v], axis, delta))
		}
	}
	modelAfterMeshMove()
}

// modelGizmoDragging reports whether an axis drag is in progress.
func modelGizmoDragging() bool { return modelGizmo.Dragging }

// ---------- overlays ----------

// drawModelSelection queues the selection highlight and the gizmo. Called with
// depth testing off so handles stay visible through the mesh — a vertex on the
// far side is still a vertex you want to be able to grab.
func drawModelSelection(mx, my int32) {
	if modelDoc == nil {
		return
	}
	if _, _, ok := modelViewport(); !ok {
		return
	}

	switch modelSelection.Mode {
	case modelSelVertex:
		sel := make(map[int]bool, len(modelSelection.Verts))
		for _, v := range modelSelection.Verts {
			sel[v] = true
		}
		// With a bone active, unselected vertices are tinted by how much that
		// bone moves them. It is the only way to see a binding: weights are
		// invisible until something is dragged, and by then it is too late to
		// notice that half a limb was left out.
		active := modelActiveBone()
		for vi, p := range modelDoc.Verts {
			// A hidden vertex offers no handle — but one that is already
			// selected keeps its marker, because that is feedback about what an
			// edit will touch, not an invitation to click.
			if !sel[vi] && !modelVertexVisible(vi) {
				continue
			}
			col := modelVertColor
			if active >= 0 {
				if w := modelDoc.WeightOf(vi, active); w > 0 {
					col = modelWeightColor(w)
				}
			}
			if sel[vi] {
				col = modelSelColor
			}
			modelGizmoBox(p, modelVertHandlePx, col)
		}

	case modelSelEdge:
		for _, e := range modelSelection.Edges {
			if e.A < len(modelDoc.Verts) && e.B < len(modelDoc.Verts) {
				gizmo.GizmoLine(modelDoc.Verts[e.A], modelDoc.Verts[e.B], modelSelColor)
			}
		}

	case modelSelFace:
		for _, fi := range modelSelection.Faces {
			if fi < 0 || fi >= len(modelDoc.Faces) {
				continue
			}
			f := &modelDoc.Faces[fi]
			n := len(f.Verts)
			for i := 0; i < n; i++ {
				a := modelDoc.Verts[f.Verts[i]]
				b := modelDoc.Verts[f.Verts[(i+1)%n]]
				gizmo.GizmoLine(a, b, modelFaceSelColor)
			}
			// A short normal stub: which way "out" is decides which way an
			// extrude goes, so it is worth being able to see it.
			c := modelDoc.FaceCenter(fi)
			gizmo.GizmoLine(c, c.Add(modelDoc.FaceNormal(fi).Mul(0.25)), modelFaceSelColor)
		}
	}

	switch modelGizmoTool {
	case modelToolRotate:
		drawModelRotateGizmo(mx, my)
	case modelToolScale:
		drawModelScaleGizmo(mx, my)
	default:
		drawModelGizmo(mx, my)
	}
}

// drawModelGizmo draws the three translate arms, brightening the one under the
// cursor (or the one being dragged).
func drawModelGizmo(mx, my int32) {
	origin, ok := modelGizmoOrigin()
	if !ok {
		return
	}
	armLen := modelGizmoArmLength(origin)

	hover := -1
	if modelGizmo.Dragging {
		hover = modelGizmo.Axis
	} else {
		hover = modelPickGizmoAxis(mx, my)
	}

	dirs, cols := modelMoveAxes()
	yellow := mgl32.Vec3{1, 1, 0}
	for i, axis := range dirs {
		col := cols[i]
		if i == hover {
			col = yellow
		}
		tip := origin.Add(axis.Mul(armLen))
		gizmo.GizmoLine(origin, tip, col)
		// The grab box at the tip is the affordance that makes the arm findable
		// — a one-pixel line is a hard target at any distance. Same size and
		// placement as renderTranslateGizmo's.
		modelGizmoBox(tip, gizmoTipHalfPx, col)
	}
}

// modelGizmoBox draws a screen-constant cube at p. This is gizmoBox
// (editor_3d_pick.go) against this window's camera; the original sizes itself
// from the main window's Projection and WinHeight.
func modelGizmoBox(p mgl32.Vec3, halfPx float32, col mgl32.Vec3) {
	half := modelWorldSizeForPixels(p, halfPx)
	hv := mgl32.Vec3{half, half, half}
	gizmo.GizmoAABB(p.Sub(hv), p.Add(hv), col)
}

// modelWorldSizeForPixels is worldSizeForPixels against this window's viewport.
func modelWorldSizeForPixels(p mgl32.Vec3, halfPx float32) float32 {
	w, h, ok := modelViewport()
	if !ok {
		return 0.05
	}
	return worldSizeForPixelsIn(p, modelCam.position(), halfPx,
		modelCam.proj(w, h).At(1, 1), h)
}

// modelEditorSelectionKey handles the selection-mode hotkeys. Returns true when
// the key was consumed.
func modelEditorSelectionKey(t sdl.KeyboardEvent) bool {
	switch t.Keysym.Scancode {
	case sdl.SCANCODE_1:
		modelSetSelMode(modelSelVertex)
	case sdl.SCANCODE_2:
		modelSetSelMode(modelSelEdge)
	case sdl.SCANCODE_3:
		modelSetSelMode(modelSelFace)
	case sdl.SCANCODE_4:
		modelSetSelMode(modelSelBone)
	default:
		return false
	}
	return true
}
