package texture

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/texdoc"
)

func newTestEditor(t *testing.T, w, h int) *TexEditorState {
	t.Helper()
	ed, err := NewTexEditorDoc("t", w, h)
	if err != nil {
		t.Fatalf("NewTexEditorDoc: %v", err)
	}
	return ed
}

func TestBrushStampsExpectedIndices(t *testing.T) {
	ed := newTestEditor(t, 5, 5)
	ed.BrushSize = 1
	ed.BeginStroke()
	ed.StampBrush(2, 2, 7)
	ed.EndStroke()

	for y := 0; y < 5; y++ {
		for x := 0; x < 5; x++ {
			got := ed.Doc.Layers[0].Idx[y*5+x]
			want := uint8(0)
			if x == 2 && y == 2 {
				want = 7
			}
			if got != want {
				t.Errorf("(%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}
}

func TestBrushCircularShapeAtSizeThree(t *testing.T) {
	ed := newTestEditor(t, 5, 5)
	ed.BrushSize = 2 // radius 1: plus-shape (center + 4 neighbors)
	ed.BeginStroke()
	ed.StampBrush(2, 2, 9)
	ed.EndStroke()

	wantSet := map[[2]int]bool{
		{2, 2}: true, {1, 2}: true, {3, 2}: true, {2, 1}: true, {2, 3}: true,
	}
	for y := 0; y < 5; y++ {
		for x := 0; x < 5; x++ {
			got := ed.Doc.Layers[0].Idx[y*5+x]
			if wantSet[[2]int{x, y}] {
				if got != 9 {
					t.Errorf("(%d,%d) = %d, want 9 (in brush)", x, y, got)
				}
			} else if got != 0 {
				t.Errorf("(%d,%d) = %d, want 0 (outside brush)", x, y, got)
			}
		}
	}
}

func TestBrushClampsAtCanvasEdges(t *testing.T) {
	ed := newTestEditor(t, 3, 3)
	ed.BrushSize = 3 // radius 2 — larger than the whole canvas
	ed.BeginStroke()
	ed.StampBrush(0, 0, 5) // centered at a corner
	ed.EndStroke()
	// Must not panic (out-of-bounds texels silently clamped), and in-bounds
	// texels within the brush must still be painted.
	if got := ed.Doc.Layers[0].Idx[0]; got != 5 {
		t.Errorf("corner texel = %d, want 5", got)
	}
}

func TestSprayStaysWithinBrushRadius(t *testing.T) {
	ed := newTestEditor(t, 21, 21)
	ed.BrushSize = 5 // radius 4
	ed.SprayDensity = 200
	ed.BeginStroke()
	ed.StampSpray(10, 10, 3)
	ed.EndStroke()

	r := ed.BrushSize - 1
	for y := 0; y < 21; y++ {
		for x := 0; x < 21; x++ {
			if ed.Doc.Layers[0].Idx[y*21+x] == 0 {
				continue
			}
			dx, dy := x-10, y-10
			if dx*dx+dy*dy > r*r {
				t.Errorf("(%d,%d) painted outside brush radius %d from center", x, y, r)
			}
		}
	}
}

func TestSprayClampsAtCanvasEdges(t *testing.T) {
	ed := newTestEditor(t, 3, 3)
	ed.BrushSize = 5 // radius larger than the whole canvas
	ed.SprayDensity = 100
	ed.BeginStroke()
	ed.StampSpray(0, 0, 5) // must not panic on out-of-bounds candidates
	ed.EndStroke()
}

func TestSprayAtBrushSizeOneAlwaysHitsCenter(t *testing.T) {
	// Radius 0 collapses StampSpray's random offset to (0,0) every dab, so
	// this is deterministic: every call paints exactly the center texel.
	ed := newTestEditor(t, 5, 5)
	ed.BrushSize = 1
	ed.SprayDensity = 3
	ed.BeginStroke()
	ed.StampSpray(2, 2, 6)
	ed.EndStroke()

	for y := 0; y < 5; y++ {
		for x := 0; x < 5; x++ {
			got := ed.Doc.Layers[0].Idx[y*5+x]
			want := uint8(0)
			if x == 2 && y == 2 {
				want = 6
			}
			if got != want {
				t.Errorf("(%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}
}

func TestEraserWritesTransparentSlot(t *testing.T) {
	ed := newTestEditor(t, 3, 3)
	ed.Doc.Layers[0].Idx[4] = 3 // center texel, some opaque color
	ed.BrushSize = 1
	ed.BeginStroke()
	ed.StampBrush(1, 1, 0) // eraser = paint with index 0
	ed.EndStroke()
	if got := ed.Doc.Layers[0].Idx[4]; got != 0 {
		t.Errorf("erased texel = %d, want 0 (transparent slot)", got)
	}
}

func TestFillFloodsContiguousRegion(t *testing.T) {
	ed := newTestEditor(t, 3, 1)
	ed.Doc.Layers[0].Idx = []uint8{0, 0, 5}
	ed.BeginStroke()
	ed.Fill(0, 0, 2)
	ed.EndStroke()
	want := []uint8{2, 2, 5}
	for i, w := range want {
		if got := ed.Doc.Layers[0].Idx[i]; got != w {
			t.Errorf("Idx[%d] = %d, want %d", i, got, w)
		}
	}
}

func TestEyedropperReadsIndex(t *testing.T) {
	ed := newTestEditor(t, 2, 2)
	ed.Doc.Layers[0].Idx[3] = 42
	got, ok := ed.Eyedropper(1, 1)
	if !ok || got != 42 {
		t.Errorf("Eyedropper = (%d, %v), want (42, true)", got, ok)
	}
	if _, ok := ed.Eyedropper(-1, 0); ok {
		t.Error("Eyedropper out of bounds should report ok=false")
	}
}

func TestLineAndRectStamps(t *testing.T) {
	ed := newTestEditor(t, 5, 5)
	ed.BeginStroke()
	ed.StampLine(0, 0, 4, 4, 3)
	ed.EndStroke()
	for i := 0; i < 5; i++ {
		if got := ed.Doc.Layers[0].Idx[i*5+i]; got != 3 {
			t.Errorf("diagonal (%d,%d) = %d, want 3", i, i, got)
		}
	}

	ed2 := newTestEditor(t, 5, 5)
	ed2.BeginStroke()
	ed2.StampRect(1, 1, 3, 3, 4, false)
	ed2.EndStroke()
	if got := ed2.Doc.Layers[0].Idx[2*5+2]; got != 0 {
		t.Errorf("rect interior = %d, want 0 (outline only)", got)
	}
	if got := ed2.Doc.Layers[0].Idx[1*5+1]; got != 4 {
		t.Errorf("rect corner = %d, want 4", got)
	}

	ed3 := newTestEditor(t, 5, 5)
	ed3.BeginStroke()
	ed3.StampRect(1, 1, 3, 3, 4, true)
	ed3.EndStroke()
	if got := ed3.Doc.Layers[0].Idx[2*5+2]; got != 4 {
		t.Errorf("filled rect interior = %d, want 4", got)
	}
}

func TestUndoRestoresPriorIndices(t *testing.T) {
	ed := newTestEditor(t, 3, 3)
	ed.BeginStroke()
	ed.StampBrush(1, 1, 6)
	ed.EndStroke()
	if !ed.CanUndo() {
		t.Fatal("expected undo history after a paint stroke")
	}
	ed.Undo()
	if got := ed.Doc.Layers[0].Idx[4]; got != 0 {
		t.Errorf("after undo, texel = %d, want 0", got)
	}
	if !ed.CanRedo() {
		t.Fatal("expected redo to be available after an undo")
	}
	ed.Redo()
	if got := ed.Doc.Layers[0].Idx[4]; got != 6 {
		t.Errorf("after redo, texel = %d, want 6", got)
	}
}

func TestUndoNoOpStrokeRecordsNothing(t *testing.T) {
	ed := newTestEditor(t, 3, 3)
	ed.BeginStroke()
	ed.StampBrush(1, 1, 0) // painting transparent onto already-transparent
	ed.EndStroke()
	if ed.CanUndo() {
		t.Error("a stroke that changed nothing should not record an undo entry")
	}
}

func TestUndoStructuralLayerOps(t *testing.T) {
	ed := newTestEditor(t, 2, 2)
	ed.Doc.Layers[0].Idx[0] = 9
	if err := ed.AddLayer("top"); err != nil {
		t.Fatal(err)
	}
	if len(ed.Doc.Layers) != 2 {
		t.Fatalf("layer count = %d, want 2", len(ed.Doc.Layers))
	}
	ed.Undo()
	if len(ed.Doc.Layers) != 1 {
		t.Fatalf("after undo, layer count = %d, want 1", len(ed.Doc.Layers))
	}
	if got := ed.Doc.Layers[0].Idx[0]; got != 9 {
		t.Errorf("after undoing AddLayer, base layer data = %d, want 9 (preserved)", got)
	}
	ed.Redo()
	if len(ed.Doc.Layers) != 2 {
		t.Fatalf("after redo, layer count = %d, want 2", len(ed.Doc.Layers))
	}
}

func TestLayerCapEnforcedThroughEditor(t *testing.T) {
	ed := newTestEditor(t, 2, 2)
	for i := 1; i < texdoc.TexMaxLayers; i++ {
		if err := ed.AddLayer("l"); err != nil {
			t.Fatalf("AddLayer %d: %v", i, err)
		}
	}
	if err := ed.AddLayer("overflow"); err == nil {
		t.Error("AddLayer beyond the cap should fail")
	}
	// A failed op must not leave a bogus undo entry behind.
	undoCountBefore := len(ed.undoStack)
	if err := ed.AddLayer("overflow again"); err == nil {
		t.Error("AddLayer beyond the cap should fail")
	}
	if len(ed.undoStack) != undoCountBefore {
		t.Error("a rejected structural op recorded an undo entry")
	}
}

func TestLayerReorderInvariants(t *testing.T) {
	ed := newTestEditor(t, 2, 2)
	ed.Doc.Layers[0].Name = "bottom"
	if err := ed.AddLayer("top"); err != nil {
		t.Fatal(err)
	}
	if err := ed.MoveLayer(0, true); err != nil { // move "bottom" up
		t.Fatal(err)
	}
	if ed.Doc.Layers[1].Name != "bottom" {
		t.Errorf("after MoveLayer up, Layers[1].Name = %q, want bottom", ed.Doc.Layers[1].Name)
	}
	if err := ed.MoveLayer(1, false); err != nil { // move it back down
		t.Fatal(err)
	}
	if ed.Doc.Layers[0].Name != "bottom" {
		t.Errorf("after MoveLayer down, Layers[0].Name = %q, want bottom", ed.Doc.Layers[0].Name)
	}
	if err := ed.MoveLayer(0, false); err == nil {
		t.Error("moving the bottom layer down should fail")
	}
}

func TestUndoRingEvictsOldestPastByteBudget(t *testing.T) {
	ed := newTestEditor(t, 4, 4) // 16 bytes/layer snapshot per paint entry
	entrySizeBytes := 16
	// Push enough paint entries to exceed the budget several times over.
	n := texUndoByteBudget/entrySizeBytes + 5
	for i := 0; i < n; i++ {
		ed.BeginStroke()
		val := uint8(1 + i%250)
		ed.StampBrush(i%4, (i/4)%4, val)
		ed.EndStroke()
	}
	if ed.undoBytes > texUndoByteBudget {
		t.Errorf("undoBytes = %d, exceeds budget %d", ed.undoBytes, texUndoByteBudget)
	}
	if len(ed.undoStack) >= n {
		t.Errorf("undo stack has %d entries, expected eviction (pushed %d)", len(ed.undoStack), n)
	}
}

func TestNewTexEditorDocRejectsOversizedCanvas(t *testing.T) {
	if _, err := NewTexEditorDoc("t", texdoc.TexMaxDim+1, 64); err == nil {
		t.Error("NewTexEditorDoc accepted width > texdoc.TexMaxDim")
	}
}

// ---------- bump channel ----------

// TestUndoGoesBackToTheChannelItWasPaintedOn is the whole reason a paint undo
// entry records a channel: the two stacks share one undo stack, and applying a
// height stroke's bytes to a colour layer would repaint the albedo with
// heights.
func TestUndoGoesBackToTheChannelItWasPaintedOn(t *testing.T) {
	ed := newTestEditor(t, 4, 4)
	ed.BeginStroke()
	ed.StampBrush(1, 1, 5) // a colour texel
	ed.EndStroke()

	ed.Doc.SetChannel(texdoc.ChanBump)
	ed.BeginStroke()
	ed.StampBrush(2, 2, 180) // a height
	ed.EndStroke()

	// Switch away before undoing — the case that used to write to whatever
	// happened to be checked out.
	ed.Doc.SetChannel(texdoc.ChanColor)
	ed.Undo()

	if ed.Doc.Channel() != texdoc.ChanBump {
		t.Fatalf("undo left channel %v checked out, want the bump channel it was painted on", ed.Doc.Channel())
	}
	if got := ed.Doc.Layers[0].Idx[2*4+2]; got != 0 {
		t.Errorf("height texel = %d after undo, want 0", got)
	}
	ed.Doc.SetChannel(texdoc.ChanColor)
	if got := ed.Doc.Layers[0].Idx[1*4+1]; got != 5 {
		t.Errorf("colour texel = %d, want 5 — the undo reached the wrong channel", got)
	}
}

// TestUndoGoesBackToTheFrameItWasPaintedOn is the same property for the
// animation stack, which the channel bookkeeping made it natural to record.
func TestUndoGoesBackToTheFrameItWasPaintedOn(t *testing.T) {
	ed := newTestEditor(t, 4, 4)
	if err := ed.AddFrame(); err != nil {
		t.Fatal(err)
	}
	if err := ed.SetActiveFrame(1); err != nil {
		t.Fatal(err)
	}
	ed.BeginStroke()
	ed.StampBrush(0, 0, 3)
	ed.EndStroke()

	if err := ed.SetActiveFrame(0); err != nil {
		t.Fatal(err)
	}
	ed.Undo()

	if got := ed.Doc.ActiveFrameIndex(); got != 1 {
		t.Fatalf("undo left frame %d checked out, want frame 1", got)
	}
	if got := ed.Doc.Layers[0].Idx[0]; got != 0 {
		t.Errorf("frame 1 texel = %d after undo, want 0", got)
	}
}

// TestRedoAfterAChannelUndo round-trips the pair, since redo shares the same
// checkout path.
func TestRedoAfterAChannelUndo(t *testing.T) {
	ed := newTestEditor(t, 4, 4)
	ed.Doc.SetChannel(texdoc.ChanBump)
	ed.BeginStroke()
	ed.StampBrush(1, 1, 99)
	ed.EndStroke()

	ed.Doc.SetChannel(texdoc.ChanColor)
	ed.Undo()
	ed.Doc.SetChannel(texdoc.ChanColor)
	ed.Redo()

	if ed.Doc.Channel() != texdoc.ChanBump {
		t.Fatalf("redo left channel %v checked out", ed.Doc.Channel())
	}
	if got := ed.Doc.Layers[0].Idx[1*4+1]; got != 99 {
		t.Errorf("height texel = %d after redo, want 99", got)
	}
}

// TestPaintIndexIsPerChannel — the brush must not lose its colour because the
// artist went to touch up the height field.
func TestPaintIndexIsPerChannel(t *testing.T) {
	ed := newTestEditor(t, 2, 2)
	ed.SetPaintIndex(12)
	ed.Doc.SetChannel(texdoc.ChanBump)
	if got := ed.PaintIndex(); got == 12 {
		t.Error("the bump channel inherited the colour channel's palette index")
	}
	ed.SetPaintIndex(200)
	ed.Doc.SetChannel(texdoc.ChanColor)
	if got := ed.PaintIndex(); got != 12 {
		t.Errorf("colour paint index = %d after painting the bump channel, want 12", got)
	}
}

// TestDeletingTheBumpMapIsUndoable — it throws away every frame's height
// layers at once, which is the most destructive single action in the editor.
func TestDeletingTheBumpMapIsUndoable(t *testing.T) {
	ed := newTestEditor(t, 4, 4)
	ed.SetChannel(texdoc.ChanBump)
	ed.BeginStroke()
	ed.StampBrush(2, 2, 140)
	ed.EndStroke()

	ed.DisableBump()
	if ed.Doc.HasBump() {
		t.Fatal("DisableBump did nothing")
	}

	ed.Undo()
	if !ed.Doc.HasBump() {
		t.Fatal("undo did not bring the bump channel back")
	}
	ed.Doc.SetChannel(texdoc.ChanBump)
	if got := ed.Doc.Layers[0].Idx[2*4+2]; got != 140 {
		t.Errorf("height texel = %d after undoing the delete, want 140", got)
	}
}

// TestCreatingTheBumpChannelIsUndoable: switching channels is a view change,
// but the switch that *creates* the channel changes the document, so it takes
// a snapshot like every other structural op.
func TestCreatingTheBumpChannelIsUndoable(t *testing.T) {
	ed := newTestEditor(t, 2, 2)
	if ed.CanUndo() {
		t.Fatal("precondition: fresh editor has undo history")
	}
	ed.SetChannel(texdoc.ChanBump)
	if !ed.CanUndo() {
		t.Fatal("creating the bump channel recorded no undo step")
	}
	ed.SetChannel(texdoc.ChanColor)
	if ed.CanUndo() {
		// Switching back is a view change and must not have pushed anything.
		if len(ed.undoStack) != 1 {
			t.Errorf("undo stack has %d entries, want 1 — switching channels is not an edit", len(ed.undoStack))
		}
	}
	ed.Undo()
	if ed.Doc.HasBump() {
		t.Error("undo did not remove the bump channel it created")
	}
}

// TestBumpDirtyGatesTheNormalMapDerivation: painting colour must not make the
// UI re-derive a normal map, which is a Sobel over the whole canvas.
func TestBumpDirtyGatesTheNormalMapDerivation(t *testing.T) {
	ed := newTestEditor(t, 4, 4)
	ed.BumpDirty = false

	ed.BeginStroke()
	ed.StampBrush(1, 1, 3) // colour
	ed.EndStroke()
	if ed.BumpDirty {
		t.Error("a colour stroke marked the height field dirty")
	}
	if !ed.PreviewDirty {
		t.Error("a colour stroke did not mark the preview dirty")
	}

	ed.SetChannel(texdoc.ChanBump)
	ed.BumpDirty = false
	ed.BeginStroke()
	ed.StampBrush(1, 1, 90)
	ed.EndStroke()
	if !ed.BumpDirty {
		t.Error("a height stroke did not mark the height field dirty")
	}
}

func TestImportLayerImageIsOneUndoStep(t *testing.T) {
	ed := newTestEditor(t, 4, 4)
	path := filepath.Join(t.TempDir(), "in.png")
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ed.ImportLayerImage(path, texdoc.FitStretch, false); err != nil {
		t.Fatal(err)
	}
	if len(ed.Doc.Layers) != 2 || ed.Doc.Layers[1].Name != "in" || ed.Doc.Layers[1].Idx[0] == 0 {
		t.Fatalf("import did not add a painted layer named after the file")
	}
	ed.Undo()
	if len(ed.Doc.Layers) != 1 {
		t.Fatalf("undo left %d layers, want 1", len(ed.Doc.Layers))
	}
}
