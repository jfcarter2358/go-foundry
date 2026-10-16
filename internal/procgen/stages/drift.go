package stages

// drift.go — the `drift` stage: sand piled against a scatter stage's rocks
// (the method is internal/procgen/place/drift.go; docs/canyon/PHASE_8.md).
//
// The terrain is generated before any rock is placed on it, and a rock is
// seated by sinking it into that ground, so every rock met the sand in a
// flat cut. This stage runs after the scatter and answers it:
//
//  1. Each rock of the chosen rules is rasterised from its cooked mesh (the
//     fallback cut a cook keeps, a few thousand triangles) onto the terrain's
//     grid, and place.Drift raises the ground into a fillet against it, with
//     a tail down the wind — or, in a wash, down the water.
//  2. The terrain's tiles the rise touches are recooked from the raised
//     heights exactly as the terrain stage cooks them (the same tile plan,
//     base, quantisation step and locked rim, so they meet the untouched
//     tiles), and SUPERSEDE the terrain stage's placements of them.
//  3. The terrain material's maps are rewritten with the sand in them: the
//     weights (sand over the aprons, so gravel and cracked ground stop where
//     the drift starts), the ground colour, and the height a rock's ground
//     blend reads. The recooked tiles wear a copy of the terrain material
//     with the new weights.
//  4. The scatter's placements are re-emitted (superseding the scatter's):
//     a rock whose material blends into the ground wears a copy that reads
//     the new height, so its foot takes the sand's colour where the sand now
//     is; anything that is not drifted against (a pebble, a bush) and stands
//     on an apron is lifted onto it.

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/material"
	"foundry/internal/nanite"
	"foundry/internal/procgen"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/place"
	"foundry/internal/procgen/terrain"
)

// DriftKind is the stage's name.
const DriftKind = "drift"

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     DriftKind,
		Label:    "Sand drift",
		Category: "Placement",
		Version:  8,
		Doc: "Sand that answers a scatter stage's rocks: the terrain rises into a fillet against each rock (steep at the rock, " +
			"easing out) with a tail down the wind — or, in a wash, down the water — instead of meeting it in a flat cut. The " +
			"touched terrain tiles are recooked and replace the terrain stage's, the material's sand is painted over the " +
			"drifts, and the rocks' ground blend follows the new sand line.",
		Params:   driftParams(),
		Run:      runDrift,
		Validate: validateDrift,
	})
}

