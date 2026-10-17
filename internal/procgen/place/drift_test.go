package place

import (
	"context"
	"fmt"
	"math"
	"testing"

	"foundry/internal/procgen/terrain"
)

// boxMesh is a closed axis-aligned box from lo to hi.
func boxMesh(lo, hi [3]float64) ([][3]float64, [][3]int) {
	var pos [][3]float64
	for k := 0; k < 8; k++ {
		p := lo
		if k&1 != 0 {
			p[0] = hi[0]
		}
		if k&2 != 0 {
			p[1] = hi[1]
		}
		if k&4 != 0 {
			p[2] = hi[2]
		}
		pos = append(pos, p)
	}
	tris := [][3]int{{0, 1, 3}, {0, 3, 2}, {4, 6, 7}, {4, 7, 5}, {0, 4, 5}, {0, 5, 1},
		{2, 3, 7}, {2, 7, 6}, {0, 2, 6}, {0, 6, 4}, {1, 5, 7}, {1, 7, 3}}
	return pos, tris
}

func flatGrid() *terrain.Grid { return terrain.NewGrid(80, 0.5) } // 40 m, samples every 0.5 m about the origin

func TestRasterColumnsSeesTheBoxTopAndUnderside(t *testing.T) {
	g := flatGrid()
	pos, tris := boxMesh([3]float64{-1.1, -0.3, -0.6}, [3]float64{1.1, 1.7, 0.6})
	c := RasterColumns(g, pos, tris)
	if c == nil {
		t.Fatal("no columns")
	}
	// The samples inside the box's XZ: x in -1..1 (5 per metre... 0.5 m steps).
	if c.NI != 5 || c.NJ != 3 {
		t.Fatalf("window %d×%d, want 5×3", c.NI, c.NJ)
	}
	for k := range c.Bot {
		if math.Abs(float64(c.Bot[k])+0.3) > 1e-6 || math.Abs(float64(c.Top[k])-1.7) > 1e-6 {
			t.Fatalf("column %d: %.3f..%.3f, want -0.3..1.7", k, c.Bot[k], c.Top[k])
		}
	}
}

func TestDriftFilletsTheRockAndTrailsDownwind(t *testing.T) {
	g := flatGrid()
	s := DefaultDrift()
	pos, tris := boxMesh([3]float64{-1.1, -0.3, -1.1}, [3]float64{1.1, 2, 1.1})
	rocks := []DriftRock{{Cols: RasterColumns(g, pos, tris), Dir: [2]float64{1, 0}, Seed: 3}}
	low, ltris := boxMesh([3]float64{8, -0.1, 8}, [3]float64{9, 0.3, 9}) // 0.3 m: under MinHeight
	rocks = append(rocks, DriftRock{Cols: RasterColumns(g, low, ltris), Dir: [2]float64{1, 0}})
	res, err := Drift(g, nil, rocks, s, terrain.NewPool(context.Background(), 2))
	if err != nil {
		t.Fatal(err)
	}
	if res.Rocks != 1 || res.Small != 1 {
		t.Fatalf("%d rocks drifted, %d too small; want 1, 1", res.Rocks, res.Small)
	}
	at := func(x, z float64) float64 {
		i := int(math.Round((x - g.MinX) / g.Cell))
		j := int(math.Round((z - g.MinZ) / g.Cell))
		return float64(res.Rise[j*g.S()+i])
	}
	hc := s.Fillet * 2 // exposed height 2 m
	// Against the rock, about the contact height (±15% variation).
	if h := at(1.5, 0); h < 0.6*hc || h > 1.15*hc {
		t.Errorf("sand at the rock's lee face %.3f, want about %.2f", h, hc)
	}
	// Downwind it runs on further than upwind.
	if dn, up := at(4, 0), at(-4, 0); !(dn > 0.05 && up == 0) {
		t.Errorf("4 m downwind %.3f, upwind %.3f: the tail should run downwind only that far", dn, up)
	}
	// It falls away from the rock.
	if a, b := at(1.5, 0), at(3, 0); !(b < a) {
		t.Errorf("the drift does not fall away: %.3f at 1.5 m, %.3f at 3 m", a, b)
	}
	// None near the low rock, none past the edge band.
	if h := at(8.5, 8.5); h != 0 {
		t.Errorf("a rock under min_height got %.3f", h)
	}
	for k, v := range res.Rise {
		i, j := k%g.S(), k/g.S()
		if (i < s.Edge || j < s.Edge || i >= g.S()-s.Edge || j >= g.S()-s.Edge) && v != 0 {
			t.Fatalf("sand at the grid's edge (%d, %d)", i, j)
		}
	}
}

