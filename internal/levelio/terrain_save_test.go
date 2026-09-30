package levelio

import (
	"strings"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// A chunk made of nothing but terrain goes through the level file and comes
// back the same ground, found by collision's chunk lookup like any other.
func TestATerrainChunkSurvivesALevelRoundTrip(t *testing.T) {
	withStubTextures(t)
	for _, n := range []string{"grass", "rock"} {
		gfx.Textures[n], gfx.TextureNames[n] = 1, n
	}
	tr := world.NewTerrain(mgl32.Vec3{-8, -6, -8}, 2, 8, 6, 8)
	tr.Biomes = []world.TerrainBiome{{Preset: "meadow"}, {Preset: "cliff", Density: 1.5}}
	tr.Generate(world.TerrainFill{Height: 3, Hills: 2, HillSize: 4, Top: 1, Under: 3, Deep: 3, UnderDepth: 1, Biome: 1})
	tr.SetBlock(2, 2, 2, 0)
	tr.SetPaint(tr.FaceKey(0, 0, 0, world.FaceXNeg), 2)
	tr.Layers = [world.TerrainLayerCount]string{"grass", "", "rock"}
	tr.TextureScale = 3
	tr.Smooth = true
	ch := world.Chunk{ID: "ground", Scale: 1, Origin: mgl32.Vec3{10, 0, 0}, Terrain: tr}
	ch.RecomputeBounds()
	src := world.Level{Chunks: []world.Chunk{ch}}

	raw, err := LevelToRaw(&src)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"terrain:", "block_size: 2", "size_y: 6", "blocks:", "paint:", "smooth: true", "preset: cliff", "- rock", "texture_scale: 3"} {
		if !strings.Contains(string(out), key) {
			t.Errorf("saved level is missing %q:\n%s", key, out)
		}
	}
	var again world.RawLevel
	if err := yaml.Unmarshal(out, &again); err != nil {
		t.Fatal(err)
	}
	lvl, err := rawToLevel(again)
	if err != nil {
		t.Fatalf("rawToLevel: %v", err)
	}
	got := world.FindChunkByID(&lvl, "ground")
	if got == nil || got.Terrain == nil {
		t.Fatal("the terrain chunk came back without its terrain")
	}
	g := got.Terrain
	if string(g.Blocks) != string(tr.Blocks) || g.Layers != tr.Layers || !g.Smooth ||
		g.TextureScale != 3 || g.BlockSize != 2 || len(g.Biomes) != 2 || len(g.Paint) != len(tr.Paint) {
		t.Fatalf("the terrain changed in the round trip")
	}
	for k, v := range tr.Paint {
		if g.Paint[k] != v {
			t.Fatalf("face %d came back painted %d, was %d", k, g.Paint[k], v)
		}
	}
	// Its bounds came from the terrain alone, so the spatial index finds it.
	lo, hi := got.WorldBounds()
	if lo.X() > 2.01 || hi.X() < 17.99 {
		t.Fatalf("a brushless terrain chunk has bounds %v..%v", lo, hi)
	}
	if found := world.ChunksOverlapping(&lvl, mgl32.Vec3{9, -1, -1}, mgl32.Vec3{11, 1, 1}, nil); len(found) != 1 {
		t.Fatalf("the spatial index found %d chunks over the terrain", len(found))
	}
}

func TestATerrainWhoseBlocksDoNotFitIsNotSaved(t *testing.T) {
	tr := world.NewTerrain(mgl32.Vec3{}, 1, 4, 4, 4)
	tr.SetBlock(1, 1, 1, 1)
	src := world.Level{Chunks: []world.Chunk{{ID: "g", Scale: 1, Terrain: tr}}}
	raw, err := LevelToRaw(&src)
	if err != nil {
		t.Fatal(err)
	}
	raw.Chunks[0].Terrain.SizeX = 5
	if err := ValidateRawLevel(&raw); err == nil {
		t.Fatal("a level whose terrain blocks do not fit its grid validated")
	}
}
