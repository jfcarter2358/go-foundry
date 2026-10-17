package score

// marks.go — the vocabulary of event marks.
//
// A mark is a name and an optional value attached to an event. The list is
// open: a name this build does not know is kept, saved and shown by
// `foundry score inspect` as a warning, so a file written by a newer build
// loses nothing in an older one. The names here are the ones playback and
// engraving understand. Inside a voice's "( )" a few marks have the
// one-character spellings musicians already use for them (Aliases).

import "strconv"

// MarkKind groups marks by what they do.
type MarkKind uint8

const (
	MarkUnknown MarkKind = iota
	// MarkDynamic sets a level: ppp..fff, and the accents sfz, fp…
	MarkDynamic
	// MarkArticulation shapes one note: staccato, accent, tenuto…
	MarkArticulation
	// MarkOrnament decorates one note: trill, mordent, turn, tremolo.
	MarkOrnament
	// MarkTechnique is playing-technique text ("pizz.", "arco") that picks
	// an instrument articulation until changed.
	MarkTechnique
	// MarkText is free text: expression ("dolce"), a staff text.
	MarkText
	// MarkSpanner starts or ends something drawn across notes: a hairpin,
	// a slur, a glissando, an ottava, a pedal.
	MarkSpanner
	// MarkNotation changes how the note is drawn only: fermata, arpeggio,
	// a stem direction.
	MarkNotation
	// MarkPlayback changes only how the note plays and prints nothing: a
	// velocity set in the piano roll (vel=).
	MarkPlayback
	// MarkLyric is a syllable sung on the note, one verse a mark: ly, ly2
	// … ly8 (lyrics.go).
	MarkLyric
	// MarkTab places a note on a tablature staff: str= names the string it
	// is played on (tab.go).
	MarkTab
)

// Dynamic is what a dynamic mark plays as. Level is MIDI velocity / 127;
// a Momentary dynamic (sfz) accents only the notes it is on, and After,
// when set, is the level that follows it (fp: forte, then piano).
type Dynamic struct {
	Level     float32
	Momentary bool
	After     float32
}

// Dynamics are the dynamic marks and how they play. The levels follow the
// usual MIDI velocity scale (mf 80, f 96 …).
var Dynamics = map[string]Dynamic{
	"pppp": {Level: 8.0 / 127}, "ppp": {Level: 16.0 / 127}, "pp": {Level: 32.0 / 127},
	"p": {Level: 48.0 / 127}, "mp": {Level: 64.0 / 127}, "mf": {Level: 80.0 / 127},
	"f": {Level: 96.0 / 127}, "ff": {Level: 112.0 / 127}, "fff": {Level: 120.0 / 127},
	"ffff": {Level: 127.0 / 127},
	"sf":   {Level: 112.0 / 127, Momentary: true}, "sfz": {Level: 112.0 / 127, Momentary: true},
	"sffz": {Level: 124.0 / 127, Momentary: true}, "fz": {Level: 112.0 / 127, Momentary: true},
	"rfz": {Level: 104.0 / 127, Momentary: true}, "rf": {Level: 104.0 / 127, Momentary: true},
	"fp": {Level: 96.0 / 127, After: 48.0 / 127}, "sfp": {Level: 112.0 / 127, After: 48.0 / 127},
	"sfpp": {Level: 112.0 / 127, After: 32.0 / 127},
	// niente is nothing: where a diminuendo goes to, or a crescendo
	// starts from.
	"niente": {Level: 0},
}

// DynamicSteps are the plain dynamics from softest to loudest: what an
// open-ended hairpin moves one step along.
var DynamicSteps = []string{"niente", "pppp", "ppp", "pp", "p", "mp", "mf", "f", "ff", "fff", "ffff"}

// StepDynamic is the level one step louder (dir > 0) or softer than
// level: the nearest step's neighbour, clamped at the ends.
func StepDynamic(level float32, dir int) float32 {
	k := 0
	for i, n := range DynamicSteps {
		if d, b := Dynamics[n].Level-level, Dynamics[DynamicSteps[k]].Level-level; d*d < b*b {
			k = i
		}
	}
	if dir > 0 {
		k = min(k+1, len(DynamicSteps)-1)
	} else if dir < 0 {
		k = max(k-1, 0)
	}
	return Dynamics[DynamicSteps[k]].Level
}

