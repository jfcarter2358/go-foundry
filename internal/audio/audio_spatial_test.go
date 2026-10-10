package audio

// audio_spatial_test.go — the handedness, and the curves either side of it.
//
// The test this file exists for is TestBasisRightMatchesTheEnginesOwnSpelling.
// The engine's right vector is written inline in gfx.MoveCamera and nowhere
// else, and it is Up × Forward rather than Forward × Up — the engine is Y-up
// but left-handed about it. BasisFrom cannot import gfx to check itself
// against, so the formula is restated here. Get the sign backwards and every
// sound in the game plays out of the wrong ear, which no other test in this
// package would notice and which reads as correct in a diff.

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// engineRight is gfx.MoveCamera's inline spelling, copied deliberately. If
// that expression ever changes, this test is the thing that should fail.
func engineRight(dir mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{dir.Z(), 0, -dir.X()}
}

func directionFromAngles(yaw, pitch float64) mgl32.Vec3 {
	return mgl32.Vec3{
		float32(math.Cos(yaw) * math.Cos(pitch)),
		float32(math.Sin(pitch)),
		float32(math.Sin(yaw) * math.Cos(pitch)),
	}
}

func TestBasisRightMatchesTheEnginesOwnSpelling(t *testing.T) {
	for _, yaw := range []float64{0, 0.7, math.Pi / 2, 2.5, math.Pi, 4.2, 5.9} {
		dir := directionFromAngles(yaw, 0)
		got := BasisFrom(mgl32.Vec3{}, dir, mgl32.Vec3{0, 1, 0}).Right
		want := engineRight(dir).Normalize()
		if got.Sub(want).Len() > 1e-5 {
			t.Errorf("yaw %.2f: right is %v, the engine's own formula says %v", yaw, got, want)
		}
	}
}

func TestASourceOnTheListenersRightPansRight(t *testing.T) {
	// Facing +X, which is yaw 0. The engine's right is then {0,0,-1}, so a
	// source at -Z is to the listener's right.
	b := BasisFrom(mgl32.Vec3{}, mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 1, 0})

	l, r := PanGains(b, mgl32.Vec3{0, 0, -5}, 0)
	if r <= l {
		t.Errorf("a source to the right gave l=%.3f r=%.3f; it is coming out of the wrong ear", l, r)
	}

	l, r = PanGains(b, mgl32.Vec3{0, 0, 5}, 0)
	if l <= r {
		t.Errorf("a source to the left gave l=%.3f r=%.3f; it is coming out of the wrong ear", l, r)
	}
}

func TestPanIsConstantPowerAllTheWayRound(t *testing.T) {
	b := BasisFrom(mgl32.Vec3{}, mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 1, 0})
	for i := 0; i < 64; i++ {
		a := float64(i) / 64 * 2 * math.Pi
		p := mgl32.Vec3{float32(math.Cos(a)) * 3, 0, float32(math.Sin(a)) * 3}
		l, r := PanGains(b, p, 0)
		if power := l*l + r*r; math.Abs(float64(power)-1) > 1e-4 {
			t.Fatalf("at %.2f rad the pan carries power %.4f, not 1 — it will dip audibly as it crosses", a, power)
		}
	}
}

func TestAListenerAtTheSourcePositionIsCentredRatherThanNaN(t *testing.T) {
	b := BasisFrom(mgl32.Vec3{4, 4, 4}, mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 1, 0})
	l, r := PanGains(b, mgl32.Vec3{4, 4, 4}, 0)
	if math.IsNaN(float64(l)) || math.IsNaN(float64(r)) || math.Abs(float64(l-r)) > 1e-5 {
		t.Errorf("a source at the listener gave l=%.3f r=%.3f; one NaN here silences the whole device", l, r)
	}
}

func TestFullSpreadCentresASourceThatIsPlainlyOffToOneSide(t *testing.T) {
	b := BasisFrom(mgl32.Vec3{}, mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 1, 0})
	l, r := PanGains(b, mgl32.Vec3{0, 0, -5}, 1)
	if math.Abs(float64(l-r)) > 1e-5 {
		t.Errorf("spread 1 gave l=%.3f r=%.3f; it is meant to collapse the image to the centre", l, r)
	}
}

func TestATiltedListenerUpRotatesThePanAxis(t *testing.T) {
	// Rolled fully onto its side, riding a vehicle. What was to the right is
	// now overhead, and a pan that ignored Up would not notice.
	upright := BasisFrom(mgl32.Vec3{}, mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 1, 0})
	rolled := BasisFrom(mgl32.Vec3{}, mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 0, -1})

	if upright.Right.Sub(rolled.Right).Len() < 0.5 {
		t.Fatalf("rolling the listener left Right at %v; the basis is ignoring Up", rolled.Right)
	}

	src := mgl32.Vec3{0, 0, -5} // hard right when upright
	lu, ru := PanGains(upright, src, 0)
	lr, rr := PanGains(rolled, src, 0)
	if ru <= lu {
		t.Errorf("upright: expected the source on the right, got l=%.3f r=%.3f", lu, ru)
	}
	if math.Abs(float64(lr-rr)) > 1e-4 {
		t.Errorf("rolled onto its side the source is overhead and should be centred, got l=%.3f r=%.3f", lr, rr)
	}
}

func TestAttenuationIsFullInsideRefAndSilentBeyondMax(t *testing.T) {
	f := Falloff{Ref: 2, Max: 20, Rolloff: 1, Model: AttenInverse}
	for _, d := range []float32{0, 1, 2} {
		if g := Attenuation(d, f); g != 1 {
			t.Errorf("at %.1f (inside ref) gain is %.3f, want 1", d, g)
		}
	}
	for _, d := range []float32{20, 25, 1000} {
		if g := Attenuation(d, f); g != 0 {
			t.Errorf("at %.1f (past max) gain is %.3f, want 0", d, g)
		}
	}
}

