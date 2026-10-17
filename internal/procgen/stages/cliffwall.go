package stages

// cliffwall.go — the `cliff_wall` stage: cliff chunks (a rocks stage of the
// cliff_chunk preset: flat backs) stacked in tiers up a terrain's walls,
// faces out, backs seated into the heightfield's stretched cliff faces so
// they cover them, with ledge slabs between tiers and capping rims. This is
// what gives a canyon the vertical and overhanging rock a heightfield
// cannot.
//
// The walls are found two ways (source): TRACED — the feet of every cliff
// band of the terrain's cliff mask (place.TraceCliffFeet), optionally only
// inside a region — or along authored SPLINES drawn in the 2D view, each
// facing the side it is told (or downhill). The method — measuring a wall
// across, tiers, seating, ledges — is internal/procgen/place's wall.go.
//
// The chunks are hero meshes (hundreds of thousands of triangles), so the
// stage's defaults keep their number bounded (max_chunks) and min_height
// skips walls too low to want them; PHASE_3.md has the cost per placement.

import (
	"fmt"
	"math"
	"time"

	"foundry/internal/procgen"
	"foundry/internal/procgen/place"
)

// CliffWallKind is the stage's name.
const CliffWallKind = "cliff_wall"

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     CliffWallKind,
		Label:    "Cliff wall",
		Category: "Placement",
		// Per environment region of a level generation doc: the columns
		// the region owns (place_region.go).
		Regional: procgen.RegionalPerRegion,
		Version:  8, // 2: WallSlope, turned backs; 3: ledges on the face above the tiers, steeper foot and rim; 4-6: tallest stretches first, bigger chunks; 7: materials, seating by the back's silhouette; 8: contact and backing tests, tiers pitched by the chunk placed, focus
		Doc: "Cliff chunks stacked in tiers up a terrain's walls — along the feet of the cliffs traced from the cliff mask, " +
			"or along splines — faces out, backs seated into the heightfield's stretched faces so they cover them, with " +
			"ledge slabs between tiers and on the rims.",
		Params:   cliffWallParams(),
		Run:      runCliffWall,
		Preview:  previewCliffWall,
		Load:     loadFootprints,
		Validate: validateCliffWall,
	})
}

