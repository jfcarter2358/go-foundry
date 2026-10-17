package stages

// place_common.go — what the placement stages (scatter, cliff_wall,
// vegetation) share: the mask-band parameters a rule is filtered by, the
// rocks stages' variants as internal/procgen/place sees them (from a run's
// manifest, or from a rocks preview's one variant), the instances as level
// placements, and the preview's instanced proxies.
//
// The preview of a placement stage runs the same placement code as its run,
// over the upstream terrain's PREVIEW (a coarse grid with the same
// landforms) and with each rocks stage's previewed variant standing in for
// all of its variants: the counts, the clumping and the spacing are the
// run's, give or take the coarser ground. In the 3D view the rocks are one
// decimated proxy of that variant per rule, drawn instanced
// (procgen.PreviewMesh.Instances), so thousands cost one draw.

import (
	"fmt"
	"math"

	"foundry/internal/cluster"
	"foundry/internal/procgen"
	"foundry/internal/procgen/place"
	"foundry/internal/procgen/rocks"
	"foundry/internal/procgen/terrain"
)

// RockSources are the stage kinds whose product is a RocksArtifact — what
// scatter and cliff_wall place: the generated rocks, and the downloaded scans
// a later stage describes in the same manifest format. The placement stages
// read the artifact, never the kind, so a new source only joins this list.
var RockSources = []string{RocksKind, "polyhaven_rocks"}

// sizedVariants keeps the variants whose largest side is at least min
// (0: all), or the largest one when none is.
func sizedVariants(vs []place.Variant, min float64) []place.Variant {
	if min <= 0 || len(vs) == 0 {
		return vs
	}
	side := func(v place.Variant) float64 { return math.Max(v.Extent[0], math.Max(v.Extent[1], v.Extent[2])) }
	var out []place.Variant
	big := 0
	for i, v := range vs {
		if side(v) >= min {
			out = append(out, v)
		}
		if side(v) > side(vs[big]) {
			big = i
		}
	}
	if len(out) == 0 {
		return []place.Variant{vs[big]}
	}
	return out
}

// lightVariants keeps the variants of at most max triangles (0: all) —
// what a dense rule places by the thousand — or, when none is that light,
// the lightest one.
func lightVariants(vs []place.Variant, max int) []place.Variant {
	if max <= 0 || len(vs) == 0 {
		return vs
	}
	var out []place.Variant
	least := 0
	for i, v := range vs {
		if v.Triangles <= max {
			out = append(out, v)
		}
		if v.Triangles < vs[least].Triangles {
			least = i
		}
	}
	if len(out) == 0 {
		return []place.Variant{vs[least]}
	}
	return out
}

