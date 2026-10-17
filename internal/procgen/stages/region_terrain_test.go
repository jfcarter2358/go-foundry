package stages

// region_terrain_test.go — the region_terrain stage and the phase-2 GATES
// (docs/regions/SPEC.md):
//
//	(a) one region whose footprint is a classic terrain's square, anchored
//	    at the origin, regenerates the classic terrain stage's files byte
//	    for byte (heights, masks, albedo, relief, the tiles' meshes);
//	(b) a river crosses a region seam: a canyon in region A drawn to its
//	    edge with low savannah-like region B keeps a high-flow channel
//	    running on into B, downhill;
//	(c) tiled erosion: editing a far region's preset leaves a distant
//	    tile's heights byte-identical;
//
// and the seam is smooth: no slope in the band steeper than the steeper
// region's own.

import (
	"context"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

// regionItem is a regions-stage list item: a rectangular footprint.
func regionItem(id, env, generator string, x0, z0, x1, z1, transition float64) procgen.Params {
	return procgen.Params{
		"id": procgen.String(id), "environment": procgen.String(env), "generator": procgen.String(generator),
		"transition": procgen.Float(transition),
		"footprint":  procgen.Spline([]float64{x0, z0}, []float64{x1, z0}, []float64{x1, z1}, []float64{x0, z1}),
	}
}

// writePreset writes a one-stage preset (its terrain) and returns its path.
func writePreset(t *testing.T, dir, name string, params procgen.Params) string {
	t.Helper()
	p := filepath.Join(dir, name+procgen.Ext)
	d := &procgen.Doc{Name: name, Seed: 5, Stages: []procgen.Stage{{Kind: TerrainKind, ID: "ground", Params: params}}}
	if err := procgen.WriteDoc(p, d); err != nil {
		t.Fatal(err)
	}
	return p
}

// worldDoc is a level generation doc over the given regions.
func worldDoc(name string, items []procgen.Params, grid, ground procgen.Params) *procgen.Doc {
	rp := procgen.Params{"regions": procgen.List(items...)}
	for k, v := range grid {
		rp[k] = v
	}
	gp := procgen.Params{"regions": procgen.StageRef("regions"), "cook": procgen.Bool(false)}
	for k, v := range ground {
		gp[k] = v
	}
	return &procgen.Doc{Name: name, Seed: 11, Stages: []procgen.Stage{
		{Kind: RegionsKind, ID: "regions", Params: rp},
		{Kind: RegionTerrainKind, ID: "ground", Params: gp},
	}}
}

func runDoc(t *testing.T, d *procgen.Doc) (*procgen.StageResult, *terrain.Terrain) {
	t.Helper()
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: t.TempDir(), Workers: 8})
	if err != nil {
		t.Fatal(err)
	}
	st := res.Stage("ground")
	return st, st.Artifact.(*terrain.Terrain)
}

