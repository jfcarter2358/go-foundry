package engrave

// sky.go — the ink already placed, as a skyline.
//
// What goes around the notes (articulations outside the staff, slurs,
// fermatas, dynamics, hairpins, ottava and pedal lines, and over the system
// tempo and rehearsal marks) is placed clear of everything drawn before it.
// A sky keeps, for every quarter space across the layout, the highest and
// the lowest ink there; placing a thing asks for the extreme over its
// width and adds the thing itself, so the next one goes beyond it.

import "math"

// skyStep is the width of one column of the sky, in staff spaces.
const skyStep = 0.25

// sky is the extremes of the ink so far, column by column.
type sky struct {
	x0       float64
	top, bot []float64
}

// newSky covers x0..x1 with no ink.
func newSky(x0, x1 float64) *sky {
	n := int(math.Ceil((x1-x0)/skyStep)) + 1
	k := &sky{x0: x0, top: make([]float64, n), bot: make([]float64, n)}
	for i := range k.top {
		k.top[i], k.bot[i] = math.Inf(1), math.Inf(-1)
	}
	return k
}

// span is the columns x0..x1 covers, clamped to the sky.
func (k *sky) span(x0, x1 float64) (int, int) {
	a := int(math.Floor((x0 - k.x0) / skyStep))
	b := int(math.Floor((x1 - k.x0) / skyStep))
	return min(max(a, 0), len(k.top)-1), min(max(b, 0), len(k.top)-1)
}

// add puts ink into the sky.
func (k *sky) add(r Rect) {
	if r.Empty() && r.W() == 0 && r.H() == 0 {
		return
	}
	a, b := k.span(r.Min.X, r.Max.X)
	for i := a; i <= b; i++ {
		k.top[i] = math.Min(k.top[i], r.Min.Y)
		k.bot[i] = math.Max(k.bot[i], r.Max.Y)
	}
}

// topIn is the highest ink over x0..x1 (y down: the least y), and never
// lower than def.
func (k *sky) topIn(x0, x1, def float64) float64 {
	a, b := k.span(x0, x1)
	y := def
	for i := a; i <= b; i++ {
		y = math.Min(y, k.top[i])
	}
	return y
}

// botIn is the lowest ink over x0..x1, and never higher than def.
func (k *sky) botIn(x0, x1, def float64) float64 {
	a, b := k.span(x0, x1)
	y := def
	for i := a; i <= b; i++ {
		y = math.Max(y, k.bot[i])
	}
	return y
}

// skyOf is a sky holding items — the music's, not the staff's frame: staff
// lines, barlines, clefs and signatures are not ink a slur or a dynamic
// keeps clear of (what is placed outside the staff keeps clear of the staff
// by its own rule).
func (e *engraver) skyOf(items []Item, x0, x1 float64) *sky {
	k := newSky(x0, x1)
	for i := range items {
		switch items[i].Role {
		case RoleStaff, RoleBarline, RoleClef, RoleKey, RoleTime, RoleBrace:
			continue
		}
		if items[i].Faint {
			continue
		}
		k.add(itemBounds(e.f, &items[i]))
	}
	return k
}
