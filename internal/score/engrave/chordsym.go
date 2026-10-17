package engrave

// chordsym.go — chord symbols over a line's top staff.
//
// A symbol stands at its written position, its root's left edge just
// before the notehead there, all of a system's at one baseline over the
// highest ink of the staff (a lead sheet's changes read along one line).
// Its position is a column of the bar even where no note starts (a change
// in the middle of a held note): spacing gives it a column of its own
// (columns), and keeps two symbols apart by their widths (chordRods).
//
// A symbol prints as a lead sheet spells it (score.Chord.Printed): the
// root letter large, its flat or sharp the chord-symbol glyph beside it,
// the rest smaller — m, maj7, 7(♭9,♯11), sus4 — with SMuFL's ° and ø for
// diminished and half-diminished, and a slash bass at the root's size. On
// a transposing staff shown at written pitch the symbol is transposed with
// it, as a part's is.

import (
	"math"
	"strings"

	"foundry/internal/score"
)

const (
	chordSize    = 2.0  // the root's em, in spaces
	chordSufSize = 1.5  // the rest's
	chordAccSize = 0.42 // the root's flat or sharp (csym glyphs are three spaces tall at 1)
	chordSufAcc  = 0.32 // a flat or sharp in the rest
	chordGap     = 0.9  // between two symbols
	chordLift    = 0.7  // the highest ink to the baseline, at least
	chordLow     = -2.2 // the baseline is at least this far over the top line
)

// chordRun is one piece of a printed chord symbol: a run of text or a
// glyph, its size, how far it is raised from the baseline, and its width.
type chordRun struct {
	text, glyph string
	size, dy, w float64
}

// csymAccidental is the chord-symbol glyph for an alteration.
func csymAccidental(alter int) string {
	switch alter {
	case -2:
		return "csymAccidentalDoubleFlat"
	case -1:
		return "csymAccidentalFlat"
	case 1:
		return "csymAccidentalSharp"
	case 2:
		return "csymAccidentalDoubleSharp"
	}
	return ""
}

// chordRuns sets a chord symbol out as pieces left to right.
func (e *engraver) chordRuns(c score.Chord) []chordRun {
	var out []chordRun
	text := func(s string, size, dy float64) {
		if s != "" {
			out = append(out, chordRun{text: s, size: size, dy: dy, w: TextWidth(s, size)})
		}
	}
	glyph := func(g string, size, dy float64) {
		if g != "" {
			out = append(out, chordRun{glyph: g, size: size, dy: dy, w: e.f.Width(g)*size + 0.12})
		}
	}
	root, ra, rest, bass, ba := c.Printed()
	text(root, chordSize, 0)
	glyph(csymAccidental(ra), chordAccSize, -0.3)
	// The rest: text, with its signs as glyphs.
	var b strings.Builder
	flush := func() { text(b.String(), chordSufSize, 0); b.Reset() }
	for _, r := range rest {
		switch r {
		case '♭':
			flush()
			glyph("csymAccidentalFlat", chordSufAcc, -0.15)
		case '♯':
			flush()
			glyph("csymAccidentalSharp", chordSufAcc, -0.15)
		case '°':
			flush()
			glyph("csymDiminished", 0.42, -0.55)
		case 'ø':
			flush()
			glyph("csymHalfDiminished", 0.42, -0.55)
		default:
			b.WriteRune(r)
		}
	}
	flush()
	if bass != "" {
		text("/", chordSize, 0)
		text(bass, chordSize, 0)
		glyph(csymAccidental(ba), chordAccSize, -0.3)
	}
	return out
}

// chordWidth is a printed chord symbol's width.
func (e *engraver) chordWidth(c score.Chord) float64 {
	w := 0.0
	for _, r := range e.chordRuns(c) {
		w += r.w
	}
	return w
}

// shownChord is a chord symbol as a staff shows it: transposed with a
// transposing staff at written pitch.
func (sc *staffCtx) shownChord(c score.Chord) score.Chord {
	if sc.transposed {
		return c.Transpose(sc.iv)
	}
	return c
}