func TestDriftsCombineByTheLargerNotTheSum(t *testing.T) {
	g := flatGrid()
	s := DefaultDrift()
	s.Tail = 0
	a, at := boxMesh([3]float64{-3, 0, -1}, [3]float64{-1, 2, 1})
	b, bt := boxMesh([3]float64{1, 0, -1}, [3]float64{3, 2, 1})
	pool := terrain.NewPool(context.Background(), 2)
	both, err := Drift(g, nil, []DriftRock{{Cols: RasterColumns(g, a, at)}, {Cols: RasterColumns(g, b, bt)}}, s, pool)
	if err != nil {
		t.Fatal(err)
	}
	one, err := Drift(g, nil, []DriftRock{{Cols: RasterColumns(g, a, at)}}, s, pool)
	if err != nil {
		t.Fatal(err)
	}
	other, err := Drift(g, nil, []DriftRock{{Cols: RasterColumns(g, b, bt)}}, s, pool)
	if err != nil {
		t.Fatal(err)
	}
	for k := range both.Rise {
		if want := max(one.Rise[k], other.Rise[k]); both.Rise[k] != want {
			t.Fatalf("sample %d rose %.3f with both rocks, want the larger of theirs alone, %.3f", k, both.Rise[k], want)
		}
	}
	mid := (g.S()/2)*g.S() + g.S()/2 // (0, 0), between the two
	if both.Rise[mid] <= 0 {
		t.Error("no shared drift between rocks 2 m apart")
	}
}

// A cliff (its own shape) gets one smooth ramp at its foot: full height at
// the rock and falling steadily away from it (no dip beside the face, no
// spikes along it), nothing on the terrain's own steep wall behind the rock,
// and nothing along its rim, where the rock barely clears the top.
// (slope says the mesa's face is steep; a cliff's ramp needs no slope fade,
// and must not use one: it made spikes, and softened, a dip.)
func TestDriftRampsACliffFootNotItsRim(t *testing.T) {
	g := flatGrid()
	sz := g.S()
	// A mesa 10 m high for x > 2, its face steep between 1.75 and 2.25.
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			if g.X(i) > 2 {
				g.H[j*sz+i] = 10
			}
		}
	}
	slope := func(k int) float64 {
		if x := g.X(k % sz); x > 1.75 && x < 2.25 {
			return 85
		}
		return 0
	}
	// The wall's rock: a slab standing 1 m out from the mesa's face, from
	// under the floor to 0.1 m over its top, and lying 0.1 m proud of the
	// top for 3 m behind the rim.
	face, ft := boxMesh([3]float64{1, -0.2, -15}, [3]float64{2.4, 10.1, 15})
	top, tt := boxMesh([3]float64{2.4, 9.9, -15}, [3]float64{5.5, 10.1, 15})
	own := &DriftShape{Height: 2, Spread: 4, Vary: 0.3, VaryLength: 15, Reach: 2.4}
	res, err := Drift(g, slope, []DriftRock{{Cols: RasterColumns(g, face, ft), Own: own, Dir: [2]float64{-1, 0}},
		{Cols: RasterColumns(g, top, tt), Own: own, Dir: [2]float64{-1, 0}}}, DefaultDrift(), terrain.NewPool(context.Background(), 2))
	if err != nil {
		t.Fatal(err)
	}
	at := func(x, z float64) float64 {
		i := int(math.Round((x - g.MinX) / g.Cell))
		j := int(math.Round((z - g.MinZ) / g.Cell))
		return float64(res.Rise[j*sz+i])
	}
	for _, z := range []float64{-8, -3, 0, 4, 9} {
		if h := at(0.5, z); h < 1.0 {
			t.Errorf("z %g: sand at the cliff's foot %.2f, want about 2", z, h)
		}
		// Falling steadily away from the face: no dip beside it.
		prev := math.Inf(1)
		for x := 1.0; x > -12; x -= 0.5 {
			h := at(x, z)
			if h > prev+0.02 {
				t.Errorf("z %g: the sand rises again %.2f m out from the face (%.2f after %.2f)", z, 1-x, h, prev)
				break
			}
			prev = h
		}
		// The terrain's own wall behind the rock is never raised.
		for _, x := range []float64{2, 2.5} {
			if h := at(x, z); h != 0 {
				t.Errorf("z %g: the terrain's wall behind the rock rose %.2f at x %g", z, h, x)
			}
		}
		if h := at(7, z); h != 0 {
			t.Errorf("z %g: sand on the mesa top behind the rim: %.2f", z, h)
		}
	}
	// No spikes along the foot: neighbours differ by little.
	for z := -10.0; z < 10; z += 0.5 {
		if d := math.Abs(at(0.5, z+0.5) - at(0.5, z)); d > 0.15 {
			t.Errorf("a spike along the foot at z %g: %.2f between neighbours", z, d)
		}
	}
}

