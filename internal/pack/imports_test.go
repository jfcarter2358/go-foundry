package pack

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeImports stands in for the PE reader: each file's imports by base name.
func fakeImports(t *testing.T, table map[string][]string) {
	prev := importedDLLs
	t.Cleanup(func() { importedDLLs = prev })
	importedDLLs = func(path string) ([]string, bool, error) {
		names, ok := table[filepath.Base(path)]
		return names, ok, nil
	}
}

func TestMissingDLLsFollowsShippedLibraries(t *testing.T) {
	dir := writeTree(t, map[string]string{"game.exe": "MZ", "SDL2.dll": "MZ"})
	fakeImports(t, map[string][]string{
		"game.exe": {"KERNEL32.dll", "SDL2.dll", "OPENGL32.dll", "libgcc_s_seh-1.dll"},
		"SDL2.dll": {"SETUPAPI.dll", "api-ms-win-crt-runtime-l1-1-0.dll", "libfoo.dll"},
	})
	got, err := MissingDLLs(filepath.Join(dir, "game.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"libfoo.dll", "libgcc_s_seh-1.dll"}; !reflect.DeepEqual(got, want) {
		t.Errorf("missing = %v, want %v", got, want)
	}
}

// A project made by an installed editor has no SDL2.dll of its own: the pack
// takes the editor's, and refuses outright when there is none anywhere.
func TestWindowsPackSuppliesOrRefusesDLLs(t *testing.T) {
	src := writeTree(t, map[string]string{"resources/game.yaml": testManifest})
	exe := filepath.Join(t.TempDir(), "game.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0755); err != nil {
		t.Fatal(err)
	}
	fakeImports(t, map[string][]string{
		"test-game.exe": {"KERNEL32.dll", "SDL2.dll"},
		"SDL2.dll":      {"USER32.dll"},
	})

	out := filepath.Join(t.TempDir(), "install")
	_, err := Pack(Options{Src: filepath.Join(src, "resources"), Out: out, Exe: exe, GOOS: "windows", Logf: t.Logf})
	if err == nil || !strings.Contains(err.Error(), "SDL2.dll") {
		t.Fatalf("with no SDL2.dll anywhere: %v, want a refusal naming it", err)
	}

	editorRuntimes := writeTree(t, map[string]string{"SDL2.dll": "MZ the editor's"})
	_, err = Pack(Options{Src: filepath.Join(src, "resources"), Out: out, Exe: exe, GOOS: "windows",
		LibDirs: []string{filepath.Join(src, "runtimes"), editorRuntimes}, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "SDL2.dll")); string(b) != "MZ the editor's" {
		t.Errorf("SDL2.dll = %q, want the editor's copy", b)
	}
}

func TestMingwRuntimeDLLsSayHowToLinkThemIn(t *testing.T) {
	dir := writeTree(t, map[string]string{"foundry.exe": "MZ"})
	fakeImports(t, map[string][]string{"foundry.exe": {"KERNEL32.dll", "libstdc++-6.dll"}})
	err := checkDLLs(filepath.Join(dir, "foundry.exe"), "elsewhere")
	if err == nil || !strings.Contains(err.Error(), "-static-libstdc++") {
		t.Errorf("err = %v, want the static-link hint", err)
	}
}
