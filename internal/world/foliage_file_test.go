package world

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/fileutil"

	"github.com/go-gl/mathgl/mgl32"
)

// restoreBiomes puts the preset table back to the built-ins after a test that
// loads files.
func restoreBiomes(t *testing.T) {
	t.Cleanup(func() { LoadBiomes(t.TempDir()) })
}

// Every built-in written as a file reads back as itself: the file format
// carries everything a preset is.
func TestBuiltinBiomesRoundTripThroughTheirFiles(t *testing.T) {
	for i := range builtinFoliagePresets {
		p := &builtinFoliagePresets[i]
		data, err := MarshalBiome(p)
		if err != nil {
			t.Fatalf("%s: %v", p.Name, err)
		}
		back, err := ParseBiome(p.Name, data)
		if err != nil {
			t.Fatalf("%s does not read back:\n%v\n%s", p.Name, err, data)
		}
		if !reflect.DeepEqual(*back, *p) {
			t.Errorf("%s changed in the round trip:\nwant %+v\ngot  %+v", p.Name, *p, *back)
		}
	}
}

// A file with mistakes is refused, and every mistake is named — not just the
// first, and not silently defaulted.
func TestABadBiomeFileNamesEveryProblem(t *testing.T) {
	src := `
label: Broken
ground:
  tint: [0.5, 0.5]
  cover: 2
species:
  - shape: palm_tree
    spacing: 3
    scale: [1, 1]
    colors: [[0.1, 0.2, 0.3], [0.1, 0.2, 0.3]]
  - shape: cactus
    variants: [5]
    spacing: 0.01
    scale: [2, 1]
    colors: [[0.1, 0.2, 0.3], [0.1, 0.2, 0.3]]
    where: [floor]
`
	_, err := ParseBiome("broken", []byte(src))
	if err == nil {
		t.Fatal("a broken biome parsed")
	}
	for _, want := range []string{"ground.tint", "ground.cover", `"palm_tree"`, "variants", "spacing", "scale", `"floor"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %s:\n%v", want, err)
		}
	}
	if _, err := ParseBiome("typo", []byte("ground: {tint: [1,1,1], cover: 1}\nspecies: []\nspacng: 3\n")); err == nil ||
		!strings.Contains(err.Error(), "spacng") {
		t.Errorf("an unknown key was not refused by name: %v", err)
	}
}

// A project's files lay over the built-ins by name and add new biomes; a bad
// file leaves what it would have replaced; deleting a file puts the built-in
// back.
func TestBiomeFilesOverrideAndExtendTheBuiltins(t *testing.T) {
	restoreBiomes(t)
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name+BiomeExt), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("meadow", "label: Alpine Meadow\nground: {texture: alp, tint: [0.3, 0.5, 0.2], cover: 0.5}\nspecies:\n  - {shape: flower, spacing: 0.5, scale: [1, 1], colors: [[0.2,0.4,0.1],[0.3,0.5,0.1]]}\n")
	write("moon", "ground: {tint: [0.6, 0.6, 0.6], cover: 0}\nspecies:\n  - {shape: rock, spacing: 2, scale: [0.5, 1], colors: [[0.5,0.5,0.5],[0.6,0.6,0.6]]}\n")
	write("scree", "this: is not a biome\n")
	gen := FoliagePresetGeneration()
	n, errs := LoadBiomes(dir)
	if n != 2 || len(errs) != 1 {
		t.Fatalf("loaded %d with %d errors, want 2 and 1: %v", n, len(errs), errs)
	}
	if FoliagePresetGeneration() == gen {
		t.Error("loading did not move the generation")
	}
	if p := FoliagePresetByName("meadow"); p == nil || p.Label != "Alpine Meadow" || len(p.Species) != 1 || p.GroundTexture != "alp" {
		t.Errorf("the meadow file did not override the built-in: %+v", p)
	}
	if p := FoliagePresetByName("moon"); p == nil || p.Label != "moon" {
		t.Errorf("a new biome file did not add a biome: %+v", p)
	}
	if p := FoliagePresetByName("scree"); p == nil || !reflect.DeepEqual(*p, *BuiltinFoliagePreset("scree")) {
		t.Error("a file that does not parse replaced its built-in")
	}
	if BiomeSource("moon") != filepath.Join(dir, "moon"+BiomeExt) || BiomeSource("forest") != "built-in" {
		t.Errorf("sources: moon %q, forest %q", BiomeSource("moon"), BiomeSource("forest"))
	}

	os.Remove(filepath.Join(dir, "meadow"+BiomeExt))
	os.Remove(filepath.Join(dir, "moon"+BiomeExt))
	if err := ReloadBiome(dir, "meadow"); err != nil {
		t.Fatal(err)
	}
	if err := ReloadBiome(dir, "moon"); err != nil {
		t.Fatal(err)
	}
	if p := FoliagePresetByName("meadow"); p == nil || p.Label != "Meadow" {
		t.Error("deleting the file did not put the built-in meadow back")
	}
	if FoliagePresetByName("moon") != nil {
		t.Error("deleting a new biome's file left the biome")
	}
}

// The biome files the repository ships parse, and every ground texture they
// name exists.
func TestShippedBiomeFilesLoad(t *testing.T) {
	restoreBiomes(t)
	old := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join("..", "..", "resources")
	t.Cleanup(func() { fileutil.ResourcesPath = old })
	n, errs := LoadBiomes(BiomesDir())
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if n < len(builtinFoliagePresets) {
		t.Errorf("only %d biome files ship for %d built-ins", n, len(builtinFoliagePresets))
	}
	for _, p := range FoliagePresetsSorted() {
		if msg := BiomeTextureProblem(p); msg != "" {
			t.Errorf("%s: %s", p.Name, msg)
		}
	}
}

// A painted face carries its biome's ground channel at full weight, bare
// ground none; in smooth view the weight falls off across the edge of the
// paint rather than stopping on it. A slot with its own GroundLayer, and a
// texture already given a channel, take none of their own.
func TestPaintedFacesCarryTheirBiomesGroundChannel(t *testing.T) {
	eachMode(t, func(t *testing.T, smooth bool) {
		tr := NewTerrain(mgl32.Vec3{}, 1, 16, 4, 16)
		tr.Biomes = []TerrainBiome{{Preset: "desert"}, {Preset: "beach"}, {Preset: "jungle", GroundLayer: 2}, {Preset: "desert", Density: 2}}
		tr.Generate(TerrainFill{Height: 2, Top: 1, Under: 1, Deep: 1})
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				slot := uint8(1)
				if x >= 8 {
					slot = 2
				}
				if z >= 12 {
					slot = 0
				}
				tr.SetPaint(tr.FaceKey(x, 1, z, FaceYPos), slot)
			}
		}
		tr.Smooth = smooth
		tr.Touch()
		if got := tr.GroundTextures(); !reflect.DeepEqual(got, []string{"ground_desert", "ground_beach"}) {
			t.Fatalf("ground channels %v", got)
		}
		ch := func(g uint64, c int) int { return int((g >> (8 * uint(c))) & 0xff) }
		var sawFade bool
		for _, s := range tr.Sections() {
			for _, f := range s.Faces {
				bx, _, bz, d := tr.FaceBlock(f.Key)
				if d != FaceYPos {
					continue
				}
				for c := 0; c < 4; c++ {
					g := f.Ground[c]
					switch {
					case bz >= 1 && bz <= 10 && bx >= 1 && bx <= 6:
						if ch(g, 0) != 255 || ch(g, 1) != 0 {
							t.Fatalf("a face deep in the desert paint has ground %016x", g)
						}
					case bz >= 1 && bz <= 10 && bx >= 9 && bx <= 14:
						if ch(g, 1) != 255 || ch(g, 0) != 0 {
							t.Fatalf("a face deep in the beach paint has ground %016x", g)
						}
					case bz >= 13:
						if smooth && ch(g, 0)+ch(g, 1) != 0 && bz >= 14 {
							t.Fatalf("bare ground well away from the paint has ground %016x", g)
						}
						if !smooth && g != 0 {
							t.Fatalf("a bare block face has ground %016x", g)
						}
					}
					if v := ch(g, 0); v > 0 && v < 255 {
						sawFade = true
					}
				}
			}
		}
		if smooth && !sawFade {
			t.Error("the smooth surface's ground stops dead at the paint's edge")
		}
	})
}

// A terrain's per-shape amounts change what grows on it: 0 takes a kind of
// thing away entirely, a higher amount grows more of it, and the other kinds
// are left as they were.
func TestFoliageAmountsScaleEachKindAlone(t *testing.T) {
	count := func(amounts map[string]float32) map[FoliageShape]int {
		tr := NewTerrain(mgl32.Vec3{}, 1, 32, 8, 32)
		tr.Biomes = []TerrainBiome{{Preset: "meadow"}}
		for k, v := range amounts {
			s, _ := FoliageShapeByKey(k)
			tr.Biomes[0].SetAmount(s, v)
		}
		tr.Generate(TerrainFill{Height: 3, Top: 1, Under: 1, Deep: 1, Biome: 1})
		tr.Touch()
		out := map[FoliageShape]int{}
		for _, s := range tr.Sections() {
			for _, b := range tr.BuildFoliage(s).Batches {
				out[b.Shape] += len(b.Instances)
			}
		}
		return out
	}
	base := count(nil)
	got := count(map[string]float32{"flower": 0, "grass": 2})
	if got[FoliageFlower] != 0 {
		t.Errorf("flowers at amount 0 still grew %d", got[FoliageFlower])
	}
	if r := float64(got[FoliageGrass]) / float64(base[FoliageGrass]); r < 1.7 || r > 2.3 {
		t.Errorf("grass at amount 2 grew %.2fx as much (%d vs %d)", r, got[FoliageGrass], base[FoliageGrass])
	}
	if got[FoliageTallGrass] != base[FoliageTallGrass] {
		t.Errorf("tall grass changed (%d -> %d) though only grass and flowers were set", base[FoliageTallGrass], got[FoliageTallGrass])
	}

	var b TerrainBiome
	b.SetAmount(FoliageRock, 3)
	b.SetAmount(FoliageRock, 1)
	if b.Amounts != nil {
		t.Error("setting an amount back to 1 left an override behind")
	}
	b.SetAmount(FoliageRock, 9)
	if b.Amount(FoliageRock) != MaxFoliageAmount {
		t.Errorf("amount not clamped: %v", b.Amount(FoliageRock))
	}
}

// A level may write a shape's short name in `amounts:`; it is kept under the
// shape's own key.
func TestAmountsAcceptShortShapeNames(t *testing.T) {
	tr, err := TerrainFromRaw(&RawTerrain{SizeX: 2, SizeY: 2, SizeZ: 2, BlockSize: 1,
		Biomes: []RawTerrainBiome{{Preset: "forest", Amounts: map[string]float32{"broadleaf": 0, "litter": 2}}}})
	if err != nil {
		t.Fatal(err)
	}
	b := &tr.Biomes[0]
	if b.Amount(FoliageBroadleaf) != 0 || b.Amount(FoliageLitter) != 2 {
		t.Fatalf("short names were not honoured: %v", b.Amounts)
	}
}

// A fill paints only the tops of the columns, so a smooth hill's terraces
// have unpainted risers. They must not thin the biome's ground out across
// every step of the slope: the ground stays fully painted all over the hill.
func TestUnpaintedRisersDoNotThinThePaint(t *testing.T) {
	tr := NewTerrain(mgl32.Vec3{}, 1, 48, 16, 48)
	tr.Biomes = []TerrainBiome{{Preset: "desert"}}
	tr.Generate(TerrainFill{Height: 4, Hills: 5, HillSize: 8, Seed: 3, Top: 1, Under: 1, Deep: 1, Biome: 1})
	tr.Smooth = true
	tr.Touch()
	low, total := 0, 0
	for _, s := range tr.Sections() {
		for _, f := range s.Faces {
			x, _, z, d := tr.FaceBlock(f.Key)
			if d != FaceYPos || x < 2 || z < 2 || x > 45 || z > 45 {
				continue
			}
			for c := 0; c < 4; c++ {
				total++
				if f.Ground[c]&0xff < 250 {
					low++
				}
			}
		}
	}
	if total == 0 || low*50 > total {
		t.Fatalf("%d of %d painted corners on a hill are thinned below full paint", low, total)
	}
}
