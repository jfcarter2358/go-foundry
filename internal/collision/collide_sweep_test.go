package collision

// collide_sweep_test.go — moving fast without going through things.
//
// The wall case here is the one that used to fail. At the shipped walking speed
// of 0.1 the positional resolvers hold fine; at 0.5 the player cleared the wall
// *and* left the chunk in a single step, the lookup then found nothing to
// resolve against, and it kept going forever.

import (
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestSweepStepsScalesWithSpeed(t *testing.T) {
	half := mgl32.Vec3{0.3, 0.9, 0.3} // the player

	if got := SweepSteps(half, mgl32.Vec3{}); got != 1 {
		t.Errorf("standing still asked for %d steps, want 1", got)
	}
	if got := SweepSteps(half, mgl32.Vec3{0.1, 0, 0}); got != 1 {
		t.Errorf("walking asked for %d steps, want 1 — the common case must stay free", got)
	}

	// Beyond half the smallest extent it has to start splitting, and the count
	// has to keep up with the distance.
	slow := SweepSteps(half, mgl32.Vec3{1, 0, 0})
	fast := SweepSteps(half, mgl32.Vec3{10, 0, 0})
	if slow < 2 {
		t.Errorf("a 1-unit step for a 0.3-radius body asked for %d steps", slow)
	}
	if fast <= slow {
		t.Errorf("ten times the distance asked for %d steps, no more than %d", fast, slow)
	}

	// And it is bounded, so one absurd velocity cannot stall a frame.
	if got := SweepSteps(half, mgl32.Vec3{1e9, 0, 0}); got != maxSweepSteps {
		t.Errorf("an enormous step asked for %d, want the cap %d", got, maxSweepSteps)
	}
	// A body with no size still asks for a finite number.
	if got := SweepSteps(mgl32.Vec3{}, mgl32.Vec3{5, 0, 0}); got < 1 || got > maxSweepSteps {
		t.Errorf("a zero-extent body asked for %d steps", got)
	}
}

func TestFastPlayerDoesNotGoThroughAWall(t *testing.T) {
	// Speeds spanning three orders of magnitude, all aimed at the x=0 wall.
	for _, speed := range []float32{0.1, 0.5, 2, 20, 200} {
		lvl := collisionTestLevel()
		p := newTestPlayer(mgl32.Vec3{5, 0.9, 5})
		settle(p, lvl, 5)

		for i := 0; i < 60; i++ {
			MovePlayer(p, lvl, walkMove(mgl32.Vec3{-speed, 0, 0}))
		}
		if p.Position.X() < p.BoundHalfX-1e-3 {
			t.Errorf("speed %v: ended at x=%v, past the wall at x=0", speed, p.Position.X())
		}
		if p.CurrentChunk == "" {
			t.Errorf("speed %v: ended outside every chunk at x=%v", speed, p.Position.X())
		}
	}
}

func TestFastPlayerStaysInsideTheLevel(t *testing.T) {
	// Charging in every horizontal direction in turn must leave the player
	// somewhere the engine can still find, whatever the speed.
	dirs := []mgl32.Vec3{{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}, {1, 0, 1}, {-1, 0, -1}}
	for _, d := range dirs {
		for _, speed := range []float32{1, 15, 150} {
			lvl := collisionTestLevel()
			p := newTestPlayer(mgl32.Vec3{5, 0.9, 5})
			settle(p, lvl, 5)

			for i := 0; i < 40; i++ {
				MovePlayer(p, lvl, walkMove(d.Mul(speed)))
			}
			if p.CurrentChunk == "" {
				t.Errorf("dir %v speed %v: escaped the level to %v", d, speed, p.Position)
			}
		}
	}
}

func TestFastFallLandsOnTheFloor(t *testing.T) {
	lvl := collisionTestLevel()
	// Dropped from just under the room's (solid, sealed) ceiling rather than
	// from outside and above it — a brush roof is real geometry, so falling
	// onto it from outside lands ON the roof, same as any other floor. The
	// room is only 8 units tall, so this is still a fall many times the
	// player's own height at the speed below.
	p := newTestPlayer(mgl32.Vec3{5, 7.5, 5})
	// Already at terminal velocity, sixty units a frame — far more than the
	// player is tall, and well past the point where a single positional clamp
	// per frame would be sampling the world in the wrong places.
	p.Velocity[1] = -60

	mv := walkMove(mgl32.Vec3{})
	mv.MaxFallSpeed = 60

	landed := false
	for i := 0; i < 40 && !landed; i++ {
		landed = MovePlayer(p, lvl, mv)
	}
	if !landed {
		t.Fatalf("never landed; ended at y=%v", p.Position.Y())
	}
	if got := p.Position.Y(); math.Abs(float64(got-p.BoundHalfY)) > 1e-3 {
		t.Errorf("came to rest at y=%v, want %v", got, p.BoundHalfY)
	}
	if p.CurrentChunk == "" {
		t.Error("landed outside every chunk")
	}
}

func TestFastEntityDoesNotLeaveTheLevel(t *testing.T) {
	withCollisionMasters(t, true, true)
	lvl := collisionTestLevel()

	e := solidEntity("bullet", mgl32.Vec3{5, 2, 5})
	e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ = 0.2, 0.2, 0.2
	e.BoundOffset = mgl32.Vec3{}
	e.Collision.Gravity = false
	e.Velocity = mgl32.Vec3{-90, 0, 0} // straight at the x=0 wall
	lvl.Entities = []world.Entity{e}

	for i := 0; i < 30; i++ {
		EntityUpdate(lvl, 1.0/60.0, true, nil)
	}
	got := lvl.Entities[0].Position
	if got.X() < lvl.Entities[0].BoundHalfX-1e-2 {
		t.Errorf("entity ended at %v, past the wall at x=0", got)
	}
}

// ---------- SweepHit ----------

func TestSweepHitFindsTheContactFraction(t *testing.T) {
	// A unit box travelling +X toward another whose near face is 3 units away.
	// It has 2 units of clearance over a 4-unit move, so it touches halfway.
	a := Box{C: mgl32.Vec3{0, 0, 0}, Half: mgl32.Vec3{0.5, 0.5, 0.5}}
	b := Box{C: mgl32.Vec3{3, 0, 0}, Half: mgl32.Vec3{0.5, 0.5, 0.5}}

	tHit, hit := SweepHit(a, b, mgl32.Vec3{4, 0, 0})
	if !hit {
		t.Fatal("reported no contact on a move that ends inside the target")
	}
	if math.Abs(float64(tHit-0.5)) > 1e-2 {
		t.Errorf("contact at t=%v, want 0.5", tHit)
	}
	// And the position it reports really is just touching.
	at := Translate(a, mgl32.Vec3{4 * tHit, 0, 0})
	if _, depth, ok := Penetration(at, b); ok && depth > 1e-2 {
		t.Errorf("the reported contact point is already %v deep", depth)
	}
}

func TestSweepHitMissesWhenItFallsShort(t *testing.T) {
	a := Box{C: mgl32.Vec3{0, 0, 0}, Half: mgl32.Vec3{0.5, 0.5, 0.5}}
	b := Box{C: mgl32.Vec3{10, 0, 0}, Half: mgl32.Vec3{0.5, 0.5, 0.5}}

	if tHit, hit := SweepHit(a, b, mgl32.Vec3{4, 0, 0}); hit {
		t.Errorf("reported contact at t=%v on a move that stops well short", tHit)
	}
}

func TestSweepHitMissesWhenItPassesToTheSide(t *testing.T) {
	a := Box{C: mgl32.Vec3{0, 0, 0}, Half: mgl32.Vec3{0.5, 0.5, 0.5}}
	b := Box{C: mgl32.Vec3{5, 8, 0}, Half: mgl32.Vec3{0.5, 0.5, 0.5}}

	if _, hit := SweepHit(a, b, mgl32.Vec3{20, 0, 0}); hit {
		t.Error("reported contact with a box eight units off the path")
	}
}

func TestSweepHitReportsAnExistingOverlapImmediately(t *testing.T) {
	a := Box{C: mgl32.Vec3{0, 0, 0}, Half: mgl32.Vec3{1, 1, 1}}
	b := Box{C: mgl32.Vec3{0.5, 0, 0}, Half: mgl32.Vec3{1, 1, 1}}

	tHit, hit := SweepHit(a, b, mgl32.Vec3{5, 0, 0})
	if !hit || tHit != 0 {
		t.Errorf("already-overlapping pair reported t=%v hit=%v, want 0/true", tHit, hit)
	}
}

func TestSweepHitDoesNotStepOverAThinTarget(t *testing.T) {
	// The property that matters: a long move past a thin obstacle must still
	// find it. Conservative advancement never steps farther than the remaining
	// clearance, so there is no step size for the obstacle to hide between.
	a := Box{C: mgl32.Vec3{0, 0, 0}, Half: mgl32.Vec3{0.1, 0.1, 0.1}}
	thin := Box{C: mgl32.Vec3{50, 0, 0}, Half: mgl32.Vec3{0.01, 5, 5}}

	tHit, hit := SweepHit(a, thin, mgl32.Vec3{200, 0, 0})
	if !hit {
		t.Fatal("a 200-unit move stepped straight over a 0.02-thick wall")
	}
	if want := float32(49.89 / 200); math.Abs(float64(tHit-want)) > 5e-3 {
		t.Errorf("contact at t=%v, want about %v", tHit, want)
	}
}
