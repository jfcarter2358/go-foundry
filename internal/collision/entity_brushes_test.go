package collision

// entity_brushes_test.go — walking up to, into and around an entity built out
// of brushes.
//
// The four claims, in the order a body meets them:
//
//  1. From outside, the hull is solid. A frame-owned chunk is not in the world
//     spatial hash, so without entity_brushes.go the player walks through a
//     ship as if it were not there.
//  2. Through the doorway, the body is handed into the entity's frame — with no
//     authored doorway volume anywhere. The admitting region is the entity's
//     solid underfoot, inside its bounds, which is what keeps it exactly
//     antisymmetric with stepping off.
//  3. Aboard, it is carried: the entity moves and the occupant's LOCAL position
//     does not change, because nothing moved it — the coordinates did.
//  4. Walking back out puts it back in the world, in the world's coordinates.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// hutEntity is an entity whose brushes are a hollow room with a gap in one
// wall — somewhere to walk into, and a way in.
//
// The room spans local (0,0,0) to (6,3,6) with half-metre walls, and the +X
// wall is left out so there is a doorway rather than a sealed box.
func hutEntity(id string, pos mgl32.Vec3) world.Entity {
	e := world.NewBlankEntity(id)
	e.Position = pos
	e.Scale = mgl32.Vec3{1, 1, 1}
	room := world.NewHollowRoom("wall", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{6, 3, 6}, 0.5, "concrete")
	// NewHollowRoom emits floor, ceiling and the four walls; dropping the one
	// whose centre is at maximum X leaves the doorway.
	var keep []world.Brush
	var maxX int
	for i := range room {
		lo, hi := room[i].LocalAABB()
		c := lo.Add(hi).Mul(0.5)
		blo, bhi := room[maxX].LocalAABB()
		if c.X() > blo.Add(bhi).Mul(0.5).X() {
			maxX = i
		}
	}
	for i := range room {
		if i != maxX {
			keep = append(keep, room[i])
		}
	}
	e.Brushes = keep
	return e
}

// hutLevel is one hut standing in an otherwise empty world, indexed the way a
// loaded level is.
func hutLevel(t *testing.T, at mgl32.Vec3) *world.Level {
	t.Helper()
	e := hutEntity("hut", at)
	lvl := &world.Level{Entities: []world.Entity{e}}

	ch := world.Chunk{ID: world.EntityGeometryChunkID(e.ID), Scale: 1, FrameOwner: e.ID}
	ch.FromEntity = e.ID
	ch.Brushes = world.CloneEntityBrushes(e.Brushes)
	ch.RecomputeBounds()
	lvl.Chunks = append(lvl.Chunks, ch)

	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	return lvl
}

// walkerY is the height a walker's CENTRE stands at with its feet on the hut's
// floor. MovePlayer treats Position as the cylinder's centre rather than its
// base, so a body placed at the floor's own height would be half buried in it
// and would be pushed sideways out of the slab before it ever reached the
// doorway.
const walkerY = float32(0.95)

// walker is a player-shaped body in world space, standing on the floor.
func walker(x, z float32) world.Entity {
	p := world.NewBlankEntity("player")
	p.Position = mgl32.Vec3{x, walkerY, z}
	p.Scale = mgl32.Vec3{1, 1, 1}
	p.BoundHalfX, p.BoundHalfY, p.BoundHalfZ = 0.3, 0.9, 0.3
	return p
}

// walkStep is one tick of the real game loop and nothing else: MovePlayer, then
// the portal check. Deliberately not followed by UpdateLocation — app.go does
// not call it either, and a helper that tidied up the derived location behind
// MovePlayer would be testing a loop nobody runs.
func walkStep(lvl *world.Level, p *world.Entity, step mgl32.Vec3) {
	MovePlayer(p, lvl, PlayerMove{Intent: step, DT: 1.0 / 60})
	if c := FindFramePortalCrossing(lvl, p); c != nil {
		CrossFramePortal(lvl, p, c, 0)
	}
}

// TestAnEntitysHullIsSolidFromOutside is claim 1. Its brushes live in the
// entity's frame and so are in none of the world's chunks; without the
// world-space pass a body walks straight through them.
func TestAnEntitysHullIsSolidFromOutside(t *testing.T) {
	lvl := hutLevel(t, mgl32.Vec3{0, 0, 0})
	// Standing outside the -X wall, walking east into it. The wall's outer face
	// is at x=0, so a body of radius 0.3 must stop at about x=-0.3.
	p := walker(-2, 3)
	for i := 0; i < 60; i++ {
		walkStep(lvl, &p, mgl32.Vec3{0.1, 0, 0})
	}
	if p.Frame != "" {
		t.Fatalf("the walker ended up aboard the hut (frame %q) without going through the doorway", p.Frame)
	}
	if p.Position.X() > -0.2 {
		t.Errorf("walked to x=%v — straight through the hut's wall", p.Position.X())
	}
}

