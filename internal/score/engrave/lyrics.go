package engrave

// lyrics.go — the words under a staff.
//
// Each verse is a line of text under the staff, all of a system's at one
// baseline, below the lowest note, articulation and slur of the system
// (Gould: lyrics in a straight line, clear of everything), the next verse
// under it. A syllable is centred under its notehead; one that starts a
// melisma (score.Lyric.Extend) is set left, at the head's left edge, and
// an extender line runs from it at the baseline to the end of the last
// note it is sung over. Between the syllables of a word stand hyphens:
// one centred in the gap, more along a long one. A hyphen or extender
// that runs off the layout's end (a system break) goes to the end, and
// one that runs in from before the layout starts at its start.
//
// Spacing makes the room (lyricRods): two syllables of a verse are kept a
// word space apart, more when a hyphen stands between them, across
// barlines too. A second voice's words are a line of their own under the
// first's.

import (
	"math"

	"foundry/internal/score"
)

const (
	lyricSize   = 1.7  // a syllable's em, in spaces
	lyricLine   = 2.3  // baseline to baseline, verse to verse
	lyricClear  = 1.1  // the lowest ink to the first verse's tops
	lyricDrop   = 2.9  // the bottom line to the first verse's baseline, at least
	lyricAscent = 1.25 // a syllable's height over its baseline
	wordSpace   = 0.6  // between two syllables of different words
	hyphenRoom  = 1.4  // between two syllables a hyphen joins
	hyphenW     = 0.55 // a hyphen's length
	hyphenEvery = 5.0  // a long gap has a hyphen at least this often
)

// lyricRow is one line of words under a staff: a voice's verse.
type lyricRow struct {
	voice, verse int
}

// lyricRows are a staff's lines of words, verse by verse, a voice's after
// the voice before's at each verse.
func lyricRows(sc *staffCtx) []lyricRow {
	if sc.tab != nil && !sc.tabOnly {
		return nil // the words go under the notation
	}
	var rows []lyricRow
	for verse := 1; verse <= score.MaxVerses; verse++ {
		for vi, v := range sc.st.Voices {
			if voiceHasVerse(v, verse) {
				rows = append(rows, lyricRow{vi, verse})
			}
		}
	}
	return rows
}

func voiceHasVerse(v *score.Voice, verse int) bool {
	name := score.LyricMark(verse)
	for _, m := range v.Measures {
		for i := range m.Events {
			for _, mk := range m.Events[i].Marks {
				if mk.Name == name && mk.Value != "" {
					return true
				}
			}
		}
	}
	return false
}

// syllableSpan is where a syllable sets relative to its event's x: its
// left edge, given its width.
func syllableLeft(x *ev, l score.Lyric, w float64) float64 {
	if l.Extend {
		return -0.1
	}
	return x.headW/2 - w/2
}

// lyricKey is one staff's line of words, for spacing across barlines.
type lyricKey struct {
	sc  *staffCtx
	row lyricRow
}

// lyricEdge is a bar's first or last syllable of a line of words: its
// column, how far its text reaches past the column (left of it for the
// first, right for the last), and for the last whether a hyphen follows.
type lyricEdge struct {
	col    int
	reach  float64
	hyphen bool
}

// lyricRods keeps a line's syllables apart in a bar, and notes its first
// and last for the rods across the barlines (crossBarRods).
func (e *engraver) lyricRods(bi int, bc *barCtx) {
	bc.lyrHead, bc.lyrTail = map[lyricKey]lyricEdge{}, map[lyricKey]lyricEdge{}
	for _, sc := range e.staves {
		for _, row := range lyricRows(sc) {
			if row.voice >= len(sc.bars[bi].voices) {
				continue
			}
			type syl struct {
				col    int
				l, r   float64
				hyphen bool
			}
			var syls []syl
			for _, x := range sc.bars[bi].voices[row.voice] {
				if x.grace || x.rest || x.space || x.col == nil {
					continue
				}
				ly, ok := x.e.Lyric(row.verse)
				if !ok {
					continue
				}
				w := TextWidth(ly.Text, lyricSize)
				left := x.shift + syllableLeft(x, ly, w)
				for k, c := range bc.cols {
					if c == x.col {
						syls = append(syls, syl{k, -left, left + w, ly.Hyphen})
					}
				}
			}
			for i := 1; i < len(syls); i++ {
				a, b := syls[i-1], syls[i]
				if b.col <= a.col {
					continue
				}
				gap := wordSpace
				if a.hyphen {
					gap = hyphenRoom
				}
				need := a.r + b.l + gap
				for k := a.col; k < b.col-1; k++ {
					need -= bc.rods[k]
				}
				bc.rods[b.col-1] = math.Max(bc.rods[b.col-1], need)
			}
			if len(syls) > 0 {
				key := lyricKey{sc, row}
				f, l := syls[0], syls[len(syls)-1]
				bc.lyrHead[key] = lyricEdge{col: f.col, reach: f.l}
				bc.lyrTail[key] = lyricEdge{col: l.col, reach: l.r, hyphen: l.hyphen}
			}
		}
	}
}