func driftParams() []procgen.ParamDef {
	f, v2 := procgen.Float, procgen.Vec2
	d := place.DefaultDrift()
	return []procgen.ParamDef{
		{Name: "terrain", Type: procgen.ParamStage, StageKinds: TerrainSources, Default: procgen.StageRef(""),
			Doc: "The terrain stage whose tiles the sand is added to (it must cook its tiles)."},
		{Name: "ground", Type: procgen.ParamStage, StageKinds: []string{TerrainMaterialKind}, Default: procgen.StageRef(""),
			Doc: "The terrain_material stage the tiles wear: its weights get the sand, and the recooked tiles wear a copy of it."},
		{Name: "scatter", Type: procgen.ParamStage, StageKinds: []string{ScatterKind}, Default: procgen.StageRef(""),
			Doc: "The scatter stage whose rocks the sand piles against."},
		{Name: "rules", Type: procgen.ParamString, Default: procgen.String(""),
			Doc: "The scatter's rules (by name, separated by spaces) whose rocks get drifts; empty: every rule. Pebbles are " +
				"too small for the terrain's grid anyway (see Min height), and plants are not walls to the wind."},
		{Name: "wind", Type: procgen.ParamFloat, Label: "Wind (°)", Default: f(60), Min: 0, Max: 360, Step: 1,
			Doc: "The direction the wind blows toward, degrees clockwise from +Z seen from above (90: toward +X). The tails run this way."},
		{Name: "flow", Type: procgen.ParamVec2, Label: "Wash flow", Default: v2(0.2, 0.5), Min: 0, Max: 1, Step: 0.01,
			Doc: "Between these levels of the terrain's flow mask, a rock's tail turns from the wind to downhill: in a wash the water lays the sand."},
		{Name: "walls", Added: true, Type: procgen.ParamStage, StageKinds: []string{WallSheetKind}, Default: procgen.StageRef(""),
			Doc: "A wall_sheet stage whose cliffs get sand ramps at their feet (the wall_* parameters shape them). Empty: none."},
		{Name: "wall_height", Added: true, Type: procgen.ParamFloat, Label: "Wall ramp (m)", Default: f(2.2), Min: 0, Max: 20, Step: 0.1,
			Doc: "How high the sand climbs a cliff's foot, on average."},
		{Name: "wall_spread", Added: true, Type: procgen.ParamFloat, Label: "Wall spread", Default: f(4.5), Min: 0.5, Max: 30, Step: 0.1,
			Doc: "How far a cliff's ramp runs out per metre of its height (its slope at the wall is 2/spread)."},
		{Name: "wall_tail", Added: true, Type: procgen.ParamFloat, Label: "Wall tail", Default: f(0.6), Min: 0, Max: 20, Step: 0.1,
			Doc: "How much further a cliff's ramp runs downwind."},
		{Name: "wall_vary", Added: true, Type: procgen.ParamFloat, Label: "Wall vary", Default: f(0.6), Min: 0, Max: 1, Step: 0.01,
			Doc: "How much a cliff's ramp rises and falls along the wall (a share of its height), over about 15 m."},
		{Name: "fillet", Type: procgen.ParamFloat, Label: "Fillet", Default: f(d.Fillet), Min: 0, Max: 1, Step: 0.01,
			Doc: "How high the sand climbs a rock, as a fraction of the rock's height above the ground."},
		{Name: "max_height", Type: procgen.ParamFloat, Label: "Max height (m)", Default: f(d.MaxHeight), Min: 0, Max: 10, Step: 0.05,
			Doc: "The most sand against any rock, metres."},
		{Name: "min_height", Type: procgen.ParamFloat, Label: "Min height (m)", Default: f(d.MinHeight), Min: 0, Max: 10, Step: 0.05,
			Doc: "Rocks standing less than this above the ground get none: a drift a grid cell across is a pimple, not a drift."},
		{Name: "spread", Type: procgen.ParamFloat, Label: "Spread", Default: f(d.Spread), Min: 0.5, Max: 20, Step: 0.1,
			Doc: "How far the sand runs out per metre of its height: its slope at the rock is 2/spread (3: about 34°, sand's angle of repose)."},
		{Name: "tail", Type: procgen.ParamFloat, Label: "Tail", Default: f(d.Tail), Min: 0, Max: 20, Step: 0.1,
			Doc: "How much longer the sand runs downwind (the lee tail behind a rock), as a multiple of its run elsewhere."},
		{Name: "windward", Type: procgen.ParamFloat, Label: "Windward", Default: f(d.Windward), Min: 0, Max: 5, Step: 0.05,
			Doc: "How much shorter the ramp on the side the wind comes from is."},
		{Name: "slope_fade", Type: procgen.ParamVec2, Label: "Slope fade (°)", Default: v2(d.SlopeFade[0], d.SlopeFade[1]), Min: 0, Max: 90, Step: 0.5,
			Doc: "Sand slides off steep ground: drifts fade out between these slopes."},
		{Name: "sand_cover", Type: procgen.ParamFloat, Label: "Sand cover", Default: f(1), Min: 0, Max: 1, Step: 0.01,
			Doc: "How fully the drifts are painted the sand layer (gravel and cracked ground give way to it)."},
		{Name: "sand_depth", Type: procgen.ParamFloat, Label: "Sand depth (m)", Default: f(0.12), Min: 0.005, Max: 2, Step: 0.005,
			Doc: "The drift depth at which the sand covers fully; thinner sand lets the layer under it show through, by height."},
		{Name: "ground_band", Type: procgen.ParamFloat, Label: "Ground blend (m)", Default: f(0), Min: 0, Max: 20, Step: 0.05,
			Doc: "The re-placed rocks' ground blend height (how far up a foot takes the sand's colour); 0 keeps their scatter rule's."},
		{Name: "lift", Type: procgen.ParamBool, Label: "Lift the rest", Default: procgen.Bool(true),
			Doc: "Lift the scatter's other placements (pebbles, plants) standing on a drift onto it, rather than leaving them buried."},
		{Name: "cook_memory", Type: procgen.ParamInt, Label: "Cook memory (MB)", Default: procgen.Int(2000), Min: 100, Max: 64000,
			Doc: "How much memory the tiles' cooks may hold at once."},
	}
}

