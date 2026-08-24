package main

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// joinTestChunk is selectTestChunk plus three free points — chunk points 6, 7
// and 8, in no sector — which is the raw material a join turns into one.
func joinTestChunk(t *testing.T) *Chunk {
	t.Helper()
	ch := selectTestChunk(t)
	ch.Points = append(ch.Points, mgl32.Vec2{0, 20}, mgl32.Vec2{10, 20}, mgl32.Vec2{10, 30})
	for len(ch.PointNames) < len(ch.Points) {
		ch.PointNames = append(ch.PointNames, "")
	}
	Editor.PendingEdges = nil
	return ch
}

// selectPoints replaces the selection with the given chunk points, in order,
// as the map's point mode would.
func selectPoints(ch *Chunk, pts ...int) {
	sels := make([]vertexSel, 0, len(pts))
	for _, p := range pts {
		sel := freePointSel(Editor.ActiveChunkIdx, p)
		for si := range ch.Sectors {
			if vi := sectorVertexPos(&ch.Sectors[si], p); vi >= 0 {
				sel = vertexSel{chunkIdx: Editor.ActiveChunkIdx, sectorIdx: si,
					vertIdx: vi, pointIdx: p, isFloor: true}
				break
			}
		}
		sels = append(sels, sel)
	}
	Editor.SelectedVertices = sels
}

// ---------- what the selection means to a join ----------

// Joining works in chunk points, not in handles. A corner two sectors share is
// two handles on one point, and a corner's floor and ceiling handles are two
// more — all of which have to collapse, or a join would try to draw an edge
// from a point to itself.
func TestEditorSelectedChunkPointsCollapsesHandlesOntoPoints(t *testing.T) {
	defer saveEditorSelection()()
	ch := joinTestChunk(t)

	Editor.SelectedVertices = []vertexSel{
		vsel(ch, 0, 1, true),  // chunk point 1, from sector A
		vsel(ch, 1, 0, true),  // chunk point 1 again, from sector B
		vsel(ch, 0, 1, false), // and again, its ceiling handle
		vsel(ch, 0, 0, true),  // chunk point 0
	}
	got := editorSelectedChunkPoints()
	want := []int{1, 0}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (order is pick order)", got, want)
		}
	}
}

// A selection carried over from the 3D view can name points in chunks the map
// is not showing. An edge cannot span two chunks, so those are dropped rather
// than joined to something they have no relation to.
func TestEditorSelectedChunkPointsIgnoresOtherChunks(t *testing.T) {
	defer saveEditorSelection()()
	ch := joinTestChunk(t)

	Editor.SelectedVertices = []vertexSel{
		vsel(ch, 0, 0, true),
		{chunkIdx: 7, sectorIdx: 0, vertIdx: 0, pointIdx: 0, isFloor: true},
	}
	if got := editorSelectedChunkPoints(); len(got) != 1 || got[0] != 0 {
		t.Errorf("got %v, want just chunk 0's point", got)
	}
}

// ---------- the plan ----------

