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
		for _, ln := range l.Lanes {
			for _, p := range ln.Points {
				if !inRange(p.At.Bar) || p.HasTo && (!inRange(p.To.Bar) || PosLess(p.To, p.At)) {
					add(Error, L, 0, 0, p.At.Bar, "lane %s: point outside the score or ending before it starts", ln.Name)
				}
			}
		}
		for si, st := range l.Staves {
			for vi, v := range st.Voices {
				s.validateVoice(v, L, si+1, vi+1, add)
			}
		}
	}
	return out
}

func (s *Score) validateVoice(v *Voice, L, S, V int, add func(Severity, int, int, int, int, string, ...any)) {
	type tie struct {
		bar  int
		midi int
	}
	var ties []tie
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
				}
			}
			for _, nt := range e.Notes {
				if mi := nt.Pitch.MIDI(); mi < 0 || mi > 127 {
					add(Error, L, S, V, bar, "%s is outside MIDI 0..127", nt.Pitch)
				} else if mi < 12 {
					add(Warning, L, S, V, bar, "%s is below the lowest note the engine plays (C0)", nt.Pitch)
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
