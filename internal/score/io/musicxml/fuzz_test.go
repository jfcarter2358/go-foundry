package musicxml

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/interpret"
)

// FuzzImport: no file, however broken, panics the importer, and whatever
// it imports validates without errors in its bars, prints, and plays.
func FuzzImport(f *testing.F) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*.musicxml"))
	for _, p := range files {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := Read(b)
		if err != nil {
			return
		}
		s, _, err := Import(d, Options{Name: "fuzz"})
		if err != nil {
			return
		}
		out, err := score.Marshal(s)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		back, err := score.Parse(out)
		if err != nil {
			t.Fatalf("an import does not read back: %v\n%s", err, out)
		}
		if _, err := interpret.Interpret(back, interpret.Options{}); err != nil {
			t.Fatalf("interpret: %v", err)
		}
	})
}
