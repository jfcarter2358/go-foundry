package levelio

import (
	"math"
	"os"
	"strings"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestZZBiomePreview(t *testing.T) {
	if os.Getenv("ZZ_BIOMES") == "" {
		t.Skip()
	}
	for _, name := range strings.Fields(os.Getenv("ZZ_BIOMES")) {
		tr := world.NewTerrain(mgl32.Vec3{-48, -8, -48}, 1, 96, 32, 96)
		tr.Biomes = []world.TerrainBiome{{Preset: name}}
		tr.Layers[0], tr.Layers[1], tr.Layers[2] = "dirt", "dirt", "concrete"
		tr.Generate(world.TerrainFill{Height: 8, Hills: 2, HillSize: 14, Seed: 7, Top: 1, Under: 2, Deep: 3, UnderDepth: 2, Biome: 1})
		// A plateau to the south-west with sheer sides.
		for z := 0; z < 96; z++ {
			for x := 0; x < 96; x++ {
				if math.Hypot(float64(x-26), float64(z-26)) < 16 {
					for y := 0; y < 22; y++ {
						tr.SetBlock(x, y, z, 1)
					}
					for y := 0; y < 32; y++ {
						for d := 0; d < 6; d++ {
							tr.SetPaint(tr.FaceKey(x, y, z, d), 1)
						}
					}
				}
			}
		}
		tr.Smooth = true
		tr.Touch()
		ch := world.Chunk{ID: "ground", Scale: 1, Terrain: tr}
		ch.RecomputeBounds()
		lvl := world.Level{Chunks: []world.Chunk{ch}, Spawn: &world.SpawnPoint{Position: mgl32.Vec3{12, 12, 12}}}
		if err := SaveLevel(&lvl, "../../resources/levels/zz_b_"+name+".yaml"); err != nil {
			t.Fatal(err)
		}
	}
}