// DefaultDynamic is the level a line plays at before its first dynamic.
const DefaultDynamic = "mf"

// markKinds are the known names other than dynamics. What each plays as
// is internal/score/interpret's; the values they take:
//
//	tr=#, mord=b, turn=#/b    the auxiliary note's accidental (a turn's
//	                          upper/lower); none follows the key
//	trem=3                    tremolo strokes on one note, 1..4 (3 when
//	                          bare: unmeasured)
//	trem2=3                   a two-note tremolo between this event and the
//	                          next, strokes 1..4
//	ferm=short|long|verylong  a fermata's length
//	arp=down                  an arpeggio rolled from the top
//	gliss=wavy                a glissando to the next note drawn wavy
//	cresc="poco a poco"       a hairpin; a value prints as text with dashes
//	                          instead ("text" prints "cresc."/"dim.")
//	on_beat, before_beat      a grace group's timing, over Playback.Graces
//	vel=96                    the event's notes play at this MIDI velocity
//	                          (1..127) whatever the dynamics say — the
//	                          piano roll's velocity lane; nothing prints
//	scoop, plop               a jazz slide into the note from below, from
//	                          above (drawn left of it)
//	doit, fall                a slide up or down out of the note at its end
//	                          (drawn right of it); doit=long, fall=long go
//	                          further, over more of the note
//	bend                      a jazz bend: the pitch dips a semitone and
//	                          comes back (drawn over the note)
//	open, stopped             "o" and "+" over the note: a horn's open and
//	                          stopped notes, a hi-hat open and closed (a
//	                          drum map can name either, drums.go)
//	str=2, str=E4/1+B3/2      the tablature string a note is played on
//	                          (tab.go)
//	cross=up, cross=down      the event's notes written on the line's
//	                          staff above or below its own (a piano's
//	                          hands crossing, a run beamed across the
//	                          grand staff); its voice is still its own
//	ly=Hal-, ly2=glo_         a verse's syllable (lyrics.go)
var markKinds = map[string]MarkKind{
	// articulations
	"stacc": MarkArticulation, "staccmo": MarkArticulation, "acc": MarkArticulation,
	"ten": MarkArticulation, "marc": MarkArticulation, "portato": MarkArticulation,
	"legato": MarkArticulation,
	// jazz articulations: into the note from below (scoop) or above
	// (plop), out of it up (doit) or down (fall), a dip in it (bend)
	"scoop": MarkArticulation, "plop": MarkArticulation, "doit": MarkArticulation,
	"fall": MarkArticulation, "bend": MarkArticulation,
	// ornaments
	"tr": MarkOrnament, "mord": MarkOrnament, "imord": MarkOrnament, "turn": MarkOrnament,
	"iturn": MarkOrnament, "trem": MarkOrnament, "trem2": MarkOrnament,
	// notation only
	"ferm": MarkNotation, "arp": MarkNotation, "up": MarkNotation, "down": MarkNotation,
	"on_beat": MarkNotation, "before_beat": MarkNotation,
	// playback only
	"vel": MarkPlayback,
	// tablature
	"str": MarkTab,
	// brass and percussion: open and stopped / closed (the "o" and "+"
	// over a note: a horn's stopped note, a hi-hat open or closed)
	"open": MarkNotation, "stopped": MarkNotation,
	// cross-staff: the notes on the staff above or below
	"cross": MarkNotation,
	// text
	"text": MarkText, "expr": MarkText, "tech": MarkTechnique,
	// spanners: starts and ends
	"cresc": MarkSpanner, "dim": MarkSpanner, "hairpin_end": MarkSpanner,
	"slur": MarkSpanner, "slur_end": MarkSpanner, "gliss": MarkSpanner,
	"8va": MarkSpanner, "8vb": MarkSpanner, "15ma": MarkSpanner, "15mb": MarkSpanner, "ottava_end": MarkSpanner,
	"ped": MarkSpanner, "ped_end": MarkSpanner,
}

// Aliases are the one-character spellings accepted inside "( )".
var Aliases = map[string]string{
	".": "stacc", "'": "staccmo", ">": "acc", "_": "ten", "^": "marc",
}

