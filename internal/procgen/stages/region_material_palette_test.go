package stages

// region_material_palette_test.go — a world of more than eight ground
// layers (region_material_palette.go): three environments of four layers
// each make a palette of twelve; every tile, strip and ring tile is
// re-dressed in a material of at most eight of them on the palette's one
// atlas, its weights the palette's own bytes over an aligned rectangle; a
// tile that would need more than eight is refused by name.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/material"
	"foundry/internal/procgen"
)

// paletteWorld is three regions in a row, west/mid/east, each its own
// preset with four layers of its own (the fake's texture at distinct
// repeats); widthMid is the middle region's width and band the transition.
func paletteWorld(t *testing.T, widthMid, band float64, ground procgen.Params) *procgen.Doc {
	t.Helper()
	dir := t.TempDir()
	f, i := procgen.Float, procgen.Int
	plain := func(base float64) procgen.Params {
		return procgen.Params{"base_height": f(base), "relief": f(8), "mesa_height": f(0), "mesa_tiers": i(0), "terrace": f(0),
			"droplets": f(0.1), "talus_iterations": i(5), "canyons": procgen.List()}
	}
	var items []procgen.Params
	half := widthMid / 2
	xs := [][2]float64{{-half - 128, -half}, {-half, half}, {half, half + 128}}
	for e, name := range []string{"west", "mid", "east"} {
		sc := [4]float64{float64(1 + 4*e), float64(2 + 4*e), float64(3 + 4*e), float64(4 + 4*e)}
		pr := groundPreset(t, dir, name, plain(float64(e)), sc, procgen.Params{"rock_scale": f(0), "rock_slope": procgen.Vec2(15, 25)})
		// Deep enough that the footprints fill the union's square: no gap
		// for the nearest-region fill to share among all three.
		items = append(items, regionItem(name, name+"_env", pr, xs[e][0], -240, xs[e][1], 240, band))
	}
	d := worldDoc("pal", items, procgen.Params{"resolution": f(1), "tile_size": f(64), "warp_amount": f(8)}, ground)
	d.Stages = append(d.Stages, procgen.Stage{Kind: RegionMaterialKind, ID: "ground_mat",
		Params: procgen.Params{"terrain": procgen.StageRef("ground"), "tile_margin": f(16)}})
	return d
}

