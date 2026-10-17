package engrave

// spacing.go — columns and horizontal spacing.
//
// A bar's columns are its onsets: every time at which an event starts in
// any staff, so a quarter in the flute and a quarter in the cello line up,
// and a triplet against duplets interleaves. Each column knows how much ink
// sits left of a normal notehead's left edge there (accidentals, grace
// notes, a head displaced by a second, a voice's offset) and right of it
// (heads, dots, flags), on its widest staff.
//
// The room between two columns is the larger of what the time between
// them asks for and what the ink needs. Time asks logarithmically from an
// eighth (ideal): a half note is not twice a quarter's width, as Behind
// Bars has it, and the same value is as wide in every bar of the galley.
// The ink rod is the left column's right ink, the right column's left ink
// and noteGap between: accidentals and dots never collide, however tight.
// Each gap keeps both: its spring (the time's room) and its rod (the
// ink's). A page layout stretches every spring of a system by one factor
// to fill its measure, never below the rods (stretch, justify).

import (
	"math"
	"sort"

	"foundry/internal/score"
)

const (
	spaceRef      = 1.0 / 8 // the value spacing is reckoned from
	spaceShortest = 2.7     // an eighth's room
	spaceShort    = 0.45    // how values shorter than an eighth shrink
	spaceLog      = 0.65    // what each doubling of the value adds, as a share of that
	lastGap       = 0.8     // the last note's ink to the closing barline, at least
	// startRepeatGap is the signatures to a repeat barline opening the
	// layout.
	startRepeatGap = 0.6
)

// columns gives every bar its onsets, and every event its column.
func (e *engraver) columns() {
	for bi, bc := range e.bars {
		offs := []score.Frac{{}}
		end := bc.length
		for _, sc := range e.staves {
			for _, v := range sc.bars[bi].voices {
				for _, x := range v {
					if !x.grace && !x.space {
						offs = append(offs, x.onset)
					}
					if t := x.onset.Add(x.actual); end.Less(t) {
						end = t // an overfull bar: room for all of it
					}
				}
			}
		}
		// A chord symbol where nothing starts stands in a column of its own.
		offs = append(offs, e.chordOffsets(bc.bar)...)
		sort.Slice(offs, func(i, j int) bool { return offs[i].Less(offs[j]) })
		for _, o := range offs {
			if !o.Less(end) {
				continue
			}
			if n := len(bc.cols); n > 0 && bc.cols[n-1].off.Eq(o) {
				continue
			}
			bc.cols = append(bc.cols, &column{off: o, offF: o.Float()})
		}
		bc.cols = append(bc.cols, &column{off: end, offF: end.Float(), end: true})
		for _, sc := range e.staves {
			for _, v := range sc.bars[bi].voices {
				for _, x := range v {
					x.col = bc.colFrom(x.onset)
				}
			}
		}
	}
}

// colFrom is the first column at or after a time (a grace note goes with
// the note after it; at the bar's end, with the barline).
func (bc *barCtx) colFrom(t score.Frac) *column {
	i := sort.Search(len(bc.cols), func(i int) bool { return !bc.cols[i].off.Less(t) })
	if i == len(bc.cols) {
		i--
	}
	return bc.cols[i]
}

// measure arranges every column's ink and spaces every bar.
func (e *engraver) measure() {
	e.tailW = e.tailPositions().w
	for bi, bc := range e.bars {
		for _, c := range bc.cols {
			for _, sc := range e.staves {
				sb := sc.bars[bi]
				var evs []*ev
				for _, v := range sb.voices {
					for _, x := range v {
						// A cross-staff event is arranged with the staff it
						// is written on.
						if x.col == c && !x.space && !x.barRest && (x.cross == 0 || sc.tab != nil) {
							evs = append(evs, x)
						}
					}
				}
				var crossed []*ev
				if sc.tab == nil {
					for _, x := range e.crossedInto(sc, bi) {
						if x.col == c {
							crossed = append(crossed, x)
						}
					}
				}
				// Arranged in this staff's own coordinates, then put back.
				for _, x := range crossed {
					for _, n := range x.notes {
						n.y -= x.crossY
					}
				}
				evs = append(evs, crossed...)
				l, r := 0.0, 0.0
				switch {
				case len(evs) > 0 && sc.tab != nil:
					l, r = e.arrangeTab(evs)
				case len(evs) > 0:
					l, r = e.arrangeColumn(sb, c, evs)
				}
				for _, x := range crossed {
					for _, n := range x.notes {
						n.y += x.crossY
					}
				}
				// A clef change stands left of everything else there.
				for _, cm := range sc.clefMarks {
					if cm.col == c {
						cm.inkLeft = l
						l += e.clefChangeWidth(cm)
					}
				}
				c.left, c.right = math.Max(c.left, l), math.Max(c.right, r)
			}
		}
		n := len(bc.cols) - 1
		bc.widths, bc.rods, bc.springs = make([]float64, n), make([]float64, n), make([]float64, n)
		for k := 0; k+1 < len(bc.cols); k++ {
			a, b := bc.cols[k], bc.cols[k+1]
			gap := noteGap
			if b.end {
				gap = lastGap
			}
			bc.springs[k] = e.ideal(b.offF - a.offF)
			bc.rods[k] = a.right + b.left + gap
		}
		e.dynamicRods(bi, bc)
		e.lyricRods(bi, bc)
		e.chordRods(bi, bc)
		if bi > 0 {
			bc.prefixW = e.barPrefixWidth(bc)
		}
		first := bc.cols[0]
		switch {
		case bi == 0 && e.repeatStarts(bc.bar):
			// The layout opens on a repeat: its barline after the
			// signatures, then the music.
			bc.lead = startRepeatGap + e.barlineWidth(barRepeatStart) + math.Max(barGapAfter, first.left+0.6)
		case bi == 0:
			bc.lead = math.Max(prefixGap, first.left+0.8)
		case bc.prefixW > 0:
			bc.lead = math.Max(prefixGap, first.left+0.8)
		default:
			bc.lead = math.Max(barGapAfter, first.left+0.6)
		}
		// A bar rest wants a bar wide enough to sit in the middle of.
		if len(bc.cols) == 2 {
			bc.rods[0] = math.Max(bc.rods[0], 5.5-bc.lead)
		}
		e.multiRestWidths(bc)
	}
	e.crossBarRods()
	e.stretch(1)
}

