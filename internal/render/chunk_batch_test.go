package render

// chunk_batch_test.go — grouping, invalidation and culling of chunk batches,
// headless. With uploads off a batch has no GL objects, but everything that
// decides what goes in it, when it is rebuilt, and which ranges a pass draws
// or queues is still exercised.

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// batchChunk is a chunk of brushes laid out along X, the i'th one textured
// textures[i], built the way the loader builds one.
func batchChunk(t *testing.T, id string, textures ...string) world.Chunk {
	t.Helper()
	ch := world.Chunk{ID: id, Scale: 1}
	for i, tex := range textures {
		b := world.NewBoxBrush(id+"_b"+string(rune('0'+i)), mgl32.Vec3{float32(i) * 3, 0, 0}, mgl32.Vec3{1, 1, 1}, tex)
		if err := b.Load(); err != nil {
			t.Fatal(err)
		}
		ch.Brushes = append(ch.Brushes, b)
	}
	ch.RecomputeBounds()
	return ch
}

func batchLevel(t *testing.T, chunks ...world.Chunk) *world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	lvl := &world.Level{Chunks: chunks}
	t.Cleanup(func() { ReleaseChunkBatches(lvl) })
	return lvl
}

// everything is a frustum nothing in these fixtures falls outside.
func everything() Frustum {
	return ExtractFrustum(mgl32.Ortho(-500, 500, -500, 500, -500, 500))
}

func groupFor(t *testing.T, lvl *world.Level, chunkID, texture string) *batchGroup {
	t.Helper()
	cb := levelBatchesFor(lvl).chunks[chunkID]
	if cb == nil {
		t.Fatalf("chunk %q has no batch", chunkID)
	}
	for _, g := range cb.groups {
		if g.key.mat.Texture == texture {
			return g
		}
	}
	t.Fatalf("chunk %q has no %q batch", chunkID, texture)
	return nil
}

func TestFacesOfOneMaterialShareABatch(t *testing.T) {
	lvl := batchLevel(t, batchChunk(t, "room", "concrete", "concrete", "brick"))
	SyncChunkBatches(lvl)

	cb := levelBatchesFor(lvl).chunks["room"]
	if len(cb.groups) != 2 {
		t.Fatalf("got %d batches for two materials", len(cb.groups))
	}
	concrete := groupFor(t, lvl, "room", "concrete")
	if len(concrete.firsts) != 2 {
		t.Errorf("two concrete boxes left %d ranges, want one per brush", len(concrete.firsts))
	}
	// Six faces of two triangles each, per box.
	if want := int32(2 * 6 * 6); concrete.batch.Verts != want {
		t.Errorf("concrete batch holds %d vertices, want %d", concrete.batch.Verts, want)
	}

	st := DrawChunkBrushes(lvl, &lvl.Chunks[0], everything(), mgl32.Ident4(), mgl32.Vec3{},
		func(BatchDraw, float32) { t.Error("an opaque material was queued as transparent") })
	if st.Brushes != 3 || st.DrawCalls != 2 {
		t.Errorf("drew %d brushes in %d calls, want 3 in 2 (one per material)", st.Brushes, st.DrawCalls)
	}
}

func TestABrushEditRebuildsOnlyTheMaterialsItUses(t *testing.T) {
	lvl := batchLevel(t, batchChunk(t, "room", "concrete", "brick"), batchChunk(t, "hall", "concrete"))
	SyncChunkBatches(lvl)
	brick := groupFor(t, lvl, "room", "brick")
	hall := groupFor(t, lvl, "hall", "concrete")
	brickFP, hallFP := brick.fp, hall.fp
	roomConcreteFP := groupFor(t, lvl, "room", "concrete").fp

	b := &lvl.Chunks[0].Brushes[0] // the concrete one
	for i := range b.Verts {
		b.Verts[i] = b.Verts[i].Add(mgl32.Vec3{0, 0.5, 0})
	}
	if err := b.Load(); err != nil {
		t.Fatal(err)
	}
	SyncChunkBatches(lvl)

	if groupFor(t, lvl, "room", "concrete").fp == roomConcreteFP {
		t.Error("the edited brush's material was not rebuilt")
	}
	if g := groupFor(t, lvl, "room", "brick"); g != brick || g.fp != brickFP {
		t.Error("a material the edit did not touch was rebuilt")
	}
	if g := groupFor(t, lvl, "hall", "concrete"); g != hall || g.fp != hallFP {
		t.Error("another chunk's batch was rebuilt by an edit in this one")
	}
}

