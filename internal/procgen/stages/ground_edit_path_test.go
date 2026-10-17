package stages

// ground_edit_path_test.go — ground_edit's paths end to end
// (ground_edit_path*.go, docs/terrain-paint/PHASE_P4.md): paths laid with
// their masks, table and stones (aligned, scaled, sunk, no stones on a
// game trail); the artifact what Load reads; a path edit rerunning only
// what it reached downstream and matching a full run, for the ground_edit
// and for grass reading the wear; a document without paths unchanged; the
// path presets slotting into a document; orient_to reading the paths, a
// hand-painted direction layer and the slope.

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
	"foundry/internal/procgen/place"
	"foundry/internal/procgen/terrain"
)

// pathSpline is a spline value of (x, z, width) points.
func pathSpline(pts ...[3]float64) procgen.Value {
	var ps [][]float64
	for _, p := range pts {
		ps = append(ps, []float64{p[0], p[1], p[2]})
	}
	return procgen.Spline(ps...)
}

// pathDoc is gentleDoc's terrain with a ground_edit holding a winding rock
// path, a stepping-stone line and a game trail, the stones from a
// procedural rocks stage.
func pathDoc() *procgen.Doc {
	d := gentleDoc()
	e, i, ref, s, b := procgen.Enum, procgen.Int, procgen.StageRef, procgen.String, procgen.Bool
	d.Stages = append(d.Stages[:1], append([]procgen.Stage{{Kind: RocksKind, ID: "stones",
		Params: procgen.Params{"preset": e("boulder"), "variants": i(2), "triangles": i(200)}}}, d.Stages[1:]...)...)
	ed := d.StageByID("edit")
	ed.Params = procgen.Params{"terrain": ref("ground"), "path_rocks": ref("stones"),
		"paths": procgen.List(
			procgen.Params{"name": s("climb"), "style": e(terrain.PathRock),
				"path": pathSpline([3]float64{-110, -100, 2}, [3]float64{-60, -70, 2.2}, [3]float64{-20, -90, 1.8}, [3]float64{30, -60, 2})},
			procgen.Params{"name": s("hops"), "style": e(terrain.PathStepping), "flatten": b(false),
				"path": pathSpline([3]float64{40, 20, 1.2}, [3]float64{70, 40, 1.2}, [3]float64{100, 45, 1.2})},
			procgen.Params{"name": s("deer"), "style": e(terrain.PathGame), "flatten": b(false),
				"path": pathSpline([3]float64{-100, 60, 0.8}, [3]float64{-40, 90, 0.8})},
		)}
	return d
}

