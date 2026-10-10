package score

// drums.go — where an unpitched hit sits on a percussion staff.
//
// A drum kit's notes are stored as their General MIDI percussion keys (an
// acoustic snare is key 38, so "D2"; that is what plays the kit's .finst),
// but a percussion staff does not show pitch: each instrument of the kit has
// a place on the staff and a notehead shape, after the common drum-set
// notation (bass drum in the bottom space, snare in the third, toms between,
// hi-hat above the staff with an x, crash on the ledger line above it, ride
// on the top line). GMDrums is that map for the General MIDI kit, keys 35-59;
// a key it does not name is shown at its own pitch's place with a plain
// head. Per-instrument maps are a later phase's (docs/score/SPEC.md, phase
// 6); this one is what every percussion staff uses until then.

import "fmt"

// DrumHead is a notehead shape on a percussion staff.
type DrumHead uint8

const (
	DrumHeadNormal DrumHead = iota
	DrumHeadX
	// DrumHeadCircleX is an x in a circle: an open hi-hat, a side stick.
	DrumHeadCircleX
	DrumHeadDiamond
	DrumHeadTriangle
)

// Drum is one instrument of a kit as a percussion staff shows it.
type Drum struct {
	// Key is the General MIDI percussion key (the note played).
	Key  int
	Name string
	// Pos is the staff position in half spaces above the bottom line: 0
	// the bottom line, 1 the space above it, 4 the middle line, 9 the space
	// above the staff.
	Pos  int
	Head DrumHead
}

// GMDrums is the General MIDI kit, in the order a click on a staff
// position chooses between instruments that share it (the first wins).
var GMDrums = []Drum{
	{36, "Bass Drum", 1, DrumHeadNormal},
	{35, "Bass Drum 2", 0, DrumHeadNormal},
	{38, "Snare", 5, DrumHeadNormal},
	{37, "Side Stick", 5, DrumHeadCircleX},
	{40, "Electric Snare", 5, DrumHeadDiamond},
	{39, "Hand Clap", 4, DrumHeadX},
	{42, "Closed Hi-Hat", 9, DrumHeadX},
	{46, "Open Hi-Hat", 9, DrumHeadCircleX},
	{44, "Pedal Hi-Hat", -1, DrumHeadX},
	{49, "Crash Cymbal", 10, DrumHeadX},
	{57, "Crash Cymbal 2", 12, DrumHeadX},
	{50, "High Tom", 8, DrumHeadNormal},
	{51, "Ride Cymbal", 8, DrumHeadX},
	{53, "Ride Bell", 8, DrumHeadDiamond},
	{59, "Ride Cymbal 2", 6, DrumHeadX},
	{48, "Hi-Mid Tom", 7, DrumHeadNormal},
	{54, "Tambourine", 7, DrumHeadX},
	{56, "Cowbell", 7, DrumHeadTriangle},
	{47, "Low-Mid Tom", 6, DrumHeadNormal},
	{45, "Low Tom", 4, DrumHeadNormal},
	{43, "High Floor Tom", 3, DrumHeadNormal},
	{58, "Vibraslap", 3, DrumHeadX},
	{41, "Low Floor Tom", 2, DrumHeadNormal},
	{52, "Chinese Cymbal", 11, DrumHeadX},
	{55, "Splash Cymbal", 11, DrumHeadCircleX},
}

// DrumForKey is the kit instrument a key plays, if the map names it.
func DrumForKey(key int) (Drum, bool) {
	for _, d := range GMDrums {
		if d.Key == key {
			return d, true
		}
	}
	return Drum{}, false
}

// DrumAt is the instrument a click at a staff position writes: the first
// the map puts there.
func DrumAt(pos int) (Drum, bool) {
	for _, d := range GMDrums {
		if d.Pos == pos {
			return d, true
		}
	}
	return Drum{}, false
}

// PercussionBottom is the diatonic number of a percussion staff's bottom
// line: its positions are counted from E4 as a treble staff's are.
var PercussionBottom = Pitch{Step: 2, Octave: 4}.Diatonic()

// DrumDisplay is where a stored pitch shows on a percussion staff and with
// what head: its kit instrument's place, or (a key the map does not name)
// its own letter's place, without an accidental.
func DrumDisplay(p Pitch) (pos int, head DrumHead) {
	if d, ok := DrumForKey(p.MIDI()); ok {
		return d.Pos, d.Head
	}
	return p.Diatonic() - PercussionBottom, DrumHeadNormal
}

// DrumPitch is the pitch a drum is stored as: its key, spelled with sharps.
func DrumPitch(d Drum) Pitch { return SpellMIDI(d.Key, 0, 1) }

// DrumName names what a stored pitch plays on a kit: "Snare", or "key 61".
func DrumName(p Pitch) string {
	if d, ok := DrumForKey(p.MIDI()); ok {
		return d.Name
	}
	return fmt.Sprintf("key %d", p.MIDI())
}
