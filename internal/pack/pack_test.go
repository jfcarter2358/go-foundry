package pack

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

const testManifest = "title: Test Game\nversion: 1.2.3\nstart_level: intro\n"

// exists reports whether a slash-separated path is in dir.
func exists(dir, slash string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(slash)))
	return err == nil
}

// A project directory is where the editor runs, so it holds the editor's own
// binary, window layout, log and settings beside resources/. None of them is
// the game's, and the binary above all must never ship: it is the editor. The
// same files strayed into resources/ stay out too.
func TestTheEditorsOwnFilesAreNotPacked(t *testing.T) {
	project := writeTree(t, map[string]string{
		"resources/game.yaml":             testManifest,
		"resources/audio.yaml":            "music: 0.5\n", // a shipped default mix: the game's
		"resources/LICENSE":               "mine\n",
		"resources/game.yaml.bak":         testManifest,
		"resources/imgui.ini":             "[Window]\n",
		"resources/foundry":               "\x7fELF\x02\x01\x01 a stray editor",
		"resources/levels/intro.yaml":     "chunks: []\n",
		"resources/levels/intro.yaml.bak": "chunks: []\n",
		"resources/shaders/vert.glsl":     "void main() {}\n",
		"imgui.ini":                       "[Window]\n",
		"foundry.log":                     "[foundry] hello\n",
		"release.yaml":                    "prune: true\n",
		"claude.yaml":                     "model: opus\n",
		"foundry":                         "\x7fELF\x02\x01\x01 the editor",
		"foundry.exe":                     "MZ\x90\x00 the editor, for Windows",
		"SDL2.dll":                        "MZ\x90\x00 the library the Windows editor loads",
	})
	exe := filepath.Join(t.TempDir(), "foundry-release-windows-amd64.exe")
	if err := os.WriteFile(exe, []byte("MZ game"), 0755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "install")

	res, err := Pack(Options{Src: filepath.Join(project, "resources"), Out: out, Exe: exe, GOOS: "windows", Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stamp.Exe != "test-game.exe" || res.Stamp.Version != "1.2.3" || res.Stamp.DataDir != "Test Game" {
		t.Errorf("stamp = %+v", res.Stamp)
	}
	for _, want := range []string{"test-game.exe", "SDL2.dll", StampName,
		"resources/game.yaml", "resources/audio.yaml", "resources/LICENSE",
		"resources/levels/intro.yaml", "resources/shaders/vert.glsl"} {
		if !exists(out, want) {
			t.Errorf("%s was not packed", want)
		}
	}
	for _, unwanted := range []string{"imgui.ini", "foundry.log", "release.yaml", "claude.yaml", "foundry", "foundry.exe",
		"resources/imgui.ini", "resources/foundry", "resources/game.yaml.bak", "resources/levels/intro.yaml.bak"} {
		if exists(out, unwanted) {
			t.Errorf("%s was packed — it belongs to the editor", unwanted)
		}
	}
}

// Everything the game reads goes in the tree's resources/, where the engine
// looks for it — except a runtime library, which the platform's loader only
// finds beside the executable, and only on the platform it is for.
func TestRuntimeLibrariesLandBesideTheExecutable(t *testing.T) {
	src := writeTree(t, map[string]string{
		"game.yaml":         testManifest,
		"SDL2.dll":          "MZ\x90\x00 a library",
		"levels/intro.yaml": "chunks: []\n",
	})
	exe := filepath.Join(t.TempDir(), "game.exe")
	if err := os.WriteFile(exe, []byte("MZ game"), 0755); err != nil {
		t.Fatal(err)
	}

	windows := filepath.Join(t.TempDir(), "windows")
	if _, err := Pack(Options{Src: src, Out: windows, Exe: exe, GOOS: "windows", Logf: t.Logf}); err != nil {
		t.Fatal(err)
	}
	if !exists(windows, "SDL2.dll") {
		t.Error("SDL2.dll is not beside the executable, where Windows loads it from")
	}
	if exists(windows, "resources/SDL2.dll") {
		t.Error("SDL2.dll was packed into resources/ as well")
	}
	if !exists(windows, "resources/levels/intro.yaml") || exists(windows, "levels/intro.yaml") {
		t.Error("the content is not under the tree's resources/")
	}

	linux := filepath.Join(t.TempDir(), "linux")
	if _, err := Pack(Options{Src: src, Out: linux, Exe: exe, GOOS: "linux", Logf: t.Logf}); err != nil {
		t.Fatal(err)
	}
	if exists(linux, "SDL2.dll") || exists(linux, "resources/SDL2.dll") {
		t.Error("a Windows library was packed for Linux")
	}
}

// Packing from a project directory, the project root has a game.yaml in it,
// which is what refuseDangerousOutput takes as proof of an old install tree.
// Pointed at the project, a pack would have emptied it.
func TestOutputOverTheContentIsRefused(t *testing.T) {
	root := writeTree(t, map[string]string{
		"game.yaml":         testManifest,
		"levels/intro.yaml": "chunks: []\n",
	})
	for _, out := range []string{
		root,
		filepath.Dir(root),
		filepath.Join(root, "levels", "build"),
		filepath.Join(root, "shaders"),
	} {
		if err := CheckOutput(root, out); err == nil {
			t.Errorf("CheckOutput(%q) accepted an output that would take the content with it", out)
		}
	}
	if err := CheckOutput(root, filepath.Join(root, "build", "install-linux")); err != nil {
		t.Errorf("the default output was refused: %v", err)
	}

	exe := filepath.Join(t.TempDir(), "game")
	if err := os.WriteFile(exe, []byte("\x7fELF"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Pack(Options{Src: root, Out: root, Exe: exe, Logf: t.Logf}); err == nil {
		t.Error("a pack into its own content directory was allowed")
	}
	if !exists(root, "levels/intro.yaml") {
		t.Fatal("a refused pack still emptied the project")
	}
}

func TestARepositoryIsNeverEmptied(t *testing.T) {
	repo := writeTree(t, map[string]string{
		"game.yaml":     testManifest,
		".git/HEAD":     "ref: refs/heads/main\n",
		"levels/a.yaml": "chunks: []\n",
	})
	if err := refuseDangerousOutput(repo); err == nil {
		t.Error("a repository with a game.yaml in it was accepted as an install tree to empty")
	}
}

// stageTree is an install tree as a pack leaves it, every file 0644 — as it is
// when staged on a filesystem with no executable bit to set.
func stageTree(t *testing.T) string {
	t.Helper()
	dir := writeTree(t, map[string]string{
		"test-game":         "\x7fELF",
		"test-game.exe":     "MZ",
		"install.sh":        "#!/bin/sh\n",
		StampName:           "TITLE='Test Game'\n",
		"levels/intro.fbsp": "compiled",
	})
	return dir
}

func TestTarballHasOneFolderAndRunnableFiles(t *testing.T) {
	stage := stageTree(t)
	out := filepath.Join(t.TempDir(), "dist", "test-game-1.2.3-linux-amd64.tar.gz")
	if err := TarGz(stage, out, "test-game-1.2.3", "test-game"); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]int64{}
	bodies := map[string]string{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		modes[hdr.Name] = hdr.Mode
		body, _ := io.ReadAll(tr)
		bodies[hdr.Name] = string(body)
	}

	for name, want := range map[string]int64{
		"test-game-1.2.3/":                  0755,
		"test-game-1.2.3/test-game":         0755,
		"test-game-1.2.3/install.sh":        0755,
		"test-game-1.2.3/" + StampName:      0644,
		"test-game-1.2.3/levels/":           0755,
		"test-game-1.2.3/levels/intro.fbsp": 0644,
		"test-game-1.2.3/test-game.exe":     0644,
	} {
		got, ok := modes[name]
		if !ok {
			t.Errorf("%s is not in the tarball (have %v)", name, modes)
			continue
		}
		if got != want {
			t.Errorf("%s mode = %o, want %o", name, got, want)
		}
	}
	if bodies["test-game-1.2.3/levels/intro.fbsp"] != "compiled" {
		t.Error("a file's contents did not survive the tarball")
	}
}

func TestZipHasOneFolder(t *testing.T) {
	stage := stageTree(t)
	out := filepath.Join(t.TempDir(), "test-game-1.2.3-windows-amd64.zip")
	if err := Zip(stage, out, "test-game-1.2.3", "test-game.exe"); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	names := map[string]fs.FileMode{}
	for _, f := range zr.File {
		names[f.Name] = f.Mode()
	}
	for _, want := range []string{"test-game-1.2.3/", "test-game-1.2.3/test-game.exe", "test-game-1.2.3/levels/intro.fbsp", "test-game-1.2.3/" + StampName} {
		if _, ok := names[want]; !ok {
			t.Errorf("%s is not in the zip (have %v)", want, names)
		}
	}
	if m := names["test-game-1.2.3/test-game.exe"]; m.Perm() != 0755 {
		t.Errorf("the executable's mode = %v, want 0755", m)
	}
}
