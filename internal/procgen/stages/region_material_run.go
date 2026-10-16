package stages

// region_material_run.go — the region_material stage's run, preview and
// load (region_material.go has the stage, the environments' sources and
// the layer dedupe).
//
// Outputs, in the stage's folder:
//
//	weights.png        layers 1–4 (RGBA), over the terrain's rectangle
//	weights2.png       layers 5–8, when there are more than four
//	ground.png         the ground's colour (layers' tinted means by weight)
//	height.png         the terrain's height, 16 bits (red high, green low)
//	strata.png         the lead region's strata ramp
//	far_weights.png    the backdrop's (+ far_weights2.png), when it has one
//	region_material.yaml
//	    a TerrainMaterialArtifact: material, maps, rectangle, height range,
//	    ground/rock means, coverage by role (sand, cracked, gravel, rock),
//	    and layers: [{name, id, role, environments, scale, triplanar,
//	    coverage}, ...]
//
// and resources/materials/gen_<doc>_<stage>.fmat (+ _far.fmat).
//
// The weights are worked out in float per sample: for each environment e,
// groundWeights(e's params) × biome_e, summed into the global layer each of
// e's four layers maps to, then normalised to sum to one (a key a preset
// leaves empty gives its share to the others) and stored as bytes.

import (
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"foundry/internal/material"
	"foundry/internal/polyhaven/phimport"
	"foundry/internal/procgen"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/terrain"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func loadRegionMaterial(res *procgen.StageResult) (any, error) {
	data, err := os.ReadFile(filepath.Join(res.Dir, RegionMaterialFile))
	if err != nil {
		return nil, err
	}
	var a TerrainMaterialArtifact
	if err := yaml.UnmarshalStrict(data, &a); err != nil {
		return nil, fmt.Errorf("%s: %w", RegionMaterialFile, err)
	}
	return &a, nil
}

// envSeed is environment env's ground-rule noise seed under seed.
func envSeed(seed uint64, env string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(env))
	return noise.Hash64(seed, h.Sum64())
}

// layerLookup finds a textures stage's layer id (with its params) as a
// TextureLayer: the run imports it, the preview reads what is on disk.
type layerLookup func(id string, tex procgen.Params) (*TextureLayer, error)

// resolveBiomeLayers is every environment's four layers, tints and tinted
// means (as buildTerrainMaterialMaps works them out).
func resolveBiomeLayers(srcs []biomeSource, lookup layerLookup) ([]*biomeLayerSet, error) {
	var out []*biomeLayerSet
	for _, src := range srcs {
		b := &biomeLayerSet{src: src}
		for i, key := range groundRoleKeys {
			name := src.p.Str(key)
			if name == "" {
				continue
			}
			id, scale, ok := textureItem(src.tex, name)
			if !ok {
				return nil, fmt.Errorf("environment %s: its textures stage has no layer %q (for %s)", src.env, name, key)
			}
			l, err := lookup(id, src.tex)
			if err != nil {
				return nil, fmt.Errorf("environment %s, layer %s (%s): %w", src.env, name, id, err)
			}
			cp := *l
			cp.Name = name
			cp.Scale = scale
			if cp.Scale <= 0 {
				cp.Scale = cp.Size
			}
			b.layers[i] = &cp
		}
		var means [groundRoles][3]float64
		for i, l := range b.layers {
			means[i] = [3]float64{0.5, 0.5, 0.5}
			if l != nil {
				if m, ok := layerMean(l.Albedo); ok {
					means[i] = m
				}
			}
		}
		// The four rules' tints toward the sand's hue; the covers (snow,
		// mud, ash) keep their own colour.
		var four [4][3]float64
		var fourL [4]*TextureLayer
		copy(four[:], means[:4])
		copy(fourL[:], b.layers[:4])
		var tints [groundRoles][3]float64
		t4 := layerTints(four, fourL, src.p.Float("layer_match"))
		copy(tints[:], t4[:])
		for i := 4; i < groundRoles; i++ {
			tints[i] = [3]float64{1, 1, 1}
		}
		rt, st, ct := src.p.Vec3("rock_tint"), src.p.Vec3("sand_tint"), src.p.Vec3("cracked_tint")
		for ch := 0; ch < 3; ch++ {
			tints[3][ch] *= rt[ch]
			tints[0][ch] *= st[ch]
			tints[1][ch] *= ct[ch]
		}
		for i := range means {
			for ch := 0; ch < 3; ch++ {
				means[i][ch] *= tints[i][ch]
			}
		}
		b.tints, b.means = tints, means
		out = append(out, b)
	}
	return out, nil
}

