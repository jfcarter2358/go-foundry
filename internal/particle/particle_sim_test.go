package particle

// particle_sim_test.go — the simulation's load-bearing properties.
//
// All of it runs headlessly, which is the whole reason this package has no GL
// and no level in it.

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// stepFor advances a system by dt repeatedly, as a frame loop would.
func stepFor(s *System, dt float32, steps int) {
	for i := 0; i < steps; i++ {
		s.Step(dt, mgl32.Ident4())
	}
}

// TestEmissionRateIsExact — a rate of N per second over one second must emit N,
// carry and all. The fractional carry is the part that goes wrong: dropped, a
// rate below one per frame emits nothing ever.
func TestEmissionRateIsExact(t *testing.T) {
	l := NewLayer("l")
	l.Rate = 10
	l.LifeMin, l.LifeMax = 100, 100 // outlive the test so nothing is reaped
	l.SpeedMin, l.SpeedMax = 0, 0
	l.MaxParticles = 1000
	s := NewSystem(l, 0, 7)

	stepFor(s, 1.0/60.0, 60)

	// One second at 10/s. The substep walk can land a particle either side of
	// the boundary, so one either way is the honest tolerance.
	if n := s.Count(); n < 9 || n > 11 {
		t.Errorf("rate 10/s for 1s emitted %d particles, want ~10", n)
	}
}

// TestSlowRateStillEmits is the carry bug stated directly: 0.5/s must produce a
// particle every two seconds, not none.
func TestSlowRateStillEmits(t *testing.T) {
	l := NewLayer("l")
	l.Rate = 0.5
	l.LifeMin, l.LifeMax = 100, 100
	l.MaxParticles = 100
	s := NewSystem(l, 0, 3)

	stepFor(s, 1.0/60.0, 60*4) // four seconds

	if n := s.Count(); n < 1 {
		t.Fatal("a rate below one per second emitted nothing at all")
	} else if n > 3 {
		t.Errorf("rate 0.5/s for 4s emitted %d, want ~2", n)
	}
}

// TestPoolNeverExceedsItsCap, and never reallocates after the first fill. The
// cap is what makes a particle budget mean anything.
func TestPoolNeverExceedsItsCap(t *testing.T) {
	l := NewLayer("l")
	l.Rate = 10000
	l.LifeMin, l.LifeMax = 100, 100
	l.MaxParticles = 64
	s := NewSystem(l, 0, 11)

	capBefore := cap(s.Live)
	stepFor(s, 1.0/60.0, 120)

	if n := s.Count(); n != 64 {
		t.Errorf("pool holds %d, want exactly the cap of 64", n)
	}
	if cap(s.Live) != capBefore {
		t.Errorf("the pool reallocated (cap %d -> %d); it is preallocated so a frame's cost is predictable",
			capBefore, cap(s.Live))
	}
}

// TestParticlesDieAtTheirLifetime, and the pool is compacted when they do.
func TestParticlesDieAtTheirLifetime(t *testing.T) {
	l := NewLayer("l")
	l.Rate = 0
	l.LifeMin, l.LifeMax = 0.5, 0.5
	l.MaxParticles = 50
	s := NewSystem(l, 0, 5)

	s.Burst(10, mgl32.Ident4())
	if s.Count() != 10 {
		t.Fatalf("burst produced %d, want 10", s.Count())
	}
	stepFor(s, 1.0/60.0, 20) // ~0.33s: still alive
	if s.Count() != 10 {
		t.Errorf("after 0.33s of a 0.5s life, %d alive; want 10", s.Count())
	}
	stepFor(s, 1.0/60.0, 20) // past 0.5s
	if s.Count() != 0 {
		t.Errorf("after 0.66s of a 0.5s life, %d still alive; want 0", s.Count())
	}
}

// TestSwapRemoveDoesNotSkip is the specific bug the backwards walk exists to
// prevent: with a forward walk, removing element i swaps in the last one and
// the loop steps past it, so half a batch of simultaneously-dying particles
// survives an extra frame.
func TestSwapRemoveDoesNotSkip(t *testing.T) {
	l := NewLayer("l")
	l.Rate = 0
	l.LifeMin, l.LifeMax = 0.1, 0.1
	l.MaxParticles = 200
	s := NewSystem(l, 0, 13)

	s.Burst(100, mgl32.Ident4())
	// One step long enough to kill every one of them at once.
	s.Step(0.2, mgl32.Ident4())
	if s.Count() != 0 {
		t.Errorf("%d of 100 identically-aged particles survived their death frame", s.Count())
	}
}

