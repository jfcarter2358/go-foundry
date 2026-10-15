package grassfield

import (
	"bytes"
	"math"
	"math/rand"
	"testing"
)

func testKit() *Kit {
	src := &SourceMesh{
		Positions: [][3]float32{{-0.1, 0, 0}, {0.1, 0, 0}, {0, 1, 0}, {0.3, 0, 0.2}, {0.4, 0, 0.2}, {0.35, 0.8, 0.25}},
		Normals:   [][3]float32{{0, 0, 1}, {0, 0, 1}, {0, 0, 1}, {0, 0, 1}, {0, 0, 1}, {0, 0, 1}},
		UVs:       [][2]float32{{0, 0}, {1, 0}, {0.5, 1}, {0, 0}, {1, 0}, {0.5, 1}},
		Indices:   []uint32{0, 1, 2, 3, 4, 5},
	}
	h, r := src.Extent()
	return &Kit{Kinds: []Kind{
		{Name: "a", Texture: "ph_grass_cov", Height: h, Radius: r, Bend: 0.2, Flutter: 0.05, Flags: KindCasts,
			LODs: []Mesh{KindMesh(src, h, r, 0.2, 0.05), KindMesh(Thin(src, 0.5, 1), h, r, 0.2, 0.05)}},
		{Name: "b", Texture: "ph_flower_cov", Height: h, Radius: r, Range: 0.6, LODs: []Mesh{KindMesh(src, h, r, 0, 0)}},
	}}
}

func TestKitRoundTrip(t *testing.T) {
	k := testKit()
	data, err := EncodeKit(k)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeKit(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Kinds) != 2 || got.Kinds[0].Name != "a" || got.Kinds[1].Texture != "ph_flower_cov" ||
		got.Kinds[1].Range != 0.6 || !got.Kinds[0].Casts() || got.Kinds[1].Casts() {
		t.Fatalf("kinds %+v", got.Kinds)
	}
	for i := range k.Kinds {
		for l := range k.Kinds[i].LODs {
			a, b := k.Kinds[i].LODs[l], got.Kinds[i].LODs[l]
			if len(a.Vertices) != len(b.Vertices) || len(a.Indices) != len(b.Indices) {
				t.Fatalf("kind %d lod %d sizes differ", i, l)
			}
			for j := range a.Vertices {
				if a.Vertices[j] != b.Vertices[j] {
					t.Fatalf("kind %d lod %d vertex float %d", i, l, j)
				}
			}
		}
	}
	// Truncations are errors, never panics.
	for n := 0; n < len(data); n += 7 {
		if _, err := DecodeKit(data[:n]); err == nil {
			t.Fatalf("a kit cut to %d bytes decoded", n)
		}
	}
}

// randomInstances scatters n instances over the tile.
func randomInstances(n int, seed int64, min, max [2]float32, kinds int) []Instance {
	rng := rand.New(rand.NewSource(seed))
	out := make([]Instance, n)
	for i := range out {
		out[i] = Instance{Kind: rng.Intn(kinds),
			Pos: [3]float32{min[0] + rng.Float32()*(max[0]-min[0]), 10 + rng.Float32()*5, min[1] + rng.Float32()*(max[1]-min[1])},
			Yaw: rng.Float32() * 6.28, Scale: 1 + rng.Float32()*3,
			Tint: [3]float32{0.8 + rng.Float32()*0.4, 1, 0.9}}
	}
	return out
}