// On a bumpy foot — a hump 2 m out from the cliff, a hollow against it —
// the sand is one surface falling away from the wall: it fills the hollow
// and buries the hump, rather than rising over the hump and dipping beside
// the rock.
func TestCliffSandBuriesABumpyFoot(t *testing.T) {
	g := flatGrid()
	sz := g.S()
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			x := g.X(i)
			h := 0.9*math.Exp(-(x+1)*(x+1)/0.6) - 0.6*math.Exp(-(x-1.2)*(x-1.2)/0.3)
			if x > 2 {
				h = 10
			}
			g.H[j*sz+i] = float32(h)
		}
	}
	face, ft := boxMesh([3]float64{1.5, -1, -15}, [3]float64{2.4, 10.1, 15})
	top, tt := boxMesh([3]float64{2.4, 9.9, -15}, [3]float64{5.5, 10.1, 15})
	own := &DriftShape{Height: 2, Spread: 4, Vary: 0, VaryLength: 15, Reach: 2.4}
	res, err := Drift(g, nil, []DriftRock{{Cols: RasterColumns(g, face, ft), Own: own, Dir: [2]float64{-1, 0}},
		{Cols: RasterColumns(g, top, tt), Own: own, Dir: [2]float64{-1, 0}}}, DefaultDrift(), terrain.NewPool(context.Background(), 2))
	if err != nil {
		t.Fatal(err)
	}
	surf := func(x float64) float64 {
		i := int(math.Round((x - g.MinX) / g.Cell))
		k := (sz/2)*sz + i
		return float64(g.H[k] + res.Rise[k])
	}
	prev := math.Inf(1)
	for x := 1.5; x > -10; x -= 0.5 {
		s := surf(x)
		if s > prev+0.02 {
			t.Fatalf("the sand's surface rises again %.1f m from the wall: %.2f after %.2f", 1.5-x, s, prev)
		}
		prev = s
	}
	if s := surf(1.0); s < 1.5 {
		t.Errorf("the hollow against the wall is not filled: sand surface %.2f", s)
	}
}

