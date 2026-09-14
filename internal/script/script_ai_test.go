package script

// script_ai_test.go — AI controllers, as a script reaches them: the events a
// controller posts, the verbs that overrule it, and the properties that read and
// tune it.

import (
	"strings"
	"testing"

	"foundry/internal/ai"
	"foundry/internal/flang"
	"foundry/internal/hooks"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// aiScriptLevel is a room with a guard that has a controller, a crate that does
// not, and a console that is an objective.
func aiScriptLevel(t *testing.T) *world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	ch := world.Chunk{ID: "main"}
	ch.Brushes = world.NewHollowRoom("room", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{20, 4, 20}, 0.5, "concrete")
	ch.RecomputeBounds()
	spec := world.DefaultAISpec()
	guard := world.Entity{
		ID: "guard", Position: mgl32.Vec3{4, 0, 4}, Scale: mgl32.Vec3{1, 1, 1},
		BoundHalfX: 0.3, BoundHalfY: 0.9, BoundHalfZ: 0.3, BoundOffset: mgl32.Vec3{0, 0.9, 0},
		Collision: world.EntityCollision{World: true, Gravity: true, Movable: true},
		AI:        &spec,
	}
	crate := world.NewBlankEntity("crate")
	crate.Position = mgl32.Vec3{8, 0, 8}
	console := world.NewVolume("console", mgl32.Vec3{15, 1, 15})
	obj := world.DefaultVolumeObjective()
	obj.Action = "hack"
	console.Objective = &obj
	plain := world.NewVolume("plain", mgl32.Vec3{2, 1, 2})
	lvl := &world.Level{
		Chunks:   []world.Chunk{ch},
		Entities: []world.Entity{guard, crate},
		Volumes:  []world.Volume{console, plain},
	}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	ai.ResetSession()
	hooks.TakeAIEvents()
	t.Cleanup(func() {
		ai.ResetSession()
		hooks.TakeAIEvents()
	})
	return lvl
}

func TestAIEventsArePostedToTheAgentAndTheObjective(t *testing.T) {
	lvl := aiScriptLevel(t)
	rt := bindRuntime(t, lvl)
	rt.queue = nil

	hooks.RecordAIEvent(hooks.AIEvent{Atom: "target_acquired", Agent: "guard", Target: "player"})
	hooks.RecordAIEvent(hooks.AIEvent{Atom: "hack", Agent: "guard", Objective: "console", ToObjective: true})
	hooks.RecordAIEvent(hooks.AIEvent{Atom: "ai_arrived", Agent: "guard"})
	postAIEvents(rt)

	guard := flang.RefVal{Kind: flang.RefEntity, A: "guard"}
	console := flang.RefVal{Kind: flang.RefVolume, A: "console"}
	player := flang.RefVal{Kind: flang.RefEntity, A: "player"}
	want := []queuedMsg{
		{to: guard, atom: "target_acquired", args: []flang.Value{guard, player}},
		{to: guard, atom: "hack", args: []flang.Value{guard, console}},
		{to: console, atom: "hack", args: []flang.Value{guard, console}},
		{to: guard, atom: "ai_arrived", args: []flang.Value{guard}},
	}
	if len(rt.queue) != len(want) {
		t.Fatalf("posted %d messages, want %d: %+v", len(rt.queue), len(want), rt.queue)
	}
	for i, w := range want {
		m := rt.queue[i]
		if m.to != w.to || m.atom != w.atom || len(m.args) != len(w.args) {
			t.Fatalf("message %d was (:%s:) to %v with %d args, want (:%s:) to %v with %d", i, m.atom, m.to, len(m.args), w.atom, w.to, len(w.args))
		}
		for j := range w.args {
			if !flang.Equal(m.args[j], w.args[j]) {
				t.Errorf("message %d argument %d was %v, want %v", i, j, m.args[j], w.args[j])
			}
		}
	}
}

func TestEveryAIEventIsInTheReference(t *testing.T) {
	documented := map[string]bool{}
	for _, d := range EventDocs() {
		documented[strings.Trim(d.Atom, ":")] = true
	}
	for _, atom := range aiEventAtoms {
		if !documented[atom] {
			t.Errorf("a controller posts :%s: but script_event_doc.go does not list it", atom)
		}
	}
}

