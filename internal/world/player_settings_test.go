package world

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// A level with no `player:` block gets the engine's default, and no viewmodel.
func TestAbsentPlayerBlockFallsBackToTheDefault(t *testing.T) {
	p := mustPlayerSettings(t, nil)
	if p.StepHeight != 0 || len(p.Models) != 0 {
		t.Fatalf("absent block gave %+v, want the zero value", p)
	}
	if got, want := p.EffectiveStepHeight(), DefaultStepHeight(); got != want {
		t.Errorf("step height %v, want the default %v", got, want)
	}
}

// The default is a stair riser, not a body height. It was the latter, which on
// solid brush geometry meant the player could walk up onto anything shorter
// than themselves — and gave StepUp a lift tall enough to cross a ceiling with.
func TestTheDefaultStepHeightIsACurb(t *testing.T) {
	if got := DefaultStepHeight(); got != 0.5 {
		t.Errorf("DefaultStepHeight() = %v, want 0.5", got)
	}
	if body := NewPlayer(mgl32.Vec3{}); DefaultStepHeight() >= body.BoundHalfY*2 {
		t.Errorf("the default is still the body's own height (%v)", body.BoundHalfY*2)
	}
}

// A non-positive step height is the default rather than "no stepping at all":
// zero clearance would be refused by the seam between two floors that differ by
// a rounding error, so it is never what an author meant.
func TestNonPositiveStepHeightIsTheDefault(t *testing.T) {
	for _, v := range []float32{0, -1} {
		p := PlayerSettings{StepHeight: v}
		if got, want := p.EffectiveStepHeight(), DefaultStepHeight(); got != want {
			t.Errorf("step height %v gave %v, want the default %v", v, got, want)
		}
	}
}

func TestAuthoredStepHeightIsUsedAsGiven(t *testing.T) {
	v := float32(0.75)
	p := mustPlayerSettings(t, &RawPlayer{StepHeight: &v})
	if got := p.EffectiveStepHeight(); got != v {
		t.Errorf("step height %v, want %v", got, v)
	}
}

// Saving a level that never touched the block must write no block, or every
// level authored before this one would gain one the first time it was opened.
func TestDefaultPlayerSettingsWriteNoBlock(t *testing.T) {
	if r := PlayerSettingsToRaw(PlayerSettings{}); r != nil {
		t.Errorf("default settings wrote %+v, want no block", r)
	}
}

func TestPlayerSettingsRoundTrip(t *testing.T) {
	in := PlayerSettings{StepHeight: 0.75}
	raw := PlayerSettingsToRaw(in)
	if raw == nil || raw.StepHeight == nil {
		t.Fatalf("set settings wrote %+v, want a step height", raw)
	}
	out := mustPlayerSettings(t, raw)
	if out.StepHeight != in.StepHeight || len(out.Models) != 0 {
		t.Errorf("round trip gave %+v, want %+v", out, in)
	}
}

// A level's player viewmodel round-trips like an entity's own models: the
// same RawEntityModel conversion, just reached through PlayerSettings instead
// of an EntityDef.
func TestPlayerViewmodelRoundTrips(t *testing.T) {
	in := PlayerSettings{Models: []EntityModel{
		{Name: "hands", Path: "models/fp_arms.fmdl"},
		{Name: "rifle", Path: "models/rifle.fmdl", Parent: "hands", Bone: "hand_socket.R"},
	}}
	raw := PlayerSettingsToRaw(in)
	if raw == nil || len(raw.Models) != 2 {
		t.Fatalf("wrote %+v, want two models", raw)
	}
	out := mustPlayerSettings(t, raw)
	if len(out.Models) != 2 {
		t.Fatalf("round trip gave %d models, want 2", len(out.Models))
	}
	if out.Models[1].Parent != "hands" || out.Models[1].Bone != "hand_socket.R" {
		t.Errorf("rifle's mount came back as %q/%q", out.Models[1].Parent, out.Models[1].Bone)
	}
	if out.Models[0].Path != "models/fp_arms.fmdl" {
		t.Errorf("hands' path came back as %q", out.Models[0].Path)
	}
}

