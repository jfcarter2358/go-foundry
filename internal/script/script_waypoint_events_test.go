package script

// script_waypoint_events_test.go — a waypoint's own events, as messages.
//
// The world records which atoms a reached point carries; what is tested here is
// that each becomes `(:atom:, animation ref)` to the brush, after the point's
// `:waypoint:`, and that the linter takes a level's own event for a design
// rather than a typo of an engine one.

import (
	"testing"

	"foundry/internal/flang"
	"foundry/internal/hooks"
)

func TestAPointsEventsArePostedToTheBrushAfterItsArrival(t *testing.T) {
	lvl, _ := waypointScriptLevel(t)
	rt := bindRuntime(t, lvl)
	rt.queue = nil

	hooks.RecordWaypointEvent(hooks.WaypointEvent{Object: "room", Animation: "rise", Index: 0,
		Events: []string{"lift_top", "chime"}})
	postWaypointEvents(rt)

	if len(rt.queue) != 3 {
		t.Fatalf("posted %d messages for an arrival with two events, want 3", len(rt.queue))
	}
	brush := flang.RefVal{Kind: flang.RefBrush, A: "room"}
	anim := flang.RefVal{Kind: flang.RefWaypoints, A: "rise"}

	if rt.queue[0].atom != "waypoint" {
		t.Errorf("the first message was :%s:, want the point's :waypoint: ahead of its events", rt.queue[0].atom)
	}
	for i, want := range []string{"lift_top", "chime"} {
		m := rt.queue[i+1]
		if m.atom != want || m.to != brush || len(m.args) != 1 || m.args[0] != anim {
			t.Errorf("event %d was posted as %+v, want (:%s:, @rise) to the brush", i, m, want)
		}
	}
}

func TestLintTakesALevelsWaypointEventForADesign(t *testing.T) {
	lvl, _ := waypointScriptLevel(t)
	src := "def f(:waypointz:, anim ref) -> null\n    log(\"at the top\")\nend\n"
	lint := func() []ScriptIssue {
		f, errs := flang.Parse("lint.flang", []byte(src))
		if len(errs) > 0 {
			t.Fatalf("parse errors: %v", errs)
		}
		return LintScript(lvl, f, nil, nil)
	}

	if !issuesMention(lint(), "did you mean :waypoint:") {
		t.Fatal("with no point posting it, :waypointz: is not taken for a near miss — the test proves nothing")
	}
	lvl.WaypointAnimations[0].Points[0].Events = []string{"waypointz"}
	if issues := lint(); issuesMention(issues, "did you mean") {
		t.Errorf("a handler for an event the level's waypoint posts was called a typo: %v", issues)
	}
}
