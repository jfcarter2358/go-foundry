package terrain

import (
	"context"
	"math"
	"testing"
)

// The backdrop is the map inside the square, continuous across its edge,
// its own country further out (mesas: a real spread of heights), the same
// on every call, and eased to the lowland at the end of its reach.
func TestFarFieldContinuesTheMap(t *testing.T) {
	s := Defaults()
	s.Size, s.Cell = 256, 1
	s.Droplets.Density, s.Thermal.Iterations, s.Drainage.Iterations = 0.1, 5, 5
	s.Mesa.Scale = 160
	res, err := Generate(&s, NewPool(context.Background(), 4), nil)
	if err != nil {
		t.Fatal(err)
	}
	g := res.Grid
	f := NewFarField(&s, res, 16, 200, 2000)
	for _, q := range [][2]float64{{0, 0}, {-100, 40}, {127, -127}} {
		if got, want := f.Height(q[0], q[1]), float64(g.Sample(q[0], q[1])); got != want {
			t.Errorf("inside at %v: %.3f, the map %.3f", q, got, want)
		}
	}
	// Across the edge: a metre out is within a few metres of the edge.
	for z := -120.0; z <= 120; z += 30 {
		in, out := float64(g.Sample(128, z)), f.Height(129, z)
		if math.Abs(in-out) > 4 {
			t.Errorf("edge at z=%g: map %.1f, a metre out %.1f", z, in, out)
		}
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for x := 400.0; x < 1500; x += 37 {
		for z := -1000.0; z < 1000; z += 41 {
			h := f.Height(x, z)
			lo, hi = math.Min(lo, h), math.Max(hi, h)
		}
	}
	if hi-lo < 0.5*s.Mesa.Height {
		t.Errorf("the far country spans only %.1f m of height (mesas %g m)", hi-lo, s.Mesa.Height)
	}
	if a, b := f.Height(900, 300), f.Height(900, 300); a != b {
		t.Error("not deterministic")
	}
	if h := f.Height(128+2000, 0); h > s.Base.Height+1e-6 {
		t.Errorf("at the end of the reach %.1f, above the lowland %.1f", h, s.Base.Height)
	}
}
