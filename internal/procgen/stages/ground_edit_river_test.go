package stages

// ground_edit_river_test.go — ground_edit's rivers end to end
// (ground_edit_river*.go, docs/terrain-paint/PHASE_W3.md): a painted river
// carved with its ribbons, masks and table; the artifact what Load reads;
// ribbons meeting across tiles without a crack; drainage rivers traced and
// one adopted; a river edit rerunning only what it reached downstream (and
// matching a full run); a document without rivers unchanged.

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

// riverPath is a spline value of (x, z, width, depth) points.
func riverPath(pts ...[4]float64) procgen.Value {
	var ps [][]float64
	for _, p := range pts {
		ps = append(ps, []float64{p[0], p[1], p[2], p[3]})
	}
	return procgen.Spline(ps...)
}

// riverDoc is gentleDoc's terrain with a ground_edit holding one painted
// river across the map, crossing tile edges.
func riverDoc() *procgen.Doc {
	d := gentleDoc()
	ed := d.StageByID("edit")
	ed.Params = procgen.Params{"terrain": procgen.StageRef("ground"),
		"rivers": procgen.List(procgen.Params{"name": procgen.String("brook"),
			"path": riverPath([4]float64{-115, 30, 5, 1}, [4]float64{-40, 10, 6, 1.2}, [4]float64{20, -15, 4, 1},
				[4]float64{115, -5, 6, 1.2})})}
	return d
}

