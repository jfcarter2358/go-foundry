package main

import (
	"foundry/internal/model"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// withModelDoc installs a document for the duration of a test and restores the
// editor globals afterwards, so tests do not leak selection state into each
// other.
func withModelDoc(t *testing.T, m *model.ModelDoc) {
	t.Helper()
	prevDoc, prevSel := modelDoc, modelSelection
	// The pending extrude is face indices into the old document; left in place
	// it can match a selection in the new one, and a press of E would then
	// re-aim that phantom instead of extruding.
	prevPending := modelExtrudePending
	modelClearPendingExtrude()
	modelDoc = m
	modelClearSelection()
	modelSelection.Mode = modelSelVertex
	t.Cleanup(func() {
		modelDoc, modelSelection = prevDoc, prevSel
		modelExtrudePending = prevPending
	})
}

func TestSelModeFaceToVertsAndBack(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)

	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{0}

	modelSetSelMode(modelSelVertex)
	if len(modelSelection.Verts) != 4 {
		t.Fatalf("face -> vertex gave %d verts, want 4", len(modelSelection.Verts))
	}

	// Back up to faces: exactly the one face whose corners are all selected.
	modelSetSelMode(modelSelFace)
	if len(modelSelection.Faces) != 1 || modelSelection.Faces[0] != 0 {
		t.Errorf("vertex -> face gave %v, want [0]", modelSelection.Faces)
	}
}

func TestSelModePartialCoverageDoesNotSelectFace(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)

	// Three of face 0's four corners. A face is selected only when it is fully
	// covered, or dragging a corner would silently pick up its whole face.
	modelSelection.Mode = modelSelVertex
	modelSelection.Verts = append([]int(nil), m.Faces[0].Verts[:3]...)

	modelSetSelMode(modelSelFace)
	if len(modelSelection.Faces) != 0 {
		t.Errorf("partially covered face was selected: %v", modelSelection.Faces)
	}
}

func TestSelModeVertsToEdges(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)

	f := m.Faces[0]
	modelSelection.Mode = modelSelVertex
	modelSelection.Verts = append([]int(nil), f.Verts...)

	modelSetSelMode(modelSelEdge)
	// A quad's four corners span its own four edges, plus the two face
	// diagonals are not edges, so exactly 4.
	if len(modelSelection.Edges) != 4 {
		t.Errorf("vertex -> edge gave %d edges, want 4: %v", len(modelSelection.Edges), modelSelection.Edges)
	}
}

func TestSelectedVertsResolvesEveryMode(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)

	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{0}
	if got := len(modelSelectedVerts()); got != 4 {
		t.Errorf("face mode resolved to %d verts, want 4", got)
	}

	modelSelection.Mode = modelSelEdge
	modelSelection.Edges = []model.EdgeKey{model.MakeEdgeKey(m.Faces[0].Verts[0], m.Faces[0].Verts[1])}
	if got := len(modelSelectedVerts()); got != 2 {
		t.Errorf("edge mode resolved to %d verts, want 2", got)
	}
}

func TestToggleIntCtrlClickAddsAndRemoves(t *testing.T) {
	sel := toggleInt(nil, 3, false)
	if len(sel) != 1 || sel[0] != 3 {
		t.Fatalf("plain click gave %v, want [3]", sel)
	}
	// Plain click replaces rather than accumulating.
	if sel = toggleInt(sel, 5, false); len(sel) != 1 || sel[0] != 5 {
		t.Fatalf("plain click gave %v, want [5]", sel)
	}
	if sel = toggleInt(sel, 7, true); len(sel) != 2 {
		t.Fatalf("ctrl+click gave %v, want two entries", sel)
	}
	// Ctrl+click on something already selected deselects it.
	if sel = toggleInt(sel, 5, true); len(sel) != 1 || sel[0] != 7 {
		t.Fatalf("ctrl+click on a selected item gave %v, want [7]", sel)
	}
}

func TestToggleIntDoesNotAliasCallerSlice(t *testing.T) {
	// The removal path slices around the element; without a full-slice
	// expression it would write into the caller's backing array.
	orig := []int{1, 2, 3}
	got := toggleInt(orig, 2, true)
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("removal gave %v, want [1 3]", got)
	}
	if orig[0] != 1 || orig[1] != 2 || orig[2] != 3 {
		t.Errorf("caller's slice was mutated: %v", orig)
	}
}

func TestModelUndoRedoRoundTrip(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	prevU, prevR := modelUndoStack, modelRedoStack
	modelUndoStack, modelRedoStack = boundedStack[modelSnapshot]{}, boundedStack[modelSnapshot]{}
	t.Cleanup(func() { modelUndoStack, modelRedoStack = prevU, prevR })

	if ModelCanUndo() {
		t.Fatal("undo available before any edit")
	}

	ModelPushUndo()
	modelDoc.SubdivideFaces([]int{0})
	subdividedFaces := len(modelDoc.Faces)
	if subdividedFaces != 9 {
		t.Fatalf("setup: faces = %d, want 9", subdividedFaces)
	}

	ModelUndo()
	if len(modelDoc.Faces) != 6 || len(modelDoc.Verts) != 8 {
		t.Errorf("after undo: %d faces, %d verts; want 6, 8", len(modelDoc.Faces), len(modelDoc.Verts))
	}
	if !modelDoc.Dirty() {
		t.Error("restored document not marked dirty; the GPU mesh would go stale")
	}

	ModelRedo()
	if len(modelDoc.Faces) != subdividedFaces {
		t.Errorf("after redo: %d faces, want %d", len(modelDoc.Faces), subdividedFaces)
	}
}

func TestModelPushUndoSnapshotsIndependently(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	prevU, prevR := modelUndoStack, modelRedoStack
	modelUndoStack, modelRedoStack = boundedStack[modelSnapshot]{}, boundedStack[modelSnapshot]{}
	t.Cleanup(func() { modelUndoStack, modelRedoStack = prevU, prevR })

	// The snapshot must be a deep copy: mutating the live document in place
	// (as MoveVerts does) must not reach back into the stack.
	ModelPushUndo()
	modelDoc.MoveVerts([]int{0}, mgl32.Vec3{0, 5, 0})
	ModelUndo()

	if modelDoc.Verts[0].Y() != -0.5 {
		t.Errorf("undo left vertex 0 at y=%v, want -0.5 — the snapshot aliased the document",
			modelDoc.Verts[0].Y())
	}
}
