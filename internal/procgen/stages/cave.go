package stages

// cave.go — the `cave` stage: a sandstone cave system along an authored 3D
// path, cut through a terrain (docs/adventure/PHASE_C.md; the method is
// internal/procgen/cave).
//
// The run:
//
//  1. THE VOID along the path (cave.New): a flat sandy floor, walls and a
//     vault, roughness, strata ledges, roof drapery and fallen blocks.
//  2. THE CUT: every terrain quad the void passes through (dilated by
//     cut_margin) is marked (cave.Cut). Every tile of the terrain stage the
//     cave touches — a hole, or the cave passing under it — is recooked
//     from the terrain's own heights with the terrain's own tile plan,
//     quantisation step and base (as drift does), with the marked quads
//     left out and no skirts (a skirt standing across a tunnel would be a
//     wall in it), and SUPERSEDES the terrain stage's tile. The recooked
//     tiles are open meshes: navigation reads them triangle by triangle,
//     which is what lets an agent walk into a cave a closed tile would fill.
//  3. THE ROCK round the void (cave.Rock): the ground's solid less the void,
//     its top hidden under the terrain except round the holes, where it
//     stands a lip over their ragged edges so cave and terrain meet without
//     a crack. It is meshed in sections on a world-aligned grid (dual
//     contouring at `cell`, as wall_sheet's), with the triangles nobody can
//     see dropped (deep in the rock, on a section's box) and simplified
//     within 3 cm with the section's edges locked.
//  4. THE BAKE: normals from the rock's field; vertex AO = the field's
//     near AO times the SKY's visibility (cave.BakeSky: rays to the open
//     sky through the mouths), so the cave darkens away from its mouths.
//  5. THREE MESHES a section: the sand floor (wearing the ground material's
//     sand layer), the rock (its rock layer, with a ground blend that fades
//     the walls' feet into sand colour at the cave floor's own height), and
//     the ground round a mouth (wearing the terrain tiles' own material).
//     Each is cooked with mesh collision and placed with draw and shadow
//     distances.
//  6. ROCKS: with `rocks` (a rocks or polyhaven_rocks stage), its variants
//     are strewn on the cave floor, more against the walls — the "rock pile"
//     breakdown at every scale the void's own blocks start.
//  7. cave.yaml: the path, the mouths, the interior's boxes and sample
//     points with their sky visibility (for later phases' ambient volumes
//     and lights), read back by Load into the artifact wall_sheet's `caves`
//     subtracts from its shell.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"

	"foundry/internal/cluster"
	"foundry/internal/material"
	"foundry/internal/model"
	"foundry/internal/procgen"
	"foundry/internal/procgen/cave"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/sdf"
	"foundry/internal/procgen/terrain"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// CaveKind is the stage's name.
const CaveKind = "cave"

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     CaveKind,
		Label:    "Cave",
		Category: "Placement",
		// 2: a point picks its nearest segment by a centreline at most 2.5 m
		// over the floor (a tall chamber's floor stepped up across it), and
		// the floor lifts only to level ground (a mouth at a steep bank's
		// foot rose into a hump a metre and more high).
		// 3: pieces of rock touching no face of their section's box (drapery
		// hanging in the open air over a mouth) are dropped; the ground round
		// a mouth wears the tiles' material, not the far backdrop's.
		Version: 3,
		Doc: "A sandstone cave along a 3D path through a terrain: a sandy floor strewn with fallen blocks, walls stepped by " +
			"strata ledges, a vault hung with rock drapery. Cuts the terrain's tiles open where it comes through the ground, " +
			"meshes the rock in sections with mesh collision, and bakes the interior dark away from the mouths.",
		Params:   caveParams(),
		Run:      runCave,
		Preview:  previewCave,
		Load:     loadCave,
		Validate: validateCave,
	})
}

