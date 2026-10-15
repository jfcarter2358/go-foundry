package stages

// terrain_material_cover_test.go — the cover rules (terrain_material_
// cover.go): snow by altitude and slope, wet ground by standing water, ash
// by vents; a terrain_material with covers is an eight-layer splat; a
// region material dedupes and carries them; none set changes nothing.

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

func TestCoverSharesAndStack(t *testing.T) {
	p := procgen.Resolve(procgen.KindByName(TerrainMaterialKind).Params, procgen.Params{
		"snow": procgen.String("s"), "snow_height": procgen.Vec2(100, 120), "snow_slope": procgen.Vec2(30, 45),
		"snow_breakup": procgen.Float(0), "wet": procgen.String("w"), "wet_distance": procgen.Float(10), "wet_height": procgen.Float(2),
		"ash": procgen.String("a"), "ash_radius": procgen.Vec2(1, 2), "breakup": procgen.Float(0),
	})
	cr := coverRulesOf(p, []terrain.VentMeta{{X: 1000, Z: 0, Radius: 100}}, 1)
	if cr == nil {
		t.Fatal("covers off")
	}
	cases := []struct {
		name string
		s    coverSample
		want [3]float64
	}{
		{"high and gentle", coverSample{x: 0, z: 0, h: 150, slope: 10}, [3]float64{0, 0, 1}},
		{"high and steep", coverSample{x: 0, z: 0, h: 150, slope: 60}, [3]float64{0, 0, 0}},
		{"low", coverSample{x: 0, z: 0, h: 50, slope: 10}, [3]float64{0, 0, 0}},
		{"in the water", coverSample{h: 0, waterDist: 0, waterAbove: -1, hasWater: true}, [3]float64{1, 0, 0}},
		{"on the bank", coverSample{h: 0, waterDist: 3, waterAbove: 0.5, hasWater: true}, [3]float64{-1, 0, 0}},
		{"high above the water", coverSample{h: 0, waterDist: 3, waterAbove: 5, hasWater: true}, [3]float64{0, 0, 0}},
		{"at the vent", coverSample{x: 1000, z: 50, h: 0, slope: 10}, [3]float64{0, 1, 0}},
		{"far from the vent", coverSample{x: 1300, z: 0, h: 0, slope: 10}, [3]float64{0, 0, 0}},
	}
	for _, c := range cases {
		got := cr.shares(&c.s)
		for r := 0; r < 3; r++ {
			if c.want[r] < 0 {
				if got[r] <= 0.05 || got[r] >= 1 {
					t.Errorf("%s: cover %d %.3f, want partly", c.name, r, got[r])
				}
				continue
			}
			if math.Abs(got[r]-c.want[r]) > 1e-6 {
				t.Errorf("%s: cover %d %.3f, want %.0f", c.name, r, got[r], c.want[r])
			}
		}
	}
	// Stacked, the seven weights sum to one, and snow lies over the rest.
	out := make([]float32, groundRoles)
	cr.stack([]float32{0.5, 0.2, 0.2, 0.1}, [3]float64{0.5, 0.2, 0.6}, out, 0)
	sum := float32(0)
	for _, v := range out {
		sum += v
	}
	if math.Abs(float64(sum)-1) > 1e-5 || math.Abs(float64(out[roleSnow])-0.6) > 1e-6 {
		t.Fatalf("stacked %v (sum %v)", out, sum)
	}
}

// coverDoc is a small ridged terrain, a textures stage with a sand layer
// and a "snowy" one (the fake's one texture at another repeat, so a
// distinct layer), and a terrain_material over them with the given extra
// params.
func coverDoc(extra procgen.Params) *procgen.Doc {
	f, i, s, ref := procgen.Float, procgen.Int, procgen.String, procgen.StageRef
	mat := procgen.Params{"terrain": ref("ground"), "textures": ref("tex"), "sand": s("sand"), "cracked": s("sand"),
		"gravel": s("sand"), "rock": s("sand")}
	for k, v := range extra {
		mat[k] = v
	}
	return &procgen.Doc{Name: "cov", Seed: 3, Stages: []procgen.Stage{
		{Kind: TerrainKind, ID: "ground", Params: procgen.Params{"size": f(256), "resolution": f(2), "tile_size": f(128),
			"mesa_height": f(0), "mesa_tiers": i(0), "relief": f(2), "droplets": f(0), "drainage": f(0), "talus_iterations": i(5),
			"canyons": procgen.List(), "ridge_height": f(80), "ridge_scale": f(300), "ridge_octaves": i(3), "cook": procgen.Bool(false)}},
		{Kind: PolyHavenTexturesKind, ID: "tex", Params: procgen.Params{"layers": procgen.List(
			procgen.Params{"name": s("sand"), "id": s("sand")},
			procgen.Params{"name": s("snowy"), "id": s("sand"), "scale": f(7)})}},
		{Kind: TerrainMaterialKind, ID: "ground_mat", Params: mat},
	}}
}

func runCoverDoc(t *testing.T, d *procgen.Doc) (*TerrainMaterialArtifact, *terrain.Terrain, string) {
	t.Helper()
	_, res := withFakePolyHaven(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	r, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: filepath.Join(res, "generated", d.Name), Workers: 4})
	if err != nil {
		t.Fatal(err)
	}
	return r.Stage("ground_mat").Artifact.(*TerrainMaterialArtifact), r.Stage("ground").Artifact.(*terrain.Terrain), res
}

