package script

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/world"
)

// A name the level does not have is remembered as missing for the tick — and
// forgotten the moment something takes it, in the same handler.
func TestAMissingNameIsFoundOnceSomethingTakesIt(t *testing.T) {
	root := withTempScriptDir(t)
	maker := writeScript(t, root, "maker.flang", `def on_tick(:tick:, dt float) -> null
    if @crate == null
        @self.attr.misses += 1
    end
    if @self.attr.misses == 1
        spawn("crate", <0.0, 0.0, 0.0>)
    end
    if @crate != null
        @self.attr.found += 1
    end
end`)
	dir := filepath.Join(fileutil.ResourcesPath, world.EntitiesDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "crate.yaml"), []byte("id: crate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	world.ReloadEntityDefs()
	t.Cleanup(world.ReloadEntityDefs)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{ID: "maker", Script: maker,
		Attributes: world.AttrSet{"misses": world.IntAttr(0), "found": world.IntAttr(0)}})
	runTicks(t, lvl, 3)
	misses, _ := lvl.Entities[0].Attr("misses")
	found, _ := lvl.Entities[0].Attr("found")
	if misses.I != 1 || found.I != 3 {
		t.Fatalf("misses %d, found %d; want 1 and 3 — the spawned crate must be found in the tick it was made", misses.I, found.I)
	}
}
