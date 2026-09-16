package collision

// step_up_test.go — StepUp, and the two ways a vertical teleport goes wrong.
//
// Stepping up is not a swept move: the body is lifted straight to a probe
// height and re-resolved there, so the only thing keeping it honest is that
// the probe lands in free space. Both bugs below came from that not being
// checked properly, and they pull in opposite directions — one let the body
// through geometry it never touched, the other refused a step that was
// perfectly legal — which is why the fix for either has to be tested against
// the other.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// stepUpAllowance is what these fixtures set rather than taking the default.
//
// The engine's default is a curb (world.DefaultStepHeight), and a lift that
// short cannot reach a ceiling to be ejected through — so left on the default
// these tests would pass without exercising anything. 1.8 is a height an author
// can still set, and is what the default used to be; the resolver has to be
// safe at every allowance the field offers, not just at the one it ships with.
const stepUpAllowance = float32(1.8)

func stepRoom(ceilY float32, extra ...world.Brush) *world.Level {
	brushes := world.NewHollowRoom("r",
		mgl32.Vec3{-5, 0, -5}, mgl32.Vec3{5, ceilY, 5}, 0.5, "concrete")
	brushes = append(brushes, extra...)
	ch := world.Chunk{ID: "room", Brushes: brushes}
	ch.RecomputeBounds()
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	lvl.Player.StepHeight = stepUpAllowance
	if err := world.RebuildIndexes(lvl); err != nil {
		panic(err)
	}
	return lvl
}

// block is a solid step sitting on the floor at x:[2,4], topY tall.
func block(topY float32) world.Brush {
	return world.NewBoxBrush("block",
		mgl32.Vec3{3, topY / 2, 0}, mgl32.Vec3{1, topY / 2, 2}, "concrete")
}

func stepMove(intent mgl32.Vec3, jump bool) PlayerMove {
	return PlayerMove{
		Intent: intent, Jump: jump, Gravity: true, DTScale: 1.0,
		GravityAccel: 0.010, MaxFallSpeed: 0.50, JumpVelocity: 0.20,
	}
}

// walkAt drops a player in, walks it toward +X, and lets the last stride
// settle so what is asserted is where the body rests rather than wherever it
// happened to be mid-stride.
func walkAt(lvl *world.Level, frames int) *world.Entity {
	p := world.NewPlayer(mgl32.Vec3{-2, 1, 0})
	for i := 0; i < 40; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
	}
	for i := 0; i < frames; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{0.08, 0, 0}, false))
	}
	for i := 0; i < 40; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
	}
	return p
}

func feet(p *world.Entity) float32 { return p.Position.Y() - p.BoundHalfY }

// Walking into a wall must not put the player on the roof.
//
// The raise lands the body straddling the ceiling slab, more than half of the
// way through it. A minimum-translation push has no memory of which side the
// body came from, so from there the shortest way out is the FAR side: the body
// was ejected upward, settled on the roof, and then walked off through the top
// of the wall it had been standing against.
func TestWalkingIntoAWallDoesNotStepThroughTheCeiling(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ceilY  float32
		intent mgl32.Vec3
	}{
		{"into a wall, 4 high", 4, mgl32.Vec3{0.2, 0, 0}},
		{"into a corner, 2.5 high", 2.5, mgl32.Vec3{0.2, 0, 0.2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lvl := stepRoom(tc.ceilY)
			p := world.NewPlayer(mgl32.Vec3{0, 1, 0})
			for i := 0; i < 30; i++ {
				MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
			}
			for i := 0; i < 400; i++ {
				MovePlayer(p, lvl, stepMove(tc.intent, i%40 == 0))
				if head := p.Position.Y() + p.BoundHalfY; head > tc.ceilY+0.02 {
					t.Fatalf("frame %d: head reached y=%v, through the ceiling at %v (pos %v)",
						i, head, tc.ceilY, p.Position)
				}
			}
			if x, z := p.Position.X(), p.Position.Z(); x < -5 || x > 5 || z < -5 || z > 5 {
				t.Errorf("ended outside the room at %v", p.Position)
			}
		})
	}
}

