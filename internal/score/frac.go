package score

// frac.go — exact musical time.
//
// Every position and duration in a score is a Frac of a whole note: a
// quarter is 1/4, a triplet eighth 1/12, a 256th inside a 7:4 tuplet
// 1/448. Floats would drift — three triplet eighths do not add up to a
// quarter in binary — and an editor that compares "does this bar add up"
// or "is this note on the beat" needs the answer to be exact. Playback
// converts to ticks (960 a quarter) and seconds only at the very end, from
// the exact value, so nothing accumulates.
//
// A Frac is always reduced with a positive denominator; the zero value is
// zero. Arithmetic cancels before it multiplies and checks every product
// and sum: a result that would not fit in int64 panics with an
// *OverflowError, which the entry points that read files and interpret
// scores (ParseVoice, Parse, interpret.Interpret) recover into an ordinary
// error. A real score never gets near the limit; a hostile or corrupt one
// cannot crash the editor.

import (
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"
)

// Frac is an exact rational number: Num/Den, reduced, Den > 0. The zero
// value (0/0) reads as 0.
type Frac struct {
	Num, Den int64
}

// OverflowError is what Frac arithmetic panics with when a result does not
// fit in int64.
type OverflowError struct{ Op string }

func (e *OverflowError) Error() string { return "score: time arithmetic overflows (" + e.Op + ")" }

// F returns num/den reduced. It panics on a zero denominator.
func F(num, den int64) Frac {
	if den == 0 {
		panic("score: zero denominator")
	}
	return reduce(num, den)
}

// Whole returns n as a Frac.
func Whole(n int64) Frac { return Frac{n, 1} }