// chordStaff reports the staff a line's chord symbols stand over: its
// first in the layout.
func chordStaff(sc *staffCtx) bool { return sc.first }

// chordRods keeps a line's chord symbols in a bar apart, and notes the
// bar's first and last for the rods across its barlines.
func (e *engraver) chordRods(bi int, bc *barCtx) {
	bc.chdHead, bc.chdTail = map[*staffCtx]lyricEdge{}, map[*staffCtx]lyricEdge{}
	for _, sc := range e.staves {
		if !chordStaff(sc) {
			continue
		}
		type sym struct {
			col  int
			l, r float64
		}
		var syms []sym
		for _, ch := range e.s.Lines[sc.line].Chords {
			if ch.At.Bar != bc.bar {
				continue
			}
			for k, c := range bc.cols {
				if c.off.Eq(ch.At.Offset) && !c.end {
					syms = append(syms, sym{k, 0.1, e.chordWidth(sc.shownChord(ch.Chord)) - 0.1})
				}
			}
		}
		for i := 1; i < len(syms); i++ {
			a, b := syms[i-1], syms[i]
			if b.col <= a.col {
				continue
			}
			need := a.r + b.l + chordGap
			for k := a.col; k < b.col-1; k++ {
				need -= bc.rods[k]
			}
			bc.rods[b.col-1] = math.Max(bc.rods[b.col-1], need)
		}
		if len(syms) > 0 {
			f, l := syms[0], syms[len(syms)-1]
			bc.chdHead[sc] = lyricEdge{col: f.col, reach: f.l}
			bc.chdTail[sc] = lyricEdge{col: l.col, reach: l.r}
		}
	}
}

// chordCrossNeed is how much more room bar a's last gap needs so its last
// chord symbol clears bar b's first.
func (e *engraver) chordCrossNeed(a, b *barCtx, fixed float64) float64 {
	need := 0.0
	last := len(a.rods) - 1
	for sc, t := range a.chdTail {
		h, ok := b.chdHead[sc]
		if !ok {
			continue
		}
		n := t.reach + h.reach + chordGap - fixed
		for k := t.col; k < last; k++ {
			n -= a.rods[k]
		}
		for k := 0; k < h.col && k < len(b.rods); k++ {
			n -= b.rods[k]
		}
		need = math.Max(need, n)
	}
	return need
}

// chordOffsets are the onsets a bar's chord symbols need columns at, over
// the lines in the layout.
func (e *engraver) chordOffsets(bar int) []score.Frac {
	var out []score.Frac
	for _, sc := range e.staves {
		if !chordStaff(sc) {
			continue
		}
		for _, ch := range e.s.Lines[sc.line].Chords {
			if ch.At.Bar == bar {
				out = append(out, ch.At.Offset)
			}
		}
	}
	return out
}

// drawChordSymbols draws a line's chord symbols over its top staff.
func (e *engraver) drawChordSymbols(sc *staffCtx) {
	chords := e.s.Lines[sc.line].Chords
	if len(chords) == 0 {
		return
	}
	endX := e.layoutEndX()
	base := math.Min(chordLow, sc.sky.topIn(-8, endX+8, 0)-chordLift)
	for i, ch := range chords {
		if ch.At.Bar < e.from || ch.At.Bar > e.to {
			continue
		}
		x := e.xAtPos(ch.At) - 0.1
		ref := Ref{Line: sc.line, Staff: -1, Voice: -1, Bar: ch.At.Bar, Event: i, Note: -1}
		var box Rect
		for _, r := range e.chordRuns(sc.shownChord(ch.Chord)) {
			var it Item
			if r.glyph != "" {
				it = e.glyphItem(RoleChordSymbol, ref, r.glyph, x, base+r.dy, r.size)
			} else {
				it = Item{Kind: ItemText, Role: RoleChordSymbol, Ref: ref, P: Point{X: x, Y: base + r.dy}, Text: r.text, Font: TextPlain, Size: r.size}
			}
			box = box.Union(e.put(sc, nil, it))
			x += r.w
		}
		sc.chords = append(sc.chords, ChordBox{Line: sc.line, Index: i, Bar: ch.At.Bar, Box: box})
	}
}
