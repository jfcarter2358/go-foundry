package stages

// ruins.go — the `ruins` stage: ancient sandstone architecture laid out
// along an authored path down a slope (docs/adventure/PHASE_D.md; the
// architecture and the layout are internal/procgen/ruins).
//
// The run:
//
//  1. THE LAYOUT on the terrain stage's heights (ruins.Lay): terraces
//     stepping down the path, stairs between them, buildings either side of
//     the walkway, gateways across it, walls along the terrace edges,
//     towers on plinths on the slope beside, rubble and fallen columns.
//  2. THE LIBRARY: every building variant the layout uses is meshed ONCE
//     at a fine cell (dual contouring, 3–7 cm: the cornices, dentils, niches
//     and merlons are geometry), its buried base dropped, simplified within
//     `simplify`, baked (normals from the field, vertex AO from the field
//     with the floor under it, a dust tint on what faces up) and cooked to
//     Nanite with mesh collision — then placed as many times as the layout
//     says, each a transform. Instancing is what makes a mountainside of
//     carved towers cheap.
//  3. THE SITE (terraces, stairs, plinths: unique to the layout) is one
//     field meshed in 32 m world sections as wall_sheet's and cave's are:
//     half-cell overlaps, what is under the terrain dropped, box sides and
//     open edges locked in the simplification, one quantisation step.
//  4. MATERIALS: three, one per family (temples, walls, the site), each a
//     Poly Haven masonry layer of a polyhaven_textures stage projected
//     triplanar, its colour matched toward the ground's rock, with macro
//     variation and desert varnish; the site's feet blend into the ground.
//  5. ruins.yaml: the terraces' floors, the stairs, the plinths, every
//     instance and the free spots on the floors, for later stages to dress.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"foundry/internal/material"
	"foundry/internal/model"
	"foundry/internal/procgen"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/ruins"
	"foundry/internal/procgen/sdf"
	"foundry/internal/procgen/terrain"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// RuinsKind is the stage's name.
const RuinsKind = "ruins"

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     RuinsKind,
		Label:    "Ruins",
		Category: "Placement",
		// 2: the terraces' parapets are their own lightly weathered part
		// with bigger, square merlons (they eroded to cushions at the site's
		// cell), and the grade is warmer and more saturated (the towers read
		// grey against the reference's sandstone).
		// 3: pieces of a ruined variant that reach nowhere near its floor
		// (an arch's stones left floating by the ruin's cuts) are dropped.
		Version: 3,
		Doc: "Ancient sandstone ruins along a path down a slope: terraces held by battered retaining walls, stairs between " +
			"them, stepped temple towers with cornices, carved niches and domed crowns, pavilions on columns, gateways, " +
			"crenellated walls, fallen columns and rubble. The buildings are a library of variants meshed once at a fine " +
			"cell and instanced; the terraces and stairs are meshed in sections. Everything has mesh collision.",
		Params:   ruinsParams(),
		Run:      runRuins,
		Preview:  previewRuins,
		Load:     loadRuins,
		Validate: validateRuins,
	})
}

