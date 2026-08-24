package main

// sector_edge_id_test.go — the regression suite that makes edge identity safe.
//
// Every editor operation that touches a sector's edge ring has to keep three
// index-aligned slices in step and carry each edge's name across. The invariant
// is one function (validateSectorEdges), so the shape of nearly every test here
// is: build a sector, remember what an edge is called, do the thing, assert the
// invariant still holds and the name still points at the same edge.

import (
	"foundry/internal/gfx"
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// withoutGeometryUploads makes Geometry.Load stop at the CPU mesh, so an editor
// operation that rebuilds a sector can run without a GL context. Restored on
// cleanup, in the style of withTempTextureDir.
func withoutGeometryUploads(t *testing.T) {
	t.Helper()
	prev := gfx.GeometryUploadsEnabled
	gfx.GeometryUploadsEnabled = false
	t.Cleanup(func() { gfx.GeometryUploadsEnabled = prev })
}

// edgeIDTestChunk builds a single square sector with named edges, GL-free.
func edgeIDTestChunk(t *testing.T) *world.Chunk {
	t.Helper()
	withoutGeometryUploads(t)
	ch := &world.Chunk{
		Points:     []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}},
		PointNames: []string{"P0", "P1", "P2", "P3"},
	}
	idx := []int{0, 1, 2, 3}
	var walls []world.Wall
	for i := range idx {
		walls = append(walls, world.Wall{
			StartPoint: ch.Points[idx[i]],
			EndPoint:   ch.Points[idx[(i+1)%len(idx)]],
			Texture:    "concrete",
			Collision:  true,
			Visible:    true,
			Alpha:      1,
		})
	}
	s := world.Sector{
		ID:           "room",
		PointIndices: idx,
		EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
		Walls:        walls,
		Floor:        world.Floor{Enabled: true, VertexOffsets: []float32{0, 0, 0, 0}},
		Ceiling:      world.Ceiling{Enabled: true, Height: 4, VertexOffsets: []float32{0, 0, 0, 0}},
	}
	world.EnsureEdgeIDs(&s)
	world.SyncEdgeRecordIDs(&s)
	ch.Sectors = []world.Sector{s}
	return ch
}

// mustValidate fails the test unless every sector in ch holds the invariant.
func mustValidate(t *testing.T, ch *world.Chunk, stage string) {
	t.Helper()
	if err := world.ValidateChunkEdges(ch); err != nil {
		t.Fatalf("%s: edge invariant broken: %v", stage, err)
	}
}

func TestEnsureEdgeIDsMintsOnePerEdge(t *testing.T) {
	ch := edgeIDTestChunk(t)
	s := &ch.Sectors[0]
	if len(s.EdgeIDs) != 4 {
		t.Fatalf("got %d edge IDs, want 4", len(s.EdgeIDs))
	}
	mustValidate(t, ch, "fresh sector")
}

func TestMigrateEdgeIDIgnoresDirection(t *testing.T) {
	// The whole reason the migration key is an unordered pair: the loader's
	// winding fixup turns edge A→B into B→A, and a direction-sensitive key would
	// rename the edge on the next save.
	if got, want := world.MigrateEdgeID("A", "B"), world.MigrateEdgeID("B", "A"); got != want {
		t.Errorf("MigrateEdgeID is direction-sensitive: %q vs %q", got, want)
	}
	if got := world.MigrateEdgeID("B", "A"); got != "A|B" {
		t.Errorf("MigrateEdgeID(B,A) = %q, want %q", got, "A|B")
	}
}

func TestDedupeSectorEdgeIDsResolvesCollisions(t *testing.T) {
	ch := edgeIDTestChunk(t)
	s := &ch.Sectors[0]
	s.EdgeIDs = []string{"dup", "dup", "", "other"}
	world.DedupeSectorEdgeIDs(s)
	world.SyncEdgeRecordIDs(s)
	mustValidate(t, ch, "after dedupe")

	seen := map[string]bool{}
	for i, id := range s.EdgeIDs {
		if id == "" {
			t.Errorf("edge %d still has no ID", i)
		}
		if seen[id] {
			t.Errorf("edge %d reuses ID %q", i, id)
		}
		seen[id] = true
	}
	if s.EdgeIDs[0] != "dup" {
		t.Errorf("first claimant lost its name: got %q, want %q", s.EdgeIDs[0], "dup")
	}
}

