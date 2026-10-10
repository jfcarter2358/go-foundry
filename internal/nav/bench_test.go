package nav

import (
	"fmt"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// bigLevel is a 200×200 ground slab studded with 400 pillars — larger than any
// level the engine ships, as a ceiling on what a first path request costs.
func bigLevel() *world.Level {
	ch := world.Chunk{ID: "big"}
	ch.Brushes = append(ch.Brushes, world.NewBoxBrush("ground", mgl32.Vec3{100, -0.5, 100}, mgl32.Vec3{100, 0.5, 100}, "c"))
	for i := 0; i < 20; i++ {
		for j := 0; j < 20; j++ {
			ch.Brushes = append(ch.Brushes, world.NewBoxBrush(fmt.Sprintf("p%d_%d", i, j),
				mgl32.Vec3{5 + float32(i)*10, 2, 5 + float32(j)*10}, mgl32.Vec3{1, 2, 1}, "c"))
		}
	}
	return &world.Level{Chunks: []world.Chunk{ch}}
}

func BenchmarkBuildLargeLevel(b *testing.B) {
	lvl := bigLevel()
	for i := 0; i < b.N; i++ {
		Invalidate()
		For(lvl, playerParams)
	}
}

func BenchmarkPathAcrossLargeLevel(b *testing.B) {
	lvl := bigLevel()
	Invalidate()
	g := For(lvl, playerParams)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.FindPath(mgl32.Vec3{1, 0, 1}, mgl32.Vec3{199, 0, 198})
	}
}

func BenchmarkSignature(b *testing.B) {
	lvl := bigLevel()
	for i := 0; i < b.N; i++ {
		Signature(lvl)
	}
}

// BenchmarkBuildHeightfield rasterises bigLevel's heightfield from nothing:
// every tile, no cache — a level's first build.
func BenchmarkBuildHeightfield(b *testing.B) {
	solids := gatherSolids(bigLevel())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resetTiles()
		buildHeightfield(solids, DefaultCellSize, MaxSlopeCos)
	}
}
