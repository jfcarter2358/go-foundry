package stages

// regions.go — the `regions` stage: a level generation doc's environment
// regions (docs/regions/SPEC.md, phase 1).
//
// The first stage of every level generation doc (the level_world template,
// level_world.go). Its params hold the level's region volumes — copied in,
// sorted by id, by genlevel.SyncRegions before every run and every preview
// with the level open, because a stage's hash may depend only on its params
// — plus the settings every region shares:
//
//	regions        the synced list (READ-ONLY in the editor: the level is the
//	               source of truth): id, environment, generator, priority,
//	               transition, anchor/anchored, footprint (a closed spline)
//	warp_amount,   the boundary meander: a domain warp of up to warp_amount
//	warp_scale     metres, warp_scale metres across, so a band follows a
//	               natural line instead of the straight sides clicked in
//	resolution,    the union terrain's (region_terrain's) sample spacing,
//	tile_size,     its world tile grid, the overlap round each tiled-erosion
//	margin,        window, and whether erosion runs once over the union
//	erosion        (global) or in overlapping windows with fixed seeds
//	               (tiled). region_terrain reads them from here.
//
// What it does:
//
//   - Validate refuses a region list procgen/regions refuses (too few
//     points, crossing edges, duplicate keys, no environment) and a broken
//     resources/environments/adjacency.yaml.
//   - The adjacency table (adjacency.go): a region left at the default
//     transition takes the table's width against each neighbouring
//     environment (in the hash, through HashExtra), and Warn lists the
//     forbidden pairs that touch — warnings; the run proceeds.
//   - Expand (procgen/regional.go) hands the run engine each region with its
//     preset document — the generator override, or the environment's preset
//     (the project's file, else the built-in) — and its weight function, so
//     the presets' PerRegion stages run once per region.
//   - Run writes regions.yaml, the resolved set (keys, anchors, bounds), and
//     sets the *regions.Set as its artifact; Load reads it back.
//   - Preview paints the biome overlay: each pixel the environments' colours
//     mixed by their weights, the regions' outlines over it, and one grey
//     weight layer per region for the hover readout.
//
// The kind is Shared: one stage serves every region, and a region's preset
// has no regions stage of its own to expand.

import (
	"fmt"
	"math"
	"strings"

	"foundry/internal/procgen"
	"foundry/internal/procgen/regions"
)

// RegionsKind is the stage's name.
const RegionsKind = regions.StageKind

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     RegionsKind,
		Label:    "Regions",
		Category: "Level",
		Regional: procgen.RegionalShared,
		Version:  1,
		Doc: "The level's environment regions, copied from its region volumes before every run (the level is the " +
			"source of truth: edit the footprints in the level editor). Each region grows its environment preset; " +
			"they blend across each region's transition band, with a meandering edge. Previews as a biome-colour overlay.",
		Params:   regionsParams(),
		Run:      runRegions,
		Load:     loadRegions,
		Preview:  previewRegions,
		Validate: validateRegions,
		Expand:   expandRegions,
		// The adjacency table's pair widths the regions take (adjacency.go),
		// and the forbidden pairs that touch, as warnings.
		HashExtra: regionsPairHash,
		Warn:      regionsWarnings,
	})
}

