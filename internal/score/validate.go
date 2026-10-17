package score

// validate.go — what is musically wrong with a score that parsed.
//
// Parse rejects what cannot be read (a bad pitch, a meter that is not one);
// Validate reports what reads but will not play or print as meant: a bar
// that adds up to more than its meter, a tie to nothing, a section that
// names a section that is not there, an instrument the library does not
// have. Errors are things that are wrong; warnings are things that are
// probably wrong (a short first voice, a mark this build does not know).

import (
	"fmt"
	"strings"

	"foundry/internal/tracker"
)

// Severity grades an Issue.
type Severity uint8

const (
	Warning Severity = iota
	Error
)

// Issue is one problem, with where it is (1-based; 0 is "not one place").
type Issue struct {
	Severity Severity
	Line     int
	Staff    int
	Voice    int
	Bar      int
	Msg      string
}

func (i Issue) String() string {
	var where []string
	if i.Line > 0 {
		where = append(where, fmt.Sprintf("line %d", i.Line))
	}
	if i.Staff > 0 {
		where = append(where, fmt.Sprintf("staff %d", i.Staff))
	}
	if i.Voice > 0 {
		where = append(where, fmt.Sprintf("voice %d", i.Voice))
	}
	if i.Bar > 0 {
		where = append(where, fmt.Sprintf("bar %d", i.Bar))
	}
	sev := "warning"
	if i.Severity == Error {
		sev = "error"
	}
	if len(where) == 0 {
		return sev + ": " + i.Msg
	}
	return sev + ": " + strings.Join(where, ", ") + ": " + i.Msg
}

// HasErrors reports whether any issue is an error.
func HasErrors(issues []Issue) bool {
	for _, i := range issues {
		if i.Severity == Error {
			return true
		}
	}
	return false
}