// An empty player: block with only a viewmodel, and no step_height, must not
// silently default StepHeight to a real number written back out — it stays
// nil (the engine's default) both ways.
func TestPlayerViewmodelAloneLeavesStepHeightAtTheDefault(t *testing.T) {
	in := PlayerSettings{Models: []EntityModel{{Name: "hands", Path: "models/fp_arms.fmdl"}}}
	raw := PlayerSettingsToRaw(in)
	if raw == nil {
		t.Fatal("wrote no block at all, want one for the viewmodel")
	}
	if raw.StepHeight != nil {
		t.Errorf("wrote step_height %v with none given", *raw.StepHeight)
	}
	if out := mustPlayerSettings(t, raw); out.EffectiveStepHeight() != DefaultStepHeight() {
		t.Errorf("effective step height %v, want the default", out.EffectiveStepHeight())
	}
}

// mustPlayerSettings is PlayerSettingsFromRaw for a block the test knows is
// well-typed.
func mustPlayerSettings(t *testing.T, r *RawPlayer) PlayerSettings {
	t.Helper()
	p, err := PlayerSettingsFromRaw(r)
	if err != nil {
		t.Fatalf("PlayerSettingsFromRaw: %v", err)
	}
	return p
}

// The player's script, attributes and params round-trip the way an entity
// type's do, and a mistyped attribute is an error rather than a silent drop.
func TestPlayerScriptRoundTrips(t *testing.T) {
	in := PlayerSettings{
		Script:       "scripts/player_combat.flang",
		Attributes:   AttrSet{"health": {Type: AttrInt, I: 100}},
		ScriptParams: AttrSet{"restart_level": {Type: AttrString, S: "level1"}},
	}
	raw := PlayerSettingsToRaw(in)
	if raw == nil || raw.Script != in.Script || len(raw.Attributes) != 1 || len(raw.Params) != 1 {
		t.Fatalf("wrote %+v, want the script, one attribute and one param", raw)
	}
	out := mustPlayerSettings(t, raw)
	if out.Script != in.Script {
		t.Errorf("script came back as %q", out.Script)
	}
	if a, ok := out.Attributes.Attr("health"); !ok || a.I != 100 {
		t.Errorf("health came back as %+v (present %v)", a, ok)
	}
	if a, ok := out.ScriptParams.Attr("restart_level"); !ok || a.S != "level1" {
		t.Errorf("restart_level came back as %+v (present %v)", a, ok)
	}

	bad := &RawPlayer{Attributes: map[string]RawAttribute{"health": {Type: "int", Value: "lots"}}}
	if _, err := PlayerSettingsFromRaw(bad); err == nil {
		t.Error("a mistyped attribute loaded without an error")
	}
}

// Each play session starts the player from the authored attributes, not from
// wherever the last session's script left them.
func TestApplyPlayerScriptStartsFromTheAuthoredAttributes(t *testing.T) {
	prev := PlayerEntity
	defer func() { PlayerEntity = prev }()
	PlayerEntity = NewPlayer(mgl32.Vec3{})

	lvl := &Level{Player: PlayerSettings{
		Script:     "scripts/player_combat.flang",
		Attributes: AttrSet{"health": {Type: AttrInt, I: 100}},
	}}
	ApplyPlayerScript(lvl)
	if PlayerEntity.Script != lvl.Player.Script {
		t.Fatalf("player script %q, want %q", PlayerEntity.Script, lvl.Player.Script)
	}
	PlayerEntity.SetAttr("health", AttrValue{Type: AttrInt, I: 0})
	if a, _ := lvl.Player.Attributes.Attr("health"); a.I != 100 {
		t.Fatalf("a write to the player reached the level's authored copy (health %d)", a.I)
	}
	ApplyPlayerScript(lvl)
	if a, _ := PlayerEntity.Attr("health"); a.I != 100 {
		t.Errorf("second session started at health %d, want 100", a.I)
	}
}