// TestGravityPullsDown, and the layer's own multiplier scales it — including
// negative, which is how smoke rises.
func TestGravityPullsDown(t *testing.T) {
	newFall := func(mult float32) *System {
		l := NewLayer("l")
		l.Rate = 0
		l.Drag = 0
		l.Gravity = mult
		l.LifeMin, l.LifeMax = 10, 10
		l.SpeedMin, l.SpeedMax = 0, 0
		l.Shape = ShapePoint
		l.MaxParticles = 4
		s := NewSystem(l, 10, 17)
		s.Burst(1, mgl32.Ident4())
		stepFor(s, 1.0/60.0, 60)
		return s
	}

	if y := newFall(1).Live[0].Pos.Y(); y > -4 {
		t.Errorf("a second of gravity 1 at 10 u/s^2 moved to y=%v; want about -5", y)
	}
	if y := newFall(0).Live[0].Pos.Y(); math.Abs(float64(y)) > 1e-4 {
		t.Errorf("gravity 0 should float, got y=%v", y)
	}
	if y := newFall(-1).Live[0].Pos.Y(); y < 4 {
		t.Errorf("negative gravity should rise, got y=%v", y)
	}
}

// TestRampsEvaluateAtTheEndsAndBetween covers the curve every layer's size and
// alpha go through.
func TestRamps(t *testing.T) {
	r := Ramp{{T: 0, V: 0}, {T: 0.5, V: 1}, {T: 1, V: 0}}
	for _, c := range []struct{ at, want float32 }{
		{-1, 0}, {0, 0}, {0.25, 0.5}, {0.5, 1}, {0.75, 0.5}, {1, 0}, {2, 0},
	} {
		if got := r.At(c.at); math.Abs(float64(got-c.want)) > 1e-5 {
			t.Errorf("ramp at %v: got %v, want %v", c.at, got, c.want)
		}
	}
	// An empty ramp is "always 1", which is what lets a layer carry none.
	if got := (Ramp{}).At(0.5); got != 1 {
		t.Errorf("empty ramp: got %v, want 1", got)
	}
	// A single stop holds its value everywhere.
	if got := (Ramp{{T: 0.5, V: 3}}).At(0); got != 3 {
		t.Errorf("single-stop ramp: got %v, want 3", got)
	}
}

func TestGradient(t *testing.T) {
	g := Gradient{{T: 0, C: mgl32.Vec3{1, 0, 0}}, {T: 1, C: mgl32.Vec3{0, 0, 1}}}
	mid := g.At(0.5)
	if math.Abs(float64(mid.X()-0.5)) > 1e-5 || math.Abs(float64(mid.Z()-0.5)) > 1e-5 {
		t.Errorf("gradient midpoint: got %v, want {0.5 0 0.5}", mid)
	}
	if got := (Gradient{}).At(0.5); got != (mgl32.Vec3{1, 1, 1}) {
		t.Errorf("empty gradient: got %v, want white", got)
	}
}

// TestNilCollideIsANoOp — the seam defaults to nil, and a layer that asks for
// collision in a context with no world installed must simply not collide rather
// than panic.
func TestNilCollideIsANoOp(t *testing.T) {
	prev := Collide
	Collide = nil
	t.Cleanup(func() { Collide = prev })

	l := NewLayer("l")
	l.Rate = 0
	l.Collision = CollideBounce
	l.Gravity = 1
	l.Drag = 0
	l.LifeMin, l.LifeMax = 10, 10
	l.MaxParticles = 4
	s := NewSystem(l, 10, 19)
	s.Burst(1, mgl32.Ident4())
	stepFor(s, 1.0/60.0, 60)

	if s.Count() != 1 {
		t.Fatalf("particle vanished with no collider installed")
	}
	if y := s.Live[0].Pos.Y(); y > -4 {
		t.Errorf("with no collider the particle should have fallen freely, y=%v", y)
	}
}

