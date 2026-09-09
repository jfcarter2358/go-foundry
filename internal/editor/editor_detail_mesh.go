package editor

// editor_detail_mesh.go — the tools detail mode puts in an author's hands, and
// where they come from.
//
// They are the MODEL EDITOR'S operations, run on a brush's detail mesh. Not a
// reimplementation of them: model.ModelDoc's extrude, subdivide, smooth and
// delete are called directly, on a document built from the detail mesh and read
// back afterwards. "Edit the faces of a brush the way you edit a model" is
// meant literally, and the way to keep it literal a year from now is for there
// to be one implementation to fix bugs in.
//
// # Why edit mode cannot have these
//
// Every one of them makes a mesh that is no longer convex. A real extrude
// duplicates the face and walls in the gap with side quads, which is a bump
// standing off a surface; a subdivide followed by dragging one new corner is a
// dent. On a brush's HULL both are illegal — collision reads its verts as a
// hull and the compiler clips its faces as half-spaces (world/brush.go) — which
// is why edit mode's extrude slides the face's own plane instead and why a bump
// on a wall has always had to be a second brush. On a DETAIL mesh there is no
// such rule, because nothing downstream asks it a question about volume.
//
// # The bridge
//
// A ModelFace is a vertex loop and its UVs; a BrushFace is a vertex loop plus a
// texture, a UV transform, a blend, an alpha and three flags. So the document
// carries the topology and a parallel slice carries everything else, and each
// operation says where its output faces got their attributes from. That mapping
// is the only thing here worth reading closely — get it wrong and a subdivided
// wall comes back untextured.
//
// UVs are deliberately not bridged. A brush face's texture coordinates are
// derived from its own plane when it is built (world.BuildBrushFaceMesh), not
// stored per corner, so there is nothing for a ModelFace's UVs to carry and
// nothing an operation could damage.

import (
	"foundry/internal/gfx"
	"foundry/internal/model"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	// detailExtrudeStep is how far one press of E pushes a detail face when
	// there is no grid to snap to. An eighth of a unit — half what edit mode's
	// extrude uses, because detail is worked at a smaller scale than the solids
	// it decorates: a ledge is a few centimetres proud of a wall, not a quarter
	// of a metre.
	detailExtrudeStep float32 = 0.125
)

// ---------- the bridge ----------

// meshBridge is a brush mesh viewed as a model document, plus the per-face
// attributes a ModelFace has no room for.
//
// The operations below take a *world.BrushMesh, not a brush: a hull and a
// detail mesh are the same type and the same operations are correct on both.
// Which of them an author may REACH is the editor's business, not theirs — edit
// mode offers only the ones that keep a solid convex, detail mode offers the
// lot. Subdivide is the one they share.
type meshBridge struct {
	doc *model.ModelDoc
	// attrs is index-parallel to doc.Faces. Each entry is the source face with
	// its Indices and Geometry meaningless — the topology lives in the
	// document and the GPU handles are re-made by the reload that follows.
	attrs []world.BrushFace
}

// bridgeMesh builds the document an operation runs on.
func bridgeMesh(m *world.BrushMesh) *meshBridge {
	br := &meshBridge{
		doc:   &model.ModelDoc{Verts: append([]mgl32.Vec3(nil), m.Verts...)},
		attrs: make([]world.BrushFace, len(m.Faces)),
	}
	br.doc.Faces = make([]model.ModelFace, len(m.Faces))
	for i := range m.Faces {
		br.doc.Faces[i] = model.ModelFace{
			Verts:  append([]int(nil), m.Faces[i].Indices...),
			Smooth: m.Faces[i].Smooth,
		}
		br.attrs[i] = m.Faces[i]
	}
	return br
}

