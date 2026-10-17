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

// An install with -codecs carries the encoder libraries for its platform
// and their licences in codecs/, with the README naming their source; a
// folder with no library for the platform is refused.
func TestAnEditorInstallCarriesItsCodecs(t *testing.T) {
	src := writeTree(t, map[string]string{
		"editor":                       "\x7fELF the editor",
		"shaders/fragment.glsl":        "void main() {}\n",
		"codecs/libmp3lame.so.0":       "\x7fELF lame",
		"codecs/libvorbisenc.so.2":     "\x7fELF vorbisenc",
		"codecs/libvorbisenc-2.dll":    "MZ not for linux",
		"codecs/LAME-COPYING.txt":      "GNU LIBRARY GENERAL PUBLIC LICENSE",
		"codecs/libvorbis-COPYING.txt": "Copyright (c) 2002-2020 Xiph.org Foundation",
		"nocodecs/README-only.txt":     "nothing here",
	})
	out := filepath.Join(t.TempDir(), "editor-linux")
	if _, err := StageEditor(EditorOptions{Out: out, Exe: filepath.Join(src, "editor"), ExeName: "foundry", GOOS: "linux",
		Shaders: filepath.Join(src, "shaders"), Codecs: filepath.Join(src, "codecs"), Logf: t.Logf}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"codecs/libmp3lame.so.0", "codecs/libvorbisenc.so.2", "codecs/LAME-COPYING.txt", "codecs/libvorbis-COPYING.txt", "codecs/README.txt"} {
		if !exists(out, want) {
			t.Errorf("%s was not staged", want)
		}
	}
	if exists(out, "codecs/libvorbisenc-2.dll") {
		t.Error("a Windows codec went into the Linux install")
	}
	if readme, _ := os.ReadFile(filepath.Join(out, "codecs", "README.txt")); !strings.Contains(string(readme), "lame.sourceforge.io") || !strings.Contains(string(readme), "replaced") && !strings.Contains(string(readme), "instead") {
		t.Errorf("README:\n%s", readme)
	}
	if _, err := StageEditor(EditorOptions{Out: filepath.Join(t.TempDir(), "x"), Exe: filepath.Join(src, "editor"), ExeName: "foundry", GOOS: "linux",
		Shaders: filepath.Join(src, "shaders"), Codecs: filepath.Join(src, "nocodecs"), Logf: t.Logf}); err == nil {
		t.Error("a codecs folder with no library was accepted")
	}
}

// FOUNDRY_CODECS_WINDOWS=build/codecs/windows-amd64 (what `task
// codecs-windows` fetches) stages the real DLLs into a Windows install: each
// must find what it imports in codecs/ or Windows.
func TestTheFetchedWindowsCodecsStage(t *testing.T) {
	dir := os.Getenv("FOUNDRY_CODECS_WINDOWS")
	if dir == "" {
		t.Skip("set FOUNDRY_CODECS_WINDOWS to the folder packaging/codecs/fetch-windows.sh fills")
	}
	src := writeTree(t, map[string]string{"editor.exe": "MZ the editor", "shaders/fragment.glsl": "void main() {}\n"})
	out := filepath.Join(t.TempDir(), "editor-windows")
	if _, err := StageEditor(EditorOptions{Out: out, Exe: filepath.Join(src, "editor.exe"), ExeName: "foundry.exe", GOOS: "windows",
		Shaders: filepath.Join(src, "shaders"), Codecs: dir, Logf: t.Logf}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"libogg-0.dll", "libvorbis-0.dll", "libvorbisenc-2.dll", "libmp3lame-0.dll", "LAME-COPYING.txt", "README.txt"} {
		if !exists(out, filepath.Join("codecs", want)) {
			t.Errorf("codecs/%s was not staged", want)
		}
	}
}
