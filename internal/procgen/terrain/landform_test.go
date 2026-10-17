package terrain

// landform_test.go — the landforms of landform.go: each one off is the
// classic field to the byte, each one on makes the shape it says.
//
// FOUNDRY_LANDFORM_DUMP=<dir> writes every case's shaded relief (and its
// heights) as <case>_relief.png for a person to look at:
//
//	FOUNDRY_LANDFORM_DUMP=/tmp/lf go test ./internal/procgen/terrain -run TestLandform -v

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// plainSettings is a gentle lowland with no mesas, eroded lightly: the
// ground the landforms are tested on.
func plainSettings(size, cell float64) Settings {
	s := Defaults()
	s.Seed = 77
	s.Size, s.Cell = size, cell
	s.Base = BaseShape{Height: 0, Relief: 3, Frequency: 0.004, Octaves: 4, Gain: 0.5, Warp: 40}
	s.Mesa.Height = 0
	s.Strata.Terrace = 0
	s.Drainage.Strength = 0
	s.Droplets.Density = 0
	s.Thermal.Iterations = 20
	s.Detail.Amplitude = 0
	return s
}

func genLandform(t *testing.T, name string, s Settings) *Result {
	t.Helper()
	res, err := Generate(&s, NewPool(context.Background(), 4), nil)
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("FOUNDRY_LANDFORM_DUMP"); dir != "" {
		os.MkdirAll(dir, 0o755)
		meta := NewMeta(&s, res)
		files, err := Encode(res, meta, NewPool(context.Background(), 4))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{ReliefFile, HeightmapFile} {
			if err := os.WriteFile(filepath.Join(dir, name+"_"+f), files[f], 0o644); err != nil {
				t.Fatal(err)
			}
		}
		lo, hi := res.Grid.Range()
		t.Logf("%s: heights %.1f .. %.1f m -> %s", name, lo, hi, dir)
	}
	return res
}