func caveParams() []procgen.ParamDef {
	f, i, v2 := procgen.Float, procgen.Int, procgen.Vec2
	d := cave.Defaults()
	return append([]procgen.ParamDef{
		{Name: "terrain", Type: procgen.ParamStage, StageKinds: []string{TerrainKind}, Default: procgen.StageRef(""),
			Doc: "The terrain stage the cave is cut through (it must cook its tiles): its tiles where the cave passes are recooked with holes."},
		{Name: "ground", Type: procgen.ParamStage, StageKinds: []string{TerrainMaterialKind}, Default: procgen.StageRef(""),
			Doc: "The terrain_material stage: the walls wear its rock layer, the floor its sand layer."},
		{Name: "path", Type: procgen.ParamSpline, Label: "Path", Spline3D: true, Default: procgen.Spline(),
			Channels: []string{"width", "height"}, ChannelDefaults: []float64{8, 7}, MinItems: 0,
			Doc: "The cave's course: each point is on the FLOOR (x, y, z), with the width wall to wall and the height floor to " +
				"roof there. Start and end it outside the ground: those are the mouths. A wide, tall point is a chamber."},
		{Name: "arch", Type: procgen.ParamFloat, Label: "Vault", Default: f(d.Arch), Min: 0.05, Max: 1, Step: 0.01,
			Doc: "The share of the height that is vaulted roof; below it the walls stand vertical."},
		{Name: "wander", Type: procgen.ParamFloat, Label: "Wander (m)", Default: f(d.Wander), Min: 0, Max: 50, Step: 0.1,
			Doc: "How far the cave wanders off the drawn path (never at the mouths)."},
		{Name: "rough", Type: procgen.ParamFloat, Label: "Roughness (m)", Default: f(d.Rough), Min: 0, Max: 4, Step: 0.01,
			Doc: "Lumps and hollows in the walls and the roof."},
		{Name: "ledges", Type: procgen.ParamFloat, Label: "Ledges (m)", Default: f(d.Ledges), Min: 0, Max: 4, Step: 0.01,
			Doc: "How far the soft beds weather back between the hard ones: ledges along the walls, steps in the roof."},
		{Name: "bed_spacing", Type: procgen.ParamFloat, Label: "Bed spacing (m)", Default: f(d.BedSpacing), Min: 0.2, Max: 20, Step: 0.05,
			Doc: "The beds' typical thickness."},
		{Name: "drapery", Type: procgen.ParamFloat, Default: f(d.Drapery), Min: 0, Max: 1, Step: 0.01,
			Doc: "The share of the roof hung with rock drapery: thin fins and curtains."},
		{Name: "drapery_length", Type: procgen.ParamFloat, Label: "Drapery length (m)", Default: f(d.DraperyLength), Min: 0, Max: 30, Step: 0.1,
			Doc: "How far the longest drapery hangs (at most half the cave's height there)."},
		{Name: "drapery_spacing", Type: procgen.ParamFloat, Label: "Drapery spacing (m)", Default: f(d.DraperySpacing), Min: 0.4, Max: 20, Step: 0.05,
			Doc: "The fins' typical spacing."},
		{Name: "blocks", Type: procgen.ParamFloat, Label: "Fallen blocks", Default: f(d.Blocks), Min: 0, Max: 1, Step: 0.01,
			Doc: "How thickly the floor is strewn with angular blocks fallen from the roof, half buried in the sand."},
		{Name: "block_size", Type: procgen.ParamFloat, Label: "Block size (m)", Default: f(d.BlockSize), Min: 0.3, Max: 10, Step: 0.05,
			Doc: "The big blocks' typical size; a second lattice of rubble about a third of it fills between."},
		{Name: "clear", Type: procgen.ParamFloat, Label: "Clear lane", Default: f(d.Clear), Min: 0, Max: 1, Step: 0.01,
			Doc: "How much clearer of blocks and rocks the middle of the floor is: 1 keeps a lane to walk (its rubble low " +
				"enough to step over, its rocks small and without collision)."},
		{Name: "sand", Type: procgen.ParamFloat, Label: "Sand ripple (m)", Default: f(d.Sand), Min: 0, Max: 1, Step: 0.005,
			Doc: "The sand floor's undulation."},
		{Name: "cell", Type: procgen.ParamFloat, Label: "Cell (m)", Default: f(0.3), Min: 0.1, Max: 2, Step: 0.05,
			Doc: "The mesher's grid: the finest feature the rock has. Halving it is four times the triangles."},
		{Name: "weathering", Type: procgen.ParamInt, Default: i(1), Min: 0, Max: sdf.MaxWeathering,
			Doc: "Steps of curvature flow that round the rock's convex edges."},
		{Name: "cut_margin", Type: procgen.ParamFloat, Label: "Cut margin (m)", Default: f(0.3), Min: 0, Max: 4, Step: 0.05,
			Doc: "A terrain quad this close to the void is cut out of its tile."},
		{Name: "lip", Type: procgen.ParamFloat, Label: "Mouth lip (m)", Default: f(0.4), Min: 0.05, Max: 4, Step: 0.05,
			Doc: "How far the rock stands over the terrain round a hole, covering the cut tiles' ragged edge."},
		{Name: "lip_band", Type: procgen.ParamFloat, Label: "Lip band (m)", Default: f(2.2), Min: 0.5, Max: 20, Step: 0.1,
			Doc: "How far round a hole the lip reaches before the rock sinks back under the terrain."},
		{Name: "sky_cell", Type: procgen.ParamFloat, Label: "Sky cell (m)", Default: f(1), Min: 0.25, Max: 8, Step: 0.05,
			Doc: "The sky-visibility bake's grid."},
		{Name: "sky_reach", Type: procgen.ParamFloat, Label: "Sky reach (m)", Default: f(24), Min: 2, Max: 200, Step: 1,
			Doc: "How far round the cave the sky bake's grid reaches (past it, rays test the open terrain)."},
		{Name: "darkness", Type: procgen.ParamFloat, Default: f(0.95), Min: 0, Max: 1, Step: 0.01,
			Doc: "How much of the ambient light the deep interior loses (1: all of it, where no sky is seen)."},
		{Name: "sky_gain", Type: procgen.ParamFloat, Label: "Sky gain", Default: f(2.5), Min: 0.5, Max: 20, Step: 0.1,
			Doc: "Scales the sky's visibility before it darkens: higher keeps more light in round a mouth."},
		{Name: "sand_color", Type: procgen.ParamColor, Label: "Sand colour", Default: procgen.Color(0, 0, 0), Min: 0, Max: 1,
			Doc: "The colour the walls' feet blend into on the cave floor; black takes the ground material's own sand."},
		{Name: "ground_band", Type: procgen.ParamFloat, Label: "Ground blend (m)", Default: f(0.7), Min: 0, Max: 10, Step: 0.05,
			Doc: "How far up the walls and blocks the floor's sand colour reaches."},
		{Name: "rocks", Type: procgen.ParamStage, StageKinds: RockSources, Default: procgen.StageRef(""),
			Doc: "A rocks or polyhaven_rocks stage whose variants are strewn on the cave floor. Empty: none."},
		{Name: "rock_density", Type: procgen.ParamFloat, Label: "Rocks per m²", Default: f(0.12), Min: 0, Max: 5, Step: 0.005,
			Doc: "How many rocks per square metre of floor (fewer in the clear lane)."},
		{Name: "rock_size", Type: procgen.ParamVec2, Label: "Rock size (m)", Default: v2(0.25, 1.4), Min: 0.02, Max: 20, Step: 0.01,
			Doc: "The rocks' sizes, smallest to largest (smaller ones far more often)."},
		{Name: "draw_distance", Type: procgen.ParamFloat, Label: "Draw distance (m)", Default: f(700), Min: 0, Max: 100000, Step: 10,
			Doc: "How far the cave's sections are drawn (0: no limit). The rocks get a fifth of it."},
		{Name: "shadow_distance", Type: procgen.ParamFloat, Label: "Shadow distance (m)", Default: f(250), Min: 0, Max: 100000, Step: 10,
			Doc: "How far the sections cast shadows (0: as drawn). The rocks get a fifth of it."},
		{Name: "cook_memory", Type: procgen.ParamInt, Label: "Cook memory (MB)", Default: i(3000), Min: 100, Max: 64000,
			Doc: "How much memory the sections' meshing and cooks, and the tiles' cooks, may hold at once."},
		{Name: "preview_cell", Type: procgen.ParamFloat, Label: "Preview cell (m)", Default: f(1), Min: 0.25, Max: 8, Step: 0.05,
			PreviewOnly: true, Doc: "The 3D preview's mesh grid."},
		// Ambient volumes on the interior (Added: off places none, so no
		// existing cave's hash or output moves).
		{Name: "chunk", Added: true, Type: procgen.ParamString, Label: "Chunk", Default: procgen.String(""),
			Doc: "The level chunk the cave's sections and rocks stream with (an ID), except those within Chunk mouth reach of a " +
				"mouth, which stream by their place like every other placement (so a mouth seen from outside stays when the " +
				"interior is switched off). Empty: every placement by its place."},
		{Name: "chunk_mouth", Added: true, Type: procgen.ParamFloat, Label: "Chunk mouth reach (m)", Default: f(28), Min: 0, Max: 500, Step: 1,
			Doc: "How far from a mouth placements keep streaming by their place rather than with Chunk."},
		{Name: "ambient", Added: true, Type: procgen.ParamBool, Label: "Ambient volumes", Default: procgen.Bool(false),
			Doc: "Place an ambient-darkening volume (a volume's ambient: block) over each interior box, grown to enclose the rock, " +
				"so everything in the cave — the player, scanned rocks, entities — loses the sky's ambient light as the walls do. " +
				"Each box keeps more of it the more sky its samples see."},
		{Name: "ambient_sky", Added: true, Type: procgen.ParamFloat, Label: "Ambient sky", Default: f(0.1), Min: 0, Max: 1, Step: 0.01,
			Doc: "The share of the sky ambient left deep inside, on upward faces."},
		{Name: "ambient_ground", Added: true, Type: procgen.ParamFloat, Label: "Ambient ground", Default: f(0.15), Min: 0, Max: 1, Step: 0.01,
			Doc: "The share left on downward faces."},
		{Name: "ambient_fade", Added: true, Type: procgen.ParamFloat, Label: "Ambient fade (m)", Default: f(3), Min: 0, Max: 50, Step: 0.1,
			Doc: "How far inside a volume's faces the darkening eases in."},
		{Name: "ambient_margin", Added: true, Type: procgen.ParamFloat, Label: "Ambient margin (m)", Default: f(4), Min: 0, Max: 50, Step: 0.1,
			Doc: "How far each interior box is grown, so the walls and roof lie inside it past the fade."},
		{Name: "ambient_dust", Added: true, Type: procgen.ParamFloat, Label: "Ambient dust", Default: f(0.02), Min: 0, Max: 1, Step: 0.001,
			Doc: "Extra dust for the light shafts inside (the fog's shafts: block)."},
	}, cavePileParams()...)
}

func validateCave(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	var out []string
	if p.Str("terrain") == "" {
		out = append(out, "choose a terrain stage")
	}
	if p.Str("ground") == "" {
		out = append(out, "choose a ground (terrain_material) stage")
	}
	if n := len(p.Points("path")); n < 2 {
		out = append(out, "draw the cave's path: at least two points")
	}
	if r := p.Vec2("rock_size"); r[1] < r[0] {
		out = append(out, "the rock sizes must rise")
	}
	return out
}

// caveSettings is the stage's cave.Settings.
func caveSettings(env *procgen.Env) cave.Settings {
	p := env.Params
	s := cave.Settings{Arch: p.Float("arch"), Wander: p.Float("wander"), Rough: p.Float("rough"), Ledges: p.Float("ledges"),
		BedSpacing: p.Float("bed_spacing"), Drapery: p.Float("drapery"), DraperyLength: p.Float("drapery_length"),
		DraperySpacing: p.Float("drapery_spacing"), Blocks: p.Float("blocks"), BlockSize: p.Float("block_size"),
		Clear: p.Float("clear"), Sand: p.Float("sand"), Seed: env.Seed}
	for _, q := range p.Points("path") {
		s.Path = append(s.Path, cave.Point{X: q[0], Y: q[1], Z: q[2], Width: q[3], Height: q[4]})
	}
	return s
}

// ---------- the artifact and cave.yaml ----------

// CaveFile is the stage's manifest.
const CaveFile = "cave.yaml"

// CaveArtifact is a cave stage's product: its settings (from which Void
// rebuilds the void exactly), and what later phases place by — the mouths,
// the interior's boxes, and sample points with how much sky each sees.
type CaveArtifact struct {
	Version  int            `yaml:"version"`
	Settings cave.Settings  `yaml:"settings"`
	Bounds   [2][3]float64  `yaml:"bounds,flow"`
	Mouths   []CaveMouth    `yaml:"mouths,omitempty"`
	Interior []CaveBox      `yaml:"interior,omitempty"`
	Samples  []CaveSample   `yaml:"samples,omitempty"`
	Floor    *CaveFloorMeta `yaml:"floor,omitempty"`
	// Holes is how many terrain quads were cut; Tiles the recooked tiles.
	Holes int      `yaml:"holes"`
	Tiles []string `yaml:"tiles,omitempty"`

	void *cave.Void
}

// CaveMouth is where the path comes out of the ground: its floor point and
// the direction out of the cave (x, z).
type CaveMouth struct {
	Position [3]float64 `yaml:"position,flow"`
	Out      [2]float64 `yaml:"out,flow"`
	Width    float64    `yaml:"width"`
	Height   float64    `yaml:"height"`
}

// CaveBox is an axis-aligned box of the interior (a run of the path under
// the ground): where an ambient-darkening volume goes.
type CaveBox struct {
	Min [3]float64 `yaml:"min,flow"`
	Max [3]float64 `yaml:"max,flow"`
}

