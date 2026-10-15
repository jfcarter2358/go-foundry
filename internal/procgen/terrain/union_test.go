package terrain

// union_test.go — GenerateUnion: the one-provider identity with Generate,
// the blend's weights, the shared erosion over a seam, tiled erosion's
// locality and its windows' partition of unity, and determinism.

import (
	"context"
	"math"
	"testing"

	"foundry/internal/procgen/noise"
)

// oneProvider is s as a union of one provider over Generate's own square.
func oneProvider(s Settings) *Union {
	n := s.Cells()
	return &Union{N: n, Cell: s.Cell, MinX: -s.Size / 2, MinZ: -s.Size / 2, Seed: 99,
		Providers: []Provider{{Name: "only", Settings: s, Measure: CentredSquare(s.Size)}}}
}

// THE IDENTITY: one provider over the classic square is Generate, bit for
// bit, with or without a weight function (whose shares, normalised, need
// not be exactly 1 — the blend must not do arithmetic on them).
func TestUnionOfOneIsGenerate(t *testing.T) {
	for name, s := range goldenSettings() {
		s := s
		want, err := Generate(&s, NewPool(context.Background(), 4), nil)
		if err != nil {
			t.Fatal(err)
		}
		u := oneProvider(s)
		got, err := GenerateUnion(u, NewPool(context.Background(), 3), nil)
		if err != nil {
			t.Fatal(err)
		}
		if a, b := ResultHash(want), ResultHash(got); a != b {
			t.Errorf("%s: the union of one differs from Generate", name)
		}
		u.Weights = func(x, z float64, out []RegionWeight) []RegionWeight {
			return append(out[:0], RegionWeight{0, 1 - 1e-16})
		}
		got, err = GenerateUnion(u, NewPool(context.Background(), 2), nil)
		if err != nil {
			t.Fatal(err)
		}
		if a, b := ResultHash(want), ResultHash(got); a != b {
			t.Errorf("%s: with a weight function the union of one differs from Generate", name)
		}
	}
}

// And its backdrop is the classic one.
func TestUnionFarOfOneIsTheFarField(t *testing.T) {
	s := small()
	s.Droplets.Density, s.Thermal.Iterations = 0.2, 5
	res, err := Generate(&s, NewPool(context.Background(), 4), nil)
	if err != nil {
		t.Fatal(err)
	}
	u := oneProvider(s)
	ures, err := GenerateUnion(u, NewPool(context.Background(), 4), nil)
	if err != nil {
		t.Fatal(err)
	}
	a := NewFarField(&s, res, 16, 100, 1000)
	b := NewUnionFar(u, ures, 16, 100, 1000)
	for _, q := range [][2]float64{{0, 0}, {140, 10}, {-300, 250}, {700, -900}, {129, 128}} {
		if x, y := a.Height(q[0], q[1]), b.Height(q[0], q[1]); x != y {
			t.Errorf("at %v: far field %v, union far %v", q, x, y)
		}
	}
}

// stripes is a weight function for providers in a row along X: provider i
// owns x between edges[i-1] and edges[i], blended over band.
func stripes(edges []float64, band float64) WeightFunc {
	return func(x, z float64, out []RegionWeight) []RegionWeight {
		out = out[:0]
		r := 0
		for r < len(edges) && x > edges[r]+band/2 {
			r++
		}
		if r < len(edges) && x > edges[r]-band/2 {
			t := noise.SmoothStep(edges[r]-band/2, edges[r]+band/2, x)
			return append(out, RegionWeight{r, 1 - t}, RegionWeight{r + 1, t})
		}
		return append(out, RegionWeight{r, 1})
	}
}

// twoPlains is a union of a high plain (west) and a low one (east), meeting
// at x = 0 over a 60 m band, 256 m at 1 m.
func twoPlains() *Union {
	a := Defaults()
	a.Cell, a.Seed = 1, 5
	a.Mesa.Height = 0
	a.Base.Height, a.Base.Relief = 20, 4
	a.Droplets.Density, a.Thermal.Iterations, a.Drainage.Iterations = 0.3, 10, 20
	a.Strata.Terrace = 0 // plains: no ledges, so a step is the blend's
	b := a
	b.Seed = 6
	b.Base.Height = 0
	b.Strata.Layers = []Layer{{Thickness: 20, Hardness: 0.1, Color: [3]float64{0.6, 0.6, 0.3}}}
	return &Union{N: 256, Cell: 1, MinX: -128, MinZ: -128, Seed: 3, Weights: stripes([]float64{0}, 60),
		Providers: []Provider{
			{Name: "west", Settings: a, Measure: Rect{-128, -128, 0, 128}},
			{Name: "east", Settings: b, Measure: Rect{0, -128, 128, 128}},
		}}
}