// importLookup imports a layer through the Poly Haven importer (cached on
// disk: a second ask converts nothing), at most once per id.
func importLookup(rc *procgen.RunContext, offline bool) layerLookup {
	var mu sync.Mutex
	done := map[string]*TextureLayer{}
	return func(id string, tex procgen.Params) (*TextureLayer, error) {
		key := id + "|" + tex.Str("resolution") + "|" + fmt.Sprint(tex.Int("max_size"))
		mu.Lock()
		defer mu.Unlock()
		if l := done[key]; l != nil {
			return l, nil
		}
		c := stageClient(tex)
		if offline && !c.Offline {
			c = c.Clone()
			c.Offline = true
		}
		ts, err := phimport.ImportTextures(rc.Ctx, c, id, phimport.TextureOptions{Resources: PolyHavenResources(),
			Resolution: tex.Str("resolution"), MaxSize: tex.Int("max_size"), Log: func(f string, a ...any) { rc.Logf(f, a...) }})
		if err != nil {
			return nil, err
		}
		l := &TextureLayer{ID: id, Resolution: ts.Resolution, Size: ts.Size, Albedo: ts.Albedo, Normal: ts.Normal, ARM: ts.ARM,
			Rough: ts.Rough, AO: ts.AO, Height: ts.Height}
		done[key] = l
		return l, nil
	}
}

// diskLookup is the preview's: the layer as last imported (its size from
// the provenance record), never a download.
func diskLookup(id string, _ procgen.Params) (*TextureLayer, error) {
	l := &TextureLayer{ID: id, Albedo: phimport.TextureName(id, "")}
	prov, err := phimport.ReadProvenance(filepath.Join(PolyHavenResources(), filepath.FromSlash(phimport.TextureSetsDir), id,
		phimport.ProvenanceFile))
	if err == nil && prov.TextureSet != nil {
		l.Size = prov.TextureSet.Size
	}
	return l, nil
}

// regionWeights is the global layers' weights at every terrain sample
// (len(layers) per sample, summing to one where any layer is): each
// environment's ground rules × its biome weight.
func regionWeights(t *terrain.Terrain, sets []*biomeLayerSet, nl int, seed uint64, pool terrain.Pool) ([]float32, error) {
	sz := t.Grid.S()
	n := sz * sz
	out := make([]float32, nl*n)
	for _, b := range sets {
		bm, err := t.Mask(terrain.BiomeMask(b.src.env))
		if err != nil {
			return nil, fmt.Errorf("environment %s: %w (rerun the terrain)", b.src.env, err)
		}
		// Its four rules and its covers, when its preset names any
		// (terrain_material_cover.go): nc weights a sample.
		// A region-referenced snow line is measured over its own core.
		w, nc, err := coverGroundWeightsCore(t, b.src.p, envSeed(seed, b.src.env), pool, bm)
		if err != nil {
			return nil, err
		}
		for k := 0; k < n; k++ {
			bw := bm[k]
			if bw <= 0 {
				continue
			}
			for i := 0; i < nc; i++ {
				if s := b.slot[i]; s >= 0 {
					out[k*nl+s] += bw * w[nc*k+i]
				}
			}
		}
	}
	normaliseLayers(out, nl)
	return out, nil
}

// normaliseLayers scales every sample's nl weights to sum to one.
func normaliseLayers(w []float32, nl int) {
	for k := 0; k+nl <= len(w); k += nl {
		var s float32
		for i := 0; i < nl; i++ {
			s += w[k+i]
		}
		if s > 1e-6 {
			for i := 0; i < nl; i++ {
				w[k+i] /= s
			}
		}
	}
}

// splitWeights is the nl-per-sample weights as one or two RGBA byte maps
// (layers 1–4, 5–8; the second nil for four layers or fewer).
func splitWeights(w []float32, nl, n int) (first, second []uint8) {
	first = make([]uint8, 4*n)
	if nl > 4 {
		second = make([]uint8, 4*n)
	}
	for k := 0; k < n; k++ {
		for i := 0; i < nl; i++ {
			v := uint8(clampUnit(float64(w[k*nl+i]))*255 + 0.5)
			if i < 4 {
				first[4*k+i] = v
			} else {
				second[4*k+i-4] = v
			}
		}
	}
	return first, second
}