// bandParams are the mask filters a rule (scatter's, vegetation's) carries:
// see place.Rule.
func bandParams() []procgen.ParamDef {
	v2 := procgen.Vec2
	f := procgen.Float
	unitBand := func(name, label, doc string) procgen.ParamDef {
		return procgen.ParamDef{Name: name, Type: procgen.ParamVec2, Label: label, Default: v2(0, 1), Min: 0, Max: 1, Step: 0.01, Doc: doc}
	}
	return []procgen.ParamDef{
		{Name: "slope", Type: procgen.ParamVec2, Label: "Slope (°)", Default: v2(0, 40), Min: 0, Max: 90, Step: 0.5,
			Doc: "The ground slopes it grows on, degrees (from, to)."},
		{Name: "slope_soft", Type: procgen.ParamFloat, Label: "Slope fade (°)", Default: f(6), Min: 0, Max: 45, Step: 0.1,
			Doc: "How many degrees beyond the slope band the weight fades out over."},
		unitBand("height", "Height", "The heights it grows at, as a fraction of the terrain's range (0 its lowest, 1 its highest)."),
		unitBand("flow", "Flow", "The flow mask's band: 0 is dry ground, toward 1 washes and canyon floors that carry water."),
		unitBand("deposition", "Deposition", "The deposition mask's band: toward 1 where sediment settled (fans, talus, wash floors)."),
		unitBand("curvature", "Curvature", "The curvature mask's band: 0.5 is flat, above it hollows, below it crests."),
		{Name: "cliff", Type: procgen.ParamVec2, Label: "Cliff", Default: v2(0, 0.6), Min: 0, Max: 1, Step: 0.01,
			Doc: "The cliff mask's band: keep it low to stay off the walls themselves."},
		{Name: "soft", Type: procgen.ParamFloat, Label: "Band fade", Default: f(0.1), Min: 0, Max: 1, Step: 0.01,
			Doc: "How far beyond each 0..1 mask band the weight fades out over."},
		{Name: "cliff_base", Type: procgen.ParamFloat, Label: "Cliff base", Default: f(0), Min: 0, Max: 1, Step: 0.01,
			Doc: "How strongly it keeps to the FOOT of cliffs — talus under a wall: 0 ignores cliffs, 1 grows only within the distance below one."},
		{Name: "cliff_base_distance", Type: procgen.ParamFloat, Label: "Cliff base distance (m)", Default: f(25), Min: 0.5, Max: 500, Step: 0.5,
			Doc: "How far out from a cliff's foot the cliff-base weight reaches."},
		{Name: "cluster", Type: procgen.ParamFloat, Default: f(0.5), Min: 0, Max: 1, Step: 0.01,
			Doc: "Clumping: 0 spreads it evenly over the ground its bands allow, 1 gathers it in patches with bare ground between."},
		{Name: "cluster_scale", Type: procgen.ParamFloat, Label: "Cluster size (m)", Default: f(25), Min: 0.5, Max: 1000, Step: 0.5,
			Doc: "The patches' size."},
		// Added: an empty area is the whole ground, so no rule's hash moves.
		{Name: "near", Added: true, Type: procgen.ParamSpline, Label: "Area", Default: procgen.Spline(),
			Channels: []string{"radius"}, ChannelDefaults: []float64{20},
			Doc: "Keep the rule to an area: within each point's radius of the path through the points (one point: a disc) — " +
				"rubble along a slot canyon's floor, plants down a stairway. Empty: the whole ground."},
		{Name: "near_soft", Added: true, Type: procgen.ParamFloat, Label: "Area fade (m)", Default: f(4), Min: 0, Max: 500, Step: 0.5,
			Doc: "How far past the area's radius the rule fades out."},
	}
}

// bandRule reads the band params of a rule item into r.
func bandRule(it procgen.Params, r *place.Rule) {
	r.Slope, r.SlopeSoft = it.Vec2("slope"), it.Float("slope_soft")
	r.Height, r.Flow, r.Deposition = it.Vec2("height"), it.Vec2("flow"), it.Vec2("deposition")
	r.Curvature, r.Cliff, r.Soft = it.Vec2("curvature"), it.Vec2("cliff"), it.Float("soft")
	r.CliffBase, r.CliffBaseDistance = it.Float("cliff_base"), it.Float("cliff_base_distance")
	r.Cluster, r.ClusterScale = it.Float("cluster"), it.Float("cluster_scale")
	r.Near = nil
	for _, q := range it.Points("near") {
		if len(q) >= 3 {
			r.Near = append(r.Near, [3]float64{q[0], q[1], q[2]})
		}
	}
	r.NearSoft = it.Float("near_soft")
}

// placeVariant is a rocks manifest entry as place sees it.
func placeVariant(v *rocks.VariantMeta, model string) place.Variant {
	f := func(x float32) float64 { return float64(x) }
	pv := place.Variant{
		Name: v.Name, Model: model, Size: v.Size,
		Extent: [3]float64{f(v.Extent[0]), f(v.Extent[1]), f(v.Extent[2])}, Height: f(v.Height),
		Radius: f(v.Radius), FootprintRadius: f(v.FootprintRadius), BaseBand: f(v.BaseBand), Top: f(v.Top),
		Back: v.Back != nil, FlatBase: v.FlatBase,
		Min: [3]float64{f(v.Min[0]), f(v.Min[1]), f(v.Min[2])}, Max: [3]float64{f(v.Max[0]), f(v.Max[1]), f(v.Max[2])}, Collision: v.Collision, PlaceCollision: v.PlaceCollision,
		Triangles: v.Triangles,
	}
	if v.Back != nil {
		pv.BackDir = [3]float64{f(v.Back[0]), f(v.Back[1]), f(v.Back[2])}
	}
	return pv
}

