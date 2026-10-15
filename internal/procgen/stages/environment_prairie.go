package stages

// environment_prairie.go — the `tallgrass_prairie` and `oak_savannah`
// environment presets (docs/regions/PRESETS.md step 2; ENVIRONMENT.md
// priorities 9 and 10).
//
// The two temperate grasslands share their ground and their grass stage —
// ENVIRONMENT.md's "same grass stage on both sides" — so a prairie running
// into an oak savannah changes only in its trees.
//
// LANDFORM. Gently rolling plains: long swells every ~600 m, 22 m (the
// prairie) or 28 m (the oak savannah) of relief over a few octaves, a
// dendritic net of shallow grassy draws from drainage, no droplets (on
// slopes this low their trails comb the ground, docs/regions/PHASE_6.md).
// Both sit on the shared 0 m base by their own relief: no edge rise.
//
// GROUND (environments_shared.go). Green temperate grass everywhere
// (leafy_grass) tinted toward the green-gold of tall grass in late summer;
// the savannah's withered grass on the flats in small patches (it reads pale
// straw against the green: at a quarter of the flats, or down the draws, it
// paved the plain); the savannah's boulder rock on the banks. (The
// savannah's stony soil in the draws was tried and dropped: on these gentle
// plains the flow mask paved half the ground with it.) Of these three layers
// only leafy_grass is not the savannah's, so prairie ↔ savannah bands hold
// five, and the two grasslands share all three.
//
// LIFE (Poly Haven only — no engine foliage biome).
//   - The PRAIRIE is its grass, on the grass instancer (grass_field,
//     environments_grass.go; docs/regions/PRESETS_2B.md): a continuous
//     sward of grass_medium_01's tall clumps (0.24–0.32 m as scanned,
//     grown 4.4–6.4× to 1–2 m) at 3 per m² over every gentle slope, its
//     mid clumps grown big under them as the sward's body, grass_medium_02's
//     drier clumps in patches, and wildflower drifts of dandelion_01 and
//     celandine_01 (yellow) and flower_heliophila (blue-white); the tint
//     drifts green-gold to straw over ~60 m. As scenery (step 2) the same
//     grass could afford ~3% of the ground under a clump. A few flowering
//     shrub clumps (shrub_01) along the draws and the savannah's granite
//     boulders (shared imports) on the banks stay scenery.
//   - The OAK SAVANNAH is the same grass sparser and a little shorter,
//     under open-grown broadleaf trees in clumps. Poly Haven has NO OAK:
//     the stand-in is island_tree_01 — a broad, gnarled, wide-crowned
//     broadleaf on a short twisted trunk; its mesh is 5 m across (Poly
//     Haven lists 12.5 m), grown 2–2.8× it reads as an open-grown oak — in
//     clumps where a slow cluster noise is high, open grass between; and
//     the odd jacaranda_tree (a wide umbrella crown, no flowers in its
//     scan; 24 m as scanned, placed at 0.55–0.75) as a great tree.
//
// BUDGET. Scenery: ~400 placements per km² (shrubs, boulders; the oak
// savannah's trees ~800). Grass: ~4.8 M clumps per km² on the prairie
// (1.26 M in a 512 m region, 15 MB of fields), ~2.5 M on the oak
// savannah; the instancer draws ~37 k of them a frame from the ground,
// 3.2–3.5 ms of GPU on the GTX 1070 at 2560×1080 (PRESETS_2B.md).

import "foundry/internal/procgen"

// The temperate grassland presets' names.
const (
	TallgrassPrairieEnvironment = "tallgrass_prairie"
	OakSavannahEnvironment      = "oak_savannah"
)

func init() {
	procgen.RegisterEnvironment(procgen.Environment{
		Name:  TallgrassPrairieEnvironment,
		Label: "Tallgrass prairie",
		Doc:   "Gently rolling plains of tall green-gold grass with wildflower drifts, grassy draws, a few flowering shrubs.",
		Color: [3]float64{0.62, 0.78, 0.3},
		New:   TallgrassPrairiePreset,
	})
	procgen.RegisterEnvironment(procgen.Environment{
		Name:  OakSavannahEnvironment,
		Label: "Oak savannah",
		Doc:   "Rolling prairie grass under open-grown broadleaf trees in clumps (Poly Haven has no oak: island_tree_01 and jacaranda_tree stand in).",
		Color: [3]float64{0.5, 0.62, 0.22},
		New:   OakSavannahPreset,
	})
}

