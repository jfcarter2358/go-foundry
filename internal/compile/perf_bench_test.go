package compile

// perf_bench_test.go — benchmarks for the compile's hot stages, on a level big
// enough to have one: a floor scattered with overlapping blocks (some turned
// off the axes, some sunk into each other), lit by a sun and a few point
// lights with two bounces. Small enough to run in seconds, big enough that
// CSG, the splitter choice, the visibility flood and the bake each do real
// work.

import (
	"math"
	"math/rand"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/world"
)

// benchField is n×n cells of scattered blocks on one floor, sealed by the
// level's seal box. Seeded, so every run builds the same level.
func benchField(n int) *world.Level {
	rng := rand.New(rand.NewSource(int64(n)))
	const cell = 12
	half := float32(n*cell) / 2
	floor := paint(boxFromTo("floor", mgl32.Vec3{-half, -1, -half}, mgl32.Vec3{half, 0, half}), "red")
	brushes := []world.Brush{floor}
	id := 0
	for x := 0; x < n; x++ {
		for z := 0; z < n; z++ {
			for k := 0; k < 3; k++ {
				cx := -half + float32(x*cell) + rng.Float32()*cell
				cz := -half + float32(z*cell) + rng.Float32()*cell
				size := mgl32.Vec3{1 + rng.Float32()*3, 1 + rng.Float32()*5, 1 + rng.Float32()*3}
				// Sunk a little into the floor, so CSG has a cut to make.
				lo := mgl32.Vec3{cx - size.X(), -0.5, cz - size.Z()}
				hi := mgl32.Vec3{cx + size.X(), size.Y(), cz + size.Z()}
				id++
				b := boxFromTo("b"+string(rune('a'+id%26))+itoa(id), lo, hi)
				if k == 2 {
					// Turned off the axes about its own centre.
					turnBrushY(&b, mgl32.Vec3{cx, 0, cz}, rng.Float32()*math.Pi)
				}
				tex := "white"
				if k == 1 {
					tex = "red"
				}
				brushes = append(brushes, paint(b, tex))
			}
		}
	}
	lvl := &world.Level{Chunks: []world.Chunk{{ID: "field", Brushes: brushes}}}
	sun := world.DirectionalLight{Pitch: -55, Yaw: 30, Color: mgl32.Vec3{1, 0.95, 0.9}, Brightness: 1, CastShadows: true, ShadowStrength: 1}
	sun.SyncDirection()
	lvl.Lights = world.LightSet{
		Global:      world.GlobalLight{Color: mgl32.Vec3{0.6, 0.7, 1}, Brightness: 0.2, Bounces: 2, BounceScale: 1},
		Directional: []world.DirectionalLight{sun},
	}
	for i := 0; i < 4; i++ {
		lvl.Lights.Points = append(lvl.Lights.Points, world.PointLight{
			Position: mgl32.Vec3{-half/2 + float32(i%2)*half, 3, -half/2 + float32(i/2)*half},
			Color:    mgl32.Vec3{1, 0.8, 0.5}, Brightness: 2, Radius: float32(cell) * 2, CastShadows: true, ShadowStrength: 1,
		})
	}
	lvl.Seal = &world.Seal{Position: mgl32.Vec3{0, 10, 0}, Half: mgl32.Vec3{half + 4, 14, half + 4}}
	return lvl
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

// turnBrushY turns a brush's verts about the vertical through c.
func turnBrushY(b *world.Brush, c mgl32.Vec3, angle float32) {
	m := mgl32.HomogRotate3DY(angle)
	for i, v := range b.Verts {
		r := m.Mul4x1(v.Sub(c).Vec4(1))
		b.Verts[i] = mgl32.Vec3{r.X(), r.Y(), r.Z()}.Add(c)
	}
}

var benchAlbedos = map[string]mgl32.Vec3{"red": {0.8, 0.2, 0.2}, "white": {0.8, 0.8, 0.8}}

func withBenchAlbedos(b testing.TB) {
	old := textureAlbedo
	textureAlbedo = func(name string) (mgl32.Vec3, bool) {
		c, ok := benchAlbedos[name]
		return c, ok
	}
	b.Cleanup(func() { textureAlbedo = old })
}

const benchFieldSize = 8

func BenchmarkCSG(b *testing.B) {
	lvl := benchField(benchFieldSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := csg(lvl); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildBSP(b *testing.B) {
	faces, _, err := csg(benchField(benchFieldSize))
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BuildBSP(append([]Face(nil), faces...))
	}
}

// BenchmarkMightSee is the visibility's coarse candidate pass alone (the
// exact flood behind it is budget-bound and far too slow to benchmark on an
// open field).
func BenchmarkMightSee(b *testing.B) {
	faces, _, err := csg(benchField(benchFieldSize))
	if err != nil {
		b.Fatal(err)
	}
	tr := BuildBSP(faces)
	portals := BuildPortals(tr)
	byLeaf := make([][]int, len(tr.Leaves))
	for i := range portals {
		byLeaf[portals[i].A] = append(byLeaf[portals[i].A], i)
	}
	b.Logf("%d portals", len(portals))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := &visState{portals: portals, byLeaf: byLeaf, portalBytes: (len(portals) + 7) / 8}
		s.buildMightSee()
	}
}

func BenchmarkBakeLight(b *testing.B) {
	withBenchAlbedos(b)
	lvl := benchField(benchFieldSize / 2)
	faces, _, err := csg(lvl)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		tr := BuildBSP(append([]Face(nil), faces...))
		b.StartTimer()
		BakeLight(tr, lvl, DefaultLightOptions())
	}
}

// BenchmarkFaceBVHNearest is the bounce's inner loop alone: nearest-hit rays
// fanned out from every vertex of a baked field.
func BenchmarkFaceBVHNearest(b *testing.B) {
	withBenchAlbedos(b)
	lvl := benchField(benchFieldSize / 2)
	faces, _, err := csg(lvl)
	if err != nil {
		b.Fatal(err)
	}
	tr := BuildBSP(faces)
	tr.SubdivideFaces(DefaultLightOptions().GridStep)
	bv := buildFaceBVH(tr, true)
	fan := cosineFan(16)
	b.ResetTimer()
	hits := 0
	for i := 0; i < b.N; i++ {
		for fi := range tr.Faces {
			f := &tr.Faces[fi]
			o := f.Points[0].Add(f.Plane.N.Mul(0.05))
			t, bit := basisAbout(f.Plane.N)
			for _, l := range fan {
				dir := t.Mul(l.X()).Add(bit.Mul(l.Y())).Add(f.Plane.N.Mul(l.Z()))
				if _, _, ok := bv.nearest(o, dir, fi); ok {
					hits++
				}
			}
		}
	}
	_ = hits
}