// GATE (a).
func TestGateOneRegionIsTheClassicTerrain(t *testing.T) {
	classic := desertGroundDoc()
	cs, _ := runDoc(t, classic)
	g := classic.Stages[0]
	k := procgen.KindByName(TerrainKind)
	classicSeed := procgen.StageSeed(classic.Seed, g.ID, k.Params, procgen.Resolve(k.Params, g.Params))
	prev := regionProviderSeed
	regionProviderSeed = func(int64, string, string, []procgen.ParamDef, procgen.Params) uint64 { return classicSeed }
	t.Cleanup(func() { regionProviderSeed = prev })

	preset := writePreset(t, t.TempDir(), "desert_ground", g.Params)
	item := regionItem("only", DesertCanyonEnvironment, preset, -128, -128, 128, 128, 150)
	item["anchored"], item["anchor"] = procgen.Bool(true), procgen.Vec2(0, 0)
	d := worldDoc("one", []procgen.Params{item},
		procgen.Params{"resolution": procgen.Float(1), "tile_size": procgen.Float(128)},
		procgen.Params{"texture": g.Params["texture"], "uv_scale": g.Params["uv_scale"], "material": g.Params["material"]})
	rs, rt := runDoc(t, d)

	var files []string
	for _, f := range []string{terrain.HeightmapFile, terrain.AlbedoFile, terrain.ReliefFile} {
		files = append(files, f)
	}
	for _, m := range terrain.MaskNames {
		files = append(files, terrain.MaskFile(m))
	}
	for _, f := range rt.Meta.Tiles.Files {
		files = append(files, f)
	}
	if len(rt.Meta.Tiles.Files) != 4 {
		t.Fatalf("%d tiles, want 4", len(rt.Meta.Tiles.Files))
	}
	for _, f := range files {
		a, err := os.ReadFile(cs.File(f))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(rs.File(f))
		if err != nil {
			t.Fatal(err)
		}
		if string(a) != string(b) {
			t.Errorf("%s differs from the classic terrain stage's", f)
		}
	}
	// The union grid is the classic grid, and the biome is everywhere.
	if g := rt.Grid; g.N != 256 || g.MinX != -128 || g.MinZ != -128 || g.Cell != 1 {
		t.Errorf("union grid %d cells from (%g, %g) at %g", g.N, g.MinX, g.MinZ, g.Cell)
	}
	if rt.Meta.Biomes == nil || len(rt.Meta.Biomes.Regions) != 1 || rt.Biomes()[0] != DesertCanyonEnvironment {
		t.Fatalf("biomes %+v", rt.Meta.Biomes)
	}
	bm, err := rt.Mask(terrain.BiomeMask(DesertCanyonEnvironment))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range bm {
		if v != 1 {
			t.Fatalf("a one-region terrain's biome weight is %v somewhere", v)
		}
	}
}

// seamPresets are gate (b)'s presets: A a canyon drawn east to its edge in
// low-relief ground; B a lower, gentle savannah plain.
func seamPresets(t *testing.T, dir string, bRelief float64) (a, b string) {
	f, i, e := procgen.Float, procgen.Int, procgen.Enum
	a = writePreset(t, dir, "seam_canyon", procgen.Params{
		"relief": f(6), "mesa_height": f(0), "mesa_tiers": i(0), "droplets": f(0.4), "talus_iterations": i(30),
		"canyons": procgen.List(procgen.Params{
			"path":    procgen.Spline([]float64{-150, 10, 40, 22}, []float64{20, -10, 50, 26}, []float64{192, 0, 55, 30}),
			"profile": e("u"), "floor": f(0.3), "meander": f(8), "tributaries": i(2), "tributary_length": f(80),
		})})
	b = writePreset(t, dir, "seam_plain", procgen.Params{
		"base_height": f(-40), "relief": f(bRelief), "frequency": f(0.002), "mesa_height": f(0), "mesa_tiers": i(0),
		"terrace": f(0), "resistance": f(0.15), "droplets": f(0.4), "talus_angle": f(28), "talus_iterations": i(30),
		"strata": procgen.List(procgen.Params{"thickness": f(20), "hardness": f(0.2), "color": procgen.Color(0.7, 0.62, 0.38)}),
	})
	return a, b
}

func seamDoc(t *testing.T) *procgen.Doc {
	a, b := seamPresets(t, t.TempDir(), 5)
	return worldDoc("seam", []procgen.Params{
		regionItem("canyon", DesertCanyonEnvironment, a, -384, -192, 0, 192, 150),
		regionItem("plain", SavannahEnvironment, b, 0, -192, 384, 192, 150),
	}, procgen.Params{"resolution": procgen.Float(2), "tile_size": procgen.Float(128), "warp_amount": procgen.Float(25)}, nil)
}

