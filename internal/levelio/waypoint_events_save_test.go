package levelio

// waypoint_events_save_test.go — a waypoint's events through the level file:
// kept where a point has some, and no `events:` key where none does.

import (
	"reflect"
	"strings"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func TestWaypointEventsSurviveTheLevelFile(t *testing.T) {
	plain := world.NewWaypoint(mgl32.Vec3{0, 1, 0})
	top := world.NewWaypoint(mgl32.Vec3{0, 6, 0})
	top.Events = []string{"lift_top", "chime"}
	lvl := world.Level{WaypointAnimations: []world.WaypointAnimation{{ID: "lift", Points: []world.Waypoint{plain, top}}}}

	raw, err := LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	text, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := strings.Count(string(text), "events:"); got != 1 {
		t.Errorf("wrote %d events keys for one point with events and one without, want 1:\n%s", got, text)
	}

	var back world.RawLevel
	if err := yaml.Unmarshal(text, &back); err != nil {
		t.Fatalf("unmarshal:\n%s\n%v", text, err)
	}
	got := world.WaypointAnimationFromRaw(back.WaypointAnimations[0])
	if got.Points[0].Events != nil || !reflect.DeepEqual(got.Points[1].Events, top.Events) {
		t.Errorf("events changed across the level file: %v and %v\n\n%s", got.Points[0].Events, got.Points[1].Events, text)
	}
}
