package stages

// terrain_test.go — the terrain stage end to end: its files and metadata,
// Load, determinism, the preview, validation, and — the one that matters
// most for a tiled world — that neighbouring tiles meet exactly after the
// cook's quantisation at every DAG cut.

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/nanite"
	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"

	"github.com/go-gl/mathgl/mgl32"
)

// smallTerrainDoc is a quick terrain: 64 m at 0.5 m in four 32 m tiles, a
// tall mesa so the tiles' heights differ a lot, and light erosion.
func smallTerrainDoc() *procgen.Doc {
	return &procgen.Doc{Name: "t", Seed: 7, Stages: []procgen.Stage{{Kind: TerrainKind, ID: "ground", Params: procgen.Params{
		"size": procgen.Float(64), "resolution": procgen.Float(0.5), "tile_size": procgen.Float(32),
		"mesa_height": procgen.Float(120), "mesa_scale": procgen.Float(40), "mesa_coverage": procgen.Float(0.35),
		"mesa_tiers": procgen.Int(1), "mesa_rim": procgen.Float(8),
		"drainage_iterations": procgen.Int(10), "droplets": procgen.Float(0.2), "talus_iterations": procgen.Int(10),
		"canyons": procgen.List(procgen.Params{
			"path":        procgen.Spline([]float64{-40, -20, 14, 20}, []float64{40, 10, 18, 25}),
			"tributaries": procgen.Int(1), "tributary_length": procgen.Float(20),
		}),
	}}}}
}

func TestTerrainRunsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	d := smallTerrainDoc()
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	st := res.Stage("ground")
	want := []string{terrain.MetaFile, terrain.HeightmapFile, terrain.AlbedoFile, terrain.ReliefFile, "tile_0_0.fnm", "tile_1_1.fnm"}
	for _, m := range terrain.MaskNames {
		want = append(want, terrain.MaskFile(m))
	}
	for _, f := range want {
		if _, err := os.Stat(st.File(f)); err != nil {
			t.Errorf("no %s: %v", f, err)
		}
	}
	pl := res.Placements()
	if len(pl) != 4 {
		t.Fatalf("%d placements, want 4", len(pl))
	}
	// Tiles are placed at their centres, collide as meshes and stream with
	// whatever chunk they stand in.
	if p := pl[0]; p.ID != "gen.t.ground.tile_0_0" || p.Position != (procgen.Point3{X: -16, Z: -16}) ||
		p.Collision != "mesh" || p.Chunk != procgen.ChunkAuto {
		t.Errorf("first placement %+v", p)
	}
	m, err := nanite.Load(st.File("tile_1_0.fnm"))
	if err != nil {
		t.Fatal(err)
	}
	if !m.ClosedSolid {
		t.Error("a tile is not a closed solid: nav would see one solid per triangle")
	}

	// The metadata a later stage reads, and Load.
	art := st.Artifact.(*terrain.Terrain)
	loaded, err := loadTerrain(st)
	if err != nil {
		t.Fatal(err)
	}
	lt := loaded.(*terrain.Terrain)
	if lt.Meta.Samples != 129 || lt.Meta.Resolution != 0.5 || lt.Meta.Tiles.Count != 2 || len(lt.Meta.Tiles.Files) != 4 ||
		lt.Meta.Tiles.Step == 0 || len(lt.Meta.Masks) != len(terrain.MaskNames) || len(lt.Meta.Paths) < 2 {
		t.Errorf("meta %+v", lt.Meta)
	}
	span := float64(art.Meta.Height.Hi - art.Meta.Height.Lo)
	for k := range art.Grid.H {
		if d := math.Abs(float64(lt.Grid.H[k] - art.Grid.H[k])); d > span/65535+1e-3 {
			t.Fatalf("sample %d loads as %v, ran %v", k, lt.Grid.H[k], art.Grid.H[k])
		}
	}
	for _, name := range terrain.MaskNames {
		a, err := art.Mask(name)
		if err != nil {
			t.Fatal(err)
		}
		b, err := lt.Mask(name)
		if err != nil {
			t.Fatal(err)
		}
		for k := range a {
			if math.Abs(float64(a[k]-b[k])) > 0.5/255+1e-6 {
				t.Fatalf("mask %s sample %d: loaded %v, ran %v", name, k, b[k], a[k])
			}
		}
	}
	if x, z := 3.25, -7.5; lt.HeightAt(x, z) == 0 || lt.NormalAt(x, z)[1] <= 0 || !lt.Contains(x, z) || lt.Contains(40, 0) {
		t.Error("the lookups do not answer")
	}
	// The strata mask is an index into the strata list.
	strata, _ := lt.Mask(terrain.MaskStrata)
	for _, v := range strata {
		if idx := math.Round(float64(v) * 255); idx < 0 || int(idx) >= len(lt.Meta.Strata.Layers) {
			t.Fatalf("strata index %v outside the %d layers", idx, len(lt.Meta.Strata.Layers))
		}
	}

	// A second run skips.
	res2, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil || len(res2.Ran) != 0 {
		t.Fatalf("rerun ran %v (%v)", res2.Ran, err)
	}
}

