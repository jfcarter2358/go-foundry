package stages

// wall_sheet.go — the `wall_sheet` stage: a terrain's cliffs built as rock
// geometry (internal/procgen/wallsheet has the method; docs/canyon/PHASE_8.md
// why). Beside cliff_wall, which clads walls with scanned chunks, this
// stage makes the wall itself: beds, ledges, overhangs, joints and alcoves
// in one continuous surface, in the terrain material's own rock.
//
// The run: the walls' fields over the terrain (wallsheet.NewField), the band
// cut into sections, each section meshed (dual contouring of the shell cut
// to its box), its normals taken from the shell's own field (so no seam
// shades where two sections' boxes meet), its vertices coloured — ambient
// occlusion from the field, a tint per bed and per panel — and cooked as
// one Nanite mesh with mesh collision, placed at its centre. Every section
// wears one material: the terrain material's splat with only its rock layer
// (a weights texture of pure rock), its bedding relief turned down to fine
// laminations (the beds are geometry now), and a ground blend so a wall's
// foot takes the floor's colour.

import (
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"foundry/internal/cluster"
	"foundry/internal/material"
	"foundry/internal/model"
	"foundry/internal/procgen"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/sdf"
	"foundry/internal/procgen/terrain"
	"foundry/internal/procgen/wallsheet"

	"github.com/go-gl/mathgl/mgl32"
)

// WallSheetKind is the stage's name.
const WallSheetKind = "wall_sheet"

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     WallSheetKind,
		Label:    "Wall sheet",
		Category: "Placement",
		// Per environment region of a level generation doc: the walls stand
		// out by the region's weight (place_region.go).
		Regional: procgen.RegionalPerRegion,
		Version:  3,
		Doc: "A terrain's cliffs as real rock: a shell over every wall with hard beds standing as ledges over soft ones " +
			"weathered back beneath them, vertical joints cutting the face into panels, alcoves and a rough, rounded " +
			"surface — overhangs and silhouettes a heightfield cannot have — in the terrain material's own rock.",
		Params:   wallSheetParams(),
		Run:      runWallSheet,
		Validate: validateWallSheet,
	})
}

