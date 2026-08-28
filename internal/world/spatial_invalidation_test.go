package world

// spatial_invalidation_test.go — the spatial hash is keyed on world cells
// derived from each chunk's live ModelMatrix, so it is only valid until one of
// those transforms changes. Before chunk transforms were live this could not
// happen: Origin was consumed at load and never moved again.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func hashTestLevel() *Level {
	ch := Chunk{
		ID: "room", Scale: 1,
		Points:   []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}},
		MinBound: mgl32.Vec3{0, 0, 0},
		MaxBound: mgl32.Vec3{10, 4, 10},
		Sectors: []Sector{{
			ID:           "s0",
			PointIndices: []int{0, 1, 2, 3},
			MinBound:     mgl32.Vec2{0, 0},
			MaxBound:     mgl32.Vec2{10, 10},
			Floor:        Floor{Enabled: true, Height: 0},
			Ceiling:      Ceiling{Height: 4},
		}},
	}
	return &Level{Chunks: []Chunk{ch}}
}

// Moving a chunk without re-bucketing leaves it findable nowhere: not at its
// new position (no refs in that cell) and not at its old one (the stale refs
// fail the exact test, which uses the new matrix).
func TestSpatialHashMustBeRebuiltAfterAChunkMoves(t *testing.T) {
	lvl := hashTestLevel()
	RebuildSpatialHash(lvl)

	inside := mgl32.Vec3{5, 1, 5}
	if ch, _ := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, inside); ch == nil {
		t.Fatal("fixture: the chunk is not findable at its own position to begin with")
	}

	// Move it, and rebuild the way the editor now does.
	offset := mgl32.Vec3{200, 0, -100}
	lvl.Chunks[0].Origin = offset
	RebuildSpatialHash(lvl)

	if ch, _ := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, inside.Add(offset)); ch == nil {
		t.Error("after moving the chunk and rebuilding, it is not findable at its new position")
	}
	if ch, _ := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, inside); ch != nil {
		t.Error("the chunk is still findable at its old position after being moved")
	}
}

// Rotation and scale change the world footprint too, so they invalidate the
// hash for the same reason translation does.
func TestSpatialHashFollowsRotationAndScale(t *testing.T) {
	lvl := hashTestLevel()
	lvl.Chunks[0].Scale = 3
	RebuildSpatialHash(lvl)

	// Local (9,·,9) is inside the sector; at scale 3 that is world (27,·,27),
	// which is outside the un-scaled footprint entirely.
	if ch, _ := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, mgl32.Vec3{27, 3, 27}); ch == nil {
		t.Error("a 3x-scaled chunk is not findable at its scaled-out position")
	}
	if ch, _ := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, mgl32.Vec3{35, 3, 35}); ch != nil {
		t.Error("found the chunk beyond its own scaled footprint")
	}
}

// RebuildIndexes must keep doing the bucketing, since that is the path the
// loader and the vehicle-binding editor calls go through.
func TestRebuildIndexesRebucketsTheHash(t *testing.T) {
	lvl := hashTestLevel()
	lvl.Chunks[0].Origin = mgl32.Vec3{50, 0, 50}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	if ch, _ := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, mgl32.Vec3{55, 1, 55}); ch == nil {
		t.Error("RebuildIndexes did not bucket the moved chunk at its world position")
	}
}

// A frame-owned chunk whose frames never got refreshed has the ZERO matrix in
// Xform, not the identity. Composing against it collapses the chunk to a point
// and — worse — makes WorldToLocal invert a singular matrix and return NaN,
// which then spreads silently through collision and picking.
func TestModelMatrixSurvivesAnUnrefreshedFrame(t *testing.T) {
	ch := Chunk{
		ID: "cabin", FrameOwner: "ship", Scale: 1,
		Origin: mgl32.Vec3{3, 4, 5},
		// Xform left at its zero value, as RebuildIndexes leaves it when
		// BuildFrames fails.
	}

	m := ch.ModelMatrix()
	if m == (mgl32.Mat4{}) {
		t.Fatal("ModelMatrix returned the zero matrix for an unrefreshed frame-owned chunk")
	}
	if got := ch.LocalToWorld(mgl32.Vec3{1, 0, 0}); got != (mgl32.Vec3{4, 4, 5}) {
		t.Errorf("LocalToWorld = %v, want the chunk's own placement applied (4,4,5)", got)
	}
	back := ch.WorldToLocal(mgl32.Vec3{4, 4, 5})
	for i := 0; i < 3; i++ {
		if back[i] != back[i] { // NaN
			t.Fatalf("WorldToLocal produced NaN: %v", back)
		}
	}
	if back != (mgl32.Vec3{1, 0, 0}) {
		t.Errorf("WorldToLocal = %v, want the round trip (1,0,0)", back)
	}
}

