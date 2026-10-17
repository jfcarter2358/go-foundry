package stages

// noise_preview_test.go — the demo stage end to end: run, outputs, rerun,
// Load, preview; and every registered kind documents itself.

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/nanite"
	"foundry/internal/procgen"
)

func demoDoc(t *testing.T) *procgen.Doc {
	t.Helper()
	d, err := procgen.NewFromTemplate("noise_demo", "demo")
	if err != nil {
		t.Fatal(err)
	}
	// Small, so the cook is quick.
	d.Stages[0].Params["size"] = procgen.Float(64)
	d.Stages[0].Params["resolution"] = procgen.Int(32)
	return d
}

func TestNoisePreviewRunsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	d := demoDoc(t)
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("template has problems: %v", probs)
	}
	var lines []string
	res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir, Log: func(l string) { lines = append(lines, l) }})
	if err != nil {
		t.Fatal(err)
	}
	st := res.Stage("ground")
	for _, f := range []string{"heightmap.png", "slope.png", "meta.yaml", "tile_0_0.fnm", "tile_1_1.fnm", procgen.PlacementsFileName} {
		if _, err := os.Stat(st.File(f)); err != nil {
			t.Errorf("no %s: %v", f, err)
		}
	}
	pl := res.Placements()
	if len(pl) != 4 {
		t.Fatalf("%d placements, want 4 tiles", len(pl))
	}
	if pl[0].ID != "gen.demo.ground.tile_0_0" || pl[0].Collision != "mesh" || pl[0].Chunk != procgen.ChunkAuto {
		t.Errorf("first placement %+v", pl[0])
	}
	// The tiles are closed solids, and adjacent ones meet (their corners
	// line up in world space).
	m, err := nanite.Load(st.File("tile_0_0.fnm"))
	if err != nil {
		t.Fatal(err)
	}
	if !m.ClosedSolid {
		t.Error("a tile is not a closed solid")
	}
	if pl[1].ID != "gen.demo.ground.tile_0_1" || pl[1].Position.Z-pl[0].Position.Z != 32 {
		t.Errorf("tile corners %v and %v are not one tile apart", pl[0].Position, pl[1].Position)
	}
	if len(lines) == 0 {
		t.Error("nothing was logged")
	}

	// A second run skips; its downstream would Load the field back.
	res2, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil || len(res2.Ran) != 0 {
		t.Fatalf("rerun ran %v (%v)", res2.Ran, err)
	}
	art, err := loadNoisePreview(res2.Stage("ground"))
	if err != nil {
		t.Fatal(err)
	}
	f := art.(*Heightfield)
	orig := st.Artifact.(*Heightfield)
	if f.N != orig.N || len(f.H) != len(orig.H) {
		t.Fatalf("Load gave %d cells, the run %d", f.N, orig.N)
	}
	// 16-bit quantisation over the range: within one step.
	step := float64(orig.Hi-orig.Lo) / 65535
	for i := range f.H {
		if math.Abs(float64(f.H[i]-orig.H[i])) > step+1e-4 {
			t.Fatalf("sample %d: loaded %v, ran %v", i, f.H[i], orig.H[i])
		}
	}

	// cook: false places nothing and writes .glb.
	d.Stages[0].Params["cook"] = procgen.Bool(false)
	res3, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res3.Placements()) != 0 {
		t.Error("cook: false still placed tiles")
	}
	if _, err := os.Stat(filepath.Join(dir, "ground", "tile_0_0.glb")); err != nil {
		t.Error("cook: false wrote no .glb")
	}
	if _, err := os.Stat(filepath.Join(dir, "ground", "tile_0_0.fnm")); !os.IsNotExist(err) {
		t.Error("the previous run's .fnm was not cleared")
	}
}

func TestNoisePreviewIsDeterministic(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	d := demoDoc(t)
	d.Stages[0].Params["cook"] = procgen.Bool(false)
	if _, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: a}); err != nil {
		t.Fatal(err)
	}
	if _, err := procgen.Run(context.Background(), d.Clone(), procgen.Options{OutDir: b, Workers: 1}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"heightmap.png", "slope.png", "tile_1_0.glb"} {
		x, _ := os.ReadFile(filepath.Join(a, "ground", f))
		y, _ := os.ReadFile(filepath.Join(b, "ground", f))
		if len(x) == 0 || string(x) != string(y) {
			t.Errorf("%s differs between runs", f)
		}
	}
	d2 := d.Clone()
	d2.Stages[0].Params["seed"] = procgen.Seed(9)
	c := t.TempDir()
	procgen.Run(context.Background(), d2, procgen.Options{OutDir: c})
	x, _ := os.ReadFile(filepath.Join(a, "ground", "heightmap.png"))
	y, _ := os.ReadFile(filepath.Join(c, "ground", "heightmap.png"))
	if string(x) == string(y) {
		t.Error("a different seed made the same heightmap")
	}
}

func TestNoisePreviewPreview(t *testing.T) {
	d := demoDoc(t)
	d.Stages[0].Params["resolution"] = procgen.Int(512)
	p, err := procgen.Preview(context.Background(), d, "ground", procgen.NewPreviewCache(4), procgen.PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Images) != 3 || p.Images[0].W != noisePreviewMaxSamples+1 {
		t.Errorf("images %d, first %d wide: the preview should cap at %d cells", len(p.Images), p.Images[0].W, noisePreviewMaxSamples)
	}
	if len(p.Meshes) != 1 || len(p.Meshes[0].Indices) != noisePreviewMaxSamples*noisePreviewMaxSamples*6 {
		t.Errorf("meshes %d", len(p.Meshes))
	}
	if len(p.Points) != 1 || len(p.Points[0].Radii) != 1 {
		t.Error("the bump is not a point layer with its radius")
	}
	hasPath := false
	for _, l := range p.Lines {
		if l.Name == "Carve path" && len(l.Points) == 4 {
			hasPath = true
		}
	}
	if !hasPath {
		t.Error("the carve path is not a line layer")
	}
	if p.FrameBounds() != (procgen.Rect{MinX: -32, MinZ: -32, MaxX: 32, MaxZ: 32}) {
		t.Errorf("bounds %v", p.FrameBounds())
	}
}

func TestValidateWantsTilesToDivideCells(t *testing.T) {
	d := demoDoc(t)
	d.Stages[0].Params["tiles"] = procgen.Int(3)
	probs := d.Validate()
	if len(probs) != 1 || !strings.Contains(probs[0].Msg, "divide") {
		t.Errorf("problems %v", probs)
	}
}

func TestEveryKindDocumentsItself(t *testing.T) {
	// The schema is the docs (the guide table, the tooltips, `foundry gen
	// list`, the Claude reference): an undocumented param is a hole in all of
	// them at once.
	var check func(kind string, defs []procgen.ParamDef)
	check = func(kind string, defs []procgen.ParamDef) {
		for _, p := range defs {
			if p.Doc == "" {
				t.Errorf("%s: param %s has no Doc", kind, p.Name)
			}
			check(kind, p.Fields)
		}
	}
	for _, k := range procgen.Kinds() {
		if k.Doc == "" || k.Label == "" || k.Category == "" {
			t.Errorf("kind %s lacks a Label, Doc or Category", k.Name)
		}
		check(k.Name, k.Params)
	}
	for _, tp := range procgen.Templates() {
		d, err := procgen.NewFromTemplate(tp.Name, "x")
		if err != nil {
			t.Fatal(err)
		}
		if probs := d.Validate(); len(probs) > 0 {
			t.Errorf("template %s has problems: %v", tp.Name, probs)
		}
	}
}
