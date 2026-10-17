package interpret

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/score"
)

// FuzzScoreFile: any .fscore that parses validates, performs and saves
// without a panic, and what it saves reads back to the same file.
func FuzzScoreFile(f *testing.F) {
	if b, err := os.ReadFile(filepath.Join("..", "..", "..", "resources", "sounds", "score_demo.fscore")); err == nil {
		f.Add(b)
	}
	if b, err := os.ReadFile(filepath.Join("..", "scorecli", "testdata", "expression.fscore")); err == nil {
		f.Add(b)
	}
	f.Add([]byte("bars: 2\npickup: 1/4\nrepeats: [{start: 1, end: 2}]\ntempo: [{bar: 1, bpm: 60, ramp: true}, {bar: 2, beat: \"2\", bpm: 90}]\nlines:\n- name: A\n  instrument: x\n  staves: [{clef: treble, voices: [\"| /eC4 qD4~ | 3{eD4 eE4 eF4} h.G4 |\"]}]\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		s, err := score.Parse(b)
		if err != nil {
			return
		}
		s.Validate(nil)
		if _, err := Interpret(s, Options{}); err != nil {
			return
		}
		out, err := score.Marshal(s)
		if err != nil {
			return
		}
		back, err := score.Parse(out)
		if err != nil {
			t.Fatalf("a saved score does not read back: %v\n%s", err, out)
		}
		again, err := score.Marshal(back)
		if err != nil || string(again) != string(out) {
			t.Fatalf("saving is not a fixpoint:\n%s\nvs\n%s", out, again)
		}
	})
}
