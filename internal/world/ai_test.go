package world

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// guardDef is a type with a controller that differs from the defaults in a
// handful of fields, lists included.
func guardDef(t *testing.T) *EntityDef {
	t.Helper()
	const src = `
id: guard
bound_half_x: 0.3
bound_half_y: 0.9
bound_half_z: 0.3
ai:
  speed: 5
  targets: [player, "#intruder"]
  on_target: watch
  objectives: ["#route"]
  loop: true
  move_anim: walk
`
	var raw RawEntityDef
	if err := yaml.Unmarshal([]byte(src), &raw); err != nil {
		t.Fatal(err)
	}
	def, err := entityDefFromRaw(raw, "guard")
	if err != nil {
		t.Fatal(err)
	}
	return def
}

func TestAnAIBlockParsesOverTheDefaults(t *testing.T) {
	def := guardDef(t)
	s := def.AI
	if s == nil {
		t.Fatal("the ai block was dropped")
	}
	want := DefaultAISpec()
	want.Speed = 5
	want.OnTarget = AIWatch
	want.Loop = true
	want.MoveAnim = "walk"
	want.Targets = []string{"player", "#intruder"}
	want.Objectives = []string{"#route"}
	if !s.Equal(&want) {
		t.Errorf("parsed %+v, want %+v", *s, want)
	}
	if AIToRaw(s, nil) == nil {
		t.Error("a definition's spec wrote no block at all")
	}
}

func TestInstancesGetTheirOwnControllerNotTheTypes(t *testing.T) {
	def := guardDef(t)
	a, b := def.Instantiate("a"), def.Instantiate("b")
	a.AI.Speed = 9
	a.AI.Targets[0] = "someone_else"
	if b.AI.Speed != 5 || b.AI.Targets[0] != "player" || def.AI.Targets[0] != "player" {
		t.Error("tuning one instance's controller reached another instance or the type")
	}
}

func TestAnInstanceSavesOnlyWhatItChangedOfItsController(t *testing.T) {
	def := guardDef(t)
	lookup := func(string) (*EntityDef, error) { return def, nil }

	e := def.Instantiate("g1")
	if r := EntityToRaw(&e, def); r.AI != nil {
		t.Errorf("an untouched instance wrote an ai block: %+v", *r.AI)
	}

	e.AI.Speed = 2
	e.AI.Objectives = []string{"post_a", "post_b"}
	r := EntityToRaw(&e, def)
	if r.AI == nil || r.AI.Speed == nil || *r.AI.Speed != 2 || len(r.AI.Objectives) != 2 {
		t.Fatalf("the instance's changes were not written: %+v", r.AI)
	}
	if r.AI.OnTarget != "" || r.AI.Loop != nil || r.AI.Targets != nil || r.AI.MoveAnim != nil {
		t.Errorf("fields the instance left alone were written: %+v", *r.AI)
	}

	back, err := EntityFromRaw(r, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !back.AI.Equal(e.AI) {
		t.Errorf("round trip gave %+v, want %+v", *back.AI, *e.AI)
	}
}

func TestATypelessEntitysControllerRoundTrips(t *testing.T) {
	e := NewBlankEntity("drone")
	spec := DefaultAISpec()
	e.AI = &spec
	r := EntityToRaw(&e, nil)
	if r.AI == nil {
		t.Fatal("a typeless entity with a default controller wrote no ai block, so it would load without one")
	}
	out, err := yaml.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "ai: {}") {
		t.Errorf("a default controller should save as `ai: {}`:\n%s", out)
	}
	var again RawEntity
	if err := yaml.Unmarshal(out, &again); err != nil {
		t.Fatal(err)
	}
	back, err := EntityFromRaw(again, nil)
	if err != nil {
		t.Fatal(err)
	}
	if back.AI == nil || !back.AI.Equal(&spec) {
		t.Errorf("the controller did not survive YAML: %+v", back.AI)
	}
}

