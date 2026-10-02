package nanite

import (
	"math"
	"math/rand"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// Fields of every width come back as they went in, a cluster at a time on a
// byte boundary, and a short stream is an error, not a panic.
func TestBitPackingRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	type field struct {
		v uint32
		n uint8
	}
	var fields [][]field
	var w bitWriter
	for c := 0; c < 50; c++ {
		var fs []field
		for i := 0; i < rng.Intn(40); i++ {
			n := uint8(rng.Intn(33))
			v := rng.Uint32()
			if n < 32 {
				v &= uint32(1)<<n - 1
			}
			fs = append(fs, field{v, n})
			w.write(v, n)
		}
		w.align()
		fields = append(fields, fs)
	}
	r := bitReader{buf: w.buf}
	for c, fs := range fields {
		for i, f := range fs {
			got, err := r.read(f.n)
			if err != nil {
				t.Fatal(err)
			}
			if got != f.v {
				t.Fatalf("cluster %d field %d (%d bits): %d, want %d", c, i, f.n, got, f.v)
			}
		}
		r.align()
	}
	if r.pos != len(w.buf) {
		t.Fatalf("read %d of %d bytes", r.pos, len(w.buf))
	}
	if _, err := r.read(8); err == nil {
		t.Fatal("reading past the end did not fail")
	}
}

// A position snapped to the grid is within half a step of where it was,
// decodes exactly (re-quantising gives the same integer), and the grid is
// as fine as asked unless float32 cannot hold it.
func TestQuantisationIsExactOnItsGrid(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for _, tc := range []struct {
		lo, hi mgl32.Vec3
		bits   int
	}{
		{mgl32.Vec3{-1, -1, -1}, mgl32.Vec3{1, 1, 1}, 18},
		{mgl32.Vec3{0, 0, 0}, mgl32.Vec3{120, 30, 80}, 18},
		// A small mesh far from its origin: float32 limits the grid.
		{mgl32.Vec3{4000, 0, 0}, mgl32.Vec3{4002, 1, 1}, 18},
	} {
		q := NewQuant(tc.lo, tc.hi, 4, tc.bits, 0, 0)
		ext := float64(max(tc.hi[0]-tc.lo[0], tc.hi[1]-tc.lo[1], tc.hi[2]-tc.lo[2]))
		if tc.lo[0] == 0 && float64(q.Step) > ext/math.Ldexp(1, tc.bits-1) {
			t.Errorf("%v: step %g is coarser than %d bits over %g", tc, q.Step, tc.bits, ext)
		}
		if f, _ := math.Frexp(float64(q.Step)); f != 0.5 {
			t.Errorf("step %g is not a power of two", q.Step)
		}
		for i := 0; i < 2000; i++ {
			p := tc.lo[0] + rng.Float32()*(tc.hi[0]-tc.lo[0])
			qi := q.PosQ(p)
			back := q.Pos(qi)
			if d := math.Abs(float64(back - p)); d > float64(q.Step)/2*1.0001 {
				t.Fatalf("%g snapped to %g, %g away with a step of %g", p, back, d, q.Step)
			}
			if q.PosQ(back) != qi {
				t.Fatalf("%g does not re-quantise to its own integer", back)
			}
			if float64(q.Pos(qi)) != float64(qi)*float64(q.Step) {
				t.Fatalf("decode of %d is not exact", qi)
			}
		}
	}
}

// Octahedral normals come back within a small angle, and unit length.
func TestOctahedralNormals(t *testing.T) {
	q := Quant{Step: 1, UVStep: 1, NormalBits: DefaultNormalBits}
	rng := rand.New(rand.NewSource(3))
	worst := 0.0
	for i := 0; i < 20000; i++ {
		n := mgl32.Vec3{float32(rng.NormFloat64()), float32(rng.NormFloat64()), float32(rng.NormFloat64())}.Normalize()
		d := q.Normal(q.NormalQ(n))
		if math.Abs(float64(d.Len())-1) > 1e-5 {
			t.Fatalf("decoded normal %v is not unit", d)
		}
		a := math.Acos(math.Min(1, float64(n.Dot(d))))
		worst = math.Max(worst, a)
	}
	t.Logf("worst normal error %.4f°", worst*180/math.Pi)
	if deg := worst * 180 / math.Pi; deg > 0.1 {
		t.Fatalf("worst normal error %.3f°", deg)
	}
	// The axes and a zero normal are fine.
	for _, n := range []mgl32.Vec3{{0, 0, 1}, {0, 0, -1}, {1, 0, 0}, {0, -1, 0}} {
		if d := q.Normal(q.NormalQ(n)); d.Sub(n).Len() > 1e-3 {
			t.Errorf("%v decodes to %v", n, d)
		}
	}
	if d := q.Normal(q.NormalQ(mgl32.Vec3{})); math.Abs(float64(d.Len())-1) > 1e-5 {
		t.Errorf("a zero normal decodes to %v", d)
	}
}
