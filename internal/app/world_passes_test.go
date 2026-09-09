package app

// world_passes_test.go — the rule that a render pass drawing level content is
// stood up by the engine, not by the editor.
//
// This is a source-level check, which is unusual, and it is here because the
// failure it guards has no runtime symptom to assert on. GrassInit and
// SpriteInit were called from editor.EditorInit; `-tags release` compiles
// internal/editor out entirely, so a shipped game called none of them; and each
// pass early-outs on its own `initialized` flag, so nothing logged, nothing
// errored and nothing crashed. The game just quietly had no biome scatter, no
// ground patches, no sprites and no particles — a bug that
// survives every test that checks the engine starts, which is the same class of
// failure `make release-check` exists to catch, and it is caught the same way:
// by looking at what is wired to what rather than at what happens.
//
// It runs under both build tags, so the release build's own suite covers it too.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"foundry/internal/testenv"
)

// worldPassInits are the render entry points that stand up a pass drawing level
// content. Anything added beside them in render/init.go belongs in this list.
var worldPassInits = []string{"GrassInit", "SpriteInit", "SkyInit"}

func TestTheEngineStandsUpTheWorldPasses(t *testing.T) {
	src := testenv.RepoFile(t, "internal", "app", "app.go")
	if !strings.Contains(src, "render.InitWorldPasses()") {
		t.Error("internal/app/app.go does not call render.InitWorldPasses(); " +
			"a release build would draw no scatter, sprites or particles")
	}

	// And that function is the one place they are named, so adding a fourth
	// pass to it is enough to have every build stand it up.
	initSrc := testenv.RepoFile(t, "internal", "render", "init.go")
	for _, name := range worldPassInits {
		if !strings.Contains(initSrc, name+"()") {
			t.Errorf("render.InitWorldPasses does not call %s(); that pass never initialises", name)
		}
	}
}

// The other half: the editor must not be where any of them is called from, or
// the release build is back to missing whichever one moved.
func TestTheEditorDoesNotStandUpTheWorldPasses(t *testing.T) {
	editorDir := filepath.Join(testenv.RepoRoot(t), "internal", "editor")
	entries, err := os.ReadDir(editorDir)
	if err != nil {
		t.Fatalf("read internal/editor: %v", err)
	}

	// A call, not a mention: both files carry comments explaining the rule, and
	// a comment naming GrassInit must not fail this.
	calls := regexp.MustCompile(`render\.(` + strings.Join(append(
		append([]string{}, worldPassInits...), "InitWorldPasses"), "|") + `)\(`)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(editorDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			if i := strings.Index(line, "//"); i >= 0 {
				line = line[:i]
			}
			if m := calls.FindString(line); m != "" {
				t.Errorf("internal/editor/%s calls %s — a world pass must be stood up "+
					"by render.InitWorldPasses, or the release build (which has no editor) never does",
					e.Name(), strings.TrimSuffix(m, "("))
			}
		}
	}
}
