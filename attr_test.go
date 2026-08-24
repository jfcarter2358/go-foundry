package main

import "testing"

// Attributes are the engine's only user-extensible data, so what matters here
// is that a value survives YAML unchanged and that a type is never silently
// swapped for another — a script reading `health` as an int must not find a
// float there because a level was resaved.

func TestAttrRoundTripPreservesTypeAndValue(t *testing.T) {
	cases := map[string]AttrValue{
		"health":  IntAttr(25),
		"speed":   FloatAttr(12.5),
		"locked":  BoolAttr(true),
		"name":    StringAttr("Front Door"),
		"zeroInt": IntAttr(0),
		// The zero of each type is the interesting case: an `any`-based
		// encoding cannot tell these apart once YAML has been through it.
		"zeroFloat": FloatAttr(0),
		"zeroBool":  BoolAttr(false),
		"zeroStr":   StringAttr(""),
	}

	raw := attrsToRaw(cases)
	got, err := attrsFromRaw(raw)
	if err != nil {
		t.Fatalf("attrsFromRaw: %v", err)
	}
	if len(got) != len(cases) {
		t.Fatalf("got %d attributes, want %d", len(got), len(cases))
	}
	for name, want := range cases {
		if got[name] != want {
			t.Errorf("%s: got %+v, want %+v", name, got[name], want)
		}
	}
}

func TestAttrTypeInferredWhenAbsent(t *testing.T) {
	// Hand-authored YAML is allowed to leave the type off; yaml.v2 hands us the
	// Go type it decoded, which is what the inference reads.
	cases := []struct {
		name string
		in   any
		want AttrType
	}{
		{"int", 7, AttrInt},
		{"float", 1.5, AttrFloat},
		{"bool", true, AttrBool},
		{"str", "hello", AttrString},
	}
	for _, c := range cases {
		got, err := attrFromRaw(c.name, RawAttribute{Value: c.in})
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got.Type != c.want {
			t.Errorf("%s: inferred %s, want %s", c.name, got.Type, c.want)
		}
	}
}

func TestAttrDeclaredTypeWinsOverWrittenForm(t *testing.T) {
	// `{type: float, value: 40}` is what an author writes; YAML has no way to
	// say 40.0. Refusing it would be pedantry, so it widens.
	got, err := attrFromRaw("top_speed", RawAttribute{Type: "float", Value: 40})
	if err != nil {
		t.Fatalf("attrFromRaw: %v", err)
	}
	if got.Type != AttrFloat {
		t.Fatalf("got type %s, want float", got.Type)
	}
	if f, _ := got.Float(); f != 40 {
		t.Errorf("got %v, want 40", f)
	}
}

func TestAttrRejectsUnknownTypeAndBadValue(t *testing.T) {
	if _, err := attrFromRaw("x", RawAttribute{Type: "vector", Value: 1}); err == nil {
		t.Error("unknown type accepted")
	}
	if _, err := attrFromRaw("x", RawAttribute{Type: "int", Value: "not a number"}); err == nil {
		t.Error("unparseable int accepted")
	}
	// A whole attribute block fails the load rather than dropping the bad one,
	// so a typo surfaces at load time instead of as a missing attribute later.
	if _, err := attrsFromRaw(map[string]RawAttribute{
		"good": {Type: "int", Value: 1},
		"bad":  {Type: "int", Value: "x"},
	}); err == nil {
		t.Error("a block with one bad attribute loaded anyway")
	}
}

func TestAttrNumericCoercion(t *testing.T) {
	if v, ok := IntAttr(3).Float(); !ok || v != 3 {
		t.Errorf("int did not widen to float: %v %v", v, ok)
	}
	if v, ok := FloatAttr(3.9).Int(); !ok || v != 3 {
		t.Errorf("float did not truncate to int: %v %v", v, ok)
	}
	// Numbers deliberately do not coerce to bool — flang has no "0 is false".
	if _, ok := IntAttr(1).Bool(); ok {
		t.Error("int coerced to bool")
	}
	if _, ok := StringAttr("5").Int(); ok {
		t.Error("string coerced to int on read")
	}
}

func TestConvertAttrCarriesValueAcross(t *testing.T) {
	// Retyping in the editor must not throw away a number the user just typed.
	if got := convertAttr(IntAttr(25), AttrFloat); got != FloatAttr(25) {
		t.Errorf("int→float: got %+v", got)
	}
	if got := convertAttr(FloatAttr(2.7), AttrInt); got != IntAttr(2) {
		t.Errorf("float→int: got %+v", got)
	}
	if got := convertAttr(IntAttr(25), AttrString); got != StringAttr("25") {
		t.Errorf("int→str: got %+v", got)
	}
	if got := convertAttr(StringAttr("true"), AttrBool); got != BoolAttr(true) {
		t.Errorf("str→bool: got %+v", got)
	}
	// Nothing to carry across: falls back to the target's zero rather than
	// keeping a value of the wrong type.
	if got := convertAttr(StringAttr("nonsense"), AttrInt); got != IntAttr(0) {
		t.Errorf("str→int of a non-number: got %+v", got)
	}
}

func TestMergeAttrsOverridesByName(t *testing.T) {
	base := AttrSet{"health": IntAttr(25), "loot": IntAttr(10)}
	over := AttrSet{"health": IntAttr(50), "unique": BoolAttr(true)}

	got := mergeAttrs(base, over)
	if v := got["health"]; v != IntAttr(50) {
		t.Errorf("health: got %+v, want the override", v)
	}
	if v := got["loot"]; v != IntAttr(10) {
		t.Errorf("loot: got %+v, want the definition's", v)
	}
	if v := got["unique"]; v != BoolAttr(true) {
		t.Errorf("unique: got %+v, want the override's own", v)
	}
	// The definition must not be edited by the instances made from it.
	if base["health"] != IntAttr(25) {
		t.Error("mergeAttrs mutated the base set")
	}
}

func TestAttrNamesAreSorted(t *testing.T) {
	// Every walk of a set goes through this; Go randomises map iteration, which
	// would otherwise churn the saved file between runs.
	s := AttrSet{"zeta": IntAttr(1), "alpha": IntAttr(2), "mid": IntAttr(3)}
	got := s.AttrNames()
	want := []string{"alpha", "mid", "zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
