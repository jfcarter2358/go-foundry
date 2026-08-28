package uifont

// uifont_layout.go — turning a string into positioned glyph quads.
//
// One implementation of the line-breaking rules, used by both Layout and
// Measure. Measure could be a faster special case that never builds a quad, but
// two implementations of "where does this line break" would drift, and a HUD
// whose measured height disagrees with its drawn height by one line is a bug
// nobody can see until it clips.

import (
	"unicode/utf8"
)

// Quad is one glyph in text-local pixels, with the origin at the left end of
// its own line's baseline and Y increasing downwards. Y0 is therefore negative
// for anything above the baseline.
type Quad struct {
	X0, Y0, X1, Y1 float32
	U0, V0, U1, V1 float32
}

// Line is one laid-out line.
//
// Start and End are byte offsets into the source string, which is what lets a
// text field map a click back to a caret position and what lets a wrapped
// label highlight a range.
type Line struct {
	Quads      []Quad
	Width      float32
	Start, End int
}

// Layout breaks s into lines and positions every glyph. maxW <= 0 disables
// wrapping, leaving only explicit newlines.
//
// out is reused: pass back the previous result and a steady UI allocates
// nothing per frame, because each Line keeps its own quad buffer.
//
// Every line's quads are relative to *that line's* baseline-left origin, not to
// the block's. The caller offsets line n by n*LineHeight. Keeping it that way
// makes horizontal alignment a per-line translation of Width, which is exactly
// what alignment is.
func Layout(a *Atlas, s string, maxW float32, out []Line) []Line {
	n, i := 0, 0
	for {
		if n == len(out) {
			out = append(out, Line{})
		}
		ln := &out[n]
		ln.Quads = ln.Quads[:0]
		resume, done := a.layLine(s, i, maxW, ln, true)
		n++
		if done {
			break
		}
		i = resume
	}
	return out[:n]
}

// Measure is the size of s with no wrapping, honouring explicit newlines.
func Measure(a *Atlas, s string) (w, h float32) {
	w, h, _ = MeasureWrapped(a, s, 0)
	return w, h
}

// MeasureWrapped is the size of s wrapped to maxW, and the line count. It
// allocates nothing: the same line-breaking walk runs with quad emission off.
func MeasureWrapped(a *Atlas, s string, maxW float32) (w, h float32, lines int) {
	var ln Line
	for i := 0; ; {
		resume, done := a.layLine(s, i, maxW, &ln, false)
		lines++
		if ln.Width > w {
			w = ln.Width
		}
		if done {
			break
		}
		i = resume
	}
	return w, float32(lines) * a.LineHeight, lines
}

// layLine lays one line starting at byte offset from, filling ln. It reports
// where the next line starts and whether the string is exhausted.
//
// Wrapping backtracks: on overflow it returns the last break opportunity and
// the caller re-lays from there. Re-laying a word is cheaper than carrying a
// speculative second buffer, and it keeps the quad positions on a line derived
// from one left-to-right pass rather than patched up after the fact.
func (a *Atlas) layLine(s string, from int, maxW float32, ln *Line, emit bool) (resume int, done bool) {
	ln.Quads = ln.Quads[:0]
	ln.Start, ln.End, ln.Width = from, from, 0

	pen := float32(0)
	prev := rune(-1)

	// The last place this line could be cut, if it turns out to be too long.
	haveBreak := false
	brkEnd, brkResume, brkQuads := from, from, 0
	brkWidth := float32(0)

	i := from
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])

		if r == '\n' {
			ln.End, ln.Width = i, pen
			return i + size, false
		}
		if r == '\r' {
			i += size
			continue
		}

		// A space is the break itself, so the breakpoint is recorded before it
		// is laid down: the line ends where the space begins, and the next one
		// starts after it. That is what keeps a trailing space out of both the
		// measured width and the visible line.
		if r == ' ' || r == '\t' {
			haveBreak = true
			brkEnd, brkResume, brkQuads, brkWidth = i, i+size, len(ln.Quads), pen
		}

		g, adv := a.advanceOf(r, prev)

		if maxW > 0 && pen+adv > maxW && len(ln.Quads) > 0 {
			if haveBreak && brkEnd > from {
				ln.Quads = ln.Quads[:brkQuads]
				ln.End, ln.Width = brkEnd, brkWidth
				return brkResume, false
			}
			// One word longer than the whole line. Breaking mid-word is ugly;
			// letting it run off the edge is worse, because the text that
			// overflows is invisible rather than merely awkward.
			ln.End, ln.Width = i, pen
			return i, false
		}

		if emit && g.W > 0 && g.H > 0 {
			x := pen + adv - g.Advance // the pen after kerning, before the advance
			ln.Quads = append(ln.Quads, Quad{
				X0: x + g.BearingX, Y0: g.BearingY,
				X1: x + g.BearingX + g.W, Y1: g.BearingY + g.H,
				U0: g.U0, V0: g.V0, U1: g.U1, V1: g.V1,
			})
		}
		pen += adv
		prev = r
		i += size

		// A hyphen stays on the line it ends, so its breakpoint is recorded
		// after it rather than before.
		if breakAfter(r) {
			haveBreak = true
			brkEnd, brkResume, brkQuads, brkWidth = i, i, len(ln.Quads), pen
		}
	}

	ln.End, ln.Width = len(s), pen
	return len(s), true
}