func validateDrift(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	var out []string
	for _, k := range []string{"terrain", "ground", "scatter"} {
		if p.Str(k) == "" {
			out = append(out, "choose a "+k+" stage")
		}
	}
	if b := p.Vec2("slope_fade"); b[1] < b[0] {
		out = append(out, "the slope fade runs backwards")
	}
	return out
}

// driftMesh is a model's fallback mesh, read once per model.
type driftMesh struct {
	pos  [][3]float64
	tris [][3]int
}

// loadDriftMesh reads a model's cooked mesh: its fallback cut (a few
// thousand triangles, plenty for a boulder's outline), or with full the
// whole mesh (a cliff's foot is a long, fine line).
func loadDriftMesh(model string, full bool) (*driftMesh, error) {
	m, err := nanite.Load(fileutil.ResolveAssetPath(model))
	if err != nil {
		return nil, err
	}
	verts, idx := m.FallbackVertices, m.Fallback
	if full || len(idx) == 0 {
		if err := m.Flat(); err != nil {
			return nil, err
		}
		verts, idx = m.Vertices, m.Indices
	}
	dm := &driftMesh{pos: make([][3]float64, len(verts)/5), tris: make([][3]int, len(idx)/3)}
	for k := range dm.pos {
		dm.pos[k] = [3]float64{float64(verts[5*k]), float64(verts[5*k+1]), float64(verts[5*k+2])}
	}
	for k := range dm.tris {
		dm.tris[k] = [3]int{int(idx[3*k]), int(idx[3*k+1]), int(idx[3*k+2])}
	}
	return dm, nil
}

// placed is the mesh moved where p puts it: scaled, turned Y·X·Z, moved.
func (dm *driftMesh) placed(p procgen.Placement) [][3]float64 {
	r := place.RotY(float64(p.Rotation.Y)).Mul(place.RotX(float64(p.Rotation.X))).Mul(place.RotZ(float64(p.Rotation.Z)))
	s := [3]float64{1, 1, 1}
	if p.Scale != nil {
		s = [3]float64{float64(p.Scale.X), float64(p.Scale.Y), float64(p.Scale.Z)}
	}
	out := make([][3]float64, len(dm.pos))
	for k, v := range dm.pos {
		w := r.Apply([3]float64{v[0] * s[0], v[1] * s[1], v[2] * s[2]})
		out[k] = [3]float64{w[0] + float64(p.Position.X), w[1] + float64(p.Position.Y), w[2] + float64(p.Position.Z)}
	}
	return out
}

// ruleOf is the scatter rule a placement came from: its ID less the
// trailing _NNNNN.
func ruleOf(id string) string {
	if i := strings.LastIndexByte(id, '_'); i > 0 {
		return id[:i]
	}
	return id
}

