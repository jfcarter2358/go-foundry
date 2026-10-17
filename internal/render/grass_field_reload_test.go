package render

// grass_field_reload_test.go — a rewritten field is forgotten (and reloaded
// on the next sync); the others stay resident.

import (
	"testing"

	"foundry/internal/world"
)

func TestReloadGrassFieldsForgetsOnlyThoseNamed(t *testing.T) {
	saved := grassFields
	t.Cleanup(func() { grassFields = saved })
	lvl := &world.Level{}
	grassFields.fields = map[string]*grassFieldGPU{
		"a.fgrass": {path: "a.fgrass"}, "b.fgrass": {path: "b.fgrass"},
	}
	grassFields.lvl = lvl
	ReloadGrassFields([]string{"a.fgrass", "missing.fgrass"})
	if _, ok := grassFields.fields["a.fgrass"]; ok {
		t.Error("the rewritten field is still resident")
	}
	if _, ok := grassFields.fields["b.fgrass"]; !ok {
		t.Error("a field nobody rewrote was dropped")
	}
	if grassFields.lvl != nil {
		t.Error("the next sync keeps its per-frame load budget: a stroke's tiles would come back over several frames")
	}
}