// writeBack replaces the mesh with what the operation produced.
//
// Every face's GPU handle is dropped first and none is carried across. An
// operation can hand one face's attributes to several of its output faces — a
// subdivided face's four children are all the same surface — and a Geometry
// copied into two BrushFaces is one buffer two of them would free and draw. The
// reload that follows uploads the lot again, which is cheap next to being wrong.
func (br *meshBridge) writeBack(m *world.BrushMesh) bool {
	if len(br.attrs) != len(br.doc.Faces) {
		// The operation and the mapping above it disagree about how many faces
		// came out, which means the mapping is describing something the
		// document no longer does. Changing nothing is the only safe answer:
		// writing back would either index out of range or hand faces the wrong
		// textures, and both are worse than the edit not happening.
		return false
	}
	m.Release()
	m.Verts = append([]mgl32.Vec3(nil), br.doc.Verts...)
	m.Faces = make([]world.BrushFace, len(br.doc.Faces))
	for i := range br.doc.Faces {
		f := br.attrs[i]
		f.Indices = append([]int(nil), br.doc.Faces[i].Verts...)
		f.Geometry = gfx.Geometry{}
		m.Faces[i] = f
	}
	return true
}

// ---------- the operations, as pure mesh edits ----------

// meshExtrudeFaces pushes the given faces out along their shared normal,
// walling in the gap with side quads, and returns the moved cap faces.
//
// The sides inherit the FIRST selected face's attributes. The document does not
// report which cap each side grew from, and the answer that matters is the one
// an author expects: the walls of a bump are painted like the bump.
func meshExtrudeFaces(m *world.BrushMesh, faces []int, dist float32) []int {
	if m == nil || len(faces) == 0 {
		return nil
	}
	br := bridgeMesh(m)
	caps, sides := br.doc.ExtrudeFaces(faces, dist)
	if len(caps) == 0 {
		return nil
	}
	// Caps keep the indices they went in with, so their attributes are already
	// in the right places; the sides are appended, in the order the document
	// created them.
	src := br.attrs[caps[0]]
	for range sides {
		br.attrs = append(br.attrs, src)
	}
	if !br.writeBack(m) {
		return nil
	}
	return caps
}

// meshInsetFaces shrinks each of the given faces within its own plane and
// walls the gap with a ring of quads, returning the shrunken faces.
//
// One face at a time, so each ring can inherit the attributes of the face it
// was cut from rather than of whichever face happened to be first. The ring is
// appended at the END of the face list — InsetFaces does not renumber anything
// — so unlike subdivide there is no shift to carry.
func meshInsetFaces(m *world.BrushMesh, faces []int, dist float32) []int {
	if m == nil || len(faces) == 0 {
		return nil
	}
	br := bridgeMesh(m)
	todo := append([]int(nil), faces...)
	sortInts(todo)

	var out []int
	for _, fi := range todo {
		if fi < 0 || fi >= len(br.doc.Faces) {
			continue
		}
		src := br.attrs[fi]
		inner, ring := br.doc.InsetFaces([]int{fi}, dist)
		if len(inner) == 0 {
			continue
		}
		for range ring {
			br.attrs = append(br.attrs, src)
		}
		out = append(out, inner...)
	}
	if len(out) == 0 || !br.writeBack(m) {
		return nil
	}
	return out
}

// meshSubdivideFaces splits each of the given faces at its edge midpoints,
// and returns every face the split produced.
//
// One face at a time, deliberately. SubdivideFaces rebuilds the whole face list
// in place, and with several faces selected there is no way to tell from the
// result which children belong to which parent — which is exactly what the
// attribute mapping needs to know. Called per face, the answer is unambiguous:
// the children land where their parent was, and everything after it shifts.
// The midpoints are still shared between neighbours, because each call sees the
// vertices the last one inserted.
func meshSubdivideFaces(m *world.BrushMesh, faces []int) []int {
	if m == nil || len(faces) == 0 {
		return nil
	}
	br := bridgeMesh(m)

	// Ascending, so the shift each split causes applies to the faces still to
	// come and can simply be accumulated.
	todo := append([]int(nil), faces...)
	sortInts(todo)
	var out []int
	shift := 0
	for _, fi := range todo {
		at := fi + shift
		if at < 0 || at >= len(br.doc.Faces) {
			continue
		}
		src := br.attrs[at]
		children := br.doc.SubdivideFaces([]int{at})
		if len(children) == 0 {
			continue
		}
		// The children replaced their parent in place: one of them is at the
		// parent's index and the rest follow it.
		grown := make([]world.BrushFace, 0, len(br.attrs)+len(children)-1)
		grown = append(grown, br.attrs[:at]...)
		for range children {
			grown = append(grown, src)
		}
		grown = append(grown, br.attrs[at+1:]...)
		br.attrs = grown
		out = append(out, children...)
		shift += len(children) - 1
	}
	if len(out) == 0 || !br.writeBack(m) {
		return nil
	}
	return out
}

