package uifont

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// testFace is the compiled-in bitmap face. It needs no fixture on disk and its
// metrics are exact integers, so every expectation below is a whole number
// rather than a tolerance.
func testFace(t *testing.T, px float32) *Atlas {
	t.Helper()
	Reset()
	t.Cleanup(Reset)
	prev := LogMissing
	LogMissing = nil
	t.Cleanup(func() { LogMissing = prev })
	return Face("", px)
}

// ttfFace is the same thing for the real rasteriser, skipped until a font is
// checked into testdata/. The TTF path and the bitmap path share every line of
// layout code, so the bitmap tests cover the logic; these cover the binding to
// x/image.
func ttfFace(t *testing.T, px float32) *Atlas {
	t.Helper()
	names, _ := filepath.Glob("testdata/*" + FontExt)
	if len(names) == 0 {
		t.Skip("no .ttf in internal/uifont/testdata — drop one in to exercise the opentype path")
	}
	data, err := os.ReadFile(names[0])
	if err != nil {
		t.Fatalf("read %s: %v", names[0], err)
	}
	f, err := parseFontBytes(data)
	if err != nil {
		t.Fatalf("parse %s: %v", names[0], err)
	}
	return newAtlas(strings.TrimSuffix(filepath.Base(names[0]), FontExt), px, f)
}

func TestFallbackDataIsWellFormed(t *testing.T) {
	for r := rune(0x20); r <= 0x7E; r++ {
		pat, ok := fallbackGlyphs[r]
		if !ok {
			t.Fatalf("no fallback glyph for %q (U+%04X)", r, r)
		}
		rows := splitRows(pat)
		if n := strings.Count(pat, "/"); n != fallbackRows-1 {
			t.Fatalf("%q: %d rows, want %d", r, n+1, fallbackRows)
		}
		for i, row := range rows {
			if len(row) != fallbackCols {
				t.Fatalf("%q row %d: %d cols, want %d", r, i, len(row), fallbackCols)
			}
			if strings.Trim(row, ".#") != "" {
				t.Fatalf("%q row %d: %q has something other than . and #", r, i, row)
			}
		}
	}
	if len(fallbackGlyphs) != 0x7F-0x20 {
		t.Fatalf("%d fallback glyphs, want %d", len(fallbackGlyphs), 0x7F-0x20)
	}
}

// TestFallbackSitsOnTheBaseline is the one that catches an off-by-one in the
// cell geometry: caps must sit entirely above the baseline and a descender must
// cross it, or every line of text is drawn at the wrong height.
func TestFallbackSitsOnTheBaseline(t *testing.T) {
	a := testFace(t, 16)

	h, ok := a.Glyph('H')
	if !ok {
		t.Fatal("no H")
	}
	if h.BearingY >= 0 {
		t.Errorf("H top at %v, want above the baseline (negative)", h.BearingY)
	}
	if bottom := h.BearingY + h.H; bottom != 0 {
		t.Errorf("H bottom at %v, want exactly on the baseline", bottom)
	}

	g, ok := a.Glyph('g')
	if !ok {
		t.Fatal("no g")
	}
	if bottom := g.BearingY + g.H; bottom <= 0 {
		t.Errorf("g bottom at %v, want below the baseline", bottom)
	}
}

func TestSpaceHasAnAdvanceAndNoPixels(t *testing.T) {
	a := testFace(t, 16)
	g, ok := a.Glyph(' ')
	if !ok {
		t.Fatal("space reported missing — it is a real glyph with no ink")
	}
	if g.W != 0 || g.H != 0 {
		t.Errorf("space is %vx%v, want no pixels", g.W, g.H)
	}
	if g.Advance <= 0 {
		t.Errorf("space advance %v, want positive", g.Advance)
	}
}

func TestNotdefSubstitutedForAnUnknownRune(t *testing.T) {
	a := testFace(t, 16)
	if _, ok := a.Glyph('Ω'); ok {
		t.Fatal("the bitmap face claims to have Ω")
	}

	lines := Layout(a, "Ω", 0, nil)
	if len(lines) != 1 || len(lines[0].Quads) != 1 {
		t.Fatalf("got %d lines / %d quads, want 1 and 1", len(lines), len(lines[0].Quads))
	}
	nd := a.Notdef()
	if q := lines[0].Quads[0]; q.U0 != nd.U0 || q.V0 != nd.V0 {
		t.Errorf("unknown rune drew UVs %v,%v, want notdef's %v,%v", q.U0, q.V0, nd.U0, nd.V0)
	}
}

func TestMeasureMatchesLayout(t *testing.T) {
	a := testFace(t, 16)
	for _, s := range []string{"", "A", "Hello, world!", "one\ntwo\nthree", "trailing\n"} {
		lines := Layout(a, s, 0, nil)
		w, h := Measure(a, s)

		var want float32
		for _, ln := range lines {
			if ln.Width > want {
				want = ln.Width
			}
		}
		if w != want {
			t.Errorf("%q: Measure width %v, Layout max %v", s, w, want)
		}
		if got := float32(len(lines)) * a.LineHeight; h != got {
			t.Errorf("%q: Measure height %v, %d lines is %v", s, h, len(lines), got)
		}
	}
}

