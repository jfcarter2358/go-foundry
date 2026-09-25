package gfx

// ease.go — the shapes a blend can take over its time.
//
// The world's tweens (world/tween.go) are straight lines, which is right for a
// door: it should arrive at the rate it set off at. A camera is different. A
// cut that starts and stops dead reads as a mistake, so every camera blend
// takes a curve, and a script names it.

import "strings"

// Ease is one easing curve.
type Ease int

const (
	// EaseInOut starts gently and settles gently — what a camera move almost
	// always wants, and so the default.
	EaseInOut Ease = iota
	EaseLinear
	EaseIn
	EaseOut
)

// easeNames are the spellings a script uses, in the order the reference
// lists them.
var easeNames = []struct {
	name string
	e    Ease
}{
	{"ease_in_out", EaseInOut},
	{"linear", EaseLinear},
	{"ease_in", EaseIn},
	{"ease_out", EaseOut},
}

// ParseEase reads a curve's name, with or without the colons an atom is
// written with.
func ParseEase(name string) (Ease, bool) {
	name = strings.Trim(name, ":")
	for _, n := range easeNames {
		if n.name == name {
			return n.e, true
		}
	}
	return EaseInOut, false
}

// EaseNames are every curve's name, for an error message and the reference.
func EaseNames() []string {
	out := make([]string, len(easeNames))
	for i, n := range easeNames {
		out[i] = n.name
	}
	return out
}

// Apply maps a linear fraction of the blend (0–1) onto the curve.
func (e Ease) Apply(t float32) float32 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	switch e {
	case EaseLinear:
		return t
	case EaseIn:
		return t * t * t
	case EaseOut:
		u := 1 - t
		return 1 - u*u*u
	}
	// Cubic in-out: steeper through the middle than smoothstep, so a long move
	// does not spend most of its time looking like it has barely started.
	if t < 0.5 {
		return 4 * t * t * t
	}
	u := -2*t + 2
	return 1 - u*u*u/2
}
