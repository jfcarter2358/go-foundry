package editor

// editor_detail.go — DETAIL mode: shaping what a brush looks like without
// touching what it is.
//
// The 3D view has three contexts now, and they are three answers to "what does
// a click address":
//
//   - Object mode picks up a whole brush. Placing and moving solids.
//   - Edit mode picks a point, an edge or a face of one, and reshapes the
//     SOLID. Every tool here has to keep the brush convex, because collision
//     reads its verts as a hull and the compiler clips its faces as
//     half-spaces (world/brush.go).
//   - Detail mode picks the same three units of a second mesh riding on the
//     brush's surface — its detail mesh (world/brush_detail.go) — and nothing
//     done in here reaches collision at all.
//
// That third context is what makes a window reveal, a ledge, a lip around a
// doorway or a panel pressed into a wall possible. Every one of those is
// concave, so none of them can be the solid; and none of them should be
// something the player catches on. Detail mode is where a surface stops being
// the shape of the thing behind it.
//
// The gesture is Ctrl+Shift+click on a brush. Tab is already the object/edit
// round trip and detail is a place you go INTO one particular brush — the way
// the model editor is entered on one particular model — so it is reached by
// naming the brush rather than by cycling.
//
// # What is shared and what is not
//
// Everything below the selection is shared. A vertexSel, a faceSel and an
// edgeSel each carry a `detail` flag saying which of the brush's two meshes
// their indices name, selMesh resolves it, and from there the gizmos, the
// panels, the outlines, the undo and the geometry rebuild are the same code
// working on a different vertex pool. What is NOT shared is the convexity
// warning — a detail mesh has no such rule and warning about it would be noise
// — and the tools: detail mode gets the model editor's vocabulary
// (editor_detail_mesh.go), which is exactly the vocabulary edit mode cannot
// have.

