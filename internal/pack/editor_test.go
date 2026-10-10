package pack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The editor's install is the editor, its shaders and its runtimes — and no
// project, so nothing in it is anybody's work.
func TestAnEditorInstallIsTheEngineOnly(t *testing.T) {
	src := writeTree(t, map[string]string{
		"editor.exe":                     "MZ the editor",
		"SDL2.dll":                       "MZ a library",
		"shaders/fragment.glsl":          "void main() {}\n",
		"shaders/fragment.glsl.bak":      "old",
		"rt/foundry-release-linux-amd64": "\x7fELF",
	})
	out := filepath.Join(t.TempDir(), "editor-windows")
	res, err := StageEditor(EditorOptions{
		Out: out, Exe: filepath.Join(src, "editor.exe"), ExeName: "foundry.exe", GOOS: "windows", Version: "1.2.3",
		Shaders:  filepath.Join(src, "shaders"),
		Libs:     []string{filepath.Join(src, "SDL2.dll")},
		Runtimes: []string{filepath.Join(src, "rt", "foundry-release-linux-amd64")},
		Logf:     t.Logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stamp.Title != EditorTitle || res.Stamp.Kind != KindEditor || res.Stamp.Version != "1.2.3" {
		t.Errorf("stamp = %+v", res.Stamp)
	}
	stamp, _ := os.ReadFile(filepath.Join(out, StampName))
	if !strings.Contains(string(stamp), "KIND='editor'") {
		t.Errorf("stamp:\n%s", stamp)
	}
	for _, want := range []string{"foundry.exe", "SDL2.dll", "resources/shaders/fragment.glsl", "runtimes/foundry-release-linux-amd64", "licenses/Bravura-OFL.txt"} {
		if !exists(out, want) {
			t.Errorf("%s was not staged", want)
		}
	}
	if ofl, _ := os.ReadFile(filepath.Join(out, "licenses", "Bravura-OFL.txt")); !strings.Contains(string(ofl), "SIL Open Font License") {
		t.Error("the Bravura license is not the OFL")
	}
	for _, unwanted := range []string{"resources/shaders/fragment.glsl.bak", "resources/game.yaml"} {
		if exists(out, unwanted) {
			t.Errorf("%s was staged", unwanted)
		}
	}
}

// A release made by an installed editor ships that editor's shaders, which are
// the ones its runtimes were built against — not whatever the project has.
func TestAPackCanTakeItsShadersFromTheEngine(t *testing.T) {
	src := writeTree(t, map[string]string{
		"game.yaml":             testManifest,
		"shaders/fragment.glsl": "the project's stale copy",
	})
	engine := writeTree(t, map[string]string{"fragment.glsl": "the engine's"})
	exe := filepath.Join(t.TempDir(), "game")
	if err := os.WriteFile(exe, []byte("\x7fELF"), 0755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "install")
	if _, err := Pack(Options{Src: src, Out: out, Exe: exe, GOOS: "linux", Shaders: engine, Logf: t.Logf}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(out, "resources", "shaders", "fragment.glsl"))
	if string(got) != "the engine's" {
		t.Errorf("shipped shader = %q", got)
	}
}