// prairieTerrain is the grasslands' rolling plain with relief metres of
// swell.
func prairieTerrain(relief float64) procgen.Params {
	f, i := procgen.Float, procgen.Int
	return plainTerrain(procgen.Params{
		"relief": f(relief), "frequency": f(0.0017), "octaves": i(5), "warp": f(150),
		"strata": procgen.List(
			procgen.Params{"thickness": f(20), "hardness": f(0.2), "color": procgen.Color(0.42, 0.36, 0.26)},
			procgen.Params{"thickness": f(30), "hardness": f(0.25), "color": procgen.Color(0.48, 0.42, 0.3)}),
		"resistance": f(0.15),
		"drainage":   f(2), "drainage_fill": f(0.04), "drainage_diffusion": f(0.2),
		"droplets": f(0), "talus_angle": f(26), "detail": f(0.1),
		"sand_color": procgen.Color(0.48, 0.5, 0.3)})
}

// prairieMaterial is the grasslands' terrain_material: green-gold grass,
// cracked the flats' patch layer, dry grass in the draws, the savannah's
// rock.
func prairieMaterial(cracked string, cover float64) procgen.Params {
	f, s, ref, v2 := procgen.Float, procgen.String, procgen.StageRef, procgen.Vec2
	return procgen.Params{"terrain": ref("ground"), "textures": ref("tex"),
		"sand": s("grass"), "cracked": s(cracked), "gravel": s("grass"), "rock": s("rock"),
		// The savannah's rock repeat: the same palette entry.
		"rock_scale": f(6), "rock_slope": v2(30, 44), "cliff": f(0.6),
		"flat_slope": v2(3, 9), "cracked_cover": f(cover), "sand_deposition": v2(0.35, 0.75),
		"wash_flow": v2(0.8, 0.95), "wash_width": f(2),
		"layer_match": f(0.1), "breakup": f(0.6), "breakup_scale": f(18),
		"strata": f(0), "streaks": f(0), "macro": f(0.25), "macro_scale": f(60),
		"anti_tile": procgen.Bool(true), "gravel_relief": f(0.03), "blend": f(0.3),
		// Late-summer tallgrass: less blue than the scan's spring green.
		"sand_tint": procgen.Color(0.64, 0.66, 0.38),
		// The dry grass gold, not the scan's bleached straw. withered_grass
		// is ONE palette entry for the prairie's and the oak savannah's dry
		// patches and the savannah's everywhere layer, and a shared entry
		// wears the first environment's tint by name — the oak savannah's,
		// this — so this is also what makes the savannah gold (its own
		// sand_tint is never seen in a world that has an oak savannah;
		// PRESETS_6).
		"cracked_tint": procgen.Color(0.85, 0.7, 0.45)}
}

// prairieCover is the grasslands' ground cover, on the grass instancer
// (environments_grass.go): a continuous sward of grass_medium_01's tall
// clumps (0.24–0.32 m as scanned, ×k·(4.2–6.2): 1–2 m) at grass clumps per
// m² over every gentle slope, its mid clumps under them, grass_medium_02's
// drier clumps in patches, and wildflower drifts; after the imports
// (prairiePlants). The tint drifts between late-summer green-gold and straw
// over ~60 m, so the plain reads in broad colour fields rather than one
// flat green.
func prairieCover(grass, flowers, k float64) []procgen.Stage {
	gold, straw := [3]float64{1.04, 0.96, 0.62}, [3]float64{1.1, 0.95, 0.6}
	green := [3]float64{0.88, 0.96, 0.62}
	return append(prairiePlants(), grassStage(
		grassWindOf(grassTint(grassRule("tallgrass", "grass", "tall", grass, [2]float64{4.4 * k, 6.4 * k}, 28, 0.2, 45, 400),
			green, gold, 60), 0.35, 0.05),
		// The bushy mid clumps grown big: the sward's body, 0.9–1.5 m.
		grassWindOf(grassTint(grassRule("undergrass", "grass", "mid", grass*0.45, [2]float64{5 * k, 7 * k}, 30, 0.25, 30, 450),
			green, gold, 60), 0.3, 0.04),
		grassTint(grassRule("dry_grass", "dry_grass", "", grass*0.15, [2]float64{3.5 * k, 5 * k}, 28, 0.75, 25, 450),
			straw, gold, 40),
		grassRange(grassTint(grassRule("flowers", "flowers", "dandelion celandine heliophila_small heliophila_medium", flowers,
			[2]float64{3, 4.5}, 22, 0.85, 14, 1500), [3]float64{1, 1, 1}, [3]float64{1, 1, 1}, 40), 0.7, false)))
}

// prairiePlants are the grasslands' grass and flower imports.
func prairiePlants() []procgen.Stage {
	return []procgen.Stage{
		plantsStage("grass", 0, presetAsset("grass_medium_01", 0)),
		plantsStage("dry_grass", 0, presetAsset("grass_medium_02", 0)),
		plantsStage("flowers", 0, presetAsset("dandelion_01", 0), presetAsset("celandine_01", 200000),
			presetAsset("flower_heliophila", 0)),
	}
}