func ruinsParams() []procgen.ParamDef {
	f, i, v2, s := procgen.Float, procgen.Int, procgen.Vec2, procgen.String
	d := ruins.Defaults()
	return []procgen.ParamDef{
		{Name: "terrain", Type: procgen.ParamStage, StageKinds: []string{TerrainKind}, Default: procgen.StageRef(""),
			Doc: "The terrain stage the ruins stand on: the layout follows its heights."},
		{Name: "ground", Type: procgen.ParamStage, StageKinds: []string{TerrainMaterialKind}, Default: procgen.StageRef(""),
			Doc: "Optional terrain_material stage: the masonry's colour is matched toward its rock, and the terraces' feet blend into its ground."},
		{Name: "textures", Type: procgen.ParamStage, StageKinds: []string{PolyHavenTexturesKind}, Default: procgen.StageRef(""),
			Doc: "The polyhaven_textures stage whose layers the masonry wears (projected triplanar)."},
		{Name: "temple_layer", Type: procgen.ParamString, Label: "Temple layer", Default: s("temple"),
			Doc: "The textures stage's layer the towers, pavilions, gates, columns and rubble wear (e.g. large_sandstone_blocks)."},
		{Name: "wall_layer", Type: procgen.ParamString, Label: "Wall layer", Default: s("masonry"),
			Doc: "Its layer for the boundary walls (e.g. rabdentse_ruins_wall). Missing: the temple layer."},
		{Name: "site_layer", Type: procgen.ParamString, Label: "Terrace layer", Default: s("terrace"),
			Doc: "Its layer for the terraces, stairs and plinths (e.g. sandstone_blocks_08). Missing: the wall layer."},
		{Name: "texture_scale", Type: procgen.ParamFloat, Label: "Texture scale", Default: f(1), Min: 0.1, Max: 10, Step: 0.01,
			Doc: "Multiplies each layer's metres a repeat."},
		{Name: "path", Type: procgen.ParamSpline, Label: "Path", Default: procgen.Spline(),
			Channels: []string{"width"}, ChannelDefaults: []float64{30}, MinItems: 0,
			Doc: "The way down, top to bottom, in plan: terraces step down along it, stairs join them. Each point's width is the terraces' width there."},
		{Name: "terrace_length", Type: procgen.ParamVec2, Label: "Terrace length (m)", Default: v2(d.TerraceLength[0], d.TerraceLength[1]), Min: 4, Max: 200, Step: 0.5,
			Doc: "A terrace's length along the path, shortest to longest."},
		{Name: "walkway", Type: procgen.ParamFloat, Label: "Walkway (m)", Default: f(d.Walkway), Min: 1.2, Max: 20, Step: 0.1,
			Doc: "The clear way down the middle of the terraces, and the stairs' width."},
		{Name: "rise", Type: procgen.ParamFloat, Label: "Riser (m)", Default: f(d.Rise), Min: 0.05, Max: 0.5, Step: 0.01,
			Doc: "The tallest step: keep it under the player's step height (0.5 by default)."},
		{Name: "tread", Type: procgen.ParamFloat, Label: "Tread (m)", Default: f(d.Tread), Min: 0.2, Max: 2, Step: 0.01,
			Doc: "A step's run."},
		{Name: "max_flight", Type: procgen.ParamFloat, Label: "Longest flight (m)", Default: f(d.MaxFlight), Min: 1, Max: 100, Step: 0.5,
			Doc: "The longest stair between two terraces. Where the ground falls faster than a stair, the next terrace stands on a foundation instead."},
		{Name: "clearance", Type: procgen.ParamFloat, Label: "Floor clearance (m)", Default: f(d.Clearance), Min: 0.05, Max: 5, Step: 0.05,
			Doc: "A terrace floor's height over the highest ground under it."},
		{Name: "foundation", Type: procgen.ParamFloat, Label: "Foundation (m)", Default: f(d.Foundation), Min: 0.3, Max: 20, Step: 0.1,
			Doc: "How far the terraces, stairs and plinths reach under the lowest ground beneath them."},
		{Name: "buildings", Type: procgen.ParamFloat, Default: f(d.Buildings), Min: 0, Max: 1, Step: 0.01,
			Doc: "How full the terraces' sides are of buildings (the rest: open courts, rubble)."},
		{Name: "gates", Type: procgen.ParamFloat, Default: f(d.Gates), Min: 0, Max: 1, Step: 0.01,
			Doc: "The chance a terrace has a gateway over the walkway at its top."},
		{Name: "walls", Type: procgen.ParamFloat, Default: f(d.Walls), Min: 0, Max: 1, Step: 0.01,
			Doc: "The chance a terrace's long edge carries a crenellated wall (else a low parapet)."},
		{Name: "outliers", Type: procgen.ParamInt, Label: "Slope buildings", Default: i(d.Outliers), Min: 0, Max: 500,
			Doc: "How many towers and shrines to stand on the slope beside the path, each on a plinth into the ground."},
		{Name: "spread", Type: procgen.ParamFloat, Label: "Spread (m)", Default: f(d.Spread), Min: 0, Max: 500, Step: 1,
			Doc: "How far beside the terraces the slope's buildings may stand."},
		{Name: "max_plinth", Type: procgen.ParamFloat, Label: "Tallest plinth (m)", Default: f(d.MaxPlinth), Min: 1, Max: 200, Step: 0.5,
			Doc: "A slope building whose plinth would be taller than this is not built (a sheer drop)."},
		{Name: "ruin", Type: procgen.ParamFloat, Default: f(d.Ruin), Min: 0, Max: 1, Step: 0.01,
			Doc: "The share of buildings and walls that are ruined: tops fallen, corners lost."},
		{Name: "debris", Type: procgen.ParamFloat, Default: f(d.Debris), Min: 0, Max: 1, Step: 0.01,
			Doc: "How much rubble and how many fallen columns lie about."},
		{Name: "scale", Type: procgen.ParamVec2, Label: "Building scale", Default: v2(d.Scale[0], d.Scale[1]), Min: 0.3, Max: 4, Step: 0.01,
			Doc: "The buildings' size range."},
		{Name: "outlier_scale", Added: true, Type: procgen.ParamVec2, Label: "Slope building scale", Default: v2(0, 0), Min: 0, Max: 4, Step: 0.01,
			Doc: "The slope buildings' size range (0: the building scale): towers off the path bigger than the terraces' ones, a city seen from above."},
		{Name: "weathering", Type: procgen.ParamFloat, Default: f(1), Min: 0, Max: 3, Step: 0.01,
			Doc: "The stone's erosion: low swells, grain and eroded patches (1: about 5 cm)."},
		{Name: "rounding", Type: procgen.ParamInt, Label: "Edge rounding", Default: i(1), Min: 0, Max: sdf.MaxWeathering,
			Doc: "Curvature-flow steps that round the stone's convex edges, a cell at a time."},
		{Name: "detail", Type: procgen.ParamFloat, Default: f(1), Min: 0.2, Max: 2, Step: 0.01,
			Doc: "Scales the buildings' meshing resolution (1: 3–7 cm cells; 2: half that and about four times the triangles). The tallest towers are capped by the mesher's 512 cells."},
		{Name: "site_cell", Type: procgen.ParamFloat, Label: "Terrace cell (m)", Default: f(0.1), Min: 0.03, Max: 1, Step: 0.01,
			Doc: "The terraces' and stairs' meshing grid. Keep it under the riser."},
		{Name: "simplify", Type: procgen.ParamFloat, Label: "Simplify (m)", Default: f(0.006), Min: 0, Max: 0.1, Step: 0.001,
			Doc: "How far the simplification may move the stone (Nanite simplifies further with distance)."},
		{Name: "match", Type: procgen.ParamFloat, Label: "Match rock colour", Default: f(0.85), Min: 0, Max: 1, Step: 0.01,
			Doc: "How far the masonry's colour moves toward the ground's rock (or, without a ground, a warm sandstone)."},
		{Name: "stone_color", Type: procgen.ParamColor, Label: "Stone colour", Default: procgen.Color(0.8, 0.56, 0.37), Min: 0, Max: 1,
			Doc: "The sandstone's colour the masonry is matched toward (its hue; Brightness sets how light)."},
		{Name: "rock_share", Type: procgen.ParamFloat, Label: "Rock share", Default: f(0.4), Min: 0, Max: 1, Step: 0.01,
			Doc: "With a ground, how much of the match target is the ground's own rock colour rather than Stone colour."},
		{Name: "brightness", Type: procgen.ParamFloat, Default: f(0.5), Min: 0, Max: 1, Step: 0.01,
			Doc: "The masonry textures' mean brightness after grading (each scan scaled to it). 0: as shot."},
		{Name: "saturation", Type: procgen.ParamFloat, Default: f(1.25), Min: 0, Max: 3, Step: 0.01,
			Doc: "Scales the masonry's colour saturation after the match: scans shot under grey skies read grey in desert light."},
		{Name: "dust", Type: procgen.ParamFloat, Default: f(0.4), Min: 0, Max: 1, Step: 0.01,
			Doc: "Sand-coloured dust on the faces turned up (ledges, cornice tops, treads), in the vertex colour."},
		{Name: "streaks", Type: procgen.ParamFloat, Label: "Desert varnish", Default: f(0.25), Min: 0, Max: 1, Step: 0.01,
			Doc: "Dark streaks down the walls."},
		{Name: "ground_band", Type: procgen.ParamFloat, Label: "Ground blend (m)", Default: f(1.2), Min: 0, Max: 10, Step: 0.05,
			Doc: "With a ground, how far up the terraces' and plinths' feet its colour reaches."},
		{Name: "draw_distance", Type: procgen.ParamFloat, Label: "Draw distance (m)", Default: f(1500), Min: 0, Max: 100000, Step: 10,
			Doc: "How far the buildings and terraces are drawn (0: no limit)."},
		{Name: "shadow_distance", Type: procgen.ParamFloat, Label: "Shadow distance (m)", Default: f(500), Min: 0, Max: 100000, Step: 10,
			Doc: "How far they cast shadows (0: as drawn)."},
		{Name: "small_draw_distance", Type: procgen.ParamFloat, Label: "Small draw distance (m)", Default: f(350), Min: 0, Max: 100000, Step: 10,
			Doc: "How far columns, walls, rubble and fallen columns are drawn (their shadows: a third)."},
		{Name: "cook_memory", Type: procgen.ParamInt, Label: "Cook memory (MB)", Default: i(3000), Min: 100, Max: 64000,
			Doc: "How much memory the terrace sections' meshing and cooks may hold at once."},
		{Name: "preview_cell", Type: procgen.ParamFloat, Label: "Preview cell (m)", Default: f(0.5), Min: 0.1, Max: 4, Step: 0.05,
			PreviewOnly: true, Doc: "The 3D preview's meshing grid."},
	}
}

