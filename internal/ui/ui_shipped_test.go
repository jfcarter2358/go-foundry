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
	"regexp"
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

// srcIDLiteral finds the widget ids a screen's script dispatches on.
//
// A regex over the source rather than a parse of it: internal/ui sits below
// internal/flang and must not import it, not even in a test. The cost is that
// an id written some other way is simply not checked, which makes this a net
// that catches the common case and never reports a failure that is not real.
var srcIDLiteral = regexp.MustCompile(`src\.id\s*==\s*"([^"]+)"`)

// A screen and the script that answers its clicks are two files, and nothing
// ties them together: renaming a button in the .fui leaves a handler matching
// an id that no longer exists, and the menu quietly stops working with no error
// anywhere. This is that tie.
func TestShippedScreenScriptsNameWidgetsThatExist(t *testing.T) {
	withShippedAssets(t)
	root := repoRoot(t)

	checked := 0
	for _, f := range ScanScreenFiles() {
		name := strings.TrimSuffix(f, UIExt)
		s, err := LoadScreen(UIPath(name))
		if err != nil || s.Script == "" {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, "resources", "scripts",
			strings.TrimPrefix(s.Script, "scripts/")))
		if err != nil {
			t.Errorf("%s names script %q: %v", f, s.Script, err)
			continue
		}
		for _, m := range srcIDLiteral.FindAllStringSubmatch(string(src), -1) {
			checked++
			if s.Find(m[1]) == nil {
				t.Errorf("%s handles a click on %q, but %s has no such widget",
					s.Script, m[1], f)
			}
		}
	}
	if checked == 0 {
		t.Skip("no shipped screen dispatches on a widget id yet")
	}
}

// relLuminance is the standard sRGB luminance weighting. Approximate — it skips
// the gamma decode — which is all this needs: the question is "can these two be
// told apart", not "what is the exact contrast ratio".
func relLuminance(c [4]float32) float32 {
	return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
}

// backdropFor returns the colour a node's text is drawn over: its nearest
// ancestor with a background solid enough to hide what is behind it, itself
// included. Reports false when there is none — a HUD label sits over the world,
// and nothing here knows what colour that is.
func backdropFor(chain []*Node) ([4]float32, bool) {
	for i := len(chain) - 1; i >= 0; i-- {
		if c := chain[i].Style.Color; c[3] >= 0.5 {
			return c, true
		}
	}
	return [4]float32{}, false
}

// A widget's colour is a plain value on the node, not a fallback: a .fui that
// does not write `color:` gets DefaultBackground, which is opaque WHITE for
// everything except a label — and DefaultTextSpec is opaque white too. So a
// hand-written button with no colour renders as a white box with a white label
// on it, which is invisible rather than merely ugly, and nothing else notices:
// the screen loads, the script runs, the click is delivered to a button the
// player cannot see.
//
// The engine's own theme has legible button colours, but it is the palette the
// UI EDITOR stamps into a new widget rather than a draw-time fallback (see
// ui_theme.go's header). Hand-authoring a screen skips that step, so this is
// the check that it was not skipped.
func TestShippedScreenTextIsLegibleAgainstItsBackground(t *testing.T) {
	withShippedAssets(t)

	const minDiff = 0.25
	for _, f := range ScanScreenFiles() {
		name := strings.TrimSuffix(f, UIExt)
		s, err := LoadScreen(UIPath(name))
		if err != nil {
			continue // TestShippedScreensLoad is where that is a failure
		}
		var walk func(chain []*Node, n *Node)
		walk = func(chain []*Node, n *Node) {
			chain = append(chain, n)
			if n.Text.Value != "" && n.Style.Texture == "" {
				if bg, ok := backdropFor(chain); ok {
					d := relLuminance(bg) - relLuminance(n.Text.Color)
					if d < 0 {
						d = -d
					}
					if d < minDiff {
						t.Errorf("%s: %q on %q is invisible — background luminance %.2f, "+
							"text luminance %.2f. A button with no `color:` is opaque white.",
							f, n.Text.Value, n.ID, relLuminance(bg), relLuminance(n.Text.Color))
					}
				}
			}
			for i := range n.Children {
				walk(chain, &n.Children[i])
			}
		}
		walk(nil, &s.Root)
	}
}
