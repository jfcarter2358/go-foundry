package stages

// region_terrain_run.go — the region_terrain stage's run, preview and load
// (region_terrain.go has the stage and how the union is planned).
//
// The run is the classic terrain stage's (terrain_run.go) over the union:
// the same images, the same terrain.yaml (strata: the LEAD region's — the
// one owning most of the union; every region's own list is under biomes:),
// the same tiles on one world tile grid cooked on one quantisation step,
// the same backdrop (terrain_far.go, fed a terrain.UnionFar: each region's
// own country continued past the edge, blended by the weights at the
// nearest edge sample). Plus, per environment, mask_biome_<env>.png — the
// sum of its regions' shares, listed in terrain.yaml's masks so a
// downstream stage asks Terrain.Mask("biome_<env>") — and biomes.png, the
// environments' colours mixed by weight.
//
//	terrain.yaml   (as the terrain stage's, plus)
//	  biomes:
//	    overlay: biomes.png
//	    environments: [{name: desert_canyon, color: [0.86, 0.46, 0.24], mask: biome_desert_canyon}, ...]
//	    regions:
//	      - {key: canyon, environment: desert_canyon, preset: built-in desert_canyon, stage: ground,
//	         seed: ..., anchor: [-300, 0], bounds: [-600, -300, 0, 300], priority: 0, transition: 200,
//	         strata: [...]}
//	    erosion: global          # or tiled, with window / margin (metres)
//	    lead: canyon
//	    extent: footprints
//
// The preview runs the same union at preview_cells samples a side (the
// providers' Settings.Scaled: coarser drainage, fewer droplets), always
// with global erosion, and shows the relief with the biome overlay, the
// masks, the carved canyons, the tile grid and the region outlines.

import (
	"fmt"
	"time"

	"foundry/internal/procgen"
	"foundry/internal/procgen/regions"
	"foundry/internal/procgen/terrain"

	"gopkg.in/yaml.v2"
)

// regionTerrainInputs is the run's or preview's regions set and the regions
// stage's params.
func regionTerrainInputs(env *procgen.Env, upstream func(id string) (any, error)) (*regions.Set, procgen.Params, error) {
	ref := env.Params.Str("regions")
	st, gp, err := regionsStageParams(env.Doc, ref)
	if err != nil {
		return nil, nil, err
	}
	if upstream != nil {
		if a, err := upstream(ref); err == nil {
			if set, ok := a.(*regions.Set); ok && set != nil {
				return set, gp, nil
			}
		}
	}
	// No artifact (a preview whose regions stage failed): rebuild it.
	set, err := regionSet(gp, regionsSeed(env.Doc, st, gp))
	return set, gp, err
}

