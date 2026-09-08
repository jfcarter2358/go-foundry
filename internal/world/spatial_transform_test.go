package world

// spatial_transform_test.go — the spatial hash and its linear fallbacks must
// resolve a world-space query correctly against a chunk that has its own live
// Origin/Rotation/Scale, not just against one sitting at the identity. See
// spatial.go's own doc comments: bucketing is conservative (a rotated
// chunk's axis-aligned world box), but the bounds test that actually decides
// the answer runs in each candidate's own local space.

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// placedRoom builds a 10×10×8 room (local, around its own origin) placed by
// the given transform. Chunk.MinBound/MaxBound are filled the way
// RecomputeBounds would.
func placedRoom(id string, origin mgl32.Vec3, rotation, scale float32) Chunk {
	ch := Chunk{
		ID:       id,
		Origin:   origin,
		Rotation: rotation,
		Scale:    scale,
		Brushes:  []Brush{NewBoxBrush("room", mgl32.Vec3{5, 4, 5}, mgl32.Vec3{5, 4, 5}, "")},
	}
	ch.RecomputeBounds()
	return ch
}

// lookupAllPaths runs every world-space lookup path this file exercises and
// reports whether each one found the room.
func lookupAllPaths(t *testing.T, lvl *Level, pos mgl32.Vec3) (sh, linear, chunkOnly bool) {
	t.Helper()
	if c := FindChunkSH(lvl.Spatial, lvl.Chunks, pos); c != nil {
		sh = true
	}
	if c := FindChunk(lvl.Chunks, pos); c != nil {
		linear = true
	}
	if c := FindChunk(lvl.Chunks, pos); c != nil {
		chunkOnly = true
	}
	return
}

func TestFindChunkResolvesAMovedChunk(t *testing.T) {
	ch := placedRoom("moved", mgl32.Vec3{100, 0, 200}, 0, 1)
	lvl := &Level{Chunks: []Chunk{ch}}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}

	// The room's centre in world space is Origin + (5, 4, 5) — inside.
	inside := mgl32.Vec3{105, 4, 205}
	if sh, linear, chunkOnly := lookupAllPaths(t, lvl, inside); !sh || !linear || !chunkOnly {
		t.Errorf("centre of the moved room not found: hash=%v linear=%v chunk=%v", sh, linear, chunkOnly)
	}

	// The room's local footprint, un-offset — now empty space.
	stale := mgl32.Vec3{5, 4, 5}
	if sh, linear, _ := lookupAllPaths(t, lvl, stale); sh || linear {
		t.Errorf("query at the chunk's pre-move footprint still resolved: hash=%v linear=%v (Origin must not be ignored)", sh, linear)
	}
}

func TestFindChunkResolvesARotatedChunk(t *testing.T) {
	// 90°: local +X maps to world -Z (mgl32's HomogRotate3DY convention), so
	// the room's local (5,5) footprint centre lands at world (0,4,-5).
	ch := placedRoom("rotated", mgl32.Vec3{}, float32(math.Pi/2), 1)
	lvl := &Level{Chunks: []Chunk{ch}}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}

	inside := mgl32.Vec3{0, 4, -5}
	if sh, linear, chunkOnly := lookupAllPaths(t, lvl, inside); !sh || !linear || !chunkOnly {
		t.Errorf("centre of the rotated room not found: hash=%v linear=%v chunk=%v", sh, linear, chunkOnly)
	}

	// The room's unrotated footprint centre — now empty space.
	stale := mgl32.Vec3{5, 4, 5}
	if sh, linear, _ := lookupAllPaths(t, lvl, stale); sh || linear {
		t.Errorf("query at the chunk's pre-rotation footprint still resolved: hash=%v linear=%v", sh, linear)
	}
}

