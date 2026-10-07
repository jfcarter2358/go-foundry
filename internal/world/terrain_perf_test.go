package world

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// benchTerrain is a 96x48x96 meadow of rolling hills — six sections along X
// and Z, three up — built once, so a benchmark times only what an edit
// costs.
func benchTerrain(smooth bool) *Terrain {
	t := NewTerrain(mgl32.Vec3{}, 1, 96, 48, 96)
	t.Generate(TerrainFill{Height: 24, Hills: 6, HillSize: 20, Seed: 5, Top: 1, Under: 2, Deep: 3, UnderDepth: 2})
	t.Smooth = smooth
	t.Touch()
	t.Sections()
	return t
}

// groundY is the top solid block of column (x, z), or -1.
func groundY(t *Terrain, x, z int) int {
	for y := t.SizeY - 1; y >= 0; y-- {
		if t.Solid(x, y, z) {
			return y
		}
	}
	return -1
}

// BenchmarkTerrainEditRebuild mines (then restores) the ground block at a
// section corner and rebuilds the surface: what one swing of a pick costs
// the frame it lands.
func BenchmarkTerrainEditRebuild(b *testing.B) {
	for _, smooth := range []bool{false, true} {
		name := "blocks"
		if smooth {
			name = "smooth"
		}
		b.Run(name, func(b *testing.B) {
			t := benchTerrain(smooth)
			x, z := 2*TerrainSectionSize, 2*TerrainSectionSize
			y := groundY(t, x, z)
			m := t.Block(x, y, z)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if i%2 == 0 {
					t.SetBlock(x, y, z, 0)
				} else {
					t.SetBlock(x, y, z, m)
				}
				t.Sections()
			}
		})
	}
}

// sectionsUnder rebuilds t's surface with the union build on or off and the
// given worker cap, restoring both switches after.
func sectionsUnder(t *Terrain, union bool, workers int) []*TerrainSection {
	oldU, oldW := terrainUnionBuild, terrainBuildWorkers
	terrainUnionBuild, terrainBuildWorkers = union, workers
	defer func() { terrainUnionBuild, terrainBuildWorkers = oldU, oldW }()
	return t.Sections()
}

// sameSections fails unless every section of got matches want field for
// field — faces, box, face index and ray bins; only the generations differ.
func sameSections(tb testing.TB, what string, got, want []*TerrainSection) {
	tb.Helper()
	if len(got) != len(want) {
		tb.Fatalf("%s: %d sections, want %d", what, len(got), len(want))
	}
	for i := range got {
		g, w := *got[i], *want[i]
		g.Gen, g.FoliageGen, w.Gen, w.FoliageGen = 0, 0, 0, 0
		if len(g.Faces) == 0 && len(w.Faces) == 0 {
			g.Faces, w.Faces = nil, nil
		}
		if !reflect.DeepEqual(g, w) {
			tb.Fatalf("%s: section (%d,%d,%d) differs (%d faces vs %d)", what, g.X, g.Y, g.Z, len(g.Faces), len(w.Faces))
		}
	}
}

// The union build (one field and one set of dual cells per box of dirty
// sections, its stages split across workers) must give exactly the faces
// the one-section-at-a-time build gives: after a whole build, and after
// every step of random sequences of mining, placing, filling and painting,
// at the default smoothing and at the widest (whose reach dirties four
// sections a side).
func TestTerrainUnionBuildMatchesPerSection(t *testing.T) {
	for _, smoothing := range []float32{0, TerrainSmoothingMax} {
		ref := benchTerrain(true)
		ref.Smoothing = smoothing
		ref.Biomes = []TerrainBiome{{Preset: "forest", GroundLayer: 4}, {Preset: "meadow"}}
		ref.Touch()
		uni, ser := ref.Clone(), ref.Clone()
		sameSections(t, "full build", sectionsUnder(uni, true, 8), sectionsUnder(ref, false, 8))
		sameSections(t, "full build, serial", sectionsUnder(ser, true, 1), sectionsUnder(ref, false, 8))
		r := rand.New(rand.NewSource(int64(7 + smoothing)))
		for step := 0; step < 14; step++ {
			x, z := r.Intn(ref.SizeX), r.Intn(ref.SizeZ)
			y := max(groundY(ref, x, z), 0)
			for _, tr := range []*Terrain{ref, uni, ser} {
				switch step % 4 {
				case 0:
					tr.SetBlock(x, y, z, 0)
				case 1:
					tr.SetBlock(x, y+1, z, 2)
				case 2:
					tr.FillBox([3]int{x - 2, y - 3, z - 1}, [3]int{x + 1, y, z + 4}, 0)
				case 3:
					tr.SetPaint(tr.FaceKey(x, y, z, FaceYPos), uint8(1+step%2))
				}
			}
			// Two edits between rebuilds now and then, so a rebuild's dirty
			// set is not always one box.
			if step%3 == 1 {
				continue
			}
			want := sectionsUnder(ref, false, 8)
			sameSections(t, "edit", sectionsUnder(uni, true, 8), want)
			sameSections(t, "edit, serial", sectionsUnder(ser, true, 1), want)
		}
	}
}

// BenchmarkTerrainFullBuild builds a whole smooth surface from scratch — a
// level load, or a Touch.
func BenchmarkTerrainFullBuild(b *testing.B) {
	t := benchTerrain(true)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t.Touch()
		t.Sections()
	}
}