func runDrift(rc *procgen.RunContext) error {
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
	if t.Meta.Tiles.Step == 0 || t.Meta.Tiles.Count == 0 {
		return fmt.Errorf("the terrain stage %s does not cook its tiles", p.Str("terrain"))
	}
	ground, err := groundArtifact(rc, p.Str("ground"))
	if err != nil {
		return err
	}
	if ground == nil {
		return fmt.Errorf("choose a ground stage")
	}
	// The drifts are painted into a four-layer splat's weights, sand in the
	// first channel. An eight-layer region material (docs/regions/PHASE_4.md)
	// holds its sand in whichever layer its environments chose: not yet.
	if ground.Weights2 != "" || ground.Palette > 0 {
		return fmt.Errorf("the ground stage %s is an eight-layer or palette region material; drift paints only a four-layer terrain_material", p.Str("ground"))
	}
	sc := rc.Upstream(p.Str("scatter"))
	if sc == nil {
		return fmt.Errorf("no scatter stage %q", p.Str("scatter"))
	}
	g := t.Grid
	sz := g.S()
	slopeMask, err := t.Mask(terrain.MaskSlope)
	if err != nil {
		return err
	}
	flowMask, err := t.Mask(terrain.MaskFlow)
	if err != nil {
		return err
	}

	// 1. The rocks' columns.
	want := map[string]bool{}
	for _, r := range strings.Fields(p.Str("rules")) {
		want[r] = true
	}
	var drifted []int
	for k, pl := range sc.Placements {
		if len(want) == 0 || want[ruleOf(pl.ID)] {
			drifted = append(drifted, k)
		}
	}
	meshes := map[string]*driftMesh{}
	var meshMu sync.Mutex
	meshOf := func(model string, full bool) (*driftMesh, error) {
		meshMu.Lock()
		defer meshMu.Unlock()
		if m, ok := meshes[model]; ok {
			return m, nil
		}
		m, err := loadDriftMesh(model, full)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", model, err)
		}
		meshes[model] = m
		return m, nil
	}
	wind := p.Float("wind") * math.Pi / 180
	wx, wz := math.Sin(wind), math.Cos(wind)
	fl := p.Vec2("flow")
	rocks := make([]place.DriftRock, len(drifted))
	if err := rc.Parallel(len(drifted), func(n int) error {
		pl := sc.Placements[drifted[n]]
		m, err := meshOf(pl.Model, false)
		if err != nil {
			return err
		}
		x, z := float64(pl.Position.X), float64(pl.Position.Z)
		// The tail: down the wind, turned downhill as the ground becomes a wash.
		dx, dz := wx, wz
		if fw := noise.SmoothStep(fl[0], fl[1], sampleField(g, flowMask, x, z)); fw > 0 {
			gx := float64(g.Sample(x+1, z) - g.Sample(x-1, z))
			gz := float64(g.Sample(x, z+1) - g.Sample(x, z-1))
			if l := math.Hypot(gx, gz); l > 1e-6 {
				dx, dz = (1-fw)*dx-fw*gx/l, (1-fw)*dz-fw*gz/l
				if l := math.Hypot(dx, dz); l > 1e-6 {
					dx, dz = dx/l, dz/l
				}
			}
		}
		rocks[n] = place.DriftRock{Cols: place.RasterColumns(g, m.placed(pl), m.tris), Dir: [2]float64{dx, dz},
			Seed: noise.Hash64(rc.Seed, uint64(drifted[n]))}
		return nil
	}); err != nil {
		return err
	}
	// The cliffs: every section of the walls stage, each its own obstacle
	// with the wall ramp's shape, the tail down the wind.
	var ws *procgen.StageResult
	if id := p.Str("walls"); id != "" {
		if ws = rc.Upstream(id); ws == nil {
			return fmt.Errorf("no walls stage %q", id)
		}
		own := &place.DriftShape{Height: p.Float("wall_height"), Spread: p.Float("wall_spread"), Tail: p.Float("wall_tail"),
			Vary: p.Float("wall_vary"), VaryLength: 15, Reach: wallDepth(rc, id)}
		walls := make([]place.DriftRock, len(ws.Placements))
		if err := rc.Parallel(len(ws.Placements), func(n int) error {
			pl := ws.Placements[n]
			m, err := meshOf(pl.Model, true)
			if err != nil {
				return err
			}
			walls[n] = place.DriftRock{Cols: place.RasterColumns(g, m.placed(pl), m.tris), Dir: [2]float64{wx, wz},
				Seed: noise.Hash64(rc.Seed, 0x77a11), Own: own}
			return nil
		}); err != nil {
			return err
		}
		rocks = append(rocks, walls...)
	}
	rc.Progress(0.1)
	s := place.DriftSettings{Fillet: p.Float("fillet"), MaxHeight: p.Float("max_height"), MinHeight: p.Float("min_height"),
		Spread: p.Float("spread"), Tail: p.Float("tail"), Windward: p.Float("windward"), SlopeFade: p.Vec2("slope_fade"), Edge: 2}
	dr, err := place.Drift(g, func(k int) float64 { return float64(slopeMask[k]) * 90 }, rocks, s, &rc.Env)
	if err != nil {
		return err
	}
	rise := dr.Rise
	var vol, top float64
	for _, v := range rise {
		vol += float64(v)
		top = math.Max(top, float64(v))
	}
	rc.Logf("%d of %d rocks and wall sections drifted (%d too low), %.0f m³ of sand, deepest %.2f m, in %s", dr.Rocks, len(rocks), dr.Small,
		vol*g.Cell*g.Cell, top, time.Since(t0).Round(time.Millisecond))
	rc.Progress(0.2)

	// 2. The raised field and its maps.
	g2 := g.Clone()
	for k := range g2.H {
		g2.H[k] += rise[k]
	}
	maps, err := driftMaps(ground, g2, rise, p.Float("sand_cover"), p.Float("sand_depth"))
	if err != nil {
		return err
	}
	for name, pix := range map[string][]uint8{"weights.png": maps.weights, "ground.png": maps.ground, "height.png": maps.height} {
		data, err := terrain.EncodeRGBA(sz, sz, pix)
		if err != nil {
			return err
		}
		if err := rc.WriteFile(name, data); err != nil {
			return err
		}
	}
	a := *ground
	a.Weights, a.Ground, a.Height = mapRef(rc, "weights.png"), mapRef(rc, "ground.png"), mapRef(rc, "height.png")
	a.Material = genMaterialName(rc, "")
	base := readMaterialDoc(ground.Material)
	if base == nil {
		return fmt.Errorf("the ground stage's material %s cannot be read", ground.Material)
	}
	md := base.Clone()
	md.Name = a.Material
	md.Doc = "Generated terrain with sand drifted against the rocks: " + base.Doc
	if e := md.Find(material.KindSplat); e != nil {
		e.Set("weights", material.TextureValue(a.Weights))
	}
	if err := writeMaterial(rc, md); err != nil {
		return err
	}

	// 3. The touched tiles, recooked.
	if err := driftTiles(rc, t, g2, rise, a.Material); err != nil {
		return err
	}

	// 4. The scatter's placements, re-emitted.
	if err := driftPlacements(rc, sc, ground, &a, rise, g, drifted, true); err != nil {
		return err
	}
	if ws != nil {
		// The walls: re-worn so their feet blend into the sand where it now
		// lies; never lifted.
		all := make([]int, len(ws.Placements))
		for k := range all {
			all[k] = k
		}
		if err := driftPlacements(rc, ws, ground, &a, rise, g, all, false); err != nil {
			return err
		}
	}
	rc.Artifact = &a
	rc.Progress(1)
	return nil
}

