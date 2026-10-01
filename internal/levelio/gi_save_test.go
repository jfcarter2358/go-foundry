package levelio

// gi_save_test.go — live GI's level settings, each light's GI switch, and the
// unbaked-level choice, through a save and a load.

import (
	"strings"
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"gopkg.in/yaml.v2"
)

func TestGISettingsAndNoBakeSurviveALevelRoundTrip(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	withStubTextures(t)

	lvl, err := LoadLevel(writeDisabledChunkLevel(t, "false"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got, want := lvl.Lights.Global.GI, world.DefaultGISettings(); got != want {
		t.Errorf("a level that says nothing of GI loaded %+v, want the defaults %+v", got, want)
	}
	if lvl.NoBake {
		t.Error("a level that says nothing of its bake loaded unbaked")
	}

	lvl.Lights.Global.GI = world.GISettings{Disabled: true, Intensity: 1.5, ProbeSpacing: 3, AmbientFloor: 0.2}
	lvl.Lights.Points = append(lvl.Lights.Points, world.PointLight{ID: "fill", Radius: 4, NoGI: true})
	lvl.NoBake = true
	raw, err := LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("to raw: %v", err)
	}
	back, err := rawToLevel(raw)
	if err != nil {
		t.Fatalf("from raw: %v", err)
	}
	if back.Lights.Global.GI != lvl.Lights.Global.GI {
		t.Errorf("GI settings came back %+v, want %+v", back.Lights.Global.GI, lvl.Lights.Global.GI)
	}
	if n := len(back.Lights.Points); n == 0 || !back.Lights.Points[n-1].NoGI {
		t.Error("a light's no_gi was dropped")
	}
	if !back.NoBake {
		t.Error("compile: bake: none was dropped")
	}

	// A baked level writes no `bake:` key, so adding it changed no level file.
	lvl.NoBake = false
	raw, err = LevelToRaw(&lvl)
	if err != nil {
		t.Fatal(err)
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "bake: ") {
		t.Errorf("a baked level wrote a bake key:\n%s", out)
	}
}

// Out-of-range GI numbers in a hand-edited file are pulled back into range
// rather than handed to the renderer.
func TestGISettingsAreClampedOnLoad(t *testing.T) {
	big, neg := float32(100), float32(-1)
	ls := rawToLightSet(world.RawLights{Global: world.RawGlobalLight{
		GI: &world.RawGI{ProbeSpacing: &big, Intensity: &neg, AmbientFloor: &big},
	}})
	gi := ls.Global.GI
	if gi.ProbeSpacing != world.GIProbeSpacingMax || gi.Intensity != 0 || gi.AmbientFloor != 1 {
		t.Errorf("clamped to %+v", gi)
	}
}
