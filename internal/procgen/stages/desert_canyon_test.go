package stages

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/testenv"
)

// The template is a valid document wiring every stage the canyon project
// made, and resources/generators/desert_canyon.fgen is exactly the template
// (so the two cannot drift: regenerate it with
// `foundry gen new desert_canyon -template desert_canyon -force` in the repo).
func TestDesertCanyonTemplateIsShippedAndValid(t *testing.T) {
	d, err := procgen.NewFromTemplate(DesertCanyonTemplate, "desert_canyon")
	if err != nil {
		t.Fatal(err)
	}
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("template problems: %v", probs)
	}
	kinds := map[string]bool{}
	for _, s := range d.Stages {
		kinds[s.Kind] = true
	}
	for _, k := range []string{PolyHavenTexturesKind, TerrainKind, TerrainMaterialKind, PolyHavenRocksKind, PolyHavenPlantsKind,
		CliffWallKind, ScatterKind, VegetationKind, EnvironmentKind} {
		if !kinds[k] {
			t.Errorf("the template has no %s stage", k)
		}
	}
	want, err := procgen.EncodeDoc(d)
	if err != nil {
		t.Fatal(err)
	}
	have, err := os.ReadFile(filepath.Join(testenv.RepoRoot(t), "resources", "generators", "desert_canyon.fgen"))
	if err != nil {
		t.Fatal(err)
	}
	if string(have) != string(want) {
		t.Error("resources/generators/desert_canyon.fgen is not the template's document: regenerate it")
	}
}