func runRegionTerrain(rc *procgen.RunContext) error {
	p := rc.Params
	set, gp, err := regionTerrainInputs(&rc.Env, rc.UpstreamArtifact)
	if err != nil {
		return err
	}
	plan, err := planUnion(rc.Doc, set, gp, p, rc.Seed)
	if err != nil {
		return err
	}
	// No material named: the region_material that reads this terrain's
	// (the tiles and the backdrop wear what it will write).
	if m, far := regionTerrainAutoMaterial(rc.Doc, rc.Stage, p); m != "" {
		p = p.Clone()
		rc.Params = p
		p["material"] = procgen.String(m)
		if far != "" {
			p["far_material"] = procgen.String(far)
		}
		rc.Logf("tiles wear %s (the region_material reading this terrain)", m)
	}
	u := plan.u
	for i, pv := range u.Providers {
		rc.Logf("region %s: %s (%s, stage %s), seed %d, anchor (%.1f, %.1f)", pv.Name, set.Regions[i].Environment,
			plan.pres[i].from, plan.pres[i].stage.ID, pv.Settings.Seed, pv.Anchor[0], pv.Anchor[1])
	}
	cook := p.Bool("cook")
	share := float32(1)
	if cook {
		share = 0.45
	}
	if u.Erosion == terrain.ErosionTiled {
		// Windows whose inputs did not change come back from last run
		// (region_terrain_incremental.go).
		u.Cache = erosionCache{rc}
	}
	t0 := time.Now()
	res, err := terrain.GenerateUnion(u, &rc.Env, func(_ terrain.Phase, done float32) { rc.Progress(done * share) })
	if err != nil {
		return err
	}
	if u.Cache != nil {
		rc.Logf("erosion windows: %d eroded, %d reused from the last run", res.WindowsEroded, res.WindowsCached)
	}
	g := res.Grid
	lo, hi := g.Range()
	erosion := u.Erosion
	if erosion != terrain.ErosionTiled {
		erosion = terrain.ErosionGlobal
	}
	rc.Logf("union %.0f m square from (%.0f, %.0f) at %g m: %d², %d regions, heights %.1f .. %.1f m, %d droplets, %s erosion, lead %s, %s",
		g.Size(), g.MinX, g.MinZ, g.Cell, g.S(), set.Len(), lo, hi, res.Droplets, erosion, set.Regions[res.Lead].Key,
		time.Since(t0).Round(time.Millisecond))
	for _, ph := range []terrain.Phase{terrain.PhaseWeights, terrain.PhaseShape, terrain.PhaseCanyons, terrain.PhaseErosion,
		terrain.PhaseDrainage, terrain.PhaseStrata, terrain.PhaseDroplets, terrain.PhaseThermal, terrain.PhaseDetail, terrain.PhaseMasks} {
		if d, ok := res.Timings[ph]; ok {
			rc.Logf("  %-9s %s", ph, d.Round(time.Millisecond))
		}
	}

	lead := &u.Providers[res.Lead].Settings
	meta := terrain.NewMeta(lead, res)
	envs, planes, rgb := biomeLayers(set, res)
	for e, name := range envs {
		mn := terrain.BiomeMask(name)
		res.Masks[mn] = planes[e]
		meta.Masks = append(meta.Masks, terrain.MaskMeta{Name: mn, File: terrain.MaskFile(mn), Encoding: "unit",
			Doc: "the " + name + " environment's share (its regions' weights summed), 0..1"})
	}
	meta.Biomes = biomesMeta(plan, res, envs, p)
	// Standing water, when a region's preset asks (region_terrain_water.go).
	water := regionWater(plan, res)
	if water != nil && !water.Wet() {
		water = nil
	}
	var waterMask []float32
	if water != nil {
		waterMask = terrain.WaterMaskValues(g, water)
		res.Masks[terrain.MaskWater] = waterMask
		meta.Masks = append(meta.Masks, terrain.MaskMeta{Name: terrain.MaskWater, File: terrain.MaskFile(terrain.MaskWater), Encoding: "unit",
			Doc: fmt.Sprintf("standing water's depth over %g m, 0..1 (0: dry)", terrain.WaterMaskDepth)})
		meta.Water = waterMeta(plan, water)
	}

	t1 := time.Now()
	files, err := terrain.Encode(res, meta, &rc.Env)
	if err != nil {
		return err
	}
	sz := g.S()
	for e, name := range envs {
		b, err := terrain.EncodeGray8(sz, sz, planes[e])
		if err != nil {
			return err
		}
		files[terrain.MaskFile(terrain.BiomeMask(name))] = b
	}
	if files[terrain.BiomesFile], err = terrain.EncodeRGB(sz, sz, rgb); err != nil {
		return err
	}
	if waterMask != nil {
		if files[terrain.MaskFile(terrain.MaskWater)], err = terrain.EncodeGray8(sz, sz, waterMask); err != nil {
			return err
		}
	}
	rgb = nil
	for _, name := range sortedKeys(files) {
		if err := rc.WriteFile(name, files[name]); err != nil {
			return err
		}
	}
	files = nil
	rc.Logf("images written in %s", time.Since(t1).Round(time.Millisecond))

	// Tiles: the classic stage's, on the union's grid.
	tparams := p.Clone()
	tparams["tile_size"] = procgen.Float(plan.tile)
	tp := planTiles(tparams, g)
	tp.reuse = true
	if erosion == terrain.ErosionTiled {
		// A far edit must not move every tile's base (file comment of
		// region_terrain_incremental.go).
		stableTileBase(&tp, g)
	}
	tris := tileTriangles(tp)
	meta.Tiles = terrain.TilesMeta{Size: tp.size, Count: tp.count, Base: tp.base, Triangles: tris}
	if cook {
		meta.Tiles.Step = tp.quant
	}
	ext := ".glb"
	if cook {
		ext = ".fnm"
	}
	for tj := 0; tj < tp.count; tj++ {
		for ti := 0; ti < tp.count; ti++ {
			meta.Tiles.Files = append(meta.Tiles.Files, tileName(ti, tj)+ext)
		}
	}
	if err := writeTiles(rc, g, tp, tris, cook, share); err != nil {
		return err
	}
	waterMat := ""
	if water != nil {
		if waterMat, err = writeWater(rc, g, tp, water, cook); err != nil {
			return err
		}
	}
	if err := writeFarWith(rc, g, &meta, tp, cook, func(cell, blend, reach float64) farHeights {
		return terrain.NewUnionFar(u, res, cell, blend, reach)
	}); err != nil {
		return err
	}
	if water != nil && meta.Far != nil && cook {
		// The sea runs on over the backdrop (region_terrain_water_far.go).
		coast := func(r int) bool { c := plan.pres[r].params.Str("coast"); return c != "" && c != "off" }
		if err := writeFarWater(rc, g, meta.Far, water, waterMat, coast); err != nil {
			return err
		}
	}
	rc.Artifact = terrain.FromResult(res, meta, rc.Dir)
	data, err := yaml.Marshal(meta)
	if err != nil {
		return err
	}
	return rc.WriteFile(terrain.MetaFile, data)
}

