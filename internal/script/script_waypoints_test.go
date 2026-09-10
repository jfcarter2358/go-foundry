package script

// script_waypoints_test.go — sending a dynamic brush along a waypoint animation
// from a script.
//
// The travelling itself is world/waypoint_test.go's. What is tested here is the
// binding: that the verbs reach the right brush and the right animation by
// either spelling, that the dynamic gate refuses what writing `position` would
// refuse, that the arrivals become messages to the brush, and that the linter
// says so before a door is walked up to.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/hooks"
	"foundry/internal/world"
)

// waypointScriptLevel is the dynamic-brush fixture plus an animation called
// "rise" that lifts the "room" brush four units over two seconds.
func waypointScriptLevel(t *testing.T) (*world.Level, *world.Brush) {
	t.Helper()
	return withRiseAnimation(dynamicBrushLevel(t))
}

func withRiseAnimation(lvl *world.Level) (*world.Level, *world.Brush) {
	ch := &lvl.Chunks[0]
	b := &ch.Brushes[0]
	top := world.NewWaypoint(ch.LocalToWorld(b.Position()).Add(mgl32.Vec3{0, 4, 0}))
	top.Time = 2
	lvl.WaypointAnimations = []world.WaypointAnimation{
		{ID: "rise", Points: []world.Waypoint{top}},
	}
	hooks.TakeWaypointEvents()
	return lvl, b
}

func TestPlayWaypointsSendsABrushAlongAnAnimation(t *testing.T) {
	lvl, b := waypointScriptLevel(t)
	start := b.Position()

	got := runScript(t, lvl, "", "def main() -> null\n    return play_waypoints(@room, @rise)\nend")
	if !flang.Equal(got, flang.BoolVal(true)) {
		t.Fatalf("play_waypoints returned %v, want true", got)
	}
	if !vecNear(b.Position(), start, 1e-5) {
		t.Fatalf("sending the brush moved it at once, to %v; the engine's tick is what moves it", b.Position())
	}

	world.AdvanceBrushes(lvl, 1)
	if rose := b.Position().Y() - start.Y(); rose < 1.99 || rose > 2.01 {
		t.Errorf("after a second at 2/s the brush rose %v, want 2", rose)
	}
	world.AdvanceBrushes(lvl, 1)
	if rose := b.Position().Y() - start.Y(); rose < 3.99 || rose > 4.01 {
		t.Errorf("after two seconds the brush rose %v, want the whole 4", rose)
	}
	if b.Waypoints != nil {
		t.Error("the brush is still following after reaching the last point")
	}
	hooks.TakeWaypointEvents()
}

func TestPlayWaypointsAlsoTakesTheAnimationByName(t *testing.T) {
	lvl, b := waypointScriptLevel(t)
	runScript(t, lvl, "", script(`    play_waypoints(@room, "rise")`))
	if b.Waypoints == nil || b.Waypoints.Animation != "rise" {
		t.Fatalf("naming the animation as a string did not send the brush along it: %+v", b.Waypoints)
	}
}

// The gate `position` has, for the same reason: a static brush is baked, and a
// path that worked in the editor and did nothing in a shipped build is the bug
// the refusal exists to prevent.
func TestPlayWaypointsRefusesABrushThatIsNotDynamic(t *testing.T) {
	lvl, b := withRiseAnimation(boundedTestLevel(t))
	err := runScriptErr(t, lvl, "", script("    play_waypoints(@room, @rise)"))
	if !strings.Contains(err.Error(), "not dynamic") {
		t.Errorf("refused with %q, want it to name the dynamic toggle", err)
	}
	if b.Waypoints != nil {
		t.Error("the refused call sent the brush anyway")
	}
}

func TestPlayWaypointsOnAnAnimationWithNoPointsReturnsFalse(t *testing.T) {
	lvl, b := waypointScriptLevel(t)
	lvl.WaypointAnimations[0].Points = nil
	got := runScript(t, lvl, "", "def main() -> null\n    return play_waypoints(@room, @rise)\nend")
	if !flang.Equal(got, flang.BoolVal(false)) {
		t.Errorf("play_waypoints on an empty animation returned %v, want false", got)
	}
	if b.Waypoints != nil {
		t.Error("the brush was sent along an animation with nowhere to go")
	}
}

func TestStopWaypointsLeavesTheBrushWhereItGotTo(t *testing.T) {
	lvl, b := waypointScriptLevel(t)
	runScript(t, lvl, "", script("    play_waypoints(@room, @rise)"))
	world.AdvanceBrushes(lvl, 1)
	halfway := b.Position()

	got := runScript(t, lvl, "", "def main() -> null\n    return stop_waypoints(@room)\nend")
	if !flang.Equal(got, flang.BoolVal(true)) {
		t.Errorf("stop_waypoints on a travelling brush returned %v, want true", got)
	}
	world.AdvanceBrushes(lvl, 1)
	if !vecNear(b.Position(), halfway, 1e-5) {
		t.Errorf("a stopped brush moved on to %v", b.Position())
	}
	got = runScript(t, lvl, "", "def main() -> null\n    return stop_waypoints(@room)\nend")
	if !flang.Equal(got, flang.BoolVal(false)) {
		t.Errorf("stop_waypoints on a brush following nothing returned %v, want false", got)
	}
	hooks.TakeWaypointEvents()
}

