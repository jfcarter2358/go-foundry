package wallsheet

// wallsheet_weight_test.go — Settings.Weight, an environment region's share
// (docs/regions/PHASE_4.md): the shell stands out of the terrain by the
// weight, sinks under it where the weight is 0, and no section is meshed
// where the region has no share.

import (
	"context"
	"testing"

	"foundry/internal/procgen/sdf"
	"foundry/internal/procgen/terrain"
)

func TestWeightSinksTheShell(t *testing.T) {
	g := cliffGrid()
	s := Defaults()
	s.Seed = 9
	pool := terrain.NewPool(context.Background(), 4)
	full, err := NewField(g, s, pool)
	if err != nil {
		t.Fatal(err)
	}
	// The region owns z < -12 and has no share of z > -4 (the grid is
	// z ±32 in 32 m sections: the north pair is none of its).
	s.Weight = func(x, z float64) float64 {
		switch {
		case z < -12:
			return 1
		case z > -4:
			return 0
		}
		return (-4 - z) / 8
	}
	wf, err := NewField(g, s, pool)
	if err != nil {
		t.Fatal(err)
	}
	// Half way up the cliff, a point just out from the terrain: in the rock
	// where the region owns the wall, in the air where it has none.
	h := float64(g.Sample(0, 0))
	in := func(f *Field, z float64) float64 { return f.F(sdf.Vec3{-0.6, h, z}) }
	if a, b := in(full, -24), in(wf, -24); a != b {
		t.Errorf("where the weight is 1 the shell moved: %.3f vs %.3f", a, b)
	}
	if in(full, 20) >= 0 {
		t.Fatalf("fixture: the full shell does not stand out at z 20 (%.3f)", in(full, 20))
	}
	if d := in(wf, 20); d <= 0 {
		t.Errorf("where the weight is 0 the shell still stands out (%.3f)", d)
	}
	nf, nw := len(full.Sections()), len(wf.Sections())
	if nw == 0 || nw >= nf {
		t.Errorf("%d sections weighted against %d", nw, nf)
	}
	for _, sec := range wf.Sections() {
		if sec.Lo[2] >= 0 {
			t.Errorf("a section at z %.0f, where the region has no share", sec.Lo[2])
		}
	}
}
