package terrain

// path_test.go — LayPaths and PathStones (path*.go, docs/terrain-paint/
// PHASE_P4.md): the flatten levels the cross-section to a smoothed
// centreline and leaves the ground past the blend alone, the surface mask
// has a worn (irregular) edge and the wear band reaches past it, the
// direction masks hold the tangent, flagstones never overlap and face along
// the path, stepping stones are a stride apart and zig-zag, and a path
// crossing a river neither flattens nor paints the channel but fords it.

import (
	"math"
	"testing"
)

// sideSlopeGrid is a 64 m map at 0.5 m sloping 12 cm/m across X, with a
// 0.4 m bump along Z at z = 5 (so the along-smoothing has work to do).
func sideSlopeGrid() *Grid {
	g := NewGrid(128, 0.5)
	s := g.S()
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			x, z := g.X(i), g.Z(j)
			g.H[j*s+i] = float32(5 + 0.12*x + 0.4*math.Exp(-(z-5)*(z-5)/2))
		}
	}
	return g
}

func straightPath(style string, w float64) PathSpec {
	return PathSpec{Name: "p", Style: style, Points: [][2]float64{{0, -25}, {0, 0}, {0, 25}}, Width: []float64{w, w, w},
		Shape: DefaultPathShape(), Seed: 11}
}

func TestPathFlattenLevelsTheCrossSection(t *testing.T) {
	g := sideSlopeGrid()
	before := g.Clone()
	res := LayPaths(g, []PathSpec{straightPath(PathDirt, 3)}, nil, nil)
	if len(res.Paths) != 1 || res.Changed == 0 {
		t.Fatalf("paths %d, changed %d", len(res.Paths), res.Changed)
	}
	p := res.Paths[0]
	if math.Abs(p.Length-50) > 0.6 {
		t.Fatalf("length %.2f, want about 50", p.Length)
	}
	// Across the path (half width 1.5), at z = -15 (away from the bump),
	// the cross slope is gone: every sample within 1.3 m of the centre is
	// within 2 cm of the centreline's level.
	for _, z := range []float64{-15, 15} {
		c := float64(g.Sample(0, z))
		for x := -1.25; x <= 1.25; x += 0.25 {
			if d := math.Abs(float64(g.Sample(x, z)) - c); d > 0.02 {
				t.Fatalf("at (%.2f, %.0f) the ground is %.3f m off the centreline's level", x, z, d)
			}
		}
		// It was sloping there: 0.12 × 2.5 m across.
		if d := float64(before.Sample(1.25, z) - before.Sample(-1.25, z)); d < 0.25 {
			t.Fatalf("the test ground is not sloping: %.3f", d)
		}
		// Past the blend (1.5 + 1.2 m), the ground is untouched.
		for _, x := range []float64{-4, 4, 8} {
			if g.Sample(x, z) != before.Sample(x, z) {
				t.Fatalf("(%g, %g) moved, past the blend", x, z)
			}
		}
	}
	// Along: the 0.4 m bump is smoothed down on the centreline.
	if b, a := before.Sample(0, 5)-before.Sample(0, -5), g.Sample(0, 5)-g.Sample(0, -5); a > 0.6*b {
		t.Fatalf("the bump along the path was %.3f, and is still %.3f", b, a)
	}
	// Flatten off: nothing moves.
	g2 := before.Clone()
	sp := straightPath(PathGame, 1)
	sp.Shape.Flatten = false
	if r := LayPaths(g2, []PathSpec{sp}, nil, nil); r.Changed != 0 {
		t.Fatalf("an unflattened path moved %d samples", r.Changed)
	}
}

func TestPathStepsOnASteepClimb(t *testing.T) {
	g := NewGrid(64, 0.5)
	s := g.S()
	for k := range g.H {
		g.H[k] = float32(0.45 * g.Z(k/s)) // 0.45 rise per metre along Z
	}
	sp := straightPath(PathRock, 2)
	sp.Shape.Steps = true
	res := LayPaths(g, []PathSpec{sp}, nil, nil)
	st := res.Paths[0].Stations
	// Mid-path targets sit near multiples of the rise.
	near := 0
	total := 0
	for _, x := range st[len(st)/4 : 3*len(st)/4] {
		total++
		q := x.Target / sp.Shape.StepRise
		if math.Abs(q-math.Round(q)) < 0.1 {
			near++
		}
	}
	if near < total*8/10 {
		t.Fatalf("%d of %d targets on a tread", near, total)
	}
}

