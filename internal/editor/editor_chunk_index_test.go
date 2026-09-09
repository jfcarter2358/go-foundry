package editor

// editor_chunk_index_test.go — an edit is not finished until the level can
// find what it changed.
//
// The spatial hash buckets each chunk by its bounds carried into world space,
// and every world-space lookup goes through it: collision, entity location,
// the script location verbs. The editor mutates chunks and brushes directly,
// so it owns keeping that index true — and when it does not, the level is
// drawn exactly as authored and is not solid at all, which is what pressing
// Play on a freshly built room used to do.

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// editorOnLevel points the editor at a fresh level indexed the way a load
// leaves one, and puts the old one back afterwards.
func editorOnLevel(t *testing.T) *world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	lvl := &world.Level{}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("indexing an empty level: %v", err)
	}
	prev, prevIdx := Editor.level, Editor.ActiveChunkIdx
	Editor.level, Editor.ActiveChunkIdx = lvl, -1
	t.Cleanup(func() { Editor.level, Editor.ActiveChunkIdx = prev, prevIdx })
	return lvl
}

// standsIn reports the chunk a body at pos resolves against — the same lookup
// MovePlayer makes, through the same index.
func standsIn(lvl *world.Level, pos mgl32.Vec3) *world.Chunk {
	return lvl.Frames.FrameFor(nil).ChunkAt(lvl, pos)
}

func TestANewChunkIsSolidTheMomentItIsMade(t *testing.T) {
	lvl := editorOnLevel(t)
	gfx.Cam.Position = mgl32.Vec3{20, 2, -40}

	createChunkAtCamera("room")

	// Standing on the new room's floor, in world coordinates.
	pos := mgl32.Vec3{20, 1, -40}
	if ch := standsIn(lvl, pos); ch == nil {
		t.Fatalf("nothing to collide with at %v: the chunk is not in the spatial hash", pos)
	}
}

func TestABrushStampedOutsideTheChunkRebucketsIt(t *testing.T) {
	lvl := editorOnLevel(t)
	gfx.Cam.Position = mgl32.Vec3{}
	createChunkAtCamera("room")
	ch := &lvl.Chunks[0]

	// A second room well clear of the first, so it lands in cells the chunk
	// was not bucketed into. SpatialCellSize is what "well clear" has to beat.
	far := 4 * world.SpatialCellSize
	for _, b := range world.NewHollowRoom("far",
		mgl32.Vec3{far, 0, 0}, mgl32.Vec3{far + 10, 4, 10}, 0.5, "concrete") {
		appendBrush(ch, b)
	}

	pos := mgl32.Vec3{far + 5, 1, 5}
	if got := standsIn(lvl, pos); got == nil {
		t.Fatalf("nothing to collide with at %v: the chunk kept its old buckets", pos)
	}
}

func TestDeletingAChunkDoesNotLeaveTheIndexNamingIt(t *testing.T) {
	lvl := editorOnLevel(t)

	gfx.Cam.Position = mgl32.Vec3{0, 0, 0}
	createChunkAtCamera("first")
	gfx.Cam.Position = mgl32.Vec3{60, 0, 0}
	createChunkAtCamera("second")

	// Drop the first. Every hash entry for the second names index 1, which is
	// one past the end of the shortened slice.
	Editor.ActiveChunkIdx = 0
	deleteActiveChunk()

	if ch := standsIn(lvl, mgl32.Vec3{0, 1, 0}); ch != nil {
		t.Errorf("the deleted chunk still answers at its old position as %q", ch.ID)
	}
	ch := standsIn(lvl, mgl32.Vec3{60, 1, 0})
	if ch == nil {
		t.Fatal("the surviving chunk went missing from the index")
	}
	if ch.ID != "second" {
		t.Errorf("the index answered with %q, want the surviving chunk", ch.ID)
	}
}