// dumpStage copies a run's pictures to FOUNDRY_REGION_TERRAIN_DUMP/<name>_*
// for a person to look at, plus the relief shaded in the biome colours.
func dumpStage(t *testing.T, st *procgen.StageResult, tr *terrain.Terrain, name string) {
	dir := os.Getenv("FOUNDRY_REGION_TERRAIN_DUMP")
	if dir == "" {
		return
	}
	os.MkdirAll(dir, 0o755)
	for _, f := range []string{terrain.ReliefFile, terrain.BiomesFile, terrain.MaskFile(terrain.MaskFlow),
		terrain.MaskFile(terrain.MaskSlope), terrain.MaskFile(terrain.MaskCliff), terrain.AlbedoFile} {
		if data, err := os.ReadFile(st.File(f)); err == nil {
			os.WriteFile(filepath.Join(dir, name+"_"+f), data, 0o644)
		}
	}
	bio, err := readRGB(st.File(terrain.BiomesFile))
	if err != nil {
		t.Fatal(err)
	}
	sz := tr.Grid.S()
	png, err := terrain.EncodeRGBA(sz, sz, terrain.Relief(tr.Grid, bio))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, name+"_biome_relief.png"), png, 0o644)
	t.Logf("pictures in %s/%s_*", dir, name)
}

// GATE (b), and the seam's smoothness.
func TestGateRiverCrossesTheSeam(t *testing.T) {
	st, tr := runDoc(t, seamDoc(t))
	dumpStage(t, st, tr, "seam")
	g := tr.Grid
	flow, err := tr.Mask(terrain.MaskFlow)
	if err != nil {
		t.Fatal(err)
	}
	wb, err := tr.Mask(terrain.BiomeMask(SavannahEnvironment))
	if err != nil {
		t.Fatal(err)
	}
	// From the canyon floor 100 m before its drawn end, follow the water:
	// once it is in the channel (the flow first passes minFlow, a few
	// samples off the carved centreline) the channel must carry on, high
	// flow and downhill, until it is 150 m into the savannah.
	main := tr.Meta.Paths[0]
	start := main.Points[0]
	for _, q := range main.Points {
		if q[0] <= -100 {
			start = q
		}
	}
	path := g.DrainPath(start[0], start[1], 4000)
	const minFlow = 0.45
	inB, lastH := 0.0, float32(math.Inf(1))
	reached, inChannel := false, false
	for n, k := range path {
		x := g.X(k % g.S())
		h := g.H[k]
		if os.Getenv("FOUNDRY_RIVER_LOG") != "" && n%5 == 0 {
			t.Logf("step %d at (%.0f, %.0f): h %.2f flow %.2f savannah %.2f", n, x, g.Z(k/g.S()), h, flow[k], wb[k])
		}
		if n > 0 && h > lastH+0.5 {
			t.Errorf("the river climbs %.2f m at x %.0f", h-lastH, x)
		}
		lastH = h
		if !inChannel {
			inChannel = flow[k] >= minFlow
			if !inChannel && x > -60 {
				t.Fatalf("the water from the canyon floor reaches x %.0f without finding the channel", x)
			}
			continue
		}
		if flow[k] < minFlow {
			t.Fatalf("the channel's flow falls to %.2f at x %.0f (%d steps down): it does not carry on", flow[k], x, n)
		}
		if wb[k] >= 0.5 {
			inB += g.Cell
		}
		if inB >= 150 {
			reached = true
			break
		}
	}
	if !reached {
		t.Fatalf("the river ran %.0f m in the savannah before it ended", inB)
	}
	if h0, h1 := g.Sample(start[0], start[1]), lastH; h1 >= h0 {
		t.Errorf("the river ends at %.1f m, above where it started (%.1f m)", h1, h0)
	}

}

