package notate

// voices.go — overlapping notes into chords and voices.

import (
	"math"
	"sort"

	"foundry/internal/score"
)

// Separate groups a staff's notes into chords and deals the chords out to
// at most maxVoices voices (score.MaxVoices at most), returned top voice
// first.
//
// Notes that start together and end together (within tol) are a chord.
// Each chord goes to a voice that is free when it starts — of several, the
// one whose last pitch is nearest, counting each beat a voice has been
// silent as a few semitones further, so a melody and a held bass stay
// apart and a line that had to split for a moment comes back together —
// or to a new voice. When every voice is busy and no more are allowed,
// it joins the chord that started with it, or cuts short the voice that
// would have ended first. With maxVoices 1 there is no separation: what
// starts together is one chord and a note still sounding at the next
// onset is cut there.
// Grace notes (Note.Grace) are left out; AttachGraces hangs them on the
// chords afterwards.
func Separate(notes []Note, maxVoices int, tol score.Frac) [][]Chord {
	maxVoices = max(1, min(maxVoices, score.MaxVoices))
	ns := make([]Note, 0, len(notes))
	for _, n := range notes {
		if !n.Grace {
			ns = append(ns, n)
		}
	}
	sort.SliceStable(ns, func(i, j int) bool {
		if c := ns[i].Start.Cmp(ns[j].Start); c != 0 {
			return c < 0
		}
		return ns[i].MIDI > ns[j].MIDI
	})
	// Chords: same start, ends within tol (any end with one voice).
	var chords []Chord
	for i := 0; i < len(ns); {
		j := i
		for j < len(ns) && ns[j].Start.Eq(ns[i].Start) {
			j++
		}
		group := ns[i:j]
		used := make([]bool, len(group))
		for k := range group {
			if used[k] {
				continue
			}
			c := Chord{Start: group[k].Start, End: group[k].End}
			sum, cnt := 0, 0
			for q := k; q < len(group); q++ {
				if used[q] {
					continue
				}
				d := group[q].End.Sub(c.End)
				if d.Sign() < 0 {
					d = d.Neg()
				}
				if maxVoices > 1 && tol.Less(d) {
					continue
				}
				used[q] = true
				c.MIDI = append(c.MIDI, group[q].MIDI)
				c.End = score.MaxFrac(c.End, group[q].End)
				if group[q].Velocity > 0 {
					sum, cnt = sum+group[q].Velocity, cnt+1
				}
			}
			if cnt > 0 {
				c.Velocity = (sum + cnt/2) / cnt
			}
			chords = append(chords, c)
		}
		i = j
	}

	type voice struct {
		chords []Chord
		end    score.Frac
		last   float64
	}
	var vs []*voice
	for _, c := range chords {
		p := meanPitch(c.MIDI)
		var best *voice
		bestD := math.Inf(1)
		for _, v := range vs {
			if c.Start.Less(v.end) {
				continue
			}
			idle := c.Start.Sub(v.end).Float()
			if d := math.Abs(v.last-p) + idle*idlePenalty; d < bestD {
				best, bestD = v, d
			}
		}
		switch {
		case best != nil:
		case len(vs) < maxVoices:
			best = &voice{}
			vs = append(vs, best)
		default:
			// Every voice is busy: join a chord that started with this
			// one, or cut the voice that frees first.
			joined := false
			for _, v := range vs {
				lc := &v.chords[len(v.chords)-1]
				if lc.Start.Eq(c.Start) {
					lc.MIDI = append(lc.MIDI, c.MIDI...)
					lc.End = score.MaxFrac(lc.End, c.End)
					v.end = lc.End
					joined = true
					break
				}
			}
			if joined {
				continue
			}
			best = vs[0]
			for _, v := range vs[1:] {
				if v.end.Less(best.end) {
					best = v
				}
			}
			best.chords[len(best.chords)-1].End = c.Start
		}
		best.chords = append(best.chords, c)
		best.end, best.last = c.End, p
	}
	// Top voice first.
	sort.SliceStable(vs, func(i, j int) bool { return voiceMean(vs[i].chords) > voiceMean(vs[j].chords) })
	out := make([][]Chord, len(vs))
	for i, v := range vs {
		cs := v.chords
		for k := 0; k+1 < len(cs); k++ {
			if cs[k+1].Start.Less(cs[k].End) {
				cs[k].End = cs[k+1].Start
			}
		}
		// Cutting may leave a chord with no length (two onsets at once in
		// one voice): drop it.
		kept := cs[:0]
		for _, c := range cs {
			if c.Start.Less(c.End) {
				kept = append(kept, c)
			}
		}
		out[i] = kept
	}
	return out
}

// idlePenalty is how much further, in semitones per whole note, a voice
// that has been silent counts against one that just stopped: three a
// quarter.
const idlePenalty = 12

func meanPitch(ms []int) float64 {
	if len(ms) == 0 {
		return 0
	}
	s := 0
	for _, m := range ms {
		s += m
	}
	return float64(s) / float64(len(ms))
}

// voiceMean is a voice's pitch weighted by how long each chord lasts.
func voiceMean(cs []Chord) float64 {
	var sum, w float64
	for _, c := range cs {
		d := c.End.Sub(c.Start).Float()
		sum += meanPitch(c.MIDI) * d
		w += d
	}
	if w == 0 {
		return 0
	}
	return sum / w
}

// Legato closes the small gaps a player leaves between notes: a chord that
// stops short of the voice's next one (or, when none follows in its bar,
// of the bar line) by less than half its own length, and by no more than
// maxGap, is held to it. A played legato line comes out as quarters and
// halves rather than double-dotted values and 16th rests; a short note
// with a long gap after it (staccato, a drum hit) keeps its rest.
func Legato(voice []Chord, l Layout, maxGap score.Frac) {
	for i := range voice {
		c := &voice[i]
		b := l.BarAt(c.End.Sub(score.F(1, 1<<20)))
		target := l.Starts[min(b+1, len(l.Starts)-1)]
		if i+1 < len(voice) && voice[i+1].Start.Less(target) {
			target = voice[i+1].Start
		}
		gap := target.Sub(c.End)
		if gap.Sign() <= 0 || maxGap.Less(gap) {
			continue
		}
		if gap.MulInt(2).Less(c.End.Sub(c.Start)) {
			c.End = target
		}
	}
}
