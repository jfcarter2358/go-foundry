package audio

// audio_spatial.go — where a sound is, turned into what each ear hears.
//
// Everything in this file is a pure function of plain vectors and scalars. No
// device, no package state, no SDL. That is deliberate and it is the whole
// reason the mixer was written in Go rather than handed to SDL_mixer or
// OpenAL: this is where every real bug in positional audio lives, and a bug
// here is inaudible in a code review and obvious in a test.
//
// The one thing worth reading twice is ListenerBasis.Right. See BasisFrom.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// ListenerBasis is where the listener is and which way it is facing, in the
// engine's own handedness.
type ListenerBasis struct {
	Position mgl32.Vec3
	Forward  mgl32.Vec3
	Up       mgl32.Vec3
	Right    mgl32.Vec3
}

// BasisFrom builds the listener basis from a camera pose.
//
// Right is Up × Forward, which reads backwards and is correct. The engine's
// right vector is spelled inline in gfx.MoveCamera as
//
//	right := mgl32.Vec3{Cam.Direction.Z(), 0, -Cam.Direction.X()}
//
// At yaw 0 the direction is {1,0,0} and that gives {0,0,-1} — which is
// Up × Forward, not Forward × Up. The engine is Y-up but left-handed about it,
// so taking the textbook cross product here would put every sound on the wrong
// side of the listener, and it would look perfectly reasonable on the page.
// Going through the cross product rather than copying the inline spelling is
// what generalises it to a tilted Up, which is what a listener riding a
// vehicle has.
//
// A zero or degenerate Forward/Up falls back to a world-aligned basis rather
// than producing NaNs that would reach the mixer and silence the device.
func BasisFrom(pos, forward, up mgl32.Vec3) ListenerBasis {
	f := safeNormalize(forward, mgl32.Vec3{1, 0, 0})
	u := safeNormalize(up, mgl32.Vec3{0, 1, 0})

	r := u.Cross(f)
	if r.Len() < 1e-6 {
		// Looking straight along Up. Any perpendicular will do; the pan is
		// degenerate here anyway because the source is directly overhead.
		r = mgl32.Vec3{0, 0, -1}
	}
	r = r.Normalize()

	// Re-orthogonalise Up against the plane the other two settled on, so a
	// caller that hands in a slightly off-axis Up does not skew the pan.
	u = f.Cross(r).Normalize()

	return ListenerBasis{Position: pos, Forward: f, Up: u, Right: r}
}

func safeNormalize(v, fallback mgl32.Vec3) mgl32.Vec3 {
	if l := v.Len(); l > 1e-6 {
		return v.Mul(1 / l)
	}
	return fallback
}

// ---------- distance attenuation ----------

// AttenModel names how gain falls off with distance. It is a string rather
// than an iota because it is written into level files, and a reordered enum
// must not be able to silently reinterpret an old one — the same rule
// particle_file.go states for its own name tables.
type AttenModel string

const (
	AttenInverse     AttenModel = "inverse"
	AttenLinear      AttenModel = "linear"
	AttenExponential AttenModel = "exponential"
)

// Falloff is one source's distance response.
type Falloff struct {
	// Ref is the distance within which the sound plays at full gain.
	Ref float32
	// Max is the distance at which it reaches silence. Beyond it the source
	// is not merely quiet but skipped, which is what keeps a level full of
	// ambience from costing anything to mix.
	Max float32
	// Rolloff scales how fast the curve falls between the two. 1 is the
	// model's own shape.
	Rolloff float32
	// Model selects the curve. An unrecognised name reads as AttenInverse
	// rather than as silence, so a hand-edited level with a typo is audible
	// and therefore fixable.
	Model AttenModel
}

// DefaultFalloff is what a freshly placed sound gets: audible across a room,
// gone across a level.
func DefaultFalloff() Falloff {
	return Falloff{Ref: 1, Max: 40, Rolloff: 1, Model: AttenInverse}
}

// Attenuation returns the gain multiplier for a source at dist, in [0,1].
//
// Every branch clamps before dividing. An inverse curve at zero distance is
// the obvious way to produce +Inf, and one NaN reaching the mix buffer takes
// the whole device silent rather than just the one voice.
func Attenuation(dist float32, f Falloff) float32 {
	ref := f.Ref
	if ref < 1e-4 {
		ref = 1e-4
	}
	max := f.Max
	if max < ref {
		max = ref
	}
	roll := f.Rolloff
	if roll < 0 {
		roll = 0
	}

	if dist <= ref {
		return 1
	}
	if dist >= max {
		return 0
	}

	var g float32
	switch f.Model {
	case AttenLinear:
		g = 1 - roll*(dist-ref)/(max-ref)
	case AttenExponential:
		g = float32(math.Pow(float64(dist/ref), float64(-roll)))
	default: // AttenInverse, and anything unrecognised
		g = ref / (ref + roll*(dist-ref))
	}

	// Fade the last stretch to zero regardless of model, so a curve that is
	// still audible at Max does not click off at the cutoff.
	const fadeBand = 0.15
	if t := (max - dist) / ((max - ref) * fadeBand); t < 1 {
		g *= t
	}

	return clamp01(g)
}

