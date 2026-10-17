package stages

import (
	"strings"
	"testing"

	"foundry/internal/procgen"
)

// The presets whose grass is on the grass instancer (docs/regions/
// PRESETS_2B.md): each has a valid "sward" grass_field stage, every rule of
// it names one of the preset's own polyhaven_plants stages, and no scatter
// rule still places a Poly Haven grass set as scenery.
func TestPresetGrassIsOnTheInstancer(t *testing.T) {
	presets := map[string]func() *procgen.Doc{
		"tallgrass_prairie": TallgrassPrairiePreset, "oak_savannah": OakSavannahPreset, "savannah": SavannahPreset,
		"wetland": WetlandPreset, "coast": CoastPreset, "tundra": TundraPreset, "alpine": AlpinePreset,
		"pine_forest": PineForestPreset,
	}
	for name, mk := range presets {
		d := mk()
		if probs := d.Validate(); len(probs) > 0 {
			t.Errorf("%s: %v", name, probs)
		}
		sw := d.StageByID("sward")
		if sw == nil || sw.Kind != GrassFieldKind {
			t.Errorf("%s: no grass_field stage", name)
			continue
		}
		rules := sw.Params.Items("rules")
		if len(rules) == 0 {
			t.Errorf("%s: the sward has no rules", name)
		}
		for _, it := range rules {
			src := d.StageByID(it.Str("plants"))
			if src == nil || src.Kind != PolyHavenPlantsKind {
				t.Errorf("%s: rule %s names %q, not a plants stage", name, it.Str("name"), it.Str("plants"))
			}
		}
		grassStages := map[string]bool{}
		for _, st := range d.Stages {
			if st.Kind != PolyHavenPlantsKind {
				continue
			}
			for _, a := range st.Params.Items("assets") {
				if strings.HasPrefix(a.Str("id"), "grass_medium") {
					grassStages[st.ID] = true
				}
			}
		}
		for _, st := range d.Stages {
			if st.Kind != ScatterKind {
				continue
			}
			for _, it := range st.Params.Items("rules") {
				if grassStages[it.Str("rocks")] {
					t.Errorf("%s: scatter %s still places grass rule %s as scenery", name, st.ID, it.Str("name"))
				}
			}
		}
	}
}
