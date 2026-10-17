package engrave

// cross.go — notes written on the other staff of a grand staff.
//
// An event marked cross=up or cross=down (score.Event.CrossStaff) stays in
// its own voice — its rhythm, its beams, its ties are its staff's — but its
// notes are written on the line's staff above or below: a piano run that
// passes from one hand's staff to the other's, a hand crossing over. Its
// notes take that staff's clef, key and ottava, and its ledger lines are
// that staff's. A beam that joins notes on both staves is a kneed beam:
// flat, in the middle of the gap between the staves, the stems of each
// staff's notes reaching it from their side (the notes on the upper staff
// stemmed down, the lower staff's up).
//
// Staves are engraved in their own coordinates and stacked afterwards by
// their ink (finish), so where the other staff will be is not known while
// its notes are placed. A score with cross-staff notes is therefore
// engraved twice: once with every note on its own staff, which says how
// far apart the staves of each such grand staff are, and again with those
// distances fixed (at least crossMinStep) so a cross-staff note can be put
// where the other staff will stand.

import (
	"math"

	"foundry/internal/score"
)

// crossMinStep is the least distance from a grand staff's upper staff's
// top line to its lower staff's when notes cross between them: room for a
// kneed beam in the gap.
const crossMinStep = 10.5

// hasCross reports a cross-staff event in the lines and bars to engrave.
func hasCross(s *score.Score, o Options) bool {
	lines := o.Lines
	if lines == nil {
		for i := range s.Lines {
			lines = append(lines, i)
		}
	}
	from, to := max(1, o.From), o.To
	if to <= 0 || to > s.Measures() {
		to = s.Measures()
	}
	for _, li := range lines {
		if li >= 0 && li < len(s.Lines) && lineHasCross(s.Lines[li], from, to) {
			return true
		}
	}
	return false
}

// lineHasCross reports a cross-staff event of a line in bars from..to.
func lineHasCross(l *score.Line, from, to int) bool {
	if len(l.Staves) < 2 {
		return false
	}
	for _, st := range l.Staves {
		for _, v := range st.Voices {
			for b := from; b <= to && b <= len(v.Measures); b++ {
				for i := range v.Measures[b-1].Events {
					if v.Measures[b-1].Events[i].CrossStaff() != 0 {
						return true
					}
				}
			}
		}
	}
	return false
}

// crossLayout engraves a score with its cross-staff notes on their own
// staves and returns the distances to fix between the staves of each line
// that has them (index i: from staff i-1's top to staff i's; 0 where the
// stacking may do as it likes).
func crossLayout(s *score.Score, o Options) []float64 {
	plain := o
	plain.noCross = true
	l := Engrave(s, plain)
	steps := make([]float64, len(l.Staves))
	crossing := map[int]bool{}
	from, to := max(1, o.From), o.To
	if to <= 0 || to > s.Measures() {
		to = s.Measures()
	}
	for li, line := range s.Lines {
		crossing[li] = lineHasCross(line, from, to)
	}
	for i := 1; i < len(l.Staves); i++ {
		a, b := l.Staves[i-1], l.Staves[i]
		if a.Line != b.Line || !crossing[a.Line] {
			continue
		}
		d := b.Top - a.Top
		if !b.Tab && !a.Tab {
			d = math.Max(d, crossMinStep)
		}
		if len(o.StaffSteps) == len(steps) {
			d = math.Max(d, o.StaffSteps[i])
		}
		steps[i] = d
	}
	return steps
}

// crossTarget is the staff a cross-staff event's notes go on, and how far
// its top line is from the home staff's (positive: below). ok is false
// where there is none to go to (one staff, a drum staff, a tablature, the
// edge of the grand staff) or this engraving puts every note on its own.
func (e *engraver) crossTarget(home *staffCtx, dir int) (*staffCtx, float64, bool) {
	if dir == 0 || e.o.noCross || e.o.crossSteps == nil || home.tab != nil || home.percussion {
		return nil, 0, false
	}
	hi, ti := -1, -1
	for i, sc := range e.staves {
		if sc == home {
			hi = i
		}
		if sc.line == home.line && sc.staff == home.staff+dir && sc.tab == nil && !sc.percussion {
			ti = i
		}
	}
	if hi < 0 || ti < 0 || len(e.o.crossSteps) != len(e.staves) {
		return nil, 0, false
	}
	off := 0.0
	a, b := min(hi, ti), max(hi, ti)
	for i := a + 1; i <= b; i++ {
		if e.o.crossSteps[i] <= 0 {
			return nil, 0, false
		}
		off += e.o.crossSteps[i]
	}
	if ti < hi {
		off = -off
	}
	return e.staves[ti], off, true
}

// midY is the middle line an event's stem reaches toward: its staff's, or
// for a cross-staff event the other staff's.
func midY(x *ev) float64 { return 2 + x.crossY }

// kneed sets a beam group that joins notes of both staves: the group's
// own staff's notes stemmed toward the other staff, the crossed ones back
// toward theirs, the beam between. It reports whether the group is one.
func (g *beamGroup) kneed() bool {
	home, cross := false, false
	off := 0.0
	for _, x := range g.evs {
		if x.cross != 0 {
			cross, off = true, x.crossY
		} else {
			home = true
		}
	}
	if !home || !cross {
		return false
	}
	g.knee, g.kneeY = true, off
	// Home notes reach toward the other staff: up when it is above.
	g.up = off < 0
	for _, x := range g.evs {
		x.up = g.up
		if x.cross != 0 {
			x.up = !g.up
		}
	}
	return true
}

// layoutKnee puts a kneed beam flat in the middle of the gap between the
// staves, never closer to a note than a stem's shortest.
func (e *engraver) layoutKnee(g *beamGroup, xs []float64) {
	g.x0, g.slope = xs[0], 0
	if g.kneeY < 0 {
		// The other staff above: its bottom line at kneeY+4, this staff's
		// top at 0.
		g.y0 = (g.kneeY + 4) / 2
	} else {
		g.y0 = (4 + g.kneeY) / 2
	}
}

// crossedInto are the cross-staff events the line's other staves write on
// a staff in a layout bar: its accidentals are reckoned with them.
func (e *engraver) crossedInto(sc *staffCtx, bi int) []*ev {
	if sc.tab != nil || sc.percussion {
		return nil
	}
	var out []*ev
	for _, oc := range e.staves {
		if oc == sc || oc.line != sc.line || oc.tab != nil || bi >= len(oc.bars) {
			continue
		}
		for _, v := range oc.bars[bi].voices {
			for _, x := range v {
				if x.cross != 0 && oc.staff+x.cross == sc.staff && len(x.notes) > 0 {
					out = append(out, x)
				}
			}
		}
	}
	return out
}
