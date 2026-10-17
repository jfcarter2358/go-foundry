package score

// tab.go — tablature: where on a fretted instrument's strings a note is
// played.
//
// A staff with a Tab is a guitar's, a bass's, a lute's: its music is the
// staff's own notes (pitches, stored as they sound, like every staff's),
// shown as notation with a tablature staff linked under it — one line a
// string, a fret number on the line — or as tablature alone (Tab.Only).
// Nothing is stored twice: the tablature is the notation read through the
// instrument's tuning, so an edit on either is an edit of both.
//
// Which string a note is played on is worked out (Tab.Place): every note
// of a chord on a string of its own, the hand's stretch as small as it can
// be, near where the hand was for the note before, open strings and low
// frets preferred. A note can be put on a string of the writer's choosing
// with the str= mark, which Place keeps:
//
//	qE4(str=2)                the one note on the second string (B, fret 5)
//	hG3+D4(str=G3/3+D4/2)     each note of a chord by its pitch
//
// Strings are numbered as players number them: 1 is the highest, the top
// line of the tablature staff.

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// MaxFret is the highest fret a note is placed on.
const MaxFret = 24

// MaxStrings is the most strings a tablature has.
const MaxStrings = 10

// Tab is a staff's tablature.
type Tab struct {
	// Tuning is the open strings, lowest first (the last is string 1).
	Tuning []Pitch
	// Capo is the fret a capo stands at: frets are counted from it.
	Capo int
	// Only shows the staff as tablature alone, with the rhythm under it,
	// instead of notation with tablature linked under it.
	Only bool
}

// Clone copies the tablature.
func (t *Tab) Clone() *Tab {
	if t == nil {
		return nil
	}
	c := *t
	c.Tuning = append([]Pitch(nil), t.Tuning...)
	return &c
}

// TuningPreset is a named tuning.
type TuningPreset struct {
	Name, Label string
	// Strings are lowest first, as Tab.Tuning.
	Strings []Pitch
}

func pitches(names ...string) []Pitch {
	out := make([]Pitch, len(names))
	for i, n := range names {
		p, err := ParsePitch(n)
		if err != nil {
			panic("score: tuning " + err.Error())
		}
		out[i] = p
	}
	return out
}

// TuningPresets are the tunings a tablature can be given by name. A
// guitar sounds an octave below where it is written: its low E is E2.
var TuningPresets = []TuningPreset{
	{"guitar", "Guitar (standard: E A D G B E)", pitches("E2", "A2", "D3", "G3", "B3", "E4")},
	{"guitar_drop_d", "Guitar, drop D", pitches("D2", "A2", "D3", "G3", "B3", "E4")},
	{"guitar_eb", "Guitar, a half step down", pitches("Eb2", "Ab2", "Db3", "Gb3", "Bb3", "Eb4")},
	{"guitar_dadgad", "Guitar, DADGAD", pitches("D2", "A2", "D3", "G3", "A3", "D4")},
	{"guitar_open_g", "Guitar, open G", pitches("D2", "G2", "D3", "G3", "B3", "D4")},
	{"guitar_open_d", "Guitar, open D", pitches("D2", "A2", "D3", "F#3", "A3", "D4")},
	{"guitar_7", "Seven-string guitar", pitches("B1", "E2", "A2", "D3", "G3", "B3", "E4")},
	{"bass", "Bass (4 strings: E A D G)", pitches("E1", "A1", "D2", "G2")},
	{"bass_5", "Bass, 5 strings (B E A D G)", pitches("B0", "E1", "A1", "D2", "G2")},
	{"bass_6", "Bass, 6 strings (B E A D G C)", pitches("B0", "E1", "A1", "D2", "G2", "C3")},
	{"ukulele", "Ukulele (G C E A)", pitches("G4", "C4", "E4", "A4")},
	{"mandolin", "Mandolin (G D A E)", pitches("G3", "D4", "A4", "E5")},
	{"banjo", "Banjo, open G (g D G B D)", pitches("G4", "D3", "G3", "B3", "D4")},
}

// FindTuning finds a tuning by name.
func FindTuning(name string) (TuningPreset, bool) {
	for _, t := range TuningPresets {
		if t.Name == name {
			return t, true
		}
	}
	return TuningPreset{}, false
}