func validateRuins(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	var out []string
	if p.Str("terrain") == "" {
		out = append(out, "choose a terrain stage")
	}
	if len(p.Points("path")) < 2 {
		out = append(out, "draw the ruins' path: at least two points, top to bottom")
	}
	if v := p.Vec2("terrace_length"); v[1] < v[0] {
		out = append(out, "the terrace lengths must rise")
	}
	if v := p.Vec2("scale"); v[1] < v[0] {
		out = append(out, "the building scales must rise")
	}
	if p.Float("site_cell") > p.Float("rise") {
		out = append(out, "the terrace cell is coarser than the riser: the steps will not mesh")
	}
	return out
}

// ruinsSettings is the stage's ruins.Settings.
func ruinsSettings(env *procgen.Env) ruins.Settings {
	p := env.Params
	s := ruins.Defaults()
	s.Seed = env.Seed
	s.TerraceLength = p.Vec2("terrace_length")
	s.Walkway, s.Rise, s.Tread, s.MaxFlight = p.Float("walkway"), p.Float("rise"), p.Float("tread"), p.Float("max_flight")
	s.Clearance, s.Foundation = p.Float("clearance"), p.Float("foundation")
	s.Buildings, s.Gates, s.Walls = p.Float("buildings"), p.Float("gates"), p.Float("walls")
	s.Outliers, s.Spread, s.MaxPlinth = p.Int("outliers"), p.Float("spread"), p.Float("max_plinth")
	s.Ruin, s.Debris, s.Scale = p.Float("ruin"), p.Float("debris"), p.Vec2("scale")
	s.OutlierScale = p.Vec2("outlier_scale")
	for _, q := range p.Points("path") {
		w := 30.0
		if len(q) > 2 {
			w = q[2]
		}
		s.Path = append(s.Path, ruins.PathPoint{X: q[0], Z: q[1], Width: w})
	}
	return s
}

// groundFunc is the terrain's height as the layout reads it.
func groundFunc(g *terrain.Grid) ruins.Ground {
	return func(x, z float64) float64 { return float64(g.Sample(x, z)) }
}

// ---------- the manifest ----------

// RuinsFile is the stage's manifest.
const RuinsFile = "ruins.yaml"

// RuinsArtifact is the ruins stage's product (ruins.yaml): what stands
// where, and the free floor for later stages to dress.
type RuinsArtifact struct {
	Version   int              `yaml:"version"`
	Terraces  []RuinsTerrace   `yaml:"terraces"`
	Stairs    []RuinsStair     `yaml:"stairs,omitempty"`
	Plinths   []RuinsTerrace   `yaml:"plinths,omitempty"`
	Instances []RuinsInstance  `yaml:"instances,omitempty"`
	Spots     [][3]float64     `yaml:"spots,flow,omitempty"`
	Variants  []RuinsVariant   `yaml:"variants,omitempty"`
	Site      RuinsSiteSummary `yaml:"site"`
}

// RuinsTerrace is a terrace's (or plinth's) floor: its four corners in
// plan, its floor height, its body's bottom.
type RuinsTerrace struct {
	Corners [4][2]float64 `yaml:"corners,flow"`
	Floor   float64       `yaml:"floor"`
	Bottom  float64       `yaml:"bottom"`
	Yaw     float64       `yaml:"yaw"`
}

// RuinsStair is a flight: from its top (on the upper floor) to its foot.
type RuinsStair struct {
	From  [3]float64 `yaml:"from,flow"`
	To    [3]float64 `yaml:"to,flow"`
	Steps int        `yaml:"steps"`
	Rise  float64    `yaml:"rise"`
	Tread float64    `yaml:"tread"`
	Width float64    `yaml:"width"`
}

// RuinsInstance is a placed building.
type RuinsInstance struct {
	Variant  string     `yaml:"variant"`
	Position [3]float64 `yaml:"position,flow"`
	Yaw      float64    `yaml:"yaw"`
	Scale    float64    `yaml:"scale"`
}

// RuinsVariant is a library variant as cooked.
type RuinsVariant struct {
	Name      string  `yaml:"name"`
	Kind      string  `yaml:"kind"`
	Model     string  `yaml:"model"`
	Triangles int     `yaml:"triangles"`
	Cell      float64 `yaml:"cell"`
	Instances int     `yaml:"instances"`
	Bytes     int64   `yaml:"bytes"`
}

// RuinsSiteSummary is the terraces' meshing.
type RuinsSiteSummary struct {
	Sections  int   `yaml:"sections"`
	Triangles int   `yaml:"triangles"`
	Bytes     int64 `yaml:"bytes"`
}

func loadRuins(res *procgen.StageResult) (any, error) {
	data, err := os.ReadFile(filepath.Join(res.Dir, RuinsFile))
	if err != nil {
		return nil, err
	}
	var a RuinsArtifact
	if err := yaml.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("%s: %w", RuinsFile, err)
	}
	return &a, nil
}

func rectCorners(r ruins.Rect) [4][2]float64 {
	var c [4][2]float64
	for k, s := range [4][2]float64{{-1, -1}, {1, -1}, {1, 1}, {-1, 1}} {
		x, z := r.World(s[0]*r.HA, s[1]*r.HB)
		c[k] = [2]float64{x, z}
	}
	return c
}

