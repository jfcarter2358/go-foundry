package stages

// region_material_test.go — the region_material stage (docs/regions/
// PHASE_3.md) end to end on the fake Poly Haven: two regions, each growing
// a preset with its own ground material, baked into one eight-layer splat;
// shared textures sharing a layer; the four-layer fall-back; the error
// past eight layers; the far backdrop's material; the auto material of a
// region terrain; and the hash following a preset's ground.

import (
	"context"
	"image"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/material"
	"foundry/internal/procgen"
	"foundry/internal/procgen/regions"
	"foundry/internal/procgen/terrain"
)

// groundPreset writes a preset with a plain terrain, a textures stage of
// four layers — all the fake's one texture, "sand", at the given repeats —
// and a terrain_material over them; it returns its path.
func groundPreset(t *testing.T, dir, name string, terrainP procgen.Params, scales [4]float64, extra procgen.Params) string {
	t.Helper()
	f, s, ref := procgen.Float, procgen.String, procgen.StageRef
	var items []procgen.Params
	keys := procgen.Params{"terrain": ref("ground"), "textures": ref("tex")}
	for i, k := range splatLayerKeys {
		n := name + "_" + k
		items = append(items, procgen.Params{"name": s(n), "id": s("sand"), "scale": f(scales[i])})
		keys[k] = s(n)
	}
	for k, v := range extra {
		keys[k] = v
	}
	d := &procgen.Doc{Name: name, Seed: 5, Stages: []procgen.Stage{
		{Kind: PolyHavenTexturesKind, ID: "tex", Params: procgen.Params{"layers": procgen.List(items...)}},
		{Kind: TerrainKind, ID: "ground", Params: terrainP},
		{Kind: TerrainMaterialKind, ID: "ground_mat", Params: keys},
	}}
	p := filepath.Join(dir, name+procgen.Ext)
	if err := procgen.WriteDoc(p, d); err != nil {
		t.Fatal(err)
	}
	return p
}

// twoBiomeWorld is a level doc of a hilly west region (preset A) and a
// gentle east one (preset B) meeting at x = 0 over an 80 m band, with a
// region_material; scalesB's first repeat equal to A's makes their sand
// one shared layer.
func twoBiomeWorld(t *testing.T, scalesB [4]float64, ground procgen.Params) *procgen.Doc {
	t.Helper()
	dir := t.TempDir()
	f, i := procgen.Float, procgen.Int
	a := groundPreset(t, dir, "hills", procgen.Params{"relief": f(30), "mesa_height": f(25), "mesa_scale": f(60),
		"mesa_coverage": f(0.4), "mesa_tiers": i(1), "droplets": f(0.2), "talus_iterations": i(10), "canyons": procgen.List()},
		[4]float64{1, 2, 3, 4}, procgen.Params{"rock_scale": f(0)})
	b := groundPreset(t, dir, "plain", procgen.Params{"base_height": f(-5), "relief": f(6), "mesa_height": f(0), "mesa_tiers": i(0),
		"terrace": f(0), "droplets": f(0.2), "talus_iterations": i(10), "canyons": procgen.List()},
		// rock_scale 0: its rock at its textures stage's repeat (scalesB[3]),
		// not the stage's default 6 m, which would make it the hills' rock.
		scalesB, procgen.Params{"rock_slope": procgen.Vec2(20, 30), "strata": f(0), "rock_scale": f(0)})
	d := worldDoc("biomes", []procgen.Params{
		regionItem("hills", DesertCanyonEnvironment, a, -160, -96, 0, 96, 80),
		regionItem("plain", SavannahEnvironment, b, 0, -96, 128, 96, 80),
	}, procgen.Params{"resolution": f(1), "tile_size": f(64), "warp_amount": f(10)}, ground)
	d.Stages = append(d.Stages, procgen.Stage{Kind: RegionMaterialKind, ID: "ground_mat",
		Params: procgen.Params{"terrain": procgen.StageRef("ground")}})
	return d
}

func runRegionMaterialDoc(t *testing.T, d *procgen.Doc) (*procgen.Result, *TerrainMaterialArtifact, *terrain.Terrain, string) {
	t.Helper()
	_, res := withFakePolyHaven(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	r, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: filepath.Join(res, "generated", d.Name), Workers: 8})
	if err != nil {
		t.Fatal(err)
	}
	return r, r.Stage("ground_mat").Artifact.(*TerrainMaterialArtifact), r.Stage("ground").Artifact.(*terrain.Terrain), res
}

