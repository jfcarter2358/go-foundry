package stages

// paint_incremental_test.go — a stroke and an incremental rerun give
// exactly what a run from nothing gives, for every stage that reruns per
// tile (grass_field, scatter, terrain_material, region_material), and they
// really do reuse the tiles the stroke did not reach.

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
)

// paintWorldDoc is a 256 m terrain in 64 m tiles: 4×4 terrain tiles, each
// exactly one paint tile.
func paintWorldDoc() *procgen.Doc {
	d := smallTerrainDoc()
	p := d.Stages[0].Params
	p["size"], p["resolution"], p["tile_size"] = procgen.Float(256), procgen.Float(1), procgen.Float(64)
	return d
}

// logged runs d into dir and returns the result and its log.
func logged(t *testing.T, d *procgen.Doc, dir string) (*procgen.Result, string) {
	t.Helper()
	var mu sync.Mutex
	var b strings.Builder
	res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir, Workers: 4, Log: func(l string) {
		mu.Lock()
		b.WriteString(l + "\n")
		mu.Unlock()
	}})
	if err != nil {
		t.Fatalf("run: %v\n%s", err, b.String())
	}
	return res, b.String()
}

// stageFiles is every file in stage's folder under root, by its name there.
func stageFiles(root, stage string) map[string][]byte {
	out := map[string][]byte{}
	dir := filepath.Join(root, stage)
	filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out[filepath.ToSlash(rel)], _ = os.ReadFile(p)
		}
		return nil
	})
	return out
}

// sameStageFiles fails unless the two file sets hold the same names and bytes.
func sameStageFiles(t *testing.T, stage string, fa, fb map[string][]byte) {
	t.Helper()
	var names []string
	for n := range fa {
		names = append(names, n)
	}
	for n := range fb {
		if _, ok := fa[n]; !ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	bad := 0
	for _, n := range names {
		x, okx := fa[n]
		y, oky := fb[n]
		switch {
		case !okx || !oky:
			t.Errorf("%s/%s: only in the %s run", stage, n, map[bool]string{true: "incremental", false: "full"}[okx])
			bad++
		case !bytes.Equal(x, y):
			t.Errorf("%s/%s differs (%d vs %d bytes)", stage, n, len(x), len(y))
			bad++
		}
		if bad > 8 {
			t.Fatal("too many differences")
		}
	}
	if len(names) == 0 {
		t.Fatalf("stage %s wrote nothing", stage)
	}
}

// forgetStage drops stage's manifest entry and folder, so the next run
// makes it from nothing while every other stage stays as it is.
func forgetStage(t *testing.T, dir, stage string) {
	t.Helper()
	m := procgen.ReadManifest(dir)
	delete(m.Stages, stage)
	if err := m.Write(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, stage)); err != nil {
		t.Fatal(err)
	}
}

// incrementalMatchesFull runs d, makes stage again from nothing (so it reads
// its upstream back from their files: procgen's upstreamLineage — a fresh
// upstream is not a loaded one, and a piece made against one is not reused
// against the other), applies stroke, reruns (incremental: the stage reuses
// what the stroke did not reach), keeps the stage's files, then makes the
// stage again from nothing in the same folder — the same paths in every
// file, the same upstream outputs — and compares. It returns the
// incremental run's log.
func incrementalMatchesFull(t *testing.T, d *procgen.Doc, root string, stroke func(d *procgen.Doc), stage string) string {
	t.Helper()
	dir := filepath.Join(root, "run")
	logged(t, d, dir)
	forgetStage(t, dir, stage)
	logged(t, d, dir)
	stroke(d)
	_, log := logged(t, d, dir)
	inc := stageFiles(dir, stage)
	forgetStage(t, dir, stage)
	logged(t, d, dir)
	sameStageFiles(t, stage, inc, stageFiles(dir, stage))
	return log
}

