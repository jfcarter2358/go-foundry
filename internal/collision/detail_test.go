package collision

// detail_test.go — the promise a brush's detail mesh makes to this package:
// there isn't one.
//
// Detail geometry is what a brush LOOKS like (world/brush_detail.go). Nothing
// in here may read it, so a wall with a ledge carved along it stops a body in
// exactly the place the flat wall did. These are the tests that would fail if
// some later pass "helpfully" started collideing against what is drawn — which
// is easy to do by accident, since drawing already walks the detail mesh
// everywhere else in the engine.

import (
	"testing"

	"foundry/internal/leveltest"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// withLedgeOnTheFloor gives the level's floor brush a detail mesh with a step
// standing half a unit proud of it, right where the test body is dropped.
//
// A step rather than a bump on a wall because a floor's answer is a single
// number: if collision ever read the detail, the body would rest on top of the
// step instead of on the floor, and that is a clean assertion.
func withLedgeOnTheFloor(t *testing.T, lvl *world.Level) *world.Brush {
	t.Helper()
	ch := &lvl.Chunks[0]
	for bi := range ch.Brushes {
		b := &ch.Brushes[bi]
		lo, hi := b.LocalAABB()
		// The floor: the brush the drop point at (5, y, 5) lands on.
		if lo.X() > 5 || hi.X() < 5 || lo.Z() > 5 || hi.Z() < 5 || hi.Y() > 0.5 {
			continue
		}
		b.SeedDetail()
		base := len(b.Detail.Verts)
		const top = 0.5
		b.Detail.Verts = append(b.Detail.Verts,
			mgl32.Vec3{4, top, 4}, mgl32.Vec3{6, top, 4},
			mgl32.Vec3{6, top, 6}, mgl32.Vec3{4, top, 6})
		b.Detail.Faces = append(b.Detail.Faces, world.NewBrushFace(
			[]int{base, base + 3, base + 2, base + 1}, "concrete"))
		ch.RecomputeBounds()
		_ = world.RebuildIndexes(lvl)
		return b
	}
	t.Fatal("no floor brush under (5, ?, 5) in the fixture")
	return nil
}

// TestDetailGeometryDoesNotStopAPlayer is the feature: a player walks through
// what is only drawn.
func TestDetailGeometryDoesNotStopAPlayer(t *testing.T) {
	lvl := collisionTestLevel()
	floor := withLedgeOnTheFloor(t, lvl)
	if floor == nil {
		return
	}

	p := newTestPlayer(mgl32.Vec3{5, 6, 5})
	if !settle(p, lvl, 400) {
		t.Fatalf("never landed; ended at y=%v", p.Position.Y())
	}
	// The floor's top is y=0, so a resting body's centre is one half-height
	// above it (plus the separation margin). Resting on the DETAIL step would
	// put it half a unit higher.
	if got, want := p.Position.Y(), p.BoundHalfY; got < want || got > want+0.01 {
		t.Errorf("rested at y=%v, want %v: the player is standing on geometry that is only drawn",
			got, want)
	}
}

// TestDetailGeometryIsNotAWall is the same assertion on the horizontal: a
// pillar of detail standing in a doorway is scenery, not a door.
func TestDetailGeometryIsNotAWall(t *testing.T) {
	lvl := collisionTestLevel()
	ch := &lvl.Chunks[0]
	floor := withLedgeOnTheFloor(t, lvl)
	if floor == nil {
		return
	}
	// A wall of detail across the body's path, standing on the floor brush.
	base := len(floor.Detail.Verts)
	floor.Detail.Verts = append(floor.Detail.Verts,
		mgl32.Vec3{6, 0, 4}, mgl32.Vec3{6, 0, 6},
		mgl32.Vec3{6, 3, 6}, mgl32.Vec3{6, 3, 4})
	floor.Detail.Faces = append(floor.Detail.Faces, world.NewBrushFace(
		[]int{base, base + 1, base + 2, base + 3}, "concrete"))
	ch.RecomputeBounds()
	_ = world.RebuildIndexes(lvl)

	p := newTestPlayer(mgl32.Vec3{5, 6, 5})
	settle(p, lvl, 400)
	start := p.Position.X()
	for i := 0; i < 200; i++ {
		MovePlayer(p, lvl, walkMove(mgl32.Vec3{0.05, 0, 0}))
	}
	// Well PAST the drawn wall at x=6, not merely away from where it started:
	// a body stopped by it would come to rest just short of 6, which is still
	// a respectable distance from x=5.
	if p.Position.X() <= 6.5 {
		t.Errorf("walked from x=%v to x=%v: the wall at x=6 is only drawn, and it stopped the player",
			start, p.Position.X())
	}
}

// TestDetailedBrushKeepsItsHullShape is the cheap structural version of the two
// above, and the one that says WHY they pass: the hull is what collision reads,
// and nothing about carving a surface changes it.
func TestDetailedBrushKeepsItsHullShape(t *testing.T) {
	ch := *leveltest.TwoRoomChunk()
	b := &ch.Brushes[0]
	before := append([]mgl32.Vec3(nil), b.Verts...)

	b.SeedDetail()
	b.Detail.Verts[0] = b.Detail.Verts[0].Add(mgl32.Vec3{0, 5, 0})
	b.Detail.Faces = b.Detail.Faces[:1]

	if len(b.Verts) != len(before) {
		t.Fatalf("the hull has %d corners, had %d", len(b.Verts), len(before))
	}
	for i := range before {
		if b.Verts[i] != before[i] {
			t.Errorf("hull corner %d moved to %v, was %v", i, b.Verts[i], before[i])
		}
	}
}
