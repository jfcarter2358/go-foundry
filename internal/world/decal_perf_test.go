package world

import (
	"fmt"
	"testing"

	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// decalField is a 96x96 block terrain with 256 scorch marks spread over it in
// a 16x16 grid — a fought-over field.
func decalField(tb testing.TB) *Level {
	tb.Helper()
	prev := gfx.GeometryUploadsEnabled
	gfx.GeometryUploadsEnabled = false
	tb.Cleanup(func() { gfx.GeometryUploadsEnabled = prev })
	tr := NewTerrain(mgl32.Vec3{-48, -8, -48}, 1, 96, 16, 96)
	tr.Generate(TerrainFill{Height: 8, Top: 1, Under: 1, Deep: 1})
	ch := Chunk{ID: "ground", Terrain: tr}
	ch.RecomputeBounds()
	lvl := &Level{Chunks: []Chunk{ch}}
	for i := 0; i < 16; i++ {
		for j := 0; j < 16; j++ {
			d := NewDecal(fmt.Sprintf("scorch%d_%d", i, j), mgl32.Vec3{-45 + float32(i)*6, 0, -45 + float32(j)*6}, mgl32.Vec3{0, 1, 0})
			d.Width, d.Height, d.Depth = 2, 2, 1
			lvl.Decals = append(lvl.Decals, d)
		}
	}
	SyncDecals(lvl, 0)
	return lvl
}

// BenchmarkSyncDecalsAfterTerrainEdit mines (and restores) one block in the
// field's middle and brings every decal up to date: before dirty boxes, every
// decal in the level re-clipped.
func BenchmarkSyncDecalsAfterTerrainEdit(b *testing.B) {
	lvl := decalField(b)
	tr := lvl.Chunks[0].Terrain
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.SetBlock(48, 7, 48, uint8(i%2))
		SyncDecals(lvl, 0)
	}
}

// BenchmarkStepDecals ages a level of 256 decals none of which expire — the
// common tick.
func BenchmarkStepDecals(b *testing.B) {
	lvl := decalField(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		StepDecals(lvl, 1.0/60)
	}
}
