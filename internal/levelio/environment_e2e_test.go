package levelio

// environment_e2e_test.go — a level's sky and fog, all the way through YAML.
//
// The per-type round trips live in internal/world (environment_test.go); this
// is the assertion those cannot make, that the two new top-level keys are wired
// into BOTH directions of the loader. A `sky:` written by LevelToRaw and never
// read by loadRawLevel would pass every test in world and lose the sky on the
// next load.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func TestALevelWithSkyAndFogSurvivesSaveAndLoad(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	lvl, _ := NewBlankLevel()

	s := world.DefaultSky()
	s.Kind = world.SkyCubemap
	s.SetFacesFromBase("dusk")
	s.Rotation = 45
	lvl.Sky = &s

	f := world.DefaultFog()
	f.Mode = world.FogExp2
	f.Color = mgl32.Vec3{0.5, 0.4, 0.35}
	f.Start = 20
	f.Density = 0.01
	f.MaxOpacity = 0.9
	lvl.Fog = &f

	path := filepath.Join(t.TempDir(), "environment_level.yaml")
	if err := SaveLevel(&lvl, path); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}

	// The file has to actually carry the keys. An omitempty mistake would drop
	// the whole feature while every in-memory assertion below still passed.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"sky:", "fog:", "dusk_px", "dusk_nz", "exp2"} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("the saved level does not mention %q:\n%s", key, data)
		}
	}

	back, err := LoadLevel(path)
	if err != nil {
		t.Fatalf("LoadLevel: %v", err)
	}

	if back.Sky == nil {
		t.Fatal("the sky did not come back")
	}
	if back.Sky.Kind != world.SkyCubemap || back.Sky.Faces[0] != "dusk_px" || back.Sky.Rotation != 45 {
		t.Errorf("sky came back as %+v", *back.Sky)
	}
	if back.Fog == nil {
		t.Fatal("the fog did not come back")
	}
	if back.Fog.Mode != world.FogExp2 || back.Fog.Start != 20 || back.Fog.Density != 0.01 {
		t.Errorf("fog came back as %+v", *back.Fog)
	}
	if back.Fog.Color != (mgl32.Vec3{0.5, 0.4, 0.35}) {
		t.Errorf("fog colour came back as %v", back.Fog.Color)
	}
}

func TestALevelWithNoEnvironmentGainsNoKeys(t *testing.T) {
	// Every level in the project predates this feature. There is no version
	// number in the format, so the guarantee that none of them changes on its
	// next save is carried entirely by omitempty on the two pointers.
	out, err := yaml.Marshal(world.RawLevel{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"sky:", "fog:"} {
		if strings.Contains(string(out), key) {
			t.Errorf("a level with no environment wrote %q:\n%s", key, out)
		}
	}
}

func TestABlankLevelHasNoSkyOrFog(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	lvl, err := NewBlankLevel()
	if err != nil {
		t.Fatalf("NewBlankLevel: %v", err)
	}
	if lvl.Sky != nil || lvl.Fog != nil {
		t.Errorf("a blank level came up with sky=%+v fog=%+v; both are opt-in", lvl.Sky, lvl.Fog)
	}
}