// TestWalkingInHandsTheBodyToTheEntitysFrame is claim 2: no doorway volume is
// authored anywhere, and the test is the geometry underfoot.
func TestWalkingInHandsTheBodyToTheEntitysFrame(t *testing.T) {
	lvl := hutLevel(t, mgl32.Vec3{0, 0, 0})
	// Outside the doorway (the +X wall is missing), walking west into the hut.
	p := walker(9, 3)
	for i := 0; i < 80 && p.Frame == ""; i++ {
		walkStep(lvl, &p, mgl32.Vec3{-0.1, 0, 0})
	}
	if p.Frame != "hut" {
		t.Fatalf("after walking into the hut the body is in frame %q, want hut", p.Frame)
	}
	if p.CurrentChunk != world.EntityGeometryChunkID("hut") {
		t.Errorf("aboard, but standing in chunk %q", p.CurrentChunk)
	}
}

// TestABodyAboardIsCarried is claim 3, and the whole reason the geometry is a
// frame rather than a set of world-space hulls: the occupant is not moved, the
// coordinates are.
func TestABodyAboardIsCarried(t *testing.T) {
	lvl := hutLevel(t, mgl32.Vec3{0, 0, 0})
	p := walker(9, 3)
	for i := 0; i < 80 && p.Frame == ""; i++ {
		walkStep(lvl, &p, mgl32.Vec3{-0.1, 0, 0})
	}
	if p.Frame != "hut" {
		t.Fatalf("never got aboard; frame is %q", p.Frame)
	}
	local := p.Position

	// The hut moves a hundred metres. Its occupant's local position must not
	// change at all.
	hut := world.FindEntity(lvl, "hut")
	hut.Position = mgl32.Vec3{100, 0, -40}
	world.RefreshFrames(lvl, lvl.Frames)

	if !vecNear(p.Position, local, 1e-5) {
		t.Errorf("the occupant's local position moved from %v to %v when the hut did",
			local, p.Position)
	}
	// And in world terms it has gone with the hut.
	got := lvl.Frames.Frame("hut").ToWorld(p.Position)
	if !vecNear(got, local.Add(mgl32.Vec3{100, 0, -40}), 1e-4) {
		t.Errorf("world position %v — the occupant was left behind", got)
	}
}

// TestWalkingBackOutReturnsToTheWorld is claim 4, and the half that would be a
// trap if it were missing: standing on the geometry lets a body in, and
// stepping off it is the only thing that has to let it out.
func TestWalkingBackOutReturnsToTheWorld(t *testing.T) {
	lvl := hutLevel(t, mgl32.Vec3{0, 0, 0})
	p := walker(9, 3)
	for i := 0; i < 80 && p.Frame == ""; i++ {
		walkStep(lvl, &p, mgl32.Vec3{-0.1, 0, 0})
	}
	if p.Frame != "hut" {
		t.Fatalf("never got aboard; frame is %q", p.Frame)
	}
	for i := 0; i < 120 && p.Frame != ""; i++ {
		walkStep(lvl, &p, mgl32.Vec3{0.1, 0, 0})
	}
	if p.Frame != "" {
		t.Fatalf("walking back out through the doorway left the body in frame %q", p.Frame)
	}
}

// TestAnEntityIsNotSolidToItself: a body aboard resolves against the hut's
// brushes through the ordinary chunk path, in the hut's coordinates. Doing it
// again through the world-space pass would push it twice.
func TestAnEntityIsNotSolidToItself(t *testing.T) {
	lvl := hutLevel(t, mgl32.Vec3{0, 0, 0})
	p := walker(3, 3)
	p.Frame = "hut"
	frame := lvl.Frames.Frame("hut")

	out, _, _ := resolveAgainstEntityBrushes(lvl, &p, frame, p.Position, 0.3, 0.9)
	if out != p.Position {
		t.Errorf("a body aboard the hut was pushed by the hut's own brushes: %v -> %v",
			p.Position, out)
	}
}