func TestFieldRoundTripAndOrder(t *testing.T) {
	spec := FieldSpec{Kit: "generated/w/grass/grass.fgk", Min: [2]float32{-128, 0}, Max: [2]float32{0, 128}, Cell: 16, Kinds: 3}
	ins := randomInstances(5000, 1, spec.Min, spec.Max, 3)
	f, err := BuildField(spec, ins)
	if err != nil {
		t.Fatal(err)
	}
	if f.CellsX != 8 || f.CellsZ != 8 || f.Count() != 5000 {
		t.Fatalf("cells %dx%d count %d", f.CellsX, f.CellsZ, f.Count())
	}
	data, err := EncodeField(f)
	if err != nil {
		t.Fatal(err)
	}
	g, err := DecodeField(data)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := EncodeField(g)
	if !bytes.Equal(data, again) {
		t.Fatal("decode then encode changed the file")
	}
	// Every instance comes back within quantisation, in its cell's run, in
	// rank order.
	total := 0
	for c := 0; c < g.CellsX*g.CellsZ; c++ {
		box := g.CellBox(c)
		for k := 0; k < g.Kinds; k++ {
			r := g.Run(c, k)
			prev := -1
			for i := r.Offset; i < r.Offset+r.Count; i++ {
				in := g.Unpack(int(i))
				if int(in.Rank) < prev {
					t.Fatalf("cell %d kind %d out of rank order", c, k)
				}
				prev = int(in.Rank)
				if in.Pos[0] < box[0]-0.01 || in.Pos[0] > box[2]+0.01 || in.Pos[2] < box[1]-0.01 || in.Pos[2] > box[3]+0.01 {
					t.Fatalf("instance %d at %v outside cell %d %v", i, in.Pos, c, box)
				}
				total++
			}
		}
	}
	if total != 5000 {
		t.Fatalf("runs cover %d of 5000", total)
	}
	// Quantisation error.
	all := g.Instances()
	for _, in := range ins[:200] {
		found := false
		for _, o := range all {
			if math.Abs(float64(o.Pos[0]-in.Pos[0])) < 0.005 && math.Abs(float64(o.Pos[2]-in.Pos[2])) < 0.005 {
				found = true
				if o.Kind != in.Kind || math.Abs(float64(o.Pos[1]-in.Pos[1])) > 0.001 ||
					math.Abs(float64(o.Scale-in.Scale)) > 0.02 || math.Abs(float64(o.Tint[0]-in.Tint[0])) > 0.01 ||
					o.Rank != Rank(in.Pos[0], in.Pos[2]) {
					t.Fatalf("instance %+v came back as %+v", in, o)
				}
				d := math.Mod(float64(o.Yaw-in.Yaw)+3*math.Pi, 2*math.Pi) - math.Pi
				if math.Abs(d) > 0.02 {
					t.Fatalf("yaw %g came back %g", in.Yaw, o.Yaw)
				}
				break
			}
		}
		if !found {
			t.Fatalf("instance at %v lost", in.Pos)
		}
	}
	for n := 0; n < len(data); n += 997 {
		if _, err := DecodeField(data[:n]); err == nil {
			t.Fatalf("a field cut to %d bytes decoded", n)
		}
	}
}