// nrgbaOf decodes a map the stage wrote.
func nrgbaOf(t *testing.T, res, ref string) *image.NRGBA {
	t.Helper()
	img, ok := readPNG(t, filepath.Join(res, filepath.FromSlash(ref)+".png")).(*image.NRGBA)
	if !ok {
		t.Fatalf("%s is not NRGBA", ref)
	}
	return img
}

func TestRegionMaterialBakesTwoBiomesIntoEightLayers(t *testing.T) {
	// B's sand is A's (repeat 1): one shared layer; seven in all.
	r, a, tr, res := runRegionMaterialDoc(t, twoBiomeWorld(t, [4]float64{1, 5, 6, 7}, nil))
	if len(a.Layers) != 7 || a.Weights2 == "" {
		t.Fatalf("%d layers, weights2 %q; want 7 and a second map", len(a.Layers), a.Weights2)
	}
	shared := a.Layers[0]
	if shared.Role != "sand" || len(shared.Environments) != 2 {
		t.Errorf("layer 1 %+v: want the sand both environments share", shared)
	}
	// Coverage by role sums to one.
	if s := a.Coverage[0] + a.Coverage[1] + a.Coverage[2] + a.Coverage[3]; math.Abs(s-1) > 0.02 {
		t.Errorf("coverage %v sums to %.3f", a.Coverage, s)
	}
	// The material: an eight-layer splat whose layers are the list's, the
	// hills' rock (the lead's? whichever) banded and the other not.
	md, err := material.ReadMaterial(filepath.Join(res, "materials", a.Material+".fmat"))
	if err != nil {
		t.Fatal(err)
	}
	fx := material.ResolveSurfaceDoc(md).Fx
	if !fx.HasSplat || !fx.SplatEight || fx.SplatWeights2 != a.Weights2 {
		t.Fatalf("not an eight-layer splat: %+v", fx)
	}
	rocks, banded := 0, 0
	for i, l := range a.Layers {
		sl := fx.SplatLayers[i]
		if sl.Texture != l.Name || math.Abs(float64(sl.Scale)-l.Scale) > 1e-3 || sl.Triplanar != l.Triplanar {
			t.Errorf("material layer %d %+v, manifest %+v", i+1, sl, l)
		}
		if l.Role == "rock" {
			rocks++
			if !sl.NoStrata {
				banded++
			}
		}
	}
	if rocks != 2 || banded != 1 {
		t.Errorf("%d rock layers, %d banded by the strata; want 2 and 1 (the lead's)", rocks, banded)
	}
	// Every texel's eight weights sum to one; deep in each region only its
	// own layers (and the shared sand) have weight; across the band the
	// hills' share falls smoothly.
	w1, w2 := nrgbaOf(t, res, a.Weights), nrgbaOf(t, res, a.Weights2)
	g := tr.Grid
	sz := g.S()
	hillsOnly, plainOnly := map[int]bool{}, map[int]bool{}
	for i, l := range a.Layers {
		if len(l.Environments) == 1 {
			if l.Environments[0] == DesertCanyonEnvironment {
				hillsOnly[i] = true
			} else {
				plainOnly[i] = true
			}
		}
	}
	weight := func(k, i int) float64 {
		if i < 4 {
			return float64(w1.Pix[4*k+i]) / 255
		}
		return float64(w2.Pix[4*k+i-4]) / 255
	}
	share := func(k int, set map[int]bool) float64 {
		s := 0.0
		for i := range set {
			s += weight(k, i)
		}
		return s
	}
	for k := 0; k < sz*sz; k++ {
		s := 0.0
		for i := range a.Layers {
			s += weight(k, i)
		}
		if s < 0.97 || s > 1.03 {
			t.Fatalf("texel %d's weights sum to %.3f", k, s)
		}
	}
	col := func(x float64) int { return int(math.Round((x - g.MinX) / g.Cell)) }
	// Down a column, within the footprints' z (the union is padded to a
	// square, and its padding is gap the nearest regions share).
	colMean := func(i int, set map[int]bool) float64 {
		s, n := 0.0, 0
		for j := 0; j < sz; j++ {
			if math.Abs(g.Z(j)) <= 80 {
				s += share(j*sz+i, set)
				n++
			}
		}
		return s / float64(n)
	}
	if v := colMean(col(-120), plainOnly); v > 0.01 {
		t.Errorf("deep in the hills the plain's own layers weigh %.3f", v)
	}
	if v := colMean(col(100), hillsOnly); v > 0.01 {
		t.Errorf("deep in the plain the hills' own layers weigh %.3f", v)
	}
	prevShare, maxStep := -1.0, 0.0
	for x := -70.0; x <= 70; x += g.Cell {
		v := colMean(col(x), hillsOnly)
		if prevShare >= 0 {
			maxStep = math.Max(maxStep, math.Abs(v-prevShare))
		}
		prevShare = v
	}
	if mid := colMean(col(0), hillsOnly) + colMean(col(0), plainOnly); mid < 0.2 {
		t.Errorf("at the seam neither biome's own layers weigh much (%.3f)", mid)
	}
	// A seam would step by about the band's whole change (~0.5); the rules
	// within each biome (slopes, washes) wiggle it by a few hundredths.
	if maxStep > 0.15 {
		t.Errorf("the hills' share steps by %.3f between neighbouring columns across the band: a seam", maxStep)
	}
	t.Logf("layers %+v; largest column step across the band %.3f", a.Layers, maxStep)
	// The tiles wear it.
	for _, p := range r.Placements() {
		if strings.Contains(p.ID, ".ground.tile") && p.Material != "" && p.Material != a.Material {
			t.Errorf("tile %s wears %q", p.ID, p.Material)
		}
	}
}