func wallSheetParams() []procgen.ParamDef {
	f, i, v2 := procgen.Float, procgen.Int, procgen.Vec2
	d := wallsheet.Defaults()
	return []procgen.ParamDef{
		{Name: "terrain", Type: procgen.ParamStage, StageKinds: TerrainSources, Default: procgen.StageRef(""),
			Doc: "The terrain stage whose cliffs are built."},
		{Name: "ground", Type: procgen.ParamStage, StageKinds: GroundSources, Default: procgen.StageRef(""),
			Doc: "The terrain_material (or region_material) stage: the walls wear its rock layer, and their feet blend into its ground."},
		{Name: "cell", Type: procgen.ParamFloat, Label: "Cell (m)", Default: f(0.4), Min: 0.1, Max: 4, Step: 0.05,
			Doc: "The mesher's grid: the finest feature the rock has. Halving it is four times the triangles."},
		{Name: "depth", Type: procgen.ParamFloat, Label: "Relief (m)", Default: f(d.Depth), Min: 0.2, Max: 20, Step: 0.1,
			Doc: "How far the rock stands out of the terrain at most: the depth of the deepest recess, the reach of the furthest ledge."},
		{Name: "steep", Type: procgen.ParamVec2, Label: "Steep (°)", Default: v2(d.SteepDeg[0], d.SteepDeg[1]), Min: 0, Max: 90, Step: 0.5,
			Doc: "Ground steeper than the first starts to be wall; by the second it is all wall."},
		{Name: "min_wall", Type: procgen.ParamFloat, Label: "Min wall (m)", Default: f(d.MinWall), Min: 0, Max: 200, Step: 0.5,
			Doc: "Steep ground with less relief than this (over 8 m) is not a cliff — a dune face, a bank — and gets no rock."},
		{Name: "bed_spacing", Type: procgen.ParamFloat, Label: "Bed spacing (m)", Default: f(d.BedSpacing), Min: 0.3, Max: 50, Step: 0.1,
			Doc: "The beds' typical thickness."},
		{Name: "soft_share", Type: procgen.ParamFloat, Label: "Soft beds", Default: f(d.SoftShare), Min: 0, Max: 1, Step: 0.01,
			Doc: "The share of beds that are soft and weather back under the hard ones (fewer near the rims: caprock)."},
		{Name: "soft_depth", Type: procgen.ParamFloat, Label: "Soft depth", Default: f(d.SoftDepth), Min: 0, Max: 1, Step: 0.01,
			Doc: "How far back a soft bed weathers, as a share of the relief."},
		{Name: "joint_spacing", Type: procgen.ParamFloat, Label: "Joint spacing (m)", Default: f(d.JointSpacing), Min: 1, Max: 200, Step: 0.5,
			Doc: "The vertical joints' typical spacing: the width of the panels the face breaks into."},
		{Name: "joint_depth", Type: procgen.ParamFloat, Label: "Joint depth", Default: f(d.JointDepth), Min: 0, Max: 1, Step: 0.01,
			Doc: "How deep an open joint cuts, as a share of the relief."},
		{Name: "panel_depth", Type: procgen.ParamFloat, Label: "Panel setback", Default: f(d.PanelDepth), Min: 0, Max: 1, Step: 0.01,
			Doc: "How far a panel between joints can stand back from its neighbours, as a share of the relief."},
		{Name: "alcoves", Type: procgen.ParamFloat, Label: "Alcoves", Default: f(d.Alcoves), Min: 0, Max: 1, Step: 0.01,
			Doc: "The chance a panel's soft bed is hollowed into an alcove."},
		{Name: "rough", Type: procgen.ParamFloat, Label: "Roughness (m)", Default: f(d.Rough), Min: 0, Max: 2, Step: 0.01,
			Doc: "The surface's lumps and grain."},
		{Name: "weathering", Type: procgen.ParamInt, Label: "Weathering", Default: i(1), Min: 0, Max: sdf.MaxWeathering,
			Doc: "Steps of curvature flow that round the rock's convex edges."},
		{Name: "bedding", Type: procgen.ParamFloat, Label: "Laminations", Default: f(0.06), Min: 0, Max: 2, Step: 0.01,
			Doc: "The material's bedding relief on the walls: the beds are geometry now, so only fine laminations."},
		{Name: "ground_band", Type: procgen.ParamFloat, Label: "Ground blend (m)", Default: f(0.8), Min: 0, Max: 20, Step: 0.05,
			Doc: "How far up a wall's foot takes the floor's colour. 0: none."},
		{Name: "cook_memory", Type: procgen.ParamInt, Label: "Cook memory (MB)", Default: i(3000), Min: 100, Max: 64000,
			Doc: "How much memory the sections' meshing and cooks may hold at once."},
		{Name: "caves", Added: true, Type: procgen.ParamStage, StageKinds: []string{CaveKind}, Default: procgen.StageRef(""),
			Doc: "A cave stage whose void is cut out of the walls, so its mouths open through the rock. Empty: none."},
		// A close-range detail normal (docs/adventure/PHASE_F.md, Phase F2):
		// the rock layer repeats every several metres, and a camera pressed
		// to the wall magnifies it into a smear.
		{Name: "detail_normal", Added: true, Type: procgen.ParamTexture, Label: "Detail normal", Default: procgen.Texture(""),
			Doc: "A texture whose normal map is laid over the walls close up (the material's detail_normal effect). Empty: none."},
		{Name: "detail_scale", Added: true, Type: procgen.ParamFloat, Label: "Detail scale (m)", Default: f(0.8), Min: 0.05, Max: 20, Step: 0.05,
			Doc: "Metres a repeat of the detail normal."},
		{Name: "detail_strength", Added: true, Type: procgen.ParamFloat, Label: "Detail strength", Default: f(0.6), Min: 0, Max: 2, Step: 0.05,
			Doc: "How much the detail bends the normal."},
		{Name: "detail_distance", Added: true, Type: procgen.ParamFloat, Label: "Detail distance (m)", Default: f(5), Min: 1, Max: 200, Step: 0.5,
			Doc: "Gone by this far from the eye: the farther, the more of the wall pays for it."},
	}
}

