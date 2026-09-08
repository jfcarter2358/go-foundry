package editor

// The map's right-click menu could delete a point but never a whole sector —
// the only way to remove one was to select a wall edge and press Delete. These
// cover the row that fixes that, which is built as data so it can be asked
// about without an ImGui frame.

import (
	"testing"
)

// map2DSectorChunk is the Explorer tests' two-sector chunk with the map's own
// switches in a known state.
func map2DSectorChunk() {
	explorerSectorChunk()
	Editor.Mode = Mode2D
	Editor.ActiveSectorIdx = -1
	Editor.ActiveLayer = 0
	editor2DMenuSectorIdx = -1
}

// The row names the sector it would remove: the menu opens over one room while
// another may be selected, and "Delete" alone would not say which it meant.
func TestMap2DMenuNamesTheSectorUnderTheCursor(t *testing.T) {
	defer saveEditorSelection()()
	map2DSectorChunk()
	ch := activeChunk()

	// (2,2) is inside s0, (6,2) inside s1.
	editor2DMenuSectorIdx = editor2DSectorAt(ch, 2, 2)
	mustMenuItem(t, editor2DSectorMenuItems(ch), "Delete s0")

	editor2DMenuSectorIdx = editor2DSectorAt(ch, 6, 2)
	mustMenuItem(t, editor2DSectorMenuItems(ch), "Delete s1")
}

// A menu opened over bare map with nothing selected must not offer to delete
// something — there is nothing for the row to mean.
func TestMap2DMenuHasNoSectorRowOverEmptyMap(t *testing.T) {
	defer saveEditorSelection()()
	map2DSectorChunk()
	ch := activeChunk()

	editor2DMenuSectorIdx = editor2DSectorAt(ch, 50, 50)
	if editor2DMenuSectorIdx != -1 {
		t.Fatalf("point well outside every sector resolved to sector %d", editor2DMenuSectorIdx)
	}
	if items := editor2DSectorMenuItems(ch); len(items) != 0 {
		t.Errorf("menu over empty map offers %d sector rows, want none", len(items))
	}
}

// The selection is the fallback, so a sector picked from the Explorer — whose
// interior may be nowhere near the cursor — is still deletable from the map.
func TestMap2DMenuFallsBackToTheSelectedSector(t *testing.T) {
	defer saveEditorSelection()()
	map2DSectorChunk()
	ch := activeChunk()
	Editor.ActiveSectorIdx = 1

	mustMenuItem(t, editor2DSectorMenuItems(ch), "Delete s1")
}

// A sector on a layer the map is not editing is not what the cursor is over:
// the click is aimed at the layer being worked on, not the one ghosted under it.
func TestMap2DSectorPickIgnoresOtherLayers(t *testing.T) {
	defer saveEditorSelection()()
	map2DSectorChunk()
	ch := activeChunk()
	ch.Sectors[0].Layer = 1

	if si := editor2DSectorAt(ch, 2, 2); si != -1 {
		t.Errorf("picked sector %d on an inactive layer, want none", si)
	}
}

// The row itself: the sector goes, its points stay, and the pick is dropped so
// the next menu does not act on an index that has shifted.
func TestMap2DDeleteSectorRowRemovesIt(t *testing.T) {
	defer saveEditorSelection()()
	map2DSectorChunk()
	ch := activeChunk()
	nPoints := len(ch.Points)
	editor2DMenuSectorIdx = editor2DSectorAt(ch, 2, 2)

	mustMenuItem(t, editor2DSectorMenuItems(ch), "Delete s0").Do()

	if len(ch.Sectors) != 1 || ch.Sectors[0].ID != "s1" {
		t.Errorf("sectors after delete = %v, want just s1", sectorIDs(ch))
	}
	if len(ch.Points) != nPoints {
		t.Errorf("chunk points after delete = %d, want %d unchanged", len(ch.Points), nPoints)
	}
	if editor2DMenuSectorIdx != -1 {
		t.Errorf("editor2DMenuSectorIdx = %d, want -1 after the delete", editor2DMenuSectorIdx)
	}
}
