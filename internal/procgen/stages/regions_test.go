package stages

// regions_test.go — the regions stage, the environment presets, the
// level_world template and the phase-1 GATE (docs/regions/SPEC.md): a level
// with a desert canyon region and a savannah region, synced into a level
// generation doc, previews as a biome overlay showing both colours, blended
// across the band between them.

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/procgen/regions"
	"foundry/internal/world"
)

// twoRegionLevel is a level with two 600 m regions meeting at x = 0, the
// desert canyon to the west and the savannah to the east, a 200 m band.
func twoRegionLevel() *world.RawLevel {
	region := func(id, env string, x float32) world.RawVolume {
		return world.RawVolume{ID: id, Position: world.RawVec3{X: x, Y: 40}, Half: world.RawVec3{X: 300, Y: 25, Z: 300},
			Region: &world.RawVolumeRegion{Environment: env, Transition: 200,
				Footprint: [][2]float32{{-300, -300}, {300, -300}, {300, 300}, {-300, 300}}}}
	}
	return &world.RawLevel{Volumes: []world.RawVolume{
		// Listed east first: the sync sorts by id.
		region("savannah_east", SavannahEnvironment, 300),
		{ID: "a_trigger", Half: world.RawVec3{X: 1, Y: 1, Z: 1}},
		region("canyon_west", DesertCanyonEnvironment, -300),
	}}
}

func syncedWorldDoc(t *testing.T) *procgen.Doc {
	t.Helper()
	d, err := procgen.NewFromTemplate(LevelWorldTemplate, "arena_world")
	if err != nil {
		t.Fatal(err)
	}
	if d.Level != "arena" {
		t.Errorf("template targets %q, want arena", d.Level)
	}
	changed, err := genlevel.SyncRegions(d, twoRegionLevel())
	if err != nil || !changed {
		t.Fatalf("sync: changed=%v err=%v", changed, err)
	}
	if again, _ := genlevel.SyncRegions(d, twoRegionLevel()); again {
		t.Error("a second sync of the same level changed the doc")
	}
	return d
}

func rgbAt(im *procgen.ImageLayer, x, z float64) [3]float64 {
	i := int((x - im.Bounds.MinX) / (im.Bounds.MaxX - im.Bounds.MinX) * float64(im.W))
	j := int((z - im.Bounds.MinZ) / (im.Bounds.MaxZ - im.Bounds.MinZ) * float64(im.H))
	o := (j*im.W + i) * 4
	return [3]float64{float64(im.RGBA[o]) / 255, float64(im.RGBA[o+1]) / 255, float64(im.RGBA[o+2]) / 255}
}

func colorNear(a, b [3]float64, tol float64) bool {
	return math.Abs(a[0]-b[0]) <= tol && math.Abs(a[1]-b[1]) <= tol && math.Abs(a[2]-b[2]) <= tol
}

