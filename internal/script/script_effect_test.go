package script

// script_effect_test.go — effects reached from a script, end to end through
// the real event loop: the ref kind, the cache, refFor, the property table,
// the play/stop verbs, explode, and scriptedRefs all have to agree.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/world"
)

// effectLevel builds a level holding one scripted, stopped explosion.
func effectLevel(t *testing.T, script string) *world.Level {
	t.Helper()
	lvl := tickTestLevel(t)
	fx := world.NewEffect("boom", world.EffectExplosion, mgl32.Vec3{0, 1, 0})
	fx.Playing = false
	fx.Loop = false
	fx.Time = 2
	fx.Script = script
	lvl.Effects = append(lvl.Effects, fx)
	return lvl
}

func TestScriptCanReachAnEffect(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    @self.prop.radius = 9.0
    @self.prop.light_brightness = 2.0
end`)

	lvl := effectLevel(t, path)
	runTicks(t, lvl, 3)

	if x := lvl.Effects[0].Explosion; x.Radius != 9 || x.LightBrightness != 2 {
		t.Errorf("radius %v, light %v; the script's writes did not reach the effect", x.Radius, x.LightBrightness)
	}
}

func TestPlayDetonatesAnEffectAndStopEndsIt(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    play(@boom)
end`)

	lvl := effectLevel(t, path)
	lvl.Effects[0].Enabled = false
	runTicks(t, lvl, 1)

	fx := &lvl.Effects[0]
	if !fx.Enabled || !fx.Playing || fx.Time != 0 {
		t.Fatalf("after play: enabled %v, playing %v, time %v; want it set off from the start",
			fx.Enabled, fx.Playing, fx.Time)
	}

	path = writeScript(t, root, "stop.flang", `def on_spawn(:spawn:) -> null
    stop(@boom)
end`)
	fx.Script = path
	runTicks(t, lvl, 1)
	if lvl.Effects[0].Playing {
		t.Error("stop did not stop the effect")
	}
}

func TestExplodePlacesASelfRemovingExplosion(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    fx = explode(<1.0, 2.0, 3.0>, 6.0)
    @self.attr.made = fx.prop.kind
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "rocket", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{"made": world.StringAttr("")},
	})
	runTicks(t, lvl, 1)

	if len(lvl.Effects) != 1 {
		t.Fatalf("explode placed %d effects, want 1", len(lvl.Effects))
	}
	fx := lvl.Effects[0]
	if !fx.OneShot || fx.Loop {
		t.Error("explode's effect is not a non-looping one-shot; it would never be cleaned up")
	}
	if fx.Position != (mgl32.Vec3{1, 2, 3}) || fx.Explosion.Radius != 6 {
		t.Errorf("explode placed %+v at %v", fx.Explosion, fx.Position)
	}
	if a, _ := lvl.Entities[0].Attr("made"); a.S != "explosion" {
		t.Errorf("the returned ref read kind %q; explode should return the effect", a.S)
	}
}
