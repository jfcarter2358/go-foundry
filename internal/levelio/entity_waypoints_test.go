package levelio

// entity_waypoints_test.go — a type's paths through a load, a save and a reload.
//
// A carried path is the type's content, materialised per placement: the level
// must hold one live copy after every load and write none, or a reload — which
// is what a compiled release does — would hold two.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/world"

	"gopkg.in/yaml.v2"
)

func TestAnEntitysPathsAreMaterialisedAndNeverSaved(t *testing.T) {
	root := withInstallRoot(t)
	if err := os.MkdirAll(filepath.Join(root, world.EntitiesDirName), 0o755); err != nil {
		t.Fatalf("mkdir entities: %v", err)
	}
	def, err := yaml.Marshal(world.RawEntityDef{ID: "ship", WaypointAnimations: []world.RawWaypointAnimation{{
		ID: "door_close", Length: 2,
		Points: []world.RawWaypoint{{Position: world.RawVec3{Y: 1}, Time: 2}},
	}}})
	if err != nil {
		t.Fatalf("marshal definition: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, world.EntitiesDirName, "ship.yaml"), def, 0o644); err != nil {
		t.Fatalf("write definition: %v", err)
	}
	world.ReloadEntityDefs()
	t.Cleanup(world.ReloadEntityDefs)

	count := func(l *world.Level, id string) int {
		n := 0
		for i := range l.WaypointAnimations {
			if l.WaypointAnimations[i].ID == id {
				n++
			}
		}
		return n
	}

	lvl, err := rawToLevel(world.RawLevel{
		Entities:           []world.RawEntity{{Type: "ship", ID: "ship"}},
		WaypointAnimations: []world.RawWaypointAnimation{{ID: "lift", Points: []world.RawWaypoint{{}}}},
	})
	if err != nil {
		t.Fatalf("rawToLevel: %v", err)
	}
	a := world.FindWaypointAnimation(&lvl, "ship.door_close")
	if a == nil || a.FromEntity != "ship" {
		t.Fatalf("no ship.door_close riding the ship among %+v", lvl.WaypointAnimations)
	}

	back, err := LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	if len(back.WaypointAnimations) != 1 || back.WaypointAnimations[0].ID != "lift" {
		t.Errorf("saving wrote %+v; want only the level's own lift", back.WaypointAnimations)
	}
	if len(back.Entities) != 1 || len(back.Entities[0].WaypointAnimations) != 0 {
		t.Errorf("the placement restated its type's paths: %+v", back.Entities)
	}

	again, err := rawToLevel(back)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if n := count(&again, "ship.door_close"); n != 1 {
		t.Errorf("after a save and reload the level holds %d ship.door_close, want 1", n)
	}
}