// CaveSample is a point on the path's centreline, a third of the height up,
// with the sky's visibility there and whether it is under the ground: for
// lights and ambient.
type CaveSample struct {
	Position [3]float64 `yaml:"position,flow"`
	Sky      float64    `yaml:"sky"`
	Under    bool       `yaml:"under"`
}

// CaveFloorMeta is the floor height and colour maps the rock's ground
// blend reads.
type CaveFloorMeta struct {
	Height   string     `yaml:"height"`
	Color    string     `yaml:"color"`
	Min      [2]float64 `yaml:"min,flow"`
	Max      [2]float64 `yaml:"max,flow"`
	HeightLo float32    `yaml:"height_lo"`
	HeightHi float32    `yaml:"height_hi"`
}

// Void is the cave's void, compiled on first use, without its terrain
// (VoidOn adds it).
func (a *CaveArtifact) Void() *cave.Void {
	if a.void == nil {
		a.void = cave.New(a.Settings)
	}
	return a.void
}

// VoidOn is the cave's void as its run cut it, on the terrain g: its floor
// lifted to the ground where the ground lies just over it.
func (a *CaveArtifact) VoidOn(g *terrain.Grid) *cave.Void {
	v := cave.New(a.Settings)
	v.Ground = groundOf(g)
	return v
}

// groundOf is the void's ground on g (cave.GroundBelow, 1.5 m).
func groundOf(g *terrain.Grid) func(x, z float64) float64 { return cave.GroundBelow(g, 1.5) }

func loadCave(res *procgen.StageResult) (any, error) {
	data, err := os.ReadFile(filepath.Join(res.Dir, CaveFile))
	if err != nil {
		return nil, err
	}
	var a CaveArtifact
	if err := yaml.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("%s: %w", CaveFile, err)
	}
	return &a, nil
}

// ---------- the run ----------

func runCave(rc *procgen.RunContext) error {
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
	if t.Meta.Tiles.Step == 0 || t.Meta.Tiles.Count == 0 {
		return fmt.Errorf("the terrain stage %s does not cook its tiles", tid)
	}
	ground, err := groundArtifact(rc, p.Str("ground"))
	if err != nil {
		return err
	}
	if ground == nil {
		return fmt.Errorf("choose a ground stage")
	}
	if ground.Weights2 != "" || ground.Palette > 0 {
		return fmt.Errorf("the ground stage %s is a region material; the cave wears a four-layer terrain_material", p.Str("ground"))
	}
	g := t.Grid
	cs := caveSettings(&rc.Env)
	v := cave.New(cs)
	v.Ground = groundOf(g)
	if v.Empty() {
		return fmt.Errorf("the cave's path needs at least two points")
	}
	rc.Logf("void compiled in %s (box %.0f×%.0f×%.0f m)", time.Since(t0).Round(time.Millisecond),
		v.Box().Size()[0], v.Box().Size()[1], v.Box().Size()[2])

	// 1. The cut, and the tiles.
	ts := time.Now()
	cut, holes, err := cave.Cut(v, g, p.Float("cut_margin"), &rc.Env)
	if err != nil {
		return err
	}
	rock := cave.NewRock(v, g, nil)
	rock.Lip, rock.Band = p.Float("lip"), p.Float("lip_band")
	if holes > 0 {
		rock.Hole = cave.HoleDistance(g, cut, rock.Band+2*g.Cell)
	}
	tiles, err := caveTiles(rc, t, v, cut)
	if err != nil {
		return err
	}
	rc.Logf("%d terrain quads cut, %d tiles recooked, in %s", holes, len(tiles), time.Since(ts).Round(time.Millisecond))
	rc.Progress(0.15)

	// 2. The sky.
	ts = time.Now()
	sky, err := cave.BakeSky(v, g, p.Float("sky_cell"), p.Float("sky_reach"), &rc.Env)
	if err != nil {
		return err
	}
	rc.Logf("sky baked on %d×%d×%d voxels in %s", sky.NX, sky.NY, sky.NZ, time.Since(ts).Round(time.Millisecond))
	rc.Progress(0.25)

	// 3. Materials.
	floorMeta, err := caveFloorMaps(rc, v, rock, ground)
	if err != nil {
		return err
	}
	rockMat, sandMat, err := caveMaterials(rc, ground, floorMeta)
	if err != nil {
		return err
	}
	// The ground round a mouth wears the terrain tiles' material: the
	// ground stage's own (the map's), never its far backdrop's — the first
	// terrain placement with a material was a far ring tile, whose layers
	// repeat six times wider, and the cliff round a mouth was smeared close
	// up. Version 3.
	groundMat := ground.Material
	if up := rc.Upstream(tid); groundMat == "" && up != nil {
		for _, pl := range up.Placements {
			if pl.Material != "" && pl.Material != ground.FarMaterial {
				groundMat = pl.Material
				break
			}
		}
	}

	// The manifest (its mouths say which placements stream with the
	// level's chunks, and which stay round a mouth).
	a := caveManifest(v, g, sky, cs)
	chunkAt := caveChunker(p.Str("chunk"), p.Float("chunk_mouth"), a.Mouths)

	// 4. The sections.
	if err := caveSections(rc, v, rock, sky, rockMat, sandMat, groundMat, chunkAt); err != nil {
		return err
	}

	// 5. Rocks on the floor.
	if id := p.Str("rocks"); id != "" {
		if err := caveRocks(rc, v, sky, id, chunkAt); err != nil {
			return err
		}
	}

	// 5b. Piles of broken rock in the chambers (cave_pile.go).
	if err := cavePiles(rc, v, sky, chunkAt); err != nil {
		return err
	}

	// 6. The manifest.
	a.Holes, a.Tiles, a.Floor = holes, tiles, floorMeta
	data, err := yaml.Marshal(a)
	if err != nil {
		return err
	}
	if err := rc.WriteFile(CaveFile, data); err != nil {
		return err
	}
	a.void = v
	if p.Bool("ambient") {
		rc.PlaceVolume(caveAmbientVolumes(a, p)...)
	}
	rc.Artifact = a
	rc.Logf("%d mouths, %d interior boxes; done in %s", len(a.Mouths), len(a.Interior), time.Since(t0).Round(time.Millisecond))
	rc.Progress(1)
	return nil
}

// caveTiles recooks the terrain's tiles the cave touches: holes where the
// void comes through, no skirts. It returns their names.
func caveTiles(rc *procgen.RunContext, t *terrain.Terrain, v *cave.Void, cut []bool) ([]string, error) {
	tid := rc.Params.Str("terrain")
	// A ground_edit's tiles are planned as its source terrain's.
	st := terrainSourceStage(rc.Doc, tid)
	k := procgen.KindByName(TerrainKind)
	if st == nil || k == nil {
		return nil, fmt.Errorf("no terrain stage %q", tid)
	}
	tparams := procgen.Resolve(k.Params, st.Params)
	g := t.Grid
	tp := planTiles(tparams, g)
	tp.quant, tp.base = t.Meta.Tiles.Step, t.Meta.Tiles.Base
	n := g.N
	// A tile is touched where it has a hole, or the cave runs under it (its
	// skirts would stand across the tunnel, and a closed tile would be one
	// solid filling it for navigation).
	var touched [][2]int
	for tj := 0; tj < tp.count; tj++ {
		for ti := 0; ti < tp.count; ti++ {
			hit := false
			for j := tj * tp.per; j < min((tj+1)*tp.per, n) && !hit; j++ {
				for i := ti * tp.per; i < min((ti+1)*tp.per, n); i++ {
					if cut[j*n+i] {
						hit = true
						break
					}
				}
			}
			for j := tj * tp.per; j <= min((tj+1)*tp.per, n) && !hit; j += 2 {
				for i := ti * tp.per; i <= min((ti+1)*tp.per, n); i += 2 {
					x, z := g.X(i), g.Z(j)
					if v.PlanDistance(x, z) > 1 {
						continue
					}
					if fl, _, ok := v.FloorAt(x, float64(g.At(i, j)), z); ok && fl < float64(g.At(i, j)) {
						hit = true
						break
					}
				}
			}
			if hit {
				touched = append(touched, [2]int{ti, tj})
			}
		}
	}
	up := rc.Upstream(tid)
	byID := map[string]procgen.Placement{}
	if up != nil {
		for _, pl := range up.Placements {
			byID[pl.ID] = pl
		}
	}
	collision := tparams.Str("collision")
	tris := tileTriangles(tp)
	conc := cookConcurrency(tris, rc.Params.Int("cook_memory"), rc.Workers, true)
	sem := make(chan struct{}, conc)
	uv := float32(tparams.Float("uv_scale"))
	tex := tparams.Str("texture")
	names := make([]string, len(touched))
	err := rc.Parallel(len(touched), func(m int) error {
		sem <- struct{}{}
		defer func() { <-sem }()
		if err := rc.Err(); err != nil {
			return err
		}
		ti, tj := touched[m][0], touched[m][1]
		hg, centre := tileGridAt(g, tp, ti, tj, uv, tparams.Str("uv_mode") == "terrain")
		hg.Closed = false
		step := tp.step
		hg.Skip = func(i, j int) bool {
			for dj := 0; dj < step; dj++ {
				for di := 0; di < step; di++ {
					gi, gj := ti*tp.per+i*step+di, tj*tp.per+j*step+dj
					if gi < n && gj < n && cut[gj*n+gi] {
						return true
					}
				}
			}
			return false
		}
		var b meshout.Builder
		b.Slot("ground", tex)
		hg.Build(&b)
		name := tileName(ti, tj)
		rect := hg.Rect()
		if _, err := meshout.Cook(compactImport(b.Mesh()), rc.Path(name+".fnm"), meshout.CookOptions{LockRect: &rect, PositionStep: tp.quant,
			CollisionMesh: collision == "mesh" || collision == "auto"}); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		rc.Supersede(tid, name)
		pl := procgen.Placement{ID: name, Model: rc.AssetPath(name + ".fnm"), Position: centre, Collision: collision,
			Chunk: procgen.ChunkAuto, Material: tparams.Str("material")}
		if old, ok := byID[name]; ok {
			pl.Material, pl.Chunk, pl.Collision = old.Material, old.Chunk, old.Collision
			pl.DrawDistance, pl.ShadowDistance = old.DrawDistance, old.ShadowDistance
		}
		rc.Place(pl)
		names[m] = name
		return nil
	})
	sort.Strings(names)
	return names, err
}

