package editor

// editor_object_mode_test.go — the object/edit split.
//
// What is worth asserting is not that a bool flips, but the three things the
// split exists to give an author: that a click means a different thing in each
// context, that the gizmo moves the WHOLE brush in object mode, and that Tab
// does not lose your place in either direction.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// A fresh editor is in object mode. This is the default the whole feature is
// stated in terms of, and it is a zero value, so it is exactly the kind of
// thing a later refactor can flip without noticing.
func TestObjectModeIsTheDefault(t *testing.T) {
	defer saveEditorSelection()()
	Editor = EditorState{}
	if editorInEditMode() {
		t.Error("a zero EditorState is in edit mode")
	}
}

// The same click, the same geometry: object mode picks up the brush, edit mode
// picks the face.
func TestObjectModeSelectsTheWholeBrush(t *testing.T) {
	defer saveEditorSelection()()
	click := centreClick()
	Editor.level = occluderLevel(t)
	Editor.SpawnPlacementMode = false
	Editor.EditContext = editorObjectMode
	editorVisReset()

	Editor3DHandleClick(click)

	if len(Editor.SelectedBrushes) != 1 {
		t.Fatalf("object mode selected %d brushes, want 1", len(Editor.SelectedBrushes))
	}
	if got := Editor.SelectedBrushes[0]; got != (brushSel{chunkIdx: 0, brushIdx: 0}) {
		t.Errorf("selected %+v, want brush 0 of chunk 0", got)
	}
	if len(Editor.SelectedFaces) != 0 {
		t.Errorf("it also selected %d faces; the two contexts must not both be live",
			len(Editor.SelectedFaces))
	}
	if Editor.GizmoKind != gizmoKindBrush {
		t.Errorf("gizmo kind = %q, want %q", Editor.GizmoKind, gizmoKindBrush)
	}
	if Editor.PanelFocus != panelFocusBrush {
		t.Errorf("panel focus = %q, want %q", Editor.PanelFocus, panelFocusBrush)
	}
}

// Ctrl+click builds a multi-selection and takes one back out, the same gesture
// and the same toggle rule face mode has.
func TestObjectModeCtrlClickTogglesTheSelection(t *testing.T) {
	defer saveEditorSelection()()
	brushTestLevel(t)
	Editor.EditContext = editorObjectMode

	a := brushSel{chunkIdx: 0, brushIdx: 0}
	b := brushSel{chunkIdx: 0, brushIdx: 1}
	editorSelectBrushObject(a, false)
	editorSelectBrushObject(b, true)
	if len(Editor.SelectedBrushes) != 2 {
		t.Fatalf("ctrl+click left %d brushes selected, want 2", len(Editor.SelectedBrushes))
	}
	editorSelectBrushObject(b, true)
	if len(Editor.SelectedBrushes) != 1 || Editor.SelectedBrushes[0] != a {
		t.Errorf("ctrl+clicking the second brush again left %+v, want only the first",
			Editor.SelectedBrushes)
	}
}

// The gizmo drags every corner of the selection, which is what makes moving a
// brush a move rather than a shear. Two brushes at once, because a
// multi-selection has to move as one body.
func TestObjectModeGizmoDragsEveryCorner(t *testing.T) {
	defer saveEditorSelection()()
	ch := brushTestLevel(t)
	Editor.EditContext = editorObjectMode

	setBrushSelection([]brushSel{{chunkIdx: 0, brushIdx: 0}, {chunkIdx: 0, brushIdx: 1}})
	want := len(ch.Brushes[0].Verts) + len(ch.Brushes[1].Verts)
	if got := len(gizmoVertexSels()); got != want {
		t.Fatalf("the gizmo would drag %d handles, want all %d corners", got, want)
	}

	before := append([]mgl32.Vec3(nil), Editor.level.Chunks[0].Brushes[0].Verts...)
	Editor.GizmoDragWorld = selectedVerticesWorld()
	setSelectedVertexWorld(func(i int, _ vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		return Editor.GizmoDragWorld[i].Add(mgl32.Vec3{3, 0, 0})
	})

	after := Editor.level.Chunks[0].Brushes[0].Verts
	for i := range before {
		if !vec3Close(after[i], before[i].Add(mgl32.Vec3{3, 0, 0}), 1e-4) {
			t.Fatalf("corner %d moved to %v, want %v — the whole solid should translate",
				i, after[i], before[i].Add(mgl32.Vec3{3, 0, 0}))
		}
	}
	if got := len(Editor.level.Chunks[0].Brushes[1].Verts); got == 0 {
		t.Fatal("the second brush lost its corners")
	}
}

