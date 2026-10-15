package stages

// terrain_material.go — the `terrain_material` stage: the material a
// terrain stage's tiles wear (docs/canyon/PHASE_5.md).
//
// It reads a terrain's masks and a polyhaven_textures stage's layers and
// writes a `splat` material that paints the ground the way the land is
// shaped:
//
//   - ROCK (a sandstone or rock-wall texture) on slopes steeper than
//     rock_slope and wherever the cliff mask says cliff — projected
//     triplanar, so a wall's texture is not smeared down it, and banded by a
//     STRATA RAMP built from the terrain's own strata layers, so the bedding
//     the terrain stepped into its cliffs shows in their colour;
//   - GRAVEL in the washes (the flow mask);
//   - CRACKED ground on the flats away from washes and sand, in patches;
//   - SAND everywhere else, which is the deposition fans, the dunes against
//     walls and the gentle slopes.
//
// The four weights are worked out per terrain sample in that order of
// priority (rock takes its share first, gravel its share of the rest, and so
// on), with noise bending each band's edge so the boundaries wander like real
// ones, and written as one RGBA texture over the terrain's rectangle. The
// shader height-blends the layers by their _height maps, so pebbles poke out
// of sand rather than fading through it.
//
// It also writes what a rock needs to sit in this ground: the ground's
// colour (the layers' average colours mixed by the weights) and its height
// (16 bits split over red and green) over the same rectangle. scatter and
// cliff_wall name this stage as their `ground` to give their rocks a
// ground_blend and a colour match toward it.
//
// The terrain stage's tiles wear the material when its `material` parameter
// names it: gen_<generator>_<this stage's id>, which the log and the stage's
// notes print.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/material"
	"foundry/internal/procgen"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/terrain"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// TerrainMaterialKind is the stage's name.
const TerrainMaterialKind = "terrain_material"

// TerrainMaterialFile is the stage's manifest.
const TerrainMaterialFile = "terrain_material.yaml"

// TerrainMaterialArtifact is what the stage made: the material's name and
// the maps a rock's material reads.
type TerrainMaterialArtifact struct {
	Version  int    `yaml:"version"`
	Material string `yaml:"material"`
	// Weights, Ground and Height are the maps as materials name them
	// (resources-relative, no .png); Strata the ramp.
	Weights string `yaml:"weights"`
	Ground  string `yaml:"ground"`
	Height  string `yaml:"height"`
	Strata  string `yaml:"strata,omitempty"`
	// Min and Max are the maps' world rectangle (texel corners, X and Z);
	// HeightLo and HeightHi what 0 and 65535 of the height map stand for.
	Min      [2]float64 `yaml:"min,flow"`
	Max      [2]float64 `yaml:"max,flow"`
	HeightLo float32    `yaml:"height_lo"`
	HeightHi float32    `yaml:"height_hi"`
	// GroundMean is the ground's average colour, weighted by area: what a
	// rock's colour match aims at.
	GroundMean [3]float64 `yaml:"ground_mean,flow"`
	// Coverage is each layer's share of the ground (sand, cracked, gravel,
	// rock).
	Coverage [4]float64 `yaml:"coverage,flow"`
	// FarMaterial is the material for the terrain's far backdrop
	// (terrain_far.go), with its own weights over the far square; empty when
	// the terrain has no backdrop.
	// RockMean is the rock layer's average colour as the splat draws it
	// (its tint in): what a wall chunk's colour match aims at, when its
	// stage asks to match the walls rather than the ground.
	RockMean    [3]float64 `yaml:"rock_mean,flow,omitempty"`
	FarMaterial string     `yaml:"far_material,omitempty"`
	FarWeights  string     `yaml:"far_weights,omitempty"`
	// Weights2 and FarWeights2 are an eight-layer splat's second weights
	// maps (layers 5–8), and Layers its layers — a region_material's
	// (docs/regions/PHASE_3.md); a terrain_material writes none of them.
	Weights2    string                `yaml:"weights2,omitempty"`
	FarWeights2 string                `yaml:"far_weights2,omitempty"`
	Layers      []RegionMaterialLayer `yaml:"layers,omitempty"`
	// Tiles are a region_material's per-tile materials (its per_tile
	// switch; region_material_tiles.go).
	Tiles []RegionTileMaterial `yaml:"tiles,omitempty"`
	// CoverCoverage is a terrain_material's cover layers' shares of the
	// ground (wet, ash, snow; terrain_material_cover.go), when it has any.
	CoverCoverage []float64 `yaml:"cover_coverage,flow,omitempty"`
	// Palette is a region material's ground layers when there are more than
	// eight (region_material_palette.go): every tile wears a material of at
	// most eight of them, sharing one atlas.
	Palette int `yaml:"palette,omitempty"`
}

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     TerrainMaterialKind,
		Label:    "Terrain material",
		Category: "Materials",
		// Served by the level generation doc's shared stages for every
		// region; never expanded per region (docs/regions/SPEC.md).
		Regional: procgen.RegionalShared,
		Version:  1,
		Doc: "The material a terrain's tiles wear: sand, cracked ground, gravel and rock layers from a Poly Haven " +
			"textures stage, splatted by the terrain's masks (rock on slopes and cliffs, triplanar and banded by the " +
			"strata; gravel in washes; cracked ground on flats; sand in between), height-blended. Also writes the " +
			"ground colour and height maps scatter and cliff_wall blend their rocks into.",
		Params:   terrainMaterialParams(),
		Run:      runTerrainMaterial,
		Preview:  previewTerrainMaterial,
		Load:     loadTerrainMaterial,
		Validate: validateTerrainMaterial,
	})
}

