package interpret

// feel.go — swing and humanise: the two ways a performance leaves the
// grid on purpose.
//
// Swing is a warp of written time within each pair of eighths (or
// sixteenths), counted from the bar's start (from the full bar's start in
// a pickup): the first of the pair is lengthened to Amount of the pair, the
// second shortened to fit, and everything inside each half — a note's
// start and end, a slide's breakpoints, a grace note — moves in proportion.
// A pair the bar cuts short (the last eighth of a 5/8 bar) stays straight,
// so a bar still begins where it did. Swing happens in exact score time,
// before the tempo map.
//
// Humanise moves each note's start and end (and its slide's breakpoints)
// in seconds by up to Timing milliseconds either way (a triangle distribution: most notes move a
// little), and its velocity by up to Velocity of itself. The amounts come
// from a hash of the seed and where the note is written — its line, staff,
// voice, tick and pass, so the notes of a chord move together and an edit
// elsewhere leaves every other note's nudge as it was — so the same score
// plays the same way every time.

import (
	"math"
	"sort"

	"foundry/internal/score"
)

// swing warps every note of a swung line (and its bends) in written time.
func (in *interp) swing() {
	s := in.s
	for li, l := range s.Lines {
		w := s.SwingFor(l)
		if !w.On() {
			continue
		}
		amount := score.F(int64(math.Round(float64(w.Amount)*960)), 960)
		u := w.UnitLength()
		for i := range in.notes {
			n := &in.notes[i]
			if n.line != li || n.drop {
				continue
			}
			end := in.warp(n.at.Add(n.dur), amount, u)
			n.at = in.warp(n.at, amount, u)
			n.dur = end.Sub(n.at)
			for k := range n.bends {
				n.bends[k].at = in.warp(n.bends[k].at, amount, u)
			}
		}
	}
}

// warp is where a performance position lands under swing a (the first
// note's share of each pair) of unit u.
func (in *interp) warp(x, a, u score.Frac) score.Frac {
	k := sort.Search(len(in.order), func(i int) bool { return x.Less(in.order[i].start) }) - 1
	if k < 0 {
		return x
	}
	pb := in.order[k]
	end := pb.start.Add(pb.length)
	origin := pb.start
	if full := in.s.MeterAt(pb.bar).Length(); pb.bar == 1 && pb.length.Less(full) {
		// A pickup is the end of a bar: its pairs count from where the
		// whole bar would have begun.
		origin = pb.start.Sub(full.Sub(pb.length))
	}
	pair := u.MulInt(2)
	off := x.Sub(origin)
	if off.Sign() < 0 {
		return x
	}
	j := off.Div(pair).Floor()
	ps := origin.Add(pair.MulInt(j))
	if end.Less(ps.Add(pair)) {
		return x // a pair the bar cuts short
	}
	r := x.Sub(ps)
	first := pair.Mul(a) // the first note's share
	if r.Less(u) {
		return ps.Add(r.Div(u).Mul(first))
	}
	return ps.Add(first).Add(r.Sub(u).Div(u).Mul(pair.Sub(first)))
}

// humanizeHash is a well-mixed 64 bits from the seed and a note's place.
func humanizeHash(seed int64, vals ...int64) uint64 {
	h := uint64(seed)*0x9E3779B97F4A7C15 + 0x632BE59BD9B4E019
	for _, v := range vals {
		h ^= uint64(v) + 0x9E3779B97F4A7C15 + (h << 6) + (h >> 2)
		// splitmix64's finaliser.
		h ^= h >> 30
		h *= 0xBF58476D1CE4E5B9
		h ^= h >> 27
		h *= 0x94D049BB133111EB
		h ^= h >> 31
	}
	return h
}

// unit2 is two uniform numbers in [0, 1) from a hash.
func unit2(h uint64) (float64, float64) {
	return float64(h>>40) / (1 << 24), float64((h>>16)&0xFFFFFF) / (1 << 24)
}

// humanize moves a finished note (seconds, velocity) as its line's
// humanise says.
func (in *interp) humanize(n *Note) {
	l := in.s.Lines[n.Line]
	h := in.s.HumanizeFor(l)
	if !h.On() {
		return
	}
	if h.Timing > 0 {
		a, b := unit2(humanizeHash(h.Seed, int64(n.Line), int64(n.Staff), int64(n.Voice), n.Tick, int64(n.Pass)))
		d := (a + b - 1) * float64(h.Timing) / 1000
		if n.Start+d < 0 {
			d = -n.Start
		}
		n.Start += d
		n.End += d
		// A slide moves with its note: its breakpoints are ticks, so each
		// goes where its own time plus the nudge falls on the tempo map.
		for k := range n.Bend {
			b := &n.Bend[k]
			b.Tick = in.tempo.Tick(in.tempo.Seconds(b.Tick) + d)
		}
	}
	if h.Velocity > 0 {
		a, b := unit2(humanizeHash(h.Seed^0x5bd1e995, int64(n.Line), int64(n.Staff), int64(n.Voice), n.Tick, int64(n.Pass), int64(n.MIDI)))
		f := 1 + float32(a+b-1)*h.Velocity
		n.Velocity = float32(math.Max(0, math.Min(1, float64(n.Velocity*f))))
	}
}
