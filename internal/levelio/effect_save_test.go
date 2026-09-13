package levelio

// effect_save_test.go — effects through the whole level load/save path.
//
// world/effect_test.go covers the entry's own YAML form; this covers what the
// loader and saver around it do: every placed effect reaches the level with a
// level-unique ID, a one-shot a script set off is never written, and a level
// with no effects writes no key.

import (
	"strings"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func TestEffectsSurviveALevelRoundTrip(t *testing.T) {
	src := world.RawLevel{
		Sounds: []world.RawSound{{ID: "boom", Clip: ""}},
	}
	fx := world.NewEffect("boom", world.EffectExplosion, mgl32.Vec3{1, 2, 3})
	fx.Explosion.Radius = 7
	fx.Loop = false
	src.Effects = []world.RawEffect{world.EffectToRaw(&fx)}

	lvl, err := rawToLevel(src)
	if err != nil {
		t.Fatalf("rawToLevel: %v", err)
	}
	if len(lvl.Effects) != 1 {
		t.Fatalf("loaded %d effects, want 1", len(lvl.Effects))
	}
	got := lvl.Effects[0]
	if got.ID == "boom" {
		t.Error("the effect kept an ID the level's sound already has")
	}
	if got.Explosion.Radius != 7 || got.Loop || got.Kind != world.EffectExplosion {
		t.Errorf("loaded effect lost its fields: %+v", got)
	}

	// A script's one-shot is not level content.
	shot := world.NewEffect("shot", world.EffectExplosion, mgl32.Vec3{})
	shot.OneShot = true
	lvl.Effects = append(lvl.Effects, shot)

	raw, err := LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	if len(raw.Effects) != 1 || raw.Effects[0].ID != got.ID {
		t.Fatalf("saved effects %+v; want only %q, and never the one-shot", raw.Effects, got.ID)
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"effects:", "kind: explosion", "radius: 7", "loop: false"} {
		if !strings.Contains(string(out), key) {
			t.Errorf("saved level is missing %q:\n%s", key, out)
		}
	}
}

func TestEffectlessLevelGainsNoEffectsKey(t *testing.T) {
	var lvl world.Level
	raw, err := LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	out, _ := yaml.Marshal(raw)
	if strings.Contains(string(out), "effects:") {
		t.Fatalf("empty level wrote an effects key:\n%s", out)
	}
}