func validateWallSheet(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	var out []string
	if p.Str("terrain") == "" {
		out = append(out, "choose a terrain stage")
	}
	if p.Str("ground") == "" {
		out = append(out, "choose a ground (terrain_material) stage")
	}
	if s := p.Vec2("steep"); s[1] <= s[0] {
		out = append(out, "the steep band must rise")
	}
	return out
}

// sectionBytesPerTriangle is a section's peak memory per output triangle
// while it is meshed and cooked mesh-only (meshout's figure plus the
// mesher's sparse grid).
const sectionBytesPerTriangle = 2500

func runWallSheet(rc *procgen.RunContext) error {
	p := rc.Params
	t0 := time.Now()
	ta, err := rc.UpstreamArtifact(p.Str("terrain"))
	if err != nil {
		return err
	}
	t, ok := ta.(*terrain.Terrain)
	if !ok {
		return fmt.Errorf("stage %s is not a terrain stage", p.Str("terrain"))
	}
	ground, err := groundArtifact(rc, p.Str("ground"))
	if err != nil {
		return err
	}
	if ground == nil {
		return fmt.Errorf("choose a ground stage")
	}
	s := wallsheet.Defaults()
	s.Depth, s.SteepDeg, s.MinWall = p.Float("depth"), p.Vec2("steep"), p.Float("min_wall")
	s.BedSpacing, s.SoftShare, s.SoftDepth = p.Float("bed_spacing"), p.Float("soft_share"), p.Float("soft_depth")
	s.JointSpacing, s.JointDepth, s.PanelDepth = p.Float("joint_spacing"), p.Float("joint_depth"), p.Float("panel_depth")
	s.Alcoves, s.Rough, s.Seed = p.Float("alcoves"), p.Float("rough"), rc.Seed
	if reg := placeRegion(&rc.Env); reg != nil {
		s.Weight = reg.At
	}
	f, err := wallsheet.NewField(t.Grid, s, &rc.Env)
	if err != nil {
		return err
	}
	if id := p.Str("caves"); id != "" {
		ca, err := rc.UpstreamArtifact(id)
		if err != nil {
			return err
		}
		c, ok := ca.(*CaveArtifact)
		if !ok {
			return fmt.Errorf("stage %s is not a cave stage", id)
		}
		f.Carve = c.VoidOn(t.Grid).At
	}
	secs := f.Sections()
	cell := p.Float("cell")
	rc.Logf("%d sections of %g m over the walls, cell %g m, fields in %s", len(secs), s.Section, cell, time.Since(t0).Round(time.Millisecond))
	rc.Progress(0.05)

	mat, err := wallMaterial(rc, ground)
	if err != nil {
		return err
	}
	// One quantisation step for every section, from the tallest.
	ext := s.Section
	for _, sec := range secs {
		ext = math.Max(ext, sec.Hi[1]-sec.Lo[1])
	}
	step := float32(math.Ldexp(1, int(math.Floor(math.Log2(ext)))-18))
	// About two triangles per cell of wall: the budget's estimate.
	estTris := int(2 * s.Section * 60 / (cell * cell))
	conc := max(1, min(int(float64(p.Int("cook_memory"))*(1<<20)/(float64(estTris)*sectionBytesPerTriangle)), rc.Workers))
	if rc.Workers <= 0 {
		conc = max(1, min(conc, 4))
	}
	sem := make(chan struct{}, conc)
	var done, tris, rawTris atomic.Int64
	var tMesh, tSimp, tBake, tCook atomic.Int64 // nanoseconds, summed over the sections
	aoShape := sdf.Shape{F: f.F, Lip: 2}
	err = rc.Parallel(len(secs), func(n int) error {
		sem <- struct{}{}
		defer func() { <-sem }()
		if err := rc.Err(); err != nil {
			return err
		}
		sec := secs[n]
		ts := time.Now()
		m, _, err := sdf.Polygonize(f.Shape(sec, cell), sdf.MeshOptions{Cell: cell, Weathering: p.Int("weathering"),
			Sharpness: 0.35, Workers: 1, Ctx: rc.Ctx})
		tMesh.Add(int64(time.Since(ts)))
		if err != nil {
			return fmt.Errorf("section %d,%d: %w", sec.I, sec.J, err)
		}
		if m.TriangleCount() == 0 {
			return nil
		}
		centre := sdf.Vec3{(sec.Lo[0] + sec.Hi[0]) / 2, sec.Lo[1], (sec.Lo[2] + sec.Hi[2]) / 2}
		raw := m.TriangleCount()
		ts = time.Now()
		dropBuried(f, m)
		simplifyWall(m, sec, cell)
		tSimp.Add(int64(time.Since(ts)))
		ts = time.Now()
		im := wallImport(f, aoShape, m, sec, centre, cell)
		tBake.Add(int64(time.Since(ts)))
		ts = time.Now()
		rawTris.Add(int64(raw))
		name := fmt.Sprintf("wall_%d_%d", sec.I, sec.J)
		if _, err := meshout.Cook(im, rc.Path(name+".fnm"), meshout.CookOptions{PositionStep: step, CollisionMesh: true,
			AttrRadiusCap: 1}); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		tCook.Add(int64(time.Since(ts)))
		tris.Add(int64(m.TriangleCount()))
		rc.Place(procgen.Placement{ID: name, Model: rc.AssetPath(name + ".fnm"),
			Position:  procgen.Point3{X: float32(centre[0]), Y: float32(centre[1]), Z: float32(centre[2])},
			Collision: "mesh", Chunk: procgen.ChunkAuto, Material: mat})
		rc.Progress(0.05 + 0.95*float32(done.Add(1))/float32(len(secs)))
		return nil
	})
	if err != nil {
		return err
	}
	sec := func(v *atomic.Int64) string { return time.Duration(v.Load()).Round(time.Second).String() }
	rc.Logf("%d sections, %.1f M triangles (%.1f M meshed), in %s (summed over %d at a time: mesh %s, simplify %s, bake %s, cook %s)",
		len(secs), float64(tris.Load())/1e6, float64(rawTris.Load())/1e6, time.Since(t0).Round(time.Millisecond), conc,
		sec(&tMesh), sec(&tSimp), sec(&tBake), sec(&tCook))
	rc.Progress(1)
	return nil
}

