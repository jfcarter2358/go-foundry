package place

import (
	"context"
	"math"
	"reflect"
	"testing"

	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/terrain"
)

// stepGround is a test terrain: size metres at cell, a lowland gently
// rolling, a cliff of height h rising along x = 0 (to a plateau at x > 0)
// over about 6 m, with slope and cliff masks made from the heights as the
// terrain stage makes them.
func stepGround(t testing.TB, size, cell, h float64) *Ground {
	return groundOf(t, size, cell, func(x, z float64) float64 {
		return 0.6*math.Sin(x*0.05) + 0.4*math.Cos(z*0.07) + h*smooth(-3, 3, x+2*math.Sin(z*0.03))
	})
}

// groundOf is a test terrain of the heights hf, with its masks.
func groundOf(t testing.TB, size, cell float64, hf func(x, z float64) float64) *Ground {
	t.Helper()
	n := int(size / cell)
	g := terrain.NewGrid(n, cell)
	s := n + 1
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			g.H[j*s+i] = float32(hf(g.X(i), g.Z(j)))
		}
	}
	slope := make([]float32, s*s)
	cliff := make([]float32, s*s)
	flat := make([]float32, s*s)
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			dx := float64(g.At(i+1, j)-g.At(i-1, j)) / (2 * cell)
			dz := float64(g.At(i, j+1)-g.At(i, j-1)) / (2 * cell)
			a := math.Atan(math.Hypot(dx, dz)) * 180 / math.Pi
			slope[j*s+i] = float32(a / 90)
			cliff[j*s+i] = float32(smooth(44, 56, a))
			flat[j*s+i] = 0.5
		}
	}
	res := &terrain.Result{Grid: g, Masks: map[string][]float32{
		terrain.MaskSlope: slope, terrain.MaskCliff: cliff, terrain.MaskFlow: flat, terrain.MaskDeposition: flat,
		terrain.MaskCurvature: flat, terrain.MaskStrata: flat, terrain.MaskStrataHeight: flat,
	}}
	gr, err := NewGround(terrain.FromResult(res, terrain.Meta{}, ""))
	if err != nil {
		t.Fatal(err)
	}
	return gr
}

func boulders(n int) []Variant {
	var out []Variant
	for i := 0; i < n; i++ {
		f := 1 + 0.1*float64(i)
		out = append(out, Variant{Name: "b", Model: "m.fnm", Size: 1.6 * f, Extent: [3]float64{1.6 * f, 1.1 * f, 1.4 * f},
			Height: 1.1 * f, Radius: 0.8 * f, FootprintRadius: 0.7 * f, BaseBand: 0.13 * f, Top: 1 * f,
			Collision: "hulls", PlaceCollision: "hulls", Triangles: 5000})
	}
	return out
}

func chunks(n int) []Variant {
	var out []Variant
	for i := 0; i < n; i++ {
		f := 1 + 0.05*float64(i)
		out = append(out, Variant{Name: "c", Model: "c.fnm", Size: 8 * f, Extent: [3]float64{6 * f, 8 * f, 3 * f}, Height: 8 * f,
			Radius: 3.5 * f, FootprintRadius: 3 * f, BaseBand: 1 * f, Top: 7.8 * f, Back: true, FlatBase: true,
			Collision: "mesh", PlaceCollision: "mesh", Triangles: 150000})
	}
	return out
}

func pool(workers int) terrain.Pool { return terrain.NewPool(context.Background(), workers) }