func TestTerrainIsDeterministicOnAnyWorkerCount(t *testing.T) {
	d := smallTerrainDoc()
	d.Stages[0].Params["cook"] = procgen.Bool(false)
	a, b := t.TempDir(), t.TempDir()
	if _, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: a, Workers: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := procgen.Run(context.Background(), d.Clone(), procgen.Options{OutDir: b, Workers: 1}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{terrain.HeightmapFile, terrain.MaskFile(terrain.MaskFlow), terrain.AlbedoFile, "tile_1_0.glb", terrain.MetaFile} {
		x, _ := os.ReadFile(filepath.Join(a, "ground", f))
		y, _ := os.ReadFile(filepath.Join(b, "ground", f))
		if len(x) == 0 || string(x) != string(y) {
			t.Errorf("%s differs between 7 workers and 1", f)
		}
	}
	d2 := d.Clone()
	d2.Stages[0].Params["seed"] = procgen.Seed(3)
	c := t.TempDir()
	if _, err := procgen.Run(context.Background(), d2, procgen.Options{OutDir: c}); err != nil {
		t.Fatal(err)
	}
	x, _ := os.ReadFile(filepath.Join(a, "ground", terrain.HeightmapFile))
	y, _ := os.ReadFile(filepath.Join(c, "ground", terrain.HeightmapFile))
	if string(x) == string(y) {
		t.Error("a different seed made the same heightmap")
	}
}

// seamKeys is a cooked tile's top-surface vertices on one of its edges at
// the cut of the given error, as world positions (exact float bits): side 0
// is x = +half, 1 is x = -half, 2 is z = +half, 3 is z = -half (local).
func seamKeys(m *nanite.Mesh, centre procgen.Point3, half, base float32, side int, errAt float32) map[[3]uint32]bool {
	out := map[[3]uint32]bool{}
	for ci := range m.Clusters {
		c := &m.Clusters[ci]
		if !c.Selected(errAt) {
			continue
		}
		for _, ix := range m.Indices[3*c.TriFirst : 3*(c.TriFirst+c.TriCount)] {
			x, y, z := m.Vertices[5*ix], m.Vertices[5*ix+1], m.Vertices[5*ix+2]
			if y <= base+1e-3 {
				continue // a skirt's foot or the bottom
			}
			on := false
			switch side {
			case 0:
				on = x == half
			case 1:
				on = x == -half
			case 2:
				on = z == half
			case 3:
				on = z == -half
			}
			if on {
				w := mgl32.Vec3{x + centre.X, y + centre.Y, z + centre.Z}
				out[[3]uint32{math.Float32bits(w[0]), math.Float32bits(w[1]), math.Float32bits(w[2])}] = true
			}
		}
	}
	return out
}

func TestTerrainTilesMeetExactlyAtEveryLOD(t *testing.T) {
	dir := t.TempDir()
	d := smallTerrainDoc()
	res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	st := res.Stage("ground")
	meta := st.Artifact.(*terrain.Terrain).Meta
	tiles := map[[2]int]*nanite.Mesh{}
	centres := map[[2]int]procgen.Point3{}
	natural := map[float32]bool{}
	for _, p := range res.Placements() {
		var i, j int
		name := strings.TrimPrefix(p.ID, "gen.t.ground.")
		if _, err := fmt.Sscanf(name, "tile_%d_%d", &i, &j); err != nil {
			t.Fatal(err)
		}
		m, err := nanite.Read(st.File(name + ".fnm"))
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Flat(); err != nil {
			t.Fatal(err)
		}
		if m.Quant.Step != meta.Tiles.Step {
			t.Errorf("%s cooked on a %g step, the field's is %g", name, m.Quant.Step, meta.Tiles.Step)
		}
		natural[nanite.NewQuant(m.Lo, m.Hi, 0, 0, 0, 0).Step] = true
		tiles[[2]int{i, j}], centres[[2]int{i, j}] = m, p.Position
		if m.Levels < 3 {
			t.Fatalf("%s has %d DAG levels: the test needs simplification", name, m.Levels)
		}
	}
	if len(natural) < 2 {
		t.Fatalf("every tile would have quantised on the same step anyway (%v): the fixture does not test the shared step", natural)
	}
	half := float32(meta.Tiles.Size / 2)
	per := int(meta.Tiles.Size/meta.Resolution) + 1
	errs := []float32{0, 0.005, 0.02, 0.1, 0.5, 2, 1e9}
	check := func(a, b [2]int, sa, sb int) {
		full := seamKeys(tiles[a], centres[a], half, meta.Tiles.Base, sa, 0)
		if len(full) != per {
			t.Fatalf("tile %v edge %d has %d vertices at full detail, want %d", a, sa, len(full), per)
		}
		for _, ea := range errs {
			ka := seamKeys(tiles[a], centres[a], half, meta.Tiles.Base, sa, ea)
			if len(ka) != per {
				t.Errorf("tile %v at error %g keeps %d of the %d seam vertices: it cracks", a, ea, len(ka), per)
			}
			for _, eb := range errs {
				kb := seamKeys(tiles[b], centres[b], half, meta.Tiles.Base, sb, eb)
				for k := range ka {
					if !kb[k] {
						t.Errorf("tiles %v (error %g) and %v (error %g): seam vertex %v of one is not on the other",
							a, ea, b, eb, mgl32.Vec3{math.Float32frombits(k[0]), math.Float32frombits(k[1]), math.Float32frombits(k[2])})
						return
					}
				}
			}
		}
	}
	check([2]int{0, 0}, [2]int{1, 0}, 0, 1) // along x
	check([2]int{0, 1}, [2]int{1, 1}, 0, 1)
	check([2]int{0, 0}, [2]int{0, 1}, 2, 3) // along z
	check([2]int{1, 0}, [2]int{1, 1}, 2, 3)
}

func TestTerrainPreview(t *testing.T) {
	d := smallTerrainDoc()
	d.Stages[0].Params["preview_cells"] = procgen.Int(32)
	p, err := procgen.Preview(context.Background(), d, "ground", procgen.NewPreviewCache(4), procgen.PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Images) < 10 || p.Images[0].Name != "Relief" || p.Images[0].W != 33 || p.Images[0].Values == nil {
		t.Errorf("%d images, the first %q %d wide", len(p.Images), p.Images[0].Name, p.Images[0].W)
	}
	if len(p.Meshes) != 4 {
		t.Errorf("%d preview meshes, want one per tile", len(p.Meshes))
	}
	canyons := 0
	for _, l := range p.Lines {
		if l.Name == "Canyon" || l.Name == "Tributary" {
			canyons++
		}
	}
	if canyons != 2 {
		t.Errorf("%d carved paths drawn, want the canyon and its tributary", canyons)
	}
	if _, ok := p.Artifact.(*terrain.Terrain); !ok {
		t.Errorf("the preview's artifact is a %T, want a *terrain.Terrain for downstream previews", p.Artifact)
	}
}

func TestTerrainValidate(t *testing.T) {
	d := smallTerrainDoc()
	d.Stages[0].Params["tile_size"] = procgen.Float(30)
	d.Stages[0].Params["mesh_step"] = procgen.Int(7)
	probs := d.Validate()
	joined := ""
	for _, p := range probs {
		joined += p.Msg + "\n"
	}
	if !strings.Contains(joined, "whole number of 30 m tiles") || !strings.Contains(joined, "Mesh step (7)") {
		t.Errorf("problems:\n%s", joined)
	}
}

func TestCookConcurrencyIsBoundedByMemory(t *testing.T) {
	// A 0.5 m 128 m tile: about 133k triangles.
	tris := tileTriangles(tilePlan{per: 256, step: 1})
	if got := cookConcurrency(tris, 3000, 20, false); got < 2 || got > 5 {
		t.Errorf("%d tiles of %d triangles with hulls at once in 3 GB", got, tris)
	}
	if got := cookConcurrency(tris, 3000, 40, true); got < 15 || got > 30 {
		t.Errorf("%d mesh-only tiles of %d triangles at once in 3 GB", got, tris)
	}
	if got := cookConcurrency(tris, 100, 20, false); got != 1 {
		t.Errorf("a budget below one cook gives %d, want 1", got)
	}
	if got := cookConcurrency(1000, 1<<20, 6, true); got != 6 {
		t.Errorf("a huge budget gives %d, want the 6 workers", got)
	}
}

// TestTerrainSchemaDefaultsAreTheGeneratorsDefaults: a document that sets
// nothing runs terrain.Defaults() — the schema and the package agree, so the
// guide's table, the editor's "default" marks and a CLI run all describe the
// same terrain.
func TestTerrainSchemaDefaultsAreTheGeneratorsDefaults(t *testing.T) {
	k := procgen.KindByName(TerrainKind)
	env := procgen.Env{Params: procgen.Resolve(k.Params, nil), Seed: 9}
	got := terrainSettings(&env)
	want := terrain.Defaults()
	want.Seed = 9
	if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
		t.Errorf("the schema's defaults map to\n%+v\nwant terrain.Defaults()\n%+v", got, want)
	}
}
