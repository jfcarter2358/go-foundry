package score

// meter.go — time signatures.
//
// A meter is a list of beat groups over a denominator: 4/4 is [4]/4, 6/8 is
// [6]/8, and an additive 3+2+2/8 is [3 2 2]/8. The groups are what beaming
// and the conductor's beats follow; the bar's length is their sum over the
// denominator. "C" and "C|" are common and cut time: 4/4 and 2/2 with their
// own symbol.

import (
	"fmt"
	"strconv"
	"strings"
)

// Meter is a time signature.
type Meter struct {
	// Groups are the numerator's terms: [4] for 4/4, [3 2 2] for 3+2+2/8.
	Groups []int
	// Den is the denominator, a power of two.
	Den int
	// Symbol is "common" (C) or "cut" (C|), or "" for numbers.
	Symbol string
}

// FourFour is the meter a score without one is in.
func FourFour() Meter { return Meter{Groups: []int{4}, Den: 4} }

// Num is the numerator: the groups' sum.
func (m Meter) Num() int {
	n := 0
	for _, g := range m.Groups {
		n += g
	}
	return n
}

// Length is a full bar in whole notes.
func (m Meter) Length() Frac {
	if m.Den <= 0 {
		return Whole(1)
	}
	return F(int64(m.Num()), int64(m.Den))
}

// Compound reports a single-group meter counted in dotted beats: 6/8,
// 9/8, 12/16 — a numerator that is a multiple of three above three over
// a denominator of 8 or more.
func (m Meter) Compound() bool {
	return len(m.Groups) == 1 && m.Den >= 8 && m.Groups[0] > 3 && m.Groups[0]%3 == 0
}

// Beats are the bar's beats in whole notes, in order: four 1/4 for 4/4,
// two 3/8 for 6/8, 3/8 2/8 2/8 for 3+2+2/8.
func (m Meter) Beats() []Frac {
	if m.Den <= 0 || len(m.Groups) == 0 {
		return []Frac{F(1, 4), F(1, 4), F(1, 4), F(1, 4)}
	}
	unit := F(1, int64(m.Den))
	var out []Frac
	switch {
	case len(m.Groups) > 1:
		for _, g := range m.Groups {
			out = append(out, unit.MulInt(int64(g)))
		}
	case m.Compound():
		for i := 0; i < m.Groups[0]/3; i++ {
			out = append(out, unit.MulInt(3))
		}
	default:
		for i := 0; i < m.Groups[0]; i++ {
			out = append(out, unit)
		}
	}
	return out
}

// Eq reports the same meter, symbol included.
func (m Meter) Eq(o Meter) bool {
	if m.Den != o.Den || m.Symbol != o.Symbol || len(m.Groups) != len(o.Groups) {
		return false
	}
	for i := range m.Groups {
		if m.Groups[i] != o.Groups[i] {
			return false
		}
	}
	return true
}

// String writes "4/4", "3+2+2/8", "C", "C|".
func (m Meter) String() string {
	switch m.Symbol {
	case "common":
		return "C"
	case "cut":
		return "C|"
	}
	parts := make([]string, len(m.Groups))
	for i, g := range m.Groups {
		parts[i] = strconv.Itoa(g)
	}
	return strings.Join(parts, "+") + "/" + strconv.Itoa(m.Den)
}

// ParseMeter reads what String writes.
func ParseMeter(s string) (Meter, error) {
	t := strings.ReplaceAll(strings.TrimSpace(s), " ", "")
	switch t {
	case "C", "c":
		return Meter{Groups: []int{4}, Den: 4, Symbol: "common"}, nil
	case "C|", "c|":
		return Meter{Groups: []int{2}, Den: 2, Symbol: "cut"}, nil
	}
	num, den, ok := strings.Cut(t, "/")
	if !ok {
		return Meter{}, fmt.Errorf("bad time signature %q (want e.g. 4/4, 6/8, 3+2+2/8, C)", s)
	}
	d, err := strconv.Atoi(den)
	if err != nil || d < 1 || d > 128 || d&(d-1) != 0 {
		return Meter{}, fmt.Errorf("time signature %q: the denominator must be a power of two up to 128", s)
	}
	m := Meter{Den: d}
	for _, g := range strings.Split(num, "+") {
		n, err := strconv.Atoi(g)
		if err != nil || n < 1 || n > 64 {
			return Meter{}, fmt.Errorf("time signature %q: bad numerator %q", s, g)
		}
		m.Groups = append(m.Groups, n)
	}
	return m, nil
}
