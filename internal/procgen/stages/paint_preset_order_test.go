package stages

// paint_preset_order_test.go — a recipe's stages land in an order every
// reference agrees with, whatever was applied first (phase W5 found the
// rock path's stones stage appended below an existing ground_edit, which
// the run refused).

import (
	"testing"

	"foundry/internal/procgen"
)

// stageOrderOK fails the test where a stage names one that runs after it.
func stageOrderOK(t *testing.T, what string, d *procgen.Doc) {
	t.Helper()
	pos := map[string]int{}
	for i := range d.Stages {
		pos[d.Stages[i].ID] = i
	}
	for i := range d.Stages {
		k := procgen.KindByName(d.Stages[i].Kind)
		if k == nil {
			continue
		}
		for _, ref := range procgen.StageRefs(k.Params, procgen.Resolve(k.Params, d.Stages[i].Params)) {
			if j, ok := pos[ref]; ok && j > i {
				t.Errorf("%s: %s names %s, which runs after it", what, d.Stages[i].ID, ref)
			}
		}
	}
	if probs := d.Validate(); len(probs) > 0 {
		t.Errorf("%s: invalid: %v", what, probs)
	}
}

func TestRockPathOverAnExistingGroundEditKeepsTheOrder(t *testing.T) {
	ps := shippedPresets(t)
	river, path, pond := FindPreset(ps, "river"), FindPreset(ps, "rock_path"), FindPreset(ps, "pond")
	if river == nil || path == nil || pond == nil {
		t.Fatal("missing a shipped preset")
	}
	rstroke := [][2]float64{{-60, -40}, {-20, -30}, {20, -32}, {60, -20}}
	pstroke := [][2]float64{{-10, -70}, {-5, -30}, {0, 10}}
	steps := map[string]func(d *procgen.Doc) error{
		"river": func(d *procgen.Doc) error { _, _, _, err := AddRiverStroke(d, river, rstroke, 3); return err },
		"path":  func(d *procgen.Doc) error { _, _, _, err := AddPathStroke(d, path, pstroke, 1.5); return err },
		"pond":  func(d *procgen.Doc) error { _, err := ApplyRecipe(d, pond); return err },
	}
	for _, order := range [][]string{{"river", "path"}, {"path", "river"}, {"pond", "path"}, {"pond", "river", "path"}} {
		d := freshDoc()
		name := ""
		for _, s := range order {
			name += s + ">"
			if err := steps[s](d); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		stageOrderOK(t, name, d)
		ge := firstStageOf(d, GroundEditKind)
		if ge == nil || len(ge.Params["paths"].Items) == 0 || ge.Params["paths"].Items[0].Str("rocks") == "" {
			t.Fatalf("%s: no path with rocks in the ground_edit", name)
		}
	}
	// The hoist never loops on a cycle (which validation reports).
	d := freshDoc()
	d.Stages = append(d.Stages, procgen.Stage{Kind: GroundEditKind, ID: "a", Params: procgen.Params{"terrain": procgen.StageRef("b")}},
		procgen.Stage{Kind: GroundEditKind, ID: "b", Params: procgen.Params{"terrain": procgen.StageRef("a")}})
	OrderStagesByRefs(d)
}
