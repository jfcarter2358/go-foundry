package notate

// write.go — one voice's chords as measures.

import (
	"fmt"
	"sort"

	"foundry/internal/score"
)

// Chord is notes sounding together in one voice, from Start to End in whole
// notes from the top of the score.
type Chord struct {
	Start, End score.Frac
	// MIDI are the pitches, any order.
	MIDI []int
	// Velocity is the chord's MIDI velocity (1..127; 0 unknown).
	Velocity int
	// Marks go on the chord's first event: a dynamic, a text.
	Marks []score.Mark
	// Graces are grace notes leading to the chord, each one event's
	// pitches, in the order they play (AttachGraces).
	Graces [][]int
}

// RestKind is what a voice's silences are written as.
type RestKind uint8

const (
	// Rests prints them.
	Rests RestKind = iota
	// Spaces takes the time and prints nothing (score.EventSpace).
	Spaces
)

// WriteOptions shape Write.
type WriteOptions struct {
	// Gaps is what silence inside a bar with notes becomes.
	Gaps RestKind
	// BarRests writes a bar with nothing in it as a bar rest ("R"); off,
	// the bar is left empty (the voice is silent there and prints nothing),
	// which is right for every voice but a staff's first.
	BarRests bool
}

// Write turns a voice's chords (in time order, not overlapping — Separate's
// output) into a voice of measures over the layout. Chords past the
// layout's end are cut at it.
func Write(chords []Chord, l Layout, o WriteOptions) (v *score.Voice, err error) {
	defer score.RecoverOverflow(&err)
	spelled := spell(chords, l)
	v = &score.Voice{Measures: make([]score.Measure, l.Bars())}
	ci := 0
	for b := 0; b < l.Bars(); b++ {
		br := l.bar(b)
		end := br.start.Add(br.length)
		var spans []span
		for ci < len(chords) && !br.start.Less(chords[ci].End) {
			ci++
		}
		at := br.start
		for i := ci; i < len(chords) && chords[i].Start.Less(end); i++ {
			c := &chords[i]
			a, z := score.MaxFrac(c.Start, br.start), score.MinFrac(c.End, end)
			if !a.Less(z) {
				continue
			}
			if at.Less(a) {
				spans = append(spans, span{a: at.Sub(br.start), b: a.Sub(br.start)})
			}
			sp := span{a: a.Sub(br.start), b: z.Sub(br.start), pitches: spelled[i],
				first: !c.Start.Less(br.start), tieOut: end.Less(c.End), marks: c.Marks}
			if sp.first {
				sp.graces = spellGraces(c, spelled[i], l.Fifths[b])
			}
			spans = append(spans, sp)
			at = z
		}
		if len(spans) == 0 {
			if o.BarRests {
				v.Measures[b] = score.Measure{Events: []score.Event{{Kind: score.EventBarRest}}}
			}
			continue
		}
		if at.Less(end) {
			spans = append(spans, span{a: at.Sub(br.start), b: br.length})
		}
		m, err := writeBar(spans, br, o.Gaps)
		if err != nil {
			return nil, fmt.Errorf("bar %d: %w", b+1, err)
		}
		v.Measures[b] = m
	}
	v.Trim()
	return v, nil
}

// span is a stretch of one bar: a chord (or its part in this bar) or a
// silence, as offsets into the bar.
type span struct {
	a, b    score.Frac
	pitches []score.Pitch // nil for a silence
	// first is the chord's own start (its marks go here); tieOut is a
	// chord that carries on into the next bar.
	first, tieOut bool
	marks         []score.Mark
	// graces are the chord's grace notes, spelled, written before it.
	graces [][]score.Pitch
}

// region is a stretch of a bar written one way: plainly, or as one tuplet
// of Num notes in the time of Den.
type region struct {
	a, b     score.Frac
	num, den int
}

