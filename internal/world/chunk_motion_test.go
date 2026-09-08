package world

// chunk_motion_test.go — AdvanceChunks, the tick that moves a chunk that
// carries a velocity.
//
// Four things are asserted, and they are what the property promises: the origin
// integrates, nothing chunk-local is rewritten under it, the world-space index
// follows it, and a chunk that is switched off does not drift while it is out
// of the world.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// movingChunkLevel is one 10x10 room at the origin with a velocity, bucketed
// into the spatial hash the way a loaded level would be.
func movingChunkLevel(t *testing.T, vel mgl32.Vec3) *Level {
	t.Helper()
	ch := Chunk{
		ID:         "lift",
		Points:     []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}},
		PointNames: []string{"P0", "P1", "P2", "P3"},
		MinBound:   mgl32.Vec3{0, 0, 0},
		MaxBound:   mgl32.Vec3{10, 4, 10},
		Scale:      1,
		Velocity:   vel,
		Sectors: []Sector{{
			ID:           "A",
			PointIndices: []int{0, 1, 2, 3},
			EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
			MinBound:     mgl32.Vec2{0, 0},
			MaxBound:     mgl32.Vec2{10, 10},
			Floor:        Floor{Enabled: true},
			Ceiling:      Ceiling{Enabled: true, Height: 4},
		}},
	}
	lvl := &Level{Chunks: []Chunk{ch}}
	RebuildSpatialHash(lvl)
	return lvl
}

func TestAdvanceChunksIntegratesVelocityOntoOrigin(t *testing.T) {
	lvl := movingChunkLevel(t, mgl32.Vec3{2, 0, -4})

	if !AdvanceChunks(lvl, 0.5) {
		t.Fatal("AdvanceChunks reported nothing moved; a chunk with a velocity did")
	}
	approxVec3(t, lvl.Chunks[0].Origin, mgl32.Vec3{1, 0, -2}, 1e-5, "origin after half a second")

	AdvanceChunks(lvl, 0.5)
	approxVec3(t, lvl.Chunks[0].Origin, mgl32.Vec3{2, 0, -4}, 1e-5, "origin after a full second")

	// The velocity itself is untouched: nothing damps it, which is the whole
	// contract — what should slow a platform down is the game's rule.
	if lvl.Chunks[0].Velocity != (mgl32.Vec3{2, 0, -4}) {
		t.Errorf("velocity became %v; integration must not consume it", lvl.Chunks[0].Velocity)
	}
}

func TestAdvanceChunksReportsAStillChunkUnmoved(t *testing.T) {
	// The return is what marks the play session dirty, so a level whose chunks
	// all sit still must not report a write every frame of play.
	lvl := movingChunkLevel(t, mgl32.Vec3{})
	if AdvanceChunks(lvl, 1.0/60) {
		t.Error("a chunk with no velocity reported a move")
	}
	if lvl.Chunks[0].Origin != (mgl32.Vec3{}) {
		t.Errorf("origin drifted to %v", lvl.Chunks[0].Origin)
	}
}

func TestAdvanceChunksRewritesNoLocalGeometry(t *testing.T) {
	// Same reason writing `origin` from a script rebuilds nothing: every
	// per-chunk coordinate is chunk-local and rides out through ModelMatrix.
	lvl := movingChunkLevel(t, mgl32.Vec3{0, 3, 0})
	before := append([]mgl32.Vec2(nil), lvl.Chunks[0].Points...)
	minB, maxB := lvl.Chunks[0].MinBound, lvl.Chunks[0].MaxBound

	AdvanceChunks(lvl, 2)

	ch := &lvl.Chunks[0]
	for i := range before {
		if ch.Points[i] != before[i] {
			t.Fatalf("point %d moved to %v; chunk-local coordinates must not be rewritten", i, ch.Points[i])
		}
	}
	if ch.MinBound != minB || ch.MaxBound != maxB {
		t.Errorf("chunk-local bounds became %v..%v; they are local and do not move", ch.MinBound, ch.MaxBound)
	}
	approxVec3(t, ch.LocalToWorld(mgl32.Vec3{5, 0, 5}), mgl32.Vec3{5, 6, 5}, 1e-5, "local (5,0,5) carried out")
}

