package stages

// ground_edit_test.go — the ground_edit stage end to end: ponds carved with
// their water at the spill height or a pinned level, height layers, the
// artifact being what Load reads back, downstream stages reading the edited
// ground, and a stroke's incremental rerun matching a run from nothing.

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
	"foundry/internal/procgen/terrain"
)

// gentleDoc is a 256 m gently rolling terrain at 1 m in 64 m tiles, and a
// ground_edit over it with a pond layer and a height layer.
func gentleDoc() *procgen.Doc {
	f, i, ref := procgen.Float, procgen.Int, procgen.StageRef
	tp := plainTerrain(procgen.Params{"size": f(256), "resolution": f(1), "tile_size": f(64),
		"base_height": f(10), "relief": f(3), "frequency": f(0.008), "octaves": i(3),
		"drainage_iterations": i(4), "droplets": f(0), "talus_iterations": i(4)})
	return &procgen.Doc{Name: "ge", Seed: 11, Stages: []procgen.Stage{
		{Kind: TerrainKind, ID: "ground", Params: tp},
		{Kind: GroundEditKind, ID: "edit", Params: procgen.Params{"terrain": ref("ground"),
			"height": procgen.List(procgen.Params{"layer": procgen.Mask("lift")}),
			"water":  procgen.List(procgen.Params{"layer": procgen.Mask("pond")})}},
	}}
}

func paintGentle(d *procgen.Doc) {
	paintDisc(d, "pond", paint.KindWater, -60, -60, 16)
	paintDisc(d, "pond", paint.KindWater, 70, 50, 10)
	d.EnsurePaintLayer(paint.LayerDef{Name: "lift", Kind: paint.KindHeight})
}

