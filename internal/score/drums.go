package score

// drums.go — where an unpitched hit sits on a percussion staff.
//
// A drum's notes are stored as the MIDI keys that play them (an acoustic
// snare is key 38, so "D2"), but a percussion staff does not show pitch:
// each sound of a kit has a place on the staff, a notehead shape, and
// perhaps an articulation drawn with it, after the common drum-set
// notation (bass drum in the bottom space, snare in the third, toms
// between, hi-hat above the staff with an x — "o" over it open —, crash on
// the ledger line above it, ride on the top line).
//
// That map is the instrument's: a .finst's playback block names its drums
// (tracker.DrumNote), and Kit is the map a staff reads. An instrument that
// names none — and a score whose instrument cannot be read — uses GMKit,
// the General MIDI kit, keys 35-59; a key a map does not name is shown at
// its own pitch's place with a plain head. A single drum (a tambourine, a
// triangle) maps its one sound onto the line of a one-line staff
// (Staff.Lines).
//
// The map works both ways. Writing, a click on a line or space writes the
// first sound there and a drum's shortcut letter writes it; playing, an
// articulation mark on a note picks the sound at the same place and head
// that names it — a closed hi-hat with "open" over it plays the open one.

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"foundry/internal/tracker"
)

// DrumHead is a notehead shape on a percussion staff.
type DrumHead uint8

const (
	DrumHeadNormal DrumHead = iota
	DrumHeadX
	// DrumHeadCircleX is an x in a circle: an open hi-hat, a side stick.
	DrumHeadCircleX
	DrumHeadDiamond
	DrumHeadTriangle
	// DrumHeadSlash is a slash: a rhythm to play without saying what.
	DrumHeadSlash
)

// drumHeadNames are the heads as a .finst names them (tracker.DrumHeads).
var drumHeadNames = []string{"normal", "x", "circle_x", "diamond", "triangle", "slash"}

// String names a head as a drum map does.
func (h DrumHead) String() string {
	if int(h) < len(drumHeadNames) {
		return drumHeadNames[h]
	}
	return "normal"
}

// ParseDrumHead reads a head's name ("" is normal).
func ParseDrumHead(s string) (DrumHead, bool) {
	if s == "" {
		return DrumHeadNormal, true
	}
	for i, n := range drumHeadNames {
		if n == s {
			return DrumHead(i), true
		}
	}
	return DrumHeadNormal, false
}

// Drum is one sound of a kit as a percussion staff shows it.
type Drum struct {
	// Key is the MIDI key the note is stored as and plays.
	Key  int
	Name string
	// Pos is the staff position in half spaces above the bottom line: 0
	// the bottom line, 1 the space above it, 4 the middle line, 9 the space
	// above the staff.
	Pos  int
	Head DrumHead
	// Articulation is a mark drawn with the note ("open", "stopped"),
	// telling it from another sound at the same place with the same head.
	Articulation string
	// Shortcut is the letter (A-G) that writes it in note input.
	Shortcut byte
}

// GMDrums is the General MIDI kit, in the order a click on a staff
// position chooses between instruments that share it (the first wins).
var GMDrums = []Drum{
	{Key: 36, Name: "Bass Drum", Pos: 1, Shortcut: 'B'},
	{Key: 35, Name: "Bass Drum 2", Pos: 0},
	{Key: 38, Name: "Snare", Pos: 5, Shortcut: 'A'},
	{Key: 37, Name: "Side Stick", Pos: 5, Head: DrumHeadCircleX},
	{Key: 40, Name: "Electric Snare", Pos: 5, Head: DrumHeadDiamond},
	{Key: 39, Name: "Hand Clap", Pos: 4, Head: DrumHeadX},
	{Key: 42, Name: "Closed Hi-Hat", Pos: 9, Head: DrumHeadX, Shortcut: 'G'},
	{Key: 46, Name: "Open Hi-Hat", Pos: 9, Head: DrumHeadCircleX},
	{Key: 44, Name: "Pedal Hi-Hat", Pos: -1, Head: DrumHeadX, Shortcut: 'F'},
	{Key: 49, Name: "Crash Cymbal", Pos: 10, Head: DrumHeadX, Shortcut: 'C'},
	{Key: 57, Name: "Crash Cymbal 2", Pos: 12, Head: DrumHeadX},
	{Key: 50, Name: "High Tom", Pos: 8, Shortcut: 'E'},
	{Key: 51, Name: "Ride Cymbal", Pos: 8, Head: DrumHeadX, Shortcut: 'D'},
	{Key: 53, Name: "Ride Bell", Pos: 8, Head: DrumHeadDiamond},
	{Key: 59, Name: "Ride Cymbal 2", Pos: 6, Head: DrumHeadX},
	{Key: 48, Name: "Hi-Mid Tom", Pos: 7},
	{Key: 54, Name: "Tambourine", Pos: 7, Head: DrumHeadX},
	{Key: 56, Name: "Cowbell", Pos: 7, Head: DrumHeadTriangle},
	{Key: 47, Name: "Low-Mid Tom", Pos: 6},
	{Key: 45, Name: "Low Tom", Pos: 4},
	{Key: 43, Name: "High Floor Tom", Pos: 3},
	{Key: 58, Name: "Vibraslap", Pos: 3, Head: DrumHeadX},
	{Key: 41, Name: "Low Floor Tom", Pos: 2},
	{Key: 52, Name: "Chinese Cymbal", Pos: 11, Head: DrumHeadX},
	{Key: 55, Name: "Splash Cymbal", Pos: 11, Head: DrumHeadCircleX},
}

