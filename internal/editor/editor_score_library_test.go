package editor

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/testenv"
	"foundry/internal/tracker"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// scoreLibFixture is a project with the repo's library copied in by name
// only where needed: an empty sounds/ folder, and an .sfz with two
// keyswitched articulations and two velocity layers to import.
func scoreLibFixture(t *testing.T) (sfzPath string) {
	t.Helper()
	res := filepath.Join(t.TempDir(), "resources")
	if err := os.MkdirAll(filepath.Join(res, "sounds"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() {
		fileutil.ResourcesPath = prev
		tracker.ReloadSongs()
	})
	src := t.TempDir()
	wav := func(name string, hz float64, amp float32) {
		d := make([]float32, 24000)
		for i := range d {
			d[i] = amp * float32(math.Sin(2*math.Pi*hz*float64(i)/48000))
		}
		f, err := os.Create(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := tracker.WriteWAV(f, d, 1, 48000, 16); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	wav("soft.wav", 220, 0.2)
	wav("loud.wav", 220, 0.8)
	wav("pizz.wav", 330, 0.6)
	sfzPath = filepath.Join(src, "Chamber Strings.sfz")
	text := `<global> sw_lokey=c1 sw_hikey=c#1 sw_default=c1
<group> sw_last=c1 sw_label=Arco
<region> sample=soft.wav lokey=g3 hikey=e7 pitch_keycenter=a3 lovel=0 hivel=63
<region> sample=loud.wav lokey=g3 hikey=e7 pitch_keycenter=a3 lovel=64 hivel=127
<group> sw_last=c#1 sw_label=Pizz.
<region> sample=pizz.wav lokey=g3 hikey=e7 pitch_keycenter=e4
`
	if err := os.WriteFile(sfzPath, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return sfzPath
}

// waitImport waits for the background import to land.
func waitImport(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for scoreLibrary.result != nil {
		if time.Now().After(deadline) {
			t.Fatal("import did not finish")
		}
		scoreLibPollImport()
		time.Sleep(5 * time.Millisecond)
	}
}

// Importing an SFZ from the dialog puts it in the Imported category with
// its articulations, layers and range, ready to use on a line.
func TestScoreLibraryImportsAndUses(t *testing.T) {
	path := scoreLibFixture(t)
	prevEd := scEd
	t.Cleanup(func() { scEd = prevEd; scoreLibrary.entries = nil; scoreLibrary.result = nil })
	scEd = scoreTestState(t)
	scoreOpenLibrary(0, false)
	if len(scoreLibrary.entries) != 0 {
		t.Fatalf("an empty library lists %d", len(scoreLibrary.entries))
	}
	scoreLibChoose(path)
	scoreLibStartImport()
	waitImport(t)
	if !strings.HasPrefix(scEd.Status, "Imported 1 instrument into imported/") {
		t.Fatalf("status %q", scEd.Status)
	}
	L := &scoreLibrary
	if L.sel != "imported/chamber_strings" || L.cat != "imported" || len(L.entries) != 1 {
		t.Fatalf("selected %q in %q of %d", L.sel, L.cat, len(L.entries))
	}
	en := L.entries[0]
	if en.Source != "SFZ" || en.Title != "Chamber Strings" || en.Arts != 1 {
		t.Errorf("entry %+v", en)
	}
	d := scoreLibDescribe(en.Name, en.Inst)
	if d.Range != "G3 - E7" || d.Layers != "2 (pp / f)" || d.RoundRobin != "none" || d.Clef != "Treble" {
		t.Errorf("details %+v", d)
	}
	if strings.Join(d.Articulations, ",") != "sustain,pizzicato (pizz.)" {
		t.Errorf("articulations %v", d.Articulations)
	}
	if shown := scoreLibShown(L.entries, "", "pizz"); len(shown) != 0 {
		t.Errorf("search matched articulation words: %d", len(shown))
	}
	if shown := scoreLibShown(L.entries, "", "sfz"); len(shown) != 1 {
		t.Errorf("search by source: %d", len(shown))
	}
	scoreLibUse(L.sel)
	if got := scEd.Score.Lines[0].Instrument; got != "imported/chamber_strings" {
		t.Errorf("the line plays %q", got)
	}
	// The dialog's whole frame: the columns, then the import panel.
	scoreDrawTest(t)
	scEd.Score.Lines[0].Instrument = "imported/chamber_strings"
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		away := imgui.Vec2{X: 5, Y: 5}
		scoreOpenLibrary(0, false)
		scoreFrame(away, false)
		scoreFrame(away, false)
		scoreLibrary.importing = true
		scoreLibrary.browseDir = filepath.Dir(path)
		scoreLibChoose(path)
		scoreFrame(away, false)
		scoreFrame(away, false)
		if len(scoreLibrary.browseList) != 1 {
			t.Errorf("the browser lists %d; only the .sfz imports", len(scoreLibrary.browseList))
		}
		scoreLibrary.importing = false
		scoreOpenLibrary(0, true)
		scoreFrame(away, false)
		scoreFrame(away, false)
	})
}

// The shipped library reads: every entry loads, the categories count
// them, and an instrument with layers names them.
func TestScoreLibraryReadsTheShippedLibrary(t *testing.T) {
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	t.Cleanup(func() { fileutil.ResourcesPath = prev; tracker.ReloadSongs() })
	es := scoreLibScan()
	if len(es) < 150 {
		t.Fatalf("%d entries", len(es))
	}
	cats, counts := scoreLibCategories(es)
	if cats[0] != "" || counts[""] != len(es) {
		t.Errorf("All first: %v %d", cats[:2], counts[""])
	}
	for _, e := range es {
		if e.Err != "" {
			t.Errorf("%s: %s", e.Name, e.Err)
		}
	}
	for _, e := range es {
		if e.Name == "strings/violin_section" {
			d := scoreLibDescribe(e.Name, e.Inst)
			if !strings.HasPrefix(d.Layers, "2 (") || len(d.Articulations) < 4 {
				t.Errorf("violins: %+v", d)
			}
		}
	}
}
