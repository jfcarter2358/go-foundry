package editor

import (
	"foundry/internal/leveltest"
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// selectTestChunk is leveltest.SplitChunk in a level with the editor pointed at it,
// with the map's own switches put in a known state: floor heights, no
// selection, nothing being dragged.
func selectTestChunk(t *testing.T) *world.Chunk {
	t.Helper()
	lvl := &world.Level{Chunks: []world.Chunk{*leveltest.SplitChunk()}}
	Editor.level = lvl
	Editor.Mode = Mode2D
	Editor.ActiveChunkIdx = 0
	Editor.ActiveSectorIdx = -1
	Editor.ActiveEdgeKind = ""
	Editor.ActiveEdgeIdx = -1
	Editor.ActiveSurface = ""
	Editor.EditFloorVerts = true
	Editor.SelectedVertices = nil
	Editor.SpawnSelected = false
	Editor.DragVertexSI, Editor.DragVertexVI = -1, -1
	return &lvl.Chunks[0]
}

// primarySel is the first selected handle, which is the one the panel and the
// 2D drag follow.
func primarySel(t *testing.T) vertexSel {
	t.Helper()
	if len(Editor.SelectedVertices) == 0 {
		t.Fatal("nothing selected")
	}
	return Editor.SelectedVertices[0]
}

// Candidates come back nearest-first, so a plain click takes the closest thing
// under the cursor before it starts cycling.
func TestEditor2DVertexCandidatesAreNearestFirst(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)

	// (10,0) is chunk point 1, a corner of both sectors; (0,0) is point 0 and
	// belongs to sector A alone. A click just off point 1 must list it first.
	got := editor2DVertexCandidates(ch, 9.5, 0, 20)
	if len(got) < 3 {
		t.Fatalf("expected every corner within 20 units, got %d", len(got))
	}
	if got[0].pointIdx != 1 {
		t.Errorf("nearest candidate is point %d, want 1", got[0].pointIdx)
	}
}

// Two sectors meeting at one corner put two handles in the same spot. Clicking
// again has to reach the second one, or the sector behind could never be
// selected by its corner at all.
func TestEditor2DPointClickCyclesStackedCorners(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)

	// Chunk point 1 is sector A's vertex 1 and sector B's vertex 0.
	click := func() vertexSel {
		if !editor2DHandlePointClick(ch, 10, 0, 0.5, false, true) {
			t.Fatal("a click on a corner missed it")
		}
		return primarySel(t)
	}

	first := click()
	second := click()
	if first == second {
		t.Fatalf("clicking again did not advance: still %+v", first)
	}
	if first.pointIdx != 1 || second.pointIdx != 1 {
		t.Fatalf("cycling left chunk point 1: %+v then %+v", first, second)
	}
	if third := click(); third != first {
		t.Errorf("the cycle did not wrap back to the first: got %+v, want %+v", third, first)
	}
}

// Ctrl+click is how a multi-selection is built, on the map exactly as in the 3D
// view: it takes every corner under the cursor at once rather than cycling
// through them one click at a time.
func TestEditor2DPointCtrlClickTakesEveryCornerUnderTheCursor(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)

	if !editor2DHandlePointClick(ch, 10, 0, 0.5, true, true) {
		t.Fatal("ctrl+click on a corner missed it")
	}
	if n := len(Editor.SelectedVertices); n != 2 {
		t.Fatalf("ctrl+click selected %d handles, want both sectors' corner", n)
	}
	// And it adds rather than replaces: the far corner joins the selection.
	if !editor2DHandlePointClick(ch, 0, 0, 0.5, true, true) {
		t.Fatal("ctrl+click on the second corner missed it")
	}
	if n := len(Editor.SelectedVertices); n != 3 {
		t.Fatalf("ctrl+click replaced the selection: %d handles, want 3", n)
	}
	// An ctrl+click never starts a drag — the group moves through the gizmo.
	if Editor.DragVertexVI >= 0 {
		t.Error("ctrl+click started a single-vertex drag")
	}
}