// TestBounceReversesTheNormalComponent — the response that makes a spark skip
// off a floor, checked against a flat plane at y = 0.
func TestBounceReversesTheNormalComponent(t *testing.T) {
	prev := Collide
	// A floor at y = 0: anything crossing it is stopped there, normal up.
	Collide = func(from, to mgl32.Vec3, r float32) (mgl32.Vec3, mgl32.Vec3, bool) {
		if to.Y() > 0 {
			return mgl32.Vec3{}, mgl32.Vec3{}, false
		}
		return mgl32.Vec3{to.X(), 0, to.Z()}, mgl32.Vec3{0, 1, 0}, true
	}
	t.Cleanup(func() { Collide = prev })

	l := NewLayer("l")
	l.Rate = 0
	l.Collision = CollideBounce
	l.Bounce = 1 // perfectly elastic, so the sign flip is unmistakable
	l.Friction = 0
	l.Gravity = 1
	l.Drag = 0
	l.LifeMin, l.LifeMax = 10, 10
	l.MaxParticles = 4
	s := NewSystem(l, 10, 23)
	s.Burst(1, mgl32.Ident4())
	s.Live[0].Pos = mgl32.Vec3{0, 1, 0}
	s.Live[0].Vel = mgl32.Vec3{1, -5, 0}

	stepFor(s, 1.0/60.0, 30)

	if s.Live[0].Vel.Y() <= 0 {
		t.Errorf("after bouncing off an upward normal the particle is still descending (vy=%v)", s.Live[0].Vel.Y())
	}
	if s.Live[0].Pos.Y() < -1e-3 {
		t.Errorf("the particle ended below the floor at y=%v", s.Live[0].Pos.Y())
	}
	// Friction 0 leaves the tangential component alone.
	if math.Abs(float64(s.Live[0].Vel.X()-1)) > 1e-3 {
		t.Errorf("friction 0 changed the tangential velocity: vx=%v, want 1", s.Live[0].Vel.X())
	}
}

// TestKillEndsTheParticleOnContact.
func TestKillEndsTheParticleOnContact(t *testing.T) {
	prev := Collide
	Collide = func(from, to mgl32.Vec3, r float32) (mgl32.Vec3, mgl32.Vec3, bool) {
		if to.Y() > 0 {
			return mgl32.Vec3{}, mgl32.Vec3{}, false
		}
		return mgl32.Vec3{to.X(), 0, to.Z()}, mgl32.Vec3{0, 1, 0}, true
	}
	t.Cleanup(func() { Collide = prev })

	l := NewLayer("l")
	l.Rate = 0
	l.Collision = CollideKill
	l.Gravity = 1
	l.LifeMin, l.LifeMax = 100, 100
	l.MaxParticles = 4
	s := NewSystem(l, 10, 29)
	s.Burst(1, mgl32.Ident4())
	s.Live[0].Pos = mgl32.Vec3{0, 0.1, 0}
	s.Live[0].Vel = mgl32.Vec3{0, -5, 0}

	stepFor(s, 1.0/60.0, 10)
	if s.Count() != 0 {
		t.Error("a kill-on-contact particle survived hitting the floor")
	}
}

