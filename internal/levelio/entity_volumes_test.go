package levelio

// entity_volumes_test.go — the wiring a placement binds on a volume its entity's
// type carries, from the live level volume through a save and back.
//
// The type cannot bind a `ref` param that names its own placement — only the
// level knows the ship is called "ship" — so the binding is made on the
// materialised volume. That volume is skipped on save, which used to mean the
// binding was too: the editor kept it in memory, and a compiled release loaded
// the level from disk with the param unbound.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/scriptdata"
	"foundry/internal/world"
)

func TestSavingKeepsParamsBoundOnACarriedVolume(t *testing.T) {
	root := withInstallRoot(t)
	if err := os.MkdirAll(filepath.Join(root, world.EntitiesDirName), 0o755); err != nil {
		t.Fatalf("mkdir entities: %v", err)
	}
	const def = `
id: ship
volumes:
  - name: trigger
    script: scripts/dropship_trigger.flang
`
	if err := os.WriteFile(filepath.Join(root, world.EntitiesDirName, "ship.yaml"), []byte(def), 0o644); err != nil {
		t.Fatalf("write definition: %v", err)
	}
	world.ReloadEntityDefs()
	t.Cleanup(world.ReloadEntityDefs)

	lvl, err := rawToLevel(world.RawLevel{Entities: []world.RawEntity{{Type: "ship", ID: "ship"}}})
	if err != nil {
		t.Fatalf("rawToLevel: %v", err)
	}
	trigger := func(l *world.Level) *world.Volume {
		t.Helper()
		for i := range l.Volumes {
			if l.Volumes[i].ID == "ship.trigger" {
				return &l.Volumes[i]
			}
		}
		t.Fatalf("no ship.trigger among %d volumes", len(l.Volumes))
		return nil
	}

	// Untouched, the placement still restates nothing of its type's.
	back, err := LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	if len(back.Entities) != 1 || len(back.Entities[0].Volumes) != 0 {
		t.Fatalf("an untouched placement wrote volume overrides: %+v", back.Entities)
	}

	// What the level editor's script panel does.
	v := trigger(&lvl)
	if v.ScriptParams == nil {
		v.ScriptParams = scriptdata.AttrSet{}
	}
	v.ScriptParams["ship"] = scriptdata.StringAttr("ship")

	back, err = LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	if len(back.Volumes) != 0 {
		t.Errorf("saving wrote the carried volume itself (%d), which would load twice", len(back.Volumes))
	}
	if len(back.Entities) != 1 || len(back.Entities[0].Volumes) != 1 {
		t.Fatalf("the binding was not written as a placement override: %+v", back.Entities)
	}
	if o := back.Entities[0].Volumes[0]; o.Name != "trigger" || o.Script != "" {
		t.Errorf("override should name the volume and leave the type's script alone: %+v", o)
	}

	// And it comes back bound, which is what the compiled level loads.
	again, err := rawToLevel(back)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := trigger(&again).ScriptParams.Attr("ship")
	if !ok || got.S != "ship" {
		t.Errorf("reloaded param ship = %+v (bound %v), want the ID \"ship\"", got, ok)
	}
	if s := trigger(&again).Script; s != "scripts/dropship_trigger.flang" {
		t.Errorf("reloaded script %q, want the type's", s)
	}
}
