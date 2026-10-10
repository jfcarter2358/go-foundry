package model

import (
	"image/color"
	"testing"

	"foundry/internal/texdoc"
)

func at(pat []color.RGBA, w, x, y int) color.RGBA { return pat[y*w+x] }

// The checker has to actually alternate: adjacent squares must differ, or it
// shows neither texel density nor anything else.
func TestAtlasGuideCheckerAlternates(t *testing.T) {
	const w, h = 128, 128
	pat := AtlasGuidePattern(w, h)
	sq := atlasCheckerTexels

	// Sample the centre of each square and check it disagrees with its
	// right-hand and lower neighbours.
	for sy := 0; sy < h/sq-1; sy++ {
		for sx := 0; sx < w/sq-1; sx++ {
			cx, cy := sx*sq+sq/2, sy*sq+sq/2
			c := at(pat, w, cx, cy)
			right := at(pat, w, cx+sq, cy)
			down := at(pat, w, cx, cy+sq)
			if c == right {
				t.Fatalf("square (%d,%d) matches its right neighbour: %v", sx, sy, c)
			}
			if c == down {
				t.Fatalf("square (%d,%d) matches its lower neighbour: %v", sx, sy, c)
			}
		}
	}
}

// Half the squares are the constant dark colour and half are tinted. Neither
// may be missing, or there is no checker at all.
func TestAtlasGuideHasBothDarkAndLitSquares(t *testing.T) {
	const w, h = 128, 128
	pat := AtlasGuidePattern(w, h)
	dark, lit := 0, 0
	for _, c := range pat {
		if c == atlasGuideDark {
			dark++
		} else {
			lit++
		}
	}
	if dark == 0 || lit == 0 {
		t.Fatalf("dark=%d lit=%d — the pattern is not a checker", dark, lit)
	}
	// A checker is an even split, give or take the edge squares.
	ratio := float64(lit) / float64(dark)
	if ratio < 0.8 || ratio > 1.25 {
		t.Errorf("lit/dark ratio = %.2f, want roughly 1", ratio)
	}
}

// Light squares must stay clearly brighter than the dark ones everywhere,
// including at the dim end of the brightness ramp — otherwise the checker stops
// reading as a checker in that corner.
func TestAtlasGuideLitSquaresStayBrighterThanDark(t *testing.T) {
	lum := func(c color.RGBA) float64 {
		return 0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)
	}
	darkLum := lum(atlasGuideDark)
	for _, uv := range [][2]float32{{0, 0}, {1, 1}, {1, 0}, {0, 1}, {0.5, 0.5}} {
		c := atlasGuideColorAt(uv[0], uv[1])
		if got := lum(c); got < darkLum*2 {
			t.Errorf("tint at %v has luminance %.1f, dark squares are %.1f — too close",
				uv, got, darkLum)
		}
	}
}

// The gradient's whole purpose: opposite corners must look clearly different,
// and must still do so after quantisation to the game's earth-toned palette.
func TestAtlasGuideCornersAreDistinct(t *testing.T) {
	pal := texdoc.DefaultPalette()
	corners := map[string][2]float32{
		"top-left":     {0, 0},
		"top-right":    {1, 0},
		"bottom-left":  {0, 1},
		"bottom-right": {1, 1},
	}
	slot := map[string]uint8{}
	for name, uv := range corners {
		slot[name] = texdoc.QuantizeNearest(&pal, atlasGuideColorAt(uv[0], uv[1]))
	}
	for a := range slot {
		for b := range slot {
			if a < b && slot[a] == slot[b] {
				t.Errorf("corners %q and %q quantise to the same palette slot %d",
					a, b, slot[a])
			}
		}
	}
}

// A pure diagonal sweep would give (u,v) and (v,u) the same colour, making the
// two off-corners indistinguishable. Keeping hue and brightness on separate
// axes is what prevents that, so assert it directly rather than trusting the
// constants — a well-meant change to either would break it silently.
func TestAtlasGuideOffDiagonalPointsDiffer(t *testing.T) {
	pal := texdoc.DefaultPalette()
	for _, pair := range [][4]float32{
		{0.2, 0.7, 0.7, 0.2},
		{0.1, 0.9, 0.9, 0.1},
		{0.35, 0.65, 0.65, 0.35},
	} {
		a := texdoc.QuantizeNearest(&pal, atlasGuideColorAt(pair[0], pair[1]))
		b := texdoc.QuantizeNearest(&pal, atlasGuideColorAt(pair[2], pair[3]))
		if a == b {
			t.Errorf("(%v,%v) and (%v,%v) mirror to the same slot %d — the sweep is ambiguous",
				pair[0], pair[1], pair[2], pair[3], a)
		}
	}
}

