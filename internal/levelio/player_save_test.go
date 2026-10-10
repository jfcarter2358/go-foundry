package levelio

// player_save_test.go — the level's `player:` block, all the way through YAML.
//
// The round trip of the type itself lives in internal/world
// (player_settings_test.go); this is the assertion that cannot make, that the
// key is wired into BOTH directions of the loader. A step height written by
// LevelToRaw and never read by loadRawLevel would pass every test over there
// and still be lost on the next load.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/testenv"
)

func TestALevelStepHeightSurvivesSaveAndLoad(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	lvl, _ := NewBlankLevel()
	lvl.Player.StepHeight = 0.5

	path := filepath.Join(t.TempDir(), "player_level.yaml")
	if err := SaveLevel(&lvl, path); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"player:", "step_height:"} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("the saved level does not mention %q:\n%s", key, data)
		}
	}

	back, err := LoadLevel(path)
	if err != nil {
		t.Fatalf("LoadLevel: %v", err)
	}
	if got := back.Player.StepHeight; got != 0.5 {
		t.Errorf("step height came back as %v, want 0.5", got)
	}
}

// A level that never set one writes no block, so every level authored before
// this existed opens and saves unchanged.
func TestALevelWithNoPlayerBlockWritesNone(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	lvl, _ := NewBlankLevel()

	path := filepath.Join(t.TempDir(), "plain_level.yaml")
	if err := SaveLevel(&lvl, path); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "player:") {
		t.Errorf("a level that set nothing gained a player block:\n%s", data)
	}

	back, err := LoadLevel(path)
	if err != nil {
		t.Fatalf("LoadLevel: %v", err)
	}
	if got := back.Player.StepHeight; got != 0 {
		t.Errorf("step height came back as %v, want 0 (follow the default)", got)
	}
}
