package stages

// environments_shared.go — what the nine environment presets of
// docs/regions/PRESETS.md step 2 have in common: one catalogue of ground
// layers they share, the base-level convention they meet each other at,
// and the plain-ground terrain every one of them starts from.
//
// THE SHARED LAYER CATALOGUE. A world's ground is one PALETTE of at most 32
// layers (region_material_palette.go), and every terrain tile wears at most
// eight of them. A layer is the same palette entry in two presets when its
// Poly Haven id, its repeat (scale) and its projection (triplanar, the rock
// role only) are the same (regionLayer.key) — so the presets below name
// their layers from this table and never spell an id themselves. With the
// table, all eleven presets together hold 23 distinct layers, and where two
// presets meet their tiles need at most eight:
//
//	preset             sand            cracked          gravel           rock           cover
//	desert_canyon      red_sand        cracked_red      river_pebbles    cliff_side     —
//	dunes              red_sand        cracked_red      red_sand         cliff_side     —        (all the desert's)
//	savannah           withered_grass  dry_ground_01    dry_ground_rocks rock_boulder   —
//	tallgrass_prairie  leafy_grass     withered_grass   withered_grass   rock_boulder   —        (1 own)
//	oak_savannah       leafy_grass     withered_grass   withered_grass   rock_boulder   —        (the prairie's)
//	pine_forest        needles         needles          needles          lichen_rock    —
//	alpine             scree           alpine_turf      scree            lichen_rock    snow
//	tundra             alpine_turf     frozen_ground    alpine_turf      lichen_rock    snow
//	coast              coast_sand      coast_sand       coast_sand       lichen_rock    wet: damp_sand
//	wetland            leafy_grass     leaf_mud         —                lichen_rock    wet: mud
//	volcanic           cinders         bare_lava        cinders          basalt         ash: frozen_ground
//
// Every preset has at most four layers, so any two of them meet within a
// tile's eight. Three presets meeting at one tile can exceed eight;
// docs/regions/PRESETS_2.md lists which triples fit.
//
// THE BASE LEVEL. Every preset meets its neighbours at 0 m: its ground
// across a transition band stands near 0 — the plains by their own relief
// (centred on 0), the high presets (pine hills, alpine, volcanic) by
// `base_elevation: 0` with an `edge_rise`, so their relief climbs from 0
// inside their own footprint instead of meeting a plain as an escarpment
// (terrain/edge.go). Standing water lies BELOW the base: the coast's sea at
// −1.5 m, the wetland's water table at −0.25 m, tundra ponds in their own
// hollows. So a wetland or a coast floods only its own low ground, never a
// neighbour's band.
//
// Every preset leaves out the level-wide stages (environment, spawn). Its
// plants are Poly Haven scenery only — trees and rocks in its `scatter`
// stage, grass, ferns and flowers in its `cover` stage — never the
// engine's foliage biomes (the user's direction, docs/regions/PRESETS_2.md).
// The CPU frame pays ~1 ms per thousand placements in view
// (docs/regions/PHASE_6.md), so the target is ~3,000 a km²; the measured
// counts per preset, and the ones over it, are in PRESETS_2.md.

import "foundry/internal/procgen"

// envLayer is one ground layer of the shared catalogue: the textures
// stage's layer name, its Poly Haven texture id and its repeat in metres.
// A rock layer's repeat is its terrain_material's rock_scale (sharedRockScale
// for every preset here), not this.
type envLayer struct {
	name, id string
	scale    float64
}

// The shared ground layers (Poly Haven ids checked against the live
// catalogue, 2026-10-13, `foundry polyhaven search -t textures`).
var (
	// The desert's own four (desert_canyon.go): dunes reuse them all.
	layerRedSand    = envLayer{"sand", "red_sand", 3}
	layerCrackedRed = envLayer{"cracked", "cracked_red_ground", 4.5}
	layerCliffSide  = envLayer{"cliff", "cliff_side", 10}
	// The savannah's (environments_builtin.go): the prairie and the oak
	// savannah share its dry grass and boulder rock.
	layerWitheredGrass = envLayer{"dry_grass", "withered_grass", 2.5}
	layerBoulderRock   = envLayer{"rock", "rock_boulder_dry", 6}
	// Temperate grass: green turf with a few dead leaves, the prairie's,
	// the oak savannah's and the wetland's everywhere layer.
	layerLeafyGrass = envLayer{"grass", "leafy_grass", 3}
	// Pine needles (Poly Haven's pine_forest collection).
	layerNeedles = envLayer{"needles", "forest_leaves_04", 2.5}
	// Grey broken rock and gravel: alpine scree (tried in the pine
	// forest's, tundra's and coast's washes too: the flow mask paved them).
	layerScree = envLayer{"scree", "rocks_ground_01", 4.5}
	// Grey lichen-crusted rock: the cliffs of every cool preset.
	layerLichenRock = envLayer{"rock", "lichen_rock", 6}
	// Olive moss and lichen over small stones: alpine turf and the
	// tundra's everywhere layer (aerial_grass_rock, tried first, rendered
	// acid yellow-green).
	layerAlpineTurf = envLayer{"turf", "rocks_ground_03", 3}
	// Grey rock (rock_surface, the coolest of Poly Haven's rock scans) for the alpine's faces at a long repeat: lichen_rock's
	// orange lichen read as desert rock there, and its 9 m repeat as a
	// lattice across a 200 m face (docs/regions/PRESETS_2B.md).
	layerAlpineRock = envLayer{"rock", "rock_surface", 16}
	// Snow, for the snow cover of alpine and tundra.
	layerSnow = envLayer{"snow", "snow_02", 4}
)

