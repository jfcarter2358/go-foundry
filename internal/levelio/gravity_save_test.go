package levelio

// gravity_save_test.go — the gravity keys, all the way through YAML.
//
// The behaviour of the model lives in internal/world (gravity_test.go); this is
// the assertion that cannot make: that each key is wired into BOTH directions
// of the loader. A `gravity:` written by LevelToRaw and never read by
// loadRawLevel passes every test over there and is still lost on the next load.
//
// Zero is what each of these is tested with on purpose. It is the value the
// pointer representation exists for — a zero-g level is a thing people will
// actually ask for — and it is the one an `omitempty` on a plain float would
// silently drop.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestZeroLevelGravitySurvivesSaveAndLoad(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	lvl, _ := NewBlankLevel()
	zero := float32(0)
	lvl.Gravity = &zero

	path := filepath.Join(t.TempDir(), "zero_g.yaml")
	if err := SaveLevel(&lvl, path); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "gravity:") {
		t.Fatalf("the saved level does not mention gravity:\n%s", data)
	}

	back, err := LoadLevel(path)
	if err != nil {
		t.Fatalf("LoadLevel: %v", err)
	}
	if back.Gravity == nil {
		t.Fatal("zero-g came back as a level that never set a gravity")
	}
	if *back.Gravity != 0 {
		t.Errorf("gravity came back as %v, want 0", *back.Gravity)
	}
	if got := world.LevelGravity(&back); got != 0 {
		t.Errorf("the loaded level falls at %v, want 0", got)
	}
}

// A level that never set one writes no key, so every level authored before this
// existed opens and saves unchanged — and loads at the engine's own rate.
func TestALevelWithNoGravityKeyWritesNone(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	lvl, _ := NewBlankLevel()
	path := filepath.Join(t.TempDir(), "plain.yaml")
	if err := SaveLevel(&lvl, path); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "gravity:") {
		t.Errorf("a level that never set a gravity wrote one:\n%s", data)
	}

	back, err := LoadLevel(path)
	if err != nil {
		t.Fatalf("LoadLevel: %v", err)
	}
	if back.Gravity != nil {
		t.Errorf("it came back carrying a gravity of %v", *back.Gravity)
	}
	if got := world.LevelGravity(&back); got != world.DefaultGravity {
		t.Errorf("it falls at %v, want the engine's %v", got, world.DefaultGravity)
	}
}

func TestVolumeGravitySurvivesSaveAndLoad(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	lvl, _ := NewBlankLevel()
	zero := float32(0)
	lvl.Volumes = append(lvl.Volumes, world.Volume{
		ID: "chamber", Enabled: true, Position: mgl32.Vec3{1, 2, 3},
		HalfX: 2, HalfY: 2, HalfZ: 2, Gravity: &zero,
	})

	path := filepath.Join(t.TempDir(), "chamber.yaml")
	if err := SaveLevel(&lvl, path); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}
	back, err := LoadLevel(path)
	if err != nil {
		t.Fatalf("LoadLevel: %v", err)
	}
	if len(back.Volumes) != 1 {
		t.Fatalf("got %d volumes back, want 1", len(back.Volumes))
	}
	if g := back.Volumes[0].Gravity; g == nil || *g != 0 {
		t.Fatalf("the chamber's gravity came back as %v, want 0", g)
	}
	// And the saved pointer is the level's own, not one shared with the Raw
	// form it was written through — see world.CopyFloatPtr.
	if back.Volumes[0].Gravity == lvl.Volumes[0].Gravity {
		t.Error("the loaded volume shares its gravity pointer with the one that was saved")
	}
}

func TestPlayerGravityKnobsSurviveSaveAndLoad(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	lvl, _ := NewBlankLevel()
	lvl.Player.GravityScale = 0.25
	lvl.Player.MaxFallSpeed = 12

	path := filepath.Join(t.TempDir(), "player_gravity.yaml")
	if err := SaveLevel(&lvl, path); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"player:", "gravity_scale:", "max_fall_speed:"} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("the saved level does not mention %q:\n%s", key, data)
		}
	}

	back, err := LoadLevel(path)
	if err != nil {
		t.Fatalf("LoadLevel: %v", err)
	}
	if back.Player.GravityScale != 0.25 || back.Player.MaxFallSpeed != 12 {
		t.Errorf("the player block came back as scale %v cap %v, want 0.25 and 12",
			back.Player.GravityScale, back.Player.MaxFallSpeed)
	}
}