// Validate checks the score. instrumentExists, when not nil, is asked about
// every line's instrument (tracker.InstrumentPath(name) != "" in a game).
func (s *Score) Validate(instrumentExists func(string) bool) (out []Issue) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(*OverflowError); !ok {
				panic(r)
			}
			out = append(out, Issue{Severity: Error, Msg: "the score's time arithmetic overflows (a tuplet nested too deep?)"})
		}
	}()
	add := func(sev Severity, line, staff, voice, bar int, format string, a ...any) {
		out = append(out, Issue{Severity: sev, Line: line, Staff: staff, Voice: voice, Bar: bar, Msg: fmt.Sprintf(format, a...)})
	}
	n := s.Measures()
	inRange := func(bar int) bool { return bar >= 1 && bar <= n }

	for _, m := range s.Meter {
		if !inRange(m.Bar) {
			add(Error, 0, 0, 0, m.Bar, "time signature %s past the last bar (%d)", m.Meter, n)
		}
	}
	for _, k := range s.Key {
		if !inRange(k.Bar) {
			add(Error, 0, 0, 0, k.Bar, "key signature past the last bar (%d)", n)
		}
	}
	for _, t := range s.Tempo {
		if !inRange(t.At.Bar) || !t.At.Offset.Less(s.BarLength(t.At.Bar)) && !t.At.Offset.IsZero() {
			add(Error, 0, 0, 0, t.At.Bar, "tempo mark outside the score")
		}
	}
	seen := map[string]bool{}
	for _, sec := range s.Sections {
		if seen[sec.Name] {
			add(Error, 0, 0, 0, sec.Bar, "two sections named %q", sec.Name)
		}
		seen[sec.Name] = true
		if !inRange(sec.Bar) {
			add(Error, 0, 0, 0, sec.Bar, "section %q past the last bar (%d)", sec.Name, n)
		}
	}
	for i, sec := range s.Sections {
		if sec.Next != "" && s.SectionIndex(sec.Next) < 0 {
			add(Error, 0, 0, 0, sec.Bar, "section %q goes next to %q, which is not a section", sec.Name, sec.Next)
		}
		if i > 0 && sec.Bar < s.Sections[i-1].Bar {
			add(Error, 0, 0, 0, sec.Bar, "sections must be in bar order")
		}
		if i > 0 && sec.Bar == s.Sections[i-1].Bar {
			add(Error, 0, 0, 0, sec.Bar, "sections %q and %q start on the same bar", s.Sections[i-1].Name, sec.Name)
		}
	}
	for _, m := range s.Markers {
		if !inRange(m.At.Bar) {
			add(Error, 0, 0, 0, m.At.Bar, "marker %q past the last bar", m.Name)
		}
	}
	for i, r := range s.Repeats {
		if r.Start < 1 || r.End < r.Start || r.End > n {
			add(Error, 0, 0, 0, r.Start, "repeat %d..%d is not inside bars 1..%d", r.Start, r.End, n)
		}
		if r.Times > 16 {
			add(Error, 0, 0, 0, r.Start, "repeat plays %d times (at most 16)", r.Times)
		}
		if i > 0 && r.Start <= s.Repeats[i-1].End {
			add(Error, 0, 0, 0, r.Start, "repeats overlap (bars %d..%d and %d..%d)", s.Repeats[i-1].Start, s.Repeats[i-1].End, r.Start, r.End)
		}
	}
	s.validateForm(n, add)
	for _, t := range s.Tempo {
		if t.BPM == 0 && !t.Ramp && t.Change() == TempoSet {
			add(Warning, 0, 0, 0, t.At.Bar, "tempo text %q sets no tempo (give it a bpm, or write rit., a tempo …)", t.Text)
		}
		if t.HasTo {
			if !t.Change().Gradual() {
				add(Warning, 0, 0, 0, t.At.Bar, "tempo mark %q has an end (to:) but is not a gradual change", t.Text)
			}
			if !PosLess(t.At, t.To) || !inRange(t.To.Bar) && !(t.To.Bar == n+1 && t.To.Offset.IsZero()) {
				add(Error, 0, 0, 0, t.At.Bar, "tempo change %q ends before it starts or past the score", t.Text)
			}
		}
	}
	params := map[string]bool{}
	for _, p := range s.Params {
		if params[p.Name] {
			add(Error, 0, 0, 0, 0, "two params named %q", p.Name)
		}
		params[p.Name] = true
	}
	for _, b := range s.Bindings {
		if !params[b.Param] {
			add(Error, 0, 0, 0, 0, "binding names param %q, which is not a param", b.Param)
		}
		if s.LineIndex(b.Line) < 0 {
			add(Error, 0, 0, 0, 0, "binding names line %q, which is not a line", b.Line)
		}
		switch b.Target {
		case tracker.BindVolume, tracker.BindPan, tracker.BindSendA, tracker.BindSendB, tracker.BindCutoff:
		default:
			add(Error, 0, 0, 0, 0, "binding target %q (want volume, pan, send_a, send_b or cutoff)", b.Target)
		}
	}

	lineNames := map[string]bool{}
	for li, l := range s.Lines {
		L := li + 1
		if lineNames[strings.ToLower(l.Name)] {
			add(Warning, L, 0, 0, 0, "two lines named %q (scripts find lines by name)", l.Name)
		}
		lineNames[strings.ToLower(l.Name)] = true
		switch {
		case l.Instrument == "":
			add(Error, L, 0, 0, 0, "no instrument")
		case instrumentExists != nil && !instrumentExists(l.Instrument):
			add(Error, L, 0, 0, 0, "no instrument %q in the library", l.Instrument)
		}
		if l.Volume < 0 || l.Volume > 4 {
			add(Warning, L, 0, 0, 0, "volume %.2f", l.Volume)
		}
		if l.Pan < -1 || l.Pan > 1 {
			add(Error, L, 0, 0, 0, "pan %.2f is outside -1..1", l.Pan)
		}
		if l.SendA < 0 || l.SendA > 1 || l.SendB < 0 || l.SendB > 1 {
			add(Warning, L, 0, 0, 0, "sends a %.2f b %.2f: a send is 0..1 (it plays clamped)", l.SendA, l.SendB)
		}
		validateLanes(ScopeLine, l.Lanes, l.Inserts, "the line has no such insert", inRange,
			func(sev Severity, bar int, f string, a ...any) { add(sev, L, 0, 0, bar, f, a...) })
		validateChain("insert", l.Inserts, func(f string, a ...any) { add(Warning, L, 0, 0, 0, f, a...) })
		for _, o := range l.Overrides {
			if !inRange(o.From.Bar) || !PosLess(o.From, o.To) || !inRange(o.To.Bar) && !(o.To.Bar == n+1 && o.To.Offset.IsZero()) {
				add(Error, L, 0, 0, o.From.Bar, "dynamic override ends before it starts or is outside the score")
			}
		}
		for si, st := range l.Staves {
			for _, c := range st.Clefs {
				if !inRange(c.At.Bar) || !c.At.Offset.Less(s.BarLength(c.At.Bar)) {
					add(Error, L, si+1, 0, c.At.Bar, "clef change to %s outside the score", c.Clef)
				}
			}
			if st.Lines != 0 && (!st.Percussion() || st.Lines < 1 || st.Lines > 5) {
				add(Error, L, si+1, 0, 0, "a %s staff of %d lines (only a percussion staff, of 1 to 5)", st.Clef, st.Lines)
			}
			if st.Tab != nil {
				if n := st.Tab.Strings(); n < 1 || n > MaxStrings {
					add(Error, L, si+1, 0, 0, "tablature of %d strings (1..%d)", n, MaxStrings)
				}
				if st.Percussion() {
					add(Error, L, si+1, 0, 0, "a percussion staff has no tablature")
				}
			}
			for vi, v := range st.Voices {
				s.validateVoice(st, v, L, si+1, vi+1, add)
			}
		}
		for i, c := range l.Chords {
			if !inRange(c.At.Bar) || !c.At.Offset.Less(s.BarLength(c.At.Bar)) && !c.At.Offset.IsZero() {
				add(Error, L, 0, 0, c.At.Bar, "chord symbol %s outside the score", c.Chord)
			}
			if i > 0 && !PosLess(l.Chords[i-1].At, c.At) {
				add(Error, L, 0, 0, c.At.Bar, "two chord symbols at %s", s.FormatPos(c.At))
			}
		}
		if cp := l.ChordPlay; cp != nil && cp.Style != ChordsSilent {
			if len(l.Chords) == 0 {
				add(Warning, L, 0, 0, 0, "chord_play with no chord symbols to play")
			}
			if cp.Instrument != "" && instrumentExists != nil && !instrumentExists(cp.Instrument) {
				add(Error, L, 0, 0, 0, "chord_play: no instrument %q in the library", cp.Instrument)
			}
		}
	}
	warn := func(f string, a ...any) { add(Warning, 0, 0, 0, 0, f, a...) }
	for k, b := range [2]*Bus{&s.Buses.A, &s.Buses.B} {
		if b.Return < 0 || b.Return > 4 {
			warn("bus %c: return %.2f is outside 0..4 (it plays clamped)", 'a'+k, b.Return)
		}
		where := fmt.Sprintf("bus %c: ", 'a'+k)
		validateLanes(ScopeBus, b.Lanes, b.FX, "the bus has no such effect", inRange,
			func(sev Severity, bar int, f string, a ...any) { add(sev, 0, 0, 0, bar, where+f, a...) })
	}
	validateLanes(ScopeMaster, s.MasterLanes, s.Master, "the master has no such effect", inRange,
		func(sev Severity, bar int, f string, a ...any) { add(sev, 0, 0, 0, bar, "master: "+f, a...) })
	validateChain("bus a effect", s.Buses.A.FX, warn)
	validateChain("bus b effect", s.Buses.B.FX, warn)
	validateChain("master effect", s.Master, warn)
	s.validateLayout(warn)
	return out
}

