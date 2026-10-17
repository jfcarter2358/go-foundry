package meshcollide

import (
	"math"
	"math/rand"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// ---------- fixtures ----------

// boxMesh is a closed axis-aligned box, 8 corners, wound outward.
func boxMesh(lo, hi mgl32.Vec3) ([]mgl32.Vec3, []uint32) {
	var pos []mgl32.Vec3
	for i := 0; i < 8; i++ {
		p := lo
		if i&1 != 0 {
			p[0] = hi[0]
		}
		if i&2 != 0 {
			p[1] = hi[1]
		}
		if i&4 != 0 {
			p[2] = hi[2]
		}
		pos = append(pos, p)
	}
	quads := [][4]uint32{
		{0, 4, 6, 2}, // -x
		{1, 3, 7, 5}, // +x
		{0, 1, 5, 4}, // -y
		{2, 6, 7, 3}, // +y
		{0, 2, 3, 1}, // -z
		{4, 5, 7, 6}, // +z
	}
	var idx []uint32
	for _, q := range quads {
		idx = append(idx, q[0], q[1], q[2], q[0], q[2], q[3])
	}
	return pos, idx
}

// lMesh is a closed L-shaped prism: the outline (0,0) (2,0) (2,1) (1,1)
// (1,2) (0,2) in XZ, from y 0 to 1. Its notch is the square (1..2, 1..2).
func lMesh() ([]mgl32.Vec3, []uint32) {
	outline := [][2]float32{{0, 0}, {2, 0}, {2, 1}, {1, 1}, {1, 2}, {0, 2}}
	n := uint32(len(outline))
	var pos []mgl32.Vec3
	for _, y := range []float32{0, 1} {
		for _, o := range outline {
			pos = append(pos, mgl32.Vec3{o[0], y, o[1]})
		}
	}
	var idx []uint32
	// Caps fanned from the reflex corner (3), which sees every other corner.
	for i := uint32(0); i < n; i++ {
		a, b := (3+i)%n, (4+i)%n
		if a == 3 || b == 3 {
			continue
		}
		idx = append(idx, 3, b, a)       // bottom, facing -y
		idx = append(idx, n+3, n+a, n+b) // top, facing +y
	}
	for i := uint32(0); i < n; i++ {
		a, b := i, (i+1)%n
		idx = append(idx, a, b, n+b, a, n+b, n+a)
	}
	return pos, idx
}

// icosphere is a unit sphere at c, subdivided level times.
func icosphere(c mgl32.Vec3, r float32, level int) ([]mgl32.Vec3, []uint32) {
	t := float32((1 + math.Sqrt(5)) / 2)
	pos := []mgl32.Vec3{
		{-1, t, 0}, {1, t, 0}, {-1, -t, 0}, {1, -t, 0},
		{0, -1, t}, {0, 1, t}, {0, -1, -t}, {0, 1, -t},
		{t, 0, -1}, {t, 0, 1}, {-t, 0, -1}, {-t, 0, 1},
	}
	for i := range pos {
		pos[i] = pos[i].Normalize()
	}
	idx := []uint32{0, 11, 5, 0, 5, 1, 0, 1, 7, 0, 7, 10, 0, 10, 11, 1, 5, 9, 5, 11, 4, 11, 10, 2, 10, 7, 6, 7, 1, 8,
		3, 9, 4, 3, 4, 2, 3, 2, 6, 3, 6, 8, 3, 8, 9, 4, 9, 5, 2, 4, 11, 6, 2, 10, 8, 6, 7, 9, 8, 1}
	for l := 0; l < level; l++ {
		mid := map[[2]uint32]uint32{}
		m := func(a, b uint32) uint32 {
			k := [2]uint32{min(a, b), max(a, b)}
			if v, ok := mid[k]; ok {
				return v
			}
			pos = append(pos, pos[a].Add(pos[b]).Normalize())
			mid[k] = uint32(len(pos) - 1)
			return mid[k]
		}
		var next []uint32
		for i := 0; i < len(idx); i += 3 {
			a, b, cc := idx[i], idx[i+1], idx[i+2]
			ab, bc, ca := m(a, b), m(b, cc), m(cc, a)
			next = append(next, a, ab, ca, b, bc, ab, cc, ca, bc, ab, bc, ca)
		}
		idx = next
	}
	for i := range pos {
		pos[i] = c.Add(pos[i].Mul(r))
	}
	return pos, idx
}

func flat(pos []mgl32.Vec3) []float32 {
	out := make([]float32, 0, 3*len(pos))
	for _, p := range pos {
		out = append(out, p[0], p[1], p[2])
	}
	return out
}

func toV3(pos []mgl32.Vec3) []v3 {
	out := make([]v3, len(pos))
	for i, p := range pos {
		out[i] = v3{float64(p[0]), float64(p[1]), float64(p[2])}
	}
	return out
}

// insideHull reports whether p is inside h within tol.
func insideHull(h *Hull, p mgl32.Vec3, tol float32) bool {
	for _, pl := range h.Planes {
		if (mgl32.Vec3{pl[0], pl[1], pl[2]}).Dot(p)-pl[3] > tol {
			return false
		}
	}
	return true
}

// ---------- the hull ----------

func TestQuickHullOfACubeAndItsInsides(t *testing.T) {
	pos, _ := boxMesh(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 1, 1})
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		pos = append(pos, mgl32.Vec3{rng.Float32(), rng.Float32(), rng.Float32()})
	}
	pts := toV3(pos)
	h, ok := quickHull(pts)
	if !ok {
		t.Fatal("no hull of a cube")
	}
	if len(h.verts) != 8 {
		t.Errorf("a cube's hull has %d vertices, want 8", len(h.verts))
	}
	if v := hullVolume(pts, h); math.Abs(v-1) > 1e-9 {
		t.Errorf("volume %v, want 1", v)
	}
}

