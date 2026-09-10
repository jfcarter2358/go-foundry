package levelio

// spawn_save_test.go — the spawn's control and gravity switches, all the way
// through YAML. Both directions of the loader have to carry them, or a spawn
// ticked off in the editor walks and falls again on the next load.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"foundry/internal/world"
)

func TestASpawnsPlayerSwitchesSurviveSaveAndLoad(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	// Loading the spawn loads its marker's texture. Another test in the package
	// usually registers it first; run alone, nothing has.
	if _, ok := gfx.Textures["default"]; !ok {
		gfx.Textures["default"] = 1
		t.Cleanup(func() { delete(gfx.Textures, "default") })
	}

	lvl, _ := NewBlankLevel()
	lvl.Spawn = &world.SpawnPoint{DisableControl: true, DisableGravity: true}

	path := filepath.Join(t.TempDir(), "spawn_level.yaml")
	if err := SaveLevel(&lvl, path); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"disable_control: true", "disable_gravity: true"} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("the saved level does not mention %q:\n%s", key, data)
		}
	}

	back, err := LoadLevel(path)
	if err != nil {
		t.Fatalf("LoadLevel: %v", err)
	}
	if back.Spawn == nil {
		t.Fatal("the spawn did not come back")
	}
	if !back.Spawn.DisableControl || !back.Spawn.DisableGravity {
		t.Errorf("came back as control off %v, gravity off %v, want both off",
			back.Spawn.DisableControl, back.Spawn.DisableGravity)
	}
}

// A spawn that leaves both on writes neither key, so every level saved before
// they existed opens and saves unchanged.
func TestAnOrdinarySpawnWritesNoPlayerSwitches(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	lvl, _ := NewBlankLevel()
	lvl.Spawn = &world.SpawnPoint{}

	path := filepath.Join(t.TempDir(), "plain_spawn_level.yaml")
	if err := SaveLevel(&lvl, path); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"disable_control", "disable_gravity"} {
		if strings.Contains(string(data), key) {
			t.Errorf("an ordinary spawn gained %q:\n%s", key, data)
		}
	}
}
