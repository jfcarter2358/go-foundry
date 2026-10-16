package stages

// ground_edit.go — the `ground_edit` stage: painted height and water carved
// into a finished terrain (docs/terrain-paint/PHASE_W2.md; the user's page
// is docs/guide/36-terrain-paint.md, the stage's section in
// docs/guide/33-generators.md).
//
// A terrain stage's heights come out of erosion; an author painting a pond,
// a terrace or a path's bed wants to change them AFTER erosion, by hand,
// without rerunning it. ground_edit reads a terrain (or region_terrain)
// stage's output and two kinds of paint layer:
//
//   - `height` layers, each a signed offset in metres (paint modes add,
//     erase, smooth, set and flatten — flatten paints toward the absolute
//     height sampled under the brush as the stroke starts), summed with a
//     scale each;
//   - `water` layers, each carved as ponds, lakes or a marsh
//     (terrain.CarveBasins: a shelving bank, a shelf, a deeper middle, a
//     little noise; hummocks for a marsh), the water standing at each body's
//     spill height or at a level pinned in the `bodies` table.
//
// WHERE THE EDITED TERRAIN GOES — the mechanism, and why. ground_edit is a
// TERRAIN SOURCE (TerrainSources): its artifact is a *terrain.Terrain, and
// its output folder is a terrain folder (terrain.yaml, heightmap.png, every
// mask), so every stage that reads a terrain — terrain_material,
// region_material, scatter, grass_field, drift, wall_sheet, vegetation —
// reads the edited ground by naming ground_edit as its `terrain` instead of
// the terrain stage. The alternative, the terrain stage applying the edits
// itself, would rerun erosion (seconds to minutes) on every stroke: the
// terrain stage is not incremental below the tile, and erosion is global.
// As a separate stage, a stroke reruns ground_edit against the terrain's
// files read back, recooks the tiles it moved and nothing else. The terrain
// stage's own output is never touched, so a document without ground_edit is
// byte for byte what it was (TestTerrainStageGolden).
//
// ground_edit OWNS THE TERRAIN'S PLACEMENTS downstream: it supersedes every
// placement of its source (tiles, backdrop, stitch strips, region water)
// and re-places each — a moved tile with its recooked mesh, the rest
// unchanged — so a later stage that supersedes "the terrain's" tiles (drift
// recooking, region_material's per-tile materials, a cave) finds them all
// under ground_edit, as it would under a terrain stage. Stages that need
// the terrain stage's own parameters (drift's tile plan, region_material's
// regions) follow ground_edit to its source (terrainSourceStage).
//
// The run is ground_edit_run.go; the bodies, their table and the water
// meshes ground_edit_water.go; flatten's ground lookup ground_edit_base.go;
// the rivers (phase W3) ground_edit_river*.go; the footpaths (phase P4)
// ground_edit_path*.go.

import (
	"fmt"

	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
	"foundry/internal/procgen/terrain"
)

// GroundEditKind is the stage's name.
const GroundEditKind = "ground_edit"

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     GroundEditKind,
		Label:    "Ground edit",
		Category: "Terrain",
		Version:  1,
		Doc: "Painted edits to a finished terrain: height layers raise, lower and flatten the ground, water layers carve " +
			"ponds, lakes and marshes with shelving banks, the water standing at each body's spill height or a pinned level, and " +
			"rivers (painted splines, or traced from drainage) cut channels whose flowing water never runs uphill, and " +
			"paths (rock paths, stepping stones, dirt and gravel trails, game trails) flatten their bed, wear the ground and lay stones. " +
			"Recooks only the tiles it moves; stages reading the terrain name this stage instead to get the edited ground.",
		Params:   groundEditParams(),
		Run:      runGroundEdit,
		Load:     loadTerrain,
		Validate: validateGroundEdit,
		// A stroke recooks only the tiles it moved (ground_edit_run.go).
		Incremental: true,
		// A pond's level is its whole outline's: downstream pieces key on
		// the heights they read (procgen.RectKeyer, terrain.RectKey).
		RectKeyed: true,
	})
}

// Water styles: what a water layer's bodies are.
const (
	waterStylePond  = "pond"
	waterStyleLake  = "lake"
	waterStyleMarsh = "marsh"
)

// waterStyleMaterial is a style's default water preset
// (resources/materials/water_*.fmat, phase W1).
var waterStyleMaterial = map[string]string{
	waterStylePond:  "water_lake",
	waterStyleLake:  "water_lake",
	waterStyleMarsh: "water_swamp",
}