func TestQuickHullHoldsEveryPointAndIsClosed(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		var pts []v3
		for i := 0; i < 50+trial*40; i++ {
			// A blob: points in a ball with a few far ones.
			p := v3{rng.NormFloat64(), rng.NormFloat64() * 0.5, rng.NormFloat64() * 2}
			pts = append(pts, p)
		}
		h, ok := quickHull(pts)
		if !ok {
			t.Fatal("no hull")
		}
		planes := planesOf(pts, h)
		for _, p := range pts {
			for _, pl := range planes {
				if d := pl[0]*p[0] + pl[1]*p[1] + pl[2]*p[2] - pl[3]; d > 1e-6 {
					t.Fatalf("trial %d: a point is %v outside its hull", trial, d)
				}
			}
		}
		// Closed: every directed edge has its reverse.
		edges := map[[2]int]int{}
		for _, tr := range h.tris {
			for k := 0; k < 3; k++ {
				edges[[2]int{tr[k], tr[(k+1)%3]}]++
			}
		}
		for e, n := range edges {
			if n != 1 || edges[[2]int{e[1], e[0]}] != 1 {
				t.Fatalf("trial %d: edge %v used %d times, reverse %d", trial, e, n, edges[[2]int{e[1], e[0]}])
			}
		}
		// Euler: V - E + F = 2.
		if v, e, f := len(h.verts), len(edges)/2, len(h.tris); v-e+f != 2 {
			t.Fatalf("trial %d: V-E+F = %d", trial, v-e+f)
		}
	}
}

func TestQuickHullRefusesAFlatSet(t *testing.T) {
	pts := []v3{{0, 0, 0}, {1, 0, 0}, {0, 0, 1}, {1, 0, 1}, {0.5, 0, 0.5}}
	if _, ok := quickHull(pts); ok {
		t.Error("a flat point set made a hull")
	}
}

// ---------- the clean ----------

