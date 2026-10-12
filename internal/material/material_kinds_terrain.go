package material

// material_kinds_terrain.go — the terrain and rock effects (docs/canyon/
// PHASE_5.md): the surface effects a generated landscape is painted with.
//
// The effects in material_kinds.go are the engine's old per-object fields
// moved into a schema. These are new, and they share one property those do
// not: they project in WORLD space rather than reading the mesh's own
// texture coordinates. A heightfield tile's UVs are a flat sheet stretched
// down every cliff, a generated rock has box-projected UVs that seam on
// every edge, and a scanned rock's UVs are an atlas that knows nothing of
// the ground it is sitting on — so everything here is worked out from where
// the fragment IS:
//
//   - triplanar: the surface's own texture and normal map, projected along
//     the three world axes and blended by the normal. For UV-less generated
//     meshes. The side projections stand upright (v runs down world height),
//     so a texture with horizontal bedding keeps it level, as terrain_frag's
//     triplanarGround does for the block terrain.
//   - splat: up to four texture layers (each with its _normal, _height and
//     _ao siblings) mixed by a weights texture laid over the terrain's world
//     rectangle, height-blended so sand fills the gaps between gravel stones
//     rather than fading through them. A layer can be triplanar — the rock of
//     a cliff — and a strata ramp can band the triplanar layers by height.
//     This is what a terrain stage's tiles wear.
//   - ground_blend: a rock fades toward the colour of the ground under it,
//     within a band above the ground's height there: the seam where a rock
//     meets the terrain stops being a line between two photographs.
//   - streaks: desert varnish — dark vertical streaks down steep faces and a
//     darkening under overhangs, in world space.
//   - detail_normal: a second, small normal map close up.
//   - macro_variation: slow world-space noise on the albedo, which breaks a
//     tiling texture's repeat at distance.
//   - color_grade: hue, saturation, a luminance-preserving tint, and a
//     "match" that carries the texture's own mean colour toward a target
//     colour — what turns grey-brown granite scans into red sandstone
//     country rock without painting them.
//
// Each is a uniform branch in fragment.glsl (surface_fx.glsl), taken only by
// surfaces wearing a material that carries it, so a level that names none
// draws exactly as it did. The textures they sample are bound by
// gfx/material_fx.go.

import "github.com/go-gl/mathgl/mgl32"

// The terrain and rock effect kind names.
const (
	KindTriplanar      = "triplanar"
	KindSplat          = "splat"
	KindGroundBlend    = "ground_blend"
	KindStreaks        = "streaks"
	KindDetailNormal   = "detail_normal"
	KindMacroVariation = "macro_variation"
	KindColorGrade     = "color_grade"
)

// SplatLayers is how many layers a splat mixes: one per channel of its
// weights texture.
const SplatLayers = 4

// SplatLayerParam is splat layer i's (0-based) parameter called field:
// "layer1", "layer1_scale" and so on. Exported for the generator that writes
// them (procgen/stages/terrain_material.go).
func SplatLayerParam(i int, field string) string {
	n := "layer" + string(rune('1'+i))
	if field == "" {
		return n
	}
	return n + "_" + field
}