// runVariants is a rocks stage's variants, from this run (or its files).
func runVariants(rc *procgen.RunContext, id string) ([]place.Variant, *RocksArtifact, error) {
	a, err := rc.UpstreamArtifact(id)
	if err != nil {
		return nil, nil, err
	}
	art, ok := a.(*RocksArtifact)
	if !ok || art.Manifest == nil {
		return nil, nil, fmt.Errorf("stage %s is not a rocks stage", id)
	}
	var out []place.Variant
	for i := range art.Manifest.Variants {
		v := &art.Manifest.Variants[i]
		out = append(out, placeVariant(v, art.ModelPath(v)))
	}
	if len(out) == 0 {
		return nil, nil, fmt.Errorf("rocks stage %s has no variants", id)
	}
	return out, art, nil
}

// runGround is the terrain stage's ground for this run.
func runGround(rc *procgen.RunContext, id string) (*place.Ground, error) {
	if id == "" {
		return nil, fmt.Errorf("choose a terrain stage")
	}
	a, err := rc.UpstreamArtifact(id)
	if err != nil {
		return nil, err
	}
	t, ok := a.(*terrain.Terrain)
	if !ok {
		return nil, fmt.Errorf("stage %s is not a terrain stage", id)
	}
	return place.NewGround(t, terrain.MaskFlow, terrain.MaskDeposition, terrain.MaskCurvature, terrain.MaskCliff)
}

// previewGround is the terrain stage's preview ground (nil when it has
// none).
func previewGround(pc *procgen.PreviewContext, id string) (*place.Ground, *procgen.PreviewData, error) {
	up := pc.Upstream(id)
	if up == nil {
		return nil, nil, fmt.Errorf("the terrain stage %q has no preview", id)
	}
	t, ok := up.Artifact.(*terrain.Terrain)
	if !ok {
		return nil, nil, fmt.Errorf("stage %s is not a terrain stage", id)
	}
	g, err := place.NewGround(t, terrain.MaskFlow, terrain.MaskDeposition, terrain.MaskCurvature, terrain.MaskCliff)
	return g, up, err
}

// previewRock is a rocks stage's previewed variant as place sees it, with
// the variant's mesh: the preview's stand-in for every variant. When the
// rocks stage has no preview, a nominal variant of its preset's size and
// no mesh.
func previewRock(pc *procgen.PreviewContext, id string) (place.Variant, *rocks.Mesh) {
	if up := pc.Upstream(id); up != nil {
		if r, ok := up.Artifact.(*rocks.Result); ok && r != nil {
			return placeVariant(&r.Meta, ""), r.Mesh
		}
		// A manifest without a mesh (the Poly Haven stages' previews: what
		// is imported so far) stands in by its first variant's measurements.
		if a, ok := up.Artifact.(*RocksArtifact); ok && a != nil && a.Manifest != nil && len(a.Manifest.Variants) > 0 {
			v := &a.Manifest.Variants[0]
			return placeVariant(v, a.ModelPath(v)), nil
		}
	}
	preset := "boulder"
	if s := pc.Doc.StageByID(id); s != nil {
		if v, ok := s.Params["preset"]; ok {
			preset = v.Str
		}
	}
	_, size, _, _ := rocks.PresetDoc(preset)
	return place.Variant{Name: preset, Size: size, Extent: [3]float64{size, size * 0.7, size}, Height: size * 0.7,
		Radius: size / 2, FootprintRadius: size * 0.45, BaseBand: size * 0.08, Top: size * 0.65,
		Back: preset == "cliff_chunk", FlatBase: preset != "boulder" && preset != "rubble" && preset != "pebble",
		Collision: "hulls", PlaceCollision: "hulls"}, nil
}