// The seam is smooth: across the band between the built-in desert canyon
// (mesas, cliffs) and savannah (rolling plain) presets — the real pair — no
// slope is steeper than the desert's own steepest: blending makes no
// cliffs. 2 m, 1.2 x 0.6 km.
func TestSeamMakesNoCliffs(t *testing.T) {
	d := worldDoc("pair", []procgen.Params{
		regionItem("canyon", DesertCanyonEnvironment, "", -600, -300, 0, 300, 200),
		regionItem("plain", SavannahEnvironment, "", 0, -300, 600, 300, 200),
	}, procgen.Params{"resolution": procgen.Float(2), "tile_size": procgen.Float(128)}, nil)
	st, tr := runDoc(t, d)
	dumpStage(t, st, tr, "pair")
	g := tr.Grid
	slope := func(i, j int) float64 {
		gx := float64(g.At(i+1, j)-g.At(i-1, j)) / (2 * g.Cell)
		gz := float64(g.At(i, j+1)-g.At(i, j-1)) / (2 * g.Cell)
		return math.Atan(math.Hypot(gx, gz)) * 180 / math.Pi
	}
	band, desert, plain := 0.0, 0.0, 0.0
	var at [2]float64
	var bandAll, desertAll []float64
	for j := 1; j < g.N; j++ {
		for i := 1; i < g.N; i++ {
			x, z := g.X(i), g.Z(j)
			if math.Abs(z) > 290 {
				continue
			}
			sl := slope(i, j)
			switch {
			case math.Abs(x) < 100+2*40: // the band and the edge meander
				if sl > band {
					band, at = sl, [2]float64{x, z}
				}
				if x < 0 {
					bandAll = append(bandAll, sl)
				}
			case x < 0 && x > -590:
				desert = math.Max(desert, sl)
				desertAll = append(desertAll, sl)
			case x > 0 && x < 590:
				plain = math.Max(plain, sl)
			}
		}
	}
	pct := func(v []float64, q float64) float64 {
		sort.Float64s(v)
		return v[int(q*float64(len(v)-1))]
	}
	bp, dp := pct(bandAll, 0.99), pct(desertAll, 0.99)
	t.Logf("steepest: band %.1f° at %v, desert %.1f°, savannah %.1f°; 99th percentile: band (desert side) %.1f°, desert %.1f°",
		band, at, desert, plain, bp, dp)
	// Both maxima sit at the talus cap (the cliff angle, 80°) plus the
	// detail's bumps, so they are compared within a degree. The 99th
	// percentiles sit at the cap too: since phase 6's savannah (45 m of
	// relief, drained) they agree to a tenth of a degree, so within a
	// quarter of one.
	if band > desert+1 {
		t.Errorf("the band's steepest slope %.1f° is steeper than the desert's own %.1f°: blending made a cliff", band, desert)
	}
	if bp > dp+0.25 {
		t.Errorf("the band's 99th-percentile slope %.1f° is steeper than the desert's %.1f°", bp, dp)
	}
}

// GATE (c).
func TestGateTiledErosionLeavesDistantTilesAlone(t *testing.T) {
	run := func(cRelief float64) *terrain.Terrain {
		dir := t.TempDir()
		a, b := seamPresets(t, dir, 5)
		os.MkdirAll(filepath.Join(dir, "c"), 0o755)
		_, c := seamPresets(t, filepath.Join(dir, "c"), cRelief)
		d := worldDoc("tiled", []procgen.Params{
			// The footprints tile the union: no gap, whose fill (phase 1:
			// the nearest regions share an unclaimed sample) would hand the
			// far region a share far from its band.
			regionItem("canyon", DesertCanyonEnvironment, a, -384, -384, -128, 384, 150),
			regionItem("plain", SavannahEnvironment, b, -128, -384, 128, 384, 150),
			regionItem("far", SavannahEnvironment, c, 128, -384, 384, 384, 150),
		}, procgen.Params{"resolution": procgen.Float(2), "tile_size": procgen.Float(128), "warp_amount": procgen.Float(25),
			"erosion": procgen.Enum("tiled"), "margin": procgen.Float(32)},
			procgen.Params{"window": procgen.Int(1)})
		_, tr := runDoc(t, d)
		return tr
	}
	a, b := run(5), run(14)
	if a.Meta.Biomes.Erosion != terrain.ErosionTiled || a.Meta.Biomes.Margin != 32 {
		t.Fatalf("biomes %+v", a.Meta.Biomes)
	}
	g := a.Grid
	tile := a.Meta.Tiles.Size
	same, changed := 0, 0
	for j := 0; j < g.S(); j++ {
		for i := 0; i < g.S(); i++ {
			k := j*g.S() + i
			x := g.X(i)
			// Tile column 0: x in [-384, -256].
			if x <= g.MinX+tile {
				if a.Grid.H[k] != b.Grid.H[k] {
					t.Fatalf("tile column 0 sample at (%.0f, %.0f): %v then %v", x, g.Z(j), a.Grid.H[k], b.Grid.H[k])
				}
				same++
			}
			if x > 200 && a.Grid.H[k] != b.Grid.H[k] {
				changed++
			}
		}
	}
	if same == 0 || changed == 0 {
		t.Errorf("%d samples compared, %d changed near the edited region: the test does not test", same, changed)
	}
}