// lyricCrossNeed is how much more room bar a's last gap needs so its last
// syllable on each line clears bar b's first: fixed is the barline, b's
// head and its lead.
func (e *engraver) lyricCrossNeed(a, b *barCtx, fixed float64) float64 {
	need := 0.0
	last := len(a.rods) - 1
	for key, t := range a.lyrTail {
		h, ok := b.lyrHead[key]
		if !ok {
			continue
		}
		gap := wordSpace
		if t.hyphen {
			gap = hyphenRoom
		}
		n := t.reach + h.reach + gap - fixed
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

// drawLyrics draws a staff's words: each line's syllables, hyphens and
// extenders.
func (e *engraver) drawLyrics(sc *staffCtx) {
	rows := lyricRows(sc)
	if len(rows) == 0 {
		return
	}
	endX := e.layoutEndX()
	low := sc.sky.botIn(-8, endX+8, sc.height())
	base := math.Max(sc.height()+lyricDrop, low+lyricClear+lyricAscent)
	thick := 0.12
	for ri, row := range rows {
		y := base + float64(ri)*lyricLine
		ls := score.VoiceLyrics(sc.st.Voices[row.voice], row.verse)
		type placed struct {
			x0, x1 float64
			in     bool
		}
		at := make([]placed, len(ls))
		for i, la := range ls {
			x := e.evAt(sc, row.voice, evKey{la.Bar, la.Event})
			if x == nil || x.col == nil {
				continue
			}
			w := TextWidth(la.Text, lyricSize)
			x0 := x.x + syllableLeft(x, la.Lyric, w)
			ref := x.ref
			e.put(sc, x, Item{Kind: ItemText, Role: RoleLyric, Ref: ref, P: Point{X: x0, Y: y}, Text: la.Text, Font: TextPlain, Size: lyricSize})
			at[i] = placed{x0, x0 + w, true}
		}
		before := func(bar int) bool { return bar < e.from }
		after := func(bar int) bool { return bar > e.to }
		start, end := e.layoutStart(), e.layoutEnd()
		for i, la := range ls {
			// The hyphen to the word's next syllable.
			if la.Hyphen && i+1 < len(ls) {
				nx := ls[i+1]
				var x0, x1 float64
				switch {
				case at[i].in:
					x0 = at[i].x1
				case before(la.Bar) && !before(nx.Bar):
					x0 = start
				default:
					continue
				}
				switch {
				case at[i+1].in:
					x1 = at[i+1].x0
				case after(nx.Bar):
					x1 = end
				default:
					continue
				}
				e.hyphens(sc, la, x0, x1, y, thick)
			}
			// The extender over the notes the syllable is held over.
			if la.Extend && (la.UntilBar != la.Bar || la.Until != la.Event) {
				var x0, x1 float64
				switch {
				case at[i].in:
					x0 = at[i].x1 + 0.15
				case before(la.Bar) && !before(la.UntilBar):
					x0 = start
				default:
					continue
				}
				if z := e.evAt(sc, row.voice, evKey{la.UntilBar, la.Until}); z != nil && z.col != nil {
					x1 = z.x + z.headW
				} else if after(la.UntilBar) {
					x1 = end
				} else {
					continue
				}
				if x1-x0 > 0.4 {
					ref := Ref{Line: sc.line, Staff: sc.staff, Voice: row.voice, Bar: la.Bar, Event: la.Event, Note: -1}
					e.put(sc, nil, Item{Kind: ItemLine, Role: RoleLine, Ref: ref, P: Point{X: x0, Y: y + 0.08}, Q: Point{X: x1, Y: y + 0.08}, Width: 0.1})
				}
			}
		}
	}
}

// hyphens fills the gap between two syllables of a word with a hyphen in
// its middle, or several evenly along a long one.
func (e *engraver) hyphens(sc *staffCtx, la score.LyricAt, x0, x1, y, thick float64) {
	gap := x1 - x0
	if gap < hyphenW*0.8 {
		return
	}
	n := max(1, int(gap/hyphenEvery))
	w := math.Min(hyphenW, gap*0.6)
	ref := Ref{Line: sc.line, Staff: sc.staff, Voice: -1, Bar: la.Bar, Event: la.Event, Note: -1}
	hy := y - 0.42*lyricSize*0.5
	for k := 0; k < n; k++ {
		cx := x0 + gap*(float64(k)+0.5)/float64(n)
		e.put(sc, nil, Item{Kind: ItemLine, Role: RoleLine, Ref: ref, P: Point{X: cx - w/2, Y: hy}, Q: Point{X: cx + w/2, Y: hy}, Width: thick})
	}
}
