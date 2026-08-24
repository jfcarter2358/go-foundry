package texture

import (
	"image/color"
	"testing"

	"foundry/internal/texdoc"
)

func testPalette() texdoc.TexPalette {
	var pal texdoc.TexPalette
	pal[0] = color.RGBA{0, 0, 0, 0}
	pal[1] = color.RGBA{255, 0, 0, 255}
	pal[2] = color.RGBA{0, 255, 0, 255}
	pal[3] = color.RGBA{0, 0, 255, 255}
	pal[4] = color.RGBA{10, 10, 10, 255}
	pal[5] = color.RGBA{200, 200, 200, 128} // semi-transparent
	pal[6] = color.RGBA{255, 0, 0, 255}     // duplicate of slot 1
	return pal
}

func TestQuantizeExactMatch(t *testing.T) {
	pal := testPalette()
	got := texdoc.QuantizeNearest(&pal, color.RGBA{0, 255, 0, 255})
	if got != 2 {
		t.Errorf("exact match for slot 2 color = %d, want 2", got)
	}
}

func TestQuantizeExactMatchLowestIndexOnTie(t *testing.T) {
	pal := testPalette()
	// Slots 1 and 6 are identical; the lower index must win.
	got := texdoc.QuantizeNearest(&pal, color.RGBA{255, 0, 0, 255})
	if got != 1 {
		t.Errorf("duplicate exact match = %d, want lowest index 1", got)
	}
}

func TestQuantizeNearestNeighbor(t *testing.T) {
	pal := testPalette()
	// Close to slot 4 (10,10,10,255) but not exact.
	got := texdoc.QuantizeNearest(&pal, color.RGBA{12, 9, 11, 255})
	if got != 4 {
		t.Errorf("nearest neighbor = %d, want 4", got)
	}
}

func TestQuantizeAlphaMatchedFirst(t *testing.T) {
	pal := testPalette()
	// An input with alpha 128 should prefer slot 5 (alpha 128) over a much
	// closer RGB match at full alpha, since alpha distance is minimized first.
	got := texdoc.QuantizeNearest(&pal, color.RGBA{201, 199, 201, 128})
	if got != 5 {
		t.Errorf("alpha-first match = %d, want 5", got)
	}
}

func TestQuantizeMemoCacheMatchesUncached(t *testing.T) {
	pal := testPalette()
	q := texdoc.NewQuantCache(&pal)
	inputs := []color.RGBA{
		{0, 255, 0, 255},
		{255, 0, 0, 255},
		{12, 9, 11, 255},
		{201, 199, 201, 128},
		{77, 88, 99, 255},
		{0, 255, 0, 255}, // repeat, should hit the cache
	}
	for _, c := range inputs {
		want := texdoc.QuantizeNearest(&pal, c)
		got := q.Quantize(c)
		if got != want {
			t.Errorf("cached Quantize(%v) = %d, want %d (uncached)", c, got, want)
		}
	}
}

func TestRemapIndices(t *testing.T) {
	src := testPalette()
	dst := testPalette()
	// Destination palette drops the exact matches for slots 1 and 2 (and the
	// slot-6 duplicate of slot 1, so index 1 has no exact match anywhere in
	// dst), forcing a nearest-neighbor remap.
	dst[1] = color.RGBA{254, 1, 1, 255}
	dst[2] = color.RGBA{1, 254, 1, 255}
	dst[6] = color.RGBA{9, 9, 9, 255}

	indices := []uint8{0, 1, 2, 3, 4}
	got := texdoc.RemapIndices(&src, indices, &dst)
	want := []uint8{0, 1, 2, 3, 4}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("texdoc.RemapIndices[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestDefaultPaletteSlotZeroTransparent(t *testing.T) {
	pal := texdoc.DefaultPalette()
	if pal[0].A != 0 {
		t.Errorf("default palette slot 0 alpha = %d, want 0 (transparent by convention)", pal[0].A)
	}
}