// TestAnEntityWithBrushesIsNotAlsoABoundingBox: the cage would push a body out
// of the very volume it is trying to walk into, so an entity that carries
// brushes presents those and not its box.
func TestAnEntityWithBrushesIsNotAlsoABoundingBox(t *testing.T) {
	withCollisionMasters(t, true, true)
	lvl := hutLevel(t, mgl32.Vec3{0, 0, 0})

	hut := world.FindEntity(lvl, "hut")
	hut.BoundHalfX, hut.BoundHalfY, hut.BoundHalfZ = 3, 1.5, 3
	hut.BoundOffset = mgl32.Vec3{3, 1.5, 3}
	hut.Collision = world.EntityCollision{World: true, Entities: true}

	// A crate standing inside the hut, well within that box.
	crate := solidEntity("crate", mgl32.Vec3{3, 0.5, 3})
	crate.Frame = "hut"
	lvl.Entities = append(lvl.Entities, crate)

	before := world.FindEntity(lvl, "crate").Position
	ResolveEntityCollisions(lvl, nil)
	if got := world.FindEntity(lvl, "crate").Position; got != before {
		t.Errorf("the hut's bounding cage shoved a crate standing inside it: %v -> %v", before, got)
	}
}

// ---------- an entity whose solid does not fill its own box ----------
//
// The hut above is a box, so its brushes and their bounding box are very nearly
// the same region and "inside the bounds" is a fair reading of "inside the
// hut". A ship with wings is the case where they part company: its chunk's AABB
// spans the wingspan, and most of that is the open air a body walks through on
// its way to the ramp. Handing that body aboard means the world's chunks stop
// being consulted at all, so the ground it is standing on stops existing — the
// player walking up to a parked ship fell through the level.
//
// What settles it is being carried: aboard is standing ON the entity's solid,
// which its deck, its ramp and its hold's floor all are, and the air under a
// wing is not.

// wingedShipEntity is a hut with two wings bolted on well clear of the hull,
// two metres up — high enough to walk under, wide enough that the bounds of the
// whole thing are mostly open air.
func wingedShipEntity(id string, pos mgl32.Vec3) world.Entity {
	e := hutEntity(id, pos)
	e.Brushes = append(e.Brushes,
		world.NewBoxBrush("wing_negx", mgl32.Vec3{-8, 2.2, 3}, mgl32.Vec3{8, 0.2, 2}, "hull"),
		world.NewBoxBrush("wing_posx", mgl32.Vec3{14, 2.2, 3}, mgl32.Vec3{8, 0.2, 2}, "hull"),
	)
	return e
}

// wingedShipLevel parks one on a world floor, so that "walked through the
// ground" is a thing a test can actually observe.
func wingedShipLevel(t *testing.T) *world.Level {
	t.Helper()
	e := wingedShipEntity("ship", mgl32.Vec3{0, 0, 0})
	lvl := &world.Level{Entities: []world.Entity{e}}

	// The world is one big room rather than a bare slab: a chunk's bounds are
	// what MovePlayer looks a body up in, and a body standing on top of a slab
	// is above that slab's bounds and so in no chunk at all.
	ground := world.Chunk{ID: "ground", Scale: 1}
	ground.Brushes = world.NewHollowRoom("ground",
		mgl32.Vec3{-60, 0, -60}, mgl32.Vec3{60, 20, 60}, 1, "grass")
	ground.RecomputeBounds()
	lvl.Chunks = append(lvl.Chunks, ground)

	ch := world.Chunk{ID: world.EntityGeometryChunkID(e.ID), Scale: 1, FrameOwner: e.ID}
	ch.FromEntity = e.ID
	ch.Brushes = world.CloneEntityBrushes(e.Brushes)
	ch.RecomputeBounds()
	lvl.Chunks = append(lvl.Chunks, ch)

	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	return lvl
}

// gravityWalk is walkStep with gravity on, which is what makes falling through
// the world visible at all.
func gravityWalk(lvl *world.Level, p *world.Entity, step mgl32.Vec3) {
	MovePlayer(p, lvl, PlayerMove{
		Intent: step, DT: 1.0 / 60, Gravity: true,
		GravityAccel: 72, MaxFallSpeed: 60, JumpVelocity: 18,
	})
	if c := FindFramePortalCrossing(lvl, p); c != nil {
		CrossFramePortal(lvl, p, c, 0)
	}
}

