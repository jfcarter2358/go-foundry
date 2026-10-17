package edit

// record.go — a take played in real time, written into a line.
//
// The Score editor records from a MIDI keyboard while the score plays
// (internal/editor/editor_score_midi.go): what comes out is notes with
// written times, like a MIDI file's. WriteTake writes them the way the MIDI
// import writes a file's part (notate.Transcribe: quantised, split onto a
// grand staff, dealt into voices, velocity read as dynamics, spelled in the
// key) over the bars the take covered, replacing what those bars held in
// every voice of the line's staves — Sibelius's Flexi-time records over
// the passage the same way.

import (
	"fmt"

	"foundry/internal/score"
	"foundry/internal/score/io/notate"
)

// TakeOptions shape WriteTake: notate.TranscribeOptions' choices that a
// recording leaves to the writer (the staves come from the line).
type TakeOptions struct {
	Grid                           notate.Grid
	Voices                         int
	Split                          int
	NoGraces, NoLegato, NoDynamics bool
	// Pedal are the damper pedal's presses during the take (written
	// times), written as pedal marks.
	Pedal []notate.PedalSpan
}

// WriteTake replaces bars a..z of a line with notes (concert MIDI, written
// times in whole notes from the top of the score, notate.Note), and returns
// how many notes were written. Notes are cut at bar z's end. A line on two
// staves is a grand staff split at Split; a percussion staff writes its
// hits as a drum part. Ties from bar a-1 into the replaced bars are taken
// off (what they tied to is gone).
func WriteTake(s *score.Score, line, a, z int, notes []notate.Note, o TakeOptions) (int, error) {
	if line < 0 || line >= len(s.Lines) {
		return 0, fmt.Errorf("no line %d", line+1)
	}
	l := s.Lines[line]
	if len(l.Staves) == 0 {
		return 0, fmt.Errorf("%s has no staff", l.Name)
	}
	if a < 1 || z < a || z > s.Measures() {
		return 0, fmt.Errorf("bars %d-%d are not in the score", a, z)
	}
	lay := notate.LayoutOf(s)
	from, to := lay.Starts[a-1], lay.Starts[z]
	var kept []notate.Note
	for _, n := range notes {
		if !n.Start.Less(to) || !from.Less(n.End) {
			continue
		}
		if n.Start.Less(from) {
			n.Start = from
		}
		if to.Less(n.End) {
			n.End = to
		}
		if !n.Start.Less(n.End) {
			continue
		}
		kept = append(kept, n)
	}
	clefs := []string{l.Staves[0].Clef}
	if len(l.Staves) > 1 {
		clefs = append(clefs, l.Staves[1].Clef)
	}
	staves, err := notate.Transcribe(kept, lay, notate.TranscribeOptions{
		Grid: o.Grid, Voices: o.Voices, Clefs: clefs, Split: o.Split, Percussion: l.Staves[0].Percussion(),
		NoGraces: o.NoGraces, NoLegato: o.NoLegato, NoDynamics: o.NoDynamics, Pedal: o.Pedal,
	})
	if err != nil {
		return 0, err
	}
	carried := staffMarks(s, l, a, z, o)
	written := 0
	for si, ts := range staves {
		st := l.Staves[si]
		n := max(len(st.Voices), len(ts.Voices))
		for vi := 0; vi < n; vi++ {
			v, err := EnsureVoice(s, line, si, vi)
			if err != nil {
				return 0, err
			}
			if a > 1 {
				untieInto(v, a-1)
			}
			for b := a; b <= z; b++ {
				var m score.Measure
				if vi < len(ts.Voices) {
					m = ts.Voices[vi].Measure(b).Clone()
				}
				if vi == 0 && len(m.Events) == 0 {
					m = score.Measure{Events: []score.Event{{Kind: score.EventBarRest}}}
				}
				for _, ev := range m.Events {
					if ev.Kind == score.EventChord {
						written += len(ev.Notes)
					}
				}
				*measureOf(s, v, b) = m
			}
			if vi > 0 {
				v.Trim()
			}
		}
	}
	for _, k := range carried {
		k.attach(s, l)
	}
	return written, nil
}

// carriedMark is a mark of a replaced bar that belongs to the staff more
// than to the note it sat on, and where it was.
type carriedMark struct {
	staff, bar int
	at         score.Frac
	mark       score.Mark
}

// staffMarks are the marks of bars a..z a take keeps: text, technique,
// hairpins, slurs, ottava and pedal lines — so a hairpin, an 8va or a slur
// begun before the take still ends where it did, and a "pizz." still
// switches there — and the dynamics when the take writes none of its own
// (NoDynamics), the pedal when it played none. A note's own marks
// (articulations, ornaments, lyrics, strings, a glissando to the next
// note) go with the notes.
func staffMarks(s *score.Score, l *score.Line, a, z int, o TakeOptions) []carriedMark {
	var out []carriedMark
	for si, st := range l.Staves {
		for _, v := range st.Voices {
			for b := a; b <= z && b <= len(v.Measures); b++ {
				m := v.Measures[b-1]
				offs, _ := m.Offsets(s.BarLength(b))
				for ei, ev := range m.Events {
					for _, mk := range ev.Marks {
						keep := false
						switch mk.Kind() {
						case score.MarkText, score.MarkTechnique:
							keep = true
						case score.MarkSpanner:
							switch mk.Name {
							case "gliss":
							case "ped", "ped_end":
								keep = len(o.Pedal) == 0
							default:
								keep = true
							}
						case score.MarkDynamic:
							keep = o.NoDynamics
						}
						if keep {
							out = append(out, carriedMark{staff: si, bar: b, at: offs[ei], mark: mk})
						}
					}
				}
			}
		}
	}
	return out
}

// attach puts a carried mark back on its staff's first voice, on the
// event sounding where it was (the bar's first when none starts before).
func (k carriedMark) attach(s *score.Score, l *score.Line) {
	st := l.Staves[k.staff]
	if len(st.Voices) == 0 || k.bar > len(st.Voices[0].Measures) {
		return
	}
	m := &st.Voices[0].Measures[k.bar-1]
	if len(m.Events) == 0 {
		return
	}
	offs, _ := m.Offsets(s.BarLength(k.bar))
	at := 0
	for i, off := range offs {
		if m.Events[i].Grace == score.NotGrace && !k.at.Less(off) {
			at = i
		}
	}
	ev := &m.Events[at]
	for _, mk := range ev.Marks {
		if mk == k.mark {
			return
		}
	}
	ev.Marks = append(ev.Marks, k.mark)
}

// untieInto takes the ties off a bar's last sounding event.
func untieInto(v *score.Voice, bar int) {
	if bar < 1 || bar > len(v.Measures) {
		return
	}
	es := v.Measures[bar-1].Events
	for i := len(es) - 1; i >= 0; i-- {
		if es[i].Grace != score.NotGrace {
			continue
		}
		for k := range es[i].Notes {
			es[i].Notes[k].Tie = false
		}
		return
	}
}