func TestEulerYXZRoundTrips(t *testing.T) {
	rng := noise.NewRNG(7)
	for k := 0; k < 500; k++ {
		ax := normalize([3]float64{rng.Range(-1, 1), rng.Range(-1, 1), rng.Range(-1, 1)})
		m := RotAxis(ax, rng.Range(-3, 3)).Mul(FromTo([3]float64{0, 1, 0}, normalize([3]float64{rng.Range(-1, 1), 1, rng.Range(-1, 1)})))
		e := m.EulerYXZ()
		back := RotY(e[1]).Mul(RotX(e[0])).Mul(RotZ(e[2]))
		for r := 0; r < 3; r++ {
			for c := 0; c < 3; c++ {
				if math.Abs(back[r][c]-m[r][c]) > 1e-9 {
					t.Fatalf("rotation %d: euler %v rebuilds %v, want %v", k, e, back, m)
				}
			}
		}
	}
	up := [3]float64{0, 1, 0}
	n := normalize([3]float64{0.3, 1, -0.2})
	if got := FromTo(up, n).Apply(up); length([3]float64{got[0] - n[0], got[1] - n[1], got[2] - n[2]}) > 1e-12 {
		t.Errorf("FromTo(up, n)·up = %v, want %v", got, n)
	}
	if got := FromTo(up, scale(up, -1)).Apply(up); got[1] > -0.999999 {
		t.Errorf("FromTo(up, -up)·up = %v", got)
	}
}

