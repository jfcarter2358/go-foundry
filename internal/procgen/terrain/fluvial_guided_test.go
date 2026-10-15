package terrain

// fluvial_guided_test.go — the drainage's edge-aware upsampling
// (edgeAwareChange, docs/regions/PHASE_4.md part A2): at a sharp wall the
// rim takes the plateau's change, not a distance-weighted half of it (the
// knife-edged levee that drew a sawtooth along a ridge's crest), and on
// smooth ground it is the plain B-spline.

import (
	"math"
	"testing"
)

func TestGuidedChangeKeepsTheRimWithThePlateau(t *testing.T) {
	// A coarse grid 8 samples a side: columns 0..3 a plateau 21 m high
	// lowered 18 m, columns 4..7 a canyon floor at 0 lowered 2 m.
	const s = 8
	before := make([]float32, s*s)
	change := make([]float32, s*s)
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			k := j*s + i
			if i < 4 {
				before[k], change[k] = 21, -18
			} else {
				before[k], change[k] = 0, -2
			}
		}
	}
	// A fine sample on the rim, three quarters of a coarse cell short of
	// the floor's first column, at the plateau's height.
	fx, fz := 3.25, 4.0
	plain := bspline(change, s, fx, fz)
	rim := edgeAwareChange(change, before, s, fx, fz, 21.5, 4)
	if plain > -12 || plain < -16 {
		t.Fatalf("fixture: the plain B-spline should give a part-way change at the rim, got %.2f", plain)
	}
	if math.Abs(float64(rim)+18) > 0.6 {
		t.Errorf("the rim's change is %.2f, want the plateau's (-18); distance alone gives %.2f", rim, plain)
	}
	// A floor sample just past the wall takes the floor's change.
	if fl := edgeAwareChange(change, before, s, 3.9, fz, 0.2, 4); math.Abs(float64(fl)+2) > 0.6 {
		t.Errorf("the floor's change is %.2f, want about -2", fl)
	}
	// Half way down the wall: between the two, by height.
	if mid := edgeAwareChange(change, before, s, 3.6, fz, 10.5, 4); mid > -7 || mid < -13 {
		t.Errorf("half way down the wall the change is %.2f, want about -10", mid)
	}
}

func TestGuidedChangeIsTheBSplineOnSmoothGround(t *testing.T) {
	const s = 10
	before := make([]float32, s*s)
	change := make([]float32, s*s)
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			before[j*s+i] = 5 + 0.05*float32(i) // a gentle slope: 5 cm a coarse cell
			change[j*s+i] = -float32(math.Sin(float64(i)*0.7) + math.Cos(float64(j)*0.4))
		}
	}
	worst := 0.0
	for k := 0; k < 200; k++ {
		fx, fz := 1+7*float64(k%20)/20, 1+7*float64(k/20)/10
		h := float32(5 + 0.05*fx)
		d := edgeAwareChange(change, before, s, fx, fz, h, 4)
		worst = math.Max(worst, math.Abs(float64(d-bspline(change, s, fx, fz))))
	}
	if worst > 0.01 {
		t.Errorf("on gentle ground the guided change departs from the B-spline by %.4f m", worst)
	}
}