func cliffWallParams() []procgen.ParamDef {
	d := place.DefaultWall()
	ph7 := place.DefaultWallPhase7
	f := procgen.Float
	i := procgen.Int
	deg := func(name, label string, def, max float64, doc string) procgen.ParamDef {
		return procgen.ParamDef{Name: name, Type: procgen.ParamFloat, Label: label, Default: f(def), Min: 0, Max: max, Step: 0.1, Doc: doc}
	}
	unit := func(name, label string, def float64, doc string) procgen.ParamDef {
		return procgen.ParamDef{Name: name, Type: procgen.ParamFloat, Label: label, Default: f(def), Min: 0, Max: 1, Step: 0.01, Doc: doc}
	}
	out := []procgen.ParamDef{
		{Name: "terrain", Type: procgen.ParamStage, StageKinds: TerrainSources, Default: procgen.StageRef(""),
			Doc: "The terrain stage whose walls are lined."},
		{Name: "chunks", Type: procgen.ParamStage, StageKinds: RockSources, Default: procgen.StageRef(""),
			Doc: "The cliff chunks to stack: a rocks stage of the cliff_chunk preset, or any stage whose rocks manifest gives its variants a back (a scanned cliff's back need not be flat: its box's face across the back direction is seated, buried by Inset)."},
		{Name: "ledges", Type: procgen.ParamStage, StageKinds: RockSources, Default: procgen.StageRef(""),
			Doc: "Ledge slabs (a rocks stage of the ledge_slab preset, or any rocks manifest) for ledges between tiers and on rims; none for no ledges."},
		{Name: "seed", Type: procgen.ParamSeed, Default: procgen.Seed(0), Doc: "Changes every chunk's variant and jitter."},
		{Name: "source", Type: procgen.ParamEnum, Default: procgen.Enum("traced"), Choices: []string{"traced", "splines", "both"},
			Doc: "Where the walls run: traced along the feet of the cliff mask's bands, along the splines below, or both."},
		{Name: "paths", Type: procgen.ParamList, Default: procgen.List(), Fields: []procgen.ParamDef{
			{Name: "path", Type: procgen.ParamSpline, MinItems: 0, Default: procgen.Spline(),
				Doc: "A wall's line, drawn along its foot in the 2D view."},
			{Name: "side", Type: procgen.ParamEnum, Default: procgen.Enum("auto"), Choices: []string{"auto", "left", "right"},
				Doc: "Which side of the line the wall faces (as the line is drawn): auto faces downhill."},
		}, Doc: "Authored wall lines (source splines or both)."},
		{Name: "region_min", Type: procgen.ParamVec2, Label: "Region min", Default: procgen.Vec2(0, 0),
			Doc: "Traced walls only inside this XZ rectangle (with Region max); both zero for the whole terrain."},
		{Name: "region_max", Type: procgen.ParamVec2, Label: "Region max", Default: procgen.Vec2(0, 0),
			Doc: "The region's other corner."},
		unit("cliff_threshold", "Cliff threshold", 0.5, "The cliff mask level the traced feet follow."),
		{Name: "trace_cell", Type: procgen.ParamFloat, Label: "Trace cell (m)", Default: f(2), Min: 0.25, Max: 50, Step: 0.05,
			Doc: "The grid the cliff mask is averaged onto before tracing: coarser follows the walls' broad lines, finer every notch."},
		{Name: "min_length", Type: procgen.ParamFloat, Label: "Min length (m)", Default: f(24), Min: 0, Max: 10000, Step: 0.5,
			Doc: "Traced feet shorter than this are dropped (boulder-sized steep spots)."},
		{Name: "smooth", Type: procgen.ParamInt, Default: i(3), Min: 0, Max: 50, Doc: "Smoothing passes over a traced line."},
		{Name: "min_height", Type: procgen.ParamFloat, Label: "Min wall (×chunk)", Default: f(d.MinHeight), Min: 0, Max: 10, Step: 0.01,
			Doc: "Walls lower than this fraction of the smallest chunk (at the smallest scale) get none."},
		{Name: "min_wall", Type: procgen.ParamFloat, Label: "Min wall (m)", Default: f(d.MinWall), Min: 0, Max: 1000, Step: 0.5,
			Doc: "Walls lower than this many metres get no chunks (the heightfield's own steps and banks are left alone)."},
		{Name: "scale", Type: procgen.ParamVec2, Default: procgen.Vec2(d.Scale[0], d.Scale[1]), Min: 0.05, Max: 50, Step: 0.01,
			Doc: "The chunks' scale range; within it the scale is chosen so a column's tiers span foot to rim."},
		unit("scale_jitter", "Scale jitter", d.ScaleJitter, "Each chunk's scale varies by up to this fraction."),
		{Name: "tiers", Type: procgen.ParamInt, Default: i(d.Tiers), Min: 1, Max: 64,
			Doc: "The most tiers a column stacks; a wall taller than they reach keeps its heightfield above."},
		unit("overlap", "Tier overlap", d.Overlap, "How much of a chunk's height the tier above overlaps."),
		{Name: "spacing", Type: procgen.ParamFloat, Default: f(d.Spacing), Min: 0.2, Max: 3, Step: 0.01,
			Doc: "The column step along the wall, as a fraction of a chunk's width (under 1: neighbours overlap)."},
		deg("foot_slope", "Foot slope (°)", d.FootSlope, 89, "Across the wall, where the slope eases below this outward is its foot."),
		deg("wall_slope", "Wall slope (°)", d.WallSlope, 89, "A wall must be at least this steep somewhere across it: gentler banks are left to the talus."),
		deg("rim_slope", "Rim slope (°)", d.RimSlope, 89, "Where it eases below this inward is its rim."),
		{Name: "search", Type: procgen.ParamFloat, Label: "Search (m)", Default: f(d.Search), Min: 1, Max: 500, Step: 0.5,
			Doc: "How far across the line the wall is looked for, each way."},
		{Name: "inset", Type: procgen.ParamFloat, Label: "Inset (m)", Default: f(d.Inset), Min: 0, Max: 20, Step: 0.01,
			Doc: "How far behind the heightfield's face a chunk's back is buried, at its least."},
		unit("protrude", "Protrude", d.Protrude, "How far a chunk's front stands proud of the face, as a fraction of its depth."),
		unit("lean", "Lean", d.Lean, "How much of the face's lean a chunk follows (tilting back into the wall)."),
		deg("max_lean", "Max lean (°)", d.MaxLean, 80, "The most a chunk leans back."),
		unit("sink", "Sink", d.Sink, "How far the bottom tier sits below the wall's foot, as a fraction of its height."),
		deg("yaw_jitter", "Yaw jitter (°)", d.YawJitter, 90, "Each chunk turns by up to this about the vertical."),
		deg("roll_jitter", "Roll jitter (°)", d.RollJitter, 45, "Each chunk rolls by up to this about its facing."),
		unit("along_jitter", "Along jitter", d.AlongJitter, "Each chunk slides along the wall by up to this fraction of a column step."),
		unit("drop_jitter", "Drop jitter", d.DropJitter, "Each chunk above the first tier moves up or down by up to this fraction of its height."),
		unit("ledge_chance", "Ledge chance", d.LedgeChance, "The chance of a ledge slab between two tiers."),
		unit("rim_ledge", "Rim ledge", d.RimLedge, "The chance of a ledge slab capping a column's rim."),
		{Name: "ledge_scale", Type: procgen.ParamFloat, Label: "Ledge length", Default: f(d.LedgeScale), Min: 0.05, Max: 5, Step: 0.01,
			Doc: "A ledge's length as a fraction of the chunk's width it sits on."},
		{Name: "collision", Type: procgen.ParamEnum, Default: procgen.Enum("auto"), Choices: []string{"auto", "mesh", "hulls", "box", "none"},
			Doc: "How the chunks and ledges collide: auto is what their rocks stages ask (mesh for cliff chunks)."},
		{Name: "max_chunks", Type: procgen.ParamInt, Label: "Max chunks", Default: i(d.MaxChunks), Min: 0, Max: 1000000,
			Doc: "The most chunks and ledges placed (0: no cap). Hero chunks are dense: keep this to what the walls need. The tallest stretches are walled first."},
		{Name: "stretch", Type: procgen.ParamFloat, Label: "Stretch (m)", Default: f(d.StretchLength), Min: 1, Max: 10000, Step: 1,
			Doc: "Walls are lined in stretches this long, the tallest first and each whole, so a capped wall set covers the big faces rather than dotting columns about."},
		{Name: "seat", Type: procgen.ParamEnum, Default: procgen.Enum("silhouette"), Choices: []string{"silhouette", "box"},
			Doc: "How a chunk's back is buried: silhouette pushes it in until the rim of its back — what is seen of an open-backed " +
				"scan from the side — is in the wall; box buries its bounding box's back face (phase 3's rule, which leaves an " +
				"open-backed scan standing proud with a gap behind it)."},
		{Name: "max_back_gap", Type: procgen.ParamFloat, Label: "Max back gap", Default: f(d.MaxBackGap), Min: 0, Max: 1, Step: 0.01,
			Doc: "A chunk that the silhouette seating would bury deeper than this fraction of its depth past the box seating is tried as another variant, " +
				"and dropped if none fits: it would vanish into the wall."},
		{Name: "back_cover", Type: procgen.ParamFloat, Label: "Back cover", Default: f(d.BackCover), Min: 0.1, Max: 1, Step: 0.01,
			Doc: "The share of the back's rim the silhouette seating buries (1: all of it; lower lets the odd far-forward lip stay out rather than bury the piece)."},
		{Name: "ground", Type: procgen.ParamStage, StageKinds: GroundSources, Default: procgen.StageRef(""),
			Doc: "A terrain_material (or region_material) stage: the chunks blend into its ground colour at their feet and match toward it."},
		{Name: "match_to", Added: true, Type: procgen.ParamEnum, Label: "Match to", Default: procgen.Enum("ground"), Choices: []string{"ground", "rock"},
			Doc: "What the chunks' colour match (match_ground) aims at: the ground's average colour, or the terrain material's rock layer — the colour of the bare heightfield walls the chunks stand on, which is what they must not stand out from."},
		{Name: "match_brightness", Added: true, Type: procgen.ParamFloat, Label: "Match brightness", Default: f(1), Min: 0.1, Max: 4, Step: 0.01,
			Doc: "Scales the chunks' colour after the match (with match_ground above 0): below 1 darkens pale scans to sit with the walls."},
		unit("min_contact", "Min contact", ph7.MinContact, "The share of a chunk's back that must touch the face (buried, or within Contact gap of it). "+
			"A piece with less hangs off the wall with daylight behind it — a rock stuck to a pillar's flank — and is tried as another variant, then dropped. 0: no test."),
		{Name: "contact_gap", Type: procgen.ParamFloat, Label: "Contact gap (m)", Default: f(ph7.ContactGap), Min: 0, Max: 20, Step: 0.05,
			Doc: "How far in front of the face a chunk's back may stand and still count as touching it."},
		{Name: "backing", Type: procgen.ParamFloat, Label: "Backing (×width)", Default: f(ph7.Backing), Min: 0, Max: 20, Step: 0.05,
			Doc: "How much rock a wall must have behind its rim, in chunk widths: a pillar or fin narrower than that gets no chunks (they would hang off its sides). 0: no test."},
		unit("height_norm", "Height evening", ph7.HeightNorm, "Evens the variants' sizes in a column: 0 scales every variant alike (a 40 m scan beside a 5 m one stays eight times as big), 1 brings each to the variants' mean height."),
		unit("tier_jitter", "Tier jitter", ph7.TierJitter, "Each tier's overlap with the one below varies by up to this fraction, so the joints up a column do not run at one pitch."),
		{Name: "focus", Type: procgen.ParamVec2, Label: "Focus", Default: procgen.Vec2(0, 0),
			Doc: "Where the walls are mostly seen from (the spawn, the canyon floor): with a cap, the stretches nearest it are walled first (with Focus range)."},
		{Name: "focus_range", Type: procgen.ParamFloat, Label: "Focus range (m)", Default: f(0), Min: 0, Max: 100000, Step: 10,
			Doc: "The distance from Focus at which a wall's priority halves; 0 ignores Focus (the tallest stretches first)."},
	}
	// The chunks' and ledges' material (materials_out.go).
	out = append(out, rockFxParams(0.3)...)
	return out
}