func scatterOnce(t testing.TB, g *Ground, workers int, rules ...Rule) *ScatterResult {
	t.Helper()
	res, err := Scatter(g, ScatterSettings{Rules: rules, Seed: 99, Margin: 1}, pool(workers))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestScatterIsDeterministicOnAnyWorkerCount(t *testing.T) {
	g := stepGround(t, 128, 0.5, 20)
	r := DefaultRule()
	r.Variants = boulders(4)
	r.Density = 3
	small := r
	small.Name, small.Satellites, small.Scale, small.Density = "small", 1.5, [2]float64{0.25, 0.5}, 2
	a := scatterOnce(t, g, 1, r, small)
	b := scatterOnce(t, g, 9, r, small)
	if len(a.Instances) < 50 {
		t.Fatalf("only %d instances", len(a.Instances))
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("1 worker placed %d, 9 workers %d — or the same count differently", len(a.Instances), len(b.Instances))
	}
	c, err := Scatter(g, ScatterSettings{Rules: []Rule{r, small}, Seed: 100, Margin: 1}, pool(4))
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(a.Instances, c.Instances) {
		t.Error("a different seed placed the same rocks")
	}
	if a.Stats[1].Satellites == 0 {
		t.Error("no satellites were thrown round the first rule's rocks")
	}
}

func TestScatterKeepsFootprintsApart(t *testing.T) {
	g := stepGround(t, 96, 0.5, 15)
	big := DefaultRule()
	big.Variants, big.Density, big.Spacing = boulders(3), 8, 1.2
	small := big
	small.Name, small.Scale, small.Density, small.Spacing = "small", [2]float64{0.2, 0.4}, 20, 1
	res := scatterOnce(t, g, 4, big, small)
	in := res.Instances
	if res.Stats[0].TooClose == 0 {
		t.Error("a dense rule rejected nothing for spacing")
	}
	sp := []float64{big.Spacing, small.Spacing}
	for a := range in {
		for b := a + 1; b < len(in); b++ {
			f := math.Max(sp[in[a].Rule], sp[in[b].Rule])
			d := math.Hypot(in[a].Pos[0]-in[b].Pos[0], in[a].Pos[2]-in[b].Pos[2])
			if d < f*(in[a].Radius+in[b].Radius)-1e-9 {
				t.Fatalf("instances %d and %d are %.3f m apart, radii %.2f + %.2f at spacing %.2f", a, b, d, in[a].Radius, in[b].Radius, f)
			}
		}
	}
}

func TestScatteredRocksNeitherFloatNorSink(t *testing.T) {
	g := stepGround(t, 128, 0.5, 25)
	r := DefaultRule()
	r.Variants, r.Density, r.Slope = boulders(4), 4, [2]float64{0, 60}
	r.Align, r.Tilt = 0.4, 20
	res := scatterOnce(t, g, 4, r)
	if len(res.Instances) < 100 {
		t.Fatalf("only %d instances", len(res.Instances))
	}
	for k, in := range res.Instances {
		v := r.Variants[in.Variant]
		s := in.Scale
		// No point of the base ring above the ground.
		for a := 0; a < 24; a++ {
			sn, cs := math.Sincos(float64(a) * math.Pi / 12)
			o := in.Rot.Apply([3]float64{v.FootprintRadius * s * cs, 0.5 * v.BaseBand * s, v.FootprintRadius * s * sn})
			gy := g.Height(in.Pos[0]+o[0], in.Pos[2]+o[2])
			if in.Pos[1]+o[1] > gy+0.03 {
				t.Fatalf("rock %d: base ring point %d floats %.3f m above the ground", k, a, in.Pos[1]+o[1]-gy)
			}
		}
		// Not buried past its bounds: the ground at the centre is below
		// its top by most of its height.
		h := v.Height * s
		if gc := g.Height(in.Pos[0], in.Pos[2]); gc-in.Pos[1] > (r.MaxBury+1.4*r.Sink)*h+1e-6 {
			t.Fatalf("rock %d: %.2f m of its %.2f m height buried", k, gc-in.Pos[1], h)
		}
	}
}

func TestScatterHonoursSlopeAndPowerLaw(t *testing.T) {
	g := stepGround(t, 128, 0.5, 25)
	r := DefaultRule()
	r.Variants, r.Density, r.Slope, r.SlopeSoft = boulders(2), 3, [2]float64{0, 10}, 2
	r.Scale, r.ScaleBias, r.Spacing = [2]float64{0.5, 2}, 3, 0.5
	res := scatterOnce(t, g, 4, r)
	small := 0
	for _, in := range res.Instances {
		if sl := g.SlopeDeg(in.Pos[0], in.Pos[2]); sl > 12.5 {
			t.Fatalf("a rock on a %.1f° slope, the band is 0..10° (+2)", sl)
		}
		if in.Scale < 0.8 {
			small++
		}
	}
	// A power law of exponent 3 on [0.5, 2] puts ~63% under 0.8.
	if f := float64(small) / float64(len(res.Instances)); f < 0.5 {
		t.Errorf("%.0f%% of the rocks are small, the power law wants most", f*100)
	}
}

func TestCliffBaseGathersAtTheFoot(t *testing.T) {
	g := stepGround(t, 128, 0.5, 25)
	r := DefaultRule()
	r.Variants, r.Density, r.CliffBase, r.CliffBaseDistance = boulders(3), 6, 1, 12
	r.Cluster = 0
	res := scatterOnce(t, g, 4, r)
	if len(res.Instances) < 30 {
		t.Fatalf("only %d rocks at the foot", len(res.Instances))
	}
	for _, in := range res.Instances {
		// The foot is the lowland side (x < 0), within the distance of
		// the cliff band (whose foot is about x = -3).
		if in.Pos[0] > 0 || in.Pos[0] < -3-12-3 {
			t.Fatalf("a talus rock at x = %.1f: the foot is x in [-18, 0]", in.Pos[0])
		}
	}
}

func TestCollisionBySize(t *testing.T) {
	v := boulders(1)[0]
	if c := collisionFor("auto", &v, 0.1, 0.35, 4); c != "none" {
		t.Errorf("a %.2f m pebble collides as %s", v.Extent[0]*0.1, c)
	}
	if c := collisionFor("auto", &v, 1, 0.35, 4); c != "hulls" {
		t.Errorf("a 1.6 m boulder collides as %s", c)
	}
	if c := collisionFor("auto", &v, 4, 0.35, 4); c != "mesh" {
		t.Errorf("a 6.4 m boulder collides as %s", c)
	}
	v.Collision = "mesh"
	if c := collisionFor("auto", &v, 1, 0.35, 4); c != "mesh" {
		t.Errorf("a mesh-cooked boulder collides as %s", c)
	}
	if c := collisionFor("box", &v, 1, 0.35, 4); c != "box" {
		t.Errorf("an explicit box became %s", c)
	}
}

func TestTraceFindsTheFootOfTheCliff(t *testing.T) {
	g := stepGround(t, 128, 0.5, 25)
	lines := TraceCliffFeet(g, TraceSettings{Threshold: 0.5, Cell: 2, MinLength: 20, Smooth: 2})
	if len(lines) == 0 {
		t.Fatal("no cliff feet traced")
	}
	total := 0.0
	for _, l := range lines {
		total += l.Length()
		for k, p := range l.Points {
			if p[0] > 0.5 || p[0] < -6 {
				t.Fatalf("a foot point at x = %.2f: the cliff band's downhill edge is near x = -2", p[0])
			}
			if o := l.Out[k]; o[0] > -0.7 {
				t.Fatalf("out direction %v at %v: the wall faces -X", o, p)
			}
		}
	}
	if total < 100 {
		t.Errorf("the feet run %.0f m, the cliff 128 m", total)
	}
}

func TestWallsStackChunksIntoTheFace(t *testing.T) {
	g := stepGround(t, 128, 0.5, 25)
	lines := TraceCliffFeet(g, TraceSettings{Threshold: 0.5, Cell: 2, MinLength: 20, Smooth: 2})
	s := DefaultWall()
	s.Chunks = chunks(3)
	s.Ledges = boulders(2)
	s.Seed = 5
	res, err := Walls(g, lines, s, pool(1))
	if err != nil {
		t.Fatal(err)
	}
	var nchunk, nledge int
	top := math.Inf(-1)
	for _, in := range res.Instances {
		if in.Kind == "ledge" {
			nledge++
			continue
		}
		nchunk++
		v := s.Chunks[in.Variant]
		// Facing out of the wall (-X): +Z of the chunk along -X.
		fwd := in.Rot.Apply([3]float64{0, 0, 1})
		if fwd[0] > -0.8 {
			t.Fatalf("a chunk faces %v, the wall -X", fwd)
		}
		// Its back is in the wall: behind the face at its base height.
		if in.Pos[0] < -4 {
			t.Fatalf("a chunk's back at x = %.2f, in front of the wall's foot", in.Pos[0])
		}
		top = math.Max(top, in.Pos[1]+v.Height*in.Scale)
	}
	if nchunk < 20 {
		t.Fatalf("%d chunks along 128 m of wall", nchunk)
	}
	if top < 22 {
		t.Errorf("the tiers reach %.1f m, the rim is at 25 m", top)
	}
	if nledge == 0 {
		t.Error("no ledges")
	}
	if len(res.Ground) == 0 {
		t.Error("no ground footprints for scatter to avoid")
	}
	again, _ := Walls(g, lines, s, pool(1))
	if !reflect.DeepEqual(res, again) {
		t.Error("walls are not deterministic")
	}
}

func TestPatchesCoverOnlyTheWeightedCells(t *testing.T) {
	g := stepGround(t, 128, 0.5, 25)
	r := DefaultRule()
	r.Slope, r.SlopeSoft, r.Cluster = [2]float64{0, 15}, 1, 0
	ps, err := Patches(g, &r, PatchSettings{Cell: 8, Threshold: 0.9, MaxSize: 32}, pool(4))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) == 0 {
		t.Fatal("no patches")
	}
	area := 0.0
	for _, p := range ps {
		if p.MaxX-p.MinX > 32+1e-9 || p.MaxZ-p.MinZ > 32+1e-9 {
			t.Fatalf("a patch %.0f×%.0f m, at most 32", p.MaxX-p.MinX, p.MaxZ-p.MinZ)
		}
		if p.MinX < -2 && p.MaxX > 2 {
			t.Fatalf("a patch over the cliff: x %.0f..%.0f", p.MinX, p.MaxX)
		}
		if p.MaxY < p.MinY {
			t.Fatalf("a patch's height range is empty")
		}
		area += (p.MaxX - p.MinX) * (p.MaxZ - p.MinZ)
		// Patches never overlap.
		for _, q := range ps {
			if &q != &p && p != q && p.MinX < q.MaxX && q.MinX < p.MaxX && p.MinZ < q.MaxZ && q.MinZ < p.MaxZ {
				t.Fatalf("patches overlap: %+v and %+v", p, q)
			}
		}
	}
	// Most of the 128 m square is flat; the cliff takes a strip.
	if area < 0.7*128*128 {
		t.Errorf("the patches cover %.0f m², the flat ground ~%.0f", area, 0.9*128*128)
	}
}

