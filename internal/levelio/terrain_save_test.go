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
	tr := world.NewTerrain(mgl32.Vec3{-8, 0, -8}, 2, 8, 8)
	for i := range tr.Heights {
		tr.Heights[i] = float32(i%5) * 0.3
	}
	tr.SetHole(1, 1, true)
	tr.Layers = [world.TerrainLayerCount]string{"grass", "", "rock"}
	tr.TextureScale = 3
	tr.PaintWeight(4, 4, 2, 1)
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
	for _, key := range []string{"terrain:", "cell_size: 2", "cells_x: 8", "heights:", "holes:", "weights:", "- rock", "texture_scale: 3"} {
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
	for i := range tr.Heights {
		if d := g.Heights[i] - tr.Heights[i]; d > 1e-3 || d < -1e-3 {
			t.Fatalf("height %d came back %v, was %v", i, g.Heights[i], tr.Heights[i])
		}
	}
	if !g.IsHole(1, 1) || g.Layers != tr.Layers ||
		g.Weight(4, 4, 2) != 255 || g.TextureScale != 3 || g.CellSize != 2 {
		t.Fatalf("the terrain changed in the round trip: %+v", g)
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

func TestATerrainWhoseArraysDoNotFitIsNotSaved(t *testing.T) {
	tr := world.NewTerrain(mgl32.Vec3{}, 1, 4, 4)
	tr.Heights[3] = 1
	src := world.Level{Chunks: []world.Chunk{{ID: "g", Scale: 1, Terrain: tr}}}
	raw, err := LevelToRaw(&src)
	if err != nil {
		t.Fatal(err)
	}
	raw.Chunks[0].Terrain.CellsX = 5
	if err := ValidateRawLevel(&raw); err == nil {
		t.Fatal("a level whose terrain heights do not fit its grid validated")
	}
}