// wallSettings maps the params onto place.WallSettings (variants filled by
// the caller).
func wallSettings(env *procgen.Env, p procgen.Params, seed uint64) place.WallSettings {
	s := place.DefaultWall()
	s.Seed = seed
	s.Scale, s.ScaleJitter = p.Vec2("scale"), p.Float("scale_jitter")
	if s.Scale[1] < s.Scale[0] {
		s.Scale[0], s.Scale[1] = s.Scale[1], s.Scale[0]
	}
	s.Tiers, s.Overlap, s.Spacing, s.MinHeight = p.Int("tiers"), p.Float("overlap"), p.Float("spacing"), p.Float("min_height")
	s.MinWall = p.Float("min_wall")
	s.FootSlope, s.RimSlope, s.Search = p.Float("foot_slope"), p.Float("rim_slope"), p.Float("search")
	s.WallSlope = p.Float("wall_slope")
	s.Inset, s.Protrude, s.Lean, s.MaxLean, s.Sink = p.Float("inset"), p.Float("protrude"), p.Float("lean"), p.Float("max_lean"), p.Float("sink")
	s.YawJitter, s.RollJitter, s.AlongJitter = p.Float("yaw_jitter"), p.Float("roll_jitter"), p.Float("along_jitter")
	s.DropJitter = p.Float("drop_jitter")
	s.LedgeChance, s.RimLedge, s.LedgeScale = p.Float("ledge_chance"), p.Float("rim_ledge"), p.Float("ledge_scale")
	s.Collision, s.MaxChunks = p.Str("collision"), p.Int("max_chunks")
	s.StretchLength = p.Float("stretch")
	s.SeatBox = p.Str("seat") == "box"
	s.MaxBackGap = p.Float("max_back_gap")
	s.BackCover = p.Float("back_cover")
	s.MinContact, s.ContactGap, s.Backing = p.Float("min_contact"), p.Float("contact_gap"), p.Float("backing")
	s.TierJitter, s.HeightNorm = p.Float("tier_jitter"), p.Float("height_norm")
	s.Focus, s.FocusRange = presetPoint(env, p.Vec2("focus")), p.Float("focus_range")
	s.Keep = regionWallKeep(env)
	return s
}