// Kit is a drum map: the sounds a percussion staff writes, in the order a
// click on a staff position chooses between those that share it.
type Kit struct {
	// Name is where the map came from: an instrument's name, or "General
	// MIDI".
	Name  string
	Drums []Drum
	// GM reports the General MIDI map (the instrument names none).
	GM bool
}

// GMKit is the General MIDI kit's map.
var GMKit = &Kit{Name: "General MIDI", Drums: GMDrums, GM: true}

// KitOf is an instrument's drum map: its own, or the General MIDI kit's.
func KitOf(in *tracker.Instrument) *Kit {
	if in == nil || len(in.Playback.Drums) == 0 {
		return GMKit
	}
	k := &Kit{Name: in.Name}
	for _, d := range in.Playback.Drums {
		h, _ := ParseDrumHead(d.Head)
		dr := Drum{Key: d.Key, Name: d.Name, Pos: d.Line, Head: h, Articulation: d.Articulation}
		if d.Shortcut != "" {
			dr.Shortcut = strings.ToUpper(d.Shortcut)[0]
		}
		k.Drums = append(k.Drums, dr)
	}
	return k
}

// ForKey is the sound a key plays, if the map names it.
func (k *Kit) ForKey(key int) (Drum, bool) {
	for _, d := range k.Drums {
		if d.Key == key {
			return d, true
		}
	}
	return Drum{}, false
}

// At is the sound a click at a staff position writes: the first the map
// puts there.
func (k *Kit) At(pos int) (Drum, bool) {
	for _, d := range k.Drums {
		if d.Pos == pos {
			return d, true
		}
	}
	return Drum{}, false
}

// ByShortcut is the sound a letter writes in note input.
func (k *Kit) ByShortcut(letter byte) (Drum, bool) {
	if letter >= 'a' && letter <= 'z' {
		letter -= 'a' - 'A'
	}
	for _, d := range k.Drums {
		if d.Shortcut == letter {
			return d, true
		}
	}
	return Drum{}, false
}

// Display is where a stored pitch shows on a percussion staff, with what
// head and what articulation: its sound's, or (a key the map does not
// name) its own letter's place, a plain head and none.
func (k *Kit) Display(p Pitch) (pos int, head DrumHead, art string) {
	if d, ok := k.ForKey(p.MIDI()); ok {
		return d.Pos, d.Head, d.Articulation
	}
	return p.Diatonic() - PercussionBottom, DrumHeadNormal, ""
}

// NameOf names what a stored pitch plays: "Snare", or "key 61".
func (k *Kit) NameOf(p Pitch) string {
	if d, ok := k.ForKey(p.MIDI()); ok {
		return d.Name
	}
	return fmt.Sprintf("key %d", p.MIDI())
}

// Sound is the key a note plays: its own, unless the event carries an
// articulation that another sound at the same place with the same head is
// written with (a closed hi-hat marked "open" plays the open hi-hat).
func (k *Kit) Sound(key int, marks []Mark) int {
	d, ok := k.ForKey(key)
	if !ok || len(marks) == 0 {
		return key
	}
	for _, m := range marks {
		if m.Name == d.Articulation {
			return key
		}
	}
	for _, m := range marks {
		for _, o := range k.Drums {
			if o.Articulation != "" && o.Articulation == m.Name && o.Pos == d.Pos && o.Head == d.Head {
				return o.Key
			}
		}
	}
	return key
}

// Step is the sound a stored key moves to when it is moved up (dir > 0) or
// down a place: the next sound in the map at the next position that has
// one, or (several at one place) the next of them in the map's order — so
// Up and Down walk through every sound of the kit.
func (k *Kit) Step(key, dir int) (Drum, bool) {
	cur, ok := k.ForKey(key)
	order := k.Ordered()
	if len(order) == 0 {
		return Drum{}, false
	}
	if !ok {
		p := SpellMIDI(key, 0, 1).Diatonic() - PercussionBottom
		for i := range order {
			j := i
			if dir < 0 {
				j = len(order) - 1 - i
			}
			if (dir > 0 && order[j].Pos > p) || (dir < 0 && order[j].Pos < p) {
				return order[j], true
			}
		}
		return Drum{}, false
	}
	for i, d := range order {
		if d.Key == cur.Key {
			j := i + 1
			if dir < 0 {
				j = i - 1
			}
			if j < 0 || j >= len(order) {
				return Drum{}, false
			}
			return order[j], true
		}
	}
	return Drum{}, false
}