func TestUnionBlendsAcrossTheBand(t *testing.T) {
	u := twoPlains()
	res, err := GenerateUnion(u, NewPool(context.Background(), 4), nil)
	if err != nil {
		t.Fatal(err)
	}
	g := res.Grid
	wa, wb := res.RegionWeights(0), res.RegionWeights(1)
	for k := range wa {
		if d := wa[k] + wb[k] - 1; math.Abs(float64(d)) > 1e-6 {
			t.Fatalf("sample %d: shares %v + %v", k, wa[k], wb[k])
		}
	}
	// Owned outright away from the band; shared inside it.
	at := func(x, z float64) int {
		return int(math.Round((z-g.MinZ)/g.Cell))*g.S() + int(math.Round((x-g.MinX)/g.Cell))
	}
	if wa[at(-60, 0)] != 1 || wb[at(60, 0)] != 1 || wa[at(0, 0)] <= 0.3 || wa[at(0, 0)] >= 0.7 {
		t.Errorf("shares: west at -60 %v, east at 60 %v, west at 0 %v", wa[at(-60, 0)], wb[at(60, 0)], wa[at(0, 0)])
	}
	// The high plain steps down to the low one through the band: on
	// average west of it is ~20 m higher, and no neighbour-to-neighbour
	// step anywhere is a cliff.
	mean := func(x0, x1 float64) float64 {
		sum, n := 0.0, 0
		for j := 0; j < g.S(); j++ {
			for i := 0; i < g.S(); i++ {
				if x := g.X(i); x >= x0 && x <= x1 {
					sum += float64(g.H[j*g.S()+i])
					n++
				}
			}
		}
		return sum / float64(n)
	}
	if d := mean(-120, -40) - mean(40, 120); d < 12 || d > 28 {
		t.Errorf("the west is %.1f m above the east, want about 20", d)
	}
	worst := 0.0
	for j := 1; j < g.S(); j++ {
		for i := 1; i < g.S(); i++ {
			k := j*g.S() + i
			worst = math.Max(worst, math.Abs(float64(g.H[k]-g.H[k-1])))
			worst = math.Max(worst, math.Abs(float64(g.H[k]-g.H[k-g.S()])))
		}
	}
	if worst > 3 {
		t.Errorf("a %.2f m step between neighbouring samples", worst)
	}
	// Each region's colours: the albedo hint is the east's sandy olive deep
	// in the east and the west's red deep in the west.
	ae, aw := res.Albedo[3*at(100, 0):3*at(100, 0)+3], res.Albedo[3*at(-100, 0):3*at(-100, 0)+3]
	if !(ae[1] > aw[1]) {
		t.Errorf("albedo west %v, east %v: the east's layer is greener", aw, ae)
	}
}

func TestUnionIsTheSameOnAnyWorkerCount(t *testing.T) {
	for _, mode := range []string{ErosionGlobal, ErosionTiled} {
		a, b := twoPlains(), twoPlains()
		a.Erosion, b.Erosion = mode, mode
		a.Window, a.Margin, b.Window, b.Margin = 64, 16, 64, 16
		ra, err := GenerateUnion(a, NewPool(context.Background(), 1), nil)
		if err != nil {
			t.Fatal(err)
		}
		rb, err := GenerateUnion(b, NewPool(context.Background(), 7), nil)
		if err != nil {
			t.Fatal(err)
		}
		if ResultHash(ra) != ResultHash(rb) {
			t.Errorf("%s: 1 worker and 7 differ", mode)
		}
	}
}

// The windows' fades are a partition of unity, whatever the margin.
func TestTiledWindowsPartitionUnity(t *testing.T) {
	for _, c := range []struct{ n, w, m int }{{256, 64, 16}, {256, 64, 0}, {250, 64, 30}, {100, 40, 70}} {
		spans := windowSpans(c.n, c.w, c.m+1)
		for i := 0; i <= c.n; i++ {
			sum := float32(0)
			for a, sp := range spans {
				sum += fade(i, sp, a == 0, a == len(spans)-1, float64(c.m)/2)
			}
			if math.Abs(float64(sum-1)) > 1e-6 {
				t.Fatalf("n %d window %d margin %d: weights at %d sum to %v (spans %v)", c.n, c.w, c.m, i, sum, spans)
			}
		}
	}
}

// Tiled erosion is local: change the far east provider and the west
// window's samples keep their heights to the bit; the samples near the east
// do change.
func TestTiledErosionIsLocal(t *testing.T) {
	mk := func(eastRelief float64) *Union {
		u := twoPlains()
		c := u.Providers[1]
		c.Name = "far"
		c.Settings.Seed = 9
		c.Settings.Base.Relief = eastRelief
		c.Measure = Rect{64, -128, 128, 128}
		u.Providers[1].Measure = Rect{0, -128, 64, 128}
		u.Providers = append(u.Providers, c)
		u.Weights = stripes([]float64{0, 80}, 30)
		u.Erosion, u.Window, u.Margin = ErosionTiled, 64, 16
		return u
	}
	ra, err := GenerateUnion(mk(4), NewPool(context.Background(), 4), nil)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := GenerateUnion(mk(9), NewPool(context.Background(), 4), nil)
	if err != nil {
		t.Fatal(err)
	}
	g := ra.Grid
	same, changed := 0, 0
	for j := 0; j < g.S(); j++ {
		for i := 0; i < g.S(); i++ {
			k := j*g.S() + i
			x := g.X(i)
			// The west window's core [-128, -64] less its fade: depends on
			// ground out to -64 + 8 + 16 at most.
			if x <= -64-8 {
				if ra.Grid.H[k] != rb.Grid.H[k] {
					t.Fatalf("sample (%d, %d) at x %.0f: %v then %v", i, j, x, ra.Grid.H[k], rb.Grid.H[k])
				}
				same++
			}
			if x > 100 && ra.Grid.H[k] != rb.Grid.H[k] {
				changed++
			}
		}
	}
	if same == 0 || changed == 0 {
		t.Errorf("%d samples compared, %d changed in the east: the test does not test", same, changed)
	}
}