func TestRegionTerrainOutputsAndLoad(t *testing.T) {
	d := seamDoc(t)
	st, art := runDoc(t, d)
	for _, f := range []string{terrain.MetaFile, terrain.HeightmapFile, terrain.BiomesFile,
		terrain.MaskFile(terrain.BiomeMask(DesertCanyonEnvironment)), terrain.MaskFile(terrain.BiomeMask(SavannahEnvironment)),
		"tile_0_0.glb", "tile_5_5.glb"} {
		if _, err := os.Stat(st.File(f)); err != nil {
			t.Errorf("no %s: %v", f, err)
		}
	}
	lt, err := loadTerrain(st)
	if err != nil {
		t.Fatal(err)
	}
	tr := lt.(*terrain.Terrain)
	if tr.Meta.Biomes == nil || len(tr.Meta.Biomes.Regions) != 2 || tr.Meta.Biomes.Lead == "" {
		t.Fatalf("biomes %+v", tr.Meta.Biomes)
	}
	if b := tr.Meta.Bounds; b.MinX != -384 || b.MaxX != 384 || b.MinZ != -384 || b.MaxZ != 384 {
		t.Errorf("bounds %+v, want the padded 768 m square", b)
	}
	for _, q := range []struct {
		x, z float64
		env  string
		want float32
	}{{-300, 0, DesertCanyonEnvironment, 1}, {300, 0, SavannahEnvironment, 1}, {-300, 0, SavannahEnvironment, 0}} {
		v, err := tr.BiomeAt(q.env, q.x, q.z)
		if err != nil || math.Abs(float64(v-q.want)) > 0.01 {
			t.Errorf("%s at (%g, %g): %v (%v), want %v", q.env, q.x, q.z, v, err, q.want)
		}
	}
	if w, _ := tr.BiomeAt(DesertCanyonEnvironment, 0, 0); w < 0.2 || w > 0.8 {
		t.Errorf("on the seam the canyon's share is %v", w)
	}
	if h, a := tr.HeightAt(10, 20), art.HeightAt(10, 20); math.Abs(float64(h-a)) > 0.01 {
		t.Errorf("loaded height %v, ran %v", h, a)
	}
	// Downstream stages take it as a terrain.
	if !strings.Contains(strings.Join(TerrainSources, " "), RegionTerrainKind) {
		t.Error("region_terrain is not a terrain source")
	}
	// A rerun skips; editing a preset reruns.
	res2, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: filepath.Dir(st.Dir)})
	if err != nil || len(res2.Ran) != 0 {
		t.Fatalf("rerun ran %v (%v)", res2.Ran, err)
	}
	gen := d.Stages[0].Params.Items("regions")[1].Str("generator")
	pd, _ := procgen.ReadDoc(gen)
	pd.Stages[0].Params["relief"] = procgen.Float(7)
	procgen.WriteDoc(gen, pd)
	res3, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: filepath.Dir(st.Dir)})
	if err != nil || len(res3.Ran) != 1 || res3.Ran[0] != "ground" {
		t.Errorf("after editing a preset the run ran %v (%v), want the terrain", res3.Ran, err)
	}
}

