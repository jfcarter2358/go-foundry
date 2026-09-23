package sim

// tween_step_test.go — one tick eases a channel once.
//
// This has a specific history. The frame steps an entity's tweens inside
// collision.EntityUpdate; cmd/levelsim, which mirrored the frame by hand, also
// stepped them itself. With its -play flag on, both ran, so every `move_to` in a
// headless run went at twice its authored rate — and since that harness was what
// timings were measured with, the numbers it produced were wrong in a way
// nothing about them looked wrong.
//
// The mirror is gone (there is one frame now, and it is Step), but the shape of
// the bug is not specific to a harness: anything that adds a second call to
// AdvanceTweens, anywhere in the frame, brings it straight back. Half a second
// into a one-second ease, a channel is halfway. That is the whole assertion.

import (
	"strings"
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func easingLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := &world.Level{
		Entities: []world.Entity{{
			ID: "crate", Position: mgl32.Vec3{}, Scale: mgl32.Vec3{1, 1, 1}, Visible: true,
		}},
	}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	prev := world.PlayerEntity
	t.Cleanup(func() { world.PlayerEntity = prev })
	// No player: the frame's player passes are all guarded on there being one,
	// and an ease has nothing to do with a body.
	world.PlayerEntity = nil
	return lvl
}

func TestAnEaseIsSteppedOncePerFrame(t *testing.T) {
	lvl := easingLevel(t)
	e := world.FindEntity(lvl, "crate")
	// Ten metres in one second, so being one tick out is 0.17 m and being
	// stepped twice is five metres — no rounding argument to have.
	e.Ease(lvl.Frames, world.TweenMove, mgl32.Vec3{10, 0, 0}, 1)

	const hz = 60
	for i := 0; i < hz/2; i++ {
		Step(lvl, 1.0/hz, Gates{ViewW: 1920, ViewH: 1080}, Input{})
	}

	// Halfway through the ease, halfway along it.
	if got := e.Position.X(); got < 4.8 || got > 5.2 {
		t.Errorf("half a second into a one-second ease the crate is at x %v, want about 5 — "+
			"at 10 it was stepped twice per frame, at 2.5 half as often as it should be", got)
	}

	// And it finishes where it was sent, rather than overshooting: a tween
	// clamps at its target, so a double step shows up as arriving early and
	// stopping, which the first assertion is what catches.
	for i := 0; i < hz/2; i++ {
		Step(lvl, 1.0/hz, Gates{ViewW: 1920, ViewH: 1080}, Input{})
	}
	if got := e.Position; got.Sub(mgl32.Vec3{10, 0, 0}).Len() > 0.01 {
		t.Errorf("after the full second the crate is at %v, want <10, 0, 0>", got)
	}
}

// The other half of the rule, stated where a reader will find it: the frame has
// exactly one call to each of the passes that ease something. A second one is
// the bug above, and it has no runtime symptom beyond a rate being wrong.
func TestTheFrameEasesEachKindOnce(t *testing.T) {
	src := readSimSource(t)
	for _, pass := range []string{"AdvanceTweens(", "AdvanceBrushes(", "AdvanceChunks(", "AdvanceWaypointFollowers("} {
		if n := countCalls(src, pass); n > 1 {
			t.Errorf("sim.Step calls %s %d times; more than once doubles whatever it advances", pass, n)
		}
	}
	// AdvanceTweens specifically must NOT be called here: EntityUpdate owns it,
	// and the frame calling it as well is exactly the double step.
	if countCalls(src, "AdvanceTweens(") != 0 {
		t.Error("sim.Step calls AdvanceTweens itself; collision.EntityUpdate already does, " +
			"so every entity ease would run at twice its authored rate")
	}
}

// readSimSource reads sim.go itself, the way player_camera_test.go reads it: the
// invariant is about which calls the frame makes, and there is no way to ask a
// running frame that.
func readSimSource(t *testing.T) string {
	t.Helper()
	return testenv.RepoFile(t, "internal", "sim", "sim.go")
}

// countCalls counts calls, not mentions: a comment naming a pass must not fail
// the test above, and both files here carry comments that name all of them.
func countCalls(src, call string) int {
	n := 0
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		n += strings.Count(line, call)
	}
	return n
}
