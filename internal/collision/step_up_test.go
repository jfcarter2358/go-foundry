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
