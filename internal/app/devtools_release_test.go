//go:build release

package app

import "testing"

// The release build's whole guarantee is that the editor is not in the binary,
// and the thing that would quietly undo it is someone answering devActive with
// something other than a constant false — at which point a stub would start
// being called rather than compiled away, and the next person to add a call
// site would reach for the real editor to make it work.
//
// There is no assertion available for "package X is not linked": `go list
// -deps -tags release ./cmd/foundry` is that check, and `make release-check`
// runs it. What is checkable here is the contract the stubs promise.

func TestReleaseBuildHasNoEditor(t *testing.T) {
	if devAvailable {
		t.Error("devAvailable is true in a release build")
	}
	if devActive() {
		t.Error("devActive() is true in a release build")
	}
	if devWireframe() {
		t.Error("devWireframe() is true in a release build")
	}
}

// Nothing routes to an editor window, so every event has to reach the game.
func TestReleaseBuildConsumesNoEvents(t *testing.T) {
	if devRouteEvent(nil) {
		t.Error("devRouteEvent consumed an event in a release build")
	}
}