// meshSmoothFaces runs one Catmull-Clark subdivision over the whole detail
// mesh — the model editor's smoothing, which is a shape change rather than a
// shading one.
//
// Every face becomes one quad per corner, emitted in source order, so each
// child takes its parent's attributes. A face with fewer than three corners is
// skipped by the document and so is skipped here, or the two would fall out of
// step and the mesh would come back wearing the wrong textures.
func meshSmoothFaces(m *world.BrushMesh) bool {
	if m == nil || len(m.Faces) == 0 {
		return false
	}
	br := bridgeMesh(m)
	grown := make([]world.BrushFace, 0, len(br.attrs)*4)
	for i := range br.doc.Faces {
		n := len(br.doc.Faces[i].Verts)
		if n < 3 {
			continue
		}
		for j := 0; j < n; j++ {
			grown = append(grown, br.attrs[i])
		}
	}
	br.doc.CatmullClark()
	// If CatmullClark's emission rule ever stops being "one quad per corner, in
	// source order", the count above stops matching and writeBack refuses
	// rather than repainting the mesh from a mapping that no longer describes
	// it.
	br.attrs = grown
	return br.writeBack(m)
}

// meshDeleteFaces removes the given faces, and any vertex nothing else uses.
func meshDeleteFaces(m *world.BrushMesh, faces []int) bool {
	if m == nil || len(faces) == 0 {
		return false
	}
	drop := map[int]bool{}
	for _, fi := range faces {
		if fi >= 0 && fi < len(m.Faces) {
			drop[fi] = true
		}
	}
	if len(drop) == 0 {
		return false
	}
	br := bridgeMesh(m)
	br.doc.DeleteFaces(faces)
	kept := make([]world.BrushFace, 0, len(br.attrs)-len(drop))
	for i := range br.attrs {
		if !drop[i] {
			kept = append(kept, br.attrs[i])
		}
	}
	if len(kept) != len(br.doc.Faces) {
		return false
	}
	br.attrs = kept
	return br.writeBack(m)
}

// ---------- the keys ----------

// detailMeshHandleKey runs detail mode's tools. Reported as consumed only when
// one of them ran, so the keys stay available to whatever else wants them.
//
// The bindings are the model editor's where it has one — E extrudes, Delete
// deletes — and the level editor's where the model editor uses a panel button,
// since B already subdivides a brush face in edit mode and pressing it here
// should not mean something else.
func detailMeshHandleKey(sc sdl.Scancode, shift bool) bool {
	if !editorInDetailMode() {
		return false
	}
	switch sc {
	case sdl.SCANCODE_E:
		d := detailExtrudeDefault()
		if shift {
			// Shift pulls the face back in instead, the same reversal edit
			// mode's extrude gives it.
			d = -d
		}
		detailExtrudeSelection(d)
		return true
	case sdl.SCANCODE_I:
		detailInsetSelection(detailInsetDefault())
		return true
	case sdl.SCANCODE_B:
		if shift {
			detailSmoothSelection()
			return true
		}
		detailSubdivideSelection()
		return true
	case sdl.SCANCODE_DELETE, sdl.SCANCODE_X:
		detailDeleteSelection()
		return true
	}
	return false
}

// detailInsetDefault is how wide a border one press of I leaves: the grid
// quantum when snapping is on, so a frame lands on the same grid the level is
// built to, and the extrude step otherwise — a border and a bump are worked at
// the same scale, and having one key move by a different amount from the other
// makes a recess come out lopsided.
func detailInsetDefault() float32 { return detailExtrudeDefault() }