// wallLines is the lines the walls run along.
// For a region the splines and the rectangle are in the preset's
// coordinates, moved by the region's anchor (presetPoint).
func wallLines(env *procgen.Env, g *place.Ground, p procgen.Params) []place.WallLine {
	var lines []place.WallLine
	src := p.Str("source")
	if src == "traced" || src == "both" {
		traced := place.TraceCliffFeet(g, place.TraceSettings{Threshold: p.Float("cliff_threshold"), Cell: p.Float("trace_cell"),
			MinLength: p.Float("min_length"), Smooth: p.Int("smooth")})
		lo, hi := p.Vec2("region_min"), p.Vec2("region_max")
		if lo != hi {
			traced = clipLines(traced, presetPoint(env, lo), presetPoint(env, hi), p.Float("min_length"))
		}
		lines = append(lines, traced...)
	}
	if src == "splines" || src == "both" {
		for _, it := range p.Items("paths") {
			var pts [][2]float64
			for _, q := range it.Points("path") {
				pts = append(pts, presetPoint(env, [2]float64{q[0], q[1]}))
			}
			lines = append(lines, place.SplineLines(g, pts, it.Str("side"))...)
		}
	}
	return lines
}

// clipLines keeps the runs of each line inside the rectangle.
func clipLines(lines []place.WallLine, lo, hi [2]float64, minLen float64) []place.WallLine {
	x0, x1 := math.Min(lo[0], hi[0]), math.Max(lo[0], hi[0])
	z0, z1 := math.Min(lo[1], hi[1]), math.Max(lo[1], hi[1])
	var out []place.WallLine
	for _, l := range lines {
		var cur place.WallLine
		flush := func() {
			if len(cur.Points) >= 2 && cur.Length() >= minLen {
				out = append(out, cur)
			}
			cur = place.WallLine{}
		}
		for k, q := range l.Points {
			if q[0] < x0 || q[0] > x1 || q[1] < z0 || q[1] > z1 {
				flush()
				continue
			}
			cur.Points = append(cur.Points, q)
			cur.Out = append(cur.Out, l.Out[k])
		}
		flush()
	}
	return out
}

