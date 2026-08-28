package editor

// One selection at a time — the rule the Explorer and the Properties block
// below it are both built on. Every one of these used to be two properties
// blocks up at once, neither of them wrong.

import (
	"foundry/internal/leveltest"
	"foundry/internal/world"
	"testing"
)

// selectTestLevel is a level with one chunk, one painted biome sector, and one
// of every level-scoped object, pointed at by the editor.
func selectTestLevel(t *testing.T) *world.Level {
	t.Helper()
	ch := *leveltest.SplitChunk()
	ch.BiomeSectors = []world.BiomeSector{{ID: "bs1"}}
	lvl := &world.Level{
		Chunks:   []world.Chunk{ch},
		Entities: []world.Entity{{ID: "crate"}},
		Volumes:  []world.Volume{{ID: "trigger", Enabled: true}},
	}
	lvl.Lights.Points = []world.PointLight{{}}
	lvl.Spawn = &world.SpawnPoint{}

	Editor.level = lvl
	Editor.ActiveChunkIdx = 0
	Editor.ActiveSectorIdx = -1
	Editor.ActiveEdgeIdx = -1
	Editor.ActiveEdgeKind = ""
	Editor.ActiveBiomeSectorIdx = -1
	Editor.ChunkSelected = false
	Editor.SpawnSelected = false
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
		Editor.ChunkSelected,
		Editor.ActiveEntityIdx >= 0,
		Editor.ActiveVolumeIdx >= 0,
		Editor.SelectedLightKind != lightKindNone,
		Editor.ActiveSectorIdx >= 0,
		Editor.ActiveBiomeSectorIdx >= 0,
		len(Editor.SelectedVertices) > 0,
		len(Editor.SelectedFaces) > 0,
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
		{"chunk", func() { editorSelectChunk(0) }},
		{"sector", func() { editorSelectSector(0, 0) }},
		{"biome sector", func() { editorSelectBiomeSector(0, 0) }},
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

// A sector is picked through the chunk that holds it, but the chunk is not the
// selection — the panel was asked for the sector's fields.
func TestSelectingASectorLeavesTheChunkActiveNotSelected(t *testing.T) {
	defer saveEditorSelection()()
	selectTestLevel(t)

	editorSelectSector(0, 1)
	if Editor.ActiveChunkIdx != 0 {
		t.Errorf("ActiveChunkIdx = %d, want the sector's own chunk", Editor.ActiveChunkIdx)
	}
	if Editor.ChunkSelected {
		t.Error("the chunk is selected as well as the sector")
	}
	if Editor.ActiveSectorIdx != 1 {
		t.Errorf("ActiveSectorIdx = %d, want 1", Editor.ActiveSectorIdx)
	}
	if Editor.PanelFocus != panelFocusSector {
		t.Errorf("PanelFocus = %q, want %q", Editor.PanelFocus, panelFocusSector)
	}
}

// Re-selecting the chunk a sector belongs to drops the sector. This is the case
// that used to survive: editorSelectChunk only cleared what was picked inside a
// chunk when the *chunk* changed.
func TestSelectingTheActiveChunkDropsASectorInsideIt(t *testing.T) {
	defer saveEditorSelection()()
	selectTestLevel(t)

	editorSelectSector(0, 0)
	editorSelectChunk(0)
	if Editor.ActiveSectorIdx >= 0 {
		t.Errorf("sector %d is still selected under the selected chunk", Editor.ActiveSectorIdx)
	}
	if !Editor.ChunkSelected {
		t.Error("the chunk did not end up selected")
	}
}

// Picking paint from the Explorer arms the layer that can edit it, so the row
// does not lead to a polygon nothing can touch.
func TestSelectingABiomeSectorTurnsTheLayerOn(t *testing.T) {
	defer saveEditorSelection()()
	selectTestLevel(t)
	Editor.BiomeLayerActive = false

	editorSelectBiomeSector(0, 0)
	if !Editor.BiomeLayerActive {
		t.Error("the biome layer is still off")
	}
	if Editor.ActiveBiomeSectorIdx != 0 {
		t.Errorf("ActiveBiomeSectorIdx = %d, want 0", Editor.ActiveBiomeSectorIdx)
	}
}

// Out-of-range indices name nothing, and selecting nothing must not clear what
// is selected — undo replaces the level wholesale, and a stale row redrawn one
// frame later would otherwise wipe the selection on its own.
func TestSelectingAMissingObjectChangesNothing(t *testing.T) {
	defer saveEditorSelection()()
	selectTestLevel(t)

	editorSelectEntity(0)
	editorSelectSector(0, 99)
	editorSelectSector(99, 0)
	editorSelectBiomeSector(0, 99)
	if Editor.ActiveEntityIdx != 0 {
		t.Errorf("ActiveEntityIdx = %d, want the entity to have survived", Editor.ActiveEntityIdx)
	}
	if got := selectionCount(); got != 1 {
		t.Errorf("%d selections after three misses, want 1", got)
	}
}
