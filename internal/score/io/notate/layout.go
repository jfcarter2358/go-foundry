// Package notate writes timed notes as notation: the step every importer
// that does not arrive as notation needs. A MIDI file (and later the MIDI
// keyboard's real-time recording and the .ftrk importer) knows when each
// note starts and stops; a score needs bars, voices, note values, ties,
// rests and tuplets, and a spelling for each pitch.
//
// The stages are separate so a dialog can show and redo each one:
//
//	Quantize  snaps onsets and ends to a grid, choosing per beat between
//	          the plain grid and a tuplet one (triplets), so swung or
//	          humanised playing lands where it was meant.
//	Separate  splits overlapping notes into chords and up to four voices.
//	Write     turns one voice's chords into measures: rests in the gaps,
//	          notes split at bar lines and beats with ties, values chosen by
//	          the meter (no dotted note across the middle of a 4/4 bar, none
//	          across a beat of 6/8), tuplet brackets where the rhythm needs
//	          them, pitches spelled in the key.
//
// Time is exact (score.Frac, whole notes from the top of the score), so
// quantised input comes out with exactly the values it went in with.
package notate

import "foundry/internal/score"

// Layout is a score's bar grid: where each bar starts, its meter and its
// key. Build it with LayoutOf after the score's bars, meters, keys and
// pickup are set.
type Layout struct {
	// Starts are each bar's start in whole notes, with one more for the end.
	Starts []score.Frac
	Meters []score.Meter
	Fifths []int
	// Pickup is the first bar's length when it is an anacrusis, else zero.
	Pickup score.Frac
}

// LayoutOf reads a score's bar grid.
func LayoutOf(s *score.Score) Layout {
	n := s.Measures()
	l := Layout{Starts: s.BarStarts(), Meters: make([]score.Meter, n), Fifths: make([]int, n), Pickup: s.Pickup}
	for b := 1; b <= n; b++ {
		l.Meters[b-1] = s.MeterAt(b)
		l.Fifths[b-1] = s.KeyAt(b).Fifths
	}
	return l
}

// Bars is the bar count.
func (l Layout) Bars() int { return len(l.Meters) }

// End is the end of the last bar.
func (l Layout) End() score.Frac { return l.Starts[len(l.Starts)-1] }

// BarAt is the 0-based bar holding a time; past the end it is the last.
func (l Layout) BarAt(t score.Frac) int {
	lo, hi := 0, l.Bars()-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if l.Starts[mid].Cmp(t) <= 0 {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// BarLen is a bar's length (0-based bar).
func (l Layout) BarLen(b int) score.Frac { return l.Starts[b+1].Sub(l.Starts[b]) }

// bar is what writing and quantising need to know about one bar.
type bar struct {
	start, length score.Frac
	meter         score.Meter
	// beats are the beat starts as offsets into the bar, ascending, the
	// first 0. A pickup holds the last beats of a full bar.
	beats []score.Frac
	// origin is where a full bar would have started, as an offset: 0, or
	// negative in a pickup (alignment counts from there).
	origin score.Frac
	// grouped is a meter whose beats are not all the same plain unit:
	// compound (6/8) or additive (3+2+2/8). Nothing but a note on a beat
	// may cross one of its beats.
	grouped bool
}

func (l Layout) bar(b int) bar {
	m := l.Meters[b]
	length := l.BarLen(b)
	br := bar{start: l.Starts[b], length: length, meter: m, grouped: m.Compound() || len(m.Groups) > 1}
	full := m.Length()
	at := score.Frac{}
	if length.Less(full) {
		at = length.Sub(full)
	}
	br.origin = at
	for _, bt := range m.Beats() {
		if at.Sign() >= 0 && at.Less(length) {
			br.beats = append(br.beats, at)
		}
		at = at.Add(bt)
	}
	if len(br.beats) == 0 || !br.beats[0].IsZero() {
		br.beats = append([]score.Frac{{}}, br.beats...)
	}
	// A bar longer than its meter (an irregular bar written as one)
	// carries on in the meter's last beat.
	if len(m.Beats()) > 0 {
		last := m.Beats()[len(m.Beats())-1]
		for at.Less(length) {
			br.beats = append(br.beats, at)
			at = at.Add(last)
		}
	}
	return br
}

// beatEnd is the end of beat i of a bar.
func (b bar) beatEnd(i int) score.Frac {
	if i+1 < len(b.beats) {
		return b.beats[i+1]
	}
	return b.length
}

// BeatEnd is the end of the beat a time falls in (the bar's end for its
// last beat; past the score's end, the score's end).
func (l Layout) BeatEnd(t score.Frac) score.Frac {
	if l.Bars() == 0 || !t.Less(l.End()) {
		return l.End()
	}
	b := l.BarAt(t)
	br := l.bar(b)
	off := t.Sub(br.start)
	for i := len(br.beats) - 1; i >= 0; i-- {
		if !off.Less(br.beats[i]) {
			return br.start.Add(br.beatEnd(i))
		}
	}
	return br.start.Add(br.beatEnd(0))
}

// onBeat reports whether an offset is a beat start (or the bar's end).
func (b bar) onBeat(x score.Frac) bool {
	if x.Eq(b.length) {
		return true
	}
	for _, bt := range b.beats {
		if bt.Eq(x) {
			return true
		}
	}
	return false
}

// middle is the bar's strong middle — beat 3 of 4/4, beat 4 of 6/4 —
// or -1 when it has none (an odd number of beats).
func (b bar) middle() score.Frac {
	n := len(b.beats)
	if n < 4 || n%2 != 0 || !b.origin.IsZero() {
		return score.Whole(-1)
	}
	return b.beats[n/2]
}
