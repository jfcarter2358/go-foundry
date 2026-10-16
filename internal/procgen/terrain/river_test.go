package terrain

// river_test.go — CarveRivers and TraceDrainage (river*.go,
// docs/terrain-paint/PHASE_W3.md): the surface never rises, a painted path
// is turned downhill, a tributary meets its main river's surface, a steep
// drop becomes a waterfall with a plunge pool, a river ending in a pond
// ends at its level, and drainage tracing follows the valley floors.

import (
	"math"
	"testing"
)

// valleyGrid is a 128 m map at 1 m: a valley along X falling toward +X
// (5 cm/m), its sides rising 8 cm/m from z = 0, with a little roughness.
func valleyGrid() *Grid {
	g := NewGrid(128, 1)
	s := g.S()
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			x, z := g.X(i), g.Z(j)
			g.H[j*s+i] = float32(10 - 0.05*x + 0.08*math.Abs(z) + 0.05*math.Sin(x*0.7)*math.Cos(z*0.9))
		}
	}
	return g
}

func valleyRiver(name string, pts ...[2]float64) RiverSpec {
	w := make([]float64, len(pts))
	d := make([]float64, len(pts))
	for i := range pts {
		w[i], d[i] = 4, 1
	}
	return RiverSpec{Name: name, Points: pts, Width: w, Depth: d, Shape: DefaultRiverShape(), Seed: 7}
}

func checkMonotone(t *testing.T, r *River) {
	t.Helper()
	for i := 1; i < len(r.Stations); i++ {
		if r.Stations[i].Surface > r.Stations[i-1].Surface+1e-9 {
			t.Fatalf("%s: surface rises at station %d: %.4f -> %.4f", r.Name, i, r.Stations[i-1].Surface, r.Stations[i].Surface)
		}
	}
}

func TestRiverSurfaceNeverRisesAndTheChannelHoldsIt(t *testing.T) {
	g := valleyGrid()
	before := g.Clone()
	sp := valleyRiver("main", [2]float64{-55, 0}, [2]float64{-20, 3}, [2]float64{10, -3}, [2]float64{55, 1})
	res := CarveRivers(g, []RiverSpec{sp}, nil, math.Inf(-1))
	if len(res.Rivers) != 1 {
		t.Fatalf("%d rivers", len(res.Rivers))
	}
	r := &res.Rivers[0]
	checkMonotone(t, r)
	if r.Reversed {
		t.Fatalf("drawn downhill, but reversed")
	}
	if r.Drop < 3 {
		t.Fatalf("drop %.2f m over 110 m of a 5%% valley", r.Drop)
	}
	if res.Changed == 0 {
		t.Fatalf("nothing carved")
	}
	// The centreline is under water, a metre-ish deep at the thalweg, and
	// the ground a few metres outside the banks is above the water.
	s := g.S()
	wet, deep := 0, 0
	for _, st := range r.Stations[20 : len(r.Stations)-20] {
		i := int(math.Round((st.X - g.MinX) / g.Cell))
		j := int(math.Round((st.Z - g.MinZ) / g.Cell))
		if lv, ok := res.Wet(j*s + i); ok {
			wet++
			if float64(lv)-float64(g.H[j*s+i]) > 0.4 {
				deep++
			}
		}
		for _, side := range []float64{-1, 1} {
			o := side * (st.Width/2 + 2)
			bx, bz := st.X-st.TZ*o, st.Z+st.TX*o
			if h := float64(g.Sample(bx, bz)); h < st.Surface {
				t.Fatalf("bank at (%.1f, %.1f) is %.3f, under the water %.3f", bx, bz, h, st.Surface)
			}
		}
	}
	n := len(r.Stations) - 40
	if wet < n*9/10 || deep < n/2 {
		t.Fatalf("centreline wet %d / deep %d of %d", wet, deep, n)
	}
	// Ground far from the river is untouched.
	for _, p := range [][2]int{{5, 5}, {120, 120}, {64, 5}} {
		k := p[1]*s + p[0]
		if g.H[k] != before.H[k] {
			t.Fatalf("sample %v moved", p)
		}
	}
	// The flow runs downstream (+X) through the middle.
	mid := r.Stations[len(r.Stations)/2]
	i := int(math.Round((mid.X - g.MinX) / g.Cell))
	j := int(math.Round((mid.Z - g.MinZ) / g.Cell))
	if smp := res.Samples[int32(j*s+i)]; smp.FlowX <= 0.1 {
		t.Fatalf("flow at the middle %+v", smp)
	}
}

func TestPaintedRiverIsOrientedDownhill(t *testing.T) {
	g := valleyGrid()
	sp := valleyRiver("up", [2]float64{55, 0}, [2]float64{0, 2}, [2]float64{-55, 0})
	res := CarveRivers(g, []RiverSpec{sp}, nil, math.Inf(-1))
	r := &res.Rivers[0]
	if !r.Reversed {
		t.Fatalf("drawn uphill, not reversed")
	}
	if first, last := r.Stations[0], r.Stations[len(r.Stations)-1]; first.X > last.X {
		t.Fatalf("still runs from x %.1f to %.1f", first.X, last.X)
	}
	checkMonotone(t, r)
}