func regionsParams() []procgen.ParamDef {
	f := procgen.Float
	half := 256.0
	return []procgen.ParamDef{
		{Name: regions.ParamRegions, Type: procgen.ParamList, Label: "Regions", Default: procgen.List(), ReadOnly: true,
			Doc: "The level's region volumes, synced before every run (sorted by id). Read-only here: move a region's " +
				"footprint points in the level editor (select the volume; drag a point, Ctrl+click an edge to add one, " +
				"right-click a point to delete it).",
			Fields: []procgen.ParamDef{
				{Name: regions.FieldID, Type: procgen.ParamString, Label: "ID", Default: procgen.String(""),
					Doc: "The level volume's id. Expanded stages are named r_<id>_<stage>."},
				{Name: regions.FieldEnvironment, Type: procgen.ParamString, Label: "Environment", Default: procgen.String(DesertCanyonEnvironment),
					Doc: "The environment preset: resources/environments/<name>.fgen, or the built-in of that name."},
				{Name: regions.FieldGenerator, Type: procgen.ParamString, Label: "Generator", Default: procgen.String(""),
					Doc: "Optional: a .fgen to grow instead of the environment's preset (a preset name, or a path under resources/)."},
				{Name: regions.FieldPriority, Type: procgen.ParamInt, Default: procgen.Int(0), Min: -1000, Max: 1000,
					Doc: "Higher wins where regions overlap; equal priorities share the band evenly."},
				{Name: regions.FieldTransition, Type: procgen.ParamFloat, Label: "Transition (m)", Default: f(regions.DefaultTransition), Min: 1, Max: 10000,
					Doc: "The blend band's width, centred on the footprint's edge."},
				{Name: regions.FieldAnchored, Type: procgen.ParamBool, Label: "Anchored", Default: procgen.Bool(false),
					Doc: "Use Anchor as the preset's origin; off measures it from the footprint's centroid."},
				{Name: regions.FieldAnchor, Type: procgen.ParamVec2, Label: "Anchor", Default: procgen.Vec2(0, 0),
					Doc: "World XZ the preset's own coordinates (canyon paths) are measured from, when Anchored."},
				{Name: regions.FieldFootprint, Type: procgen.ParamSpline, Label: "Footprint", Closed: true, MinItems: 3,
					Default: procgen.Spline([]float64{-half, -half}, []float64{half, -half}, []float64{half, half}, []float64{-half, half}),
					Doc:     "The region's outline in world XZ: a closed polygon, concave allowed."},
			}},
		{Name: "warp_amount", Type: procgen.ParamFloat, Label: "Edge meander (m)", Default: f(40), Min: 0, Max: 2000,
			Doc: "How far the boundary wanders from the footprint's straight edges (a domain warp). 0 keeps them straight."},
		{Name: "warp_scale", Type: procgen.ParamFloat, Label: "Meander length (m)", Default: f(300), Min: 10, Max: 20000,
			Doc: "The wavelength of the boundary's wander."},
		{Name: "resolution", Type: procgen.ParamFloat, Label: "Resolution (m)", Default: f(0.5), Min: 0.25, Max: 16, Step: 0.05,
			Doc: "The union terrain's (region_terrain's) sample spacing. Must divide Tile size."},
		{Name: "tile_size", Type: procgen.ParamFloat, Label: "Tile size (m)", Default: f(128), Min: 16, Max: 128,
			Doc: "The world tile grid every region's terrain tiles share: the union terrain is whole tiles from the world origin."},
		{Name: "margin", Type: procgen.ParamFloat, Label: "Erosion margin (m)", Default: f(64), Min: 0, Max: 2000,
			Doc: "With tiled erosion, the overlap round each window; window seams are cross-faded over this width."},
		{Name: "erosion", Type: procgen.ParamEnum, Default: procgen.Enum("global"), Choices: []string{"global", "tiled"},
			Doc: "Erosion once over the union (global: rivers cross the whole world), or in overlapping windows with fixed " +
				"seeds so editing one region leaves distant tiles byte-identical (tiled)."},
		{Name: "preview_size", Type: procgen.ParamInt, Label: "Preview size (px)", Default: procgen.Int(384), Min: 64, Max: 2048,
			PreviewOnly: true, Doc: "The overlay's longer side, in pixels."},
		// A template's one-shot layout (genlevel/regions_seed.go): Added, so
		// at its default (off — every doc but a fresh template's) it is not
		// hashed and no existing world doc goes stale.
		{Name: regions.ParamSeedLevel, Added: true, Type: procgen.ParamBool, Label: "Seed the level", Default: procgen.Bool(false),
			Doc: "Set by a template that ships a region layout (canyon_savannah): before the first run, when the target level " +
				"has no region volumes, this list is written into the level as region volumes, then this switch turns itself off " +
				"and the level leads as usual."},
	}
}