func TestInsertSectorVertexKeepsFirstHalfID(t *testing.T) {
	ch := edgeIDTestChunk(t)
	s := &ch.Sectors[0]
	splitID := s.EdgeIDs[1]
	otherID := s.EdgeIDs[3]

	// Add the midpoint of edge 1 to the pool, then split edge 1 on it.
	newPt := ch.AddPoint(mgl32.Vec2{10, 5})
	insertSectorVertex(ch, s, 1, newPt, 0.5)

	mustValidate(t, ch, "after vertex insert")
	if len(s.EdgeIDs) != 5 {
		t.Fatalf("got %d edges after split, want 5", len(s.EdgeIDs))
	}
	// splitSectorEdgeNear leaves the first half selected as "the edge the user
	// clicked", so that half has to stay the same object to a script.
	if s.EdgeIDs[1] != splitID {
		t.Errorf("first half changed identity: got %q, want %q", s.EdgeIDs[1], splitID)
	}
	if s.EdgeIDs[2] == splitID {
		t.Errorf("second half reused the split edge's ID %q", splitID)
	}
	// Everything past the split shifted by one index but must keep its name.
	if s.EdgeIDs[4] != otherID {
		t.Errorf("edge after the split lost its ID: got %q, want %q", s.EdgeIDs[4], otherID)
	}
}

func TestRemoveSectorPointKeepsSurvivingEdgeID(t *testing.T) {
	ch := edgeIDTestChunk(t)
	s := &ch.Sectors[0]
	// Split first so removal leaves a legal (>3 vertex) polygon.
	newPt := ch.AddPoint(mgl32.Vec2{10, 5})
	insertSectorVertex(ch, s, 1, newPt, 0.5)
	survivorID := s.EdgeIDs[1]

	removeSectorPoint(ch, s, newPt)

	mustValidate(t, ch, "after vertex removal")
	if len(s.EdgeIDs) != 4 {
		t.Fatalf("got %d edges after removal, want 4", len(s.EdgeIDs))
	}
	if world.PolyEdgeIdxByID(s, survivorID) < 0 {
		t.Errorf("the merged-into edge %q lost its ID", survivorID)
	}
}

func TestConvertEdgePreservesID(t *testing.T) {
	// The site that proves identity belongs to the edge: a wall that becomes a
	// portal and comes back must be the same object throughout, or a script's
	// address breaks the moment an author opens a doorway.
	ch := edgeIDTestChunk(t)
	s := &ch.Sectors[0]
	want := s.EdgeIDs[2]

	convertEdge(ch, s, "wall", 2, "portal")
	mustValidate(t, ch, "after wall→portal")
	if got := s.EdgeIDs[2]; got != want {
		t.Fatalf("wall→portal changed the edge ID: got %q, want %q", got, want)
	}
	if p := world.PortalByEdgeID(s, want); p == nil {
		t.Fatalf("edge %q does not resolve to a portal after conversion", want)
	} else if p.ID != want {
		t.Errorf("portal record ID %q does not match edge ID %q", p.ID, want)
	}
	if world.WallByEdgeID(s, want) != nil {
		t.Errorf("edge %q still resolves as a wall after conversion", want)
	}

	kind, idx, ok := world.EdgeRecordByID(s, want)
	if !ok || kind != "portal" {
		t.Fatalf("EdgeRecordByID(%q) = (%q, %d, %v), want a portal", want, kind, idx, ok)
	}

	convertEdge(ch, s, "portal", idx, "wall")
	mustValidate(t, ch, "after portal→wall")
	if got := s.EdgeIDs[2]; got != want {
		t.Fatalf("portal→wall changed the edge ID: got %q, want %q", got, want)
	}
	if w := world.WallByEdgeID(s, want); w == nil {
		t.Errorf("edge %q does not resolve back to a wall", want)
	}
}

