package stages

// grass_field.go — the `grass_field` stage: Poly Haven grass, flowers and
// reeds as a dense, GPU-instanced sward (docs/regions/PRESETS_2B.md).
//
// The scatter stage places a Poly Haven grass clump as one scenery
// placement, which costs the Nanite cull and the CPU frame per clump; at
// ~1 ms per thousand in view a preset could afford a few thousand clumps
// per km² — patches, never a prairie (docs/regions/PRESETS_2.md). This
// stage writes the same clumps for the grass instancer instead:
//
//   - a KIT (grass.fgk, internal/grassfield): every clump the rules name —
//     a polyhaven_plants stage's variants, filtered by size and name — as
//     up to three small meshes (as imported, capped at max_triangles; then
//     thinned to ~35% and ~10% of that by keeping whole blades, each
//     widened), with its `_cov` albedo and its wind;
//   - per terrain tile, a FIELD (grass_<i>_<j>.fgrass): the tile's clumps
//     from every rule (procgen/place grass.go: a jittered lattice at the
//     rule's density, each point kept with the scatter-style weight of its
//     slope, height and mask bands, clumping noise, the region's share and
//     an optional shore band), 12 bytes each, sorted into 16 m cells by
//     kind in a fixed hashed order;
//   - GROUND COVER (grass_field_cover.go; docs/terrain-paint/PHASE_P3.md):
//     a rule may lay its clumps along the ground (`orient: ground`), lean
//     them (`tilt_jitter`), push them into it (`sink`), keep them still
//     (`wind: false`) or be CARDS — flat alpha-cut patches of a Poly Haven
//     ground texture cut out by its own height map, for leaf litter;
//   - per field, ONE level volume with a `grass:` block naming the file
//     (world/volume_grass.go), boxed to the tile, `chunk: auto`, owned by
//     the stage's ID prefix like every placement, so the splice replaces
//     exactly what the last run wrote.
//
// render/grass*.go draws the fields: per-cell culling, a thinning by
// distance in that hashed order (survivors grow to keep the cover), wind,
// alpha-to-coverage cutout, shadows near the camera. Nothing here touches
// the engine's foliage biomes (.fbio) — the presets' plants are Poly Haven
// only (the user's direction).
//
// Regional: PerRegion, like scatter — each region's run places its own
// share (the region's weight multiplies every rule's), keyed by world
// position, so two regions' fields on one tile are two volumes that blend
// across the band.

import (
	"fmt"

	"foundry/internal/procgen"
	"foundry/internal/procgen/place"
)

// GrassFieldKind is the stage's name.
const GrassFieldKind = "grass_field"

// grassFieldVersion is the stage's version (its output format).
const grassFieldVersion = 1

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name: GrassFieldKind,
		// Per-tile reruns after a paint stroke (docs/terrain-paint/PHASE_P2.md).
		Incremental: true,
		Label:       "Grass field",
		Category:    "Placement",
		Regional:    procgen.RegionalPerRegion,
		Version:     grassFieldVersion,
		Doc: "Poly Haven grass, flowers and reeds as a dense GPU-instanced sward: each rule names a polyhaven_plants " +
			"stage's clumps and where they grow (slope, height and mask bands, clumping, the shore of standing water), " +
			"at a density per square metre. Writes a kit of small LOD meshes and one compact instance file per terrain " +
			"tile, placed as one grass volume per tile — not a scenery placement per clump.",
		Params:   grassFieldParams(),
		Run:      runGrassField,
		Preview:  previewGrassField,
		Load:     loadGrassField,
		Validate: validateGrassField,
	})
}