// TallgrassPrairiePreset is the prairie's document.
func TallgrassPrairiePreset() *procgen.Doc {
	f, s, ref, v2 := procgen.Float, procgen.String, procgen.StageRef, procgen.Vec2
	return &procgen.Doc{Name: TallgrassPrairieEnvironment, Seed: 1859,
		Doc: "Environment preset: tallgrass prairie (rolling plains, tall green-gold grass, wildflowers, grassy draws)",
		Stages: append([]procgen.Stage{
			texturesStage(layerLeafyGrass, layerWitheredGrass, layerBoulderRock),
			{Kind: TerrainKind, ID: "ground", Params: prairieTerrain(22)},
			{Kind: TerrainMaterialKind, ID: "ground_mat", Params: prairieMaterial("dry_grass", 0.12)},
			plantsStage("shrubs", 8000, presetAsset("shrub_01", 60000)),
			rocksStage("boulders", 5000, "", presetAsset("namaqualand_boulder_04", 0), presetAsset("namaqualand_boulder_05", 0)),
			{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "ground": ref("ground_mat"),
				"rules": procgen.List(
					procgen.Params{"name": s("shrubs"), "rocks": ref("shrubs"), "density": f(0.03), "scale": v2(0.8, 1.4),
						"slope": v2(0, 20), "flow": v2(0.3, 1), "align": f(0.3), "tilt": f(0), "sink": f(0.04), "spacing": f(1.4),
						"cluster": f(0.85), "cluster_scale": f(25), "collision": procgen.Enum("none")},
					procgen.Params{"name": s("boulders"), "rocks": ref("boulders"), "density": f(0.01), "scale": v2(0.4, 1.2),
						"scale_bias": f(2.2), "slope": v2(8, 35), "cluster": f(0.9), "cluster_scale": f(40),
						"align": f(0.6), "sink": f(0.3), "match_ground": f(0.3)})}},
		}, prairieCover(3, 0.12, 1)...)}
}

// OakSavannahPreset is the oak savannah's document.
func OakSavannahPreset() *procgen.Doc {
	f, s, ref, v2 := procgen.Float, procgen.String, procgen.StageRef, procgen.Vec2
	return &procgen.Doc{Name: OakSavannahEnvironment, Seed: 1776,
		Doc: "Environment preset: oak savannah (rolling prairie grass, open-grown broadleaf trees in clumps)",
		Stages: append([]procgen.Stage{
			texturesStage(layerLeafyGrass, layerWitheredGrass, layerBoulderRock),
			{Kind: TerrainKind, ID: "ground", Params: prairieTerrain(28)},
			{Kind: TerrainMaterialKind, ID: "ground_mat", Params: prairieMaterial("dry_grass", 0.12)},
			plantsStage("oaks", 0, presetAsset("island_tree_01", 400000)),
			plantsStage("great_trees", 0, presetAsset("jacaranda_tree", 150000)),
			rocksStage("boulders", 5000, "", presetAsset("namaqualand_boulder_04", 0), presetAsset("namaqualand_boulder_05", 0)),
			{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "ground": ref("ground_mat"),
				"rules": procgen.List(
					// Open-grown trees in clumps: only where the slow cluster
					// noise is high, a tree every ~15 m inside a clump.
					// island_tree_01's mesh is 5 m (Poly Haven lists 12.5 m):
					// grown 2–2.8× to an open-grown oak's 10–14 m.
					procgen.Params{"name": s("oaks"), "rocks": ref("oaks"), "density": f(0.07), "scale": v2(2, 2.8),
						"slope": v2(0, 18), "cliff": v2(0, 0.2), "align": f(0), "tilt": f(0), "sink": f(0.03), "spacing": f(1.2),
						"cluster": f(1), "cluster_scale": f(80)},
					// The odd great tree: jacaranda_tree is 24 m across as
					// scanned; at 0.55–0.75 a 13–18 m crown.
					procgen.Params{"name": s("great_trees"), "rocks": ref("great_trees"), "density": f(0.01), "scale": v2(0.55, 0.75),
						"slope": v2(0, 14), "cliff": v2(0, 0.2), "align": f(0), "tilt": f(0), "sink": f(0.03), "spacing": f(1.5),
						"cluster": f(0.6), "cluster_scale": f(120)},
					procgen.Params{"name": s("boulders"), "rocks": ref("boulders"), "density": f(0.01), "scale": v2(0.4, 1.2),
						"scale_bias": f(2.2), "slope": v2(8, 35), "cluster": f(0.9), "cluster_scale": f(40),
						"align": f(0.6), "sink": f(0.3), "match_ground": f(0.3)})}},
		}, prairieCover(1.6, 0.06, 0.8)...)}
}
