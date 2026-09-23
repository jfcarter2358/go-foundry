package claude

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want [3]int
		ok   bool
	}{
		{"2.1.278 (Claude Code)", [3]int{2, 1, 278}, true},
		{"1.0.0", [3]int{1, 0, 0}, true},
		{"10.20.30 (Claude Code)", [3]int{10, 20, 30}, true},
		{"unreleased build", [3]int{}, false},
	}
	for _, c := range cases {
		got, ok := ParseVersion(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseVersion(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestVersionComparison(t *testing.T) {
	if less([3]int{2, 1, 278}, MinVersion) {
		t.Error("the version this was developed against is reported as too old")
	}
	if !less([3]int{1, 9, 9}, [3]int{2, 0, 0}) {
		t.Error("1.9.9 should be older than 2.0.0")
	}
	if less([3]int{2, 0, 0}, [3]int{2, 0, 0}) {
		t.Error("a version is not older than itself")
	}
}

func TestConfiguredPathIsTriedFirst(t *testing.T) {
	got := Candidates("/my/claude")
	if len(got) == 0 || got[0] != "/my/claude" {
		t.Errorf("candidates begin with %v, want the configured path first", got)
	}
}

func TestVSCodeBundledCopyIsFound(t *testing.T) {
	// The machine this was written on has the CLI only inside the VS Code
	// extension, which is exactly the case worth covering.
	if runtime.GOOS == "windows" {
		t.Skip("path layout differs on Windows")
	}
	home := t.TempDir()
	ext := filepath.Join(home, ".vscode-server", "extensions", "anthropic.claude-code-2.1.278-linux-x64", "resources", "native-binary")
	if err := os.MkdirAll(ext, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(ext, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	found := vscodeBundled(home)
	if len(found) == 0 || found[0] != bin {
		t.Fatalf("vscodeBundled = %v, want %s", found, bin)
	}
	if !isExecutableFile(bin) {
		t.Error("the bundled copy was not recognised as runnable")
	}
}

func TestNewerExtensionVersionWins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("path layout differs on Windows")
	}
	home := t.TempDir()
	for _, v := range []string{"2.1.100", "2.1.278"} {
		if err := os.MkdirAll(filepath.Join(home, ".vscode-server", "extensions", "anthropic.claude-code-"+v, "resources", "native-binary"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	found := vscodeBundled(home)
	if len(found) == 0 || !strings.Contains(found[0], "2.1.278") {
		t.Errorf("first candidate = %v, want the newest extension", found)
	}
}

func TestNotFoundSaysWhereItLooked(t *testing.T) {
	err := &ErrNotFound{Tried: []string{"/a", "/b"}}
	if !strings.Contains(err.Error(), "/a") || !strings.Contains(err.Error(), "claude.com") {
		t.Errorf("message = %q, want the paths tried and where to get it", err.Error())
	}
}
