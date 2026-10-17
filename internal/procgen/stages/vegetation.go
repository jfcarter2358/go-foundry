package stages

// vegetation.go — the `vegetation` stage: the engine's own biomes (desert,
// badlands, mesa, scrub... biomes/*.fbio) grown over a terrain where its
// masks allow, as `kind: biome` scatter volumes.
//
// A biome volume grows its species on whatever ground a column cast from
// its top finds (world.GrowVolumePlants over biome.GroundQuery); since
// phase 1 that sees mesh-collision scenery, so it grows on the terrain
// stage's tiles. A volume is a box, so the masks are honoured by WHICH boxes
// there are: each rule weighs the ground with the same bands a scatter rule
// has (slope, height, flow, deposition, curvature, cliff, cliff base,
// clumping), the cells over its threshold are merged into rectangles
// (place.Patches) and each rectangle becomes one volume, its density the
// rule's times the cells' mean weight, spanning the ground's heights under
// it. The biome's own slope limits still apply per plant.
//
// A rule's `amounts` scales the biome's species by shape key, the volume's
// own amounts: the default turns the biome's rocks and pebbles off, since
// the scatter stage places real ones.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"foundry/internal/procgen"
	"foundry/internal/procgen/place"
)

// VegetationKind is the stage's name.
const VegetationKind = "vegetation"

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     VegetationKind,
		Label:    "Vegetation",
		Category: "Placement",
		// Per environment region of a level generation doc (place_region.go).
		Regional: procgen.RegionalPerRegion,
		Version:  1,
		Doc: "Sparse plants from the engine's biomes (desert, badlands, mesa...) grown on a terrain where its masks allow: " +
			"each rule's weighted cells merged into rectangles, each a `kind: biome` scatter volume over the terrain's tiles.",
		Params:   vegetationParams(),
		Run:      runVegetation,
		Preview:  previewVegetation,
		Validate: validateVegetation,
	})
}

func vegetationParams() []procgen.ParamDef {
	f := procgen.Float
	fields := []procgen.ParamDef{
		{Name: "name", Type: procgen.ParamString, Default: procgen.String("plants"), Doc: "The rule's name: its volumes' IDs start with it."},
		{Name: "enabled", Type: procgen.ParamBool, Default: procgen.Bool(true), Doc: "Off skips the rule."},
		{Name: "biome", Type: procgen.ParamString, Default: procgen.String("desert"),
			Doc: "The biome to grow: a file in biomes/ or a built-in (desert, badlands, mesa, scrub, savannah...)."},
		{Name: "density", Type: procgen.ParamFloat, Default: f(0.5), Min: 0, Max: 8, Step: 0.01,
			Doc: "The biome's density (1 as the biome has it), times each rectangle's mean weight."},
		{Name: "amounts", Type: procgen.ParamString, Default: procgen.String("rock=0 pebbles=0"),
			Doc: "Per-species amounts by shape key, `key=value` separated by spaces (0 none, 1 as the biome has it, up to 4): " +
				"e.g. `rock=0 pebbles=0 grass=0.3 deadwood=2`. The default leaves the rocks to the scatter stage."},
		{Name: "tint", Type: procgen.ParamColor, Default: procgen.Color(1, 1, 1), Min: 0, Max: 2,
			Doc: "Multiplies every colour in the biome (white: none)."},
		{Name: "color", Type: procgen.ParamColor, Label: "Outline colour", Default: procgen.Color(1, 1, 1), Min: 0, Max: 1,
			Doc: "The rule's rectangles in the 2D view. White picks a colour per rule."},
		{Name: "threshold", Type: procgen.ParamFloat, Default: f(0.4), Min: 0, Max: 1.5, Step: 0.01,
			Doc: "The mean weight a cell needs to grow anything."},
	}
	fields = append(fields, bandParams()...)
	return []procgen.ParamDef{
		{Name: "terrain", Type: procgen.ParamStage, StageKinds: TerrainSources, Default: procgen.StageRef(""),
			Doc: "The terrain stage to grow on."},
		{Name: "seed", Type: procgen.ParamSeed, Default: procgen.Seed(0), Doc: "Moves the clumps."},
		{Name: "rules", Type: procgen.ParamList, MinItems: 1, Fields: fields, Default: procgen.List(),
			Doc: "The rules: each a biome and where it grows."},
		{Name: "cell", Type: procgen.ParamFloat, Label: "Cell (m)", Default: f(16), Min: 1, Max: 1000, Step: 0.5,
			Doc: "The grid the weights are judged on: finer follows the masks closer with more volumes."},
		{Name: "max_size", Type: procgen.ParamFloat, Label: "Max volume (m)", Default: f(96), Min: 1, Max: 10000, Step: 1,
			Doc: "The longest side a volume may have (each regrows on its own when edited, and large ones take longer)."},
		{Name: "headroom", Type: procgen.ParamFloat, Label: "Headroom (m)", Default: f(4), Min: 0.5, Max: 1000, Step: 0.5,
			Doc: "How far a volume reaches above the highest ground under it (and below the lowest)."},
		// Added: 0 (the default) is the classic, hard-edged rectangles, so
		// no existing stage's output or hash moves (docs/regions/PHASE_6.md).
		{Name: "fray", Added: true, Type: procgen.ParamFloat, Label: "Fray (m)", Default: f(0), Min: 0, Max: 200, Step: 0.5,
			Doc: "Cross-fades neighbouring volumes: each grows by 0.625 × fray past its cells on every side and fades its " +
				"plants out over fray at its edge (its edge_fade_distance), so where two volumes of different density meet their " +
				"plants blend over the overlap instead of stepping along a ruled line, and the outer edge of a patch is ragged. " +
				"0: hard rectangles. About three quarters of the cell is a good start."},
	}
}