import (
	"math"

	"foundry/internal/gizmo"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// selMesh resolves which of a brush's two meshes a selection addresses.
//
// nil for a detail selection on a brush that has no detail mesh, which is how
// a stale selection left behind by an undo or a re-seed stops resolving
// instead of reading the wrong vertex pool.
func selMesh(b *world.Brush, detail bool) *world.BrushMesh {
	if b == nil {
		return nil
	}
	if detail {
		return b.Detail
	}
	return &b.BrushMesh
}

// editorInDetailMode reports whether the viewport is shaping a brush's detail
// mesh.
func editorInDetailMode() bool { return Editor.EditContext == editorDetailMode }

// EditorInDetailMode is editorInDetailMode for outside the package — the
// keybinding reference (internal/app) shows a different block per context.
func EditorInDetailMode() bool { return editorInDetailMode() }

// detailBrush resolves the brush detail mode is working on, or nil.
func detailBrush() (*world.Chunk, *world.Brush) {
	if !editorInDetailMode() {
		return nil, nil
	}
	return vertexSelBrush(vertexSel{
		chunkIdx: Editor.DetailBrush.chunkIdx, brushIdx: Editor.DetailBrush.brushIdx})
}

// isDetailBrush reports whether the given chunk/brush pair is the one detail
// mode is pointed at. What picking asks to decide that nothing else in the
// level is clickable while a brush is being detailed.
func isDetailBrush(ci, bi int) bool {
	return editorInDetailMode() &&
		Editor.DetailBrush.chunkIdx == ci && Editor.DetailBrush.brushIdx == bi
}

// ---------- entering and leaving ----------

// editorEnterDetailMode points detail mode at a brush, seeding its detail mesh
// from its hull if this is the first time.
//
// Seeding a COPY rather than starting empty is what makes the mode safe to
// enter: the wall looks exactly as it did until the first edit, so an author
// who wanted a closer look has not silently deleted their level's geometry.
func editorEnterDetailMode(s brushSel) {
	if !brushSelValid(s) {
		return
	}
	ch, b := vertexSelBrush(vertexSel{chunkIdx: s.chunkIdx, brushIdx: s.brushIdx})
	if ch == nil || b == nil {
		return
	}
	if b.Detail == nil {
		// The seed is a real edit to the level — it is what a save would write
		// out — so it is undoable like any other.
		EditorPushUndo()
		b.SeedDetail()
		if err := b.Load(); err != nil {
			editorSetStatus("Detail: %v", err)
		}
		editorChunkGeometryChanged(ch)
	}

	editorClearObjectSelections()
	clearVertexSelection()
	clearFaceSelection()
	Editor.EditContext = editorDetailMode
	Editor.DetailBrush = s
	Editor.ActiveChunkIdx, Editor.ActiveBrushIdx = s.chunkIdx, s.brushIdx
	Editor.PanelFocus = panelFocusBrush
	editorVisReset()
	editorSetStatus("Detail mode: %s — %d faces. Collision is unchanged. Tab or Escape to leave",
		b.ID, len(b.Detail.Faces))
}

// editorEnterDetailModeOnSelection is the menu's and the panel's way in: detail
// the brush that is currently selected, whichever context named it.
func editorEnterDetailModeOnSelection() {
	s, ok := detailModeTarget()
	if !ok {
		editorSetStatus("Select a brush to detail")
		return
	}
	editorEnterDetailMode(s)
}

// detailModeTarget is the brush "enter detail mode" would act on: the object
// selection, or the brush whichever face or corner is selected belongs to. The
// same resolution Tab does going back to object mode, so the two agree about
// what "the brush you are working on" means.
func detailModeTarget() (brushSel, bool) {
	if s := primaryBrushSel(); brushSelValid(s) {
		return s, true
	}
	if f := primaryFaceSel(); faceSelValid(f) {
		return brushSel{chunkIdx: f.chunkIdx, brushIdx: f.brushIdx}, true
	}
	if len(Editor.SelectedVertices) > 0 {
		v := Editor.SelectedVertices[0]
		if s := (brushSel{chunkIdx: v.chunkIdx, brushIdx: v.brushIdx}); brushSelValid(s) {
			return s, true
		}
	}
	if editorInDetailMode() && brushSelValid(Editor.DetailBrush) {
		return Editor.DetailBrush, true
	}
	return brushSel{}, false
}

// editorExitDetailMode leaves detail mode with the brush that was being
// detailed selected, so leaving is the same round trip Tab makes out of edit
// mode: you come back to the thing you were inside.
func editorExitDetailMode() {
	if !editorInDetailMode() {
		return
	}
	back := Editor.DetailBrush
	clearVertexSelection()
	clearFaceSelection()
	Editor.EditContext = editorObjectMode
	Editor.DetailBrush = brushSel{chunkIdx: -1, brushIdx: -1}
	editorVisReset()
	if brushSelValid(back) {
		setBrushSelection([]brushSel{back})
		Editor.PanelFocus = panelFocusBrush
		return
	}
	Editor.PanelFocus = panelFocusNone
}

// ---------- the tools' shared tail ----------

// detailFaceIndices is the current selection as plain face indices into the
// detail mesh, which is what every operation in editor_detail_mesh.go takes.
// Entries naming anything else are dropped, so a stale hull selection cannot
// be handed to a detail op.
func detailFaceIndices() []int {
	var out []int
	seen := map[int]bool{}
	for _, sel := range Editor.SelectedFaces {
		if !sel.detail || !isDetailBrush(sel.chunkIdx, sel.brushIdx) || seen[sel.faceIdx] {
			continue
		}
		if faceSelFace(sel) == nil {
			continue
		}
		seen[sel.faceIdx] = true
		out = append(out, sel.faceIdx)
	}
	return out
}

// detailSelectFaces makes the given detail-mesh face indices the selection.
// What every op calls when it is done, since each of them renumbers the faces
// it acted on.
func detailSelectFaces(faces []int) {
	sels := make([]faceSel, 0, len(faces))
	for _, fi := range faces {
		sel := brushDetailFaceSel(Editor.DetailBrush.chunkIdx, Editor.DetailBrush.brushIdx, fi)
		if faceSelValid(sel) {
			sels = append(sels, sel)
		}
	}
	if len(sels) == 0 {
		clearFaceSelection()
		return
	}
	setFaceSelection(sels)
	Editor.PanelFocus = panelFocusFace
}

// detailGeometryChanged reuploads the brush being detailed and rolls its
// chunk's bounds up around the result — a bump pulled off a wall reaches
// outside the box the hull alone described, and a chunk culled against the
// old one would drop it as the wall left the frustum.
func detailGeometryChanged() {
	ch, b := detailBrush()
	if ch == nil || b == nil {
		return
	}
	if err := b.Load(); err != nil {
		editorSetStatus("Detail: %v", err)
	}
	editorChunkGeometryChanged(ch)
}

// ---------- what the viewport draws ----------

// detailHullColor is the wire cage the hull is drawn as while its detail mesh
// is being shaped: dim, and a different hue from any selection, because it is
// there to be read rather than clicked.
var detailHullColor = mgl32.Vec3{0.55, 0.35, 0.15}

// drawDetailHullCage outlines the brush's HULL while detail mode is on.
//
// The hull is not drawn at all otherwise — a detailed brush is drawn from its
// detail mesh and the solid's own buffers are freed — so without this the one
// thing an author needs to see in here is invisible: where the player will
// actually stop. Every face's corner loop rather than a bounding box, for the
// reason drawSelectedBrushOutline gives.
func drawDetailHullCage() {
	ch, b := detailBrush()
	if ch == nil || b == nil {
		return
	}
	for fi := range b.Faces {
		pts := b.FacePoints(&b.Faces[fi])
		n := len(pts)
		if n < 2 {
			continue
		}
		for j := 0; j < n; j++ {
			gizmo.GizmoLine(
				ch.LocalToWorld(pts[j]),
				ch.LocalToWorld(pts[(j+1)%n]), detailHullColor)
		}
	}
}

// ---------- what a click can reach ----------

// pickMeshFor decides which of a brush's two meshes a click addresses in the
// current context, and reports false for a brush that cannot be picked at all.
//
// One function, read by all three pickers — faces, corners and edges — so a
// click, a handle box and an edge prism can never disagree about what is under
// the cursor.
//
//   - Detail mode: only the brush being detailed, and only its detail mesh.
//     Everything else in the level is scenery to aim past. That is the whole
//     safety of the mode: while carving a surface, nothing you click can move
//     a solid.
//   - Edit mode: the HULL, always — that is what edit mode reshapes. On a
//     detailed brush the hull has no uploaded geometry, so its faces are
//     picked on the CPU instead; see pickBrushFaceT.
//   - Object mode: whatever is drawn, since a click there is aiming at what it
//     can see and resolves to the whole brush anyway.
func pickMeshFor(ci, bi int, b *world.Brush) (m *world.BrushMesh, detail, ok bool) {
	switch {
	case editorInDetailMode():
		if !isDetailBrush(ci, bi) || b.Detail == nil {
			return nil, false, false
		}
		return b.Detail, true, true
	case editorInEditMode():
		return &b.BrushMesh, false, true
	default:
		return b.DrawMesh(), b.HasDetail(), true
	}
}

// pickBrushFaceT is the ray test one brush face answers.
//
// It prefers the uploaded triangles, which is what the picker has always used
// and what keeps a click agreeing with the pixels. A face with nothing uploaded
// falls back to its own corners, fanned the same way BuildBrushFaceMesh fans
// them — the case that matters is the hull of a DETAILED brush, whose buffers
// were freed the moment its detail mesh took over the drawing, and which edit
// mode still has to let an author click on.
func pickBrushFaceT(origin, dir mgl32.Vec3, m *world.BrushMesh, f *world.BrushFace, model mgl32.Mat4) float32 {
	if len(f.Geometry.Vertices) > 0 {
		return pickModelT(origin, dir, f.Geometry.Vertices, model)
	}
	pts := m.FacePoints(f)
	if len(pts) < 3 {
		return float32(math.MaxFloat32)
	}
	for i := range pts {
		pts[i] = mulPoint(model, pts[i])
	}
	best := float32(math.MaxFloat32)
	for i := 1; i < len(pts)-1; i++ {
		if h, t, front := rayTriangleIntersect(origin, dir, pts[0], pts[i], pts[i+1]); h {
			if eff := effectivePickT(t, front); eff < best {
				best = eff
			}
		}
	}
	return best
}

// mulPoint carries a point through a transform. The editor's own copy of the
// one world keeps unexported (world/brush_dynamic.go).
func mulPoint(m mgl32.Mat4, p mgl32.Vec3) mgl32.Vec3 {
	if m == mgl32.Ident4() {
		return p
	}
	r := m.Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// editor3DDetailClick is the Ctrl+Shift+click gesture: enter detail mode on the
// brush under the cursor.
//
// The ray is cast against what is DRAWN, whatever context the click arrived in,
// because the author is aiming at a wall they can see. Clicking the brush
// already being detailed leaves the mode rather than doing nothing, so the
// gesture toggles and there is no way to get stuck in a mode whose way out you
// have to remember.
func editor3DDetailClick(mx, my int32) {
	hit := pick3DDrawnBrush(mx, my)
	if hit.kind != pickBrushFace {
		editorSetStatus("Ctrl+Shift+click a brush to shape its detail")
		return
	}
	if isDetailBrush(hit.chunkIdx, hit.brushIdx) {
		editorExitDetailMode()
		editorSetStatus("Left detail mode")
		return
	}
	editorEnterDetailMode(brushSel{chunkIdx: hit.chunkIdx, brushIdx: hit.brushIdx})
}

// pick3DDrawnBrush is pick3D with the context set aside: it finds the brush a
// ray hits by what is on screen, not by what the current mode has decided is
// clickable.
//
// Detail mode's own picker deliberately answers for one brush only
// (pickMeshFor), which is what makes the mode safe to work in — and which would
// otherwise make it impossible to Ctrl+Shift+click STRAIGHT FROM one detailed
// brush to the next.
func pick3DDrawnBrush(mx, my int32) pickHit {
	ctx := Editor.EditContext
	Editor.EditContext = editorObjectMode
	defer func() { Editor.EditContext = ctx }()
	return pick3D(mx, my)
}