func TestPathMasksHaveAWornEdgeAndAWearBand(t *testing.T) {
	g := sideSlopeGrid()
	res := LayPaths(g, []PathSpec{straightPath(PathDirt, 3)}, nil, nil)
	surf := res.Masks[PathSurfaceMask(PathDirt)]
	wear := res.Masks[MaskPathWear]
	mg := *g
	at := func(m []float32, x, z float64) float64 { mg.H = m; return float64(mg.Sample(x, z)) }
	// Centre full, far side empty, every other style's surface empty.
	if v := at(surf, 0, 0); v < 0.99 {
		t.Fatalf("surface at the centre %.2f", v)
	}
	if v := at(surf, 6, 0); v != 0 {
		t.Fatalf("surface 6 m off %.2f", v)
	}
	if v := at(res.Masks[PathSurfaceMask(PathRock)], 0, 0); v != 0 {
		t.Fatalf("the rock surface is painted by a dirt trail: %.2f", v)
	}
	// The edge (where the surface falls through 0.5) wanders along the path.
	var edges []float64
	for z := -20.0; z <= 20; z += 0.5 {
		for x := 0.0; x < 4; x += 0.05 {
			if at(surf, x, z) < 0.5 {
				edges = append(edges, x)
				break
			}
		}
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, e := range edges {
		lo, hi = math.Min(lo, e), math.Max(hi, e)
	}
	if hi-lo < 0.35 {
		t.Fatalf("the edge runs %.2f–%.2f m out: a ruled line, not worn", lo, hi)
	}
	// The wear reaches past the surface's edge and fades: some wear 0.5 m
	// beyond the edge, none 4 m beyond.
	if v := at(wear, hi+0.5, 0); v <= 0 && at(wear, lo+0.5, 0) <= 0 {
		t.Fatalf("no wear band past the edge")
	}
	if v := at(wear, 8, 0); v != 0 {
		t.Fatalf("wear 8 m off: %.2f", v)
	}
	// Direction: the tangent (0, ±1) on the path, still (0.5) far off.
	dz := at(res.Masks[MaskPathDirZ], 0, 0)
	if math.Abs(dz-0.5) < 0.49 || math.Abs(at(res.Masks[MaskPathDirX], 0, 0)-0.5) > 0.01 {
		t.Fatalf("direction on the path (%.2f, %.2f)", at(res.Masks[MaskPathDirX], 0, 0), dz)
	}
	if v := at(res.Masks[MaskPathDirZ], 10, 0); v != 0.5 {
		t.Fatalf("direction 10 m off %.2f", v)
	}
}

func TestFlagstonesFaceAlongThePathAndNeverOverlap(t *testing.T) {
	g := sideSlopeGrid()
	sp := PathSpec{Name: "r", Style: PathRock, Points: [][2]float64{{-20, -20}, {0, -5}, {10, 15}, {-5, 28}},
		Width: []float64{1.8, 2.2, 1.6, 2}, Shape: DefaultPathShape(), Seed: 3}
	specs := []PathSpec{sp}
	res := LayPaths(g, specs, nil, nil)
	stones := PathStones(g, specs, res)
	if len(stones) < 60 {
		t.Fatalf("%d flagstones on a %.0f m path", len(stones), res.Paths[0].Length)
	}
	p := &res.Paths[0]
	for i := range stones {
		a := &stones[i]
		if a.Kind != StonesFlag {
			t.Fatalf("stone %d is %s", i, a.Kind)
		}
		for j := i + 1; j < len(stones); j++ {
			if stonesOverlap(a, &stones[j]) {
				t.Fatalf("stones %d and %d overlap", i, j)
			}
		}
		// Its heading matches the nearest station's within ±0.2 rad (mod π).
		best, bd := PathStation{}, math.Inf(1)
		for _, s := range p.Stations {
			if d := math.Hypot(s.X-a.X, s.Z-a.Z); d < bd {
				best, bd = s, d
			}
		}
		if bd > 1.3 {
			t.Fatalf("stone %d is %.2f m off the centreline", i, bd)
		}
		d := math.Remainder(a.Yaw-math.Atan2(best.TX, best.TZ), math.Pi)
		if math.Abs(d) > 0.2 {
			t.Fatalf("stone %d turned %.2f rad off the path", i, d)
		}
		if a.Thick < 0.08 || a.Thick > 0.15 || a.Width > 1.2 || a.Length > 0.9 {
			t.Fatalf("stone %d is %.2f × %.2f × %.2f", i, a.Length, a.Width, a.Thick)
		}
	}
}

func TestSteppingStonesAreAStrideApartAndZigZag(t *testing.T) {
	g := sideSlopeGrid()
	sp := straightPath(PathStepping, 1.2)
	sp.Shape.Flatten = false
	specs := []PathSpec{sp}
	res := LayPaths(g, specs, nil, nil)
	stones := PathStones(g, specs, res)
	if len(stones) < 55 || len(stones) > 85 {
		t.Fatalf("%d stepping stones over 50 m", len(stones))
	}
	lefts := 0
	for i, s := range stones {
		if s.Kind != StonesStep {
			t.Fatalf("stone %d is %s", i, s.Kind)
		}
		if s.X > 0 {
			lefts++
		}
		if i == 0 {
			continue
		}
		if d := s.Z - stones[i-1].Z; d < 0.55 || d > 0.85 {
			t.Fatalf("stride %d: %.2f m", i, d)
		}
		if math.Abs(s.X) > 0.2 {
			t.Fatalf("stone %d is %.2f m off the line", i, s.X)
		}
	}
	if lefts < len(stones)/3 || lefts > 2*len(stones)/3 {
		t.Fatalf("%d of %d stones left of the line: not a zig-zag", lefts, len(stones))
	}
}

func TestPathAcrossARiverFordsItWithoutTouchingTheChannel(t *testing.T) {
	g := valleyGrid()
	// A fordable stream: half a metre deep at most.
	spec := valleyRiver("r", [2]float64{-60, 0}, [2]float64{60, 0})
	spec.Depth = []float64{0.45, 0.45}
	rv := CarveRivers(g, []RiverSpec{spec}, nil, math.Inf(-1))
	carved := g.Clone()
	// A rock path across the valley, north to south.
	sp := PathSpec{Name: "x", Style: PathRock, Points: [][2]float64{{5, -30}, {5, 30}}, Width: []float64{2, 2},
		Shape: DefaultPathShape(), Seed: 5}
	specs := []PathSpec{sp}
	res := LayPaths(g, specs, nil, rv)
	if res.Paths[0].Crossings != 1 {
		t.Fatalf("%d crossings", res.Paths[0].Crossings)
	}
	wetN := 0
	for k32, s := range rv.Samples {
		k := int(k32)
		if s.Wet || s.Bed > 0.05 {
			if g.H[k] != carved.H[k] {
				t.Fatalf("sample %d in the channel moved %.3f", k, g.H[k]-carved.H[k])
			}
		}
		if s.Wet {
			wetN++
			for _, m := range []string{PathSurfaceMask(PathRock), MaskPathWear, MaskPathClear} {
				if res.Masks[m][k] != 0 {
					t.Fatalf("%s painted under the river at %d", m, k)
				}
			}
		}
	}
	if wetN == 0 || res.Changed == 0 {
		t.Fatalf("wet %d, changed %d", wetN, res.Changed)
	}
	stones := PathStones(g, specs, res)
	ford, flag := 0, 0
	for _, s := range stones {
		switch s.Kind {
		case StonesFord:
			ford++
			if math.IsNaN(s.Level) || s.Level-float64(g.Sample(s.X, s.Z)) > FordMaxDepth {
				t.Fatalf("ford stone at (%.1f, %.1f): level %.2f over ground %.2f", s.X, s.Z, s.Level, g.Sample(s.X, s.Z))
			}
		case StonesFlag:
			flag++
			if k := sampleIndex(g, s.X, s.Z); k >= 0 {
				if rs, ok := rv.Samples[int32(k)]; ok && rs.Wet {
					t.Fatalf("a flagstone in the river at (%.1f, %.1f)", s.X, s.Z)
				}
			}
		}
	}
	if ford < 3 || flag < 40 {
		t.Fatalf("%d ford stones, %d flagstones", ford, flag)
	}
	// A game trail fords nothing.
	g2 := carved.Clone()
	gs := []PathSpec{{Name: "g", Style: PathGame, Points: sp.Points, Width: []float64{0.8, 0.8}, Shape: DefaultPathShape(), Seed: 1}}
	gr := LayPaths(g2, gs, nil, rv)
	if n := len(PathStones(g2, gs, gr)); n != 0 {
		t.Fatalf("a game trail laid %d stones", n)
	}
}