func TestRegionMaterialFallsBackToFourLayers(t *testing.T) {
	// The same four textures at the same repeats in both: four layers, the
	// classic splat, every layer shared.
	_, a, _, res := runRegionMaterialDoc(t, twoBiomeWorld(t, [4]float64{1, 2, 3, 4}, nil))
	if len(a.Layers) != 4 || a.Weights2 != "" {
		t.Fatalf("%d layers, weights2 %q; want the four shared ones and no second map", len(a.Layers), a.Weights2)
	}
	md, err := material.ReadMaterial(filepath.Join(res, "materials", a.Material+".fmat"))
	if err != nil {
		t.Fatal(err)
	}
	if fx := material.ResolveSurfaceDoc(md).Fx; !fx.HasSplat || fx.SplatEight {
		t.Fatalf("not a four-layer splat: %+v", fx)
	}
}

func TestRegionMaterialFarBackdropAndAutoMaterial(t *testing.T) {
	f := procgen.Float
	// No material named on the terrain: it wears the region material's,
	// tiles and backdrop. (Cooked: the backdrop is only made for a cooked
	// terrain.)
	d := twoBiomeWorld(t, [4]float64{1, 5, 6, 7}, procgen.Params{"far": f(256), "far_cell": f(16), "far_blend": f(64), "far_tile": f(256),
		"cook": procgen.Bool(true), "cook_memory": procgen.Int(1000)})
	r, a, tr, res := runRegionMaterialDoc(t, d)
	if a.FarMaterial != a.Material+"_far" || a.FarWeights == "" || a.FarWeights2 == "" {
		t.Fatalf("far material %q weights %q %q", a.FarMaterial, a.FarWeights, a.FarWeights2)
	}
	fd, err := material.ReadMaterial(filepath.Join(res, "materials", a.FarMaterial+".fmat"))
	if err != nil {
		t.Fatal(err)
	}
	if fx := material.ResolveSurfaceDoc(fd).Fx; !fx.SplatEight || fx.HasDetailNormal {
		t.Fatalf("far material %+v", fx)
	}
	// East of the map the backdrop is the plain's: its own layers.
	fm := tr.Meta.Far
	w2 := nrgbaOf(t, res, a.FarWeights2)
	w1 := nrgbaOf(t, res, a.FarWeights)
	n := fm.Samples
	east, west := 0.0, 0.0
	for j := 0; j < n; j++ {
		for _, side := range []struct {
			i   int
			sum *float64
		}{{n - 2, &east}, {1, &west}} {
			k := j*n + side.i
			for c, l := range a.Layers {
				if len(l.Environments) != 1 || l.Environments[0] != SavannahEnvironment {
					continue
				}
				v := 0.0
				if c < 4 {
					v = float64(w1.Pix[4*k+c])
				} else {
					v = float64(w2.Pix[4*k+c-4])
				}
				*side.sum += v / 255 / float64(n)
			}
		}
	}
	if east < 0.3 || west > 0.05 {
		t.Errorf("the plain's own layers weigh %.3f on the backdrop's east edge and %.3f on its west", east, west)
	}
	wear := map[string]string{}
	for _, p := range r.Placements() {
		for _, pre := range []string{".ground.tile_", ".ground.far_", ".ground.strip_"} {
			if strings.Contains(p.ID, pre) {
				wear[pre] = p.Material
			}
		}
	}
	if wear[".ground.tile_"] != a.Material || wear[".ground.far_"] != a.FarMaterial || wear[".ground.strip_"] != a.Material {
		t.Errorf("auto materials %v, want %s and %s", wear, a.Material, a.FarMaterial)
	}
}

