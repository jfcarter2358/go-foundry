package notate

// quantize.go — snapping played time to written time.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"foundry/internal/score"
)

// Note is one played note: a pitch from Start to End, whole notes from the
// top of the score.
type Note struct {
	Start, End score.Frac
	MIDI       int
	// Velocity is 1..127 (0 unknown).
	Velocity int
	// Grace is a played grace note (MarkGraces): it takes no time, and
	// Quantize puts it at the onset of the note it leads to.
	Grace bool
	// Marks go on the chord the note is written in (a glissando from it).
	Marks []score.Mark
}

// Grid is what quantising snaps to: a plain value (Unit, a 16th is 1/16)
// and the tuplets a beat may be divided into instead (3 for triplets).
type Grid struct {
	Unit    score.Frac
	Tuplets []int
}

// DefaultGrid is 16ths with triplets: what most played music is written in.
func DefaultGrid() Grid { return Grid{Unit: score.F(1, 16), Tuplets: []int{3}} }

// ParseGrid reads a grid as "16", "16,3", "32,3,5", "8t" (8ths and
// triplets): the plain value as a note-value denominator, then tuplets.
func ParseGrid(s string) (Grid, error) {
	parts := strings.Split(strings.ReplaceAll(strings.TrimSpace(s), " ", ""), ",")
	first := parts[0]
	var g Grid
	if strings.HasSuffix(first, "t") {
		first = strings.TrimSuffix(first, "t")
		g.Tuplets = []int{3}
	}
	n, err := strconv.Atoi(first)
	if err != nil || n < 1 || n > 256 || n&(n-1) != 0 {
		return Grid{}, fmt.Errorf("grid %q: want a note value (4, 8, 16, 32, 64) and tuplets, e.g. 16,3", s)
	}
	g.Unit = score.F(1, int64(n))
	for _, p := range parts[1:] {
		t, err := strconv.Atoi(p)
		if err != nil || t < 3 || t > 15 || t%2 == 0 {
			return Grid{}, fmt.Errorf("grid %q: tuplet %q (want an odd number 3..15)", s, p)
		}
		if !hasInt(g.Tuplets, t) {
			g.Tuplets = append(g.Tuplets, t)
		}
	}
	return g, nil
}

// String writes what ParseGrid reads.
func (g Grid) String() string {
	s := strconv.FormatInt(g.Unit.Den, 10)
	for _, t := range g.Tuplets {
		s += "," + strconv.Itoa(t)
	}
	return s
}

func hasInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// beatGrid is one beat's chosen grid: points From + k*Step up to To.
type beatGrid struct {
	from, to, step score.Frac
}

func (g beatGrid) snap(x score.Frac) score.Frac {
	k := x.Sub(g.from).Div(g.step).Round()
	p := g.from.Add(g.step.MulInt(k))
	return score.MaxFrac(g.from, score.MinFrac(p, g.to))
}

// cost is how far the points are from a grid.
func (g beatGrid) cost(xs []score.Frac) float64 {
	c := 0.0
	for _, x := range xs {
		d := x.Sub(g.snap(x)).Float()
		if d < 0 {
			d = -d
		}
		c += d
	}
	return c
}

// beatRegion is one beat of the layout, absolute.
type beatRegion struct {
	a, b    score.Frac
	grouped bool
	plain   beatGrid
	chosen  beatGrid
}

// regions are every beat of the layout, in order.
func (l Layout) regions(g Grid) []beatRegion {
	var out []beatRegion
	for b := 0; b < l.Bars(); b++ {
		br := l.bar(b)
		for i := range br.beats {
			a, z := br.start.Add(br.beats[i]), br.start.Add(br.beatEnd(i))
			// The plain grid counts from where a full bar starts, so a
			// pickup's 16ths sit where a full bar's would.
			origin := br.start.Add(br.origin)
			k := a.Sub(origin).Div(g.Unit).Floor()
			plain := beatGrid{from: origin.Add(g.Unit.MulInt(k)), to: z, step: g.Unit}
			out = append(out, beatRegion{a: a, b: z, grouped: br.meter.Compound(), plain: plain, chosen: plain})
		}
	}
	return out
}

// tupletGrid is beat r divided for tuplet t: t, or t times the power of two
// that keeps it as fine as the plain grid (16ths and triplets: 3 a quarter;
// 32nds and triplets: 6).
func tupletGrid(r beatRegion, unit score.Frac, t int) (beatGrid, bool) {
	n := r.b.Sub(r.a).Div(unit)
	if !n.IsInt() || r.grouped {
		return beatGrid{}, false
	}
	p := 1
	for p < t {
		p *= 2
	}
	sub := int64(t) * max(1, n.Floor()/int64(p))
	return beatGrid{from: r.a, to: r.b, step: r.b.Sub(r.a).Div(score.Whole(sub))}, true
}

// tupletBias is how much better a tuplet grid must fit a beat than the
// plain one to be chosen: played 16ths wobble, and a triplet grid is close
// to every one of them.
const tupletBias = 0.6

// fallbackTuplets are tried for a beat whose grid would merge onsets that
// were played apart (a quintuplet on a grid of 16ths and triplets).
var fallbackTuplets = []int{3, 5, 7}

