package editor

// One selection at a time — the rule the Explorer and the Properties block
// below it are both built on. Every one of these used to be two properties
// blocks up at once, neither of them wrong.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// selectTestLevel is a level with one chunk of two brushes and one of every
// level-scoped object, pointed at by the editor.
func selectTestLevel(t *testing.T) *world.Level {
	t.Helper()
	ch := *brushTestLevel(t)
	lvl := &world.Level{
		Chunks:   []world.Chunk{ch},
		Entities: []world.Entity{{ID: "crate"}},
		Volumes:  []world.Volume{{ID: "trigger", Enabled: true}},
	}
	lvl.Lights.Points = []world.PointLight{{}}
	lvl.Spawn = &world.SpawnPoint{}
	lvl.Seal = &world.Seal{Half: mgl32.Vec3{10, 10, 10}}

	Editor.level = lvl
	Editor.ActiveChunkIdx = 0
	Editor.ChunkSelected = false
	Editor.SpawnSelected = false
	Editor.SealSelected = false
	Editor.ActiveEntityIdx = -1
	Editor.ActiveVolumeIdx = -1
	Editor.SelectedLightKind = lightKindNone
	Editor.SelectedVertices = nil
	Editor.SelectedFaces = nil
	return lvl
}

// selectionCount is how many of the panel's properties blocks would draw.
func selectionCount() int {
	n := 0
	for _, on := range []bool{
		Editor.SpawnSelected,
		Editor.SealSelected,
		Editor.ChunkSelected,
		Editor.ActiveEntityIdx >= 0,
		Editor.ActiveVolumeIdx >= 0,
		Editor.SelectedLightKind != lightKindNone,
		len(Editor.SelectedVertices) > 0,
		len(Editor.SelectedFaces) > 0,
		len(Editor.SelectedBrushes) > 0,
	} {
		if on {
			n++
		}
	}
	return n
}

// Whichever object is picked, it is the only one picked — whatever was
// selected before it, and by whichever route.
func TestSelectingAnObjectDropsEveryOther(t *testing.T) {
	defer saveEditorSelection()()

	selects := []struct {
		name string
		do   func()
	}{
		{"spawn", editorSelectSpawn},
		{"seal", editorSelectSeal},
		{"chunk", func() { editorSelectChunk(0) }},
		{"brush", func() { editorSelectBrush(0, 0) }},
		{"entity", func() { editorSelectEntity(0) }},
		{"volume", func() { editorSelectVolume(0) }},
		{"light", func() { editorSelectLight(lightKindPoint, 0) }},
	}
	// Every ordered pair: select one thing, then another, and only the second
	// may survive.
	for _, first := range selects {
		for _, second := range selects {
			selectTestLevel(t)
			first.do()
			if got := selectionCount(); got != 1 {
				t.Fatalf("selecting the %s alone left %d selections", first.name, got)
			}
			second.do()
			if got := selectionCount(); got != 1 {
				t.Errorf("%s then %s left %d selections, want 1", first.name, second.name, got)
			}
		}
	}
}

// A brush is picked through the chunk that holds it, but the chunk is not the
// selection — the panel was asked for the brush's fields.
func TestSelectingABrushLeavesTheChunkActiveNotSelected(t *testing.T) {
	defer saveEditorSelection()()
	selectTestLevel(t)

	editorSelectBrush(0, 1)
	if Editor.ActiveChunkIdx != 0 {
		t.Errorf("ActiveChunkIdx = %d, want the brush's own chunk", Editor.ActiveChunkIdx)
	}
	if Editor.ChunkSelected {
		t.Error("the chunk is selected as well as the brush")
	}
	if Editor.ActiveBrushIdx != 1 {
		t.Errorf("ActiveBrushIdx = %d, want 1", Editor.ActiveBrushIdx)
	}
	if Editor.PanelFocus != panelFocusBrush {
		t.Errorf("PanelFocus = %q, want %q", Editor.PanelFocus, panelFocusBrush)
	}
}

// Selecting a brush from the Explorer selects the brush itself, in object mode
// — not one of its faces, which would be a guess at which face was meant.
func TestSelectingABrushSelectsTheWholeBrush(t *testing.T) {
	defer saveEditorSelection()()
	selectTestLevel(t)
	Editor.EditContext = editorEditMode

	editorSelectBrush(0, 1)
	if editorInEditMode() {
		t.Error("selecting a brush from the Explorer left the editor in edit mode")
	}
	if len(Editor.SelectedBrushes) != 1 {
		t.Fatalf("got %d selected brushes, want 1", len(Editor.SelectedBrushes))
	}
	if got := (Editor.SelectedBrushes[0]); got != (brushSel{chunkIdx: 0, brushIdx: 1}) {
		t.Errorf("selected %+v, want brush 1 of chunk 0", got)
	}
	if len(Editor.SelectedFaces) != 0 {
		t.Errorf("it also selected %d faces", len(Editor.SelectedFaces))
	}
}

// Re-selecting the chunk a brush belongs to drops the brush. This is the case
// that used to survive: editorSelectChunk only cleared what was picked inside a
// chunk when the *chunk* changed.
func TestSelectingTheActiveChunkDropsABrushInsideIt(t *testing.T) {
	defer saveEditorSelection()()
	selectTestLevel(t)

	editorSelectBrush(0, 0)
	editorSelectChunk(0)
	if len(Editor.SelectedFaces) > 0 {
		t.Errorf("a brush is still selected under the selected chunk")
	}
	if !Editor.ChunkSelected {
		t.Error("the chunk did not end up selected")
	}
}

// Out-of-range indices name nothing, and selecting nothing must not clear what
// is selected — undo replaces the level wholesale, and a stale row redrawn one
// frame later would otherwise wipe the selection on its own.
func TestSelectingAMissingObjectChangesNothing(t *testing.T) {
	defer saveEditorSelection()()
	selectTestLevel(t)

	editorSelectEntity(0)
	editorSelectBrush(0, 99)
	editorSelectBrush(99, 0)
	editorSelectBrush(0, -1)
	if Editor.ActiveEntityIdx != 0 {
		t.Errorf("ActiveEntityIdx = %d, want the entity to have survived", Editor.ActiveEntityIdx)
	}
	if got := selectionCount(); got != 1 {
		t.Errorf("%d selections after three misses, want 1", got)
	}
}
