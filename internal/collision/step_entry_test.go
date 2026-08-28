package collision

// step_entry_test.go — the riser between two sectors at different heights.
//
// A portal fills that riser with wall geometry (world.RebuildGaps) but is not
// itself a wall, so nothing in the wall resolver ever looks at it. Walking into
// a shoulder-high step therefore admitted the body into the sector behind the
// riser, where the floor clamp lifted it onto that floor in one frame: the
// reported bug, "walking into a drawn gap taller than the player walks through
// it".
//
// What has to hold is a pair, not a single rule. A riser taller than the body
// blocks; a riser shorter than it is still a step, because stepping up is how
// every stair in every shipped level is climbed, and because a body that could
// not step would be stopped by the seam between two sectors whose floors differ
// by a rounding error.

import (
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// stepLevel is two 20x20 sectors side by side in one chunk, sharing the edge at
// x=20 as a gap-filled portal, with the east sector's floor `riser` units above
// the west one's.
func stepLevel(t *testing.T, riser float32) *world.Level {
	t.Helper()
	ch := world.Chunk{
		ID:         "step",
		Scale:      1,
		Points:     []mgl32.Vec2{{0, 0}, {20, 0}, {20, 20}, {0, 20}, {40, 0}, {40, 20}},
		PointNames: make([]string, 6),
		Layers:     []world.ChunkLayer{{Name: "ground", BaseY: 0, CeilY: 24}},
	}
	west := world.Sector{
		ID:           "west",
		PointIndices: []int{0, 1, 2, 3},
		EdgeKinds:    []string{"wall", "portal", "wall", "wall"},
		Floor:        world.Floor{Enabled: true, Height: 0, VertexOffsets: []float32{0, 0, 0, 0}},
		Ceiling:      world.Ceiling{Enabled: true, Height: 24, VertexOffsets: []float32{0, 0, 0, 0}},
		Portals: []world.Portal{{
			ID: "gap", StartPoint: mgl32.Vec2{20, 0}, EndPoint: mgl32.Vec2{20, 20},
			ToSector: "east", DrawGap: true, Visible: true,
		}},
		MinBound: mgl32.Vec2{0, 0},
		MaxBound: mgl32.Vec2{20, 20},
	}
	east := world.Sector{
		ID:           "east",
		PointIndices: []int{1, 4, 5, 2},
		EdgeKinds:    []string{"wall", "wall", "wall", "portal"},
		Floor:        world.Floor{Enabled: true, Height: riser, VertexOffsets: []float32{0, 0, 0, 0}},
		Ceiling:      world.Ceiling{Enabled: true, Height: 24, VertexOffsets: []float32{0, 0, 0, 0}},
		Portals: []world.Portal{{
			ID: "gap", StartPoint: mgl32.Vec2{20, 20}, EndPoint: mgl32.Vec2{20, 0},
			ToSector: "west", DrawGap: true, Visible: true,
		}},
		MinBound: mgl32.Vec2{20, 0},
		MaxBound: mgl32.Vec2{40, 20},
	}
	ch.Sectors = []world.Sector{west, east}
	ch.RecomputeBounds()

	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	return lvl
}

// walkEast pushes the body east for as many frames as it takes to cross the
// step, or until it stops making progress.
func walkEast(p *world.Entity, lvl *world.Level) {
	for i := 0; i < 300 && p.Position.X() < 25; i++ {
		MovePlayer(p, lvl, walkMove(mgl32.Vec3{0.2, 0, 0}))
	}
}

func TestARiserTallerThanTheBodyBlocksIt(t *testing.T) {
	lvl := stepLevel(t, 4) // more than twice the player's 1.8-unit height
	p := newTestPlayer(mgl32.Vec3{5, 2, 10})
	if !settle(p, lvl, 400) {
		t.Fatalf("never landed on the west floor; y=%v", p.Position.Y())
	}
	walkEast(p, lvl)

	if p.Position.X() >= 20 {
		t.Errorf("walked to x=%v, through a 4-unit riser at x=20", p.Position.X())
	}
	if got, want := p.Position.Y(), float32(0)+p.BoundHalfY; !nearly(got, want) {
		t.Errorf("ended at y=%v, want the west floor at %v — it was lifted up the riser",
			got, want)
	}
	if p.CurrentSector != "west" {
		t.Errorf("ended in sector %q, want west", p.CurrentSector)
	}
}

// The other half: a step short enough to stand on top of is still walked up,
// which is how every stair in the shipped levels is climbed.
func TestAStepShorterThanTheBodyIsStillClimbed(t *testing.T) {
	lvl := stepLevel(t, 1)
	p := newTestPlayer(mgl32.Vec3{5, 2, 10})
	if !settle(p, lvl, 400) {
		t.Fatalf("never landed on the west floor; y=%v", p.Position.Y())
	}
	walkEast(p, lvl)

	if p.Position.X() <= 20 {
		t.Errorf("stopped at x=%v; a 1-unit step should be walked up", p.Position.X())
	}
	if got, want := p.Position.Y(), float32(1)+p.BoundHalfY; !nearly(got, want) {
		t.Errorf("ended at y=%v, want the east floor at %v", got, want)
	}
}

// A ledge out of walking reach is still reachable by jumping onto it: the
// refusal is measured from the body's feet as well as from the floor it left,
// or every ledge taller than a step would be sealed off.
func TestJumpingOntoALedgeIsStillAdmitted(t *testing.T) {
	lvl := stepLevel(t, 3)
	p := newTestPlayer(mgl32.Vec3{19, 2, 10})
	if !settle(p, lvl, 400) {
		t.Fatalf("never landed on the west floor; y=%v", p.Position.Y())
	}
	// Walking alone must not get up a 3-unit riser.
	walkEast(p, lvl)
	if p.Position.X() >= 20 {
		t.Fatalf("walked up a 3-unit riser to x=%v without jumping", p.Position.X())
	}

	// Now jump east off it. The apex clears 3 units of riser at this tuning.
	for i := 0; i < 200; i++ {
		mv := walkMove(mgl32.Vec3{0.12, 0, 0})
		if p.Position.Y() <= p.BoundHalfY+1e-3 {
			mv.Jump = true
		}
		MovePlayer(p, lvl, mv)
		if p.Position.X() > 21 {
			break
		}
	}
	if p.Position.X() <= 20 {
		t.Errorf("jumping never got onto the ledge; stopped at x=%v y=%v",
			p.Position.X(), p.Position.Y())
	}
}
