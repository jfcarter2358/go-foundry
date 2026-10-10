package collision

// raycast_accel_test.go — the culled raycast must answer exactly what the
// brute-force one did (raycast_oracle_test.go), on levels built to exercise
// every path a ray can take: chunks moved, turned and scaled, dynamic brushes
// at rest and moved, a frame-owned chunk, a compiled chunk with its surfaces, a
// hidden compiled chunk, a disabled chunk, and every kind of body.

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"unsafe"

	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

type stressOpts struct {
	chunks, brushesPerChunk, bodies int
	extent                          float32
	// dynamicEvery makes one brush in this many dynamic; 0 means one in seven,
	// far more than a real level, to exercise the path.
	dynamicEvery int
}

// raycastStressLevel builds a deterministic level of random boxes and bodies.
func raycastStressLevel(tb testing.TB, seed int64, o stressOpts) *world.Level {
	tb.Helper()
	rng := rand.New(rand.NewSource(seed))
	r := func(lo, hi float32) float32 { return lo + rng.Float32()*(hi-lo) }
	lvl := &world.Level{}
	side := int(math.Ceil(math.Sqrt(float64(o.chunks))))
	cellW := o.extent / float32(side)
	for c := 0; c < o.chunks; c++ {
		ch := world.Chunk{ID: fmt.Sprintf("c%d", c)}
		ch.Origin = mgl32.Vec3{float32(c%side) * cellW, r(-1, 1), float32(c/side) * cellW}
		switch c % 5 {
		case 1:
			ch.Rotation = r(-3, 3)
		case 2:
			ch.Scale = r(0.5, 2)
		case 3:
			ch.Rotation, ch.Scale = r(-3, 3), r(0.7, 1.4)
		}
		for b := 0; b < o.brushesPerChunk; b++ {
			br := world.NewBoxBrush(fmt.Sprintf("c%d_b%d", c, b),
				mgl32.Vec3{r(0, cellW), r(-2, 6), r(0, cellW)},
				mgl32.Vec3{r(0.1, 2), r(0.1, 2), r(0.1, 2)}, "")
			if rng.Intn(9) == 0 {
				br.Faces[rng.Intn(len(br.Faces))].NoDraw = true
			}
			every := o.dynamicEvery
			if every == 0 {
				every = 7
			}
			if rng.Intn(every) == 0 {
				br.Dynamic = true
				if rng.Intn(2) == 0 {
					br.Offset = mgl32.Vec3{r(-3, 3), r(-3, 3), r(-3, 3)}
					br.Rotation = mgl32.Vec3{r(-1, 1), r(-1, 1), r(-1, 1)}
					br.Scale = mgl32.Vec3{r(0.5, 1.5), r(0.5, 1.5), r(0.5, 1.5)}
				}
			}
			ch.Brushes = append(ch.Brushes, br)
		}
		lvl.Chunks = append(lvl.Chunks, ch)
	}
	// One chunk rides a frame, one is compiled, one is switched off.
	if len(lvl.Chunks) > 3 {
		f := &lvl.Chunks[1]
		f.FrameOwner = "ship"
		f.Xform = mgl32.Translate3D(r(-5, 5), r(0, 3), r(-5, 5)).Mul4(mgl32.HomogRotate3DX(r(-0.5, 0.5)))
		lvl.Chunks[2].Compiled = true
		lvl.Chunks[3].Disabled = true
	}
	if t, ok := tb.(*testing.T); ok {
		testenv.WithoutGeometryUploads(t)
	} else {
		gfx.GeometryUploadsEnabled = false // a benchmark's binary never draws
	}
	for ci := range lvl.Chunks {
		for bi := range lvl.Chunks[ci].Brushes {
			if err := lvl.Chunks[ci].Brushes[bi].Load(); err != nil {
				tb.Fatal(err)
			}
		}
		lvl.Chunks[ci].RecomputeBounds()
	}
	// The compiled chunk's static brushes are surfaces, in world space.
	if len(lvl.Chunks) > 3 {
		static := &world.StaticGeometry{HiddenChunks: map[string]bool{}}
		for _, ci := range []int{2, 4 % len(lvl.Chunks)} {
			ch := &lvl.Chunks[ci]
			m := ch.ModelMatrix()
			for bi := range ch.Brushes {
				b := &ch.Brushes[bi]
				for fi := range b.Faces {
					g := b.Faces[fi].Geometry
					if len(g.Vertices) == 0 {
						continue
					}
					var s world.StaticSurface
					s.Chunk, s.Brush = ch.ID, b.ID
					s.Geometry.Vertices = make([]float32, len(g.Vertices))
					copy(s.Geometry.Vertices, g.Vertices)
					for i := 0; i+4 < len(s.Geometry.Vertices); i += 5 {
						p := m.Mul4x1(mgl32.Vec4{g.Vertices[i], g.Vertices[i+1], g.Vertices[i+2], 1})
						s.Geometry.Vertices[i], s.Geometry.Vertices[i+1], s.Geometry.Vertices[i+2] = p.X(), p.Y(), p.Z()
					}
					static.Surfaces = append(static.Surfaces, s)
				}
			}
		}
		static.HiddenChunks[lvl.Chunks[4%len(lvl.Chunks)].ID] = true
		lvl.Static = static
	}
	for i := 0; i < o.bodies; i++ {
		e := world.NewBlankEntity(fmt.Sprintf("e%d", i))
		e.Position = mgl32.Vec3{r(0, o.extent), r(-1, 4), r(0, o.extent)}
		e.Rotation = mgl32.Vec3{0, r(-3, 3), 0}
		e.BoundingKind = world.BoundingType(rng.Intn(3))
		e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ = r(0.2, 1.5), r(0.2, 1.5), r(0.2, 1.5)
		e.Scale = mgl32.Vec3{r(0.5, 2), r(0.5, 2), r(0.5, 2)}
		e.Visible = rng.Intn(4) != 0
		if rng.Intn(3) == 0 {
			e.Tags = []string{"t"}
		}
		lvl.Entities = append(lvl.Entities, e)

		v := world.NewVolume(fmt.Sprintf("v%d", i), mgl32.Vec3{r(0, o.extent), r(-1, 4), r(0, o.extent)})
		v.Shape = world.VolumeShape(rng.Intn(3))
		v.HalfX, v.HalfY, v.HalfZ = r(0.3, 3), r(0.3, 3), r(0.3, 3)
		v.Rotation = mgl32.Vec3{r(-1, 1), r(-1, 1), r(-1, 1)}
		v.Enabled = rng.Intn(5) != 0
		lvl.Volumes = append(lvl.Volumes, v)

		s := world.NewScenery(fmt.Sprintf("s%d", i), "", mgl32.Vec3{r(0, o.extent), r(-1, 4), r(0, o.extent)})
		s.LocalMin, s.LocalMax = mgl32.Vec3{-r(0.2, 1), 0, -r(0.2, 1)}, mgl32.Vec3{r(0.2, 1), r(0.5, 2), r(0.2, 1)}
		s.Rotation = mgl32.Vec3{0, r(-3, 3), 0}
		s.Solid = rng.Intn(4) != 0
		lvl.Scenery = append(lvl.Scenery, s)
	}
	return lvl
}