func TestTributaryMeetsTheMainRiverSurface(t *testing.T) {
	g := valleyGrid()
	main := valleyRiver("main", [2]float64{-55, 0}, [2]float64{55, 0})
	trib := valleyRiver("trib", [2]float64{-5, 45}, [2]float64{0, 20}, [2]float64{2, 0.5})
	trib.Width = []float64{2, 2, 2}
	trib.Depth = []float64{0.5, 0.5, 0.5}
	res := CarveRivers(g, []RiverSpec{trib, main}, nil, math.Inf(-1))
	if len(res.Confluences) != 1 || res.Confluences[0].Main != "main" || res.Confluences[0].Tributary != "trib" {
		t.Fatalf("confluences %+v", res.Confluences)
	}
	var m, tr *River
	for i := range res.Rivers {
		switch res.Rivers[i].Name {
		case "main":
			m = &res.Rivers[i]
		case "trib":
			tr = &res.Rivers[i]
		}
	}
	checkMonotone(t, m)
	checkMonotone(t, tr)
	if tr.Into != "main" {
		t.Fatalf("trib into %q", tr.Into)
	}
	mouth := tr.Stations[len(tr.Stations)-1]
	// The main river's surface nearest the mouth.
	best, lv := math.Inf(1), 0.0
	for _, s := range m.Stations {
		if d := math.Hypot(s.X-mouth.X, s.Z-mouth.Z); d < best {
			best, lv = d, s.Surface
		}
	}
	if math.Abs(mouth.Surface-lv) > 0.01 {
		t.Fatalf("tributary mouth %.3f, main river there %.3f", mouth.Surface, lv)
	}
	if tr.Source <= tr.Mouth+0.5 {
		t.Fatalf("tributary source %.2f mouth %.2f: it should fall down the valley side", tr.Source, tr.Mouth)
	}
}

func TestSteepDropBecomesAWaterfall(t *testing.T) {
	g := NewGrid(128, 1)
	s := g.S()
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			x, z := g.X(i), g.Z(j)
			h := 12 - 0.02*x + 0.1*math.Abs(z)
			// A 5 m cliff across the valley at x = 0, two metres wide.
			h -= 5 * math.Min(math.Max((x+1)/2, 0), 1)
			g.H[j*s+i] = float32(h)
		}
	}
	sp := valleyRiver("falls", [2]float64{-55, 0}, [2]float64{55, 0})
	res := CarveRivers(g, []RiverSpec{sp}, nil, math.Inf(-1))
	if len(res.Falls) != 1 {
		t.Fatalf("falls %+v", res.Falls)
	}
	f := res.Falls[0]
	if f.Drop < 4 || f.Drop > 7 || math.Abs(f.X) > 6 || f.DirX < 0.9 {
		t.Fatalf("fall %+v", f)
	}
	r := &res.Rivers[0]
	checkMonotone(t, r)
	// A step, not a ramp: the surface just past the lip is the foot's.
	var plunge, normal float64
	for i, st := range r.Stations {
		if st.S > f.S && st.S < f.S+1 && math.Abs(st.Surface-f.Foot) > 1e-6 {
			t.Fatalf("station %d past the lip at %.3f, foot %.3f", i, st.Surface, f.Foot)
		}
		if st.S > f.S+1 && st.S < f.S+4 {
			plunge = math.Max(plunge, st.Depth)
		}
		if st.S > f.S+30 && st.S < f.S+40 {
			normal = math.Max(normal, st.Depth)
		}
	}
	if plunge < normal+1 {
		t.Fatalf("plunge pool %.2f m deep, the river downstream %.2f", plunge, normal)
	}
	// Foam at the lip.
	for _, st := range r.Stations {
		if math.Abs(st.S-f.S) < 0.3 && st.Foam < 0.7 {
			t.Fatalf("lip foam %.2f", st.Foam)
		}
	}
}

func TestRiverIntoAPondEndsAtItsLevel(t *testing.T) {
	g := valleyGrid()
	s := g.S()
	// A pond round (40, 0) standing at 7 m.
	w := &WaterResult{Level: make([]float32, s*s), Body: make([]int32, s*s), Bodies: []WaterBody{{Pond: true, Level: 7}}}
	for k := range w.Body {
		w.Body[k], w.Level[k] = -1, float32(math.NaN())
	}
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			if math.Hypot(g.X(i)-40, g.Z(j)) < 10 {
				g.H[j*s+i] = 5
				w.Body[j*s+i], w.Level[j*s+i] = 0, 7
			}
		}
	}
	sp := valleyRiver("in", [2]float64{-55, 0}, [2]float64{55, 0})
	res := CarveRivers(g, []RiverSpec{sp}, w, math.Inf(-1))
	var in, out *River
	for i := range res.Rivers {
		switch res.Rivers[i].Name {
		case "in":
			in = &res.Rivers[i]
		case "in.2":
			out = &res.Rivers[i]
		}
	}
	if in == nil || out == nil {
		t.Fatalf("rivers %d", len(res.Rivers))
	}
	if in.IntoWater != 0 || math.Abs(in.Mouth-7) > 0.01 {
		t.Fatalf("into %d mouth %.3f", in.IntoWater, in.Mouth)
	}
	if out.FromWater != 0 || math.Abs(out.Source-7) > 1e-3 {
		t.Fatalf("from %d source %.3f", out.FromWater, out.Source)
	}
	checkMonotone(t, in)
	checkMonotone(t, out)
	// The pond itself is untouched.
	k := (s/2)*s + (s/2 + 40)
	if g.H[k] != 5 {
		t.Fatalf("pond bed moved to %.3f", g.H[k])
	}
}