// caveSharpness is the mesher's: sharper than a wall sheet's, so the fallen
// blocks' fractured edges and the ledges' lips stay angular.
const caveSharpness = 0.6

// compactImport drops the vertices no triangle uses (a holed tile's
// samples inside its holes): the cook must not see them, or its coarser
// levels can reach across a hole to them.
func compactImport(m *model.ImportMesh) *model.ImportMesh {
	remap := make([]int32, len(m.Positions))
	for i := range remap {
		remap[i] = -1
	}
	n := int32(0)
	for _, ix := range m.Indices {
		if remap[ix] < 0 {
			remap[ix] = n
			n++
		}
	}
	out := &model.ImportMesh{Slots: m.Slots, TriSlot: m.TriSlot, Files: m.Files,
		Positions: make([]mgl32.Vec3, n), Normals: make([]mgl32.Vec3, n), UVs: make([]mgl32.Vec2, n)}
	if m.Colors != nil {
		out.Colors = make([][4]uint8, n)
	}
	for i, r := range remap {
		if r < 0 {
			continue
		}
		out.Positions[r], out.Normals[r] = m.Positions[i], m.Normals[i]
		if i < len(m.UVs) {
			out.UVs[r] = m.UVs[i]
		}
		if m.Colors != nil {
			out.Colors[r] = m.Colors[i]
		}
	}
	out.Indices = make([]uint32, len(m.Indices))
	for k, ix := range m.Indices {
		out.Indices[k] = uint32(remap[ix])
	}
	return out
}

// caveSection is one box of the rock to mesh.
type caveSection struct {
	I, J, K int
	Lo, Hi  sdf.Vec3
}

// caveSectionSize is a section's plan side (m).
const caveSectionSize = 32

// sectionsOf cuts the cave's rock into world-aligned sections: plan squares
// over the void and the holes' lips, each cut into slabs of at most 480
// cells in height.
func sectionsOf(v *cave.Void, r *cave.Rock, cell float64) []caveSection {
	box := v.Box()
	sec := float64(caveSectionSize)
	i0, i1 := int(math.Floor(box.Lo[0]/sec)), int(math.Floor(box.Hi[0]/sec))
	j0, j1 := int(math.Floor(box.Lo[2]/sec)), int(math.Floor(box.Hi[2]/sec))
	type rng struct{ lo, hi float64 }
	ranges := map[[2]int]*rng{}
	grow := func(si, sj int, lo, hi float64) {
		k := [2]int{si, sj}
		if q := ranges[k]; q != nil {
			q.lo, q.hi = math.Min(q.lo, lo), math.Max(q.hi, hi)
			return
		}
		ranges[k] = &rng{lo, hi}
	}
	// The void: each dense point's cross-section, out to its reach.
	reach := 2.5
	for _, d := range v.Dense() {
		x, y, z, w, h := d[0], d[1], d[2], d[3], d[4]
		rr := w/2 + reach + 1
		for sj := int(math.Floor((z - rr) / sec)); sj <= int(math.Floor((z+rr)/sec)); sj++ {
			for si := int(math.Floor((x - rr) / sec)); si <= int(math.Floor((x+rr)/sec)); si++ {
				grow(si, sj, y-2, y+h*1.35+reach)
			}
		}
	}
	// The holes' lips: the ground round each, a little over.
	g := r.G
	if r.Hole != nil {
		sz := g.S()
		for j := 0; j < sz; j++ {
			for i := 0; i < sz; i++ {
				if float64(r.Hole[j*sz+i]) > r.Band+cell {
					continue
				}
				x, z := g.X(i), g.Z(j)
				h := float64(g.H[j*sz+i])
				grow(int(math.Floor(x/sec)), int(math.Floor(z/sec)), h-1.5, h+r.Lip+1)
			}
		}
	}
	_ = i0
	_ = i1
	_ = j0
	_ = j1
	var out []caveSection
	maxH := 480 * cell
	keys := make([][2]int, 0, len(ranges))
	for k := range ranges {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a][1] != keys[b][1] {
			return keys[a][1] < keys[b][1]
		}
		return keys[a][0] < keys[b][0]
	})
	for _, k := range keys {
		q := ranges[k]
		lo := math.Floor(q.lo/cell) * cell
		for kk := 0; lo < q.hi; kk++ {
			hi := math.Min(lo+maxH, q.hi)
			out = append(out, caveSection{I: k[0], J: k[1], K: kk,
				Lo: sdf.Vec3{float64(k[0]) * sec, lo, float64(k[1]) * sec},
				Hi: sdf.Vec3{float64(k[0]+1) * sec, hi, float64(k[1]+1) * sec}})
			lo = hi
		}
	}
	return out
}

// caveSections meshes, bakes, cooks and places every section.
func caveSections(rc *procgen.RunContext, v *cave.Void, r *cave.Rock, sky *cave.Sky, rockMat, sandMat, groundMat string, chunkAt func(x, y, z float64) string) error {
	p := rc.Params
	cell := p.Float("cell")
	secs := sectionsOf(v, r, cell)
	lip := r.LipBound()
	field := sdf.Shape{F: r.F, Lip: lip}
	// One quantisation step for every section, from the tallest.
	ext := float64(caveSectionSize)
	for _, s := range secs {
		ext = math.Max(ext, s.Hi[1]-s.Lo[1])
	}
	step := float32(math.Ldexp(1, int(math.Floor(math.Log2(ext)))-18))
	estTris := int(2 * caveSectionSize * 40 / (cell * cell))
	conc := max(1, min(int(float64(p.Int("cook_memory"))*(1<<20)/(float64(estTris)*sectionBytesPerTriangle)), max(rc.Workers, 1)))
	if rc.Workers <= 0 {
		conc = max(1, min(conc, 4))
	}
	rc.Logf("%d sections of %d m, cell %g m, %d at a time", len(secs), caveSectionSize, cell, conc)
	sem := make(chan struct{}, conc)
	var done, tris, rawTris atomic.Int64
	var tMesh, tSimp, tBake, tCook atomic.Int64
	draw, shadow := float32(p.Float("draw_distance")), float32(p.Float("shadow_distance"))
	darkness, gain := p.Float("darkness"), p.Float("sky_gain")
	err := rc.Parallel(len(secs), func(n int) error {
		sem <- struct{}{}
		defer func() { <-sem }()
		if err := rc.Err(); err != nil {
			return err
		}
		sec := secs[n]
		// The box overlaps its neighbours' by half a cell, so its faces fall
		// between lattice planes: a lattice point on a shared face would be
		// outside both boxes, and the cell layer either side of it — where a
		// wall crosses from one section into the next — meshed by neither.
		sec.Lo = sec.Lo.Sub(sdf.Vec3{cell / 2, cell / 2, cell / 2})
		sec.Hi = sec.Hi.Add(sdf.Vec3{cell / 2, cell / 2, cell / 2})
		lo, hi := sec.Lo, sec.Hi
		snap := func(x float64) float64 { return math.Floor(x/cell) * cell }
		shape := sdf.Shape{
			F: func(q sdf.Vec3) float64 {
				b := math.Max(math.Max(lo[0]-q[0], q[0]-hi[0]), math.Max(math.Max(lo[1]-q[1], q[1]-hi[1]), math.Max(lo[2]-q[2], q[2]-hi[2])))
				if b > 1 {
					return b
				}
				return math.Max(r.F(q), b)
			},
			Box: sdf.Box{Lo: sdf.Vec3{snap(lo[0]), snap(lo[1]), snap(lo[2])}, Hi: hi},
			Lip: lip,
		}
		ts := time.Now()
		m, _, err := sdf.Polygonize(shape, sdf.MeshOptions{Cell: cell, Weathering: p.Int("weathering"), Sharpness: caveSharpness, Workers: 1, Ctx: rc.Ctx})
		tMesh.Add(int64(time.Since(ts)))
		if err != nil {
			return fmt.Errorf("section %d,%d,%d: %w", sec.I, sec.J, sec.K, err)
		}
		raw := m.TriangleCount()
		rawTris.Add(int64(raw))
		ts = time.Now()
		dropFloatingCave(m, sec, cell)
		dropHiddenCave(v, r, m, sec, cell)
		if m.TriangleCount() == 0 {
			rc.Progress(0.3 + 0.6*float32(done.Add(1))/float32(len(secs)))
			return nil
		}
		simplifyBox(m, sec.Lo, sec.Hi, cell, caveSimplifyError)
		tSimp.Add(int64(time.Since(ts)))
		ts = time.Now()
		parts := caveImport(v, r, field, sky, m, sec, cell, darkness, gain)
		tBake.Add(int64(time.Since(ts)))
		ts = time.Now()
		for _, part := range parts {
			if part.mesh == nil || part.mesh.TriangleCount() == 0 {
				continue
			}
			name := fmt.Sprintf("%s_%d_%d_%d", part.name, sec.I, sec.J, sec.K)
			if _, err := meshout.Cook(part.mesh, rc.Path(name+".fnm"), meshout.CookOptions{PositionStep: step, CollisionMesh: true,
				AttrRadiusCap: 1}); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			mat := rockMat
			switch part.name {
			case "sand":
				mat = sandMat
			case "ground":
				mat = groundMat
			}
			c := part.centre
			rc.Place(procgen.Placement{ID: name, Model: rc.AssetPath(name + ".fnm"),
				Position:  procgen.Point3{X: float32(c[0]), Y: float32(c[1]), Z: float32(c[2])},
				Collision: "mesh", Chunk: chunkAt(c[0], c[1], c[2]), Material: mat, DrawDistance: draw, ShadowDistance: shadow})
			tris.Add(int64(part.mesh.TriangleCount()))
		}
		tCook.Add(int64(time.Since(ts)))
		rc.Progress(0.3 + 0.6*float32(done.Add(1))/float32(len(secs)))
		return nil
	})
	if err != nil {
		return err
	}
	sum := func(a *atomic.Int64) string { return time.Duration(a.Load()).Round(time.Second).String() }
	rc.Logf("%.2f M triangles kept (%.2f M meshed): mesh %s, simplify %s, bake %s, cook %s (summed)",
		float64(tris.Load())/1e6, float64(rawTris.Load())/1e6, sum(&tMesh), sum(&tSimp), sum(&tBake), sum(&tCook))
	return nil
}