func TestNewlineStartsALineEvenAtTheEnd(t *testing.T) {
	a := testFace(t, 16)
	if n := len(Layout(a, "a\nb", 0, nil)); n != 2 {
		t.Errorf(`"a\nb" is %d lines, want 2`, n)
	}
	// A trailing newline opens a line the caret can sit on. Dropping it would
	// put the caret at the end of the previous line instead.
	if n := len(Layout(a, "a\n", 0, nil)); n != 2 {
		t.Errorf(`"a\n" is %d lines, want 2`, n)
	}
}

func TestWrapBreaksAtSpaces(t *testing.T) {
	a := testFace(t, 16)
	limit, _ := Measure(a, "aaa bbb")

	lines := Layout(a, "aaa bbb ccc", limit, nil)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if got := "aaa bbb"; text(lines[0], "aaa bbb ccc") != got {
		t.Errorf("line 0 = %q, want %q", text(lines[0], "aaa bbb ccc"), got)
	}
	if got := "ccc"; text(lines[1], "aaa bbb ccc") != got {
		t.Errorf("line 1 = %q, want %q", text(lines[1], "aaa bbb ccc"), got)
	}
	// The space that caused the break belongs to neither line, so it must not
	// show up in the measured width.
	if lines[0].Width != limit {
		t.Errorf("line 0 width %v, want %v — a trailing space leaked in", lines[0].Width, limit)
	}
}

func TestWrapBreaksAWordTooLongToFit(t *testing.T) {
	a := testFace(t, 16)
	limit, _ := Measure(a, "aaa")

	lines := Layout(a, "aaaaaaaaaa", limit, nil)
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4 (3+3+3+1)", len(lines))
	}
	for i, want := range []string{"aaa", "aaa", "aaa", "a"} {
		if got := text(lines[i], "aaaaaaaaaa"); got != want {
			t.Errorf("line %d = %q, want %q", i, got, want)
		}
	}
}

func TestWrapNeverExceedsTheLimit(t *testing.T) {
	a := testFace(t, 16)
	const s = "The quick brown fox jumps over the lazy dog"
	for _, limit := range []float32{20, 40, 80, 160, 320} {
		for _, ln := range Layout(a, s, limit, nil) {
			// A single glyph wider than the limit is the one case that has to
			// overflow; anything else is a wrapping bug.
			if ln.Width > limit && ln.End-ln.Start > 1 {
				t.Errorf("limit %v: line %q is %v wide", limit, s[ln.Start:ln.End], ln.Width)
			}
		}
	}
}

func TestCaretRoundTrips(t *testing.T) {
	a := testFace(t, 16)
	const s = "Health: 72/100"
	for i := 0; i <= len(s); i++ {
		if i < len(s) && !utf8.RuneStart(s[i]) {
			continue
		}
		if got := IndexAt(a, s, CaretX(a, s, i)); got != i {
			t.Errorf("index %d -> x %v -> index %d", i, CaretX(a, s, i), got)
		}
	}
}

func TestCaretXIsMonotonic(t *testing.T) {
	a := testFace(t, 16)
	const s = "abcdef"
	prev := float32(-1)
	for i := 0; i <= len(s); i++ {
		x := CaretX(a, s, i)
		if x <= prev {
			t.Fatalf("caret went backwards at %d: %v then %v", i, prev, x)
		}
		prev = x
	}
}

func TestAtlasGrowsRatherThanDropsGlyphs(t *testing.T) {
	// Big enough that printable ASCII cannot fit on the first page.
	a := testFace(t, 96)

	if a.W == firstPage {
		t.Fatalf("page is still %d — the test no longer forces a grow", firstPage)
	}
	if a.W > maxPage {
		t.Fatalf("page grew to %d, past the %d cap", a.W, maxPage)
	}
	for r := rune(0x21); r <= 0x7E; r++ {
		g, ok := a.Glyph(r)
		if !ok {
			t.Fatalf("%q was lost in the grow", r)
		}
		if g.U1 <= g.U0 || g.V1 <= g.V0 {
			t.Fatalf("%q has an empty UV rect after the grow", r)
		}
		if g.U1 > 1 || g.V1 > 1 {
			t.Fatalf("%q has UVs off the page: %v %v", r, g.U1, g.V1)
		}
	}
}

