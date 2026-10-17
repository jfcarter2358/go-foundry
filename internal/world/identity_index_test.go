package world

// identity_index_test.go — LevelIDIndex answers what uniqueLevelID does, and
// does it without the quadratic rescan.

import (
	"fmt"
	"testing"
	"time"
)

func TestLevelIDIndexMatchesUniqueLevelID(t *testing.T) {
	// Two copies of one level; mint the same sequence of bases in each, one
	// through the index and one through uniqueLevelID (appending the answer,
	// as the loader does), and they must agree at every step.
	build := func() *Level {
		return &Level{
			Entities: []Entity{{ID: "crate"}, {ID: "crate_2"}},
			Volumes:  []Volume{{ID: "rock"}},
			Chunks:   []Chunk{{ID: "ground", Brushes: []Brush{{ID: "rock_3"}}}},
			Scenery:  []Scenery{{ID: "rock_2"}},
		}
	}
	a, b := build(), build()
	ix := NewLevelIDIndex(a)
	bases := []string{"rock", "rock", "", "crate", "player", "rock", "fresh", "fresh", "", "spawn"}
	for i, base := range bases {
		got := ix.Unique(base, "scenery")
		want := UniqueSceneryID(b, base)
		if got != want {
			t.Fatalf("step %d (%q): index says %q, uniqueLevelID %q", i, base, got, want)
		}
		b.Scenery = append(b.Scenery, Scenery{ID: want})
	}
	if !ix.Taken("player") || !ix.Taken("") || !ix.Taken("crate_2") || ix.Taken("nobody") {
		t.Error("Taken disagrees with LevelIDTaken")
	}
}

func TestLevelIDIndexSeesEveryKind(t *testing.T) {
	lvl := &Level{
		Entities: []Entity{{ID: "e"}}, Volumes: []Volume{{ID: "v"}}, Sprites: []Sprite{{ID: "s"}},
		Emitters: []Emitter{{ID: "em"}}, Sounds: []Sound{{ID: "so"}}, Effects: []Effect{{ID: "ef"}},
		Scenery: []Scenery{{ID: "sc"}}, Trees: []Tree{{ID: "tr"}}, Decals: []Decal{{ID: "de"}},
		WaypointAnimations: []WaypointAnimation{{ID: "wa"}},
		Chunks:             []Chunk{{ID: "ch", Brushes: []Brush{{ID: "br"}}}},
	}
	lvl.Lights.Directional = append(lvl.Lights.Directional, DirectionalLight{ID: "sun"})
	lvl.Lights.Points = append(lvl.Lights.Points, PointLight{ID: "lamp"})
	ix := NewLevelIDIndex(lvl)
	for _, id := range []string{"e", "v", "s", "em", "so", "ef", "sc", "tr", "de", "wa", "ch", "br", "sun", "lamp"} {
		if !LevelIDTaken(lvl, id) {
			t.Errorf("LevelIDTaken does not see %q", id)
		}
		if !ix.Taken(id) {
			t.Errorf("the index does not see %q", id)
		}
	}
}

func TestMintingTenThousandIDsIsLinear(t *testing.T) {
	// 20k placements all asking for the same base — the worst case for the
	// suffix search as well as the rescan. The old path took tens of seconds
	// here; the index takes milliseconds. The bound is loose for slow CI.
	lvl := &Level{}
	ix := NewLevelIDIndex(lvl)
	start := time.Now()
	seen := map[string]bool{}
	for i := 0; i < 20000; i++ {
		id := ix.Unique("rock", "scenery")
		if seen[id] {
			t.Fatalf("minted %q twice", id)
		}
		seen[id] = true
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("20k IDs took %s", took)
	}
	if !seen["rock"] || !seen["rock_20000"] {
		t.Error("the suffixes are not rock, rock_2 ... rock_20000")
	}
}

func BenchmarkLevelIDIndexDistinctIDs(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ix := NewLevelIDIndex(&Level{})
		for k := 0; k < 10000; k++ {
			ix.Unique(fmt.Sprintf("gen.canyon.talus.r%d", k), "scenery")
		}
	}
}