// Tab is a round trip: into edit mode on a selected brush and back out again
// leaves that same brush selected.
func TestTabCarriesTheSelectionBothWays(t *testing.T) {
	defer saveEditorSelection()()
	brushTestLevel(t)
	Editor.EditContext = editorObjectMode
	editorSelectBrushObject(brushSel{chunkIdx: 0, brushIdx: 1}, false)

	editorToggleEditContext()
	if !editorInEditMode() {
		t.Fatal("Tab did not enter edit mode")
	}
	if len(Editor.SelectedBrushes) != 0 {
		t.Error("the brush selection survived into edit mode")
	}
	if Editor.ActiveBrushIdx != 1 {
		t.Errorf("ActiveBrushIdx = %d, want the brush that was selected", Editor.ActiveBrushIdx)
	}

	// Pick a face of it, then come back.
	editorSelectFace(brushFaceSel(0, 1, 2), false)
	editorToggleEditContext()
	if editorInEditMode() {
		t.Fatal("Tab did not leave edit mode")
	}
	if len(Editor.SelectedBrushes) != 1 || Editor.SelectedBrushes[0].brushIdx != 1 {
		t.Errorf("coming back selected %+v, want the brush the face belonged to",
			Editor.SelectedBrushes)
	}
	if len(Editor.SelectedFaces) != 0 {
		t.Error("the face selection survived into object mode")
	}
}

// Asking for a unit while in object mode goes there rather than doing nothing —
// reaching for a corner is exactly the intent, and a dead key teaches an author
// the editor is broken. (What 1, 2 and 3 are bound to; see editor.go.)
func TestUnitKeysEnterEditMode(t *testing.T) {
	defer saveEditorSelection()()
	brushTestLevel(t)
	Editor.EditContext = editorObjectMode

	editorEnterEditUnit(editorSelPoint)

	if !editorInEditMode() {
		t.Fatal("asking for point mode from object mode did not enter edit mode")
	}
	if Editor.SelMode != editorSelPoint {
		t.Errorf("selection unit = %v, want Point", Editor.SelMode)
	}
}

// Delete works on either selection, because a brush is what a level is built
// from whichever unit named it.
func TestDeleteRemovesAnObjectModeSelection(t *testing.T) {
	defer saveEditorSelection()()
	ch := brushTestLevel(t)
	before := len(ch.Brushes)
	Editor.EditContext = editorObjectMode
	editorSelectBrushObject(brushSel{chunkIdx: 0, brushIdx: 0}, false)

	EditorDeleteSelected()

	if got := len(Editor.level.Chunks[0].Brushes); got != before-1 {
		t.Fatalf("%d brushes left, want %d", got, before-1)
	}
	if Editor.level.Chunks[0].Brushes[0].ID != "b" {
		t.Errorf("the wrong brush was deleted; %q survived", Editor.level.Chunks[0].Brushes[0].ID)
	}
	if len(Editor.SelectedBrushes) != 0 {
		t.Error("the selection survived the delete it named")
	}
}

// The Dynamic flag is authored on the brush and nothing else, so the panel's
// checkbox and a script's read have to agree about one field.
func TestDynamicIsPlainAuthoredState(t *testing.T) {
	defer saveEditorSelection()()
	ch := brushTestLevel(t)
	ch.Brushes[0].Dynamic = true

	var b *world.Brush
	if _, b = vertexSelBrush(vertexSel{chunkIdx: 0, brushIdx: 0}); b == nil {
		t.Fatal("cannot resolve the brush")
	}
	if !b.Dynamic {
		t.Error("Dynamic did not survive into the level the editor holds")
	}
}
