package stages

// region_material.go — the `region_material` stage: ONE splat material for
// a region terrain's tiles, painted by every biome's own ground rules
// (docs/regions/SPEC.md "Biome-weighted materials", docs/regions/PHASE_3.md).
//
// A region terrain (region_terrain.go) is one heightfield grown from several
// environment presets. Each preset carries its own ground material: a
// `terrain_material` stage (which layer goes where: rock on steep ground,
// gravel in the washes, cracked ground on the flats, sand between) and the
// `polyhaven_textures` stage its layers come from. This stage, for each
// ENVIRONMENT the terrain holds:
//
//  1. loads the preset of the first region (by key) of that environment
//     (its generator override, else resources/environments/<env>.fgen,
//     else the built-in) and resolves its first enabled terrain_material
//     stage's params and the textures stage that stage names;
//  2. imports that environment's four layers (sand, cracked, gravel, rock
//     keys → the textures stage's named layers → Poly Haven ids) itself,
//     through the same importer the textures stage runs — so a preset's
//     textures stage needs no expansion: nothing per region is placed by
//     it, and the downloads are shared and cached on disk;
//  3. evaluates that preset's ground rules over the WHOLE union terrain
//     with that preset's params (groundWeights, the terrain_material
//     stage's own function, unchanged), and multiplies them by the
//     environment's biome weight (the terrain's mask_biome_<env>);
//  4. maps every environment's four layers onto one DEDUPED global list —
//     the same Poly Haven texture at the same repeat and projection is one
//     layer, so two biomes that share a rock share its weight — and sums
//     the products into it. More than eight distinct layers is an error
//     that names them all;
//  5. writes the result as a splat material: four layers or fewer, the
//     classic four-layer splat (weights.png); five to eight, the
//     eight-layer splat (weights.png for layers 1–4, weights2.png for 5–8).
//     Plus ground.png (the layers' tinted colours mixed by weight — what a
//     rock's ground blend reads), height.png (16 bits) and strata.png (the
//     LEAD region's strata, as terrain.yaml's strata are the lead's).
//
// So biome weighting is BAKED per texel at the terrain's mask resolution;
// the height-lifted blend between all eight layers happens in the shader,
// so across a seam band a desert's sand and a savannah's grass meet by
// their height maps the way sand and gravel do within one biome.
//
// THE MATERIAL-LEVEL KNOBS (height blend, normal strength, AO, strata ramp
// and its strength, bedding, wall normal, macro variation, varnish, detail)
// are one per material, so they come from the LEAD environment's preset
// (the lead region's — the one owning most of the union). Per-layer knobs
// (scale, triplanar, tint, anti-tile, relief) stay each biome's own. The
// strata ramp and the bedding are the lead's rock's: every layer that is
// triplanar and NOT the lead environment's has its `layerN_strata` turned
// off, so a savannah's rock is not banded with the canyon's beds.
//
// A deduped layer keeps the first environment's per-layer values (tint,
// anti-tile, relief) — environments in name order. Two biomes that share
// a texture at different tints therefore draw it at the first one's tint;
// give the presets different scales if they must differ.
//
// THE FAR BACKDROP (terrain_material_far.go's, per biome): each
// environment's far weights from the far heights by its own params
// (farGroundWeights), blended by the biome weight at the NEAREST UNION
// SAMPLE to each far lattice point — exactly the shares terrain.UnionFar
// blends the backdrop's heights by (docs/regions/PHASE_2.md), so the
// backdrop's colours follow its shapes. Written as <material>_far, with
// far_weights.png (+ far_weights2.png).
//
// The terrain's tiles wear the material when region_terrain's `material`
// names it — which the level_world template sets, and which region_terrain
// defaults to when it is empty and a region_material names it
// (regionTerrainAutoMaterial). Its artifact is a *TerrainMaterialArtifact
// (Weights2/FarWeights2 and Layers added), so every stage that reads a
// ground reads this one.
//
// Kind Shared, standing in for the presets' terrain_material stages
// (SharedFor), so a preset placement stage expanded per region (phase 4)
// that names its preset's terrain_material is rewritten to this stage.

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"foundry/internal/material"
	"foundry/internal/procgen"
	"foundry/internal/procgen/regions"
)

