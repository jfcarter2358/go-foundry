package texture

// texture_doc_test.go — the layered-document model from internal/texdoc,
// exercised here because this is the package that composites and stores it.

import (
	"image/color"
	"testing"

	"foundry/internal/texdoc"
)

// flattenPalette gives distinct, easily-reasoned-about colors: 0 transparent,
// 1 red, 2 green, 3 blue, 4 a 50% grey used to probe blend-quantisation.
func flattenPalette() texdoc.TexPalette {
	var pal texdoc.TexPalette
	pal[0] = color.RGBA{0, 0, 0, 0}
	pal[1] = color.RGBA{255, 0, 0, 255}
	pal[2] = color.RGBA{0, 255, 0, 255}
	pal[3] = color.RGBA{0, 0, 255, 255}
	pal[4] = color.RGBA{128, 128, 128, 255}
	return pal
}

func onePixelDoc(pal texdoc.TexPalette) *texdoc.TexDoc {
	d, err := texdoc.NewTexDoc("t", 1, 1, pal)
	if err != nil {
		panic(err)
	}
	return d
}

// everyTexelOnPalette is the invariant that matters most: Flatten must never
// produce a color outside d.Pal.
func everyTexelOnPalette(t *testing.T, d *texdoc.TexDoc) {
	t.Helper()
	out := d.Flatten()
	for i, px := range out.Pix {
		found := false
		for _, c := range d.Pal {
			if out.Palette[px] == c {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("texel %d (index %d) is not a palette entry", i, px)
		}
	}
}

func TestFlattenOpaqueOverOpaque(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	d.Layers[0].Idx[0] = 1 // red
	if err := d.AddLayer("top"); err != nil {
		t.Fatal(err)
	}
	d.Layers[1].Idx[0] = 3 // blue, fully opaque, fully covers red

	out := d.Flatten()
	if got := out.Pix[0]; got != 3 {
		t.Errorf("top opaque layer index = %d, want 3 (blue)", got)
	}
	everyTexelOnPalette(t, d)
}

func TestFlattenFiftyPercentOver(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	d.Layers[0].Idx[0] = 1 // red
	if err := d.AddLayer("top"); err != nil {
		t.Fatal(err)
	}
	d.Layers[1].Idx[0] = 3 // blue
	d.Layers[1].Opacity = 0.5

	out := d.Flatten()
	// 50% blue over red composites to roughly (128,0,128); with this palette's
	// only candidates being pure red/green/blue/grey, grey (slot 4) is the
	// nearest entry — the key invariant is that it lands on-palette at all.
	everyTexelOnPalette(t, d)
	if got := out.Pix[0]; got != 4 {
		t.Errorf("50%% blend index = %d, want 4 (grey, nearest to the blended color)", got)
	}
}

func TestFlattenHiddenLayerSkipped(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	d.Layers[0].Idx[0] = 1 // red
	if err := d.AddLayer("top"); err != nil {
		t.Fatal(err)
	}
	d.Layers[1].Idx[0] = 3 // blue
	d.Layers[1].Visible = false

	out := d.Flatten()
	if got := out.Pix[0]; got != 1 {
		t.Errorf("hidden top layer still visible: index = %d, want 1 (red)", got)
	}
}

func TestFlattenLayerOpacityZeroSkipped(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	d.Layers[0].Idx[0] = 1
	if err := d.AddLayer("top"); err != nil {
		t.Fatal(err)
	}
	d.Layers[1].Idx[0] = 3
	d.Layers[1].Opacity = 0

	out := d.Flatten()
	if got := out.Pix[0]; got != 1 {
		t.Errorf("zero-opacity layer contributed: index = %d, want 1 (red)", got)
	}
}

func TestFlattenBlendModes(t *testing.T) {
	pal := flattenPalette()
	pal[5] = color.RGBA{64, 0, 0, 255}    // dark red, multiply(red,red)/255 target-ish
	pal[6] = color.RGBA{255, 255, 0, 255} // yellow, screen(red,green) target-ish

	cases := []struct {
		name  string
		blend texdoc.TexBlend
	}{
		{"normal", texdoc.BlendNormal},
		{"multiply", texdoc.BlendMultiply},
		{"add", texdoc.BlendAdd},
		{"screen", texdoc.BlendScreen},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := onePixelDoc(pal)
			d.Layers[0].Idx[0] = 1 // red
			if err := d.AddLayer("top"); err != nil {
				t.Fatal(err)
			}
			d.Layers[1].Idx[0] = 2 // green
			d.Layers[1].Blend = c.blend
			everyTexelOnPalette(t, d)
		})
	}
}

