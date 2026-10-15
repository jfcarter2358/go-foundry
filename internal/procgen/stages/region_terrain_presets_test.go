package stages

// region_terrain_presets_test.go — the terrain stage's landform, edge and
// water parameters (terrain_landform.go) and the region terrain's water
// surfaces (region_terrain_water.go): defaults are off, presets that set
// them grow mountains that rise from their neighbours' level and seas and
// ponds placed as water.
//
// FOUNDRY_PRESETS_DUMP=<dir> writes the worlds' relief and biome pictures.

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/material"
	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

func TestLandformParamsDefaultOff(t *testing.T) {
	k := procgen.KindByName(TerrainKind)
	p := procgen.Resolve(k.Params, procgen.Params{})
	s := terrainSettings(&procgen.Env{Params: p})
	if s.Landform.Any() || s.Edge != (terrain.Edge{}) {
		t.Fatalf("defaults switch something on: %+v %+v", s.Landform, s.Edge)
	}
	if r := waterRule(p); r.HasLevel || r.Ponds {
		t.Fatalf("defaults ask for water: %+v", r)
	}
	for _, d := range landformParams() {
		if !d.Added {
			t.Errorf("%s is not Added: every existing terrain stage's hash would move", d.Name)
		}
	}
	// Every one maps.
	p = procgen.Resolve(k.Params, procgen.Params{
		"ridge_height": procgen.Float(300), "ridge_valleys": procgen.Float(0.5), "dune_height": procgen.Float(10),
		"dune_wind": procgen.Float(45), "coast": procgen.Enum("-z"), "sea_level": procgen.Float(-3), "cliff_height": procgen.Float(20),
		"volcanoes": procgen.Int(2), "volcano_vents": procgen.Spline([]float64{10, 20}), "lava_lobes": procgen.Int(3),
		"hummock_height": procgen.Float(1), "pond_cover": procgen.Float(0.2), "base_elevation": procgen.Float(4), "edge_rise": procgen.Float(300),
		"water": procgen.Bool(true), "water_level": procgen.Float(-3), "ponds": procgen.Bool(true),
	})
	s = terrainSettings(&procgen.Env{Params: p})
	lf := s.Landform
	if lf.Ridge.Height != 300 || lf.Ridge.Valleys != 0.5 || lf.Dunes.Height != 10 || lf.Dunes.Wind != 45 || lf.Coast.Side != "-z" ||
		lf.Coast.SeaLevel != -3 || lf.Coast.Cliff != 20 || lf.Volcano.Count != 2 || len(lf.Volcano.Vents) != 1 ||
		lf.Volcano.Vents[0] != [2]float64{10, 20} || lf.Volcano.Lobes != 3 || lf.Hummocks.Height != 1 || lf.Hummocks.Ponds != 0.2 ||
		s.Edge != (terrain.Edge{Level: 4, Rise: 300}) {
		t.Fatalf("mapped %+v, edge %+v", lf, s.Edge)
	}
	if r := waterRule(p); !r.HasLevel || r.Level != -3 || !r.Ponds {
		t.Fatalf("water %+v", r)
	}
}

// presetsDump writes a region terrain's relief and biomes to the dump
// folder, if one is set.
func presetsDump(t *testing.T, st *procgen.StageResult, name string) {
	dir := os.Getenv("FOUNDRY_PRESETS_DUMP")
	if dir == "" {
		return
	}
	os.MkdirAll(dir, 0o755)
	for _, f := range []string{terrain.ReliefFile, terrain.BiomesFile, terrain.HeightmapFile, terrain.MaskFile(terrain.MaskWater)} {
		data, err := os.ReadFile(st.File(f))
		if err != nil {
			continue
		}
		os.WriteFile(filepath.Join(dir, name+"_"+f), data, 0o644)
	}
}

