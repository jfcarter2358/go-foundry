package texture

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/texdoc"
)

func texelAt(ed *TexEditorState, x, y int) uint8 {
	return ed.Doc.Layers[ed.Doc.Active].Idx[y*ed.Doc.W+x]
}

func TestSelectionFencesPaint(t *testing.T) {
	ed := newTestEditor(t, 8, 8)
	ed.SelectRect(2, 2, 4, 4, SelectReplace)
	ed.BeginStroke()
	ed.StampRect(0, 0, 7, 7, 5, true)
	ed.EndStroke()
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			want := uint8(0)
			if x >= 2 && x <= 4 && y >= 2 && y <= 4 {
				want = 5
			}
			if got := texelAt(ed, x, y); got != want {
				t.Fatalf("(%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}
}

// A flood fill whose region runs out of the selection must stop at its edge,
// not spin forever on texels setTexel refuses to change.
func TestFillStopsAtSelectionEdge(t *testing.T) {
	ed := newTestEditor(t, 8, 8)
	ed.SelectRect(1, 1, 3, 3, SelectReplace)
	ed.BeginStroke()
	ed.Fill(2, 2, 9)
	ed.EndStroke()
	n := 0
	for _, v := range ed.Doc.Layers[0].Idx {
		if v == 9 {
			n++
		}
	}
	if n != 9 {
		t.Fatalf("filled %d texels, want 9", n)
	}
}

func TestSelectModesCombine(t *testing.T) {
	ed := newTestEditor(t, 4, 1)
	ed.SelectRect(0, 0, 1, 0, SelectReplace)
	ed.SelectRect(3, 0, 3, 0, SelectAdd)
	ed.SelectRect(1, 0, 1, 0, SelectSubtract)
	want := []bool{true, false, false, true}
	for x, w := range want {
		if ed.InSelection(x, 0) != w {
			t.Fatalf("x=%d selected=%v, want %v", x, !w, w)
		}
	}
	ed.SelectRect(0, 0, 0, 0, SelectSubtract)
	ed.SelectRect(3, 0, 3, 0, SelectSubtract)
	if ed.HasSelection() {
		t.Fatal("subtracting everything should leave no selection, not an empty fence")
	}
}

func TestLassoSelectsTexelCentresInside(t *testing.T) {
	ed := newTestEditor(t, 8, 8)
	// A triangle covering the top-left half of a 4x4 block.
	ed.Select(ed.LassoMask([]mgl32.Vec2{{0, 0}, {4, 0}, {0, 4}}), SelectReplace)
	if !ed.InSelection(0, 0) || !ed.InSelection(2, 0) || !ed.InSelection(0, 2) {
		t.Error("texels inside the lasso are not selected")
	}
	if ed.InSelection(3, 3) || ed.InSelection(5, 5) {
		t.Error("texels outside the lasso are selected")
	}
}

func TestCopyPasteLandsInPlaceAsFloat(t *testing.T) {
	ed := newTestEditor(t, 8, 8)
	ed.BeginStroke()
	ed.setTexel(1, 1, 4)
	ed.setTexel(2, 1, 6)
	ed.EndStroke()
	ed.SelectRect(1, 1, 2, 1, SelectReplace)
	if !ed.Copy() {
		t.Fatal("Copy failed")
	}
	ed.Deselect()

	// Paste into another document of the same size, onto a different layer.
	other := newTestEditor(t, 8, 8)
	if !other.Paste() {
		t.Fatal("Paste failed")
	}
	if texelAt(other, 1, 1) != 4 || texelAt(other, 2, 1) != 6 {
		t.Fatal("paste did not land where the copy came from")
	}
	if !other.InSelection(1, 1) || other.InSelection(0, 0) {
		t.Fatal("paste should select what it pasted")
	}
	other.Nudge(3, 2)
	if texelAt(other, 1, 1) != 0 || texelAt(other, 4, 3) != 4 || texelAt(other, 5, 3) != 6 {
		t.Fatal("moving the paste should carry it, leaving nothing behind")
	}
	other.Undo()
	if texelAt(other, 1, 1) != 4 || texelAt(other, 4, 3) != 0 || !other.InSelection(1, 1) {
		t.Fatal("undoing the move should restore the art and the selection")
	}
	other.Undo()
	if texelAt(other, 1, 1) != 0 || other.HasSelection() {
		t.Fatal("undoing the paste should remove it and its selection")
	}
}

func TestCutClearsOnlySelection(t *testing.T) {
	ed := newTestEditor(t, 4, 4)
	ed.BeginStroke()
	ed.StampRect(0, 0, 3, 3, 7, true)
	ed.EndStroke()
	ed.SelectRect(0, 0, 1, 1, SelectReplace)
	ed.Cut()
	if texelAt(ed, 0, 0) != 0 || texelAt(ed, 1, 1) != 0 || texelAt(ed, 2, 2) != 7 {
		t.Fatal("cut should clear the selection and nothing else")
	}
	if ed.Paste(); texelAt(ed, 0, 0) != 7 {
		t.Fatal("pasting a cut should bring it back")
	}
}

// Dragging a float across art must not eat what it passes over, and content
// dragged off the canvas edge must come back when dragged back.
func TestMoveKeepsFloatWhole(t *testing.T) {
	ed := newTestEditor(t, 8, 1)
	ed.BeginStroke()
	ed.setTexel(0, 0, 3)
	ed.setTexel(4, 0, 9)
	ed.EndStroke()
	ed.SelectRect(0, 0, 0, 0, SelectReplace)

	if !ed.BeginMove() {
		t.Fatal("BeginMove failed")
	}
	ed.MoveBy(4, 0) // over the 9
	if texelAt(ed, 4, 0) != 3 {
		t.Fatal("float should cover what it sits on")
	}
	ed.MoveBy(6, 0) // past it
	if texelAt(ed, 4, 0) != 9 {
		t.Fatal("the texel the float passed over was eaten")
	}
	ed.MoveBy(20, 0) // off the canvas entirely
	ed.EndMove()
	if ed.HasSelection() {
		t.Fatal("a float wholly off the canvas selects nothing")
	}
	// Selection is gone, so the float can't be picked up by a drag — but the
	// undo of the move brings both back.
	ed.Undo()
	if texelAt(ed, 0, 0) != 3 || !ed.InSelection(0, 0) {
		t.Fatal("undo should restore the content and selection")
	}
}

// Painting after a move invalidates the float: the next move lifts the layer
// as it stands, paint included, instead of stamping stale base content back.
func TestPaintAfterMoveRelifts(t *testing.T) {
	ed := newTestEditor(t, 6, 1)
	ed.BeginStroke()
	ed.setTexel(0, 0, 3)
	ed.EndStroke()
	ed.SelectRect(0, 0, 1, 0, SelectReplace)
	ed.Nudge(1, 0) // selection now 1..2, texel 1 = 3
	ed.BeginStroke()
	ed.setTexel(2, 0, 8)
	ed.EndStroke()
	ed.Nudge(2, 0)
	got := []uint8{texelAt(ed, 0, 0), texelAt(ed, 1, 0), texelAt(ed, 2, 0), texelAt(ed, 3, 0), texelAt(ed, 4, 0)}
	want := []uint8{0, 0, 0, 3, 8}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row = %v, want %v", got, want)
		}
	}
}