func terrainMaterialParams() []procgen.ParamDef {
	f, s, v2 := procgen.Float, procgen.String, procgen.Vec2
	unit := func(name, label string, def float64, doc string) procgen.ParamDef {
		return procgen.ParamDef{Name: name, Type: procgen.ParamFloat, Label: label, Default: f(def), Min: 0, Max: 1, Step: 0.01, Doc: doc}
	}
	layer := func(name, def, doc string) procgen.ParamDef {
		return procgen.ParamDef{Name: name, Type: procgen.ParamString, Default: s(def), Doc: doc}
	}
	return append([]procgen.ParamDef{
		{Name: "terrain", Type: procgen.ParamStage, StageKinds: TerrainSources, Default: procgen.StageRef(""),
			Doc: "The terrain stage whose masks place the layers."},
		{Name: "textures", Type: procgen.ParamStage, StageKinds: []string{PolyHavenTexturesKind}, Default: procgen.StageRef(""),
			Doc: "The Poly Haven textures stage the layers come from."},
		layer("sand", "sand", "The textures stage's layer for sand: deposition, gentle slopes, everything the others leave."),
		layer("cracked", "cracked", "Its layer for cracked ground, on the flats."),
		layer("gravel", "gravel", "Its layer for gravel, in the washes."),
		layer("rock", "cliff", "Its layer for rock, on steep slopes and cliffs. The textures stage's default `cliff` is rock_face, a natural "+
			"varnished face; its `rock`, red_sandstone_wall, is masonry — blocks and mortar — and reads as a brick wall on a slope."),
		{Name: "rock_slope", Type: procgen.ParamVec2, Label: "Rock slope (°)", Default: v2(32, 46), Min: 0, Max: 90, Step: 0.5,
			Doc: "Rock takes over between these slopes, degrees."},
		unit("cliff", "Cliff", 1, "How much the cliff mask adds rock on top of the slope rule."),
		{Name: "flat_slope", Type: procgen.ParamVec2, Label: "Flat slope (°)", Default: v2(5, 12), Min: 0, Max: 90, Step: 0.5,
			Doc: "Ground flatter than the first is flat (cracked ground's), steeper than the second is not."},
		{Name: "wash_flow", Type: procgen.ParamVec2, Label: "Wash flow", Default: v2(0.22, 0.42), Min: 0, Max: 1, Step: 0.01,
			Doc: "Gravel takes over between these levels of the flow mask."},
		{Name: "wash_width", Type: procgen.ParamFloat, Label: "Wash width (m)", Default: f(5), Min: 0, Max: 200, Step: 0.5,
			Doc: "The flow mask is a channel a few metres wide; it is blurred over this width first, so a wash is a gravel bed, not a line."},
		{Name: "sand_deposition", Type: procgen.ParamVec2, Label: "Sand deposition", Default: v2(0.25, 0.65), Min: 0, Max: 1, Step: 0.01,
			Doc: "Where the deposition mask passes these, the flats are sand rather than cracked ground."},
		unit("layer_match", "Layer match", 0.5, "How far the cracked, gravel and rock layers' colours are tinted toward the sand's hue (keeping their brightness), so scans shot in different places sit together."),
		unit("cracked_cover", "Cracked cover", 0.7, "How much of the flats cracked ground covers (the rest stays sand), in patches."),
		unit("breakup", "Breakup", 0.5, "How much noise bends the edges between layers."),
		{Name: "breakup_scale", Type: procgen.ParamFloat, Label: "Breakup scale (m)", Default: f(14), Min: 0.5, Max: 1000, Step: 0.5,
			Doc: "The size of the bends."},
		{Name: "blend", Type: procgen.ParamFloat, Label: "Height blend", Default: f(0.3), Min: 0.005, Max: 1, Step: 0.005,
			Doc: "How soft the transitions are in height-map units: low, the higher layer's tops win crisply."},
		{Name: "rock_triplanar", Type: procgen.ParamBool, Label: "Rock triplanar", Default: procgen.Bool(true),
			Doc: "Project the rock layer along all three axes, so cliffs are not smeared."},
		{Name: "rock_scale", Type: procgen.ParamFloat, Label: "Rock scale (m)", Default: f(6), Min: 0, Max: 1000, Step: 0.1,
			Doc: "Metres a repeat of the rock layer (0: the textures stage's). Larger than a ground texture's: a cliff is seen from far off, and a 2 m repeat reads as cloth."},
		unit("strata", "Strata bands", 0.4, "How strongly the terrain's strata colours band the rock."),
		{Name: "strata_warp", Type: procgen.ParamFloat, Label: "Strata warp (m)", Default: f(1.5), Min: 0, Max: 50, Step: 0.1,
			Doc: "How far the bands wander up and down."},
		unit("macro", "Macro variation", 0.18, "Slow brightness and warmth variation, which hides the textures' repeat at distance."),
		{Name: "macro_scale", Type: procgen.ParamFloat, Label: "Macro scale (m)", Default: f(45), Min: 1, Max: 10000, Step: 1,
			Doc: "The variation's patch size."},
		unit("streaks", "Desert varnish", 0.35, "Dark varnish streaks down the steep rock."),
		{Name: "detail", Type: procgen.ParamTexture, Label: "Detail normal", Default: procgen.Texture(""),
			Doc: "A texture whose normal map adds close-up grain everywhere (empty: none)."},
		{Name: "detail_scale", Type: procgen.ParamFloat, Label: "Detail scale (m)", Default: f(0.6), Min: 0.01, Max: 100, Step: 0.01,
			Doc: "Metres a repeat of the detail."},
		{Name: "normal_strength", Type: procgen.ParamFloat, Label: "Normal strength", Default: f(1), Min: 0, Max: 4, Step: 0.05,
			Doc: "Scales the layers' normal maps."},
		unit("ao", "AO", 0.8, "How much the layers' ambient occlusion darkens the ambient light."),
		{Name: "rock_tint", Added: true, Type: procgen.ParamColor, Label: "Rock tint", Default: procgen.Color(1, 1, 1), Min: 0, Max: 4,
			Doc: "Multiplies the rock layer's colour on top of the layer match: warmer and redder makes desert-varnished sandstone of a pale scan."},
		{Name: "sand_tint", Added: true, Type: procgen.ParamColor, Label: "Sand tint", Default: procgen.Color(1, 1, 1), Min: 0, Max: 4,
			Doc: "Multiplies the sand layer's colour (the everywhere layer: a savannah's grass): below 1 in blue deepens a " +
				"bleached scan toward gold, so a plain does not read pale straw at distance."},
		{Name: "cracked_tint", Added: true, Type: procgen.ParamColor, Label: "Cracked tint", Default: procgen.Color(1, 1, 1), Min: 0, Max: 4,
			Doc: "Multiplies the cracked layer's colour on top of the layer match: a volcanic preset darkens its bare lava with it, so the lobes read darker than the cinders round them."},
		{Name: "cap_rock", Added: true, Type: procgen.ParamFloat, Label: "Caprock", Default: f(0), Min: 0, Max: 1, Step: 0.01,
			Doc: "Bare rock on the high flats — a mesa's caprock, slickrock with sand in its hollows — rather than sand: how much, in patches. 0: the flats are sand and cracked ground at any height."},
		{Name: "cap_height", Added: true, Type: procgen.ParamVec2, Label: "Caprock height", Default: v2(0.5, 0.7), Min: 0, Max: 1, Step: 0.01,
			Doc: "Caprock fades in between these fractions of the terrain's height range."},
		{Name: "bedding", Added: true, Type: procgen.ParamFloat, Label: "Bedding relief", Default: f(0), Min: 0, Max: 2, Step: 0.01,
			Doc: "Ledges and laminations bent into the rock's normals on steep walls (the splat's bedding): each bed weathers back under " +
				"a sharp lip, so the heightfield's smooth walls read as layered rock in raking light. The recess as a fraction of a bed; 0: none."},
		{Name: "bedding_spacing", Added: true, Type: procgen.ParamFloat, Label: "Bedding spacing (m)", Default: f(1.2), Min: 0.05, Max: 50, Step: 0.05,
			Doc: "The beds' typical thickness."},
		{Name: "wall_normal", Added: true, Type: procgen.ParamFloat, Label: "Wall normal strength", Default: f(1), Min: 0, Max: 4, Step: 0.05,
			Doc: "Multiplies the rock layer's normal map on walls (its side projections) on top of Normal strength."},
		{Name: "anti_tile", Added: true, Type: procgen.ParamBool, Label: "Anti-tile", Default: procgen.Bool(false),
			Doc: "Lay every layer in shifted (on the ground, also turned) patches so no feature of a texture recurs on a grid " +
				"(the splat's anti_tile). About half again the texture reads; the far backdrop never takes it."},
		{Name: "macro_vertical", Added: true, Type: procgen.ParamFloat, Label: "Macro on walls", Default: f(0), Min: 0, Max: 1, Step: 0.05,
			Doc: "How much the macro variation also changes with height, so a wall is not one colour top to bottom."},
		{Name: "gravel_relief", Added: true, Type: procgen.ParamFloat, Label: "Gravel relief (m)", Default: f(0), Min: 0, Max: 0.5, Step: 0.005,
			Doc: "Parallax depth of the gravel layer (the splat's relief): its stones stand up off the ground instead of lying painted on it. 0: none."},
		{Name: "cracked_relief", Added: true, Type: procgen.ParamFloat, Label: "Cracked relief (m)", Default: f(0), Min: 0, Max: 0.5, Step: 0.005,
			Doc: "Parallax depth of the cracked-ground layer: its plates lift at their edges. 0: none."},
		{Name: "far_scale", Added: true, Type: procgen.ParamFloat, Label: "Far texture scale", Default: f(1), Min: 0.1, Max: 50, Step: 0.1,
			Doc: "Multiplies the layers' metres-a-repeat in the far backdrop's material: a 10 m rock repeat up a far peak a kilometre off " +
				"tiles into a regular stipple; 4–8 reads as strata instead."},
		{Name: "sand_ripple", Added: true, Type: procgen.ParamFloat, Label: "Sand ripples", Default: f(0), Min: 0, Max: 2, Step: 0.01,
			Doc: "Wind ripples on the sand layer (the splat's ripple): sinuous crests a hand's breadth apart in the normals, " +
				"a steep lee and a gentle windward face, patchy, on level ground, faded with distance. The ripples' slope; 0: none."},
		{Name: "sand_ripple_wavelength", Added: true, Type: procgen.ParamFloat, Label: "Ripple wavelength (m)", Default: f(0.14), Min: 0.02, Max: 5, Step: 0.01,
			Doc: "Crest to crest."},
		{Name: "sand_ripple_wind", Added: true, Type: procgen.ParamFloat, Label: "Ripple wind (°)", Default: f(60), Min: -360, Max: 360, Step: 1,
			Doc: "The way the wind blows across the ripples, degrees clockwise from +Z seen from above (as drift's wind)."},
		{Name: "wash_bars", Added: true, Type: procgen.ParamFloat, Label: "Wash bars", Default: f(0), Min: 0, Max: 1, Step: 0.01,
			Doc: "Sand bars in the washes: streaks of sand drawn out along the flow between gravel channels, so a wash is not one " +
				"gravel carpet. How much of the gravel they take; 0: none."},
		{Name: "wash_bar_size", Added: true, Type: procgen.ParamVec2, Label: "Wash bar size (m)", Default: v2(28, 5), Min: 0.5, Max: 500, Step: 0.5,
			Doc: "A bar's typical length along the flow and width across it."},
	}, coverParams()...)
}