// A scanned cliff's back need not face −Z or sit on z = 0: the chunk is
// turned so its own back faces into the wall, and its back plane (the
// box's face across the back direction) is seated as the generated ones'.
func TestWallsSeatAnyBackDirection(t *testing.T) {
	g := stepGround(t, 128, 0.5, 25)
	lines := TraceCliffFeet(g, TraceSettings{Threshold: 0.5, Cell: 2, MinLength: 20, Smooth: 2})
	s := DefaultWall()
	plain := chunks(1)[0]
	scan := plain
	// The same box turned a quarter: its back faces +X, off the origin.
	scan.BackDir = [3]float64{1, 0, 0}
	scan.Extent = [3]float64{3, 8, 6}
	scan.Min, scan.Max = [3]float64{2, 0, -3}, [3]float64{5, 8, 3}
	s.Chunks = []Variant{plain}
	a, _ := Walls(g, lines, s, pool(1))
	s.Chunks = []Variant{scan}
	b, _ := Walls(g, lines, s, pool(1))
	if len(a.Instances) == 0 || len(a.Instances) != len(b.Instances) {
		t.Fatalf("%d plain chunks, %d turned", len(a.Instances), len(b.Instances))
	}
	for k := range a.Instances {
		ia, ib := a.Instances[k], b.Instances[k]
		// The back faces into the wall (+X) and the back plane's bottom
		// centre lands where the plain chunk's does.
		if d := ib.Rot.Apply(scan.BackDir); d[0] < 0.8 {
			t.Fatalf("chunk %d's back faces %v, the wall is +X", k, d)
		}
		pa := ia.Pos // the plain chunk's back plane is its origin
		pb := add(ib.Pos, ib.Rot.Apply(scale([3]float64{5, 0, 0}, ib.Scale)))
		if math.Hypot(pa[0]-pb[0], pa[2]-pb[2]) > 1e-6 || math.Abs(pa[1]-pb[1]) > 1e-6 {
			t.Fatalf("chunk %d: back plane at %v, the plain chunk's at %v", k, pb, pa)
		}
	}
}