func TestRegionTerrainPreview(t *testing.T) {
	d := seamDoc(t)
	d.Stages[1].Params["preview_cells"] = procgen.Int(96)
	p, err := procgen.Preview(context.Background(), d, "ground", procgen.NewPreviewCache(4), procgen.PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, im := range p.Images {
		names[im.Name] = true
		if im.W != 97 {
			t.Errorf("%s is %d wide, want 97", im.Name, im.W)
		}
	}
	for _, n := range []string{"Relief", "Biome relief", RegionsOverlayName, "Flow", terrain.BiomeMask(SavannahEnvironment)} {
		if !names[n] {
			t.Errorf("no %s layer (have %v)", n, names)
		}
	}
	outlines := 0
	for _, l := range p.Lines {
		if l.Closed {
			outlines++
		}
	}
	if outlines != 2 || len(p.Meshes) == 0 {
		t.Errorf("%d outlines, %d meshes", outlines, len(p.Meshes))
	}
	if tr, ok := p.Artifact.(*terrain.Terrain); !ok || len(tr.Biomes()) != 2 {
		t.Errorf("artifact %T", p.Artifact)
	}
}

func TestLevelWorldGrowsARegionTerrain(t *testing.T) {
	d := LevelWorld("arena_world")
	if len(d.Stages) != 3 || d.Stages[1].Kind != RegionTerrainKind || d.Stages[1].Params.Str("regions") != "regions" {
		t.Fatalf("template stages %+v", d.Stages)
	}
	// Its tiles wear the region material's (phase 3).
	if d.Stages[1].Params.Str("material") != "gen_arena_world_ground_mat" || d.Stages[2].Kind != RegionMaterialKind ||
		d.Stages[2].Params.Str("terrain") != "ground" {
		t.Errorf("template material slot %+v", d.Stages[1:])
	}
	// A preset's terrain is stood in for by region_terrain.
	if k := procgen.KindByName(RegionTerrainKind); len(k.SharedFor) != 1 || k.SharedFor[0] != TerrainKind {
		t.Errorf("SharedFor %v", k.SharedFor)
	}
}

// readRGB decodes a PNG to RGB bytes.
func readRGB(path string) ([]uint8, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	out := make([]uint8, 0, 3*b.Dx()*b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bb, _ := img.At(x, y).RGBA()
			out = append(out, uint8(r>>8), uint8(g>>8), uint8(bb>>8))
		}
	}
	return out, nil
}

// TestRegionTerrainBench times a square kilometre of two regions — the
// built-in desert canyon west of x = 0, the savannah east — uncooked:
//
//	FOUNDRY_REGION_BENCH=<out dir> FOUNDRY_REGION_BENCH_RES=0.5 \
//	FOUNDRY_REGION_BENCH_EROSION=global|tiled \
//	    go test ./internal/procgen/stages -run TestRegionTerrainBench -v -timeout 30m
//
// Wrap it in /usr/bin/time -v for the memory peak (docs/regions/PHASE_2.md
// has the numbers). FOUNDRY_REGION_TERRAIN_DUMP also writes the pictures.
func TestRegionTerrainBench(t *testing.T) {
	out := os.Getenv("FOUNDRY_REGION_BENCH")
	if out == "" {
		t.Skip("set FOUNDRY_REGION_BENCH to an output folder")
	}
	res := 1.0
	if v := os.Getenv("FOUNDRY_REGION_BENCH_RES"); v != "" {
		fmt.Sscanf(v, "%g", &res)
	}
	erosion := "global"
	if v := os.Getenv("FOUNDRY_REGION_BENCH_EROSION"); v != "" {
		erosion = v
	}
	d := worldDoc("bench", []procgen.Params{
		regionItem("canyon", DesertCanyonEnvironment, "", -512, -512, 0, 512, 200),
		regionItem("plain", SavannahEnvironment, "", 0, -512, 512, 512, 200),
	}, procgen.Params{"resolution": procgen.Float(res), "tile_size": procgen.Float(128), "erosion": procgen.Enum(erosion)}, nil)
	t0 := time.Now()
	r, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: out, Force: true, Workers: 8,
		Log: func(l string) { t.Log(l) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%g m, %s erosion: %s", res, erosion, time.Since(t0).Round(time.Millisecond))
	st := r.Stage("ground")
	dumpStage(t, st, st.Artifact.(*terrain.Terrain), fmt.Sprintf("bench_%g_%s", res, erosion))
}
