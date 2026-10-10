//go:build !release

package app

import (
	"testing"

	"foundry/internal/editor"
	"foundry/internal/release"
)

// The development build's counterpart to devtools_release_test.go: the same
// two predicates, asserted the other way round, so that a mistake that made
// both files agree — a devAvailable left false, a devActive stubbed out in the
// wrong file — fails on one side or the other rather than on neither.

func TestDevelopmentBuildHasTheEditor(t *testing.T) {
	if !devAvailable {
		t.Error("devAvailable is false in a development build")
	}
	// devActive is state, not a constant, and the editor has not been opened in
	// a unit test — so the check is that it is a real predicate rather than the
	// release build's constant. That it is false here is EditorInit's default.
	if devActive() {
		t.Error("the editor reports active with no EditorInit having run")
	}
}

// A development build never lets quit_game end the process. The play-session
// half toggles the editor, which needs a GL context; the half checkable here is
// the one with no session to end — the editor is already open, the request is
// taken, and nothing about the editor changes.
func TestDevelopmentBuildQuitKeepsTheEditor(t *testing.T) {
	was := editor.Editor.Active
	defer func() { editor.Editor.Active = was }()

	editor.Editor.Active = true
	if !devInterceptQuit() {
		t.Error("devInterceptQuit let quit_game through in a development build — it would close the editor")
	}
	if !editor.Editor.Active {
		t.Error("quit_game with the editor already open left the editor")
	}
}

// A development binary is the release builder when it is asked to be, and only
// then: anything else on its command line has to fall through to the editor.
func TestDevelopmentBuildRunsOnlyItsOwnCommands(t *testing.T) {
	for _, args := range [][]string{nil, {}, {"-v"}, {"levels/intro.yaml"}} {
		if _, ran := devRunCommand(args); ran {
			t.Errorf("devRunCommand(%q) ran a tool instead of the editor", args)
		}
	}
	if code, ran := devRunCommand([]string{release.Command, "-h"}); !ran || code != 0 {
		t.Errorf("devRunCommand(%s -h) = %d, %v; want the release builder's usage", release.Command, code, ran)
	}
}
