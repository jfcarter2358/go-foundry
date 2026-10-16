package editor

// editor_generator_paint_bench_test.go — stroke-to-visible times on a real
// project (docs/terrain-paint/PHASE_P2.md, "Measured"). Opt-in: it needs a
// GL context and a project whose generator has run into its level.
//
//	FOUNDRY_GL_TEST=1 FOUNDRY_P2_BENCH=<project root>:<generator>:<level> \
//	    go test -run TestGeneratorPaintLiveBench -v ./internal/editor/
//
// It loads the level as the editor would (meshes uploaded), opens the
// generator, and for each of a warm-up and three strokes — a density layer
// (grass), a material layer (the splat), a scatter's layer — paints a small
// disc somewhere unpainted, lets the real 300 ms debounce run, and waits
// for the live update to land: mouse-up to the level edited and the files
// reloaded, which the next frame draws.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/levelio"
	"foundry/internal/material"
	"foundry/internal/procgen"
	"foundry/internal/uiwin"
)

func TestGeneratorPaintLiveBench(t *testing.T) {
	spec := os.Getenv("FOUNDRY_P2_BENCH")
	if spec == "" {
		t.Skip("FOUNDRY_P2_BENCH=<root>:<generator>:<level> not set")
	}
	parts := strings.Split(spec, ":")
	if len(parts) != 3 {
		t.Fatalf("FOUNDRY_P2_BENCH %q: want root:generator:level", spec)
	}
	gltest.Context(t)
	withGeneratorState(t)
	fileutil.ResourcesPath = filepath.Join(parts[0], "resources")
	gfx.RescanTextures()
	material.LoadAll()
	savedLevel, savedPath, savedLive := Editor.level, Editor.SavePath, genPaintLive
	t.Cleanup(func() { Editor.level, Editor.SavePath, genPaintLive = savedLevel, savedPath, savedLive })
	t0 := time.Now()
	lvl, err := levelio.LoadLevel(levelio.LevelPath(parts[2]))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("level %s loaded in %s: %d scenery, %d volumes", parts[2], time.Since(t0).Round(time.Millisecond), len(lvl.Scenery), len(lvl.Volumes))
	Editor.level, Editor.SavePath = &lvl, levelio.LevelPath(parts[2])
	gpath := filepath.Join(fileutil.ResourcesPath, "generators", parts[1]+".fgen")
	d, err := procgen.ReadDoc(gpath)
	if err != nil {
		t.Fatal(err)
	}
	genSetDoc(d, gpath)
	genPaintLive.off = false
	if _, why := genLiveTarget(); why != "" {
		t.Fatalf("not live: %s", why)
	}
	stroke := func(layer string, x, z float64) {
		genPaint.layer, genPaint.radius, genPaint.strength, genPaint.falloff = layer, 6, 1, 0.3
		genPaint.mode, genPaint.spacing = 0, 0.25
		genPaintBegin(x, z)
		genPaintDrag(x+4, z)
		genPaintEnd() // mouse-up: arms the debounce
		deadline := time.Now().Add(60 * time.Second)
		genPaintLive.last = nil
		for genPaintLive.last == nil && time.Now().Before(deadline) {
			genPaintLiveStep()
			uiwin.DrainWindowOpens()
			time.Sleep(2 * time.Millisecond)
		}
		if genPaintLive.last == nil {
			t.Fatalf("%s: no live update (%s)", layer, genStatus)
		}
		t.Logf("%-8s %s", layer, genPaintLive.last)
	}
	stroke("meadow", 330, -120) // warm-up: the first rerun after a fresh upstream rebuilds once
	stroke("meadow", -320, 60)
	stroke("trail", 60, 330)
	stroke("rubble", -60, -330)
	stroke("meadow", 200, -60)
}