// regionsFromParams is the region list as procgen/regions reads it.
func regionsFromParams(p procgen.Params) []regions.Region {
	items := p.Items(regions.ParamRegions)
	out := make([]regions.Region, 0, len(items))
	for _, it := range items {
		r := regions.Region{
			ID:          it.Str(regions.FieldID),
			Environment: strings.TrimSpace(it.Str(regions.FieldEnvironment)),
			Generator:   strings.TrimSpace(it.Str(regions.FieldGenerator)),
			Priority:    it.Int(regions.FieldPriority),
			Transition:  it.Float(regions.FieldTransition),
			HasAnchor:   it.Bool(regions.FieldAnchored),
			Anchor:      it.Vec2(regions.FieldAnchor),
		}
		r.Key = regions.Key(r.ID)
		for _, pt := range it.Points(regions.FieldFootprint) {
			if len(pt) >= 2 {
				r.Footprint = append(r.Footprint, regions.Point{pt[0], pt[1]})
			}
		}
		out = append(out, r)
	}
	return out
}

// regionWarp is the boundary meander for a stage seeded seed.
func regionWarp(p procgen.Params, seed uint64) regions.Warp {
	return regions.Warp{Amount: p.Float("warp_amount"), Scale: p.Float("warp_scale"), Seed: seed}
}

// regionSet builds the set from resolved params, each default-transition
// region taking the project adjacency table's pair widths (adjacency.go).
func regionSet(p procgen.Params, seed uint64) (*regions.Set, error) {
	rs, err := resolvedRegions(p)
	if err != nil {
		return nil, err
	}
	return regions.NewSet(rs, regionWarp(p, seed))
}

// regionsSeed is the stage's seed, as the run engine computes it — the hooks
// that are not handed an Env (Validate, Expand) need it for the warp.
func regionsSeed(d *procgen.Doc, s *procgen.Stage, p procgen.Params) uint64 {
	return procgen.StageSeed(d.Seed, s.ID, regionsParams(), p)
}

func validateRegions(d *procgen.Doc, s *procgen.Stage, p procgen.Params) []string {
	out := regions.Validate(regionsFromParams(p))
	if _, err := ProjectAdjacency(); err != nil {
		out = append(out, err.Error())
	}
	return out
}

// expandRegions hands the run engine each region and its preset.
func expandRegions(d *procgen.Doc, s *procgen.Stage, p procgen.Params) ([]procgen.RegionExpansion, error) {
	set, err := regionSet(p, regionsSeed(d, s, p))
	if err != nil {
		return nil, err
	}
	out := make([]procgen.RegionExpansion, 0, set.Len())
	for i := range set.Regions {
		r := &set.Regions[i]
		var pre *procgen.Doc
		var from string
		if r.Generator != "" {
			pre, from, err = procgen.LoadGenerator(r.Generator)
		} else {
			pre, from, err = procgen.LoadEnvironment(r.Environment)
		}
		if err != nil {
			return nil, fmt.Errorf("region %s: %w", r.ID, err)
		}
		out = append(out, procgen.RegionExpansion{Preset: pre, Region: procgen.RegionContext{
			Key: r.Key, ID: r.ID, Environment: r.Environment, Preset: from,
			Weight: set.WeightFunc(i),
			Bounds: procRect(set.RegionBounds(i)), Band: procRect(set.BandBounds(i)),
			Anchor: r.AnchorPoint(),
		}})
	}
	return out, nil
}

func procRect(r regions.Rect) procgen.Rect {
	return procgen.Rect{MinX: r.MinX, MinZ: r.MinZ, MaxX: r.MaxX, MaxZ: r.MaxZ}
}

// environmentColor is an environment's overlay colour, clamped to 0..1.
func environmentColor(env string) [3]float64 {
	c := procgen.EnvironmentColor(env)
	for k := range c {
		c[k] = math.Max(0, math.Min(1, c[k]))
	}
	return c
}