func TestRegionMaterialPaletteOfTwelve(t *testing.T) {
	f := procgen.Float
	d := paletteWorld(t, 224, 40, procgen.Params{"far": f(256), "far_cell": f(16), "far_blend": f(64), "far_tile": f(256),
		"cook": procgen.Bool(true), "cook_memory": procgen.Int(1000)})
	r, a, tr, res := runRegionMaterialDoc(t, d)
	if a.Palette != 12 || len(a.Layers) != 12 || a.Weights != "" {
		t.Fatalf("palette %d, %d layers, union weights %q", a.Palette, len(a.Layers), a.Weights)
	}
	var atlas [material.SplatAtlasMax]string
	for i, l := range a.Layers {
		atlas[i] = l.Name
	}
	byName := map[string]RegionTileMaterial{}
	for _, tm := range a.Tiles {
		byName[tm.Material] = tm
	}
	// Every tile, strip and ring tile the terrain placed is re-dressed by
	// the material stage, once, its model and position untouched; none
	// keeps the union's name.
	orig := map[string]procgen.Placement{}
	for _, p := range r.Stage("ground").Placements {
		if p.Material == a.Material || p.Material == a.FarMaterial {
			orig[p.ID] = p
		}
	}
	got := map[string]procgen.Placement{}
	pre := procgen.StagePrefix(d.Name, "ground_mat")
	for _, p := range r.Placements() {
		if id, ok := strings.CutPrefix(p.ID, pre); ok {
			if _, dup := got[id]; dup {
				t.Fatalf("%s placed twice", id)
			}
			got[id] = p
		}
		if p.Material == a.Material || p.Material == a.FarMaterial {
			t.Fatalf("%s still wears the union's name %q", p.ID, p.Material)
		}
	}
	if len(got) != len(orig) || len(orig) == 0 {
		t.Fatalf("%d re-dressed, the terrain placed %d", len(got), len(orig))
	}
	four, eight := 0, 0
	uw := map[string]bool{}
	for id, p := range got {
		o := orig[id]
		if p.Model != o.Model || p.Position != o.Position {
			t.Errorf("%s moved", id)
		}
		tm, ok := byName[p.Material]
		if !ok {
			t.Fatalf("%s wears %q, not a palette material", id, p.Material)
		}
		if tm.Far != strings.HasPrefix(id, "far_") {
			t.Errorf("%s wears %s (far %v)", id, tm.Material, tm.Far)
		}
		uw[tm.Material] = true
	}
	g := tr.Grid
	for name := range uw {
		tm := byName[name]
		md, err := material.ReadMaterial(filepath.Join(res, "materials", name+".fmat"))
		if err != nil {
			t.Fatal(err)
		}
		fx := material.ResolveSurfaceDoc(md).Fx
		if !fx.HasSplat || fx.SplatAtlas != atlas || len(tm.Layers) > material.SplatLayersMax {
			t.Fatalf("%s: layers %v, atlas %v", name, tm.Layers, fx.SplatAtlas)
		}
		if (len(tm.Layers) > 4) != fx.SplatEight {
			t.Errorf("%s: %d layers, eight-layer %v", name, len(tm.Layers), fx.SplatEight)
		}
		if len(tm.Layers) > 4 {
			eight++
		} else {
			four++
		}
		// Each layer is the palette's own, at its own repeat.
		for c, l := range tm.Layers {
			if fx.SplatLayers[c].Texture != a.Layers[l-1].Name || float64(fx.SplatLayers[c].Scale) != a.Layers[l-1].Scale {
				t.Errorf("%s layer %d: %+v, the palette's layer %d is %+v", name, c+1, fx.SplatLayers[c], l, a.Layers[l-1])
			}
		}
		if tm.Far {
			continue
		}
		// The weights rectangle is on the union's texel grid, aligned.
		w := nrgbaOf(t, res, tm.Weights)
		cell := g.Cell
		i0 := (float64(fx.SplatMin.X()) - a.Min[0]) / cell
		j0 := (float64(fx.SplatMin.Y()) - a.Min[1]) / cell
		if i0 != float64(int(i0)) || int(i0)%paletteAlign != 0 || int(j0)%paletteAlign != 0 {
			t.Errorf("%s: weights from texel (%v, %v), not on the %d-texel grid", name, i0, j0, paletteAlign)
		}
		wdx := float64(fx.SplatMax.X()-fx.SplatMin.X()) / cell
		if int(wdx+0.5) != w.Bounds().Dx() {
			t.Errorf("%s: %v texels across its rectangle, its map %d", name, wdx, w.Bounds().Dx())
		}
	}
	t.Logf("palette of %d: %d tile materials (%d four-layer, %d eight-layer)", a.Palette, len(uw), four, eight)
	if four == 0 || eight == 0 {
		t.Errorf("want both four-layer tiles (deep in a region) and eight-layer ones (at a seam): %d, %d", four, eight)
	}
}

