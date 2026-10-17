package edit

// roll.go — a line's notes as a piano roll sees and edits them.
//
// The piano roll shows each note as a bar of time: a pitch held from where
// it starts to where its last tied part stops. RollNotes reads a line that
// way — every staff and voice, tie chains merged into one note — and the
// edits here (add, remove, replace, velocity) change those timed notes and
// write the result back as notation.
//
// Writing back is the importers' step (internal/score/io/notate): the bars
// an edit touches, widened to whole tie chains, are cut wherever any note
// in them starts or stops, each stretch becomes a chord of the notes
// sounding through it (the ones that carry on tied into the next), and
// notate.Write turns that into note values the meter allows, tied across
// beats and barlines, with tuplet brackets where an onset needs one (a
// note drawn on a triplet grid comes out as a triplet). What was written
// there is kept where it still applies: each note's spelling, accidental
// display, its event's marks and grace notes at the same onset, the marks
// on a rest. A note drawn new is spelled by the caller (in the key).
//
// Within one voice notes cannot overlap unless they start and stop
// together (a chord). Writing over a voice works as note entry does: what
// the new note lands on is cut short, what it covers goes. The editor asks
// RollFreeVoice first and puts a note that would overlap into another
// voice, so drawing in a piano roll loses nothing.

import (
	"fmt"
	"sort"

	"foundry/internal/score"
	"foundry/internal/score/io/notate"
)

// RollPart is one written event a roll note sounds in, and the note's index
// in its chord.
type RollPart struct {
	Loc  Loc
	Note int
}

// RollNote is one note as the piano roll shows it.
type RollNote struct {
	Staff, Voice int
	// Start and End are whole notes from the top of the score in written
	// order (no repeats): where the note starts, and where its last tied
	// part stops.
	Start, End score.Frac
	// Pitch is the note as stored (concert pitch), spelled.
	Pitch score.Pitch
	// Vel is the velocity its first event sets (vel=), 0 when the line's
	// dynamics set it.
	Vel int
	// Parts are the events it is written in, its attack first.
	Parts []RollPart
	// Marks and Graces are what a note moved on its own takes with it: its
	// event's marks and the grace notes before it (RollReplace fills them;
	// RollAdd writes them at the new place).
	Marks  []score.Mark
	Graces []score.Event
}

// MIDI is the note's MIDI key.
func (n RollNote) MIDI() int { return n.Pitch.MIDI() }

// Length is how long the note sounds, in whole notes.
func (n RollNote) Length() score.Frac { return n.End.Sub(n.Start) }

// same reports the same note: one place, one pitch.
func (n RollNote) same(o RollNote) bool {
	return n.Staff == o.Staff && n.Voice == o.Voice && n.Start.Eq(o.Start) && n.MIDI() == o.MIDI()
}

// RollNotes are line li's notes on every staff and voice, in time order
// (then by staff, voice and pitch). Grace notes are not on the roll.
func RollNotes(s *score.Score, li int) []RollNote {
	if li < 0 || li >= len(s.Lines) {
		return nil
	}
	starts := s.BarStarts()
	var out []RollNote
	for si, st := range s.Lines[li].Staves {
		for vi := range st.Voices {
			out = append(out, rollVoice(s, li, si, vi, starts)...)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if c := a.Start.Cmp(b.Start); c != 0 {
			return c < 0
		}
		if a.Staff != b.Staff {
			return a.Staff < b.Staff
		}
		if a.Voice != b.Voice {
			return a.Voice < b.Voice
		}
		return a.MIDI() < b.MIDI()
	})
	return out
}