// TestWalkingUnderAWingStaysInTheWorld is the bug: the whole wingspan is inside
// the geometry chunk's bounds, and a body admitted out there is a body for whom
// the world's ground no longer exists.
func TestWalkingUnderAWingStaysInTheWorld(t *testing.T) {
	lvl := wingedShipLevel(t)
	p := walker(-20, 3)
	for i := 0; i < 200; i++ {
		gravityWalk(lvl, &p, mgl32.Vec3{0.1, 0, 0})
		if p.Frame != "" {
			t.Fatalf("step %d at %v: walking under the wing put the body aboard %q",
				i, p.Position, p.Frame)
		}
		if p.Position.Y() < walkerY-0.2 {
			t.Fatalf("step %d: the body sank to y=%v — the world stopped being solid",
				i, p.Position.Y())
		}
	}
	// And the hull still stopped it: the -X wall's outer face is at x=-0.5.
	if p.Position.X() > -0.7 {
		t.Errorf("walked to x=%v — straight through the ship's wall", p.Position.X())
	}
}

// TestWalkingIntoAWingedShipStillBoardsIt: the stricter rule must not have shut
// the door. The way in is the aft opening, exactly as for the hut.
func TestWalkingIntoAWingedShipStillBoardsIt(t *testing.T) {
	lvl := wingedShipLevel(t)
	p := walker(9, 3)
	for i := 0; i < 120 && p.Frame == ""; i++ {
		gravityWalk(lvl, &p, mgl32.Vec3{-0.1, 0, 0})
	}
	if p.Frame != "ship" {
		t.Fatalf("after walking into the hold the body is in frame %q, want ship", p.Frame)
	}
}

// TestWalkingOutOfAWingedShipReturnsToTheWorld is the same boundary run
// backwards, and the half that would strand a passenger: outside the hold but
// still inside the wingspan is inside the geometry chunk's bounds, so the
// bounds alone would keep the body aboard with nothing under its feet.
func TestWalkingOutOfAWingedShipReturnsToTheWorld(t *testing.T) {
	lvl := wingedShipLevel(t)
	p := walker(9, 3)
	for i := 0; i < 120 && p.Frame == ""; i++ {
		gravityWalk(lvl, &p, mgl32.Vec3{-0.1, 0, 0})
	}
	if p.Frame != "ship" {
		t.Fatalf("never got aboard; frame is %q", p.Frame)
	}
	for i := 0; i < 60 && p.Frame != ""; i++ {
		gravityWalk(lvl, &p, mgl32.Vec3{0.1, 0, 0})
	}
	if p.Frame != "" {
		t.Fatalf("walking out under the wing left the body aboard %q at %v",
			p.Frame, p.Position)
	}
	// Still well inside the geometry chunk's bounds, which reach x=22 — the
	// bounds are not what let it out.
	if p.Position.X() > 22 {
		t.Fatalf("only left the ship past its bounds, at x=%v", p.Position.X())
	}
	if p.Position.Y() < walkerY-0.2 {
		t.Errorf("landed at y=%v — the world was not solid on the way out", p.Position.Y())
	}
}

// TestStandingOnAWingIsAboard: underfoot is the rule, and a wing is as much a
// floor as a deck is. A body on top of one is carried by the ship, which is the
// whole point of being handed into its frame.
func TestStandingOnAWingIsAboard(t *testing.T) {
	lvl := wingedShipLevel(t)
	// The -X wing's top face is at y=2.4; a walker's centre stands 0.9 above it.
	p := walker(-8, 3)
	p.Position = mgl32.Vec3{-8, 3.3, 3}

	c := FindFramePortalCrossing(lvl, &p)
	if c == nil || c.ToID != "ship" {
		t.Fatalf("standing on the ship's wing did not hand the body aboard: %+v", c)
	}
}

func TestAnEntityInOnlyDecorativeBrushesStillCollidesByItsBox(t *testing.T) {
	withCollisionMasters(t, true, true)
	a := solidEntity("a", mgl32.Vec3{0, 0, 0})
	a.Collision.Gravity = false
	a.Brushes = []world.Brush{world.NewBoxBrush("body", mgl32.Vec3{}, mgl32.Vec3{0.5, 0.5, 0.5}, "")}
	a.Brushes[0].Solid = false
	b := solidEntity("b", mgl32.Vec3{0.6, 0, 0})
	b.Collision.Gravity = false
	lvl := &world.Level{Entities: []world.Entity{a, b}}

	if moved := ResolveEntityCollisions(lvl, nil); len(moved) == 0 {
		t.Fatal("two overlapping boxes were left overlapping because one wears a non-solid brush")
	}

	lvl.Entities[0].Brushes[0].Solid = true
	lvl.Entities[0].Position = mgl32.Vec3{}
	lvl.Entities[1].Position = mgl32.Vec3{0.6, 0, 0}
	if moved := ResolveEntityCollisions(lvl, nil); len(moved) != 0 {
		t.Error("an entity with a solid brush was resolved by its box as well")
	}
}
