package score

// staff.go — what a staff shows at a point: its clef, the ottava line over
// it, its written pitch, and the accidentals already in force in the bar.
//
// All of these change how music is written, never how it sounds: pitches
// are stored as they sound, so a passage under an 8va line keeps its
// pitches and is drawn an octave lower, and a B-flat clarinet's concert C
// is drawn as a D. The engraver and note entry ask these questions; the
// interpreter does not need to.

import "sort"

// ClefChange is a clef change inside a staff: from At on the staff reads
// in Clef.
type ClefChange struct {
	At   Pos
	Clef string
}

// ClefAt is the clef in force at a point: the staff's own until the first
// change at or before it.
func (st *Staff) ClefAt(p Pos) string {
	c := st.Clef
	for _, ch := range st.Clefs {
		if !posLess(p, ch.At) {
			c = ch.Clef
		}
	}
	return c
}

// SortClefs puts the clef changes in order.
func (st *Staff) SortClefs() {
	sort.SliceStable(st.Clefs, func(i, j int) bool { return posLess(st.Clefs[i].At, st.Clefs[j].At) })
}

// Written is a sounding pitch as the staff writes it (its transposition
// undone); Sounding is the reverse.
func (st *Staff) Written(p Pitch) Pitch {
	if st.Transpose == (Interval{}) {
		return p
	}
	return p.Transpose(Interval{Diatonic: -st.Transpose.Diatonic, Chromatic: -st.Transpose.Chromatic})
}

// Sounding is a written pitch as the staff's instrument sounds it.
func (st *Staff) Sounding(p Pitch) Pitch {
	if st.Transpose == (Interval{}) {
		return p
	}
	return p.Transpose(st.Transpose)
}

// OttavaShifts are the ottava marks and the octaves they move the sound by
// against what is written: under 8va the page shows notes an octave below
// where they sound.
var OttavaShifts = map[string]int{"8va": 1, "8vb": -1, "15ma": 2, "15mb": -2}

// OttavaSpan is an ottava line over a staff, in written positions: From
// the event carrying the mark to the end of the event carrying
// "ottava_end" (inclusive, like every spanner end), the next ottava, or
// the end of the voice. Octaves is OttavaShifts' value.
type OttavaSpan struct {
	From, To Pos
	Octaves  int
	Voice    int
}

// Ottavas are a staff's ottava lines, in order of where they start. Every
// voice's marks count (an ottava line is the staff's); one voice's line
// closes at its own ottava_end.
func (s *Score) Ottavas(st *Staff) []OttavaSpan {
	var out []OttavaSpan
	for vi, v := range st.Voices {
		open := -1
		var last Pos
		for bi, m := range v.Measures {
			bar := bi + 1
			offs, _ := m.Offsets(s.BarLength(bar))
			for ei, e := range m.Events {
				at := Pos{Bar: bar, Offset: offs[ei]}
				end := s.posAfter(bar, offs[ei].Add(m.Actual(e, s.BarLength(bar))))
				for _, mk := range e.Marks {
					if k, ok := OttavaShifts[mk.Name]; ok {
						if open >= 0 {
							out[open].To = at
						}
						out = append(out, OttavaSpan{From: at, To: end, Octaves: k, Voice: vi})
						open = len(out) - 1
					}
				}
				if open >= 0 {
					out[open].To = end
				}
				for _, mk := range e.Marks {
					if mk.Name == "ottava_end" && open >= 0 {
						out[open].To = end
						open = -1
					}
				}
				last = end
			}
		}
		if open >= 0 {
			out[open].To = last
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return posLess(out[i].From, out[j].From) })
	return out
}

// OttavaAt is the octaves an ottava line moves the sound by at a point of
// a staff, 0 when none is over it.
func (s *Score) OttavaAt(st *Staff, p Pos) int {
	k := 0
	for _, o := range s.Ottavas(st) {
		if !posLess(p, o.From) && posLess(p, o.To) {
			k = o.Octaves
		}
	}
	return k
}

// posAfter is a bar and an offset that may reach the bar's end, as a
// position: the end of a bar is the start of the next.
func (s *Score) posAfter(bar int, off Frac) Pos {
	if l := s.BarLength(bar); !off.Less(l) {
		return Pos{Bar: bar + 1, Offset: off.Sub(l)}
	}
	return Pos{Bar: bar, Offset: off}
}

// AlterInForce is the accidental a note on a staff's line or space has at
// a point of a bar: the key's, unless an earlier note in the bar on that
// staff — in any of its voices, grace notes included — wrote another on the
// same line or space. A tied-over note neither shows an accidental nor
// changes the bar's. Pitches compared are sounding; diatonic is a sounding
// staff position (Pitch.Diatonic), so a transposing staff's caller moves
// its written position by the staff's interval first.
func (s *Score) AlterInForce(st *Staff, bar int, at Frac, diatonic int) int {
	alter := KeyAlter(s.KeyAt(bar).Fifths, ((diatonic%7)+7)%7)
	type hit struct {
		off   Frac
		alter int
	}
	var hits []hit
	for _, v := range st.Voices {
		if bar > len(v.Measures) {
			continue
		}
		m := v.Measures[bar-1]
		offs, _ := m.Offsets(s.BarLength(bar))
		tied := map[int]bool{}
		if bar > 1 && bar-1 <= len(v.Measures) {
			if pm := v.Measures[bar-2]; len(pm.Events) > 0 {
				for _, nt := range pm.Events[len(pm.Events)-1].Notes {
					if nt.Tie {
						tied[nt.Pitch.MIDI()] = true
					}
				}
			}
		}
		for ei, e := range m.Events {
			if !offs[ei].Less(at) {
				break
			}
			next := map[int]bool{}
			for _, nt := range e.Notes {
				if nt.Pitch.Diatonic() == diatonic && !(tied[nt.Pitch.MIDI()] && e.Grace == NotGrace) {
					hits = append(hits, hit{offs[ei], int(nt.Pitch.Alter)})
				}
				if nt.Tie {
					next[nt.Pitch.MIDI()] = true
				}
			}
			if e.Grace == NotGrace {
				tied = next
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].off.Less(hits[j].off) })
	for _, h := range hits {
		alter = h.alter
	}
	return alter
}
