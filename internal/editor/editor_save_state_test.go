package editor

import (
	"testing"

	"foundry/internal/model"
)

// A model edited and edited back reads as saved again, and Save All's fresh
// check sees an edit the throttle would still be sitting on.
func TestSavedStateFollowsContent(t *testing.T) {
	prevDoc, prevState := modelDoc, modelSaved
	defer func() { modelDoc, modelSaved = prevDoc, prevState }()

	modelDoc = model.NewCubeDoc()
	modelSaved = savedState{}
	modelMarkSaved()
	if modelEditorDirty() {
		t.Fatal("a freshly baselined document reads as unsaved")
	}

	orig := modelDoc.Verts[0]
	modelDoc.Verts[0][0] += 1
	modelDoc.Touch()
	saveStateFresh = true
	dirty := modelEditorDirty()
	saveStateFresh = false
	if !dirty {
		t.Fatal("an edited document reads as saved")
	}

	modelDoc.Verts[0] = orig
	modelDoc.Touch()
	saveStateFresh = true
	dirty = modelEditorDirty()
	saveStateFresh = false
	if dirty {
		t.Fatal("a document edited back to its saved content reads as unsaved")
	}
}