// RegionMaterialKind is the stage's name.
const RegionMaterialKind = "region_material"

// RegionMaterialFile is the stage's manifest (a TerrainMaterialArtifact).
const RegionMaterialFile = "region_material.yaml"

// RegionMaterialLayer is one layer of a region material, as its manifest
// lists it.
type RegionMaterialLayer struct {
	// Name is the layer's engine albedo texture; ID its Poly Haven asset.
	Name string `yaml:"name"`
	ID   string `yaml:"id"`
	// Role is the ground rule it answers: sand, cracked, gravel or rock.
	Role string `yaml:"role"`
	// Environments are the biomes using it (more than one when shared).
	Environments []string `yaml:"environments,flow"`
	Scale        float64  `yaml:"scale"`
	Triplanar    bool     `yaml:"triplanar,omitempty"`
	// Coverage is its share of the ground.
	Coverage float64 `yaml:"coverage"`
}

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name: RegionMaterialKind,
		// Per-tile reruns after a paint stroke (docs/terrain-paint/PHASE_P2.md).
		Incremental: true,
		Label:       "Region material",
		Category:    "Materials",
		Regional:    procgen.RegionalShared,
		// The presets' terrain_material stages are this stage, per region.
		SharedFor: []string{TerrainMaterialKind},
		// 2: soft-ground layers (sand, snow) marked for the ground trails.
		Version: 2,
		Doc: "The material a region terrain's tiles wear: each environment's own ground rules and textures (its preset's " +
			"terrain_material and Poly Haven textures stages) evaluated over the whole terrain, weighted by the " +
			"environment's biome mask, and baked into one splat of up to eight layers (textures shared between " +
			"environments share a layer). Also writes the ground colour and height maps rocks blend into, and the far " +
			"backdrop's material.",
		Params:    regionMaterialParams(),
		Run:       runRegionMaterial,
		Preview:   previewRegionMaterial,
		Load:      loadRegionMaterial,
		Validate:  validateRegionMaterial,
		HashExtra: regionMaterialPresetsHash,
	})
}

func regionMaterialParams() []procgen.ParamDef {
	return append([]procgen.ParamDef{
		{Name: "terrain", Type: procgen.ParamStage, StageKinds: []string{RegionTerrainKind, GroundEditKind}, Default: procgen.StageRef(""),
			Doc: "The region terrain whose biome masks weight each environment's ground, and whose masks place its layers."},
		{Name: "offline", Type: procgen.ParamBool, Default: procgen.Bool(false),
			Doc: "Use only Poly Haven textures already downloaded (each preset's textures stage may also say so)."},
		{Name: "preview_layers", Type: procgen.ParamBool, Label: "Preview per layer", Default: procgen.Bool(false), PreviewOnly: true,
			Doc: "Also show every layer's weight as its own grey picture in the preview."},
	}, append(regionTileParams(), // + the paint list (terrain_material_paint.go)
		splatPaintParam("a role (sand, cracked, gravel, rock, wet, ash, snow: every environment's layer of it) or one layer's Poly Haven id or albedo name"))...)
}

func validateRegionMaterial(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	if p.Str("terrain") == "" {
		return []string{"Choose a region terrain stage"}
	}
	return nil
}

// splatRoleNames are the four ground rules, in terrain_material's weight
// order (splatLayerKeys).
var splatRoleNames = groundRoleKeys

// biomeSource is one environment's ground material: the preset it comes
// from, its terrain_material params and the textures stage they name.
type biomeSource struct {
	env, from string
	// region is the key of the region whose preset it is.
	region string
	// stage is the preset's terrain_material stage ID; p its resolved
	// params; tex the resolved params of the textures stage it names.
	stage string
	p     procgen.Params
	tex   procgen.Params
}

// regionMaterialRegions is the regions behind terrain stage terrainRef
// (a region_terrain) in d: its regions stage's list, sorted by key.
func regionMaterialRegions(d *procgen.Doc, terrainRef string) ([]regions.Region, error) {
	ts := terrainSourceStage(d, terrainRef) // through a ground_edit
	if ts == nil || ts.Kind != RegionTerrainKind {
		return nil, fmt.Errorf("Terrain must name a region_terrain stage (got %q)", terrainRef)
	}
	tp := procgen.Resolve(procgen.KindByName(RegionTerrainKind).Params, ts.Params)
	_, gp, err := regionsStageParams(d, tp.Str("regions"))
	if err != nil {
		return nil, err
	}
	rs := regionsFromParams(gp)
	sort.SliceStable(rs, func(a, b int) bool { return rs[a].Key < rs[b].Key })
	return rs, nil
}