func TestDrainageTracesTheValleyFloors(t *testing.T) {
	g := NewGrid(160, 1)
	s := g.S()
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			x, z := g.X(i), g.Z(j)
			// A main valley along X at z = 0 falling to +X, and a side
			// valley along Z at x = -20 falling into it from +Z.
			h := 20 - 0.06*x + 0.12*math.Abs(z)
			if z > 0 {
				h = math.Min(h, 20-0.06*(-20)+0.12*math.Abs(x+20)+0.08*z)
			}
			g.H[j*s+i] = float32(h)
		}
	}
	set := DefaultDrainage()
	set.Threshold = 0.45
	set.MinLength = 30
	rs := TraceDrainage(g, set)
	if len(rs) < 2 {
		for _, r := range rs {
			t.Logf("%s parent %d len %.0f %v", r.Name, r.Parent, polyLength(r.Points), r.Points)
		}
		t.Fatalf("%d rivers traced", len(rs))
	}
	main := rs[0]
	if main.Parent != -1 || polyLength(main.Points) < 100 {
		t.Fatalf("main %+v", main)
	}
	for _, p := range main.Points[len(main.Points)/3:] {
		if math.Abs(p[1]) > 3 {
			t.Fatalf("main river off the floor at %v", p)
		}
	}
	if main.Width[len(main.Width)-1] <= main.Width[0] {
		t.Fatalf("width does not grow downstream: %v", main.Width)
	}
	var side *TracedRiver
	for i := range rs {
		r := &rs[i]
		if r.Parent == 0 && polyLength(r.Points) > 40 {
			side = r
			break
		}
	}
	if side == nil {
		t.Fatalf("no side valley traced: %d rivers", len(rs))
	}
	mouth := side.Points[len(side.Points)-1]
	if math.Abs(mouth[1]) > 3 || math.Abs(mouth[0]+20) > 6 {
		t.Fatalf("side river mouth at %v, want near (-20, 0)", mouth)
	}
	// Carving what was traced keeps the tributary on the main river.
	var specs []RiverSpec
	for _, r := range rs {
		specs = append(specs, RiverSpec{Name: r.Name, Points: r.Points, Width: r.Width, Depth: r.Depth, Drainage: true,
			Shape: DefaultRiverShape(), Seed: 3})
	}
	res := CarveRivers(g, specs, nil, math.Inf(-1))
	if len(res.Confluences) == 0 {
		t.Fatalf("no confluence carved")
	}
	for i := range res.Rivers {
		checkMonotone(t, &res.Rivers[i])
	}
	// Trimmed against a hand river along the main valley: the main
	// drainage river is gone, the side one ends on the hand river.
	hand := valleyRiver("hand", [2]float64{-80, 0}, [2]float64{80, 0})
	tr := TrimAgainst(rs, []RiverSpec{hand}, 20)
	for _, r := range tr {
		if r.Name == main.Name && polyLength(r.Points) > 20 {
			t.Fatalf("main drainage river kept under the hand river: %v", r.Points)
		}
	}
}

func TestLongSteepStretchIsRapidsNotAFall(t *testing.T) {
	// A river down a long 40% slope: no step (that would cut a slot gorge
	// the length of the slope), a sloping foamy surface instead.
	g := NewGrid(128, 1)
	s := g.S()
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			x, z := g.X(i), g.Z(j)
			h := 30 - 0.02*x + 0.15*math.Abs(z)
			if x > -20 && x < 20 {
				h -= 0.4 * (x + 20)
			} else if x >= 20 {
				h -= 16
			}
			g.H[j*s+i] = float32(h)
		}
	}
	sp := valleyRiver("rapids", [2]float64{-55, 0}, [2]float64{55, 0})
	res := CarveRivers(g, []RiverSpec{sp}, nil, math.Inf(-1))
	if len(res.Falls) != 0 {
		t.Fatalf("falls %+v on a 40 m slope", res.Falls)
	}
	r := &res.Rivers[0]
	checkMonotone(t, r)
	for _, st := range r.Stations {
		if st.X > -10 && st.X < 10 && st.Foam < 0.6 {
			t.Fatalf("rapids at x %.1f foam %.2f", st.X, st.Foam)
		}
	}
}