// NewTab is a tablature in a named tuning (guitar when the name is not one).
func NewTab(tuning string) *Tab {
	t, ok := FindTuning(tuning)
	if !ok {
		t = TuningPresets[0]
	}
	return &Tab{Tuning: append([]Pitch(nil), t.Strings...)}
}

// Preset is the name of the tablature's tuning, "" for one of its own.
func (t *Tab) Preset() string {
	for _, p := range TuningPresets {
		if sameTuning(p.Strings, t.Tuning) {
			return p.Name
		}
	}
	return ""
}

func sameTuning(a, b []Pitch) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Strings is how many strings the tablature has.
func (t *Tab) Strings() int { return len(t.Tuning) }

// standardTunings are the presets an instrument is tuned to unless the
// music asks otherwise: what a tuning is printed against.
var standardTunings = []string{"guitar", "guitar_7", "bass", "bass_5", "bass_6", "ukulele", "mandolin", "banjo"}

// Retuned is a string tuned away from its instrument's standard tuning.
type Retuned struct {
	// String is 1-based, the highest string 1.
	String int
	Pitch  Pitch
}

// Retuning says what a page prints of the tuning at the start of the
// music: nothing for a standard tuning; the strings that differ from the
// nearest standard tuning of as many strings (drop D: string 6 to D); or,
// when more than half differ or no standard has that many strings, every
// string (all true).
func (t *Tab) Retuning() (strs []Retuned, all bool) {
	best := -1
	var bestDiff []Retuned
	for _, name := range standardTunings {
		p, _ := FindTuning(name)
		if len(p.Strings) != len(t.Tuning) {
			continue
		}
		var diff []Retuned
		for i, q := range t.Tuning {
			if q != p.Strings[i] {
				diff = append(diff, Retuned{String: len(t.Tuning) - i, Pitch: q})
			}
		}
		if best < 0 || len(diff) < best {
			best, bestDiff = len(diff), diff
		}
	}
	switch {
	case best == 0:
		return nil, false
	case best < 0 || 2*best > len(t.Tuning):
		for i, q := range t.Tuning {
			strs = append(strs, Retuned{String: len(t.Tuning) - i, Pitch: q})
		}
		return strs, true
	}
	sort.Slice(bestDiff, func(i, j int) bool { return bestDiff[i].String > bestDiff[j].String })
	return bestDiff, false
}

// Open is a string's open pitch (string 1 the highest), with the capo.
func (t *Tab) Open(str int) int {
	if str < 1 || str > len(t.Tuning) {
		return -1
	}
	return t.Tuning[len(t.Tuning)-str].MIDI() + t.Capo
}

// Fret is the fret a pitch is played at on a string, and whether it can be.
func (t *Tab) Fret(p Pitch, str int) (int, bool) {
	o := t.Open(str)
	if o < 0 {
		return 0, false
	}
	f := p.MIDI() - o
	return f, f >= 0 && f <= MaxFret-t.Capo
}

// TabNote is where one note is played: a string (1 the highest) and a fret;
// OK is false for a note the tuning cannot reach (or a chord with more
// notes than strings), which has neither.
type TabNote struct {
	String, Fret int
	OK           bool
}

// Place puts a chord's notes on strings. fixed holds, per note, the string
// the writer chose (0 for none, or a string that cannot play it); near is
// the fret the hand is at (0 for none: low frets and open strings first).
// The result is per note, in the chord's order.
func (t *Tab) Place(notes []Pitch, fixed []int, near float64) []TabNote {
	out := make([]TabNote, len(notes))
	n := t.Strings()
	if n == 0 || len(notes) == 0 {
		return out
	}
	// Highest note first: it has the fewest strings to choose from at the
	// top, the most at the bottom; the search is the same either way.
	order := make([]int, len(notes))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return notes[order[a]].MIDI() > notes[order[b]].MIDI() })
	cand := make([][]int, len(notes))
	for i, p := range notes {
		if i < len(fixed) && fixed[i] > 0 {
			if _, ok := t.Fret(p, fixed[i]); ok {
				cand[i] = []int{fixed[i]}
				continue
			}
		}
		for s := 1; s <= n; s++ {
			if _, ok := t.Fret(p, s); ok {
				cand[i] = append(cand[i], s)
			}
		}
	}
	best, bestCost := make([]int, len(notes)), math.Inf(1)
	cur := make([]int, len(notes))
	used := make([]bool, n+1)
	var walk func(k int)
	walk = func(k int) {
		if k == len(order) {
			if c := t.placeCost(notes, cur, near); c < bestCost {
				bestCost = c
				copy(best, cur)
			}
			return
		}
		i := order[k]
		if len(cand[i]) == 0 {
			cur[i] = 0 // cannot be played: left off the strings
			walk(k + 1)
			return
		}
		placed := false
		for _, s := range cand[i] {
			if used[s] {
				continue
			}
			used[s], cur[i], placed = true, s, true
			walk(k + 1)
			used[s] = false
		}
		if !placed {
			// Every string it could take is taken: off the strings.
			cur[i] = 0
			walk(k + 1)
		}
	}
	walk(0)
	for i, s := range best {
		if s == 0 {
			continue
		}
		f, _ := t.Fret(notes[i], s)
		out[i] = TabNote{String: s, Fret: f, OK: true}
	}
	return out
}

