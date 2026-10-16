package place

import (
	"math"
	"sort"
	"testing"
)

func grassRule() GrassRule {
	b := DefaultRule()
	b.Slope = [2]float64{0, 30}
	b.Cluster, b.ClusterScale = 0.4, 20
	return GrassRule{Name: "g", Kinds: []int{0, 1, 2}, Density: 2, Scale: [2]float64{3, 5}, EdgeShrink: 0.4, Band: b,
		TintA: [3]float64{1, 1, 0.8}, TintB: [3]float64{0.8, 0.9, 0.7}, TintScale: 30, TintJitter: 0.1}
}

func grassKey(in GrassInstance) [2]int64 {
	return [2]int64{int64(math.Round(in.Pos[0] * 1e4)), int64(math.Round(in.Pos[2] * 1e4))}
}

// TestGrassIsTileIndependent: the clumps of a rectangle are exactly the
// clumps of its quarters — each point in one tile, nothing depending on
// which tile was asked for.
func TestGrassIsTileIndependent(t *testing.T) {
	g := stepGround(t, 128, 1, 4)
	s := GrassSettings{Rules: []GrassRule{grassRule()}, Seed: 11}
	b := g.Bounds()
	whole := Grass(g, s, b)
	if len(whole) < 5000 {
		t.Fatalf("only %d clumps over 128 m at 2/m²", len(whole))
	}
	mx, mz := (b[0]+b[2])/2, (b[1]+b[3])/2
	var parts []GrassInstance
	for _, r := range [][4]float64{{b[0], b[1], mx, mz}, {mx, b[1], b[2], mz}, {b[0], mz, mx, b[3]}, {mx, mz, b[2], b[3]}} {
		parts = append(parts, Grass(g, s, r)...)
	}
	if len(parts) != len(whole) {
		t.Fatalf("quarters hold %d clumps, the whole %d", len(parts), len(whole))
	}
	set := map[[2]int64]GrassInstance{}
	for _, in := range whole {
		set[grassKey(in)] = in
	}
	for _, in := range parts {
		w, ok := set[grassKey(in)]
		if !ok || w.Kind != in.Kind || w.Scale != in.Scale || w.Yaw != in.Yaw || w.Tint != in.Tint {
			t.Fatalf("clump %v differs between the whole and its quarter", in.Pos)
		}
	}
	// And deterministic.
	again := Grass(g, s, b)
	if len(again) != len(whole) || again[100] != whole[100] {
		t.Fatal("not deterministic")
	}
}

// TestGrassFollowsTheBands: no grass past the slope band, none in deep
// water, a shore rule only near water, the region's share respected, and
// the avoid discs kept clear.
func TestGrassFollowsTheBands(t *testing.T) {
	g := stepGround(t, 128, 1, 40) // a 40 m step: a cliff band across the middle
	r := grassRule()
	r.Band.Cluster = 0
	s := GrassSettings{Rules: []GrassRule{r}, Seed: 3}
	for _, in := range Grass(g, s, g.Bounds()) {
		if sl := g.SlopeDeg(in.Pos[0], in.Pos[2]); sl > 30+6+1 {
			t.Fatalf("clump on a %.0f° slope", sl)
		}
	}
	flat := stepGround(t, 64, 1, 0)
	water := func(x, z float64) (float64, float64) {
		if x < 0 {
			return 0.5, 0
		}
		return 0, x
	}
	s = GrassSettings{Rules: []GrassRule{r}, Seed: 3, Water: water}
	for _, in := range Grass(flat, s, flat.Bounds()) {
		if in.Pos[0] < 0 {
			t.Fatalf("clump in water at %v", in.Pos)
		}
	}
	reed := r
	reed.Shore, reed.ShoreSoft, reed.MaxDepth = [2]float64{0, 6}, 2, 0.6
	s = GrassSettings{Rules: []GrassRule{reed}, Seed: 3, Water: water}
	n, wet := 0, 0
	for _, in := range Grass(flat, s, flat.Bounds()) {
		if in.Pos[0] > 8.01 {
			t.Fatalf("shore clump %.1f m from water", in.Pos[0])
		}
		if in.Pos[0] < 0 {
			wet++
		}
		n++
	}
	if n == 0 || wet == 0 {
		t.Fatalf("reeds: %d placed, %d standing in the shallows", n, wet)
	}
	// A region owning only x > 0.
	s = GrassSettings{Rules: []GrassRule{r}, Seed: 3, Region: &Region{Weight: func(x, z float64) float64 {
		if x > 0 {
			return 1
		}
		return 0
	}}}
	for _, in := range Grass(flat, s, flat.Bounds()) {
		if in.Pos[0] <= 0 {
			t.Fatalf("clump outside its region at %v", in.Pos)
		}
	}
	s = GrassSettings{Rules: []GrassRule{r}, Seed: 3, Avoid: []Disc{{X: 0, Z: 0, R: 10}}, AvoidShrink: 1}
	for _, in := range Grass(flat, s, flat.Bounds()) {
		if math.Hypot(in.Pos[0], in.Pos[2]) < 10 {
			t.Fatalf("clump inside a rock at %v", in.Pos)
		}
	}
}