// rollVoice reads one voice's notes, tie chains merged.
func rollVoice(s *score.Score, li, si, vi int, starts []score.Frac) []RollNote {
	v := Voice(s, li, si, vi)
	if v == nil {
		return nil
	}
	var out []RollNote
	pending := map[int]int{}
	for b := 1; b <= len(v.Measures) && b < len(starts); b++ {
		m := v.Measures[b-1]
		barLen := s.BarLength(b)
		offs, _ := m.Offsets(barLen)
		for ei := range m.Events {
			e := &m.Events[ei]
			if e.Grace != score.NotGrace {
				continue
			}
			if e.Kind != score.EventChord {
				pending = map[int]int{}
				continue
			}
			t0 := starts[b-1].Add(offs[ei])
			t1 := t0.Add(m.Actual(*e, barLen))
			next := map[int]int{}
			for k, nt := range e.Notes {
				midi := nt.Pitch.MIDI()
				part := RollPart{Loc: Loc{Line: li, Staff: si, Voice: vi, Bar: b, Event: ei}, Note: k}
				i, ok := pending[midi]
				if ok && out[i].End.Eq(t0) {
					out[i].End = t1
					out[i].Parts = append(out[i].Parts, part)
				} else {
					out = append(out, RollNote{Staff: si, Voice: vi, Start: t0, End: t1, Pitch: nt.Pitch, Vel: e.VelocityMark(), Parts: []RollPart{part}})
					i = len(out) - 1
				}
				if nt.Tie {
					next[midi] = i
				}
			}
			pending = next
		}
	}
	return out
}

// RollFreeVoice is the voice of a staff that can take a note from start
// to end without cutting another: prefer if it can, else the lowest that
// can, -1 when none of the four can. A voice can when no note in it
// overlaps the span, or the notes that do start and stop with it (a
// chord). Voices a staff does not have yet count as free.
func RollFreeVoice(s *score.Score, li, staff int, start, end score.Frac, prefer int) int {
	st := Staff(s, li, staff)
	if st == nil {
		return -1
	}
	starts := s.BarStarts()
	free := func(vi int) bool {
		if vi >= len(st.Voices) {
			return true
		}
		for _, n := range rollVoice(s, li, staff, vi, starts) {
			if n.Start.Less(end) && start.Less(n.End) && !(n.Start.Eq(start) && n.End.Eq(end)) {
				return false
			}
		}
		return true
	}
	if prefer >= 0 && prefer < score.MaxVoices && free(prefer) {
		return prefer
	}
	for vi := 0; vi < score.MaxVoices; vi++ {
		if free(vi) {
			return vi
		}
	}
	return -1
}

// RollAdd writes a note into its staff and voice (n.Staff, n.Voice) from
// n.Start to n.End, with n.Pitch's spelling and velocity n.Vel (0: the
// dynamics'), and returns it as written. Notes of the voice it overlaps
// are cut or go, as in note entry; one starting and stopping with it
// makes a chord with it.
func RollAdd(s *score.Score, li int, n RollNote) (RollNote, error) {
	if err := rollCheck(s, li, n); err != nil {
		return n, err
	}
	if err := rewrite(s, li, n.Staff, n.Voice, nil, []RollNote{n}); err != nil {
		return n, err
	}
	return rollFind(s, li, n)
}

// RollRemove takes a note out: its events become rests (or lose the
// pitch, in a chord).
func RollRemove(s *score.Score, li int, n RollNote) error {
	if _, err := rollFind(s, li, n); err != nil {
		return err
	}
	return rewrite(s, li, n.Staff, n.Voice, []RollNote{n}, nil)
}

// RollReplace replaces a note with another — moved in time or pitch,
// lengthened or shortened, put on another staff or voice — and returns the
// new one as written. A note that was alone in its event takes the event's
// marks (those of its notes, not of its place) and grace notes with it.
func RollReplace(s *score.Score, li int, old, n RollNote) (RollNote, error) {
	out, err := RollMove(s, li, []RollNote{old}, []RollNote{n}, false)
	if err != nil {
		return n, err
	}
	return out[0], nil
}

