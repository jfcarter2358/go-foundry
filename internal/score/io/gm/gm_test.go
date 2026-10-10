package gm

import (
	"path/filepath"
	"runtime"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/tracker"
)

func useRepoResources(t *testing.T) {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(filepath.Dir(here), "..", "..", "..", "..", "resources")
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
}

// Every instrument the tables name is in the starter library.
func TestEveryTargetIsInTheLibrary(t *testing.T) {
	useRepoResources(t)
	check := func(what, name string) {
		if tracker.InstrumentPath(name) == "" {
			t.Errorf("%s: no %s in the library", what, name)
		}
	}
	for p := 0; p < 128; p++ {
		check(Names[p], Instrument(p))
	}
	for _, r := range rules {
		check("rule "+r.phrases[0], r.instrument)
	}
	for cat, fs := range families {
		for _, f := range fs {
			check("family "+cat, f)
		}
	}
	check("drum kit", DrumKit)
	check("fallback", Fallback)
}

// Every library instrument exports as a program, and the instruments a
// program imports as come back as themselves (several library instruments
// share a program, so not every one can).
func TestProgramRoundTrips(t *testing.T) {
	useRepoResources(t)
	for _, name := range tracker.ScanInstrumentNames() {
		if Percussive(name) {
			continue
		}
		p := Program(name)
		if p < 0 || p > 127 {
			t.Errorf("%s: program %d", name, p)
		}
	}
	for _, name := range []string{"strings/violin_section", "woodwinds/flute", "brass/horn", "keys/grand_piano", "choir/choir"} {
		if got := Instrument(Program(name)); got != name {
			t.Errorf("%s → %d → %s", name, Program(name), got)
		}
	}
}

func TestMatchNames(t *testing.T) {
	for name, want := range map[string]string{
		"Violin I": "strings/violin_section", "Vln. 2": "strings/violin_section", "Violoncello": "strings/cello_section",
		"Double Bass": "strings/contrabass_section", "Clarinet in Bb": "woodwinds/clarinet", "Bass Clarinet": "woodwinds/clarinet",
		"wind.reed.clarinet.bflat": "woodwinds/clarinet", "Horn in F": "brass/horn", "English Horn": "woodwinds/oboe",
		"brass.french-horn": "brass/horn", "Piano": "keys/grand_piano", "Pno.": "keys/grand_piano",
		"keyboard.piano.grand": "keys/grand_piano", "Timpani": "percussion/timpani", "pitched-percussion.timpani": "percussion/timpani",
		"Drum Set": DrumKit, "Bass Drum": DrumKit, "Percussion": "percussion/orchestral_percussion",
		"Alto Saxophone": "woodwinds/clarinet", "Soprano": "choir/choir", "Electric Bass": "bass/bass_finger",
		"Acoustic Guitar": "plucked/guitar_steel", "Strings": "strings/violin_section", "Harp": "keys/harp",
		"Synth Pad": "pads/pad_warm", "Whatever": "",
	} {
		if got := Match(name); got != want {
			t.Errorf("Match(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestPickFallsBack(t *testing.T) {
	useRepoResources(t)
	if got := Pick("strings/no_such_thing"); got != "strings/violin_section" {
		t.Errorf("family fallback: %s", got)
	}
	if got := Pick("nope/none"); got != Fallback {
		t.Errorf("fallback: %s", got)
	}
	if d, c := Transposition(-2); d != -1 || c != -2 {
		t.Errorf("-2: %d %d", d, c)
	}
	if d, _ := Transposition(-7); d != -4 {
		t.Errorf("-7: %d", d)
	}
	if d, _ := Transposition(12); d != 7 {
		t.Errorf("+12: %d", d)
	}
}
