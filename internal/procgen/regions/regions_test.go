package regions

import (
	"context"
	"math"
	"testing"
)

func square(cx, cz, half float64) []Point {
	return []Point{{cx - half, cz - half}, {cx + half, cz - half}, {cx + half, cz + half}, {cx - half, cz + half}}
}

func mustSet(t *testing.T, rs []Region, w Warp) *Set {
	t.Helper()
	s, err := NewSet(rs, w)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Two equal-priority squares sharing the edge x = 0.
func twoNeighbours(t *testing.T, w Warp) *Set {
	return mustSet(t, []Region{
		{ID: "west", Environment: "desert_canyon", Transition: 100, Footprint: square(-250, 0, 250)},
		{ID: "east", Environment: "savannah", Transition: 100, Footprint: square(250, 0, 250)},
	}, w)
}

func TestSignedDistanceSquare(t *testing.T) {
	sq := square(0, 0, 10)
	cases := []struct{ x, z, want float64 }{
		{0, 0, -10}, {5, 0, -5}, {10, 0, 0}, {15, 0, 5}, {13, 14, 5}, {-10, -10, 0},
	}
	for _, c := range cases {
		if d := SignedDistance(sq, c.x, c.z); math.Abs(d-c.want) > 1e-9 {
			t.Errorf("d(%v, %v) = %v, want %v", c.x, c.z, d, c.want)
		}
	}
}

// An L: the notch's corner is outside, the arms inside, and the distance
// near the reflex corner is to the nearest edge, not to the hull.
func TestSignedDistanceConcave(t *testing.T) {
	l := []Point{{0, 0}, {100, 0}, {100, 40}, {40, 40}, {40, 100}, {0, 100}}
	if d := SignedDistance(l, 20, 80); d >= 0 {
		t.Errorf("inside the upright arm: d = %v, want < 0", d)
	}
	if d := SignedDistance(l, 80, 20); d >= 0 {
		t.Errorf("inside the foot: d = %v, want < 0", d)
	}
	if d := SignedDistance(l, 70, 70); math.Abs(d-30) > 1e-9 {
		t.Errorf("in the notch: d = %v, want 30 (to the reflex corner's edges)", d)
	}
	if !PointInPolygon(l, 39, 99) || PointInPolygon(l, 41, 41) {
		t.Error("PointInPolygon disagrees with the L")
	}
	// Winding does not matter.
	rev := make([]Point, len(l))
	for i := range l {
		rev[i] = l[len(l)-1-i]
	}
	for _, p := range []Point{{20, 80}, {70, 70}, {-5, 50}} {
		if a, b := SignedDistance(l, p[0], p[1]), SignedDistance(rev, p[0], p[1]); a != b {
			t.Errorf("winding changed d at %v: %v vs %v", p, a, b)
		}
	}
	s := mustSet(t, []Region{{ID: "l", Environment: "x", Transition: 10, Footprint: l}}, Warp{})
	if w := s.Weight(0, 20, 80); w != 1 {
		t.Errorf("single region weight = %v, want 1", w)
	}
}

func TestValidateRefusesBadFootprints(t *testing.T) {
	bow := []Point{{0, 0}, {10, 10}, {10, 0}, {0, 10}}
	cases := map[string][]Region{
		"too few points": {{ID: "a", Environment: "e", Footprint: []Point{{0, 0}, {1, 0}}}},
		"no area":        {{ID: "a", Environment: "e", Footprint: []Point{{0, 0}, {1, 0}, {2, 0}}}},
		"crossing":       {{ID: "a", Environment: "e", Footprint: bow}},
		"no env":         {{ID: "a", Footprint: square(0, 0, 1)}},
		"duplicate key": {{ID: "Desert A", Environment: "e", Footprint: square(0, 0, 1)},
			{ID: "desert-a", Environment: "e", Footprint: square(5, 0, 1)}},
		"no id": {{ID: "--", Environment: "e", Footprint: square(0, 0, 1)}},
	}
	for name, rs := range cases {
		if _, err := NewSet(rs, Warp{}); err == nil {
			t.Errorf("%s: NewSet accepted it", name)
		}
	}
}

func TestKey(t *testing.T) {
	for in, want := range map[string]string{
		"region_1": "region_1", "Desert Canyon!": "desert_canyon", "  a--b  ": "a_b", "9lives": "r9lives", "--": "",
	} {
		if got := Key(in); got != want {
			t.Errorf("Key(%q) = %q, want %q", in, got, want)
		}
	}
}

func sum(ws []W) float64 {
	s := 0.0
	for _, w := range ws {
		s += w.Weight
	}
	return s
}

// Everywhere — inside, in the band, in a gap no region reaches, far away —
// the shares sum to 1.
func TestWeightsSumToOne(t *testing.T) {
	for _, warp := range []Warp{{}, {Amount: 40, Scale: 200, Seed: 7}} {
		s := mustSet(t, []Region{
			{ID: "a", Environment: "desert_canyon", Transition: 100, Footprint: square(-250, 0, 250)},
			{ID: "b", Environment: "savannah", Transition: 150, Priority: 2, Footprint: square(100, 50, 200)},
			{ID: "c", Environment: "savannah", Transition: 60, Footprint: square(900, 900, 100)},
		}, warp)
		var buf []W
		for x := -800.0; x <= 1300; x += 37 {
			for z := -800.0; z <= 1300; z += 41 {
				buf = s.Weights(x, z, buf)
				if math.Abs(sum(buf)-1) > 1e-9 {
					t.Fatalf("warp %v: weights at (%v, %v) sum to %v: %v", warp, x, z, sum(buf), buf)
				}
				for _, w := range buf {
					if w.Weight <= 0 || w.Weight > 1+1e-12 {
						t.Fatalf("share out of range at (%v, %v): %v", x, z, buf)
					}
				}
			}
		}
	}
}

func TestInsideIsOneFarFromEdges(t *testing.T) {
	s := twoNeighbours(t, Warp{Amount: 30, Scale: 200, Seed: 3})
	// 50 m band + up to 60 m of warp: 150 m in is past both.
	for _, p := range []Point{{-250, 0}, {-150, 100}, {-400, -200}} {
		if w := s.Weight(s.Index("west"), p[0], p[1]); w != 1 {
			t.Errorf("west at %v = %v, want exactly 1", p, w)
		}
	}
	if w := s.Weight(s.Index("east"), 250, 0); w != 1 {
		t.Errorf("east at its centre = %v, want 1", w)
	}
}

// Equal priorities split the band symmetrically about the shared edge: on
// the edge both are 0.5, and at mirrored offsets the weights swap.
func TestBandSymmetryForEqualPriority(t *testing.T) {
	s := twoNeighbours(t, Warp{})
	west, east := s.Index("west"), s.Index("east")
	for z := -200.0; z <= 200; z += 50 {
		if w := s.Weight(west, 0, z); math.Abs(w-0.5) > 1e-12 {
			t.Errorf("on the edge at z=%v west = %v, want 0.5", z, w)
		}
		for _, dx := range []float64{5, 20, 35, 49} {
			a, b := s.Weight(west, -dx, z), s.Weight(east, dx, z)
			if math.Abs(a-b) > 1e-12 {
				t.Errorf("asymmetric band at dx=%v z=%v: west %v east %v", dx, z, a, b)
			}
			if a <= 0.5 || a >= 1 {
				t.Errorf("west %v m inside = %v, want in (0.5, 1)", dx, a)
			}
		}
	}
	// Monotone across the band.
	prev := 2.0
	for x := -60.0; x <= 60; x += 5 {
		w := s.Weight(west, x, 0)
		if w > prev+1e-12 {
			t.Fatalf("west weight rises at x=%v: %v > %v", x, w, prev)
		}
		prev = w
	}
}

// A higher-priority region carves its footprint out of a lower one.
func TestPriorityWins(t *testing.T) {
	s := mustSet(t, []Region{
		{ID: "big", Environment: "desert_canyon", Transition: 40, Footprint: square(0, 0, 500)},
		{ID: "island", Environment: "savannah", Transition: 40, Priority: 1, Footprint: square(0, 0, 100)},
	}, Warp{})
	big, island := s.Index("big"), s.Index("island")
	if w := s.Weight(island, 0, 0); w != 1 {
		t.Errorf("island centre = %v, want 1", w)
	}
	if w := s.Weight(big, 0, 0); w != 0 {
		t.Errorf("big under the island = %v, want 0", w)
	}
	if w := s.Weight(island, 100, 0); math.Abs(w-0.5) > 1e-12 {
		t.Errorf("island edge = %v, want 0.5 (it takes its share first)", w)
	}
	if w := s.Weight(big, 300, 0); w != 1 {
		t.Errorf("big away from the island = %v, want 1", w)
	}
	// Flip the priorities: the island vanishes under the big one.
	s2 := mustSet(t, []Region{
		{ID: "big", Environment: "desert_canyon", Transition: 40, Priority: 1, Footprint: square(0, 0, 500)},
		{ID: "island", Environment: "savannah", Transition: 40, Footprint: square(0, 0, 100)},
	}, Warp{})
	if w := s2.Weight(s2.Index("island"), 0, 0); w != 0 {
		t.Errorf("lower-priority island = %v, want 0", w)
	}
}

func TestGapGoesToNearestRegion(t *testing.T) {
	s := mustSet(t, []Region{
		{ID: "a", Environment: "x", Transition: 20, Footprint: square(-500, 0, 100)},
		{ID: "b", Environment: "y", Transition: 20, Footprint: square(500, 0, 100)},
	}, Warp{})
	if w := s.Weight(s.Index("a"), -100, 0); w != 1 {
		t.Errorf("gap nearer a: a = %v, want 1", w)
	}
	if w := s.Weight(s.Index("b"), 150, 30); w != 1 {
		t.Errorf("gap nearer b: b = %v, want 1", w)
	}
}

// Past two neighbours' outer corners — where neither band reaches — the
// hand-off is still a smooth band, not a step at the midline.
func TestNoStepWhereNoRegionReaches(t *testing.T) {
	s := twoNeighbours(t, Warp{Amount: 20, Scale: 200, Seed: 5})
	west := s.Index("west")
	for _, z := range []float64{0, 280, 330, 400, 600} {
		prev := s.Weight(west, -150, z)
		for x := -149.0; x <= 150; x++ {
			w := s.Weight(west, x, z)
			if math.Abs(w-prev) > 0.05 {
				t.Fatalf("west jumps %v -> %v at x=%v z=%v", prev, w, x, z)
			}
			prev = w
		}
	}
}

func TestDeterministicAndOrderIndependent(t *testing.T) {
	rs := []Region{
		{ID: "b", Environment: "savannah", Transition: 100, Footprint: square(250, 0, 250)},
		{ID: "a", Environment: "desert_canyon", Transition: 100, Footprint: square(-250, 0, 250)},
	}
	w := Warp{Amount: 50, Scale: 300, Seed: 99}
	s1 := mustSet(t, rs, w)
	s2 := mustSet(t, []Region{rs[1], rs[0]}, w)
	g := GridOver(s1.Bounds(), 8, 128)
	r1, err := s1.Raster(context.Background(), g, 3)
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := s2.Raster(nil, g, 1)
	for k := range r1.Planes {
		for i := range r1.Planes[k] {
			if r1.Planes[k][i] != r2.Planes[k][i] {
				t.Fatalf("plane %d sample %d differs: %v vs %v", k, i, r1.Planes[k][i], r2.Planes[k][i])
			}
		}
	}
	// The warp actually moves the edge, and a different seed moves it
	// differently.
	plain := twoNeighbours(t, Warp{})
	warped := twoNeighbours(t, w)
	other := twoNeighbours(t, Warp{Amount: 50, Scale: 300, Seed: 100})
	diff, diff2 := 0, 0
	for z := -240.0; z <= 240; z += 10 {
		a := plain.Weight(0, 0, z)
		if math.Abs(warped.Weight(0, 0, z)-a) > 1e-3 {
			diff++
		}
		if math.Abs(warped.Weight(0, 0, z)-other.Weight(0, 0, z)) > 1e-3 {
			diff2++
		}
	}
	if diff == 0 || diff2 == 0 {
		t.Errorf("the warp changed %d edge samples and the seed %d; want both > 0", diff, diff2)
	}
}

func TestRasterMatchesWeightsAndBounds(t *testing.T) {
	s := twoNeighbours(t, Warp{Amount: 20, Scale: 150, Seed: 1})
	b := s.Bounds()
	if b.MinX > -500-50 || b.MaxX < 500+50 {
		t.Errorf("bounds %v do not hold both footprints plus half a band", b)
	}
	g := GridOver(b, 10, 64)
	if g.W > 64 || g.H > 64 {
		t.Errorf("grid %dx%d over the cap", g.W, g.H)
	}
	r, err := s.Raster(nil, g, 0)
	if err != nil {
		t.Fatal(err)
	}
	for j := 0; j < g.H; j += 7 {
		for i := 0; i < g.W; i += 5 {
			x, z := g.At(i, j)
			tot := float32(0)
			for k := range r.Planes {
				tot += r.At(k, i, j)
				if want := float32(s.Weight(k, x, z)); r.At(k, i, j) != want {
					t.Fatalf("raster (%d,%d) region %d = %v, Weight says %v", i, j, k, r.At(k, i, j), want)
				}
			}
			if math.Abs(float64(tot)-1) > 1e-5 {
				t.Fatalf("raster (%d,%d) sums to %v", i, j, tot)
			}
		}
	}
	// Outside a region's band box its raw weight is 0 wherever the warp
	// carries the point: the box is honest.
	for i := 0; i < s.Len(); i++ {
		bb := s.BandBounds(i)
		for z := bb.MinZ; z <= bb.MaxZ; z += 13 {
			for _, x := range []float64{bb.MinX - 0.5, bb.MaxX + 0.5} {
				if wx, wz := s.Warp.at(x, z); s.raw(i, wx, wz) != 0 {
					t.Fatalf("region %d has raw weight at (%v, %v), outside its band box %v", i, x, z, bb)
				}
			}
		}
	}
	if cent := Centroid(square(10, -20, 5)); math.Abs(cent[0]-10) > 1e-9 || math.Abs(cent[1]+20) > 1e-9 {
		t.Errorf("centroid %v, want (10, -20)", cent)
	}
}

func TestAdjacencyDefaults(t *testing.T) {
	a := DefaultAdjacency()
	r, ok := a.Lookup(EnvSavannah, EnvDesertCanyon)
	if !ok || r.Transition != 200 || r.Forbidden {
		t.Errorf("desert/savannah = %+v %v, want a 200 m band", r, ok)
	}
	r, ok = a.Lookup(EnvTundra, EnvDesertCanyon)
	if !ok || !r.Forbidden || r.Intermediate != EnvSavannah {
		t.Errorf("desert/tundra = %+v, want forbidden via savannah", r)
	}
	if r, ok := a.Lookup(EnvAlpine, EnvCoast); !ok || r.Transition != 100 {
		t.Errorf("coast/any = %+v %v, want 100 m", r, ok)
	}
	if _, ok := a.Lookup(EnvSavannah, EnvSavannah); ok {
		t.Error("an environment has a rule against itself")
	}
	// ENVIRONMENT.md's 11 rows (desert/alpine and desert/tundra apart) plus
	// dunes/alpine and dunes/tundra, then the step-2 presets' rows
	// (defaultPresetAdjacency, docs/regions/PRESETS_2.md).
	extra := NewAdjacency()
	defaultPresetAdjacency(extra)
	if len(a.Pairs()) != 13+len(extra.Pairs()) {
		t.Errorf("%d pairs, want 13 + %d", len(a.Pairs()), len(extra.Pairs()))
	}
}

// A forbidden pair's intermediate may itself touch both sides: inserting
// it must end the warning, not move it.
func TestAdjacencyIntermediatesAreAllowed(t *testing.T) {
	a := DefaultAdjacency()
	for _, p := range a.Pairs() {
		r, _ := a.Rule(p)
		if !r.Forbidden {
			continue
		}
		if r.Intermediate == "" {
			t.Errorf("%s/%s is forbidden with no intermediate", p.A, p.B)
			continue
		}
		for _, side := range []string{p.A, p.B} {
			if side == AnyEnvironment {
				continue
			}
			if rr, ok := a.Lookup(side, r.Intermediate); ok && rr.Forbidden {
				t.Errorf("%s/%s inserts %s, which %s forbids", p.A, p.B, r.Intermediate, side)
			}
		}
	}
}