// writeBar writes the spans of one bar, which cover it end to end.
func writeBar(spans []span, br bar, gaps RestKind) (score.Measure, error) {
	regions := tupletRegions(spans, br)
	var m score.Measure
	// tuplets is each region's tuplet in the measure (1-based), added the
	// first time the region is written into.
	tuplets := make([]int, len(regions))
	for _, sp := range spans {
		at := sp.a
		for ri := range regions {
			r := regions[ri]
			if !at.Less(r.b) || !r.a.Less(sp.b) {
				continue
			}
			// A run of plain regions writes as one stretch, so a half note
			// can cross a beat; a tuplet region stops at its own end.
			runEnd := r.b
			if r.num == 0 {
				for j := ri + 1; j < len(regions) && regions[j].num == 0; j++ {
					runEnd = regions[j].b
				}
			}
			z := score.MinFrac(sp.b, runEnd)
			if !at.Less(z) {
				continue
			}
			durs, err := pieces(at, z, r, br, sp.pitches == nil)
			if err != nil {
				return m, err
			}
			tup := 0
			if r.num != 0 {
				if tuplets[ri] == 0 {
					m.Tuplets = append(m.Tuplets, score.Tuplet{Num: r.num, Den: r.den})
					tuplets[ri] = len(m.Tuplets)
				}
				tup = tuplets[ri]
			}
			for k, d := range durs {
				e := score.Event{Kind: score.EventRest, Dur: d, Tuplet: tup}
				if sp.pitches == nil {
					if gaps == Spaces {
						e.Kind = score.EventSpace
					}
				} else {
					e.Kind = score.EventChord
					lastPiece := k == len(durs)-1 && z.Eq(sp.b)
					tie := !lastPiece || sp.tieOut
					e.Notes = make([]score.Note, len(sp.pitches))
					for i, p := range sp.pitches {
						e.Notes[i] = score.Note{Pitch: p, Tie: tie}
					}
					if sp.first && at.Eq(sp.a) && k == 0 {
						if len(sp.marks) > 0 {
							e.Marks = append([]score.Mark(nil), sp.marks...)
						}
						// A single grace is an eighth, a run 16ths.
						gd := score.Duration{Base: score.Eighth}
						if len(sp.graces) > 1 {
							gd.Base = score.D16
						}
						for _, gp := range sp.graces {
							ge := score.Event{Kind: score.EventChord, Dur: gd, Grace: score.Acciaccatura, Tuplet: tup}
							for _, p := range gp {
								ge.Notes = append(ge.Notes, score.Note{Pitch: p})
							}
							m.Events = append(m.Events, ge)
						}
					}
				}
				m.Events = append(m.Events, e)
			}
			at = z
			if !at.Less(sp.b) {
				break
			}
		}
	}
	return m, nil
}

// tupletRegions splits a bar into beats and marks the ones whose rhythm
// needs a tuplet: a beat holding an onset or end that no plain note value
// reaches (a third of a beat) is a tuplet of the odd number its offsets
// divide by. Two tuplet beats of the same kind that a note crosses between
// are merged into one bracket (quarter-note triplets over two beats).
func tupletRegions(spans []span, br bar) []region {
	rs := make([]region, len(br.beats))
	for i := range br.beats {
		rs[i] = region{a: br.beats[i], b: br.beatEnd(i)}
	}
	odd := func(r region) int {
		o := 1
		for _, sp := range spans {
			for _, x := range [2]score.Frac{sp.a, sp.b} {
				if x.Less(r.a) || r.b.Less(x) {
					continue
				}
				o = lcm(o, oddPart(x.Sub(r.a).Den))
			}
		}
		return o
	}
	for i := range rs {
		if o := odd(rs[i]); o > 1 {
			rs[i].num, rs[i].den = o, score.DefaultTupletDen(o)
		}
	}
	for merged := true; merged; {
		merged = false
		for i := 0; i+1 < len(rs); i++ {
			if rs[i].num == 0 || rs[i].num != rs[i+1].num {
				continue
			}
			cut := rs[i].b
			crossed := false
			for _, sp := range spans {
				if sp.a.Less(cut) && cut.Less(sp.b) {
					crossed = true
					break
				}
			}
			if !crossed {
				continue
			}
			r := region{a: rs[i].a, b: rs[i+1].b}
			if o := odd(r); o > 1 {
				r.num, r.den = o, score.DefaultTupletDen(o)
			}
			rs = append(rs[:i], append([]region{r}, rs[i+2:]...)...)
			merged = true
			break
		}
	}
	return rs
}