func TestSelectionFencesBumpChannelToo(t *testing.T) {
	ed := newTestEditor(t, 4, 4)
	ed.SelectRect(0, 0, 0, 0, SelectReplace)
	ed.SetChannel(texdoc.ChanBump)
	ed.BeginStroke()
	ed.StampRect(0, 0, 3, 3, 200, true)
	ed.EndStroke()
	if texelAt(ed, 0, 0) != 200 || texelAt(ed, 1, 1) != 0 {
		t.Fatal("the selection should fence the bump channel as well")
	}
}

func rowOf(ed *TexEditorState, y int) []uint8 {
	return append([]uint8(nil), ed.Doc.Layers[ed.Doc.Active].Idx[y*ed.Doc.W:(y+1)*ed.Doc.W]...)
}

// Scaling resamples by nearest neighbour, moves the selection with the
// content, and is one undo step that takes the selection back too.
func TestScaleResamplesAndUndoes(t *testing.T) {
	ed := newTestEditor(t, 8, 1)
	ed.BeginStroke()
	ed.setTexel(0, 0, 3)
	ed.setTexel(1, 0, 5)
	ed.EndStroke()
	ed.SelectRect(0, 0, 1, 0, SelectReplace)

	if !ed.BeginScale() {
		t.Fatal("BeginScale failed")
	}
	ed.ScaleEdges(0, 0, 2, 0) // right edge 2 -> 4: double the width
	ed.EndScale()
	if got, want := rowOf(ed, 0), []uint8{3, 3, 5, 5, 0, 0, 0, 0}; string(got) != string(want) {
		t.Fatalf("row = %v, want %v", got, want)
	}
	if !ed.InSelection(3, 0) || ed.InSelection(4, 0) {
		t.Fatal("the selection should cover the scaled content")
	}
	ed.Undo()
	if got, want := rowOf(ed, 0), []uint8{3, 5, 0, 0, 0, 0, 0, 0}; string(got) != string(want) {
		t.Fatalf("after undo row = %v, want %v", got, want)
	}
	if ed.InSelection(2, 0) {
		t.Fatal("undo should put the selection back")
	}
}

// Shrinking and then growing back resamples from the original content, so
// nothing is lost on the way down.
func TestScaleDownAndBackIsLossless(t *testing.T) {
	ed := newTestEditor(t, 8, 1)
	ed.BeginStroke()
	for x, v := range []uint8{1, 2, 3, 4} {
		ed.setTexel(x, 0, v)
	}
	ed.EndStroke()
	ed.SelectRect(0, 0, 3, 0, SelectReplace)

	ed.BeginScale()
	ed.ScaleEdges(0, 0, -3, 0) // one texel wide
	ed.EndScale()
	ed.BeginScale()
	ed.ScaleEdges(0, 0, 3, 0) // back to four
	ed.EndScale()
	if got, want := rowOf(ed, 0), []uint8{1, 2, 3, 4, 0, 0, 0, 0}; string(got) != string(want) {
		t.Fatalf("row = %v, want %v", got, want)
	}
}

// Dragging an edge past the opposite one mirrors the content.
func TestScalePastOppositeEdgeMirrors(t *testing.T) {
	ed := newTestEditor(t, 8, 1)
	ed.BeginStroke()
	ed.setTexel(2, 0, 1)
	ed.setTexel(3, 0, 2)
	ed.EndStroke()
	ed.SelectRect(2, 0, 3, 0, SelectReplace)

	ed.BeginScale()
	ed.ScaleEdges(0, 0, -4, 0) // right edge 4 -> 0, past the left edge at 2
	ed.EndScale()
	if got, want := rowOf(ed, 0), []uint8{2, 1, 0, 0, 0, 0, 0, 0}; string(got) != string(want) {
		t.Fatalf("row = %v, want %v", got, want)
	}
}