func runRegionMaterial(rc *procgen.RunContext) error {
	p := rc.Params
	t0 := time.Now()
	ta, err := rc.UpstreamArtifact(p.Str("terrain"))
	if err != nil {
		return err
	}
	t, ok := ta.(*terrain.Terrain)
	if !ok || t.Meta.Biomes == nil {
		return fmt.Errorf("stage %s is not a region terrain (it has no biomes)", p.Str("terrain"))
	}
	rs, err := regionMaterialRegions(rc.Doc, p.Str("terrain"))
	if err != nil {
		return err
	}
	srcs, err := biomeSources(rs)
	if err != nil {
		return err
	}
	if err := checkBiomes(t, srcs); err != nil {
		return err
	}
	lead := leadEnvironment(t.Meta.Biomes.Lead, rs, srcs)
	sets, err := resolveBiomeLayers(srcs, importLookup(rc, p.Bool("offline")))
	if err != nil {
		return err
	}
	rc.Progress(0.2)
	layers, err := mergeRegionLayers(sets, lead)
	if err != nil {
		return err
	}
	for _, b := range sets {
		rc.Logf("environment %s: %s (%s, stage %s), layers %s", b.src.env, b.src.region, b.src.from, b.src.stage, describeSlots(b, layers))
	}
	nl := len(layers)
	g := t.Grid
	sz := g.S()
	n := sz * sz
	// Kept across strokes when the stage reads paint (paint_weight_cache.go).
	w, wn, reused, err := cachedWeights(rc, func() ([]float32, int, error) {
		w, err := regionWeights(t, sets, nl, rc.Seed, &rc.Env)
		return w, nl, err
	})
	if err != nil {
		return err
	}
	if wn != nl || len(w) != n*nl {
		return fmt.Errorf("unpainted weights: %d layers over %d values, want %d over %d", wn, len(w), nl, n*nl)
	}
	if err := regionMaterialPaint(&rc.Env, p, g, w, layers, t); err != nil {
		return err
	}
	rc.Progress(0.55)
	// More than eight layers is a palette (region_material_palette.go): no
	// union weights or material, every tile dressed in at most eight.
	palette := nl > material.SplatLayersMax
	var w1, w2 []uint8
	if palette {
		rc.Logf("%d ground layers: a palette — every tile wears a material of at most eight of them (per-tile materials are on)", nl)
	} else {
		w1, w2 = splitWeights(w, nl, n)
	}
	ground := make([]uint8, 4*n)
	height := make([]uint8, 4*n)
	lo, hi := g.Range()
	span := float64(hi - lo)
	if span == 0 {
		span = 1
	}
	var cover [groundRoles]float64
	perLayer := make([]float64, nl)
	var groundMean [3]float64
	for k := 0; k < n; k++ {
		var col [3]float64
		for i, l := range layers {
			wi := float64(w[k*nl+i])
			cover[l.role] += wi
			perLayer[i] += wi
			for ch := 0; ch < 3; ch++ {
				col[ch] += wi * l.mean[ch]
			}
		}
		for ch := 0; ch < 3; ch++ {
			ground[4*k+ch] = uint8(clampUnit(col[ch])*255 + 0.5)
			groundMean[ch] += col[ch]
		}
		ground[4*k+3] = 255
		v := uint16(clampUnit(float64(g.H[k]-lo)/span)*65535 + 0.5)
		height[4*k], height[4*k+1], height[4*k+2], height[4*k+3] = uint8(v>>8), uint8(v), 0, 255
	}
	strata := strataRamp(&t.Meta.Strata, float64(lo), float64(hi))
	type file struct {
		name string
		w, h int
		pix  []uint8
	}
	files := []file{{"ground.png", sz, sz, ground}, {"height.png", sz, sz, height},
		{"strata.png", len(strata) / 4, 1, strata}}
	if w1 != nil {
		files = append([]file{{"weights.png", sz, sz, w1}}, files...)
	}
	if w2 != nil {
		files = append(files, file{"weights2.png", sz, sz, w2})
	}
	if err := rc.Parallel(len(files), func(i int) error {
		if (files[i].name == "height.png" || files[i].name == "strata.png") && keepUnpainted(rc, reused, files[i].name) {
			return nil // follows no paint (paint_weight_cache.go)
		}
		data, err := terrain.EncodeRGBA(files[i].w, files[i].h, files[i].pix)
		if err != nil {
			return err
		}
		return rc.WriteFile(files[i].name, data)
	}); err != nil {
		return err
	}
	rc.Progress(0.7)
	half := g.Cell / 2
	a := &TerrainMaterialArtifact{Version: 1, Material: genMaterialName(rc, ""),
		Weights: mapRef(rc, "weights.png"), Ground: mapRef(rc, "ground.png"), Height: mapRef(rc, "height.png"),
		Strata: mapRef(rc, "strata.png"),
		Min:    [2]float64{g.MinX - half, g.MinZ - half}, Max: [2]float64{g.X(sz-1) + half, g.Z(sz-1) + half},
		HeightLo: lo, HeightHi: hi}
	if palette {
		a.Weights = ""
	}
	if w2 != nil {
		a.Weights2 = mapRef(rc, "weights2.png")
	}
	for c := range a.GroundMean {
		a.GroundMean[c] = round3(groundMean[c] / float64(n))
	}
	for r := range a.Coverage {
		a.Coverage[r] = round3(cover[r] / float64(n))
	}
	for _, l := range layers {
		if l.role == 3 && containsString(l.envs, lead) {
			for c := range a.RockMean {
				a.RockMean[c] = round3(l.mean[c])
			}
			break
		}
	}
	for i, l := range layers {
		a.Layers = append(a.Layers, RegionMaterialLayer{Name: l.layer.Albedo, ID: l.layer.ID, Role: splatRoleNames[l.role],
			Environments: l.envs, Scale: l.scale, Triplanar: l.triplanar, Coverage: round3(perLayer[i] / float64(n))})
	}
	leadP := leadParams(srcs, lead)
	if palette {
		// Every tile its own material of at most eight; the union material
		// is never written (its name is what region_terrain's placements
		// wear until they are re-dressed).
		var farW []float32
		fn := 0
		if t.Meta.Far != nil {
			if farW, fn, err = regionFarWeights(rc, t, sets, nl, a); err != nil {
				return err
			}
			a.FarMaterial = a.Material + "_far"
		}
		if err := writeRegionPalette(rc, t, a, layers, leadP, lead, w, farW, fn); err != nil {
			return err
		}
		return finishRegionMaterial(rc, a, nl, sz, lead, t0)
	}
	if err := writeMaterial(rc, regionMaterialDoc(a, leadP, layers, lead)); err != nil {
		return err
	}
	var far *farWeightMaps
	if t.Meta.Far != nil {
		if far, err = writeRegionFarMaterial(rc, t, sets, layers, a, leadP, lead); err != nil {
			return err
		}
	}
	// Per-tile materials (region_material_tiles.go), when asked for.
	if err := writeRegionTileMaterials(rc, t, a, layers, leadP, lead, w1, w2, far); err != nil {
		return err
	}
	return finishRegionMaterial(rc, a, nl, sz, lead, t0)
}

