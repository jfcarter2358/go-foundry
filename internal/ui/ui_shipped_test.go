package ui

// ui_shipped_test.go — the documents that ship with the engine must load.
//
// A .fui is validated on read, so a typo in one of them is a screen that simply
// does not come up at runtime, with the reason in a log line nobody is reading.
// This turns that into a build failure.
//
// repoRoot is walked for locally rather than taken from internal/testenv,
// because that package imports internal/gfx and this one must not — not even in
// a test, where the layering check cannot see it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// withShippedAssets points the asset paths at resources/, which is what gets
// copied next to the executable at build time.
func withShippedAssets(t *testing.T) {
	t.Helper()
	prev := fileutil.ExecutablePath
	fileutil.ExecutablePath = filepath.Join(repoRoot(t), "resources")
	t.Cleanup(func() { fileutil.ExecutablePath = prev })
}

func TestShippedScreensLoad(t *testing.T) {
	withShippedAssets(t)

	var logs []string
	prevLog := Logf
	Logf = func(format string, args ...any) { logs = append(logs, format) }
	t.Cleanup(func() { Logf = prevLog })

	files := ScanScreenFiles()
	if len(files) == 0 {
		t.Skip("no screens in resources/ui yet")
	}

	for _, f := range files {
		name := strings.TrimSuffix(f, UIExt)
		t.Run(name, func(t *testing.T) {
			s, err := LoadScreen(UIPath(name))
			if err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			if s.ID == "" {
				t.Error("no id")
			}
			// It must also survive being written back, which is the assertion
			// that catches a field the loader accepts and the writer refuses.
			if err := SaveScreen(s, filepath.Join(t.TempDir(), f)); err != nil {
				t.Errorf("re-saving %s: %v", f, err)
			}
		})
	}

	if len(logs) != 0 {
		t.Errorf("loading the shipped screens logged %d complaints: %v", len(logs), logs)
	}
}

func TestShippedThemeLoads(t *testing.T) {
	withShippedAssets(t)
	if _, err := os.Stat(ThemePath()); os.IsNotExist(err) {
		t.Skip("no theme file yet")
	}
	if _, err := LoadTheme(ThemePath()); err != nil {
		t.Fatalf("%s: %v", ThemePath(), err)
	}
}

// TestShippedHUDTakesNoInput is the property that makes a HUD a HUD. A screen
// that pauses or captures input by accident is a game that has stopped.
func TestShippedHUDTakesNoInput(t *testing.T) {
	withShippedAssets(t)
	if _, err := os.Stat(UIPath("hud")); os.IsNotExist(err) {
		t.Skip("no hud.fui yet")
	}
	s, err := LoadScreen(UIPath("hud"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.Modal || s.Pauses || s.ShowsCursor {
		t.Errorf("hud.fui is modal=%v pauses=%v cursor=%v; a HUD must be none of those",
			s.Modal, s.Pauses, s.ShowsCursor)
	}
}