func TestAnObjectiveBlockRoundTrips(t *testing.T) {
	v := NewVolume("console", mgl32.Vec3{1, 2, 3})
	o := DefaultVolumeObjective()
	o.Action = "hack"
	o.Duration = 0 // a meaningful zero: done on arrival
	o.Capacity = 2
	o.AgentTag = "engineer"
	o.Face, o.Once = true, true
	v.Objective = &o

	out, err := yaml.Marshal(VolumeToRaw(&v))
	if err != nil {
		t.Fatal(err)
	}
	var raw RawVolume
	if err := yaml.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	back, err := VolumeFromRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if back.Objective == nil || *back.Objective != o {
		t.Errorf("objective came back as %+v, want %+v\n%s", back.Objective, o, out)
	}

	var bare RawVolume
	if err := yaml.Unmarshal([]byte("id: x\nobjective: {}\n"), &bare); err != nil {
		t.Fatal(err)
	}
	if got, _ := VolumeFromRaw(bare); got.Objective == nil || *got.Objective != DefaultVolumeObjective() {
		t.Errorf("`objective: {}` loaded as %+v, want the defaults", got.Objective)
	}
}

func TestActionAtomsIgnoreTheColonsAnAuthorTyped(t *testing.T) {
	o := VolumeObjective{Action: " :hack: "}
	if got := o.ActionAtom(); got != "hack" {
		t.Errorf("ActionAtom = %q, want hack", got)
	}
}

func TestAWaypointsAIBlockRoundTripsAndClonesApart(t *testing.T) {
	a := WaypointAnimation{ID: "assault", Points: []Waypoint{NewWaypoint(mgl32.Vec3{1, 0, 1}), NewWaypoint(mgl32.Vec3{5, 0, 1})}}
	a.Points[1].AI = &WaypointAI{
		Volume: "yard", Wait: 2.5, WaitForSignal: true, SpeedScale: 1.8,
		Engage: AIEngageIgnore, TakeCover: true, CoverRadius: 4, Face: true, Animation: "crouch",
	}
	out, err := yaml.Marshal(WaypointAnimationToRaw(&a))
	if err != nil {
		t.Fatal(err)
	}
	var raw RawWaypointAnimation
	if err := yaml.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	back := WaypointAnimationFromRaw(raw)
	if back.Points[0].AI != nil {
		t.Error("a point with no AI block came back with one")
	}
	if got := back.Points[1].AI; got == nil || *got != *a.Points[1].AI {
		t.Fatalf("the AI block came back as %+v, want %+v\n%s", got, a.Points[1].AI, out)
	}

	c := a.Clone()
	c.Points[1].AI.Wait = 99
	if a.Points[1].AI.Wait == 99 {
		t.Error("a cloned path shares its points' AI blocks with the original")
	}

	cover := &WaypointAI{TakeCover: true}
	if cover.EffectiveEngage() != AIEngageHold {
		t.Error("a cover point that inherits its engage mode does not hold")
	}
	var none *WaypointAI
	if none.Stops() || none.EffectiveSpeedScale() != 1 || none.EffectiveCoverRadius() != DefaultCoverRadius {
		t.Error("a point with no AI block is not a plain pass-through")
	}
}

func TestAnAIBlocksPathSurvivesTheInstanceDiff(t *testing.T) {
	def := guardDef(t)
	e := def.Instantiate("g")
	e.AI.Waypoints, e.AI.WaypointsLoop = "north_beat", true
	r := EntityToRaw(&e, def)
	if r.AI == nil || r.AI.Waypoints == nil || *r.AI.Waypoints != "north_beat" || r.AI.WaypointsLoop == nil {
		t.Fatalf("an instance's path was not written: %+v", r.AI)
	}
	back, err := EntityFromRaw(r, func(string) (*EntityDef, error) { return def, nil })
	if err != nil {
		t.Fatal(err)
	}
	if back.AI.Waypoints != "north_beat" || !back.AI.WaypointsLoop {
		t.Errorf("the path came back as %q loop %v", back.AI.Waypoints, back.AI.WaypointsLoop)
	}
}