func TestFlattenSingleLayerPassthrough(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	d.Layers[0].Idx[0] = 2
	out := d.Flatten()
	if got := out.Pix[0]; got != 2 {
		t.Errorf("single layer passthrough index = %d, want 2", got)
	}
}

func TestFlattenEmptyDoc(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal) // layer index defaults to 0 (transparent)
	out := d.Flatten()
	if got := out.Pix[0]; got != 0 {
		t.Errorf("empty doc index = %d, want 0 (transparent)", got)
	}
}

func TestFlattenEveryTexelOnPalette(t *testing.T) {
	pal := texdoc.DefaultPalette()
	d, err := texdoc.NewTexDoc("t", 8, 8, pal)
	if err != nil {
		t.Fatal(err)
	}
	for i := range d.Layers[0].Idx {
		d.Layers[0].Idx[i] = uint8(i % texdoc.TexPaletteSz)
	}
	if err := d.AddLayer("top"); err != nil {
		t.Fatal(err)
	}
	for i := range d.Layers[1].Idx {
		d.Layers[1].Idx[i] = uint8((i * 7) % texdoc.TexPaletteSz)
	}
	d.Layers[1].Opacity = 0.37
	d.Layers[1].Blend = texdoc.BlendScreen
	everyTexelOnPalette(t, d)
}

func TestNewTexDocRejectsOversizedCanvas(t *testing.T) {
	pal := flattenPalette()
	if _, err := texdoc.NewTexDoc("t", texdoc.TexMaxDim+1, 64, pal); err == nil {
		t.Error("texdoc.NewTexDoc accepted width > texdoc.TexMaxDim")
	}
	if _, err := texdoc.NewTexDoc("t", 64, texdoc.TexMaxDim+1, pal); err == nil {
		t.Error("texdoc.NewTexDoc accepted height > texdoc.TexMaxDim")
	}
	if _, err := texdoc.NewTexDoc("t", texdoc.TexMaxDim, texdoc.TexMaxDim, pal); err != nil {
		t.Errorf("texdoc.NewTexDoc rejected the exact cap: %v", err)
	}
}

// ---------- animation stack (frames) ----------

func TestNewDocStartsWithOneFrame(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	if d.FrameCount() != 1 {
		t.Fatalf("FrameCount = %d, want 1", d.FrameCount())
	}
	if got := d.FPS(); got != 0 {
		t.Errorf("FPS of a one-frame (unanimated) doc = %v, want 0", got)
	}
}

func TestAddFrameGrowsStackAndFPS(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	if err := d.AddFrame(); err != nil {
		t.Fatal(err)
	}
	if err := d.AddFrame(); err != nil {
		t.Fatal(err)
	}
	if d.FrameCount() != 3 {
		t.Fatalf("FrameCount = %d, want 3", d.FrameCount())
	}
	if got := d.FPS(); got != 3 {
		t.Errorf("FPS = %v, want 3 (one frame count per second)", got)
	}
}

func TestFrameEditsAreIsolatedPerFrame(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	d.Layers[0].Idx[0] = 1 // red, on frame 0
	if err := d.AddFrame(); err != nil {
		t.Fatal(err)
	}
	if err := d.SetActiveFrame(1); err != nil {
		t.Fatal(err)
	}
	if got := d.Layers[0].Idx[0]; got != 0 {
		t.Errorf("frame 1 started with texel = %d, want 0 (fresh, not inherited from frame 0)", got)
	}
	d.Layers[0].Idx[0] = 3 // blue, on frame 1

	if got := d.FlattenFrame(0).Pix[0]; got != 1 {
		t.Errorf("frame 0 flattened = %d, want 1 (red, unaffected by editing frame 1)", got)
	}
	if got := d.FlattenFrame(1).Pix[0]; got != 3 {
		t.Errorf("frame 1 flattened = %d, want 3 (blue)", got)
	}

	if err := d.SetActiveFrame(0); err != nil {
		t.Fatal(err)
	}
	if got := d.Layers[0].Idx[0]; got != 1 {
		t.Errorf("after checking back out to frame 0, texel = %d, want 1 (preserved)", got)
	}
}