// ruinsManifest is the layout as ruins.yaml.
func ruinsManifest(l *ruins.Layout) *RuinsArtifact {
	a := &RuinsArtifact{Version: 1}
	for _, t := range l.Terraces {
		a.Terraces = append(a.Terraces, RuinsTerrace{Corners: rectCorners(t.Rect), Floor: t.Floor, Bottom: t.Bottom, Yaw: t.Yaw()})
	}
	for _, p := range l.Plinths {
		a.Plinths = append(a.Plinths, RuinsTerrace{Corners: rectCorners(p.Rect), Floor: p.Top, Bottom: p.Bottom, Yaw: p.Yaw()})
	}
	for _, s := range l.Stairs {
		x0, z0 := s.World(-s.HA, 0)
		x1, z1 := s.World(s.HA, 0)
		a.Stairs = append(a.Stairs, RuinsStair{From: [3]float64{x0, s.Top, z0}, To: [3]float64{x1, s.Bottom, z1}, Steps: s.Steps,
			Rise: s.Rise, Tread: s.Tread, Width: 2 * s.HB})
	}
	for _, in := range l.Instances {
		a.Instances = append(a.Instances, RuinsInstance{Variant: in.Variant, Position: [3]float64{in.X, in.Y, in.Z}, Yaw: in.Yaw, Scale: in.Scale})
	}
	for _, sp := range l.Spots {
		a.Spots = append(a.Spots, [3]float64{sp.X, sp.Y, sp.Z})
	}
	return a
}

// ---------- the run ----------

func runRuins(rc *procgen.RunContext) error {
	p := rc.Params
	t0 := time.Now()
	tid := p.Str("terrain")
	ta, err := rc.UpstreamArtifact(tid)
	if err != nil {
		return err
	}
	t, ok := ta.(*terrain.Terrain)
	if !ok {
		return fmt.Errorf("stage %s is not a terrain stage", tid)
	}
	ground, err := groundArtifact(rc, p.Str("ground"))
	if err != nil {
		return err
	}
	g := t.Grid
	gf := groundFunc(g)
	st := ruinsSettings(&rc.Env)
	lib := ruins.Library(noise.Hash64(rc.Seed, 0x4C49_42), p.Float("weathering"))
	lay := ruins.Lay(st, gf, lib)
	if len(lay.Terraces) == 0 {
		return fmt.Errorf("the path is too short for a terrace")
	}
	rc.Logf("layout: %d terraces, %d stairs, %d plinths, %d instances in %s", len(lay.Terraces), len(lay.Stairs), len(lay.Plinths),
		len(lay.Instances), time.Since(t0).Round(time.Millisecond))

	// Materials.
	mats, err := ruinsMaterials(rc, ground)
	if err != nil {
		return err
	}

	// The library: every variant the layout uses, meshed and cooked once.
	uses := map[string]int{}
	for _, in := range lay.Instances {
		uses[in.Variant]++
	}
	a := ruinsManifest(lay)
	var names []string
	for _, v := range lib {
		if uses[v.Name] > 0 {
			names = append(names, v.Name)
		}
	}
	models := map[string]string{}
	for k, name := range names {
		if err := rc.Err(); err != nil {
			return err
		}
		v := ruins.ByName(lib, name)
		rv, err := ruinsVariant(rc, v)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		rv.Instances = uses[name]
		a.Variants = append(a.Variants, *rv)
		models[name] = rv.Model
		rc.Progress(0.05 + 0.55*float32(k+1)/float32(len(names)))
	}

	// Place the instances.
	draw, shadow := float32(p.Float("draw_distance")), float32(p.Float("shadow_distance"))
	small := float32(p.Float("small_draw_distance"))
	for k, in := range lay.Instances {
		v := ruins.ByName(lib, in.Variant)
		mat := mats.temple
		if v.Family == "wall" {
			mat = mats.wall
		}
		dd, sd := draw, shadow
		switch v.Kind {
		case ruins.KindColumn, ruins.KindFallen, ruins.KindRubble, ruins.KindWall:
			dd, sd = small, small/3
		}
		sc := float32(in.Scale)
		rc.Place(procgen.Placement{ID: fmt.Sprintf("%s_%04d", v.Name, k), Model: models[in.Variant],
			Position: procgen.Point3{X: float32(in.X), Y: float32(in.Y), Z: float32(in.Z)},
			Rotation: procgen.Point3{Y: float32(in.Yaw)}, Scale: &procgen.Point3{X: sc, Y: sc, Z: sc},
			Collision: "mesh", Chunk: procgen.ChunkAuto, Material: mat, DrawDistance: dd, ShadowDistance: sd})
	}

	// The site.
	site := ruins.NewSite(lay, noise.Hash64(rc.Seed, 0x5349), st.Walkway, p.Float("weathering"))
	if !site.Empty() {
		sum, err := ruinsSite(rc, site, gf, mats.site, draw, shadow)
		if err != nil {
			return err
		}
		a.Site = sum
	}

	// The manifest.
	data, err := yaml.Marshal(a)
	if err != nil {
		return err
	}
	if err := rc.WriteFile(RuinsFile, data); err != nil {
		return err
	}
	rc.Artifact = a
	var vt int
	var vb int64
	for _, v := range a.Variants {
		vt += v.Triangles
		vb += v.Bytes
	}
	rc.Logf("%d variants: %.2f M triangles, %.1f MB; site: %d sections, %.2f M triangles, %.1f MB; %d instances; done in %s",
		len(a.Variants), float64(vt)/1e6, float64(vb)/(1<<20), a.Site.Sections, float64(a.Site.Triangles)/1e6,
		float64(a.Site.Bytes)/(1<<20), len(a.Instances), time.Since(t0).Round(time.Millisecond))
	rc.Progress(1)
	return nil
}

// ---------- the library's meshes ----------

