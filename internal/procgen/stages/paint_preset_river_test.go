package stages

// paint_preset_river_test.go — the river brushes (paint_preset_river.go,
// docs/terrain-paint/PHASE_W3.md): a stroke becomes a simplified river
// spline in the ground_edit the recipe slots in, the dressing lands (mask
// entries in the material, the boulders' rocks stage above the ground_edit,
// the readers rewired), and a second stroke only adds a river.

import (
	"math"
	"testing"

)

func TestRiverBrushStrokeBecomesASpline(t *testing.T) {
	ps := shippedPresets(t)
	p := FindPreset(ps, "river")
	if p == nil || p.River == nil {
		t.Fatal("no river brush shipped")
	}
	d := freshDoc()
	if !NeedsRecipe(d, p) {
		t.Fatal("a doc without a ground_edit needs the river recipe")
	}
	// A wobbly drag along x: 41 points, a centimetre of jitter.
	var pts [][2]float64
	for i := 0; i <= 40; i++ {
		x := -60 + 3*float64(i)
		pts = append(pts, [2]float64{x, 10*math.Sin(x/40) + 0.01*float64(i%3)})
	}
	ge, idx, notes, err := AddRiverStroke(d, p, pts, 3)
	if err != nil {
		t.Fatalf("%v\n%v", err, notes)
	}
	st := d.StageByID(ge)
	if st == nil || st.Kind != GroundEditKind || idx != 0 {
		t.Fatalf("ground_edit %q item %d", ge, idx)
	}
	it := st.Params["rivers"].Items[0]
	path := it.Points("path")
	if len(path) < 3 || len(path) > 15 {
		t.Fatalf("simplified to %d points", len(path))
	}
	for _, q := range path {
		if q[2] != 6 || q[3] != 1.3 {
			t.Fatalf("point %v: want width 6 (the brush's diameter) and depth 1.3", q)
		}
	}
	if it.Str("material") != "water_river" || it.Float("pools") != 0.65 || it.Str("name") != "river_1" {
		t.Fatalf("item %v", it)
	}
	if st.Params.Str("river_rocks") != "river_rocks_src" {
		t.Fatalf("river_rocks %q", st.Params.Str("river_rocks"))
	}
	// The rocks stage sits above the ground_edit that reads it; the
	// material stands on the ground_edit and paints the bed and bars.
	pos := map[string]int{}
	for i, s := range d.Stages {
		pos[s.ID] = i
	}
	if pos["river_rocks_src"] > pos[ge] {
		t.Fatalf("river_rocks_src after %s: %v", ge, pos)
	}
	mat := d.StageByID("ground_mat")
	if mat.Params.Str("terrain") != ge {
		t.Fatalf("material stands on %q", mat.Params.Str("terrain"))
	}
	masks := map[string]string{}
	for _, it := range mat.Params["paint"].Items {
		masks[it.Str("mask")] = it.Str("target")
	}
	if masks["river_bed"] == "" || mat.Params.Str("wet") != "pond_mud" {
		t.Fatalf("material paint %v, wet %q", mat.Params["paint"].Items, mat.Params.Str("wet"))
	}
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("invalid: %v", probs)
	}
	// A second stroke adds a river and nothing else.
	n := len(d.Stages)
	_, idx2, notes2, err := AddRiverStroke(d, p, [][2]float64{{0, -50}, {10, -10}, {5, 8}}, 2)
	if err != nil || idx2 != 1 || len(notes2) != 0 || len(d.Stages) != n {
		t.Fatalf("second stroke: idx %d notes %v err %v stages %d->%d", idx2, notes2, err, n, len(d.Stages))
	}
	// Too short a drag is not a river.
	if _, _, _, err := AddRiverStroke(d, p, [][2]float64{{0, 0}, {0.5, 0}}, 2); err == nil {
		t.Fatal("a half-metre drag made a river")
	}
	// The stream on a document that already has a pond's ground_edit:
	// it gains the boulders' rocks, keeps its water list.
	d2 := freshDoc()
	if _, err := ApplyRecipe(d2, FindPreset(ps, "pond")); err != nil {
		t.Fatal(err)
	}
	sp := FindPreset(ps, "stream")
	if !NeedsRecipe(d2, sp) {
		// The ground_edit is there: the stroke goes straight in, and the
		// dressing waits for the recipe to be applied (the editor offers it).
		if _, err := ApplyRecipe(d2, sp); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := AddRiverStroke(d2, sp, pts, 2); err != nil {
		t.Fatal(err)
	}
	g2 := firstStageOf(d2, GroundEditKind)
	if len(g2.Params["water"].Items) != 1 || g2.Params.Str("river_rocks") != "river_rocks_src" ||
		g2.Params["rivers"].Items[0].Points("path")[0][2] != 2.8 {
		t.Fatalf("stream on a pond doc: %v", g2.Params)
	}
	if probs := d2.Validate(); len(probs) > 0 {
		t.Fatalf("invalid: %v", probs)
	}
}
