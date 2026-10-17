package stages

// ground_edit_falls_test.go — phase W4 in ground_edit (ground_edit_falls.go,
// ground_obstacles.go; docs/terrain-paint/PHASE_W4.md): a fall gets its
// sheet and mist placed under the stage's prefix, the pool foams round the
// landing, an edit reruns to the same bytes as a full run, a document
// without falls places none, and a scatter over the edited ground keeps
// off the boulders the rivers dropped.

import (
	"math"
	"strings"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
	"foundry/internal/procgen/terrain"
)

// fallsDoc is riverDoc with the ground east of x ≈ 20 sunk 5 m by a hard
// height stroke, so the brook drops over a cliff there.
func fallsDoc() *procgen.Doc {
	d := riverDoc()
	ed := d.StageByID("edit")
	ed.Params["height"] = procgen.List(procgen.Params{"layer": procgen.Mask("drop")})
	d.EnsurePaintLayer(paint.LayerDef{Name: "drop", Kind: paint.KindHeight}).Apply(paint.Stroke{X: 95, Z: -10, Radius: 75,
		Strength: 1, Falloff: 0, Mode: paint.ModeSet, Value: -5})
	return d
}

func TestGroundEditPlacesWaterfallSheetsAndMist(t *testing.T) {
	d := fallsDoc()
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	res, log := logged(t, d, t.TempDir())
	ed := res.Stage("edit")
	art := ed.Artifact.(*terrain.Terrain)
	if art.Meta.Rivers == nil || len(art.Meta.Rivers.Falls) == 0 {
		t.Fatalf("no waterfall\n%s", grepLines(log, "[edit]"))
	}
	falls := art.Meta.Rivers.Falls
	// One sheet per fall, wearing water_falls, walk-through.
	sheets := map[string]bool{}
	for _, p := range ed.Placements {
		if !strings.HasPrefix(p.ID, "falls_") {
			continue
		}
		sheets[p.ID] = true
		if p.Material != defaultFallsMaterial || p.Collision != "none" || p.Solid == nil || *p.Solid ||
			!strings.HasSuffix(p.Model, ".fnm") {
			t.Errorf("%s: %+v", p.ID, p)
		}
	}
	if len(sheets) != len(falls) || !sheets["falls_brook_1"] {
		t.Fatalf("sheets %v for %d falls", sheets, len(falls))
	}
	// Mist at each foot: the shipped effect, a little above the pool, out
	// past the lip along the heading, under the stage's prefix in the
	// result.
	f := falls[0]
	n := 0
	for _, e := range ed.Emitters {
		if !strings.HasPrefix(e.ID, "mist_brook_1_") {
			continue
		}
		n++
		if e.Effect != defaultFallsMist || math.Abs(float64(e.Position.Y)-(f.Foot+0.15)) > 0.02 || e.RateScale <= 0 {
			t.Errorf("mist %+v (foot %.2f)", e, f.Foot)
		}
		along := (float64(e.Position.X)-f.At[0])*f.Dir[0] + (float64(e.Position.Z)-f.At[1])*f.Dir[1]
		if along < 0.3 || along > 0.5*(f.Lip-f.Foot)+1.5 {
			t.Errorf("mist %s %.2f m past the lip", e.ID, along)
		}
	}
	if n == 0 {
		t.Fatalf("no mist at the fall; emitters %+v", ed.Emitters)
	}
	prefixed := 0
	for _, e := range res.Emitters() {
		if strings.HasPrefix(e.ID, procgen.StagePrefix(d.Name, "edit")+"mist_") {
			prefixed++
		}
	}
	if prefixed != len(ed.Emitters) {
		t.Errorf("%d of %d emitters prefixed", prefixed, len(ed.Emitters))
	}
	// The pool foams round where the sheet lands.
	c := terrain.Waterfall{X: f.At[0], Z: f.At[1], Lip: f.Lip, Foot: f.Foot, DirX: f.Dir[0], DirZ: f.Dir[1], Width: f.Width}.Curtain(1)
	lx, lz := f.At[0]+f.Dir[0]*c.Out, f.At[1]+f.Dir[1]*c.Out
	if fo, _ := art.MaskAt(terrain.MaskRiverFoam, lx, lz); fo < 0.6 {
		t.Errorf("foam %.2f where the sheet lands", fo)
	}
}

