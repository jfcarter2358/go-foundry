package script

// script_light_attach_test.go — attach and detach on point lights.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/world"
)

func TestAttachCarriesADynamicPointLight(t *testing.T) {
	lvl := followScriptLevel(t)
	lamp := &lvl.Lights.Points[0]
	lamp.Dynamic = true
	lamp.Position = mgl32.Vec3{0, 1, 0}
	crate := lvl.Entities[0].ID

	got := runScript(t, lvl, "", "def main() -> null\n    return attach(@lamp, @"+crate+")\nend")
	if !flang.Equal(got, flang.BoolVal(true)) {
		t.Fatalf("attach(@lamp, @%s) returned %v", crate, got)
	}
	if lamp.Attach.Entity != crate {
		t.Fatalf("lamp attach = %+v", lamp.Attach)
	}
	if got := runScript(t, lvl, "", "def main() -> null\n    return @lamp.prop.carrier\nend"); !flang.Equal(got, flang.RefVal{Kind: flang.RefEntity, A: crate}) {
		t.Errorf("@lamp.prop.carrier = %v", got)
	}
	want := lvl.Entities[0].Position.Add(mgl32.Vec3{0, 1, 0})
	if at := world.SceneLights(lvl).Points[0].Position; !vecNear(at, want, 1e-4) {
		t.Errorf("the scene lamp is at %v, want %v", at, want)
	}

	runScript(t, lvl, "", "def main() -> null\n    return detach(@lamp)\nend")
	if !lamp.Attach.IsZero() || !vecNear(lamp.Position, want, 1e-4) {
		t.Errorf("detach left attach %+v at %v, want loose at %v", lamp.Attach, lamp.Position, want)
	}
}

func TestAttachRefusesAStaticOrDirectionalLight(t *testing.T) {
	lvl := staticLightLevel(t)
	crate := lvl.Entities[0].ID
	for _, id := range []string{"lamp", "sun"} {
		err := runScriptErr(t, lvl, "", script("    attach(@"+id+", @"+crate+")"))
		if err == nil {
			t.Errorf("attach(@%s) was allowed", id)
			continue
		}
		if id == "sun" && !strings.Contains(err.Error(), "directional") {
			t.Errorf("attach(@sun) failed with %q, want it to say the light is directional", err)
		}
		if id == "lamp" && !strings.Contains(err.Error(), "static light") {
			t.Errorf("attach(@lamp) failed with %q, want the static-light refusal", err)
		}
	}
	if !lvl.Lights.Points[0].Attach.IsZero() {
		t.Error("the refused attach bound the lamp anyway")
	}
	if issues := lintBody(t, lvl, "    attach(@lamp, @"+crate+")"); !issuesMention(issues, "is static") {
		t.Errorf("lint said nothing about attaching a static light: %v", issues)
	}
	if issues := lintBody(t, lvl, "    attach(@sun, @"+crate+")"); !issuesMention(issues, "directional") {
		t.Errorf("lint said nothing about attaching a directional light: %v", issues)
	}
}