// onBoxFace is which face of the section's box p lies on (within tol): 0
// none, 1..6 −x +x −y +y −z +z.
func onBoxFace(p, lo, hi sdf.Vec3, tol float64) int {
	for a := 0; a < 3; a++ {
		if math.Abs(p[a]-lo[a]) < tol {
			return 1 + 2*a
		}
		if math.Abs(p[a]-hi[a]) < tol {
			return 2 + 2*a
		}
	}
	return 0
}

// dropFloatingCave drops every connected piece of a section's raw mesh
// that touches none of the section box's faces. The raw mesh is the rock
// clipped to the box, so every piece of rock that reaches past the box
// (every wall, roof and floor, and everything joined to them) touches a box
// face; a piece that does not is closed inside the box with nothing holding
// it — rock drapery left hanging in the air where a mouth's roof opens to
// the sky (the fin's foot is under the ground's surface, its roof is not),
// a sliver of strata cut off by the void, a bubble of air sealed in the
// rock. None of those is anything but a floating fragment or invisible.
// Version 3.
func dropFloatingCave(m *sdf.Mesh, sec caveSection, cell float64) int {
	nv := len(m.Positions)
	if nv == 0 {
		return 0
	}
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
	anchored := map[int32]bool{}
	for i, q := range m.Positions {
		if onBoxFace(q, sec.Lo, sec.Hi, cell) != 0 {
			anchored[find(int32(i))] = true
		}
	}
	dropped := map[int32]bool{}
	out := m.Indices[:0]
	for t := 0; t+2 < len(m.Indices); t += 3 {
		root := find(int32(m.Indices[t]))
		if !anchored[root] {
			dropped[root] = true
			continue
		}
		out = append(out, m.Indices[t], m.Indices[t+1], m.Indices[t+2])
	}
	m.Indices = out
	return len(dropped)
}

// dropHiddenCave removes what nobody sees: the section box's own faces
// through the rock, and the triangles whose every vertex is in the rock away from the void and from
// the holes' lips (the box's cut through solid rock, the ground's top hidden
// under the terrain's own tiles).
func dropHiddenCave(v *cave.Void, r *cave.Rock, m *sdf.Mesh, sec caveSection, cell float64) {
	keep := make([]bool, len(m.Positions))
	// boxed: a vertex the box made, inside the unbounded rock.
	boxed := make([]bool, len(m.Positions))
	for i, q := range m.Positions {
		boxed[i] = onBoxFace(q, sec.Lo, sec.Hi, 0.6*cell) != 0 && r.F(q) < -0.7*cell
		if v.At(q) < 2.5*cell {
			keep[i] = true
			continue
		}
		if r.HoleDist(q[0], q[2]) < r.Band+cell && r.Ground(q) > -(r.Hidden+3*cell) {
			keep[i] = true
		}
	}
	out := m.Indices[:0]
	for t := 0; t+2 < len(m.Indices); t += 3 {
		a, b, c := m.Indices[t], m.Indices[t+1], m.Indices[t+2]
		if !keep[a] && !keep[b] && !keep[c] {
			continue
		}
		// The box's own faces: where the box cuts through the rock, the
		// unbounded field is inside it at every corner. (Not by position
		// alone: a real wall can run along a box face a few centimetres in,
		// and the cells where a wall meets the box hold one vertex for both.)
		if boxed[a] && boxed[b] && boxed[c] {
			continue
		}
		out = append(out, a, b, c)
	}
	m.Indices = out
}

// caveSimplifyError is the most a section's simplification may move the
// rock (m): a centimetre, so the fractured block edges and ledge lips keep
// their shape.
const caveSimplifyError = 0.012

// simplifyBox decimates m within maxErr, its vertices on the box's sides
// locked, then compacts it (simplifyWall's method for any box).
func simplifyBox(m *sdf.Mesh, lo, hi sdf.Vec3, cell, maxErr float64) {
	const stride = 6
	verts := make([]float32, 0, len(m.Positions)*stride)
	for i, q := range m.Positions {
		n := m.Normals[i]
		verts = append(verts, float32(q[0]), float32(q[1]), float32(q[2]), float32(n[0]), float32(n[1]), float32(n[2]))
	}
	// Locked: the box's sides (the next section meets them) and every open
	// edge the drop left (collapsing one would pull the rock off the void's
	// wall and open a crack to the sky).
	border := make([]bool, len(m.Positions))
	type edge struct{ a, b uint32 }
	cnt := make(map[edge]int, len(m.Indices))
	for t := 0; t+2 < len(m.Indices); t += 3 {
		for e := 0; e < 3; e++ {
			a, b := m.Indices[t+e], m.Indices[t+(e+1)%3]
			if a > b {
				a, b = b, a
			}
			cnt[edge{a, b}]++
		}
	}
	for e, c := range cnt {
		if c == 1 {
			border[e.a], border[e.b] = true, true
		}
	}
	side := func(v uint32) bool { return border[v] || onBoxFace(m.Positions[v], lo, hi, 0.6*cell) != 0 }
	nw := float32(0.05)
	res := cluster.Simplify(verts, stride, m.Indices, cluster.SimplifyOptions{MaxError: float32(maxErr),
		AttrWeights: []float32{nw, nw, nw}, Locked: side})
	m.Indices = res.Indices
	m.Compact()
}

// cavePart is one of a section's three meshes.
type cavePart struct {
	name   string
	mesh   *model.ImportMesh
	centre sdf.Vec3
}

