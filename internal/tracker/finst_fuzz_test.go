package tracker

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzInstrumentFile: any .finst that parses saves and reads back to the
// same file, and plays every articulation at any velocity without a panic.
func FuzzInstrumentFile(f *testing.F) {
	seeds, _ := filepath.Glob(filepath.Join("..", "..", "resources", "sounds", "instruments", "strings", "*.finst"))
	for _, p := range seeds {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		in, err := ParseInstrument(b)
		if err != nil {
			return
		}
		out, err := MarshalInstrument(in)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		back, err := ParseInstrument(out)
		if err != nil {
			t.Fatalf("a saved instrument does not read back: %v\n%s", err, out)
		}
		again, _ := MarshalInstrument(back)
		if string(again) != string(out) {
			t.Fatalf("saving is not a fixpoint:\n%s\nvs\n%s", out, again)
		}
		// Every sample slot plays a short DC sample.
		for i := range back.Samples {
			d := make([]float32, 64)
			for k := range d {
				d[k] = 0.25
			}
			back.SetLoaded(i, &Sample{Name: "s", Data: d, Channels: 1, Rate: 48000, BaseNote: NoteC4, Volume: 1})
		}
		e := NewEngine(1)
		e.SetRate(48000)
		buf := make([]float32, 256*2)
		for art := 0; art <= len(back.Articulations)+1; art++ {
			for _, v := range []float32{0, 0.3, 0.7, 1} {
				e.NoteOn(0, back, float32(NoteC4), NoteOptions{Velocity: v, Volume: 1, Articulation: art})
				e.Render(buf, 256, 48000)
			}
		}
	})
}
