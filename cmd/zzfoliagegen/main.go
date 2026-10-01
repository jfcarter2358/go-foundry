// Temporary: the foliage bench landscape. Delete with the level.
package main

import (
	"log"
	"os"

	"foundry/internal/levelio"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func main() {
	lvl, err := levelio.NewBlankLevel()
	if err != nil {
		log.Fatal(err)
	}
	const n, h, bs = 320, 32, 3
	tr := world.NewTerrain(mgl32.Vec3{-n * bs / 2, -12, -n * bs / 2}, bs, n, h, n)
	tr.Layers = [world.TerrainLayerCount]string{"grass", "dirt", "rock"}
	tr.Biomes = []world.TerrainBiome{{Preset: "meadow"}, {Preset: "forest"}, {Preset: "pine"}}
	tr.Generate(world.TerrainFill{Height: 10, Hills: 7, HillSize: 45, Seed: 4, Top: 1, Under: 2, Deep: 3, UnderDepth: 2, Biome: 1})
	tr.Smooth = true
	for z := 0; z < n; z++ {
		for x := 0; x < n; x++ {
			top := -1
			for y := h - 1; y >= 0; y-- {
				if tr.Solid(x, y, z) {
					top = y
					break
				}
			}
			if top < 0 {
				continue
			}
			v := world.FBM3(mgl32.Vec3{float32(x) / 30, 0, float32(z) / 30}, 77, 3)
			wx := float32(x) + (v-0.5)*40
			slot := uint8(1)
			if wx < 70 {
				slot = 2
			} else if wx > 250 {
				slot = 3
			}
			tr.SetPaint(tr.FaceKey(x, top, z, world.FaceYPos), slot)
		}
	}
	ch := world.Chunk{ID: "ground", Scale: 1, Terrain: tr}
	ch.RecomputeBounds()
	lvl.Chunks = append(lvl.Chunks, ch)
	sun := world.DirectionalLight{ID: "sun", Yaw: 35, Pitch: -38, Color: mgl32.Vec3{1, 0.96, 0.88}, Brightness: 0.9, CastShadows: true, ShadowStrength: 0.6, Dynamic: true}
	sun.SyncDirection()
	lvl.Lights.Directional = append(lvl.Lights.Directional, sun)
	lvl.Lights.Global.Brightness = 0.45
	lvl.Spawn = &world.SpawnPoint{Position: mgl32.Vec3{0, 30, 0}}
	raw, err := levelio.LevelToRaw(&lvl)
	if err != nil {
		log.Fatal(err)
	}
	raw.Sky = &world.RawSky{}
	raw.ViewDistance = 2500
	out, _ := yaml.Marshal(raw)
	if err := os.WriteFile("resources/levels/zz_foliage.yaml", out, 0o644); err != nil {
		log.Fatal(err)
	}
}