// sampleField is a per-sample field bilinear at (x, z).
func sampleField(g *terrain.Grid, v []float32, x, z float64) float64 {
	sz := g.S()
	fx := noise.Clamp((x-g.MinX)/g.Cell, 0, float64(sz-1))
	fz := noise.Clamp((z-g.MinZ)/g.Cell, 0, float64(sz-1))
	i, j := min(int(fx), sz-2), min(int(fz), sz-2)
	u, w := fx-float64(i), fz-float64(j)
	a := float64(v[j*sz+i])*(1-u) + float64(v[j*sz+i+1])*u
	b := float64(v[(j+1)*sz+i])*(1-u) + float64(v[(j+1)*sz+i+1])*u
	return a*(1-w) + b*w
}

type driftMapSet struct{ weights, ground, height []uint8 }

// readRGBA reads a map a material names (resources-relative, no .png).
func readRGBA(ref string, sz int) ([]uint8, error) {
	f, err := os.Open(fileutil.ResolveAssetPath(ref + ".png"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	im, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	if b := im.Bounds(); b.Dx() != sz || b.Dy() != sz {
		return nil, fmt.Errorf("%s is %d×%d, the terrain %d²", ref, b.Dx(), b.Dy(), sz)
	}
	n := image.NewNRGBA(image.Rect(0, 0, sz, sz))
	draw.Draw(n, n.Bounds(), im, im.Bounds().Min, draw.Src)
	return n.Pix, nil
}

// driftMaps is the terrain material's maps with the sand in: the weights
// carried toward pure sand by the drift's depth over sand_depth (times
// cover), the ground colour toward the sand's own (the average ground
// colour where the weights were already all sand), and the height map of
// the raised field in the material's own height range.
func driftMaps(ground *TerrainMaterialArtifact, g2 *terrain.Grid, rise []float32, cover, depth float64) (*driftMapSet, error) {
	sz := g2.S()
	w, err := readRGBA(ground.Weights, sz)
	if err != nil {
		return nil, err
	}
	gc, err := readRGBA(ground.Ground, sz)
	if err != nil {
		return nil, err
	}
	var sand [3]float64
	n := 0.0
	for k := 0; k < sz*sz; k++ {
		if w[4*k] >= 242 {
			for c := 0; c < 3; c++ {
				sand[c] += float64(gc[4*k+c])
			}
			n++
		}
	}
	if n == 0 {
		sand, n = [3]float64{170, 110, 80}, 1
	}
	for c := range sand {
		sand[c] /= n
	}
	out := &driftMapSet{weights: w, ground: gc, height: make([]uint8, 4*sz*sz)}
	span := float64(ground.HeightHi - ground.HeightLo)
	if span == 0 {
		span = 1
	}
	for k := 0; k < sz*sz; k++ {
		if r := float64(rise[k]); r > 0 {
			share := cover * noise.SmoothStep(0, depth, r)
			for c := 0; c < 4; c++ {
				target := 0.0
				if c == 0 {
					target = 255
				}
				out.weights[4*k+c] = uint8(math.Round(float64(w[4*k+c])*(1-share) + target*share))
			}
			for c := 0; c < 3; c++ {
				out.ground[4*k+c] = uint8(math.Round(float64(gc[4*k+c])*(1-share) + sand[c]*share))
			}
		}
		v := uint16(noise.Clamp(float64(g2.H[k]-ground.HeightLo)/span, 0, 1)*65535 + 0.5)
		out.height[4*k], out.height[4*k+1], out.height[4*k+2], out.height[4*k+3] = uint8(v>>8), uint8(v), 0, 255
	}
	return out, nil
}

// driftTiles recooks the terrain tiles the rise touches from the raised
// field g2, with the terrain stage's own tile plan, and puts them in place
// of the terrain stage's.
func driftTiles(rc *procgen.RunContext, t *terrain.Terrain, g2 *terrain.Grid, rise []float32, mat string) error {
	tid := rc.Params.Str("terrain")
	// A ground_edit's tiles are planned as its source terrain's.
	st := terrainSourceStage(rc.Doc, tid)
	k := procgen.KindByName(TerrainKind)
	if st == nil || k == nil {
		return fmt.Errorf("no terrain stage %q", tid)
	}
	tparams := procgen.Resolve(k.Params, st.Params)
	tp := planTiles(tparams, t.Grid)
	// The terrain's own quantisation step and base, as it cooked: the
	// raised field must not move them, or the seams to its untouched tiles
	// would open.
	tp.quant, tp.base = t.Meta.Tiles.Step, t.Meta.Tiles.Base
	sz := g2.S()
	var touched [][2]int
	for tj := 0; tj < tp.count; tj++ {
		for ti := 0; ti < tp.count; ti++ {
			hit := false
			for j := tj * tp.per; j <= min((tj+1)*tp.per, sz-1) && !hit; j++ {
				for i := ti * tp.per; i <= min((ti+1)*tp.per, sz-1); i++ {
					if rise[j*sz+i] > 0 {
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
	collision := tparams.Str("collision")
	meshOnly := collision == "mesh" || collision == "auto"
	tris := tileTriangles(tp)
	conc := cookConcurrency(tris, rc.Params.Int("cook_memory"), rc.Workers, meshOnly)
	rc.Logf("%d of %d tiles touched, cooking %d at a time", len(touched), tp.count*tp.count, conc)
	sem := make(chan struct{}, conc)
	uv := float32(tparams.Float("uv_scale"))
	tex := tparams.Str("texture")
	var done atomic.Int64
	t0 := time.Now()
	err := rc.Parallel(len(touched), func(n int) error {
		sem <- struct{}{}
		defer func() { <-sem }()
		if err := rc.Err(); err != nil {
			return err
		}
		ti, tj := touched[n][0], touched[n][1]
		hg, centre := tileGridAt(g2, tp, ti, tj, uv, tparams.Str("uv_mode") == "terrain")
		var b meshout.Builder
		b.Slot("ground", tex)
		hg.Build(&b)
		name := tileName(ti, tj)
		rect := hg.Rect()
		if _, err := meshout.Cook(b.Mesh(), rc.Path(name+".fnm"), meshout.CookOptions{LockRect: &rect, PositionStep: tp.quant,
			CollisionMesh: meshOnly}); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		rc.Supersede(tid, name)
		rc.Place(procgen.Placement{ID: name, Model: rc.AssetPath(name + ".fnm"), Position: centre,
			Collision: collision, Chunk: procgen.ChunkAuto, Material: mat})
		rc.Progress(0.25 + 0.6*float32(done.Add(1))/float32(max(len(touched), 1)))
		return nil
	})
	if err != nil {
		return err
	}
	rc.Logf("tiles cooked in %s", time.Since(t0).Round(time.Millisecond))
	return nil
}

// driftPlacements re-emits the scatter's placements: rocks that blend into
// the ground wear a copy of their material reading the new maps, and the
// rest that stand on a drift are lifted onto it.
func driftPlacements(rc *procgen.RunContext, sc *procgen.StageResult, ground, a *TerrainMaterialArtifact, rise []float32,
	g *terrain.Grid, drifted []int, lift bool) error {
	isDrifted := make(map[int]bool, len(drifted))
	for _, k := range drifted {
		isDrifted[k] = true
	}
	band := rc.Params.Float("ground_band")
	lift = lift && rc.Params.Bool("lift")
	swapped := map[string]string{}
	swap := func(name string) (string, error) {
		if name == "" {
			return "", nil
		}
		if s, ok := swapped[name]; ok {
			return s, nil
		}
		swapped[name] = name
		d := readMaterialDoc(name)
		if d == nil {
			return name, nil
		}
		e := d.Find(material.KindGroundBlend)
		if e == nil || e.Texture("height") != ground.Height {
			return name, nil
		}
		c := d.Clone()
		part := strings.TrimPrefix(name, "gen_"+safeName(rc.Doc.Name, "gen")+"_")
		c.Name = genMaterialName(rc, part)
		c.Doc += ", over the drifted sand"
		ce := c.Find(material.KindGroundBlend)
		ce.Set("ground", material.TextureValue(a.Ground))
		ce.Set("height", material.TextureValue(a.Height))
		if band > 0 {
			ce.Set("band", material.FloatValue(float32(band)))
		}
		if err := writeMaterial(rc, c); err != nil {
			return "", err
		}
		swapped[name] = c.Name
		return c.Name, nil
	}
	var lifted, reworn int
	for k, pl := range sc.Placements {
		q := pl
		m, err := swap(pl.Material)
		if err != nil {
			return err
		}
		changed := m != pl.Material
		q.Material = m
		if lift && !isDrifted[k] {
			if r := sampleField(g, rise, float64(pl.Position.X), float64(pl.Position.Z)); r > 0.01 {
				q.Position.Y += float32(r)
				changed = true
				lifted++
			}
		}
		if !changed {
			continue
		}
		if m != pl.Material {
			reworn++
		}
		rc.Supersede(sc.ID, pl.ID)
		rc.Place(q)
	}
	rc.Logf("re-placed %s's placements: %d wear the drifted ground's blend, %d lifted onto a drift", sc.ID, reworn, lifted)
	return nil
}

// wallDepth is a wall_sheet stage's relief (how far its rock stands out of
// the terrain), 2.5 m when it cannot be read.
func wallDepth(rc *procgen.RunContext, id string) float64 {
	st, k := rc.Doc.StageByID(id), procgen.KindByName(WallSheetKind)
	if st == nil || k == nil {
		return 2.5
	}
	return procgen.Resolve(k.Params, st.Params).Float("depth")
}
