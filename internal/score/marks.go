package score

// marks.go — the vocabulary of event marks.
//
// A mark is a name and an optional value attached to an event. The list is
// open: a name this build does not know is kept, saved and shown by
// `foundry score inspect` as a warning, so a file written by a newer build
// loses nothing in an older one. The names here are the ones playback and
// engraving understand. Inside a voice's "( )" a few marks have the
// one-character spellings musicians already use for them (Aliases).

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
}

// DefaultDynamic is the level a line plays at before its first dynamic.
const DefaultDynamic = "mf"

// markKinds are the known names other than dynamics.
var markKinds = map[string]MarkKind{
	// articulations
	"stacc": MarkArticulation, "staccmo": MarkArticulation, "acc": MarkArticulation,
	"ten": MarkArticulation, "marc": MarkArticulation, "portato": MarkArticulation,
	"legato": MarkArticulation,
	// ornaments
	"tr": MarkOrnament, "mord": MarkOrnament, "imord": MarkOrnament, "turn": MarkOrnament,
	"iturn": MarkOrnament, "trem": MarkOrnament,
	// notation only
	"ferm": MarkNotation, "arp": MarkNotation, "up": MarkNotation, "down": MarkNotation,
	// text
	"text": MarkText, "expr": MarkText, "tech": MarkTechnique,
	// spanners: starts and ends (playback in a later phase)
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