// RollMove replaces notes with others, all at once (news[i] replaces
// olds[i]), so moving a run or a chord never has a note land on one of its
// own not yet moved. With free, a new note that would cut another goes
// into a voice of its staff that can take it (RollFreeVoice, its own voice
// first). It returns the new notes as written.
func RollMove(s *score.Score, li int, olds, news []RollNote, free bool) (out []RollNote, err error) {
	if len(olds) != len(news) {
		return nil, fmt.Errorf("%d notes replaced by %d", len(olds), len(news))
	}
	// All or nothing: a move refused part way (no voice free for a note)
	// leaves the score as it was.
	saved := s.Clone()
	defer func() {
		if err != nil {
			*s = *saved
		}
	}()
	found := make([]RollNote, len(olds))
	moving := map[Loc]int{}
	for i, old := range olds {
		if err := rollCheck(s, li, news[i]); err != nil {
			return nil, err
		}
		o, err := rollFind(s, li, old)
		if err != nil {
			return nil, err
		}
		found[i] = o
		moving[o.Parts[0].Loc]++
	}
	for i, o := range found {
		// Every note of an event moving: the notes take the event's own
		// marks (an articulation, a slur) and grace notes with them; what a
		// rest can carry (a dynamic, text) stays where it was written.
		n := &news[i]
		l := o.Parts[0].Loc
		if e := Event(s, l); e != nil && moving[l] == len(e.Notes) && n.Marks == nil && n.Graces == nil {
			n.Marks = noteMarks(e.Marks)
			n.Graces = gracesBefore(s, l)
		}
		if n.Vel == 0 && o.Vel != 0 {
			n.Vel = o.Vel
		}
	}
	// Out first, voice by voice, then in.
	type sv struct{ staff, voice int }
	byVoice := map[sv][]RollNote{}
	var order []sv
	for _, o := range found {
		k := sv{o.Staff, o.Voice}
		if _, ok := byVoice[k]; !ok {
			order = append(order, k)
		}
		byVoice[k] = append(byVoice[k], o)
	}
	for _, k := range order {
		if err := rewrite(s, li, k.staff, k.voice, byVoice[k], nil); err != nil {
			return nil, err
		}
	}
	// In: the new notes of one staff and voice together, so a moved group
	// keeps its shape (a note held through others stays held). With free, a
	// group the voice cannot take whole goes to a voice that can, or note by
	// note to voices that can take each.
	type group struct {
		k  sv
		ix []int
	}
	var groups []group
	at := map[sv]int{}
	for i, n := range news {
		k := sv{n.Staff, n.Voice}
		g, ok := at[k]
		if !ok {
			g = len(groups)
			at[k] = g
			groups = append(groups, group{k: k})
		}
		groups[g].ix = append(groups[g].ix, i)
	}
	for _, g := range groups {
		ns := make([]RollNote, len(g.ix))
		for j, i := range g.ix {
			ns[j] = news[i]
		}
		if free && !voiceTakes(s, li, g.k.staff, g.k.voice, ns) {
			moved := false
			for vi := 0; vi < score.MaxVoices && !moved; vi++ {
				if vi != g.k.voice && voiceTakes(s, li, g.k.staff, vi, ns) {
					for j := range ns {
						ns[j].Voice = vi
					}
					moved = true
				}
			}
			if !moved {
				// Note by note, each written before the next looks, so two
				// never pick one voice they would share badly.
				for j := range ns {
					v := RollFreeVoice(s, li, ns[j].Staff, ns[j].Start, ns[j].End, ns[j].Voice)
					if v < 0 {
						return nil, fmt.Errorf("no voice is free for %s there: all four have notes in the way", ns[j].Pitch)
					}
					ns[j].Voice = v
					if err := rewrite(s, li, g.k.staff, v, nil, ns[j:j+1]); err != nil {
						return nil, err
					}
				}
				for j, i := range g.ix {
					news[i] = ns[j]
				}
				continue
			}
		}
		byV := map[int][]RollNote{}
		var vs []int
		for _, n := range ns {
			if _, ok := byV[n.Voice]; !ok {
				vs = append(vs, n.Voice)
			}
			byV[n.Voice] = append(byV[n.Voice], n)
		}
		for _, v := range vs {
			if err := rewrite(s, li, g.k.staff, v, nil, byV[v]); err != nil {
				return nil, err
			}
		}
		for j, i := range g.ix {
			news[i] = ns[j]
		}
	}
	out = make([]RollNote, len(news))
	for i, n := range news {
		w, err := rollFind(s, li, n)
		if err != nil {
			return nil, err
		}
		out[i] = w
	}
	return out, nil
}

// voiceTakes reports whether a voice can take notes without cutting one
// already in it: each either clear of the voice's notes or starting and
// stopping with the ones it meets (a chord).
func voiceTakes(s *score.Score, li, staff, vi int, ns []RollNote) bool {
	if Staff(s, li, staff) == nil {
		return false
	}
	for _, o := range rollVoice(s, li, staff, vi, s.BarStarts()) {
		for _, n := range ns {
			if o.Start.Less(n.End) && n.Start.Less(o.End) && !(o.Start.Eq(n.Start) && o.End.Eq(n.End) && o.MIDI() != n.MIDI()) {
				return false
			}
		}
	}
	return true
}