// More than eight distinct layers is a palette (region_material_palette.go):
// twelve merge; more than the palette's 32 are refused, naming them.
func TestRegionMaterialPaletteLimits(t *testing.T) {
	mk := func(envs int) []*biomeLayerSet {
		var sets []*biomeLayerSet
		for e := 0; e < envs; e++ {
			p := procgen.Resolve(procgen.KindByName(TerrainMaterialKind).Params, procgen.Params{"rock_scale": procgen.Float(0)})
			b := &biomeLayerSet{src: biomeSource{env: string(rune('a' + e)), p: p}}
			for i := 0; i < 4; i++ {
				b.layers[i] = &TextureLayer{ID: "tex", Albedo: "ph_tex", Scale: float64(1 + 4*e + i)}
				b.tints[i] = [3]float64{1, 1, 1}
			}
			sets = append(sets, b)
		}
		return sets
	}
	if l, err := mergeRegionLayers(mk(3), "a"); err != nil || len(l) != 12 {
		t.Fatalf("three sets: %d layers, %v", len(l), err)
	}
	if l, err := mergeRegionLayers(mk(8), "a"); err != nil || len(l) != 32 {
		t.Fatalf("eight sets: %d layers, %v", len(l), err)
	}
	_, err := mergeRegionLayers(mk(9), "a")
	if err == nil || !strings.Contains(err.Error(), "36 distinct ground layers") || !strings.Contains(err.Error(), "i rock") {
		t.Fatalf("err %v", err)
	}
}

func TestRegionMaterialHashFollowsThePresetsGround(t *testing.T) {
	d := twoBiomeWorld(t, [4]float64{1, 5, 6, 7}, nil)
	h1, err := procgen.StageHashes(d)
	if err != nil {
		t.Fatal(err)
	}
	// Edit the plain's ground material only: the material reruns, the
	// terrain does not.
	rs, _ := regionMaterialRegions(d, "ground")
	var plain string
	for _, r := range rs {
		if r.Key == "plain" {
			plain = r.Generator
		}
	}
	pd, err := procgen.ReadDoc(plain)
	if err != nil {
		t.Fatal(err)
	}
	pd.StageByID("ground_mat").Params["cracked_cover"] = procgen.Float(0.9)
	if err := procgen.WriteDoc(plain, pd); err != nil {
		t.Fatal(err)
	}
	h2, err := procgen.StageHashes(d)
	if err != nil {
		t.Fatal(err)
	}
	if h1["ground_mat"] == h2["ground_mat"] {
		t.Error("editing a preset's terrain_material did not change the region material's hash")
	}
	if h1["ground"] != h2["ground"] {
		t.Error("editing a preset's terrain_material changed the terrain's hash")
	}
	if k := procgen.KindByName(RegionMaterialKind); k.Regional != procgen.RegionalShared || len(k.SharedFor) != 1 ||
		k.SharedFor[0] != TerrainMaterialKind {
		t.Errorf("region_material is %v for %v", k.Regional, k.SharedFor)
	}
}

func TestSavannahPresetHasAGround(t *testing.T) {
	d := SavannahPreset()
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	srcs, err := biomeSources([]regions.Region{{Key: "s", ID: "s", Environment: SavannahEnvironment}})
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 1 || srcs[0].p.Str("sand") != "grass" || len(srcs[0].tex.Items("layers")) != 4 {
		t.Fatalf("savannah ground %+v", srcs)
	}
}

// The preview: no download, the layers in false colour and the ground
// colour, a note per layer, and one grey picture per layer on request.
func TestRegionMaterialPreview(t *testing.T) {
	_, res := withFakePolyHaven(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	d := twoBiomeWorld(t, [4]float64{1, 5, 6, 7}, procgen.Params{"preview_cells": procgen.Int(96)})
	d.StageByID("ground_mat").Params["preview_layers"] = procgen.Bool(true)
	p, err := procgen.Preview(context.Background(), d, "ground_mat", procgen.NewPreviewCache(4), procgen.PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, im := range p.Images {
		names[im.Name] = true
	}
	if !names["Layers"] || !names["Ground colour"] || len(p.Images) != 2+7 {
		t.Errorf("images %v", names)
	}
	if len(p.Notes) == 0 || !strings.Contains(p.Notes[0], "7 layers (eight-layer splat)") {
		t.Errorf("notes %v", p.Notes)
	}
	if len(p.Meshes) == 0 {
		t.Error("no meshes")
	}
}
