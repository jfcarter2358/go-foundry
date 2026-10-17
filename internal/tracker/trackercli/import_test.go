package trackercli

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/tracker"
)

// import-instrument writes an .sfz into a project's library, and the
// library then lists and checks it.
func TestImportInstrumentVerb(t *testing.T) {
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, "resources", "sounds"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	d := make([]float32, 24000)
	for i := range d {
		d[i] = 0.5 * float32(math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	f, err := os.Create(filepath.Join(src, "a4.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.WriteWAV(f, d, 1, 48000, 16); err != nil {
		t.Fatal(err)
	}
	f.Close()
	sfzPath := filepath.Join(src, "Bell.sfz")
	if err := os.WriteFile(sfzPath, []byte("<region> sample=a4.wav pitch_keycenter=a4 lokey=c3 hikey=c6 trigger=release\n<region> sample=a4.wav pitch_keycenter=a4 lokey=c3 hikey=c6 ampeg_release=1 fil_veltrack=1200\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := fileutil.ResourcesPath
	t.Cleanup(func() { fileutil.ResourcesPath = prev; tracker.ReloadSongs() })

	var out bytes.Buffer
	if code := Main(proj, []string{"import-instrument", sfzPath}, &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	got := out.String()
	for _, want := range []string{"imported/bell", `"Bell"`, "1 zones", "C-3..C-6", "release-trigger", "fil_veltrack ×1"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if _, err := os.Stat(filepath.Join(proj, "resources", "sounds", "instruments", "imported", "bell.finst")); err != nil {
		t.Error(err)
	}
	out.Reset()
	if code := Main(proj, []string{"check"}, &out); code != 0 {
		t.Errorf("check after import:\n%s", out.String())
	}
	out.Reset()
	if code := Main(proj, []string{"import-instrument", filepath.Join(src, "a4.wav")}, &out); code != 2 {
		t.Errorf("a WAV imported (exit %d): %s", code, out.String())
	}
}
