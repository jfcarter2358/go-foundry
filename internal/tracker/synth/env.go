package synth

// env.go — envelopes as functions of time.
//
// Rendering is offline, so an envelope is just a curve evaluated at t. The
// shapes that feed a loop all arrive at their sustain level in finite time
// rather than approaching it exponentially forever: a loop that starts after
// the attack and decay then sees a constant, and a constant is periodic.

import "math"

// t60 is an exponential decay reaching -60 dB at T seconds.
func t60(t, T float64) float64 {
	if T <= 0 {
		return 0
	}
	return math.Exp(-6.907755 * t / T)
}

// smooth is the cubic smoothstep on [0, 1].
func smooth(x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	return x * x * (3 - 2*x)
}

// rise is a 0 -> 1 onset over a seconds that starts fast and lands softly —
// how a bow, breath or slow pad swells in.
func rise(t, a float64) float64 {
	if a <= 0 || t >= a {
		return 1
	}
	if t <= 0 {
		return 0
	}
	x := t / a
	return math.Sin(x * math.Pi / 2)
}

// ads is attack, decay to sustain s, then s exactly from t = a + d on. The
// decay is a cubic that leaves the peak quickly the way an exponential does.
func ads(t, a, d, s float64) float64 {
	if t < a {
		return rise(t, a)
	}
	if t < a+d && d > 0 {
		x := (t - a) / d
		return s + (1-s)*(1-x)*(1-x)*(1-x)
	}
	return s
}

// fadeIn is a raised-cosine onset over a seconds, for declicking.
func fadeIn(t, a float64) float64 {
	if a <= 0 || t >= a {
		return 1
	}
	return 0.5 - 0.5*math.Cos(math.Pi*t/a)
}

// gate is 1 until `at`, then falls to 0 over rel seconds.
func gate(t, at, rel float64) float64 {
	if t < at {
		return 1
	}
	if t >= at+rel {
		return 0
	}
	return 0.5 + 0.5*math.Cos(math.Pi*(t-at)/rel)
}

// sweep is an exponential glide from a to b with time constant tau: the shape
// of every drum's pitch drop.
func sweep(t, a, b, tau float64) float64 { return b + (a-b)*math.Exp(-t/tau) }

// expGlide moves geometrically from a to b over T seconds, then holds b —
// the shape of risers and lasers, which should sound even in pitch.
func expGlide(t, a, b, T float64) float64 {
	x := clamp(t/T, 0, 1)
	return a * math.Pow(b/a, x)
}