func randomRay(rng *rand.Rand, extent float32) (mgl32.Vec3, mgl32.Vec3) {
	r := func(lo, hi float32) float32 { return lo + rng.Float32()*(hi-lo) }
	origin := mgl32.Vec3{r(-5, extent+5), r(-3, 7), r(-5, extent+5)}
	dir := mgl32.Vec3{r(-1, 1), r(-0.6, 0.6), r(-1, 1)}
	if rng.Intn(10) == 0 {
		dir = mgl32.Vec3{0, 0, r(-1, 1)} // axis-parallel, the slab test's edge case
	}
	return origin, dir
}

func randomFilter(rng *rand.Rand, extent float32) RayFilter {
	f := RayFilter{
		Range:    rng.Float32() * extent * 1.5,
		Entities: rng.Intn(3) != 0,
		Volumes:  rng.Intn(3) == 0,
		Geometry: rng.Intn(5) != 0,
	}
	if rng.Intn(6) == 0 {
		f.VisibleOnly = true
	}
	if rng.Intn(6) == 0 {
		f.Tag = "t"
	}
	if rng.Intn(4) == 0 {
		f.Ignore = map[string]bool{
			fmt.Sprintf("e%d", rng.Intn(20)):                  true,
			fmt.Sprintf("c%d_b%d", rng.Intn(9), rng.Intn(40)): true,
		}
	}
	return f
}