func TestAdvanceChunksRebucketsTheSpatialHash(t *testing.T) {
	// Without this, collision, entity location and the script location verbs
	// would all keep answering from the footprint the chunk moved off.
	lvl := movingChunkLevel(t, mgl32.Vec3{100, 0, 0})

	inside := mgl32.Vec3{5, 1, 5}
	if ch, _ := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, inside); ch == nil {
		t.Fatal("fixture: the chunk is not findable at its own position to begin with")
	}

	AdvanceChunks(lvl, 1)

	if ch, _ := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, inside.Add(mgl32.Vec3{100, 0, 0})); ch == nil {
		t.Error("the moved chunk is not findable at its new position; the hash was not re-bucketed")
	}
	if ch, _ := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, inside); ch != nil {
		t.Error("the chunk is still findable at the footprint it moved off")
	}
}

func TestAdvanceChunksLeavesADisabledChunkWhereItIs(t *testing.T) {
	// A disabled chunk is out of the world entirely, so there is nothing there
	// to move — and a streaming level must not find its switched-off areas had
	// wandered off before it switched them back on.
	lvl := movingChunkLevel(t, mgl32.Vec3{5, 0, 0})
	lvl.Chunks[0].Disabled = true

	if AdvanceChunks(lvl, 1) {
		t.Error("a disabled chunk reported a move")
	}
	if lvl.Chunks[0].Origin != (mgl32.Vec3{}) {
		t.Errorf("a disabled chunk drifted to %v", lvl.Chunks[0].Origin)
	}
}

func TestAdvanceChunksSurvivesANilLevel(t *testing.T) {
	if AdvanceChunks(nil, 1) {
		t.Error("a nil level reported a move")
	}
}

func TestAdvanceChunksCarriesAttachedEntities(t *testing.T) {
	lvl := movingChunkLevel(t, mgl32.Vec3{2, 0, -4})
	lvl.Entities = []Entity{{ID: "rider", AttachedChunk: "lift", Position: mgl32.Vec3{1, 2, 3}}}

	AdvanceChunks(lvl, 0.5)

	approxVec3(t, lvl.Entities[0].Position, mgl32.Vec3{2, 2, 1}, 1e-5, "rider position after half a second")
}

func TestAdvanceChunksLeavesAMismatchedFrameRiderBehind(t *testing.T) {
	// A rider expressed in a different space than the chunk it names is left
	// alone rather than nudged into coordinates that mean something else —
	// the same "unresolvable name reads as not attached" convention Attach
	// follows.
	lvl := movingChunkLevel(t, mgl32.Vec3{2, 0, -4})
	lvl.Entities = []Entity{{ID: "rider", AttachedChunk: "lift", Frame: "ship", Position: mgl32.Vec3{1, 2, 3}}}

	AdvanceChunks(lvl, 0.5)

	approxVec3(t, lvl.Entities[0].Position, mgl32.Vec3{1, 2, 3}, 1e-5, "rider position must be untouched")
}

func TestAdvanceChunksLeavesAnUnattachedEntityBehind(t *testing.T) {
	lvl := movingChunkLevel(t, mgl32.Vec3{2, 0, -4})
	lvl.Entities = []Entity{{ID: "bystander", Position: mgl32.Vec3{1, 2, 3}}}

	AdvanceChunks(lvl, 0.5)

	approxVec3(t, lvl.Entities[0].Position, mgl32.Vec3{1, 2, 3}, 1e-5, "an entity not attached to the chunk must not move")
}

func TestCarryAttachedEntitiesSurvivesNilArgumentsAndZeroDelta(t *testing.T) {
	lvl := &Level{Entities: []Entity{{ID: "rider", AttachedChunk: "lift", Position: mgl32.Vec3{1, 2, 3}}}}
	ch := &Chunk{ID: "lift"}

	CarryAttachedEntities(nil, ch, mgl32.Vec3{1, 0, 0})
	CarryAttachedEntities(lvl, nil, mgl32.Vec3{1, 0, 0})
	CarryAttachedEntities(lvl, ch, mgl32.Vec3{})

	approxVec3(t, lvl.Entities[0].Position, mgl32.Vec3{1, 2, 3}, 1e-5, "position must be untouched")
}