// detailExtrudeDefault is how far one press of E travels: the grid quantum when
// snapping is on, so detail lands on the same grid the level is built to, and a
// small fixed step otherwise.
func detailExtrudeDefault() float32 {
	if ws().SnapToGrid && ws().StepSize > 0 {
		return ws().StepSize
	}
	return detailExtrudeStep
}

// ---------- the tools, wrapped in undo, reload and a status line ----------

func detailExtrudeSelection(dist float32) {
	m, faces, ok := detailOpTarget("extrude")
	if !ok {
		return
	}
	ws().pushUndo()
	caps := meshExtrudeFaces(m, faces, dist)
	if len(caps) == 0 {
		EditorPopUndo()
		ws().setStatus("Nothing to extrude: the selected faces have no common direction")
		return
	}
	detailGeometryChanged()
	detailSelectFaces(caps)
	ws().setStatus("Extruded %d detail face(s) by %.3f", len(caps), dist)
}

func detailInsetSelection(dist float32) {
	m, faces, ok := detailOpTarget("inset")
	if !ok {
		return
	}
	ws().pushUndo()
	inner := meshInsetFaces(m, faces, dist)
	if len(inner) == 0 {
		EditorPopUndo()
		editorSetStatus("Nothing to inset: %.3f is wider than the selected face(s)", dist)
		return
	}
	detailGeometryChanged()
	// The inner faces stay selected, because the next thing after an inset is
	// almost always an extrude of what it left in the middle.
	detailSelectFaces(inner)
	editorSetStatus("Inset %d detail face(s) by %.3f", len(inner), dist)
}

func detailSubdivideSelection() {
	m, faces, ok := detailOpTarget("subdivide")
	if !ok {
		return
	}
	ws().pushUndo()
	children := meshSubdivideFaces(m, faces)
	if len(children) == 0 {
		EditorPopUndo()
		return
	}
	detailGeometryChanged()
	detailSelectFaces(children)
	ws().setStatus("Subdivided %d detail face(s) into %d", len(faces), len(children))
}

func detailSmoothSelection() {
	ch, b := detailBrush()
	if ch == nil || b == nil || b.Detail == nil {
		return
	}
	ws().pushUndo()
	if !meshSmoothFaces(b.Detail) {
		EditorPopUndo()
		ws().setStatus("Nothing to smooth")
		return
	}
	detailGeometryChanged()
	// Every face is new, so there is no selection to carry: smoothing acts on
	// the whole mesh rather than on what was picked.
	clearFaceSelection()
	ws().setStatus("Smoothed %s's detail mesh into %d faces", b.ID, len(b.Detail.Faces))
}

func detailDeleteSelection() {
	m, faces, ok := detailOpTarget("delete")
	if !ok {
		return
	}
	ws().pushUndo()
	if !meshDeleteFaces(m, faces) {
		EditorPopUndo()
		return
	}
	detailGeometryChanged()
	// Deleting renumbers every face after the ones that went, and every vertex
	// the mesh no longer uses, so nothing the selection named survives.
	clearFaceSelection()
	clearVertexSelection()
	ws().setStatus("Deleted %d detail face(s)", len(faces))
}

// detailOpTarget is the precondition every tool above shares: detail mode is
// on, the brush still resolves, and at least one of its detail faces is
// selected.
func detailOpTarget(what string) (*world.BrushMesh, []int, bool) {
	_, b := detailBrush()
	if b == nil || b.Detail == nil {
		return nil, nil, false
	}
	faces := detailFaceIndices()
	if len(faces) == 0 {
		// Say which of the two it is. "Select a detail face" in front of a
		// viewport that is plainly showing a selection is the most confusing
		// message this mode can print, and the selection being on the SOLID —
		// a different mesh with a different set of indices — is exactly the
		// state in which it used to appear.
		if n := len(ws().SelectedFaces); n > 0 {
			ws().setStatus(
				"Cannot %s: the %d selected face(s) are on the solid, not the detail mesh — click the surface", what, n)
			return nil, nil, false
		}
		ws().setStatus("Select a detail face to %s", what)
		return nil, nil, false
	}
	return b.Detail, faces, true
}