// The measurement the design rests on: over a real checker grid, squares far
// apart on the atlas must mostly land on different palette entries. The palette
// is earth-toned and cannot represent a full-saturation sweep, so this is
// asserted against measured numbers rather than assumed.
func TestAtlasGuideDistantSquaresDiffer(t *testing.T) {
	const n = 8 // squares along each axis
	pal := texdoc.DefaultPalette()

	slots := make([][]uint8, n)
	seen := map[uint8][][2]int{}
	for i := range slots {
		slots[i] = make([]uint8, n)
		for j := 0; j < n; j++ {
			u := (float32(i) + 0.5) / n
			v := (float32(j) + 0.5) / n
			s := texdoc.QuantizeNearest(&pal, atlasGuideColorAt(u, v))
			slots[i][j] = s
			seen[s] = append(seen[s], [2]int{i, j})
		}
	}

	if len(seen) < 36 {
		t.Errorf("only %d distinguishable colours over %d squares; the sweep has collapsed",
			len(seen), n*n)
	}

	// Two squares more than two cells apart sharing a colour is a genuine
	// ambiguity — that is the case where colour stops telling you where you are.
	far := 0
	for _, cells := range seen {
		for a := 0; a < len(cells); a++ {
			for b := a + 1; b < len(cells); b++ {
				dx, dy := cells[a][0]-cells[b][0], cells[a][1]-cells[b][1]
				if dx*dx+dy*dy > 8 {
					far++
				}
			}
		}
	}
	if far > 12 {
		t.Errorf("%d far-apart square pairs share a colour, want few", far)
	}
}

// Every atlas size the dialog offers must produce a real checker — in
// particular the smallest, where the square size is clamped to the image.
//
// The list is spelled out rather than read from the dialog (texEditorSizeOptions
// in texture_editor_ui.go): this package must not depend on the UI, and a new
// size added there failing here is exactly the reminder that is wanted.
func TestAtlasGuideWorksAtEverySize(t *testing.T) {
	for _, s := range []int32{64, 128, 256, 512, 1024} {
		size := int(s)
		pat := AtlasGuidePattern(size, size)
		if len(pat) != size*size {
			t.Fatalf("%d²: pattern has %d texels, want %d", size, len(pat), size*size)
		}
		distinct := map[color.RGBA]bool{}
		for _, c := range pat {
			distinct[c] = true
		}
		if len(distinct) < 2 {
			t.Errorf("%d²: pattern is a single flat colour", size)
		}
		for i, c := range pat {
			if c.A != 255 {
				t.Fatalf("%d²: texel %d is not opaque (%v) — the model would show through", size, i, c)
			}
		}
	}
}

// A degenerate size must not panic or divide by zero.
func TestAtlasGuideHandlesDegenerateSizes(t *testing.T) {
	for _, wh := range [][2]int{{0, 0}, {1, 1}, {0, 8}, {8, 0}, {3, 5}} {
		pat := AtlasGuidePattern(wh[0], wh[1])
		if want := wh[0] * wh[1]; len(pat) != want {
			t.Errorf("%dx%d: got %d texels, want %d", wh[0], wh[1], len(pat), want)
		}
	}
}

// FillAtlasGuide must write the guide into the layer it is given and leave the
// document's other layers alone — the Paint layer above it has to stay empty.
func TestFillAtlasGuideWritesOnlyItsLayer(t *testing.T) {
	doc, err := texdoc.NewTexDoc("t", 64, 64, texdoc.GlobalPalette)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.AddLayer("Paint"); err != nil {
		t.Fatal(err)
	}
	FillAtlasGuide(doc, 0)

	nonZero := 0
	for _, idx := range doc.Layers[0].Idx {
		if idx != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Error("guide layer is still empty after FillAtlasGuide")
	}
	for _, idx := range doc.Layers[1].Idx {
		if idx != 0 {
			t.Fatal("the Paint layer was written to; it must start empty")
		}
	}
}

// An out-of-range layer index must be ignored rather than panic.
func TestFillAtlasGuideIgnoresBadLayer(t *testing.T) {
	doc, err := texdoc.NewTexDoc("t", 16, 16, texdoc.GlobalPalette)
	if err != nil {
		t.Fatal(err)
	}
	FillAtlasGuide(doc, -1)
	FillAtlasGuide(doc, 99)
	FillAtlasGuide(nil, 0)
	for _, idx := range doc.Layers[0].Idx {
		if idx != 0 {
			t.Fatal("a bad layer index wrote to layer 0")
		}
	}
}

// The guide must be opaque once flattened: a transparent texel is discarded by
// the shader, so the model would show holes where it had not been painted.
func TestAtlasGuideFlattensOpaque(t *testing.T) {
	doc, err := texdoc.NewTexDoc("t", 64, 64, texdoc.GlobalPalette)
	if err != nil {
		t.Fatal(err)
	}
	FillAtlasGuide(doc, 0)
	flat := doc.Flatten()
	for i, idx := range flat.Pix {
		if doc.Pal[idx].A != 255 {
			t.Fatalf("texel %d flattened to palette slot %d with alpha %d",
				i, idx, doc.Pal[idx].A)
		}
	}
}