// parseAmounts reads `key=value` pairs.
func parseAmounts(s string) (map[string]interface{}, error) {
	out := map[string]interface{}{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == ';' || r == '\t' }) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			k, v, ok = strings.Cut(f, ":")
		}
		if !ok || k == "" {
			return nil, fmt.Errorf("amount %q is not key=value", f)
		}
		x, err := strconv.ParseFloat(v, 64)
		if err != nil || x < 0 || x > 4 {
			return nil, fmt.Errorf("amount %q: the value must be a number from 0 to 4", f)
		}
		out[k] = x
	}
	return out, nil
}

func validateVegetation(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	var out []string
	if p.Str("terrain") == "" {
		out = append(out, "Choose a terrain stage")
	}
	for i, it := range p.Items("rules") {
		if _, err := parseAmounts(it.Str("amounts")); err != nil {
			out = append(out, fmt.Sprintf("rule %d: %v", i+1, err))
		}
		if strings.TrimSpace(it.Str("biome")) == "" {
			out = append(out, fmt.Sprintf("rule %d names no biome", i+1))
		}
	}
	return out
}

// vegetationPatches is each enabled rule's rectangles (index-aligned with
// the rules list; nil for a skipped rule).
func vegetationPatches(env *procgen.Env, g *place.Ground) ([][]place.Patch, error) {
	p := env.Params
	items := p.Items("rules")
	out := make([][]place.Patch, len(items))
	for i, it := range items {
		if !it.Bool("enabled") {
			continue
		}
		r := place.DefaultRule()
		bandRule(it, &r)
		ps, err := place.Patches(g, &r, place.PatchSettings{Cell: p.Float("cell"), Threshold: it.Float("threshold"),
			MaxSize: p.Float("max_size"), Seed: noiseSeed(env.Seed, i), Region: placeRegion(env)}, env)
		if err != nil {
			return nil, err
		}
		out[i] = ps
		env.Progress(float32(i+1) / float32(len(items)))
	}
	return out, nil
}

func noiseSeed(seed uint64, i int) uint64 { return seed ^ uint64(i+1)*0x9e3779b97f4a7c15 }