func TestAIMoveToCommandsAnAgentAndRefusesAnythingElse(t *testing.T) {
	lvl := aiScriptLevel(t)
	got := runScript(t, lvl, "", "def main() -> null\n    return ai_move_to(@guard, <16.0, 0.0, 16.0>)\nend")
	if !flang.Equal(got, flang.BoolVal(true)) {
		t.Fatalf("ai_move_to returned %v, want true for a reachable point", got)
	}
	st := lvl.Entities[0].AIState
	if st == nil || !st.Command || st.CommandGoal != (mgl32.Vec3{16, 0, 16}) {
		t.Fatalf("the guard was not commanded: %+v", st)
	}

	err := runScriptErr(t, lvl, "", "def main() -> null\n    return ai_move_to(@crate, <1.0, 0.0, 1.0>)\nend")
	if err == nil || !strings.Contains(err.Error(), "no AI controller") {
		t.Errorf("ai_move_to on an entity with no controller gave %v, want it refused by name", err)
	}
}

func TestAIObjectiveHandsAnAgentAVolume(t *testing.T) {
	lvl := aiScriptLevel(t)
	got := runScript(t, lvl, "", "def main() -> null\n    return ai_objective(@guard, @console)\nend")
	if !flang.Equal(got, flang.BoolVal(true)) {
		t.Fatalf("ai_objective returned %v, want true", got)
	}
	if v := runScript(t, lvl, "", "def main() -> null\n    return @guard.prop.ai_objective\nend"); !flang.Equal(v, flang.RefVal{Kind: flang.RefVolume, A: "console"}) {
		t.Errorf("ai_objective reads back as %v, want @console", v)
	}
	if v := runScript(t, lvl, "", "def main() -> null\n    return @console.prop.objective_agents\nend"); !flang.Equal(v, flang.IntVal(1)) {
		t.Errorf("objective_agents is %v with one agent assigned, want 1", v)
	}

	err := runScriptErr(t, lvl, "", "def main() -> null\n    return ai_objective(@guard, @plain)\nend")
	if err == nil || !strings.Contains(err.Error(), "not an objective") {
		t.Errorf("ai_objective with a plain volume gave %v, want it refused", err)
	}
}

func TestAIPropertiesReadAndTuneTheController(t *testing.T) {
	lvl := aiScriptLevel(t)
	if v := runScript(t, lvl, "", "def main() -> null\n    return @guard.prop.ai_state\nend"); !flang.Equal(v, flang.AtomVal("idle")) {
		t.Errorf("ai_state is %v, want :idle:", v)
	}
	if v := runScript(t, lvl, "", "def main() -> null\n    return @crate.prop.ai_state\nend"); !flang.Equal(v, flang.AtomVal("none")) {
		t.Errorf("ai_state on a crate is %v, want :none:", v)
	}

	runScript(t, lvl, "", "def main() -> null\n    @guard.prop.ai_speed = 7.5\n    @guard.prop.ai_targets = [\"player\", \"#intruder\"]\n    @guard.prop.ai_on_target = :watch:\nend")
	spec := lvl.Entities[0].AI
	if spec.Speed != 7.5 {
		t.Errorf("ai_speed wrote %v, want 7.5", spec.Speed)
	}
	if len(spec.Targets) != 2 || spec.Targets[0] != "player" || spec.Targets[1] != "#intruder" {
		t.Errorf("ai_targets wrote %v", spec.Targets)
	}
	if spec.OnTarget != world.AIWatch {
		t.Errorf("ai_on_target wrote %v, want watch", world.AITargetModeName(spec.OnTarget))
	}

	err := runScriptErr(t, lvl, "", "def main() -> null\n    @crate.prop.ai_speed = 2.0\nend")
	if err == nil || !strings.Contains(err.Error(), "no AI controller") {
		t.Errorf("tuning a crate's controller gave %v, want it reported", err)
	}

	runScript(t, lvl, "", "def main() -> null\n    @console.prop.objective_duration = 4.0\n    @console.prop.objective_action = :repair:\nend")
	if o := lvl.Volumes[0].Objective; o.Duration != 4 || o.ActionAtom() != "repair" {
		t.Errorf("objective properties wrote duration %v and action %q", o.Duration, o.Action)
	}
}

func TestFindPathAnswersWithCornersOrNull(t *testing.T) {
	lvl := aiScriptLevel(t)
	got := runScript(t, lvl, "", "def main() -> null\n    return find_path(<2.0, 0.0, 2.0>, <18.0, 0.0, 18.0>)\nend")
	l, ok := got.(*flang.ListVal)
	if !ok || len(l.Elems) == 0 {
		t.Fatalf("find_path across an open room gave %v, want a list of corners", got)
	}
	last, _ := toVec3(l.Elems[len(l.Elems)-1])
	if last.Sub(mgl32.Vec3{18, 0, 18}).Len() > 0.01 {
		t.Errorf("the path ends at %v, want the goal", last)
	}

	// Outside the room is on its roof, which no walk from inside reaches.
	if v := runScript(t, lvl, "", "def main() -> null\n    return find_path(<2.0, 0.0, 2.0>, <10.0, 4.5, 10.0>)\nend"); !flang.Equal(v, flang.NullVal{}) {
		t.Errorf("find_path to the roof from inside gave %v, want null", v)
	}
}