func validateTerrainMaterial(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	var out []string
	if p.Str("terrain") == "" {
		out = append(out, "Choose a terrain stage")
	}
	if p.Str("textures") == "" {
		out = append(out, "Choose a Poly Haven textures stage")
	}
	return out
}

func loadTerrainMaterial(res *procgen.StageResult) (any, error) {
	data, err := os.ReadFile(filepath.Join(res.Dir, TerrainMaterialFile))
	if err != nil {
		return nil, err
	}
	var a TerrainMaterialArtifact
	if err := yaml.UnmarshalStrict(data, &a); err != nil {
		return nil, fmt.Errorf("%s: %w", TerrainMaterialFile, err)
	}
	return &a, nil
}

// splatLayerNames are the four layers in weight-channel order.
var splatLayerKeys = [4]string{"sand", "cracked", "gravel", "rock"}

// groundWeights is the four weights at every terrain sample (sand, cracked,
// gravel, rock), each 0..1 and summing to 1.
func groundWeights(t *terrain.Terrain, p procgen.Params, seed uint64, pool terrain.Pool) ([]float32, error) {
	g := t.Grid
	sz := g.S()
	masks := map[string][]float32{}
	for _, n := range []string{terrain.MaskSlope, terrain.MaskFlow, terrain.MaskDeposition, terrain.MaskCliff} {
		m, err := t.Mask(n)
		if err != nil {
			return nil, err
		}
		masks[n] = m
	}
	// The washes: the flow mask blurred over wash_width (two box passes),
	// renormalised so a channel's core keeps its strength.
	if r := int(math.Round(p.Float("wash_width") / g.Cell / 2)); r > 0 {
		f := boxBlur(masks[terrain.MaskFlow], sz, r)
		f = boxBlur(f, sz, r)
		// Blurred only: a one-sample channel alone (a rill down a slope)
		// fades below the wash band instead of drawing a line of gravel.
		out := make([]float32, len(f))
		for k := range f {
			out[k] = min(f[k]*2.5, 1)
		}
		masks[terrain.MaskFlow] = out
	}
	rs, fs := p.Vec2("rock_slope"), p.Vec2("flat_slope")
	wf, sd := p.Vec2("wash_flow"), p.Vec2("sand_deposition")
	var bars []float32
	if amt := p.Float("wash_bars"); amt > 0 {
		sz2 := p.Vec2("wash_bar_size")
		var err error
		bars, err = washBars(g, masks[terrain.MaskFlow], wf[0]*0.5, sz2[0], sz2[1], amt, noise.Hash64(seed, 77), pool)
		if err != nil {
			return nil, err
		}
	}
	cliffW, cover := p.Float("cliff"), p.Float("cracked_cover")
	brk, bs := p.Float("breakup"), math.Max(p.Float("breakup_scale"), 0.5)
	sm := noise.SmoothStep
	capAmt, capH := p.Float("cap_rock"), p.Vec2("cap_height")
	hlo, hhi := g.Range()
	hspan := math.Max(float64(hhi-hlo), 1e-3)
	out := make([]float32, 4*sz*sz)
	err := pool.Parallel(sz, func(j int) error {
		z := g.Z(j)
		for i := 0; i < sz; i++ {
			k := j*sz + i
			x := g.X(i)
			n := func(s uint64) float64 { return noise.FBM2(noise.Hash64(seed, s), x/bs, z/bs, 3, 0.5) }
			slope := float64(masks[terrain.MaskSlope][k])*90 + n(1)*brk*8
			flow := float64(masks[terrain.MaskFlow][k]) + n(2)*brk*0.12
			dep := float64(masks[terrain.MaskDeposition][k]) + n(3)*brk*0.08
			rock := math.Max(sm(rs[0], rs[1], slope), cliffW*float64(masks[terrain.MaskCliff][k]))
			if capAmt > 0 {
				// Caprock: high, not buried in sand, in patches.
				hf := (float64(g.H[k]-hlo) / hspan) + n(5)*brk*0.05
				capW := capAmt * sm(capH[0], capH[1], hf) * (1 - sm(0.15, 0.45, dep)) * sm(-0.35, 0.15, n(6))
				rock = math.Max(rock, capW)
			}
			rock = noise.Clamp(rock, 0, 1)
			gravel := sm(wf[0], wf[1], flow)
			bar := 0.0
			if bars != nil {
				bar = float64(bars[k])
			}
			flat := 1 - sm(fs[0], fs[1], slope)
			sand := sm(sd[0], sd[1], dep)
			patch := sm(-0.25, 0.25, n(4)+(cover-0.5)*1.2)
			cracked := flat * (1 - sand) * patch
			wR := rock
			rem := 1 - wR
			// A bar takes its share of the gravel straight to sand: in a wash
			// it is sediment, never cracked flat.
			wG := rem * gravel * (1 - bar)
			barSand := rem * gravel * bar
			rem -= wG + barSand
			wC := rem * cracked
			wS := rem - wC + barSand
			out[4*k], out[4*k+1], out[4*k+2], out[4*k+3] = float32(wS), float32(wC), float32(wG), float32(wR)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// A sample's softening: the masks' one-sample features (a terrace lip in
	// the cliff mask) otherwise draw as hard lines a sample wide.
	ch := make([]float32, sz*sz)
	for c := 0; c < 4; c++ {
		for k := range ch {
			ch[k] = out[4*k+c]
		}
		b := boxBlur(ch, sz, 1)
		for k := range b {
			out[4*k+c] = b[k]
		}
	}
	return out, nil
}

// washBars is each sample's sand-bar share (0..amt) in the washes: blobs of
// noise a bar's width across, smeared along the flow over a bar's length by
// a line integral convolution — each wash sample averages the noise along
// the downhill streamline through it, forward and back — so the bars are
// drawn out down the wash and bend with it. (Noise in a frame turned to the
// flow would be cheaper, but a frame that turns with position compresses
// and tears its noise a few hundred metres from the origin.) The averaging
// flattens the noise's spread, so the result is restretched by its own
// spread over the washes before the bars are cut from it. Only samples with
// flow above minFlow are traced: the rest of the map is never a wash.
func washBars(g *terrain.Grid, flow []float32, minFlow, length, width, amt float64, seed uint64, pool terrain.Pool) ([]float32, error) {
	sz := g.S()
	// The downhill direction, from heights blurred over a bar's width so a
	// cobble-sized bump does not turn the flow.
	r := max(int(math.Round(width/g.Cell/2)), 1)
	h := boxBlur(g.H, sz, r)
	h = boxBlur(h, sz, r)
	dir := make([]float32, 2*sz*sz)
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			i0, i1 := max(i-1, 0), min(i+1, sz-1)
			j0, j1 := max(j-1, 0), min(j+1, sz-1)
			gx := float64(h[j*sz+i1]-h[j*sz+i0]) / float64(i1-i0)
			gz := float64(h[j1*sz+i]-h[j0*sz+i]) / float64(j1-j0)
			l := math.Hypot(gx, gz)
			if l < 1e-6 {
				continue // flat: the streamline stops here
			}
			dir[2*(j*sz+i)], dir[2*(j*sz+i)+1] = float32(-gx/l), float32(-gz/l)
		}
	}
	steps := max(int(math.Round(length/g.Cell/2)), 1)
	src := func(x, z float64) float64 { return noise.FBM2(seed, x/width, z/width, 2, 0.5) }
	raw := make([]float32, sz*sz)
	in := make([]bool, sz*sz)
	err := pool.Parallel(sz, func(j int) error {
		for i := 0; i < sz; i++ {
			k := j*sz + i
			if float64(flow[k]) < minFlow {
				continue
			}
			in[k] = true
			sum, n := src(g.X(i), g.Z(j)), 1.0
			for _, sgn := range [2]float64{1, -1} {
				fi, fj := float64(i), float64(j)
				for s := 0; s < steps; s++ {
					ci, cj := int(fi+0.5), int(fj+0.5)
					if ci < 0 || cj < 0 || ci >= sz || cj >= sz {
						break
					}
					dx, dz := float64(dir[2*(cj*sz+ci)]), float64(dir[2*(cj*sz+ci)+1])
					if dx == 0 && dz == 0 {
						break
					}
					fi += sgn * dx
					fj += sgn * dz
					// Weighted toward the middle (a tent), so a bar's ends
					// taper instead of stopping square.
					w := 1 - float64(s+1)/float64(steps+1)
					sum += w * src(g.MinX+fi*g.Cell, g.MinZ+fj*g.Cell)
					n += w
				}
			}
			raw[k] = float32(sum / n)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var m, m2, cnt float64
	for k, ok := range in {
		if ok {
			v := float64(raw[k])
			m += v
			m2 += v * v
			cnt++
		}
	}
	out := make([]float32, sz*sz)
	if cnt == 0 {
		return out, nil
	}
	m /= cnt
	sd := math.Sqrt(math.Max(m2/cnt-m*m, 1e-12))
	for k, ok := range in {
		if ok {
			z := (float64(raw[k]) - m) / sd
			// About a third of the wash in bars, their edges a little soft.
			out[k] = float32(amt * noise.SmoothStep(0.2, 0.8, z))
		}
	}
	return out, nil
}

// strataRamp is the strata tints over lo..hi as a 256-texel ramp (RGBA, tint
// / 2 so mid-grey is 1), from the terrain's strata layers.
func strataRamp(m *terrain.StrataMeta, lo, hi float64) []uint8 {
	const n = 256
	out := make([]uint8, n*4)
	if len(m.Layers) == 0 {
		for i := range out {
			out[i] = 128
		}
		return out
	}
	// The layers' thickness-weighted mean colour, its brightness kept out of
	// the tint: the ramp says how each bed differs, not how light the rock is.
	var mean [3]float64
	tot := 0.0
	for _, l := range m.Layers {
		for c := 0; c < 3; c++ {
			mean[c] += l.Color[c] * l.Thickness
		}
		tot += l.Thickness
	}
	for c := range mean {
		mean[c] /= math.Max(tot, 1e-6)
	}
	period := tot
	tint := make([][3]float64, n)
	for i := 0; i < n; i++ {
		y := lo + (float64(i)+0.5)/n*(hi-lo)
		u := math.Mod(y-m.Base, math.Max(period, 1e-3))
		if u < 0 {
			u += period
		}
		li := 0
		for li < len(m.Layers)-1 && u >= m.Layers[li].Thickness {
			u -= m.Layers[li].Thickness
			li++
		}
		for c := 0; c < 3; c++ {
			// Clamped: a pale bed in a red stack would otherwise bleach the
			// rock to white and a dark one blacken it.
			tint[i][c] = noise.Clamp(m.Layers[li].Color[c]/math.Max(mean[c], 1e-3), 0.7, 1.3)
		}
	}
	// A little softness at the bed boundaries.
	for i := 0; i < n; i++ {
		var s [3]float64
		w := 0.0
		for d := -1; d <= 1; d++ {
			q := min(max(i+d, 0), n-1)
			for c := 0; c < 3; c++ {
				s[c] += tint[q][c]
			}
			w++
		}
		for c := 0; c < 3; c++ {
			out[4*i+c] = uint8(noise.Clamp(s[c]/w/2, 0, 1)*255 + 0.5)
		}
		out[4*i+3] = 255
	}
	return out
}

// layerMeans caches the layers' average colours by file and time.
var layerMeans sync.Map

func layerMean(name string) ([3]float64, bool) {
	st, err := os.Stat(filepath.Join(fileutil.TexturesDir(), name+".png"))
	if err != nil {
		return [3]float64{}, false
	}
	key := fmt.Sprintf("%s|%d|%d", name, st.Size(), st.ModTime().UnixNano())
	if v, ok := layerMeans.Load(key); ok {
		return v.([3]float64), true
	}
	m, ok := textureMean(name)
	if ok {
		layerMeans.Store(key, m)
	}
	return m, ok
}

// splatLayers resolves the four layer keys against the texture set.
func splatLayers(p procgen.Params, set *TextureSetManifest) ([4]*TextureLayer, error) {
	var out [4]*TextureLayer
	for i, k := range splatLayerKeys {
		name := p.Str(k)
		if name == "" {
			continue
		}
		l := set.Layer(name)
		if l == nil {
			return out, fmt.Errorf("the textures stage has no layer %q (for %s)", name, k)
		}
		out[i] = l
	}
	return out, nil
}

// coverLayers resolves the cover keys (wet, ash, snow) against the texture
// set: nil where a key is empty.
func coverLayers(p procgen.Params, set *TextureSetManifest) ([3]*TextureLayer, error) {
	var out [3]*TextureLayer
	for r := 0; r < 3; r++ {
		key := groundRoleKeys[roleWet+r]
		name := p.Str(key)
		if name == "" {
			continue
		}
		l := set.Layer(name)
		if l == nil {
			return out, fmt.Errorf("the textures stage has no layer %q (for %s)", name, key)
		}
		out[r] = l
	}
	return out, nil
}

// terrainMaterialMaps is everything the run writes, worked out.
type terrainMaterialMaps struct {
	weights, ground, height, strata []uint8
	coverage                        [4]float64
	// weights2 and coverCoverage are the cover layers' (wet, ash, snow in
	// RGB of weights2), nil without covers.
	weights2      []uint8
	coverCoverage []float64
	tints         [4][3]float64
	groundMean    [3]float64
	rockMean      [3]float64
}

func buildTerrainMaterialMaps(t *terrain.Terrain, p procgen.Params, layers [4]*TextureLayer, covers [3]*TextureLayer, seed uint64,
	pool terrain.Pool) (*terrainMaterialMaps, error) {
	g := t.Grid
	sz := g.S()
	// The four rules, and the covers laid over them when p names any
	// (terrain_material_cover.go): nc weights a sample, 4 or 7.
	w, nc, err := coverGroundWeights(t, p, seed, pool)
	if err != nil {
		return nil, err
	}
	var coverMeans [3][3]float64
	for r, l := range covers {
		coverMeans[r] = [3]float64{0.5, 0.5, 0.5}
		if l != nil {
			if m, ok := layerMean(l.Albedo); ok {
				coverMeans[r] = m
			}
		}
	}
	var means [4][3]float64
	for i, l := range layers {
		means[i] = [3]float64{0.5, 0.5, 0.5}
		if l != nil {
			if m, ok := layerMean(l.Albedo); ok {
				means[i] = m
			}
		}
	}
	// The layers' tints toward the sand's hue (layer_match), so a greenish
	// cracked-mud scan sits in red sand country; the ground map uses the
	// tinted colours, as the shader will draw them.
	tints := layerTints(means, layers, p.Float("layer_match"))
	rt, st, ct := p.Vec3("rock_tint"), p.Vec3("sand_tint"), p.Vec3("cracked_tint")
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
	out := &terrainMaterialMaps{weights: make([]uint8, 4*sz*sz), ground: make([]uint8, 4*sz*sz), height: make([]uint8, 4*sz*sz),
		tints: tints, rockMean: means[3]}
	if nc > 4 {
		out.weights2 = make([]uint8, 4*sz*sz)
		out.coverCoverage = make([]float64, 3)
	}
	lo, hi := g.Range()
	span := float64(hi - lo)
	if span == 0 {
		span = 1
	}
	for k := 0; k < sz*sz; k++ {
		var col [3]float64
		for c := 0; c < 4; c++ {
			wc := float64(w[nc*k+c])
			out.weights[4*k+c] = uint8(noise.Clamp(wc, 0, 1)*255 + 0.5)
			out.coverage[c] += wc
			for ch := 0; ch < 3; ch++ {
				col[ch] += wc * means[c][ch]
			}
		}
		for c := 4; c < nc; c++ {
			wc := float64(w[nc*k+c])
			out.weights2[4*k+c-4] = uint8(noise.Clamp(wc, 0, 1)*255 + 0.5)
			out.coverCoverage[c-4] += wc
			for ch := 0; ch < 3; ch++ {
				col[ch] += wc * coverMeans[c-4][ch]
			}
		}
		for ch := 0; ch < 3; ch++ {
			out.ground[4*k+ch] = uint8(noise.Clamp(col[ch], 0, 1)*255 + 0.5)
			out.groundMean[ch] += col[ch]
		}
		out.ground[4*k+3] = 255
		v := uint16(noise.Clamp(float64(g.H[k]-lo)/span, 0, 1)*65535 + 0.5)
		out.height[4*k], out.height[4*k+1], out.height[4*k+2], out.height[4*k+3] = uint8(v>>8), uint8(v), 0, 255
	}
	n := float64(sz * sz)
	for c := range out.coverage {
		out.coverage[c] /= n
	}
	for c := range out.coverCoverage {
		out.coverCoverage[c] /= n
	}
	for ch := range out.groundMean {
		out.groundMean[ch] /= n
	}
	out.strata = strataRamp(&t.Meta.Strata, float64(lo), float64(hi))
	return out, nil
}

func runTerrainMaterial(rc *procgen.RunContext) error {
	p := rc.Params
	t0 := time.Now()
	ta, err := rc.UpstreamArtifact(p.Str("terrain"))
	if err != nil {
		return err
	}
	t, ok := ta.(*terrain.Terrain)
	if !ok {
		return fmt.Errorf("stage %s is not a terrain stage", p.Str("terrain"))
	}
	sa, err := rc.UpstreamArtifact(p.Str("textures"))
	if err != nil {
		return err
	}
	set, ok := sa.(*TextureSetManifest)
	if !ok {
		return fmt.Errorf("stage %s is not a Poly Haven textures stage", p.Str("textures"))
	}
	layers, err := splatLayers(p, set)
	if err != nil {
		return err
	}
	covers, err := coverLayers(p, set)
	if err != nil {
		return err
	}
	rc.Progress(0.05)
	maps, err := buildTerrainMaterialMaps(t, p, layers, covers, rc.Seed, &rc.Env)
	if err != nil {
		return err
	}
	rc.Progress(0.6)
	g := t.Grid
	sz := g.S()
	files := []struct {
		name string
		w, h int
		pix  []uint8
	}{{"weights.png", sz, sz, maps.weights}, {"ground.png", sz, sz, maps.ground}, {"height.png", sz, sz, maps.height},
		{"strata.png", len(maps.strata) / 4, 1, maps.strata}}
	if maps.weights2 != nil {
		files = append(files, struct {
			name string
			w, h int
			pix  []uint8
		}{"weights2.png", sz, sz, maps.weights2})
	}
	if err := rc.Parallel(len(files), func(i int) error {
		data, err := terrain.EncodeRGBA(files[i].w, files[i].h, files[i].pix)
		if err != nil {
			return err
		}
		return rc.WriteFile(files[i].name, data)
	}); err != nil {
		return err
	}
	lo, hi := g.Range()
	// Texel centres on the samples: the rectangle runs half a cell past them.
	half := g.Cell / 2
	a := &TerrainMaterialArtifact{Version: 1, Material: genMaterialName(rc, ""),
		Weights: mapRef(rc, "weights.png"), Ground: mapRef(rc, "ground.png"), Height: mapRef(rc, "height.png"),
		Strata: mapRef(rc, "strata.png"),
		Min:    [2]float64{g.MinX - half, g.MinZ - half}, Max: [2]float64{g.X(sz-1) + half, g.Z(sz-1) + half},
		HeightLo: lo, HeightHi: hi, Coverage: maps.coverage}
	for c := range a.GroundMean {
		a.GroundMean[c] = round3(maps.groundMean[c])
		if layers[3] != nil {
			a.RockMean[c] = round3(maps.rockMean[c])
		}
	}
	for c := range a.Coverage {
		a.Coverage[c] = round3(a.Coverage[c])
	}
	if maps.weights2 != nil {
		a.Weights2 = mapRef(rc, "weights2.png")
		for _, c := range maps.coverCoverage {
			a.CoverCoverage = append(a.CoverCoverage, round3(c))
		}
	}
	d := terrainMaterialDoc(a, p, layers, covers, maps.tints)
	if err := writeMaterial(rc, d); err != nil {
		return err
	}
	if t.Meta.Far != nil {
		if err := writeFarMaterial(rc, t, p, layers, covers, maps.tints, a); err != nil {
			return err
		}
	}
	data, err := yaml.Marshal(a)
	if err != nil {
		return err
	}
	if err := rc.WriteFile(TerrainMaterialFile, data); err != nil {
		return err
	}
	rc.Artifact = a
	rc.Logf("material %s (set the terrain stage's material to it): %d² maps, sand %.0f%%, cracked %.0f%%, gravel %.0f%%, rock %.0f%%, in %s",
		a.Material, sz, 100*a.Coverage[0], 100*a.Coverage[1], 100*a.Coverage[2], 100*a.Coverage[3], time.Since(t0).Round(time.Millisecond))
	rc.Progress(1)
	return nil
}

// terrainMaterialDoc is the splat material over the maps in a.
func terrainMaterialDoc(a *TerrainMaterialArtifact, p procgen.Params, layers [4]*TextureLayer, covers [3]*TextureLayer, tints [4][3]float64) *material.Doc {
	d := &material.Doc{Name: a.Material, Domain: material.DomainSurface,
		Doc: "Generated terrain: sand, cracked ground, gravel and rock splatted by the terrain's masks"}
	e := d.AddEffect(material.KindSplat)
	e.Set("weights", material.TextureValue(a.Weights))
	e.Set("bounds_min", material.Vec2Value(mgl32.Vec2{float32(a.Min[0]), float32(a.Min[1])}))
	e.Set("bounds_max", material.Vec2Value(mgl32.Vec2{float32(a.Max[0]), float32(a.Max[1])}))
	e.Set("blend", material.FloatValue(float32(p.Float("blend"))))
	e.Set("normal_strength", material.FloatValue(float32(p.Float("normal_strength"))))
	e.Set("ao", material.FloatValue(float32(p.Float("ao"))))
	for i, l := range layers {
		if l == nil {
			continue
		}
		e.Set(material.SplatLayerParam(i, ""), material.TextureValue(l.Albedo))
		scale := l.Scale
		if i == 3 && p.Float("rock_scale") > 0 {
			scale = p.Float("rock_scale")
		}
		if scale <= 0 {
			scale = 3
		}
		e.Set(material.SplatLayerParam(i, "scale"), material.FloatValue(float32(round3(scale))))
		if i == 3 && p.Bool("rock_triplanar") {
			e.Set(material.SplatLayerParam(i, "triplanar"), material.BoolValue(true))
		}
		if p.Bool("anti_tile") {
			e.Set(material.SplatLayerParam(i, "anti_tile"), material.BoolValue(true))
		}
		if r := [4]float64{0, p.Float("cracked_relief"), p.Float("gravel_relief"), 0}[i]; r > 0 {
			e.Set(material.SplatLayerParam(i, "relief"), material.FloatValue(float32(r)))
		}
		if t := tints[i]; t != [3]float64{1, 1, 1} {
			e.Set(material.SplatLayerParam(i, "tint"), material.ColorValue(mgl32.Vec3{float32(round3(t[0])), float32(round3(t[1])), float32(round3(t[2]))}))
		}
	}
	// The covers (terrain_material_cover.go): layers 5–7 over weights2's
	// red, green and blue — an eight-layer splat — when there are any.
	if a.Weights2 != "" {
		e.Set("weights2", material.TextureValue(a.Weights2))
		for r, l := range covers {
			if l == nil {
				continue
			}
			i := material.SplatLayers + r
			e.Set(material.SplatLayerParam(i, ""), material.TextureValue(l.Albedo))
			scale := l.Scale
			if scale <= 0 {
				scale = 3
			}
			e.Set(material.SplatLayerParam(i, "scale"), material.FloatValue(float32(round3(scale))))
			if p.Bool("anti_tile") {
				e.Set(material.SplatLayerParam(i, "anti_tile"), material.BoolValue(true))
			}
		}
	}
	terrainMaterialExtras(d, e, a, p)
	return d
}

// terrainMaterialExtras sets a ground splat's material-level knobs from a
// terrain_material's params p — the strata ramp over a's height range, the
// bedding, the walls' normal strength — on its splat effect e, and adds
// the macro variation, varnish and detail effects to d. Shared with
// region_material (the lead environment's params).
func terrainMaterialExtras(d *material.Doc, e *material.Effect, a *TerrainMaterialArtifact, p procgen.Params) {
	if st := p.Float("strata"); st > 0 && a.Strata != "" {
		e.Set("strata", material.TextureValue(a.Strata))
		e.Set("strata_range", material.Vec2Value(mgl32.Vec2{a.HeightLo, a.HeightHi}))
		e.Set("strata_strength", material.FloatValue(float32(st)))
		e.Set("strata_warp", material.FloatValue(float32(p.Float("strata_warp"))))
	}
	if b := p.Float("bedding"); b > 0 {
		e.Set("bedding", material.FloatValue(float32(b)))
		e.Set("bedding_spacing", material.FloatValue(float32(p.Float("bedding_spacing"))))
	}
	if wn := p.Float("wall_normal"); wn != 1 {
		e.Set("wall_normal", material.FloatValue(float32(wn)))
	}
	if r := p.Float("sand_ripple"); r > 0 {
		e.Set("ripple", material.FloatValue(float32(r)))
		e.Set("ripple_wavelength", material.FloatValue(float32(p.Float("sand_ripple_wavelength"))))
		e.Set("ripple_angle", material.FloatValue(float32(p.Float("sand_ripple_wind"))))
	}
	if m := p.Float("macro"); m > 0 {
		e := d.AddEffect(material.KindMacroVariation)
		e.Set("strength", material.FloatValue(float32(m)))
		e.Set("scale", material.FloatValue(float32(p.Float("macro_scale"))))
		if v := p.Float("macro_vertical"); v > 0 {
			e.Set("vertical", material.FloatValue(float32(v)))
		}
	}
	if s := p.Float("streaks"); s > 0 {
		e := d.AddEffect(material.KindStreaks)
		e.Set("strength", material.FloatValue(float32(s)))
	}
	if det := p.Str("detail"); det != "" {
		e := d.AddEffect(material.KindDetailNormal)
		e.Set("map", material.TextureValue(det))
		e.Set("scale", material.FloatValue(float32(p.Float("detail_scale"))))
	}
}

func previewTerrainMaterial(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
	p := pc.Params
	up := pc.Upstream(p.Str("terrain"))
	if up == nil {
		return nil, fmt.Errorf("the terrain stage %q has no preview", p.Str("terrain"))
	}
	t, ok := up.Artifact.(*terrain.Terrain)
	if !ok {
		return nil, fmt.Errorf("stage %s is not a terrain stage", p.Str("terrain"))
	}
	var layers [4]*TextureLayer
	if tp := pc.Upstream(p.Str("textures")); tp != nil {
		if set, ok := tp.Artifact.(*TextureSetManifest); ok {
			layers, _ = splatLayers(p, set)
		}
	}
	if layers == ([4]*TextureLayer{}) {
		// The textures stage's preview names what is imported, not a
		// manifest; read the set's manifest from its last run if there is one.
		layers = previewLayersFromDisk(pc, p)
	}
	maps, err := buildTerrainMaterialMaps(t, p, layers, [3]*TextureLayer{}, pc.Seed, &pc.Env)
	if err != nil {
		return nil, err
	}
	sz := t.Grid.S()
	b := up.FrameBounds()
	out := &procgen.PreviewData{Bounds: b}
	// The weights in false colour: sand yellow, cracked brown, gravel blue,
	// rock red.
	falseCol := [4][3]float64{{0.95, 0.85, 0.4}, {0.6, 0.35, 0.15}, {0.3, 0.55, 1}, {0.85, 0.2, 0.2}}
	wimg := make([]uint8, 4*sz*sz)
	for k := 0; k < sz*sz; k++ {
		var c [3]float64
		for l := 0; l < 4; l++ {
			wl := float64(maps.weights[4*k+l]) / 255
			for ch := 0; ch < 3; ch++ {
				c[ch] += wl * falseCol[l][ch]
			}
		}
		// The covers: wet teal, ash charcoal, snow white.
		for l := 0; maps.weights2 != nil && l < 3; l++ {
			wl := float64(maps.weights2[4*k+l]) / 255
			for ch := 0; ch < 3; ch++ {
				c[ch] += wl * [3][3]float64{{0.2, 0.7, 0.65}, {0.2, 0.2, 0.22}, {1, 1, 1}}[l][ch]
			}
		}
		for ch := 0; ch < 3; ch++ {
			wimg[4*k+ch] = uint8(noise.Clamp(c[ch], 0, 1)*255 + 0.5)
		}
		wimg[4*k+3] = 255
	}
	out.Images = append(out.Images,
		procgen.ImageLayer{Name: "Ground colour", W: sz, H: sz, RGBA: maps.ground, Bounds: b},
		procgen.ImageLayer{Name: "Layers", W: sz, H: sz, RGBA: wimg, Bounds: b})
	for _, m := range terrainMeshes(up) {
		m.Image = "Ground colour"
		out.Meshes = append(out.Meshes, m)
	}
	out.Notes = append(out.Notes,
		fmt.Sprintf("sand %.0f%%, cracked %.0f%%, gravel %.0f%%, rock %.0f%% (Layers: sand yellow, cracked brown, gravel blue, rock red)",
			100*maps.coverage[0], 100*maps.coverage[1], 100*maps.coverage[2], 100*maps.coverage[3]),
		"the terrain stage's tiles wear it when its material is gen_"+safeName(pc.Doc.Name, "gen")+"_"+safeName(pc.Stage.ID, "stage"))
	return out, nil
}

// previewLayersFromDisk is the textures stage's layers from its last run's
// manifest (nil layers when it has not run).
func previewLayersFromDisk(pc *procgen.PreviewContext, p procgen.Params) [4]*TextureLayer {
	var out [4]*TextureLayer
	id := p.Str("textures")
	if id == "" {
		return out
	}
	set, err := LoadTextureSet(filepath.Join(procgen.OutDirFor(pc.Doc, procgen.Options{}), id))
	if err != nil {
		return out
	}
	out, _ = splatLayers(p, set)
	return out
}

// boxBlur is vals (sz² row-major) box-blurred r samples each way, rows then
// columns, edges clamped.
func boxBlur(vals []float32, sz, r int) []float32 {
	tmp := make([]float32, len(vals))
	out := make([]float32, len(vals))
	pass := func(src, dst []float32, at func(a, b int) int) {
		for a := 0; a < sz; a++ {
			var sum float32
			for b := -r; b <= r; b++ {
				sum += src[at(a, min(max(b, 0), sz-1))]
			}
			n := float32(2*r + 1)
			for b := 0; b < sz; b++ {
				dst[at(a, b)] = sum / n
				sum += src[at(a, min(b+r+1, sz-1))] - src[at(a, max(b-r, 0))]
			}
		}
	}
	pass(vals, tmp, func(row, col int) int { return row*sz + col })
	pass(tmp, out, func(col, row int) int { return row*sz + col })
	return out
}

// layerTints is each layer's tint toward the first (sand) layer's chroma by
// amount, brightness kept (luma-normalised ratios, as color_grade's match).
func layerTints(means [4][3]float64, layers [4]*TextureLayer, amount float64) [4][3]float64 {
	var out [4][3]float64
	for i := range out {
		out[i] = [3]float64{1, 1, 1}
	}
	if amount <= 0 || layers[0] == nil {
		return out
	}
	chroma := func(c [3]float64) [3]float64 {
		y := 0.299*c[0] + 0.587*c[1] + 0.114*c[2]
		if y <= 1e-4 {
			return [3]float64{1, 1, 1}
		}
		return [3]float64{c[0] / y, c[1] / y, c[2] / y}
	}
	target := chroma(means[0])
	for i := 1; i < 4; i++ {
		if layers[i] == nil {
			continue
		}
		src := chroma(means[i])
		for c := 0; c < 3; c++ {
			out[i][c] = 1 + (target[c]/math.Max(src[c], 1e-3)-1)*amount
		}
	}
	return out
}
