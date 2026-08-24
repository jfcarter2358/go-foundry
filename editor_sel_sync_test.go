package main

import "testing"

// The two viewports do not talk to each other; they read and write one
// selection. These tests pin that down from both directions, since "select it
// here and it is selected there" is only true for as long as neither view keeps
// a selection of its own on the side.

// A corner picked on the map is the same corner the 3D view has selected, right
// down to the gizmo it puts on it.
func TestSelectionMadeIn2DIsTheOneIn3D(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)

	if !editor2DHandlePointClick(ch, 0, 0, 0.5, false, true) {
		t.Fatal("a click on a corner missed it")
	}
	sel := primarySel(t)
	if sel.chunkIdx != 0 || sel.sectorIdx != 0 || sel.vertIdx != 0 {
		t.Fatalf("selected %+v, want sector 0's first corner", sel)
	}
	// What the 3D view reads to draw the handle and hang its gizmo on.
	if Editor.GizmoKind != gizmoKindVertices {
		t.Errorf("GizmoKind = %q, want %q", Editor.GizmoKind, gizmoKindVertices)
	}
	if !isVertexSelected(sel) {
		t.Error("the picked corner is not in the shared selection")
	}
	Editor.Mode = Mode3D
	if _, ok := selectedVerticesCenter(); !ok {
		t.Error("the 3D view found no centre for a selection made on the map")
	}
}

// And the other way: a corner picked in 3D is drawn as selected on the map,
// which needs the map's own chunk to have followed the selection there.
func TestSelectionMadeIn3DShowsOnTheMap(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)
	Editor.ActiveChunkIdx = 99 // the map is looking somewhere else entirely

	sel := vertexSel{chunkIdx: 0, sectorIdx: 1, vertIdx: 2, pointIdx: ch.Sectors[1].PointIndices[2], isFloor: true}
	setVertexSelection([]vertexSel{sel})

	if Editor.ActiveChunkIdx != 0 {
		t.Fatalf("the map is still on chunk %d, so it cannot draw the selection", Editor.ActiveChunkIdx)
	}
	if !editor2DVertexSelected(1, 2, sel.pointIdx, true) {
		t.Error("the map does not read the 3D selection as selected")
	}
}

// An edge is its two endpoints in both views, so one picked on the map arrives
// in 3D as a pair of selected corners — and the wall behind it arrives with it,
// which is what the map draws in the selected colour.
func TestEdgeSelectionCrossesBetweenViews(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)

	if !editor2DHandleEdgeClick(ch, 5, 0, 1, false) {
		t.Fatal("a click on an edge missed it")
	}
	sel := edgeSel{chunkIdx: 0, sectorIdx: 0, kind: edgeKind3DFloor, vertIdx: 0}
	if !isEdgeSelected(sel) {
		t.Error("the 3D view does not read the map's edge pick as a selected edge")
	}
	if Editor.ActiveEdgeKind != "wall" {
		t.Errorf("ActiveEdgeKind = %q, want wall", Editor.ActiveEdgeKind)
	}
}

// The vertical run between a corner's floor and ceiling handles lies along no
// wall, so selecting it must clear the note rather than leave the panel editing
// an unrelated wall.
func TestVerticalEdgeSelectionNotesNoWall(t *testing.T) {
	defer saveEditorSelection()()
	ch := selectTestChunk(t)
	ch.Sectors[0].Ceiling.Enabled = true

	Editor.ActiveSectorIdx = 0
	Editor.ActiveEdgeKind = "wall"
	Editor.ActiveEdgeIdx = 3

	if !setEdgeSelection([]edgeSel{{chunkIdx: 0, sectorIdx: 0, kind: edgeKind3DVertical, vertIdx: 0}}) {
		t.Fatal("a vertical edge did not resolve to its two handles")
	}
	if len(Editor.SelectedVertices) != 2 {
		t.Fatalf("a vertical edge selected %d handles, want its floor and ceiling", len(Editor.SelectedVertices))
	}
	if Editor.ActiveEdgeKind != "" || Editor.ActiveEdgeIdx != -1 {
		t.Errorf("a vertical edge noted %q #%d, want nothing",
			Editor.ActiveEdgeKind, Editor.ActiveEdgeIdx)
	}
}

// The selection unit is shared too, so pressing 1 in one view puts the other in
// point mode as well — otherwise a selection made here would be invisible
// there, drawn in a unit that view was not showing.
func TestSelModeIsSharedByBothViews(t *testing.T) {
	defer saveEditorSelection()()
	Editor.SelMode = editorSelFace

	Editor.Mode = Mode2D
	editorSetSelMode(editorSelPoint)
	Editor.Mode = Mode3D
	if Editor.SelMode != editorSelPoint {
		t.Errorf("SelMode = %v in the 3D view, want the Point mode set on the map", Editor.SelMode)
	}
}