// A plain click on one corner is still the map's original grab-and-drag
// gesture, which is what the whole 2D workflow is built on.
func TestEditor2DPointClickStartsASingleVertexDrag(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)

	editor2DHandlePointClick(ch, 0, 0, 0.5, false, true)
	if Editor.DragVertexSI != 0 || Editor.DragVertexVI != 0 {
		t.Errorf("drag target = sector %d vertex %d, want 0/0",
			Editor.DragVertexSI, Editor.DragVertexVI)
	}
	if len(Editor.SelectedVertices) != 1 {
		t.Errorf("a plain click selected %d handles, want 1", len(Editor.SelectedVertices))
	}
}

// The map edits floor corners or ceiling corners, never both at once, and the
// selection it produces has to say which — that is what makes it legible in the
// 3D view, where the two sit at different heights.
func TestEditor2DPointClickFollowsTheFloorCeilingSwitch(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)

	Editor.EditFloorVerts = false
	editor2DHandlePointClick(ch, 0, 0, 0.5, false, true)
	if primarySel(t).isFloor {
		t.Error("ceiling mode selected a floor handle")
	}
}

// An edge selection is its two endpoints — the same thing it is in 3D — so one
// gizmo moves an edge in either view without knowing edges exist.
func TestEditor2DEdgeClickSelectsBothEndpoints(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)

	// Midway along sector A's bottom wall, from (0,0) to (10,0).
	if !editor2DHandleEdgeClick(ch, 5, 0, 1, false) {
		t.Fatal("a click on an edge missed it")
	}
	if n := len(Editor.SelectedVertices); n != 2 {
		t.Fatalf("an edge selected %d handles, want its 2 endpoints", n)
	}
	// And the wall behind it is noted, so the side panel has something to edit.
	if Editor.ActiveEdgeKind != "wall" || Editor.ActiveEdgeIdx != 0 {
		t.Errorf("edge click noted %q #%d, want wall #0",
			Editor.ActiveEdgeKind, Editor.ActiveEdgeIdx)
	}
}

// The wall between two sectors is drawn twice, once from each side. Repeated
// clicks have to reach both, the same way stacked corners do.
func TestEditor2DEdgeClickCyclesStackedEdges(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)

	// The shared edge runs from (10,0) to (10,10); click its middle.
	cands := editor2DEdgeCandidates(ch, 10, 5, 1)
	if len(cands) < 2 {
		t.Fatalf("expected the shared edge from both sectors, got %d", len(cands))
	}

	editor2DHandleEdgeClick(ch, 10, 5, 1, false)
	first := Editor.ActiveSectorIdx
	editor2DHandleEdgeClick(ch, 10, 5, 1, false)
	if Editor.ActiveSectorIdx == first {
		t.Errorf("clicking again stayed on sector %d; the other side is unreachable", first)
	}
}

// Face mode is the map's original ladder minus its vertex rung: a click inside
// a sector selects that sector, and says which of its two horizontal surfaces
// it means so the 3D view can outline it.
func TestEditor2DFaceClickSelectsTheSectorInterior(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)
	lvl := Editor.level

	if !editor2DHandleFaceClick(lvl, ch, 15, 5, 0.5, false) {
		t.Fatal("a click inside sector B selected nothing")
	}
	if Editor.ActiveSectorIdx != 1 {
		t.Errorf("selected sector %d, want 1", Editor.ActiveSectorIdx)
	}
	if Editor.ActiveSurface != "floor" {
		t.Errorf("ActiveSurface = %q, want floor", Editor.ActiveSurface)
	}

	Editor.EditFloorVerts = false
	editor2DHandleFaceClick(lvl, ch, 5, 5, 0.5, false)
	if Editor.ActiveSurface != "ceiling" {
		t.Errorf("in ceiling mode ActiveSurface = %q, want ceiling", Editor.ActiveSurface)
	}
}

