package stages

// region_terrain_incremental_test.go — editing a far region with tiled
// erosion: the near tiles' cooked .fnm files are not rewritten, the near
// erosion windows are read back, the run is faster, and the result is what a
// fresh run of the edited world makes.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

func TestRegionTerrainIncrementalLeavesNearTilesAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("cooks 36 tiles twice")
	}
	presets := t.TempDir()
	a, b := seamPresets(t, presets, 5)
	os.MkdirAll(filepath.Join(presets, "c"), 0o755)
	_, c := seamPresets(t, filepath.Join(presets, "c"), 5)
	mk := func() *procgen.Doc {
		return worldDoc("inc", []procgen.Params{
			regionItem("canyon", DesertCanyonEnvironment, a, -384, -384, -128, 384, 150),
			regionItem("plain", SavannahEnvironment, b, -128, -384, 128, 384, 150),
			regionItem("far", SavannahEnvironment, c, 128, -384, 384, 384, 150),
		}, procgen.Params{"resolution": procgen.Float(2), "tile_size": procgen.Float(128), "warp_amount": procgen.Float(25),
			"erosion": procgen.Enum("tiled"), "margin": procgen.Float(32)},
			procgen.Params{"window": procgen.Int(1), "cook": procgen.Bool(true), "cook_memory": procgen.Int(1500)})
	}
	out := t.TempDir()
	run := func(dir string) (*procgen.Result, time.Duration) {
		t.Helper()
		t0 := time.Now()
		res, err := procgen.Run(context.Background(), mk(), procgen.Options{OutDir: dir, Workers: 8})
		if err != nil {
			t.Fatal(err)
		}
		return res, time.Since(t0)
	}
	_, first := run(out)
	stage := filepath.Join(out, "ground")
	man := procgen.ReadManifest(out).Stages["ground"]
	if len(man.Tiles) != 36 {
		t.Fatalf("%d tile hashes in the manifest, want 36", len(man.Tiles))
	}
	// Date every file back, to see which the next run writes.
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	stamps := map[string][]byte{}
	for name := range man.Tiles {
		p := filepath.Join(stage, name+".fnm")
		os.Chtimes(p, old, old)
		stamps[name], _ = os.ReadFile(p)
	}

	// Edit the far east region (its preset's relief): only its tiles and the
	// windows that reach it change.
	writePresetRelief(t, c, 14)
	res, second := run(out)
	if len(res.Ran) == 0 || res.Ran[0] != "ground" {
		t.Fatalf("ran %v", res.Ran)
	}
	man2 := procgen.ReadManifest(out).Stages["ground"]
	kept, rewritten := 0, 0
	for name := range man.Tiles {
		p := filepath.Join(stage, name+".fnm")
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		data, _ := os.ReadFile(p)
		same := st.ModTime().Equal(old) && string(data) == string(stamps[name])
		if same {
			kept++
			if man2.Tiles[name] != man.Tiles[name] {
				t.Errorf("%s kept but its hash changed", name)
			}
		} else {
			rewritten++
		}
		// Tile column 0 (x in [-384, -256]): its window's square and the
		// one it fades into end at -96, out of reach of the far region's
		// band (which reaches about x = 3). Column 1 fades into window 2,
		// whose square reaches x = 32, so some of it may change.
		var ti, tj int
		if _, err := fmt.Sscanf(name, "tile_%d_%d", &ti, &tj); err == nil && ti == 0 && !same {
			t.Errorf("%s (column %d) was rewritten by an edit at the far east", name, ti)
		}
	}
	if kept == 0 || rewritten == 0 {
		t.Errorf("%d tiles kept, %d rewritten: want some of each", kept, rewritten)
	}
	t.Logf("first run %s, incremental run %s: %d tiles kept, %d recooked", first.Round(time.Millisecond),
		second.Round(time.Millisecond), kept, rewritten)
	if second >= first {
		t.Errorf("the incremental run (%s) was not faster than the first (%s)", second, first)
	}

	// The same world from nothing: identical heights, identical tile hashes.
	fresh := t.TempDir()
	run(fresh)
	for _, f := range []string{terrain.HeightmapFile, terrain.MetaFile} {
		x, _ := os.ReadFile(filepath.Join(stage, f))
		y, _ := os.ReadFile(filepath.Join(fresh, "ground", f))
		if string(x) != string(y) {
			t.Errorf("%s differs between the incremental run and a fresh one", f)
		}
	}
	man3 := procgen.ReadManifest(fresh).Stages["ground"]
	for name, h := range man3.Tiles {
		if man2.Tiles[name] != h {
			t.Errorf("%s: incremental hash %s, fresh %s", name, man2.Tiles[name], h)
		}
	}
	// The erosion windows are outputs of the stage (pruned with it).
	if _, err := os.Stat(filepath.Join(stage, "erosion", "w_0_0.bin")); err != nil {
		t.Errorf("no erosion window file: %v", err)
	}
}

// writePresetRelief rewrites a seamPresets preset's relief.
func writePresetRelief(t *testing.T, path string, relief float64) {
	t.Helper()
	d, err := procgen.ReadDoc(path)
	if err != nil {
		t.Fatal(err)
	}
	d.Stages[0].Params["relief"] = procgen.Float(relief)
	if err := procgen.WriteDoc(path, d); err != nil {
		t.Fatal(err)
	}
}