// A refreshed Xform must still compose normally — the guard must not swallow
// real frame transforms.
func TestModelMatrixStillComposesARefreshedFrame(t *testing.T) {
	ch := Chunk{
		ID: "cabin", FrameOwner: "ship", Scale: 1,
		Origin: mgl32.Vec3{1, 0, 0},
		Xform:  mgl32.Translate3D(100, 0, 0),
	}
	if got := ch.LocalToWorld(mgl32.Vec3{}); got != (mgl32.Vec3{101, 0, 0}) {
		t.Errorf("LocalToWorld = %v, want (101,0,0) — the frame composed over the chunk's own origin", got)
	}
}

// Duplicating a vehicle interior must keep it in the vehicle: dropping
// FrameOwner turned the copy into a world chunk whose geometry, still authored
// around the ship's origin, was then read as world geometry at (0,0,0).
func TestChunkCloneKeepsItsFrameOwner(t *testing.T) {
	ch := Chunk{ID: "cabin", FrameOwner: "ship", Scale: 1}
	cp := ch.Clone("cabin_copy", "_copy")
	if cp.FrameOwner != "ship" {
		t.Errorf("clone FrameOwner = %q, want %q", cp.FrameOwner, "ship")
	}
	// Xform is runtime-only and RefreshFrames owns it; the clone must start
	// unrefreshed rather than inheriting a stale transform.
	if cp.Xform != (mgl32.Mat4{}) {
		t.Error("clone inherited a stale Xform; RefreshFrames should be the only writer")
	}
}

// A cloned chunk renames every sector it contains, so a biome sector's drape
// binding has to be renamed with them — left alone it would name the ORIGINAL
// chunk's surface, which is a different height in a different place.
func TestChunkCloneRemapsTheDrapeBinding(t *testing.T) {
	ch := Chunk{
		ID: "villa", Scale: 1,
		Points:  []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}},
		Sectors: []Sector{{ID: "terrace", PointIndices: []int{0, 1, 2, 3}}},
		BiomeSectors: []BiomeSector{{
			ID:          "grass",
			Points:      []mgl32.Vec2{{2, 2}, {8, 2}, {8, 8}},
			DrapeSector: "terrace",
		}},
	}

	cp := ch.Clone("villa_copy", "_copy")

	if got := cp.Sectors[0].ID; got != "terrace_copy" {
		t.Fatalf("fixture: cloned sector ID = %q, want %q", got, "terrace_copy")
	}
	if got := cp.BiomeSectors[0].DrapeSector; got != "terrace_copy" {
		t.Errorf("cloned paint is bound to %q, want %q — it must follow the copy's "+
			"own sector, not the original's", got, "terrace_copy")
	}
	// The original is untouched.
	if got := ch.BiomeSectors[0].DrapeSector; got != "terrace" {
		t.Errorf("cloning changed the original's binding to %q", got)
	}
}

// A binding to something outside this chunk (spilled paint) is not in the
// rename map and must be left as it is.
func TestChunkCloneLeavesAForeignDrapeBindingAlone(t *testing.T) {
	ch := Chunk{
		ID: "painted", Scale: 1,
		BiomeSectors: []BiomeSector{{
			ID:          "spill",
			Points:      []mgl32.Vec2{{2, 2}, {8, 2}, {8, 8}},
			DrapeSector: "someOtherChunksFloor",
		}},
	}
	cp := ch.Clone("painted_copy", "_copy")
	if got := cp.BiomeSectors[0].DrapeSector; got != "someOtherChunksFloor" {
		t.Errorf("a binding to another chunk's sector was rewritten to %q", got)
	}
}
