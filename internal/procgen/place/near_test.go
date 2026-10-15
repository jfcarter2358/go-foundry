package place

import "testing"

// TestNearKeepsARuleToItsArea: a rule with a Near path weighs 1 within the
// radius of the polyline (interpolated along a segment), eases out over
// NearSoft past it and is 0 beyond; with no points the area is everywhere.
func TestNearKeepsARuleToItsArea(t *testing.T) {
	r := DefaultRule()
	r.Near = [][3]float64{{0, 0, 4}, {100, 0, 8}}
	r.NearSoft = 2
	cases := []struct {
		x, z, want float64
	}{
		{0, 3.9, 1},   // inside the first point's radius
		{50, 5.9, 1},  // mid segment: the radius is 6 there
		{50, 7, -1},   // in the fade
		{50, 8.1, 0},  // past radius + soft
		{100, 7.9, 1}, // the far end's radius
		{-5, 0, -1},   // past the first end, in the fade
		{-10, 0, 0},   // well past it
		{200, 200, 0}, // nowhere near
	}
	for _, c := range cases {
		w := r.nearWeight(c.x, c.z)
		switch {
		case c.want < 0 && (w <= 0 || w >= 1):
			t.Errorf("(%g, %g): weight %g, want inside the fade", c.x, c.z, w)
		case c.want >= 0 && w != c.want:
			t.Errorf("(%g, %g): weight %g, want %g", c.x, c.z, w, c.want)
		}
	}
	// One point is a disc.
	r.Near = [][3]float64{{10, 10, 3}}
	if w := r.nearWeight(12, 10); w != 1 {
		t.Errorf("inside a one-point area: %g", w)
	}
	if w := r.nearWeight(20, 10); w != 0 {
		t.Errorf("outside a one-point area: %g", w)
	}
}