// TestSameSeedSameResult — a reproducible preview, and the property every count
// assertion above quietly depends on.
func TestSameSeedSameResult(t *testing.T) {
	run := func() []Particle {
		l := NewLayer("l")
		l.Rate = 50
		l.LifeMin, l.LifeMax = 5, 5
		l.MaxParticles = 200
		s := NewSystem(l, 9.8, 12345)
		stepFor(s, 1.0/60.0, 30)
		return append([]Particle(nil), s.Live...)
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("same seed produced %d and %d particles", len(a), len(b))
	}
	for i := range a {
		if a[i].Pos != b[i].Pos || a[i].Life != b[i].Life {
			t.Fatalf("same seed diverged at particle %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

// TestBurstsFireOncePerCycle, and again after a loop wraps.
func TestBurstsFireOncePerCycle(t *testing.T) {
	l := NewLayer("l")
	l.Rate = 0
	l.Duration = 1
	l.Loop = false
	l.Bursts = []Burst{{Time: 0.5, Count: 5}}
	l.LifeMin, l.LifeMax = 100, 100
	l.MaxParticles = 100
	s := NewSystem(l, 0, 31)

	stepFor(s, 1.0/60.0, 20) // ~0.33s, before the burst
	if s.Count() != 0 {
		t.Errorf("a burst at t=0.5 fired at t=0.33 (%d particles)", s.Count())
	}
	stepFor(s, 1.0/60.0, 20) // past 0.5s
	if s.Count() != 5 {
		t.Errorf("burst produced %d, want 5", s.Count())
	}
	stepFor(s, 1.0/60.0, 120) // well past the end of a non-looping cycle
	if s.Count() != 5 {
		t.Errorf("a one-shot burst fired again: %d particles", s.Count())
	}
}

// TestMutedLayerDoesNothing — the authoring switch has to actually stop the
// simulation, not merely hide it, or a muted layer still costs a frame.
func TestMutedLayerDoesNothing(t *testing.T) {
	l := NewLayer("l")
	l.Muted = true
	l.Rate = 100
	l.MaxParticles = 100
	s := NewSystem(l, 0, 37)
	stepFor(s, 1.0/60.0, 60)
	if s.Count() != 0 {
		t.Errorf("a muted layer emitted %d particles", s.Count())
	}
}

// TestFillProducesOneInstancePerLiveParticle, with the ramps applied.
func TestFillAppliesRamps(t *testing.T) {
	l := NewLayer("l")
	l.Rate = 0
	l.LifeMin, l.LifeMax = 1, 1
	l.SizeMin, l.SizeMax = 2, 2
	l.SizeRamp = Ramp{{T: 0, V: 1}, {T: 1, V: 0.5}}
	l.AlphaRamp = Ramp{{T: 0, V: 1}, {T: 1, V: 0}}
	l.ColorRamp = Gradient{{T: 0, C: mgl32.Vec3{1, 0, 0}}}
	l.Tint = mgl32.Vec3{1, 1, 1}
	l.MaxParticles = 4
	s := NewSystem(l, 0, 41)
	s.Burst(1, mgl32.Ident4())

	dst := make([]Instance, 8)
	if n := s.Fill(dst, mgl32.Vec3{}, false); n != 1 {
		t.Fatalf("Fill wrote %d instances, want 1", n)
	}
	// At birth: full size, full alpha, the gradient's first colour.
	if got := dst[0].Size.X(); math.Abs(float64(got-2)) > 1e-4 {
		t.Errorf("size at birth: got %v, want 2", got)
	}
	if got := dst[0].Color.W(); math.Abs(float64(got-1)) > 1e-4 {
		t.Errorf("alpha at birth: got %v, want 1", got)
	}
	if dst[0].Color.X() != 1 || dst[0].Color.Y() != 0 {
		t.Errorf("colour at birth: got %v, want red", dst[0].Color)
	}

	// Halfway: the ramps have moved.
	s.Live[0].Age = 0.5
	s.Fill(dst, mgl32.Vec3{}, false)
	if got := dst[0].Size.X(); math.Abs(float64(got-1.5)) > 1e-4 {
		t.Errorf("size at half life: got %v, want 1.5", got)
	}
	if got := dst[0].Color.W(); math.Abs(float64(got-0.5)) > 1e-4 {
		t.Errorf("alpha at half life: got %v, want 0.5", got)
	}
}

// TestFillSortsBackToFront — what the alpha blend mode needs and the other two
// do not.
func TestFillSortsBackToFront(t *testing.T) {
	l := NewLayer("l")
	l.Rate = 0
	l.LifeMin, l.LifeMax = 10, 10
	l.SizeMin, l.SizeMax = 1, 1
	l.SizeRamp = nil
	l.MaxParticles = 8
	s := NewSystem(l, 0, 43)
	s.Burst(3, mgl32.Ident4())
	s.Live[0].Pos = mgl32.Vec3{0, 0, -1}
	s.Live[1].Pos = mgl32.Vec3{0, 0, -9}
	s.Live[2].Pos = mgl32.Vec3{0, 0, -5}

	dst := make([]Instance, 8)
	n := s.Fill(dst, mgl32.Vec3{}, true)
	if n != 3 {
		t.Fatalf("Fill wrote %d, want 3", n)
	}
	for i := 1; i < n; i++ {
		if dst[i-1].Pos.LenSqr() < dst[i].Pos.LenSqr() {
			t.Fatalf("not sorted back to front: %v then %v", dst[i-1].Pos, dst[i].Pos)
		}
	}
}

// TestSheetCells covers the three frame modes.
func TestSheetCells(t *testing.T) {
	l := NewLayer("l")
	l.SheetCols, l.SheetRows = 4, 2
	l.FrameMode = FrameOverLife
	s := NewSystem(l, 0, 47)

	p := &Particle{Life: 1}
	u, v, du, dv := s.cell(p, 0)
	if du != 0.25 || dv != 0.5 || u != 0 || v != 0 {
		t.Errorf("over-life at t=0: got %v %v %v %v, want 0 0 0.25 0.5", u, v, du, dv)
	}
	// The last cell at t=1, not one past the end.
	u, v, _, _ = s.cell(p, 1)
	if u != 0.75 || v != 0.5 {
		t.Errorf("over-life at t=1: got u=%v v=%v, want 0.75 0.5", u, v)
	}

	s.Layer.FrameMode = FrameStatic
	if u, v, _, _ := s.cell(p, 0.7); u != 0 || v != 0 {
		t.Errorf("static should always be cell 0, got u=%v v=%v", u, v)
	}

	// No sheet is the whole texture.
	s.Layer.SheetCols = 0
	if u, v, du, dv := s.cell(p, 0.5); u != 0 || v != 0 || du != 1 || dv != 1 {
		t.Errorf("no sheet: got %v %v %v %v, want the whole texture", u, v, du, dv)
	}
}

// TestConeStaysWithinItsAngle — the shape most effects are built on.
func TestConeStaysWithinItsAngle(t *testing.T) {
	l := NewLayer("l")
	l.Rate = 0
	l.Shape = ShapeCone
	l.Angle = 20
	l.Radius = 0
	l.Spread = 0
	l.SpeedMin, l.SpeedMax = 1, 1
	l.LifeMin, l.LifeMax = 10, 10
	l.MaxParticles = 500
	s := NewSystem(l, 0, 53)
	s.Burst(400, mgl32.Ident4())

	up := mgl32.Vec3{0, 1, 0}
	limit := math.Cos(20 * math.Pi / 180)
	for i := range s.Live {
		d := s.Live[i].Vel.Normalize()
		if float64(d.Dot(up)) < limit-1e-4 {
			t.Fatalf("particle %d left a 20-degree cone: dir %v", i, d)
		}
	}
}

// TestLocalSpaceCarriesLiveParticles — the difference between a torch on a
// moving ship and the smoke trailing behind it.
func TestLocalSpaceCarriesLiveParticles(t *testing.T) {
	newRig := func(local bool) *System {
		l := NewLayer("l")
		l.Rate = 0
		l.LocalSpace = local
		// A point shape, so the particle is born exactly at the emitter rather
		// than somewhere inside the default sphere's radius — the test is about
		// what happens to it afterwards.
		l.Shape = ShapePoint
		l.Gravity = 0
		l.Drag = 0
		l.SpeedMin, l.SpeedMax = 0, 0
		l.LifeMin, l.LifeMax = 100, 100
		l.MaxParticles = 4
		s := NewSystem(l, 0, 59)
		s.Step(1.0/60.0, mgl32.Ident4())
		s.Burst(1, mgl32.Ident4())
		// Now move the emitter ten units along X and step again.
		s.Step(1.0/60.0, mgl32.Translate3D(10, 0, 0))
		return s
	}

	if x := newRig(true).Live[0].Pos.X(); math.Abs(float64(x-10)) > 1e-3 {
		t.Errorf("a local-space particle should have been carried to x=10, got %v", x)
	}
	if x := newRig(false).Live[0].Pos.X(); math.Abs(float64(x)) > 1e-3 {
		t.Errorf("a world-space particle should have been left at x=0, got %v", x)
	}
}
