package editor

// editor_score_instedit.go — editing a library instrument from the Score
// editor's instrument library (mockup screen 4).
//
// Two halves, as a .finst has two:
//
//   - What the notation reads (the instrument's playback block): the range
//     and clef a new line takes, its transposition, how its glissandi and
//     staccatos play, its release, a single drum's one-line staff, a
//     fretted instrument's tuning, a kit's drum map (each sound's key,
//     name, staff place, notehead, articulation and note-input letter), and
//     the words that pick each articulation. These are edited here, in the
//     library dialog, and saved back to the .finst; every score and song
//     using the instrument reads them from there.
//   - The sound (samples, zones, velocity layers, round robin, the
//     articulations' own samples, gain and release, envelopes, filter):
//     the Tracker's instrument editor edits those (its "Layers &
//     articulations" section is editor_tracker_layers.go). "Open in
//     instrument editor"
//     opens the Tracker on a scratch song holding a copy of the instrument,
//     with its library name ready in "Save to library", which writes it
//     back.
//
// The model here has no ImGui (the panel is in editor_score_library.go).

import (
	"fmt"
	"strings"

	"foundry/internal/score"
	"foundry/internal/tracker"
	"foundry/internal/uiwin"
)

// scoreInstEdit is a library instrument's notation settings being edited.
type scoreInstEdit struct {
	// Name is the library name ("strings/violin_section").
	Name string
	// In is the instrument as its file has it (samples not resolved).
	In *tracker.Instrument
	// Low and High are the range as note names ("G3"; "" unbounded).
	Low, High string
	// Triggers are each articulation's trigger words, comma-separated.
	Triggers []string
	// Dirty is an edit not saved yet; Err why the last save was refused.
	Dirty bool
	Err   string
}

// scoreMidiName is a MIDI note as a score spells it ("G3", "C#4").
func scoreMidiName(m int) string { return score.SpellMIDI(m, 0, 1).String() }

// tkFromMIDI and tkToMIDI move between MIDI and the tracker's notes.
func tkFromMIDI(m int) uint8 { return uint8(m - 60 + int(tracker.NoteC4)) }
func tkToMIDI(n uint8) int   { return int(n) + 60 - int(tracker.NoteC4) }

// scoreOpenInstEdit reads a library instrument for editing.
func scoreOpenInstEdit(name string) (*scoreInstEdit, error) {
	path := tracker.InstrumentPath(name)
	if path == "" {
		return nil, fmt.Errorf("no instrument %q", name)
	}
	in, err := tracker.LoadInstrumentFile(path)
	if err != nil {
		return nil, err
	}
	ie := &scoreInstEdit{Name: name, In: in}
	if in.Playback.Low > 0 {
		ie.Low = scoreMidiName(tkToMIDI(in.Playback.Low))
	}
	if in.Playback.High > 0 {
		ie.High = scoreMidiName(tkToMIDI(in.Playback.High))
	}
	for _, a := range in.Articulations {
		ie.Triggers = append(ie.Triggers, strings.Join(a.Triggers, ", "))
	}
	return ie, nil
}

// parseRangeNote reads a range end: a note name ("G3", "Bb5") or nothing.
func parseRangeNote(s string) (uint8, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	p, err := score.ParsePitch(s)
	if err != nil {
		return 0, fmt.Errorf("range %q: write a note like G3 or Bb5", s)
	}
	m := p.MIDI()
	if m < 0 || m > 127 {
		return 0, fmt.Errorf("range %q is outside MIDI", s)
	}
	n := tkFromMIDI(m)
	if n < tracker.NoteMin || n > tracker.NoteMax {
		return 0, fmt.Errorf("range %q is outside what the engine plays", s)
	}
	return n, nil
}

// apply puts the text fields into the instrument.
func (ie *scoreInstEdit) apply() error {
	lo, err := parseRangeNote(ie.Low)
	if err != nil {
		return err
	}
	hi, err := parseRangeNote(ie.High)
	if err != nil {
		return err
	}
	if lo > 0 && hi > 0 && hi < lo {
		return fmt.Errorf("the range's top %s is under its bottom %s", ie.High, ie.Low)
	}
	ie.In.Playback.Low, ie.In.Playback.High = lo, hi
	for i := range ie.In.Articulations {
		var words []string
		if i < len(ie.Triggers) {
			for _, w := range strings.Split(ie.Triggers[i], ",") {
				if w = strings.TrimSpace(w); w != "" {
					words = append(words, w)
				}
			}
		}
		ie.In.Articulations[i].Triggers = words
	}
	return nil
}

// addDrum adds a sound to the drum map: the next key the map has not
// named, on the middle line.
func (ie *scoreInstEdit) addDrum() {
	used := map[int]bool{}
	for _, d := range ie.In.Playback.Drums {
		used[d.Key] = true
	}
	key := 35
	for used[key] && key < 127 {
		key++
	}
	name := "Drum"
	if d, ok := score.GMKit.ForKey(key); ok {
		name = d.Name
	}
	ie.In.Playback.Drums = append(ie.In.Playback.Drums, tracker.DrumNote{Key: key, Name: name, Line: 4, Head: "normal"})
	ie.Dirty = true
}

// removeDrum takes sound i out of the drum map.
func (ie *scoreInstEdit) removeDrum(i int) {
	ds := ie.In.Playback.Drums
	if i < 0 || i >= len(ds) {
		return
	}
	ie.In.Playback.Drums = append(ds[:i:i], ds[i+1:]...)
	ie.Dirty = true
}

// save checks the instrument as its file would read and writes it back:
// the instrument and drum map caches go, so every line playing it and every
// staff reading its map hears and shows the new settings.
func (ie *scoreInstEdit) save() error {
	ie.Err = ""
	if err := ie.apply(); err != nil {
		ie.Err = err.Error()
		return err
	}
	b, err := tracker.MarshalInstrument(ie.In)
	if err == nil {
		_, err = tracker.ParseInstrument(b)
	}
	if err != nil {
		ie.Err = err.Error()
		return err
	}
	if err := tracker.SaveInstrumentFile(tracker.InstrumentPath(ie.Name), ie.In); err != nil {
		ie.Err = err.Error()
		return err
	}
	ie.Dirty = false
	scoreInstrumentChanged()
	return nil
}

// scoreOpenInInstrumentEditor opens the Tracker's instrument editor on a
// library instrument: a scratch song holding a copy of it, its library
// name ready in "Save to library". A Tracker song with unsaved edits is
// not replaced.
func scoreOpenInInstrumentEditor(name string) error {
	if trkEd != nil && trackerEditorDirty() {
		return fmt.Errorf("the Tracker holds unsaved changes: save its song first")
	}
	lib, err := tracker.LoadInstrument(name)
	if err != nil {
		return err
	}
	s := trkTemplateEmpty()
	in := lib.Clone()
	in.Embed()
	if in.Name == "" {
		in.Name = scoreInstrumentLabel(name)
	}
	s.Instruments = []*tracker.Instrument{in}
	s.Name = "Instrument: " + scoreInstrumentLabel(name)
	tracker.ResolveSamples(s)
	trkEnsureDoc()
	trkEd.setSong(s)
	trkEd.Name = ""
	trkEd.Instr = 1
	trkLibSaveName, trkLibStatus = name, ""
	trkMarkSaved()
	trkEd.Status = "Editing the library's " + name + ": Save to library writes it back"
	uiwin.RequestWindowOpen(func() {
		OpenTrackerEditorWindow()
		trkSyncPlayer(true)
	})
	return nil
}
