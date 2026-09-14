package levelio

// ai_save_test.go — AI controllers and objectives through the whole level
// load/save path: what the level file says comes back, and what only a play
// session knows is never written.

import (
	"strings"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func TestAIAndObjectivesSurviveALevelRoundTrip(t *testing.T) {
	drone := world.NewBlankEntity("drone")
	spec := world.DefaultAISpec()
	spec.Targets = []string{"player"}
	spec.Objectives = []string{"console"}
	spec.WanderRadius = 6
	drone.AI = &spec
	// Runtime state a play session left behind.
	drone.AIState = &world.AIState{Command: true, Target: "player"}

	console := world.NewVolume("console", mgl32.Vec3{4, 1, 4})
	obj := world.DefaultVolumeObjective()
	obj.Action = "hack"
	obj.Duration = 3
	console.Objective = &obj

	src := world.Level{Entities: []world.Entity{drone}, Volumes: []world.Volume{console}}
	raw, err := LevelToRaw(&src)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ai:", "wander_radius: 6", "objective:", "action: hack", "duration: 3"} {
		if !strings.Contains(string(out), key) {
			t.Errorf("saved level is missing %q:\n%s", key, out)
		}
	}
	for _, key := range []string{"command", "Command", "session"} {
		if strings.Contains(string(out), key) {
			t.Errorf("saved level carries runtime controller state (%q):\n%s", key, out)
		}
	}

	var again world.RawLevel
	if err := yaml.Unmarshal(out, &again); err != nil {
		t.Fatal(err)
	}
	lvl, err := rawToLevel(again)
	if err != nil {
		t.Fatalf("rawToLevel: %v", err)
	}
	e := world.FindEntity(&lvl, "drone")
	if e == nil || e.AI == nil || !e.AI.Equal(&spec) {
		t.Fatalf("the drone's controller came back as %+v, want %+v", e, spec)
	}
	if e.AIState != nil {
		t.Error("runtime controller state came back from a level file")
	}
	v := world.FindVolume(&lvl, "console")
	if v == nil || v.Objective == nil || *v.Objective != obj {
		t.Fatalf("the console's objective came back as %+v, want %+v", v, obj)
	}
}
