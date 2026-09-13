package levelio

import (
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// light_save_test.go — light angles across a save and a load.
//
// Here rather than with the rest of the light tests because it goes through
// lightsToRaw, which is part of the level serialiser and sits above
// internal/world.

// Saved angles win over the direction vector written alongside them, so a
// straight-down light keeps the yaw it was authored with.
func TestSaveLoadPreservesAnglesAtVerticalPitch(t *testing.T) {
	src := world.DirectionalLight{Yaw: 123, Pitch: -90, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1}
	src.SyncDirection()

	raw := world.RawLevel{Lights: lightsToRaw(world.LightSet{Directional: []world.DirectionalLight{src}}, false)}
	lvl := buildLevelLights(t, raw)
	dl := lvl.Lights.Directional[0]
	if dl.Yaw != 123 || dl.Pitch != -90 {
		t.Errorf("round trip gave yaw=%v pitch=%v, want 123/-90", dl.Yaw, dl.Pitch)
	}
}

// Levels authored before yaw/pitch existed carry only a direction vector; the
// loader must derive angles that reproduce it.
func TestLoadLegacyDirLightDerivesAngles(t *testing.T) {
	var raw world.RawLevel
	rd := world.RawDirLight{Brightness: 1}
	rd.Direction.X = -0.5
	rd.Direction.Y = -0.7
	rd.Direction.Z = 0.3
	raw.Lights.Directional = []world.RawDirLight{rd}

	lvl := buildLevelLights(t, raw)
	dl := lvl.Lights.Directional[0]
	want := mgl32.Vec3{-0.5, -0.7, 0.3}.Normalize()
	if dl.Direction.Sub(want).Len() > 1e-5 {
		t.Errorf("derived direction %v, want %v (from angles yaw=%v pitch=%v)", dl.Direction, want, dl.Yaw, dl.Pitch)
	}
}

// A NaN direction persisted by an older build must load as a usable light.
func TestLoadNaNDirLightFallsBackToDefaults(t *testing.T) {
	var raw world.RawLevel
	nan := float32(math.NaN())
	rd := world.RawDirLight{Brightness: 1}
	rd.Direction.X, rd.Direction.Y, rd.Direction.Z = nan, nan, nan
	raw.Lights.Directional = []world.RawDirLight{rd}

	lvl := buildLevelLights(t, raw)
	dl := lvl.Lights.Directional[0]
	if dl.Yaw != world.DefaultDirLightYaw || dl.Pitch != world.DefaultDirLightPitch {
		t.Fatalf("angles = (%v, %v), want defaults", dl.Yaw, dl.Pitch)
	}
	if math.Abs(float64(dl.Direction.Len())-1) > 1e-5 {
		t.Errorf("direction %v is not a unit vector", dl.Direction)
	}
}

// buildLevelLights converts raw lights through the normal load path (the GPU-free
// half of rawToLevel).
// Dynamic survives a save and a load on both kinds, and a light that says
// nothing about it — every level authored before the flag — loads static.
func TestSaveLoadPreservesDynamic(t *testing.T) {
	dl := world.DirectionalLight{Yaw: 10, Pitch: -45, Brightness: 1, Dynamic: true}
	dl.SyncDirection()
	ls := world.LightSet{
		Directional: []world.DirectionalLight{dl, {Yaw: 20, Pitch: -45, Brightness: 1}},
		Points:      []world.PointLight{{Radius: 5, Dynamic: true}, {Radius: 6}},
	}
	lvl := buildLevelLights(t, world.RawLevel{Lights: lightsToRaw(ls, false)})
	if !lvl.Lights.Directional[0].Dynamic || lvl.Lights.Directional[1].Dynamic {
		t.Errorf("directional Dynamic round-tripped as %v/%v, want true/false",
			lvl.Lights.Directional[0].Dynamic, lvl.Lights.Directional[1].Dynamic)
	}
	if !lvl.Lights.Points[0].Dynamic || lvl.Lights.Points[1].Dynamic {
		t.Errorf("point Dynamic round-tripped as %v/%v, want true/false",
			lvl.Lights.Points[0].Dynamic, lvl.Lights.Points[1].Dynamic)
	}
}

func buildLevelLights(t *testing.T, raw world.RawLevel) *world.Level {
	t.Helper()
	lvl := &world.Level{Lights: rawToLightSet(raw.Lights)}
	if len(lvl.Lights.Directional) != len(raw.Lights.Directional) {
		t.Fatalf("got %d directional lights, want %d", len(lvl.Lights.Directional), len(raw.Lights.Directional))
	}
	return lvl
}
