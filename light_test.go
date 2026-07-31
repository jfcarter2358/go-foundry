package main

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestSyncDirectionAlwaysUnit(t *testing.T) {
	cases := []struct{ yaw, pitch float32 }{
		{0, 0}, {45, -50}, {-720, 0}, {90, -90}, {180, 90}, {37.5, -12.25},
	}
	for _, c := range cases {
		dl := DirectionalLight{Yaw: c.yaw, Pitch: c.pitch}
		dl.SyncDirection()
		l := dl.Direction.Len()
		if math.Abs(float64(l)-1) > 1e-5 {
			t.Errorf("yaw=%v pitch=%v: direction %v has length %v, want 1", c.yaw, c.pitch, dl.Direction, l)
		}
	}
}

func TestSyncDirectionClampsPitch(t *testing.T) {
	dl := DirectionalLight{Pitch: -140}
	dl.SyncDirection()
	if dl.Pitch != -90 {
		t.Errorf("pitch clamped to %v, want -90", dl.Pitch)
	}
	if dl.Direction.Y() > -0.999 {
		t.Errorf("pitch -90 should point straight down, got %v", dl.Direction)
	}
}

func TestDirLightYawPitchRoundTrip(t *testing.T) {
	cases := []struct{ yaw, pitch float32 }{
		{0, 0}, {45, -50}, {135, 30}, {-90, -12}, {179, 89},
	}
	for _, c := range cases {
		dl := DirectionalLight{Yaw: c.yaw, Pitch: c.pitch}
		dl.SyncDirection()
		yaw, pitch := DirLightYawPitch(dl.Direction)
		if math.Abs(float64(yaw-c.yaw)) > 0.01 || math.Abs(float64(pitch-c.pitch)) > 0.01 {
			t.Errorf("round trip of yaw=%v pitch=%v gave yaw=%v pitch=%v", c.yaw, c.pitch, yaw, pitch)
		}
	}
}

// A zero-length or NaN direction — what the old vector editor produced when a
// component was dragged through zero — must fall back to the defaults instead
// of propagating into the shader uniform and shadow matrix.
func TestDirLightYawPitchRejectsDegenerateVectors(t *testing.T) {
	nan := float32(math.NaN())
	inf := float32(math.Inf(1))
	for _, d := range []mgl32.Vec3{{0, 0, 0}, {nan, nan, nan}, {0, nan, 0}, {inf, 0, 0}} {
		yaw, pitch := DirLightYawPitch(d)
		if yaw != DefaultDirLightYaw || pitch != DefaultDirLightPitch {
			t.Errorf("DirLightYawPitch(%v) = (%v, %v), want defaults", d, yaw, pitch)
		}
	}
}

// Levels authored before yaw/pitch existed carry only a direction vector; the
// loader must derive angles that reproduce it.
func TestLoadLegacyDirLightDerivesAngles(t *testing.T) {
	var raw RawLevel
	rd := RawDirLight{Brightness: 1}
	rd.Direction.X = -0.5
	rd.Direction.Y = -0.7
	rd.Direction.Z = 0.3
	raw.Lights.Directional = []RawDirLight{rd}

	lvl := buildLevelLights(t, raw)
	dl := lvl.Lights.Directional[0]
	want := mgl32.Vec3{-0.5, -0.7, 0.3}.Normalize()
	if dl.Direction.Sub(want).Len() > 1e-5 {
		t.Errorf("derived direction %v, want %v (from angles yaw=%v pitch=%v)", dl.Direction, want, dl.Yaw, dl.Pitch)
	}
}

// A NaN direction persisted by an older build must load as a usable light.
func TestLoadNaNDirLightFallsBackToDefaults(t *testing.T) {
	var raw RawLevel
	nan := float32(math.NaN())
	rd := RawDirLight{Brightness: 1}
	rd.Direction.X, rd.Direction.Y, rd.Direction.Z = nan, nan, nan
	raw.Lights.Directional = []RawDirLight{rd}

	lvl := buildLevelLights(t, raw)
	dl := lvl.Lights.Directional[0]
	if dl.Yaw != DefaultDirLightYaw || dl.Pitch != DefaultDirLightPitch {
		t.Fatalf("angles = (%v, %v), want defaults", dl.Yaw, dl.Pitch)
	}
	if math.Abs(float64(dl.Direction.Len())-1) > 1e-5 {
		t.Errorf("direction %v is not a unit vector", dl.Direction)
	}
}

// Saved angles win over the direction vector written alongside them, so a
// straight-down light keeps the yaw it was authored with.
func TestSaveLoadPreservesAnglesAtVerticalPitch(t *testing.T) {
	src := DirectionalLight{Yaw: 123, Pitch: -90, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1}
	src.SyncDirection()

	raw := RawLevel{Lights: lightsToRaw(LightSet{Directional: []DirectionalLight{src}})}
	lvl := buildLevelLights(t, raw)
	dl := lvl.Lights.Directional[0]
	if dl.Yaw != 123 || dl.Pitch != -90 {
		t.Errorf("round trip gave yaw=%v pitch=%v, want 123/-90", dl.Yaw, dl.Pitch)
	}
}

// buildLevelLights converts raw lights through the normal load path (the GPU-free
// half of rawToLevel).
func buildLevelLights(t *testing.T, raw RawLevel) *Level {
	t.Helper()
	lvl := &Level{Lights: rawToLightSet(raw.Lights)}
	if len(lvl.Lights.Directional) != len(raw.Lights.Directional) {
		t.Fatalf("got %d directional lights, want %d", len(lvl.Lights.Directional), len(raw.Lights.Directional))
	}
	return lvl
}
