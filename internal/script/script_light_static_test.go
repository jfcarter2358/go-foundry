package script

// script_light_static_test.go — the static/dynamic gate on lights.
//
// The light counterpart of the brush gate in script_brush_move_test.go, for the
// same reason: a static light is baked into the compiled level's vertices, so
// changing one from a script would change it on everything lit live and not on
// the walls — working in the editor and half-working once shipped. The write is
// refused outright, in every build.

import (
	"strings"
	"testing"

	"foundry/internal/flang"
	"foundry/internal/world"
)

// staticLightLevel is the shared fixture with its lamp and sun made static.
func staticLightLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := followScriptLevel(t)
	lvl.Lights.Points[0].Dynamic = false
	lvl.Lights.Directional[0].Dynamic = false
	return lvl
}

// Every property a light lets a script write is refused on a static one, and
// the light is left as it was. Enumerated from the table rather than listed, so
// a property added later cannot slip past the gate.
func TestAStaticLightRefusesScriptWrites(t *testing.T) {
	values := map[string]string{
		"color": "<1.0, 0.0, 0.0>", "position": "<1.0, 2.0, 3.0>",
		"brightness": "5.0", "radius": "3.0", "yaw": "90.0", "pitch": "-10.0",
		"cast_shadows": "true",
	}
	for prop, acc := range propTables[flang.RefLight] {
		if acc.Set == nil {
			continue
		}
		val, ok := values[prop]
		if !ok {
			t.Errorf("light property %q is writable and this test has no value for it", prop)
			continue
		}
		// yaw and pitch belong to the sun; everything else the lamp has.
		target := "lamp"
		if prop == "yaw" || prop == "pitch" {
			target = "sun"
		}
		lvl := staticLightLevel(t)
		pt, sun := lvl.Lights.Points[0], lvl.Lights.Directional[0]

		err := runScriptErr(t, lvl, "", script("    @"+target+".prop."+prop+" = "+val))
		if err == nil {
			t.Errorf("writing %s on a static light was allowed", prop)
			continue
		}
		if !strings.Contains(err.Error(), "static light") || !strings.Contains(err.Error(), "Dynamic") {
			t.Errorf("writing %s failed with %q, want it to name the static light and the Dynamic toggle", prop, err)
		}
		if !sameLight(lvl.Lights.Points[0], pt, lvl.Lights.Directional[0], sun) {
			t.Errorf("writing %s changed the static light anyway", prop)
		}
	}
}

// sameLight compares the fields a script can write, which is what the gate has
// to leave alone. The structs themselves carry ScriptData and so do not compare.
func sameLight(p, p0 world.PointLight, d, d0 world.DirectionalLight) bool {
	return p.Position == p0.Position && p.Color == p0.Color && p.Brightness == p0.Brightness &&
		p.Radius == p0.Radius && p.CastShadows == p0.CastShadows &&
		d.Yaw == d0.Yaw && d.Pitch == d0.Pitch && d.Direction == d0.Direction &&
		d.Color == d0.Color && d.Brightness == d0.Brightness && d.CastShadows == d0.CastShadows
}

// `dynamic` reads on either kind and cannot be written: flipping it at run time
// could not take a static light back out of the bake.
func TestDynamicIsReadableAndReadOnly(t *testing.T) {
	lvl := staticLightLevel(t)
	lvl.Lights.Directional[0].Dynamic = true
	if got := runScript(t, lvl, "", "def main() -> null\n    return @lamp.prop.dynamic\nend"); !flang.Equal(got, flang.BoolVal(false)) {
		t.Errorf("a static lamp reads dynamic as %v, want false", got)
	}
	if got := runScript(t, lvl, "", "def main() -> null\n    return @sun.prop.dynamic\nend"); !flang.Equal(got, flang.BoolVal(true)) {
		t.Errorf("a dynamic sun reads dynamic as %v, want true", got)
	}
	if propTables[flang.RefLight]["dynamic"].Set != nil {
		t.Error("`dynamic` is writable on a light")
	}
}

// A static light cannot be sent along a path either, and the linter says so
// before the call runs.
func TestPlayWaypointsRefusesAStaticLight(t *testing.T) {
	for _, id := range []string{"lamp", "sun"} {
		lvl := staticLightLevel(t)
		err := runScriptErr(t, lvl, "", script("    play_waypoints(@"+id+", @rise)"))
		if err == nil || !strings.Contains(err.Error(), "static light") {
			t.Errorf("play_waypoints on static @%s gave %v, want a static-light refusal", id, err)
		}
		if lvl.Lights.Points[0].Waypoints != nil || lvl.Lights.Directional[0].Waypoints != nil {
			t.Errorf("the refused call sent @%s along the path anyway", id)
		}
		if issues := lintBody(t, lvl, "    play_waypoints(@"+id+", @rise)"); !issuesMention(issues, "is static") {
			t.Errorf("lint said nothing about play_waypoints on static @%s: %v", id, issues)
		}
	}
}

func TestLintFlagsAWriteToAStaticLight(t *testing.T) {
	lvl := staticLightLevel(t)
	if issues := lintBody(t, lvl, "    @lamp.prop.brightness = 2.0"); !issuesMention(issues, "is static") {
		t.Errorf("lint said nothing about writing a static light: %v", issues)
	}
	if issues := lintBody(t, lvl, "    x = @lamp.prop.dynamic"); issuesMention(issues, "is static") {
		t.Errorf("lint flagged a read of a static light: %v", issues)
	}
	lvl.Lights.Points[0].Dynamic = true
	if issues := lintBody(t, lvl, "    @lamp.prop.brightness = 2.0"); issuesMention(issues, "is static") {
		t.Errorf("lint flagged a write to a dynamic light: %v", issues)
	}
}
