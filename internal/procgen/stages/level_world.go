package stages

// level_world.go — the `level_world` template: a level generation doc
// (docs/regions/SPEC.md).
//
// The document behind "Generate world…" in the editor and `foundry gen world
// <level>`: resources/generators/<level>_world.fgen, targeting <level>:
//
//	regions   the level's region volumes, synced in by genlevel.SyncRegions
//	          before every run (the template starts with none), and the grid
//	          settings (resolution, tile size, erosion, margin)
//	ground    region_terrain: one eroded terrain over every region, each
//	          region's preset terrain blended across the bands (phase 2);
//	          its tiles wear ground_mat's material
//	ground_mat region_material: every environment's ground rules and
//	          textures weighted by its biome mask, baked into one splat of
//	          up to eight layers (phase 3)
//
// The environment stage moves here from the presets as the level's own in
// a later phase.

import (
	"strings"

	"foundry/internal/procgen"
)

// LevelWorldTemplate is the template's name.
const LevelWorldTemplate = "level_world"

// LevelWorldSuffix ends a level generation doc's name: <level>_world.
const LevelWorldSuffix = "_world"

func init() {
	procgen.RegisterTemplate(procgen.Template{
		Name:  LevelWorldTemplate,
		Label: "Level world (regions)",
		Doc: "A level generation doc: the level's environment regions (synced from its region volumes before every " +
			"run), each growing its environment preset, blended across their transition bands. Name it <level>_world.",
		New: LevelWorld,
	})
}

// LevelWorldDocName is the level generation doc's name for a level.
func LevelWorldDocName(level string) string { return safeName(level, "level") + LevelWorldSuffix }

// LevelWorld is the template's document. A name ending _world targets the
// level it names; any other targets a level of its own name.
func LevelWorld(name string) *procgen.Doc {
	level := strings.TrimSuffix(name, LevelWorldSuffix)
	if level == "" {
		level = name
	}
	mat := "gen_" + safeName(name, "gen") + "_ground_mat"
	return &procgen.Doc{Name: name, Seed: 1, Level: level,
		Doc: "World generation for level " + level + ": its region volumes, each growing its environment",
		Stages: []procgen.Stage{
			{Kind: RegionsKind, ID: "regions", Params: procgen.Params{}},
			// mfd: a band's broad flats get no ruled d8 wash lines (PHASE_6).
			{Kind: RegionTerrainKind, ID: "ground", Params: procgen.Params{"regions": procgen.StageRef("regions"),
				"material": procgen.String(mat), "flow_mask": procgen.Enum("mfd")}},
			{Kind: RegionMaterialKind, ID: "ground_mat", Params: procgen.Params{"terrain": procgen.StageRef("ground"),
				// Per-tile materials (region_material_tiles.go): only the band pays for eight layers.
				"per_tile": procgen.Bool(true)}},
		}}
}
