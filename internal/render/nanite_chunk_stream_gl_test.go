//go:build !nogl

package render

// GL test for scenery streaming with a chunk (world.Scenery.Chunk) on the
// GPU cull: a placement whose chunk goes off leaves the members, but the
// pool keeps its mesh — rebuilding the pool would release every resident
// page — and the chunk coming back needs no rebuild either.

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestNanitePoolOutlivesAStreamedOutMesh(t *testing.T) {
	lodProbe(t)
	a := naniteSceneryLevel(t, cookRockSeed(t, 4, 0.4), true, []mgl32.Vec3{{0, 0, 0}}, 0.6)
	b := naniteSceneryLevel(t, cookRockSeed(t, 4, 0.7), true, []mgl32.Vec3{{1.2, 0, -3}}, 0.6)
	lvl := a
	lvl.Chunks = []world.Chunk{{ID: "far"}}
	far := b.Scenery[0]
	far.ID, far.Chunk = "far_rock", "far"
	lvl.Scenery = append(lvl.Scenery, far)
	gfx.Cam.Position = mgl32.Vec3{0.1, 0.05, 3}
	freshNanitePool(t)
	world.SyncPlacementChunks(lvl, false)

	streamFrame(t, lvl)
	r := &naniteScenery
	if len(r.members) != 2 || len(r.poolList) != 2 {
		t.Fatalf("both on: %d members, %d meshes in the pool; want 2, 2", len(r.members), len(r.poolList))
	}
	pool := r.stream

	lvl.Chunks[0].Disabled = true
	world.SyncPlacementChunks(lvl, false)
	streamFrame(t, lvl)
	if len(r.members) != 1 {
		t.Errorf("chunk off: %d members, want 1", len(r.members))
	}
	if r.stream != pool || len(r.poolList) != 2 {
		t.Error("streaming a mesh out rebuilt the pool, which releases every resident page")
	}

	lvl.Chunks[0].Disabled = false
	world.SyncPlacementChunks(lvl, false)
	streamFrame(t, lvl)
	if len(r.members) != 2 {
		t.Errorf("chunk back on: %d members, want 2", len(r.members))
	}
	if r.stream != pool {
		t.Error("streaming the mesh back in rebuilt the pool")
	}
}
