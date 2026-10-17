package notate

// graces.go — played grace notes back into grace notes.
//
// An acciaccatura is played as a flick just before the beat: a note far
// shorter than the grid, ending about where the next note starts. Snapped
// to the grid it lands on that next note's onset with no length of its
// own, so it would come out as a stray chord tone (and push the line into
// a second voice). Marking it a grace keeps it out of the grid choice and
// the voices; Write prints it, slashed, in front of the note it leads to,
// and the interpreter plays it just before the beat again.

import (
	"math"
	"sort"

	"foundry/internal/score"
)

// maxGraceRun is the most grace notes one main note takes.
const maxGraceRun = 4

// MarkGraces marks the played grace notes among notes (one part's, in any
// order) and returns them in time order. A grace is an onset whose notes
// all last at most half the grid's unit and stop by about when the next
// onset starts, which is at most half a unit later; up to four in a row
// lead to the main note after them. A run has to start at least a unit
// after the onset before it, so a fast run of short notes on a coarse grid
// is not taken for graces.
func MarkGraces(notes []Note, g Grid) []Note {
	out := append([]Note(nil), notes...)
	sort.SliceStable(out, func(i, j int) bool {
		if c := out[i].Start.Cmp(out[j].Start); c != 0 {
			return c < 0
		}
		return out[i].MIDI < out[j].MIDI
	})
	if g.Unit.Sign() <= 0 || len(out) < 2 {
		return out
	}
	half := g.Unit.Div(score.Whole(2))
	quarter := g.Unit.Div(score.Whole(4))
	together := g.Unit.Div(score.Whole(16))
	// Onset groups: notes starting within a 16th of the unit of each other.
	type group struct {
		at     score.Frac
		lo, hi int // out[lo:hi]
	}
	var gs []group
	for i := 0; i < len(out); {
		j := i + 1
		for j < len(out) && out[j].Start.Sub(out[i].Start).Less(together) {
			j++
		}
		gs = append(gs, group{at: out[i].Start, lo: i, hi: j})
		i = j
	}
	short := func(k int) bool {
		if k+1 >= len(gs) {
			return false
		}
		next := gs[k+1].at
		if half.Less(next.Sub(gs[k].at)) {
			return false
		}
		for _, n := range out[gs[k].lo:gs[k].hi] {
			if half.Less(n.End.Sub(n.Start)) || next.Add(quarter).Less(n.End) {
				return false
			}
		}
		return true
	}
	for k := 0; k < len(gs); {
		if !short(k) || k > 0 && gs[k].at.Sub(gs[k-1].at).Less(g.Unit) {
			k++
			continue
		}
		run := k
		for run < len(gs) && short(run) {
			run++
		}
		// gs[run] is the main note; gs[k:run] lead to it.
		if run-k <= maxGraceRun {
			for q := k; q < run; q++ {
				for i := gs[q].lo; i < gs[q].hi; i++ {
					out[i].Grace = true
				}
			}
		}
		k = run + 1
	}
	return out
}

// AttachGraces hangs grace notes (Quantize's output, each at the onset of
// the note it leads to) on the chords of a staff's voices: each on the
// chord starting there whose pitch is nearest. It returns the graces with
// no chord at their onset in these voices (the main note went to another
// staff).
func AttachGraces(voices [][]Chord, graces []Note) (left []Note) {
	for _, g := range graces {
		var best *Chord
		bestD := math.Inf(1)
		for _, v := range voices {
			i := sort.Search(len(v), func(i int) bool { return !v[i].Start.Less(g.Start) })
			if i < len(v) && v[i].Start.Eq(g.Start) {
				if d := math.Abs(meanPitch(v[i].MIDI) - float64(g.MIDI)); d < bestD {
					best, bestD = &v[i], d
				}
			}
		}
		if best == nil {
			left = append(left, g)
			continue
		}
		best.Graces = append(best.Graces, []int{g.MIDI})
	}
	return left
}
