package engrave

// multirest.go — multi-bar rests (Options.MultiRests), as parts print them.
//
// A run of two or more bars in which every staff of the layout only rests
// (no notes, nothing marked on the rests) is printed as one bar: an H-bar
// along the middle line, with short vertical strokes at its ends, and the
// number of bars over the staff in time-signature numerals (Gould, Behind
// Bars, "Multi-bar rests"). The run's first bar carries it; the bars after
// are folded to nothing — no barlines, no width — so every bar keeps its
// place in the layout and its box (at the multi-bar rest's end).
//
// A run breaks wherever the player must read something between two of its
// bars: a rehearsal mark or a tempo mark at a bar, a key, time or clef
// change, a repeat barline (the start of one or the end of one), an
// ending over a bar, a segno or coda at a bar's start or a Fine, D.C.,
// D.S. or To Coda at its end, or a break a page layout asked for. Those
// can stand at the run's first bar (a tempo mark over the rest), never
// inside it.

import (
	"math"
	"strconv"

	"foundry/internal/score"
)

const (
	multiRestW   = 9.0  // a multi-bar rest's least width, barline to barline
	multiRestIn  = 0.8  // the H-bar's ends to the bar's start and end
	multiRestNum = -1.6 // the count's y over the staff (its glyphs' middle)
)

// foldRests finds the runs of rest bars and folds each into its first.
func (e *engraver) foldRests() {
	n := len(e.bars)
	resting := make([]bool, n)
	for bi := range e.bars {
		resting[bi] = e.barRests(bi)
	}
	for i := 0; i < n; {
		if !resting[i] {
			i++
			continue
		}
		j := i + 1
		for j < n && resting[j] && e.restJoins(j) {
			j++
		}
		if j-i >= 2 {
			e.bars[i].multi = j - i
			for k := i; k < j; k++ {
				bc := e.bars[k]
				bc.folded = k > i
				// One column at the start and the bar's end; the rests
				// themselves are not drawn.
				bc.cols = []*column{bc.cols[0], bc.cols[len(bc.cols)-1]}
				for _, sc := range e.staves {
					for _, v := range sc.bars[k].voices {
						for _, x := range v {
							x.col = nil
						}
					}
				}
			}
		}
		i = j
	}
}

// barRests reports a bar in which every staff only rests, with nothing
// marked on its rests, no clef change or tempo mark inside it and no
// ending over it.
func (e *engraver) barRests(bi int) bool {
	bc := e.bars[bi]
	for _, sc := range e.staves {
		for _, v := range sc.bars[bi].voices {
			for _, x := range v {
				if x.grace || len(x.notes) > 0 || len(x.e.Marks) > 0 {
					return false
				}
			}
		}
		for _, ch := range sc.st.Clefs {
			if ch.At.Bar == bc.bar && !ch.At.Offset.IsZero() {
				return false
			}
		}
	}
	for _, t := range e.s.Tempo {
		if t.At.Bar == bc.bar && !t.At.Offset.IsZero() {
			return false
		}
	}
	for _, v := range e.s.Voltas {
		if bc.bar >= v.Start && bc.bar <= v.End {
			return false
		}
	}
	return true
}

// restJoins reports that layout bar bi may go on a multi-bar rest from the
// bar before it: nothing is printed between the two.
func (e *engraver) restJoins(bi int) bool {
	bc, pb := e.bars[bi], e.bars[bi-1]
	if bc.showKey || bc.showTime || e.o.runBreaks[bc.bar] || e.repeatStarts(bc.bar) || e.repeatEnds(pb.bar) {
		return false
	}
	for _, sec := range e.s.Sections {
		if sec.Bar == bc.bar {
			return false
		}
	}
	for _, t := range e.s.Tempo {
		if t.At.Bar == bc.bar {
			return false
		}
	}
	for _, j := range e.s.Jumps {
		switch j.Kind {
		case score.JumpSegno, score.JumpCoda:
			if j.Bar == bc.bar {
				return false
			}
		default:
			if j.Bar == pb.bar {
				return false
			}
		}
	}
	for _, sc := range e.staves {
		for _, ch := range sc.st.Clefs {
			at := ch.At
			if l := e.s.BarLength(at.Bar); at.Bar >= 1 && !at.Offset.Less(l) {
				at = score.Pos{Bar: at.Bar + 1, Offset: at.Offset.Sub(l)}
			}
			if at.Bar == bc.bar {
				return false
			}
		}
	}
	return true
}

// multiRestWidths spaces a bar of a multi-bar rest: the first wide enough
// for the H-bar and its count, the folded ones nothing.
func (e *engraver) multiRestWidths(bc *barCtx) {
	switch {
	case bc.folded:
		bc.lead = 0
		for k := range bc.rods {
			bc.rods[k], bc.springs[k] = 0, 0
		}
	case bc.multi > 0:
		w := math.Max(multiRestW, e.runWidth(multiRestDigits(bc.multi))+2*multiRestIn+1.5)
		bc.rods[0] = math.Max(bc.rods[0], w-bc.lead)
		bc.springs[0] = math.Max(bc.springs[0], w+2-bc.lead)
	}
}

// multiRestDigits are a count's numerals.
func multiRestDigits(n int) []string {
	var out []string
	for _, c := range strconv.Itoa(n) {
		out = append(out, "timeSig"+string(c))
	}
	return out
}

// drawMultiRest draws a staff's multi-bar rest in its first bar: the
// H-bar on the middle line between two strokes, and over the first staff
// of a line the count.
func (e *engraver) drawMultiRest(sc *staffCtx, bc *barCtx) {
	ref := Ref{Line: sc.line, Staff: sc.staff, Voice: -1, Bar: bc.bar, Event: -1, Note: -1}
	a, z := bc.content+multiRestIn, bc.x1-multiRestIn
	if a >= z {
		return
	}
	th := e.d.HBarThickness
	if th <= 0 {
		th = 1
	}
	stroke := e.d.ThinBarlineThickness * 1.3
	e.addStaff(sc, Item{Kind: ItemLine, Role: RoleRest, Ref: ref, P: Point{X: a, Y: 2}, Q: Point{X: z, Y: 2}, Width: th})
	for _, x := range []float64{a + stroke/2, z - stroke/2} {
		e.addStaff(sc, Item{Kind: ItemLine, Role: RoleRest, Ref: ref, P: Point{X: x, Y: 0.95}, Q: Point{X: x, Y: 3.05}, Width: stroke})
	}
	if sc.first {
		gs := multiRestDigits(bc.multi)
		e.drawRun(sc, nil, RoleRest, ref, gs, (a+z)/2-e.runWidth(gs)/2, multiRestNum)
	}
}
