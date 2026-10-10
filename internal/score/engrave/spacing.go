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
// A page layout (phase 5) will stretch each system's springs to its
// measure on top of this.

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
	for bi, bc := range e.bars {
		for _, c := range bc.cols {
			for _, sc := range e.staves {
				sb := sc.bars[bi]
				var evs []*ev
				for _, v := range sb.voices {
					for _, x := range v {
						if x.col == c && !x.space && !x.barRest {
							evs = append(evs, x)
						}
					}
				}
				if len(evs) == 0 {
					continue
				}
				l, r := e.arrangeColumn(sb, c, evs)
				c.left, c.right = math.Max(c.left, l), math.Max(c.right, r)
			}
		}
		bc.widths = make([]float64, len(bc.cols)-1)
		for k := 0; k+1 < len(bc.cols); k++ {
			a, b := bc.cols[k], bc.cols[k+1]
			gap := noteGap
			if b.end {
				gap = lastGap
			}
			bc.widths[k] = math.Max(e.ideal(b.offF-a.offF), a.right+b.left+gap)
		}
		if bi > 0 {
			bc.prefixW = e.barPrefixWidth(bc)
		}
		first := bc.cols[0]
		switch {
		case bi == 0:
			bc.lead = math.Max(prefixGap, first.left+0.8)
		case bc.prefixW > 0:
			bc.lead = math.Max(prefixGap, first.left+0.8)
		default:
			bc.lead = math.Max(barGapAfter, first.left+0.6)
		}
		// A bar rest wants a bar wide enough to sit in the middle of.
		if len(bc.cols) == 2 {
			bc.widths[0] = math.Max(bc.widths[0], 5.5-bc.lead)
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
