package editor

import (
	"foundry/internal/testenv"
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// faceGizmoLevel is the two-sector fixture the vertex transform tests use,
// with the editor in the 3D viewport where the gizmos live.
func faceGizmoLevel(t *testing.T) *world.Chunk {
	t.Helper()
	ch := transformTestLevel(t)
	Editor.Mode = Mode3D
	Editor.SelMode = editorSelFace
	return ch
}

// hasVert reports whether sels holds the given corner handle of sector si.
func hasVert(sels []vertexSel, si, vi int, isFloor bool) bool {
	for _, s := range sels {
		if s.sectorIdx == si && s.vertIdx == vi && s.isFloor == isFloor {
			return true
		}
	}
	return false
}

// Selecting a face is what puts a gizmo on screen: without a kind the origin
// lookups all refuse, and the viewport draws nothing to grab. This is the bug
// the file exists to fix — a selected wall used to have no handles at all.
func TestSelectingAFaceAttachesTheGizmo(t *testing.T) {
	defer saveEditorSelection()()
	faceGizmoLevel(t)

	editorSelectFace(edgeFaceSel(0, 0, "wall", 0), false)
	if Editor.GizmoKind != gizmoKindFace {
		t.Fatalf("selecting a wall left the gizmo on %q", Editor.GizmoKind)
	}
	for _, tc := range []struct {
		name   string
		origin func() (mgl32.Vec3, bool)
	}{
		{"translate", translateGizmoOrigin},
		{"rotate", rotateGizmoOrigin},
		{"scale", scaleGizmoOrigin},
	} {
		// Sector A's first wall runs (0,0) → (10,0), floor 0 to ceiling 4, so
		// the middle of its quad is (5, 2, 0).
		got, ok := tc.origin()
		if !ok {
			t.Fatalf("%s gizmo has no origin for a selected wall", tc.name)
		}
		if got.Sub(mgl32.Vec3{5, 2, 0}).Len() > 1e-3 {
			t.Errorf("%s gizmo origin = %v, want (5, 2, 0)", tc.name, got)
		}
	}

	clearFaceSelection()
	if Editor.GizmoKind != gizmoKindNone {
		t.Errorf("deselecting left the gizmo on %q", Editor.GizmoKind)
	}
	if _, ok := translateGizmoOrigin(); ok {
		t.Error("the gizmo still has an origin with nothing selected")
	}
}

// A vertex selection made while a face gizmo was up must take the gizmo with
// it: the two are different units, and only one of them is on screen.
func TestVertexSelectionTakesTheGizmoBackFromAFace(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceGizmoLevel(t)

	editorSelectFace(surfaceFaceSel(0, 0, "floor"), false)
	setVertexSelection([]vertexSel{vsel(ch, 0, 0, true)})
	if Editor.GizmoKind != gizmoKindVertices {
		t.Fatalf("gizmo kind = %q, want %q", Editor.GizmoKind, gizmoKindVertices)
	}
	if len(Editor.SelectedFaces) != 0 {
		t.Errorf("the face selection survived a vertex click: %+v", Editor.SelectedFaces)
	}
}

// Which handles each kind of face owns. A wall is the four corners of its quad
// — both ends at both heights — and a floor or ceiling is its whole ring on one
// side only, because a handle on the other side belongs to the opposite face.
func TestFaceVertexSels(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceGizmoLevel(t)

	wall := faceVertexSels(edgeFaceSel(0, 0, "wall", 1))
	if len(wall) != 4 {
		t.Fatalf("a wall owns %d handles, want 4: %+v", len(wall), wall)
	}
	// Sector A's second wall runs from ring vertex 1 to ring vertex 2.
	for _, vi := range []int{1, 2} {
		for _, isFloor := range []bool{true, false} {
			if !hasVert(wall, 0, vi, isFloor) {
				t.Errorf("wall 1 is missing vertex %d (floor=%v)", vi, isFloor)
			}
		}
	}

	floor := faceVertexSels(surfaceFaceSel(0, 1, "floor"))
	if len(floor) != len(ch.Sectors[1].PointIndices) {
		t.Fatalf("a floor owns %d handles, want %d", len(floor), len(ch.Sectors[1].PointIndices))
	}
	for _, sel := range floor {
		if !sel.isFloor {
			t.Errorf("a ceiling handle turned up in the floor's set: %+v", sel)
		}
	}

	// A face whose index no longer names anything owns nothing, rather than
	// dragging a handle that is not there.
	if got := faceVertexSels(edgeFaceSel(0, 0, "wall", 99)); got != nil {
		t.Errorf("a stale wall index yielded %+v", got)
	}
}

// Walls and portals are separately compacted, so a wall's index is not its
// position on the sector's ring. Converting the first edge to a portal leaves
// wall 0 as the ring's *second* edge, and the handles have to follow.
func TestFaceVertexSelsFollowsMixedEdgeKinds(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceGizmoLevel(t)

	s := &ch.Sectors[0]
	s.EdgeKinds[0] = "portal"
	s.Portals = []world.Portal{{StartPoint: ch.Points[0], EndPoint: ch.Points[1]}}
	s.Walls = s.Walls[1:]

	if got := faceEdgeRingIndex(s, "wall", 0); got != 1 {
		t.Errorf("wall 0 sits at ring index %d, want 1", got)
	}
	if got := faceEdgeRingIndex(s, "portal", 0); got != 0 {
		t.Errorf("portal 0 sits at ring index %d, want 0", got)
	}
	for _, vi := range []int{1, 2} {
		if !hasVert(faceVertexSels(edgeFaceSel(0, 0, "wall", 0)), 0, vi, true) {
			t.Errorf("wall 0 is missing floor vertex %d", vi)
		}
	}
}

// The point of the derived handles: a drag applied to a face selection lands on
// the corners the face is built from, through the same write-back a vertex drag
// uses. Geometry is never rebuilt here — that needs a GL context — so this is
// writeSelectedVertexWorld, the write half.
func TestFaceGizmoDragMovesTheFacesCorners(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceGizmoLevel(t)

	editorSelectFace(edgeFaceSel(0, 0, "wall", 0), false)
	// Wall 0 runs (0,0) → (10,0): slide it two units along +Z, the direction it
	// faces, exactly as an arm drag would.
	w := writeSelectedVertexWorld(func(i int, sel vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		return cur.Add(mgl32.Vec3{0, 0, 2})
	})

	nearVec2(t, "wall start", ch.Points[0], mgl32.Vec2{0, 2})
	nearVec2(t, "wall end", ch.Points[1], mgl32.Vec2{10, 2})
	// The far corners of the sector stayed put: the drag is the face's, not the
	// sector's.
	nearVec2(t, "opposite corner", ch.Points[2], mgl32.Vec2{10, 10})
	if len(w.moves[0]) != 2 {
		t.Errorf("%d point moves recorded, want 2 (one per corner)", len(w.moves[0]))
	}
}

// A wall's floor and ceiling handles at one corner are two names for a single
// chunk point. The XZ write path has to move it once: a second PointMove would
// carry the biome layer riding that point along at twice the distance.
func TestFaceGizmoWritesASharedPointOnce(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := faceGizmoLevel(t)

	editorSelectFace(edgeFaceSel(0, 0, "wall", 0), false)
	sels := gizmoVertexSels()
	if len(sels) != 4 {
		t.Fatalf("the drag acts on %d handles, want 4", len(sels))
	}

	calls := 0
	setSelectedVertexPoints(func(i int, sel vertexSel, pt mgl32.Vec2) mgl32.Vec2 {
		calls++
		return pt.Add(mgl32.Vec2{0, 3})
	})
	if calls != 2 {
		t.Errorf("the point write ran %d times for 2 corners", calls)
	}
	nearVec2(t, "wall start", ch.Points[0], mgl32.Vec2{0, 3})
	nearVec2(t, "wall end", ch.Points[1], mgl32.Vec2{10, 3})
}

// With anything but a face selected, the drags read exactly the list they
// always did — the resolver is a widening, not a redirection.
func TestGizmoVertexSelsIsTheVertexSelectionOtherwise(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceGizmoLevel(t)

	setVertexSelection([]vertexSel{vsel(ch, 0, 2, false)})
	got := gizmoVertexSels()
	if len(got) != 1 || got[0] != Editor.SelectedVertices[0] {
		t.Fatalf("gizmoVertexSels = %+v, want the vertex selection %+v", got, Editor.SelectedVertices)
	}

	// The 2D map has no 3D gizmo, so a face selected there resolves to nothing
	// and the top-down view keeps working in its own unit.
	editorSelectFace(surfaceFaceSel(0, 0, "floor"), false)
	Editor.Mode = Mode2D
	if got := gizmoVertexSels(); len(got) != 0 {
		t.Errorf("a face resolved to %d handles in the 2D map, want 0", len(got))
	}
}

// The centre a rotate or scale drag pivots about is the middle of the handles,
// so a floor turns about the middle of its polygon rather than about a corner.
func TestSelectedFacesCenter(t *testing.T) {
	defer saveEditorSelection()()
	faceGizmoLevel(t)

	editorSelectFace(surfaceFaceSel(0, 0, "floor"), false)
	got, ok := selectedFacesCenter()
	if !ok {
		t.Fatal("a selected floor has no centre")
	}
	// Sector A is the square (0,0)–(10,10) at floor height 0.
	if got.Sub(mgl32.Vec3{5, 0, 5}).Len() > 1e-3 {
		t.Errorf("floor centre = %v, want (5, 0, 5)", got)
	}

	// Two faces are one body: adding sector B's floor (the square (10,0)–(20,10))
	// moves the pivot east to the middle of the pair.
	addFaceToSelection(surfaceFaceSel(0, 1, "floor"))
	got, _ = selectedFacesCenter()
	if got.Sub(mgl32.Vec3{10, 0, 5}).Len() > 1e-3 {
		t.Errorf("two floors pivot about %v, want (10, 0, 5)", got)
	}
}
