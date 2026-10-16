package interpret

// expression.go — what the marks on notes do to how long they sound, what
// pitches they play and when.
//
// The passes run over each voice's events in playing order, after the
// walk has merged ties and placed grace notes, in this order:
//
//   - lengths: staccato sounds Playback.Staccato of its written length (a
//     half by default), staccatissimo half that, portato (staccato and
//     tenuto, or "portato") and staccato under a slur 3/4, marcato 4/5.
//     A tied note takes the marks of its last tied event. Under a slur
//     each note but the slur's last runs a 64th into the next (legato) and
//     asks for the instrument's "legato" articulation.
//   - glissandi: a `gliss` goes from its event to the next one, chord note
//     to chord note from the top. The line's Gliss style (else the
//     instrument's, else a slide) says how: a slide bends the first note
//     to the second's pitch over the last three quarters of its event and
//     carries on as the second note, unbroken (unless the second is an
//     ornament or a tremolo, which strikes as itself); a chromatic or diatonic run
//     plays every semitone, or every note of the key, between them in that
//     time.
//   - ornaments, on a chord's top note: a trill (`tr`) alternates with the
//     note above about 12 notes a second, starting and ending on the
//     note; a mordent (`mord`) is note, note below, note in 32nds; an
//     inverted one (`imord`) uses the note above; a turn (`turn`) is above,
//     note, below, note in 16ths (`iturn` from below). The neighbours are
//     the key's, after accidentals earlier in the bar on the staff; the
//     mark's value can name theirs (score.OrnamentAccidentals).
//   - tremolos: `trem=n` re-strikes each note at its value with n more
//     beams (a quarter with 3 strokes in 32nds); an unmeasured tremolo
//     (3 strokes or more) on an instrument with a "tremolo" articulation
//     plays that instead. `trem2=n` alternates this event's notes with the
//     next event's over both their lengths.
//   - arpeggios: `arp` rolls the chord from the bottom (`arp=down` from
//     the top), each note a 64th after the last (less in a short chord),
//     all ending together.
//   - the pedal, per line: a note that ends while the pedal is down (`ped`
//     … `ped_end`, inclusive; a `ped` while it is down re-pedals) sounds
//     until it comes up.

import (
	"math"
	"sort"

	"foundry/internal/score"
	"foundry/internal/tracker"
)

// legatoOverlap is how far a slurred note runs into the next one.
var legatoOverlap = score.F(1, 64)

// arpeggioSpread is the most each note of an arpeggio waits after the
// one before.
var arpeggioSpread = score.F(1, 64)

// trillRate is notes a second in a trill.
const trillRate = 12.0

// maxStrikes caps the notes one trill or tremolo becomes (a cadenza bar of
// 32 whole notes at a crawl would otherwise be thousands).
const maxStrikes = 1001

// express runs the passes.
func (in *interp) express() {
	for vi := range in.voices {
		ve := &in.voices[vi]
		in.lengths(ve)
		in.glissandi(ve)
		in.ornaments(ve)
		in.tremolos(ve)
		in.arpeggios(ve)
	}
	in.pedals()
}

// instrument is a line's instrument, nil when not known.
func (in *interp) instrument(line int) *tracker.Instrument {
	if line < len(in.o.Instruments) {
		return in.o.Instruments[line]
	}
	return nil
}

func (in *interp) staccato(line int) score.Frac {
	in.instDep[line] = true
	f := float32(0.5)
	if inst := in.instrument(line); inst != nil {
		f = inst.Playback.StaccatoLength()
	}
	// Exact enough: hundredths of the written length.
	return score.F(int64(math.Round(float64(f)*100)), 100)
}

