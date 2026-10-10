package ui

import (
	"fmt"
	"math"
	"testing"
)

// A bound label is formatted again only when its value or format changed, and
// is always what formatting the current value would give — including the
// values == would confuse, 0 against -0.
func TestBoundLabelsReformatOnlyOnChange(t *testing.T) {
	label := at("l", KindText, 0, 0, 200, 40)
	label.Text.Bind = "n"
	label.Text.Format = "%.0f"
	s := screenOf("s", label)
	v := NumValue(3)
	st := &Stack{Resolve: func(string) (Value, bool) { return v, true }}
	st.Push(s)

	cases := []struct {
		v      Value
		format string
	}{
		{NumValue(3), "%.0f"}, {NumValue(3), "%.0f"}, {NumValue(4), "%.0f"},
		{NumValue(0), "%.0f"}, {NumValue(math.Copysign(0, -1)), "%.0f"}, {NumValue(0), "%.0f"},
		{NumValue(4), "%.1f"}, {StrValue("x"), ""}, {StrValue("y"), ""},
		{BoolValue(true), ""}, {BoolValue(false), ""}, {NumValue(math.NaN()), "%.0f"},
	}
	for i, c := range cases {
		v = c.v
		s.Find("l").Text.Format = c.format
		st.Tick(0, 1000, 800)
		if got, want := s.Find("l").Text.Shown(), FormatBound(c.format, c.v); got != want {
			t.Errorf("case %d: label shows %q, want %q", i, got, want)
		}
	}
	// Unbinding and rebinding to the same value formats afresh.
	st.Resolve = nil
	st.Tick(0, 1000, 800)
	st.Resolve = func(string) (Value, bool) { return NumValue(5), true }
	st.Tick(0, 1000, 800)
	if got := s.Find("l").Text.Shown(); got != "5" {
		t.Errorf("after rebinding the label shows %q", got)
	}
}

// BenchmarkBoundLabels is one Stack.Tick over a screen of forty bound labels
// whose value does not change — a HUD at rest.
func BenchmarkBoundLabels(b *testing.B) {
	var nodes []Node
	for i := 0; i < 40; i++ {
		n := at(fmt.Sprintf("l%d", i), KindText, 0, float32(i*20), 200, 20)
		n.Text.Bind = "hp"
		n.Text.Format = "%.0f%%"
		nodes = append(nodes, n)
	}
	s := screenOf("s", nodes...)
	st := &Stack{Resolve: func(string) (Value, bool) { return NumValue(72), true }}
	st.Push(s)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.Tick(0, 1000, 800)
	}
}