// TestGrowRevOutrunsUploadedRev is the contract internal/uidraw relies on: a
// page that has been rebuilt must look new to the uploader, or every glyph
// samples a region the GPU still has the old contents of.
func TestGrowRevOutrunsUploadedRev(t *testing.T) {
	a := testFace(t, 16)
	a.UploadedRev = a.Rev

	before, _ := a.Glyph('A')
	if !a.grow() {
		t.Fatal("grow refused")
	}
	after, _ := a.Glyph('A')

	if after.Rev <= a.UploadedRev {
		t.Errorf("A is rev %d after the grow, uploader is at %d", after.Rev, a.UploadedRev)
	}
	if after.U0 == before.U0 && after.V0 == before.V0 {
		t.Error("A kept its UVs across a grow — the page did not actually change size")
	}
}

func TestMissingFontFallsBackAndWarnsOnce(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	var warns []string
	prev := LogMissing
	LogMissing = func(name string, err error) { warns = append(warns, name) }
	t.Cleanup(func() { LogMissing = prev })

	for _, px := range []float32{12, 16, 24} {
		a := Face("definitely-not-a-font", px)
		if a == nil {
			t.Fatal("Face returned nil — it must never fail")
		}
		if a.face != nil {
			t.Error("a missing font produced a real face")
		}
		if _, ok := a.Glyph('A'); !ok {
			t.Error("the fallback face cannot draw an A")
		}
	}

	// Once per name, not once per size and certainly not once per frame.
	if len(warns) != 1 {
		t.Errorf("warned %d times %v, want once", len(warns), warns)
	}
}

func TestFaceCachesBySizeAndRoundsToWholePixels(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	a := Face("", 16)
	if b := Face("", 16); b != a {
		t.Error("same font and size gave two atlases")
	}
	// A size that fell out of a layout calculation must not bake a new page.
	if b := Face("", 16.2); b != a {
		t.Error("16.2 did not round onto 16 — every frame would bake a fresh page")
	}
	if b := Face("", 24); b == a {
		t.Error("a different size shared an atlas")
	}
	if b := Face("", 1); b != Face("", MinSize) {
		t.Error("a tiny size did not clamp to MinSize")
	}
}

func TestFacesIsSortedAndComplete(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	Face("b", 16)
	Face("a", 24)
	Face("a", 12)

	got := Faces()
	if len(got) != 3 {
		t.Fatalf("%d faces, want 3", len(got))
	}
	want := []struct {
		font string
		px   float32
	}{{"a", 12}, {"a", 24}, {"b", 16}}
	for i, w := range want {
		if got[i].Font != w.font || got[i].Size != w.px {
			t.Errorf("face %d is %s@%v, want %s@%v", i, got[i].Font, got[i].Size, w.font, w.px)
		}
	}
}

func TestInvalidateFontDropsEverySize(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	a := Face("x", 16)
	Face("x", 24)
	Face("y", 16)

	InvalidateFont("x")
	if len(Faces()) != 1 {
		t.Errorf("%d faces left, want only y's", len(Faces()))
	}
	if b := Face("x", 16); b == a {
		t.Error("x came back from the cache after being invalidated")
	}
}

func TestPageIsTransparentWhite(t *testing.T) {
	// Filtering between an opaque white texel and a transparent *black* one
	// gives a grey fringe around every glyph. This is the only thing stopping
	// that, and it is invisible until someone looks closely at 4K.
	a := testFace(t, 16)
	for i := 0; i < len(a.Pix.Pix); i += 4 {
		if a.Pix.Pix[i] != 255 || a.Pix.Pix[i+1] != 255 || a.Pix.Pix[i+2] != 255 {
			t.Fatalf("texel %d is not white: %v", i/4, a.Pix.Pix[i:i+4])
		}
	}
}

func TestLayoutReusesItsBuffers(t *testing.T) {
	a := testFace(t, 16)
	out := Layout(a, "one\ntwo\nthree", 0, nil)
	capsBefore := []int{cap(out[0].Quads), cap(out[1].Quads), cap(out[2].Quads)}

	out = Layout(a, "abc\nde\nf", 0, out)
	for i, c := range capsBefore {
		if cap(out[i].Quads) != c {
			t.Errorf("line %d reallocated its quads (%d -> %d)", i, c, cap(out[i].Quads))
		}
	}
}

func TestOpentypeFaceMeasuresText(t *testing.T) {
	a := ttfFace(t, 24)
	if a.face == nil {
		t.Fatal("a checked-in font did not produce a face")
	}
	if a.Ascent <= 0 || a.LineHeight <= a.Ascent {
		t.Errorf("implausible metrics: ascent %v, line height %v", a.Ascent, a.LineHeight)
	}
	w, h := Measure(a, "Hamburgefonstiv")
	if w <= 0 || h <= 0 {
		t.Errorf("measured %vx%v", w, h)
	}
	if wide, _ := Measure(a, "Hamburgefonstivx"); wide <= w {
		t.Error("adding a character did not make the string wider")
	}
}

// text is the source slice one laid-out line covers.
func text(ln Line, src string) string { return src[ln.Start:ln.End] }