func TestInferPortalsCarriesEdgeIDs(t *testing.T) {
	withoutGeometryUploads(t)
	ch := splitTestChunk()
	for si := range ch.Sectors {
		world.EnsureEdgeIDs(&ch.Sectors[si])
		world.SyncEdgeRecordIDs(&ch.Sectors[si])
	}
	// A's edge 1 is the shared one; InferPortals will promote it to a portal
	// without anyone asking, which is exactly the case an index could not survive.
	sharedID := ch.Sectors[0].EdgeIDs[1]
	unsharedID := ch.Sectors[0].EdgeIDs[0]

	ch.InferPortals()

	mustValidate(t, ch, "after InferPortals")
	a := &ch.Sectors[0]
	if a.EdgeKinds[1] != "portal" {
		t.Fatalf("shared edge was not promoted: kind %q", a.EdgeKinds[1])
	}
	if a.EdgeIDs[1] != sharedID {
		t.Errorf("promoted edge lost its ID: got %q, want %q", a.EdgeIDs[1], sharedID)
	}
	if a.EdgeIDs[0] != unsharedID {
		t.Errorf("untouched edge lost its ID: got %q, want %q", a.EdgeIDs[0], unsharedID)
	}
	if p := world.PortalByEdgeID(a, sharedID); p == nil {
		t.Errorf("promoted edge %q does not resolve to a portal", sharedID)
	}
}

func TestInferPortalsSubstitutesRatherThanDropping(t *testing.T) {
	// A kind/record mismatch used to drop the edge, leaving Walls one short of
	// the ring and silently shifting every record after it. Substituting a blank
	// wall keeps the alignment, which is what the invariant checks.
	ch := edgeIDTestChunk(t)
	s := &ch.Sectors[0]
	s.EdgeKinds[2] = "portal" // claims a portal that does not exist

	ch.InferPortals()

	mustValidate(t, ch, "after mismatched InferPortals")
	if len(ch.Sectors[0].Walls) != 4 {
		t.Errorf("got %d walls after the mismatch, want 4", len(ch.Sectors[0].Walls))
	}
}

func TestSplitSectorGivesBothHalvesValidEdges(t *testing.T) {
	ch := edgeIDTestChunk(t)
	splitSector(ch, 0, 0, 2)

	mustValidate(t, ch, "after sector split")
	if len(ch.Sectors) != 2 {
		t.Fatalf("got %d sectors after split, want 2", len(ch.Sectors))
	}
	if ch.Sectors[0].ID == ch.Sectors[1].ID {
		t.Errorf("both halves share sector ID %q", ch.Sectors[0].ID)
	}
}

func TestUniqueSectorIDAvoidsCollisions(t *testing.T) {
	ch := edgeIDTestChunk(t)
	ch.Sectors = append(ch.Sectors, world.Sector{ID: "room_2"})

	if got := world.UniqueSectorID(ch, "room"); got == "room" || got == "room_2" {
		t.Errorf("UniqueSectorID returned a name already in use: %q", got)
	}
	if got := world.UniqueSectorID(ch, "hall"); got != "hall" {
		t.Errorf("UniqueSectorID renamed a free name: got %q, want %q", got, "hall")
	}
}

func TestResolveLevelIDCollisionsRenamesDuplicates(t *testing.T) {
	chunks := []world.Chunk{
		{ID: "c", Sectors: []world.Sector{{ID: "dup"}}},
		{ID: "c", Sectors: []world.Sector{{ID: "dup"}}},
	}
	world.ResolveLevelIDCollisions(chunks)

	if chunks[0].ID == chunks[1].ID {
		t.Errorf("chunk IDs still collide: both %q", chunks[0].ID)
	}
	if chunks[0].Sectors[0].ID == chunks[1].Sectors[0].ID {
		t.Errorf("sector IDs still collide: both %q", chunks[0].Sectors[0].ID)
	}
	// The first claimant keeps the authored name, so a level that was already
	// unambiguous does not churn.
	if chunks[0].ID != "c" || chunks[0].Sectors[0].ID != "dup" {
		t.Errorf("first claimant was renamed: chunk %q, sector %q",
			chunks[0].ID, chunks[0].Sectors[0].ID)
	}
}

func TestAssignLightIDsMintsAndDedupes(t *testing.T) {
	ls := &world.LightSet{
		Directional: []world.DirectionalLight{{}, {ID: "sun"}},
		Points:      []world.PointLight{{ID: "sun"}, {}},
	}
	world.AssignLightIDs(ls)

	seen := map[string]bool{}
	for _, d := range ls.Directional {
		if d.ID == "" {
			t.Error("directional light left without an ID")
		}
		if seen[d.ID] {
			t.Errorf("duplicate light ID %q", d.ID)
		}
		seen[d.ID] = true
	}
	for _, p := range ls.Points {
		if p.ID == "" {
			t.Error("point light left without an ID")
		}
		if seen[p.ID] {
			t.Errorf("duplicate light ID %q", p.ID)
		}
		seen[p.ID] = true
	}
}