func sameResult(a, b RayResult) error {
	if a.Blocked != b.Blocked {
		return fmt.Errorf("blocked %v, oracle %v", a.Blocked, b.Blocked)
	}
	if a.Stop.Sub(b.Stop).Len() > 2e-3 {
		return fmt.Errorf("stopped at %v, oracle %v", a.Stop, b.Stop)
	}
	if len(a.Hits) != len(b.Hits) {
		return fmt.Errorf("%d hits %+v, oracle %d %+v", len(a.Hits), a.Hits, len(b.Hits), b.Hits)
	}
	for i := range a.Hits {
		h, o := a.Hits[i], b.Hits[i]
		if h.Kind != o.Kind || h.ID != o.ID || h.Chunk != o.Chunk || absf32(h.T-o.T) > 2e-3 {
			return fmt.Errorf("hit %d is %+v, oracle %+v", i, h, o)
		}
	}
	return nil
}

func absf32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func TestCulledRaycastMatchesTheBruteForceOne(t *testing.T) {
	const extent = 60
	for seed := int64(1); seed <= 6; seed++ {
		lvl := raycastStressLevel(t, seed, stressOpts{chunks: 9, brushesPerChunk: 40, bodies: 20, extent: extent})
		rng := rand.New(rand.NewSource(seed * 7919))
		for i := 0; i < 1500; i++ {
			origin, dir := randomRay(rng, extent)
			f := randomFilter(rng, extent)
			got := Raycast(lvl, origin, dir, f)
			want := bfRaycast(lvl, origin, dir, f)
			if err := sameResult(got, want); err != nil {
				t.Fatalf("seed %d ray %d from %v along %v, filter %+v: %v", seed, i, origin, dir, f, err)
			}
		}
	}
}

func TestLineBlockedMatchesAGeometryCast(t *testing.T) {
	const extent = 60
	for seed := int64(11); seed <= 14; seed++ {
		lvl := raycastStressLevel(t, seed, stressOpts{chunks: 9, brushesPerChunk: 40, bodies: 20, extent: extent})
		rng := rand.New(rand.NewSource(seed))
		blocked := 0
		for i := 0; i < 2000; i++ {
			from, dir := randomRay(rng, extent)
			to := from.Add(dir.Normalize().Mul(rng.Float32() * 40))
			var ignore map[string]bool
			if rng.Intn(3) == 0 {
				ignore = map[string]bool{fmt.Sprintf("c%d_b%d", rng.Intn(9), rng.Intn(40)): true, fmt.Sprintf("s%d", rng.Intn(20)): true}
			}
			want := bfRaycast(lvl, from, to.Sub(from), RayFilter{Range: to.Sub(from).Len(), Geometry: true, Ignore: ignore}).Blocked
			if got := LineBlocked(lvl, from, to, ignore); got != want {
				t.Fatalf("seed %d segment %d, %v to %v: LineBlocked %v, a geometry cast %v", seed, i, from, to, got, want)
			}
			if want {
				blocked++
			}
		}
		if blocked == 0 || blocked == 2000 {
			t.Fatalf("seed %d: %d of 2000 segments blocked — the test proves nothing", seed, blocked)
		}
	}
}

func TestTheCullingNoticesAMovedDynamicBrushAndARebuiltMesh(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch := world.Chunk{ID: "c"}
	wall := world.NewBoxBrush("wall", mgl32.Vec3{0, 0, 10}, mgl32.Vec3{2, 2, 0.5}, "")
	door := world.NewBoxBrush("door", mgl32.Vec3{0, 0, 5}, mgl32.Vec3{2, 2, 0.5}, "")
	door.Dynamic = true
	ch.Brushes = []world.Brush{wall, door}
	for i := range ch.Brushes {
		_ = ch.Brushes[i].Load()
	}
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	cast := func() string {
		h, _ := Raycast(lvl, mgl32.Vec3{}, mgl32.Vec3{0, 0, 1}, RayFilter{Range: 100, Geometry: true}).First()
		return h.ID
	}
	if got := cast(); got != "door" {
		t.Fatalf("first hit %q, want the door", got)
	}
	lvl.Chunks[0].Brushes[1].Offset = mgl32.Vec3{10, 0, 0} // slid out of the way
	if got := cast(); got != "wall" {
		t.Fatalf("with the door slid aside the first hit is %q, want the wall", got)
	}
	// Reshape the wall and reload it: the tree must be rebuilt, not trusted.
	w := &lvl.Chunks[0].Brushes[0]
	for i := range w.Verts {
		w.Verts[i] = w.Verts[i].Add(mgl32.Vec3{0, 0, 20})
	}
	_ = w.Load()
	if h, _ := Raycast(lvl, mgl32.Vec3{}, mgl32.Vec3{0, 0, 1}, RayFilter{Range: 100, Geometry: true}).First(); h.ID != "wall" || h.T < 29 {
		t.Fatalf("after the wall moved back 20 the hit is %+v, want the wall at 29.5", h)
	}
	// A chunk that moves carries its tree with it.
	lvl.Chunks[0].Origin = mgl32.Vec3{50, 0, 0}
	if res := Raycast(lvl, mgl32.Vec3{}, mgl32.Vec3{0, 0, 1}, RayFilter{Range: 100, Geometry: true}); res.Blocked {
		t.Fatalf("a chunk moved fifty units aside still blocks: %+v", res.Hits)
	}
}