func TestTheWaypointsPropertyNamesWhatABrushIsFollowing(t *testing.T) {
	lvl, _ := waypointScriptLevel(t)
	read := "def main() -> null\n    return @room.prop.waypoints\nend"

	if got := runScript(t, lvl, "", read); !flang.Equal(got, flang.NullVal{}) {
		t.Errorf("a brush following nothing reads back %v, want null", got)
	}
	runScript(t, lvl, "", script("    play_waypoints(@room, @rise)"))
	got := runScript(t, lvl, "", read)
	if r, ok := got.(flang.RefVal); !ok || r.Kind != flang.RefWaypoints || r.A != "rise" {
		t.Errorf("a brush following @rise reads back %v, want a waypoints ref to rise", got)
	}
}

func TestBrushScaleIsAWritablePlacementChannel(t *testing.T) {
	lvl := dynamicBrushLevel(t)
	b := &lvl.Chunks[0].Brushes[0]
	loBefore, hiBefore := b.LocalAABB()

	runScript(t, lvl, "", script("    @room.prop.scale = <2.0, 1.0, 1.0>"))
	if !vecNear(b.EffectiveScale(), mgl32.Vec3{2, 1, 1}, 1e-5) {
		t.Fatalf("scale is %v, want the written vec", b.EffectiveScale())
	}
	lo, hi := b.LocalAABB()
	if hi.X()-lo.X() <= hiBefore.X()-loBefore.X() {
		t.Errorf("a brush doubled along X did not grow its bounds: %v..%v", lo, hi)
	}
	got := runScript(t, lvl, "", "def main() -> null\n    return @room.prop.scale\nend")
	if !flang.Equal(got, flang.VecVal{2, 1, 1}) {
		t.Errorf("scale reads back %v, want <2,1,1>", got)
	}

	err := runScriptErr(t, lvl, "", script("    @room.prop.scale = <0.0, 1.0, 1.0>"))
	if !strings.Contains(err.Error(), "greater than zero") {
		t.Errorf("a zero scale axis failed with %q, want it refused by name", err)
	}

	static := boundedTestLevel(t)
	err = runScriptErr(t, static, "", script("    @room.prop.scale = <2.0, 1.0, 1.0>"))
	if !strings.Contains(err.Error(), "not dynamic") {
		t.Errorf("scaling a static brush failed with %q, want it to name the dynamic toggle", err)
	}
}

// The arrivals the engine records become messages to the brush, carrying the
// animation so one handler can serve several paths.
func TestWaypointArrivalsArePostedToTheBrush(t *testing.T) {
	lvl, _ := waypointScriptLevel(t)
	rt := bindRuntime(t, lvl)
	rt.queue = nil

	hooks.RecordWaypointEvent(hooks.WaypointEvent{Object: "room", Animation: "rise", Index: 0})
	hooks.RecordWaypointEvent(hooks.WaypointEvent{Object: "room", Animation: "rise", Index: 0, Ended: true})
	postWaypointEvents(rt)

	if len(rt.queue) != 2 {
		t.Fatalf("posted %d messages for an arrival and an end, want 2", len(rt.queue))
	}
	brush := flang.RefVal{Kind: flang.RefBrush, A: "room"}
	anim := flang.RefVal{Kind: flang.RefWaypoints, A: "rise"}

	arrive := rt.queue[0]
	if arrive.atom != "waypoint" || arrive.to != brush || len(arrive.args) != 3 ||
		arrive.args[1] != anim || !flang.Equal(arrive.args[2], flang.IntVal(0)) {
		t.Errorf("the arrival was posted as %+v", arrive)
	}
	end := rt.queue[1]
	if end.atom != "waypoints_end" || end.to != brush || len(end.args) != 2 || end.args[1] != anim {
		t.Errorf("the end was posted as %+v", end)
	}
}

// ---------- the linter ----------

func lintBody(t *testing.T, lvl *world.Level, body string) []ScriptIssue {
	t.Helper()
	f, errs := flang.Parse("lint.flang", []byte(script(body)))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	return LintScript(lvl, f, nil, nil)
}

func issuesMention(issues []ScriptIssue, s string) bool {
	for _, is := range issues {
		if strings.Contains(is.Msg, s) {
			return true
		}
	}
	return false
}

func TestLintCatchesPlayWaypointsOnAStaticBrush(t *testing.T) {
	lvl, _ := withRiseAnimation(boundedTestLevel(t))
	issues := lintBody(t, lvl, "    play_waypoints(@room, @rise)")
	if !issuesMention(issues, "not dynamic") {
		t.Errorf("no issue names the dynamic toggle: %v", issues)
	}
}

func TestLintSuggestsTheWaypointAnimationThatWasMeant(t *testing.T) {
	lvl, _ := waypointScriptLevel(t)
	issues := lintBody(t, lvl, `    play_waypoints(@room, "rize")`)
	if !issuesMention(issues, `did you mean "rise"`) {
		t.Errorf("a misspelt animation name got no suggestion: %v", issues)
	}
	if issues := lintBody(t, lvl, "    play_waypoints(@room, @rise)"); len(issues) != 0 {
		t.Errorf("a correct call produced issues: %v", issues)
	}
}