func TestDuplicateFrameCopiesLayersIndependently(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	d.Layers[0].Idx[0] = 2
	if err := d.DuplicateFrame(0); err != nil {
		t.Fatal(err)
	}
	if d.FrameCount() != 2 {
		t.Fatalf("FrameCount = %d, want 2", d.FrameCount())
	}
	if err := d.SetActiveFrame(1); err != nil {
		t.Fatal(err)
	}
	d.Layers[0].Idx[0] = 5 // mutate the duplicate
	if got := d.FlattenFrame(0).Pix[0]; got != 2 {
		t.Errorf("original frame changed to %d after editing its duplicate, want unchanged 2", got)
	}
}

func TestDeleteActiveFrameChecksOutAValidFrame(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	if err := d.AddFrame(); err != nil {
		t.Fatal(err)
	}
	if err := d.AddFrame(); err != nil {
		t.Fatal(err)
	}
	if err := d.SetActiveFrame(1); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteFrame(1); err != nil {
		t.Fatal(err)
	}
	if d.FrameCount() != 2 {
		t.Fatalf("FrameCount = %d, want 2", d.FrameCount())
	}
	// Deleting the checked-out frame must leave Layers/Active pointing at a
	// real surviving frame, not the deleted one's now-orphaned data.
	if d.Layers == nil || len(d.Layers) == 0 {
		t.Fatal("Layers is empty after deleting the checked-out frame")
	}
}

func TestDeleteFrameRejectsTheLastOne(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	if err := d.DeleteFrame(0); err == nil {
		t.Error("DeleteFrame should refuse to delete the only remaining frame")
	}
}

func TestMoveFrameSwapsOrder(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	d.Layers[0].Idx[0] = 1
	if err := d.AddFrame(); err != nil {
		t.Fatal(err)
	}
	if err := d.SetActiveFrame(1); err != nil {
		t.Fatal(err)
	}
	d.Layers[0].Idx[0] = 3
	if err := d.MoveFrame(0, true); err != nil { // frame 0 (red) moves after frame 1 (blue)
		t.Fatal(err)
	}
	if got := d.FlattenFrame(0).Pix[0]; got != 3 {
		t.Errorf("frame 0 after MoveFrame = %d, want 3 (blue, was frame 1)", got)
	}
	if got := d.FlattenFrame(1).Pix[0]; got != 1 {
		t.Errorf("frame 1 after MoveFrame = %d, want 1 (red, was frame 0)", got)
	}
}

func TestFrameLimit(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	for i := 1; i < texdoc.TexMaxFrames; i++ {
		if err := d.AddFrame(); err != nil {
			t.Fatalf("AddFrame %d: %v", i, err)
		}
	}
	if d.FrameCount() != texdoc.TexMaxFrames {
		t.Fatalf("frame count = %d, want %d", d.FrameCount(), texdoc.TexMaxFrames)
	}
	if err := d.AddFrame(); err == nil {
		t.Error("AddFrame beyond the cap should fail")
	}
	if err := d.DuplicateFrame(0); err == nil {
		t.Error("DuplicateFrame beyond the cap should fail")
	}
}

func TestCloneTexDocCopiesEveryFrame(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	d.Layers[0].Idx[0] = 1
	if err := d.AddFrame(); err != nil {
		t.Fatal(err)
	}
	if err := d.SetActiveFrame(1); err != nil {
		t.Fatal(err)
	}
	d.Layers[0].Idx[0] = 3

	clone := texdoc.CloneTexDoc(d)
	if clone.FrameCount() != 2 {
		t.Fatalf("clone frame count = %d, want 2", clone.FrameCount())
	}
	clone.Layers[0].Idx[0] = 9 // mutate the clone's checked-out frame
	if got := d.Layers[0].Idx[0]; got != 3 {
		t.Errorf("original doc's frame 1 changed to %d after mutating the clone, want unchanged 3", got)
	}
	if got := clone.FlattenFrame(0).Pix[0]; got != 1 {
		t.Errorf("clone frame 0 = %d, want 1 (copied from the original)", got)
	}
}

func TestLayerLimits(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	for i := 1; i < texdoc.TexMaxLayers; i++ {
		if err := d.AddLayer("l"); err != nil {
			t.Fatalf("AddLayer %d: %v", i, err)
		}
	}
	if len(d.Layers) != texdoc.TexMaxLayers {
		t.Fatalf("layer count = %d, want %d", len(d.Layers), texdoc.TexMaxLayers)
	}
	if err := d.AddLayer("overflow"); err == nil {
		t.Error("AddLayer beyond the cap should fail")
	}
	if err := d.DuplicateLayer(0); err == nil {
		t.Error("DuplicateLayer beyond the cap should fail")
	}
}
