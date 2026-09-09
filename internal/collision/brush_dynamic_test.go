package collision

// brush_dynamic_test.go — the whole point of the dynamic-brush feature: that
// what a body hits is where the brush is DRAWN, not where it was authored.
//
// The brush's Verts never change when a script moves it (world/brush_dynamic.go)
// — the placement rides on top — so the risk this guards is exactly that the
// resolver keeps reading the authored corners and the player walks through a
// closed door.

import (
	"math"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// movedBrushLevel is one chunk holding one box brush, moved by offset.
func movedBrushLevel(offset mgl32.Vec3) (*world.Chunk, *world.Brush) {
	ch := &world.Chunk{ID: "c", Scale: 1}
	b := world.NewBoxBrush("door", mgl32.Vec3{0, 1, 0}, mgl32.Vec3{1, 1, 1}, "concrete")
	b.Dynamic = true
	b.Offset = offset
	ch.Brushes = []world.Brush{b}
	ch.RecomputeBounds()
	return ch, &ch.Brushes[0]
}

// A body standing where the brush USED to be is left alone; a body standing
// where it is NOW is pushed out.
func TestCollisionFollowsAMovedBrush(t *testing.T) {
	const r, h = 0.4, 0.9

	// The brush slid five units along +X. Its authored box is at the origin.
	ch, _ := movedBrushLevel(mgl32.Vec3{5, 0, 0})

	atAuthored := mgl32.Vec3{0, 1, 0}
	out, _, _ := pushOutOfChunkBrushes(ch, atAuthored, r, h)
	if !vecNear(out, atAuthored, 1e-4) {
		t.Errorf("a body at the brush's AUTHORED position was pushed to %v; "+
			"the resolver is still reading the corners rather than the placement", out)
	}

	atMoved := mgl32.Vec3{5, 1, 0}
	out, _, _ = pushOutOfChunkBrushes(ch, atMoved, r, h)
	if vecNear(out, atMoved, 1e-4) {
		t.Fatal("a body inside the brush where it now is was not pushed out")
	}
	// Pushed clear: no longer overlapping, which a second pass confirms.
	again, _, _ := pushOutOfChunkBrushes(ch, out, r, h)
	if !vecNear(again, out, 1e-3) {
		t.Errorf("still penetrating after one push: %v then %v", out, again)
	}
}

// Turning a brush turns what a body hits. A thin slab across X, quarter-turned,
// blocks along Z instead — so a point clear of the authored slab is inside the
// turned one.
func TestCollisionFollowsARotatedBrush(t *testing.T) {
	ch := &world.Chunk{ID: "c", Scale: 1}
	b := world.NewBoxBrush("door", mgl32.Vec3{0, 1, 0}, mgl32.Vec3{2, 1, 0.25}, "concrete")
	b.Dynamic = true
	ch.Brushes = []world.Brush{b}

	// Beside the slab's narrow end, well clear of it while it is unturned.
	probe := mgl32.Vec3{0, 1, 1.4}
	const r, h = 0.3, 0.9

	if out, _, _ := pushOutOfChunkBrushes(ch, probe, r, h); !vecNear(out, probe, 1e-4) {
		t.Fatalf("the probe was not clear of the unturned slab: pushed to %v", out)
	}

	ch.Brushes[0].Rotation = mgl32.Vec3{0, math.Pi / 2, 0}
	ch.RecomputeBounds()
	out, _, _ := pushOutOfChunkBrushes(ch, probe, r, h)
	if vecNear(out, probe, 1e-4) {
		t.Fatal("the turned slab did not block a point now inside it")
	}
	if out.Z() <= probe.Z() {
		t.Errorf("pushed to %v; a body beside the turned slab should be pushed further out along +Z", out)
	}
}

// A dynamic brush that is not solid is still not solid — the placement changes
// where it is, not what it is.
func TestANonSolidDynamicBrushStillDoesNotBlock(t *testing.T) {
	ch, b := movedBrushLevel(mgl32.Vec3{5, 0, 0})
	b.Solid = false

	inside := mgl32.Vec3{5, 1, 0}
	if out, _, _ := pushOutOfChunkBrushes(ch, inside, 0.4, 0.9); !vecNear(out, inside, 1e-4) {
		t.Errorf("a non-solid brush pushed a body to %v", out)
	}
}
