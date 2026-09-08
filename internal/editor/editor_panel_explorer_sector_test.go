package editor

// The Explorer's sector rows were the one category with no right-click menu, so
// a sector reachable only from the tree — one whose surfaces are all switched
// off — could be selected but never deleted. These cover what the Delete row
// does, since the row itself needs an ImGui frame to press.

import (
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// explorerSectorChunk is two 4×4 sectors sharing an edge, with the editor
// pointed at the chunk. Floor and ceiling are left disabled so Sector.Load
// stays off the GPU — deletion reloads what remains, and these tests run
// without a GL context.
func explorerSectorChunk() *world.Chunk {
	Editor.level = &world.Level{Chunks: []world.Chunk{{
		ID:     "c0",
		Points: []mgl32.Vec2{{0, 0}, {4, 0}, {4, 4}, {0, 4}, {8, 0}, {8, 4}},
		Sectors: []world.Sector{
			{ID: "s0", PointIndices: []int{0, 1, 2, 3}},
			{ID: "s1", PointIndices: []int{1, 4, 5, 2}},
		},
	}}}
	Editor.ActiveChunkIdx = 0
	return &Editor.level.Chunks[0]
}

// Deleting keeps the chunk's points: they are usually shared with the sectors
// that are staying, and the free vertices are what a replacement is drawn from.
func TestDeleteSectorKeepsTheChunkPoints(t *testing.T) {
	defer saveEditorSelection()()
	ch := explorerSectorChunk()
	nPoints := len(ch.Points)

	deleteSector(ch, 0, 0)

	if len(ch.Sectors) != 1 || ch.Sectors[0].ID != "s1" {
		t.Errorf("sectors after delete = %v, want just s1", sectorIDs(ch))
	}
	if len(ch.Points) != nPoints {
		t.Errorf("chunk points after delete = %d, want %d unchanged", len(ch.Points), nPoints)
	}
}

// The indices above the deleted sector all shift down, so a selection left
// pointing at the old index would name a different room.
func TestDeleteSectorDropsTheSelection(t *testing.T) {
	defer saveEditorSelection()()
	ch := explorerSectorChunk()
	Editor.ActiveSectorIdx = 1
	Editor.ActiveEdgeKind, Editor.ActiveEdgeIdx = "wall", 0

	deleteSector(ch, 0, 0)

	if Editor.ActiveSectorIdx != -1 {
		t.Errorf("ActiveSectorIdx = %d, want -1", Editor.ActiveSectorIdx)
	}
	if Editor.ActiveEdgeIdx != -1 || Editor.ActiveEdgeKind != "" {
		t.Errorf("edge selection = %q #%d, want cleared", Editor.ActiveEdgeKind, Editor.ActiveEdgeIdx)
	}
}

// A sector deleted from a chunk that is not the active one must not disturb the
// selection, which is about a different chunk entirely.
func TestDeleteSectorInAnotherChunkLeavesTheSelection(t *testing.T) {
	defer saveEditorSelection()()
	ch := explorerSectorChunk()
	Editor.ActiveSectorIdx = 1

	deleteSector(ch, 3, 0)

	if Editor.ActiveSectorIdx != 1 {
		t.Errorf("ActiveSectorIdx = %d, want 1 left alone", Editor.ActiveSectorIdx)
	}
}

// An out-of-range index is a no-op rather than a panic: the row and the delete
// are a frame apart, and undo can rewrite the slice in between.
func TestDeleteSectorIgnoresAStaleIndex(t *testing.T) {
	defer saveEditorSelection()()
	ch := explorerSectorChunk()

	deleteSector(ch, 0, len(ch.Sectors))
	deleteSector(ch, 0, -1)

	if len(ch.Sectors) != 2 {
		t.Errorf("sectors = %d, want 2 unchanged", len(ch.Sectors))
	}
}

func sectorIDs(ch *world.Chunk) []string {
	ids := make([]string, len(ch.Sectors))
	for i := range ch.Sectors {
		ids[i] = ch.Sectors[i].ID
	}
	return ids
}
