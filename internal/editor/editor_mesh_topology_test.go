package editor

// editor_mesh_topology_test.go — join, dissolve and merge in all three editors.
//
// The round trip is the test that matters: subdivide a face, join the quarters
// back, dissolve the midpoints the join leaves on its outline, and the box is
// the box it started as. Each step is checked on the way, and the solid is
// checked for convexity at every one of them — join and dissolve are offered in
// edit mode on the promise that they never change it.

import (
	"testing"

	"foundry/internal/model"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// hullLineMidpoints returns the corners of face fi that are points on a line —
// what a join leaves along the edges of the face it made.
func hullLineMidpoints(t *testing.T, fi int) []int {
	t.Helper()
	_, b := faceSelBrush(brushFaceSel(0, 0, fi))
	br := bridgeMesh(&b.BrushMesh)
	var out []int
	for _, v := range b.Faces[fi].Indices {
		if _, _, ok := br.doc.LineNeighbours(v); ok {
			out = append(out, v)
		}
	}
	return out
}

func TestJoinAndDissolveRestoreASubdividedBrush(t *testing.T) {
	defer saveEditorState()()
	ch := brushTestLevel(t)
	b := &ch.Brushes[0]
	b.Faces[boxFaceTop].Texture = "brick"
	editorSetEditContext(editorEditMode)
	setFaceSelection([]faceSel{brushFaceSel(0, 0, boxFaceTop)})
	brushSubdivideSelectedFaces()
	if n := len(ws().SelectedFaces); n != 4 {
		t.Fatalf("subdivide selected %d pieces, want 4", n)
	}

	// Join: the quarters become one face again, wearing the top's texture.
	meshJoinSelectedFaces()
	if len(b.Faces) != 6 {
		t.Fatalf("the brush has %d faces after the join, want 6", len(b.Faces))
	}
	if len(ws().SelectedFaces) != 1 {
		t.Fatalf("%d faces selected after the join, want the one it made", len(ws().SelectedFaces))
	}
	top := ws().SelectedFaces[0].faceIdx
	if tex := b.Faces[top].Texture; tex != "brick" {
		t.Errorf("the joined face wears %q, want the quarters' %q", tex, "brick")
	}
	if n := len(b.Faces[top].Indices); n != 8 {
		t.Errorf("the joined face has %d corners, want its 4 corners and 4 midpoints", n)
	}
	if len(b.Verts) != 12 {
		t.Errorf("the brush has %d verts, want 12 — the centre only the quarters used goes", len(b.Verts))
	}
	if !b.IsConvex() {
		t.Error("joining coplanar faces made the brush non-convex")
	}

	// Dissolve: select the whole face's outline in point mode. The midpoints
	// go; the corners, where three edges meet, stay — and stay selected.
	editorEnterEditUnit(editorSelPoint)
	var sels []vertexSel
	for _, v := range b.Faces[top].Indices {
		sels = append(sels, vertexSel{chunkIdx: 0, brushIdx: 0, vertIdx: v})
	}
	setVertexSelection(sels)
	meshDissolveSelectedPoints()

	if len(b.Verts) != 8 || len(b.Faces) != 6 {
		t.Fatalf("%d verts, %d faces after dissolving; want the box's 8 and 6", len(b.Verts), len(b.Faces))
	}
	for fi := range b.Faces {
		if n := len(b.Faces[fi].Indices); n != 4 {
			t.Errorf("face %d has %d corners, want a quad", fi, n)
		}
	}
	if n := len(ws().SelectedVertices); n != 4 {
		t.Errorf("%d points still selected, want the top's 4 corners", n)
	}
	if !b.IsConvex() {
		t.Error("dissolving points on the brush's edges made it non-convex")
	}
}

// Edge mode: a selected run of edges through a midpoint becomes one edge. The
// run's own ends are corners and stay, whether or not they would dissolve.
func TestDissolveInEdgeModeTakesOutTheRunsInterior(t *testing.T) {
	defer saveEditorState()()
	ch := brushTestLevel(t)
	b := &ch.Brushes[0]
	editorSetEditContext(editorEditMode)
	setFaceSelection([]faceSel{brushFaceSel(0, 0, boxFaceTop)})
	brushSubdivideSelectedFaces()
	meshJoinSelectedFaces()
	top := ws().SelectedFaces[0].faceIdx
	mids := hullLineMidpoints(t, top)
	if len(mids) != 4 {
		t.Fatalf("%d midpoints on the joined face, want 4", len(mids))
	}

	// One midpoint and its two neighbours along the face's loop: a run of two
	// edges.
	loop := b.Faces[top].Indices
	at := -1
	for i, v := range loop {
		if v == mids[0] {
			at = i
		}
	}
	run := []int{loop[(at+len(loop)-1)%len(loop)], loop[at], loop[(at+1)%len(loop)]}
	editorEnterEditUnit(editorSelEdge)
	sels := make([]vertexSel, 0, 3)
	for _, v := range run {
		sels = append(sels, vertexSel{chunkIdx: 0, brushIdx: 0, vertIdx: v})
	}
	setVertexSelection(sels)
	meshDissolveSelectedPoints()

	if len(b.Verts) != 11 {
		t.Errorf("%d verts, want 11 — exactly the one midpoint the run passed through", len(b.Verts))
	}
	if n := len(ws().SelectedVertices); n != 2 {
		t.Errorf("%d points selected, want the run's two ends — the one edge it became", n)
	}
}

// Two faces of a box meet along an edge but not in a plane: their outline is
// not a brush face, and the brush is left exactly as it was.
func TestJoinRefusesFacesThatAreNotFlatTogether(t *testing.T) {
	defer saveEditorState()()
	ch := brushTestLevel(t)
	b := &ch.Brushes[0]
	editorSetEditContext(editorEditMode)
	setFaceSelection([]faceSel{brushFaceSel(0, 0, boxFaceTop), brushFaceSel(0, 0, boxFaceNegX)})
	EditorClearUndoHistory()
	meshJoinSelectedFaces()
	if len(b.Faces) != 6 || len(b.Verts) != 8 {
		t.Errorf("a refused join changed the brush: %d faces, %d verts", len(b.Faces), len(b.Verts))
	}
	if EditorCanUndo() {
		t.Error("a join that changed nothing pushed an undo entry")
	}
}

// A face on each of two brushes is two solids' faces, and cannot be one.
func TestJoinDoesNotReachAcrossBrushes(t *testing.T) {
	defer saveEditorState()()
	ch := brushTestLevel(t)
	editorSetEditContext(editorEditMode)
	setFaceSelection([]faceSel{brushFaceSel(0, 0, boxFaceTop), brushFaceSel(0, 1, boxFaceTop)})
	meshJoinSelectedFaces()
	if len(ch.Brushes[0].Faces) != 6 || len(ch.Brushes[1].Faces) != 6 {
		t.Error("joining faces of two brushes changed one of them")
	}
}

// Merging the two ends of a box's top edge collapses it: the top and the side
// along it become triangles, and the one point left is what is selected.
func TestMergeCollapsesABrushEdge(t *testing.T) {
	defer saveEditorState()()
	ch := brushTestLevel(t)
	b := &ch.Brushes[0]
	editorSetEditContext(editorEditMode)
	editorEnterEditUnit(editorSelPoint)
	loop := b.Faces[boxFaceTop].Indices
	want := b.Verts[loop[0]].Add(b.Verts[loop[1]]).Mul(0.5)
	setVertexSelection([]vertexSel{
		{chunkIdx: 0, brushIdx: 0, vertIdx: loop[0]},
		{chunkIdx: 0, brushIdx: 0, vertIdx: loop[1]},
	})
	meshMergeSelectedPoints(false)

	if len(b.Verts) != 7 || len(b.Faces) != 6 {
		t.Fatalf("%d verts, %d faces; want 7 and 6", len(b.Verts), len(b.Faces))
	}
	if len(ws().SelectedVertices) != 1 {
		t.Fatalf("%d points selected, want the merged one", len(ws().SelectedVertices))
	}
	got, _ := vertexSelPos(ws().SelectedVertices[0])
	if !vecNear(got, ch.LocalToWorld(want), 1e-4) {
		t.Errorf("merged point at %v, want the edge's midpoint %v", got, ch.LocalToWorld(want))
	}
}

// Shift+M keeps the first point picked where it is and brings the rest to it.
func TestMergeAtFirstSelectedKeepsThatPoint(t *testing.T) {
	defer saveEditorState()()
	ch := brushTestLevel(t)
	b := &ch.Brushes[0]
	editorSetEditContext(editorEditMode)
	loop := b.Faces[boxFaceTop].Indices
	first := b.Verts[loop[2]]
	setVertexSelection([]vertexSel{
		{chunkIdx: 0, brushIdx: 0, vertIdx: loop[2]},
		{chunkIdx: 0, brushIdx: 0, vertIdx: loop[3]},
	})
	meshMergeSelectedPoints(true)
	if len(ws().SelectedVertices) != 1 {
		t.Fatalf("%d points selected, want the merged one", len(ws().SelectedVertices))
	}
	got, _ := vertexSelPos(ws().SelectedVertices[0])
	if !vecNear(got, ch.LocalToWorld(first), 1e-4) {
		t.Errorf("merged point at %v, want the first selected point's %v", got, ch.LocalToWorld(first))
	}
}

// A point on each of two brushes stays each brush's own, but both land in the
// same place — how two solids are made to meet exactly.
func TestMergeAcrossBrushesMeetsInOnePlace(t *testing.T) {
	defer saveEditorState()()
	ch := brushTestLevel(t)
	editorSetEditContext(editorEditMode)
	setVertexSelection([]vertexSel{
		{chunkIdx: 0, brushIdx: 0, vertIdx: 7},
		{chunkIdx: 0, brushIdx: 1, vertIdx: 6},
	})
	centre, _ := selectedVerticesCenter()
	meshMergeSelectedPoints(false)

	if len(ch.Brushes[0].Verts) != 8 || len(ch.Brushes[1].Verts) != 8 {
		t.Errorf("verts %d / %d; each brush keeps its own points", len(ch.Brushes[0].Verts), len(ch.Brushes[1].Verts))
	}
	if len(ws().SelectedVertices) != 2 {
		t.Fatalf("%d points selected, want one per brush", len(ws().SelectedVertices))
	}
	for _, sel := range ws().SelectedVertices {
		if got, _ := vertexSelPos(sel); !vecNear(got, centre, 1e-4) {
			t.Errorf("brush %d's point is at %v, want the shared centre %v", sel.brushIdx, got, centre)
		}
	}
}

// In detail mode the same join acts on the detail mesh and leaves the hull
// alone, carrying the quarters' texture onto the face they make.
func TestDetailJoinKeepsTheTextureAndTheHull(t *testing.T) {
	defer saveEditorState()()
	b, hull := detailTestBrush(t)
	b.Detail.Faces[boxFaceTop].Texture = "brick"
	selectDetailFace(boxFaceTop)
	detailSubdivideSelection()
	meshJoinSelectedFaces()

	if len(b.Detail.Faces) != 6 {
		t.Fatalf("the detail mesh has %d faces, want 6", len(b.Detail.Faces))
	}
	sel := ws().SelectedFaces
	if len(sel) != 1 || !sel[0].detail {
		t.Fatalf("selection %+v, want the one joined detail face", sel)
	}
	if tex := b.Detail.Faces[sel[0].faceIdx].Texture; tex != "brick" {
		t.Errorf("the joined detail face wears %q, want %q", tex, "brick")
	}
	for i, v := range hull {
		if b.Verts[i] != v {
			t.Fatalf("hull vertex %d moved from %v to %v", i, v, b.Verts[i])
		}
	}
}

// The keys claim only what they act on. X with faces selected in detail mode
// is detail's delete, not a dissolve; J in object mode is nobody's.
func TestTopologyKeysClaimOnlyWhatTheyActOn(t *testing.T) {
	defer saveEditorState()()
	detailTestBrush(t)
	selectDetailFace(boxFaceTop)
	if meshTopologyHandleKey(sdl.SCANCODE_X, false) {
		t.Error("X with a face selected was taken for a dissolve; it is detail mode's delete")
	}
	if meshTopologyHandleKey(sdl.SCANCODE_M, false) {
		t.Error("M with no points selected was claimed")
	}
	if !meshTopologyHandleKey(sdl.SCANCODE_J, false) {
		t.Error("J with a face selected was not claimed")
	}

	editorExitDetailMode()
	if meshTopologyHandleKey(sdl.SCANCODE_J, false) {
		t.Error("J was claimed in object mode, which has no faces to join")
	}
}

// ---------- the entity editor ----------

// The entity editor's corner menu offers the same point operations, and they
// act on the type's brushes rather than on the level open in the main window.
func TestEntityMenuMergesCornersOfTheTypesBrush(t *testing.T) {
	entityMenuFixture(t)
	entityWS.EditContext = editorEditMode
	hull := &entityGeomLevel.Chunks[0].Brushes[0]
	loop := hull.Faces[boxFaceTop].Indices
	entityWS.SelectedVertices = []vertexSel{
		{chunkIdx: 0, brushIdx: 0, vertIdx: loop[0]},
		{chunkIdx: 0, brushIdx: 0, vertIdx: loop[1]},
	}

	items := entityContextMenuItems()
	mustMenuItem(t, items, "Dissolve [X]")
	merge := mustMenuItem(t, items, "Merge at centre [M]")
	if merge.Disabled {
		t.Fatal("Merge is greyed out with two corners selected")
	}
	merge.Do()
	if n := len(hull.Verts); n != 7 {
		t.Errorf("the type's brush has %d verts after the merge, want 7", n)
	}
}

// ---------- the model editor ----------

// The same round trip through the model editor's menu: join the quarters of a
// subdivided face, dissolve the midpoints in vertex mode, and the cube is back.
func TestModelJoinThenDissolveRestoresTheCube(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	withModelMenuState(t)
	top := -1
	for fi := range m.Faces {
		if m.FaceNormal(fi).Y() > 0.9 {
			top = fi
		}
	}
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = m.SubdivideFaces([]int{top})

	join := mustMenuItem(t, modelContextMenuItems(), "Join")
	if join.Disabled {
		t.Fatal("Join is greyed out with four faces selected")
	}
	join.Do()
	if len(modelDoc.Faces) != 6 || len(modelSelection.Faces) != 1 {
		t.Fatalf("%d faces, %d selected; want 6 and the joined one", len(modelDoc.Faces), len(modelSelection.Faces))
	}

	modelSelection.Mode = modelSelVertex
	modelSelection.Verts = append([]int(nil), modelDoc.Faces[modelSelection.Faces[0]].Verts...)
	modelSelection.Faces = nil
	mustMenuItem(t, modelContextMenuItems(), "Dissolve").Do()
	if len(modelDoc.Verts) != 8 || len(modelDoc.Faces) != 6 {
		t.Errorf("%d verts, %d faces; want the cube's 8 and 6", len(modelDoc.Verts), len(modelDoc.Faces))
	}
	if n := len(modelSelection.Verts); n != 4 {
		t.Errorf("%d points selected after the dissolve, want the 4 corners that stayed", n)
	}
}

// An edge merged in edge mode is one point, and the editor says so by
// selecting it in vertex mode.
func TestModelMergeAnEdgeSelectsThePoint(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	withModelMenuState(t)
	modelSelection.Mode = modelSelEdge
	modelSelection.Edges = []model.EdgeKey{model.MakeEdgeKey(4, 5)}

	mustMenuItem(t, modelContextMenuItems(), "Merge at centre").Do()
	if len(modelDoc.Verts) != 7 {
		t.Fatalf("%d verts, want 7", len(modelDoc.Verts))
	}
	if modelSelection.Mode != modelSelVertex || len(modelSelection.Verts) != 1 {
		t.Fatalf("selection %v %v, want the one merged point in vertex mode", modelSelection.Mode, modelSelection.Verts)
	}
	if got, want := modelDoc.Verts[modelSelection.Verts[0]], (mgl32.Vec3{0, 0.5, -0.5}); got != want {
		t.Errorf("merged point at %v, want %v", got, want)
	}
}