// wallImport is a section's mesh ready to cook, in coordinates about centre:
// normals from the shell's own field except on the section's box (so the
// seam between two sections does not shade), and a vertex colour per
// vertex — the field's ambient occlusion in alpha, a tint by bed and panel
// in RGB.
func wallImport(f *wallsheet.Field, ao sdf.Shape, m *sdf.Mesh, sec wallsheet.Section, centre sdf.Vec3, cell float64) *model.ImportMesh {
	im := &model.ImportMesh{
		Positions: make([]mgl32.Vec3, len(m.Positions)),
		Normals:   make([]mgl32.Vec3, len(m.Positions)),
		UVs:       make([]mgl32.Vec2, len(m.Positions)),
		Colors:    make([][4]uint8, len(m.Positions)),
		Indices:   m.Indices,
		TriSlot:   make([]int32, m.TriangleCount()),
		Slots:     []model.ImportSlot{{Name: "rock", BaseColor: [4]float32{1, 1, 1, 1}}},
	}
	e := cell * 0.5
	for k, p := range m.Positions {
		n := m.Normals[k]
		onBox := math.Min(math.Min(math.Abs(p[0]-sec.Lo[0]), math.Abs(p[0]-sec.Hi[0])),
			math.Min(math.Abs(p[2]-sec.Lo[2]), math.Abs(p[2]-sec.Hi[2]))) < 0.3*cell
		if !onBox {
			n = ao.Gradient(p, e)
		}
		_, bed, panel := f.Recess(p)
		occ := 1.0
		if !onBox {
			occ = ao.AO(p, n, 2.5, 4)
		}
		tint := 1 + 0.07*(noise.CellUnit(noise.Hash64(uint64(bed), 11))-0.5) + 0.06*(noise.CellUnit(panel)-0.5)
		warm := 1 + 0.04*(noise.CellUnit(noise.Hash64(uint64(bed), 12))-0.5)
		c := [3]float64{tint * warm, tint, tint / warm}
		for ch := 0; ch < 3; ch++ {
			im.Colors[k][ch] = uint8(noise.Clamp(c[ch]*0.92, 0, 1)*255 + 0.5)
		}
		im.Colors[k][3] = uint8(noise.Clamp(0.25+0.75*occ, 0, 1)*255 + 0.5)
		q := p.Sub(centre)
		im.Positions[k] = mgl32.Vec3{float32(q[0]), float32(q[1]), float32(q[2])}
		im.Normals[k] = mgl32.Vec3{float32(n[0]), float32(n[1]), float32(n[2])}
	}
	return im
}