// advanceOf resolves a rune to its glyph, substituting the notdef box for
// anything the face lacks, and returns the pen movement including kerning.
//
// It also substitutes for a glyph baked *since* the page was last uploaded.
// Its atlas coordinates point at a region the GPU has not been given yet, so
// drawing it would sample whatever was there before — usually another glyph, or
// nothing. One frame of a box is the honest answer, and the next frame has it.
//
// Only when something has actually taken responsibility for uploading: an
// UploadedRev of zero means nobody is, which is the headless case, and there
// every glyph is drawable because none of them are going anywhere near a GPU.
func (a *Atlas) advanceOf(r, prev rune) (Glyph, float32) {
	g, ok := a.Glyph(r)
	if !ok || (a.UploadedRev != 0 && g.Rev > a.UploadedRev) {
		g = a.Notdef()
	}
	adv := g.Advance
	if prev >= 0 {
		adv += a.Kern(prev, r)
	}
	return g, adv
}

// breakAfter reports whether a line may be cut immediately after this rune,
// with the rune itself staying on the earlier line.
//
// The CJK ranges are here because those scripts do not separate words with
// spaces at all: without them a paragraph of Japanese is one unbreakable "word"
// and wraps by falling off the edge.
func breakAfter(r rune) bool {
	switch {
	case r == '-' || r == '/':
		return true
	case r >= 0x3040 && r <= 0x30FF: // kana
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK unified ideographs
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // hangul syllables
		return true
	}
	return false
}

// CaretX is the pen position after the first index bytes of a single line.
//
// Single line: a text field is one, and a caret in a wrapped paragraph needs a
// line index as well as a byte offset, which is a different question with a
// different answer.
func CaretX(a *Atlas, s string, index int) float32 {
	if index > len(s) {
		index = len(s)
	}
	pen := float32(0)
	prev := rune(-1)
	for i := 0; i < index; {
		r, size := utf8.DecodeRuneInString(s[i:])
		_, adv := a.advanceOf(r, prev)
		pen += adv
		prev = r
		i += size
	}
	return pen
}

// IndexAt is CaretX's inverse: the byte offset whose caret sits nearest x.
//
// Nearest rather than "the glyph containing x", because a caret belongs between
// characters — clicking the right half of a letter should put it after that
// letter, which is what a hit test on the glyph body gets wrong.
func IndexAt(a *Atlas, s string, x float32) int {
	best, bestDist := 0, absF(x)
	pen := float32(0)
	prev := rune(-1)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		_, adv := a.advanceOf(r, prev)
		pen += adv
		prev = r
		i += size
		if d := absF(x - pen); d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

func absF(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