func TestGrassFieldIncrementalMatchesFull(t *testing.T) {
	withFakePolyHaven(t)
	d := paintWorldDoc()
	d.Stages = append(d.Stages, grassDoc().Stages[1:]...)
	g := d.StageByID("grass")
	rule := g.Params["rules"].Items[0]
	rule["paint"] = procgen.Mask("meadow")
	rule["slope"], rule["cliff"] = procgen.Vec2(0, 90), procgen.Vec2(0, 1)
	g.Params["exclude"] = procgen.List(procgen.Params{"layer": procgen.Mask("path")})
	paintDisc(d, "meadow", paint.KindDensity, -60, -60, 40)
	paintDisc(d, "meadow", paint.KindDensity, 90, 90, 20)
	d.EnsurePaintLayer(paint.LayerDef{Name: "path", Kind: paint.KindExclude})
	log := incrementalMatchesFull(t, d, t.TempDir(), func(d *procgen.Doc) {
		// A path through the meadow in tile (1, 1), and a new patch.
		d.PaintLayer("path").ApplyLine(paint.Stroke{Radius: 2, Strength: 1}, 80, 90, 110, 90)
		d.PaintLayer("meadow").Apply(paint.Stroke{X: 100, Z: 30, Radius: 8, Strength: 1})
	}, "grass")
	// The strokes reach tiles (1, 1) and (1, 0): grass_3_3 and grass_3_2.
	if !strings.Contains(log, "[grass] reused 14 of 16 tiles") {
		t.Errorf("want 14 of 16 grass tiles reused; log:\n%s", log)
	}
}

func TestScatterIncrementalMatchesFull(t *testing.T) {
	d := paintWorldDoc()
	f, i, e, ref, v2 := procgen.Float, procgen.Int, procgen.Enum, procgen.StageRef, procgen.Vec2
	d.Stages = append(d.Stages,
		procgen.Stage{Kind: RocksKind, ID: "boulders", Params: procgen.Params{"preset": e("boulder"), "variants": i(2), "triangles": i(200)}},
		procgen.Stage{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"),
			"rules": procgen.List(
				procgen.Params{"name": procgen.String("big"), "rocks": ref("boulders"), "density": f(4), "max_bury": f(2),
					"scale": v2(0.6, 1.4), "slope": v2(0, 90), "cliff": v2(0, 1), "cluster": f(0)},
				procgen.Params{"name": procgen.String("rubble"), "rocks": ref("boulders"), "density": f(60), "max_bury": f(2),
					"scale": v2(0.15, 0.3), "slope": v2(0, 90), "cliff": v2(0, 1), "cluster": f(0), "satellites": f(2),
					"paint": procgen.Mask("rubble_here")}),
			"exclude": procgen.List(procgen.Params{"layer": procgen.Mask("clear")})}},
	)
	paintDisc(d, "rubble_here", paint.KindDensity, -60, -60, 50)
	d.EnsurePaintLayer(paint.LayerDef{Name: "clear", Kind: paint.KindExclude})
	log := incrementalMatchesFull(t, d, t.TempDir(), func(d *procgen.Doc) {
		d.PaintLayer("rubble_here").Apply(paint.Stroke{X: 90, Z: 90, Radius: 12, Strength: 1})
		d.PaintLayer("clear").Apply(paint.Stroke{X: -96, Z: -96, Radius: 6, Strength: 1})
	}, "scatter")
	if !strings.Contains(log, "[scatter] tiled: 16 tiles") {
		t.Fatalf("the scatter did not tile; log:\n%s", log)
	}
	if strings.Contains(log, "16 tiles, 0 reused") || strings.Contains(log, "16 tiles, 16 reused") {
		t.Errorf("want some tiles reused and some re-scattered; log:\n%s", log)
	}
}

func TestScatterWithoutPaintStaysClassic(t *testing.T) {
	d := smallTerrainDoc()
	f, i, e, ref, v2 := procgen.Float, procgen.Int, procgen.Enum, procgen.StageRef, procgen.Vec2
	d.Stages = append(d.Stages,
		procgen.Stage{Kind: RocksKind, ID: "boulders", Params: procgen.Params{"preset": e("boulder"), "variants": i(1), "triangles": i(200)}},
		procgen.Stage{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"),
			"rules": procgen.List(procgen.Params{"name": procgen.String("r"), "rocks": ref("boulders"), "density": f(20),
				"scale": v2(0.2, 0.4), "slope": v2(0, 90), "cliff": v2(0, 1)})}})
	res, log := logged(t, d, t.TempDir())
	if strings.Contains(log, "tiled") {
		t.Errorf("a scatter reading no paint tiled:\n%s", log)
	}
	for _, p := range res.Stage("scatter").Placements {
		if !strings.HasPrefix(p.ID, "r_0") {
			t.Fatalf("classic IDs are <rule>_<nnnnn>; got %s", p.ID)
		}
	}
	if _, err := os.Stat(filepath.Join(res.OutDir, "scatter", "tiles")); err == nil {
		t.Error("a classic scatter wrote tile caches")
	}
}

