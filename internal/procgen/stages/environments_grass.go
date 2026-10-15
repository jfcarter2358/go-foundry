package stages

// environments_grass.go — the presets' Poly Haven grass as a grass_field
// stage (grass_field.go; docs/regions/PRESETS_2B.md).
//
// Step 2 of the presets placed every grass clump as scenery (coverStage),
// which could afford a few thousand per km² — patches over a grass texture.
// The grass instancer affords hundreds of thousands, so the presets' grass,
// reeds, sedge and dune grass are grass_field rules instead: one stage
// "grass" per preset, its rules naming the preset's plants stages. What stays
// scenery is what a scatter does well — trees, shrubs, rocks, the odd large
// plant.
//
// Poly Haven's grass pieces are a quarter of life size as scanned
// (grass_medium_01's tall_a is 0.32 m; its listing says ~1.2 m), so the
// scales below are ×3–6.

import "foundry/internal/procgen"

// grassStage is a preset's grass_field stage ("sward") over its ground, keeping out of
// its scatter's rocks.
func grassStage(rules ...procgen.Params) procgen.Stage {
	return procgen.Stage{Kind: GrassFieldKind, ID: "sward", Params: procgen.Params{"terrain": procgen.StageRef("ground"),
		"avoid": procgen.StageRef("scatter"), "rules": procgen.List(rules...)}}
}

// grassRule is a grass_field rule: the plants stage src's pieces whose names
// hold one of pieces (empty: all), density per m², scale, on slopes up to
// maxSlope, clumped by cluster over patches of patch metres, the near mesh
// capped at tris triangles.
func grassRule(name, src, pieces string, density float64, scale [2]float64, maxSlope, cluster, patch float64, tris int) procgen.Params {
	f, s, v2 := procgen.Float, procgen.String, procgen.Vec2
	return procgen.Params{"name": s(name), "plants": procgen.StageRef(src), "pieces": s(pieces), "density": f(density),
		"scale": v2(scale[0], scale[1]), "slope": v2(0, maxSlope), "cluster": f(cluster), "cluster_scale": f(patch),
		"max_triangles": procgen.Int(tris)}
}

// grassTint gives a rule its two tint colours and the drift size.
func grassTint(r procgen.Params, a, b [3]float64, scale float64) procgen.Params {
	r["tint_a"] = procgen.Color(a[0], a[1], a[2])
	r["tint_b"] = procgen.Color(b[0], b[1], b[2])
	r["tint_scale"] = procgen.Float(scale)
	return r
}

// grassWindOf sets a rule's wind.
func grassWindOf(r procgen.Params, bend, flutter float64) procgen.Params {
	r["wind_bend"] = procgen.Float(bend)
	r["wind_flutter"] = procgen.Float(flutter)
	return r
}

// grassShore keeps a rule to the shore of standing water: from..to metres
// from it, standing in up to depth metres of it.
func grassShore(r procgen.Params, from, to, depth float64) procgen.Params {
	r["shore"] = procgen.Vec2(from, to)
	r["max_depth"] = procgen.Float(depth)
	return r
}

// grassRange limits a rule to a share of the draw range and turns its
// shadows off (small flowers and sparse tufts).
func grassRange(r procgen.Params, share float64, shadows bool) procgen.Params {
	r["range"] = procgen.Float(share)
	r["shadows"] = procgen.Bool(shadows)
	return r
}