func runVegetation(rc *procgen.RunContext) error {
	p := rc.Params
	t0 := time.Now()
	g, err := runGround(rc, p.Str("terrain"))
	if err != nil {
		return err
	}
	patches, err := vegetationPatches(&rc.Env, g)
	if err != nil {
		return err
	}
	head := p.Float("headroom")
	// fray: each rectangle grows by fray·(0.35+0.9)/2 a side, so two
	// neighbours overlap by fray·1.25 and their edge fades — smoothstep(0.35,
	// 0.9, inset/fray) in world.GrowVolumePlants — are complementary across
	// it (S(t) + S(1.25 − t) = 1): their densities cross-fade.
	fray := p.Float("fray")
	grow := fray * (0.35 + 0.9) / 2
	total := 0
	for i, it := range p.Items("rules") {
		if patches[i] == nil {
			continue
		}
		amounts, err := parseAmounts(it.Str("amounts"))
		if err != nil {
			return err
		}
		name := safeName(it.Str("name"), fmt.Sprintf("rule%d", i+1))
		area := 0.0
		for k, pt := range patches[i] {
			biome := map[interface{}]interface{}{
				"preset":  strings.TrimSpace(it.Str("biome")),
				"density": round3(it.Float("density") * math.Min(pt.Weight, 1.5)),
			}
			if len(amounts) > 0 {
				am := map[interface{}]interface{}{}
				for k, v := range amounts {
					am[k] = v
				}
				biome["amounts"] = am
			}
			if c := it.Vec3("tint"); c != [3]float64{1, 1, 1} {
				biome["tint"] = map[interface{}]interface{}{"r": c[0], "g": c[1], "b": c[2]}
			}
			cx, cz := (pt.MinX+pt.MaxX)/2, (pt.MinZ+pt.MaxZ)/2
			y0, y1 := pt.MinY-head, pt.MaxY+head
			scatter := map[interface{}]interface{}{"kind": "biome", "biome": biome}
			if fray > 0 {
				scatter["edge_fade_distance"] = round2(fray)
			}
			rc.PlaceVolume(procgen.VolumeEntry{
				"id":       fmt.Sprintf("%s_%04d", name, k+1),
				"position": map[interface{}]interface{}{"x": round2(cx), "y": round2((y0 + y1) / 2), "z": round2(cz)},
				"half": map[interface{}]interface{}{"x": round2((pt.MaxX-pt.MinX)/2 + grow), "y": round2((y1 - y0) / 2),
					"z": round2((pt.MaxZ-pt.MinZ)/2 + grow)},
				"scatter": scatter,
			})
			area += (pt.MaxX - pt.MinX) * (pt.MaxZ - pt.MinZ)
		}
		total += len(patches[i])
		rc.Logf("%s: %d volumes of %s over %.0f m²", name, len(patches[i]), it.Str("biome"), area)
	}
	rc.Logf("%d volumes in %s", total, time.Since(t0).Round(time.Millisecond))
	rc.Progress(1)
	return nil
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func previewVegetation(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
	p := pc.Params
	t0 := time.Now()
	g, up, err := previewGround(pc, p.Str("terrain"))
	if err != nil {
		return nil, err
	}
	patches, err := vegetationPatches(&pc.Env, g)
	if err != nil {
		return nil, err
	}
	out := &procgen.PreviewData{Bounds: up.Bounds, Images: up.Images}
	out.Meshes = append(out.Meshes, terrainMeshes(up)...)
	for i, it := range p.Items("rules") {
		col := ruleColor(it, i)
		area := 0.0
		for _, pt := range patches[i] {
			out.Lines = append(out.Lines, procgen.LineLayer{Name: it.Str("name"), Closed: true, Color: col,
				Points: [][2]float64{{pt.MinX, pt.MinZ}, {pt.MaxX, pt.MinZ}, {pt.MaxX, pt.MaxZ}, {pt.MinX, pt.MaxZ}}})
			area += (pt.MaxX - pt.MinX) * (pt.MaxZ - pt.MinZ)
		}
		if patches[i] != nil {
			out.Notes = append(out.Notes, fmt.Sprintf("%s: %d volumes of %s over %.0f m²", it.Str("name"), len(patches[i]), it.Str("biome"), area))
		}
	}
	out.Notes = append(out.Notes, fmt.Sprintf("the plants themselves grow in the level (biome volumes); %s", time.Since(t0).Round(time.Millisecond)))
	return out, nil
}