// Quantize snaps notes to a grid over a layout. Each beat chooses the
// plain grid or one of the tuplet grids by which fits its onsets best (a
// tuplet only when clearly better); onsets snap to their beat's grid, ends
// to the grid of the beat they fall in. When the grid allows tuplets at
// all, a choice that would snap two onsets played apart onto one point
// loses to one that keeps them apart, trying quintuplets and septuplets
// too. A note never ends up shorter than its
// grid's step, and of two notes of a pitch that end up overlapping the
// first is cut at the second (or merged into it when they start together).
//
// Grace notes (Note.Grace) take no part in the grid: each comes back, in
// time order after the other notes, with no length at the snapped onset of
// the first other note starting after it.
func Quantize(notes []Note, l Layout, g Grid) (out []Note, err error) {
	defer score.RecoverOverflow(&err)
	if g.Unit.Sign() <= 0 {
		return nil, fmt.Errorf("quantize: grid unit %s", g.Unit)
	}
	rs := l.regions(g)
	if len(rs) == 0 {
		return append([]Note(nil), notes...), nil
	}
	var graces []Note
	plain := make([]Note, 0, len(notes))
	for _, n := range notes {
		if n.Grace {
			graces = append(graces, n)
		} else {
			plain = append(plain, n)
		}
	}
	notes = plain
	half := g.Unit.Div(score.Whole(2))
	// Each onset belongs to the beat its nearest plain grid point is in.
	regionOf := func(x score.Frac) int {
		i := sort.Search(len(rs), func(i int) bool { return x.Add(half).Less(rs[i].b) })
		return min(i, len(rs)-1)
	}
	onsets := make([][]score.Frac, len(rs))
	for _, n := range notes {
		i := regionOf(n.Start)
		onsets[i] = append(onsets[i], n.Start)
	}
	for i := range rs {
		if len(onsets[i]) == 0 {
			continue
		}
		best, bestCost := rs[i].plain, rs[i].plain.cost(onsets[i])
		limit := bestCost * tupletBias
		for _, t := range g.Tuplets {
			tg, ok := tupletGrid(rs[i], g.Unit, t)
			if !ok {
				continue
			}
			if c := tg.cost(onsets[i]); c < limit && c < bestCost {
				best, bestCost = tg, c
			}
		}
		if merged := best.merges(onsets[i], half); merged > 0 && len(g.Tuplets) > 0 {
			// The choice loses notes played apart: the grid (of all the
			// beat could take) that merges fewest, then fits best.
			for _, t := range append(append([]int(nil), g.Tuplets...), fallbackTuplets...) {
				tg, ok := tupletGrid(rs[i], g.Unit, t)
				if !ok {
					continue
				}
				m, c := tg.merges(onsets[i], half), tg.cost(onsets[i])
				if m < merged || m == merged && c < bestCost {
					best, bestCost, merged = tg, c, m
				}
			}
		}
		rs[i].chosen = best
	}
	snapStart := func(x score.Frac) score.Frac {
		ri := regionOf(x)
		start := rs[ri].chosen.snap(x)
		if !start.Less(rs[ri].b) && ri+1 < len(rs) {
			start = rs[ri+1].a
		}
		return start
	}
	out = make([]Note, 0, len(notes))
	for _, n := range notes {
		ri := regionOf(n.Start)
		start := snapStart(n.Start)
		// An end belongs to the beat it is in; one just past a beat's start
		// snaps back to it.
		ei := sort.Search(len(rs), func(i int) bool { return n.End.Less(rs[i].b) })
		ei = min(ei, len(rs)-1)
		end := rs[ei].chosen.snap(n.End)
		if ei > 0 && n.End.Sub(rs[ei].a).Less(half) {
			end = rs[ei].a
		}
		if !start.Less(end) {
			step := rs[ri].chosen.step
			if !start.Less(rs[ri].b) && ri+1 < len(rs) {
				step = rs[ri+1].chosen.step
			}
			end = start.Add(step)
		}
		if l.End().Less(end) {
			end = l.End()
		}
		if !start.Less(end) {
			continue
		}
		n.Start, n.End = start, end
		out = append(out, n)
	}
	out = Tidy(out)
	sort.SliceStable(graces, func(i, j int) bool { return graces[i].Start.Less(graces[j].Start) })
	for _, gn := range graces {
		// The note it leads to: the first of the others starting after it.
		var main *Note
		for k := range notes {
			if gn.Start.Less(notes[k].Start) && (main == nil || notes[k].Start.Less(main.Start)) {
				main = &notes[k]
			}
		}
		if main == nil {
			continue
		}
		at := snapStart(main.Start)
		if !at.Less(l.End()) {
			continue
		}
		gn.Start, gn.End = at, at
		out = append(out, gn)
	}
	return out, nil
}

// merges counts the onsets the grid snaps onto a point another onset
// already took, of those more than apart from each other: notes that
// were played as separate notes and would come out as one chord.
func (g beatGrid) merges(xs []score.Frac, apart score.Frac) int {
	seen := map[score.Frac]score.Frac{}
	n := 0
	sorted := append([]score.Frac(nil), xs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Less(sorted[j]) })
	for _, x := range sorted {
		p := g.snap(x)
		if first, ok := seen[p]; ok {
			if apart.Less(x.Sub(first)) {
				n++
			}
			continue
		}
		seen[p] = x
	}
	return n
}

// Tidy sorts notes by start (then pitch) and settles same-pitch overlaps:
// two that start together become the longer, and one still sounding when
// its pitch starts again is cut there.
func Tidy(notes []Note) []Note {
	sort.SliceStable(notes, func(i, j int) bool {
		if c := notes[i].Start.Cmp(notes[j].Start); c != 0 {
			return c < 0
		}
		return notes[i].MIDI < notes[j].MIDI
	})
	last := map[int]int{} // pitch → index in out
	out := notes[:0]
	for _, n := range notes {
		if j, ok := last[n.MIDI]; ok {
			p := &out[j]
			if p.Start.Eq(n.Start) {
				p.End = score.MaxFrac(p.End, n.End)
				p.Velocity = max(p.Velocity, n.Velocity)
				continue
			}
			if n.Start.Less(p.End) {
				p.End = n.Start
			}
		}
		last[n.MIDI] = len(out)
		out = append(out, n)
	}
	return out
}
