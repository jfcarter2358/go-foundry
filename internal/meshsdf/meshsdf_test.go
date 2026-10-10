package meshsdf

import (
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/model"

	"github.com/go-gl/mathgl/mgl32"
)

// sphereTris is a UV sphere of radius r about c: rings×segs quads, two
// triangles each.
func sphereTris(c mgl32.Vec3, r float32, rings, segs int) []mgl32.Vec3 {
	at := func(i, j int) mgl32.Vec3 {
		th := math.Pi * float64(i) / float64(rings)
		ph := 2 * math.Pi * float64(j) / float64(segs)
		return c.Add(mgl32.Vec3{
			float32(math.Sin(th) * math.Cos(ph)), float32(math.Cos(th)), float32(math.Sin(th) * math.Sin(ph)),
		}.Mul(r))
	}
	var tris []mgl32.Vec3
	for i := 0; i < rings; i++ {
		for j := 0; j < segs; j++ {
			a, b, cc, d := at(i, j), at(i+1, j), at(i+1, j+1), at(i, j+1)
			tris = append(tris, a, b, cc, a, cc, d)
		}
	}
	return tris
}

// soupTris is n random triangles in the unit cube, of every size from a
// sliver to most of the cube: a mesh with no structure at all.
func soupTris(n int, seed int64) []mgl32.Vec3 {
	r := rand.New(rand.NewSource(seed))
	tris := make([]mgl32.Vec3, 0, 3*n)
	for i := 0; i < n; i++ {
		c := mgl32.Vec3{r.Float32(), r.Float32(), r.Float32()}
		s := float32(math.Pow(10, -2.5+2.5*r.Float64()))
		for k := 0; k < 3; k++ {
			tris = append(tris, c.Add(mgl32.Vec3{r.Float32() - 0.5, r.Float32() - 0.5, r.Float32() - 0.5}.Mul(s)))
		}
	}
	return tris
}

// modelTris is a .fmdl's faces, fanned into triangles.
func modelTris(t testing.TB, path string) []mgl32.Vec3 {
	t.Helper()
	doc, err := model.LoadModel(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var tris []mgl32.Vec3
	for _, f := range doc.Faces {
		for k := 2; k < len(f.Verts); k++ {
			tris = append(tris, doc.Verts[f.Verts[0]], doc.Verts[f.Verts[k-1]], doc.Verts[f.Verts[k]])
		}
	}
	return tris
}

// shippedModels is every .fmdl the tests can find: the repository's own, and
// the game project's when FOUNDRY_GAME_MODELS names its models directory.
func shippedModels(t testing.TB) []string {
	t.Helper()
	// A package's tests run in its own directory: the repository's models
	// are two up.
	dirs := []string{filepath.Join("..", "..", "resources", "models")}
	if d := os.Getenv("FOUNDRY_GAME_MODELS"); d != "" {
		dirs = append(dirs, d)
	}
	var out []string
	for _, d := range dirs {
		m, _ := filepath.Glob(filepath.Join(d, "*.fmdl"))
		out = append(out, m...)
	}
	return out
}

func sameGrid(t *testing.T, name string, got, want *Grid) {
	t.Helper()
	if (got == nil) != (want == nil) {
		t.Fatalf("%s: got nil %v, want nil %v", name, got == nil, want == nil)
	}
	if got == nil {
		return
	}
	if got.N != want.N || got.Lo != want.Lo || got.Hi != want.Hi || got.Cell != want.Cell {
		t.Fatalf("%s: frame differs: %+v vs %+v", name, *got, *want)
	}
	for i := range want.Data {
		if math.Float32bits(got.Data[i]) != math.Float32bits(want.Data[i]) {
			t.Fatalf("%s: voxel %d is %v, want %v (bit for bit)", name, i, got.Data[i], want.Data[i])
		}
	}
}

// The bake is split across the cores by z-slab and skips the voxels a
// triangle cannot reach within the band; none of that may move a single bit
// of the answer. Checked against the bake as it was before either (bakeV1,
// below), on both the parallel path and the forced serial one.
func TestBakeMatchesTheOriginalBitForBit(t *testing.T) {
	cases := map[string][]mgl32.Vec3{
		"sphere":      sphereTris(mgl32.Vec3{1, 2, 3}, 2, 48, 96),
		"soup":        soupTris(6000, 1),
		"one tri":     {{0, 0, 0}, {1, 0, 0}, {0, 1, 0}},
		"sliver soup": soupTris(3000, 7),
	}
	for _, p := range shippedModels(t) {
		cases[filepath.Base(p)] = modelTris(t, p)
	}
	for name, tris := range cases {
		for _, n := range []int{24, 32, 64} {
			want := bakeV1(tris, n)
			for _, par := range []bool{true, false} {
				parallelBake = par
				sameGrid(t, name, Bake(tris, n), want)
			}
			parallelBake = true
		}
	}
}

func BenchmarkBake(b *testing.B) {
	cases := []struct {
		name string
		tris []mgl32.Vec3
	}{
		{"sphere20k", sphereTris(mgl32.Vec3{}, 1, 100, 100)},
		{"soup20k", soupTris(20000, 3)},
	}
	for _, p := range shippedModels(b) {
		cases = append(cases, struct {
			name string
			tris []mgl32.Vec3
		}{filepath.Base(p), modelTris(b, p)})
	}
	for _, c := range cases {
		// Side by side with the original, so a busy machine slows both.
		b.Run(c.name+"/v1", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				bakeV1(c.tris, 32)
			}
		})
		b.Run(c.name+"/new", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				Bake(c.tris, 32)
			}
		})
	}
}