// lengthFactor is how much of its written length a note's marks let it
// sound, 1 for all of it.
func (in *interp) lengthFactor(line int, marks []score.Mark, slurred bool) score.Frac {
	has := func(n string) bool {
		for _, m := range marks {
			if m.Name == n {
				return true
			}
		}
		return false
	}
	switch {
	case has("portato") || has("stacc") && has("ten"):
		return score.F(3, 4)
	case has("staccmo"):
		return in.staccato(line).Div(score.Whole(2))
	case has("stacc") && slurred:
		return score.F(3, 4)
	case has("stacc"):
		return in.staccato(line)
	case has("marc"):
		return score.F(4, 5)
	}
	return score.Whole(1)
}

// lengths applies articulation lengths and slurs.
func (in *interp) lengths(ve *voiceEvents) {
	evs := ve.evs
	// legato[k]: event k is under a slur and runs into the next; under[k]:
	// event k is under a slur at all.
	legato := make([]bool, len(evs))
	under := make([]bool, len(evs))
	on := false
	for k, x := range evs {
		if x.e.Has("slur") {
			on = true
		}
		under[k] = on
		legato[k] = on && !x.e.Has("slur_end")
		if x.e.Has("slur_end") {
			on = false
		}
	}
	last := map[int]int{} // note → the last event it sounds through
	for k, x := range evs {
		for _, idx := range x.notes {
			last[idx] = k
		}
	}
	idxs := make([]int, 0, len(last))
	for idx := range last {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	for _, idx := range idxs {
		k := last[idx]
		n := &in.notes[idx]
		if f := in.lengthFactor(ve.line, n.segMarks, under[k]); f.Less(score.Whole(1)) {
			n.dur = n.segAt.Sub(n.at).Add(n.segDur.Mul(f))
			continue
		}
		if !legato[k] || k+1 >= len(evs) || evs[k+1].e.Kind != score.EventChord {
			continue
		}
		next := evs[k+1]
		if !n.at.Add(n.dur).Eq(next.at) {
			continue
		}
		n.dur = n.dur.Add(score.MinFrac(legatoOverlap, next.dur))
		if n.art == "" {
			n.art = "legato"
		}
	}
}

// glissStyle is how a line's glissandi play.
func (in *interp) glissStyle(line int) tracker.GlissStyle {
	g := in.lineGliss(line)
	if g == tracker.GlissSlide && in.o.NoSlides {
		return tracker.GlissChromatic
	}
	return g
}

func (in *interp) lineGliss(line int) tracker.GlissStyle {
	switch in.s.Lines[line].Gliss {
	case "slide":
		return tracker.GlissSlide
	case "chromatic":
		return tracker.GlissChromatic
	case "diatonic":
		return tracker.GlissDiatonic
	}
	in.instDep[line] = true
	if inst := in.instrument(line); inst != nil {
		return inst.Playback.Gliss
	}
	return tracker.GlissSlide
}

// byPitch is a chord's sounding notes, highest first.
func (in *interp) byPitch(idxs []int) []int {
	var out []int
	for _, i := range idxs {
		i = in.resolve(i)
		if !in.notes[i].drop {
			out = append(out, i)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return in.pitchNow(out[a]) > in.pitchNow(out[b]) })
	return out
}

// pitchNow is where a note's pitch has got to: its MIDI number moved by
// its last bend.
func (in *interp) pitchNow(i int) float32 {
	n := &in.notes[i]
	if k := len(n.bends); k > 0 {
		return float32(n.midi) + n.bends[k-1].semis
	}
	return float32(n.midi)
}

func (in *interp) glissandi(ve *voiceEvents) {
	evs := ve.evs
	for k := 0; k+1 < len(evs); k++ {
		x, y := evs[k], evs[k+1]
		if !x.e.Has("gliss") || x.e.Kind != score.EventChord || y.e.Kind != score.EventChord {
			continue
		}
		style := in.glissStyle(ve.line)
		src, tgt := in.byPitch(x.notes), in.byPitch(y.notes)
		start := x.mainAt.Add(x.mainDur.Div(score.Whole(4)))
		fifths := in.s.KeyAt(x.bar).Fifths
		for p := 0; p < len(src) && p < len(tgt); p++ {
			si, ti := src[p], tgt[p]
			if si == ti {
				continue
			}
			from := in.pitchNow(si)
			to := in.notes[ti].midi
			if style == tracker.GlissSlide {
				s, t := &in.notes[si], &in.notes[ti]
				s.bends = append(s.bends, bendAt{start, from - float32(s.midi)}, bendAt{y.mainAt, float32(to - s.midi)})
				if restrikes(y.e) {
					// The target re-strikes (a trill, a tremolo): it
					// keeps its own attack, the slide arriving at it.
					continue
				}
				s.dur = t.at.Add(t.dur).Sub(s.at)
				s.segAt, s.segDur, s.segMarks = t.segAt, t.segDur, t.segMarks
				t.drop, t.into = true, si+1
				continue
			}
			steps := glissSteps(int(math.Round(float64(from))), to, style, fifths)
			if len(steps) == 0 || !start.Less(y.mainAt) {
				continue
			}
			s := &in.notes[si]
			if start.Less(s.at.Add(s.dur)) && s.at.Less(start) {
				s.dur = start.Sub(s.at)
			}
			each := y.mainAt.Sub(start).Div(score.Whole(int64(len(steps))))
			base := *s
			dir := 1
			if to < base.midi {
				dir = -1
			}
			for j, m := range steps {
				at := start.Add(each.MulInt(int64(j)))
				in.notes = append(in.notes, rawNote{at: at, dur: each, velAt: at, pitch: score.SpellMIDI(m, fifths, dir), midi: m,
					line: base.line, staff: base.staff, voice: base.voice, bar: base.bar, pass: base.pass, art: base.art,
					accent: 1, segAt: at, segDur: each, shaped: true})
			}
		}
	}
}

// restrikes reports an event whose marks make it a run of notes (an
// ornament or a tremolo): a slide into it cannot carry on as it.
func restrikes(e *score.Event) bool {
	for _, m := range e.Marks {
		switch m.Name {
		case "tr", "mord", "imord", "turn", "iturn", "trem", "trem2":
			return true
		}
	}
	return false
}

// glissSteps are the pitches strictly between two notes a run passes
// through: every semitone, or every note of the key.
func glissSteps(from, to int, style tracker.GlissStyle, fifths int) []int {
	dir := 1
	if to < from {
		dir = -1
	}
	tonic := ((7*fifths)%12 + 12) % 12
	inKey := func(m int) bool {
		switch (((m - tonic) % 12) + 12) % 12 {
		case 0, 2, 4, 5, 7, 9, 11:
			return true
		}
		return false
	}
	var out []int
	for m := from + dir; m != to && len(out) < 128; m += dir {
		if style == tracker.GlissDiatonic && !inKey(m) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// neighbour is the note a step above (dir 1) or below a note of a staff
// at a point: the letter next to it, with the accidental in force there
// unless the ornament names one.
func (in *interp) neighbour(n *rawNote, x vev, dir int, alter int, named bool) score.Pitch {
	p := score.FromDiatonic(n.pitch.Diatonic() + dir)
	if named {
		p.Alter = int8(alter)
	} else {
		st := in.s.Lines[n.line].Staves[n.staff]
		p.Alter = int8(in.s.AlterInForce(st, x.bar, x.off, p.Diatonic()))
	}
	return p
}

// ornamentNames are the ornaments a chord's top note can carry.
var ornamentNames = []string{"tr", "mord", "imord", "turn", "iturn"}

func (in *interp) ornaments(ve *voiceEvents) {
	for _, x := range ve.evs {
		if x.e.Kind != score.EventChord {
			continue
		}
		var orn score.Mark
		found := false
		for _, name := range ornamentNames {
			for _, m := range x.e.Marks {
				if m.Name == name && !found {
					orn, found = m, true
				}
			}
		}
		if !found {
			continue
		}
		top := in.byPitch(x.notes)
		if len(top) == 0 || in.notes[top[0]].shaped || len(in.notes[top[0]].bends) > 0 {
			continue
		}
		idx := top[0]
		n := in.notes[idx]
		up, low, hasUp, hasLow, _ := score.OrnamentAccidentals(orn.Value)
		if orn.Name == "mord" && hasUp && !hasLow {
			low, hasLow = up, true // a mordent's one accidental is its lower note's
		}
		upper := in.neighbour(&n, x, 1, up, hasUp).MIDI()
		lower := in.neighbour(&n, x, -1, low, hasLow).MIDI()
		main := n.midi
		var seq []int
		var lens []score.Frac
		switch orn.Name {
		case "tr":
			secs := in.tempo.Seconds(n.at.Add(n.dur).TicksFloat()) - in.tempo.Seconds(n.at.TicksFloat())
			count := min(max(3, int(math.Round(secs*trillRate))), maxStrikes)
			if count%2 == 0 {
				count++
			}
			each := n.dur.Div(score.Whole(int64(count)))
			for j := 0; j < count; j++ {
				p := main
				if j%2 == 1 {
					p = upper
				}
				seq = append(seq, p)
				lens = append(lens, each)
			}
		case "mord", "imord":
			aux := lower
			if orn.Name == "imord" {
				aux = upper
			}
			o := score.MinFrac(n.dur.Div(score.Whole(4)), score.F(1, 32))
			seq = []int{main, aux, main}
			lens = []score.Frac{o, o, n.dur.Sub(o.MulInt(2))}
		case "turn", "iturn":
			a, b := upper, lower
			if orn.Name == "iturn" {
				a, b = lower, upper
			}
			o := score.MinFrac(n.dur.Div(score.Whole(4)), score.F(1, 16))
			seq = []int{a, main, b, main}
			lens = []score.Frac{o, o, o, n.dur.Sub(o.MulInt(3))}
		}
		in.split(idx, seq, lens)
	}
}

// split turns a note into a run of notes, pitches seq, lengths lens, the
// first keeping the note's own place, velocity and accent.
func (in *interp) split(idx int, seq []int, lens []score.Frac) {
	if len(seq) == 0 {
		return
	}
	n := &in.notes[idx]
	base := *n
	at := base.at
	n.midi, n.dur, n.shaped = seq[0], lens[0], true
	n.pitch = score.SpellMIDI(seq[0], 0, 1)
	at = at.Add(lens[0])
	for j := 1; j < len(seq); j++ {
		r := base
		r.at, r.dur, r.velAt, r.midi, r.accent, r.shaped = at, lens[j], at, seq[j], 1, true
		r.pitch = score.SpellMIDI(seq[j], 0, 1)
		r.segAt, r.segDur = at, lens[j]
		r.grace = score.NotGrace
		in.notes = append(in.notes, r)
		at = at.Add(lens[j])
	}
}

// strokeValue is the note value a tremolo of n strokes re-strikes at on a
// note of value d: d's beams plus n more.
func strokeValue(d score.Duration, strokes int) score.Frac {
	b := max(int(d.Base), int(score.Quarter)) + strokes
	return score.F(1, int64(1)<<uint(min(b, 10)))
}

func (in *interp) tremolos(ve *voiceEvents) {
	evs := ve.evs
	for k, x := range evs {
		if x.e.Kind != score.EventChord {
			continue
		}
		for _, m := range x.e.Marks {
			strokes, ok := score.TremoloStrokes(m.Value)
			if !ok {
				continue
			}
			switch m.Name {
			case "trem":
				in.tremolo(ve.line, x, strokes)
			case "trem2":
				if k+1 < len(evs) && evs[k+1].e.Kind == score.EventChord {
					in.tremolo2(x, evs[k+1], strokes)
				}
			}
		}
	}
}

// tremolo re-strikes an event's notes, or gives them the instrument's
// tremolo articulation when the tremolo is unmeasured and it has one.
func (in *interp) tremolo(line int, x vev, strokes int) {
	if strokes >= 3 {
		in.instDep[line] = true
		if inst := in.instrument(line); inst != nil && inst.ArticulationIndex("tremolo") > 0 {
			for _, idx := range x.notes {
				in.notes[in.resolve(idx)].art = "tremolo"
			}
			return
		}
	}
	sub := strokeValue(x.e.Dur, strokes)
	for _, idx := range in.byPitch(x.notes) {
		n := in.notes[idx]
		if n.shaped || len(n.bends) > 0 {
			continue
		}
		count := min(max(1, n.dur.Div(sub).Round()), maxStrikes)
		each := n.dur.Div(score.Whole(count))
		seq := make([]int, count)
		lens := make([]score.Frac, count)
		for j := range seq {
			seq[j], lens[j] = n.midi, each
		}
		in.notes[idx].art = ""
		in.split(idx, seq, lens)
	}
}

// tremolo2 alternates two events' notes over both their lengths.
func (in *interp) tremolo2(x, y vev, strokes int) {
	a, b := in.byPitch(x.notes), in.byPitch(y.notes)
	if len(a) == 0 || len(b) == 0 {
		return
	}
	start := x.mainAt
	end := y.mainAt.Add(y.mainDur)
	sub := strokeValue(x.e.Dur, strokes)
	count := min(max(2, end.Sub(start).Div(sub).Round()), maxStrikes-1)
	each := end.Sub(start).Div(score.Whole(count))
	for _, i := range append(append([]int(nil), a...), b...) {
		in.notes[i].drop = true
	}
	for j := int64(0); j < count; j++ {
		group := a
		if j%2 == 1 {
			group = b
		}
		at := start.Add(each.MulInt(j))
		for _, i := range group {
			r := in.notes[i]
			r.drop, r.shaped = false, true
			r.at, r.dur, r.velAt, r.segAt, r.segDur = at, each, at, at, each
			if j > 0 {
				r.accent = 1
			}
			in.notes = append(in.notes, r)
		}
	}
}

func (in *interp) arpeggios(ve *voiceEvents) {
	for _, x := range ve.evs {
		if x.e.Kind != score.EventChord || len(x.notes) < 2 {
			continue
		}
		down := false
		has := false
		for _, m := range x.e.Marks {
			if m.Name == "arp" {
				has, down = true, m.Value == "down"
			}
		}
		if !has {
			continue
		}
		idxs := in.byPitch(x.notes) // highest first
		if !down {
			for i, j := 0, len(idxs)-1; i < j; i, j = i+1, j-1 {
				idxs[i], idxs[j] = idxs[j], idxs[i]
			}
		}
		spread := score.MinFrac(arpeggioSpread, x.mainDur.Div(score.Whole(int64(2*len(idxs)))))
		for j, idx := range idxs {
			n := &in.notes[idx]
			d := spread.MulInt(int64(j))
			if d.Less(n.dur) {
				n.at, n.dur = n.at.Add(d), n.dur.Sub(d)
			}
		}
	}
}

// pedals holds every note that ends under a line's pedal until it lifts.
func (in *interp) pedals() {
	var end score.Frac
	if n := len(in.order); n > 0 {
		end = in.order[n-1].start.Add(in.order[n-1].length)
	}
	for li := range in.lines {
		marks := in.lines[li].pedal
		if len(marks) == 0 {
			continue
		}
		sort.SliceStable(marks, func(i, j int) bool { return marks[i].at.Less(marks[j].at) })
		var spans [][2]score.Frac
		open := false
		var from score.Frac
		for _, m := range marks {
			if open && from.Less(m.at) {
				spans = append(spans, [2]score.Frac{from, m.at})
			}
			open, from = m.down, m.at
		}
		if open && from.Less(end) {
			spans = append(spans, [2]score.Frac{from, end})
		}
		for i := range in.notes {
			n := &in.notes[i]
			if n.line != li || n.drop {
				continue
			}
			e := n.at.Add(n.dur)
			for _, sp := range spans {
				if sp[0].Less(e) && e.Less(sp[1]) {
					n.dur = sp[1].Sub(n.at)
					break
				}
			}
		}
	}
}