// ---------- preview ----------

func previewRegionTerrain(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
	p := pc.Params
	set, gp, err := regionTerrainInputs(&pc.Env, func(id string) (any, error) {
		if up := pc.Upstream(id); up != nil {
			return up.Artifact, nil
		}
		return nil, fmt.Errorf("no preview of %s", id)
	})
	if err != nil {
		return nil, err
	}
	plan, err := planUnion(pc.Doc, set, gp, p, pc.Seed)
	if err != nil {
		return nil, err
	}
	u := plan.u
	full := u.N
	// The fast mode: every provider scaled to preview_cells a side (the
	// same landforms on a coarser grid, fewer droplets and talus rounds),
	// erosion global.
	if maxCells := p.Int("preview_cells"); u.N > maxCells {
		for i := range u.Providers {
			u.Providers[i].Settings = u.Providers[i].Settings.Scaled(maxCells)
		}
		u.Cell, u.N = u.Providers[0].Settings.Cell, maxCells
	}
	u.Erosion = terrain.ErosionGlobal
	t0 := time.Now()
	res, err := terrain.GenerateUnion(u, &pc.Env, func(_ terrain.Phase, done float32) { pc.Progress(done) })
	if err != nil {
		return nil, err
	}
	g := res.Grid
	sz := g.S()
	b := procgen.Rect{MinX: g.MinX, MinZ: g.MinZ, MaxX: g.MinX + g.Size(), MaxZ: g.MinZ + g.Size()}
	lo, hi := g.Range()
	meta := terrain.NewMeta(&u.Providers[res.Lead].Settings, res)
	envs, planes, rgb := biomeLayers(set, res)
	for e, name := range envs {
		mn := terrain.BiomeMask(name)
		res.Masks[mn] = planes[e]
		meta.Masks = append(meta.Masks, terrain.MaskMeta{Name: mn, File: terrain.MaskFile(mn), Encoding: "unit"})
	}
	meta.Biomes = biomesMeta(plan, res, envs, p)
	// Standing water, when a region's preset asks (region_terrain_water.go).
	water := regionWater(plan, res)
	if water != nil && !water.Wet() {
		water = nil
	}
	var waterMask []float32
	if water != nil {
		waterMask = terrain.WaterMaskValues(g, water)
		res.Masks[terrain.MaskWater] = waterMask
		meta.Masks = append(meta.Masks, terrain.MaskMeta{Name: terrain.MaskWater, File: terrain.MaskFile(terrain.MaskWater), Encoding: "unit",
			Doc: fmt.Sprintf("standing water's depth over %g m, 0..1 (0: dry)", terrain.WaterMaskDepth)})
		meta.Water = waterMeta(plan, water)
	}
	out := &procgen.PreviewData{Bounds: b, Artifact: terrain.FromResult(res, meta, "")}

	relief := procgen.ImageLayer{Name: "Relief", W: sz, H: sz, RGBA: terrain.Relief(g, res.Albedo), Values: g.H, Unit: "m", Bounds: b}
	height := procgen.RampImage("Height", sz, sz, g.H, lo, hi, b, rampHeights)
	height.Unit = "m"
	biomes := procgen.ImageLayer{Name: RegionsOverlayName, W: sz, H: sz, RGBA: rgbToRGBA(rgb), Bounds: b}
	// Relief shaded with the biome colours: the join at a glance.
	tinted := procgen.ImageLayer{Name: "Biome relief", W: sz, H: sz, RGBA: terrain.Relief(g, rgb), Values: g.H, Unit: "m", Bounds: b}
	out.Images = append(out.Images, relief, tinted, biomes, height,
		procgen.ImageLayer{Name: "Albedo", W: sz, H: sz, RGBA: rgbToRGBA(res.Albedo), Bounds: b},
		procgen.RampImage("Flow", sz, sz, res.Masks[terrain.MaskFlow], 0, 1, b, rampFlow),
		procgen.GrayImage("Slope", sz, sz, res.Masks[terrain.MaskSlope], 0, 1, b),
		procgen.RampImage("Deposition", sz, sz, res.Masks[terrain.MaskDeposition], 0, 1, b, rampSand),
		procgen.RampImage("Cliff", sz, sz, res.Masks[terrain.MaskCliff], 0, 1, b, rampCliff),
		procgen.RampImage("Curvature", sz, sz, res.Masks[terrain.MaskCurvature], 0, 1, b, rampCurve),
	)
	for e, name := range envs {
		out.Images = append(out.Images, procgen.GrayImage(terrain.BiomeMask(name), sz, sz, planes[e], 0, 1, b))
	}
	for _, cp := range res.Paths {
		l := procgen.LineLayer{Name: "Canyon", Color: [4]float32{0.35, 0.85, 1, 0.9}, Points: cp.Points}
		if cp.Tributary {
			l.Name, l.Color = "Tributary", [4]float32{0.6, 0.9, 1, 0.55}
		}
		out.Lines = append(out.Lines, l)
	}
	for k := range set.Regions {
		r := &set.Regions[k]
		c := environmentColor(r.Environment)
		line := procgen.LineLayer{Name: r.Key, Closed: true, Color: [4]float32{float32(c[0]), float32(c[1]), float32(c[2]), 1}}
		for _, q := range r.Footprint {
			line.Points = append(line.Points, [2]float64{q[0], q[1]})
		}
		out.Lines = append(out.Lines, line)
	}
	// The tile grid and the tiles as preview meshes.
	tp := tilePlan{size: plan.tile, step: 1}
	tp.count, _ = whole(g.Size(), tp.size)
	grid := [4]float32{1, 1, 1, 0.3}
	for k := 1; k < tp.count; k++ {
		x := b.MinX + (b.MaxX-b.MinX)*float64(k)/float64(tp.count)
		z := b.MinZ + (b.MaxZ-b.MinZ)*float64(k)/float64(tp.count)
		out.Lines = append(out.Lines,
			procgen.LineLayer{Name: "Tiles", Points: [][2]float64{{x, b.MinZ}, {x, b.MaxZ}}, Color: grid},
			procgen.LineLayer{Name: "Tiles", Points: [][2]float64{{b.MinX, z}, {b.MaxX, z}}, Color: grid})
	}
	if per, ok := whole(tp.size, g.Cell); ok && tp.count > 0 && tp.count*per == g.N {
		tp.per = per
		for tj := 0; tj < tp.count; tj++ {
			for ti := 0; ti < tp.count; ti++ {
				out.Meshes = append(out.Meshes, tilePreviewMesh(tileName(ti, tj), g, tp, ti, tj))
			}
		}
	} else {
		tp = tilePlan{size: g.Size(), count: 1, per: g.N, step: 1}
		out.Meshes = append(out.Meshes, tilePreviewMesh("ground", g, tp, 0, 0))
	}
	out.Notes = append(out.Notes,
		fmt.Sprintf("%d regions, %d environments; union %.0f m square from (%.0f, %.0f), heights %.1f .. %.1f m",
			set.Len(), len(envs), g.Size(), g.MinX, g.MinZ, lo, hi),
		fmt.Sprintf("preview %d² at %.2f m, global erosion (run: %d² at %g m, %s erosion), %s", sz, g.Cell, full+1,
			gp.Float("resolution"), gp.Str("erosion"), time.Since(t0).Round(time.Millisecond)),
		fmt.Sprintf("lead region %s (its drainage, droplet and talus numbers)", set.Regions[res.Lead].Key))
	return out, nil
}