func TestPlanPointJoinChainsInPickOrder(t *testing.T) {
	got := planPointJoin([]int{4, 1, 7}, false)
	want := [][2]int{{4, 1}, {1, 7}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestPlanPointJoinClosesTheLoop(t *testing.T) {
	got := planPointJoin([]int{4, 1, 7}, true)
	if len(got) != 3 || got[2] != [2]int{7, 4} {
		t.Fatalf("got %v, want the chain plus (7,4)", got)
	}
}

// Fewer than two points is nothing to join, and two points have one edge
// between them either way — closing them would ask for that same edge twice.
func TestPlanPointJoinDegenerateCases(t *testing.T) {
	if got := planPointJoin(nil, true); got != nil {
		t.Errorf("no points planned %v, want nothing", got)
	}
	if got := planPointJoin([]int{3}, true); got != nil {
		t.Errorf("one point planned %v, want nothing", got)
	}
	if got := planPointJoin([]int{3, 5}, true); len(got) != 1 {
		t.Errorf("two points closed planned %v, want the single edge once", got)
	}
}

// ---------- applying it ----------

// Free points in open space become pending edges: they are not part of any
// sector yet, so there is nothing to cut in two.
func TestJoinFreePointsDrawsPendingEdges(t *testing.T) {
	defer saveEditorSelection()()
	ch := joinTestChunk(t)
	selectPoints(ch, 6, 7, 8)

	if n := EditorJoinSelectedPoints(false); n != 2 {
		t.Fatalf("joined %d edges, want 2", n)
	}
	want := [][2]int{{6, 7}, {7, 8}}
	if len(Editor.PendingEdges) != len(want) {
		t.Fatalf("pending edges = %v, want %v", Editor.PendingEdges, want)
	}
	for i := range want {
		if Editor.PendingEdges[i] != want[i] {
			t.Fatalf("pending edges = %v, want %v", Editor.PendingEdges, want)
		}
	}
}

// Joining a pair that is already a wall must not lay a second edge over the
// first — which is what selecting a sector's whole ring and closing it would
// otherwise do.
func TestJoinSkipsPairsAlreadyConnected(t *testing.T) {
	defer saveEditorSelection()()
	ch := joinTestChunk(t)
	// Sector A's four corners, in its own winding order.
	selectPoints(ch, 0, 1, 2, 3)

	if n := EditorJoinSelectedPoints(true); n != 0 {
		t.Fatalf("joined %d edges over an existing ring, want 0", n)
	}
	if len(Editor.PendingEdges) != 0 {
		t.Errorf("laid pending edges over existing walls: %v", Editor.PendingEdges)
	}
}

// A pair already waiting as a pending edge is not doubled either.
func TestJoinSkipsPairsAlreadyPending(t *testing.T) {
	defer saveEditorSelection()()
	ch := joinTestChunk(t)
	Editor.PendingEdges = [][2]int{{7, 6}} // note: the other direction

	selectPoints(ch, 6, 7)
	if n := EditorJoinSelectedPoints(false); n != 0 {
		t.Fatalf("joined %d edges, want 0 — that edge is already pending", n)
	}
	if len(Editor.PendingEdges) != 1 {
		t.Errorf("pending edges = %v, want the one that was already there", Editor.PendingEdges)
	}
}

// A no-op join must not leave an undo entry behind: undo should reach past it
// to the last thing that actually changed the level.
func TestJoinPushesNoUndoWhenNothingChanges(t *testing.T) {
	defer saveEditorSelection()()
	EditorClearUndoHistory()
	defer EditorClearUndoHistory()
	ch := joinTestChunk(t)
	selectPoints(ch, 0, 1)

	EditorJoinSelectedPoints(false)
	if EditorCanUndo() {
		t.Error("a join that changed nothing pushed an undo entry")
	}

	selectPoints(ch, 6, 7)
	EditorJoinSelectedPoints(false)
	if !EditorCanUndo() {
		t.Error("a join that drew an edge pushed no undo entry")
	}
}

// The button and the menu row are only offered when there is something to do.
func TestEditorCanJoinPoints(t *testing.T) {
	defer saveEditorSelection()()
	ch := joinTestChunk(t)

	Editor.SelectedVertices = nil
	if editorCanJoinPoints() {
		t.Error("join offered with nothing selected")
	}
	selectPoints(ch, 6)
	if editorCanJoinPoints() {
		t.Error("join offered for a single point")
	}
	selectPoints(ch, 6, 7)
	if !editorCanJoinPoints() {
		t.Error("join not offered for two points")
	}
	// Two handles on one point are one point, so still nothing to join.
	Editor.SelectedVertices = []vertexSel{vsel(ch, 0, 1, true), vsel(ch, 1, 0, true)}
	if editorCanJoinPoints() {
		t.Error("join offered for two handles on a single point")
	}
}

// The label says what the action is about to do, so a glance at the button
// answers "how many edges is that?" without pressing it.
func TestEditorJoinLabelCountsEdges(t *testing.T) {
	defer saveEditorSelection()()
	ch := joinTestChunk(t)

	Editor.SelectedVertices = nil
	if got := editorJoinLabel(false); got != "Join Points" {
		t.Errorf("empty selection label = %q", got)
	}
	selectPoints(ch, 6, 7)
	if got := editorJoinLabel(false); got != "Join Points (1 edge)" {
		t.Errorf("two points label = %q", got)
	}
	selectPoints(ch, 6, 7, 8)
	if got := editorJoinLabel(true); got != "Join & Close Loop (3 edges)" {
		t.Errorf("three points closed label = %q", got)
	}
}

// Discarding drops the unjoined edges and keeps the points, so a wrong outline
// can be redrawn without replacing its corners.
func TestDiscardPendingEdgesKeepsThePoints(t *testing.T) {
	defer saveEditorSelection()()
	ch := joinTestChunk(t)
	before := len(ch.Points)
	selectPoints(ch, 6, 7, 8)
	EditorJoinSelectedPoints(false)

	EditorDiscardPendingEdges()
	if len(Editor.PendingEdges) != 0 {
		t.Errorf("pending edges survived the discard: %v", Editor.PendingEdges)
	}
	if len(ch.Points) != before {
		t.Errorf("the discard took %d points with it", before-len(ch.Points))
	}
}

// ---------- free points as selections ----------

// Free points have to be selectable, or there would be no way to pick the
// corners of a sector that does not exist yet.
func TestFreePointsAreSelectableOnTheMap(t *testing.T) {
	defer saveEditorSelection()()
	ch := joinTestChunk(t)

	cands := editor2DVertexCandidates(ch, 0, 20, 0.5)
	if len(cands) != 1 {
		t.Fatalf("found %d candidates at a free point, want 1", len(cands))
	}
	if !cands[0].isFreePoint() || cands[0].pointIdx != 6 {
		t.Fatalf("candidate = %+v, want free chunk point 6", cands[0])
	}

	if !editor2DHandlePointClick(ch, 0, 20, 0.5, false, true) {
		t.Fatal("a click on a free point missed it")
	}
	if len(Editor.SelectedVertices) != 1 {
		t.Fatalf("selected %d handles, want 1", len(Editor.SelectedVertices))
	}
	// It has no sector, so nothing may have been armed to drag through one.
	if Editor.DragVertexSI >= 0 || Editor.DragVertexVI >= 0 {
		t.Error("clicking a free point armed a sector-vertex drag")
	}
	if Editor.ActiveSectorIdx != -1 {
		t.Errorf("ActiveSectorIdx = %d, want none", Editor.ActiveSectorIdx)
	}
}

// A free point sits at y=0 — where the map draws it — so the gizmo has
// somewhere to put itself and a transform has something to write back.
func TestFreePointHasAPosition(t *testing.T) {
	defer saveEditorSelection()()
	ch := joinTestChunk(t)

	pos, ok := vertexSelPos(freePointSel(0, 6))
	if !ok {
		t.Fatal("a free point resolved to no position")
	}
	if pos != (mgl32.Vec3{0, 0, 20}) {
		t.Errorf("free point at %v, want (0,0,20)", pos)
	}
	if _, ok := vertexSelPos(freePointSel(0, len(ch.Points))); ok {
		t.Error("an out-of-range free point resolved to a position")
	}
}

// Selecting a free point must not drag the map's floor/ceiling switch with it:
// the point has neither, and that switch decides which handles the next click
// reaches.
func TestSelectingAFreePointLeavesTheHeightSwitchAlone(t *testing.T) {
	defer saveEditorSelection()()
	joinTestChunk(t)
	Editor.EditFloorVerts = false

	setVertexSelection([]vertexSel{freePointSel(0, 6)})
	if Editor.EditFloorVerts {
		t.Error("selecting a free point switched the map back to floor corners")
	}
}

// A free point is a chunk point like any other, so the gizmo moves it — which
// is the only way to move one, since the map's direct drag needs a sector to
// rebuild.
func TestFreePointsMoveWithTheSelection(t *testing.T) {
	defer saveEditorSelection()()
	ch := joinTestChunk(t)
	Editor.SelectedVertices = []vertexSel{freePointSel(0, 6), freePointSel(0, 7)}

	Editor.Gizmo2DStartPts = selectedVerticesWorld()
	writeSelectedVertexWorld(gizmo2DVertexTransform(func(p mgl32.Vec2) mgl32.Vec2 {
		return p.Add(mgl32.Vec2{2, 3})
	}))

	nearVec2(t, "free point 6", ch.Points[6], mgl32.Vec2{2, 23})
	nearVec2(t, "free point 7", ch.Points[7], mgl32.Vec2{12, 23})
}