func groundEditParams() []procgen.ParamDef {
	f, s, v2 := procgen.Float, procgen.String, procgen.Vec2
	d := terrain.DefaultBasinProfile()
	return append([]procgen.ParamDef{
		{Name: "terrain", Type: procgen.ParamStage, StageKinds: []string{TerrainKind, RegionTerrainKind}, Default: procgen.StageRef(""),
			Doc: "The terrain or region_terrain stage whose finished heights are edited (it must cook its tiles)."},
		{Name: "height", Type: procgen.ParamList, Label: "Height layers", Default: procgen.List(),
			Fields: []procgen.ParamDef{
				{Name: "layer", Type: procgen.ParamMask, Default: procgen.Mask(""),
					Doc: "A paint layer of kind height: metres added to the ground (negative lowers it)."},
				{Name: "scale", Type: procgen.ParamFloat, Default: f(1), Min: -10, Max: 10, Step: 0.01,
					Doc: "Multiplies the layer's offset."},
			},
			Doc: "Height layers, summed onto the terrain after erosion. Paint with add/erase/smooth, or flatten (toward the " +
				"ground height under the brush as the stroke starts) for terraces and path beds."},
		{Name: "water", Type: procgen.ParamList, Label: "Water layers", Default: procgen.List(),
			Fields: []procgen.ParamDef{
				{Name: "layer", Type: procgen.ParamMask, Default: procgen.Mask(""),
					Doc: "A paint layer of kind water: each connected painted area is one body."},
				{Name: "style", Type: procgen.ParamEnum, Default: procgen.Enum(waterStylePond),
					Choices: []string{waterStylePond, waterStyleLake, waterStyleMarsh},
					Doc: "What the bodies are, and their default water preset: pond and lake wear water_lake, marsh water_swamp. " +
						"A lake or marsh wants the shape numbers below set for it (the presets' recipes do)."},
				{Name: "material", Type: procgen.ParamString, Default: s(""),
					Doc: "The water material (any .fmat with a water effect: water, water_lake, water_swamp, water_alpine...). Empty: the style's."},
				{Name: "threshold", Type: procgen.ParamFloat, Default: f(d.Threshold), Min: 0.01, Max: 1, Step: 0.01,
					Doc: "The painted value from which the ground is water area: the outline is this contour of the brush."},
				{Name: "bank", Type: procgen.ParamFloat, Label: "Bank (m)", Default: f(d.Bank), Min: 0.1, Max: 100, Step: 0.1,
					Doc: "How far in from the outline the bed runs down to the shelf."},
				{Name: "shelf_depth", Type: procgen.ParamFloat, Label: "Shelf depth (m)", Default: f(d.ShelfDepth), Min: 0, Max: 20, Step: 0.01,
					Doc: "The shallow ledge's depth."},
				{Name: "shelf", Type: procgen.ParamFloat, Label: "Shelf (m)", Default: f(d.Shelf), Min: 0, Max: 200, Step: 0.1,
					Doc: "The shallow ledge's width before the bed drops to the deep middle."},
				{Name: "depth", Type: procgen.ParamFloat, Label: "Depth (m)", Default: f(d.Depth), Min: 0, Max: 100, Step: 0.05,
					Doc: "The deepest point of a body wide enough to reach it (a small one is shallower)."},
				{Name: "noise", Type: procgen.ParamFloat, Label: "Bed noise (m)", Default: f(d.Noise), Min: 0, Max: 5, Step: 0.01,
					Doc: "How much the bed undulates (none at the waterline)."},
				{Name: "noise_size", Type: procgen.ParamFloat, Label: "Noise size (m)", Default: f(d.NoiseSize), Min: 0.5, Max: 200, Step: 0.5,
					Doc: "The bed noise's feature size."},
				{Name: "hummocks", Type: procgen.ParamFloat, Label: "Hummocks (m)", Default: f(0), Min: 0, Max: 5, Step: 0.01,
					Doc: "A marsh's tussocks: bumps of up to this height, biased upward so some break the surface as islands. 0: none."},
				{Name: "hummock_size", Type: procgen.ParamFloat, Label: "Hummock size (m)", Default: f(d.HummockSize), Min: 0.5, Max: 100, Step: 0.1,
					Doc: "The hummocks' spacing."},
				{Name: "bank_slope", Type: procgen.ParamFloat, Label: "Bank slope", Default: f(d.BankSlope), Min: 0.02, Max: 3, Step: 0.01,
					Doc: "The steepest the ground outside the outline rises from the water, as rise per metre (0.3: about 17°). " +
						"Higher ground is cut down to it: a pond on a slope gets a cut bank uphill, not a cliff."},
				{Name: "reach", Type: procgen.ParamFloat, Label: "Bank reach (m)", Default: f(d.Reach), Min: 0, Max: 200, Step: 0.5,
					Doc: "How far outside the outline the bank may cut (or, under a pinned level above the rim, raise a berm)."},
				{Name: "min_area", Type: procgen.ParamFloat, Label: "Min area (m²)", Default: f(d.MinArea), Min: 0, Max: 100000, Step: 1,
					Doc: "Painted areas smaller than this are left dry."},
			},
			Doc: "Water layers carved as basins (terrain.CarveBasins): each painted area becomes a body with a shelving bank " +
				"and its own water surface, meshed per tile wearing the water material, listed in terrain.yaml and the water mask."},
		{Name: "bodies", Type: procgen.ParamList, Label: "Bodies", Default: procgen.List(),
			Fields: []procgen.ParamDef{
				{Name: "at", Type: procgen.ParamVec2, Default: v2(0, 0), Min: -1e7, Max: 1e7, Step: 0.1,
					Doc: "A world point (x, z) inside the body: the body is whichever painted area holds it, so repainting the edges keeps the key."},
				{Name: "level", Type: procgen.ParamFloat, Label: "Level (m)", Default: f(0), Min: -10000, Max: 10000, Step: 0.01,
					Doc: "The water surface's height, metres (when not auto). Above the rim, a berm is raised to hold it."},
				{Name: "auto", Type: procgen.ParamBool, Default: procgen.Bool(false),
					Doc: "Keep the automatic level (the spill height); the entry only sets the material."},
				{Name: "material", Type: procgen.ParamString, Default: s(""),
					Doc: "This body's water material; empty: its layer's."},
			},
			Doc: "Per-body settings, keyed by a point inside the body: a pinned level, a material. The Generator's water " +
				"bodies panel writes these (Lock / Auto)."},
		{Name: "freeboard", Type: procgen.ParamFloat, Label: "Freeboard (m)", Default: f(d.Freeboard), Min: 0, Max: 5, Step: 0.01,
			Doc: "How far below its rim an automatic level stands, so the water never spills over it."},
		{Name: "cook_memory", Type: procgen.ParamInt, Label: "Cook memory (MB)", Default: procgen.Int(2000), Min: 100, Max: 64000,
			Doc: "How much memory the recooked tiles' cooks may hold at once."},
		// Phase W3's rivers (ground_edit_river_params.go) and phase P4's
		// paths (ground_edit_path_params.go), all Added.
	}, append(riverParams(), pathParams()...)...)
}