// No ledge floats: each juts out of the wall at its height (a chunk's
// front, or the heightfield's face where the tiers fall short of the rim).
func TestLedgesSitOnTheWall(t *testing.T) {
	// A 25 m cliff under a 35° slope that climbs another 25 m: the wall's
	// transect runs up the slope to its rim, far behind the cliff.
	g := groundOf(t, 128, 0.5, func(x, z float64) float64 {
		return 25*smooth(-3, 3, x) + math.Min(math.Max(x-3, 0), 36)*0.7
	})
	lines := TraceCliffFeet(g, TraceSettings{Threshold: 0.5, Cell: 2, MinLength: 20, Smooth: 2})
	s := DefaultWall()
	s.Chunks, s.Ledges = chunks(2), boulders(2)
	s.Tiers, s.LedgeChance, s.RimLedge = 2, 0.5, 1 // the tiers fall short of the rim
	res, err := Walls(g, lines, s, pool(1))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, in := range res.Instances {
		if in.Kind != "ledge" {
			continue
		}
		n++
		// The wall is right behind it: going into the wall (+X), the
		// ground rises to its height within a few metres — it juts out of
		// a chunk or the rim, not out in the air in front of them.
		reach := math.Inf(1)
		for d := 0.0; d < 40; d += 0.25 {
			if g.Height(in.Pos[0]+d, in.Pos[2]) >= in.Pos[1]-0.5 {
				reach = d
				break
			}
		}
		if reach > 8 {
			t.Fatalf("a ledge at %v is %.1f m out from the wall at its height", in.Pos, reach)
		}
	}
	if n == 0 {
		t.Fatal("no ledges")
	}
}