// caveImport bakes a section's mesh — normals from the field, vertex AO
// from the field and the sky — and splits it into the sand floor, the rock
// and the ground round a mouth.
func caveImport(v *cave.Void, r *cave.Rock, field sdf.Shape, sky *cave.Sky, m *sdf.Mesh, sec caveSection, cell, darkness, gain float64) []cavePart {
	nv := len(m.Positions)
	normals := make([]sdf.Vec3, nv)
	colors := make([][4]uint8, nv)
	nearVoid := make([]bool, nv)
	tint := noise.Fractal{Seed: noise.Hash64(v.S.Seed, 0x5449_4E54), Octaves: 2, Frequency: 1 / 6.0}
	for k, q := range m.Positions {
		n := m.Normals[k]
		boxed := onBoxFace(q, sec.Lo, sec.Hi, 0.3*cell) != 0 && r.F(q) < -0.25*cell
		if !boxed {
			n = field.Gradient(q, cell*0.5)
		}
		normals[k] = n
		vd := v.At(q)
		nearVoid[k] = vd < 2.5*cell
		occ := 1.0
		if !boxed {
			occ = field.AO(q, n, 2.0, 4)
		}
		s := sky.At(q.Add(n.Mul(0.6)))
		if s < 0 {
			if r.Ground(q) > -0.5 {
				s = 1
			} else {
				s = 0
			}
		}
		a := (0.25 + 0.75*occ) * skyShade(s, darkness, gain)
		tv := 0.93 + 0.06*tint.At3(q[0], q[1], q[2])
		for c := 0; c < 3; c++ {
			colors[k][c] = uint8(noise.Clamp(tv, 0, 1)*255 + 0.5)
		}
		colors[k][3] = uint8(noise.Clamp(a, 0, 1)*255 + 0.5)
	}
	// Classify the triangles.
	const (
		partRock = iota
		partSand
		partGround
	)
	cls := make([]int, m.TriangleCount())
	for t := range cls {
		a, b, c := m.Indices[3*t], m.Indices[3*t+1], m.Indices[3*t+2]
		if !nearVoid[a] && !nearVoid[b] && !nearVoid[c] {
			cls[t] = partGround
			continue
		}
		pa, pb, pc := m.Positions[a], m.Positions[b], m.Positions[c]
		fn := pb.Sub(pa).Cross(pc.Sub(pa)).Normalize()
		cen := pa.Add(pb).Add(pc).Mul(1.0 / 3)
		if fn[1] > 0.72 {
			if l, ok := v.Local(cen); ok && l.B < 0.3 && l.End <= 0 && r.Ground(cen) < 0 {
				cls[t] = partSand
				continue
			}
		}
		cls[t] = partRock
	}
	names := []string{"rock", "sand", "ground"}
	var out []cavePart
	for pi, name := range names {
		remap := map[uint32]uint32{}
		im := &model.ImportMesh{Slots: []model.ImportSlot{{Name: name, BaseColor: [4]float32{1, 1, 1, 1}}}}
		var idx []uint32
		for t, c := range cls {
			if c != pi {
				continue
			}
			for e := 0; e < 3; e++ {
				o := m.Indices[3*t+e]
				nidx, ok := remap[o]
				if !ok {
					nidx = uint32(len(remap))
					remap[o] = nidx
				}
				idx = append(idx, nidx)
			}
		}
		if len(idx) == 0 {
			continue
		}
		order := make([]uint32, len(remap))
		for o, nn := range remap {
			order[nn] = o
		}
		lo := sdf.Vec3{math.Inf(1), math.Inf(1), math.Inf(1)}
		hi := sdf.Vec3{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
		for _, o := range order {
			q := m.Positions[o]
			for a := 0; a < 3; a++ {
				lo[a], hi[a] = math.Min(lo[a], q[a]), math.Max(hi[a], q[a])
			}
		}
		centre := sdf.Vec3{math.Round((lo[0]+hi[0])/2*4) / 4, math.Round(lo[1]*4) / 4, math.Round((lo[2]+hi[2])/2*4) / 4}
		im.Positions = make([]mgl32.Vec3, len(order))
		im.Normals = make([]mgl32.Vec3, len(order))
		im.UVs = make([]mgl32.Vec2, len(order))
		im.Colors = make([][4]uint8, len(order))
		for nn, o := range order {
			q := m.Positions[o].Sub(centre)
			n := normals[o]
			im.Positions[nn] = mgl32.Vec3{float32(q[0]), float32(q[1]), float32(q[2])}
			im.Normals[nn] = mgl32.Vec3{float32(n[0]), float32(n[1]), float32(n[2])}
			im.Colors[nn] = colors[o]
		}
		im.Indices = idx
		im.TriSlot = make([]int32, len(idx)/3)
		out = append(out, cavePart{name: name, mesh: im, centre: centre})
	}
	return out
}

// ---------- the floor maps and the materials ----------

// caveFloorMaps writes the height and colour maps the rock's ground blend
// reads: the cave floor's height (plus a little) and the sand's colour over
// the cave, the terrain's height and ground colour round it.
func caveFloorMaps(rc *procgen.RunContext, v *cave.Void, r *cave.Rock, ground *TerrainMaterialArtifact) (*CaveFloorMeta, error) {
	g := r.G
	box := v.Box()
	const res = 0.5
	pad := 4.0
	minX, minZ := math.Floor((box.Lo[0]-pad)/res)*res, math.Floor((box.Lo[2]-pad)/res)*res
	w := min(int(math.Ceil((box.Hi[0]+pad-minX)/res)), 4096)
	h := min(int(math.Ceil((box.Hi[2]+pad-minZ)/res)), 4096)
	sz := g.S()
	gc, err := readRGBA(ground.Ground, sz)
	if err != nil {
		return nil, err
	}
	sand := rc.Params.Vec3("sand_color")
	if sand == ([3]float64{}) {
		if wt, err := readRGBA(ground.Weights, sz); err == nil {
			var acc [3]float64
			cnt := 0.0
			for k := 0; k < sz*sz; k++ {
				if wt[4*k] >= 242 {
					for c := 0; c < 3; c++ {
						acc[c] += float64(gc[4*k+c])
					}
					cnt++
				}
			}
			if cnt > 0 {
				sand = [3]float64{acc[0] / cnt / 255, acc[1] / cnt / 255, acc[2] / cnt / 255}
			}
		}
		if sand == ([3]float64{}) {
			sand = [3]float64{0.78, 0.62, 0.45}
		}
	}
	heights := make([]float64, w*h)
	cols := make([]uint8, 4*w*h)
	lo, hi := math.Inf(1), math.Inf(-1)
	err = rc.Parallel(h, func(j int) error {
		for i := 0; i < w; i++ {
			x, z := minX+(float64(i)+0.5)*res, minZ+(float64(j)+0.5)*res
			th := float64(g.Sample(x, z))
			k := j*w + i
			gi := min(max(int(math.Round((x-g.MinX)/g.Cell)), 0), sz-1)
			gj := min(max(int(math.Round((z-g.MinZ)/g.Cell)), 0), sz-1)
			c := [3]float64{float64(gc[4*(gj*sz+gi)]) / 255, float64(gc[4*(gj*sz+gi)+1]) / 255, float64(gc[4*(gj*sz+gi)+2]) / 255}
			hv := th
			if fl, _, ok := v.FloorAt(x, th, z); ok && fl < th-0.3 && v.PlanDistance(x, z) < 0.5 {
				hv = fl + 0.05
				c = sand
			}
			heights[k] = hv
			for ch := 0; ch < 3; ch++ {
				cols[4*k+ch] = uint8(noise.Clamp(c[ch], 0, 1)*255 + 0.5)
			}
			cols[4*k+3] = 255
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, hv := range heights {
		lo, hi = math.Min(lo, hv), math.Max(hi, hv)
	}
	lo, hi = math.Floor(lo)-1, math.Ceil(hi)+1
	pix := make([]uint8, 4*w*h)
	for k, hv := range heights {
		q := uint16(noise.Clamp((hv-lo)/(hi-lo), 0, 1)*65535 + 0.5)
		pix[4*k], pix[4*k+1], pix[4*k+2], pix[4*k+3] = uint8(q>>8), uint8(q), 0, 255
	}
	for name, data := range map[string][]uint8{"floor_height.png": pix, "floor_color.png": cols} {
		enc, err := terrain.EncodeRGBA(w, h, data)
		if err != nil {
			return nil, err
		}
		if err := rc.WriteFile(name, enc); err != nil {
			return nil, err
		}
	}
	return &CaveFloorMeta{Height: mapRef(rc, "floor_height.png"), Color: mapRef(rc, "floor_color.png"),
		Min: [2]float64{minX, minZ}, Max: [2]float64{minX + float64(w)*res, minZ + float64(h)*res},
		HeightLo: float32(lo), HeightHi: float32(hi)}, nil
}

// caveMaterials writes the rock's material (the ground's rock layer, its
// feet blended into the cave floor's sand) and the floor's (its sand layer).
func caveMaterials(rc *procgen.RunContext, ground *TerrainMaterialArtifact, fm *CaveFloorMeta) (rockMat, sandMat string, err error) {
	base := readMaterialDoc(ground.Material)
	if base == nil {
		return "", "", fmt.Errorf("the ground stage's material %s cannot be read", ground.Material)
	}
	const n = 4
	write := func(name string, ch int) error {
		pix := make([]uint8, 4*n*n)
		for k := 0; k < n*n; k++ {
			pix[4*k+ch] = 255
		}
		data, err := terrain.EncodeRGBA(n, n, pix)
		if err != nil {
			return err
		}
		return rc.WriteFile(name, data)
	}
	rock := rockLayerIndex(&rc.Env, ground)
	if err := write("rock_weights.png", rock); err != nil {
		return "", "", err
	}
	if err := write("sand_weights.png", 0); err != nil {
		return "", "", err
	}
	// The rock.
	d := base.Clone()
	d.Name = genMaterialName(rc, "rock")
	d.Doc = "Generated cave rock: the terrain material's rock layer, its feet in the cave floor's sand"
	if e := d.Find(material.KindSplat); e != nil {
		e.Set("weights", material.TextureValue(mapRef(rc, "rock_weights.png")))
		e.Set("bedding", material.FloatValue(0.05))
		e.Set("bedding_spacing", material.FloatValue(0.6))
	}
	if band := rc.Params.Float("ground_band"); band > 0 {
		e := d.AddEffect(material.KindGroundBlend)
		e.Set("ground", material.TextureValue(fm.Color))
		e.Set("height", material.TextureValue(fm.Height))
		e.Set("bounds_min", material.Vec2Value(mgl32.Vec2{float32(fm.Min[0]), float32(fm.Min[1])}))
		e.Set("bounds_max", material.Vec2Value(mgl32.Vec2{float32(fm.Max[0]), float32(fm.Max[1])}))
		e.Set("height_range", material.Vec2Value(mgl32.Vec2{fm.HeightLo, fm.HeightHi}))
		e.Set("band", material.FloatValue(float32(band)))
	}
	if err := writeMaterial(rc, d); err != nil {
		return "", "", err
	}
	// The sand floor.
	s := base.Clone()
	s.Name = genMaterialName(rc, "sand")
	s.Doc = "Generated cave floor: the terrain material's sand layer"
	if e := s.Find(material.KindSplat); e != nil {
		e.Set("weights", material.TextureValue(mapRef(rc, "sand_weights.png")))
	}
	if err := writeMaterial(rc, s); err != nil {
		return "", "", err
	}
	return d.Name, s.Name, nil
}

// ---------- rocks on the floor ----------

// caveRocks strews a rocks stage's variants over the cave floor.
func caveRocks(rc *procgen.RunContext, v *cave.Void, sky *cave.Sky, id string, chunkAt func(x, y, z float64) string) error {
	p := rc.Params
	ra, err := rc.UpstreamArtifact(id)
	if err != nil {
		return err
	}
	art, ok := ra.(*RocksArtifact)
	if !ok || art.Manifest == nil || len(art.Manifest.Variants) == 0 {
		return fmt.Errorf("stage %s has no rock variants", id)
	}
	dense := v.Dense()
	rng := noise.NewRNG(noise.Hash64(rc.Seed, 0x524F_434B))
	sizes := p.Vec2("rock_size")
	density := p.Float("rock_density")
	clear := p.Float("clear")
	draw, shadow := float32(p.Float("draw_distance")/5), float32(p.Float("shadow_distance")/5)
	darkness, gain := p.Float("darkness"), p.Float("sky_gain")
	// The shade materials, one per step of darkness.
	shade := caveRockShader(rc, art, "rock_shade")
	placed := 0
	for k := 0; k+1 < len(dense); k++ {
		a, b := dense[k], dense[k+1]
		segLen := math.Hypot(b[0]-a[0], b[2]-a[2])
		area := segLen * a[3]
		want := area * density
		cnt := int(want)
		if rng.Float64() < want-float64(cnt) {
			cnt++
		}
		for c := 0; c < cnt; c++ {
			t := rng.Float64()
			x, y, z := a[0]+(b[0]-a[0])*t, a[1]+(b[1]-a[1])*t, a[2]+(b[2]-a[2])*t
			w := a[3] + (b[3]-a[3])*t
			hh := a[4] + (b[4]-a[4])*t
			u := rng.Range(-0.92, 0.92)
			// The lane: fewer in the middle, and those small.
			inLane := math.Abs(u) < 0.45
			if inLane && rng.Float64() < clear*0.85 {
				continue
			}
			dx, dz := b[0]-a[0], b[2]-a[2]
			l := math.Hypot(dx, dz)
			if l < 1e-9 {
				continue
			}
			sx, sz := -dz/l, dx/l
			px, pz := x+sx*u*w/2, z+sz*u*w/2
			// Down from a third of the height to the floor (or a block).
			top := y + hh*0.35
			hit := math.NaN()
			for yy := top; yy > y-1.5; yy -= 0.05 {
				if v.At(sdf.Vec3{px, yy, pz}) >= 0 {
					hit = yy
					break
				}
			}
			if math.IsNaN(hit) || hit > y+hh*0.3 {
				continue
			}
			if l, ok := v.Local(sdf.Vec3{px, hit + 0.1, pz}); !ok || l.End > 0 {
				continue
			}
			// Small ones far more often.
			sz01 := math.Pow(rng.Float64(), 2.2)
			size := sizes[0] + (sizes[1]-sizes[0])*sz01
			if inLane && clear > 0.5 {
				size = math.Min(size, 0.45)
			}
			vm := &art.Manifest.Variants[rng.Intn(len(art.Manifest.Variants))]
			ext := math.Max(float64(vm.Extent[0]), math.Max(float64(vm.Extent[2]), float64(vm.Extent[1])))
			if ext <= 0 {
				continue
			}
			scale := float32(size / ext)
			sink := float32(0.18*float64(vm.Height)) * scale
			collision := "none"
			if size > 0.6 {
				collision = "hulls"
			}
			// In the dark: a scanned rock has no baked AO for the cave's sky,
			// so it wears a darkened copy of its material, by the sky there.
			mat, err := shade(math.Max(caveShade(sky, sdf.Vec3{px, hit + 0.4, pz}, darkness, gain), 0.2))
			if err != nil {
				return err
			}
			rc.Place(procgen.Placement{ID: fmt.Sprintf("rock_%d", placed), Model: art.ModelPath(vm),
				Position: procgen.Point3{X: float32(px), Y: float32(hit) - sink, Z: float32(pz)},
				Rotation: procgen.Point3{X: float32(rng.Range(-0.15, 0.15)), Y: float32(rng.Range(0, 2*math.Pi)), Z: float32(rng.Range(-0.15, 0.15))},
				Scale:    &procgen.Point3{X: scale, Y: scale, Z: scale}, Collision: collision, Chunk: chunkAt(px, hit, pz),
				Material: mat, DrawDistance: draw, ShadowDistance: shadow})
			placed++
		}
	}
	rc.Logf("%d rocks on the floor", placed)
	return nil
}

// caveRockShader is a function giving a rock set's material darkened to
// f of its ambient (written once per step of darkness, named prefix_k).
func caveRockShader(rc *procgen.RunContext, art *RocksArtifact, prefix string) func(f float64) (string, error) {
	shades := map[int]string{}
	base := readMaterialDoc(art.Manifest.Material)
	return func(f float64) (string, error) {
		k := int(math.Round(f * caveShadeSteps))
		if k >= caveShadeSteps {
			return art.Manifest.Material, nil
		}
		if n, ok := shades[k]; ok {
			return n, nil
		}
		d := &material.Doc{}
		if base != nil {
			d = base.Clone()
		}
		d.Name = genMaterialName(rc, fmt.Sprintf("%s_%d", prefix, k))
		d.Doc = "Generated: a cave floor rock's material, darkened by the sky it sees"
		e := d.AddEffect(material.KindColorGrade)
		b := math.Max(float64(k), 0.4) / caveShadeSteps
		if old, ok := e.Params["brightness"]; ok {
			b *= float64(old.Num[0])
		}
		e.Set("brightness", material.FloatValue(float32(b)))
		if err := writeMaterial(rc, d); err != nil {
			return "", err
		}
		shades[k] = d.Name
		return d.Name, nil
	}
}

// caveShadeSteps is how many darkened copies of a floor rock's material
// there may be.
const caveShadeSteps = 8

// skyShade is how much of the ambient light a point that sees sky share s
// of the sky keeps: the deep interior loses darkness of it.
func skyShade(s, darkness, gain float64) float64 {
	open := math.Pow(noise.Clamp(s*gain, 0, 1), 0.75)
	return 1 - darkness*(1-open)
}

// caveShade is skyShade at p from the baked sky (open where it was not
// baked).
func caveShade(sky *cave.Sky, p sdf.Vec3, darkness, gain float64) float64 {
	s := sky.At(p)
	if s < 0 {
		s = 1
	}
	return skyShade(s, darkness, gain)
}

// ---------- the manifest ----------

// caveManifest is the cave's artifact: mouths, interior boxes and samples.
func caveManifest(v *cave.Void, g *terrain.Grid, sky *cave.Sky, cs cave.Settings) *CaveArtifact {
	a := &CaveArtifact{Version: 1, Settings: cs}
	box := v.Box()
	a.Bounds = [2][3]float64{{box.Lo[0], box.Lo[1], box.Lo[2]}, {box.Hi[0], box.Hi[1], box.Hi[2]}}
	dense := v.Dense()
	under := make([]bool, len(dense))
	for k, d := range dense {
		under[k] = d[1]+d[4]*0.5 < float64(g.Sample(d[0], d[2]))
	}
	for k := 1; k < len(dense); k++ {
		if under[k] == under[k-1] {
			continue
		}
		// The mouth: the open end of this step.
		in, out := k, k-1
		if under[k-1] {
			in, out = k-1, k
		}
		dx, dz := dense[out][0]-dense[in][0], dense[out][2]-dense[in][2]
		l := math.Max(math.Hypot(dx, dz), 1e-9)
		d := dense[in]
		a.Mouths = append(a.Mouths, CaveMouth{Position: [3]float64{d[0], d[1], d[2]}, Out: [2]float64{dx / l, dz / l},
			Width: d[3], Height: d[4]})
	}
	// Interior boxes: runs of the path under the ground, 16 m at a time.
	const run = 16.0
	var cur *CaveBox
	s0 := 0.0
	acc := 0.0
	for k, d := range dense {
		if k > 0 {
			acc += math.Hypot(d[0]-dense[k-1][0], d[2]-dense[k-1][2])
		}
		if !under[k] {
			cur = nil
			continue
		}
		hw := d[3]/2 + 0.5
		lo := [3]float64{d[0] - hw, d[1] - 0.5, d[2] - hw}
		hi := [3]float64{d[0] + hw, d[1] + d[4] + 0.5, d[2] + hw}
		if cur == nil || acc-s0 > run {
			a.Interior = append(a.Interior, CaveBox{Min: lo, Max: hi})
			cur = &a.Interior[len(a.Interior)-1]
			s0 = acc
			continue
		}
		for c := 0; c < 3; c++ {
			cur.Min[c], cur.Max[c] = math.Min(cur.Min[c], lo[c]), math.Max(cur.Max[c], hi[c])
		}
	}
	// Samples every 6 m.
	acc, last := 0.0, -1e9
	for k, d := range dense {
		if k > 0 {
			acc += math.Hypot(d[0]-dense[k-1][0], d[2]-dense[k-1][2])
		}
		if acc-last < 6 && k != len(dense)-1 {
			continue
		}
		last = acc
		q := sdf.Vec3{d[0], d[1] + d[4]/3, d[2]}
		s := sky.At(q)
		a.Samples = append(a.Samples, CaveSample{Position: [3]float64{q[0], q[1], q[2]}, Sky: math.Round(s*1000) / 1000, Under: under[k]})
	}
	return a
}

// ---------- the preview ----------

func previewCave(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
	cs := caveSettings(&pc.Env)
	v := cave.New(cs)
	out := &procgen.PreviewData{}
	if v.Empty() {
		out.Notes = append(out.Notes, "draw the cave's path in the top-down view (at least two points)")
		return out, nil
	}
	box := v.Box()
	out.Bounds = procgen.Rect{MinX: box.Lo[0] - 20, MinZ: box.Lo[2] - 20, MaxX: box.Hi[0] + 20, MaxZ: box.Hi[2] + 20}
	if up := pc.Upstream(pc.Params.Str("terrain")); up != nil {
		out.Bounds = up.Bounds
		for _, im := range up.Images {
			if im.Name == "Relief" {
				out.Images = append(out.Images, im)
			}
		}
	}
	dense := v.Dense()
	centre := procgen.LineLayer{Name: "Cave", Color: [4]float32{1, 0.75, 0.3, 0.95}}
	left := procgen.LineLayer{Name: "Cave walls", Color: [4]float32{1, 0.75, 0.3, 0.45}}
	right := procgen.LineLayer{Name: "Cave walls", Color: [4]float32{1, 0.75, 0.3, 0.45}}
	for k, d := range dense {
		centre.Points = append(centre.Points, [2]float64{d[0], d[2]})
		a, b := dense[max(k-1, 0)], dense[min(k+1, len(dense)-1)]
		dx, dz := b[0]-a[0], b[2]-a[2]
		l := math.Max(math.Hypot(dx, dz), 1e-9)
		sx, sz := -dz/l*d[3]/2, dx/l*d[3]/2
		left.Points = append(left.Points, [2]float64{d[0] + sx, d[2] + sz})
		right.Points = append(right.Points, [2]float64{d[0] - sx, d[2] - sz})
	}
	out.Lines = append(out.Lines, centre, left, right)
	pl := procgen.PointLayer{Name: "Cave width", Color: [4]float32{1, 0.75, 0.3, 0.5}}
	for _, q := range cs.Path {
		pl.Points = append(pl.Points, [2]float64{q.X, q.Z})
		pl.Radii = append(pl.Radii, q.Width/2)
	}
	out.Points = append(out.Points, pl)
	// A coarse shell of rock round the void, for the 3D view.
	cell := pc.Params.Float("preview_cell")
	sizeMax := box.Size().MaxComp() + 6
	cell = math.Max(cell, sizeMax/400)
	shell := sdf.Shape{
		F: func(q sdf.Vec3) float64 {
			d := v.At(q)
			return math.Max(-d, d-1.5)
		},
		Box: box.Grow(2), Lip: v.Lip,
	}
	m, _, err := sdf.Polygonize(shell, sdf.MeshOptions{Cell: cell, Workers: pc.Workers, Ctx: pc.Ctx})
	if err != nil {
		return nil, err
	}
	pm := procgen.PreviewMesh{Name: "cave"}
	for k, q := range m.Positions {
		n := m.Normals[k]
		pm.Positions = append(pm.Positions, [3]float32{float32(q[0]), float32(q[1]), float32(q[2])})
		pm.Normals = append(pm.Normals, [3]float32{float32(n[0]), float32(n[1]), float32(n[2])})
	}
	pm.Indices = m.Indices
	out.Meshes = append(out.Meshes, pm)
	out.Notes = append(out.Notes, fmt.Sprintf("cave: %d points, %.0f m of path, box %.0f×%.0f×%.0f m; preview shell %d triangles at %.2f m",
		len(cs.Path), pathLength(dense), box.Size()[0], box.Size()[1], box.Size()[2], m.TriangleCount(), cell))
	return out, nil
}

func pathLength(d [][5]float64) float64 {
	s := 0.0
	for k := 1; k < len(d); k++ {
		s += math.Sqrt(sq3(d[k][0]-d[k-1][0]) + sq3(d[k][1]-d[k-1][1]) + sq3(d[k][2]-d[k-1][2]))
	}
	return s
}

func sq3(x float64) float64 { return x * x }

// caveAmbientVolumes is an ambient-darkening volume over each interior box,
// grown by ambient_margin so the rock round the void lies inside past the
// fade; a box whose samples see sky keeps that much more of the ambient.
func caveAmbientVolumes(a *CaveArtifact, p procgen.Params) []procgen.VolumeEntry {
	m := p.Float("ambient_margin")
	var out []procgen.VolumeEntry
	for k, b := range a.Interior {
		lo := [3]float64{b.Min[0] - m, b.Min[1] - m, b.Min[2] - m}
		hi := [3]float64{b.Max[0] + m, b.Max[1] + m, b.Max[2] + m}
		sky, n := 0.0, 0
		for _, s := range a.Samples {
			q := s.Position
			if q[0] >= b.Min[0] && q[0] <= b.Max[0] && q[1] >= b.Min[1] && q[1] <= b.Max[1] && q[2] >= b.Min[2] && q[2] <= b.Max[2] {
				sky += s.Sky
				n++
			}
		}
		if n > 0 {
			sky /= float64(n)
		}
		keep := math.Min(math.Sqrt(math.Max(sky, 0))*2, 1)
		mix := func(base float64) float64 { return round4(base + (1-base)*keep) }
		out = append(out, procgen.VolumeEntry{
			"id":       fmt.Sprintf("ambient_%d", k),
			"shape":    "box",
			"position": map[string]interface{}{"x": round4((lo[0] + hi[0]) / 2), "y": round4((lo[1] + hi[1]) / 2), "z": round4((lo[2] + hi[2]) / 2)},
			"half":     map[string]interface{}{"x": round4((hi[0] - lo[0]) / 2), "y": round4((hi[1] - lo[1]) / 2), "z": round4((hi[2] - lo[2]) / 2)},
			"ambient": map[string]interface{}{
				"sky": mix(p.Float("ambient_sky")), "ground": mix(p.Float("ambient_ground")),
				"fade": p.Float("ambient_fade"), "fog": 1.0, "dust": p.Float("ambient_dust"),
			},
		})
	}
	return out
}

// caveChunker is the chunk a cave placement at (x, y, z) streams with: id,
// unless id is empty or the placement lies within reach of a mouth (seen
// from outside), when it is procgen.ChunkAuto.
func caveChunker(id string, reach float64, mouths []CaveMouth) func(x, y, z float64) string {
	return func(x, y, z float64) string {
		if id == "" {
			return procgen.ChunkAuto
		}
		for _, m := range mouths {
			q := m.Position
			if math.Hypot(math.Hypot(x-q[0], y-q[1]), z-q[2]) < reach {
				return procgen.ChunkAuto
			}
		}
		return id
	}
}
