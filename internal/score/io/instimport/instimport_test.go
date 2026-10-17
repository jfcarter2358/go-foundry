package instimport

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/tracker"
)

// Import picks the reader by extension and writes into the folder asked.
func TestImportByExtension(t *testing.T) {
	src := t.TempDir()
	d := make([]float32, 4800)
	for i := range d {
		d[i] = 0.5 * float32(math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	f, err := os.Create(filepath.Join(src, "a.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.WriteWAV(f, d, 1, 48000, 16); err != nil {
		t.Fatal(err)
	}
	f.Close()
	path := filepath.Join(src, "Bell.SFZ")
	if err := os.WriteFile(path, []byte("<region> sample=a.wav pitch_keycenter=69"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !Supported(path) || Supported(filepath.Join(src, "a.wav")) {
		t.Error("Supported")
	}
	sounds := t.TempDir()
	built, err := Import(path, Options{SoundsDir: sounds, Name: "imported/bells/one"})
	t.Cleanup(func() { tracker.ReloadInstruments(); tracker.ReloadSamples() })
	if err != nil || len(built) != 1 || built[0].Name != "imported/bells/one" {
		t.Fatalf("%v %+v", err, built)
	}
	for _, p := range []string{"instruments/imported/bells/one.finst", "samples/imported/bells/one/a.wav", "samples/imported/bells/one/a.fsmp"} {
		if _, err := os.Stat(filepath.Join(sounds, filepath.FromSlash(p))); err != nil {
			t.Error(err)
		}
	}
	if _, err := Import(filepath.Join(src, "a.wav"), Options{SoundsDir: sounds}); err == nil {
		t.Error("a WAV imported")
	}
	// A file that is not a SoundFont says so.
	bad := filepath.Join(src, "x.sf2")
	os.WriteFile(bad, []byte("RIFF\x04\x00\x00\x00junk"), 0o644)
	if _, err := Import(bad, Options{SoundsDir: sounds}); err == nil {
		t.Error("junk imported as a SoundFont")
	}
}