func TestBuildWeldsSeamsAndCountsOpenEdges(t *testing.T) {
	// A box whose every face has its own four corners (a UV seam on every
	// edge), as a render pool has it, plus one degenerate triangle.
	pos, idx := boxMesh(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 1, 1})
	var split []mgl32.Vec3
	var sidx []uint32
	for i := 0; i < len(idx); i += 3 {
		for k := 0; k < 3; k++ {
			split = append(split, pos[idx[i+k]])
			sidx = append(sidx, uint32(len(split)-1))
		}
	}
	sidx = append(sidx, 0, 0, 1)
	d, err := Build(Input{Positions: flat(split), Stride: 3, Indices: sidx}, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if d.VertexCount() != 8 {
		t.Errorf("%d vertices after the weld, want 8", d.VertexCount())
	}
	if d.TriangleCount() != 12 || d.Degenerate != 1 {
		t.Errorf("%d triangles, %d degenerate; want 12 and 1", d.TriangleCount(), d.Degenerate)
	}
	if d.OpenEdges != 0 || d.NonManifold != 0 {
		t.Errorf("a welded box has %d open and %d non-manifold edges", d.OpenEdges, d.NonManifold)
	}

	// A 4x4 sheet of quads: open all round its 16-edge rim.
	var sheet []mgl32.Vec3
	for z := 0; z <= 4; z++ {
		for x := 0; x <= 4; x++ {
			sheet = append(sheet, mgl32.Vec3{float32(x), 0, float32(z)})
		}
	}
	var si []uint32
	for z := 0; z < 4; z++ {
		for x := 0; x < 4; x++ {
			a := uint32(z*5 + x)
			si = append(si, a, a+5, a+1, a+1, a+5, a+6)
		}
	}
	d, err = Build(Input{Positions: flat(sheet), Stride: 3, Indices: si}, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if d.OpenEdges != 16 {
		t.Errorf("a 4x4 sheet has %d open edges, want 16", d.OpenEdges)
	}
	if len(d.Hulls) == 0 {
		t.Error("a sheet made no hull: it should be thickened")
	}
}

func TestQuantisationIsWithinItsStep(t *testing.T) {
	pos, idx := icosphere(mgl32.Vec3{3, -2, 50}, 7, 2)
	d, err := Build(Input{Positions: flat(pos), Stride: 3, Indices: idx}, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	step := d.Hi.Sub(d.Lo).Mul(1.0 / 65535)
	r := NewResident(d)
	for _, p := range r.Pos {
		// Every decoded vertex is near some source vertex.
		best := float32(math.MaxFloat32)
		for _, q := range pos {
			best = min(best, p.Sub(q).Len())
		}
		if best > step.Len() {
			t.Fatalf("a decoded vertex is %v from the source, step %v", best, step.Len())
		}
	}
}

// ---------- the tree ----------

// TestTreeRaysMatchBruteForce is the oracle: the tree's nearest hit is the
// nearest hit of every triangle tested in turn, for rays from everywhere.
func TestTreeRaysMatchBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	// A bumpy sphere and a scatter of loose triangles, so the tree has both
	// a surface and clutter to sort.
	pos, idx := icosphere(mgl32.Vec3{0, 0, 0}, 5, 3)
	for i := range pos {
		pos[i] = pos[i].Mul(1 + 0.08*float32(rng.NormFloat64()))
	}
	for i := 0; i < 300; i++ {
		c := mgl32.Vec3{rng.Float32()*30 - 15, rng.Float32()*30 - 15, rng.Float32()*30 - 15}
		base := uint32(len(pos))
		for k := 0; k < 3; k++ {
			pos = append(pos, c.Add(mgl32.Vec3{rng.Float32() - 0.5, rng.Float32() - 0.5, rng.Float32() - 0.5}.Mul(2)))
		}
		idx = append(idx, base, base+1, base+2)
	}
	d, err := Build(Input{Positions: flat(pos), Stride: 3, Indices: idx}, Settings{MaxHulls: 1})
	if err != nil {
		t.Fatal(err)
	}
	r := NewResident(d)
	hits := 0
	for i := 0; i < 3000; i++ {
		o := mgl32.Vec3{rng.Float32()*40 - 20, rng.Float32()*40 - 20, rng.Float32()*40 - 20}
		dir := mgl32.Vec3{float32(rng.NormFloat64()), float32(rng.NormFloat64()), float32(rng.NormFloat64())}
		switch i % 5 {
		case 0:
			dir = mgl32.Vec3{0, -1, 0} // axis-aligned rays take the zero-direction path
		case 1, 2:
			// Aimed at the sphere, so plenty hit.
			dir = mgl32.Vec3{rng.Float32()*8 - 4, rng.Float32()*8 - 4, rng.Float32()*8 - 4}.Sub(o).Normalize()
		}
		dir = dir.Mul(0.5 + rng.Float32()*3) // not unit: t is in its units
		tMax := rng.Float32() * 40
		want, found := float32(math.MaxFloat32), false
		for tr := 0; tr < d.TriangleCount(); tr++ {
			a, b, c := r.Tri(tr)
			if th, ok := rayTri(o, dir, a, b, c); ok && th <= tMax && th < want {
				want, found = th, true
			}
		}
		got, _, ok := r.Raycast(o, dir, tMax)
		if ok != found || (ok && math.Abs(float64(got-want)) > 1e-5*float64(max(1, want))) {
			t.Fatalf("ray %d: tree says (%v, %v), brute force (%v, %v)", i, got, ok, want, found)
		}
		if r.AnyHit(o, dir, tMax) != found {
			t.Fatalf("ray %d: AnyHit disagrees", i)
		}
		if found {
			hits++
		}
	}
	if hits < 600 {
		t.Errorf("only %d of 3000 rays hit: the oracle is not testing much", hits)
	}
}

func TestBoxQueriesFindEveryTouchingTriangle(t *testing.T) {
	pos, idx := icosphere(mgl32.Vec3{0, 0, 0}, 5, 3)
	d, _ := Build(Input{Positions: flat(pos), Stride: 3, Indices: idx}, Settings{MaxHulls: 1})
	r := NewResident(d)
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 200; i++ {
		c := mgl32.Vec3{rng.Float32()*12 - 6, rng.Float32()*12 - 6, rng.Float32()*12 - 6}
		h := mgl32.Vec3{rng.Float32(), rng.Float32(), rng.Float32()}
		lo, hi := c.Sub(h), c.Add(h)
		got := map[int]bool{}
		r.TrianglesInBox(lo, hi, func(tr int) bool { got[tr] = true; return true })
		for tr := 0; tr < d.TriangleCount(); tr++ {
			a, b, cc := r.Tri(tr)
			tlo, thi := vmin(vmin(a, b), cc), vmax(vmax(a, b), cc)
			touches := tlo[0] <= hi[0] && thi[0] >= lo[0] && tlo[1] <= hi[1] && thi[1] >= lo[1] && tlo[2] <= hi[2] && thi[2] >= lo[2]
			if touches && !got[tr] {
				t.Fatalf("box %d: triangle %d touches it and was not reported", i, tr)
			}
		}
		if r.NearBox(lo, hi) != (len(got) > 0) {
			t.Fatalf("box %d: NearBox disagrees with the walk", i)
		}
	}
}

// ---------- the hulls ----------

func TestAConvexMeshIsOneHull(t *testing.T) {
	pos, idx := boxMesh(mgl32.Vec3{-1, 0, -2}, mgl32.Vec3{1, 3, 2})
	dec := Decompose(pos, idx, Settings{})
	if len(dec.Hulls) != 1 || dec.CapHit || dec.Concavity > 1e-4 {
		t.Fatalf("a box made %d hulls (cap %v, concavity %v), want 1", len(dec.Hulls), dec.CapHit, dec.Concavity)
	}
	if n := len(dec.Hulls[0].Pts); n != 8 {
		t.Errorf("its hull has %d points, want 8", n)
	}
}

func TestAnLShapeSplitsAtItsInnerCorner(t *testing.T) {
	pos, idx := lMesh()
	s := Settings{Concavity: 0.01, ConcavityRel: 0.001}
	dec := Decompose(pos, idx, s)
	if len(dec.Hulls) < 2 {
		t.Fatalf("an L made %d hull(s)", len(dec.Hulls))
	}
	if dec.Concavity > 0.01 {
		t.Errorf("concavity left %v, tolerance 0.01", dec.Concavity)
	}
	// Every surface point is in some hull, and the notch is in none.
	for _, p := range pos {
		in := false
		for i := range dec.Hulls {
			in = in || insideHull(&dec.Hulls[i], p, 1e-4)
		}
		if !in {
			t.Errorf("corner %v is outside every hull", p)
		}
	}
	notch := mgl32.Vec3{1.5, 0.5, 1.5}
	for i := range dec.Hulls {
		if insideHull(&dec.Hulls[i], notch, 0) {
			t.Errorf("hull %d fills the notch", i)
		}
	}
	// Mutation check: one hull (the cap) must bridge the notch.
	one := Decompose(pos, idx, Settings{MaxHulls: 1, Concavity: 0.01, ConcavityRel: 0.001})
	if !one.CapHit || len(one.Hulls) != 1 || !insideHull(&one.Hulls[0], notch, 0) {
		t.Errorf("capped at one hull: cap %v, %d hulls — the single hull should bridge the notch", one.CapHit, len(one.Hulls))
	}
}

// A hull past the vertex cap keeps that many of its points, chosen by
// farthest-point insertion: inside the sphere, which stands outside it by no
// more than CapError, and CapError small — and smaller at a higher cap.
func TestHullVertexCapKeepsTheBestPoints(t *testing.T) {
	pos, idx := icosphere(mgl32.Vec3{0, 0, 0}, 1, 3) // 642 vertices
	prev := float32(1)
	for _, cap := range []int{32, 64, 128} {
		dec := Decompose(pos, idx, Settings{MaxHullVerts: cap, MaxHulls: 1})
		if len(dec.Hulls) != 1 || dec.VertCapped != 1 {
			t.Fatalf("cap %d: %d hulls, %d capped; want 1 and 1", cap, len(dec.Hulls), dec.VertCapped)
		}
		h := dec.Hulls[0]
		if len(h.Pts) > cap || len(h.Pts) < cap*3/4 {
			t.Errorf("cap %d: the capped hull has %d points", cap, len(h.Pts))
		}
		for _, p := range h.Pts {
			if p.Len() > 1+1e-5 {
				t.Errorf("cap %d: a capped hull point %v is outside the sphere", cap, p)
			}
		}
		for _, p := range pos {
			if !insideHull(&h, p, dec.CapError+1e-5) {
				t.Fatalf("cap %d: sphere point %v is more than the cap error outside", cap, p)
			}
		}
		if dec.CapError <= 0 || dec.CapError >= prev {
			t.Errorf("cap %d: cap error %v (the cap below it: %v)", cap, dec.CapError, prev)
		}
		prev = dec.CapError
	}
	if prev > 0.04 {
		t.Errorf("128 points leave a unit sphere %v short", prev)
	}

	// A long, thin piece, where a fixed set of directions crowds the ends:
	// a 10 x 0.5 x 0.5 capsule-ish rod.
	rod, ridx := icosphere(mgl32.Vec3{0, 0, 0}, 1, 3)
	for i := range rod {
		rod[i] = mgl32.Vec3{rod[i][0] * 5, rod[i][1] * 0.25, rod[i][2] * 0.25}
	}
	dec := Decompose(rod, ridx, Settings{MaxHullVerts: 64, MaxHulls: 1})
	if dec.CapError > 0.04 {
		t.Errorf("a 10 m rod capped to 64 points is %v short", dec.CapError)
	}
}

func TestDecompositionIsDeterministic(t *testing.T) {
	pos, idx := lMesh()
	a := Decompose(pos, idx, Settings{Concavity: 0.01})
	b := Decompose(pos, idx, Settings{Concavity: 0.01})
	if len(a.Hulls) != len(b.Hulls) {
		t.Fatal("two decompositions disagree on the count")
	}
	for i := range a.Hulls {
		if len(a.Hulls[i].Pts) != len(b.Hulls[i].Pts) {
			t.Fatal("two decompositions disagree")
		}
		for k := range a.Hulls[i].Pts {
			if a.Hulls[i].Pts[k] != b.Hulls[i].Pts[k] {
				t.Fatal("two decompositions disagree")
			}
		}
	}
}

// ---------- modes ----------

func TestModeResolution(t *testing.T) {
	pos, idx := lMesh()
	d, err := Build(Input{Positions: flat(pos), Stride: 3, Indices: idx}, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		over Mode
		size float32
		want Mode
	}{
		{ModeAuto, 2, ModeHulls},
		{ModeAuto, 40, ModeMesh},
		{ModeMesh, 2, ModeMesh},
		{ModeHulls, 40, ModeHulls},
		{ModeBox, 2, ModeBox},
		{ModeNone, 2, ModeNone},
	}
	for _, c := range cases {
		if got := d.Resolve(c.over, c.size); got != c.want {
			t.Errorf("Resolve(%v, %v) = %v, want %v", c.over, c.size, got, c.want)
		}
	}
	var none *Data
	if got := none.Resolve(ModeMesh, 1); got != ModeBox {
		t.Errorf("no data resolves mesh to %v, want box", got)
	}
	for _, n := range ModeNames() {
		m, ok := ParseMode(n)
		if !ok || m.String() != n {
			t.Errorf("ParseMode(%q) = %v, %v", n, m, ok)
		}
	}
	if _, ok := ParseMode("bogus"); ok {
		t.Error("a bogus mode parsed")
	}
}

func BenchmarkDecomposeSphere(b *testing.B) {
	pos, idx := icosphere(mgl32.Vec3{0, 0, 0}, 1, 4)
	for i := 0; i < b.N; i++ {
		Decompose(pos, idx, Settings{})
	}
}

// TestMeshModeCookSkipsTheHulls: a mesh cooked to collide as itself has its
// tree and no hulls, still validates, and a placement asking it for hulls
// gets its box.
func TestMeshModeCookSkipsTheHulls(t *testing.T) {
	pos, idx := boxMesh(mgl32.Vec3{-1, 0, -2}, mgl32.Vec3{1, 3, 2})
	flat := make([]float32, 0, 3*len(pos))
	for _, p := range pos {
		flat = append(flat, p[0], p[1], p[2])
	}
	d, err := Build(Input{Positions: flat, Indices: idx}, Settings{Mode: ModeMesh})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Hulls) != 0 || d.TriangleCount() != 12 || len(d.Nodes) == 0 {
		t.Errorf("%d hulls, %d triangles, %d nodes", len(d.Hulls), d.TriangleCount(), len(d.Nodes))
	}
	if m := d.Resolve(ModeAuto, 100); m != ModeMesh {
		t.Errorf("auto resolves to %v", m)
	}
	if m := d.Resolve(ModeHulls, 1); m != ModeBox {
		t.Errorf("a hulls override resolves to %v, want the box", m)
	}
}
