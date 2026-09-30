package editor

import (
	"os"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func biomeToolProject(t *testing.T) {
	t.Helper()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = t.TempDir()
	os.MkdirAll(fileutil.TexturesDir(), 0o755)
	prevLevel := Editor.level
	t.Cleanup(func() {
		fileutil.ResourcesPath = prev
		Editor.level = prevLevel
		biomePoll.stamps, biomePoll.errors = nil, nil
		world.LoadBiomes(t.TempDir())
	})
}

// biome_save refuses a file that does not validate and writes nothing; a
// good one is written, becomes paintable at once, and regrows the terrain
// already painted with it.
func TestBiomeSaveValidatesThenWritesAndRegrows(t *testing.T) {
	biomeToolProject(t)
	tr := world.NewTerrain(mgl32.Vec3{}, 1, 8, 4, 8)
	tr.Biomes = []world.TerrainBiome{{Preset: "moon"}}
	tr.Generate(world.TerrainFill{Height: 2, Top: 1, Under: 1, Deep: 1, Biome: 1})
	tr.Sections()
	Editor.level = &world.Level{Chunks: []world.Chunk{{ID: "ground", Terrain: tr}}}

	text, isErr := claudeBiomeSave("moon", "ground: {tint: [1, 1, 1], cover: 1}\nspecies:\n  - {shape: moon_rock, spacing: 1, scale: [1, 1], colors: [[1,1,1],[1,1,1]]}\n")
	if !isErr || !strings.Contains(text, "moon_rock") {
		t.Fatalf("a bad biome was not refused by its problem: %q", text)
	}
	if _, err := os.Stat(world.BiomePath("moon")); err == nil {
		t.Fatal("a biome that did not validate was written")
	}

	gen := tr.Generation
	good := "label: Moon\nground: {texture: ground_moon, tint: [0.6, 0.6, 0.6], cover: 0.3}\nspecies:\n" +
		"  - {shape: rock, spacing: 2, scale: [0.5, 1], colors: [[0.5,0.5,0.5],[0.6,0.6,0.6]], sink: 0.3}\n"
	text, isErr = claudeBiomeSave("moon", good)
	if isErr {
		t.Fatalf("a good biome was refused: %s", text)
	}
	if data, err := os.ReadFile(world.BiomePath("moon")); err != nil || string(data) != good {
		t.Fatalf("the file on disk is not what was saved: %v", err)
	}
	if p := world.FoliagePresetByName("moon"); p == nil || p.Label != "Moon" {
		t.Fatal("the saved biome cannot be painted")
	}
	if tr.Generation == gen {
		t.Error("the terrain painted with the biome was not rebuilt")
	}
	if !strings.Contains(text, "1 terrain") || !strings.Contains(text, "WARNING") {
		t.Errorf("the answer does not say what regrew and that the texture is missing: %s", text)
	}

	got, isErr := claudeBiomeGet("moon")
	if isErr || !strings.Contains(got, "label: Moon") {
		t.Errorf("biome_get did not return the file: %s", got)
	}
	got, isErr = claudeBiomeGet("savannah")
	if isErr || !strings.Contains(got, "built in") || !strings.Contains(got, "acacia") {
		t.Errorf("biome_get did not write out a built-in: %s", got)
	}
	if _, isErr = claudeBiomeSave("Bad Name", good); !isErr {
		t.Error("a biome name with spaces was accepted")
	}
}
