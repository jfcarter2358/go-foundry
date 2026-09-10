package levelio

// waypoint_save_test.go — waypoint animations through the level file.
//
// The sound tests' two assertions, for the sound tests' reasons: a fully
// specified animation survives the YAML boundary, and a level that has none
// does not grow a key — which is the whole of what keeps every level written
// before the feature byte-identical.

import (
	"reflect"
	"strings"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func TestWaypointAnimationsSurviveTheLevelFile(t *testing.T) {
	// A length past the last point — a hold at the end — which only survives
	// if the length is written rather than worked out from the points.
	want := []world.WaypointAnimation{{ID: "lift", Length: 4, Points: []world.Waypoint{
		// Reached the moment the brush is sent: a time of 0, which the file
		// leaves out and reads back as 0.
		{Position: mgl32.Vec3{0, 1, 0}, Scale: mgl32.Vec3{1, 1, 1}},
		{Position: mgl32.Vec3{0, 6, 0}, Rotation: mgl32.Vec3{0, 3.25, 0}, Scale: mgl32.Vec3{2, 1, 2}, Time: 1.5},
	}}}
	lvl := world.Level{WaypointAnimations: want}

	raw, err := LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	text, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back world.RawLevel
	if err := yaml.Unmarshal(text, &back); err != nil {
		t.Fatalf("unmarshal:\n%s\n%v", text, err)
	}

	var got []world.WaypointAnimation
	for _, rw := range back.WaypointAnimations {
		got = append(got, world.WaypointAnimationFromRaw(rw))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("waypoint animations changed across the level file:\n got %+v\nwant %+v\n\n%s", got, want, text)
	}
}

func TestALevelWithNoWaypointAnimationsWritesNoKey(t *testing.T) {
	raw, err := LevelToRaw(&world.Level{})
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "waypoint_animations:") {
		t.Errorf("a level with no waypoint animations wrote the key:\n%s", out)
	}
}

func TestAWaypointAnimationIDCollidesAcrossTheWholeLevel(t *testing.T) {
	// `play_waypoints(@door, @lift)` depends on @lift naming one object, which
	// only holds if LevelIDTaken knows this kind exists.
	lvl := world.Level{WaypointAnimations: []world.WaypointAnimation{{ID: "lift"}}}
	if !world.LevelIDTaken(&lvl, "lift") {
		t.Error("a waypoint animation's ID is not reserved; another object could take it")
	}
	if id := world.UniqueWaypointAnimationID(&lvl, "lift"); id == "lift" {
		t.Error("UniqueWaypointAnimationID handed back an ID that is already in use")
	}
}