// THE GATE.
func TestGateTwoRegionsPreviewAsABiomeOverlay(t *testing.T) {
	d := syncedWorldDoc(t)
	s := genlevel.RegionsStage(d)
	items := s.Params.Items(regions.ParamRegions)
	if len(items) != 2 || items[0].Str("id") != "canyon_west" || items[1].Str("id") != "savannah_east" {
		t.Fatalf("synced items %v, want canyon_west then savannah_east", items)
	}
	if pts := items[0].Points("footprint"); len(pts) != 4 || pts[0][0] != -600 || pts[2][0] != 0 {
		t.Fatalf("canyon footprint %v, want world XZ -600..0", pts)
	}
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}

	p, err := procgen.Preview(context.Background(), d, "regions", procgen.NewPreviewCache(4), procgen.PreviewOptions{Workers: 4})
	if err != nil {
		t.Fatal(err)
	}
	var im *procgen.ImageLayer
	for i := range p.Images {
		if p.Images[i].Name == RegionsOverlayName {
			im = &p.Images[i]
		}
	}
	if im == nil || im.W < 64 || len(im.RGBA) != im.W*im.H*4 {
		t.Fatalf("no overlay image in %+v", p.Images)
	}
	desert := procgen.EnvironmentColor(DesertCanyonEnvironment)
	savannah := procgen.EnvironmentColor(SavannahEnvironment)
	tol := 2.0 / 255
	// Deep in each region: its own colour, exactly.
	if c := rgbAt(im, -300, 0); !colorNear(c, desert, tol) {
		t.Errorf("west centre %v, want desert %v", c, desert)
	}
	if c := rgbAt(im, 300, 0); !colorNear(c, savannah, tol) {
		t.Errorf("east centre %v, want savannah %v", c, savannah)
	}
	// In the band: a blend, strictly between the two, along the whole seam.
	blended := 0
	for z := -250.0; z <= 250; z += 25 {
		c := rgbAt(im, 0, z)
		if colorNear(c, desert, tol) || colorNear(c, savannah, tol) {
			continue
		}
		between := true
		for ch := 0; ch < 3; ch++ {
			lo, hi := math.Min(desert[ch], savannah[ch]), math.Max(desert[ch], savannah[ch])
			between = between && c[ch] >= lo-tol && c[ch] <= hi+tol
		}
		if between {
			blended++
		}
	}
	if blended < 15 {
		t.Errorf("only %d of 21 seam samples are a blend of the two colours", blended)
	}
	// Walking west to east across the band the colour moves monotonically
	// from desert to savannah (red falls: desert 0.86, savannah 0.78), and
	// is half way about the seam (the warp moves the seam a little).
	prev := 2.0
	for x := -140.0; x <= 140; x += 10 {
		c := rgbAt(im, x, 0)
		if c[0] > prev+tol {
			t.Errorf("red rises at x=%v: %v after %v", x, c[0], prev)
		}
		prev = c[0]
	}
	// Outlines in the environments' colours, and per-region weight layers.
	if len(p.Lines) != 2 || !p.Lines[0].Closed {
		t.Errorf("lines %+v, want two closed outlines", p.Lines)
	}
	if len(p.Images) != 3 {
		t.Errorf("%d image layers, want the overlay and two weights", len(p.Images))
	}
	if os.Getenv("FOUNDRY_REGIONS_DUMP") != "" {
		dumpOverlay(t, im, os.Getenv("FOUNDRY_REGIONS_DUMP"))
	}
}

// The run writes regions.yaml, and Load reads it back to the same weights.
func TestRegionsRunAndLoad(t *testing.T) {
	d := syncedWorldDoc(t)
	// The regions stage alone: the template's region_terrain has tests of
	// its own (region_terrain_test.go), and a cooked 0.5 m union is minutes.
	d.Stages = d.Stages[:1]
	dir := t.TempDir()
	var logs []string
	res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir, Workers: 2,
		Log: func(l string) { logs = append(logs, l) }})
	if err != nil {
		t.Fatal(err)
	}
	sr := res.Stage("regions")
	set, ok := sr.Artifact.(*regions.Set)
	if !ok || set.Len() != 2 {
		t.Fatalf("artifact %T", sr.Artifact)
	}
	data, err := os.ReadFile(sr.File(RegionsFileName))
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseRegionsFile(data)
	if err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	for _, x := range []float64{-400, -50, 0, 30, 400} {
		if a, b := set.Weight(0, x, 10), back.Weight(0, x, 10); a != b {
			t.Errorf("loaded set disagrees at x=%v: %v vs %v", x, a, b)
		}
	}
	// With no region_terrain or region_material in the doc, the presets'
	// placement stages (per region since phase 4) have nothing to stand on:
	// each is skipped with a line saying so, their asset stages (on demand)
	// are not expanded, and nothing runs but the regions; drift is never
	// per region.
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "preset stage scatter refers to ground (terrain), and the level doc has no enabled stage standing in for terrain") {
		t.Errorf("no skip note for the canyon's scatter in:\n%s", joined)
	}
	if !strings.Contains(joined, "region canyon_west (desert_canyon): preset stage drift (drift) cannot run per region yet") {
		t.Errorf("no skip note for the canyon's drift in:\n%s", joined)
	}
	if strings.Contains(joined, "preset stage ground ") {
		t.Error("the shared terrain stage was mentioned as skipped")
	}
	if len(res.Stages) != 1 {
		t.Errorf("%d stages ran or were skipped, want just regions", len(res.Stages))
	}
}