// loadRegionPreset is region r's preset document and where it came from.
func loadRegionPreset(r *regions.Region) (*procgen.Doc, string, error) {
	var pre *procgen.Doc
	var from string
	var err error
	if r.Generator != "" {
		pre, from, err = procgen.LoadGenerator(r.Generator)
	} else {
		pre, from, err = procgen.LoadEnvironment(r.Environment)
	}
	if err != nil {
		return nil, "", fmt.Errorf("region %s: %w", r.ID, err)
	}
	return pre, from, nil
}

// biomeSources is every environment's ground material among rs, in
// environment-name order: the preset of the first region (by key) of each.
func biomeSources(rs []regions.Region) ([]biomeSource, error) {
	byEnv := map[string]*regions.Region{}
	var envs []string
	for i := range rs {
		if _, ok := byEnv[rs[i].Environment]; !ok {
			byEnv[rs[i].Environment] = &rs[i]
			envs = append(envs, rs[i].Environment)
		}
	}
	sort.Strings(envs)
	tmk, txk := procgen.KindByName(TerrainMaterialKind), procgen.KindByName(PolyHavenTexturesKind)
	var out []biomeSource
	for _, env := range envs {
		r := byEnv[env]
		pre, from, err := loadRegionPreset(r)
		if err != nil {
			return nil, err
		}
		var tm *procgen.Stage
		for i := range pre.Stages {
			if s := &pre.Stages[i]; s.Kind == TerrainMaterialKind && !s.Disabled {
				tm = s
				break
			}
		}
		if tm == nil {
			return nil, fmt.Errorf("environment %s (%s): the preset has no enabled terrain_material stage to paint its ground with", env, from)
		}
		p := procgen.Resolve(tmk.Params, tm.Params)
		ts := pre.StageByID(p.Str("textures"))
		if ts == nil || ts.Kind != PolyHavenTexturesKind {
			return nil, fmt.Errorf("environment %s (%s): its terrain_material %s names no Poly Haven textures stage (textures: %q)",
				env, from, tm.ID, p.Str("textures"))
		}
		out = append(out, biomeSource{env: env, from: from, region: r.Key, stage: tm.ID, p: p,
			tex: procgen.Resolve(txk.Params, ts.Params)})
	}
	return out, nil
}

// regionMaterialPresetsHash folds every environment's ground material — its
// preset's terrain_material and textures params — into the stage's hash:
// editing a preset's ground reruns the material.
func regionMaterialPresetsHash(d *procgen.Doc, _ *procgen.Stage, p procgen.Params) (string, error) {
	// A preset that cannot be read hashes as its error rather than failing
	// the plan: the run then says what is wrong, and every other stage of
	// the doc still runs.
	rs, err := regionMaterialRegions(d, p.Str("terrain"))
	if err != nil {
		return "error: " + err.Error(), nil
	}
	srcs, err := biomeSources(rs)
	if err != nil {
		return "error: " + err.Error(), nil
	}
	tmk, txk := procgen.KindByName(TerrainMaterialKind), procgen.KindByName(PolyHavenTexturesKind)
	var parts []string
	for _, b := range srcs {
		parts = append(parts, b.env+"="+b.region+":"+b.stage+":"+procgen.StageHash(tmk, 0, b.p, nil)+":"+
			procgen.StageHash(txk, 0, b.tex, nil))
	}
	return strings.Join(parts, ","), nil
}

// textureItem is the textures stage's layer called name: its Poly Haven
// id and its scale (0: the texture's own size).
func textureItem(tex procgen.Params, name string) (id string, scale float64, ok bool) {
	for _, it := range tex.Items("layers") {
		if strings.TrimSpace(it.Str("name")) == name {
			return strings.TrimSpace(it.Str("id")), it.Float("scale"), true
		}
	}
	return "", 0, false
}