// Where the floor at a cliff's foot steps up onto a bench, the sand does
// not step with it: no wall of sand where one foot's ramp hands over to
// another's.
func TestCliffSandHasNoStepsWhereTheFootChangesLevel(t *testing.T) {
	g := flatGrid()
	sz := g.S()
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			x, z := g.X(i), g.Z(j)
			h := 0.0
			if z > 0 {
				h = 4 / (1 + math.Exp(-(z-1.5)/0.4)) // a bench 4 m up for z > ~2
			}
			if x > 2 {
				h = 14
			}
			g.H[j*sz+i] = float32(h)
		}
	}
	face, ft := boxMesh([3]float64{1.5, -1, -15}, [3]float64{2.4, 14.1, 15})
	top, tt := boxMesh([3]float64{2.4, 13.9, -15}, [3]float64{5.5, 14.1, 15})
	own := &DriftShape{Height: 2, Spread: 4, Tail: 0.6, Vary: 0, VaryLength: 15, Reach: 2.4}
	res, err := Drift(g, nil, []DriftRock{{Cols: RasterColumns(g, face, ft), Own: own, Dir: [2]float64{-1, 0}},
		{Cols: RasterColumns(g, top, tt), Own: own, Dir: [2]float64{-1, 0}}}, DefaultDrift(), terrain.NewPool(context.Background(), 2))
	if err != nil {
		t.Fatal(err)
	}
	surf := func(x, z float64) float64 {
		i := int(math.Round((x - g.MinX) / g.Cell))
		j := int(math.Round((z - g.MinZ) / g.Cell))
		k := j*sz + i
		return float64(g.H[k] + res.Rise[k])
	}
	// Across the bench's edge, a few metres out from the wall, the sand's
	// surface may climb with the ground but never faster than the ground
	// itself or a ramp's own slope.
	if testing.Verbose() {
		for _, x := range []float64{0, 1} {
			line := ""
			for z := -8.0; z < 8; z += 0.5 {
				line += fmt.Sprintf("%.1f ", surf(x, z))
			}
			t.Logf("x %g: %s", x, line)
		}
	}
	for _, x := range []float64{0, -2, -4} {
		for z := -6.0; z < 8; z += 0.5 {
			ground := math.Abs(float64(g.Sample(x, z+0.5) - g.Sample(x, z)))
			if d := math.Abs(surf(x, z+0.5) - surf(x, z)); d > ground+0.35 {
				t.Errorf("x %g z %g: the sand's surface steps %.2f m in 0.5 m (the ground %.2f)", x, z, d, ground)
			}
		}
	}
}

// A ledge partway up a cliff — flat ground under the rock, beside steeper
// ground above it — starts no ramp of its own: no streak of sand up the
// face.
func TestCliffSandLeavesLedgesUpTheFaceBare(t *testing.T) {
	g := flatGrid()
	sz := g.S()
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			x := g.X(i)
			var h float64
			switch {
			case x <= 2:
				h = 0
			case x < 4:
				h = (x - 2) * 3 // 6 m up by x = 4
			case x <= 5:
				h = 6 // the ledge
			case x < 7:
				h = 6 + (x-5)*3
			default:
				h = 12
			}
			g.H[j*sz+i] = float32(h)
		}
	}
	face, ft := boxMesh([3]float64{1.5, -1, -15}, [3]float64{7.5, 12.1, 15})
	own := &DriftShape{Height: 2, Spread: 4, Vary: 0, VaryLength: 15, Reach: 2.4}
	res, err := Drift(g, nil, []DriftRock{{Cols: RasterColumns(g, face, ft), Own: own, Dir: [2]float64{-1, 0}}},
		DefaultDrift(), terrain.NewPool(context.Background(), 2))
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []float64{4, 4.5, 5} {
		i := int(math.Round((x - g.MinX) / g.Cell))
		if r := res.Rise[(sz/2)*sz+i]; r != 0 {
			t.Errorf("sand on the ledge at x %g: %.2f", x, r)
		}
	}
	i := int(math.Round((0.5 - g.MinX) / g.Cell))
	if r := res.Rise[(sz/2)*sz+i]; r < 1 {
		t.Errorf("sand at the foot %.2f", r)
	}
}