// validateLanes checks one owner's lanes: names its scope takes, effects
// its chain has, points inside the score, values in range.
func validateLanes(sc LaneScope, lanes []Lane, chain []FX, noSuch string, inRange func(int) bool, add func(Severity, int, string, ...any)) {
	for _, ln := range lanes {
		tgt, err := ParseLaneTargetIn(sc, ln.Name)
		if err != nil {
			add(Error, 0, "%v", err)
		} else if tgt.Kind == LaneFX && ChainSlot(chain, tgt) < 0 {
			add(Warning, 0, "lane %s: %s (the lane moves nothing)", ln.Name, noSuch)
		}
		lo, hi := tgt.Range()
		for _, p := range ln.Points {
			if !inRange(p.At.Bar) || p.HasTo && (!inRange(p.To.Bar) || PosLess(p.To, p.At)) {
				add(Error, p.At.Bar, "lane %s: point outside the score or ending before it starts", ln.Name)
			}
			if err == nil && (p.Value < lo || p.Value > hi) {
				add(Warning, p.At.Bar, "lane %s: value %g outside %g..%g (it plays clamped)", ln.Name, p.Value, lo, hi)
			}
		}
	}
}

func (s *Score) validateVoice(st *Staff, v *Voice, L, S, V int, add func(Severity, int, int, int, int, string, ...any)) {
	type tie struct {
		bar  int
		midi int
	}
	var ties []tie
	open := map[string]bool{}
	for bi, m := range v.Measures {
		bar := bi + 1
		barLen := s.BarLength(bar)
		for ti, t := range m.Tuplets {
			if t.Num < 1 || t.Den < 1 || t.Parent < 0 || t.Parent > ti {
				add(Error, L, S, V, bar, "bad tuplet %d:%d", t.Num, t.Den)
				return
			}
		}
		if len(m.Events) == 0 {
			for _, t := range ties {
				add(Warning, L, S, V, t.bar, "tie from %s goes into an empty bar", SpellMIDI(t.midi, 0, 1))
			}
			ties = ties[:0]
			continue
		}
		_, filled := m.Offsets(barLen)
		switch filled.Cmp(barLen) {
		case 1:
			add(Error, L, S, V, bar, "bar holds %s of a whole note; the bar is %s", filled, barLen)
		case -1:
			if V == 1 {
				add(Warning, L, S, V, bar, "bar holds %s of a whole note; the bar is %s", filled, barLen)
			}
		}
		for _, e := range m.Events {
			if e.Kind == EventBarRest && len(m.Events) > 1 {
				add(Error, L, S, V, bar, "a bar rest must be alone in its bar")
			}
			for _, mk := range e.Marks {
				if mk.Kind() == MarkUnknown {
					add(Warning, L, S, V, bar, "unknown mark %q (kept as written)", mk.Name)
					continue
				}
				if msg := markValueProblem(mk); msg != "" {
					add(Warning, L, S, V, bar, "%s", msg)
				}
				switch mk.Kind() {
				case MarkLyric:
					if msg := lyricProblem(&e, mk); msg != "" {
						add(Warning, L, S, V, bar, "%s", msg)
					}
				case MarkTab:
					if msg := tabProblem(st, &e, mk); msg != "" {
						add(Warning, L, S, V, bar, "%s", msg)
					}
				}
				switch mk.Name {
				case "cross":
					// L and S count from 1.
					if d := e.CrossStaff(); d != 0 && L >= 1 && L <= len(s.Lines) {
						t := S - 1 + d
						if ls := s.Lines[L-1].Staves; t < 0 || t >= len(ls) || ls[t].Percussion() || ls[t].Tab != nil && ls[t].Tab.Only {
							add(Warning, L, S, V, bar, "cross=%s: the line has no staff %s this one to write the notes on", mk.Value, map[int]string{-1: "above", 1: "below"}[d])
						}
					}
				case "cresc", "dim":
					open["hairpin"] = true
				case "hairpin_end":
					if !open["hairpin"] {
						add(Warning, L, S, V, bar, "hairpin_end with no cresc or dim before it in the voice")
					}
					open["hairpin"] = false
				case "slur":
					open["slur"] = true
				case "slur_end":
					if !open["slur"] {
						add(Warning, L, S, V, bar, "slur_end with no slur before it in the voice")
					}
					open["slur"] = false
				case "8va", "8vb", "15ma", "15mb":
					open["ottava"] = true
				case "ottava_end":
					if !open["ottava"] {
						add(Warning, L, S, V, bar, "ottava_end with no ottava before it in the voice")
					}
					open["ottava"] = false
				}
			}
			for _, nt := range e.Notes {
				if mi := nt.Pitch.MIDI(); mi < 0 || mi > 127 {
					add(Error, L, S, V, bar, "%s is outside MIDI 0..127", nt.Pitch)
				} else if mi < 12 {
					add(Warning, L, S, V, bar, "%s is below the lowest note the engine plays (C0)", nt.Pitch)
				}
			}
			if st.Tab != nil && e.Kind == EventChord {
				ps := make([]Pitch, len(e.Notes))
				for i, nt := range e.Notes {
					ps[i] = nt.Pitch
				}
				for i, at := range st.Tab.Place(ps, e.TabStrings(), 0) {
					if !at.OK {
						add(Warning, L, S, V, bar, "%s cannot be played on the tablature's strings", e.Notes[i].Pitch)
					}
				}
			}
			if e.Grace != NotGrace {
				continue
			}
			// Ties from the event before must land on this one.
			for _, t := range ties {
				found := false
				for _, nt := range e.Notes {
					if nt.Pitch.MIDI() == t.midi {
						found = true
					}
				}
				if !found {
					add(Warning, L, S, V, t.bar, "tie from %s does not reach the same pitch", SpellMIDI(t.midi, 0, 1))
				}
			}
			ties = ties[:0]
			for _, nt := range e.Notes {
				if nt.Tie {
					ties = append(ties, tie{bar, nt.Pitch.MIDI()})
				}
			}
		}
	}
	for _, t := range ties {
		add(Warning, L, S, V, t.bar, "tie from %s at the end of the voice", SpellMIDI(t.midi, 0, 1))
	}
}