func TestFallsMeshCarriesTheSheetData(t *testing.T) {
	f := terrain.Waterfall{X: 10, Z: 5, Lip: 4, Foot: 1, DirX: 1, Width: 3, Speed: 1}
	c := f.Curtain(1)
	org := [3]float64{f.X, f.Foot, f.Z}
	m := fallsMesh(&c, org)
	if len(m.Colors) != c.Rows*c.Cols || len(m.Positions) != c.Rows*c.Cols || len(m.Indices) != 6*(c.Rows-1)*(c.Cols-1) {
		t.Fatalf("mesh %d verts %d colours %d indices for %d×%d", len(m.Positions), len(m.Colors), len(m.Indices), c.Rows, c.Cols)
	}
	// Relative to the placement: the lip's centre at (0, lip − foot, 0),
	// give or take the lip's unevenness (fixes after W5).
	mid := m.Positions[c.Cols+c.Cols/2]
	if math.Abs(float64(mid.X())) > 1e-4 || math.Abs(float64(mid.Y())-3) > 0.06 || math.Abs(float64(mid.Z())) > 1e-4 {
		t.Fatalf("lip centre %v", mid)
	}
	// Red the aeration, green the edge, blue the progress; alpha opaque.
	foot := m.Colors[(c.Rows-1)*c.Cols+c.Cols/2]
	side := m.Colors[c.Cols]
	if foot[0] < 230 || foot[2] != 255 || foot[3] != 255 || side[1] != 0 || m.Colors[c.Cols+c.Cols/2][1] != 255 {
		t.Fatalf("colours foot %v side %v", foot, side)
	}
}

func TestFallsEditRebuildsToTheFullRun(t *testing.T) {
	// The brook's source moved: the ground_edit's files (tiles, ribbons,
	// sheets) match a run from nothing.
	d := fallsDoc()
	incrementalMatchesFull(t, d, t.TempDir(), func(d *procgen.Doc) {
		it := d.StageByID("edit").Params["rivers"].Items[0]
		v := it["path"].Clone()
		v.Points[0][1] = 38
		it["path"] = v
	}, "edit")
}

func TestGroundEditWithoutFallsPlacesNoSheet(t *testing.T) {
	res, _ := logged(t, riverDoc(), t.TempDir())
	ed := res.Stage("edit")
	art := ed.Artifact.(*terrain.Terrain)
	if art.Meta.Rivers != nil && len(art.Meta.Rivers.Falls) > 0 {
		t.Skip("riverDoc grew a fall")
	}
	for _, p := range ed.Placements {
		if strings.HasPrefix(p.ID, "falls_") {
			t.Errorf("sheet %s without a fall", p.ID)
		}
	}
	if len(ed.Emitters) > 0 {
		t.Errorf("emitters %+v without a fall", ed.Emitters)
	}
}

func TestScatterKeepsOffTheRiverBoulders(t *testing.T) {
	f, i, e, ref, v2, s := procgen.Float, procgen.Int, procgen.Enum, procgen.StageRef, procgen.Vec2, procgen.String
	d := riverDoc()
	d.Stages = append(d.Stages[:1], append([]procgen.Stage{{Kind: RocksKind, ID: "stones",
		Params: procgen.Params{"preset": e("boulder"), "variants": i(2), "triangles": i(200)}}}, d.Stages[1:]...)...)
	ed := d.StageByID("edit")
	ed.Params["river_rocks"] = ref("stones")
	ed.Params["rivers"].Items[0]["boulders"] = f(4)
	d.Stages = append(d.Stages, procgen.Stage{Kind: ScatterKind, ID: "rubble", Params: procgen.Params{"terrain": ref("edit"),
		"rules": procgen.List(procgen.Params{"name": s("rubble"), "rocks": ref("stones"), "density": f(400), "max_bury": f(2),
			"scale": v2(0.1, 0.2), "slope": v2(0, 90), "cliff": v2(0, 1), "cluster": f(0)})}})
	res, log := logged(t, d, t.TempDir())
	art := res.Stage("edit").Artifact.(*terrain.Terrain)
	if len(art.Meta.Obstacles) < 3 {
		t.Fatalf("obstacles %v\n%s", art.Meta.Obstacles, grepLines(log, "[edit]"))
	}
	boulders := 0
	for _, p := range res.Stage("edit").Placements {
		if strings.HasPrefix(p.ID, "boulder_") {
			boulders++
		}
	}
	if boulders != len(art.Meta.Obstacles) {
		t.Errorf("%d boulders, %d obstacles", boulders, len(art.Meta.Obstacles))
	}
	n := 0
	for _, pl := range res.Stage("rubble").Placements {
		n++
		x, z := float64(pl.Position.X), float64(pl.Position.Z)
		for _, o := range art.Meta.Obstacles {
			if math.Hypot(x-o[0], z-o[1]) < o[2]*0.9 {
				t.Fatalf("rubble %s at (%.2f, %.2f) inside boulder %v", pl.ID, x, z, o)
			}
		}
	}
	if n < 100 {
		t.Fatalf("%d rubble rocks", n)
	}
}
