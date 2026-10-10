package collision

// The particle probe's box cache (probeBoxesFor) must never answer for a brush
// that is no longer where, or what, it was when the box was taken: a spark
// would fall through a platform that moved under it, or bounce off one that
// has gone.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// landsAt drops a particle straight down at x and reports the height it
// stopped at, or false if it fell through.
func landsAt(t *testing.T, lvl *world.Level, x float32) (float32, bool) {
	t.Helper()
	pos, _, hit := ParticleCollide(lvl, mgl32.Vec3{x, 5, 0}, mgl32.Vec3{x, -0.5, 0}, 0)
	return pos.Y(), hit
}

func TestTheParticleProbeFollowsABrushThatMovesOrChanges(t *testing.T) {
	// A floor at 0, a dynamic 1 m block standing on it at x=3, and a fixed
	// plinth at x=-3 — one of each of the probe's two kinds of brush.
	block := world.NewBoxBrush("block", mgl32.Vec3{3, 0.5, 0}, mgl32.Vec3{0.5, 0.5, 0.5}, "concrete")
	block.Dynamic = true
	plinth := world.NewBoxBrush("plinth", mgl32.Vec3{-3, 0.5, 0}, mgl32.Vec3{0.5, 0.5, 0.5}, "concrete")
	lvl := seamLevel(t, slabChunk("ground", -10, 10, 0, block, plinth))
	n := len(lvl.Chunks[0].Brushes)
	b, p := &lvl.Chunks[0].Brushes[n-2], &lvl.Chunks[0].Brushes[n-1]

	check := func(what string, x, want float32) {
		t.Helper()
		// Twice: the first call may build the cache, the second must use it.
		for i := 0; i < 2; i++ {
			y, hit := landsAt(t, lvl, x)
			if !hit || y < want-0.01 || y > want+0.01 {
				t.Fatalf("%s: a particle dropped at x=%v stopped at y=%v (hit %v), want %v", what, x, y, hit, want)
			}
		}
	}

	check("block at rest", 3, 1)
	check("plinth", -3, 1)
	check("open floor", 6, 0)

	// A script slides the block along: its old footprint is floor, its new
	// one is block. Moving does not move the mesh generation on.
	b.Offset = mgl32.Vec3{3, 0, 0}
	check("moved block's old place", 3, 0)
	check("moved block", 6, 1)
	b.Offset = mgl32.Vec3{}
	check("block back at rest", 3, 1)
	check("floor it left", 6, 0)

	// An edit makes the plinth taller in place. The slice is the same one, so
	// only the generation says the cached boxes are stale.
	*p = world.NewBoxBrush("plinth", mgl32.Vec3{-3, 1, 0}, mgl32.Vec3{0.5, 1, 0.5}, "concrete")
	world.MarkBrushMeshesChanged()
	check("plinth after an edit", -3, 2)

	// And widened past the old box's edge, which is what a stale box gets
	// wrong: the probe would skip the brush where it now reaches.
	*p = world.NewBoxBrush("plinth", mgl32.Vec3{-4, 1, 0}, mgl32.Vec3{1.5, 1, 0.5}, "concrete")
	world.MarkBrushMeshesChanged()
	check("plinth widened", -5.2, 2)
}
