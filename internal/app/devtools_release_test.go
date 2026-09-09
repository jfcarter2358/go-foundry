//go:build release

package app

import (
	"path/filepath"
	"testing"

	"foundry/internal/levelio"
)

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

// The other half of the release build's guarantee, and the half that has no
// runtime symptom until it is far too late: a shipped game loads compiled
// levels and nothing else.
//
// A build that fell back to the authored format would work — it would just be
// running a level that had never been through CSG, has no visibility set and
// has no baked light, performing like it and looking subtly different, with
// nothing anywhere saying so. Failing to find a .fbsp is the loud failure, and
// this is what keeps the seam pointed that way. See levelsource_release.go.
func TestReleaseBuildLoadsOnlyCompiledLevels(t *testing.T) {
	if levelSourceExt != levelio.CompiledExt {
		t.Errorf("a release build's level source extension is %q, want %q", levelSourceExt, levelio.CompiledExt)
	}
	got := levelSourcePath("canyon")
	if filepath.Ext(got) != levelio.CompiledExt {
		t.Errorf("levelSourcePath(\"canyon\") = %q, which is not a compiled level", got)
	}
}
