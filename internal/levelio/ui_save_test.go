package levelio

// ui_save_test.go — the level's UI screen list through the YAML boundary.
//
// The same two assertions every other level-scoped list gets: what was set must
// survive a round trip, and a level that names no screens must not grow a key.

import (
	"reflect"
	"strings"
	"testing"

	"foundry/internal/world"

	"gopkg.in/yaml.v2"
)

func TestLevelUIListRoundTrips(t *testing.T) {
	lvl := world.Level{UI: []string{"hud", "compass"}, Scripts: []string{"rules.flang"}}

	raw, err := LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("to raw: %v", err)
	}
	if !reflect.DeepEqual(raw.UI, []string{"hud", "compass"}) {
		t.Fatalf("raw.UI is %v", raw.UI)
	}

	var got world.Level
	if err := ApplyRawLevel(raw, &got); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !reflect.DeepEqual(got.UI, lvl.UI) {
		t.Errorf("UI list came back as %v, want %v", got.UI, lvl.UI)
	}
	if !reflect.DeepEqual(got.Scripts, lvl.Scripts) {
		t.Errorf("the scripts list was disturbed: %v", got.Scripts)
	}
}

func TestLevelWithNoScreensGainsNoUIKey(t *testing.T) {
	raw, err := LevelToRaw(&world.Level{})
	if err != nil {
		t.Fatalf("to raw: %v", err)
	}
	data, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "ui:") {
		t.Errorf("a level with no screens wrote a ui key:\n%s", data)
	}
}
