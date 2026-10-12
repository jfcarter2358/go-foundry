package gencli

// gencli_test.go — `foundry gen` against a scratch project: new, check, run,
// splice, rerun, list.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func TestGenNewCheckRunSplice(t *testing.T) {
	root := t.TempDir()
	prev := fileutil.ResourcesPath
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	res := filepath.Join(root, "resources")
	os.MkdirAll(filepath.Join(res, "levels"), 0o755)

	// A level with something hand-made in it.
	lvl := world.RawLevel{Scenery: []world.RawScenery{{ID: "barrel", Model: "models/barrel.fmdl"}}}
	data, _ := yaml.Marshal(lvl)
	levelPath := filepath.Join(res, "levels", "arena.yaml")
	os.WriteFile(levelPath, data, 0o644)

	var out bytes.Buffer
	if code := Main(root, []string{"new", "demo", "-template", "noise_demo"}, &out); code != 0 {
		t.Fatalf("new: %d\n%s", code, out.String())
	}
	if code := Main(root, []string{"new", "demo"}, &out); code == 0 {
		t.Error("new over an existing generator succeeded without -force")
	}
	// Shrink it and point it at the level.
	docPath := filepath.Join(res, "generators", "demo.fgen")
	d, err := procgen.ReadDoc(docPath)
	if err != nil {
		t.Fatal(err)
	}
	d.Level = "arena"
	d.Stages[0].Params["size"] = procgen.Float(64)
	d.Stages[0].Params["resolution"] = procgen.Int(16)
	if err := procgen.WriteDoc(docPath, d); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if code := Main(root, []string{"check", "demo"}, &out); code != 0 || !strings.Contains(out.String(), "would run ground") {
		t.Errorf("check: %d %s", code, out.String())
	}
	out.Reset()
	if code := Main(root, []string{"run", "demo", "-q"}, &out); code != 0 {
		t.Fatalf("run: %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "spliced into levels/arena.yaml") {
		t.Errorf("run did not report the splice:\n%s", out.String())
	}
	back, err := genlevel.ReadRawLevel(levelPath)
	if err != nil {
		t.Fatal(err)
	}
	if sc, _ := genlevel.Count(&back, "demo"); sc != 4 || back.Scenery[0].ID != "barrel" {
		t.Errorf("level holds %d of the generator's placements, first %q", sc, back.Scenery[0].ID)
	}
	if back.Scenery[1].Model != "generated/demo/ground/tile_0_0.fnm" {
		t.Errorf("placement model %q: want a resources-relative path", back.Scenery[1].Model)
	}
	if _, err := os.Stat(filepath.Join(res, "generated", "demo", "manifest.yaml")); err != nil {
		t.Error("no manifest in the default output folder")
	}

	// Rerun: nothing runs, the level is not rewritten.
	out.Reset()
	Main(root, []string{"run", "demo", "-q"}, &out)
	if !strings.Contains(out.String(), "ran 0") || !strings.Contains(out.String(), "already holds") {
		t.Errorf("rerun:\n%s", out.String())
	}
	out.Reset()
	if code := Main(root, []string{"check", "demo"}, &out); code != 0 || !strings.Contains(out.String(), "up to date") {
		t.Errorf("check after run: %s", out.String())
	}
}

func TestGenList(t *testing.T) {
	var out bytes.Buffer
	if code := Main(".", []string{"list"}, &out); code != 0 || !strings.Contains(out.String(), "noise_preview") ||
		!strings.Contains(out.String(), "noise_demo") {
		t.Errorf("list: %d\n%s", code, out.String())
	}
	out.Reset()
	if code := Main(".", []string{"list", "-kind", "noise_preview"}, &out); code != 0 ||
		!strings.Contains(out.String(), "points {x, z, width, depth}") || !strings.Contains(out.String(), "position") {
		t.Errorf("list -kind:\n%s", out.String())
	}
	if code := Main(".", []string{"bogus"}, &out); code != 2 {
		t.Error("an unknown subcommand did not print usage")
	}
}

func TestGenRunReportsBadDocuments(t *testing.T) {
	root := t.TempDir()
	prev := fileutil.ResourcesPath
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	dir := filepath.Join(root, "resources", "generators")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "bad.fgen"), []byte("seed: 1\nstages:\n- stage: noise_preview\n  id: g\n  params: {sise: 3}\n"), 0o644)
	var out bytes.Buffer
	if code := Main(root, []string{"run", "bad"}, &out); code != 1 || !strings.Contains(out.String(), `did you mean "size"`) {
		t.Errorf("run of a bad document: %d\n%s", code, out.String())
	}
}

// TestGenRunResolvesChunkAutoInTheLevelFile is the file splice resolving
// `chunk: auto` against the level's chunks, as the editor's does.
func TestGenRunResolvesChunkAutoInTheLevelFile(t *testing.T) {
	root := t.TempDir()
	prev := fileutil.ResourcesPath
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	res := filepath.Join(root, "resources")
	os.MkdirAll(filepath.Join(res, "levels"), 0o755)
	// Two chunks side by side: west over x < -10, east over x > -5.
	west := world.NewBoxBrush("w", mgl32.Vec3{-25, 0, 0}, mgl32.Vec3{15, 1, 40}, "concrete")
	east := world.NewBoxBrush("e", mgl32.Vec3{17.5, 0, 0}, mgl32.Vec3{22.5, 1, 40}, "concrete")
	lvl := world.RawLevel{Chunks: []world.RawChunk{
		{ID: "west", Brushes: []world.RawBrush{world.BrushToRaw(&west)}},
		{ID: "east", Brushes: []world.RawBrush{world.BrushToRaw(&east)}},
	}}
	data, _ := yaml.Marshal(lvl)
	levelPath := filepath.Join(res, "levels", "arena.yaml")
	os.WriteFile(levelPath, data, 0o644)

	var out bytes.Buffer
	if code := Main(root, []string{"new", "demo", "-template", "noise_demo"}, &out); code != 0 {
		t.Fatalf("new: %d\n%s", code, out.String())
	}
	docPath := filepath.Join(res, "generators", "demo.fgen")
	d, _ := procgen.ReadDoc(docPath)
	d.Level = "arena"
	d.Stages[0].Params["size"] = procgen.Float(64)
	d.Stages[0].Params["resolution"] = procgen.Int(16)
	procgen.WriteDoc(docPath, d)
	if code := Main(root, []string{"run", "demo", "-q"}, &out); code != 0 {
		t.Fatalf("run: %d\n%s", code, out.String())
	}
	back, err := genlevel.ReadRawLevel(levelPath)
	if err != nil {
		t.Fatal(err)
	}
	// noise_preview places tiles at their corners: x = -32 (west) and 0 (east).
	got := map[string]string{}
	for _, s := range back.Scenery {
		got[s.ID] = s.Chunk
	}
	for id, want := range map[string]string{
		"gen.demo.ground.tile_0_0": "west", "gen.demo.ground.tile_0_1": "west",
		"gen.demo.ground.tile_1_0": "east", "gen.demo.ground.tile_1_1": "east",
	} {
		if got[id] != want {
			t.Errorf("%s streams with %q, want %q (all: %v)", id, got[id], want, got)
		}
	}
}