func TestLintKnowsTheAIVerbsAndEvents(t *testing.T) {
	lvl := aiScriptLevel(t)
	lint := func(src string) []ScriptIssue {
		f, errs := flang.Parse("lint.flang", []byte(src))
		if len(errs) > 0 {
			t.Fatalf("parse errors: %v", errs)
		}
		return LintScript(lvl, f, nil, nil)
	}

	if !issuesMention(lint("def f() -> null\n    ai_move_to(@crate, <1.0, 0.0, 1.0>)\nend\n"), "no AI controller") {
		t.Error("ai_move_to on an entity with no controller was not caught before running")
	}
	if !issuesMention(lint("def f() -> null\n    ai_objective(@guard, @plain)\nend\n"), "not an objective") {
		t.Error("ai_objective with a plain volume was not caught before running")
	}
	if !issuesMention(lint("def f(:target_aquired:, src ref, t ref) -> null\n    log(\"x\")\nend\n"), "did you mean :target_acquired:") {
		t.Error("a misspelt controller event was not caught")
	}
	if issues := lint("def f(:hack:, src ref, o ref) -> null\n    log(\"x\")\nend\n"); issuesMention(issues, "did you mean") {
		t.Errorf("a handler for an objective's own action was called a typo: %v", issues)
	}
}

func TestAIFollowWaypointsSendsAnAgentAlongAPath(t *testing.T) {
	lvl := aiScriptLevel(t)
	route := world.WaypointAnimation{ID: "patrol", Points: []world.Waypoint{
		world.NewWaypoint(mgl32.Vec3{6, 0, 6}), world.NewWaypoint(mgl32.Vec3{14, 0, 6}),
	}}
	route.Points[1].AI = &world.WaypointAI{WaitForSignal: true}
	lvl.WaypointAnimations = []world.WaypointAnimation{route}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}

	for _, spelling := range []string{"@patrol", `"patrol"`} {
		got := runScript(t, lvl, "", "def main() -> null\n    return ai_follow_waypoints(@guard, "+spelling+", true)\nend")
		if !flang.Equal(got, flang.BoolVal(true)) {
			t.Fatalf("ai_follow_waypoints(@guard, %s) returned %v", spelling, got)
		}
	}
	if v := runScript(t, lvl, "", "def main() -> null\n    return @guard.prop.ai_waypoints\nend"); !flang.Equal(v, flang.RefVal{Kind: flang.RefWaypoints, A: "patrol"}) {
		t.Errorf("ai_waypoints reads %v, want @patrol", v)
	}
	if v := runScript(t, lvl, "", "def main() -> null\n    return @guard.prop.ai_waypoint_index\nend"); !flang.Equal(v, flang.IntVal(0)) {
		t.Errorf("ai_waypoint_index reads %v, want 0", v)
	}
	if v := runScript(t, lvl, "", "def main() -> null\n    return ai_continue(@guard)\nend"); !flang.Equal(v, flang.BoolVal(false)) {
		t.Errorf("ai_continue on an agent still walking to its first point returned %v, want false", v)
	}

	runScript(t, lvl, "", "def main() -> null\n    ai_follow_waypoints(@guard, null)\nend")
	if v := runScript(t, lvl, "", "def main() -> null\n    return @guard.prop.ai_waypoints\nend"); !flang.Equal(v, flang.NullVal{}) {
		t.Errorf("after following null, ai_waypoints reads %v", v)
	}

	err := runScriptErr(t, lvl, "", "def main() -> null\n    return ai_follow_waypoints(@guard, \"nowhere\")\nend")
	if err == nil || !strings.Contains(err.Error(), "no waypoint animation") {
		t.Errorf("following a path that does not exist gave %v", err)
	}
	f, errs := flang.Parse("lint.flang", []byte("def f() -> null\n    ai_follow_waypoints(@crate, @patrol)\nend\n"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if !issuesMention(LintScript(lvl, f, nil, nil), "no AI controller") {
		t.Error("ai_follow_waypoints on an entity with no controller was not caught before running")
	}
}
