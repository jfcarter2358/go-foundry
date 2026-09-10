package script

// script_waypoint_follow_test.go — play_waypoints on everything besides a
// brush: entities, sprites, emitters, sounds, lights and volumes.
//
// What each kind does with a point is world/waypoint_follow_test.go's. What is
// tested here is the binding: that the verbs reach every kind and refuse the
// ones with a mover of their own, that `waypoints` reads back on each, that an
// arrival is posted to the object that arrived, and that the linter agrees.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/collision"
	"foundry/internal/flang"
	"foundry/internal/hooks"
	"foundry/internal/world"
)

// followScriptLevel is boundedTestLevel — which has the crate, the trigger, the
// lamp and the sun — plus a sprite, an emitter and a sound, and an animation
// called "rise" that ends one second in on {0 4 0}.
func followScriptLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := boundedTestLevel(t)
	lvl.Sprites = append(lvl.Sprites, world.NewSprite("torch", mgl32.Vec3{1, 0, 0}))
	lvl.Emitters = append(lvl.Emitters, world.NewEmitter("smoke", mgl32.Vec3{2, 0, 0}))
	lvl.Sounds = append(lvl.Sounds, world.NewSound("hum", mgl32.Vec3{3, 0, 0}))
	top := world.NewWaypoint(mgl32.Vec3{0, 4, 0})
	top.Time = 1
	lvl.WaypointAnimations = append(lvl.WaypointAnimations, world.WaypointAnimation{ID: "rise", Points: []world.Waypoint{top}})
	hooks.TakeWaypointEvents()
	t.Cleanup(func() { hooks.TakeWaypointEvents() })
	return lvl
}

func TestPlayWaypointsSendsEveryKindAPathCanCarry(t *testing.T) {
	lvl := followScriptLevel(t)
	for _, id := range []string{"crate", "torch", "smoke", "hum", "lamp", "trigger"} {
		got := runScript(t, lvl, "", "def main() -> null\n    return play_waypoints(@"+id+", @rise)\nend")
		if !flang.Equal(got, flang.BoolVal(true)) {
			t.Fatalf("play_waypoints(@%s, @rise) returned %v, want true", id, got)
		}
	}

	// Each on its own kind's tick: the entity in EntityUpdate, the rest in
	// AdvanceWaypointFollowers.
	if !world.AdvanceWaypointFollowers(lvl, 1) {
		t.Error("the lamp moved and the step did not ask for the lights to be uploaded")
	}
	collision.EntityUpdate(lvl, 1, true, nil)

	want := mgl32.Vec3{0, 4, 0}
	for id, got := range map[string]mgl32.Vec3{
		"crate":   lvl.Entities[0].Position,
		"torch":   lvl.Sprites[0].Position,
		"smoke":   lvl.Emitters[0].Position,
		"hum":     lvl.Sounds[0].Position,
		"lamp":    lvl.Lights.Points[0].Position,
		"trigger": lvl.Volumes[0].Position,
	} {
		if !vecNear(got, want, 1e-4) {
			t.Errorf("@%s ended the path at %v, want the point at %v", id, got, want)
		}
	}
}

func TestPlayWaypointsScalesASprite(t *testing.T) {
	lvl := followScriptLevel(t)
	lvl.WaypointAnimations[0].Points[0].Scale = mgl32.Vec3{2, 3, 1}
	sp := &lvl.Sprites[0] // authored 1×1

	runScript(t, lvl, "", script("    play_waypoints(@torch, @rise)"))
	world.AdvanceWaypointFollowers(lvl, 1)
	if got := sp.DrawnSize(); got != (mgl32.Vec2{2, 3}) {
		t.Errorf("at the end of the path the sprite is drawn %v, want 2×3", got)
	}
	got := runScript(t, lvl, "", "def main() -> null\n    return @torch.prop.scale\nend")
	if !flang.Equal(got, flang.VecVal{2, 3, 1}) {
		t.Errorf("scale reads back %v, want <2, 3, 1>", got)
	}

	runScript(t, lvl, "", script("    @torch.prop.scale = <1.0, 0.5, 1.0>"))
	if got := sp.DrawnSize(); got != (mgl32.Vec2{1, 0.5}) {
		t.Errorf("after writing scale the sprite is drawn %v, want 1×0.5", got)
	}
	err := runScriptErr(t, lvl, "", script("    @torch.prop.scale = <0.0, 1.0, 1.0>"))
	if !strings.Contains(err.Error(), "greater than zero") {
		t.Errorf("a zero scale failed with %q, want it refused by name", err)
	}
}

// An entity on a path does not fall: the path is its position, and gravity
// would only bank a fall speed to release the moment the path let go.
func TestAnEntityOnAPathDoesNotFall(t *testing.T) {
	lvl := followScriptLevel(t)
	lvl.WaypointAnimations[0].Points[0].Time, lvl.WaypointAnimations[0].Length = 4, 4
	e := &lvl.Entities[0]
	e.Collision.Gravity = true
	runScript(t, lvl, "", script("    play_waypoints(@crate, @rise)"))

	collision.EntityUpdate(lvl, 1, true, nil)
	if e.Velocity != (mgl32.Vec3{}) {
		t.Errorf("a second along the path the crate has banked a velocity of %v", e.Velocity)
	}
}