// placeCost weighs one way of fingering a chord: notes left off the strings
// above all, then the stretch between the fretted notes, how far the hand
// moves from where it was, and how high up the neck it goes.
func (t *Tab) placeCost(notes []Pitch, strs []int, near float64) float64 {
	cost := 0.0
	lo, hi, fretted, sum := MaxFret+1, -1, 0, 0
	for i, s := range strs {
		if s == 0 {
			cost += 1000
			continue
		}
		f, _ := t.Fret(notes[i], s)
		if f == 0 {
			cost -= 0.3 // an open string is free
			continue
		}
		lo, hi = min(lo, f), max(hi, f)
		fretted++
		sum += f
	}
	if fretted > 0 {
		span := float64(hi - lo)
		cost += 2 * span
		if span > 4 {
			cost += 10 * (span - 4) // past a hand's reach
		}
		mean := float64(sum) / float64(fretted)
		if near > 0 {
			cost += 0.5 * math.Abs(mean-near)
		} else {
			cost += 0.35 * mean
		}
	}
	return cost
}

// VoicePlaces are where a voice's notes are played, walking it from its
// start as a player does — each chord near where the hand was for the one
// before: per bar, per event, per note (nil for a rest).
func (t *Tab) VoicePlaces(v *Voice) [][][]TabNote {
	out := make([][][]TabNote, len(v.Measures))
	hand := 0.0
	for bi, m := range v.Measures {
		out[bi] = make([][]TabNote, len(m.Events))
		for ei := range m.Events {
			e := &m.Events[ei]
			if e.Kind != EventChord || len(e.Notes) == 0 {
				continue
			}
			ps := make([]Pitch, len(e.Notes))
			for i, n := range e.Notes {
				ps[i] = n.Pitch
			}
			res := t.Place(ps, e.TabStrings(), hand)
			out[bi][ei] = res
			if h := HandAt(res); h > 0 {
				hand = h
			}
		}
	}
	return out
}

