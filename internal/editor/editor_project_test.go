package editor

import (
	"os"
	"path/filepath"
	"testing"
)

// A path pasted out of Windows Explorer's "Copy as path" arrives wrapped in
// quotes. A shell would have eaten them; a text field in a dialog does not, and
// they used to become part of the folder name and fail the existence check with
// a message that looked like the folder was missing.
func TestProjectPathFromInputStripsQuotes(t *testing.T) {
	want := filepath.Clean(mustAbs(t, filepath.Join("some", "project")))
	for _, in := range []string{
		filepath.Join("some", "project"),
		"  " + filepath.Join("some", "project") + "  ",
		`"` + filepath.Join("some", "project") + `"`,
		` " ` + filepath.Join("some", "project") + ` " `,
	} {
		if got := projectPathFromInput(in); got != want {
			t.Errorf("projectPathFromInput(%q) = %q, want %q", in, got, want)
		}
	}
	if got := projectPathFromInput("   "); got != "" {
		t.Errorf("an empty field is an empty path, got %q", got)
	}
}

// Browse... opens on whatever the field holds. Half a path — typed, or left
// over from a folder that has since been moved — still has to land somewhere
// that can be listed, or the browser opens on an error instead of a listing.
func TestNearestExistingDirWalksUp(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "projects")
	if err := os.MkdirAll(real, 0755); err != nil {
		t.Fatal(err)
	}
	if got := nearestExistingDir(real); got != real {
		t.Errorf("an existing folder is itself: got %q, want %q", got, real)
	}
	deep := filepath.Join(real, "gone", "further")
	if got := nearestExistingDir(deep); got != real {
		t.Errorf("nearestExistingDir(%q) = %q, want the nearest that exists %q", deep, got, real)
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if got := nearestExistingDir(""); got != home {
			t.Errorf("an empty path falls back home: got %q, want %q", got, home)
		}
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
