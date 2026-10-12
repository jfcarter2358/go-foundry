package noise

// noise_test.go — the properties every generator relies on: determinism,
// range, continuity, and seeds that actually change the answer.

import (
	"math"
	"testing"
)

func TestRNGIsDeterministicAndSeedSensitive(t *testing.T) {
	a, b, c := NewRNG(42), NewRNG(42), NewRNG(43)
	same, diff := true, false
	for i := 0; i < 1000; i++ {
		x, y, z := a.Uint64(), b.Uint64(), c.Uint64()
		if x != y {
			same = false
		}
		if x != z {
			diff = true
		}
	}
	if !same {
		t.Error("two RNGs with one seed diverged")
	}
	if !diff {
		t.Error("seed 42 and 43 gave the same stream")
	}
}

func TestRNGRangesAndIntn(t *testing.T) {
	r := NewRNG(7)
	counts := make([]int, 10)
	for i := 0; i < 100000; i++ {
		f := r.Float64()
		if f < 0 || f >= 1 {
			t.Fatalf("Float64 = %v, outside [0,1)", f)
		}
		counts[r.Intn(10)]++
	}
	for i, n := range counts {
		// 10000 expected; five sigma is about 475.
		if n < 9400 || n > 10600 {
			t.Errorf("Intn(10) bucket %d hit %d times of 100000", i, n)
		}
	}
	if r.Intn(0) != 0 || r.Intn(-3) != 0 {
		t.Error("Intn of a non-positive n should be 0")
	}
}

func TestForkIsPositionIndependent(t *testing.T) {
	a := NewRNG(9)
	f1 := a.Fork(5).Uint64()
	a.Uint64()
	a.Uint64()
	// Fork derives from the state, so drawing changes it — what matters is
	// that a fresh RNG with the same seed forks to the same stream.
	b := NewRNG(9)
	if b.Fork(5).Uint64() != f1 {
		t.Error("Fork of the same seed and key differs")
	}
	if NewRNG(9).Fork(6).Uint64() == f1 {
		t.Error("Fork with a different key gave the same stream")
	}
}

func TestNoiseRangesAndDeterminism(t *testing.T) {
	r := NewRNG(1)
	fns := map[string]func(x, y, z float64) float64{
		"value2":    func(x, y, _ float64) float64 { return Value2(3, x, y) },
		"value3":    func(x, y, z float64) float64 { return Value3(3, x, y, z) },
		"gradient2": func(x, y, _ float64) float64 { return Gradient2(3, x, y) },
		"gradient3": func(x, y, z float64) float64 { return Gradient3(3, x, y, z) },
		"fbm2":      func(x, y, _ float64) float64 { return FBM2(3, x, y, 6, 0.5) },
		"ridged2":   func(x, y, _ float64) float64 { return Ridged2(3, x, y, 6, 0.5) },
		"billow2":   func(x, y, _ float64) float64 { return Billow2(3, x, y, 6, 0.5) },
		"fbm3":      func(x, y, z float64) float64 { return FBM3(3, x, y, z, 4, 0.5) },
	}
	for name, fn := range fns {
		lo, hi := math.Inf(1), math.Inf(-1)
		for i := 0; i < 20000; i++ {
			x, y, z := r.Range(-100, 100), r.Range(-100, 100), r.Range(-100, 100)
			v := fn(x, y, z)
			if v != fn(x, y, z) {
				t.Fatalf("%s is not deterministic at (%v,%v,%v)", name, x, y, z)
			}
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
		if lo < -1.1 || hi > 1.1 {
			t.Errorf("%s ranged [%.3f, %.3f], want about [-1, 1]", name, lo, hi)
		}
		if hi-lo < 0.5 {
			t.Errorf("%s ranged only [%.3f, %.3f]: nearly constant", name, lo, hi)
		}
	}
}

func TestNoiseIsContinuous(t *testing.T) {
	// A step of a thousandth of a cell moves the value by a small amount —
	// a lattice seam or a wrong fade would show as a jump.
	r := NewRNG(2)
	for i := 0; i < 5000; i++ {
		x, y := r.Range(-50, 50), r.Range(-50, 50)
		for _, fn := range []func(x, y float64) float64{
			func(x, y float64) float64 { return Value2(11, x, y) },
			func(x, y float64) float64 { return Gradient2(11, x, y) },
		} {
			if d := math.Abs(fn(x, y) - fn(x+1e-3, y)); d > 0.02 {
				t.Fatalf("jump of %v over 1e-3 at (%v,%v)", d, x, y)
			}
		}
	}
}

func TestSeedsChangeTheField(t *testing.T) {
	same := 0
	for i := 0; i < 100; i++ {
		x, y := float64(i)*0.37+0.1, float64(i)*0.61+0.2
		if Gradient2(1, x, y) == Gradient2(2, x, y) {
			same++
		}
	}
	if same > 5 {
		t.Errorf("%d of 100 samples equal between seeds 1 and 2", same)
	}
}

func TestCellularF1F2AndIDs(t *testing.T) {
	r := NewRNG(3)
	for i := 0; i < 5000; i++ {
		x, y := r.Range(-20, 20), r.Range(-20, 20)
		c := Cellular2(5, x, y, 1, MetricEuclidean)
		if c.F1 < 0 || c.F2 < c.F1 {
			t.Fatalf("F1 %v F2 %v out of order", c.F1, c.F2)
		}
		// The nearest point really is at distance F1.
		if d := math.Hypot(c.Point[0]-x, c.Point[1]-y); math.Abs(d-c.F1) > 1e-9 {
			t.Fatalf("Point is %v away, F1 says %v", d, c.F1)
		}
		// A sample moved a hair toward its own feature point keeps its cell.
		nx, ny := x+(c.Point[0]-x)*0.01, y+(c.Point[1]-y)*0.01
		if Cellular2(5, nx, ny, 1, MetricEuclidean).ID != c.ID {
			t.Fatalf("moving toward the feature point changed the cell")
		}
	}
	c3 := Cellular3(5, 0.3, 0.4, 0.5, 1, MetricEuclidean)
	if c3.F2 < c3.F1 || math.IsInf(c3.F1, 0) {
		t.Errorf("Cellular3 gave F1 %v F2 %v", c3.F1, c3.F2)
	}
	// Jitter 0 is a regular grid: the feature point is the cell centre.
	g := Cellular2(5, 2.2, 3.3, 0, MetricEuclidean)
	if g.Point[0] != 2.5 || g.Point[1] != 3.5 {
		t.Errorf("jitter 0 put the point at %v, want the cell centre", g.Point)
	}
}

func TestWarpMovesButIsDeterministic(t *testing.T) {
	f := Fractal{Seed: 8, Octaves: 3, Frequency: 0.1}
	x, y := Warp2(f, 10, 20, 5)
	x2, y2 := Warp2(f, 10, 20, 5)
	if x != x2 || y != y2 {
		t.Error("Warp2 is not deterministic")
	}
	if x == 10 && y == 20 {
		t.Error("Warp2 did not move the point")
	}
	if a, b := Warp2(f, 10, 20, 0); a != 10 || b != 20 {
		t.Error("Warp2 with amount 0 moved the point")
	}
}

func BenchmarkFBM2Octaves6(b *testing.B) {
	f := Fractal{Seed: 1, Octaves: 6, Frequency: 0.01}
	for i := 0; i < b.N; i++ {
		f.At2(float64(i)*0.37, float64(i)*0.11)
	}
}