func TestATextureGainingAlphaIsQueuedTransparentWithoutARebuild(t *testing.T) {
	lvl := batchLevel(t, batchChunk(t, "room", "batchglass", "concrete"))
	SyncChunkBatches(lvl)
	t.Cleanup(func() { gfx.SetTextureHasAlpha("batchglass", false) })
	fp := groupFor(t, lvl, "room", "batchglass").fp

	queued := 0
	draw := func() ChunkBrushStats {
		queued = 0
		return DrawChunkBrushes(lvl, &lvl.Chunks[0], everything(), mgl32.Ident4(), mgl32.Vec3{0, 0, 10},
			func(d BatchDraw, dist float32) {
				queued++
				if dist <= 0 {
					t.Errorf("queued at distance %v", dist)
				}
			})
	}
	if st := draw(); queued != 0 || st.DrawCalls != 2 {
		t.Fatalf("before: %d queued, %d calls", queued, st.DrawCalls)
	}

	// The texture editor paints a translucent texel into it.
	gfx.SetTextureHasAlpha("batchglass", true)
	if st := draw(); queued != 1 || st.DrawCalls != 1 {
		t.Errorf("after the texture gained alpha: %d queued (want 1), %d opaque calls (want 1)", queued, st.DrawCalls)
	}
	if groupFor(t, lvl, "room", "batchglass").fp != fp {
		t.Error("a texture's alpha flipping rebuilt the batch; it is a draw-time decision")
	}
}

func TestBatchesSurviveTheChunkSliceBeingReallocated(t *testing.T) {
	lvl := batchLevel(t, batchChunk(t, "a", "concrete"), batchChunk(t, "b", "brick", "brick"))
	SyncChunkBatches(lvl)
	bGroup := groupFor(t, lvl, "b", "brick")

	// The editor and the prefab resolver compact and re-append the slice: the
	// same chunks, at different addresses and indexes, with no mesh changed.
	lvl.Chunks = []world.Chunk{lvl.Chunks[1], lvl.Chunks[0]}
	st := DrawChunkBrushes(lvl, &lvl.Chunks[0], everything(), mgl32.Ident4(), mgl32.Vec3{}, func(BatchDraw, float32) {})
	if st.Brushes != 2 {
		t.Errorf("chunk b, now first in the slice, drew %d brushes, want its own 2", st.Brushes)
	}
	if groupFor(t, lvl, "b", "brick") != bGroup {
		t.Error("reallocating the slice rebuilt a batch")
	}

	// Removing a chunk the way the editor does releases its meshes; its batch
	// goes with it at the next sync.
	lvl.Chunks[1].ReleaseGeometry()
	lvl.Chunks = lvl.Chunks[:1]
	SyncChunkBatches(lvl)
	if _, ok := levelBatchesFor(lvl).chunks["a"]; ok {
		t.Error("a removed chunk kept its batch")
	}
	if groupFor(t, lvl, "b", "brick") != bGroup {
		t.Error("removing one chunk rebuilt another's batch")
	}
}

func TestAMovedBrushIsDrawnUnderItsOwnPlacement(t *testing.T) {
	lvl := batchLevel(t, batchChunk(t, "room", "concrete", "concrete", "concrete"))
	lvl.Chunks[0].Brushes[1].Dynamic = true
	lvl.Chunks[0].Brushes[1].Offset = mgl32.Vec3{0, 2, 0}

	st := DrawChunkBrushes(lvl, &lvl.Chunks[0], everything(), mgl32.Ident4(), mgl32.Vec3{}, func(BatchDraw, float32) {})
	if st.Brushes != 3 || st.DrawCalls != 2 {
		// The two at rest either side are not contiguous in the buffer once
		// the middle one is taken out, so they are two ranges of one call.
		t.Errorf("drew %d brushes in %d calls, want 3 in 2: the moved one alone, the rest together", st.Brushes, st.DrawCalls)
	}
	g := groupFor(t, lvl, "room", "concrete")
	if len(g.visFirst) != 0 {
		t.Error("a pass left ranges behind in the scratch lists")
	}
}

func TestADisabledChunkIsBatchedAndAnUnloadedOneIsNot(t *testing.T) {
	off := batchChunk(t, "off", "concrete")
	off.Disabled = true
	gone := batchChunk(t, "gone", "concrete")
	gone.Disabled, gone.UnloadWhenDisabled = true, true
	lvl := batchLevel(t, off, gone)
	world.SyncChunkGeometry(lvl)
	SyncChunkBatches(lvl)

	lb := levelBatchesFor(lvl)
	if lb.chunks["off"] == nil {
		t.Error("a disabled chunk has no batch; switching it on would build one mid-play")
	}
	if lb.chunks["gone"] != nil {
		t.Error("an unloaded chunk kept a batch")
	}
}

func TestFrustumCullingIsPerBrush(t *testing.T) {
	lvl := batchLevel(t, batchChunk(t, "row", "concrete", "concrete", "concrete"))
	// A box around only the first brush, at x = 0.
	f := ExtractFrustum(mgl32.Ortho(-1.5, 1.5, -5, 5, -5, 5))
	st := DrawChunkBrushes(lvl, &lvl.Chunks[0], f, mgl32.Ident4(), mgl32.Vec3{}, func(BatchDraw, float32) {})
	if st.Brushes != 1 {
		t.Errorf("drew %d brushes, want only the one inside the frustum", st.Brushes)
	}
}
