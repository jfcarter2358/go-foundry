package flang

// random_test.go — the two random builtins.
//
// Testing a random number generator is testing the promises it makes rather
// than the numbers it produces: that draws land in the range that was asked
// for, that they keep the type they were asked in, that a pinned seed repeats
// and an unpinned one does not.

import "testing"

func TestRandomStaysInTheUnitInterval(t *testing.T) {
	in := compile(t, `def main() -> list
    out = []
    i = 0
    while i < 500
        out = append(out, random())
        i = i + 1
    end
    return out
end`, nil)
	v, err := in.Call("main", nil, Pos{})
	if err != nil {
		t.Fatal(err)
	}
	list, ok := v.(*ListVal)
	if !ok {
		t.Fatalf("got %s", TypeOf(v))
	}
	distinct := map[float64]bool{}
	for i, e := range list.Elems {
		f, ok := e.(FloatVal)
		if !ok {
			t.Fatalf("draw %d is a %s, want a float", i, TypeOf(e))
		}
		if f < 0 || f >= 1 {
			t.Fatalf("draw %d is %v, want [0, 1)", i, float64(f))
		}
		distinct[float64(f)] = true
	}
	// 500 draws from a 53-bit source landing on fewer than 400 values would
	// mean the generator is stuck or nearly so — the failure that a range check
	// on its own sails straight past.
	if len(distinct) < 400 {
		t.Errorf("500 draws produced only %d distinct values", len(distinct))
	}
}

func TestRandomRangeKeepsItsBoundsAndItsType(t *testing.T) {
	// Ints in, ints out, and both ends reachable: random_range(1, 2) that never
	// returns 2 is the off-by-one this is here to catch.
	in := compile(t, `def main() -> list
    out = []
    i = 0
    while i < 400
        out = append(out, random_range(1, 2))
        i = i + 1
    end
    return out
end`, nil)
	v, err := in.Call("main", nil, Pos{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]int{}
	for i, e := range v.(*ListVal).Elems {
		n, ok := e.(IntVal)
		if !ok {
			t.Fatalf("draw %d is a %s, want an int", i, TypeOf(e))
		}
		if n < 1 || n > 2 {
			t.Fatalf("draw %d is %v, want 1 or 2", i, int64(n))
		}
		seen[int64(n)]++
	}
	if seen[1] == 0 || seen[2] == 0 {
		t.Errorf("400 draws of random_range(1, 2) gave %v; both ends should come up", seen)
	}
}

func TestRandomRangeGoesFloatWhenEitherBoundDoes(t *testing.T) {
	for _, src := range []string{
		`def main() -> float
    return random_range(2.0, 3.0)
end`,
		// One float bound is enough, matching how arithmetic promotes.
		`def main() -> float
    return random_range(2, 3.0)
end`,
	} {
		v := run(t, src)
		f, ok := v.(FloatVal)
		if !ok {
			t.Fatalf("got a %s, want a float, from:\n%s", TypeOf(v), src)
		}
		if f < 2 || f > 3 {
			t.Errorf("got %v, want [2, 3]", float64(f))
		}
	}
}

// An inverted or empty range is an author who typed one number into both
// places. It returns that number rather than erroring, which is the choice the
// particle system's own range already makes.
func TestRandomRangeWithNoRangeReturnsTheBound(t *testing.T) {
	if v := run(t, `def main() -> int
    return random_range(7, 7)
end`); v.(IntVal) != 7 {
		t.Errorf("random_range(7, 7) = %v", v)
	}
	if v := run(t, `def main() -> int
    return random_range(9, 4)
end`); v.(IntVal) != 9 {
		t.Errorf("random_range(9, 4) = %v, want the low bound back", v)
	}
	if v := run(t, `def main() -> float
    return random_range(1.5, 0.5)
end`); v.(FloatVal) != 1.5 {
		t.Errorf("random_range(1.5, 0.5) = %v, want the low bound back", v)
	}
}

// The seed is the whole reason this is not math/rand: a run has to be
// repeatable when someone asks for it, and different when nobody does.
func TestSeedingRepeatsTheSequence(t *testing.T) {
	const src = `def main() -> list
    return [random(), random_range(0, 1000000), random()]
end`
	draw := func(seed uint64) string {
		in := compile(t, src, nil)
		if seed != 0 {
			in.SeedRandom(seed)
		}
		v, err := in.Call("main", nil, Pos{})
		if err != nil {
			t.Fatal(err)
		}
		return v.String()
	}

	if a, b := draw(12345), draw(12345); a != b {
		t.Errorf("the same seed gave two sequences:\n  %s\n  %s", a, b)
	}
	if a, b := draw(12345), draw(999); a == b {
		t.Errorf("two seeds gave the same sequence: %s", a)
	}
	// And unseeded interpreters differ, which is what makes a game vary between
	// runs. Two built back to back is the case a coarse clock would break.
	if a, b := draw(0), draw(0); a == b {
		t.Errorf("two unseeded interpreters drew identically: %s", a)
	}
}

func TestRandomRejectsArguments(t *testing.T) {
	for _, e := range []string{
		`random(3)`,
		`random_range(1)`,
		`random_range("a", "b")`,
	} {
		if err := runErr(t, expr(e), nil); err == nil {
			t.Errorf("no error from %s", e)
		}
	}
}