// splatParams is the splat effect's schema: the weights and their world
// rectangle, the four layers, the blend and the strata ramp.
func splatParams() []ParamDef {
	out := []ParamDef{
		{Name: "weights", Type: ParamTexture, Label: "Weights",
			Doc: "Each layer's weight in one channel: red the first layer, green the second, blue the third, alpha " +
				"the fourth. Laid over the world rectangle below. A texture name, or a resources-relative path " +
				"without .png (generated/canyon/ground_mat/weights) — generators write theirs beside their output.",
			Default: TextureValue("")},
		{Name: "bounds_min", Type: ParamVec2, Label: "Bounds min",
			Doc: "The weights texture's first texel's corner, world X and Z.", Default: Vec2Value(mgl32.Vec2{-512, -512})},
		{Name: "bounds_max", Type: ParamVec2, Label: "Bounds max",
			Doc: "Its opposite corner.", Default: Vec2Value(mgl32.Vec2{512, 512})},
		{Name: "blend", Type: ParamFloat, Label: "Height blend",
			Doc: "How soft the transition between layers is, in height-map units. Low: the higher layer's tops win " +
				"crisply (stones poke through sand); high: a plain cross-fade.",
			Default: FloatValue(0.15), Min: 0.005, Max: 1},
		{Name: "normal_strength", Type: ParamFloat, Label: "Normal strength",
			Doc: "Scales every layer's normal map.", Default: FloatValue(1), Min: 0, Max: 4},
		{Name: "ao", Type: ParamFloat, Label: "AO",
			Doc:     "How much of the layers' ambient occlusion (<layer>_ao) darkens the ambient light.",
			Default: FloatValue(1), Min: 0, Max: 1},
	}
	for i := 0; i < SplatLayers; i++ {
		ch := []string{"red", "green", "blue", "alpha"}[i]
		out = append(out,
			ParamDef{Name: SplatLayerParam(i, ""), Type: ParamTexture, Label: "Layer " + string(rune('1'+i)),
				Doc: "The layer's albedo, weighted by the weights texture's " + ch + ". Its <name>_normal, <name>_height " +
					"and <name>_ao are used when they exist (a Poly Haven import writes all three).",
				Default: TextureValue("")},
			ParamDef{Name: SplatLayerParam(i, "scale"), Type: ParamFloat, Label: "  Scale (m)",
				Doc: "Metres a repeat.", Default: FloatValue(3), Min: 0.01, Max: 1000},
			ParamDef{Name: SplatLayerParam(i, "triplanar"), Type: ParamBool, Label: "  Triplanar",
				Doc: "Project along all three axes rather than straight down — for rock on slopes and cliffs, which " +
					"a top-down projection smears.", Default: BoolValue(false)},
			ParamDef{Name: SplatLayerParam(i, "height"), Type: ParamFloat, Label: "  Height",
				Doc:     "How much its height map lifts it over its neighbours in the blend.",
				Default: FloatValue(1), Min: 0, Max: 4},
			ParamDef{Name: SplatLayerParam(i, "tint"), Type: ParamColor, Label: "  Tint",
				Doc: "Multiplies its albedo.", Default: ColorValue(mgl32.Vec3{1, 1, 1}), Min: 0, Max: 4},
		)
	}
	out = append(out,
		ParamDef{Name: "strata", Type: ParamTexture, Label: "Strata ramp",
			Doc: "A ramp of tints read by world height (left the bottom of the range, right the top): the bedding " +
				"of the rock, banding the triplanar layers. Multiplied in, so mid-grey is no change. Empty: none.",
			Default: TextureValue("")},
		ParamDef{Name: "strata_range", Type: ParamVec2, Label: "Strata range",
			Doc: "The world heights the ramp spans (bottom, top).", Default: Vec2Value(mgl32.Vec2{0, 100})},
		ParamDef{Name: "strata_strength", Type: ParamFloat, Label: "Strata strength",
			Doc: "0 ignores the ramp; 1 multiplies it in fully.", Default: FloatValue(0.6), Min: 0, Max: 1},
		ParamDef{Name: "strata_warp", Type: ParamFloat, Label: "Strata warp (m)",
			Doc:     "How far noise bends the bands up and down, so they wander like real bedding.",
			Default: FloatValue(1.5), Min: 0, Max: 50},
		ParamDef{Name: "bedding", Type: ParamFloat, Label: "Bedding relief",
			Doc: "Ledges and laminations bent into the triplanar layers' normals on steep faces: each bed weathers " +
				"back under a sharp lip, so a smooth wall reads as layered rock in raking light. The depth of a " +
				"bed's recess as a fraction of its thickness; 0 is off.",
			Default: FloatValue(0), Min: 0, Max: 2},
		ParamDef{Name: "bedding_spacing", Type: ParamFloat, Label: "Bedding spacing (m)",
			Doc:     "The beds' typical thickness; laminations run inside each at about a third of it.",
			Default: FloatValue(1.2), Min: 0.05, Max: 50},
		ParamDef{Name: "wall_normal", Type: ParamFloat, Label: "Wall normal strength",
			Doc: "Multiplies the triplanar layers' normal maps on their side projections only (walls), on top of " +
				"Normal strength: a wall seen across a canyon wants more relief than ground seen underfoot.",
			Default: FloatValue(1), Min: 0, Max: 4},
	)
	return out
}