// FromGM is the key of this map that plays a General MIDI drum key (a
// MIDI file's channel 10): the key itself when the map names it, else
// the sound of the same name, else the one written at the same place with
// the same head (and articulation). false when nothing in the map
// answers it: the key is kept as it is.
func (k *Kit) FromGM(key int) (int, bool) {
	if k == nil || k.GM {
		return key, true
	}
	if _, ok := k.ForKey(key); ok {
		return key, true
	}
	gm, ok := GMKit.ForKey(key)
	if !ok {
		return key, false
	}
	for _, d := range k.Drums {
		if strings.EqualFold(d.Name, gm.Name) {
			return d.Key, true
		}
	}
	for _, d := range k.Drums {
		if d.Pos == gm.Pos && d.Head == gm.Head && d.Articulation == gm.Articulation {
			return d.Key, true
		}
	}
	return key, false
}

// ToGM is FromGM the other way: the General MIDI key a sound of this map
// is, by its name, then its place and head.
func (k *Kit) ToGM(key int) (int, bool) {
	if k == nil || k.GM {
		return key, true
	}
	d, ok := k.ForKey(key)
	if !ok {
		_, gm := GMKit.ForKey(key)
		return key, gm
	}
	for _, g := range GMKit.Drums {
		if strings.EqualFold(g.Name, d.Name) {
			return g.Key, true
		}
	}
	for _, g := range GMKit.Drums {
		if g.Pos == d.Pos && g.Head == d.Head {
			return g.Key, true
		}
	}
	return key, false
}

// Ordered is the map's sounds from the lowest place up, sounds sharing a
// place in the map's order.
func (k *Kit) Ordered() []Drum {
	out := append([]Drum(nil), k.Drums...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Pos < out[j-1].Pos; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// DrumPitch is the pitch a drum is stored as: its key, spelled with sharps.
func DrumPitch(d Drum) Pitch { return SpellMIDI(d.Key, 0, 1) }

// PercussionBottom is the diatonic number of a percussion staff's bottom
// line: its positions are counted from E4 as a treble staff's are.
var PercussionBottom = Pitch{Step: 2, Octave: 4}.Diatonic()

// The General MIDI kit's map, as the functions every percussion staff used
// before maps were per instrument.

// DrumForKey is the General MIDI kit's sound for a key.
func DrumForKey(key int) (Drum, bool) { return GMKit.ForKey(key) }

// DrumAt is the General MIDI kit's sound at a staff position.
func DrumAt(pos int) (Drum, bool) { return GMKit.At(pos) }

// DrumDisplay is where a stored pitch shows in the General MIDI kit's map.
func DrumDisplay(p Pitch) (pos int, head DrumHead) {
	pos, head, _ = GMKit.Display(p)
	return pos, head
}

// DrumName names what a stored pitch plays in the General MIDI kit.
func DrumName(p Pitch) string { return GMKit.NameOf(p) }

// ---------- the library's maps ----------

// LoadKitInstrument reads a library instrument for its drum map, without
// its samples. It is a variable so tests can give staves maps without the
// library.
var LoadKitInstrument = func(name string) (*tracker.Instrument, time.Time, error) {
	path := tracker.InstrumentPath(name)
	if path == "" {
		return nil, time.Time{}, fmt.Errorf("no instrument %q", name)
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	in, err := tracker.LoadInstrumentFile(path)
	return in, st.ModTime(), err
}

type kitEntry struct {
	kit     *Kit
	mod     time.Time
	checked time.Time
}

var (
	kitMu    sync.Mutex
	kitCache = map[string]kitEntry{}
)

// kitRecheck is how often a cached map looks at its file again: an
// instrument edited on disk is seen within it.
const kitRecheck = 2 * time.Second

// KitFor is a library instrument's drum map (GMKit when it names none or
// cannot be read), cached and re-read when its file changes.
func KitFor(instrument string) *Kit {
	if instrument == "" {
		return GMKit
	}
	now := time.Now()
	kitMu.Lock()
	e, ok := kitCache[instrument]
	kitMu.Unlock()
	if ok && now.Sub(e.checked) < kitRecheck {
		return e.kit
	}
	in, mod, err := LoadKitInstrument(instrument)
	kit := GMKit
	if err == nil {
		if ok && mod.Equal(e.mod) {
			kit = e.kit
		} else {
			kit = KitOf(in)
		}
	}
	kitMu.Lock()
	kitCache[instrument] = kitEntry{kit: kit, mod: mod, checked: now}
	kitMu.Unlock()
	return kit
}

// ReloadKits drops the cached drum maps.
func ReloadKits() {
	kitMu.Lock()
	kitCache = map[string]kitEntry{}
	kitMu.Unlock()
}

// Kit is the drum map a line's percussion staves read: its instrument's.
func (l *Line) Kit() *Kit { return KitFor(l.Instrument) }

// Percussion reports a percussion staff: drums by place, not pitches.
func (st *Staff) Percussion() bool { return st.Clef == "percussion" }

// StaffLines is how many lines a staff has: its Lines, or five.
func (st *Staff) StaffLines() int {
	if st.Lines <= 0 || st.Lines > 5 {
		return 5
	}
	return st.Lines
}