// HandAt is where a placed chord leaves the hand: its fretted notes' mean
// fret, 0 when it is all open strings.
func HandAt(ps []TabNote) float64 {
	sum, n := 0, 0
	for _, p := range ps {
		if p.OK && p.Fret > 0 {
			sum += p.Fret
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return float64(sum) / float64(n)
}

// ---------- the str= mark ----------

// TabStrings are the strings the writer chose for an event's notes (its
// str= mark), per note in the chord's order, 0 where none is chosen.
func (e *Event) TabStrings() []int {
	out := make([]int, len(e.Notes))
	for _, m := range e.Marks {
		if m.Name != "str" {
			continue
		}
		strs, err := parseTabStrings(m.Value, e.Notes, false)
		if err == nil {
			copy(out, strs)
		}
	}
	return out
}

// SetTabStrings sets every note's string choice at once (per note in the
// chord's order, 0 for none).
func (e *Event) SetTabStrings(strs []int) {
	var out []Mark
	for _, m := range e.Marks {
		if m.Name != "str" {
			out = append(out, m)
		}
	}
	if len(strs) == len(e.Notes) {
		if v := formatTabStrings(strs, e.Notes); v != "" {
			out = append(out, Mark{Name: "str", Value: v})
		}
	}
	e.Marks = out
}

// SetTabString puts one note of an event on a string (0 lets it be worked
// out again), keeping the others' choices; the mark goes when none is left.
func (e *Event) SetTabString(note, str int) {
	if note < 0 || note >= len(e.Notes) {
		return
	}
	strs := e.TabStrings()
	strs[note] = str
	e.SetTabStrings(strs)
}

// formatTabStrings writes per-note strings as a str= value: the string
// alone for a single note, pitch/string pairs for a chord.
func formatTabStrings(strs []int, notes []Note) string {
	if len(notes) == 1 {
		if strs[0] > 0 {
			return strconv.Itoa(strs[0])
		}
		return ""
	}
	var parts []string
	for i, s := range strs {
		if s > 0 {
			parts = append(parts, notes[i].Pitch.String()+"/"+strconv.Itoa(s))
		}
	}
	return strings.Join(parts, "+")
}

// parseTabStrings reads a str= value against an event's notes; strict
// refuses a pair naming a pitch the chord does not have (otherwise it is
// passed over: a note whose pitch changed lost its string).
func parseTabStrings(v string, notes []Note, strict bool) ([]int, error) {
	out := make([]int, len(notes))
	if !strings.Contains(v, "/") {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxStrings {
			return nil, fmt.Errorf("str=%s: want a string number 1..%d", v, MaxStrings)
		}
		if len(notes) != 1 {
			return nil, fmt.Errorf("str=%s on a chord: name each note's string (str=E4/1+B3/2)", v)
		}
		out[0] = n
		return out, nil
	}
	for _, part := range strings.Split(v, "+") {
		ps, ns, ok := strings.Cut(part, "/")
		if !ok {
			return nil, fmt.Errorf("str=%s: want pitch/string pairs joined by + (E4/1+B3/2)", v)
		}
		p, err := ParsePitch(ps)
		if err != nil {
			return nil, fmt.Errorf("str=%s: %v", v, err)
		}
		n, err := strconv.Atoi(ns)
		if err != nil || n < 1 || n > MaxStrings {
			return nil, fmt.Errorf("str=%s: want a string number 1..%d", v, MaxStrings)
		}
		found := false
		for i, nt := range notes {
			if nt.Pitch == p {
				out[i], found = n, true
			}
		}
		if !found && strict {
			return nil, fmt.Errorf("str=%s: the chord has no %s", v, p)
		}
	}
	return out, nil
}

// tabProblem says what is wrong with an event's str= mark on a staff, or "".
func tabProblem(st *Staff, e *Event, mk Mark) string {
	strs, err := parseTabStrings(mk.Value, e.Notes, true)
	if err != nil {
		return err.Error()
	}
	if st.Tab == nil {
		return ""
	}
	seen := map[int]bool{}
	for i, s := range strs {
		if s == 0 {
			continue
		}
		if seen[s] {
			return fmt.Sprintf("str=%s puts two notes on string %d", mk.Value, s)
		}
		seen[s] = true
		if s > st.Tab.Strings() {
			return fmt.Sprintf("str=%s: the tablature has %d strings", mk.Value, st.Tab.Strings())
		}
		if _, ok := st.Tab.Fret(e.Notes[i].Pitch, s); !ok {
			return fmt.Sprintf("%s cannot be played on string %d", e.Notes[i].Pitch, s)
		}
	}
	return ""
}

// ParseTuning reads a tuning: a preset's name, or pitches lowest first.
func ParseTuning(name string, strs []string) ([]Pitch, error) {
	if len(strs) == 0 {
		t, ok := FindTuning(name)
		if !ok {
			var names []string
			for _, p := range TuningPresets {
				names = append(names, p.Name)
			}
			return nil, fmt.Errorf("tuning %q (want one of %s, or the strings' pitches lowest first)", name, strings.Join(names, ", "))
		}
		return append([]Pitch(nil), t.Strings...), nil
	}
	if len(strs) < 1 || len(strs) > MaxStrings {
		return nil, fmt.Errorf("a tablature has 1..%d strings, not %d", MaxStrings, len(strs))
	}
	out := make([]Pitch, len(strs))
	for i, s := range strs {
		p, err := ParsePitch(s)
		if err != nil {
			return nil, fmt.Errorf("tuning: %v", err)
		}
		out[i] = p
	}
	return out, nil
}