// A mountain region with an edge rise beside a plain: across the band it
// stands at the plain's level, so the band is no escarpment; deep inside it
// is the full range.
func TestRegionTerrainEdgeRise(t *testing.T) {
	dir := t.TempDir()
	f, i := procgen.Float, procgen.Int
	flat := procgen.Params{"relief": f(3), "mesa_height": f(0), "mesa_tiers": i(0), "terrace": f(0), "droplets": f(0),
		"talus_iterations": i(10), "drainage_iterations": i(20), "canyons": procgen.List()}
	plain := writePreset(t, dir, "plain", flat)
	mtn := func(rise float64) string {
		p := flat.Clone()
		p["ridge_height"], p["ridge_scale"], p["ridge_octaves"] = f(220), f(600), i(4)
		p["edge_rise"], p["base_elevation"] = f(rise), f(0)
		return writePreset(t, dir, "alpine", p)
	}
	run := func(rise float64) (*procgen.StageResult, *terrain.Terrain) {
		d := worldDoc("edge", []procgen.Params{
			regionItem("plain", SavannahEnvironment, plain, -384, -256, 0, 256, 120),
			regionItem("alpine", "alpine", mtn(rise), 0, -256, 384, 256, 120),
		}, procgen.Params{"resolution": f(2), "tile_size": f(128), "warp_amount": f(10)}, nil)
		return runDoc(t, d)
	}
	stOff, off := run(0)
	stOn, on := run(200)
	presetsDump(t, stOff, "edge_off")
	presetsDump(t, stOn, "edge_on")
	band := func(tr *terrain.Terrain) (mean, steep float64) {
		g := tr.Grid
		n := 0
		for j := 1; j < g.N; j++ {
			for i := 1; i < g.N; i++ {
				if x := g.X(i); x < -40 || x > 40 {
					continue
				}
				mean += float64(g.At(i, j))
				n++
				dx := float64(g.At(i+1, j)-g.At(i-1, j)) / (2 * g.Cell)
				dz := float64(g.At(i, j+1)-g.At(i, j-1)) / (2 * g.Cell)
				steep = math.Max(steep, math.Atan(math.Hypot(dx, dz))*180/math.Pi)
			}
		}
		return mean / float64(n), steep
	}
	mOff, sOff := band(off)
	mOn, sOn := band(on)
	t.Logf("band: mean %.1f m, steepest %.0f° without the edge rise; %.1f m, %.0f° with it", mOff, sOff, mOn, sOn)
	if !(math.Abs(mOn) < 4 && sOn < sOff-15) {
		t.Errorf("with the edge rise the band is at %.1f m with slopes to %.0f° (without: %.1f m, %.0f°)", mOn, sOn, mOff, sOff)
	}
}

