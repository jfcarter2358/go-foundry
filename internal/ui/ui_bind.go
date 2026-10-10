package ui

// ui_bind.go — where a widget's number or string comes from when it does not
// come from the author.
//
// A HUD is a *view* of state. Under a push model, every place that changes
// health — the collision resolver, a damage script, a pickup, the restore that
// runs when play ends — has to remember to update the bar, and the one that
// forgets produces a HUD that is wrong in a way no test catches. A pull
// expresses the relationship once, in the document, where the editor can show
// it and where there is exactly one place for it to be wrong.
//
// Formatting lives here rather than in the drawing layer because the layout
// solve measures the *formatted* string: a label that auto-sizes to "72%" is a
// different width from one that auto-sizes to "0.7200000286102295".

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ValueKind is which of a bound value's fields carries it.
type ValueKind int

const (
	ValueNone ValueKind = iota
	ValueNum
	ValueStr
	ValueBool
)

// Value is one binding's answer. A small closed set rather than an interface,
// matching the four scalars a level attribute can hold — which is the only
// thing a binding can ever be reading.
type Value struct {
	Kind ValueKind
	Num  float64
	Str  string
	Bool bool
}

// NumValue, StrValue and BoolValue are the three constructors a resolver needs.
func NumValue(v float64) Value { return Value{Kind: ValueNum, Num: v} }
func StrValue(v string) Value  { return Value{Kind: ValueStr, Str: v} }
func BoolValue(v bool) Value   { return Value{Kind: ValueBool, Bool: v} }

// AsNum coerces a value to a number: a bool is one or zero, a string is parsed
// if it looks numeric. Coercing rather than refusing because a bar bound to an
// attribute that happens to hold "3" should fill, not stay empty and say
// nothing about why.
func (v Value) AsNum() float64 {
	switch v.Kind {
	case ValueNum:
		return v.Num
	case ValueBool:
		if v.Bool {
			return 1
		}
	case ValueStr:
		if f, err := strconv.ParseFloat(v.Str, 64); err == nil {
			return f
		}
	}
	return 0
}

// AsStr is the value as it would be spelled with no format string.
func (v Value) AsStr() string {
	switch v.Kind {
	case ValueStr:
		return v.Str
	case ValueBool:
		return strconv.FormatBool(v.Bool)
	case ValueNum:
		return trimNum(v.Num)
	}
	return ""
}

// FormatBound spells a bound value the way a widget's format string asks.
//
// A format that does not match what the binding produced yields Go's own error
// text rather than an empty label, so a mistake in a format string is visible
// on screen, where it was made, instead of silently rendering nothing.
func FormatBound(format string, v Value) string {
	if format == "" {
		return v.AsStr()
	}
	if v.Kind == ValueNum || strings.ContainsAny(format, "feEgG") {
		return fmt.Sprintf(format, v.AsNum())
	}
	if v.Kind == ValueBool {
		return fmt.Sprintf(format, v.Bool)
	}
	return fmt.Sprintf(format, v.Str)
}

// trimNum spells a float without a tail of zeros: a health of 72 should read as
// "72", not "72.00".
func trimNum(f float64) string {
	s := strconv.FormatFloat(f, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// ---------- applying a resolver to a document ----------

// bindText sets a node's shown string from its binding, or clears the binding's
// hold on it when there is no resolver or no answer.
//
// Clearing rather than leaving the last value is what makes the editor's
// "literal" preview mode work, and what stops a HUD from freezing on the last
// number it saw when the thing it was watching is destroyed.
//
// The string is only re-formatted when the value or the format changed since
// the frame before. Every bound label went through fmt.Sprintf every frame —
// an allocation and a format parse apiece, for a HUD whose numbers mostly sit
// still.
func bindText(resolve func(string) (Value, bool), n *Node) {
	if n.Text.Bind == "" || resolve == nil {
		n.Text.bound, n.Text.hasBound = "", false
		n.Text.boundFrom = boundMemo{}
		return
	}
	v, ok := resolve(n.Text.Bind)
	if !ok {
		n.Text.bound, n.Text.hasBound = "", false
		n.Text.boundFrom = boundMemo{}
		return
	}
	if n.Text.hasBound && n.Text.boundFrom.same(n.Text.Format, v) {
		return
	}
	n.Text.bound, n.Text.hasBound = FormatBound(n.Text.Format, v), true
	n.Text.boundFrom = boundMemo{ok: true, format: n.Text.Format, v: v}
}

// boundMemo is the value and format a bound label's string was made from.
type boundMemo struct {
	ok     bool
	format string
	v      Value
}

// same reports whether formatting v with format would give back the string
// already made. A number is compared by its bits rather than with ==, which
// would call 0 and -0 the same value when "%.0f" spells them differently, and
// would never call NaN the same as itself — merely a missed reuse, but bits
// are no dearer.
func (m boundMemo) same(format string, v Value) bool {
	return m.ok && m.format == format && m.v.Kind == v.Kind &&
		math.Float64bits(m.v.Num) == math.Float64bits(v.Num) &&
		m.v.Str == v.Str && m.v.Bool == v.Bool
}

// bindValue does the same for a bar's or slider's number and its ceiling.
func bindValue(resolve func(string) (Value, bool), n *Node) {
	n.Value.bound, n.Value.hasBound = 0, false
	n.Value.boundMax, n.Value.hasBoundMax = 0, false
	if resolve == nil {
		return
	}
	if n.Value.Bind != "" {
		if v, ok := resolve(n.Value.Bind); ok {
			n.Value.bound, n.Value.hasBound = float32(v.AsNum()), true
		}
	}
	if n.Value.BindMax != "" {
		if v, ok := resolve(n.Value.BindMax); ok {
			n.Value.boundMax, n.Value.hasBoundMax = float32(v.AsNum()), true
		}
	}
}

// Bound reports whether a node's text is owned by a binding. A script writing
// to it is refused rather than silently overwritten on the next tick, because
// last-writer-wins between a script and a poll is decided by frame timing,
// which is not something anyone can debug.
func (n *Node) TextIsBound() bool { return n.Text.Bind != "" }

// ValueIsBound is TextIsBound for a bar's or slider's number.
func (n *Node) ValueIsBound() bool { return n.Value.Bind != "" }

// SetText writes a node's string, refusing when a binding owns it.
func (n *Node) SetText(s string) bool {
	if n.TextIsBound() {
		return false
	}
	n.Text.Value = s
	return true
}

// SetValue writes a node's number, refusing when a binding owns it.
func (n *Node) SetValue(v float32) bool {
	if n.ValueIsBound() {
		return false
	}
	n.Value.Value = v
	return true
}
