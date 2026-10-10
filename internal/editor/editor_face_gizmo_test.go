package editor

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// faceGizmoLevel is the two-brush fixture with the editor in face-select mode,
// where the face gizmo lives.
func faceGizmoLevel(t *testing.T) *world.Chunk {
	t.Helper()
	ch := brushTestLevel(t)
	Editor.SelMode = editorSelFace
	return ch
}

// hasVert reports whether sels holds the given corner of the given brush.
func hasVert(sels []vertexSel, bi, vi int) bool {
	for _, s := range sels {
		if s.brushIdx == bi && s.vertIdx == vi {
			return true
		}
	}
	return false
}

// Selecting a face is what puts a gizmo on screen: without a kind the origin
// lookups all refuse, and the viewport draws nothing to grab. This is the bug
// the file exists to fix — a selected face used to have no handles at all.
func TestSelectingAFaceAttachesTheGizmo(t *testing.T) {
	defer saveEditorSelection()()
	faceGizmoLevel(t)

	editorSelectFace(brushFaceSel(0, 0, boxFacePosX), false)
	if Editor.GizmoKind != gizmoKindFace {
		t.Fatalf("selecting a face left the gizmo on %q", Editor.GizmoKind)
	}
	for _, tc := range []struct {
		name   string
		origin func() (mgl32.Vec3, bool)
	}{
		{"translate", translateGizmoOrigin},
		{"rotate", rotateGizmoOrigin},
		{"scale", scaleGizmoOrigin},
	} {
		// Brush "a" is the unit box centred on (1,1,1), so the middle of its
		// +X face is (2, 1, 1).
		got, ok := tc.origin()
		if !ok {
			t.Fatalf("%s gizmo has no origin for a selected face", tc.name)
		}
		if got.Sub(mgl32.Vec3{2, 1, 1}).Len() > 1e-3 {
			t.Errorf("%s gizmo origin = %v, want (2, 1, 1)", tc.name, got)
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
	faceGizmoLevel(t)

	editorSelectFace(brushFaceSel(0, 0, boxFaceTop), false)
	setVertexSelection([]vertexSel{{chunkIdx: 0, brushIdx: 0, vertIdx: 0}})
	if Editor.GizmoKind != gizmoKindVertices {
		t.Fatalf("gizmo kind = %q, want %q", Editor.GizmoKind, gizmoKindVertices)
	}
	if len(Editor.SelectedFaces) != 0 {
		t.Errorf("the face selection survived a vertex click: %+v", Editor.SelectedFaces)
	}
}

// Which handles a face owns: exactly the corners its index loop names.
//
// This used to be the interesting question in the file — a wall owned the four
// handles of its two corners at two heights, a floor owned one *side* of its
// ring, and a wall's index had to be mapped back onto its position on that ring
// because walls and portals were compacted separately. A brush face names its
// corners outright.
func TestFaceVertexSels(t *testing.T) {
	defer saveEditorSelection()()
	faceGizmoLevel(t)

	face := faceVertexSels(brushFaceSel(0, 0, boxFacePosX))
	if len(face) != 4 {
		t.Fatalf("a box face owns %d handles, want 4: %+v", len(face), face)
	}
	// Corners 1, 3, 5 and 7 are the +X ones — see world.CornerSign, whose bit
	// 0 is the X sign.
	for _, vi := range []int{1, 3, 5, 7} {
		if !hasVert(face, 0, vi) {
			t.Errorf("the +X face is missing corner %d", vi)
		}
	}

	// A face whose index no longer names anything owns nothing, rather than
	// dragging a handle that is not there.
	if got := faceVertexSels(brushFaceSel(0, 0, 99)); got != nil {
		t.Errorf("a stale face index yielded %+v", got)
	}
}

// Two selected faces of one brush share corners, and a handle written twice
// would move the same vert twice per drag.
func TestSelectedFaceVerticesAreDeduplicated(t *testing.T) {
	defer saveEditorSelection()()
	faceGizmoLevel(t)

	// Two adjacent faces of one box: 8 corners named, 6 distinct (they share
	// the 2 along the edge where they meet).
	setFaceSelection([]faceSel{
		brushFaceSel(0, 0, boxFacePosX),
		brushFaceSel(0, 0, boxFaceTop),
	})
	got := selectedFaceVertices()
	if len(got) != 6 {
		t.Fatalf("two adjacent faces resolved to %d handles, want 6 distinct", len(got))
	}
	seen := map[vertexSel]bool{}
	for _, sel := range got {
		if seen[sel] {
			t.Fatalf("handle %+v is in the set twice", sel)
		}
		seen[sel] = true
	}
}

// The point of the derived handles: a drag applied to a face selection lands on
// the corners the face is built from, through the same write-back a vertex drag
// uses.
func TestFaceGizmoDragMovesTheFacesCorners(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceGizmoLevel(t)

	editorSelectFace(brushFaceSel(0, 0, boxFacePosX), false)
	// Slide the +X face two units along +X, the direction it faces, exactly as
	// an arm drag would.
	w := writeSelectedVertexWorld(func(i int, sel vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		return cur.Add(mgl32.Vec3{2, 0, 0})
	})

	b := &ch.Brushes[0]
	for _, vi := range []int{1, 3, 5, 7} {
		if got := b.Verts[vi].X(); !nearlyEqual(got, 4, 1e-4) {
			t.Errorf("corner %d is at x=%v, want 4", vi, got)
		}
	}
	// The far side stayed put: the drag is the face's, not the brush's.
	for _, vi := range []int{0, 2, 4, 6} {
		if got := b.Verts[vi].X(); !nearlyEqual(got, 0, 1e-4) {
			t.Errorf("corner %d moved to x=%v; the -X face should not have", vi, got)
		}
	}
	if len(w.touchedBrushes) != 1 {
		t.Errorf("%d brushes recorded as touched, want 1", len(w.touchedBrushes))
	}
}

// With anything but a face selected, the drags read exactly the list they
// always did — the resolver is a widening, not a redirection.
func TestGizmoVertexSelsIsTheVertexSelectionOtherwise(t *testing.T) {
	defer saveEditorSelection()()
	faceGizmoLevel(t)

	setVertexSelection([]vertexSel{{chunkIdx: 0, brushIdx: 0, vertIdx: 2}})
	got := gizmoVertexSels()
	if len(got) != 1 || got[0] != Editor.SelectedVertices[0] {
		t.Fatalf("gizmoVertexSels = %+v, want the vertex selection %+v", got, Editor.SelectedVertices)
	}
}

// The centre a rotate or scale drag pivots about is the middle of the handles,
// so a face turns about the middle of itself rather than about a corner.
func TestSelectedFacesCenter(t *testing.T) {
	defer saveEditorSelection()()
	faceGizmoLevel(t)

	editorSelectFace(brushFaceSel(0, 0, boxFaceTop), false)
	got, ok := selectedFacesCenter()
	if !ok {
		t.Fatal("a selected face has no centre")
	}
	// Brush "a" is the unit box centred on (1,1,1), so its top face's middle
	// is (1, 2, 1).
	if got.Sub(mgl32.Vec3{1, 2, 1}).Len() > 1e-3 {
		t.Errorf("face centre = %v, want (1, 2, 1)", got)
	}

	// Two faces are one body: adding brush "b"'s top face (centred on (3,2,1))
	// moves the pivot east to the middle of the pair.
	addFaceToSelection(brushFaceSel(0, 1, boxFaceTop))
	got, _ = selectedFacesCenter()
	if got.Sub(mgl32.Vec3{2, 2, 1}).Len() > 1e-3 {
		t.Errorf("two faces pivot about %v, want (2, 2, 1)", got)
	}
}