// RollSetVelocity sets the velocity a note plays at (1..127), or with 0
// gives it back to the dynamics. It is set on the note's first event, so
// the other notes of a chord share it.
func RollSetVelocity(s *score.Score, li int, n RollNote, vel int) error {
	o, err := rollFind(s, li, n)
	if err != nil {
		return err
	}
	e := Event(s, o.Parts[0].Loc)
	if e == nil {
		return ErrNoEvent
	}
	e.SetVelocityMark(min(max(vel, 0), 127))
	return nil
}

// rollCheck refuses a note that cannot be written.
func rollCheck(s *score.Score, li int, n RollNote) error {
	if Staff(s, li, n.Staff) == nil {
		return fmt.Errorf("no staff %d of line %d", n.Staff+1, li+1)
	}
	if n.Voice < 0 || n.Voice >= score.MaxVoices {
		return fmt.Errorf("a staff has voices 1 to %d", score.MaxVoices)
	}
	if n.Start.Sign() < 0 || !n.Start.Less(n.End) {
		return fmt.Errorf("a note needs a length")
	}
	if !Playable(n.Pitch) {
		return fmt.Errorf("%s is out of range", PitchName(n.Pitch))
	}
	if n.Vel < 0 || n.Vel > 127 {
		return fmt.Errorf("velocity %d: want 1..127", n.Vel)
	}
	return nil
}

// rollFind finds a note as it is now: the same staff, voice, start and
// pitch.
func rollFind(s *score.Score, li int, n RollNote) (RollNote, error) {
	for _, o := range rollVoice(s, li, n.Staff, n.Voice, s.BarStarts()) {
		if o.same(n) {
			return o, nil
		}
	}
	return n, fmt.Errorf("no %s there", PitchName(n.Pitch))
}

// gracesBefore are the grace notes written just before an event.
func gracesBefore(s *score.Score, l Loc) []score.Event {
	v := Voice(s, l.Line, l.Staff, l.Voice)
	if v == nil || l.Bar < 1 || l.Bar > len(v.Measures) {
		return nil
	}
	m := v.Measures[l.Bar-1]
	k := l.Event
	for k > 0 && m.Events[k-1].Grace != score.NotGrace {
		k--
	}
	var out []score.Event
	for _, e := range m.Events[k:l.Event] {
		out = append(out, e.Clone())
	}
	return out
}

// onsetEvents are what a voice has written at each onset of bars a..z
// (whole notes from the top): the main event there, and the grace notes
// before it.
type onsetEvent struct {
	ev     score.Event
	graces []score.Event
}

func onsets(s *score.Score, v *score.Voice, a, z int, starts []score.Frac) map[score.Frac]onsetEvent {
	out := map[score.Frac]onsetEvent{}
	for b := a; b <= z && b <= len(v.Measures); b++ {
		m := v.Measures[b-1]
		offs, _ := m.Offsets(s.BarLength(b))
		var graces []score.Event
		for i, e := range m.Events {
			if e.Grace != score.NotGrace {
				graces = append(graces, e.Clone())
				continue
			}
			out[fk(starts[b-1].Add(offs[i]))] = onsetEvent{ev: e.Clone(), graces: graces}
			graces = nil
		}
	}
	return out
}

// restMarks keeps, of a chord's marks, those a rest can carry when its
// note goes: dynamics, text, technique, fermatas, hairpins, pedal and
// ottava lines.
func restMarks(ms []score.Mark) []score.Mark {
	var out []score.Mark
	for _, m := range ms {
		switch m.Kind() {
		case score.MarkDynamic, score.MarkText, score.MarkTechnique:
			out = append(out, m)
		case score.MarkNotation:
			if m.Name == "ferm" {
				out = append(out, m)
			}
		case score.MarkSpanner:
			switch m.Name {
			case "slur", "slur_end", "gliss":
			default:
				out = append(out, m)
			}
		}
	}
	return out
}