// sharedRockScale is every new preset's rock_scale: the rock layer's repeat,
// and with its id the key two presets share it by.
const sharedRockScale = 9

// texParams is a layer's polyhaven_textures list item.
func (l envLayer) texParams() procgen.Params {
	return procgen.Params{"name": procgen.String(l.name), "id": procgen.String(l.id), "scale": procgen.Float(l.scale)}
}

// texturesStage is a preset's polyhaven_textures stage over the layers.
func texturesStage(layers ...envLayer) procgen.Stage {
	items := make([]procgen.Params, len(layers))
	for i, l := range layers {
		items[i] = l.texParams()
	}
	return procgen.Stage{Kind: PolyHavenTexturesKind, ID: "tex", Params: procgen.Params{"layers": procgen.List(items...)}}
}

// plainTerrain is the terrain stage's params for ground with no canyon
// country in it — no mesas, no canyons, no terraces, thick soft beds —
// with over laid on top. The size and resolution are what the preset grows
// as a square on its own (a region supplies its own).
func plainTerrain(over procgen.Params) procgen.Params {
	f, i := procgen.Float, procgen.Int
	soft := func(thick, hard, r, g, b float64) procgen.Params {
		return procgen.Params{"thickness": f(thick), "hardness": f(hard), "color": procgen.Color(r, g, b)}
	}
	p := procgen.Params{
		"size": f(512), "resolution": f(1), "tile_size": f(128),
		"mesa_height": f(0), "mesa_coverage": f(0), "mesa_tiers": i(0),
		"strata": procgen.List(
			soft(18, 0.3, 0.5, 0.48, 0.44), soft(24, 0.35, 0.46, 0.44, 0.4), soft(30, 0.4, 0.52, 0.5, 0.46)),
		"terrace": f(0), "resistance": f(0.2),
		"canyons":     procgen.List(),
		"cook_memory": i(2000),
	}
	for k, v := range over {
		p[k] = v
	}
	return p
}

// presetAsset is a Poly Haven stage's asset item; tris > 0 decimates it.
func presetAsset(id string, tris int) procgen.Params {
	p := procgen.Params{"id": procgen.String(id)}
	if tris > 0 {
		p["max_triangles"] = procgen.Int(tris)
	}
	return p
}

// plantsStage is a polyhaven_plants stage.
func plantsStage(id string, lite int, assets ...procgen.Params) procgen.Stage {
	// thin_foliage: Poly Haven's needles and blades are thousands of tiny
	// cards that edge collapse strips bare (phimport/decimate_cards.go).
	p := procgen.Params{"assets": procgen.List(assets...), "thin_foliage": procgen.Bool(true)}
	if lite > 0 {
		p["lite_triangles"] = procgen.Int(lite)
	}
	return procgen.Stage{Kind: PolyHavenPlantsKind, ID: id, Params: p}
}

// rocksStage is a polyhaven_rocks stage with no backs (scattered pieces sit
// on the ground) and a lite variant for the dense rules.
func rocksStage(id string, lite int, collision string, assets ...procgen.Params) procgen.Stage {
	p := procgen.Params{"assets": procgen.List(assets...), "back": procgen.String("none")}
	if lite > 0 {
		p["lite_triangles"] = procgen.Int(lite)
	}
	if collision != "" {
		p["collision"] = procgen.Enum(collision)
	}
	return procgen.Stage{Kind: PolyHavenRocksKind, ID: id, Params: p}
}

// coverStage is a second scatter stage for a preset's ground cover —
// Poly Haven grass clumps, flowers, ferns, moss — apart from its trees and
// rocks, so the cover's rules do not keep clear of each tree's footprint
// (a later rule of one stage keeps clear of the earlier ones) and their
// budget reads on its own. Ground cover is SCENERY here, one placement per
// clump: the presets do not use the engine's foliage biomes (the user's
// direction, docs/regions/PRESETS_2.md), so grass at scale costs the CPU
// frame ~1 ms per thousand clumps in view and is budgeted as patches —
// large clump models, scaled up, clustered — not as a lawn.
func coverStage(rules ...procgen.Params) procgen.Stage {
	return procgen.Stage{Kind: ScatterKind, ID: "cover", Params: procgen.Params{"terrain": procgen.StageRef("ground"),
		"ground": procgen.StageRef("ground_mat"), "rules": procgen.List(rules...)}}
}

// coverRule is a ground-cover scatter rule: clumps of the plants stage
// src, density per 100 m², at scale, on slopes up to maxSlope, clustered
// into patches of patch metres, overlapping (spacing 0.5), no collision,
// standing upright, only the source's pieces at least minSize across
// (Poly Haven's grass sets split into clumps from 2 cm to 0.3 m).
func coverRule(name, src string, density float64, scale [2]float64, maxSlope, patch, cluster, minSize float64) procgen.Params {
	f, s, v2 := procgen.Float, procgen.String, procgen.Vec2
	return procgen.Params{"name": s(name), "rocks": procgen.StageRef(src), "density": f(density), "scale": v2(scale[0], scale[1]),
		"slope": v2(0, maxSlope), "cliff": v2(0, 0.3), "align": f(0.4), "tilt": f(0), "sink": f(0.02), "spacing": f(0.5),
		"cluster": f(cluster), "cluster_scale": f(patch), "collision": procgen.Enum("none"), "min_size": f(minSize)}
}

// withHeight is rule r limited to the heights from lo to hi (fractions of
// the terrain's range).
func withHeight(r procgen.Params, lo, hi float64) procgen.Params {
	r["height"] = procgen.Vec2(lo, hi)
	return r
}
