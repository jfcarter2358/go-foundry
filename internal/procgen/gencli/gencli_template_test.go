package gencli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/procgen/regions"
)

// The canyon_savannah template is one command from nothing to a world
// (docs/regions/PHASE_6.md): `gen new <level>_world -template
// canyon_savannah`, then `gen run`; the run's sync writes the template's two
// region volumes into the level — making the level — and turns the seeding
// switch off in the doc on disk. A check before the run only says so.
func TestCanyonSavannahTemplateSeedsItsLevel(t *testing.T) {
	root := t.TempDir()
	prev := fileutil.ResourcesPath
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	res := filepath.Join(root, "resources")
	os.MkdirAll(filepath.Join(res, "levels"), 0o755)

	var out bytes.Buffer
	if code := Main(root, []string{"new", "arena_world", "-template", "canyon_savannah"}, &out); code != 0 {
		t.Fatalf("new: %d\n%s", code, out.String())
	}
	docPath := filepath.Join(res, "generators", "arena_world.fgen")
	levelPath := filepath.Join(res, "levels", "arena.yaml")

	out.Reset()
	if code := Main(root, []string{"check", "arena_world"}, &out); code != 0 ||
		!strings.Contains(out.String(), "a run writes this template's 2 region(s) into it") {
		t.Fatalf("check: %d\n%s", code, out.String())
	}
	if _, err := os.Stat(levelPath); err == nil {
		t.Fatal("check wrote the level")
	}

	// Only the regions stage: the terrain and the Poly Haven stages are not
	// this test's (the sync runs before any stage).
	out.Reset()
	if code := Main(root, []string{"run", "arena_world", "-stage", "regions", "-no-splice", "-q"}, &out); code != 0 {
		t.Fatalf("run: %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "wrote 2 region volume(s) into level arena: canyon_west, savannah_east") {
		t.Errorf("run output:\n%s", out.String())
	}
	raw, err := genlevel.ReadRawLevel(levelPath)
	if err != nil {
		t.Fatal(err)
	}
	if !genlevel.HasRegionVolumes(&raw) || len(raw.Volumes) != 2 {
		t.Fatalf("level has %d volumes", len(raw.Volumes))
	}
	d, err := procgen.ReadDoc(docPath)
	if err != nil {
		t.Fatal(err)
	}
	if genlevel.WantsSeed(d) {
		t.Error("the doc on disk still asks to seed its level")
	}
	if items := d.StageByID("regions").Params.Items(regions.ParamRegions); len(items) != 2 {
		t.Errorf("the doc holds %d regions", len(items))
	}

	// A second run: the level leads; nothing is written into it again.
	before, _ := os.ReadFile(levelPath)
	out.Reset()
	if code := Main(root, []string{"run", "arena_world", "-stage", "regions", "-no-splice", "-q"}, &out); code != 0 {
		t.Fatalf("second run: %d\n%s", code, out.String())
	}
	if after, _ := os.ReadFile(levelPath); string(after) != string(before) || strings.Contains(out.String(), "wrote") {
		t.Errorf("the second run touched the level:\n%s", out.String())
	}
}