func TestGroundEditCarvesPondsAtTheirSpillHeight(t *testing.T) {
	d := gentleDoc()
	paintGentle(d)
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	dir := t.TempDir()
	res, log := logged(t, d, dir)
	src, err := terrain.Load(filepath.Join(dir, "ground"))
	if err != nil {
		t.Fatal(err)
	}
	ed := res.Stage("edit")
	art := ed.Artifact.(*terrain.Terrain)
	bodies, err := ReadGroundBodies(filepath.Join(dir, "edit", BodiesFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies.Bodies) != 2 {
		t.Fatalf("want 2 bodies, got %d\n%s", len(bodies.Bodies), log)
	}
	for _, b := range bodies.Bodies {
		if math.Abs(b.Level-(b.Spill-0.05)) > 1e-3 || b.Locked {
			t.Errorf("body at %v: level %.3f, spill %.3f: want spill - freeboard", b.Anchor, b.Level, b.Spill)
		}
		if b.Area <= 0 || b.MaxDepth < 0.3 || b.Material != "water_lake" {
			t.Errorf("body at %v: area %.0f, depth %.2f, material %s", b.Anchor, b.Area, b.MaxDepth, b.Material)
		}
		// The bed under the anchor is well below the water, and the ground
		// round the outline never below it (the water cannot run out).
		x, z := b.Anchor[0], b.Anchor[1]
		if h := float64(art.HeightAt(x, z)); h > b.Level-0.3 {
			t.Errorf("body at %v: bed %.2f, level %.2f", b.Anchor, h, b.Level)
		}
	}
	// A big pond's rim: every sample just outside its outline stands above
	// the water.
	big := bodies.Bodies[0]
	if big.Painted < bodies.Bodies[1].Painted {
		big = bodies.Bodies[1]
	}
	for a := 0.0; a < 2*math.Pi; a += 0.1 {
		x, z := -60+18*math.Cos(a), -60+18*math.Sin(a)
		if h := float64(art.HeightAt(x, z)); h < big.Level {
			t.Errorf("rim at angle %.1f: ground %.3f under the water %.3f", a, h, big.Level)
		}
	}
	// Water placements, the tiles under the ponds recooked, the rest the
	// source's own.
	var water, own, theirs int
	for _, p := range ed.Placements {
		switch {
		case strings.HasPrefix(p.ID, "pond_"):
			water++
			if p.Material != "water_lake" || p.Collision != "none" {
				t.Errorf("%s: material %s, collision %s", p.ID, p.Material, p.Collision)
			}
		case strings.Contains(p.Model, "/edit/"):
			own++
		default:
			theirs++
		}
	}
	if water == 0 || own == 0 || theirs == 0 || own+theirs != 16 {
		t.Errorf("water %d, recooked %d, kept %d of 16 tiles", water, own, theirs)
	}
	if n := len(ed.Supersedes["ground"]); n != 16 {
		t.Errorf("supersedes %d of the source's placements, want all 16", n)
	}
	// The water mask holds the depth.
	if v, err := art.MaskAt(terrain.MaskWater, big.Anchor[0], big.Anchor[1]); err != nil || v <= 0 {
		t.Errorf("water mask at the anchor: %v, %v", v, err)
	}
	if art.Meta.Water == nil || len(art.Meta.Water.Bodies) != 2 || art.Meta.GroundEdit == nil {
		t.Errorf("terrain.yaml water %+v, ground_edit %+v", art.Meta.Water, art.Meta.GroundEdit)
	}
	// Far from the ponds, the ground is the source's (within a quantum: a
	// bed below the source's lowest point widens the heightmap's range).
	if a, b := art.HeightAt(0, 100), src.HeightAt(0, 100); math.Abs(float64(a-b)) > 1e-3 {
		t.Errorf("untouched ground moved: %v vs %v", a, b)
	}
	// The artifact is what Load reads back.
	back, err := terrain.Load(filepath.Join(dir, "edit"))
	if err != nil {
		t.Fatal(err)
	}
	for k := range back.Grid.H {
		if back.Grid.H[k] != art.Grid.H[k] {
			t.Fatalf("sample %d: loaded %v, in memory %v", k, back.Grid.H[k], art.Grid.H[k])
		}
	}
	for _, m := range []string{terrain.MaskSlope, terrain.MaskCurvature, terrain.MaskWater, terrain.MaskFlow} {
		a, _ := art.Mask(m)
		b, err := back.Mask(m)
		if err != nil {
			t.Fatal(err)
		}
		for k := range a {
			if a[k] != b[k] {
				t.Fatalf("mask %s sample %d: loaded %v, in memory %v", m, k, b[k], a[k])
			}
		}
	}
}

func TestGroundEditPinnedLevelAndStableKeys(t *testing.T) {
	d := gentleDoc()
	paintGentle(d)
	dir := t.TempDir()
	logged(t, d, dir)
	bodies, err := ReadGroundBodies(filepath.Join(dir, "edit", BodiesFile))
	if err != nil {
		t.Fatal(err)
	}
	var b0 GroundBody
	for _, b := range bodies.Bodies {
		if b.Anchor[0] < 0 {
			b0 = b
		}
	}
	// Pin the big pond 0.4 m above its spill height (a berm holds it) and
	// give it the swamp preset.
	want := b0.Spill + 0.4
	ed := d.StageByID("edit")
	ed.Params["bodies"] = procgen.List(procgen.Params{"at": procgen.Vec2(b0.Anchor[0], b0.Anchor[1]), "level": procgen.Float(want),
		"material": procgen.String("water_swamp")})
	// And repaint its edge: the key still finds it.
	d.PaintLayer("pond").Apply(paint.Stroke{X: -45, Z: -60, Radius: 6, Strength: 1})
	res, log := logged(t, d, dir)
	bodies, err = ReadGroundBodies(filepath.Join(dir, "edit", BodiesFile))
	if err != nil {
		t.Fatal(err)
	}
	art := res.Stage("edit").Artifact.(*terrain.Terrain)
	found := false
	for _, b := range bodies.Bodies {
		if b.Anchor[0] > 0 {
			continue
		}
		found = true
		if !b.Locked || math.Abs(b.Level-want) > 1e-3 || b.Material != "water_swamp" || b.Entry != 0 {
			t.Errorf("pinned body: %+v (want level %.3f)\n%s", b, want, log)
		}
		for a := 0.0; a < 2*math.Pi; a += 0.1 {
			x, z := -60+16.8*math.Cos(a), -60+16.8*math.Sin(a)
			if a < 0.6 || a > 2*math.Pi-0.6 {
				continue // the repainted bulge
			}
			if h := float64(art.HeightAt(x, z)); h < want {
				t.Errorf("berm at angle %.1f: %.3f under the pinned water %.3f", a, h, want)
			}
		}
	}
	if !found || len(bodies.Orphans) != 0 {
		t.Errorf("found %v, orphans %v", found, bodies.Orphans)
	}
	var swamp bool
	for _, p := range res.Stage("edit").Placements {
		swamp = swamp || p.Material == "water_swamp"
	}
	if !swamp {
		t.Error("no water surface wears the body's own material")
	}
	// Painting the key's point dry orphans the entry.
	d.PaintLayer("pond").Apply(paint.Stroke{X: -60, Z: -60, Radius: 30, Strength: 1, Mode: paint.ModeErase})
	logged(t, d, dir)
	bodies, _ = ReadGroundBodies(filepath.Join(dir, "edit", BodiesFile))
	if len(bodies.Orphans) != 1 || len(bodies.Bodies) != 1 {
		t.Errorf("after erasing: %d bodies, orphans %v", len(bodies.Bodies), bodies.Orphans)
	}
}

func TestGroundEditHeightLayerAndFlatten(t *testing.T) {
	d := gentleDoc()
	paintGentle(d)
	dir := t.TempDir()
	logged(t, d, dir)
	src, _ := terrain.Load(filepath.Join(dir, "ground"))
	// Raise a mound, then flatten a terrace to 12 m.
	l := d.PaintLayer("lift")
	l.Apply(paint.Stroke{X: 40, Z: -40, Radius: 10, Strength: 2})
	base, id, err := HeightLayerBase(d, dir, "lift")
	if err != nil || id != "edit" {
		t.Fatalf("base: %v %s", err, id)
	}
	l.Apply(paint.Stroke{X: -20, Z: 60, Radius: 12, Strength: 1, Mode: paint.ModeFlatten, Value: 12, Base: base})
	res, _ := logged(t, d, dir)
	art := res.Stage("edit").Artifact.(*terrain.Terrain)
	if dh := art.HeightAt(40, -40) - src.HeightAt(40, -40); math.Abs(float64(dh)-2) > 0.02 {
		t.Errorf("mound: raised %.3f m, want 2", dh)
	}
	for _, p := range [][2]float64{{-20, 60}, {-15, 58}, {-24, 65}} {
		if h := art.HeightAt(p[0], p[1]); math.Abs(float64(h)-12) > 0.02 {
			t.Errorf("terrace at %v: %.3f m, want 12", p, h)
		}
	}
}

func TestGroundEditDownstreamReadsTheEditedGround(t *testing.T) {
	withFakePolyHaven(t)
	d := gentleDoc()
	paintGentle(d)
	gd := grassDoc()
	d.Stages = append(d.Stages, gd.Stages[1:]...)
	g := d.StageByID("grass")
	g.Params["terrain"] = procgen.StageRef("edit")
	g.Params["rules"].Items[0]["density"] = procgen.Float(1)
	dir := t.TempDir()
	res, log := logged(t, d, dir)
	art := res.Stage("edit").Artifact.(*terrain.Terrain)
	bodies, _ := ReadGroundBodies(filepath.Join(dir, "edit", BodiesFile))
	if len(bodies.Bodies) == 0 {
		t.Fatal("no bodies")
	}
	// Every clump stands on the edited ground and none in the water.
	n, in := 0, 0
	for _, c := range grassFieldClumps(t, res, "grass") {
		n++
		x, z := float64(c.Pos[0]), float64(c.Pos[2])
		h := art.HeightAt(x, z)
		if math.Abs(float64(c.Pos[1]-h)) > 0.05 {
			t.Fatalf("clump at %v stands at %.3f, the edited ground at %.3f", c.Pos, c.Pos[1], h)
		}
		if w, _ := art.MaskAt(terrain.MaskWater, x, z); w > 0.02 {
			in++
		}
	}
	if n == 0 || in > 0 {
		t.Errorf("%d clumps, %d in the water\n%s", n, in, log)
	}
}

func TestGroundEditIncrementalMatchesFull(t *testing.T) {
	d := gentleDoc()
	paintGentle(d)
	log := incrementalMatchesFull(t, d, t.TempDir(), func(d *procgen.Doc) {
		d.PaintLayer("pond").Apply(paint.Stroke{X: 75, Z: 55, Radius: 6, Strength: 1})
	}, "edit")
	if !strings.Contains(log, "reused from the last run") || strings.Contains(log, "tiles: 0 recooked") {
		t.Errorf("want some tiles reused and some recooked:\n%s", log)
	}
}

func TestDocWithoutGroundEditIsUnchanged(t *testing.T) {
	// The golden test pins the terrain stage itself; here, that nothing a
	// ground_edit adds leaks into a terrain's output.
	d := gentleDoc()
	d.Stages = d.Stages[:1]
	dir := t.TempDir()
	logged(t, d, dir)
	data, err := os.ReadFile(filepath.Join(dir, "ground", terrain.MetaFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "ground_edit") {
		t.Error("a plain terrain.yaml has a ground_edit section")
	}
}

func TestGrassOnGroundEditIncrementalMatchesFull(t *testing.T) {
	// A pond stroke changes heights only round the pond: the grass reruns
	// the tiles whose heights moved (procgen.RectKeyer) and matches a run
	// from nothing.
	withFakePolyHaven(t)
	d := gentleDoc()
	paintGentle(d)
	d.Stages = append(d.Stages, grassDoc().Stages[1:]...)
	g := d.StageByID("grass")
	g.Params["terrain"] = procgen.StageRef("edit")
	g.Params["rules"].Items[0]["density"] = procgen.Float(0.5)
	log := incrementalMatchesFull(t, d, t.TempDir(), func(d *procgen.Doc) {
		d.PaintLayer("pond").Apply(paint.Stroke{X: 75, Z: 55, Radius: 6, Strength: 1})
	}, "grass")
	t.Log(grepLines(log, "[grass] reused", "[edit] tiles"))
	if !strings.Contains(log, "[grass] reused") || strings.Contains(log, "[grass] reused 16 of 16") ||
		strings.Contains(log, "[grass] reused 0 of 16") {
		t.Errorf("want some grass tiles reused and some rebuilt:\n%s", log)
	}
}

// grepLines is the lines of log holding any of subs.
func grepLines(log string, subs ...string) string {
	var out []string
	for _, l := range strings.Split(log, "\n") {
		for _, s := range subs {
			if strings.Contains(l, s) {
				out = append(out, l)
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

func TestMaterialOnGroundEditIncrementalMatchesFull(t *testing.T) {
	// The material paints mud by the pond layer AND reads the ground the
	// ground_edit carved from it: a pond stroke must remake the paint-free
	// weights (the slopes and the wet cover moved), not reuse them.
	_, res := withFakePolyHaven(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	d := gentleDoc()
	paintGentle(d)
	ref, s := procgen.StageRef, procgen.String
	d.Stages = append(d.Stages,
		procgen.Stage{Kind: PolyHavenTexturesKind, ID: "tex", Params: procgen.Params{
			"layers": procgen.List(procgen.Params{"name": s("sand"), "id": s("sand")})}},
		procgen.Stage{Kind: TerrainMaterialKind, ID: "ground_mat", Params: procgen.Params{
			"terrain": ref("edit"), "textures": ref("tex"), "sand": s("sand"), "cracked": s("sand"), "gravel": s("sand"),
			"rock": s("sand"), "wet": s("sand"),
			"paint": procgen.List(procgen.Params{"layer": procgen.Mask("pond"), "target": s("wet")})}})
	log := incrementalMatchesFull(t, d, filepath.Join(res, "generated"), func(d *procgen.Doc) {
		d.PaintLayer("pond").Apply(paint.Stroke{X: 75, Z: 55, Radius: 6, Strength: 1})
	}, "ground_mat")
	if strings.Contains(log, "[ground_mat] unpainted weights reused") {
		t.Errorf("the paint-free weights were reused though the ground moved; log:\n%s", log)
	}
}

func TestGroundEditOverARegionTerrain(t *testing.T) {
	// A pond carved into a region terrain, the region material standing on
	// the ground_edit: the material follows it to its regions, the tiles
	// wear the material it writes, and the pond's water is there.
	d := twoBiomeWorld(t, [4]float64{1, 5, 6, 7}, procgen.Params{"cook": procgen.Bool(true)})
	d.Stages = append(d.Stages[:len(d.Stages)-1], procgen.Stage{Kind: GroundEditKind, ID: "edit", Params: procgen.Params{
		"terrain": procgen.StageRef("ground"), "water": procgen.List(procgen.Params{"layer": procgen.Mask("pond")})}},
		procgen.Stage{Kind: RegionMaterialKind, ID: "ground_mat", Params: procgen.Params{"terrain": procgen.StageRef("edit"),
			"per_tile": procgen.Bool(true)}})
	paintDisc(d, "pond", paint.KindWater, 60, 0, 14)
	r, a, _, _ := runRegionMaterialDoc(t, d)
	ed := r.Stage("edit")
	art := ed.Artifact.(*terrain.Terrain)
	if art.Meta.Biomes == nil || art.Meta.Water == nil || len(art.Meta.Water.Bodies) != 1 {
		t.Fatalf("edited terrain: biomes %v, water %+v", art.Meta.Biomes != nil, art.Meta.Water)
	}
	tiles, water := 0, 0
	for _, p := range r.Placements() {
		switch {
		case strings.Contains(p.ID, ".edit.tile_"):
			tiles++
			if !strings.HasPrefix(p.Material, a.Material) {
				t.Errorf("%s wears %q, want the region material's %s", p.ID, p.Material, a.Material)
			}
		case strings.Contains(p.ID, ".edit.pond_"):
			water++
		case strings.Contains(p.ID, ".ground.tile_"):
			t.Errorf("the source's tile %s was not superseded", p.ID)
		}
	}
	if tiles == 0 || water == 0 {
		t.Errorf("%d tiles, %d water surfaces", tiles, water)
	}
}