// dropUngrounded drops every connected piece of m whose lowest point is more
// than reach over the floor (y = 0): nothing holds it up. It returns how
// many pieces it dropped.
func dropUngrounded(m *sdf.Mesh, reach float64) int {
	nv := len(m.Positions)
	parent := make([]int32, nv)
	for i := range parent {
		parent[i] = int32(i)
	}
	find := func(x int32) int32 {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for t := 0; t+2 < len(m.Indices); t += 3 {
		a := find(int32(m.Indices[t]))
		for k := 1; k < 3; k++ {
			if b := find(int32(m.Indices[t+k])); a != b {
				parent[b] = a
			}
		}
	}
	low := map[int32]float64{}
	for _, ix := range m.Indices {
		r := find(int32(ix))
		if y, ok := low[r]; !ok || m.Positions[ix][1] < y {
			low[r] = m.Positions[ix][1]
		}
	}
	dropped := 0
	for _, y := range low {
		if y > reach {
			dropped++
		}
	}
	if dropped == 0 {
		return 0
	}
	out := m.Indices[:0]
	for t := 0; t+2 < len(m.Indices); t += 3 {
		if low[find(int32(m.Indices[t]))] <= reach {
			out = append(out, m.Indices[t], m.Indices[t+1], m.Indices[t+2])
		}
	}
	m.Indices = out
	m.Compact()
	return dropped
}

// ruinsSharpness is the mesher's: high, for crisp mouldings.
const ruinsSharpness = 0.85

// ruinsVariant meshes, bakes and cooks one variant.
func ruinsVariant(rc *procgen.RunContext, v *ruins.Variant) (*RuinsVariant, error) {
	p := rc.Params
	ts := time.Now()
	shape := v.Build()
	box := shape.Box
	// The finest cell the mesher's 512 a side allows, or the variant's.
	ext := box.Size().MaxComp()
	cell := math.Max(v.Cell/p.Float("detail"), (ext+0.6)/480)
	m, mst, err := sdf.Polygonize(shape, sdf.MeshOptions{Cell: cell, Weathering: p.Int("rounding"), Sharpness: ruinsSharpness,
		Workers: rc.Workers, Ctx: rc.Ctx})
	if err != nil {
		return nil, err
	}
	raw := m.TriangleCount()
	tMesh := time.Since(ts)
	// The base under the floor is never seen.
	dropBelow(m, func(q sdf.Vec3) bool { return q[1] < -0.3 })
	// Nor is what a ruin's cuts left hanging in the air: a gateway's arch
	// with its crown and a pier knocked away leaves its voussoirs floating.
	dropUngrounded(m, 0.5)
	ts = time.Now()
	far := sdf.Vec3{1e6, 1e6, 1e6}
	simplifyBox(m, far.Mul(-1), far, cell, p.Float("simplify"))
	tSimp := time.Since(ts)
	ts = time.Now()
	// The bake reads the building standing on its floor.
	field := sdf.Shape{F: func(q sdf.Vec3) float64 { return math.Min(shape.F(q), q[1]+0.02) }, Lip: shape.Lip}
	im := ruinsBake(rc, m, field, cell, nil, noise.Hash64(rc.Seed, noise.HashString(v.Name)))
	tBake := time.Since(ts)
	ts = time.Now()
	name := "lib_" + v.Name
	if _, err := meshout.Cook(im, rc.Path(name+".fnm"), meshout.CookOptions{CollisionMesh: true, AttrRadiusCap: 1}); err != nil {
		return nil, err
	}
	var size int64
	if fi, err := os.Stat(rc.Path(name + ".fnm")); err == nil {
		size = fi.Size()
	}
	rc.Logf("%s: cell %.3f m (%v), %d → %d triangles; mesh %s, simplify %s, bake %s, cook %s; %.1f MB",
		v.Name, cell, mst.Cells, raw, len(im.Indices)/3, tMesh.Round(time.Millisecond), tSimp.Round(time.Millisecond),
		tBake.Round(time.Millisecond), time.Since(ts).Round(time.Millisecond), float64(size)/(1<<20))
	return &RuinsVariant{Name: v.Name, Kind: v.Kind, Model: rc.AssetPath(name + ".fnm"), Triangles: len(im.Indices) / 3,
		Cell: cell, Bytes: size}, nil
}

// dropBelow removes the triangles whose every vertex hidden says is hidden.
func dropBelow(m *sdf.Mesh, hidden func(q sdf.Vec3) bool) {
	h := make([]bool, len(m.Positions))
	for i, q := range m.Positions {
		h[i] = hidden(q)
	}
	out := m.Indices[:0]
	for t := 0; t+2 < len(m.Indices); t += 3 {
		a, b, c := m.Indices[t], m.Indices[t+1], m.Indices[t+2]
		if h[a] && h[b] && h[c] {
			continue
		}
		out = append(out, a, b, c)
	}
	m.Indices = out
	m.Compact()
}

// dustColor is what the dust tints toward (a multiplier on the masonry's
// albedo: paler, yellower).
var dustColor = [3]float64{1, 0.9, 0.72}

// ruinsBake turns a mesh into an ImportMesh: normals from the field, vertex
// AO from the field (alpha), a dust tint on the faces turned up and a slow
// tint variation (RGB). ground, when set, is the terrain's height: the
// stone near it takes its sand. Positions are relative to origin.
func ruinsBake(rc *procgen.RunContext, m *sdf.Mesh, field sdf.Shape, cell float64, ground ruins.Ground, seed uint64) *model.ImportMesh {
	return ruinsBakeAt(rc, m, field, cell, ground, seed, sdf.Vec3{}, nil)
}

func ruinsBakeAt(rc *procgen.RunContext, m *sdf.Mesh, field sdf.Shape, cell float64, ground ruins.Ground, seed uint64,
	origin sdf.Vec3, keepNormal func(q sdf.Vec3) bool) *model.ImportMesh {
	nv := len(m.Positions)
	dust := rc.Params.Float("dust")
	tint := noise.Fractal{Seed: noise.Hash64(seed, 0x5449_4E54), Octaves: 2, Frequency: 1 / 5.0}
	patch := noise.Fractal{Seed: noise.Hash64(seed, 0x4455_5354), Octaves: 3, Frequency: 1 / 1.7}
	im := &model.ImportMesh{Slots: []model.ImportSlot{{Name: "stone", BaseColor: [4]float32{1, 1, 1, 1}}},
		Positions: make([]mgl32.Vec3, nv), Normals: make([]mgl32.Vec3, nv), UVs: make([]mgl32.Vec2, nv), Colors: make([][4]uint8, nv)}
	const chunk = 2048
	_ = rc.Parallel((nv+chunk-1)/chunk, func(c int) error {
		for k := c * chunk; k < min(nv, (c+1)*chunk); k++ {
			q := m.Positions[k]
			n := m.Normals[k]
			if keepNormal == nil || !keepNormal(q) {
				n = field.Gradient(q, cell*0.5)
			}
			occ := field.AO(q, n, 1.6, 4)
			a := 0.22 + 0.78*occ
			// Dust where the stone faces up, in patches, more in the sheltered
			// corners.
			up := sdf.SmoothStep(0.45, 0.85, n[1])
			dv := dust * up * sdf.Clamp(0.55+0.6*patch.At3(q[0], q[1], q[2])+0.3*(1-occ), 0, 1)
			if ground != nil {
				if h := q[1] - ground(q[0], q[2]); h < 1.2 {
					dv = math.Max(dv, dust*0.8*sdf.SmoothStep(1.2, 0.1, h))
				}
			}
			tv := 0.92 + 0.08*tint.At3(q[0], q[1], q[2])
			for ch := 0; ch < 3; ch++ {
				c := tv * sdf.Mix(1, dustColor[ch], dv)
				im.Colors[k][ch] = uint8(noise.Clamp(c, 0, 1)*255 + 0.5)
			}
			im.Colors[k][3] = uint8(noise.Clamp(a, 0, 1)*255 + 0.5)
			r := q.Sub(origin)
			im.Positions[k] = mgl32.Vec3{float32(r[0]), float32(r[1]), float32(r[2])}
			im.Normals[k] = mgl32.Vec3{float32(n[0]), float32(n[1]), float32(n[2])}
		}
		return nil
	})
	im.Indices = append([]uint32(nil), m.Indices...)
	im.TriSlot = make([]int32, len(im.Indices)/3)
	return im
}

// ---------- the site ----------

// ruinsSectionSize is a site section's plan side (m).
const ruinsSectionSize = 32

// ruinsSite meshes the site in world sections, cooks and places them.
func ruinsSite(rc *procgen.RunContext, site *ruins.Site, gf ruins.Ground, mat string, draw, shadow float32) (RuinsSiteSummary, error) {
	p := rc.Params
	cell := p.Float("site_cell")
	field := site.Shape()
	box := site.Box()
	sec := float64(ruinsSectionSize)
	slab := math.Floor(480*cell/sec) * sec
	if slab < sec {
		slab = math.Max(480*cell, 4)
	}
	type section struct {
		i, j, k int
		lo, hi  sdf.Vec3
	}
	var secs []section
	for i := int(math.Floor(box.Lo[0] / sec)); i <= int(math.Floor(box.Hi[0]/sec)); i++ {
		for j := int(math.Floor(box.Lo[2] / sec)); j <= int(math.Floor(box.Hi[2]/sec)); j++ {
			for k := int(math.Floor(box.Lo[1] / slab)); k <= int(math.Floor(box.Hi[1]/slab)); k++ {
				lo := sdf.Vec3{float64(i) * sec, float64(k) * slab, float64(j) * sec}
				hi := sdf.Vec3{lo[0] + sec, lo[1] + slab, lo[2] + sec}
				// Skip a section no part reaches, or one wholly under the
				// terrain.
				if !site.Touches(sdf.Box{Lo: lo, Hi: hi}, 0.5) {
					continue
				}
				// Wholly more than a metre under the terrain: the clip below
				// leaves it empty.
				gmin := math.Inf(1)
				for x := lo[0]; x <= hi[0]+0.01; x += 1 {
					for z := lo[2]; z <= hi[2]+0.01; z += 1 {
						gmin = math.Min(gmin, gf(x, z))
					}
				}
				if hi[1] < gmin-1.5 {
					continue
				}
				secs = append(secs, section{i, j, k, lo, hi})
			}
		}
	}
	// One quantisation step for every section, from the tallest.
	step := float32(math.Ldexp(1, int(math.Floor(math.Log2(math.Max(slab, sec))))-18))
	estTris := int(2 * sec * 20 / (cell * cell))
	conc := max(1, min(int(float64(p.Int("cook_memory"))*(1<<20)/(float64(estTris)*sectionBytesPerTriangle)), max(rc.Workers, 1)))
	rc.Logf("site: %d sections of %g m (slabs of %g m), cell %g m, %d at a time", len(secs), sec, slab, cell, conc)
	sem := make(chan struct{}, conc)
	var tris, raw, bytes atomic.Int64
	var tMesh, tSimp, tBake, tCook atomic.Int64
	var mu sync.Mutex
	placed := 0
	err := rc.Parallel(len(secs), func(n int) error {
		sem <- struct{}{}
		defer func() { <-sem }()
		if err := rc.Err(); err != nil {
			return err
		}
		s := secs[n]
		lo := s.lo.Sub(sdf.Vec3{cell / 2, cell / 2, cell / 2})
		hi := s.hi.Add(sdf.Vec3{cell / 2, cell / 2, cell / 2})
		snap := func(x float64) float64 { return math.Floor(x/cell) * cell }
		shape := sdf.Shape{
			F: func(q sdf.Vec3) float64 {
				b := math.Max(math.Max(lo[0]-q[0], q[0]-hi[0]), math.Max(math.Max(lo[1]-q[1], q[1]-hi[1]), math.Max(lo[2]-q[2], q[2]-hi[2])))
				if b > 1 {
					return b
				}
				// Nothing more than a metre under the terrain is meshed: the
				// foundations stop there (flat bottoms nobody sees, dropped
				// below).
				under := (gf(q[0], q[2]) - 1.0 - q[1]) * 0.5
				if under > 1 {
					return math.Max(under, b)
				}
				return math.Max(math.Max(field.F(q), b), under)
			},
			Box: sdf.Box{Lo: sdf.Vec3{snap(lo[0]), snap(lo[1]), snap(lo[2])}, Hi: hi},
			Lip: field.Lip,
		}
		ts := time.Now()
		m, _, err := sdf.Polygonize(shape, sdf.MeshOptions{Cell: cell, Weathering: p.Int("rounding"), Sharpness: ruinsSharpness, Workers: 1, Ctx: rc.Ctx})
		tMesh.Add(int64(time.Since(ts)))
		if err != nil {
			return fmt.Errorf("section %d,%d,%d: %w", s.i, s.j, s.k, err)
		}
		// Hidden: under the terrain, and the box's own faces through the
		// stone.
		boxed := func(q sdf.Vec3) bool {
			return onBoxFace(q, lo, hi, 0.6*cell) != 0 && field.F(q) < -0.7*cell
		}
		ts = time.Now()
		raw.Add(int64(m.TriangleCount()))
		dropBelow(m, func(q sdf.Vec3) bool { return q[1] < gf(q[0], q[2])-0.35 || boxed(q) })
		if m.TriangleCount() == 0 {
			return nil
		}
		simplifyBox(m, lo, hi, cell, p.Float("simplify"))
		tSimp.Add(int64(time.Since(ts)))
		ts = time.Now()
		if m.TriangleCount() == 0 {
			return nil
		}
		b := m.Bounds()
		centre := sdf.Vec3{math.Round((b.Lo[0]+b.Hi[0])/2*4) / 4, math.Round(b.Lo[1]*4) / 4, math.Round((b.Lo[2]+b.Hi[2])/2*4) / 4}
		aoField := sdf.Shape{F: func(q sdf.Vec3) float64 { return math.Min(field.F(q), (q[1]-gf(q[0], q[2]))*0.7) }, Lip: field.Lip}
		im := ruinsBakeAt(rc, m, aoField, cell, gf, noise.Hash64(rc.Seed, uint64(s.i), uint64(s.j), uint64(s.k)), centre,
			func(q sdf.Vec3) bool { return onBoxFace(q, lo, hi, 0.3*cell) != 0 && field.F(q) < -0.25*cell })
		tBake.Add(int64(time.Since(ts)))
		ts = time.Now()
		name := fmt.Sprintf("site_%d_%d_%d", s.i, s.j, s.k)
		if _, err := meshout.Cook(im, rc.Path(name+".fnm"), meshout.CookOptions{PositionStep: step, CollisionMesh: true, AttrRadiusCap: 1}); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if fi, err := os.Stat(rc.Path(name + ".fnm")); err == nil {
			bytes.Add(fi.Size())
		}
		tCook.Add(int64(time.Since(ts)))
		tris.Add(int64(len(im.Indices) / 3))
		rc.Place(procgen.Placement{ID: name, Model: rc.AssetPath(name + ".fnm"),
			Position:  procgen.Point3{X: float32(centre[0]), Y: float32(centre[1]), Z: float32(centre[2])},
			Collision: "mesh", Chunk: procgen.ChunkAuto, Material: mat, DrawDistance: draw, ShadowDistance: shadow})
		mu.Lock()
		placed++
		mu.Unlock()
		rc.Progress(0.6 + 0.38*float32(placed)/float32(max(len(secs), 1)))
		return nil
	})
	if err != nil {
		return RuinsSiteSummary{}, err
	}
	sum := func(a *atomic.Int64) string { return time.Duration(a.Load()).Round(time.Second).String() }
	rc.Logf("site: %d sections placed, %.2f M triangles (%.2f M meshed); mesh %s, simplify %s, bake %s, cook %s (summed)", placed,
		float64(tris.Load())/1e6, float64(raw.Load())/1e6, sum(&tMesh), sum(&tSimp), sum(&tBake), sum(&tCook))
	return RuinsSiteSummary{Sections: placed, Triangles: int(tris.Load()), Bytes: bytes.Load()}, nil
}

// ---------- materials ----------

type ruinsMats struct{ temple, wall, site string }

// ruinsMaterials writes the three materials: a textures stage's layer
// projected triplanar, matched toward the ground's rock, varied, varnished;
// the site's feet blended into the ground.
func ruinsMaterials(rc *procgen.RunContext, ground *TerrainMaterialArtifact) (ruinsMats, error) {
	p := rc.Params
	var set *TextureSetManifest
	if id := p.Str("textures"); id != "" {
		a, err := rc.UpstreamArtifact(id)
		if err != nil {
			return ruinsMats{}, err
		}
		s, ok := a.(*TextureSetManifest)
		if !ok {
			return ruinsMats{}, fmt.Errorf("stage %s is not a Poly Haven textures stage", id)
		}
		set = s
	}
	// The stone's colour: the ground's rock, carried toward the stage's
	// sandstone colour (the masons chose the paler beds).
	stone := p.Vec3("stone_color")
	target := stone
	if ground != nil {
		rock := ground.RockMean
		if rock == ([3]float64{}) {
			rock = ground.GroundMean
		}
		if rock != ([3]float64{}) {
			w := p.Float("rock_share")
			for c := 0; c < 3; c++ {
				target[c] = rock[c]*w + stone[c]*(1-w)
			}
		}
	}
	layer := func(names ...string) *TextureLayer {
		if set == nil {
			return nil
		}
		for _, n := range names {
			if l := set.Layer(n); l != nil {
				return l
			}
		}
		return nil
	}
	write := func(part string, l *TextureLayer, site bool) (string, error) {
		d := &material.Doc{Name: genMaterialName(rc, part), Domain: material.DomainSurface,
			Doc: "Generated ruins masonry (" + part + ")"}
		if l != nil {
			d.Texture = l.Albedo
			scale := l.Scale
			if scale <= 0 {
				scale = l.Size
			}
			if scale <= 0 {
				scale = 2
			}
			e := d.AddEffect(material.KindTriplanar)
			e.Set("scale", material.FloatValue(float32(scale*p.Float("texture_scale"))))
			e.Set("sharpness", material.FloatValue(8))
			d.Doc += fmt.Sprintf(": %s (%s) triplanar", l.Name, l.ID)
		}
		if mt := p.Float("match"); mt > 0 || p.Float("brightness") > 0 {
			e := d.AddEffect(material.KindColorGrade)
			e.Set("match", material.FloatValue(float32(mt)))
			e.Set("target", material.ColorValue(mgl32.Vec3{float32(target[0]), float32(target[1]), float32(target[2])}))
			if sat := p.Float("saturation"); sat != 1 {
				e.Set("saturation", material.FloatValue(float32(sat)))
			}
			// Scans are shot in all lights: bring each layer's mean brightness
			// to the stage's, so a grey block scan and a pale one read as the
			// same stone.
			if bt := p.Float("brightness"); bt > 0 && l != nil {
				if mean, ok := textureMean(l.Albedo); ok {
					lum := 0.2126*mean[0] + 0.7152*mean[1] + 0.0722*mean[2]
					if lum > 0.01 {
						e.Set("brightness", material.FloatValue(float32(sdf.Clamp(bt/lum, 0.5, 2.5))))
					}
				}
			}
		}
		mv := d.AddEffect(material.KindMacroVariation)
		mv.Set("scale", material.FloatValue(24))
		mv.Set("strength", material.FloatValue(0.16))
		mv.Set("warmth", material.FloatValue(0.25))
		mv.Set("vertical", material.FloatValue(0.6))
		if sv := p.Float("streaks"); sv > 0 {
			e := d.AddEffect(material.KindStreaks)
			e.Set("strength", material.FloatValue(float32(sv)))
			e.Set("scale", material.FloatValue(0.8))
			e.Set("length", material.FloatValue(5))
		}
		if site && ground != nil && p.Float("ground_band") > 0 {
			e := d.AddEffect(material.KindGroundBlend)
			e.Set("ground", material.TextureValue(ground.Ground))
			e.Set("height", material.TextureValue(ground.Height))
			e.Set("bounds_min", material.Vec2Value(mgl32.Vec2{float32(ground.Min[0]), float32(ground.Min[1])}))
			e.Set("bounds_max", material.Vec2Value(mgl32.Vec2{float32(ground.Max[0]), float32(ground.Max[1])}))
			e.Set("height_range", material.Vec2Value(mgl32.Vec2{ground.HeightLo, ground.HeightHi}))
			e.Set("band", material.FloatValue(float32(p.Float("ground_band"))))
		}
		if err := writeMaterial(rc, d); err != nil {
			return "", err
		}
		return d.Name, nil
	}
	tl := layer(p.Str("temple_layer"))
	wl := layer(p.Str("wall_layer"), p.Str("temple_layer"))
	sl := layer(p.Str("site_layer"), p.Str("wall_layer"), p.Str("temple_layer"))
	if set != nil && tl == nil {
		rc.Logf("warning: the textures stage has no layer %q: the temples are untextured", p.Str("temple_layer"))
	}
	var m ruinsMats
	var err error
	if m.temple, err = write("temple", tl, false); err != nil {
		return m, err
	}
	if m.wall, err = write("wall", wl, false); err != nil {
		return m, err
	}
	if m.site, err = write("site", sl, true); err != nil {
		return m, err
	}
	return m, nil
}

// ---------- the preview ----------

func previewRuins(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
	st := ruinsSettings(&pc.Env)
	out := &procgen.PreviewData{}
	if len(st.Path) < 2 {
		out.Notes = append(out.Notes, "draw the ruins' path in the top-down view (at least two points, top to bottom)")
		return out, nil
	}
	up := pc.Upstream(pc.Params.Str("terrain"))
	var gf ruins.Ground = func(x, z float64) float64 { return 0 }
	if up != nil {
		out.Bounds = up.Bounds
		for _, im := range up.Images {
			if im.Name == "Relief" {
				out.Images = append(out.Images, im)
			}
		}
		if t, ok := up.Artifact.(*terrain.Terrain); ok && t.Grid != nil {
			gf = groundFunc(t.Grid)
		}
	}
	lib := ruins.Library(noise.Hash64(pc.Seed, 0x4C49_42), pc.Params.Float("weathering"))
	lay := ruins.Lay(st, gf, lib)
	path := procgen.LineLayer{Name: "Path", Color: [4]float32{1, 0.85, 0.4, 0.9}}
	for _, q := range lay.Path {
		path.Points = append(path.Points, [2]float64{q[0], q[1]})
	}
	out.Lines = append(out.Lines, path)
	rect := func(name string, r ruins.Rect, col [4]float32) {
		c := rectCorners(r)
		out.Lines = append(out.Lines, procgen.LineLayer{Name: name, Closed: true, Color: col,
			Points: [][2]float64{c[0], c[1], c[2], c[3]}})
	}
	for _, t := range lay.Terraces {
		rect("Terraces", t.Rect, [4]float32{0.95, 0.7, 0.45, 0.9})
	}
	for _, s := range lay.Stairs {
		rect("Stairs", s.Rect, [4]float32{0.5, 0.9, 1, 0.9})
	}
	for _, p := range lay.Plinths {
		rect("Plinths", p.Rect, [4]float32{0.9, 0.5, 0.35, 0.8})
	}
	pts := procgen.PointLayer{Name: "Buildings", Color: [4]float32{1, 0.95, 0.8, 0.7}}
	for _, in := range lay.Instances {
		v := ruins.ByName(lib, in.Variant)
		pts.Points = append(pts.Points, [2]float64{in.X, in.Z})
		pts.Radii = append(pts.Radii, math.Max(v.HX, v.HZ)*in.Scale)
	}
	out.Points = append(out.Points, pts)
	// A coarse 3D view: the site and the buildings as boxes of their
	// footprint and height (the variants' detail needs a run).
	cell := pc.Params.Float("preview_cell")
	site := ruins.NewSite(lay, 1, st.Walkway, 0)
	if !site.Empty() {
		sh := site.Shape()
		cell = math.Max(cell, sh.Box.Size().MaxComp()/480)
		m, _, err := sdf.Polygonize(sh, sdf.MeshOptions{Cell: cell, Workers: pc.Workers, Ctx: pc.Ctx})
		if err != nil {
			return nil, err
		}
		dropBelow(m, func(q sdf.Vec3) bool { return q[1] < gf(q[0], q[2])-0.5 })
		out.Meshes = append(out.Meshes, previewMeshOf("site", m))
	}
	bm := &sdf.Mesh{}
	for _, in := range lay.Instances {
		v := ruins.ByName(lib, in.Variant)
		appendBox(bm, in, v)
	}
	if len(bm.Indices) > 0 {
		out.Meshes = append(out.Meshes, previewMeshOf("buildings", bm))
	}
	kinds := map[string]int{}
	for _, in := range lay.Instances {
		kinds[ruins.ByName(lib, in.Variant).Kind]++
	}
	var ks []string
	for k, n := range kinds {
		ks = append(ks, fmt.Sprintf("%s %d", k, n))
	}
	sort.Strings(ks)
	out.Notes = append(out.Notes, fmt.Sprintf("ruins: %d terraces, %d stairs, %d plinths; %v", len(lay.Terraces), len(lay.Stairs), len(lay.Plinths), ks))
	return out, nil
}

func previewMeshOf(name string, m *sdf.Mesh) procgen.PreviewMesh {
	pm := procgen.PreviewMesh{Name: name}
	for k, q := range m.Positions {
		pm.Positions = append(pm.Positions, [3]float32{float32(q[0]), float32(q[1]), float32(q[2])})
		if k < len(m.Normals) {
			n := m.Normals[k]
			pm.Normals = append(pm.Normals, [3]float32{float32(n[0]), float32(n[1]), float32(n[2])})
		}
	}
	if len(pm.Normals) != len(pm.Positions) {
		pm.Normals = nil
	}
	pm.Indices = append([]uint32(nil), m.Indices...)
	return pm
}

// appendBox adds an instance's footprint box (to its height) to m.
func appendBox(m *sdf.Mesh, in ruins.Instance, v *ruins.Variant) {
	s, c := math.Sincos(in.Yaw)
	hx, hz, h := v.HX*in.Scale, v.HZ*in.Scale, v.Height*in.Scale
	base := uint32(len(m.Positions))
	for _, k := range [8][3]float64{{-1, 0, -1}, {1, 0, -1}, {1, 0, 1}, {-1, 0, 1}, {-1, 1, -1}, {1, 1, -1}, {1, 1, 1}, {-1, 1, 1}} {
		lx, lz := k[0]*hx, k[2]*hz
		m.Positions = append(m.Positions, sdf.Vec3{in.X + c*lx + s*lz, in.Y + k[1]*h, in.Z - s*lx + c*lz})
		m.Normals = append(m.Normals, sdf.Vec3{k[0], k[1]*2 - 1, k[2]}.Normalize())
	}
	for _, f := range [][4]uint32{{0, 3, 2, 1}, {4, 5, 6, 7}, {0, 1, 5, 4}, {1, 2, 6, 5}, {2, 3, 7, 6}, {3, 0, 4, 7}} {
		m.Indices = append(m.Indices, base+f[0], base+f[1], base+f[2], base+f[0], base+f[2], base+f[3])
	}
}