// noteValues are the values a piece can take, longest first: breve to
// 256th, each with two dots, one and none.
var noteValues = func() []score.Duration {
	var out []score.Duration
	for b := score.Breve; b <= score.D256; b++ {
		for dots := 2; dots >= 0; dots-- {
			out = append(out, score.Duration{Base: b, Dots: uint8(dots)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[j].Value().Less(out[i].Value()) })
	return out
}()

// pieces splits [a, z) of a bar, inside region r (and the plain run it
// starts, for a plain region), into written note values.
//
// Plainly written, a value must start on a multiple of half its undotted
// length counted from the bar's (full) start — e q e, s e. s, not s q; a
// double-dotted one on a multiple of its whole undotted length —
// and may cross a beat only where notation allows: never across the
// middle of a 4/4 bar or a beat of 6/8 or 3+2+2/8 unless it starts on a
// beat, and a rest only from beat to beat. Inside a tuplet the same
// alignment counts from the bracket's start, in written time.
func pieces(a, z score.Frac, r region, br bar, rest bool) ([]score.Duration, error) {
	scale, origin := score.Whole(1), br.origin
	if r.num != 0 {
		scale = score.F(int64(r.num), int64(r.den))
		origin = r.a
	}
	w := func(x score.Frac) score.Frac { return x.Sub(origin).Mul(scale) }
	var out []score.Duration
	pos := a
	for pos.Less(z) {
		left := w(z).Sub(w(pos))
		var pick score.Duration
		found := false
		for _, d := range noteValues {
			v := d.Value()
			if left.Less(v) {
				continue
			}
			// A double dot is kept for a value that starts on its own
			// length (h.. on a beat); off it, a dot and a tie read better.
			base := score.Duration{Base: d.Base}.Value()
			grain := base.Div(score.Whole(2))
			if d.Dots >= 2 {
				grain = base
			}
			if !w(pos).Div(grain).IsInt() {
				continue
			}
			end := pos.Add(v.Div(scale))
			if r.num == 0 && !crossingOK(pos, end, br, rest) {
				continue
			}
			pick, found = d, true
			break
		}
		if !found {
			// Nothing aligned fits: take the longest value that does.
			for _, d := range noteValues {
				if !left.Less(d.Value()) {
					pick, found = d, true
					break
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("%s of a whole note at %s is shorter than a 256th", left, pos)
		}
		out = append(out, pick)
		pos = pos.Add(pick.Value().Div(scale))
	}
	return out, nil
}

// crossingOK says whether a plainly written value from a to z may cross
// the beats between.
func crossingOK(a, z score.Frac, br bar, rest bool) bool {
	mid := br.middle()
	for _, bt := range br.beats {
		if !a.Less(bt) || !bt.Less(z) {
			continue
		}
		switch {
		case rest:
			if !br.onBeat(a) || !br.onBeat(z) || bt.Eq(mid) {
				return false
			}
		case br.grouped, bt.Eq(mid):
			if !br.onBeat(a) {
				return false
			}
		}
	}
	return true
}

// spell spells each chord's pitches in the key of the bar it starts in,
// sharps on the way up and flats on the way down, lowest first.
func spell(chords []Chord, l Layout) [][]score.Pitch {
	out := make([][]score.Pitch, len(chords))
	prev := -1
	for i, c := range chords {
		ms := append([]int(nil), c.MIDI...)
		sort.Ints(ms)
		fifths := 0
		if l.Bars() > 0 {
			fifths = l.Fifths[l.BarAt(c.Start)]
		}
		dir := 0
		if prev >= 0 && len(ms) > 0 {
			switch top := ms[len(ms)-1]; {
			case top > prev:
				dir = 1
			case top < prev:
				dir = -1
			}
		}
		ps := make([]score.Pitch, 0, len(ms))
		for j, m := range ms {
			if j > 0 && m == ms[j-1] {
				continue
			}
			ps = append(ps, score.SpellMIDI(m, fifths, dir))
		}
		out[i] = ps
		if len(ms) > 0 {
			prev = ms[len(ms)-1]
		}
	}
	return out
}

// spellGraces spells a chord's grace notes in the key: sharps leading up
// to the chord, flats leading down.
func spellGraces(c *Chord, main []score.Pitch, fifths int) [][]score.Pitch {
	if len(c.Graces) == 0 {
		return nil
	}
	target := 0
	for _, p := range main {
		target = max(target, p.MIDI())
	}
	out := make([][]score.Pitch, len(c.Graces))
	for i, g := range c.Graces {
		ms := append([]int(nil), g...)
		sort.Ints(ms)
		for _, m := range ms {
			dir := 0
			switch {
			case m < target:
				dir = 1
			case m > target:
				dir = -1
			}
			out[i] = append(out[i], score.SpellMIDI(m, fifths, dir))
		}
	}
	return out
}

// Fill appends silence of a length to a measure at an offset into its bar
// — the gaps an importer that reads notation finds between a voice's notes
// (a MusicXML <forward>) — split into values the meter allows, in a tuplet
// when the length needs one.
func Fill(m *score.Measure, kind score.EventKind, at, length score.Frac, meter score.Meter, barLen score.Frac) (err error) {
	defer score.RecoverOverflow(&err)
	if length.Sign() <= 0 {
		return nil
	}
	l := Layout{Starts: []score.Frac{{}, barLen}, Meters: []score.Meter{meter}, Fifths: []int{0}}
	br := l.bar(0)
	gaps := Rests
	if kind == score.EventSpace {
		gaps = Spaces
	}
	sub, err := writeBar([]span{{a: at, b: at.Add(length)}}, br, gaps)
	if err != nil {
		return err
	}
	base := len(m.Tuplets)
	m.Tuplets = append(m.Tuplets, sub.Tuplets...)
	for _, e := range sub.Events {
		if e.Tuplet > 0 {
			e.Tuplet += base
		}
		m.Events = append(m.Events, e)
	}
	return nil
}

func oddPart(n int64) int {
	if n < 0 {
		n = -n
	}
	for n > 1 && n%2 == 0 {
		n /= 2
	}
	if n == 0 || n > 1<<20 {
		return 1
	}
	return int(n)
}

func lcm(a, b int) int {
	g, h := a, b
	for h != 0 {
		g, h = h, g%h
	}
	return a / g * b
}