func grassRuleFields() []procgen.ParamDef {
	f, v2, s := procgen.Float, procgen.Vec2, procgen.String
	fields := []procgen.ParamDef{
		{Name: "name", Type: procgen.ParamString, Default: s("grass"), Doc: "The rule's name, for the log."},
		{Name: "enabled", Type: procgen.ParamBool, Default: procgen.Bool(true), Doc: "Off skips the rule."},
		{Name: "plants", Type: procgen.ParamStage, StageKinds: []string{PolyHavenPlantsKind, PolyHavenRocksKind}, Default: procgen.StageRef(""),
			Doc: "The polyhaven_plants stage whose clumps this rule grows (its variants: each Poly Haven piece is one clump kind). " +
				"A polyhaven_rocks stage works too — pebbles and stones as ground cover. Not needed for a card rule."},
		{Name: "pieces", Type: procgen.ParamString, Default: s(""),
			Doc: "Keep only the pieces whose names contain one of these words (space separated): `tall` takes " +
				"grass_medium_01's tall_a/b/c. Empty: every piece."},
		{Name: "min_size", Type: procgen.ParamFloat, Label: "Min piece size (m)", Default: f(0), Min: 0, Max: 100, Step: 0.01,
			Doc: "Leave out pieces smaller than this (their largest side as scanned)."},
		{Name: "max_triangles", Type: procgen.ParamInt, Label: "Max triangles", Default: procgen.Int(1200), Min: 16, Max: 100000,
			Doc: "The near mesh's cap: a clump of more triangles is thinned (whole blades kept, widened) to this. " +
				"Every clump within ~15 m draws it, so keep it small."},
		{Name: "density", Type: procgen.ParamFloat, Label: "Density (/m²)", Default: f(1.5), Min: 0, Max: 50, Step: 0.05,
			Doc: "Clumps per square metre at full weight. A tallgrass sward of 1–1.5 m clumps wants 1.5–3."},
		{Name: "scale", Type: procgen.ParamVec2, Default: v2(3, 5), Min: 0.01, Max: 100, Step: 0.01,
			Doc: "The clumps' scale range. Poly Haven's grass pieces are 0.1–0.4 m as scanned (about a quarter of life size)."},
		{Name: "edge_shrink", Type: procgen.ParamFloat, Label: "Edge shrink", Default: f(0.4), Min: 0, Max: 1, Step: 0.01,
			Doc: "How much shorter a clump is where its weight is low: a patch's margin is shorter, sparser grass."},
	}
	fields = append(fields, bandParams()...)
	// The paint band (paint_bands.go): off until a layer is named.
	fields = append(fields, paintRuleFields()...)
	// Orientation along a direction field (paint_orient.go, phase P4):
	// off until orient_to is set.
	fields = append(fields, orientRuleFields()...)
	fields = append(fields,
		procgen.ParamDef{Name: "shore", Type: procgen.ParamVec2, Label: "Shore band (m)", Default: v2(0, 0), Min: 0, Max: 1000, Step: 0.1,
			Doc: "Grow only this far from standing water (from, to; the terrain's water mask): reeds round a pond are (0, 6). " +
				"(0, 0): anywhere."},
		procgen.ParamDef{Name: "shore_soft", Type: procgen.ParamFloat, Label: "Shore fade (m)", Default: f(2), Min: 0, Max: 100, Step: 0.1,
			Doc: "How far beyond the shore band the weight fades out over."},
		procgen.ParamDef{Name: "max_depth", Type: procgen.ParamFloat, Label: "Max water depth (m)", Default: f(0), Min: 0, Max: 5, Step: 0.01,
			Doc: "How deep in standing water a clump may stand (reeds: 0.3–0.5). 0: never in water."},
		procgen.ParamDef{Name: "tint_a", Type: procgen.ParamColor, Label: "Tint A", Default: procgen.Color(1, 1, 1), Min: 0, Max: 2,
			Doc: "One end of the colour the clumps' albedo is multiplied by (1, 1, 1: as scanned)."},
		procgen.ParamDef{Name: "tint_b", Type: procgen.ParamColor, Label: "Tint B", Default: procgen.Color(1, 1, 1), Min: 0, Max: 2,
			Doc: "The other end: the tint mixes the two by a slow noise across the ground."},
		procgen.ParamDef{Name: "tint_scale", Type: procgen.ParamFloat, Label: "Tint scale (m)", Default: f(40), Min: 0, Max: 2000, Step: 1,
			Doc: "The tint noise's size: drifts of greener and drier grass this big."},
		procgen.ParamDef{Name: "tint_jitter", Type: procgen.ParamFloat, Label: "Tint jitter", Default: f(0.08), Min: 0, Max: 0.5, Step: 0.01,
			Doc: "Each clump's own brightness variation."},
		procgen.ParamDef{Name: "wind_bend", Type: procgen.ParamFloat, Label: "Wind bend", Default: f(0.25), Min: 0, Max: 2, Step: 0.01,
			Doc: "How far a clump's top leans in a full gust, as a fraction of its height."},
		procgen.ParamDef{Name: "wind_flutter", Type: procgen.ParamFloat, Label: "Wind flutter (m)", Default: f(0.04), Min: 0, Max: 1, Step: 0.005,
			Doc: "How far a blade tip shivers, metres."},
		procgen.ParamDef{Name: "range", Type: procgen.ParamFloat, Label: "Draw range", Default: f(1), Min: 0.1, Max: 1, Step: 0.01,
			Doc: "The fraction of the grass draw range this rule's clumps reach (flowers can stop sooner than the grass)."},
		procgen.ParamDef{Name: "shadows", Type: procgen.ParamBool, Default: procgen.Bool(true),
			Doc: "Whether these clumps cast shadows (near the camera only; the player's grass shadow distance)."},
	)
	// Ground cover (grass_field_cover.go): all Added, defaults = a grass clump.
	fields = append(fields, groundCoverRuleFields()...)
	// The scatter bands' defaults suit rocks; a sward wants less clumping.
	for i := range fields {
		switch fields[i].Name {
		case "cluster":
			fields[i].Default = f(0.25)
		case "cluster_scale":
			fields[i].Default = f(30)
		case "cliff":
			fields[i].Default = v2(0, 0.3)
		}
	}
	return fields
}

