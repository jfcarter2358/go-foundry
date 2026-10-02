package glext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every readback in the engine and its tests — internal/ and cmd/, test files
// included, since the GL tests are where the deadlock was found — goes through
// this package's wrappers (readback.go). A bare gl.ReadPixels is the hang
// coming back on the next driver that has it.
func TestEveryReadbackGoesThroughTheWrappers(t *testing.T) {
	bare := []string{"gl.ReadPixels(", "gl.GetTexImage(", "gl.GetBufferSubData("}
	wrapped := 0
	for _, root := range []string{filepath.Join(".."), filepath.Join("..", "..", "cmd")} {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			if filepath.Base(filepath.Dir(path)) == "glext" {
				return nil // the wrappers themselves
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, l := range strings.Split(string(src), "\n") {
				for _, b := range bare {
					if strings.Contains(l, b) {
						t.Errorf("%s:%d calls %s directly; use glext.%s", path, i+1, strings.TrimSuffix(b, "("), strings.TrimPrefix(strings.TrimSuffix(b, "("), "gl."))
					}
				}
				if strings.Contains(l, "glext.ReadPixels(") || strings.Contains(l, "glext.GetTexImage(") || strings.Contains(l, "glext.GetBufferSubData(") {
					wrapped++
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if wrapped < 10 {
		t.Fatalf("found only %d wrapped readbacks — the walk is not looking where the engine reads back", wrapped)
	}
}