func init() {
	RegisterKind(&Kind{
		Name: KindTriplanar, Domains: surfaceOnly, Label: "Triplanar",
		Doc: "Project the surface's texture (and its normal map) along the three world axes and blend by the " +
			"normal, instead of reading the mesh's UVs. For generated meshes with no useful UVs: rocks, cliffs, " +
			"terrain. Placement-independent, so two rocks side by side continue each other's texture.",
		Params: []ParamDef{
			{Name: "scale", Type: ParamFloat, Label: "Scale (m)",
				Doc: "Metres a repeat.", Default: FloatValue(2), Min: 0.01, Max: 1000},
			{Name: "sharpness", Type: ParamFloat, Label: "Sharpness",
				Doc: "How quickly one projection hands over to the next as the surface turns: 1 blurs broad " +
					"seams, 16 makes them narrow.", Default: FloatValue(4), Min: 1, Max: 32},
			{Name: "upright", Type: ParamBool, Label: "Upright",
				Doc: "The side projections stand upright, the texture's top toward the sky — bedding in a rock " +
					"texture stays level, as real strata do. Off: they lie as the axes fall.",
				Default: BoolValue(true)},
		},
	})

	RegisterKind(&Kind{
		Name: KindSplat, Domains: surfaceOnly, Label: "Splat (terrain layers)",
		Doc: "Mix up to four texture layers by a weights texture laid over a world rectangle — sand, cracked " +
			"ground, gravel, rock — height-blended by each layer's _height map, with its normal map and AO. " +
			"What a generated terrain's tiles wear. The surface's own texture is not drawn.",
		Params: splatParams(),
	})

	RegisterKind(&Kind{
		Name: KindGroundBlend, Domains: surfaceOnly, Label: "Ground blend",
		Doc: "Fade the surface toward the colour of the ground beneath it within a band above the ground — a " +
			"rock's foot takes the sand's colour, which hides the seam where a placed mesh meets the terrain. " +
			"Reads a terrain's ground colour and height maps over its world rectangle.",
		Params: []ParamDef{
			{Name: "ground", Type: ParamTexture, Label: "Ground colour",
				Doc: "The ground's colour over the rectangle (a terrain_material stage writes one). A texture " +
					"name or a resources-relative path without .png.", Default: TextureValue("")},
			{Name: "height", Type: ParamTexture, Label: "Ground height",
				Doc: "The ground's height over the rectangle, 16 bits in red (high byte) and green (low byte) " +
					"between Height range's two values.", Default: TextureValue("")},
			{Name: "bounds_min", Type: ParamVec2, Label: "Bounds min",
				Doc: "The maps' first texel's corner, world X and Z.", Default: Vec2Value(mgl32.Vec2{-512, -512})},
			{Name: "bounds_max", Type: ParamVec2, Label: "Bounds max",
				Doc: "Their opposite corner.", Default: Vec2Value(mgl32.Vec2{512, 512})},
			{Name: "height_range", Type: ParamVec2, Label: "Height range",
				Doc: "The world heights 0 and 65535 in the height map stand for.", Default: Vec2Value(mgl32.Vec2{0, 100})},
			{Name: "band", Type: ParamFloat, Label: "Band (m)",
				Doc: "How far above the ground the blend reaches.", Default: FloatValue(0.6), Min: 0.01, Max: 20},
			{Name: "breakup", Type: ParamFloat, Label: "Breakup",
				Doc:     "How much noise ragged the band's top edge (0: a level line; real dust settles unevenly).",
				Default: FloatValue(0.5), Min: 0, Max: 1},
			{Name: "strength", Type: ParamFloat, Label: "Strength",
				Doc: "How much of the ground colour the foot takes at the ground itself.", Default: FloatValue(0.85), Min: 0, Max: 1},
			{Name: "contact", Type: ParamFloat, Label: "Contact shadow",
				Doc:     "A darkening right at the ground line, where the rock shades the sand it sits in.",
				Default: FloatValue(0.25), Min: 0, Max: 1},
		},
	})

	RegisterKind(&Kind{
		Name: KindStreaks, Domains: surfaceOnly, Label: "Streaks (desert varnish)",
		Doc: "Dark vertical streaks down steep faces — the manganese varnish water leaves running over desert " +
			"rock — and a darkening under overhangs. World-space, so a wall of many pieces streaks as one.",
		Params: []ParamDef{
			{Name: "strength", Type: ParamFloat, Label: "Strength",
				Doc: "How dark the streaks get at their darkest.", Default: FloatValue(0.5), Min: 0, Max: 1},
			{Name: "scale", Type: ParamFloat, Label: "Width (m)",
				Doc: "Roughly how wide a streak is.", Default: FloatValue(1.5), Min: 0.05, Max: 100},
			{Name: "length", Type: ParamFloat, Label: "Length (m)",
				Doc: "Roughly how far a streak runs down the face.", Default: FloatValue(12), Min: 0.1, Max: 500},
			{Name: "color", Type: ParamColor, Label: "Colour",
				Doc: "The varnish's colour, multiplied in.", Default: ColorValue(mgl32.Vec3{0.32, 0.22, 0.17}), Min: 0, Max: 1},
			{Name: "overhang", Type: ParamFloat, Label: "Overhang",
				Doc: "How much faces turned toward the ground darken.", Default: FloatValue(0.4), Min: 0, Max: 1},
		},
	})

	RegisterKind(&Kind{
		Name: KindDetailNormal, Domains: surfaceOnly, Label: "Detail normal",
		Doc: "A second normal map at a small scale for close-up grain, projected in world space and faded out " +
			"with distance where it would only shimmer.",
		Params: []ParamDef{
			{Name: "map", Type: ParamTexture, Label: "Map",
				Doc: "The texture whose <name>_normal is the detail (or a normal map itself).", Default: TextureValue("")},
			{Name: "scale", Type: ParamFloat, Label: "Scale (m)",
				Doc: "Metres a repeat.", Default: FloatValue(0.5), Min: 0.01, Max: 100},
			{Name: "strength", Type: ParamFloat, Label: "Strength",
				Doc: "How much it bends the normal.", Default: FloatValue(0.5), Min: 0, Max: 2},
			{Name: "distance", Type: ParamFloat, Label: "Fade distance (m)",
				Doc: "Gone by this far from the eye.", Default: FloatValue(30), Min: 1, Max: 1000},
		},
	})

	RegisterKind(&Kind{
		Name: KindMacroVariation, Domains: surfaceOnly, Label: "Macro variation",
		Doc: "Slow world-space noise on the albedo's brightness and warmth. A texture repeating every few metres " +
			"reads as a grid from a hundred metres away; this breaks the grid up.",
		Params: []ParamDef{
			{Name: "scale", Type: ParamFloat, Label: "Scale (m)",
				Doc: "The size of the variation's patches.", Default: FloatValue(40), Min: 0.5, Max: 10000},
			{Name: "strength", Type: ParamFloat, Label: "Strength",
				Doc: "How far the brightness swings either way.", Default: FloatValue(0.25), Min: 0, Max: 1},
			{Name: "warmth", Type: ParamFloat, Label: "Warmth",
				Doc: "How far the colour swings between warmer and cooler with it.", Default: FloatValue(0.3), Min: 0, Max: 1},
		},
	})

	RegisterKind(&Kind{
		Name: KindColorGrade, Domains: surfaceOnly, Label: "Colour grade",
		Doc: "Regrade the albedo: a hue turn, a saturation, a tint that keeps brightness, and a MATCH that carries " +
			"the texture's own average colour toward a target colour — every texel moved by the same ratio, so " +
			"the texture's detail and its variation survive. Turns grey granite scans into red-rock country " +
			"without painting them.",
		Params: []ParamDef{
			{Name: "match", Type: ParamFloat, Label: "Match",
				Doc:     "How far the texture's average colour moves toward Target (0: not at all; 1: all the way).",
				Default: FloatValue(0), Min: 0, Max: 1},
			{Name: "target", Type: ParamColor, Label: "Target",
				Doc: "The colour matched toward — usually the ground's.", Default: ColorValue(mgl32.Vec3{0.62, 0.38, 0.27}), Min: 0, Max: 1},
			{Name: "source", Type: ParamColor, Label: "Source",
				Doc:     "The colour taken to be the texture's average. Black: measure it from the texture itself.",
				Default: ColorValue(mgl32.Vec3{0, 0, 0}), Min: 0, Max: 1},
			{Name: "hue", Type: ParamFloat, Label: "Hue (°)",
				Doc: "Turns every colour about the grey axis.", Default: FloatValue(0), Min: -180, Max: 180},
			{Name: "saturation", Type: ParamFloat, Label: "Saturation",
				Doc: "1 unchanged, 0 grey, above 1 richer.", Default: FloatValue(1), Min: 0, Max: 3},
			{Name: "tint", Type: ParamColor, Label: "Tint",
				Doc: "Multiplied in with its brightness divided out, so it changes the colour and not how light the " +
					"surface is. White: none.", Default: ColorValue(mgl32.Vec3{1, 1, 1}), Min: 0, Max: 4},
			{Name: "brightness", Type: ParamFloat, Label: "Brightness",
				Doc: "A plain multiply, last.", Default: FloatValue(1), Min: 0, Max: 4},
		},
	})
}
