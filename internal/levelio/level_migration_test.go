package levelio

// level_migration_test.go — edge IDs across the YAML round trip.
//
// The point of these is the migration path. A level written before `id:` existed
// has to gain stable IDs on load, keep them when saved, and read back identically
// — including a clockwise-wound sector, which the loader silently re-winds and
// which is exactly where a direction-sensitive naming scheme would fall apart.
//
// These use loadRawChunk/chunkToRaw rather than LoadLevel, so they stay headless:
// the conversion is pure, and the geometry upload it would otherwise trigger is
// switched off by testenv.WithoutGeometryUploads.

import (
	"foundry/internal/geom"
	"foundry/internal/testenv"
	"foundry/internal/world"
	"testing"
)

// squareRawChunk builds a four-edge sector wound counter-clockwise in the
// loader's convention, with no edge IDs — the shape of every level written
// before scripting existed.
func squareRawChunk(clockwise bool) world.RawChunk {
	pts := map[string]geom.Point{
		"A": {X: 0, Y: 0},
		"B": {X: 10, Y: 0},
		"C": {X: 10, Y: 10},
		"D": {X: 0, Y: 10},
	}
	ring := []string{"A", "B", "C", "D"}
	if clockwise {
		ring = []string{"A", "D", "C", "B"}
	}
	var edges []world.RawEdge
	for i := range ring {
		edges = append(edges, world.RawEdge{
			From:      ring[i],
			To:        ring[(i+1)%len(ring)],
			Kind:      "wall",
			Texture:   "concrete",
			Collision: true,
		})
	}
	return world.RawChunk{
		ID:     "chunk",
		Points: pts,
		Sectors: []world.RawSector{{
			ID:      "room",
			Floor:   world.RawFloor{Enabled: true, Height: 0, Texture: "concrete"},
			Ceiling: world.RawCeiling{Enabled: true, Height: 4, Texture: "concrete"},
			Edges:   edges,
		}},
	}
}

// edgeIDSet returns the sector's edge IDs as a set, for order-insensitive
// comparison across a re-wind.
func edgeIDSet(s *world.Sector) map[string]bool {
	out := make(map[string]bool, len(s.EdgeIDs))
	for _, id := range s.EdgeIDs {
		out[id] = true
	}
	return out
}

func TestEdgeIDsMintedForLegacyLevel(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, _, err := loadRawChunk(squareRawChunk(false))
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	if err := world.ValidateChunkEdges(&ch); err != nil {
		t.Fatalf("loaded chunk breaks the edge invariant: %v", err)
	}

	s := &ch.Sectors[0]
	// A file with no IDs migrates to names derived from its point pairs, which
	// is what makes the migration reviewable in a diff rather than opaque.
	want := map[string]bool{"A|B": true, "B|C": true, "C|D": true, "A|D": true}
	if got := edgeIDSet(s); len(got) != len(want) {
		t.Fatalf("got edge IDs %v, want %v", got, want)
	} else {
		for id := range want {
			if !got[id] {
				t.Errorf("missing migrated edge ID %q (got %v)", id, got)
			}
		}
	}
}

func TestEdgeIDsSurviveSaveLoadRoundTrip(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	for _, tc := range []struct {
		name      string
		clockwise bool
	}{
		{"counter-clockwise", false},
		{"clockwise", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, _, err := loadRawChunk(squareRawChunk(tc.clockwise))
			if err != nil {
				t.Fatalf("first load: %v", err)
			}
			before := edgeIDSet(&first.Sectors[0])

			raw, err := chunkToRaw(&first)
			if err != nil {
				t.Fatalf("chunkToRaw: %v", err)
			}
			// Saving must write the IDs out, or the next load would re-derive
			// them — which is the whole failure this is guarding against.
			for i, e := range raw.Sectors[0].Edges {
				if e.ID == "" {
					t.Fatalf("saved edge %d has no id", i)
				}
			}

			second, _, err := loadRawChunk(raw)
			if err != nil {
				t.Fatalf("second load: %v", err)
			}
			if err := world.ValidateChunkEdges(&second); err != nil {
				t.Fatalf("reloaded chunk breaks the edge invariant: %v", err)
			}

			after := edgeIDSet(&second.Sectors[0])
			if len(before) != len(after) {
				t.Fatalf("edge ID count changed across the round trip: %v then %v", before, after)
			}
			for id := range before {
				if !after[id] {
					t.Errorf("edge %q did not survive the round trip (got %v)", id, after)
				}
			}
		})
	}
}

func TestClockwiseSectorKeepsEdgeIDsThroughRewind(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	// The loader re-winds a clockwise sector, which flips every edge's direction.
	// A name derived from the `from` point alone would change here; the unordered
	// pair does not. This is the specific regression the migration key exists for.
	cw, _, err := loadRawChunk(squareRawChunk(true))
	if err != nil {
		t.Fatalf("load clockwise: %v", err)
	}
	ccw, _, err := loadRawChunk(squareRawChunk(false))
	if err != nil {
		t.Fatalf("load counter-clockwise: %v", err)
	}

	cwIDs, ccwIDs := edgeIDSet(&cw.Sectors[0]), edgeIDSet(&ccw.Sectors[0])
	if len(cwIDs) != len(ccwIDs) {
		t.Fatalf("winding changed the edge ID count: %v vs %v", cwIDs, ccwIDs)
	}
	for id := range ccwIDs {
		if !cwIDs[id] {
			t.Errorf("edge %q exists counter-clockwise but not clockwise (got %v)", id, cwIDs)
		}
	}
}

func TestEdgeIDsAuthoredInFileAreHonoured(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	raw := squareRawChunk(false)
	raw.Sectors[0].Edges[0].ID = "front_door"

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	s := &ch.Sectors[0]
	if world.PolyEdgeIdxByID(s, "front_door") < 0 {
		t.Fatalf("authored edge ID was not kept; got %v", s.EdgeIDs)
	}
	if w := world.WallByEdgeID(s, "front_door"); w == nil {
		t.Error(`edge "front_door" does not resolve to a wall`)
	}
}

func TestShippedTestLevelMigratesCleanly(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	// The level the editor actually opens. It predates edge IDs entirely, so this
	// is the real migration, not a fixture of one.
	raw, err := readRawLevelFile("resources/levels/test.yaml")
	if err != nil {
		t.Skipf("test level unavailable: %v", err)
	}
	for _, rc := range raw.Chunks {
		ch, _, err := loadRawChunk(rc)
		if err != nil {
			t.Fatalf("chunk %q: %v", rc.ID, err)
		}
		if err := world.ValidateChunkEdges(&ch); err != nil {
			t.Errorf("chunk %q breaks the edge invariant after migration: %v", rc.ID, err)
		}
	}

	// Its player start used to be an entity called "player" carrying the
	// player's own half-extents, from before the spawn point existed. It is a
	// spawn block now, and a level with neither drops the player at the origin.
	if raw.Spawn == nil {
		t.Error("the test level has no spawn point")
	}

	// Every entity in it has to load. The level places none today, but the walk
	// is what catches an entry written against a definition that has gone.
	for _, re := range raw.Entities {
		if _, err := world.EntityFromRaw(re, world.LoadEntityDef); err != nil {
			t.Errorf("entity %q: %v", re.ID, err)
		}
	}
}
