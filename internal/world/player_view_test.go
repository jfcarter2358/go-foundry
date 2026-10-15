package world

import (
	"testing"

	"gopkg.in/yaml.v2"
)

func TestALevelWithNoCameraBlockStaysFirstPersonAndWritesNoBlock(t *testing.T) {
	var p PlayerSettings
	if p.Camera.Effective().ThirdPerson() {
		t.Error("the zero camera is third person; every existing level would change")
	}
	if r := PlayerSettingsToRaw(p); r != nil {
		t.Errorf("zero settings wrote a player block: %+v", r)
	}
	// Read back from a file with no camera or body, and written again.
	got, err := PlayerSettingsFromRaw(&RawPlayer{StepHeight: ptrF(0.7)})
	if err != nil {
		t.Fatal(err)
	}
	r := PlayerSettingsToRaw(got)
	if r == nil || r.Camera != nil || r.Body != nil {
		t.Errorf("a step-height-only block grew camera/body keys: %+v", r)
	}
	// Explicitly the defaults: still no block.
	if r := PlayerCameraToRaw(DefaultPlayerCamera()); r != nil {
		t.Errorf("default camera wrote %+v", r)
	}
}

func TestPlayerCameraAndBodyRoundTripThroughYAML(t *testing.T) {
	src := `
camera:
  mode: third_person
  distance: 4.5
  shoulder: 0
  pitch_min: -40
body:
  turn_rate: 900
  models:
    - name: body
      path: models/marine.fmdl
      rotation: {x: 0, y: 3.14159, z: 0}
      scale: {x: 1.5, y: 1.5, z: 1.5}
  locomotion:
    sprint: rifle_sprint
    run_speed: 5.5
`
	var raw RawPlayer
	if err := yaml.Unmarshal([]byte(src), &raw); err != nil {
		t.Fatal(err)
	}
	p, err := PlayerSettingsFromRaw(&raw)
	if err != nil {
		t.Fatal(err)
	}
	c := p.Camera.Effective()
	if !c.ThirdPerson() || c.Distance != 4.5 || c.Shoulder != 0 || c.PitchMin != -40 {
		t.Errorf("camera = %+v", c)
	}
	if d := DefaultPlayerCamera(); c.Height != d.Height || c.Radius != d.Radius || c.PitchMax != d.PitchMax {
		t.Errorf("unset keys did not take the defaults: %+v", c)
	}
	if len(p.Body.Models) != 1 || p.Body.Models[0].Path != "models/marine.fmdl" || p.Body.TurnRate != 900 {
		t.Errorf("body = %+v", p.Body)
	}
	l := p.Body.Locomotion.Effective()
	if l.Sprint != "rifle_sprint" || l.RunSpeed != 5.5 || l.Walk != "walk" || l.Jump != "jump_up" {
		t.Errorf("locomotion = %+v", l)
	}

	// Out and back in again: the same settings, and only the keys that were
	// written are written.
	out, err := yaml.Marshal(PlayerSettingsToRaw(p))
	if err != nil {
		t.Fatal(err)
	}
	var raw2 RawPlayer
	if err := yaml.Unmarshal(out, &raw2); err != nil {
		t.Fatal(err)
	}
	if raw2.Camera == nil || raw2.Camera.Height != nil || raw2.Camera.Radius != nil {
		t.Errorf("camera written back as %+v, want only the authored keys", raw2.Camera)
	}
	if raw2.Camera.Shoulder == nil || *raw2.Camera.Shoulder != 0 {
		t.Error("an authored shoulder of 0 was dropped: it is not the default")
	}
	p2, err := PlayerSettingsFromRaw(&raw2)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Camera != p.Camera || p2.Body.TurnRate != p.Body.TurnRate || p2.Body.Locomotion != p.Body.Locomotion ||
		len(p2.Body.Models) != 1 || p2.Body.Models[0].Path != p.Body.Models[0].Path ||
		p2.Body.Models[0].Scale != p.Body.Models[0].Scale {
		t.Errorf("round trip changed the settings:\n%+v\n%+v", p, p2)
	}
}

func TestChooseLocomotionPicksClipsByMotion(t *testing.T) {
	l := DefaultLocomotion()
	cases := []struct {
		name string
		s    LocomotionState
		want string
	}{
		{"standing", LocomotionState{OnGround: true}, "idle"},
		{"strolling", LocomotionState{OnGround: true, Speed: 1.5}, "walk"},
		{"running", LocomotionState{OnGround: true, Speed: 6}, "run"},
		{"sprinting", LocomotionState{OnGround: true, Speed: 9.9, Sprinting: true}, "run"},
		{"crouched still", LocomotionState{OnGround: true, Crouched: true}, "crouch_idle"},
		{"crouch walking", LocomotionState{OnGround: true, Crouched: true, Speed: 2}, "crouch_walk"},
		{"jumping up", LocomotionState{Jumped: true, VerticalSpeed: 8, AirTime: 0.1}, "jump_up"},
		{"jumping at a run", LocomotionState{Jumped: true, VerticalSpeed: 8, AirTime: 0.1, Speed: 6}, "jump_run"},
		{"falling after a jump", LocomotionState{Jumped: true, VerticalSpeed: -6, AirTime: 0.6}, "jet_fall"},
		{"off a curb", LocomotionState{Speed: 6, AirTime: 0.1, VerticalSpeed: -2}, "run"},
		{"off a cliff", LocomotionState{Speed: 6, AirTime: 0.5, VerticalSpeed: -10}, "jet_fall"},
	}
	for _, c := range cases {
		if got := ChooseLocomotion(l, c.s); got.Clip != c.want {
			t.Errorf("%s: clip %q, want %q", c.name, got.Clip, c.want)
		}
	}
	// Playback follows the speed, within reason.
	if r := ChooseLocomotion(l, LocomotionState{OnGround: true, Speed: l.WalkSpeed}).Rate; r != 1 {
		t.Errorf("walk at its authored speed plays at %v, want 1", r)
	}
	if r := ChooseLocomotion(l, LocomotionState{OnGround: true, Speed: 100}).Rate; r > 1.6 {
		t.Errorf("rate %v is not clamped", r)
	}
	// A custom sprint clip falls back to the run when the model lacks it.
	l.Sprint = "rifle_sprint"
	got := ChooseLocomotion(l, LocomotionState{OnGround: true, Speed: 9.9, Sprinting: true})
	if got.Clip != "rifle_sprint" || len(got.Fallbacks) == 0 || got.Fallbacks[0] != "run" {
		t.Errorf("sprint = %+v", got)
	}
}
