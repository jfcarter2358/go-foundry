package editor

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// adoptTestLevel puts a typeless entity in a fresh level open in the editor,
// with a scratch resources directory to save types into, and a document seeded
// from it the way Make Type seeds one.
func adoptTestLevel(t *testing.T) *world.Level {
	t.Helper()
	entityTestDoc(t)
	prevRes, prevLvl, prevSrc := fileutil.ResourcesPath, Editor.level, entityDefSource
	t.Cleanup(func() {
		fileutil.ResourcesPath, Editor.level, entityDefSource = prevRes, prevLvl, prevSrc
		world.ReloadEntityDefs()
	})
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "entities"), 0o755); err != nil {
		t.Fatal(err)
	}
	fileutil.ResourcesPath = dir
	world.ReloadEntityDefs()

	lvl := &world.Level{Entities: []world.Entity{{
		ID: "crate_2", Position: mgl32.Vec3{3, 0, -4}, Rotation: mgl32.Vec3{0, 90, 0},
		Scale: mgl32.Vec3{1, 1, 1}, Tags: []string{"loot"}, Visible: true,
		Script: "scripts/crate.flang",
	}}}
	Editor.level = lvl
	e := &lvl.Entities[0]
	entityDef = &world.EntityDef{
		Name: "crate", Tags: append([]string(nil), e.Tags...), Script: e.Script,
		Visible: e.Visible, Collision: e.Collision,
	}
	entityDefSource.level, entityDefSource.id = lvl, e.ID
	return lvl
}

// Saving a type made from a placed entity turns that entity into an instance of
// it, standing where it stood and carrying nothing of its own.
func TestSavingAMadeTypeAdoptsTheEntityItCameFrom(t *testing.T) {
	lvl := adoptTestLevel(t)

	entitySaveDefAsFrom("crate", nil)

	e := &lvl.Entities[0]
	if e.Type != "crate" {
		t.Fatalf("after the save the placed entity has type %q; want crate", e.Type)
	}
	if e.ID != "crate_2" || e.Position != (mgl32.Vec3{3, 0, -4}) || e.Rotation != (mgl32.Vec3{0, 90, 0}) {
		t.Errorf("the placement moved: id %q at %v rot %v", e.ID, e.Position, e.Rotation)
	}
	def, err := world.LoadEntityDef("crate")
	if err != nil {
		t.Fatal(err)
	}
	r := world.EntityToRaw(e, def)
	if r.Script != nil || len(r.Tags) != 0 || r.Visible != nil {
		t.Errorf("the adopted entity overrides its new type: script %v tags %v visible %v", r.Script, r.Tags, r.Visible)
	}
	if entityDefSource.id != "" {
		t.Errorf("the source %q outlived the save that adopted it", entityDefSource.id)
	}
}

// The source belongs to the level it was picked in; a save after the level has
// been swapped out leaves whatever shares its ID alone.
func TestSavingAMadeTypeIgnoresASourceFromAnotherLevel(t *testing.T) {
	lvl := adoptTestLevel(t)
	other := &world.Level{Entities: []world.Entity{lvl.Entities[0]}}
	Editor.level = other

	entitySaveDefAsFrom("crate", nil)

	if got := other.Entities[0].Type; got != "" {
		t.Errorf("an entity in a different level became a %q", got)
	}
}