func TestGroundEditLaysPaths(t *testing.T) {
	d := pathDoc()
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	dir := t.TempDir()
	res, log := logged(t, d, dir)
	ed := res.Stage("edit")
	art := ed.Artifact.(*terrain.Terrain)
	tab, err := ReadGroundPaths(filepath.Join(dir, "edit", PathsFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Paths) != 3 || tab.Note != "" {
		t.Fatalf("paths.yaml %+v\n%s", tab, log)
	}
	byName := map[string]GroundPath{}
	for _, p := range tab.Paths {
		byName[p.Name] = p
	}
	if c := byName["climb"]; c.Style != terrain.PathRock || c.Stones[terrain.StonesFlag] < 100 || c.Length < 150 {
		t.Fatalf("climb %+v", c)
	}
	if h := byName["hops"]; h.Stones[terrain.StonesStep] < 50 || h.Stones[terrain.StonesFlag] != 0 {
		t.Fatalf("hops %+v", h)
	}
	if g := byName["deer"]; len(g.Stones) != 0 {
		t.Fatalf("a game trail laid stones: %+v", g)
	}
	if art.Meta.Footpaths == nil || len(art.Meta.Footpaths.Paths) != 3 {
		t.Fatalf("terrain.yaml footpaths %+v", art.Meta.Footpaths)
	}
	// The stones: flat (Y scaled below X and Z), sunk (the origin under the
	// ground), flagstones not colliding.
	src, err := terrain.Load(filepath.Join(dir, "edit"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range ed.Placements {
		if !strings.HasPrefix(p.ID, "stone_") {
			continue
		}
		n++
		g := float64(src.Grid.Sample(float64(p.Position.X), float64(p.Position.Z)))
		if float64(p.Position.Y) > g {
			t.Fatalf("%s floats: origin %.3f over ground %.3f", p.ID, p.Position.Y, g)
		}
		if p.Scale == nil || p.Scale.Y <= 0 {
			t.Fatalf("%s scale %v", p.ID, p.Scale)
		}
		if strings.HasPrefix(p.ID, "stone_climb_") && p.Collision != "none" {
			t.Fatalf("%s collides (%s)", p.ID, p.Collision)
		}
	}
	if n < 150 {
		t.Fatalf("%d stones placed", n)
	}
	// The masks are there, and the artifact is what Load reads.
	for _, name := range terrain.PathMaskNames {
		a, _ := art.Mask(name)
		b, err := src.Mask(name)
		if err != nil || len(a) != len(b) || len(a) == 0 {
			t.Fatalf("mask %s: %v (%d / %d)", name, err, len(a), len(b))
		}
		for k := range a {
			if a[k] != b[k] {
				t.Fatalf("mask %s differs at %d", name, k)
			}
		}
	}
	if v, _ := art.MaskAt(terrain.PathSurfaceMask(terrain.PathRock), -60, -70); v < 0.9 {
		t.Errorf("rock surface on the path %.2f", v)
	}
	if v, _ := art.MaskAt(terrain.MaskPathWear, -40, 90); v < 0.3 {
		t.Errorf("game trail wear %.2f", v)
	}
	if v, _ := art.MaskAt(terrain.MaskPathClear, 0, 0); v != 0 {
		t.Errorf("clear far from any path %.2f", v)
	}
	for k := range art.Grid.H {
		if art.Grid.H[k] != src.Grid.H[k] {
			t.Fatalf("height %d differs from Load", k)
		}
	}
	// No paint layer was written by the paths (paint stays an input).
	if len(d.Paint) != 0 {
		t.Fatalf("the run declared paint %v", d.Paint)
	}
}

func TestGroundEditWithoutPathsWritesNoPathOutput(t *testing.T) {
	d := gentleDoc()
	paintGentle(d)
	dir := t.TempDir()
	res, _ := logged(t, d, dir)
	art := res.Stage("edit").Artifact.(*terrain.Terrain)
	if art.Meta.Footpaths != nil {
		t.Error("a footpaths section without paths")
	}
	if _, err := os.Stat(filepath.Join(dir, "edit", PathsFile)); err == nil {
		t.Error("paths.yaml without paths")
	}
	for _, m := range art.Meta.Masks {
		if strings.HasPrefix(m.Name, "path_") {
			t.Errorf("mask %s without paths", m.Name)
		}
	}
	for _, p := range res.Stage("edit").Placements {
		if strings.HasPrefix(p.ID, "stone_") {
			t.Errorf("placement %s without paths", p.ID)
		}
	}
	k := procgen.KindByName(GroundEditKind)
	if procgen.CoveredHash(k, procgen.Resolve(k.Params, d.StageByID("edit").Params)) != "" {
		t.Error("covered hash of a ground_edit without paths or rivers is not empty")
	}
}

func TestPathEditRebuildsOnlyWhatItReached(t *testing.T) {
	// A path point moved: the ground_edit matches a run from nothing, and
	// so does the grass downstream (thinned by path_wear), which rebuilds
	// only the tiles whose rect key changed.
	withFakePolyHaven(t)
	d := pathDoc()
	d.Stages = append(d.Stages, grassDoc().Stages[1:]...)
	g := d.StageByID("grass")
	g.Params["terrain"] = procgen.StageRef("edit")
	g.Params["rules"].Items[0]["density"] = procgen.Float(0.5)
	g.Params["exclude"] = procgen.List(procgen.Params{"mask": procgen.String(terrain.MaskPathWear), "shrink": procgen.Float(0.5)})
	move := func(d *procgen.Doc) {
		it := d.StageByID("edit").Params["paths"].Items[1]
		v := it["path"].Clone()
		v.Points[2][1] = 70 // the stepping stones' far end swings north
		it["path"] = v
	}
	log := incrementalMatchesFull(t, d, t.TempDir(), move, "grass")
	t.Log(grepLines(log, "[grass] reused", "[edit] tiles", "[edit] paths"))
	if !strings.Contains(log, "[grass] reused") || strings.Contains(log, "[grass] reused 16 of 16") ||
		strings.Contains(log, "[grass] reused 0 of 16") {
		t.Errorf("want some grass tiles reused and some rebuilt:\n%s", log)
	}
	// The ground_edit itself: incremental == full after a flattened path
	// moves (its tiles keyed by content).
	d2 := pathDoc()
	log2 := incrementalMatchesFull(t, d2, t.TempDir(), func(d *procgen.Doc) {
		it := d.StageByID("edit").Params["paths"].Items[0]
		v := it["path"].Clone()
		v.Points[3][1] = -40
		it["path"] = v
	}, "edit")
	if !strings.Contains(log2, "reused from the last run") {
		t.Errorf("want the edit to keep some tiles:\n%s", grepLines(log2, "[edit]"))
	}
	// The base hash ignores the paths (RectCovered); the full hash and the
	// rect key over the path see them, a far rect key does not.
	e1, _ := procgen.PlanEnv(pathDoc(), "edit")
	d3 := pathDoc()
	move(d3)
	e2, _ := procgen.PlanEnv(d3, "edit")
	if e1.BaseHash != e2.BaseHash {
		t.Error("moving a path changed the stage's base hash")
	}
	h1, _ := procgen.StageHashes(pathDoc())
	h2, _ := procgen.StageHashes(d3)
	if h1["edit"] == h2["edit"] {
		t.Error("moving a path did not change the stage's hash")
	}
	dir := t.TempDir()
	r1, _ := logged(t, pathDoc(), dir)
	a := r1.Stage("edit").Artifact.(*terrain.Terrain)
	near, far := a.RectKey(80, 30, 110, 80), a.RectKey(-120, 100, -110, 120)
	r2, _ := logged(t, d3, dir)
	b := r2.Stage("edit").Artifact.(*terrain.Terrain)
	if b.RectKey(80, 30, 110, 80) == near || b.RectKey(-120, 100, -110, 120) != far {
		t.Error("the rect keys did not follow the moved path")
	}
}

func TestPathPresetsSlotInAGroundEdit(t *testing.T) {
	ps := shippedPresets(t)
	d := freshDoc()
	d.Stages = append(d.Stages, grassDoc().Stages[1:]...)
	d.StageByID("grass").Params["terrain"] = procgen.StageRef("ground")
	stroke := [][2]float64{{-20, -20}, {-10, -18}, {0, -12}, {10, -10}, {20, -4}}
	styles := map[string]string{}
	for _, id := range []string{"rock_path", "stepping_stones", "dirt_trail", "gravel_path", "game_trail"} {
		p := FindPreset(ps, id)
		if p == nil || p.Path == nil {
			t.Fatalf("%s is not a path brush", id)
		}
		ge, idx, _, err := AddPathStroke(d, p, stroke, 1.5)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		it := d.StageByID(ge).Params["paths"].Items[idx]
		styles[it.Str("name")] = it.Str("style")
		w := it["path"].Points[0][2]
		want := 3.0
		if id == "game_trail" {
			want = 1.5
		}
		if math.Abs(w-want) > 1e-9 {
			t.Errorf("%s: width %.2f, want %.2f", id, w, want)
		}
		if fl, ok := it["flatten"]; (id == "stepping_stones" || id == "game_trail") != (ok && !fl.Bool) {
			t.Errorf("%s: flatten %v", id, it["flatten"])
		}
	}
	if len(styles) != 5 || styles["rock_path_1"] != terrain.PathRock || styles["game_trail_5"] != terrain.PathGame {
		t.Fatalf("paths %v", styles)
	}
	ge := firstStageOf(d, GroundEditKind)
	if ge == nil || ge.Params.Str("path_rocks") != "path_rocks_src" || d.StageByID("path_rocks_src") == nil {
		t.Fatal("no ground_edit with path rocks")
	}
	if d.StageByID("grass").Params.Str("terrain") != ge.ID || d.StageByID("ground_mat").Params.Str("terrain") != ge.ID {
		t.Error("the grass and the material do not stand on the ground_edit")
	}
	masks := map[string]string{}
	for _, it := range d.StageByID("ground_mat").Params["paint"].Items {
		masks[it.Str("mask")] = it.Str("target")
	}
	for _, m := range []string{terrain.MaskPathWear, "path_rock_path", "path_dirt_trail", "path_gravel_path", "path_game_trail", "path_stepping_stones"} {
		if masks[m] == "" {
			t.Errorf("no material entry for %s: %v", m, masks)
		}
	}
	// The gravel path's texture is the gravel role's scan already: shared.
	if masks["path_gravel_path"] != "gravel" {
		t.Errorf("gravel path → %s", masks["path_gravel_path"])
	}
	ex := d.StageByID("grass").Params["exclude"].Items
	if !listHas(ex, "mask", terrain.MaskPathWear) || ex[0].Float("shrink") != 0.6 {
		t.Errorf("grass excludes %v", ex)
	}
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("invalid: %v", probs)
	}
	// Applying a recipe again changes nothing.
	before, _ := procgen.EncodeDoc(d)
	if notes, err := ApplyRecipe(d, FindPreset(ps, "rock_path")); err != nil || len(notes) != 0 {
		t.Fatalf("second application: %v %v", notes, err)
	}
	after, _ := procgen.EncodeDoc(d)
	if string(before) != string(after) {
		t.Fatal("second application changed the document")
	}
	// Round-trips.
	if _, err := procgen.DecodeDoc(after); err != nil {
		t.Fatal(err)
	}
	// A too-short stroke is refused.
	if _, _, _, err := AddPathStroke(d, FindPreset(ps, "dirt_trail"), [][2]float64{{0, 0}, {0.5, 0}}, 2); err == nil {
		t.Error("a 0.5 m stroke made a path")
	}
}

// orientTerrain is a 64 m terrain at 1 m sloping down toward +X, with a
// straight path's direction masks along Z at x = 0 (strength 1 within 2 m).
func orientTerrain() *terrain.Terrain {
	g := terrain.NewGrid(64, 1)
	s := g.S()
	dx, dz := make([]float32, len(g.H)), make([]float32, len(g.H))
	for k := range g.H {
		x := g.X(k % s)
		g.H[k] = float32(10 - 0.2*x)
		dx[k], dz[k] = 0.5, 0.5
		if math.Abs(x) <= 2 {
			dz[k] = 1
		}
	}
	meta := terrain.Meta{Footpaths: &terrain.FootpathsMeta{}}
	return terrain.FromResult(&terrain.Result{Grid: g, Masks: map[string][]float32{terrain.MaskPathDirX: dx, terrain.MaskPathDirZ: dz}}, meta, "")
}

func TestOrientToFollowsPathsLayersAndTheSlope(t *testing.T) {
	tr := orientTerrain()
	g, err := place.NewGround(tr, terrain.MaskPathDirX)
	if err != nil {
		t.Fatal(err)
	}
	d := &procgen.Doc{Name: "o"}
	// A hand-painted direction layer pointing +X everywhere near (20, 0).
	l := d.EnsurePaintLayer(paint.LayerDef{Name: "flow", Kind: paint.KindDirection})
	l.Apply(paint.Stroke{X: 20, Z: 0, Radius: 8, Strength: 1, Mode: paint.ModeSet, Dir: [2]float64{1, 0}})
	env := &procgen.Env{Doc: d}
	field := func(to string) func(x, z float64) (float64, float64, float64) {
		return orientField(env, tr, g, procgen.Params{"orient_to": procgen.Mask(to)})
	}
	near := func(a, b float64) bool { return math.Abs(math.Remainder(a-b, math.Pi)) < 1e-6 }
	// paths: along Z on the path, nothing off it.
	if x, z, s := field(OrientPaths)(0, 5); s < 0.99 || !near(math.Atan2(x, z), 0) {
		t.Fatalf("paths on the path: (%.2f, %.2f) %.2f", x, z, s)
	}
	if _, _, s := field(OrientPaths)(20, 5); s != 0 {
		t.Fatalf("paths off the path: strength %.2f", s)
	}
	// downhill: toward +X.
	if x, z, s := field(OrientDownhill)(10, 0); s < 0.99 || x < 0.99 || math.Abs(z) > 1e-6 {
		t.Fatalf("downhill (%.2f, %.2f) %.2f", x, z, s)
	}
	// A layer: its own vector off the path, the path's on it.
	if x, _, s := field("flow")(20, 0); s < 0.99 || x < 0.99 {
		t.Fatalf("layer off the path: x %.2f strength %.2f", x, s)
	}
	if x, z, _ := field("flow")(0, 0); math.Abs(x) > 1e-6 || z < 0.99 {
		t.Fatalf("layer under the path: (%.2f, %.2f), want the path's", x, z)
	}
	if field("") != nil {
		t.Fatal("no orient_to made a field")
	}
	// Placement: grass on the path all within the jitter of the heading
	// (mod π); off the field (strength 0) the yaws stay random.
	rule := place.GrassRule{Name: "g", Kinds: []int{0}, Density: 4, Scale: [2]float64{1, 1}, Band: place.DefaultRule(),
		Orient: field(OrientPaths), OrientJitter: 0.1}
	rule.Band.Slope = [2]float64{0, 90}
	ins := place.Grass(g, place.GrassSettings{Rules: []place.GrassRule{rule}, Seed: 3}, [4]float64{-1.5, -20, 1.5, 20})
	if len(ins) < 50 {
		t.Fatalf("%d clumps", len(ins))
	}
	for _, in := range ins {
		if d := math.Abs(math.Remainder(in.Yaw, math.Pi)); d > 0.1+1e-6 {
			t.Fatalf("a clump on the path is turned %.2f rad off it", d)
		}
	}
	plain := rule
	plain.Orient = nil
	a := place.Grass(g, place.GrassSettings{Rules: []place.GrassRule{rule}, Seed: 3}, [4]float64{10, -20, 20, 20})
	b := place.Grass(g, place.GrassSettings{Rules: []place.GrassRule{plain}, Seed: 3}, [4]float64{10, -20, 20, 20})
	if len(a) != len(b) {
		t.Fatalf("orientation changed the clumps: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Pos != b[i].Pos || a[i].Yaw != b[i].Yaw {
			t.Fatalf("clump %d off the field moved or turned", i)
		}
	}
}

func TestPathFordsARiverAndKeepsScatterOff(t *testing.T) {
	// A rock path across a shallow river: no sample of the channel is
	// flattened (the heights there are the river-only run's), stones stand
	// in the water as a ford, and a scatter excluding path_clear keeps its
	// rocks off the path.
	f, i, e, ref, v2, s := procgen.Float, procgen.Int, procgen.Enum, procgen.StageRef, procgen.Vec2, procgen.String
	build := func(withPath bool) *procgen.Doc {
		d := riverDoc()
		it := d.StageByID("edit").Params["rivers"].Items[0]
		v := it["path"].Clone()
		for _, q := range v.Points {
			q[3] = 0.45 // fordable
		}
		it["path"] = v
		d.Stages = append(d.Stages[:1], append([]procgen.Stage{{Kind: RocksKind, ID: "stones",
			Params: procgen.Params{"preset": e("boulder"), "variants": i(2), "triangles": i(200)}}}, d.Stages[1:]...)...)
		ed := d.StageByID("edit")
		ed.Params["path_rocks"] = ref("stones")
		if withPath {
			ed.Params["paths"] = procgen.List(procgen.Params{"name": s("ford"), "style": e(terrain.PathRock),
				"path": pathSpline([3]float64{-10, -40, 2}, [3]float64{-15, 10, 2}, [3]float64{-20, 50, 2})})
		}
		d.Stages = append(d.Stages, procgen.Stage{Kind: ScatterKind, ID: "rubble", Params: procgen.Params{"terrain": ref("edit"),
			"exclude": procgen.List(procgen.Params{"mask": s(terrain.MaskPathClear)}),
			"rules": procgen.List(procgen.Params{"name": s("rubble"), "rocks": ref("stones"), "density": f(200), "max_bury": f(2),
				"scale": v2(0.1, 0.2), "slope": v2(0, 90), "cliff": v2(0, 1), "cluster": f(0)})}})
		return d
	}
	r0, _ := logged(t, build(false), t.TempDir())
	dir := t.TempDir()
	r1, log := logged(t, build(true), dir)
	a0 := r0.Stage("edit").Artifact.(*terrain.Terrain)
	a1 := r1.Stage("edit").Artifact.(*terrain.Terrain)
	bed, err := a1.Mask(terrain.MaskRiverBed)
	if err != nil {
		t.Fatal(err)
	}
	inBed := 0
	for k, b := range bed {
		if b > 0.05 {
			inBed++
			if a1.Grid.H[k] != a0.Grid.H[k] {
				t.Fatalf("channel sample %d moved %.3f m with the path", k, a1.Grid.H[k]-a0.Grid.H[k])
			}
		}
	}
	tab, err := ReadGroundPaths(filepath.Join(dir, "edit", PathsFile))
	if err != nil || len(tab.Paths) != 1 {
		t.Fatalf("paths.yaml %v %+v", err, tab)
	}
	p := tab.Paths[0]
	if inBed == 0 || p.Crossings != 1 || p.Stones[terrain.StonesFord] < 2 || p.Stones[terrain.StonesFlag] < 50 {
		t.Fatalf("bed %d, path %+v\n%s", inBed, p, grepLines(log, "[edit]"))
	}
	// Rubble: plenty overall, none on the path's centreline.
	n, on := 0, 0
	for _, pl := range r1.Stage("rubble").Placements {
		n++
		x, z := float64(pl.Position.X), float64(pl.Position.Z)
		if c, _ := a1.MaskAt(terrain.MaskPathClear, x, z); c > 0.99 {
			on++
		}
	}
	if n < 100 || on > 0 {
		t.Fatalf("%d rubble rocks, %d on the path", n, on)
	}
}
