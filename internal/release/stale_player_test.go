package release

import (
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/levelio"
)

// A player left in a project's runtimes/ by an older Foundry cannot load what
// this editor compiles; the editor's own is used instead, and with no
// editor's to fall back on the release refuses rather than ship it.
func TestAStalePlayerIsPassedOver(t *testing.T) {
	root := t.TempDir()
	proj, editor := filepath.Join(root, "proj"), filepath.Join(root, "editor")
	prev := fileutil.ExecutablePath
	defer func() { fileutil.ExecutablePath = prev }()
	fileutil.ExecutablePath = editor
	p := Platforms[0]
	name := filepath.Join(RuntimesDirName, PlayerFileName(p))

	writeFiles(t, proj, map[string]string{name: "an old player with no stamp"})
	own := filepath.Join(proj, name)
	if v, ok := PlayerLevelVersion(own); ok {
		t.Fatalf("unstamped player read as format %d", v)
	}
	if StalePlayer(own) == "" {
		t.Error("an unstamped player was not called stale")
	}
	if got := PrebuiltPlayer(proj, p); got != own {
		t.Errorf("with no editor's: %s, want the project's (for CheckPlayer to refuse)", got)
	}

	writeFiles(t, editor, map[string]string{name: "x\x00" + levelio.CompiledVersionStamp + "\x00y"})
	if v, ok := PlayerLevelVersion(filepath.Join(editor, name)); !ok || v != levelio.CompiledVersion {
		t.Errorf("stamped player = %d, %v", v, ok)
	}
	if got := PrebuiltPlayer(proj, p); got != filepath.Join(editor, name) {
		t.Errorf("with a current editor's: %s, want the editor's", got)
	}

	writeFiles(t, proj, map[string]string{name: levelio.CompiledVersionStamp})
	if got := PrebuiltPlayer(proj, p); got != own {
		t.Errorf("with a current project's: %s, want the project's", got)
	}
}
