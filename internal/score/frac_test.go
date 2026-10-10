package score

import (
	"errors"
	"math"
	"testing"
)

func TestFracArithmetic(t *testing.T) {
	third, half := F(1, 3), F(1, 2)
	cases := []struct {
		got, want Frac
	}{
		{third.Add(third).Add(third), Whole(1)},
		{third.Add(half), F(5, 6)},
		{half.Sub(third), F(1, 6)},
		{third.Mul(F(3, 4)), F(1, 4)},
		{half.Div(F(3, 2)), third},
		{F(6, -8), F(-3, 4)},
		{Frac{}, F(0, 5)},
		{Frac{}.Add(half), half},
	}
	for i, c := range cases {
		if !c.got.Eq(c.want) || (c.got.Den != 0 && c.got != c.want && c.got.Num != 0) {
			t.Errorf("case %d: got %v, want %v", i, c.got, c.want)
		}
	}
	if F(2, 4) != (Frac{1, 2}) {
		t.Error("F does not reduce")
	}
	if !third.Less(half) || half.Less(third) || third.Cmp(F(2, 6)) != 0 {
		t.Error("ordering")
	}
}

func TestFracRoundAndTicks(t *testing.T) {
	for _, c := range []struct {
		f    Frac
		want int64
	}{{F(5, 2), 3}, {F(7, 3), 2}, {F(8, 3), 3}, {F(-5, 2), -3}, {F(-7, 3), -2}, {F(1, 3), 0}, {Whole(4), 4}} {
		if got := c.f.Round(); got != c.want {
			t.Errorf("Round(%v) = %d, want %d", c.f, got, c.want)
		}
	}
	if F(1, 4).Ticks() != TicksPerQuarter {
		t.Error("a quarter is not 960 ticks")
	}
	// Every value down to a 256th, in triplets and quintuplets, is whole.
	for b := Breve; b <= D256; b++ {
		v := Duration{Base: b}.Value()
		for _, r := range []Frac{Whole(1), F(2, 3)} {
			if x := v.Mul(r).MulInt(TicksPerWhole); !x.IsInt() {
				t.Errorf("%v x %v is %v ticks", Duration{Base: b}, r, x)
			}
		}
	}
	if F(1, 3).Floor() != 0 || F(-1, 3).Floor() != -1 || F(7, 2).Floor() != 3 {
		t.Error("Floor")
	}
}

func TestFracOverflowPanicsAndRecovers(t *testing.T) {
	big := F(math.MaxInt64/2+1, 1)
	func() {
		defer func() {
			r := recover()
			if _, ok := r.(*OverflowError); !ok {
				t.Errorf("expected an OverflowError, got %v", r)
			}
		}()
		big.Add(big)
	}()
	var err error
	func() {
		defer RecoverOverflow(&err)
		F(1, math.MaxInt64).Add(F(1, math.MaxInt64-1))
	}()
	var oe *OverflowError
	if !errors.As(err, &oe) {
		t.Errorf("RecoverOverflow left %v", err)
	}
}

func TestParseFrac(t *testing.T) {
	for in, want := range map[string]Frac{
		"3/8": F(3, 8), "2": Whole(2), "-1/4": F(-1, 4), "2+1/3": F(7, 3), "0.75": F(3, 4), "6/8": F(3, 4), ".5": F(1, 2),
	} {
		got, err := ParseFrac(in)
		if err != nil || got != want {
			t.Errorf("ParseFrac(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "x", "1/0", "1/-2", "3/", "1+-1/2"} {
		if _, err := ParseFrac(bad); err == nil {
			t.Errorf("ParseFrac(%q) should fail", bad)
		}
	}
	for _, f := range []Frac{F(7, 3), F(1, 2), Whole(3), F(-5, 4)} {
		for _, s := range []string{f.String(), f.Mixed()} {
			if g, err := ParseFrac(s); err != nil || g != f {
				t.Errorf("%q does not read back as %v", s, f)
			}
		}
	}
}