// Two regions of twelve layers between them meeting over a wide band: a
// tile in the band needs more than eight, and the run says which.
func TestRegionMaterialPaletteRefusesATileOfMoreThanEight(t *testing.T) {
	dir := t.TempDir()
	f, i := procgen.Float, procgen.Int
	plain := procgen.Params{"relief": f(20), "frequency": f(0.01), "mesa_height": f(0), "mesa_tiers": i(0), "terrace": f(0),
		"droplets": f(0.1), "talus_iterations": i(5), "canyons": procgen.List()}
	// Five layers each: the four rules and snow on the high ground.
	mk := func(name string, base float64) string {
		p := filepath.Join(dir, name+procgen.Ext)
		s, ref := procgen.String, procgen.StageRef
		var items []procgen.Params
		keys := procgen.Params{"terrain": ref("ground"), "textures": ref("tex"), "rock_scale": f(0), "rock_slope": procgen.Vec2(3, 8),
			"flat_slope": procgen.Vec2(1, 3), "snow_height": procgen.Vec2(0, 5), "snow_slope": procgen.Vec2(60, 70)}
		for k, key := range []string{"sand", "cracked", "gravel", "rock", "snow"} {
			n := name + "_" + key
			items = append(items, procgen.Params{"name": s(n), "id": s("sand"), "scale": f(base + float64(k))})
			keys[key] = s(n)
		}
		doc := &procgen.Doc{Name: name, Seed: 5, Stages: []procgen.Stage{
			{Kind: PolyHavenTexturesKind, ID: "tex", Params: procgen.Params{"layers": procgen.List(items...)}},
			{Kind: TerrainKind, ID: "ground", Params: plain},
			{Kind: TerrainMaterialKind, ID: "ground_mat", Params: keys},
		}}
		if err := procgen.WriteDoc(p, doc); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, b := mk("alpha", 1), mk("beta", 20)
	d := worldDoc("over", []procgen.Params{
		regionItem("alpha", "alpha_env", a, -128, -64, 0, 64, 120),
		regionItem("beta", "beta_env", b, 0, -64, 128, 64, 120),
	}, procgen.Params{"resolution": f(2), "tile_size": f(64), "warp_amount": f(5)}, procgen.Params{"cook": procgen.Bool(true)})
	d.Stages = append(d.Stages, procgen.Stage{Kind: RegionMaterialKind, ID: "ground_mat",
		Params: procgen.Params{"terrain": procgen.StageRef("ground")}})
	err := runExpectingError(t, d)
	if err == nil || !strings.Contains(err.Error(), "need more than 8") || !strings.Contains(err.Error(), "tile_") ||
		!strings.Contains(err.Error(), "snow") {
		t.Fatalf("err %v", err)
	}
	t.Logf("%v", err)
}

// runExpectingError runs d on the fake Poly Haven and returns its error.
func runExpectingError(t *testing.T, d *procgen.Doc) error {
	t.Helper()
	_, res := withFakePolyHaven(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	_, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: filepath.Join(res, "generated", d.Name), Workers: 4})
	return err
}

// A wall sheet on a palette ground (no union material) wears the smallest
// per-tile material holding its region's rock, at the rock's place there.
func TestPaletteRockMaterialForWalls(t *testing.T) {
	g := &TerrainMaterialArtifact{Palette: 10, Layers: []RegionMaterialLayer{
		{Role: "sand", Environments: []string{"a"}}, {Role: "rock", Environments: []string{"a"}},
		{Role: "sand", Environments: []string{"b"}}, {Role: "rock", Environments: []string{"b"}},
	}, Tiles: []RegionTileMaterial{
		{Material: "m8", Layers: []int{1, 2, 3, 4, 5, 6, 7, 8}},
		{Material: "far4", Layers: []int{3, 4}, Far: true},
		{Material: "m4", Layers: []int{3, 4, 9, 10}},
	}}
	env := &procgen.Env{Region: &procgen.RegionContext{Environment: "b"}}
	rock := rockLayerIndex(env, g)
	if rock != 3 {
		t.Fatalf("region b's rock is layer %d, want 3 (0-based)", rock)
	}
	tm, at := paletteRockMaterial(g, rock)
	if tm == nil || tm.Material != "m4" || at != 1 {
		t.Fatalf("got %+v at %d, want m4 at 1", tm, at)
	}
	if tm, _ := paletteRockMaterial(g, 7); tm == nil || tm.Material != "m8" {
		t.Fatalf("layer 8: %+v", tm)
	}
	if tm, _ := paletteRockMaterial(g, 11); tm != nil {
		t.Fatalf("a layer no tile holds: %+v", tm)
	}
}