func TestTerrainMaterialSnowIsAnEightLayerCover(t *testing.T) {
	plain, _, _ := runCoverDoc(t, coverDoc(nil))
	if plain.Weights2 != "" || plain.CoverCoverage != nil {
		t.Fatalf("no cover keys, yet weights2 %q / %v", plain.Weights2, plain.CoverCoverage)
	}
	a, tr, res := runCoverDoc(t, coverDoc(procgen.Params{"snow": procgen.String("snowy"),
		"snow_height": procgen.Vec2(40, 55), "snow_slope": procgen.Vec2(35, 50)}))
	if a.Weights2 == "" || len(a.CoverCoverage) != 3 {
		t.Fatalf("weights2 %q, cover coverage %v", a.Weights2, a.CoverCoverage)
	}
	data, err := os.ReadFile(filepath.Join(res, "materials", a.Material+".fmat"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"weights2:", "layer7:", "layer7_scale: 7"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("material lacks %q:\n%s", want, data)
		}
	}
	w2 := nrgbaOf(t, res, a.Weights2)
	w1 := nrgbaOf(t, res, a.Weights)
	g := tr.Grid
	lo, hi := g.Range()
	snowHigh, nHigh, snowLow := 0.0, 0, 0.0
	for k := range g.H {
		sw := float64(w2.Pix[4*k+2]) / 255
		total := int(w1.Pix[4*k]) + int(w1.Pix[4*k+1]) + int(w1.Pix[4*k+2]) + int(w1.Pix[4*k+3]) + int(w2.Pix[4*k]) + int(w2.Pix[4*k+1]) + int(w2.Pix[4*k+2])
		if total < 250 || total > 262 {
			t.Fatalf("texel %d's seven weights sum to %d/255", k, total)
		}
		if g.H[k] < 30 {
			snowLow = math.Max(snowLow, sw)
		}
		if g.H[k] > 65 {
			snowHigh += sw
			nHigh++
		}
	}
	t.Logf("heights %.0f..%.0f m, snow cover %.1f%%, mean snow above 65 m %.2f, most below 30 m %.2f",
		lo, hi, 100*a.CoverCoverage[2], snowHigh/math.Max(float64(nHigh), 1), snowLow)
	if snowLow > 0 || nHigh == 0 || snowHigh/float64(nHigh) < 0.4 || a.CoverCoverage[2] <= 0 {
		t.Errorf("snow: none expected below 30 m (got %.2f), mostly snow above 65 m (got %.2f over %d samples)",
			snowLow, snowHigh/math.Max(float64(nHigh), 1), nHigh)
	}
}

// Wet ground round a region terrain's water, carried into the region
// material as a layer of its own.
func TestRegionMaterialCarriesWetGround(t *testing.T) {
	dir := t.TempDir()
	f, i, b, s, ref := procgen.Float, procgen.Int, procgen.Bool, procgen.String, procgen.StageRef
	preset := func(name string, terrainP, matExtra procgen.Params, extraLayer bool) string {
		layers := []procgen.Params{{"name": s("sand"), "id": s("sand"), "scale": f(3)}}
		if extraLayer {
			layers = append(layers, procgen.Params{"name": s("mud"), "id": s("sand"), "scale": f(9)})
		}
		mat := procgen.Params{"terrain": ref("ground"), "textures": ref("tex"), "sand": s("sand"), "cracked": s("sand"),
			"gravel": s("sand"), "rock": s("sand")}
		for k, v := range matExtra {
			mat[k] = v
		}
		d := &procgen.Doc{Name: name, Seed: 5, Stages: []procgen.Stage{
			{Kind: PolyHavenTexturesKind, ID: "tex", Params: procgen.Params{"layers": procgen.List(layers...)}},
			{Kind: TerrainKind, ID: "ground", Params: terrainP},
			{Kind: TerrainMaterialKind, ID: "ground_mat", Params: mat},
		}}
		p := filepath.Join(dir, name+procgen.Ext)
		if err := procgen.WriteDoc(p, d); err != nil {
			t.Fatal(err)
		}
		return p
	}
	flat := func(base float64) procgen.Params {
		return procgen.Params{"base_height": f(base), "relief": f(2), "frequency": f(0.01), "mesa_height": f(0), "mesa_tiers": i(0),
			"terrace": f(0), "droplets": f(0), "drainage": f(0), "talus_iterations": i(4), "canyons": procgen.List()}
	}
	wp := flat(-0.3)
	wp["water"], wp["water_level"] = b(true), f(0)
	marsh := preset("marsh", wp, procgen.Params{"wet": s("mud"), "wet_distance": f(6), "wet_height": f(1)}, true)
	dry := preset("dry", flat(5), nil, false)
	d := worldDoc("wetw", []procgen.Params{
		regionItem("marsh", "wetland", marsh, -128, -64, 0, 64, 40),
		regionItem("upland", SavannahEnvironment, dry, 0, -64, 128, 64, 40),
	}, procgen.Params{"resolution": f(2), "tile_size": f(64), "warp_amount": f(5)}, nil)
	d.Stages = append(d.Stages, procgen.Stage{Kind: RegionMaterialKind, ID: "ground_mat", Params: procgen.Params{"terrain": ref("ground")}})
	_, a, tr, _ := runRegionMaterialDoc(t, d)
	var wet *RegionMaterialLayer
	for k := range a.Layers {
		if a.Layers[k].Role == "wet" {
			wet = &a.Layers[k]
		}
	}
	if wet == nil {
		t.Fatalf("no wet layer among %+v", a.Layers)
	}
	if wet.Coverage <= 0.05 || len(wet.Environments) != 1 || wet.Environments[0] != "wetland" {
		t.Fatalf("wet layer %+v", wet)
	}
	if tr.Meta.Water == nil {
		t.Fatal("no water in the terrain")
	}
	t.Logf("layers: %d, wet %.0f%%", len(a.Layers), 100*wet.Coverage)
}