func TestFindChunkResolvesAScaledChunk(t *testing.T) {
	// Scale 2: the local (5,5) centre lands at world (10,y,10), and Height (a
	// local Y baked into the mesh) scales too — floor/ceiling are 0/16, not 0/8.
	ch := placedRoom("scaled", mgl32.Vec3{}, 0, 2)
	lvl := &Level{Chunks: []Chunk{ch}}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}

	inside := mgl32.Vec3{10, 8, 10}
	if sh, linear, chunkOnly := lookupAllPaths(t, lvl, inside); !sh || !linear || !chunkOnly {
		t.Errorf("centre of the scaled room not found: hash=%v linear=%v chunk=%v", sh, linear, chunkOnly)
	}

	// Above the un-scaled ceiling height but below the actual (scaled) one.
	betweenCeilings := mgl32.Vec3{10, 12, 10}
	if sh, linear, _ := lookupAllPaths(t, lvl, betweenCeilings); !sh || !linear {
		t.Errorf("point between the local and scaled ceiling heights not found: hash=%v linear=%v (Height must scale with the chunk)", sh, linear)
	}
}

// TestSpatialHashHandlesChunksWithDifferentTransformsInTheSameCell is the
// crux of the spatial-hash rework: two chunks whose world-space footprints
// share a grid cell, each placed by a different transform, must each resolve
// only their own query — a candidate list is not all in the same local space.
func TestSpatialHashHandlesChunksWithDifferentTransformsInTheSameCell(t *testing.T) {
	a := placedRoom("a", mgl32.Vec3{0, 0, 0}, 0, 1)
	b := placedRoom("b", mgl32.Vec3{5, 0, 0}, float32(math.Pi/4), 1.5)
	lvl := &Level{Chunks: []Chunk{a, b}}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}

	// Deep inside A, away from B's rotated/scaled footprint.
	if c := FindChunkSH(lvl.Spatial, lvl.Chunks, mgl32.Vec3{1, 4, 1}); c == nil || c.ID != "a" {
		t.Errorf("query inside chunk A resolved to %v, want chunk \"a\"", c)
	}

	// B's own room centre (local (5,4,5)), carried out through its own
	// Origin/Rotation/Scale — computed via LocalToWorld rather than hand-derived
	// trig, since the point of this test is the hash's per-candidate dispatch,
	// not re-proving ModelMatrix's own math (chunk_transform_test.go does that).
	bCentre := lvl.Chunks[1].LocalToWorld(mgl32.Vec3{5, 4, 5})
	if c := FindChunkSH(lvl.Spatial, lvl.Chunks, bCentre); c == nil || c.ID != "b" {
		t.Errorf("query at chunk B's own centre (%v) resolved to %v, want chunk \"b\"", bCentre, c)
	}
}

func TestFindChunkAmongUsesTheChunksOwnPlacementWithinItsFrame(t *testing.T) {
	// The frame-owned chunk keeps its own local placement (Origin) within the
	// frame's space — the same composition ModelMatrix uses for rendering.
	// Frame.ChunkAt carries pos out of the frame only; the chunk's own Origin
	// has to be undone separately.
	interior := placedRoom("interior", mgl32.Vec3{2, 0, 0}, 0, 1)
	interior.FrameOwner = "ship"
	lvl := &Level{
		Entities: []Entity{{ID: "ship", Position: mgl32.Vec3{100, 0, 50}, Scale: mgl32.Vec3{1, 1, 1}}},
		Chunks:   []Chunk{interior},
	}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	f := lvl.Frames.Frame("ship")
	if f == nil {
		t.Fatal("no frame was built for the chunk's owner")
	}

	// Frame-local position of the room's centre: the chunk's own Origin (2,0,0)
	// plus its local (5,4,5) centre.
	frameLocal := mgl32.Vec3{7, 4, 5}
	c := f.ChunkAt(lvl, frameLocal)
	if c == nil {
		t.Fatalf("frame-local query at the interior room's centre found nothing")
	}

	// A point inside the room's *unoffset* local footprint (local X in [0,10])
	// but outside its actual, Origin-offset one (frame-local X in [2,12]) —
	// must not resolve without accounting for the chunk's own Origin.
	unoffset := mgl32.Vec3{1, 4, 5}
	if c := f.ChunkAt(lvl, unoffset); c != nil {
		t.Errorf("frame-local query ignored the chunk's own Origin and matched anyway")
	}
}