// A wall is a line on the map, and a line drawn across a sector is the more
// specific target than the sector it is drawn across.
func TestEditor2DFaceClickPrefersAWallToTheSectorBehindIt(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)

	if !editor2DHandleFaceClick(Editor.level, ch, 5, 0.05, 0.5, false) {
		t.Fatal("a click on a wall selected nothing")
	}
	if Editor.ActiveSurface != "wall" || Editor.ActiveEdgeIdx != 0 {
		t.Errorf("selected %q #%d, want wall #0", Editor.ActiveSurface, Editor.ActiveEdgeIdx)
	}
	// A face selection is not made of points, so no vertex handle survives it.
	if len(Editor.SelectedVertices) != 0 {
		t.Errorf("a face click left %d vertices selected", len(Editor.SelectedVertices))
	}
}

// Point and edge mode own their click outright: aiming at a corner and missing
// must not silently select the sector behind it.
func TestEditor2DPointAndEdgeMissesSelectNothing(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)

	if editor2DHandlePointClick(ch, 5, 5, 0.5, false, true) {
		t.Error("a click in open floor hit a corner")
	}
	if editor2DHandleEdgeClick(ch, 5, 5, 0.5, false) {
		t.Error("a click in open floor hit an edge")
	}
}

// editor2DCycle is the rule both modes share: take the nearest when nothing
// under the cursor is selected, and otherwise the one after whatever is.
func TestEditor2DCycle(t *testing.T) {
	none := func(int) bool { return false }
	if got := editor2DCycle(0, none); got != -1 {
		t.Errorf("empty candidate list = %d, want -1", got)
	}
	if got := editor2DCycle(3, none); got != 0 {
		t.Errorf("nothing selected = %d, want the nearest (0)", got)
	}
	if got := editor2DCycle(3, func(i int) bool { return i == 1 }); got != 2 {
		t.Errorf("second selected = %d, want 2", got)
	}
	if got := editor2DCycle(3, func(i int) bool { return i == 2 }); got != 0 {
		t.Errorf("last selected = %d, want a wrap to 0", got)
	}
}

// The map draws a floor cross and a ceiling cross for every corner. Each has to
// answer for its own height, or selecting a floor corner would light the
// ceiling mark sitting directly on top of it.
func TestEditor2DVertexSelectedIsHeightSpecific(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)

	Editor.SelectedVertices = []vertexSel{{
		chunkIdx: 0, sectorIdx: 0, vertIdx: 0, pointIdx: ch.Sectors[0].PointIndices[0], isFloor: true,
	}}
	if !editor2DVertexSelected(0, 0, 0, true) {
		t.Error("the selected floor corner did not read as selected")
	}
	if editor2DVertexSelected(0, 0, 0, false) {
		t.Error("the ceiling cross above a selected floor corner read as selected")
	}
}

// Everything a click can select is dropped together, so a miss cannot leave the
// panel showing a wall that the map no longer marks.
func TestEditor2DClearSelectionDropsEverything(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)
	Editor.level.Spawn = &world.SpawnPoint{Position: mgl32.Vec3{}}

	editor2DHandlePointClick(ch, 0, 0, 0.5, false, true)
	Editor.SpawnSelected = true
	Editor.ActiveSurface = "wall"
	Editor.ActiveEdgeKind = "wall"
	Editor.ActiveEdgeIdx = 2

	editor2DClearSelection()
	if len(Editor.SelectedVertices) != 0 {
		t.Error("vertices survived a cleared selection")
	}
	if Editor.SpawnSelected {
		t.Error("the spawn marker survived a cleared selection")
	}
	if Editor.ActiveSurface != "" || Editor.ActiveEdgeIdx != -1 {
		t.Errorf("a surface survived: %q #%d", Editor.ActiveSurface, Editor.ActiveEdgeIdx)
	}
}