func TestEnvironmentPresetsAreValid(t *testing.T) {
	for _, name := range []string{DesertCanyonEnvironment, SavannahEnvironment} {
		d, from, err := procgen.LoadEnvironment(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(from, name) {
			t.Errorf("%s loaded from %q", name, from)
		}
		if probs := d.Validate(); len(probs) > 0 {
			t.Errorf("%s: %v", name, probs)
		}
		if d.Level != "" {
			t.Errorf("%s targets level %q; a preset must not", name, d.Level)
		}
		hasTerrain := false
		for _, s := range d.Stages {
			if s.Kind == EnvironmentKind {
				t.Errorf("%s carries a level-wide environment stage", name)
			}
			hasTerrain = hasTerrain || s.Kind == TerrainKind
		}
		if !hasTerrain {
			t.Errorf("%s has no terrain (its field provider)", name)
		}
		if _, err := procgen.EncodeDoc(d); err != nil {
			t.Errorf("%s does not encode: %v", name, err)
		}
	}
	// The canyon preset is the template less its environment stage.
	tpl := DesertCanyon(DesertCanyonEnvironment)
	pre := DesertCanyonPreset()
	if len(pre.Stages) != len(tpl.Stages)-1 {
		t.Errorf("preset has %d stages, template %d", len(pre.Stages), len(tpl.Stages))
	}
	sav := SavannahPreset()
	g := sav.StageByID("ground").Params
	// A rolling plain: no mesas, no canyons, long low swells (relief over
	// a wavelength of the base noise: a gentle mean gradient).
	if g.Float("mesa_height") != 0 || len(g.Items("canyons")) != 0 || g.Float("relief")*g.Float("frequency") > 0.08 {
		t.Errorf("savannah terrain is not a rolling plain: %v", g)
	}
}

// Adding regions to the engine changed no existing document: the
// desert_canyon template's stage hashes are exactly the ones recorded before
// phase 1 (2026-10-12, before StageKind.Regional and the expansion existed).
// If a stage kind's own Version or schema changes on purpose, update these.
func TestDesertCanyonHashesUnchangedByRegions(t *testing.T) {
	want := map[string]string{
		"air":        "d2a74f23bb4f980ec56ace4fd04b7a156b7d9247a103d4436840d86bdbcdb7d4",
		"boulders":   "f884431f9bdd267398995b4ccd097c5830bc388eb38514e310d4574cf30774a2",
		"bushes":     "435160c11d51cc1622e66aa5dcc4f1e098acc06f767c2bc70bc18788be3c935f",
		"deadwood":   "71602a7486830366439c29597427454a1a4bfb8bd2df3b424f234317729d6e80",
		"drift":      "c8e261eeee0e6fa0085489b186d00e621c61ac438b98a12e0cf1f7898aaca7cb",
		"ground":     "9c36ee193b88439c4b1ccc2819e5def4c08b2f0dc0f9f6d94a8cb6411fb3f7f5",
		"ground_mat": "57f8200ee32e1a66f23064f0d1e266b1852929dd41cd5cf91eaf8468ff9217cd",
		"rockface":   "37607833d2d21f4002a9e87719a7b5460ad066c7b27b815da52f8911d87bcb54",
		"rockfall":   "79f2c9ee7d2925f1b3fc4ff822aee4ee4e52dd63343d79a2a53d860bfc25c57e",
		"scatter":    "e267df62e46bc601925d019d54981de628d32e46e075846356634397e5e6fbae",
		"stones":     "084428318f971755b407570611c33baadea9aef511f4e3f4a4c97f0412fec615",
		"tex":        "dd02397d13684110bec21f07a33a06cff82e9a870aafb042b84ae86eb2ce2670",
		"trees":      "397cf244cf76fe444e156690b0a0ca1b092fc80e978413e5b1f40011840a37da",
	}
	d, err := procgen.NewFromTemplate(DesertCanyonTemplate, "desert_canyon")
	if err != nil {
		t.Fatal(err)
	}
	got, err := procgen.StageHashes(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Errorf("%d hashed stages, want %d", len(got), len(want))
	}
	for id, h := range want {
		if got[id] != h {
			t.Errorf("stage %s hash %s, want %s", id, got[id], h)
		}
	}
}

func TestLevelWorldTemplate(t *testing.T) {
	if LevelWorldDocName("Level1") != "level1_world" {
		t.Errorf("doc name %q", LevelWorldDocName("Level1"))
	}
	d := LevelWorld("arena_world")
	if d.Level != "arena" || len(d.Stages) != 3 || d.Stages[0].Kind != RegionsKind || d.Stages[1].Kind != RegionTerrainKind ||
		d.Stages[2].Kind != RegionMaterialKind {
		t.Fatalf("template %+v", d)
	}
	if probs := d.Validate(); len(probs) > 0 {
		t.Errorf("problems: %v", probs)
	}
	// A bad region list is a validation problem.
	genlevel.SyncRegions(d, &world.RawLevel{Volumes: []world.RawVolume{{ID: "x", Half: world.RawVec3{X: 1, Y: 1, Z: 1},
		Region: &world.RawVolumeRegion{Environment: "savannah", Footprint: [][2]float32{{0, 0}, {1, 0}}}}}})
	if probs := d.Validate(); len(probs) == 0 {
		t.Error("a two-point footprint validated")
	}
}
