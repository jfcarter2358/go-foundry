package project

import (
	"os"
	"path/filepath"
	"testing"
)

func makeProject(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "resources"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resources", "game.yaml"), []byte("title: T\nstart_level: a\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveOrder(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "install")
	data := filepath.Join(root, "data")
	named := filepath.Join(root, "named")
	recent := filepath.Join(root, "recent")
	makeProject(t, named)
	makeProject(t, recent)
	if err := os.MkdirAll(filepath.Join(exe, "resources", "shaders"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Remember(data, recent); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"-project", named}, {"--project=" + named}, {named}} {
		if c, err := Resolve(exe, data, args); err != nil || c.Dir != named {
			t.Errorf("Resolve(%q) = %v, %v; want %s", args, c, err, named)
		}
	}
	// An install's own resources/shaders is not a project.
	if c, _ := Resolve(exe, data, nil); c.Dir != recent {
		t.Errorf("with a recent project: %s, want %s", c.Dir, recent)
	}
	makeProject(t, exe)
	if c, _ := Resolve(exe, data, nil); c.Dir != exe {
		t.Errorf("with a project beside the executable: %s, want %s", c.Dir, exe)
	}
	if c, _ := Resolve(filepath.Join(root, "bare"), filepath.Join(root, "nodata"), nil); c.Dir != DefaultNewDir() {
		t.Errorf("with nothing: %s, want the default new project", c.Dir)
	}
}

func TestRememberKeepsTheListShortAndUnique(t *testing.T) {
	data := t.TempDir()
	for i := 0; i < maxRecent+3; i++ {
		if err := Remember(data, filepath.Join("/p", string(rune('a'+i)))); err != nil {
			t.Fatal(err)
		}
	}
	if err := Remember(data, "/p/c"); err != nil {
		t.Fatal(err)
	}
	got := LoadRecent(data)
	if len(got) != maxRecent || got[0] != "/p/c" {
		t.Fatalf("recent = %v", got)
	}
	seen := map[string]bool{}
	for _, d := range got {
		if seen[d] {
			t.Errorf("%s listed twice", d)
		}
		seen[d] = true
	}
}

func TestNewAndOpenChecks(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	makeProject(t, proj)
	if err := CheckOpen(proj); err != nil {
		t.Errorf("open a project: %v", err)
	}
	if err := CheckOpen(root); err == nil {
		t.Error("opened a folder that is not a project")
	}
	if err := CheckNew(proj); err == nil {
		t.Error("made a new project over an existing one")
	}
	clone := filepath.Join(root, "clone")
	if err := os.MkdirAll(filepath.Join(clone, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Create(clone); err != nil {
		t.Errorf("new project in an empty clone: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := CheckNew(root); err == nil {
		t.Error("made a new project in a folder with files in it")
	}
}