// finishRegionMaterial writes the manifest, sets the artifact and logs.
func finishRegionMaterial(rc *procgen.RunContext, a *TerrainMaterialArtifact, nl, sz int, lead string, t0 time.Time) error {
	data, err := yaml.Marshal(a)
	if err != nil {
		return err
	}
	if err := rc.WriteFile(RegionMaterialFile, data); err != nil {
		return err
	}
	rc.Artifact = a
	artifactAsLoaded(rc, data) // a painted one (paint_weight_cache.go)
	var parts []string
	for _, l := range a.Layers {
		parts = append(parts, fmt.Sprintf("%s %s %s %.0f%%", strings.Join(l.Environments, "+"), l.Role, l.ID, 100*l.Coverage))
	}
	mode := "four-layer"
	switch {
	case nl > material.SplatLayersMax:
		mode = "palette"
	case nl > 4:
		mode = "eight-layer"
	}
	rc.Logf("material %s (%s splat, %d layers; region_terrain's material names it): %d² maps; %s; lead %s; in %s",
		a.Material, mode, nl, sz, strings.Join(parts, ", "), lead, time.Since(t0).Round(time.Millisecond))
	rc.Progress(1)
	return nil
}

// checkBiomes reports a terrain whose environments are not the regions'
// (the terrain is older than the regions list).
func checkBiomes(t *terrain.Terrain, srcs []biomeSource) error {
	have := map[string]bool{}
	for _, e := range t.Biomes() {
		have[e] = true
	}
	for _, s := range srcs {
		if !have[s.env] {
			return fmt.Errorf("the terrain has no biome %s (rerun the region terrain)", s.env)
		}
	}
	return nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// leadParams is the lead environment's terrain_material params.
func leadParams(srcs []biomeSource, lead string) procgen.Params {
	for _, s := range srcs {
		if s.env == lead {
			return s.p
		}
	}
	return srcs[0].p
}

// describeSlots says where an environment's four layers went.
func describeSlots(b *biomeLayerSet, layers []*regionLayer) string {
	var parts []string
	for i, key := range groundRoleKeys {
		if s := b.slot[i]; s >= 0 {
			parts = append(parts, fmt.Sprintf("%s=%s→%d", key, layers[s].layer.ID, s+1))
		}
	}
	return strings.Join(parts, " ")
}

// regionMaterialDoc is the splat material over a's maps: the global
// layers in order (5–8 on weights2), each with its own repeat, projection,
// tint, anti-tiling and relief; the lead environment's material-level
// knobs (leadP); the strata off on triplanar layers that are not the
// lead's.
func regionMaterialDoc(a *TerrainMaterialArtifact, leadP procgen.Params, layers []*regionLayer, lead string) *material.Doc {
	var envs []string
	seen := map[string]bool{}
	for _, l := range layers {
		for _, e := range l.envs {
			if !seen[e] {
				seen[e] = true
				envs = append(envs, e)
			}
		}
	}
	sort.Strings(envs)
	d := &material.Doc{Name: a.Material, Domain: material.DomainSurface,
		Doc: "Generated region terrain: " + strings.Join(envs, ", ") + " ground layers, each environment's rules weighted by its biome mask"}
	e := d.AddEffect(material.KindSplat)
	e.Set("weights", material.TextureValue(a.Weights))
	if a.Weights2 != "" {
		e.Set("weights2", material.TextureValue(a.Weights2))
	}
	e.Set("bounds_min", material.Vec2Value(mgl32.Vec2{float32(a.Min[0]), float32(a.Min[1])}))
	e.Set("bounds_max", material.Vec2Value(mgl32.Vec2{float32(a.Max[0]), float32(a.Max[1])}))
	e.Set("blend", material.FloatValue(float32(leadP.Float("blend"))))
	e.Set("normal_strength", material.FloatValue(float32(leadP.Float("normal_strength"))))
	e.Set("ao", material.FloatValue(float32(leadP.Float("ao"))))
	for i, l := range layers {
		e.Set(material.SplatLayerParam(i, ""), material.TextureValue(l.layer.Albedo))
		e.Set(material.SplatLayerParam(i, "scale"), material.FloatValue(float32(l.scale)))
		if l.triplanar {
			e.Set(material.SplatLayerParam(i, "triplanar"), material.BoolValue(true))
			if !l.strata {
				e.Set(material.SplatLayerParam(i, "strata"), material.BoolValue(false))
			}
		}
		if l.anti {
			e.Set(material.SplatLayerParam(i, "anti_tile"), material.BoolValue(true))
		}
		setSoft(e, i, l.layer, l.triplanar)
		if l.relief > 0 {
			e.Set(material.SplatLayerParam(i, "relief"), material.FloatValue(float32(l.relief)))
		}
		if t := l.tint; t != [3]float64{1, 1, 1} {
			e.Set(material.SplatLayerParam(i, "tint"), material.ColorValue(mgl32.Vec3{float32(round3(t[0])), float32(round3(t[1])), float32(round3(t[2]))}))
		}
	}
	terrainMaterialExtras(d, e, a, leadP)
	return d
}

// regionFarWeights is the backdrop's nl weights a lattice point: every
// environment's far weights (farGroundWeights and its covers, by its own
// params) blended by its biome weight at the nearest union sample — the
// shares terrain.UnionFar blends the backdrop's heights by — normalised.
// fn is the lattice's side.
func regionFarWeights(rc *procgen.RunContext, t *terrain.Terrain, sets []*biomeLayerSet, nl int, a *TerrainMaterialArtifact) (w []float32, fn int, err error) {
	fm := t.Meta.Far
	h, err := t.FarHeights()
	if err != nil {
		return nil, 0, err
	}
	g := t.Grid
	sz := g.S()
	fn, c := fm.Samples, fm.Cell
	// The nearest union sample to each far column and row (clamped into
	// the grid: past the edge it is the edge's).
	near := func(v, lo float64) int {
		return max(0, min(int(math.Round((v-lo)/g.Cell)), sz-1))
	}
	col := make([]int, fn)
	row := make([]int, fn)
	for i := 0; i < fn; i++ {
		col[i] = near(fm.Bounds.MinX+float64(i)*c, g.MinX)
		row[i] = near(fm.Bounds.MinZ+float64(i)*c, g.MinZ)
	}
	w = make([]float32, nl*fn*fn)
	for _, b := range sets {
		bm, err := t.Mask(terrain.BiomeMask(b.src.env))
		if err != nil {
			return nil, 0, err
		}
		wf, err := farGroundWeights(rc.Parallel, fm, h, b.src.p, envSeed(rc.Seed, b.src.env), a.HeightLo, a.HeightHi)
		if err != nil {
			return nil, 0, err
		}
		// Snow and ash on the backdrop (terrain_material_cover.go).
		wf, nc, err := farCoverWeights(rc.Parallel, fm, h, wf, b.src.p, t.Meta.Vents, envSeed(rc.Seed, b.src.env), snowRangeOf(b.src.p, t, bm))
		if err != nil {
			return nil, 0, err
		}
		for j := 0; j < fn; j++ {
			for i := 0; i < fn; i++ {
				bw := bm[row[j]*sz+col[i]]
				if bw <= 0 {
					continue
				}
				k := j*fn + i
				for r := 0; r < nc; r++ {
					if s := b.slot[r]; s >= 0 {
						w[k*nl+s] += bw * wf[nc*k+r]
					}
				}
			}
		}
	}
	normaliseLayers(w, nl)
	return w, fn, nil
}

// writeRegionFarMaterial is the backdrop's material over regionFarWeights.
func writeRegionFarMaterial(rc *procgen.RunContext, t *terrain.Terrain, sets []*biomeLayerSet, layers []*regionLayer,
	a *TerrainMaterialArtifact, leadP procgen.Params, lead string) (*farWeightMaps, error) {
	fm := t.Meta.Far
	c := fm.Cell
	nl := len(layers)
	w, fn, err := regionFarWeights(rc, t, sets, nl, a)
	if err != nil {
		return nil, err
	}
	w1, w2 := splitWeights(w, nl, fn*fn)
	fa := *a
	fa.Material = a.Material + "_far"
	for _, f := range []struct {
		name string
		pix  []uint8
		ref  *string
	}{{"far_weights.png", w1, &fa.Weights}, {"far_weights2.png", w2, &fa.Weights2}} {
		if f.pix == nil {
			*f.ref = ""
			continue
		}
		data, err := terrain.EncodeRGBA(fn, fn, f.pix)
		if err != nil {
			return nil, err
		}
		if err := rc.WriteFile(f.name, data); err != nil {
			return nil, err
		}
		*f.ref = mapRef(rc, f.name)
	}
	fa.Min = [2]float64{fm.Bounds.MinX - c/2, fm.Bounds.MinZ - c/2}
	fa.Max = [2]float64{fm.Bounds.MaxX + c/2, fm.Bounds.MaxZ + c/2}
	d := regionMaterialDoc(&fa, leadP, layers, lead)
	farMaterialTweaks(d, leadP)
	d.Doc = "Generated region terrain backdrop: every environment's layers splatted by the backdrop's slopes, weighted by the edge's biomes"
	if err := writeMaterial(rc, d); err != nil {
		return nil, err
	}
	a.FarMaterial, a.FarWeights, a.FarWeights2 = fa.Material, fa.Weights, fa.Weights2
	rc.Logf("far material %s: %d² weights at %g m, %d layers", fa.Material, fn, c, nl)
	return &farWeightMaps{w1: w1, w2: w2, n: fn, rect: [2][2]float64{fa.Min, fa.Max}}, nil
}

// farWeightMaps is the backdrop's weights as written (layers 1–4, 5–8; n²
// texels) over its rectangle (min, max), for its per-tile materials.
type farWeightMaps struct {
	w1, w2 []uint8
	n      int
	rect   [2][2]float64
}

// ---------- preview ----------

// regionLayerColors are the preview's false colours, one per global layer.
var regionLayerColors = [8][3]float64{
	{0.95, 0.85, 0.4}, {0.6, 0.35, 0.15}, {0.3, 0.55, 1}, {0.85, 0.2, 0.2},
	{0.55, 0.85, 0.3}, {0.75, 0.6, 0.85}, {0.2, 0.75, 0.7}, {0.95, 0.95, 0.95},
}

func previewRegionMaterial(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
	p := pc.Params
	up := pc.Upstream(p.Str("terrain"))
	if up == nil {
		return nil, fmt.Errorf("the terrain stage %q has no preview", p.Str("terrain"))
	}
	t, ok := up.Artifact.(*terrain.Terrain)
	if !ok || t.Meta.Biomes == nil {
		return nil, fmt.Errorf("stage %s is not a region terrain", p.Str("terrain"))
	}
	rs, err := regionMaterialRegions(pc.Doc, p.Str("terrain"))
	if err != nil {
		return nil, err
	}
	srcs, err := biomeSources(rs)
	if err != nil {
		return nil, err
	}
	if err := checkBiomes(t, srcs); err != nil {
		return nil, err
	}
	lead := leadEnvironment(t.Meta.Biomes.Lead, rs, srcs)
	sets, err := resolveBiomeLayers(srcs, diskLookup)
	if err != nil {
		return nil, err
	}
	layers, err := mergeRegionLayers(sets, lead)
	if err != nil {
		return nil, err
	}
	nl := len(layers)
	w, err := regionWeights(t, sets, nl, pc.Seed, &pc.Env)
	if err != nil {
		return nil, err
	}
	if err := regionMaterialPaint(&pc.Env, p, t.Grid, w, layers, t); err != nil {
		return nil, err
	}
	sz := t.Grid.S()
	n := sz * sz
	b := up.FrameBounds()
	out := &procgen.PreviewData{Bounds: b}
	falseCol := make([]uint8, 4*n)
	ground := make([]uint8, 4*n)
	cover := make([]float64, nl)
	for k := 0; k < n; k++ {
		var fc, gc [3]float64
		for i, l := range layers {
			wi := float64(w[k*nl+i])
			cover[i] += wi
			for ch := 0; ch < 3; ch++ {
				fc[ch] += wi * regionLayerColors[i%len(regionLayerColors)][ch]
				gc[ch] += wi * l.mean[ch]
			}
		}
		for ch := 0; ch < 3; ch++ {
			falseCol[4*k+ch] = uint8(clampUnit(fc[ch])*255 + 0.5)
			ground[4*k+ch] = uint8(clampUnit(gc[ch])*255 + 0.5)
		}
		falseCol[4*k+3], ground[4*k+3] = 255, 255
	}
	out.Images = append(out.Images,
		procgen.ImageLayer{Name: "Layers", W: sz, H: sz, RGBA: falseCol, Bounds: b},
		procgen.ImageLayer{Name: "Ground colour", W: sz, H: sz, RGBA: ground, Bounds: b})
	if p.Bool("preview_layers") {
		for i, l := range layers {
			vals := make([]float32, n)
			for k := range vals {
				vals[k] = w[k*nl+i]
			}
			out.Images = append(out.Images, procgen.GrayImage(fmt.Sprintf("Layer %d %s", i+1, l.layer.ID), sz, sz, vals, 0, 1, b))
		}
	}
	for _, m := range terrainMeshes(up) {
		m.Image = "Ground colour"
		out.Meshes = append(out.Meshes, m)
	}
	var key []string
	for i, l := range layers {
		key = append(key, fmt.Sprintf("%d %s %s %s %.0f%% (%s)", i+1, strings.Join(l.envs, "+"), splatRoleNames[l.role], l.layer.ID,
			100*cover[i]/float64(n), regionLayerColorNames[i%len(regionLayerColorNames)]))
	}
	mode := "four-layer splat"
	switch {
	case nl > material.SplatLayersMax:
		mode = "a palette: per-tile materials of at most eight; false colours repeat past eight"
	case nl > 4:
		mode = "eight-layer splat"
	}
	out.Notes = append(out.Notes,
		fmt.Sprintf("%d layers (%s), lead %s: %s", nl, mode, lead, strings.Join(key, ", ")),
		"the region terrain's tiles wear it as gen_"+safeName(pc.Doc.Name, "gen")+"_"+safeName(pc.Stage.ID, "stage"))
	return out, nil
}

// regionLayerColorNames name regionLayerColors in the notes.
var regionLayerColorNames = [8]string{"yellow", "brown", "blue", "red", "green", "lilac", "teal", "white"}
