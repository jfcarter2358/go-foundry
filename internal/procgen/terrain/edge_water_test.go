package terrain

// edge_water_test.go — the edge fade (edge.go) and standing water
// (water.go).

import (
	"context"
	"math"
	"testing"
)

// mountainPlain is a plain (west) beside ridged mountains (east), meeting at
// x = 0 over an 80 m band, 512 m at 2 m; rise is the mountains' edge rise.
func mountainPlain(rise float64) *Union {
	a := Defaults()
	a.Cell, a.Seed = 2, 5
	a.Mesa.Height = 0
	a.Base.Height, a.Base.Relief = 0, 2
	a.Droplets.Density, a.Thermal.Iterations, a.Drainage.Iterations = 0, 10, 20
	a.Strata.Terrace = 0
	b := a
	b.Seed = 6
	b.Landform.Ridge = Ridge{Height: 200, Scale: 500, Octaves: 4, Warp: 60}
	b.Edge = Edge{Level: 0, Rise: rise}
	return &Union{N: 256, Cell: 2, MinX: -256, MinZ: -256, Seed: 3, Weights: stripes([]float64{0}, 80),
		Providers: []Provider{
			{Name: "plain", Settings: a, Measure: Rect{-256, -256, 0, 256}},
			{Name: "mountains", Settings: b, Measure: Rect{0, -256, 256, 256}},
		}}
}

func TestEdgeRiseMeetsTheNeighbourAtItsLevel(t *testing.T) {
	mean := func(res *Result, x0, x1 float64) float64 {
		g := res.Grid
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
	steepest := func(res *Result, x0, x1 float64) float64 {
		g := res.Grid
		worst := 0.0
		for j := 1; j < g.N; j++ {
			for i := 1; i < g.N; i++ {
				if x := g.X(i); x >= x0 && x <= x1 {
					worst = math.Max(worst, slopeDeg(g, i, j))
				}
			}
		}
		return worst
	}
	off, err := GenerateUnion(mountainPlain(0), NewPool(context.Background(), 4), nil)
	if err != nil {
		t.Fatal(err)
	}
	on, err := GenerateUnion(mountainPlain(160), NewPool(context.Background(), 4), nil)
	if err != nil {
		t.Fatal(err)
	}
	bandOff, bandOn := mean(off, -40, 40), mean(on, -40, 40)
	deepOff, deepOn := mean(off, 200, 250), mean(on, 200, 250)
	t.Logf("band mean %.1f -> %.1f m; deep in the range %.1f -> %.1f m; band steepest %.0f° -> %.0f°",
		bandOff, bandOn, deepOff, deepOn, steepest(off, -40, 40), steepest(on, -40, 40))
	// Across the band the mountains stand at the plain's level...
	if math.Abs(bandOn) > 3 || math.Abs(bandOn) > 0.3*math.Abs(bandOff) {
		t.Errorf("band mean %.1f m with the edge rise (%.1f without): want near the level, 0", bandOn, bandOff)
	}
	// ...and rise back to the full range inside their own ground.
	if math.Abs(deepOn-deepOff) > 0.05*math.Abs(deepOff)+1 {
		t.Errorf("deep in the range %.1f m with the edge rise, %.1f without", deepOn, deepOff)
	}
}

// One provider owns every sample: nothing borders it, so the edge fade
// leaves it exactly as Generate makes it.
func TestEdgeRiseLeavesOneRegionAlone(t *testing.T) {
	s := plainSettings(256, 2)
	s.Landform.Ridge = Ridge{Height: 80, Scale: 300, Octaves: 3}
	want, err := Generate(&s, NewPool(context.Background(), 2), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Edge = Edge{Level: 0, Rise: 100}
	got, err := GenerateUnion(oneProvider(s), NewPool(context.Background(), 2), nil)
	if err != nil {
		t.Fatal(err)
	}
	if ResultHash(want) != ResultHash(got) {
		t.Fatal("an edge rise changed a region with no neighbour")
	}
}

func TestChamferDistance(t *testing.T) {
	const s = 9
	d := make([]float32, s*s)
	for k := range d {
		d[k] = float32(math.Inf(1))
	}
	d[4*s+4] = 0
	chamfer(d, s)
	if d[4*s+8] != 4 || math.Abs(float64(d[0])-4*math.Sqrt2) > 1e-5 || d[4*s+4] != 0 {
		t.Fatalf("distances %v %v %v", d[4*s+8], d[0], d[4*s+4])
	}
}

func TestFindWaterLevelsAndPonds(t *testing.T) {
	// A 64 m square at 1 m: a slope from -4 m (west) to 4 m (east) with a
	// 2 m-deep square hollow cut into its high, east half.
	g := NewGrid(64, 1)
	s := g.S()
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			h := float32(i-32) / 8
			if i >= 44 && i <= 54 && j >= 20 && j <= 30 {
				h -= 2
			}
			g.H[j*s+i] = h
		}
	}
	own := func(int) int { return 0 }
	// A level at 0: the west half (ground below 0) is wet, the hollow is
	// not (its floor is above 0).
	w := FindWater(g, own, []WaterRule{{HasLevel: true, Level: 0}})
	if len(w.Bodies) != 1 || w.Bodies[0].Pond || w.Bodies[0].Level != 0 {
		t.Fatalf("bodies %+v", w.Bodies)
	}
	if w.Body[10*s+5] != 0 || w.Body[10*s+40] != -1 || w.Body[25*s+50] != -1 {
		t.Fatal("the level floods the wrong ground")
	}
	if d := w.Depth(g, 10*s); math.Abs(float64(d)-4) > 1e-5 {
		t.Fatalf("depth at the west edge %v, want 4", d)
	}
	// Ponds: the hollow fills to just under its spill (its lowest rim
	// sample, the west side, 12/8-ish m) and nothing else does.
	w = FindWater(g, own, []WaterRule{{Ponds: true, MinDepth: 0.5, MinArea: 10}})
	if len(w.Bodies) != 1 || !w.Bodies[0].Pond {
		t.Fatalf("bodies %+v", w.Bodies)
	}
	spill := float32(43-32) / 8
	if lv := w.Bodies[0].Level; math.Abs(lv-float64(spill-pondFreeboard)) > 1e-4 {
		t.Errorf("pond level %.3f, want the spill %.3f less the freeboard", lv, spill)
	}
	if w.Body[25*s+45] != 0 || w.Body[25*s+10] != -1 {
		t.Error("the pond is in the wrong place")
	}
	// Too small a pond is left dry.
	if w = FindWater(g, own, []WaterRule{{Ponds: true, MinDepth: 0.5, MinArea: 1000}}); w.Wet() {
		t.Error("a pond smaller than its minimum area filled")
	}
	// A region without water rules stays dry.
	if w = FindWater(g, own, nil); w.Wet() {
		t.Error("water with no rule")
	}
}