func TestTerrainMaterialIncrementalMatchesFull(t *testing.T) {
	_, res := withFakePolyHaven(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	d := paintWorldDoc()
	ref, s := procgen.StageRef, procgen.String
	d.Stages = append(d.Stages,
		procgen.Stage{Kind: PolyHavenTexturesKind, ID: "tex", Params: procgen.Params{
			"layers": procgen.List(procgen.Params{"name": s("sand"), "id": s("sand")})}},
		procgen.Stage{Kind: TerrainMaterialKind, ID: "ground_mat", Params: procgen.Params{
			"terrain": ref("ground"), "textures": ref("tex"), "sand": s("sand"), "cracked": s("sand"), "gravel": s("sand"),
			"rock": s("sand"), "paint": procgen.List(procgen.Params{"layer": procgen.Mask("trail"), "target": s("gravel")})}})
	paintDisc(d, "trail", paint.KindMaterial, 0, 0, 20)
	log := incrementalMatchesFull(t, d, filepath.Join(res, "generated"), func(d *procgen.Doc) {
		d.PaintLayer("trail").ApplyLine(paint.Stroke{Radius: 3, Strength: 1}, -100, 50, 100, 50)
	}, "ground_mat")
	if !strings.Contains(log, "[ground_mat] unpainted weights reused") {
		t.Errorf("the rules ran again after a stroke; log:\n%s", log)
	}
}

func TestRegionMaterialIncrementalMatchesFull(t *testing.T) {
	_, res := withFakePolyHaven(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	d := twoBiomeWorld(t, [4]float64{1, 5, 6, 7}, nil)
	gm := d.StageByID("ground_mat")
	gm.Params["paint"] = procgen.List(procgen.Params{"layer": procgen.Mask("trail"), "target": procgen.String("gravel")})
	paintDisc(d, "trail", paint.KindMaterial, 0, 0, 15)
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	log := incrementalMatchesFull(t, d, filepath.Join(res, "generated"), func(d *procgen.Doc) {
		d.PaintLayer("trail").Apply(paint.Stroke{X: 30, Z: -20, Radius: 10, Strength: 1})
	}, "ground_mat")
	if !strings.Contains(log, "[ground_mat] unpainted weights reused") {
		t.Errorf("the blend ran again after a stroke; log:\n%s", log)
	}
}

// TestPaintKeysFollowOnlyTheirTiles: the dirty-tile computation — after a
// stroke, exactly the grass tiles and scatter tiles it can reach get new
// keys.
func TestPaintKeysFollowOnlyTheirTiles(t *testing.T) {
	d := paintWorldDoc()
	d.Stages = append(d.Stages, grassDoc().Stages[1:]...)
	g := d.StageByID("grass")
	g.Params["rules"].Items[0]["paint"] = procgen.Mask("meadow")
	paintDisc(d, "meadow", paint.KindDensity, -60, -60, 10)
	keys := func() map[string]string {
		env, err := procgen.PlanEnv(d, "grass")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for j := 0; j < 4; j++ {
			for i := 0; i < 4; i++ {
				x0, z0 := -128+64*float64(i), -128+64*float64(j)
				out[paint.TileKey{I: i, J: j}.String()] = grassTileKey(env, "t", [4]float64{x0, z0, x0 + 64, z0 + 64})
			}
		}
		return out
	}
	before := keys()
	// Tile (1, 1) of the paint grid is terrain tile (3, 3); a stroke right
	// on its edge with tile (0, 1) also reaches terrain tile (2, 3).
	d.PaintLayer("meadow").Apply(paint.Stroke{X: 96, Z: 96, Radius: 6, Strength: 1})
	d.PaintLayer("meadow").Apply(paint.Stroke{X: 64.1, Z: 100, Radius: 0.5, Strength: 1})
	after := keys()
	var changed []string
	for k := range before {
		if before[k] != after[k] {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	if strings.Join(changed, " ") != "2_3 3_3" {
		t.Errorf("changed grass keys %v, want [2_3 3_3]", changed)
	}
}