func validateGroundEdit(d *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	var out []string
	if p.Str("terrain") == "" {
		out = append(out, "choose a terrain stage")
	}
	check := func(list, kind string) {
		for i, it := range p.Items(list) {
			name := it.Str("layer")
			if name == "" {
				out = append(out, fmt.Sprintf("%s %d: choose a layer", list, i+1))
				continue
			}
			if def := d.PaintDef(name); def != nil && def.Kind != kind {
				out = append(out, fmt.Sprintf("%s %d: layer %s is a %s layer, not %s", list, i+1, name, def.Kind, kind))
			}
		}
	}
	check("height", paint.KindHeight)
	check("water", paint.KindWater)
	for i, it := range p.Items("rivers") {
		if n := len(it.Points("path")); n < 2 {
			out = append(out, fmt.Sprintf("rivers %d: draw a path of at least two points (it has %d)", i+1, n))
		}
	}
	for i, it := range p.Items("paths") {
		if n := len(it.Points("path")); n < 2 {
			out = append(out, fmt.Sprintf("paths %d: draw a path of at least two points (it has %d)", i+1, n))
		}
	}
	return out
}

// terrainSourceStage is the terrain stage behind a terrain ref: the stage
// itself, or — for a ground_edit — the terrain or region_terrain stage it
// edits. Nil when id names nothing.
func terrainSourceStage(d *procgen.Doc, id string) *procgen.Stage {
	st := d.StageByID(id)
	for hops := 0; st != nil && st.Kind == GroundEditKind && hops < 8; hops++ {
		st = d.StageByID(st.Params.Str("terrain"))
	}
	return st
}

// refersToTerrain reports whether a terrain ref names the stage with ID
// id, directly or through ground_edits.
func refersToTerrain(d *procgen.Doc, ref, id string) bool {
	if ref == id {
		return true
	}
	st := terrainSourceStage(d, ref)
	return st != nil && st.ID == id
}
