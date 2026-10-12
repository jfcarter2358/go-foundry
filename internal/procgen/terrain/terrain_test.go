package terrain

// terrain_test.go — the generator's guarantees, phase by phase: the same
// bytes on any number of workers, cancellation, a terrace that keeps the
// surface whole, canyons graded downstream, erosion that neither digs pits
// nor raises spikes, and masks in range.

import (
	"context"
	"errors"
	"math"
	"testing"
)

// small is a quick generation: 256 m at 2 m with the demo canyon.
func small() Settings {
	s := Defaults()
	s.Size, s.Cell, s.Seed = 256, 2, 42
	c := demoCanyon()
	for i := range c.Path {
		c.Path[i].X /= 4
		c.Path[i].Z /= 4
		c.Path[i].Width /= 3
		c.Path[i].Depth /= 3
	}
	c.MeanderLength = 80
	c.TributaryLength = 60
	s.Canyons = []Canyon{c}
	s.Mesa.Scale = 160
	return s
}

func TestGenerateIsTheSameOnAnyWorkerCount(t *testing.T) {
	s := small()
	a, err := Generate(&s, NewPool(context.Background(), 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate(&s, NewPool(context.Background(), 13), nil)
	if err != nil {
		t.Fatal(err)
	}
	for k := range a.Grid.H {
		if a.Grid.H[k] != b.Grid.H[k] {
			t.Fatalf("sample %d: %v on 1 worker, %v on 13", k, a.Grid.H[k], b.Grid.H[k])
		}
	}
	for _, name := range MaskNames {
		for k := range a.Masks[name] {
			if a.Masks[name][k] != b.Masks[name][k] {
				t.Fatalf("mask %s sample %d differs", name, k)
			}
		}
	}
	s2 := s
	s2.Seed++
	c, _ := Generate(&s2, NewPool(context.Background(), 0), nil)
	same := true
	for k := range a.Grid.H {
		same = same && a.Grid.H[k] == c.Grid.H[k]
	}
	if same {
		t.Error("another seed made the same field")
	}
}

func TestGenerateCancels(t *testing.T) {
	s := small()
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := Generate(&s, NewPool(ctx, 4), func(p Phase, done float32) {
		calls++
		if p == PhaseDrainage {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled generation returned %v", err)
	}
	if calls == 0 {
		t.Error("no progress was reported")
	}
}

func TestProgressRisesToTheEnd(t *testing.T) {
	s := small()
	last := float32(-1)
	_, err := Generate(&s, NewPool(context.Background(), 0), func(_ Phase, done float32) {
		if done < last-1e-6 {
			t.Errorf("progress went back from %v to %v", last, done)
		}
		last = done
	})
	if err != nil {
		t.Fatal(err)
	}
	if last < 0.85 {
		t.Errorf("progress ended at %v", last)
	}
}

func TestTerraceIsMonotonicAndContinuous(t *testing.T) {
	c := Strata{Layers: DefaultLayers(), Base: -20, Terrace: 1}.compile()
	prev := c.terraceHeight(-300)
	for y := -300.0; y < 400; y += 0.01 {
		h := c.terraceHeight(y)
		if h < prev-1e-9 {
			t.Fatalf("terrace falls at %.2f: %v after %v", y, h, prev)
		}
		if h-prev > 2 {
			t.Fatalf("terrace jumps %.2f m at %.2f", h-prev, y)
		}
		prev = h
	}
	// Layer boundaries are fixed points: a layer never moves out of its band.
	y := c.base
	for i := 0; i < 3*len(c.layers); i++ {
		if d := c.terraceHeight(y) - y; math.Abs(d) > 1e-9 {
			t.Errorf("boundary at %.1f moved by %v", y, d)
		}
		y += c.layers[i%len(c.layers)].Thickness
	}
}

func TestStrataLookups(t *testing.T) {
	c := Strata{Layers: DefaultLayers(), Base: 0, Resistance: 0.6}.compile()
	period := 0.0
	for _, l := range DefaultLayers() {
		period += l.Thickness
	}
	for _, y := range []float64{0.5, 31, 40, 100, 170} {
		i, u := c.layer(y)
		j, v := c.layer(y + period)
		k, w := c.layer(y - 3*period)
		if i != j || i != k || math.Abs(u-v) > 1e-9 || math.Abs(u-w) > 1e-9 {
			t.Errorf("height %v: layer %d/%d/%d at %v/%v/%v — the stack does not repeat", y, i, j, k, u, v, w)
		}
	}
	if i, _ := c.layer(5); i != 0 {
		t.Errorf("5 m up is layer %d", i)
	}
	if h := c.hardness(100); h < 0.9 {
		t.Errorf("the cliff-former's hardness reads %v", h)
	}
	if c.erodibility(100) >= c.erodibility(10) {
		t.Error("the hard layer erodes as fast as the soft one")
	}
}

func TestProfiles(t *testing.T) {
	for _, p := range []Profile{ProfileV, ProfileU, ProfileSlot, ProfileStepped, ProfileCustom} {
		c := Canyon{Profile: p, Steps: 3, Custom: func(u float64) float64 { return 1 - u*u }}
		f := profileFunc(&c)
		if f(0) < 0.99 || f(1) != 0 || f(1.5) != 0 {
			t.Errorf("%s: p(0)=%v p(1)=%v", ProfileNames[p], f(0), f(1))
		}
		prev := 2.0
		for u := 0.0; u <= 1; u += 0.001 {
			v := f(u)
			if v > prev+1e-9 || v < 0 || v > 1 {
				t.Fatalf("%s: p(%v)=%v after %v: a section must fall from centre to rim", ProfileNames[p], u, v, prev)
			}
			prev = v
		}
	}
}

func TestCanyonFloorsNeverRiseDownstream(t *testing.T) {
	s := small()
	s.Drainage.Strength, s.Droplets.Density, s.Thermal.Iterations, s.Strata.Terrace = 0, 0, 0, 0
	res, err := Generate(&s, NewPool(context.Background(), 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) != 1+s.Canyons[0].Tributaries {
		t.Fatalf("%d paths carved, want the canyon and %d tributaries", len(res.Paths), s.Canyons[0].Tributaries)
	}
	for pi, p := range res.Paths {
		for k := 1; k < len(p.Floor); k++ {
			if p.Floor[k] > p.Floor[k-1]+0.05 {
				t.Fatalf("path %d rises %.2f m at point %d", pi, p.Floor[k]-p.Floor[k-1], k)
			}
		}
	}
	// The canyon is cut: its centreline is near its floor.
	main := res.Paths[0]
	mid := len(main.Points) / 2
	h := res.Grid.Sample(main.Points[mid][0], main.Points[mid][1])
	if d := float64(h) - main.Floor[mid]; d > 3 || d < -3 {
		t.Errorf("mid-canyon ground %.1f m, floor %.1f m", h, main.Floor[mid])
	}
}

func TestErosionDigsNoPitsAndRaisesNoSpikes(t *testing.T) {
	s := small()
	s.Thermal.Iterations = 0
	before := s
	before.Droplets.Density = 0
	a, _ := Generate(&before, NewPool(context.Background(), 0), nil)
	b, err := Generate(&s, NewPool(context.Background(), 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	lo, hi := a.Grid.Range()
	blo, bhi := b.Grid.Range()
	if blo < lo-5 || bhi > hi+5 {
		t.Errorf("droplets took the range %.1f..%.1f to %.1f..%.1f", lo, hi, blo, bhi)
	}
	// Something did erode.
	moved := 0.0
	for k := range a.Grid.H {
		moved += math.Abs(float64(a.Grid.H[k] - b.Grid.H[k]))
	}
	if moved/float64(len(a.Grid.H)) < 0.01 {
		t.Errorf("droplets moved %.4f m a sample on average", moved/float64(len(a.Grid.H)))
	}
}

func TestThermalRelaxesSoftSlopesAndKeepsHardCliffs(t *testing.T) {
	// The same 45° ridge in soft rock and in hard: the soft one relaxes
	// toward the talus angle and sheds onto its foot; the hard one stands.
	run := func(hardness float64) (deg float64, deposited float64) {
		s := Defaults()
		s.Size, s.Cell = 64, 1
		s.Strata = Strata{Layers: []Layer{{Thickness: 1e4, Hardness: hardness}}, Base: -5000}
		g := NewGrid(64, 1)
		for j := 0; j < g.S(); j++ {
			for i := 0; i < g.S(); i++ {
				g.H[j*g.S()+i] = float32(max(0, 8-math.Abs(float64(i-32))))
			}
		}
		dep := make([]float32, len(g.H))
		off := make([]float32, len(g.H))
		s.Thermal.Iterations = 300
		if err := thermal(&s, g, s.Strata.compile(), off, dep, NewPool(context.Background(), 0), nil); err != nil {
			t.Fatal(err)
		}
		maxSlope := 0.0
		for i := 1; i < g.S(); i++ {
			maxSlope = max(maxSlope, math.Abs(float64(g.At(i, 32)-g.At(i-1, 32))))
		}
		for _, v := range dep {
			deposited += float64(v)
		}
		return math.Atan(maxSlope) * 180 / math.Pi, deposited
	}
	soft, softDep := run(0)
	hard, hardDep := run(1)
	if soft > Defaults().Thermal.TalusAngle+4 {
		t.Errorf("steepest soft slope left at %.1f°, talus angle %.0f°", soft, Defaults().Thermal.TalusAngle)
	}
	if hard < 44 || hardDep > 1e-3 {
		t.Errorf("the hard ridge relaxed to %.1f° and shed %.3f m³", hard, hardDep)
	}
	if softDep <= 0 {
		t.Error("nothing was deposited at the soft ridge's foot")
	}
}

func TestMasksAreInRange(t *testing.T) {
	s := small()
	res, err := Generate(&s, NewPool(context.Background(), 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range MaskNames {
		v := res.Masks[name]
		if len(v) != len(res.Grid.H) {
			t.Fatalf("mask %s has %d samples", name, len(v))
		}
		lo, hi := float32(1), float32(0)
		for _, x := range v {
			lo, hi = min(lo, x), max(hi, x)
		}
		if lo < 0 || hi > 1 {
			t.Errorf("mask %s spans %v..%v", name, lo, hi)
		}
		if hi == lo && name != MaskStrata {
			t.Errorf("mask %s is flat at %v", name, lo)
		}
	}
	if res.Masks[MaskCliff][0] < 0 || len(res.Albedo) != 3*len(res.Grid.H) {
		t.Error("albedo is not RGB per sample")
	}
}

func TestScaledKeepsTheLandforms(t *testing.T) {
	s := Defaults()
	p := s.Scaled(256)
	if p.Cells() != 256 || p.Size != s.Size || p.Cell != 4 {
		t.Errorf("scaled to %d cells of %g m", p.Cells(), p.Cell)
	}
	if p.Mesa != s.Mesa || p.Base != s.Base {
		t.Error("scaling changed the landforms")
	}
	if q := s.Scaled(4096); q.Cell != s.Cell {
		t.Error("scaling up changed the resolution")
	}
	bad := Defaults()
	bad.Size = 100.5
	if bad.Check() == nil {
		t.Error("a size that is not a whole number of cells passed")
	}
}