// ---------- panning ----------

// PanGains splits a source's gain between the two ears.
//
// Constant power, not constant amplitude: the pair is cos/sin of one angle, so
// l²+r² is 1 wherever the source sits. Summing to 1 linearly instead would dip
// audibly as a source crosses the centre, which is the classic way a pan
// sounds wrong without anyone being able to say why.
//
// spread in [0,1] pulls the image toward the centre — 0 is fully positional,
// 1 is dead centre regardless of direction. A source at the listener's own
// position is centred rather than being panned by whatever rounding produced
// its near-zero offset.
func PanGains(b ListenerBasis, srcPos mgl32.Vec3, spread float32) (l, r float32) {
	const centred = 0.70710678 // 1/sqrt(2), both ears equal at constant power

	d := srcPos.Sub(b.Position)
	if d.Len() < 1e-5 {
		return centred, centred
	}
	d = d.Normalize()

	// Lateral position in [-1,1]: -1 hard left, +1 hard right.
	x := d.Dot(b.Right)
	x *= 1 - clamp01(spread)

	// Map [-1,1] onto a quarter turn so the ends are hard-panned.
	theta := (float64(clampUnit(x)) + 1) * (math.Pi / 4)
	return float32(math.Cos(theta)), float32(math.Sin(theta))
}

// ---------- directionality ----------

// Cone is a source that is louder in the direction it faces. A zero Cone is
// omnidirectional, which is what a sound that never sets one gets.
type Cone struct {
	// Inner and Outer are total cone angles in radians. Full gain inside
	// Inner, OuterGain outside Outer, interpolated between.
	Inner, Outer float32
	OuterGain    float32
}

// IsZero reports an omnidirectional source, so the caller can skip the work.
func (c Cone) IsZero() bool { return c.Inner == 0 && c.Outer == 0 }

// ConeGain returns the directional multiplier for a source at srcPos facing
// srcDir. Angles are total cone widths, matching how they are authored, so the
// half-angle conversion happens here rather than in every caller.
func ConeGain(b ListenerBasis, srcPos, srcDir mgl32.Vec3, c Cone) float32 {
	if c.IsZero() {
		return 1
	}
	toListener := b.Position.Sub(srcPos)
	if toListener.Len() < 1e-5 {
		return 1 // inside the source; a cone has no meaning here
	}
	fwd := safeNormalize(srcDir, mgl32.Vec3{1, 0, 0})

	cosA := fwd.Dot(toListener.Normalize())
	angle := float32(math.Acos(float64(clampUnit(cosA))))

	inner, outer := c.Inner/2, c.Outer/2
	if outer < inner {
		inner, outer = outer, inner
	}
	switch {
	case angle <= inner:
		return 1
	case angle >= outer || outer-inner < 1e-6:
		return clamp01(c.OuterGain)
	default:
		t := (angle - inner) / (outer - inner)
		return clamp01(1 + t*(clamp01(c.OuterGain)-1))
	}
}

// ---------- doppler ----------

// DopplerRatio returns the playback-rate multiplier for a moving source and
// listener. Velocities are world units per second.
//
// The result is clamped hard. An approach speed at or past the speed of sound
// is a division by zero and then a negative rate, and gameplay velocities in
// this engine are not physical enough to be worth honouring at the limit — a
// vehicle clipping through a wall should not shriek.
func DopplerRatio(listenerVel, srcPos, srcVel, listenerPos mgl32.Vec3, speedOfSound, factor float32) float32 {
	if factor <= 0 || speedOfSound <= 1e-3 {
		return 1
	}
	d := listenerPos.Sub(srcPos)
	if d.Len() < 1e-5 {
		return 1
	}
	dir := d.Normalize()

	// Closing rates along the listener/source axis.
	vl := listenerVel.Dot(dir) * factor
	vs := srcVel.Dot(dir) * factor

	const limit = 0.9 // never let either term reach the speed of sound
	maxV := speedOfSound * limit
	vl = clampAbs(vl, maxV)
	vs = clampAbs(vs, maxV)

	ratio := (speedOfSound - vl) / (speedOfSound - vs)
	if ratio < 0.5 {
		ratio = 0.5
	}
	if ratio > 2 {
		ratio = 2
	}
	return ratio
}

// ---------- small helpers ----------

func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampUnit(v float32) float32 {
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampAbs(v, m float32) float32 {
	if v > m {
		return m
	}
	if v < -m {
		return -m
	}
	return v
}
