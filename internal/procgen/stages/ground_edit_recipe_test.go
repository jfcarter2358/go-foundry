package stages

// ground_edit_recipe_test.go — the pond, lake and marsh presets' recipes
// slot a ground_edit in between the terrain and its readers, add their
// water layers to it, and are idempotent.

import (
	"strings"
	"testing"

	"foundry/internal/procgen"
)

func TestWaterPresetRecipesSlotInAGroundEdit(t *testing.T) {
	ps := shippedPresets(t)
	d := freshDoc()
	d.Stages = append(d.Stages,
		procgen.Stage{Kind: RocksKind, ID: "boulders", Params: procgen.Params{"preset": procgen.Enum("boulder")}},
		procgen.Stage{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": procgen.StageRef("ground"),
			"rules": procgen.List(procgen.Params{"name": procgen.String("r"), "rocks": procgen.StageRef("boulders")})}})
	var all []string
	for _, id := range []string{"pond", "marsh", "lake"} {
		p := FindPreset(ps, id)
		if p == nil {
			t.Fatalf("no %s preset", id)
		}
		notes, err := ApplyRecipe(d, p)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, notes...)
		if probs := d.Validate(); len(probs) > 0 {
			t.Fatalf("%s left the document invalid: %v\nnotes %v", id, probs, notes)
		}
	}
	ge := d.StageByID("ground_edit")
	if ge == nil || ge.Kind != GroundEditKind || ge.Params.Str("terrain") != "ground" {
		t.Fatalf("ground_edit stage %+v\nnotes %v", ge, all)
	}
	var layers []string
	for _, it := range ge.Params["water"].Items {
		layers = append(layers, it.Str("layer")+":"+it.Str("style"))
	}
	if strings.Join(layers, " ") != "pond:pond marsh:marsh lake:lake" {
		t.Errorf("water layers %v", layers)
	}
	// Every reader of the terrain now stands on the edited ground, and the
	// ground_edit comes before all of them.
	geAt := -1
	for i, s := range d.Stages {
		if s.ID == "ground_edit" {
			geAt = i
		}
	}
	for i, s := range d.Stages {
		if s.ID == "ground_edit" {
			continue
		}
		if r := s.Params.Str("terrain"); r != "" {
			if r != "ground_edit" {
				t.Errorf("%s still reads %s", s.ID, r)
			}
			if i < geAt {
				t.Errorf("%s (at %d) runs before the ground_edit (at %d)", s.ID, i, geAt)
			}
		}
	}
	// The wet slot wears the mud, the water layers paint it, rocks keep out
	// of the pond and lake, and the reeds stand on the shore.
	mat := d.StageByID("ground_mat")
	if mat.Params.Str("wet") != "pond_mud" {
		t.Errorf("wet slot %q", mat.Params.Str("wet"))
	}
	if n := len(mat.Params["paint"].Items); n != 3 {
		t.Errorf("%d paint items on the material", n)
	}
	sc := d.StageByID("scatter")
	var ex []string
	for _, it := range sc.Params["exclude"].Items {
		ex = append(ex, it.Str("layer"))
	}
	if strings.Join(ex, " ") != "pond lake" {
		t.Errorf("scatter excludes %v", ex)
	}
	cover := d.StageByID("cover")
	reeds := false
	for _, it := range cover.Params["rules"].Items {
		if it.Str("name") == "shore_reeds" && it.Vec2("shore")[1] > 0 && it.Float("max_depth") > 0 {
			reeds = true
		}
	}
	if !reeds || cover.Params.Str("terrain") != "ground_edit" {
		t.Errorf("cover %+v", cover.Params)
	}
	// Idempotent.
	for _, id := range []string{"pond", "marsh", "lake"} {
		notes, err := ApplyRecipe(d, FindPreset(ps, id))
		if err != nil || len(notes) != 0 {
			t.Errorf("%s applied twice: %v %v", id, notes, err)
		}
	}
}