func reduce(num, den int64) Frac {
	if den < 0 {
		if num == math.MinInt64 || den == math.MinInt64 {
			panic(&OverflowError{"negate"})
		}
		num, den = -num, -den
	}
	if num == 0 {
		return Frac{0, 1}
	}
	g := gcd(abs64(num), den)
	return Frac{num / g, den / g}
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func abs64(v int64) int64 {
	if v < 0 {
		if v == math.MinInt64 {
			panic(&OverflowError{"abs"})
		}
		return -v
	}
	return v
}

// mul is a*b, checked.
func mul(a, b int64, op string) int64 {
	neg := (a < 0) != (b < 0)
	hi, lo := bits.Mul64(uint64(abs64(a)), uint64(abs64(b)))
	if hi != 0 || lo > math.MaxInt64 {
		panic(&OverflowError{op})
	}
	if neg {
		return -int64(lo)
	}
	return int64(lo)
}

// add is a+b, checked.
func add(a, b int64, op string) int64 {
	s := a + b
	if (a > 0 && b > 0 && s < 0) || (a < 0 && b < 0 && s >= 0) {
		panic(&OverflowError{op})
	}
	return s
}

// norm turns the zero value into 0/1.
func (f Frac) norm() Frac {
	if f.Den == 0 {
		return Frac{0, 1}
	}
	return f
}

// Add returns f + g.
func (f Frac) Add(g Frac) Frac {
	f, g = f.norm(), g.norm()
	if f.Den == g.Den {
		return reduce(add(f.Num, g.Num, "add"), f.Den)
	}
	d := gcd(f.Den, g.Den)
	den := mul(f.Den/d, g.Den, "add")
	num := add(mul(f.Num, g.Den/d, "add"), mul(g.Num, f.Den/d, "add"), "add")
	return reduce(num, den)
}

// Sub returns f - g.
func (f Frac) Sub(g Frac) Frac { return f.Add(g.Neg()) }

// Neg returns -f.
func (f Frac) Neg() Frac {
	f = f.norm()
	if f.Num == math.MinInt64 {
		panic(&OverflowError{"negate"})
	}
	return Frac{-f.Num, f.Den}
}

// Mul returns f * g.
func (f Frac) Mul(g Frac) Frac {
	f, g = f.norm(), g.norm()
	if f.Num == 0 || g.Num == 0 {
		return Frac{0, 1}
	}
	// Cancel across before multiplying: (a/b)(c/d) with gcd(a,d), gcd(c,b).
	g1 := gcd(abs64(f.Num), g.Den)
	g2 := gcd(abs64(g.Num), f.Den)
	num := mul(f.Num/g1, g.Num/g2, "mul")
	den := mul(f.Den/g2, g.Den/g1, "mul")
	return reduce(num, den)
}

// MulInt returns f * n.
func (f Frac) MulInt(n int64) Frac { return f.Mul(Frac{n, 1}) }

// Div returns f / g. It panics when g is zero.
func (f Frac) Div(g Frac) Frac {
	g = g.norm()
	if g.Num == 0 {
		panic("score: division by zero")
	}
	return f.Mul(reduce(g.Den, g.Num))
}

// Cmp returns -1, 0 or 1 as f is less than, equal to or greater than g.
func (f Frac) Cmp(g Frac) int {
	d := f.Sub(g)
	switch {
	case d.Num < 0:
		return -1
	case d.Num > 0:
		return 1
	}
	return 0
}

// Less reports f < g.
func (f Frac) Less(g Frac) bool { return f.Cmp(g) < 0 }

// Eq reports f == g (the zero value equals 0/1).
func (f Frac) Eq(g Frac) bool { f, g = f.norm(), g.norm(); return f == g }

// IsZero reports f == 0.
func (f Frac) IsZero() bool { return f.Num == 0 }

// Sign is -1, 0 or 1.
func (f Frac) Sign() int {
	switch {
	case f.Num < 0:
		return -1
	case f.Num > 0:
		return 1
	}
	return 0
}

// Float is f as a float64 (for display and the last step of playback).
func (f Frac) Float() float64 {
	f = f.norm()
	return float64(f.Num) / float64(f.Den)
}

// Floor is the largest integer not above f.
func (f Frac) Floor() int64 {
	f = f.norm()
	q := f.Num / f.Den
	if f.Num%f.Den != 0 && f.Num < 0 {
		q--
	}
	return q
}

// Round is f rounded to the nearest integer, halves away from zero.
func (f Frac) Round() int64 {
	f = f.norm()
	twice := Frac{mul(f.Num, 2, "round"), f.Den}.norm()
	q := twice.Num / twice.Den
	r := twice.Num % twice.Den
	// q is 2f truncated; round(f) = (q + sign) / 2 when q is odd.
	if r != 0 || q%2 != 0 {
		if f.Num >= 0 {
			return (q + 1) / 2
		}
		return (q - 1) / 2
	}
	return q / 2
}

// IsInt reports whether f is a whole number.
func (f Frac) IsInt() bool { return f.norm().Den == 1 }

// Min and Max return the smaller and larger.
func MinFrac(a, b Frac) Frac {
	if b.Less(a) {
		return b
	}
	return a
}

func MaxFrac(a, b Frac) Frac {
	if a.Less(b) {
		return b
	}
	return a
}

// String writes "3/8", "2", "0", "-1/4".
func (f Frac) String() string {
	f = f.norm()
	if f.Den == 1 {
		return strconv.FormatInt(f.Num, 10)
	}
	return strconv.FormatInt(f.Num, 10) + "/" + strconv.FormatInt(f.Den, 10)
}

// Mixed writes a positive fraction as whole and part: "2+1/3", "1/2", "3".
func (f Frac) Mixed() string {
	f = f.norm()
	if f.Num < 0 || f.Den == 1 {
		return f.String()
	}
	w := f.Num / f.Den
	if w == 0 {
		return f.String()
	}
	return strconv.FormatInt(w, 10) + "+" + Frac{f.Num - w*f.Den, f.Den}.String()
}

// ParseFrac reads what String and Mixed write — "3/8", "2", "-1/4",
// "2+1/3" — and a plain decimal ("0.75", exact). Overflow is an error.
func ParseFrac(s string) (f Frac, err error) {
	defer func() {
		if r := recover(); r != nil {
			if oe, ok := r.(*OverflowError); ok {
				err = fmt.Errorf("%q: %v", s, oe)
				return
			}
			panic(r)
		}
	}()
	s = strings.TrimSpace(s)
	if s == "" {
		return Frac{}, fmt.Errorf("empty fraction")
	}
	if w, part, ok := strings.Cut(s, "+"); ok && w != "" {
		a, err := ParseFrac(w)
		if err != nil {
			return Frac{}, err
		}
		b, err := ParseFrac(part)
		if err != nil {
			return Frac{}, err
		}
		if a.Sign() < 0 || b.Sign() < 0 {
			return Frac{}, fmt.Errorf("bad fraction %q", s)
		}
		return a.Add(b), nil
	}
	if n, d, ok := strings.Cut(s, "/"); ok {
		num, err1 := strconv.ParseInt(n, 10, 64)
		den, err2 := strconv.ParseInt(d, 10, 64)
		if err1 != nil || err2 != nil || den <= 0 {
			return Frac{}, fmt.Errorf("bad fraction %q", s)
		}
		return reduce(num, den), nil
	}
	if i, d, ok := strings.Cut(s, "."); ok {
		neg := strings.HasPrefix(i, "-")
		i = strings.TrimPrefix(i, "-")
		if i == "" {
			i = "0"
		}
		if d == "" || len(d) > 18 {
			return Frac{}, fmt.Errorf("bad number %q", s)
		}
		whole, err1 := strconv.ParseInt(i, 10, 64)
		part, err2 := strconv.ParseInt(d, 10, 64)
		if err1 != nil || err2 != nil || part < 0 {
			return Frac{}, fmt.Errorf("bad number %q", s)
		}
		den := int64(1)
		for range d {
			den *= 10
		}
		f := Whole(whole).Add(reduce(part, den))
		if neg {
			f = f.Neg()
		}
		return f, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return Frac{}, fmt.Errorf("bad fraction %q", s)
	}
	return Frac{n, 1}, nil
}

// RecoverOverflow turns an OverflowError panic into *err; any other panic
// carries on. Entry points use it as `defer score.RecoverOverflow(&err)`.
func RecoverOverflow(err *error) {
	if r := recover(); r != nil {
		if oe, ok := r.(*OverflowError); ok {
			*err = oe
			return
		}
		panic(r)
	}
}

// TicksPerQuarter is playback's resolution: 960 a quarter note, so every
// duration down to a 256th, and triplets and quintuplets of it, is whole.
const TicksPerQuarter = 960

// TicksPerWhole is TicksPerQuarter for a whole note.
const TicksPerWhole = 4 * TicksPerQuarter

// Ticks is f (in whole notes) in ticks, rounded to the nearest.
func (f Frac) Ticks() int64 { return f.MulInt(TicksPerWhole).Round() }

// TicksFloat is f in ticks without rounding, for seconds.
func (f Frac) TicksFloat() float64 {
	f = f.norm()
	return float64(f.Num) / float64(f.Den) * TicksPerWhole
}