// noteMarks are the marks that belong to a chord's notes rather than to
// its place: what restMarks leaves (and no vel=, which is the note's own
// velocity, carried by RollNote.Vel).
func noteMarks(ms []score.Mark) []score.Mark {
	keep := restMarks(ms)
	var out []score.Mark
	for _, m := range ms {
		stays := m.Name == "vel"
		for _, k := range keep {
			if k == m {
				stays = true
			}
		}
		if !stays {
			out = append(out, m)
		}
	}
	return out
}

// barOfTime is the bar (1-based) holding a time, the last bar for the end.
func barOfTime(starts []score.Frac, t score.Frac) int {
	b := sort.Search(len(starts), func(i int) bool { return t.Less(starts[i]) })
	return min(max(b, 1), len(starts)-1)
}

// rewrite takes notes out of and puts notes into one voice and writes the
// bars it touched again (see the file comment).
func rewrite(s *score.Score, li, si, vi int, remove, add []RollNote) (err error) {
	defer score.RecoverOverflow(&err)
	v, err := EnsureVoice(s, li, si, vi)
	if err != nil {
		return err
	}
	// Room for a note past the last bar.
	for _, n := range add {
		for {
			starts := s.BarStarts()
			if !starts[len(starts)-1].Less(n.End) {
				break
			}
			if s.Measures() >= score.MaxBars {
				return fmt.Errorf("the score holds %d bars", score.MaxBars)
			}
			EnsureBars(s, s.Measures()+1)
		}
	}
	starts := s.BarStarts()
	old := rollVoice(s, li, si, vi, starts)

	// The bars: every note taken out or put in, widened to whole tie chains.
	a, z := len(starts), 0
	cover := func(from, to score.Frac) {
		a = min(a, barOfTime(starts, from))
		last := to
		if from.Less(to) {
			// The bar the note's last instant is in.
			last = to.Sub(score.F(1, 1<<20))
		}
		z = max(z, barOfTime(starts, last))
	}
	for _, n := range remove {
		cover(n.Start, n.End)
	}
	for _, n := range add {
		cover(n.Start, n.End)
	}
	if z == 0 {
		return nil
	}
	for grown := true; grown; {
		grown = false
		t0, t1 := starts[a-1], starts[z]
		for _, n := range old {
			if n.Start.Less(t1) && t0.Less(n.End) && (n.Start.Less(t0) || t1.Less(n.End)) {
				na, nz := a, z
				cover(n.Start, n.End)
				grown = grown || a != na || z != nz
			}
		}
	}
	t0, t1 := starts[a-1], starts[z]

	// The notes of those bars after the edit.
	var notes []RollNote
	for _, n := range old {
		if n.Start.Less(t0) || !n.Start.Less(t1) {
			continue
		}
		gone := false
		for _, r := range remove {
			if n.same(r) {
				gone = true
			}
		}
		if !gone {
			notes = append(notes, n)
		}
	}
	// What the new notes land on is cut short, what they cover goes; the
	// new notes leave each other alone (a moved group keeps its shape).
	for _, n := range add {
		var kept []RollNote
		for _, k := range notes {
			switch {
			case k.MIDI() == n.MIDI() && k.Start.Less(n.End) && n.Start.Less(k.End):
				// The same pitch where the new note sounds: the new one wins.
				if k.Start.Less(n.Start) {
					k.End = n.Start
					kept = append(kept, k)
				}
			case k.Start.Eq(n.Start) && k.End.Eq(n.End), !k.Start.Less(n.End), !n.Start.Less(k.End):
				kept = append(kept, k)
			case k.Start.Less(n.Start):
				k.End = n.Start
				kept = append(kept, k)
			}
		}
		notes = kept
	}
	notes = append(notes, add...)

	// Cut the bars wherever a note starts or stops; each stretch is a chord.
	cuts := []score.Frac{t0, t1}
	for _, n := range notes {
		cuts = append(cuts, n.Start, score.MinFrac(n.End, t1))
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].Less(cuts[j]) })
	was := onsets(s, v, a, z, starts)
	var chords []notate.Chord
	for i := 0; i+1 < len(cuts); i++ {
		x, y := cuts[i], cuts[i+1]
		if !x.Less(y) {
			continue
		}
		var c notate.Chord
		seen := map[int]bool{}
		attack := false
		for _, n := range notes {
			if n.Start.Cmp(x) > 0 || n.End.Cmp(y) < 0 || seen[n.MIDI()] {
				continue
			}
			seen[n.MIDI()] = true
			c.MIDI = append(c.MIDI, n.MIDI())
			c.Pitches = append(c.Pitches, n.Pitch)
			if y.Less(n.End) {
				c.TieNext = append(c.TieNext, n.MIDI())
			}
			if n.Start.Eq(x) {
				attack = true
				if n.Vel > 0 {
					c.Velocity = max(c.Velocity, n.Vel)
				}
				c.Marks = mergeMarks(c.Marks, n.Marks)
				if len(n.Graces) > 0 {
					c.GraceEvents = n.Graces
				}
			}
		}
		if len(c.MIDI) == 0 {
			continue
		}
		if o, ok := was[fk(x)]; ok && attack && o.ev.Kind == score.EventChord {
			c.Marks = mergeMarks(c.Marks, withoutVel(o.ev.Marks))
			if c.GraceEvents == nil {
				c.GraceEvents = o.graces
			}
		}
		if c.Velocity > 0 {
			c.Marks = append(withoutVel(c.Marks), score.Mark{Name: "vel", Value: fmt.Sprint(c.Velocity)})
		}
		sort.Slice(c.Pitches, func(a, b int) bool { return c.Pitches[a].MIDI() < c.Pitches[b].MIDI() })
		c.Start, c.End = x, y
		chords = append(chords, c)
	}

	full := notate.LayoutOf(s)
	sub := notate.Layout{Starts: full.Starts[a-1 : z+1], Meters: full.Meters[a-1 : z], Fifths: full.Fifths[a-1 : z]}
	if a == 1 {
		sub.Pickup = full.Pickup
	}
	w, err := notate.Write(chords, sub, notate.WriteOptions{Gaps: notate.Rests, BarRests: vi == 0})
	if err != nil {
		return err
	}

	// What the new bars keep of the old ones: accidental display, and the
	// marks a rest now standing where a note was can carry.
	type at struct {
		t    score.Frac
		midi int
	}
	accs := map[at]score.AccShow{}
	key := func(t score.Frac, midi int) at { return at{fk(t), midi} }
	for t, o := range was {
		for _, nt := range o.ev.Notes {
			accs[key(t, nt.Pitch.MIDI())] = nt.Acc
		}
	}
	for b := a; b <= z; b++ {
		var m score.Measure
		if k := b - a; k < len(w.Measures) {
			m = w.Measures[k]
		}
		offs, _ := m.Offsets(s.BarLength(b))
		for i := range m.Events {
			e := &m.Events[i]
			if e.Grace != score.NotGrace {
				continue
			}
			t := starts[b-1].Add(offs[i])
			switch e.Kind {
			case score.EventChord:
				for k := range e.Notes {
					if acc, ok := accs[key(t, e.Notes[k].Pitch.MIDI())]; ok {
						e.Notes[k].Acc = acc
					}
				}
			case score.EventRest, score.EventSpace, score.EventBarRest:
				if o, ok := was[fk(t)]; ok {
					ms := o.ev.Marks
					if o.ev.Kind == score.EventChord {
						ms = restMarks(ms)
					}
					e.Marks = mergeMarks(e.Marks, ms)
				}
			}
		}
		for len(v.Measures) < b {
			v.Measures = append(v.Measures, score.Measure{})
		}
		v.Measures[b-1] = m
	}
	v.Trim()
	return nil
}

// fk is a time as a map key: zero spelled one way.
func fk(t score.Frac) score.Frac {
	if t.Num == 0 {
		return score.Frac{Num: 0, Den: 1}
	}
	return t
}

// mergeMarks adds marks not already there (by name and value).
func mergeMarks(into, add []score.Mark) []score.Mark {
	for _, m := range add {
		dup := false
		for _, o := range into {
			if o == m {
				dup = true
				break
			}
		}
		if !dup {
			into = append(into, m)
		}
	}
	return into
}

func withoutVel(ms []score.Mark) []score.Mark {
	var out []score.Mark
	for _, m := range ms {
		if m.Name != "vel" {
			out = append(out, m)
		}
	}
	return out
}
