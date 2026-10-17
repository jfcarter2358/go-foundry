package levelio

// scenery_many_test.go — a level with generator-sized scenery lists loads in
// linear time.
//
// The procedural generators write tens of thousands of scenery placements
// into one level. The loader used to mint each placement's ID with
// world.UniqueSceneryID, which rescans every list in the level per call:
// quadratic, and minutes at 20k. It now mints against one world.LevelIDIndex
// snapshot. This loads 20k placements (half of them sharing an ID, so the
// suffix search is exercised too) and checks both the time and the IDs.

import (
	"fmt"
	"io"
	"log"
	"testing"
	"time"

	"foundry/internal/testenv"
	"foundry/internal/world"
)

func manySceneryRaw(n int) world.RawLevel {
	var raw world.RawLevel
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("gen.canyon.talus.r%d", i)
		if i%2 == 1 {
			id = "rock" // collides with every other odd one
		}
		raw.Scenery = append(raw.Scenery, world.RawScenery{
			ID: id, Model: "generated/none/rock.fnm",
			Position: world.RawVec3{X: float32(i), Y: 0, Z: 0},
		})
	}
	raw.Trees = append(raw.Trees, world.RawTree{ID: "rock"})
	return raw
}

func TestManySceneryLoadsInLinearTime(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	// Every placement names a mesh that is not there; the loader logs each.
	prev := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(prev) })

	const n = 20000
	raw := manySceneryRaw(n)
	var lvl world.Level
	start := time.Now()
	if err := loadLevelObjects(raw, &lvl, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if len(lvl.Scenery) != n {
		t.Fatalf("%d placements loaded of %d", len(lvl.Scenery), n)
	}
	seen := map[string]bool{}
	for _, s := range lvl.Scenery {
		if seen[s.ID] {
			t.Fatalf("ID %q minted twice", s.ID)
		}
		seen[s.ID] = true
	}
	if lvl.Scenery[0].ID != "gen.canyon.talus.r0" || lvl.Scenery[1].ID != "rock" || lvl.Scenery[3].ID != "rock_2" {
		t.Errorf("IDs %q %q %q: a free ID must be kept and a taken one suffixed",
			lvl.Scenery[0].ID, lvl.Scenery[1].ID, lvl.Scenery[3].ID)
	}
	if tree := lvl.Trees[0].ID; seen[tree] {
		t.Errorf("the tree took placement ID %q", tree)
	}
	t.Logf("loaded %d placements' objects in %s", n, took)
	// Quadratic was ~minutes here; linear is well under a second. Loose for
	// a slow machine.
	if took > 10*time.Second {
		t.Errorf("loading %d placements took %s", n, took)
	}
}

func BenchmarkLoadManyScenery(b *testing.B) {
	// testenv.WithoutGeometryUploads takes a *testing.T; the benchmark runs
	// no GL either way, since every model is missing.
	prev := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(prev)
	raw := manySceneryRaw(10000)
	for i := 0; i < b.N; i++ {
		var lvl world.Level
		if err := loadLevelObjects(raw, &lvl, map[string]bool{}); err != nil {
			b.Fatal(err)
		}
	}
}