func TestBrushPlacementFieldsAreContiguous(t *testing.T) {
	var b world.Brush
	base := unsafe.Offsetof(b.Offset)
	if unsafe.Offsetof(b.Rotation) != base+12 || unsafe.Offsetof(b.Scale) != base+24 {
		t.Fatal("world.Brush's Offset, Rotation and Scale are no longer consecutive; placementOf reads them as one array")
	}
	b.Offset, b.Rotation, b.Scale = mgl32.Vec3{1, 2, 3}, mgl32.Vec3{4, 5, 6}, mgl32.Vec3{7, 8, 9}
	if *placementOf(&b) != [9]float32{1, 2, 3, 4, 5, 6, 7, 8, 9} {
		t.Fatalf("placementOf read %v", *placementOf(&b))
	}
}

// ---------- benchmarks ----------

var benchLevel *world.Level

func benchStress(b *testing.B) *world.Level {
	if benchLevel == nil {
		benchLevel = raycastStressLevel(b, 42, stressOpts{chunks: 25, brushesPerChunk: 400, bodies: 200, extent: 200})
	}
	return benchLevel
}

func benchRays(n int) [][2]mgl32.Vec3 {
	rng := rand.New(rand.NewSource(99))
	out := make([][2]mgl32.Vec3, n)
	for i := range out {
		o, d := randomRay(rng, 200)
		out[i] = [2]mgl32.Vec3{o, d}
	}
	return out
}

// A hitscan: everything reported, 1000 units of range.
func BenchmarkRaycastHitscan(b *testing.B) {
	lvl := benchStress(b)
	rays := benchRays(256)
	f := DefaultRayFilter(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := rays[i%len(rays)]
		Raycast(lvl, r[0], r[1], f)
	}
}

// A line-of-sight check: geometry only, 20 units.
func BenchmarkRaycastSight(b *testing.B) {
	lvl := benchStress(b)
	rays := benchRays(256)
	f := RayFilter{Range: 20, Geometry: true}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := rays[i%len(rays)]
		Raycast(lvl, r[0], r[1], f)
	}
}

// The same sight check answered as any-hit.
func BenchmarkLineBlocked(b *testing.B) {
	lvl := benchStress(b)
	rays := benchRays(256)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := rays[i%len(rays)]
		LineBlocked(lvl, r[0], r[0].Add(r[1].Normalize().Mul(20)), nil)
	}
}

var benchRealistic *world.Level

// A level shaped like a real one: the same size, one brush in a hundred
// dynamic.
func BenchmarkRaycastSightRealistic(b *testing.B) {
	if benchRealistic == nil {
		benchRealistic = raycastStressLevel(b, 43, stressOpts{chunks: 25, brushesPerChunk: 400, bodies: 200, extent: 200, dynamicEvery: 100})
	}
	rays := benchRays(256)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := rays[i%len(rays)]
		LineBlocked(benchRealistic, r[0], r[0].Add(r[1].Normalize().Mul(20)), nil)
	}
}

func BenchmarkRaycastHitscanRealistic(b *testing.B) {
	if benchRealistic == nil {
		benchRealistic = raycastStressLevel(b, 43, stressOpts{chunks: 25, brushesPerChunk: 400, bodies: 200, extent: 200, dynamicEvery: 100})
	}
	rays := benchRays(256)
	f := DefaultRayFilter(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := rays[i%len(rays)]
		Raycast(benchRealistic, r[0], r[1], f)
	}
}

func BenchmarkRaycastHitscanBruteForce(b *testing.B) {
	lvl := benchStress(b)
	rays := benchRays(256)
	f := DefaultRayFilter(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := rays[i%len(rays)]
		bfRaycast(lvl, r[0], r[1], f)
	}
}