func TestPlayWaypointsTurnsADirectionalLight(t *testing.T) {
	lvl := followScriptLevel(t)
	sun := &lvl.Lights.Directional[0] // yaw 0, pitch -45
	lvl.WaypointAnimations[0].Points[0].Rotation = world.DirLightEuler(90, -45)

	runScript(t, lvl, "", script("    play_waypoints(@sun, @rise)"))
	if !world.AdvanceWaypointFollowers(lvl, 1) {
		t.Error("the sun turned and the step did not ask for the lights to be uploaded")
	}
	if !nearlyEqualF(sun.Yaw, 90, 1e-3) || !nearlyEqualF(sun.Pitch, -45, 1e-3) {
		t.Errorf("at the end of the path the sun is at yaw %v pitch %v, want 90 and -45", sun.Yaw, sun.Pitch)
	}
}

func nearlyEqualF(a, b, tol float32) bool { return a-b <= tol && b-a <= tol }

func TestEveryKindReadsWhatItIsFollowing(t *testing.T) {
	lvl := followScriptLevel(t)
	for _, id := range []string{"crate", "torch", "smoke", "hum", "lamp", "sun", "trigger"} {
		read := "def main() -> null\n    return @" + id + ".prop.waypoints\nend"
		if got := runScript(t, lvl, "", read); !flang.Equal(got, flang.NullVal{}) {
			t.Errorf("@%s following nothing reads back %v, want null", id, got)
		}
		runScript(t, lvl, "", script("    play_waypoints(@"+id+", @rise)"))
		got := runScript(t, lvl, "", read)
		if r, ok := got.(flang.RefVal); !ok || r.Kind != flang.RefWaypoints || r.A != "rise" {
			t.Errorf("@%s following @rise reads back %v, want a waypoints ref to rise", id, got)
		}
		got = runScript(t, lvl, "", "def main() -> null\n    return stop_waypoints(@"+id+")\nend")
		if !flang.Equal(got, flang.BoolVal(true)) {
			t.Errorf("stop_waypoints(@%s) on a travelling object returned %v, want true", id, got)
		}
	}
}

func TestAnArrivalIsPostedToTheObjectThatArrived(t *testing.T) {
	lvl := followScriptLevel(t)
	rt := bindRuntime(t, lvl)
	rt.queue = nil

	hooks.RecordWaypointEvent(hooks.WaypointEvent{Kind: hooks.WaypointSprite, Object: "torch", Animation: "rise", Index: 0,
		Events: []string{"lit"}})
	hooks.RecordWaypointEvent(hooks.WaypointEvent{Kind: hooks.WaypointLight, Object: "lamp", Animation: "rise", Index: 0, Ended: true})
	postWaypointEvents(rt)

	if len(rt.queue) != 3 {
		t.Fatalf("posted %d messages, want an arrival, its event and an end", len(rt.queue))
	}
	torch := flang.RefVal{Kind: flang.RefSprite, A: "torch"}
	lamp := flang.RefVal{Kind: flang.RefLight, A: "lamp"}
	if m := rt.queue[0]; m.atom != "waypoint" || m.to != torch || m.args[0] != torch {
		t.Errorf("the sprite's arrival was posted as %+v, want :waypoint: to @torch from @torch", m)
	}
	if m := rt.queue[1]; m.atom != "lit" || m.to != torch {
		t.Errorf("the sprite's point event was posted as %+v, want :lit: to @torch", m)
	}
	if m := rt.queue[2]; m.atom != "waypoints_end" || m.to != lamp || m.args[0] != lamp {
		t.Errorf("the light's end was posted as %+v, want :waypoints_end: to @lamp from @lamp", m)
	}
}

// The player and vehicles have movers of their own, and a chunk is not
// something a path carries; each refusal says which.
func TestWaypointVerbsRefuseWhatAPathCannotCarry(t *testing.T) {
	cases := []struct {
		name, target, want string
		prep               func(*world.Level)
	}{
		{"static brush", "room", "not dynamic", nil},
		{"vehicle", "crate", "vehicle", func(l *world.Level) { l.Entities[0].Vehicle = &world.VehicleSpec{} }},
		{"player", "player", "cannot move the player", nil},
		{"chunk", "chunk", "a dynamic brush, an entity, a sprite", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lvl := followScriptLevel(t)
			if tc.prep != nil {
				tc.prep(lvl)
			}
			prev := world.PlayerEntity
			world.PlayerEntity = &world.Entity{ID: "player"}
			t.Cleanup(func() { world.PlayerEntity = prev })

			err := runScriptErr(t, lvl, "", script("    play_waypoints(@"+tc.target+", @rise)"))
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("failed with %q, want it to mention %q", err, tc.want)
			}
			if lvl.Entities[0].Waypoints != nil || world.PlayerEntity.Waypoints != nil {
				t.Error("the refused call sent something along the path anyway")
			}
		})
	}
}

func TestLintKnowsEveryKindAPathCanCarry(t *testing.T) {
	lvl := followScriptLevel(t)
	for _, id := range []string{"crate", "torch", "smoke", "hum", "lamp", "sun", "trigger"} {
		if issues := lintBody(t, lvl, "    play_waypoints(@"+id+", @rise)"); len(issues) != 0 {
			t.Errorf("play_waypoints on @%s produced issues: %v", id, issues)
		}
	}
	if issues := lintBody(t, lvl, "    play_waypoints(@chunk, @rise)"); !issuesMention(issues, "moves a dynamic brush, an entity") {
		t.Errorf("a path on a chunk got no issue naming what a path moves: %v", issues)
	}
	lvl.Entities[0].Vehicle = &world.VehicleSpec{}
	if issues := lintBody(t, lvl, "    stop_waypoints(@crate)"); !issuesMention(issues, "vehicle") {
		t.Errorf("a path verb on a vehicle got no issue: %v", issues)
	}
}
