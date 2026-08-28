package levelio

// emitter_save_test.go — emitters through the YAML boundary.
//
// Same two assertions the sprite tests make, and for the same reason: a
// fully-specified emitter must survive a round trip, and a level that has no
// emitters must not grow a key.

import (
	"foundry/internal/world"
	"reflect"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func TestEmitterRoundTripsEveryField(t *testing.T) {
	want := world.Emitter{
		ID:        "torch_hall",
		Effect:    "torch",
		Position:  mgl32.Vec3{1.5, 2.2, -3},
		Rotation:  mgl32.Vec3{0, 0.5, 0},
		Enabled:   false,
		Emitting:  false,
		RateScale: 0.5,
		Tint:      mgl32.Vec3{1, 0.6, 0.2},
		Attach:    world.Attach{Frame: "ship", Entity: "npc", Model: "body", Bone: "hand_r"},
	}

	got, err := world.EmitterFromRaw(world.EmitterToRaw(&want))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	// ScriptData holds maps and slices; Doc and Systems are runtime-only and
	// deliberately never serialized, which is half of what this asserts.
	got.ScriptData = world.ScriptData{}
	want.ScriptData = world.ScriptData{}
	if got.Doc != nil || got.Systems != nil {
		t.Error("the live simulation came back out of a level file; it must not be written")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("emitter changed across a round trip:\n got %+v\nwant %+v", got, want)
	}
}

func TestDefaultEmitterWritesNoOptionalKeys(t *testing.T) {
	e := world.NewEmitter("emitter", mgl32.Vec3{1, 2, 3})
	e.Effect = "torch"
	out, err := yaml.Marshal(world.EmitterToRaw(&e))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(out)
	for _, key := range []string{
		"enabled:", "emitting:", "rate_scale:", "tint:", "attach:",
		"rotation:", "tags:", "attributes:", "script:",
	} {
		if strings.Contains(text, key) {
			t.Errorf("a default emitter wrote %q; it should be omitted:\n%s", key, text)
		}
	}
}

func TestEmitterlessLevelGainsNoEmittersKey(t *testing.T) {
	var lvl world.Level
	raw, err := LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	if raw.Emitters != nil {
		t.Fatalf("empty level produced %d emitters", len(raw.Emitters))
	}
	out, _ := yaml.Marshal(raw)
	if strings.Contains(string(out), "emitters:") {
		t.Fatalf("empty level wrote an emitters key:\n%s", out)
	}
}

// TestEmitterIDsShareTheLevelNamespace — an emitter may not take a sprite's or
// a volume's name, or `@id` becomes ambiguous.
func TestEmitterIDsShareTheLevelNamespace(t *testing.T) {
	lvl := world.Level{
		Sprites:  []world.Sprite{{ID: "flame"}},
		Emitters: []world.Emitter{{ID: "smoke"}},
	}
	if got := world.UniqueEmitterID(&lvl, "flame"); got == "flame" {
		t.Error("an emitter was allowed to take a sprite's ID")
	}
	if got := world.UniqueSpriteID(&lvl, "smoke"); got == "smoke" {
		t.Error("a sprite was allowed to take an emitter's ID")
	}
}