func validateCliffWall(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	var out []string
	if p.Str("terrain") == "" {
		out = append(out, "Choose a terrain stage")
	}
	if p.Str("chunks") == "" {
		out = append(out, "Choose a rocks stage of cliff chunks")
	}
	if p.Str("source") != "traced" && len(p.Items("paths")) == 0 {
		out = append(out, "Add a path (or trace the walls)")
	}
	for i, it := range p.Items("paths") {
		if n := len(it.Points("path")); n == 1 {
			out = append(out, fmt.Sprintf("path %d needs at least two points", i+1))
		}
	}
	return out
}

func runCliffWall(rc *procgen.RunContext) error {
	p := rc.Params
	t0 := time.Now()
	g, err := runGround(rc, p.Str("terrain"))
	if err != nil {
		return err
	}
	s := wallSettings(&rc.Env, p, rc.Seed)
	var chunkArt, ledgeArt *RocksArtifact
	if s.Chunks, chunkArt, err = runVariants(rc, p.Str("chunks")); err != nil {
		return err
	}
	if id := p.Str("ledges"); id != "" {
		if s.Ledges, ledgeArt, err = runVariants(rc, id); err != nil {
			return err
		}
	}
	if !s.SeatBox {
		profileVariants(rc, s.Chunks)
	}
	ground, err := groundArtifact(rc, p.Str("ground"))
	if err != nil {
		return err
	}
	matFor := func(part string, a *RocksArtifact) (string, error) {
		if a == nil {
			return "", nil
		}
		base := ""
		if a.Manifest != nil {
			base = a.Manifest.Material
		}
		fx := rockFxOf(rc, p, ground, a)
		if p.Str("match_to") == "rock" && ground != nil && ground.RockMean != ([3]float64{}) {
			fx.MatchColor = ground.RockMean
		}
		fx.Brightness = p.Float("match_brightness")
		return rockMaterial(rc, part, base, fx)
	}
	chunkMat, err := matFor("chunks", chunkArt)
	if err != nil {
		return err
	}
	ledgeMat, err := matFor("ledges", ledgeArt)
	if err != nil {
		return err
	}
	for _, v := range s.Chunks {
		if !v.Back {
			rc.Logf("warning: %s has no flat back (use the cliff_chunk preset): it will be stood against the wall anyway", v.Name)
			break
		}
	}
	lines := wallLines(&rc.Env, g, p)
	rc.Progress(0.3)
	res, err := place.Walls(g, lines, s, &rc.Env)
	if err != nil {
		return err
	}
	var nc, nl, tris int
	pls := make([]procgen.Placement, 0, len(res.Instances))
	for k := range res.Instances {
		in := &res.Instances[k]
		var v *place.Variant
		if in.Kind == "ledge" {
			nl++
			v = &s.Ledges[in.Variant]
			pl := placementOf(fmt.Sprintf("ledge_%05d", nl), in, v)
			pl.Material = ledgeMat
			pls = append(pls, pl)
		} else {
			nc++
			v = &s.Chunks[in.Variant]
			pl := placementOf(fmt.Sprintf("chunk_%05d", nc), in, v)
			pl.Material = chunkMat
			pls = append(pls, pl)
		}
		tris += v.Triangles
	}
	rc.Place(pls...)
	if err := writeFootprints(rc, res.Ground, []footprintsRule{{Name: "chunks", Kept: nc}, {Name: "ledges", Kept: nl}}); err != nil {
		return err
	}
	rc.Artifact = &FootprintsArtifact{Discs: res.Ground}
	length := 0.0
	for i := range lines {
		length += lines[i].Length()
	}
	rc.Logf("%d lines (%.0f m), %d columns: %d chunks, %d ledges (%d walls too low, %d without a wall, %d crossing another, "+
		"%d reseated as another variant, %d dropped for a back gap, %d for too little contact, %d pillars), %.1f M triangles at full detail, %s", len(lines), length,
		res.Columns, nc, nl, res.TooLow, res.NoWall, res.Crossing, res.Reseated, res.GapDropped, res.ContactDropped, res.Pillars,
		float64(tris)/1e6, time.Since(t0).Round(time.Millisecond))
	if chunkMat != "" || ledgeMat != "" {
		rc.Logf("materials: chunks %q, ledges %q", chunkMat, ledgeMat)
	}
	if res.Capped {
		rc.Logf("warning: stopped at max_chunks (%d)", s.MaxChunks)
	}
	rc.Progress(1)
	return nil
}