func TestEveryAttenuationModelFallsMonotonicallyAndReachesZero(t *testing.T) {
	for _, m := range []AttenModel{AttenInverse, AttenLinear, AttenExponential, "nonsense"} {
		f := Falloff{Ref: 1, Max: 30, Rolloff: 1, Model: m}
		prev := float32(math.Inf(1))
		for i := 0; i <= 300; i++ {
			d := float32(i) / 10
			g := Attenuation(d, f)
			if math.IsNaN(float64(g)) || g < 0 || g > 1 {
				t.Fatalf("%s at %.1f gave %v, which is outside [0,1]", m, d, g)
			}
			if g > prev+1e-6 {
				t.Fatalf("%s rose from %.4f to %.4f at %.1f; the curve must not climb", m, prev, g, d)
			}
			prev = g
		}
		// The fade band exists so the curve does not click off at the cutoff.
		if g := Attenuation(29.9, f); g > 0.05 {
			t.Errorf("%s is still at %.3f just inside max; it will click when it cuts", m, g)
		}
	}
}

func TestAZeroConeIsOmnidirectional(t *testing.T) {
	b := BasisFrom(mgl32.Vec3{0, 0, 10}, mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 1, 0})
	if g := ConeGain(b, mgl32.Vec3{}, mgl32.Vec3{1, 0, 0}, Cone{}); g != 1 {
		t.Errorf("an unset cone attenuated to %.3f; a sound that never sets one must be heard everywhere", g)
	}
}

func TestAConeIsLoudestInFrontAndQuietestBehind(t *testing.T) {
	// Total angles: full gain within 22.5 deg of the axis, outer gain only
	// directly behind. A side sample must land strictly between the two, so
	// the outer cone has to be wider than a right angle.
	c := Cone{Inner: math.Pi / 4, Outer: 2 * math.Pi, OuterGain: 0.2}
	src := mgl32.Vec3{}
	dir := mgl32.Vec3{1, 0, 0}

	inFront := ConeGain(BasisFrom(mgl32.Vec3{10, 0, 0}, mgl32.Vec3{-1, 0, 0}, mgl32.Vec3{0, 1, 0}), src, dir, c)
	toSide := ConeGain(BasisFrom(mgl32.Vec3{0, 0, 10}, mgl32.Vec3{0, 0, -1}, mgl32.Vec3{0, 1, 0}), src, dir, c)
	behind := ConeGain(BasisFrom(mgl32.Vec3{-10, 0, 0}, mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 1, 0}), src, dir, c)

	if inFront != 1 {
		t.Errorf("inside the inner cone gain is %.3f, want 1", inFront)
	}
	if !(behind < toSide && toSide < inFront) {
		t.Errorf("cone is not monotonic front-to-back: front %.3f side %.3f behind %.3f", inFront, toSide, behind)
	}
	if math.Abs(float64(behind-0.2)) > 1e-5 {
		t.Errorf("outside the outer cone gain is %.3f, want the outer gain 0.2", behind)
	}
}

func TestDopplerRisesOnApproachAndFallsOnRetreat(t *testing.T) {
	listener := mgl32.Vec3{}
	src := mgl32.Vec3{10, 0, 0}

	toward := DopplerRatio(mgl32.Vec3{}, src, mgl32.Vec3{-20, 0, 0}, listener, 343, 1)
	away := DopplerRatio(mgl32.Vec3{}, src, mgl32.Vec3{20, 0, 0}, listener, 343, 1)
	still := DopplerRatio(mgl32.Vec3{}, src, mgl32.Vec3{}, listener, 343, 1)

	if still != 1 {
		t.Errorf("a stationary pair shifted by %.4f; it must not", still)
	}
	if toward <= 1 {
		t.Errorf("a source closing at 20u/s gave ratio %.4f; approach must raise the pitch", toward)
	}
	if away >= 1 {
		t.Errorf("a source receding at 20u/s gave ratio %.4f; retreat must lower the pitch", away)
	}
}

func TestDopplerSurvivesSupersonicNonsense(t *testing.T) {
	// Gameplay velocities here are not physical — a vehicle clipping through
	// geometry can produce anything. The formula divides by (c - vs), so this
	// is the direct route to +Inf and then to a negative playback rate.
	for _, v := range []float32{343, 400, 100000, -100000} {
		r := DopplerRatio(mgl32.Vec3{}, mgl32.Vec3{10, 0, 0}, mgl32.Vec3{v, 0, 0}, mgl32.Vec3{}, 343, 1)
		if math.IsNaN(float64(r)) || math.IsInf(float64(r), 0) || r < 0.5 || r > 2 {
			t.Errorf("source velocity %.0f gave ratio %v, which is outside the clamp", v, r)
		}
	}
}

func TestADegenerateBasisFallsBackRatherThanProducingNaNs(t *testing.T) {
	for _, b := range []ListenerBasis{
		BasisFrom(mgl32.Vec3{}, mgl32.Vec3{}, mgl32.Vec3{}),
		BasisFrom(mgl32.Vec3{}, mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, 1, 0}), // looking along up
	} {
		for _, v := range []mgl32.Vec3{b.Forward, b.Up, b.Right} {
			for i := 0; i < 3; i++ {
				if math.IsNaN(float64(v[i])) {
					t.Fatalf("degenerate input produced %v in the basis", v)
				}
			}
		}
		if l, r := PanGains(b, mgl32.Vec3{1, 2, 3}, 0); math.IsNaN(float64(l)) || math.IsNaN(float64(r)) {
			t.Fatalf("degenerate basis panned to NaN")
		}
	}
}
