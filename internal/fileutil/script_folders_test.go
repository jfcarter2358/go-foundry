package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

// The script folder helpers the editor's tree is built on. All three touch the
// disk, so they are exercised against a scratch resources tree rather than the
// real one.

// withTempScripts points ExecutablePath at a scratch directory with an empty
// scripts folder in it, and restores it afterwards.
func withTempScripts(t *testing.T) string {
	t.Helper()
	prev := ExecutablePath
	t.Cleanup(func() { ExecutablePath = prev })
	ExecutablePath = t.TempDir()
	root := ScriptsDir()
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatalf("make scripts dir: %v", err)
	}
	return root
}

// writeScriptFile creates a script at a slash-separated path under the scripts
// folder.
func writeScriptFile(t *testing.T, rel, body string) {
	t.Helper()
	full := ScriptPath(rel)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(body), 0644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestScanScriptDirsFindsEveryFolder(t *testing.T) {
	withTempScripts(t)
	writeScriptFile(t, "doors/inner/latch.flang", "-- latch\n")
	if err := MakeScriptDir("empty"); err != nil {
		t.Fatalf("MakeScriptDir: %v", err)
	}

	got := ScanScriptDirs()
	want := map[string]bool{"doors": true, "doors/inner": true, "empty": true}
	if len(got) != len(want) {
		t.Fatalf("ScanScriptDirs = %v, want the three folders", got)
	}
	for _, dir := range got {
		if !want[dir] {
			t.Errorf("unexpected folder %q", dir)
		}
	}
}

// The scripts folder itself is not one of its own sub-folders, or the tree
// would have a root inside its root.
func TestScanScriptDirsExcludesTheRoot(t *testing.T) {
	withTempScripts(t)
	for _, dir := range ScanScriptDirs() {
		if dir == "." || dir == "" {
			t.Errorf("ScanScriptDirs returned the root itself as %q", dir)
		}
	}
}

func TestMakeScriptDirNests(t *testing.T) {
	root := withTempScripts(t)
	if err := MakeScriptDir("a/b/c"); err != nil {
		t.Fatalf("MakeScriptDir: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, "a", "b", "c")); err != nil || !info.IsDir() {
		t.Fatalf("a/b/c was not created: %v", err)
	}
}

// A folder name that climbs out of the scripts folder is a typo rather than an
// attack, and a typo that creates a directory beside resources/ is still worth
// not doing.
func TestMakeScriptDirRefusesToEscape(t *testing.T) {
	withTempScripts(t)
	for _, name := range []string{"../outside", "", "/"} {
		if err := MakeScriptDir(name); err == nil {
			t.Errorf("MakeScriptDir(%q) was allowed", name)
		}
	}
	if _, err := os.Stat(filepath.Join(ExecutablePath, "outside")); err == nil {
		t.Error("a folder was created outside the scripts folder")
	}
}

func TestMoveScriptFileIntoAFolder(t *testing.T) {
	withTempScripts(t)
	writeScriptFile(t, "loose.flang", "-- body\n")

	if err := MoveScriptFile("loose.flang", "doors/loose.flang"); err != nil {
		t.Fatalf("MoveScriptFile: %v", err)
	}
	if _, err := os.Stat(ScriptPath("loose.flang")); err == nil {
		t.Error("the original file is still there")
	}
	body, err := os.ReadFile(ScriptPath("doors/loose.flang"))
	if err != nil {
		t.Fatalf("read the moved file: %v", err)
	}
	if string(body) != "-- body\n" {
		t.Errorf("the moved file holds %q", body)
	}
}

// Organising scripts must never be able to lose one, so a move onto an existing
// file is refused rather than resolved.
func TestMoveScriptFileWillNotOverwrite(t *testing.T) {
	withTempScripts(t)
	writeScriptFile(t, "a.flang", "-- a\n")
	writeScriptFile(t, "b.flang", "-- b\n")

	if err := MoveScriptFile("a.flang", "b.flang"); err == nil {
		t.Fatal("a move over an existing script was allowed")
	}
	for name, want := range map[string]string{"a.flang": "-- a\n", "b.flang": "-- b\n"} {
		body, err := os.ReadFile(ScriptPath(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(body) != want {
			t.Errorf("%s holds %q, want %q", name, body, want)
		}
	}
}

// The editor writes paths with and without the scripts/ prefix and the
// extension, since that is how levels write them; a move has to accept both.
func TestMoveScriptFileAcceptsPrefixedNames(t *testing.T) {
	withTempScripts(t)
	writeScriptFile(t, "a.flang", "-- a\n")

	if err := MoveScriptFile("scripts/a", "shared/a"); err != nil {
		t.Fatalf("MoveScriptFile: %v", err)
	}
	if _, err := os.Stat(ScriptPath("shared/a.flang")); err != nil {
		t.Fatalf("shared/a.flang is not there: %v", err)
	}
}

func TestMoveScriptFileOntoItselfIsANoop(t *testing.T) {
	withTempScripts(t)
	writeScriptFile(t, "a.flang", "-- a\n")
	if err := MoveScriptFile("a.flang", "a.flang"); err != nil {
		t.Fatalf("moving a file onto itself reported %v", err)
	}
	if _, err := os.Stat(ScriptPath("a.flang")); err != nil {
		t.Fatalf("the file went missing: %v", err)
	}
}

func TestDeleteScriptFile(t *testing.T) {
	withTempScripts(t)
	writeScriptFile(t, "doors/lift.flang", "-- lift\n")
	writeScriptFile(t, "doors/latch.flang", "-- latch\n")

	if err := DeleteScriptFile("doors/lift.flang"); err != nil {
		t.Fatalf("DeleteScriptFile: %v", err)
	}
	if _, err := os.Stat(ScriptPath("doors/lift.flang")); err == nil {
		t.Error("the file is still there")
	}
	// The folder and everything else in it survive: a delete is one file.
	if _, err := os.Stat(ScriptPath("doors/latch.flang")); err != nil {
		t.Errorf("the file beside it went too: %v", err)
	}
}

// Same names the rest of the editor passes around — with and without the
// prefix and the extension, since that is how levels write them.
func TestDeleteScriptFileAcceptsPrefixedNames(t *testing.T) {
	withTempScripts(t)
	writeScriptFile(t, "a.flang", "-- a\n")

	if err := DeleteScriptFile("scripts/a"); err != nil {
		t.Fatalf("DeleteScriptFile: %v", err)
	}
	if _, err := os.Stat(ScriptPath("a.flang")); err == nil {
		t.Error("the file is still there")
	}
}

// The one operation that cannot be undone is the one that must not be talked
// into touching anything outside the scripts folder — or into taking a whole
// folder with it.
func TestDeleteScriptFileRefusesWhatIsNotAScript(t *testing.T) {
	root := withTempScripts(t)
	outside := filepath.Join(ExecutablePath, "keep.flang")
	if err := os.WriteFile(outside, []byte("-- keep\n"), 0644); err != nil {
		t.Fatalf("write the file outside: %v", err)
	}
	if err := MakeScriptDir("doors"); err != nil {
		t.Fatalf("MakeScriptDir: %v", err)
	}

	for _, name := range []string{"../keep.flang", "", "doors", "missing.flang"} {
		if err := DeleteScriptFile(name); err == nil {
			t.Errorf("DeleteScriptFile(%q) was allowed", name)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("a file outside the scripts folder was deleted: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, "doors")); err != nil || !info.IsDir() {
		t.Errorf("the folder was deleted: %v", err)
	}
}