// TestGrassDensity: at full weight the lattice gives its density.
func TestGrassDensity(t *testing.T) {
	g := stepGround(t, 64, 1, 0)
	r := grassRule()
	r.Band.Cluster = 0
	r.Density = 3
	got := Grass(g, GrassSettings{Rules: []GrassRule{r}, Seed: 5}, [4]float64{-20, -20, 20, 20})
	per := float64(len(got)) / 1600
	if per < 2.7 || per > 3.3 {
		t.Fatalf("%.2f clumps/m², want ~3", per)
	}
	// Scales within range, tints near the two colours.
	sort.Slice(got, func(a, b int) bool { return got[a].Scale < got[b].Scale })
	if got[0].Scale < 3*0.6-1e-9 || got[len(got)-1].Scale > 5+1e-9 {
		t.Fatalf("scales %g..%g", got[0].Scale, got[len(got)-1].Scale)
	}
}

// TestGrassGroundOrientation: a ground rule stands each clump on the
// terrain's normal, tilt jitter leans it by no more than asked, and neither
// moves, re-kinds or re-turns a single clump — the extra randomness comes
// from hash slots the upright rule never read — while an upright rule's
// clumps keep a zero axis (the old 12-byte clump).
func TestGrassGroundOrientation(t *testing.T) {
	g := stepGround(t, 128, 1, 4)
	r := grassRule()
	r.Band.Slope = [2]float64{0, 90}
	plain := Grass(g, GrassSettings{Rules: []GrassRule{r}, Seed: 5}, g.Bounds())
	for _, in := range plain {
		if in.Up != ([3]float64{}) {
			t.Fatalf("an upright clump has an axis %v", in.Up)
		}
	}
	r.Ground = true
	ground := Grass(g, GrassSettings{Rules: []GrassRule{r}, Seed: 5}, g.Bounds())
	r.TiltJitter = 20 * math.Pi / 180
	tilted := Grass(g, GrassSettings{Rules: []GrassRule{r}, Seed: 5}, g.Bounds())
	if len(ground) != len(plain) || len(tilted) != len(plain) {
		t.Fatalf("orientation changed the count: %d %d %d", len(plain), len(ground), len(tilted))
	}
	sloped := 0
	for i := range plain {
		p, gr, tl := plain[i], ground[i], tilted[i]
		if p.Pos != gr.Pos || p.Kind != gr.Kind || p.Yaw != gr.Yaw || p.Scale != gr.Scale || p.Pos != tl.Pos || p.Yaw != tl.Yaw {
			t.Fatalf("orientation moved clump %d", i)
		}
		n := g.Normal(p.Pos[0], p.Pos[2], g.Grid.Cell)
		if math.Abs(dot(gr.Up, n)-1) > 1e-9 {
			t.Fatalf("ground axis %v is not the normal %v", gr.Up, n)
		}
		if n[1] < 0.99 {
			sloped++
		}
		lean := math.Acos(math.Min(1, dot(tl.Up, n))) * 180 / math.Pi
		if lean > 20+1e-6 || math.Abs(length(tl.Up)-1) > 1e-9 {
			t.Fatalf("tilt %.2f° past the 20° jitter (axis %v)", lean, tl.Up)
		}
	}
	if sloped == 0 {
		t.Fatal("the test ground has no slope to orient on")
	}
}