// A wetland region at a water level beside dry ground, with ponds: water
// surfaces placed per tile wearing the generated water material, the depth
// mask and the bodies in terrain.yaml.
func TestRegionTerrainWaterSurfaces(t *testing.T) {
	dir := t.TempDir()
	f, i, b := procgen.Float, procgen.Int, procgen.Bool
	wet := writePreset(t, dir, "wetland", procgen.Params{"base_height": f(-0.5), "relief": f(2.5), "frequency": f(0.01),
		"mesa_height": f(0), "mesa_tiers": i(0), "terrace": f(0), "droplets": f(0), "drainage": f(0), "talus_iterations": i(4),
		"canyons": procgen.List(), "water": b(true), "water_level": f(0), "ponds": b(true),
		"hummock_height": f(0.4), "hummock_spacing": f(6), "pond_cover": f(0.2), "pond_depth": f(1.2), "pond_scale": f(40)})
	dry := writePreset(t, dir, "dry", procgen.Params{"base_height": f(6), "relief": f(3), "mesa_height": f(0), "mesa_tiers": i(0),
		"terrace": f(0), "droplets": f(0), "talus_iterations": i(4), "canyons": procgen.List()})
	d := worldDoc("wet", []procgen.Params{
		regionItem("marsh", "wetland", wet, -128, -128, 0, 128, 60),
		regionItem("upland", SavannahEnvironment, dry, 0, -128, 128, 128, 60),
	}, procgen.Params{"resolution": f(2), "tile_size": f(64), "warp_amount": f(5)},
		procgen.Params{"cook": b(true), "cook_memory": i(1500)})
	st, tr := runDoc(t, d)
	presetsDump(t, st, "water")
	if tr.Meta.Water == nil || len(tr.Meta.Water.Bodies) == 0 {
		t.Fatal("no water in terrain.yaml")
	}
	kinds := map[string]int{}
	for _, wb := range tr.Meta.Water.Bodies {
		kinds[wb.Kind]++
		if wb.Region != "marsh" {
			t.Errorf("a %s body in region %s: only the marsh asks for water", wb.Kind, wb.Region)
		}
		if wb.Bounds[0] > 20 {
			t.Errorf("water at x %.0f..%.0f, east of the marsh", wb.Bounds[0], wb.Bounds[2])
		}
	}
	t.Logf("water bodies: %v (%+v)", kinds, tr.Meta.Water.Bodies[0])
	if kinds["sea"] != 1 {
		t.Errorf("%d level bodies, want the marsh's one", kinds["sea"])
	}
	wm, err := tr.Mask(terrain.MaskWater)
	if err != nil {
		t.Fatal(err)
	}
	wetSamples := 0
	for _, v := range wm {
		if v > 0 {
			wetSamples++
		}
	}
	if wetSamples == 0 {
		t.Fatal("the water mask is dry")
	}
	mat := "gen_wet_ground_water"
	var water []procgen.Placement
	for _, pl := range st.Placements {
		if strings.HasPrefix(pl.ID, "water_") {
			water = append(water, pl)
		}
	}
	if len(water) == 0 {
		t.Fatal("no water surfaces placed")
	}
	for _, pl := range water {
		if pl.Material != mat || pl.Collision != "none" || pl.Solid == nil || *pl.Solid || pl.Chunk != procgen.ChunkAuto {
			t.Errorf("%s: %+v", pl.ID, pl)
		}
		if pl.Position.X > 64 {
			t.Errorf("%s east of the marsh's tiles", pl.ID)
		}
		if _, err := os.Stat(st.File(pl.ID + ".fnm")); err != nil {
			t.Errorf("%s: %v", pl.ID, err)
		}
	}
	md := material.Get(mat)
	if md == nil {
		if data, err := os.ReadFile(filepath.Join(fileutil.ResourcesPath, material.MaterialsDirName, mat+material.MaterialExt)); err != nil {
			t.Fatalf("no water material: %v", err)
		} else if !strings.Contains(string(data), "effect: water") || !strings.Contains(string(data), "texture: water") {
			t.Fatalf("water material:\n%s", data)
		}
	}
}

// waterTileMesh covers every cell with a wet corner, at that corner's level,
// and the shore cells past them, and nothing else.
func TestWaterTileMeshCoversTheWetCells(t *testing.T) {
	g := terrain.NewGrid(16, 1)
	s := g.S()
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			g.H[j*s+i] = float32(i) - 6
		}
	}
	w := terrain.FindWater(g, func(int) int { return 0 }, []terrain.WaterRule{{HasLevel: true, Level: 0}})
	tp := tilePlan{count: 2, per: 8, step: 1, size: 8}
	cells := waterCells(g, w)
	area := 0.0
	for tj := 0; tj < 2; tj++ {
		for ti := 0; ti < 2; ti++ {
			b := waterTileMesh(g, tp, cells, ti, tj, 0.125)
			if b == nil {
				continue
			}
			m := b.Mesh()
			for k := 0; k+2 < len(m.Indices); k += 3 {
				p0, p1, p2 := m.Positions[m.Indices[k]], m.Positions[m.Indices[k+1]], m.Positions[m.Indices[k+2]]
				if p0[1] != 0 || p1[1] != 0 || p2[1] != 0 {
					t.Fatalf("a water vertex at %v, want the level 0", p0)
				}
				// Facing up: the normal (p1-p0)×(p2-p0) has +Y.
				cr := (p1[2]-p0[2])*(p2[0]-p0[0]) - (p1[0]-p0[0])*(p2[2]-p0[2])
				if cr <= 0 {
					t.Fatalf("a triangle facing down (%v)", cr)
				}
				area += float64(cr) / 2
			}
		}
	}
	// Samples x < 6 are wet (h < 0): cells 0..5 have a wet corner (cell 5's
	// west one), and the surface runs waterShoreCells more under the bank:
	// 8 cells across, 16 down.
	if want := float64((6 + waterShoreCells) * 16); area != want {
		t.Fatalf("water area %v m², want %v", area, want)
	}
}
