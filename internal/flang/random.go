package flang

// random.go — the interpreter's random number source.
//
// Deliberately not math/rand, for the reason particle_sim.go gives for its own
// xorshift: a sequence has to be reproducible from a seed *across builds*, and
// math/rand's mapping from seed to sequence is not something Go promises to
// keep. A generator written out here is.
//
// The state lives on the Interp rather than in a package-level source, so two
// interpreters — a play session and a test, or a level and the one loaded after
// it — do not draw from each other. It is xorshift64*, which is four
// instructions and has a period long enough that a script pulling one number a
// frame would need rather longer than the heat death of the universe to wrap.
//
// # Seeding, and why it is not fixed
//
// NewInterp seeds from the clock, so a game varies from run to run: loot that
// is the same every playthrough is not random in the sense anyone means it.
// SeedRandom pins it, which is the other half — a replay, a test asserting an
// exact roll, or a bug report that has to reproduce. Both directions are
// wanted; only the default has to be chosen, and "varies" is the one that
// surprises nobody.
//
// # Guards
//
// SCRIPTING.md says a clause guard may call only pure builtins. `random` is a
// builtin and is not pure, and nothing in the interpreter enforces the rule —
// guards are evaluated with the ordinary expression evaluator. A guard that
// rolls a die therefore *works*, and picks a different clause on each call
// including the ones the dispatcher makes while merely looking for a match.
// That is a bug in the script rather than in the language, and it is called out
// in SCRIPTING.md beside the rule it breaks.

import (
	"sync/atomic"
	"time"
)

// randomSeedCounter makes clock-seeded interpreters differ even when several
// are built inside one clock tick, which on a coarse timer is every interpreter
// a test file makes.
var randomSeedCounter atomic.Uint64

// TestSeeds, when set, replaces the clock as the source of fresh seeds: a test
// that needs two runs of a level to roll the same dice (harness's
// sim_digest_test.go) hands out seeds from a counter. Nil in every build that
// is not such a test.
var TestSeeds func() uint64

// newRandomSeed returns a starting state that is neither zero nor equal to the
// last one handed out. Zero is the one state xorshift cannot leave.
func newRandomSeed() uint64 {
	if TestSeeds != nil {
		if n := TestSeeds(); n != 0 {
			return n
		}
	}
	n := uint64(time.Now().UnixNano()) ^ (randomSeedCounter.Add(1) * 0x9E3779B97F4A7C15)
	if n == 0 {
		n = 0x2545F4914F6CDD1D
	}
	return n
}

// SeedRandom pins this interpreter's random sequence, so the same script run
// twice from the same seed makes the same choices. Seeding with 0 is treated as
// re-seeding from the clock rather than as a state xorshift would never leave.
func (in *Interp) SeedRandom(seed uint64) {
	if seed == 0 {
		in.rng = newRandomSeed()
		return
	}
	in.rng = seed
}

// nextRandom advances the state and returns it. xorshift64*.
func (in *Interp) nextRandom() uint64 {
	if in.rng == 0 {
		in.rng = newRandomSeed()
	}
	x := in.rng
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	in.rng = x
	return x * 2685821657736338717
}

// randomUnit returns a float in [0, 1).
//
// Built from the top 53 bits, which is every bit a float64 can hold without
// rounding two different draws onto the same value.
func (in *Interp) randomUnit() float64 {
	return float64(in.nextRandom()>>11) / float64(uint64(1)<<53)
}

// randomIntBetween returns an int in [lo, hi], both included.
//
// The span is computed in uint64 so that a range spanning the whole int64 line
// — random_range(-9e18, 9e18) — does not overflow the way hi-lo+1 would.
func (in *Interp) randomIntBetween(lo, hi int64) int64 {
	span := uint64(hi-lo) + 1
	if span == 0 {
		// The full int64 range: every draw is in it already.
		return int64(in.nextRandom())
	}
	return lo + int64(in.nextRandom()%span)
}

// RandomUnit is randomUnit for a host verb: a draw in [0, 1) from the
// interpreter whose script is running.
//
// Exported because some engine verbs are themselves random — a shot that
// scatters inside a cone — and a draw made from a separate source would be
// outside everything SeedRandom is for. A replay that pins the seed has to pin
// the bullets too, so those verbs draw from here rather than from a generator
// of their own.
func (in *Interp) RandomUnit() float64 { return in.randomUnit() }