// The step still has to work, or the fix above is just "stepping is disabled".
func TestAPlayerStepsOntoACurb(t *testing.T) {
	p := walkAt(stepRoom(4, block(0.4)), 62)
	if got := feet(p); got < 0.39 || got > 0.45 {
		t.Errorf("feet rested at y=%v, want standing on the 0.4 curb", got)
	}
	if p.Position.X() < 2 {
		t.Errorf("never got onto the block: x=%v", p.Position.X())
	}
}

// Walking a gentle slope must not bounce. On a tilted floor the flat resolve
// pushes the body out along the slope's normal, which leaves it a hair short of
// its target and reads as blocked; the step-up then lifted it, found no floor
// contact on the way back down (the lowest probe sits exactly where the body
// already rested, just clear of the floor), and left it the whole allowance in
// the air — to fall for ten frames and be lifted again, the length of every
// hillside in a terrain level.
func TestWalkingASlopeDoesNotBounce(t *testing.T) {
	ramp := world.NewBoxBrush("ramp", mgl32.Vec3{0, 0.1, 0}, mgl32.Vec3{4, 0.1, 2}, "concrete")
	// Raise the +X top edge (corners 3 and 7): a planar top rising 1.2 over
	// 8 units, about 8.5 degrees.
	ramp.Verts[3][1] += 1.2
	ramp.Verts[7][1] += 1.2
	for _, dir := range []float32{1, -1} {
		lvl := stepRoom(6, ramp)
		p := world.NewPlayer(mgl32.Vec3{-3 * dir, 2, 0})
		for i := 0; i < 60; i++ {
			MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
		}
		prev := p.Position.Y()
		for i := 0; i < 70; i++ {
			MovePlayer(p, lvl, stepMove(mgl32.Vec3{0.08 * dir, 0, 0}, false))
			if dy := p.Position.Y() - prev; dy > 0.1 || dy < -0.1 {
				t.Fatalf("walking %+v: frame %d moved %v vertically (y %v -> %v) — a bounce, not a slope",
					dir, i, dy, prev, p.Position.Y())
			}
			prev = p.Position.Y()
		}
	}
}

// And it has to work in a room with a low ceiling, which is the case a single
// full-height probe cannot serve: raising the whole allowance at once buries
// the body in the ceiling of any room shorter than that allowance plus the
// body. Refusing the step there — which is what the first version of the fix
// above did — makes a 0.4 curb impassable in a 2.5-unit room.
func TestAPlayerStepsOntoACurbUnderALowCeiling(t *testing.T) {
	p := walkAt(stepRoom(2.5, block(0.4)), 62)
	if got := feet(p); got < 0.39 || got > 0.45 {
		t.Errorf("feet rested at y=%v, want standing on the 0.4 curb", got)
	}
}

// A riser taller than the allowance is a wall, and stays one.
func TestAPlayerIsStoppedByAWallTallerThanTheStep(t *testing.T) {
	p := walkAt(stepRoom(4, block(2.5)), 62)
	if got := feet(p); got > 0.1 {
		t.Errorf("climbed a 2.5-tall block: feet at %v", got)
	}
	if p.Position.X() > 2.1 {
		t.Errorf("walked into the block: x=%v, want stopped short of it", p.Position.X())
	}
}

// ---------- a riser that belongs to an entity ----------

// A body walking into a ship is stopped by two different sets of solids: the
// chunk it is standing in, and the ship's own brushes, which are in the ship's
// coordinates and in none of this frame's chunks at all
// (collision/entity_brushes.go). The step-up used to retry against the chunk
// alone — so a threshold at the door found the raised probe unobstructed, took
// the step, and was pushed straight back out by the hull it had not consulted.
//
// The visible symptom is narrow and easy to mistake for a level-authoring
// problem: an entity whose floor is flush with the ground outside can be
// walked into, and one whose floor sits a hand's breadth above it cannot be
// entered at all, however low the sill.