// placementOf is an instance as a level placement.
func placementOf(id string, in *place.Instance, v *place.Variant) procgen.Placement {
	e := in.Euler()
	s := float32(in.Scale)
	return procgen.Placement{
		ID: id, Model: v.Model,
		Position:  procgen.Point3{X: float32(in.Pos[0]), Y: float32(in.Pos[1]), Z: float32(in.Pos[2])},
		Rotation:  procgen.Point3{X: float32(e[0]), Y: float32(e[1]), Z: float32(e[2])},
		Scale:     &procgen.Point3{X: s, Y: s, Z: s},
		Collision: in.Collision, Chunk: procgen.ChunkAuto,
	}
}

// instanceMatrix is an instance's model matrix, column-major: T·R·S.
func instanceMatrix(in *place.Instance) [16]float32 {
	var m [16]float32
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			m[c*4+r] = float32(in.Rot[r][c] * in.Scale)
		}
	}
	m[12], m[13], m[14], m[15] = float32(in.Pos[0]), float32(in.Pos[1]), float32(in.Pos[2]), 1
	return m
}

// proxyMesh is m decimated to about tris triangles for the instanced
// preview (the colours dropped from the simplifier's attributes but kept
// per surviving vertex). nil when m is.
func proxyMesh(name string, m *rocks.Mesh, tris int) procgen.PreviewMesh {
	pm := procgen.PreviewMesh{Name: name}
	if m == nil || len(m.Indices) == 0 {
		return pm
	}
	verts := make([]float32, 0, len(m.Positions)*6)
	for i, p := range m.Positions {
		n := m.Normals[i]
		verts = append(verts, p[0], p[1], p[2], n[0], n[1], n[2])
	}
	idx := m.Indices
	if m.TriangleCount() > tris {
		ext := 0.0
		for _, p := range m.Positions {
			ext = math.Max(ext, math.Abs(float64(p[0]))+math.Abs(float64(p[1]))+math.Abs(float64(p[2])))
		}
		w := float32(0.02 * ext)
		idx = cluster.Simplify(verts, 6, m.Indices, cluster.SimplifyOptions{TargetTriangles: tris,
			AttrWeights: []float32{w, w, w}}).Indices
	}
	remap := map[uint32]uint32{}
	for _, ix := range idx {
		if _, ok := remap[ix]; !ok {
			remap[ix] = uint32(len(pm.Positions))
			pm.Positions = append(pm.Positions, m.Positions[ix])
			pm.Normals = append(pm.Normals, m.Normals[ix])
			if len(m.Colors) == len(m.Positions) {
				pm.Colors = append(pm.Colors, m.Colors[ix])
			}
		}
		pm.Indices = append(pm.Indices, remap[ix])
	}
	return pm
}

// rulePalette colours a rule's dots when it has no colour of its own.
var rulePalette = [][4]float32{
	{1, 0.55, 0.2, 0.95}, {0.35, 0.8, 1, 0.95}, {0.95, 0.9, 0.3, 0.95}, {0.7, 0.45, 1, 0.95},
	{0.4, 1, 0.5, 0.95}, {1, 0.4, 0.6, 0.95}, {0.85, 0.85, 0.85, 0.95}, {0.3, 0.55, 1, 0.95},
}

// ruleColor is the rule's dot colour: its own, or the palette's by index
// while it is at the default (white).
func ruleColor(it procgen.Params, i int) [4]float32 {
	c := it.Vec3("color")
	if c == [3]float64{1, 1, 1} {
		return rulePalette[i%len(rulePalette)]
	}
	return [4]float32{float32(c[0]), float32(c[1]), float32(c[2]), 0.95}
}

// safeName is s as an ID part: lower-case letters, digits and underscores.
func safeName(s string, fallback string) string {
	out := []byte{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+'a'-'A')
		case c == ' ' || c == '-' || c == '.':
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return string(out)
}