// TestBuildFieldIsDeterministic: the input order never changes the file.
func TestBuildFieldIsDeterministic(t *testing.T) {
	spec := FieldSpec{Kit: "k.fgk", Min: [2]float32{0, 0}, Max: [2]float32{100, 100}, Cell: 16, Kinds: 2}
	ins := randomInstances(3000, 7, spec.Min, spec.Max, 2)
	a, _ := BuildField(spec, ins)
	shuffled := append([]Instance(nil), ins...)
	rand.New(rand.NewSource(3)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	b, _ := BuildField(spec, shuffled)
	da, _ := EncodeField(a)
	db, _ := EncodeField(b)
	if !bytes.Equal(da, db) {
		t.Fatal("shuffled input gave a different field")
	}
	// 100 m in 16 m cells: 7 a side, the last short.
	if a.CellsX != 7 || a.CellsZ != 7 {
		t.Fatalf("cells %dx%d", a.CellsX, a.CellsZ)
	}
}

// TestRanksAreUniform: the rank bytes spread evenly, so a prefix is an even
// thinning.
func TestRanksAreUniform(t *testing.T) {
	var hist [4]int
	for i := 0; i < 40000; i++ {
		hist[Rank(float32(i%200)*0.37, float32(i/200)*0.41)/64]++
	}
	for q, n := range hist {
		if n < 9000 || n > 11000 {
			t.Fatalf("rank quartile %d holds %d of 40000", q, n)
		}
	}
}

func TestFalloff(t *testing.T) {
	f := DefaultFalloff
	prev := float32(2)
	for d := float32(0); d <= f.Range+10; d += 0.5 {
		k := f.Keep(d, 0)
		if k > prev+1e-6 {
			t.Fatalf("keep rises at %g: %g after %g", d, k, prev)
		}
		prev = k
	}
	if f.Keep(f.Start, 0) != 1 || f.Keep(f.Range, 0) != 0 {
		t.Fatalf("keep at start %g, at range %g", f.Keep(f.Start, 0), f.Keep(f.Range, 0))
	}
	// A shorter kind is gone sooner.
	if f.Keep(f.Range*0.7, 0.6) != 0 || f.Keep(f.Range*0.7, 0) == 0 {
		t.Fatal("kind range not applied")
	}
	// The density setting thins everywhere.
	half := f.Scaled(1, 0.5)
	if math.Abs(float64(half.Keep(5, 0)-0.5)) > 1e-6 {
		t.Fatalf("density 0.5 keeps %g up close", half.Keep(5, 0))
	}
	// The distance setting stretches the curve.
	far := f.Scaled(2, 1)
	if math.Abs(float64(far.Keep(60, 0)-f.Keep(30, 0))) > 1e-5 {
		t.Fatal("distance scale is not a stretch")
	}
	// Survivors grow as the field thins, never shrink, never past MaxComp.
	pw := float32(0)
	for d := float32(1); d < f.Range; d += 3 {
		w, h := f.Grow(d)
		if w < pw-1e-6 || w > MaxComp || h > w+1e-6 || h < 1 {
			t.Fatalf("grow at %g: %g, %g", d, w, h)
		}
		pw = w
	}
}

// TestDrawCountCoversEveryVisibleInstance: an instance of rank r in a cell
// whose nearest point is dmin shows (RankFade > 0) at its own distance
// d ≥ dmin only if the CPU drew it — the prefix never cuts off something
// the vertex stage would show.
func TestDrawCountCoversEveryVisibleInstance(t *testing.T) {
	f := DefaultFalloff.Scaled(1, 0.8)
	const count = 256
	for dmin := float32(0); dmin < f.Range; dmin += 2.5 {
		n := f.DrawCount(count, dmin, 0)
		for i := n; i < count; i++ {
			rank := (float32(i) + 0.5) / count
			for d := dmin; d < dmin+30; d += 1 {
				if RankFade(f.Keep(d, 0), rank) > 0 {
					t.Fatalf("dmin %g: instance %d (rank %.3f) shows at %g but only %d drawn", dmin, i, rank, d, n)
				}
			}
		}
	}
}

// TestFadeIsNarrow: the dissolve band is a few percent of the survivors —
// an instance goes from fully shown to gone over a short distance, not a
// visible gradient across the field.
func TestFadeIsNarrow(t *testing.T) {
	f := DefaultFalloff
	rank := float32(0.2)
	var in, out float32
	for d := float32(1); d < f.Range; d += 0.05 {
		v := RankFade(f.Keep(d, 0), rank)
		if v < 1 && in == 0 {
			in = d
		}
		if v == 0 {
			out = d
			break
		}
	}
	if in == 0 || out == 0 || out-in > 0.12*out {
		t.Fatalf("rank 0.2 fades from %g to %g m", in, out)
	}
}

func TestTiers(t *testing.T) {
	f := DefaultFalloff
	tiers := f.Tiers(3)
	if len(tiers) != 3 {
		t.Fatalf("%d tiers", len(tiers))
	}
	for d := float32(0); d < f.Range; d += 0.25 {
		sum := float32(0)
		for _, tr := range tiers {
			sum += tr.Weight(d)
		}
		if math.Abs(float64(sum-1)) > 1e-5 {
			t.Fatalf("tier weights sum to %g at %g", sum, d)
		}
		for _, tr := range tiers {
			if tr.Weight(d) > 0 && !tr.Meets(d, d) {
				t.Fatalf("tier %+v shows at %g but a cell there would not draw it", tr, d)
			}
		}
	}
	if len(f.Tiers(1)) != 1 || len(f.Tiers(2)) != 2 {
		t.Fatal("tiers by LOD count")
	}
}

func TestThinKeepsHeightAndCover(t *testing.T) {
	// 40 blades in a ring.
	src := &SourceMesh{}
	for b := 0; b < 40; b++ {
		a := float64(b) / 40 * 2 * math.Pi
		x, z := float32(0.3*math.Cos(a)), float32(0.3*math.Sin(a))
		base := uint32(len(src.Positions))
		src.Positions = append(src.Positions, [3]float32{x - 0.01, 0, z}, [3]float32{x + 0.01, 0, z}, [3]float32{x, 1.2, z})
		src.Normals = append(src.Normals, [3]float32{0, 0, 1}, [3]float32{0, 0, 1}, [3]float32{0, 0, 1})
		src.UVs = append(src.UVs, [2]float32{0, 0}, [2]float32{1, 0}, [2]float32{0.5, 1})
		src.Indices = append(src.Indices, base, base+1, base+2)
	}
	th := Thin(src, 0.25, 9)
	n := len(th.Indices) / 3
	if n < 4 || n > 18 {
		t.Fatalf("thinned to %d of 40 blades at 0.25", n)
	}
	h0, _ := src.Extent()
	h1, _ := th.Extent()
	if h0 != h1 {
		t.Fatalf("height %g became %g", h0, h1)
	}
	// Each kept blade is wider (×2 at a quarter).
	w := th.Positions[1][0] - th.Positions[0][0]
	if math.Abs(float64(w)-0.04) > 1e-4 {
		t.Fatalf("kept blade width %g, want 0.04", w)
	}
	again := Thin(src, 0.25, 9)
	if len(again.Indices) != len(th.Indices) || again.Positions[0] != th.Positions[0] {
		t.Fatal("thinning is not deterministic")
	}
	if one := Thin(src, 0.0001, 9); len(one.Indices) != 3 {
		t.Fatalf("a near-zero fraction kept %d triangles, want one blade", len(one.Indices)/3)
	}
	m := KindMesh(src, h0, 0.31, 0.2, 0.05)
	if m.VertexCount() != len(src.Positions) || m.Vertices[2*VertexFloats+8] != 1 || m.Vertices[8] != 0 {
		t.Fatal("wind weights: base 0, tip 1")
	}
}
