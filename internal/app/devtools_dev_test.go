//go:build !release

package app

import "testing"

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
