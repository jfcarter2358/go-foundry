package stages

// placement_demo.go — the `placement_demo` template: a terrain, the rocks
// to place on it and every placement stage wired together — cliff walls
// along the traced feet of its cliffs, talus under them, boulder fields,
// rubble round the boulders, pebbles in the washes, desert plants on the
// flats. Phase 7's desert_canyon template builds on it; this one is the
// placement stages' worked example and the scratch level phase 3 measured.
//
// The rock budgets follow the per-placement cost (PHASE_3.md): what is
// placed by the thousand is light (rubble 600, boulders 2500 triangles);
// the cliff chunks and ledges, a few hundred placements, are dense.

import "foundry/internal/procgen"

func init() {
	procgen.RegisterTemplate(procgen.Template{
		Name:  "placement_demo",
		Label: "Canyon with placed rocks",
		Doc:   "A canyon terrain, its rocks (boulders, rubble, cliff chunks, ledges), cliff walls, scatter rules and desert plants.",
		New:   func(name string) *procgen.Doc { return PlacementDemo(name, 512) },
	})
}

// PlacementDemo is the template's document over a size-metre terrain.
func PlacementDemo(name string, size float64) *procgen.Doc {
	f, i, e, ref := procgen.Float, procgen.Int, procgen.Enum, procgen.StageRef
	v2 := procgen.Vec2
	half := size / 2
	canyon := procgen.List(procgen.Params{
		"path": procgen.Spline(
			[]float64{-half - 40, -half * 0.45, 60, 45}, []float64{-half * 0.3, -half * 0.2, 70, 55},
			[]float64{half * 0.3, half * 0.15, 80, 62}, []float64{half + 40, half * 0.45, 90, 70}),
		"profile": e("stepped"), "tributaries": i(3), "tributary_length": f(size * 0.25),
	})
	rules := procgen.List(
		procgen.Params{"name": procgen.String("talus"), "rocks": ref("boulders"),
			"density": f(6), "scale": v2(0.45, 1.8), "scale_bias": f(2.4), "slope": v2(0, 42), "cliff": v2(0, 0.55),
			"cliff_base": f(1), "cliff_base_distance": f(10), "cluster": f(0.35), "cluster_scale": f(14),
			"align": f(0.7), "tilt": f(18), "sink": f(0.12)},
		procgen.Params{"name": procgen.String("boulder_field"), "rocks": ref("boulders"),
			"density": f(0.6), "scale": v2(0.6, 2.4), "scale_bias": f(2.2), "slope": v2(0, 25), "deposition": v2(0.05, 1),
			"cluster": f(1), "cluster_scale": f(30), "align": f(0.5), "sink": f(0.15)},
		procgen.Params{"name": procgen.String("rubble"), "rocks": ref("rubble"),
			"density": f(0.4), "scale": v2(0.5, 1.6), "scale_bias": f(2.6), "slope": v2(0, 40), "cliff": v2(0, 0.5),
			"satellites": f(2.5), "satellite_spread": f(2.2), "max_instances": i(2500), "cluster": f(0.6), "cluster_scale": f(20),
			"align": f(0.8), "tilt": f(25), "sink": f(0.2), "spacing": f(0.9)},
		procgen.Params{"name": procgen.String("wash_pebbles"), "rocks": ref("rubble"),
			"density": f(20), "scale": v2(0.25, 0.7), "scale_bias": f(2), "slope": v2(0, 14), "flow": v2(0.35, 1),
			"cluster": f(0.5), "cluster_scale": f(8), "align": f(0.9), "tilt": f(20), "sink": f(0.3), "spacing": f(0.9)},
	)
	return &procgen.Doc{Name: name, Seed: 1234, Doc: "Canyon with placed rocks", Stages: []procgen.Stage{
		{Kind: TerrainKind, ID: "ground", Params: procgen.Params{"size": f(size), "resolution": f(0.5), "canyons": canyon,
			"mesa_scale": f(size * 0.5)}},
		{Kind: RocksKind, ID: "boulders", Params: procgen.Params{"preset": e("boulder"), "variants": i(8), "triangles": i(2500)}},
		{Kind: RocksKind, ID: "rubble", Params: procgen.Params{"preset": e("rubble"), "variants": i(8), "triangles": i(600)}},
		{Kind: RocksKind, ID: "cliffs", Params: procgen.Params{"preset": e("cliff_chunk"), "variants": i(4), "triangles": i(120000)}},
		{Kind: RocksKind, ID: "ledges", Params: procgen.Params{"preset": e("ledge_slab"), "variants": i(3), "triangles": i(12000)}},
		{Kind: CliffWallKind, ID: "walls", Params: procgen.Params{"terrain": ref("ground"), "chunks": ref("cliffs"),
			"ledges": ref("ledges"), "max_chunks": i(1200), "scale": v2(1, 3)}},
		{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "avoid": ref("walls"), "rules": rules}},
		{Kind: VegetationKind, ID: "plants", Params: procgen.Params{"terrain": ref("ground"), "rules": procgen.List(
			procgen.Params{"name": procgen.String("desert"), "biome": procgen.String("desert"), "density": f(0.15),
				"amounts": procgen.String("rock=0 pebbles=0 grass=0 cactus=0.35"), "slope": v2(0, 22), "cliff": v2(0, 0.3),
				"cluster": f(0.6), "cluster_scale": f(60)},
		)}},
	}}
}