func previewCliffWall(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
	p := pc.Params
	t0 := time.Now()
	g, up, err := previewGround(pc, p.Str("terrain"))
	if err != nil {
		return nil, err
	}
	s := wallSettings(&pc.Env, p, pc.Seed)
	cv, cm := previewRock(pc, p.Str("chunks"))
	s.Chunks = []place.Variant{cv}
	var lm procgen.PreviewMesh
	if id := p.Str("ledges"); id != "" {
		lv, lmesh := previewRock(pc, id)
		s.Ledges = []place.Variant{lv}
		lm = proxyMesh("ledges", lmesh, 200)
	}
	lines := wallLines(&pc.Env, g, p)
	res, err := place.Walls(g, lines, s, &pc.Env)
	if err != nil {
		return nil, err
	}
	out := &procgen.PreviewData{Bounds: up.Bounds, Images: up.Images, Artifact: &FootprintsArtifact{Discs: res.Ground}}
	out.Meshes = append(out.Meshes, terrainMeshes(up)...)
	for _, l := range lines {
		out.Lines = append(out.Lines, procgen.LineLayer{Name: "Wall foot", Points: l.Points, Color: [4]float32{1, 0.5, 0.25, 0.9}})
	}
	chunkM := proxyMesh("chunks", cm, 600)
	chunks := procgen.PointLayer{Name: "Chunks", Color: [4]float32{1, 0.55, 0.2, 0.95}}
	ledges := procgen.PointLayer{Name: "Ledges", Color: [4]float32{0.35, 0.8, 1, 0.95}}
	nc, nl := 0, 0
	for k := range res.Instances {
		in := &res.Instances[k]
		if in.Kind == "ledge" {
			nl++
			ledges.Points = append(ledges.Points, [2]float64{in.Pos[0], in.Pos[2]})
			if len(lm.Indices) > 0 {
				lm.Instances = append(lm.Instances, instanceMatrix(in))
			}
			continue
		}
		nc++
		chunks.Points = append(chunks.Points, [2]float64{in.Pos[0], in.Pos[2]})
		if len(chunkM.Indices) > 0 {
			chunkM.Instances = append(chunkM.Instances, instanceMatrix(in))
		}
	}
	out.Points = append(out.Points, chunks, ledges)
	for _, m := range []procgen.PreviewMesh{chunkM, lm} {
		if len(m.Instances) > 0 {
			out.Meshes = append(out.Meshes, m)
		}
	}
	out.Notes = append(out.Notes,
		fmt.Sprintf("%d wall lines, %d columns: %d chunks, %d ledges (%d too low, %d no wall, %d crossing)", len(lines), res.Columns,
			nc, nl, res.TooLow, res.NoWall, res.Crossing),
		fmt.Sprintf("preview on the terrain's %.2f m grid with one previewed chunk; %s", g.Grid.Cell, time.Since(t0).Round(time.Millisecond)))
	return out, nil
}