// steppedHut is a hut whose deck is `lip` above the ground outside it, with the
// +X wall left out for a doorway. The world is one big room around it, so a
// body walking in is walking from one solid onto another.
func steppedHut(t *testing.T, lip float32) *world.Level {
	t.Helper()
	e := world.NewBlankEntity("hut")
	e.Position = mgl32.Vec3{0, 0, 0}
	e.Scale = mgl32.Vec3{1, 1, 1}
	room := world.NewHollowRoom("wall",
		mgl32.Vec3{0, lip, 0}, mgl32.Vec3{6, lip + 3, 6}, 0.5, "concrete")
	for i := range room {
		if room[i].ID != "wall_wall_posx" {
			e.Brushes = append(e.Brushes, room[i])
		}
	}

	lvl := &world.Level{Entities: []world.Entity{e}}
	ground := world.Chunk{ID: "ground", Brushes: world.NewHollowRoom("ground",
		mgl32.Vec3{-60, 0, -60}, mgl32.Vec3{60, 20, 60}, 1, "grass")}
	ground.RecomputeBounds()
	lvl.Chunks = append(lvl.Chunks, ground)

	ch := world.Chunk{ID: world.EntityGeometryChunkID(e.ID), Scale: 1, FrameOwner: e.ID}
	ch.FromEntity = e.ID
	ch.Brushes = world.CloneEntityBrushes(e.Brushes)
	ch.RecomputeBounds()
	lvl.Chunks = append(lvl.Chunks, ch)

	lvl.Player.StepHeight = stepUpAllowance
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	return lvl
}

// walkWest is the game loop's own tick — MovePlayer then the portal check —
// walking toward the hut's doorway and settling on the spot at the end.
func walkWest(lvl *world.Level, p *world.Entity, frames int) {
	for i := 0; i < frames; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{-0.08, 0, 0}, false))
		if c := FindFramePortalCrossing(lvl, p); c != nil {
			CrossFramePortal(lvl, p, c, 0)
		}
	}
	for i := 0; i < 40; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
		if c := FindFramePortalCrossing(lvl, p); c != nil {
			CrossFramePortal(lvl, p, c, 0)
		}
	}
}

// TestAPlayerStepsUpIntoAnEntitysInterior: the deck is 0.35 up, well inside the
// step allowance, and walking in has to climb it.
func TestAPlayerStepsUpIntoAnEntitysInterior(t *testing.T) {
	const lip = float32(0.35)
	lvl := steppedHut(t, lip)

	p := walker(9, 3)
	walkWest(lvl, &p, 120)
	if p.Frame != "hut" {
		t.Fatalf("stopped outside the doorway at %v — never got over the sill", p.Position)
	}
	// Aboard, the position is in the hut's own coordinates, where the deck's
	// top is exactly lip.
	if got := feet(&p); got < lip-0.02 || got > lip+0.06 {
		t.Errorf("feet rested at y=%v, want standing on the deck at %v", got, lip)
	}
}

// And the allowance still means something: a deck the body could not climb is
// a wall, exactly as a curb that tall would be.
func TestAPlayerCannotStepIntoAnEntityDeckTallerThanTheStep(t *testing.T) {
	lvl := steppedHut(t, stepUpAllowance+0.5)

	p := walker(9, 3)
	walkWest(lvl, &p, 120)
	if p.Frame != "" {
		t.Errorf("climbed a %v sill with a %v step allowance", stepUpAllowance+0.5, stepUpAllowance)
	}
}

// A wall that leans back — steeper than a walkable slope, but not vertical —
// is still a wall. Every raised probe slid up the face and stopped against it
// rather than on a floor, and the settle kept that height; next frame the same
// lift ran again from there, so walking into a canyon cliff scaled it.
func TestAPlayerCannotClimbALeaningWall(t *testing.T) {
	for _, lean := range []float32{0.5, 2, 6} { // over 8 high: ~86°, ~76°, ~53°
		wall := world.NewBoxBrush("wall", mgl32.Vec3{3, 4, 0}, mgl32.Vec3{2, 4, 3}, "rock")
		// Lean the -X face back: move its top corners (2, 6) away from the player.
		wall.Verts[2][0] += lean
		wall.Verts[6][0] += lean
		p := walkAt(stepRoom(12, wall), 300)
		if got := feet(p); got > 0.25 {
			t.Errorf("lean %v: feet at y=%v, climbed the wall", lean, got)
		}
	}
}
