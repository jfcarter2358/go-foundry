package world

// scenery_chunk_test.go — scenery streaming with a chunk (Scenery.Chunk):
// switched off with it, unloaded with an unload_when_disabled one, and
// reported by the frame's reconcile so the indexes rebuild.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func sceneryChunkLevel(t *testing.T) *Level {
	t.Helper()
	ch := enableTestChunk(t)
	rock := NewScenery("rock", "", mgl32.Vec3{5, 0, 5})
	rock.Chunk = "room"
	loose := NewScenery("loose", "", mgl32.Vec3{5, 0, 5})
	lvl := &Level{Chunks: []Chunk{*ch}, Scenery: []Scenery{rock, loose}}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	SyncChunkGeometry(lvl)
	return lvl
}

func TestSceneryFollowsItsChunksSwitch(t *testing.T) {
	lvl := sceneryChunkLevel(t)
	if !lvl.Scenery[0].InWorld() || !lvl.Scenery[1].InWorld() {
		t.Fatal("scenery is out while its chunk is on")
	}

	lvl.Chunks[0].Disabled = true
	if !SyncChunkGeometry(lvl) {
		t.Fatal("the toggle was not reported")
	}
	if lvl.Scenery[0].InWorld() {
		t.Error("a prop streaming with a disabled chunk is still in the world")
	}
	if !lvl.Scenery[1].InWorld() {
		t.Error("a prop naming no chunk went out with one")
	}
	if lvl.Scenery[0].Unloaded {
		t.Error("a plain disabled chunk unloaded its scenery; only unload_when_disabled should")
	}
	if SyncChunkGeometry(lvl) {
		t.Error("the toggle was reported twice")
	}

	lvl.Chunks[0].Disabled = false
	SyncChunkGeometry(lvl)
	if !lvl.Scenery[0].InWorld() {
		t.Error("the prop did not come back with its chunk")
	}
}

func TestSceneryUnloadsWithAnUnloadWhenDisabledChunk(t *testing.T) {
	lvl := sceneryChunkLevel(t)
	lvl.Chunks[0].UnloadWhenDisabled = true
	// Bounds stand in for a loaded model: Delete clears them.
	lvl.Scenery[0].LocalMin, lvl.Scenery[0].LocalMax = mgl32.Vec3{-1, 0, -1}, mgl32.Vec3{1, 2, 1}

	lvl.Chunks[0].Disabled = true
	SyncChunkGeometry(lvl)
	s := &lvl.Scenery[0]
	if !s.Unloaded || s.HasBounds() {
		t.Fatalf("not released with its chunk: unloaded %v, bounds %v..%v", s.Unloaded, s.LocalMin, s.LocalMax)
	}

	lvl.Chunks[0].Disabled = false
	if !SyncChunkGeometry(lvl) {
		t.Fatal("re-enable not reported")
	}
	if s.Unloaded || !s.InWorld() {
		t.Error("not rebuilt when its chunk came back")
	}
}

func TestSceneryNamingAMissingChunkIsAlwaysIn(t *testing.T) {
	lvl := sceneryChunkLevel(t)
	lvl.Scenery[0].Chunk = "nowhere"
	lvl.Chunks[0].Disabled = true
	SyncChunkGeometry(lvl)
	if !lvl.Scenery[0].InWorld() {
		t.Error("a prop naming a chunk that does not exist went out")
	}
}

func TestSceneryChunkRoundTrips(t *testing.T) {
	s := NewScenery("rock", "models/rock.fmdl", mgl32.Vec3{})
	s.Chunk = "zone_3"
	if got := SceneryFromRaw(SceneryToRaw(&s)).Chunk; got != "zone_3" {
		t.Errorf("chunk %q after a round trip", got)
	}
	s.Chunk = ""
	if r := SceneryToRaw(&s); r.Chunk != "" {
		t.Errorf("an unassigned prop wrote chunk %q", r.Chunk)
	}
}

func TestSceneryChunkAtPicksTheFootprint(t *testing.T) {
	ch := enableTestChunk(t) // bounds from its brush: 0..10 x 0..4 x 0..10
	big := Chunk{ID: "big", MinBound: mgl32.Vec3{-50, -1, -50}, MaxBound: mgl32.Vec3{50, 5, 50}}
	high := Chunk{ID: "high", MinBound: mgl32.Vec3{0, 40, 0}, MaxBound: mgl32.Vec3{10, 45, 10}}
	off := *ch
	off.Disabled = true
	lvl := &Level{Chunks: []Chunk{big, high, off}}

	// Standing on the room's top, over its footprint: the room, not the
	// slab 40 m up, and not the bigger area round it.
	if got := SceneryChunkAt(lvl, mgl32.Vec3{5, 4, 5}); got != "room" {
		t.Errorf("got %q, want room (switched off, but still the area it stands in)", got)
	}
	if got := SceneryChunkAt(lvl, mgl32.Vec3{20, 0, 20}); got != "big" {
		t.Errorf("got %q, want big", got)
	}
	if got := SceneryChunkAt(lvl, mgl32.Vec3{200, 0, 0}); got != "" {
		t.Errorf("got %q outside every footprint", got)
	}
}

func TestTreesFollowTheirChunksSwitch(t *testing.T) {
	lvl := sceneryChunkLevel(t)
	in := NewTree("spruce", FoliageSitkaSpruce, mgl32.Vec3{5, 0, 5})
	in.Chunk = "room"
	loose := NewTree("loose", FoliageSitkaSpruce, mgl32.Vec3{5, 0, 5})
	lvl.Trees = []Tree{in, loose}
	SyncChunkGeometry(lvl)
	count := func() int {
		n := 0
		bs, _ := TreeBatches(lvl)
		for _, b := range bs {
			n += len(b.Instances)
		}
		return n
	}
	_, fpOn := TreeBatches(lvl)
	if count() != 2 {
		t.Fatalf("%d trees batched while their chunk is on, want 2", count())
	}

	lvl.Chunks[0].Disabled = true
	if !SyncChunkGeometry(lvl) {
		t.Fatal("a tree's chunk going off was not reported")
	}
	if lvl.Trees[0].InWorld() || !lvl.Trees[1].InWorld() {
		t.Fatalf("in-world after the toggle: %v, %v; want false, true", lvl.Trees[0].InWorld(), lvl.Trees[1].InWorld())
	}
	if _, fp := TreeBatches(lvl); fp == fpOn || count() != 1 {
		t.Errorf("the batches still hold the switched-off tree (%d batched)", count())
	}

	lvl.Chunks[0].Disabled = false
	SyncChunkGeometry(lvl)
	if _, fp := TreeBatches(lvl); fp != fpOn {
		t.Error("the batches did not come back as they were")
	}
}

func TestTreeChunkRoundTrips(t *testing.T) {
	tr := NewTree("spruce", FoliageSitkaSpruce, mgl32.Vec3{})
	tr.Chunk = "zone_3"
	if got := TreeFromRaw(TreeToRaw(&tr)).Chunk; got != "zone_3" {
		t.Errorf("chunk %q after a round trip", got)
	}
}