func grassFieldParams() []procgen.ParamDef {
	return []procgen.ParamDef{
		{Name: "terrain", Type: procgen.ParamStage, StageKinds: TerrainSources, Default: procgen.StageRef(""),
			Doc: "The terrain stage to grow on: its heights, masks, tiles and water."},
		{Name: "seed", Type: procgen.ParamSeed, Default: procgen.Seed(0), Doc: "Changes every rule's clumps."},
		{Name: "rules", Type: procgen.ParamList, MinItems: 1, Fields: grassRuleFields(), Default: procgen.List(),
			Doc: "The rules. Unlike scatter's they do not keep clear of each other: a meadow is grass and flowers mixed."},
		{Name: "avoid", Type: procgen.ParamStage, StageKinds: []string{CliffWallKind, ScatterKind}, Default: procgen.StageRef(""),
			Doc: "A scatter (or cliff_wall) stage whose footprints the grass keeps out of: no grass through a boulder."},
		{Name: "avoid_shrink", Type: procgen.ParamFloat, Label: "Avoid shrink", Default: procgen.Float(0.7), Min: 0, Max: 2, Step: 0.01,
			Doc: "Each avoided footprint's radius is taken at this fraction: grass may grow against a rock's skirt."},
		{Name: "cell", Type: procgen.ParamFloat, Label: "Cell (m)", Default: procgen.Float(16), Min: 4, Max: 64, Step: 1,
			Doc: "The culling cell: the renderer culls, thins and picks meshes per cell of this side."},
		{Name: "lod_ratios", Type: procgen.ParamVec2, Label: "LOD ratios", Default: procgen.Vec2(0.35, 0.1), Min: 0, Max: 1, Step: 0.01,
			Doc: "The mid and far meshes' share of the near mesh's triangles (0: no such mesh)."},
		{Name: "margin", Type: procgen.ParamFloat, Label: "Edge margin (m)", Default: procgen.Float(0.5), Min: 0, Max: 100, Step: 0.1,
			Doc: "Clumps keep this far in from the terrain's edge."},
		paintExcludeParam(),
		{Name: "preview_density", Type: procgen.ParamFloat, Label: "Preview density", Default: procgen.Float(0.05), Min: 0.001, Max: 5, PreviewOnly: true,
			Doc: "The preview's dots per square metre at most (the run's density is far denser)."},
	}
}

// grassRules maps the rule items onto place rules, without kinds (the run
// fills them from its kit). idx[k] is place rule k's item index.
func grassRules(p procgen.Params) ([]place.GrassRule, []int) {
	var out []place.GrassRule
	var idx []int
	for i, it := range p.Items("rules") {
		if !it.Bool("enabled") || !grassRuleHasSource(it) {
			continue
		}
		r := place.GrassRule{Name: safeName(it.Str("name"), fmt.Sprintf("rule%d", i+1)), Density: it.Float("density"),
			Scale: it.Vec2("scale"), EdgeShrink: it.Float("edge_shrink"), Band: place.DefaultRule(),
			Shore: it.Vec2("shore"), ShoreSoft: it.Float("shore_soft"), MaxDepth: it.Float("max_depth"),
			TintA: it.Vec3("tint_a"), TintB: it.Vec3("tint_b"), TintScale: it.Float("tint_scale"), TintJitter: it.Float("tint_jitter")}
		if r.Scale[1] < r.Scale[0] {
			r.Scale[0], r.Scale[1] = r.Scale[1], r.Scale[0]
		}
		bandRule(it, &r.Band)
		groundCoverRule(it, &r)
		out = append(out, r)
		idx = append(idx, i)
	}
	return out, idx
}

func validateGrassField(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	var out []string
	if p.Str("terrain") == "" {
		out = append(out, "Choose a terrain stage to grow on")
	}
	for i, it := range p.Items("rules") {
		if it.Bool("enabled") && !grassRuleHasSource(it) {
			out = append(out, fmt.Sprintf("rule %d (%s) names no plants stage (or card texture)", i+1, it.Str("name")))
		}
		out = append(out, validateCoverRule(i, it)...)
		if b := it.Vec2("slope"); b[0] > b[1] {
			out = append(out, fmt.Sprintf("rule %d: its slope band runs backwards (%g..%g°)", i+1, b[0], b[1]))
		}
	}
	return out
}