// stretch sets every gap's width for a stretch factor: each gap is its
// spring (the room its time asks for) times k, but never less than its
// rod (what the ink needs). k = 1 is the galley's natural spacing; a page
// layout finds the k that fills its measure (justify).
func (e *engraver) stretch(k float64) {
	for _, bc := range e.bars {
		for i := range bc.widths {
			bc.widths[i] = math.Max(bc.rods[i], k*bc.springs[i])
		}
	}
}

// ideal is the room a time between onsets (whole notes) asks for: an
// eighth gets spaceShortest, each doubling spaceLog of that more, and
// shorter values shrink by the square-root-ish spaceShort power — so a
// quarter is as wide in a bar of 16ths as in a bar of quarters, and a bar
// of 32nds is held apart by its ink (the rods) rather than by its time.
func (e *engraver) ideal(d float64) float64 {
	if d <= 0 {
		return 0
	}
	r := d / spaceRef
	if r < 1 {
		return e.o.Spacing * spaceShortest * math.Pow(r, spaceShort)
	}
	return e.o.Spacing * spaceShortest * (1 + spaceLog*math.Log2(r))
}

// crossBarRods keeps a bar's last dynamics and the next bar's first apart
// across the barline, widening the first bar's last gap.
func (e *engraver) crossBarRods() {
	for i := 0; i+1 < len(e.bars); i++ {
		a, b := e.bars[i], e.bars[i+1]
		if len(a.widths) == 0 {
			continue
		}
		// From a's last note to b's first: its last gap, then the barline,
		// b's head and its lead (fixed).
		fixed := e.barlineWidth(e.barlineBefore(i+1)) + b.prefixW + b.lead
		need := 0.0
		for sc, tail := range a.dynTail {
			if head, ok := b.dynHead[sc]; ok {
				need = math.Max(need, tail-head+0.5)
			}
		}
		// Only the last gap stretches: it takes up what the rest lacks.
		last := len(a.rods) - 1
		a.rods[last] = math.Max(a.rods[last], need-fixed)
		a.rods[last] = math.Max(a.rods[last], math.Max(e.lyricCrossNeed(a, b, fixed), e.chordCrossNeed(a, b, fixed)))
	}
}

// dynamicRods keeps neighbouring columns' dynamics (and expression text) on
// a staff from touching, with room for a hairpin between two dynamics.
func (e *engraver) dynamicRods(bi int, bc *barCtx) {
	bc.dynHead, bc.dynTail = map[*staffCtx]float64{}, map[*staffCtx]float64{}
	type ext struct {
		l, r         float64
		has, hairpin bool
	}
	for _, sc := range e.staves {
		exts := make([]ext, len(bc.cols))
		for _, v := range sc.bars[bi].voices {
			for _, x := range v {
				if x.grace || x.space || x.col == nil {
					continue
				}
				l, r, has, hp := e.dynExtent(x)
				if !has && !hp {
					continue
				}
				for k, c := range bc.cols {
					if c != x.col {
						continue
					}
					x0 := exts[k]
					l, r = l+x.shift, r+x.shift
					if x0.has {
						l, r = math.Min(l, x0.l), math.Max(r, x0.r)
					}
					exts[k] = ext{l, r, has || x0.has, hp || x0.hairpin}
				}
			}
		}
		// The bar's first and last dynamics, for the rod across its
		// barlines (crossBarRods).
		if n := len(bc.cols); n >= 2 {
			if exts[0].has {
				bc.dynHead[sc] = exts[0].l
			}
			if t := exts[n-2]; t.has || t.hairpin {
				r := t.r
				if !t.has {
					r = 0
				}
				if t.hairpin {
					r += 2.2
				}
				bc.dynTail[sc] = r
			}
		}
		for k := 0; k+1 < len(bc.cols) && k < len(bc.widths); k++ {
			a, b := exts[k], exts[k+1]
			if !b.has {
				continue
			}
			ar := a.r
			if !a.has {
				ar = 0
			}
			need := ar - b.l + 0.5
			if a.hairpin {
				need += 2.2
			}
			if a.has || a.hairpin {
				bc.rods[k] = math.Max(bc.rods[k], need)
			}
		}
	}
}
