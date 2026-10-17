// Package noise is the procedural generators' deterministic noise and random
// number library: integer hashing, a seeded RNG, value and gradient noise in
// two and three dimensions, fractal sums of them (FBM, ridged, billow), domain
// warping, and cellular (Worley/Voronoi) noise with cell IDs.
//
// Everything here is a pure function of its inputs and its seed. That is the
// whole contract a generator document leans on — "same .fgen, same bytes out"
// — so nothing in this package reads a clock, a global RNG or the map
// iteration order, and nothing allocates per sample.
//
// It is deliberately its own package rather than a reuse of
// world/terrain_noise.go: internal/procgen sits below internal/world (the
// layering test holds it there), and a generator's noise must not change when
// someone tunes the block terrain's. The two share their ideas — an integer
// hash per lattice point, a quintic fade — and nothing else.
//
// Precision is float64 throughout. A heightfield a kilometre across sampled at
// half a metre wants more mantissa than float32 has once a domain warp has
// added its offsets, and float64 costs nothing measurable here.
//
// Determinism caveat, stated once: Go may fuse a*b+c into one FMA instruction
// on arm64, ppc64 and s390x (never on amd64), which can change the last bit of
// a result. A document run on two architectures may therefore differ in its
// low bits; on one architecture it never does.
package noise

// rng.go — the seeded random number generator and the integer hashes.
//
// SplitMix64 for the hash (one multiply-xorshift chain per input word; it is
// what seeds xoshiro in the reference implementation, and it avalanches every
// input bit into every output bit) and xoshiro256** for the stream. Both are
// tiny, fast and have no state outside the struct, so a stage can hold one per
// worker goroutine and get the same numbers however the work is scheduled —
// provided each worker's RNG is seeded from the work item, not from the worker.
// RNG.Fork is the tool for that.

import "math"

// Mix64 is SplitMix64's finaliser: a bijective scramble of one 64-bit word.
func Mix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}

// Hash64 combines any number of words into one well-mixed word. Order
// matters: Hash64(a, b) != Hash64(b, a).
func Hash64(words ...uint64) uint64 {
	h := uint64(0x6A09E667F3BCC909)
	for _, w := range words {
		h = Mix64(h ^ w)
	}
	return h
}

// HashString folds a string into a word (FNV-1a, then mixed), for seeding
// from a name such as a stage ID.
func HashString(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return Mix64(h)
}

// hash2 and hash3 are the lattice hashes: a seed and integer coordinates to a
// word. Coordinates are sign-extended, so -1 and the top of the range differ.
func hash2(seed uint64, x, y int64) uint64 {
	return Mix64(seed ^ Mix64(uint64(x)^Mix64(uint64(y)+0x632BE59BD9B4E019)))
}

func hash3(seed uint64, x, y, z int64) uint64 {
	return Mix64(seed ^ Mix64(uint64(x)^Mix64(uint64(y)^Mix64(uint64(z)+0x8CB92BA72F3D8DD7))))
}

// unit maps a word to [0, 1) using its top 53 bits — every float64 in the
// range equally likely, and exactly representable.
func unit(h uint64) float64 { return float64(h>>11) * (1.0 / (1 << 53)) }

// RNG is a seeded xoshiro256** stream. The zero value is not useful; make one
// with NewRNG.
type RNG struct {
	s [4]uint64
}

// NewRNG seeds a stream. Any seed is fine, zero included: the state is filled
// through SplitMix64, so it is never all zeros.
func NewRNG(seed uint64) *RNG {
	r := &RNG{}
	r.Seed(seed)
	return r
}

// Seed restarts the stream from seed.
func (r *RNG) Seed(seed uint64) {
	x := seed
	for i := range r.s {
		x += 0x9E3779B97F4A7C15
		z := x
		z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
		z = (z ^ (z >> 27)) * 0x94D049BB133111EB
		r.s[i] = z ^ (z >> 31)
	}
}

func rotl(x uint64, k uint) uint64 { return (x << k) | (x >> (64 - k)) }

// Uint64 is the next word of the stream.
func (r *RNG) Uint64() uint64 {
	s := &r.s
	out := rotl(s[1]*5, 7) * 9
	t := s[1] << 17
	s[2] ^= s[0]
	s[3] ^= s[1]
	s[1] ^= s[2]
	s[0] ^= s[3]
	s[2] ^= t
	s[3] = rotl(s[3], 45)
	return out
}

// Float64 is uniform in [0, 1).
func (r *RNG) Float64() float64 { return unit(r.Uint64()) }

// Range is uniform in [lo, hi).
func (r *RNG) Range(lo, hi float64) float64 { return lo + (hi-lo)*r.Float64() }

// Intn is uniform in [0, n). n <= 0 returns 0 rather than panicking: a
// generator asked for "0 variants" should place nothing, not crash the run.
func (r *RNG) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	// Lemire's multiply-shift with rejection: unbiased, one division at most.
	un := uint64(n)
	for {
		x := r.Uint64()
		hi, lo := mul64(x, un)
		if lo >= un || lo >= (-un)%un {
			return int(hi)
		}
	}
}

// mul64 is the 128-bit product of a and b as (high, low).
func mul64(a, b uint64) (hi, lo uint64) {
	const mask32 = 1<<32 - 1
	a0, a1 := a&mask32, a>>32
	b0, b1 := b&mask32, b>>32
	w0 := a0 * b0
	t := a1*b0 + w0>>32
	w1 := t&mask32 + a0*b1
	hi = a1*b1 + t>>32 + w1>>32
	lo = a * b
	return
}

// Bool is a fair coin.
func (r *RNG) Bool() bool { return r.Uint64()>>63 == 1 }

// Chance is true with probability p.
func (r *RNG) Chance(p float64) bool { return r.Float64() < p }

// Normal is a standard normal sample (Box-Muller, one of the pair; the other
// is discarded so the stream advances by exactly two words per call — a
// cached second sample would make the sequence depend on call history).
func (r *RNG) Normal() float64 {
	u1 := r.Float64()
	for u1 <= 1e-300 {
		u1 = r.Float64()
	}
	u2 := r.Float64()
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

// Shuffle permutes n items in place through swap (Fisher-Yates).
func (r *RNG) Shuffle(n int, swap func(i, j int)) {
	for i := n - 1; i > 0; i-- {
		swap(i, r.Intn(i+1))
	}
}

// Fork is a new, independent stream derived from this one's seed material and
// key — not from its position. Forking the same RNG with the same key always
// gives the same stream, however many numbers have been drawn from either, so
// work item k of a parallel loop can seed itself with Fork(k) and be
// scheduling-proof.
func (r *RNG) Fork(key uint64) *RNG {
	return NewRNG(Hash64(r.s[0], r.s[1], r.s[2], r.s[3], key))
}
