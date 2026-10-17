package engrave

import (
	"image"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// Flats, sharps and naturals typed into text are drawn — from Bravura —
// by every backend: measured, rasterised and outlined, never the font's
// empty box.
func TestTextSigns(t *testing.T) {
	for r := range textSignGlyphs {
		for f := TextPlain; f <= TextBoldItalic; f++ {
			if textSign(f, r) == nil {
				t.Fatalf("%c in style %d is not set from Bravura", r, f)
			}
		}
	}
	if textSign(TextPlain, 'F') != nil || textSign(TextPlain, '#') != nil {
		t.Fatal("a letter the font has is replaced")
	}
	// Measured: the sign takes room, about a capital's.
	wF, wFs := TextWidth("F", 1), TextWidth("F♯", 1)
	if d := wFs - wF; d < 0.3 || d > 0.8 {
		t.Fatalf("♯ advances %.2f em", d)
	}
	// Outlined (PDF, SVG): one path for the sign, after the F.
	gs := TextGlyphs("B♭ clarinet", TextItalic)
	var sign *TextGlyph
	for i := range gs {
		if gs[i].Rune == '♭' {
			sign = &gs[i]
		}
	}
	if sign == nil || len(sign.Path) == 0 || sign.X <= 0 {
		t.Fatalf("no outline for ♭: %+v", sign)
	}
	minY, maxY := 0.0, -1.0
	for _, sg := range sign.Path {
		for _, p := range sg.P[:1] {
			minY, maxY = min(minY, p.Y), max(maxY, p.Y)
		}
	}
	if minY > -0.5 || maxY > 0.2 {
		t.Fatalf("♭ stands from %.2f to %.2f em: not on the baseline at a capital's height", minY, maxY)
	}
	// Rasterised: ink inside the bounds the face reports, and the run's
	// box the width the layout measured.
	fc := TextFace(TextPlain, 40)
	b, adv := font.BoundString(fc, "F♯")
	if got, want := float64(adv)/64, wFs*40; got < want-0.5 || got > want+0.5 {
		t.Fatalf("the face advances %.1f px, the layout measured %.1f", got, want)
	}
	img := image.NewAlpha(image.Rect(0, 0, 120, 80))
	d := font.Drawer{Dst: img, Src: image.Opaque, Face: fc, Dot: fixed.P(10, 60)}
	d.DrawString("F♯")
	ink := 0
	x0 := 10 + int(wF*40)
	for y := 0; y < 80; y++ {
		for x := x0; x < 120; x++ {
			if img.AlphaAt(x, y).A > 128 {
				ink++
				if y < 60+b.Min.Y.Floor()-1 || y > 60+b.Max.Y.Ceil()+1 {
					t.Fatalf("ink at y %d outside the bounds %v", y, b)
				}
			}
		}
	}
	if ink < 40 {
		t.Fatalf("the ♯ drew %d pixels", ink)
	}
}
