package world

// level_resync_test.go — the biome-dirty propagation has to compare an edit's
// footprint against painted polygons in a common frame. Both sides are
// chunk-local, but to different chunks, so "common" means world space. Getting
// this wrong is not a visual bug but a performance cliff: every spuriously
// dirtied sector rebuilds a height texture (up to 2048x2048 texels, each
// re-scanning the level) and a ground patch on the next frame, and a vertex
// drag runs this on every mouse-move.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// twoChunkPaintedLevel builds two chunks with the SAME local layout — the
// usual case, since chunks are authored around their own local origin — where
// only chunk B carries a transform placing it elsewhere in the world. Paint
// lives on A.
func twoChunkPaintedLevel(t *testing.T, bOrigin mgl32.Vec3, bRotation, bScale float32) (*Level, *BiomeSector) {
	t.Helper()
	square := []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	// Both chunks carry a floor at the same height, so the drape filter cannot
	// be what separates them here — the world-space XZ test has to. (A chunk
	// overlapping in XZ but at a different HEIGHT is the separate case
	// TestMarkBiomesDirtyIgnoresAFloatingChunkAbove covers.)
	floor := func(id string) []Sector {
		return []Sector{{
			ID: id, PointIndices: []int{0, 1, 2, 3},
			MinBound: mgl32.Vec2{0, 0}, MaxBound: mgl32.Vec2{10, 10},
			Floor: Floor{Enabled: true, Height: 0},
		}}
	}
	lvl := &Level{Chunks: []Chunk{
		{
			ID: "A", Scale: 1, Points: square, Sectors: floor("aFloor"),
			BiomeSectors: []BiomeSector{{
				ID:     "paintOnA",
				Points: []mgl32.Vec2{{1, 1}, {9, 1}, {9, 9}, {1, 9}},
			}},
		},
		{ID: "B", Scale: bScale, Rotation: bRotation, Origin: bOrigin,
			Points: square, Sectors: floor("bFloor")},
	}}
	prev := CurrentLevel
	CurrentLevel = func() *Level { return lvl }
	t.Cleanup(func() { CurrentLevel = prev })

	paint := &lvl.Chunks[0].BiomeSectors[0]
	paint.HeightDirty, paint.GroundDirty = false, false
	return lvl, paint
}

// A chunk placed far away in the world shares chunk A's LOCAL coordinate range
// exactly. Editing it must not touch paint on A: nothing about the two overlaps
// where it counts, in the world.
func TestMarkBiomesDirtyIgnoresPaintOnAFarAwayChunk(t *testing.T) {
	lvl, paint := twoChunkPaintedLevel(t, mgl32.Vec3{1000, 0, 1000}, 0, 1)

	MarkBiomesDirtyOverSector(&lvl.Chunks[1], &lvl.Chunks[1].Sectors[0], mgl32.Vec2{0, 0}, mgl32.Vec2{10, 10})

	if paint.HeightDirty || paint.GroundDirty {
		t.Error("editing a chunk 1000 units away dirtied paint on another chunk — " +
			"the overlap test is comparing two different chunks' local frames as if they were one")
	}
}

// Genuine cross-chunk draping must still be caught: paint that spills off its
// own chunk's floors onto a neighbour's really does sit on that neighbour, so
// editing it moves the paint. The filters must not simply stop looking at
// other chunks.
func TestMarkBiomesDirtyStillCatchesPaintSpilledOntoAnotherChunk(t *testing.T) {
	// The painted chunk has no floor at all, so every vertex of its paint
	// drapes on the neighbour.
	lvl := &Level{Chunks: []Chunk{
		{
			ID: "painted", Scale: 1,
			BiomeSectors: []BiomeSector{{
				ID:     "spill",
				Points: []mgl32.Vec2{{1, 1}, {9, 1}, {9, 9}, {1, 9}},
			}},
		},
		{
			ID: "neighbour", Scale: 1,
			Points: []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}},
			Sectors: []Sector{{
				ID: "nFloor", PointIndices: []int{0, 1, 2, 3},
				MinBound: mgl32.Vec2{0, 0}, MaxBound: mgl32.Vec2{10, 10},
				Floor: Floor{Enabled: true, Height: 0},
			}},
		},
	}}
	prev := CurrentLevel
	CurrentLevel = func() *Level { return lvl }
	t.Cleanup(func() { CurrentLevel = prev })

	paint := &lvl.Chunks[0].BiomeSectors[0]
	paint.HeightDirty, paint.GroundDirty = false, false

	MarkBiomesDirtyOverSector(&lvl.Chunks[1], &lvl.Chunks[1].Sectors[0],
		mgl32.Vec2{0, 0}, mgl32.Vec2{10, 10})

	if !paint.HeightDirty || !paint.GroundDirty {
		t.Error("editing the floor that spilled paint actually rests on did not dirty it")
	}
}

// The mirror of the reported bug, at this layer: a chunk overlapping the paint
// in XZ but floating ABOVE it is not what the paint rests on, so editing it
// must not trigger a rebuild. XZ overlap alone cannot tell these apart, which
// is why the drape filter exists.
func TestMarkBiomesDirtyIgnoresAFloatingChunkAbove(t *testing.T) {
	lvl, paint := twoChunkPaintedLevel(t, mgl32.Vec3{}, 0, 1)
	// Lift chunk B's floor well above the paint's own ground.
	lvl.Chunks[1].Sectors[0].Floor.Height = 25

	MarkBiomesDirtyOverSector(&lvl.Chunks[1], &lvl.Chunks[1].Sectors[0],
		mgl32.Vec2{0, 0}, mgl32.Vec2{10, 10})

	if paint.HeightDirty || paint.GroundDirty {
		t.Error("editing a chunk floating above the paint dirtied it — every mouse-move " +
			"then re-triangulates its ground patch (~134ms), which is the reported lag")
	}
}

// An edit on the paint's OWN chunk is the ordinary case and must be unaffected
// by any of the cross-frame handling.
func TestMarkBiomesDirtyCatchesPaintOnItsOwnChunk(t *testing.T) {
	lvl, paint := twoChunkPaintedLevel(t, mgl32.Vec3{1000, 0, 1000}, 0, 1)

	MarkBiomesDirtyOverSector(&lvl.Chunks[0], &lvl.Chunks[0].Sectors[0], mgl32.Vec2{0, 0}, mgl32.Vec2{10, 10})

	if !paint.HeightDirty || !paint.GroundDirty {
		t.Error("editing the paint's own chunk did not dirty it")
	}
}

// Rotation and scale have to go through the same world-space refit as
// translation — a rotated chunk's local box is not its world box.
func TestMarkBiomesDirtyHonorsRotationAndScale(t *testing.T) {
	// Rotated a quarter turn and scaled up, but still parked far away: the
	// transform must not accidentally sweep the box back over the paint.
	lvl, paint := twoChunkPaintedLevel(t, mgl32.Vec3{500, 0, -500}, 1.5707963, 3)

	MarkBiomesDirtyOverSector(&lvl.Chunks[1], &lvl.Chunks[1].Sectors[0], mgl32.Vec2{0, 0}, mgl32.Vec2{10, 10})

	if paint.HeightDirty || paint.GroundDirty {
		t.Error("a rotated+scaled chunk far from the paint still dirtied it")
	}
}
