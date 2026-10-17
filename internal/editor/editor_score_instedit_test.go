package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/score"
	"foundry/internal/tracker"
)

// instEditFixture is a library of one kit and one clarinet in a scratch
// project.
func instEditFixture(t *testing.T) {
	t.Helper()
	res := filepath.Join(t.TempDir(), "resources")
	os.MkdirAll(filepath.Join(res, "sounds", "instruments", "drums"), 0o755)
	os.MkdirAll(filepath.Join(res, "sounds", "instruments", "winds"), 0o755)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() {
		fileutil.ResourcesPath = prev
		tracker.ReloadInstruments()
		score.ReloadKits()
	})
	kit := tracker.NewInstrument("Kit", "")
	kit.Playback = tracker.Playback{Clef: "percussion", Drums: []tracker.DrumNote{{Key: 36, Name: "Kick", Line: 1, Head: "normal"}}}
	kit.Articulations = []tracker.Articulation{{Name: "rim", Triggers: []string{"rimshot"}}}
	if err := tracker.SaveInstrument("drums/test_kit", kit); err != nil {
		t.Fatal(err)
	}
	cl := tracker.NewInstrument("Clarinet", "")
	cl.Playback = tracker.Playback{Clef: "treble", Transposition: -2}
	if err := tracker.SaveInstrument("winds/test_clarinet", cl); err != nil {
		t.Fatal(err)
	}
}

// The library's notation settings: edited, refused when wrong, saved back
// to the .finst, the kit's map read anew by every staff.
func TestScoreInstrumentSettingsEdit(t *testing.T) {
	instEditFixture(t)
	prev := scEd
	scEd = scoreTestState(t)
	t.Cleanup(func() { scEd = prev })
	ie, err := scoreOpenInstEdit("winds/test_clarinet")
	if err != nil {
		t.Fatal(err)
	}
	ie.Low, ie.High = "D3", "Bb6"
	ie.In.Playback.Gliss = tracker.GlissChromatic
	ie.Dirty = true
	if err := ie.save(); err != nil {
		t.Fatal(err)
	}
	back, err := tracker.LoadInstrumentFile(tracker.InstrumentPath("winds/test_clarinet"))
	if err != nil {
		t.Fatal(err)
	}
	if tkToMIDI(back.Playback.Low) != 50 || tkToMIDI(back.Playback.High) != 94 || back.Playback.Gliss != tracker.GlissChromatic || back.Playback.Transposition != -2 {
		t.Errorf("saved %+v", back.Playback)
	}
	if ie2, _ := scoreOpenInstEdit("winds/test_clarinet"); ie2.Low != "D3" || ie2.High != "A#6" {
		t.Errorf("read back %q %q", ie2.Low, ie2.High)
	}
	// A range upside down, or not a note, is refused and nothing written.
	ie.High = "C3"
	if ie.save() == nil || !strings.Contains(ie.Err, "under") {
		t.Errorf("upside down: %q", ie.Err)
	}
	ie.High = "nope"
	if ie.save() == nil {
		t.Error("a range that is not a note saved")
	}

	// The kit: a sound added, a trigger word, the map read anew.
	kit, err := scoreOpenInstEdit("drums/test_kit")
	if err != nil {
		t.Fatal(err)
	}
	if score.KitFor("drums/test_kit").Drums[0].Name != "Kick" {
		t.Fatalf("kit %+v", score.KitFor("drums/test_kit"))
	}
	kit.addDrum()
	d := &kit.In.Playback.Drums[1]
	if d.Key != 35 || d.Name != "Bass Drum 2" {
		t.Errorf("added %+v", d)
	}
	d.Key, d.Name, d.Line, d.Head, d.Shortcut = 38, "Snare", 5, "x", "A"
	kit.Triggers[0] = "rimshot, cross stick"
	if err := kit.save(); err != nil {
		t.Fatal(err)
	}
	k := score.KitFor("drums/test_kit")
	if len(k.Drums) != 2 || k.Drums[1].Name != "Snare" || k.Drums[1].Head != score.DrumHeadX || k.Drums[1].Shortcut != 'A' {
		t.Errorf("the staff reads %+v", k.Drums)
	}
	back, _ = tracker.LoadInstrumentFile(tracker.InstrumentPath("drums/test_kit"))
	if strings.Join(back.Articulations[0].Triggers, "|") != "rimshot|cross stick" {
		t.Errorf("triggers %v", back.Articulations[0].Triggers)
	}
	// A map the file refuses (two sounds on one key) is not written.
	kit.In.Playback.Drums[1].Key = 36
	if kit.save() == nil {
		t.Error("two sounds on key 36 saved")
	}
	kit.removeDrum(1)
	if len(kit.In.Playback.Drums) != 1 {
		t.Error("remove")
	}
}

// Open in instrument editor: the Tracker gets a scratch song with the
// instrument's copy, its library name ready to save back; a Tracker song
// with unsaved edits is left alone.
func TestScoreOpenInInstrumentEditor(t *testing.T) {
	instEditFixture(t)
	prevTrk, prevSave := trkEd, trkLibSaveName
	t.Cleanup(func() { trkEd, trkLibSaveName = prevTrk, prevSave })
	trkEd = nil
	if err := scoreOpenInInstrumentEditor("winds/test_clarinet"); err != nil {
		t.Fatal(err)
	}
	if len(trkEd.Song.Instruments) != 1 || trkEd.Song.Instruments[0].Ref != "" || trkEd.Instr != 1 || trkLibSaveName != "winds/test_clarinet" {
		t.Errorf("tracker: %d instruments, ref %q, slot %d, save name %q", len(trkEd.Song.Instruments), trkEd.Song.Instruments[0].Ref, trkEd.Instr, trkLibSaveName)
	}
	if trkEd.Song.Instruments[0].Playback.Transposition != -2 {
		t.Error("the copy lost the instrument's settings")
	}
	// Saved back from the Tracker: the library file is the slot's.
	trkEd.Song.Instruments[0].Playback.Transposition = -3
	if err := trkEd.saveInstrumentToLibrary(1, trkLibSaveName); err != nil {
		t.Fatal(err)
	}
	if back, _ := tracker.LoadInstrumentFile(tracker.InstrumentPath("winds/test_clarinet")); back.Playback.Transposition != -3 {
		t.Errorf("not written back: %+v", back.Playback)
	}
	// An unsaved Tracker song stays.
	trkEd.Song.BPM = 99
	trkEd.touch()
	saveStateFresh = true
	defer func() { saveStateFresh = false }()
	if err := scoreOpenInInstrumentEditor("drums/test_kit"); err == nil || trkEd.Song.BPM != 99 {
		t.Errorf("replaced an unsaved song: %v", err)
	}
}