func TestGroundEditCarvesARiver(t *testing.T) {
	d := riverDoc()
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	dir := t.TempDir()
	res, log := logged(t, d, dir)
	ed := res.Stage("edit")
	art := ed.Artifact.(*terrain.Terrain)
	rv, err := ReadGroundRivers(filepath.Join(dir, "edit", RiversFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(rv.Rivers) != 1 || rv.Rivers[0].Name != "brook" || rv.Rivers[0].Material != defaultRiverMaterial ||
		rv.Rivers[0].Kind != "painted" || rv.Rivers[0].Source < rv.Rivers[0].Mouth {
		t.Fatalf("rivers.yaml %+v\n%s", rv.Rivers, log)
	}
	if art.Meta.Rivers == nil || len(art.Meta.Rivers.Rivers) != 1 {
		t.Fatalf("terrain.yaml rivers %+v", art.Meta.Rivers)
	}
	// Ribbons wearing water_river, not solid, over several tiles.
	ribbons := 0
	for _, p := range ed.Placements {
		if strings.HasPrefix(p.ID, "river_") {
			ribbons++
			if p.Material != defaultRiverMaterial || p.Collision != "none" || p.Solid == nil || *p.Solid {
				t.Errorf("%s: material %s collision %s", p.ID, p.Material, p.Collision)
			}
		}
	}
	if ribbons < 3 {
		t.Errorf("%d ribbons for a river across the map", ribbons)
	}
	// The masks: water under the river, the flow map pointing downstream
	// (from the higher end to the lower), the bed and the raised flow.
	var x, z float64 = 20, -15
	if w, _ := art.MaskAt(terrain.MaskWater, x, z); w <= 0 {
		t.Errorf("water mask at the river %.3f", w)
	}
	if b, _ := art.MaskAt(terrain.MaskRiverBed, x, z); b < 0.9 {
		t.Errorf("bed mask %.3f", b)
	}
	if f, _ := art.MaskAt(terrain.MaskFlow, x, z); f < 0.85 {
		t.Errorf("flow mask in the channel %.3f", f)
	}
	fx, _ := art.MaskAt(terrain.MaskRiverFlowX, x, z)
	if math.Abs(float64(fx)-0.5) < 0.02 {
		t.Errorf("flow map still at the river: %.3f", fx)
	}
	// The artifact is what Load reads, river masks included.
	ld, err := terrain.Load(filepath.Join(dir, "edit"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range append([]string{terrain.MaskFlow, terrain.MaskWater}, terrain.RiverMaskNames...) {
		a, _ := art.Mask(name)
		b, err := ld.Mask(name)
		if err != nil || len(a) != len(b) {
			t.Fatalf("mask %s: %v (%d / %d)", name, err, len(a), len(b))
		}
		for k := range a {
			if a[k] != b[k] {
				t.Fatalf("mask %s differs at %d: %v vs %v", name, k, a[k], b[k])
			}
		}
	}
	for k := range art.Grid.H {
		if art.Grid.H[k] != ld.Grid.H[k] {
			t.Fatalf("height %d differs", k)
		}
	}
}

func TestRiverRibbonsMeetAcrossTiles(t *testing.T) {
	// The ribbon meshes of two neighbouring tiles share every vertex on
	// their common edge, at the same height: no crack before the cook (the
	// cook then locks the rectangle).
	g := terrain.NewGrid(64, 1)
	s := g.S()
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			g.H[j*s+i] = float32(5 - 0.03*g.X(i) + 0.1*math.Abs(g.Z(j)))
		}
	}
	sp := terrain.RiverSpec{Name: "r", Points: [][2]float64{{-30, 1}, {0, -2}, {30, 2}}, Width: []float64{5, 5, 5},
		Depth: []float64{1, 1, 1}, Shape: terrain.DefaultRiverShape(), Seed: 1}
	res := terrain.CarveRivers(g, []terrain.RiverSpec{sp}, nil, math.Inf(-1))
	rv := &groundRivers{res: res, mats: []string{"water_river"}, entries: []int{0}}
	tp := tilePlan{size: 32, count: 2, per: 32}
	cells := riverCells(g, rv, nil)
	byTile := map[[2]int][]int32{}
	for c := range cells {
		ci, cj := int(c)%g.N, int(c)/g.N
		k := [2]int{ci / tp.per, cj / tp.per}
		byTile[k] = append(byTile[k], c)
	}
	world := func(ti, tj int) map[[2]float32]float32 {
		cs := byTile[[2]int{ti, tj}]
		if len(cs) == 0 {
			t.Fatalf("tile %d,%d has no river", ti, tj)
		}
		m := riverTileMesh(g, tp, rv, cs, ti, tj)
		if len(m.Colors) != len(m.Positions) {
			t.Fatalf("%d colours for %d vertices", len(m.Colors), len(m.Positions))
		}
		cx := float32(g.MinX) + 32*(float32(ti)+0.5)
		cz := float32(g.MinZ) + 32*(float32(tj)+0.5)
		out := map[[2]float32]float32{}
		for _, p := range m.Positions {
			out[[2]float32{p.X() + cx, p.Z() + cz}] = p.Y()
		}
		return out
	}
	// The river crosses x = 0 (between tiles (0, *) and (1, *)) in both
	// rows of tiles or one; compare whichever pair holds it.
	pairs, total := 0, 0
	for tj := 0; tj < 2; tj++ {
		if len(byTile[[2]int{0, tj}]) == 0 || len(byTile[[2]int{1, tj}]) == 0 {
			continue
		}
		a, b := world(0, tj), world(1, tj)
		edge := 0
		for p, y := range a {
			if p[0] != 0 {
				continue
			}
			yb, ok := b[p]
			if !ok {
				t.Fatalf("vertex %v on the tile edge is in one tile only", p)
			}
			if yb != y {
				t.Fatalf("vertex %v: %.4f in one tile, %.4f in the other", p, y, yb)
			}
			edge++
		}
		for p := range b {
			if _, ok := a[p]; p[0] == 0 && !ok {
				t.Fatalf("vertex %v on the tile edge is in one tile only", p)
			}
		}
		total += edge
		pairs++
	}
	if pairs == 0 || total < 5 {
		t.Fatalf("the river crosses the tile edge in %d pairs, %d shared vertices", pairs, total)
	}
	// Vertex colours encode the flow downstream (+X): green above 0.5.
	m := riverTileMesh(g, tp, rv, byTile[[2]int{0, 0}], 0, 0)
	if len(m.Colors) == 0 {
		m = riverTileMesh(g, tp, rv, byTile[[2]int{0, 1}], 0, 1)
	}
	hi := 0
	for _, c := range m.Colors {
		if c[1] > 140 && c[3] == 255 {
			hi++
		}
	}
	if hi < len(m.Colors)/3 {
		t.Errorf("%d of %d vertices carry a downstream flow", hi, len(m.Colors))
	}
}

func TestRiverEditRebuildsOnlyWhatItReached(t *testing.T) {
	// A river point moved: the ground_edit recooks the tiles the channel
	// moved through, and the grass downstream rebuilds the tiles whose rect
	// key changed (heights, water, the river masks) and reuses the rest —
	// matching a run from nothing.
	withFakePolyHaven(t)
	d := riverDoc()
	d.Stages = append(d.Stages, grassDoc().Stages[1:]...)
	g := d.StageByID("grass")
	g.Params["terrain"] = procgen.StageRef("edit")
	g.Params["rules"].Items[0]["density"] = procgen.Float(0.5)
	log := incrementalMatchesFull(t, d, t.TempDir(), func(d *procgen.Doc) {
		it := d.StageByID("edit").Params["rivers"].Items[0]
		v := it["path"].Clone()
		v.Points[3][1] = 25 // the mouth swings north, in the east tiles only
		it["path"] = v
	}, "grass")
	t.Log(grepLines(log, "[grass] reused", "[edit] tiles", "[edit] river"))
	if !strings.Contains(log, "[grass] reused") || strings.Contains(log, "[grass] reused 16 of 16") ||
		strings.Contains(log, "[grass] reused 0 of 16") {
		t.Errorf("want some grass tiles reused and some rebuilt:\n%s", log)
	}
	if strings.Contains(log, "tiles: 0 recooked") || !strings.Contains(log, "reused from the last run") {
		t.Errorf("want the edit to recook some tiles and keep others:\n%s", log)
	}
}

func TestRiverEditMovesTheRectKey(t *testing.T) {
	// The rect key (what downstream pieces fold in) changes over the moved
	// river and nowhere far from it.
	d := riverDoc()
	dir := t.TempDir()
	res, _ := logged(t, d, dir)
	a := res.Stage("edit").Artifact.(*terrain.Terrain)
	near := a.RectKey(80, -20, 120, 30)
	far := a.RectKey(-120, -120, -80, -80)
	it := d.StageByID("edit").Params["rivers"].Items[0]
	v := it["path"].Clone()
	v.Points[3][1] = 25
	it["path"] = v
	res2, _ := logged(t, d, dir)
	b := res2.Stage("edit").Artifact.(*terrain.Terrain)
	if b.RectKey(80, -20, 120, 30) == near {
		t.Error("the rect key over the moved river did not change")
	}
	if b.RectKey(-120, -120, -80, -80) != far {
		t.Error("a rect key far from the river changed")
	}
	// And the base hash ignores the rivers (they are RectCovered).
	e1, err := procgen.PlanEnv(riverDoc(), "edit")
	if err != nil {
		t.Fatal(err)
	}
	e2, _ := procgen.PlanEnv(d, "edit")
	if e1.BaseHash != e2.BaseHash {
		t.Error("moving a river changed the stage's base hash")
	}
	h1, _ := procgen.StageHashes(riverDoc())
	h2, _ := procgen.StageHashes(d)
	if h1["edit"] == h2["edit"] {
		t.Error("moving a river did not change the stage's hash")
	}
}

func TestDrainageRiversAndAdopt(t *testing.T) {
	d := gentleDoc()
	ed := d.StageByID("edit")
	ed.Params = procgen.Params{"terrain": procgen.StageRef("ground"), "drainage": procgen.Bool(true),
		"drainage_threshold": procgen.Float(0.5), "drainage_min_length": procgen.Float(40)}
	dir := t.TempDir()
	res, log := logged(t, d, dir)
	rv, err := ReadGroundRivers(filepath.Join(dir, "edit", RiversFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(rv.Traced) == 0 || len(rv.Rivers) == 0 {
		t.Fatalf("nothing traced:\n%s", log)
	}
	for _, r := range rv.Rivers {
		if r.Kind != "drainage" || r.Entry != -1 {
			t.Errorf("river %+v", r)
		}
	}
	if res.Stage("edit").Artifact.(*terrain.Terrain).Meta.Rivers == nil {
		t.Error("no rivers section")
	}
	// Adopt the first traced river: it becomes a hand river and is no
	// longer traced.
	tr := rv.Traced[0]
	var pts [][4]float64
	for i, p := range tr.Points {
		pts = append(pts, [4]float64{p[0], p[1], tr.Width[i], tr.Depth[i]})
	}
	ed.Params["rivers"] = procgen.List(procgen.Params{"name": procgen.String("adopted"), "from": procgen.Enum("adopted"),
		"path": riverPath(pts...)})
	logged(t, d, dir)
	rv2, err := ReadGroundRivers(filepath.Join(dir, "edit", RiversFile))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rv2.Rivers {
		if r.Name == "adopted" {
			found = r.Kind == "adopted" && r.Entry == 0
		}
	}
	if !found {
		t.Fatalf("adopted river missing: %+v", rv2.Rivers)
	}
	for _, t2 := range rv2.Traced {
		if len(t2.Points) == len(tr.Points) && t2.Points[0] == tr.Points[0] && t2.Points[len(t2.Points)-1] == tr.Points[len(tr.Points)-1] {
			t.Errorf("the adopted river is still traced as %s", t2.Name)
		}
	}
}

func TestGroundEditWithoutRiversWritesNoRiverOutput(t *testing.T) {
	d := gentleDoc()
	paintGentle(d)
	dir := t.TempDir()
	res, _ := logged(t, d, dir)
	art := res.Stage("edit").Artifact.(*terrain.Terrain)
	if art.Meta.Rivers != nil {
		t.Error("a rivers section without rivers")
	}
	if _, err := os.Stat(filepath.Join(dir, "edit", RiversFile)); err == nil {
		t.Error("rivers.yaml without rivers")
	}
	for _, m := range art.Meta.Masks {
		if strings.HasPrefix(m.Name, "river_") {
			t.Errorf("mask %s without rivers", m.Name)
		}
	}
	for _, p := range res.Stage("edit").Placements {
		if strings.HasPrefix(p.ID, "river_") || strings.HasPrefix(p.ID, "boulder_") {
			t.Errorf("placement %s without rivers", p.ID)
		}
	}
	// The stage's hash does not see the river params at their defaults.
	k := procgen.KindByName(GroundEditKind)
	p := procgen.Resolve(k.Params, d.StageByID("edit").Params)
	if procgen.CoveredHash(k, p) != "" {
		t.Error("covered hash of a ground_edit without rivers is not empty")
	}
}

func TestRiverThroughAPondKeepsThePond(t *testing.T) {
	// The pond a river runs through keeps its body, area and level; the
	// river into it ends at that level and the one out of it starts there.
	pondDoc := func(withRiver bool) *procgen.Doc {
		d := gentleDoc()
		paintGentle(d)
		if withRiver {
			d.StageByID("edit").Params["rivers"] = procgen.List(procgen.Params{"name": procgen.String("through"),
				"path": riverPath([4]float64{-120, -70, 4, 1}, [4]float64{-60, -60, 4, 1}, [4]float64{0, -50, 4, 1})})
		}
		return d
	}
	body := func(withRiver bool) (GroundBody, string) {
		dir := t.TempDir()
		logged(t, pondDoc(withRiver), dir)
		b, err := ReadGroundBodies(filepath.Join(dir, "edit", BodiesFile))
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range b.Bodies {
			if math.Hypot(x.Anchor[0]+60, x.Anchor[1]+60) < 10 {
				return x, dir
			}
		}
		t.Fatalf("no pond at (-60, -60): %+v", b.Bodies)
		return GroundBody{}, ""
	}
	without, _ := body(false)
	with, dir := body(true)
	if math.Abs(with.Level-without.Level) > 1e-3 || with.Area < 0.97*without.Area || with.MaxDepth < 0.9*without.MaxDepth {
		t.Fatalf("pond without a river %+v, with one %+v", without, with)
	}
	rv, err := ReadGroundRivers(filepath.Join(dir, "edit", RiversFile))
	if err != nil {
		t.Fatal(err)
	}
	var in, out bool
	for _, r := range rv.Rivers {
		switch r.Name {
		case "through":
			in = math.Abs(r.Mouth-with.Level) < 2e-3
		case "through.2":
			out = math.Abs(r.Source-with.Level) < 2e-3
		}
	}
	if !in || !out {
		t.Fatalf("river ends %+v, pond level %.3f", rv.Rivers, with.Level)
	}
}
