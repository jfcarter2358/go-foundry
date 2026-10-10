package editor

// editor_brush_panel_test.go — an edit made through the face fields reaches
// every selected face, and only the field that was edited.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestFaceEditReachesEverySelectedFace(t *testing.T) {
	restore := saveEditorState()
	t.Cleanup(restore)
	ch := brushTestLevel(t)
	Editor.SelectedFaces = []faceSel{
		brushFaceSel(0, 0, boxFaceTop),
		brushFaceSel(0, 0, boxFaceNegX),
		brushFaceSel(0, 1, boxFaceTop),
	}
	other := &ch.Brushes[1].Faces[boxFaceTop]
	other.UVOffset = mgl32.Vec2{0, 7}
	other.Alpha = 0.5

	primary := &ch.Brushes[0].Faces[boxFaceTop]
	before := *primary
	primary.Texture = "metal"
	primary.UVOffset[0] = 2
	applyFaceEditToSelection(&before, primary)

	for _, s := range Editor.SelectedFaces {
		if got := faceSelFace(s).Texture; got != "metal" {
			t.Errorf("face %+v wears %q, want metal", s, got)
		}
	}
	if other.UVOffset != (mgl32.Vec2{2, 7}) {
		t.Errorf("offset is %v: U should have followed and V stayed", other.UVOffset)
	}
	if other.Alpha != 0.5 {
		t.Errorf("an untouched field was overwritten: alpha %v", other.Alpha)
	}
	if got := ch.Brushes[1].Faces[boxFaceBottom].Texture; got != "concrete" {
		t.Errorf("an unselected face was repainted: %q", got)
	}
}

// Edit mode selects faces and no brushes; the panel's cursor must still land on
// the brush they belong to, or the face block is never drawn.
func TestEntityFaceSelectionSelectsItsBrush(t *testing.T) {
	entityMenuFixture(t)
	entityWS.EditContext = editorEditMode
	entityWS.SelectedFaces = []faceSel{{chunkIdx: 0, brushIdx: 0, faceIdx: 2}}
	entitySel = entityEditorSelection{}
	entityBrushSelectionSync()
	if entitySel.Kind != entitySelBrush || entitySel.Idx != 0 {
		t.Errorf("a face selection left the cursor at %+v", entitySel)
	}
}