// regionLayer is one deduped layer of the global list.
type regionLayer struct {
	layer *TextureLayer
	role  int
	// scale, triplanar: its repeat and projection (the dedupe key, with
	// its Poly Haven id).
	scale     float64
	triplanar bool
	tint      [3]float64
	anti      bool
	relief    float64
	// strata: the strata ramp and bedding band it (the lead's rock).
	strata bool
	envs   []string
	// mean is its tinted average colour.
	mean [3]float64
}

// key is its dedupe key.
func (l *regionLayer) key() string {
	return fmt.Sprintf("%s|%.4g|%v", l.layer.ID, l.scale, l.triplanar)
}

// biomeLayers is one environment's four layers resolved: the textures
// (nil where its terrain_material leaves a key empty) and where each went
// in the global list.
type biomeLayerSet struct {
	src    biomeSource
	layers [groundRoles]*TextureLayer
	tints  [groundRoles][3]float64
	means  [groundRoles][3]float64
	// slot is each of its four layers' index in the global list, -1 none.
	slot [groundRoles]int
}

// layerScale is layer i's repeat in a terrain_material with params p, as
// terrainMaterialDoc works it out.
func layerScale(p procgen.Params, i int, l *TextureLayer) float64 {
	scale := l.Scale
	if i == 3 && p.Float("rock_scale") > 0 {
		scale = p.Float("rock_scale")
	}
	if scale <= 0 {
		scale = 3
	}
	return round3(scale)
}

// mergeRegionLayers dedupes every biome's layers into the global list (in
// environment then role order) and fills each set's slots. lead is the
// lead environment: its triplanar layers carry the strata.
func mergeRegionLayers(sets []*biomeLayerSet, lead string) ([]*regionLayer, error) {
	var out []*regionLayer
	index := map[string]int{}
	for _, b := range sets {
		p := b.src.p
		for i := 0; i < groundRoles; i++ {
			b.slot[i] = -1
			l := b.layers[i]
			if l == nil {
				continue
			}
			rl := &regionLayer{layer: l, role: i, scale: layerScale(p, i, l), triplanar: i == 3 && p.Bool("rock_triplanar"),
				tint: b.tints[i], anti: p.Bool("anti_tile"), relief: roleRelief(p, i),
				mean: b.means[i]}
			rl.strata = rl.triplanar && b.src.env == lead
			if k, ok := index[rl.key()]; ok {
				g := out[k]
				g.envs = append(g.envs, b.src.env)
				g.strata = g.strata || rl.strata
				b.slot[i] = k
				continue
			}
			rl.envs = []string{b.src.env}
			index[rl.key()] = len(out)
			b.slot[i] = len(out)
			out = append(out, rl)
		}
	}
	// More than eight is a PALETTE (region_material_palette.go): every
	// tile then wears at most eight of them. More than the palette holds is
	// an error.
	if len(out) > material.SplatAtlasMax {
		var names []string
		for _, l := range out {
			names = append(names, fmt.Sprintf("%s %s (%s, %g m)", strings.Join(l.envs, "+"), splatRoleNames[l.role], l.layer.ID, l.scale))
		}
		return nil, fmt.Errorf("%d distinct ground layers, at most %d fit one world's palette: %s. Share textures between the "+
			"environments' presets (the same Poly Haven id at the same scale is one layer) or leave a layer key empty",
			len(out), material.SplatAtlasMax, strings.Join(names, "; "))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no environment has a ground layer")
	}
	return out, nil
}

// roleRelief is a role's parallax relief in a terrain_material with params
// p: cracked ground's and gravel's; none for the others.
func roleRelief(p procgen.Params, role int) float64 {
	switch role {
	case 1:
		return p.Float("cracked_relief")
	case 2:
		return p.Float("gravel_relief")
	}
	return 0
}

// leadEnvironment is the environment of the region the terrain names as
// its lead, or the first environment.
func leadEnvironment(leadKey string, rs []regions.Region, srcs []biomeSource) string {
	for _, r := range rs {
		if r.Key == leadKey {
			return r.Environment
		}
	}
	if len(srcs) > 0 {
		return srcs[0].env
	}
	return ""
}

// clampUnit is v in 0..1.
func clampUnit(v float64) float64 { return math.Max(0, math.Min(1, v)) }