func TestLandformOffIsClassic(t *testing.T) {
	s := plainSettings(256, 2)
	a, err := Generate(&s, NewPool(context.Background(), 2), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Landform params that are set but switched off by their own zero.
	s.Landform = Landform{Ridge: Ridge{Scale: 400, Octaves: 5}, Dunes: Dunes{Wavelength: 80}, Coast: Coast{Side: "off", SeaLevel: 3},
		Volcano: Volcano{Height: 100, Radius: 200}, Hummocks: Hummocks{Spacing: 5}}
	if s.Landform.Any() {
		t.Fatal("switched-off landforms report on")
	}
	b, err := Generate(&s, NewPool(context.Background(), 2), nil)
	if err != nil {
		t.Fatal(err)
	}
	if ResultHash(a) != ResultHash(b) {
		t.Fatal("landforms that are off changed the field")
	}
	if len(b.Vents) != 0 {
		t.Fatal("vents without volcanoes")
	}
}

// slopeDeg is the slope at sample k of g, degrees (central differences).
func slopeDeg(g *Grid, i, j int) float64 {
	dx := float64(g.At(i+1, j)-g.At(i-1, j)) / (2 * g.Cell)
	dz := float64(g.At(i, j+1)-g.At(i, j-1)) / (2 * g.Cell)
	return math.Atan(math.Hypot(dx, dz)) * 180 / math.Pi
}

func TestLandformRidgedMountains(t *testing.T) {
	mk := func(valleys float64) Settings {
		s := plainSettings(1024, 2)
		s.Thermal = Thermal{Iterations: 40, TalusAngle: 38, CliffAngle: 60, Rate: 0.5}
		// Eroded as an alpine preset would be: the drainage cuts the ranges
		// into spurs and gullies.
		s.Drainage = Drainage{Strength: 1.5, Iterations: 60, Cell: 4, Exponent: 0.5, Fill: 0.1, Diffusion: 0.05}
		s.Strata.Layers = []Layer{{Thickness: 50, Hardness: 0.7, Color: [3]float64{0.55, 0.55, 0.55}}}
		s.Landform.Ridge = Ridge{Height: 320, Scale: 1400, Octaves: 5, Valleys: valleys, Warp: 150}
		return s
	}
	// The lowest quarter of the ground (by height): the share of it gentler
	// than 12°.
	floors := func(g *Grid) float64 {
		hs := make([]float64, 0, g.N*g.N)
		for j := 1; j < g.N; j++ {
			for i := 1; i < g.N; i++ {
				hs = append(hs, float64(g.At(i, j)))
			}
		}
		sortFloats(hs)
		cut := hs[len(hs)/4]
		low, flat := 0, 0
		for j := 1; j < g.N; j++ {
			for i := 1; i < g.N; i++ {
				if float64(g.At(i, j)) <= cut {
					low++
					if slopeDeg(g, i, j) < 12 {
						flat++
					}
				}
			}
		}
		return float64(flat) / float64(low)
	}
	res := genLandform(t, "ridges", mk(0.6))
	g := res.Grid
	lo, hi := g.Range()
	if hi-lo < 150 {
		t.Fatalf("mountains of only %.0f m relief", hi-lo)
	}
	steep := 0
	for j := 1; j < g.N; j++ {
		for i := 1; i < g.N; i++ {
			if slopeDeg(g, i, j) > 30 {
				steep++
			}
		}
	}
	n := g.N * g.N
	v := genLandform(t, "ridges_v", mk(0))
	fu, fv := floors(g), floors(v.Grid)
	t.Logf("relief %.0f m, %d of %d samples steeper than 30°; gentle low ground: U %.0f%%, V %.0f%%", hi-lo, steep, n, 100*fu, 100*fv)
	if steep < n/20 {
		t.Errorf("only %d of %d samples steeper than 30°", steep, n)
	}
	// Glacial valleys widen the flat floors.
	if fu < 1.25*fv {
		t.Errorf("valley floors: %.0f%% of the low ground gentle with glacial valleys, %.0f%% without", 100*fu, 100*fv)
	}
}

func TestLandformDunes(t *testing.T) {
	s := plainSettings(512, 1)
	s.Thermal = Thermal{Iterations: 30, TalusAngle: 33, CliffAngle: 34, Rate: 0.5}
	s.Landform.Dunes = Dunes{Height: 14, Wavelength: 110, Wind: 0, Asymmetry: 0.9, Sinuosity: 0.5, Cover: 0.8,
		RippleHeight: 0.06, RippleWavelength: 4}
	res := genLandform(t, "dunes", s)
	g := res.Grid
	// The wind blows toward +X: the lee faces (falling toward +X) are short
	// and steep, the stoss slopes (rising toward +X) long and gentle. Over
	// the field, the steepest descents along +X outdo the steepest ascents.
	var up, down []float64
	for j := 0; j <= g.N; j += 4 {
		for i := 1; i < g.N; i++ {
			d := float64(g.At(i+1, j)-g.At(i-1, j)) / (2 * g.Cell)
			if d > 0.02 {
				up = append(up, d)
			} else if d < -0.02 {
				down = append(down, -d)
			}
		}
	}
	p95 := func(v []float64) float64 {
		if len(v) == 0 {
			return 0
		}
		c := append([]float64(nil), v...)
		sortFloats(c)
		return c[len(c)*95/100]
	}
	if len(up) == 0 || len(down) == 0 {
		t.Fatal("no dunes")
	}
	pu, pd := p95(up), p95(down)
	t.Logf("stoss p95 %.1f°, lee p95 %.1f°, stoss samples %d, lee samples %d",
		math.Atan(pu)*180/math.Pi, math.Atan(pd)*180/math.Pi, len(up), len(down))
	if pd < 1.8*pu {
		t.Errorf("lee faces (p95 %.3f) not much steeper than stoss slopes (p95 %.3f)", pd, pu)
	}
	if len(up) < 2*len(down) {
		t.Errorf("stoss slopes (%d samples) should cover far more ground than lee faces (%d)", len(up), len(down))
	}
	if math.Atan(pd)*180/math.Pi > 40 {
		t.Errorf("lee faces at %.1f°, past the angle of repose", math.Atan(pd)*180/math.Pi)
	}
}

func TestLandformCoast(t *testing.T) {
	s := plainSettings(1024, 2)
	s.Base.Relief = 18
	s.Landform.Coast = Coast{Side: "+x", SeaLevel: 0, Offset: 300, Meander: 40, Beach: 30, BeachRise: 2.5,
		Shelf: 150, Depth: 12, Cliff: 18, CliffWidth: 10, Inland: 120}
	res := genLandform(t, "coast", s)
	g := res.Grid
	// Deep at the sea edge, high inland, everything below sea level on the
	// sea side of the shoreline.
	sea, land := 0, 0
	for j := 0; j <= g.N; j += 8 {
		if h := g.At(g.N, j); h < -6 {
			sea++
		}
		if h := g.At(0, j); h > 10 {
			land++
		}
	}
	rows := g.N/8 + 1
	if sea < rows*9/10 || land < rows*7/10 {
		t.Errorf("sea edge deep on %d of %d rows, land edge high on %d", sea, rows, land)
	}
	// The wave-cut cliff: somewhere along each row a steep rise inland of
	// the shore.
	cliffs := 0
	for j := 0; j <= g.N; j += 32 {
		for i := g.N / 3; i < g.N; i++ {
			if g.At(i, j) > 0 && slopeDeg(g, max(i, 1), max(min(j, g.N-1), 1)) > 40 {
				cliffs++
				break
			}
		}
	}
	if cliffs < (g.N/32+1)*3/4 {
		t.Errorf("a cliff on only %d of %d rows", cliffs, g.N/32+1)
	}
}

func TestLandformCoastLine(t *testing.T) {
	// A drawn shoreline from north to south: walking it, the sea is on the
	// right, which is west (−X) on a +Z-down map.
	s := plainSettings(512, 2)
	s.Landform.Coast = Coast{Side: CoastLine, Line: [][2]float64{{0, -300}, {40, 0}, {0, 300}}, SeaLevel: 0,
		Beach: 20, BeachRise: 2, Shelf: 80, Depth: 10, Inland: 60}
	res := genLandform(t, "coast_line", s)
	g := res.Grid
	if w, e := g.Sample(-200, 0), g.Sample(200, 0); !(w < -5 && e > 1) {
		t.Fatalf("west %.1f m, east %.1f m: the sea should be west of the line", w, e)
	}
}

func TestLandformVolcanoes(t *testing.T) {
	s := plainSettings(1024, 2)
	s.Thermal = Thermal{Iterations: 30, TalusAngle: 34, CliffAngle: 55, Rate: 0.5}
	s.Landform.Volcano = Volcano{Count: 2, Height: 160, Radius: 260, Crater: 0.18, CraterDepth: 40,
		Lobes: 4, LobeLength: 320, LobeWidth: 50, LobeThickness: 5}
	res := genLandform(t, "volcanoes", s)
	if len(res.Vents) != 2 {
		t.Fatalf("%d vents placed, want 2", len(res.Vents))
	}
	g := res.Grid
	for _, v := range res.Vents {
		c := g.Sample(v.X, v.Z)
		rim := float32(-1e9)
		for a := 0.0; a < 2*math.Pi; a += math.Pi / 16 {
			r := v.Radius * 0.18
			rim = max(rim, g.Sample(v.X+r*math.Cos(a), v.Z+r*math.Sin(a)))
		}
		foot := g.Sample(v.X+v.Radius*1.05, v.Z)
		t.Logf("vent (%.0f, %.0f) r %.0f: crater floor %.1f, rim %.1f, foot %.1f", v.X, v.Z, v.Radius, c, rim, foot)
		if !(rim > c+15 && rim > foot+80) {
			t.Errorf("vent at (%.0f, %.0f): crater %.1f, rim %.1f, foot %.1f", v.X, v.Z, c, rim, foot)
		}
	}
	meta := NewMeta(&s, res)
	if len(meta.Vents) != 2 {
		t.Fatalf("terrain.yaml lists %d vents", len(meta.Vents))
	}
	// Placement is a pure function of the settings.
	if again := Vents(&s, CentredSquare(s.Size), [2]float64{}); again[0] != res.Vents[0] {
		t.Fatal("vents differ run to run")
	}
}

func TestLandformHummocksAndPonds(t *testing.T) {
	s := plainSettings(512, 1)
	s.Base.Relief = 2
	s.Landform.Hummocks = Hummocks{Height: 0.8, Spacing: 6, Ponds: 0.15, PondDepth: 1.5, PondScale: 70}
	res := genLandform(t, "hummocks", s)
	g := res.Grid
	// Closed hollows: the priority flood fills some ground by more than a
	// metre.
	filled := g.Clone()
	fillPits(filled)
	deep := 0
	for k := range g.H {
		if filled.H[k]-g.H[k] > 0.8 {
			deep++
		}
	}
	share := float64(deep) / float64(len(g.H))
	t.Logf("%.1f%% of the ground lies more than 0.8 m deep in a closed hollow", 100*share)
	if share < 0.03 || share > 0.4 {
		t.Errorf("ponds cover %.1f%% of the ground, want a few to a third", 100*share)
	}
}

func sortFloats(v []float64) { sort.Float64s(v) }