// KindOf classifies a mark name.
func KindOf(name string) MarkKind {
	if _, ok := Dynamics[name]; ok {
		return MarkDynamic
	}
	if LyricVerse(name) > 0 {
		return MarkLyric
	}
	return markKinds[name]
}

// Kind classifies the mark.
func (m Mark) Kind() MarkKind { return KindOf(m.Name) }

// Has reports whether an event carries a mark by name.
func (e *Event) Has(name string) bool {
	for _, m := range e.Marks {
		if m.Name == name {
			return true
		}
	}
	return false
}

// FermataScale is how much longer a fermata holds than written, by its
// value: short 1.5, plain 2, long 3, verylong 4.
func FermataScale(value string) float64 {
	switch value {
	case "short":
		return 1.5
	case "long":
		return 3
	case "verylong":
		return 4
	}
	return 2
}

// JazzMarks are the jazz articulations, in the keypad's order.
var JazzMarks = []string{"scoop", "plop", "doit", "fall", "bend"}

// FermataKinds are the values a fermata takes ("" aside).
var FermataKinds = []string{"short", "long", "verylong"}

// OrnamentAccidentals reads an ornament's value — "#", "b/n", "/#" — into
// the alterations it asks of its upper and lower auxiliary notes; ok is
// false for a value that is not one.
func OrnamentAccidentals(value string) (upper, lower int, hasUpper, hasLower, ok bool) {
	up, low, slash := value, "", false
	for i := 0; i < len(value); i++ {
		if value[i] == '/' {
			up, low, slash = value[:i], value[i+1:], true
			break
		}
	}
	read := func(t string) (int, bool) {
		switch t {
		case "#":
			return 1, true
		case "x", "##":
			return 2, true
		case "b":
			return -1, true
		case "bb":
			return -2, true
		case "n":
			return 0, true
		}
		return 0, false
	}
	if up != "" {
		if upper, ok = read(up); !ok {
			return
		}
		hasUpper = true
	}
	if slash && low != "" {
		if lower, ok = read(low); !ok {
			return
		}
		hasLower = true
	}
	return upper, lower, hasUpper, hasLower, true
}

// TremoloStrokes is a tremolo mark's strokes, 1..4 (3 when bare), and
// whether the value is one.
func TremoloStrokes(value string) (int, bool) {
	switch value {
	case "":
		return 3, true
	case "1", "2", "3", "4":
		return int(value[0] - '0'), true
	}
	return 0, false
}

// VelocityMark is an event's vel= velocity (1..127), 0 when it has none.
func (e *Event) VelocityMark() int {
	for _, m := range e.Marks {
		if m.Name == "vel" {
			if v, ok := ParseVelocity(m.Value); ok {
				return v
			}
		}
	}
	return 0
}

// ParseVelocity reads a vel= value: a whole MIDI velocity 1..127.
func ParseVelocity(v string) (int, bool) {
	n := 0
	if v == "" || len(v) > 3 {
		return 0, false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, false
		}
		n = n*10 + int(v[i]-'0')
	}
	return n, n >= 1 && n <= 127
}

// SetVelocityMark sets an event's vel= velocity, or (v <= 0) takes it off.
func (e *Event) SetVelocityMark(v int) {
	var out []Mark
	for _, m := range e.Marks {
		if m.Name != "vel" {
			out = append(out, m)
		}
	}
	if v > 0 {
		out = append(out, Mark{Name: "vel", Value: strconv.Itoa(min(v, 127))})
	}
	e.Marks = out
}

// CrossStaff is where an event's notes are written: -1 on its line's staff
// above its own, 1 on the staff below (the mark cross=up, cross=down), 0
// on its own staff.
func (e *Event) CrossStaff() int {
	for _, m := range e.Marks {
		if m.Name == "cross" {
			switch m.Value {
			case "up":
				return -1
			case "down":
				return 1
			}
		}
	}
	return 0
}

// SetCrossStaff writes an event's notes on the staff above (-1) or below
// (1) its own, or (0) back on its own.
func (e *Event) SetCrossStaff(dir int) {
	var out []Mark
	for _, m := range e.Marks {
		if m.Name != "cross" {
			out = append(out, m)
		}
	}
	switch {
	case dir < 0:
		out = append(out, Mark{Name: "cross", Value: "up"})
	case dir > 0:
		out = append(out, Mark{Name: "cross", Value: "down"})
	}
	e.Marks = out
}