// markValueProblem says what is wrong with a known mark's value, or "".
func markValueProblem(mk Mark) string {
	switch mk.Name {
	case "tr", "mord", "imord", "turn", "iturn":
		if _, _, _, _, ok := OrnamentAccidentals(mk.Value); !ok {
			return fmt.Sprintf("%s=%s: want the auxiliary note's accidental (#, b, n, x, bb; a turn's as upper/lower)", mk.Name, mk.Value)
		}
	case "trem", "trem2":
		if _, ok := TremoloStrokes(mk.Value); !ok {
			return fmt.Sprintf("%s=%s: want 1..4 strokes", mk.Name, mk.Value)
		}
	case "ferm":
		if mk.Value != "" && FermataScale(mk.Value) == 2 {
			return fmt.Sprintf("ferm=%s: want short, long or verylong", mk.Value)
		}
	case "arp":
		if mk.Value != "" && mk.Value != "up" && mk.Value != "down" {
			return fmt.Sprintf("arp=%s: want up or down", mk.Value)
		}
	case "cross":
		if mk.Value != "up" && mk.Value != "down" {
			return fmt.Sprintf("cross=%s: want up or down", mk.Value)
		}
	case "gliss":
		if mk.Value != "" && mk.Value != "wavy" && mk.Value != "straight" {
			return fmt.Sprintf("gliss=%s: want wavy or straight", mk.Value)
		}
	case "vel":
		if _, ok := ParseVelocity(mk.Value); !ok {
			return fmt.Sprintf("vel=%s: want a MIDI velocity 1..127", mk.Value)
		}
	}
	return ""
}