// wallSimplifyError is the most a section's simplification may move its
// surface (m): under a pixel at the distances a wall is seen from, and
// enough to fold the flat faces hidden under the ground (the floor in front
// of a wall, the box's bottom and sides) into a handful of triangles.
const wallSimplifyError = 0.04

// dropBuried removes the triangles wholly under the terrain — the shell's
// flat parts 12 cm down (wallsheet.Hidden), its section box's bottom, the
// back of the rock inside the mesa — which nobody can see: most of what
// was meshed, and most of what the simplification would have chewed on.
// The mesh is then open, but its every open edge is under the ground.
func dropBuried(f *wallsheet.Field, m *sdf.Mesh) {
	buried := make([]bool, len(m.Positions))
	for i, p := range m.Positions {
		buried[i] = f.Terrain(p) < -0.5*wallsheet.Hidden
	}
	out := m.Indices[:0]
	for t := 0; t+2 < len(m.Indices); t += 3 {
		a, b, c := m.Indices[t], m.Indices[t+1], m.Indices[t+2]
		if buried[a] && buried[b] && buried[c] {
			continue
		}
		out = append(out, a, b, c)
	}
	m.Indices = out
}

// simplifyWall decimates a section's mesh by error (wallSimplifyError), its
// vertices on the section's box sides locked so it still meets its
// neighbours, its normals weighed in; then compacts it.
func simplifyWall(m *sdf.Mesh, sec wallsheet.Section, cell float64) {
	const stride = 6
	verts := make([]float32, 0, len(m.Positions)*stride)
	for i, p := range m.Positions {
		n := m.Normals[i]
		verts = append(verts, float32(p[0]), float32(p[1]), float32(p[2]), float32(n[0]), float32(n[1]), float32(n[2]))
	}
	side := func(v uint32) bool {
		p := m.Positions[v]
		return math.Min(math.Min(math.Abs(p[0]-sec.Lo[0]), math.Abs(p[0]-sec.Hi[0])),
			math.Min(math.Abs(p[2]-sec.Lo[2]), math.Abs(p[2]-sec.Hi[2]))) < 0.6*cell
	}
	nw := float32(0.05)
	res := cluster.Simplify(verts, stride, m.Indices, cluster.SimplifyOptions{MaxError: wallSimplifyError,
		AttrWeights: []float32{nw, nw, nw}, Locked: side})
	m.Indices = res.Indices
	remap := make([]int32, len(m.Positions))
	for i := range remap {
		remap[i] = -1
	}
	for _, ix := range m.Indices {
		remap[ix] = 0
	}
	n := int32(0)
	for i := range remap {
		if remap[i] < 0 {
			continue
		}
		remap[i] = n
		m.Positions[n], m.Normals[n] = m.Positions[i], m.Normals[i]
		n++
	}
	m.Positions, m.Normals = m.Positions[:n], m.Normals[:n]
	for i, ix := range m.Indices {
		m.Indices[i] = uint32(remap[ix])
	}
}

