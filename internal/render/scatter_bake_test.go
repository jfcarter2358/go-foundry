package render

// scatter_bake_test.go — a scatter volume's height bake: what it costs, and
// that baking in parallel and patching after a terrain edit change nothing.

import (
	"fmt"
	"math"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// bakeScene is a 60x60 grass volume over a smooth block terrain, with a
// field of 400 boulder brushes in a second chunk across the same ground —
// the two kinds of ground a bake ray-casts.
func bakeScene(tb testing.TB) (*world.Level, *world.Volume) {
	tb.Helper()
	prev := gfx.GeometryUploadsEnabled
	gfx.GeometryUploadsEnabled = false
	tb.Cleanup(func() { gfx.GeometryUploadsEnabled = prev })
	tr := world.NewTerrain(mgl32.Vec3{-48, -30, -48}, 1, 96, 48, 96)
	tr.Generate(world.TerrainFill{Height: 24, Hills: 6, HillSize: 20, Seed: 5, Top: 1, Under: 2, Deep: 3, UnderDepth: 2})
	tr.Smooth = true
	tr.Touch()
	ground := world.Chunk{ID: "ground", Terrain: tr}
	ground.RecomputeBounds()

	rocks := world.Chunk{ID: "rocks"}
	for i := 0; i < 20; i++ {
		for j := 0; j < 20; j++ {
			c := mgl32.Vec3{-38 + float32(i)*4, -6, -38 + float32(j)*4}
			rocks.Brushes = append(rocks.Brushes, world.NewBoxBrush(fmt.Sprintf("rock%d_%d", i, j), c, mgl32.Vec3{0.6, 0.4 + float32((i*7+j*3)%5)*0.2, 0.6}, "stone"))
		}
	}
	for bi := range rocks.Brushes {
		if err := rocks.Brushes[bi].Load(); err != nil {
			tb.Fatalf("loading: %v", err)
		}
	}
	rocks.RecomputeBounds()

	lvl := &world.Level{Chunks: []world.Chunk{ground, rocks}}
	v := &world.Volume{Position: mgl32.Vec3{0, -4, 0}, HalfX: 30, HalfY: 6, HalfZ: 30, Scatter: &world.VolumeScatter{Enabled: true, Density: 1}}
	return lvl, v
}

// BenchmarkScatterHeightBake is one whole bake of bakeScene's volume: what
// switching the volume on, or (before incremental re-bakes) any edit to the
// terrain under it, costs the frame it happens.
func BenchmarkScatterHeightBake(b *testing.B) {
	lvl, v := bakeScene(b)
	lvl.Chunks[0].Terrain.Sections()
	fpMin, fpMax := scatterFootprint(v)
	w, h, _ := scatterHeightTexDims(fpMin, fpMax)
	data := make([]float32, w*h*2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bakeScatterHeight(lvl, v, data, w, h)
	}
}

// bakeAll is a whole bake of v's texture, serial or across CPUs.
func bakeAll(lvl *world.Level, v *world.Volume, parallel bool) (data []float32, w, h int) {
	prev := scatterParallelBake
	scatterParallelBake = parallel
	defer func() { scatterParallelBake = prev }()
	fpMin, fpMax := scatterFootprint(v)
	w, h, _ = scatterHeightTexDims(fpMin, fpMax)
	data = make([]float32, w*h*2)
	bakeScatterHeight(lvl, v, data, w, h)
	return data, w, h
}

// firstDiff is the first index where a and b differ in their bits, or -1.
func firstDiff(a, b []float32) int {
	for i := range a {
		if math.Float32bits(a[i]) != math.Float32bits(b[i]) {
			return i
		}
	}
	return -1
}

// Baking rows across CPUs gives the serial bake's bits.
func TestScatterBakeParallelMatchesSerial(t *testing.T) {
	lvl, v := bakeScene(t)
	ser, _, _ := bakeAll(lvl, v, false)
	par, _, _ := bakeAll(lvl, v, true)
	if i := firstDiff(ser, par); i >= 0 {
		t.Fatalf("texel float %d: parallel %v, serial %v", i, par[i], ser[i])
	}
	found := 0
	for i := 1; i < len(ser); i += 2 {
		if ser[i] > 0 {
			found++
		}
	}
	if found < len(ser)/4 {
		t.Fatalf("only %d of %d texels found ground; the scene is not exercising the bake", found, len(ser)/2)
	}
}

// Sculpting the terrain under a volume patches only the texels over where
// the surface changed, and the patched texture is the whole re-bake's, bit
// for bit — also on a terrain chunk turned and scaled, so the dirty box has
// to be carried through the placement.
func TestScatterBakeIncrementalMatchesFull(t *testing.T) {
	for _, placed := range []bool{false, true} {
		lvl, v := bakeScene(t)
		g := &lvl.Chunks[0]
		if placed {
			g.Rotation, g.Scale, g.Origin = 0.4, 1.3, mgl32.Vec3{3, 1, -2}
			g.RecomputeBounds()
		}
		tr := g.Terrain
		data, w, h := bakeAll(lvl, v, true)
		sig := scatterGeometry(lvl, v)
		e := scatterHeightEntry{key: scatterHeightKeyFor(v, &sig), base: sig.base, terrains: sig.terrains}
		fpMin, fpMax := scatterFootprint(v)
		_, _, size := scatterHeightTexDims(fpMin, fpMax)

		edits := []func(){
			func() { tr.SetBlock(48, groundTop(tr, 48, 48), 48, 0) },
			func() { tr.FillBox([3]int{30, 10, 60}, [3]int{36, 40, 63}, 0) },
			func() {
				// Two edits apart, between bakes.
				tr.SetBlock(20, groundTop(tr, 20, 70)+1, 70, 2)
				tr.SetBlock(75, groundTop(tr, 75, 25), 25, 0)
			},
		}
		for k, edit := range edits {
			edit()
			now := scatterGeometry(lvl, v)
			key := scatterHeightKeyFor(v, &now)
			x0, y0, x1, y1, can := scatterDirtyTexels(e, key, &now, w, h, fpMin, size)
			if !can {
				t.Fatalf("placed=%v edit %d: a terrain edit was not patchable", placed, k)
			}
			if k != 2 && (x1-x0)*(y1-y0)*4 > w*h*3 {
				t.Errorf("placed=%v edit %d: patch %dx%d of %dx%d is not much of a saving", placed, k, x1-x0, y1-y0, w, h)
			}
			if x1 > x0 && y1 > y0 {
				rect := make([]float32, (x1-x0)*(y1-y0)*2)
				bakeScatterHeightRect(lvl, v, rect, w, h, x0, y0, x1, y1)
				for y := y0; y < y1; y++ {
					copy(data[(y*w+x0)*2:(y*w+x1)*2], rect[(y-y0)*(x1-x0)*2:(y-y0+1)*(x1-x0)*2])
				}
			}
			full, _, _ := bakeAll(lvl, v, true)
			if i := firstDiff(data, full); i >= 0 {
				t.Fatalf("placed=%v edit %d: texel (%d,%d) patched %v, whole re-bake %v", placed, k, (i/2)%w, (i/2)/w, data[i], full[i])
			}
			e.key, e.base, e.terrains = key, now.base, now.terrains
		}

		// Anything but terrain sculpting re-bakes whole.
		rocks := &lvl.Chunks[1]
		rocks.Brushes = rocks.Brushes[:len(rocks.Brushes)-1]
		now := scatterGeometry(lvl, v)
		if _, _, _, _, can := scatterDirtyTexels(e, scatterHeightKeyFor(v, &now), &now, w, h, fpMin, size); can {
			t.Errorf("placed=%v: a brush removed was patched instead of re-baked", placed)
		}
		e.base = now.base
		tr.Touch()
		now = scatterGeometry(lvl, v)
		if _, _, _, _, can := scatterDirtyTexels(e, scatterHeightKeyFor(v, &now), &now, w, h, fpMin, size); can {
			t.Errorf("placed=%v: a whole-terrain Touch was patched", placed)
		}
	}
}

// groundTop is the top solid block of column (x, z).
func groundTop(tr *world.Terrain, x, z int) int {
	for y := tr.SizeY - 1; y > 0; y-- {
		if tr.Solid(x, y, z) {
			return y
		}
	}
	return 0
}

// BenchmarkScatterHeightRebake is the re-bake after one block is mined under
// the volume (not the terrain's own rebuild): the whole bake before
// incremental re-bakes, the patch after.
func BenchmarkScatterHeightRebake(b *testing.B) {
	lvl, v := bakeScene(b)
	tr := lvl.Chunks[0].Terrain
	data, w, h := bakeAll(lvl, v, true)
	sig := scatterGeometry(lvl, v)
	e := scatterHeightEntry{key: scatterHeightKeyFor(v, &sig), base: sig.base, terrains: sig.terrains}
	fpMin, fpMax := scatterFootprint(v)
	_, _, size := scatterHeightTexDims(fpMin, fpMax)
	y := groundTop(tr, 48, 48)
	m := tr.Block(48, y, 48)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// The terrain's own rebuild is BenchmarkTerrainEditRebuild's.
		b.StopTimer()
		if i%2 == 0 {
			tr.SetBlock(48, y, 48, 0)
		} else {
			tr.SetBlock(48, y, 48, m)
		}
		tr.Sections()
		b.StartTimer()
		now := scatterGeometry(lvl, v)
		key := scatterHeightKeyFor(v, &now)
		if x0, y0, x1, y1, can := scatterDirtyTexels(e, key, &now, w, h, fpMin, size); can {
			rect := make([]float32, (x1-x0)*(y1-y0)*2)
			bakeScatterHeightRect(lvl, v, rect, w, h, x0, y0, x1, y1)
		} else {
			bakeScatterHeight(lvl, v, data, w, h)
		}
		e.key, e.base, e.terrains = key, now.base, now.terrains
	}
}