// validateForm checks voltas and jumps against the bars and each other.
func (s *Score) validateForm(n int, add func(Severity, int, int, int, int, string, ...any)) {
	for i, v := range s.Voltas {
		if v.Start < 1 || v.End < v.Start || v.End > n {
			add(Error, 0, 0, 0, v.Start, "ending %d..%d is not inside bars 1..%d", v.Start, v.End, n)
			continue
		}
		if i > 0 && v.Start <= s.Voltas[i-1].End {
			add(Error, 0, 0, 0, v.Start, "endings overlap (bars %d..%d and %d..%d)", s.Voltas[i-1].Start, s.Voltas[i-1].End, v.Start, v.End)
		}
		r := s.VoltaRepeat(v)
		if r < 0 {
			add(Error, 0, 0, 0, v.Start, "ending %s at bar %d has no repeat before it", v.Label(), v.Start)
			continue
		}
		for _, p := range v.Passes {
			if p < 1 || p > s.Repeats[r].Plays() {
				add(Error, 0, 0, 0, v.Start, "ending %s: the repeat at bars %d..%d plays %d times", v.Label(), s.Repeats[r].Start, s.Repeats[r].End, s.Repeats[r].Plays())
				break
			}
		}
	}
	back := 0
	for _, j := range s.Jumps {
		if j.Bar < 1 || j.Bar > n {
			add(Error, 0, 0, 0, j.Bar, "%s past the last bar (%d)", j.Text(), n)
			continue
		}
		switch j.Kind {
		case JumpDS:
			if s.FirstJump(JumpSegno) == 0 {
				add(Error, 0, 0, 0, j.Bar, "%s but no segno", j.Text())
			}
			back++
		case JumpDC:
			back++
		case JumpToCoda:
			if s.FirstJump(JumpCoda) == 0 {
				add(Error, 0, 0, 0, j.Bar, "To Coda but no coda")
			}
		}
		if j.Al == JumpCoda && s.FirstJump(JumpToCoda) == 0 {
			add(Error, 0, 0, 0, j.Bar, "%s but no To Coda", j.Text())
		}
		if j.Al == JumpFine && s.FirstJump(JumpFine) == 0 {
			add(Error, 0, 0, 0, j.Bar, "%s but no Fine", j.Text())
		}
	}
	if back > 1 {
		add(Warning, 0, 0, 0, 0, "%d D.C./D.S. marks: each plays once, in the order reached", back)
	}
}