// wallMaterial writes the walls' material: the ground's splat with its rock
// layer only (a weights map of pure rock), laminations for bedding, and a
// ground blend at the feet.
func wallMaterial(rc *procgen.RunContext, ground *TerrainMaterialArtifact) (string, error) {
	// Pure rock: the fourth layer of a terrain_material; in an eight-layer
	// region material, the region's own rock layer, in whichever of the two
	// weights maps holds it (place_region_ground.go).
	rock := rockLayerIndex(&rc.Env, ground)
	name, eight := ground.Material, ground.Weights2 != ""
	if ground.Palette > 0 {
		// A palette (region_material_palette.go) has no union material: the
		// walls take the smallest per-tile material holding the rock layer,
		// and the rock's place in it (its atlas is the palette's).
		tm, at := paletteRockMaterial(ground, rock)
		if tm == nil {
			return "", fmt.Errorf("no per-tile material of the ground's palette holds its rock layer %d", rock+1)
		}
		name, rock, eight = tm.Material, at, len(tm.Layers) > material.SplatLayers
	}
	base := readMaterialDoc(name)
	if base == nil {
		return "", fmt.Errorf("the ground stage's material %s cannot be read", name)
	}
	const n = 4
	write := func(name string, ch int) error {
		pix := make([]uint8, 4*n*n)
		for k := 0; k < n*n && ch >= 0; k++ {
			pix[4*k+ch] = 255
		}
		data, err := terrain.EncodeRGBA(n, n, pix)
		if err != nil {
			return err
		}
		return rc.WriteFile(name, data)
	}
	ch1 := rock
	if eight && rock >= 4 {
		ch1 = -1
	}
	if err := write("rock_weights.png", ch1); err != nil {
		return "", err
	}
	if eight {
		ch2 := -1
		if rock >= 4 {
			ch2 = rock - 4
		}
		if err := write("rock_weights2.png", ch2); err != nil {
			return "", err
		}
	}
	d := base.Clone()
	d.Name = genMaterialName(rc, "")
	d.Doc = "Generated cliff rock: the terrain material's rock layer on the wall sheets"
	if e := d.Find(material.KindSplat); e != nil {
		e.Set("weights", material.TextureValue(mapRef(rc, "rock_weights.png")))
		if eight {
			e.Set("weights2", material.TextureValue(mapRef(rc, "rock_weights2.png")))
		}
		if b := rc.Params.Float("bedding"); b > 0 {
			e.Set("bedding", material.FloatValue(float32(b)))
			e.Set("bedding_spacing", material.FloatValue(0.7))
		} else {
			e.Set("bedding", material.FloatValue(0))
		}
	}
	if band := rc.Params.Float("ground_band"); band > 0 {
		e := d.AddEffect(material.KindGroundBlend)
		e.Set("ground", material.TextureValue(ground.Ground))
		e.Set("height", material.TextureValue(ground.Height))
		e.Set("bounds_min", material.Vec2Value(mgl32.Vec2{float32(ground.Min[0]), float32(ground.Min[1])}))
		e.Set("bounds_max", material.Vec2Value(mgl32.Vec2{float32(ground.Max[0]), float32(ground.Max[1])}))
		e.Set("height_range", material.Vec2Value(mgl32.Vec2{ground.HeightLo, ground.HeightHi}))
		e.Set("band", material.FloatValue(float32(band)))
	}
	if tex := rc.Params.Str("detail_normal"); tex != "" {
		e := d.AddEffect(material.KindDetailNormal)
		e.Set("map", material.TextureValue(tex))
		e.Set("scale", material.FloatValue(float32(rc.Params.Float("detail_scale"))))
		e.Set("strength", material.FloatValue(float32(rc.Params.Float("detail_strength"))))
		e.Set("distance", material.FloatValue(float32(rc.Params.Float("detail_distance"))))
	}
	if err := writeMaterial(rc, d); err != nil {
		return "", err
	}
	return d.Name, nil
}

// paletteRockMaterial is the smallest near per-tile material of a palette
// ground holding palette layer rock (0-based), and that layer's index in it.
func paletteRockMaterial(ground *TerrainMaterialArtifact, rock int) (*RegionTileMaterial, int) {
	var best *RegionTileMaterial
	at := -1
	for i := range ground.Tiles {
		tm := &ground.Tiles[i]
		if tm.Far {
			continue
		}
		for c, l := range tm.Layers {
			if l == rock+1 && (best == nil || len(tm.Layers) < len(best.Layers)) {
				best, at = tm, c
			}
		}
	}
	return best, at
}
