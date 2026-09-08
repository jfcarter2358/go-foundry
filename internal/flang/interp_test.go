package flang

// interp_test.go — the evaluator, against MapHost.
//
// Everything here runs with no engine, no window and no level. That is the
// point of the Host boundary: if a behaviour cannot be tested from here, an
// engine concept has leaked into the language.

import (
	"strings"
	"testing"
)

// run compiles src and calls `main` with no arguments, returning its value.
func run(t *testing.T, src string) Value {
	t.Helper()
	v, _ := runWithHost(t, src, nil)
	return v
}

// runWithHost compiles src, calls `main`, and returns the result and the host.
func runWithHost(t *testing.T, src string, host Host) (Value, *Interp) {
	t.Helper()
	in := compile(t, src, host)
	v, err := in.Call("main", nil, Pos{File: "test.flang"})
	if err != nil {
		t.Fatalf("running:\n%s\n\n%v", src, err)
	}
	return v, in
}

// compile parses src and builds an interpreter over it.
func compile(t *testing.T, src string, host Host) *Interp {
	t.Helper()
	f, errs := Parse("test.flang", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors in:\n%s\n\n%v", src, errs)
	}
	prog := NewProgram()
	if perrs := prog.Add(f); len(perrs) > 0 {
		t.Fatalf("link errors: %v", perrs)
	}
	in, ierrs := NewInterp(prog, host, Limits{})
	if len(ierrs) > 0 {
		t.Fatalf("clause errors: %v", ierrs)
	}
	return in
}

// runErr compiles src, calls `main`, and returns the error it produced.
func runErr(t *testing.T, src string, host Host) error {
	t.Helper()
	in := compile(t, src, host)
	_, err := in.Call("main", nil, Pos{File: "test.flang"})
	if err == nil {
		t.Fatalf("expected an error from:\n%s", src)
	}
	return err
}

// expr wraps an expression in a `main` that returns it.
func expr(e string) string { return "def main() -> null\n    return " + e + "\nend" }

// ---------- arithmetic and types ----------

func TestArithmeticKeepsIntsInt(t *testing.T) {
	cases := []struct {
		src  string
		want Value
	}{
		{"1 + 2", IntVal(3)},
		{"7 / 2", IntVal(3)}, // integer division truncates
		{"7 % 3", IntVal(1)},
		{"7.0 / 2.0", FloatVal(3.5)},
		{"1 + 2.0", FloatVal(3)}, // promotion on a mixed operand
		{"2 * 3 + 1", IntVal(7)},
		{"-5", IntVal(-5)},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			if got := run(t, expr(tc.src)); !Equal(got, tc.want) {
				t.Errorf("got %v (%s), want %v (%s)", got, typeName(got), tc.want, typeName(tc.want))
			}
			// int-ness is not just about the value: 7/2 giving 3.0 would be a
			// different type with the same printed digits.
			if got := run(t, expr(tc.src)); got.Type() != tc.want.Type() {
				t.Errorf("got type %s, want %s", got.Type(), tc.want.Type())
			}
		})
	}
}

func TestStringAndListConcatenation(t *testing.T) {
	if got := run(t, expr(`"foo" + "bar"`)); !Equal(got, StrVal("foobar")) {
		t.Errorf(`"foo" + "bar" = %v`, got)
	}
	got := run(t, expr(`[1, 2] + [3]`))
	list, ok := got.(*ListVal)
	if !ok || len(list.Elems) != 3 {
		t.Fatalf("list concatenation gave %v", got)
	}
}

func TestVectorOperations(t *testing.T) {
	cases := []struct {
		src  string
		want Value
	}{
		{"<1.0, 2.0, 3.0> + <1.0, 1.0, 1.0>", VecVal{2, 3, 4}},
		{"<1.0, 2.0, 3.0> - <1.0, 1.0, 1.0>", VecVal{0, 1, 2}},
		{"<1.0, 2.0, 3.0> * 2.0", VecVal{2, 4, 6}},
		{"2.0 * <1.0, 2.0, 3.0>", VecVal{2, 4, 6}},
		{"dot(<1.0, 0.0, 0.0>, <1.0, 0.0, 0.0>)", FloatVal(1)},
		{"length(<3.0, 4.0, 0.0>)", FloatVal(5)},
		{"normalize(<0.0, 0.0, 0.0>)", VecVal{}}, // no direction, not NaN
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			if got := run(t, expr(tc.src)); !Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVectorComponents(t *testing.T) {
	cases := []struct {
		src  string
		want Value
	}{
		{"<1.0, 2.0, 3.0>.x", FloatVal(1)},
		{"<1.0, 2.0, 3.0>.y", FloatVal(2)},
		{"<1.0, 2.0, 3.0>.z", FloatVal(3)},
		// A component is a float whatever built the vec, so arithmetic on one
		// does not silently become integer division.
		{"(<1.0, 2.0, 3.0> + <1.0, 1.0, 1.0>).y", FloatVal(3)},
		{"normalize(<0.0, 5.0, 0.0>).y", FloatVal(1)},
		// Rebuilding from components is the spelling that stands in for
		// assignment, so it has to read cleanly.
		{"<<1.0, 2.0, 3.0>.x, 9.0, <1.0, 2.0, 3.0>.z>", VecVal{1, 9, 3}},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			got := run(t, expr(tc.src))
			if !Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
			if got.Type() != tc.want.Type() {
				t.Errorf("got type %s, want %s", got.Type(), tc.want.Type())
			}
		})
	}
}

func TestVectorComponentMistakesSayWhatToWrite(t *testing.T) {
	// The three ways to get this wrong, each pointed at the one spelling that
	// works. A vec is a value, so nothing in it can be written to.
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			"a component that does not exist",
			expr("<1.0, 2.0, 3.0>.w"),
			`components x, y and z, not "w"`,
		},
		{
			"indexing instead of naming",
			expr("<1.0, 2.0, 3.0>[0]"),
			"write v.x, v.y or v.z",
		},
		{
			"assigning a component",
			"def main() -> null\n    v = <1.0, 2.0, 3.0>\n    v.y = 9.0\nend",
			"a vec is a value",
		},
		{
			"assigning through an index",
			"def main() -> null\n    v = <1.0, 2.0, 3.0>\n    v[1] = 9.0\nend",
			"build a new one",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := runErr(t, tc.src, nil); !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q\n  does not mention %q", err, tc.want)
			}
		})
	}
}

func TestOnlyFalseAndNullAreFalsey(t *testing.T) {
	// A language where `if count` silently means "is it non-zero" is one where
	// forgetting a comparison is invisible.
	cases := []struct {
		src  string
		want bool
	}{
		{"false", false},
		{"null", false},
		{"true", true},
		{"0", true},
		{"0.0", true},
		{`""`, true},
		{"[]", true},
	}
	for _, tc := range cases {
		src := "def main() -> bool\n    if " + tc.src + "\n        return true\n    end\n    return false\nend"
		if got := run(t, src); !Equal(got, BoolVal(tc.want)) {
			t.Errorf("if %s: got %v, want %v", tc.src, got, tc.want)
		}
	}
}

func TestEqualityAcrossTypes(t *testing.T) {
	cases := []struct {
		src  string
		want bool
	}{
		{"1 == 1.0", true},  // the numeric tower
		{`1 == "1"`, false}, // different types are unequal, not an error
		{"null == null", true},
		{"[1, 2] == [1, 2]", true},
		{`{"a": 1} == {"a": 1}`, true},
		{":on: == :on:", true},
		{":on: == :off:", false},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			if got := run(t, expr(tc.src)); !Equal(got, BoolVal(tc.want)) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOrderingMismatchedTypesIsAnError(t *testing.T) {
	err := runErr(t, expr(`1 < "a"`), nil)
	if !strings.Contains(err.Error(), "order") {
		t.Errorf("diagnostic does not explain the problem: %v", err)
	}
}

func TestShortCircuitEvaluation(t *testing.T) {
	// The right side must not run: `undefined_name` would error if it did.
	if got := run(t, expr("false and undefined_name")); !Equal(got, BoolVal(false)) {
		t.Errorf("and did not short-circuit: %v", got)
	}
	if got := run(t, expr("true or undefined_name")); !Equal(got, BoolVal(true)) {
		t.Errorf("or did not short-circuit: %v", got)
	}
}

func TestDivisionByZero(t *testing.T) {
	for _, src := range []string{"1 / 0", "1 % 0", "1.0 / 0.0"} {
		err := runErr(t, expr(src), nil)
		if !strings.Contains(err.Error(), "zero") {
			t.Errorf("%s: %v", src, err)
		}
	}
}

// ---------- control flow ----------

func TestControlFlowExecutes(t *testing.T) {
	got := run(t, `def main() -> int
    total = 0
    for x in [1, 2, 3, 4, 5]
        if x == 3
            continue
        end
        if x == 5
            break
        end
        total += x
    end
    return total
end`)
	// 1 + 2 + 4, with 3 skipped and 5 breaking out.
	if !Equal(got, IntVal(7)) {
		t.Errorf("got %v, want 7", got)
	}
}

func TestWhileLoop(t *testing.T) {
	got := run(t, `def main() -> int
    n = 0
    while n < 10
        n += 1
    end
    return n
end`)
	if !Equal(got, IntVal(10)) {
		t.Errorf("got %v, want 10", got)
	}
}

func TestVariablesAreFunctionScopedNotBlockScoped(t *testing.T) {
	// flang has no `let`, and a block does not introduce a scope — so a name
	// first assigned inside an `if` is readable after it.
	got := run(t, `def main() -> int
    if true
        x = 42
    end
    return x
end`)
	if !Equal(got, IntVal(42)) {
		t.Errorf("got %v, want 42", got)
	}
}

func TestForIteratesDictKeysInOrder(t *testing.T) {
	got := run(t, `def main() -> str
    out = ""
    for k in {"b": 1, "a": 2, "c": 3}
        out += k
    end
    return out
end`)
	// Sorted, so iterating twice gives the same answer twice — Go's map order
	// would not.
	if !Equal(got, StrVal("abc")) {
		t.Errorf("got %v, want abc", got)
	}
}

// ---------- dispatch ----------

func TestFibonacciDispatchesByStrictness(t *testing.T) {
	// The spec's own worked example. `fib(0)` must beat `fib(n int)`.
	src := `def fib(n int) when n > 1 -> int
    return fib(n - 1) + fib(n - 2)
fib(1)
    return 1
fib(0)
    return 0
end
def main() -> int
    return fib(10)
end`
	if got := run(t, src); !Equal(got, IntVal(55)) {
		t.Errorf("fib(10) = %v, want 55", got)
	}
}

func TestGuardedClauseBeatsUnguardedOne(t *testing.T) {
	// Same parameter shape, so only the guard separates them. If the unguarded
	// clause were tried first it would swallow every call.
	src := `def sign(n int) when n < 0 -> str
    return "negative"
sign(n int)
    return "not negative"
end
def main() -> str
    return sign(-5)
end`
	if got := run(t, src); !Equal(got, StrVal("negative")) {
		t.Errorf("got %v, want negative", got)
	}
}

func TestTypedParameterDoesNotMatchOtherTypes(t *testing.T) {
	src := `def describe(n int) -> str
    return "int"
describe(s str)
    return "str"
end
def main() -> str
    return describe("hello")
end`
	if got := run(t, src); !Equal(got, StrVal("str")) {
		t.Errorf("got %v, want str", got)
	}
}

func TestIntDoesNotMatchFloatParameter(t *testing.T) {
	// Arithmetic promotes freely; dispatch does not. Two clauses distinguished
	// only by int-versus-float would otherwise both match every number.
	src := `def f(n float) -> str
    return "float"
end
def main() -> str
    return f(1)
end`
	err := runErr(t, src, nil)
	if !strings.Contains(err.Error(), "no clause") {
		t.Errorf("an int matched a float parameter: %v", err)
	}
}

func TestNoMatchingClauseIsAnError(t *testing.T) {
	src := `def f(0) -> int
    return 1
end
def main() -> int
    return f(5)
end`
	err := runErr(t, src, nil)
	if !strings.Contains(err.Error(), "no clause") {
		t.Errorf("diagnostic does not explain the problem: %v", err)
	}
}

func TestUnreachableClauseIsReported(t *testing.T) {
	// Two clauses with the same shape and no guard match the same calls, so the
	// second could never run. That is a bug worth seeing at load time.
	f, errs := Parse("t.flang", []byte(`def f(a int) -> int
    return 1
f(b int)
    return 2
end`))
	if len(errs) > 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	prog := NewProgram()
	prog.Add(f)
	_, ierrs := NewInterp(prog, nil, Limits{})
	if len(ierrs) == 0 {
		t.Fatal("a shadowed clause produced no diagnostic")
	}
	if !strings.Contains(ierrs[0].Msg, "never run") {
		t.Errorf("diagnostic does not explain the problem: %q", ierrs[0].Msg)
	}
}

// ---------- limits ----------

func TestCallDepthLimitStopsRunawayRecursion(t *testing.T) {
	// A Go stack overflow is fatal and cannot be recovered, so the interpreter
	// has to count its own frames or a bad script takes the engine down.
	src := `def loop(n int) -> int
    return loop(n + 1)
end
def main() -> int
    return loop(0)
end`
	in := compile(t, src, nil)
	_, err := in.Call("main", nil, Pos{})
	if err == nil {
		t.Fatal("runaway recursion was not stopped")
	}
	if !strings.Contains(err.Error(), "depth") {
		t.Errorf("diagnostic does not explain the problem: %v", err)
	}
}

func TestStepLimitStopsEndlessLoop(t *testing.T) {
	src := `def main() -> null
    while true
        x = 1
    end
end`
	f, _ := Parse("t.flang", []byte(src))
	prog := NewProgram()
	prog.Add(f)
	in, _ := NewInterp(prog, nil, Limits{MaxSteps: 1000})
	_, err := in.Call("main", nil, Pos{})
	if err == nil {
		t.Fatal("an endless loop was not stopped")
	}
	if !strings.Contains(err.Error(), "step limit") {
		t.Errorf("diagnostic does not explain the problem: %v", err)
	}
}

// ---------- builtins ----------

func TestPureBuiltins(t *testing.T) {
	cases := []struct {
		src  string
		want Value
	}{
		{"abs(-3)", IntVal(3)}, // int in, int out
		{"abs(-3.5)", FloatVal(3.5)},
		{"floor(3.7)", IntVal(3)}, // rounding yields an int
		{"ceil(3.2)", IntVal(4)},
		{"sqrt(9.0)", FloatVal(3)},
		{"min(3, 1, 2)", IntVal(1)},
		{"max(3, 1, 2)", IntVal(3)},
		{"clamp(15, 0, 10)", IntVal(10)},
		{`upper("abc")`, StrVal("ABC")},
		{`trim("  x  ")`, StrVal("x")},
		{`len("hello")`, IntVal(5)},
		{"len([1, 2, 3])", IntVal(3)},
		{`contains("hello", "ell")`, BoolVal(true)},
		{`starts_with("hello", "he")`, BoolVal(true)},
		{`join(["a", "b"], "-")`, StrVal("a-b")},
		{`format("{} of {}", 3, "five")`, StrVal("3 of five")},
		{`int("42")`, IntVal(42)},
		{`str(42)`, StrVal("42")},
		{"type(1)", AtomVal("int")},
		{`get({"a": 1}, "b", 9)`, IntVal(9)},
		{`has({"a": 1}, "a")`, BoolVal(true)},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			if got := run(t, expr(tc.src)); !Equal(got, tc.want) {
				t.Errorf("got %v (%s), want %v", got, typeName(got), tc.want)
			}
		})
	}
}

func TestAppendMutatesTheCallersList(t *testing.T) {
	// A list is a reference value, which is what makes it usable as an
	// accumulator across a call.
	got := run(t, `def add(l list) -> null
    append(l, 3)
end
def main() -> int
    xs = [1, 2]
    add(xs)
    return len(xs)
end`)
	if !Equal(got, IntVal(3)) {
		t.Errorf("got %v, want 3 — the callee's append was not visible", got)
	}
}

func TestScriptDefinitionShadowsBuiltin(t *testing.T) {
	got := run(t, `def len(x int) -> int
    return 99
end
def main() -> int
    return len(1)
end`)
	if !Equal(got, IntVal(99)) {
		t.Errorf("got %v, want 99 — the script's own definition should win", got)
	}
}

func TestLogGoesToTheConfiguredSink(t *testing.T) {
	in := compile(t, `def main() -> null
    log("hello", 42)
end`, nil)
	var lines []string
	in.SetOutput(func(s string) { lines = append(lines, s) })
	if _, err := in.Call("main", nil, Pos{}); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0] != "hello 42" {
		t.Errorf("logged %v, want [hello 42]", lines)
	}
}

func TestWorldVerbWithoutAHostReportsCleanly(t *testing.T) {
	err := runErr(t, expr("player()"), nil)
	if !strings.Contains(err.Error(), "world") {
		t.Errorf("diagnostic does not explain the problem: %v", err)
	}
}

func TestUndefinedNameIsAnError(t *testing.T) {
	// Not null: a typo that silently produced null is a bug that hides.
	err := runErr(t, expr("mispelled_name"), nil)
	if !strings.Contains(err.Error(), "not defined") {
		t.Errorf("diagnostic does not explain the problem: %v", err)
	}
}

// ---------- the world, through MapHost ----------

func TestAddressingAndAttributes(t *testing.T) {
	h := NewMapHost()
	crate := h.AddObject("crate", RefEntity, "breakable")
	crate.Attrs["health"] = IntVal(25)
	h.SetSelf("crate")

	got, _ := runWithHost(t, `def main() -> int
    return @crate.attr.health
end`, h)
	if !Equal(got, IntVal(25)) {
		t.Errorf("read gave %v, want 25", got)
	}

	runWithHost(t, `def main() -> null
    @crate.attr.health -= 10
end`, h)
	if !Equal(crate.Attrs["health"], IntVal(15)) {
		t.Errorf("after -= 10 the attribute is %v, want 15", crate.Attrs["health"])
	}
}

func TestMissingObjectIsNullNotAnError(t *testing.T) {
	// A script that checks `if @door == null` is doing the right thing; making
	// the check itself throw would leave no way to write it.
	h := NewMapHost()
	got, _ := runWithHost(t, `def main() -> bool
    return @nothing == null
end`, h)
	if !Equal(got, BoolVal(true)) {
		t.Errorf("got %v, want true", got)
	}
}

func TestAbsentAttributeReadsAsNull(t *testing.T) {
	h := NewMapHost()
	h.AddObject("crate", RefEntity)
	got, _ := runWithHost(t, `def main() -> bool
    return @crate.attr.never_set == null
end`, h)
	if !Equal(got, BoolVal(true)) {
		t.Errorf("got %v, want true", got)
	}
}

func TestUnknownPropertyIsAnError(t *testing.T) {
	// Unlike an attribute, the property set is fixed, so a name outside it is a
	// typo rather than a new field.
	h := NewMapHost()
	h.AddObject("crate", RefEntity)
	err := runErr(t, expr("@crate.prop.wibble"), h)
	if !strings.Contains(err.Error(), "property") {
		t.Errorf("diagnostic does not explain the problem: %v", err)
	}
}

func TestTagBroadcastWritesToEveryMember(t *testing.T) {
	h := NewMapHost()
	for _, id := range []string{"lamp_a", "lamp_b"} {
		o := h.AddObject(id, RefLight, "lamps")
		o.Props["brightness"] = FloatVal(1)
	}
	h.AddObject("crate", RefEntity) // untagged: must not be touched

	runWithHost(t, `def main() -> null
    #lamps.prop.brightness = 0.0
end`, h)

	for _, id := range []string{"lamp_a", "lamp_b"} {
		if !Equal(h.Objects[id].Props["brightness"], FloatVal(0)) {
			t.Errorf("%s was not written: %v", id, h.Objects[id].Props["brightness"])
		}
	}
	if len(h.Objects["crate"].Props) != 0 {
		t.Error("an untagged object was written through the tag")
	}
}

func TestReadingThroughATagIsRefused(t *testing.T) {
	// `#lamps.attr.x` would have to be a list, and `#lamps.attr.x + 1` would
	// then silently mean something no one intended.
	h := NewMapHost()
	h.AddObject("lamp", RefLight, "lamps")
	err := runErr(t, expr("#lamps.attr.brightness"), h)
	if !strings.Contains(err.Error(), "one object at a time") {
		t.Errorf("diagnostic does not suggest the alternative: %v", err)
	}
}

func TestTagResolvesToAListOfRefs(t *testing.T) {
	h := NewMapHost()
	h.AddObject("a", RefEntity, "prop")
	h.AddObject("b", RefEntity, "prop")
	got, _ := runWithHost(t, expr("len(#prop)"), h)
	if !Equal(got, IntVal(2)) {
		t.Errorf("got %v, want 2", got)
	}
}

func TestStateIsPerObjectAndTransient(t *testing.T) {
	h := NewMapHost()
	h.AddObject("door", RefVolume)
	h.SetSelf("door")

	got, _ := runWithHost(t, `def main() -> bool
    @self.state["open"] = true
    return @self.state["open"]
end`, h)
	if !Equal(got, BoolVal(true)) {
		t.Errorf("state did not round-trip: %v", got)
	}
	// An unset key is null rather than an error, so a script can test for one.
	got, _ = runWithHost(t, expr(`@self.state["never_set"] == null`), h)
	if !Equal(got, BoolVal(true)) {
		t.Errorf("an unset state key was not null: %v", got)
	}
}

func TestRefIdAndKind(t *testing.T) {
	h := NewMapHost()
	h.AddObject("crate", RefEntity)
	got, _ := runWithHost(t, expr("@crate.id"), h)
	if !Equal(got, StrVal("crate")) {
		t.Errorf("ref.id gave %v", got)
	}
	got, _ = runWithHost(t, expr("@crate.kind"), h)
	if !Equal(got, AtomVal("entity")) {
		t.Errorf("ref.kind gave %v", got)
	}
}

func TestSendQueuesRatherThanDelivers(t *testing.T) {
	// `<-` is asynchronous: the runtime drains the queue, and the sender does
	// not run the handler.
	h := NewMapHost()
	h.AddObject("crate", RefEntity)
	h.AddObject("door", RefVolume)
	h.SetSelf("door")

	runWithHost(t, `def main() -> null
    @crate <- (:damage:, @self, 20)
end`, h)

	if len(h.Posted) != 1 {
		t.Fatalf("got %d messages, want 1", len(h.Posted))
	}
	m := h.Posted[0]
	if m.To.ID() != "crate" || m.Atom != "damage" || len(m.Args) != 2 {
		t.Errorf("posted %+v", m)
	}
	if !Equal(m.Args[1], IntVal(20)) {
		t.Errorf("argument is %v, want 20", m.Args[1])
	}
}

func TestBroadcastPostsOnePerMember(t *testing.T) {
	h := NewMapHost()
	h.AddObject("a", RefEntity, "breakable")
	h.AddObject("b", RefEntity, "breakable")
	h.AddObject("c", RefEntity)
	h.SetSelf("a")

	runWithHost(t, "def main() -> null\n    #breakable <- (:damage:, @self, 5)\nend", h)
	if len(h.Posted) != 2 {
		t.Fatalf("got %d messages, want 2", len(h.Posted))
	}
}

// A timer is a send with a delay on it, so it reaches the host as a post that
// happens to say how long to wait — the target is whoever the send named, not
// whoever set it.
func TestDelayedSendCarriesItsDelayAndTarget(t *testing.T) {
	h := NewMapHost()
	h.AddObject("crate", RefEntity)
	h.AddObject("door", RefVolume)
	h.SetSelf("door")

	runWithHost(t, `def main() -> null
    @crate <- after(500, :damage:, 20)
end`, h)

	if len(h.Posted) != 1 {
		t.Fatalf("got %d messages, want 1", len(h.Posted))
	}
	m := h.Posted[0]
	if m.To.ID() != "crate" || m.Atom != "damage" || m.DelayMS != 500 {
		t.Errorf("posted %+v", m)
	}
}

// A broadcast timer snapshots the tag the same way a prompt one does: the
// members are resolved when the timer is set, not when it comes due.
func TestDelayedBroadcastPostsOnePerMember(t *testing.T) {
	h := NewMapHost()
	h.AddObject("a", RefEntity, "lamps")
	h.AddObject("b", RefEntity, "lamps")
	h.SetSelf("a")

	runWithHost(t, "def main() -> null\n    #lamps <- after(250, :flicker:)\nend", h)
	if len(h.Posted) != 2 {
		t.Fatalf("got %d messages, want 2", len(h.Posted))
	}
	for _, m := range h.Posted {
		if m.DelayMS != 250 {
			t.Errorf("posted %+v, want a 250ms delay", m)
		}
	}
}

func TestDelayNeedsToBeANumber(t *testing.T) {
	h := NewMapHost()
	h.AddObject("crate", RefEntity)
	h.SetSelf("crate")
	err := runErr(t, "def main() -> null\n    @crate <- after(\"soon\", :ping:)\nend", h)
	if !strings.Contains(err.Error(), "milliseconds") {
		t.Errorf("error does not explain the delay: %v", err)
	}
}

// `after` is a send, so a bare call is a mistake the language can name rather
// than a verb some host might implement.
func TestBareAfterIsAnError(t *testing.T) {
	h := NewMapHost()
	h.AddObject("crate", RefEntity)
	h.SetSelf("crate")
	err := runErr(t, "def main() -> null\n    after(500, :ping:)\nend", h)
	if !strings.Contains(err.Error(), "@self <- after") {
		t.Errorf("error does not show the send form: %v", err)
	}
}

func TestSendingToNothingIsDropped(t *testing.T) {
	// An object may legitimately have been destroyed since the sender last
	// looked, so this must not be fatal.
	h := NewMapHost()
	h.AddObject("door", RefVolume)
	h.SetSelf("door")
	runWithHost(t, "def main() -> null\n    @gone <- (:open:,)\nend", h)
	if len(h.Posted) != 0 {
		t.Errorf("a message to a missing object was posted: %v", h.Posted)
	}
}

func TestCallAtomFindsTheHandler(t *testing.T) {
	// How delivery resolves a handler: by the message's atom, not by a name the
	// sender had to know.
	h := NewMapHost()
	crate := h.AddObject("crate", RefEntity)
	crate.Attrs["health"] = IntVal(25)
	h.SetSelf("crate")

	in := compile(t, `def on_damage(:damage:, src ref, amount int) -> null
    @self.attr.health -= amount
end`, h)

	_, matched, err := in.CallAtom("damage", []Value{RefVal{Kind: RefEntity, A: "crate"}, IntVal(10)}, Pos{})
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("the handler was not found")
	}
	if !Equal(crate.Attrs["health"], IntVal(15)) {
		t.Errorf("health is %v, want 15", crate.Attrs["health"])
	}

	// An atom nobody handles is not an error — a broadcast to a tag whose
	// members mostly do not care is the normal case.
	_, matched, err = in.CallAtom("nobody_handles_this", nil, Pos{})
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Error("an unhandled atom reported a match")
	}
}

// A guard that declines is not an error — it means "not for me this time",
// exactly like having no handler at all. This is what makes a guard usable on an
// event handler: a `:tick:` handler that only wants the frames where a door is
// opening would otherwise error on every other frame, and be disabled within a
// second by the repeated-failure rule.
func TestDeclinedGuardIsNotAnUnhandledMessageError(t *testing.T) {
	h := NewMapHost()
	door := h.AddObject("door", RefEntity)
	door.Attrs["opening"] = BoolVal(false)
	door.Attrs["ticks"] = IntVal(0)
	h.SetSelf("door")

	in := compile(t, `def on_tick(:tick:, dt float) when @self.attr.opening == true -> null
    @self.attr.ticks += 1
end`, h)

	_, matched, err := in.CallAtom("tick", []Value{FloatVal(0.016)}, Pos{})
	if err != nil {
		t.Fatalf("a declined guard was reported as an error: %v", err)
	}
	if matched {
		t.Error("a declined guard reported a match")
	}
	if !Equal(door.Attrs["ticks"], IntVal(0)) {
		t.Errorf("the body ran anyway: ticks = %v", door.Attrs["ticks"])
	}

	// ...and the same handler runs once the guard holds.
	door.Attrs["opening"] = BoolVal(true)
	_, matched, err = in.CallAtom("tick", []Value{FloatVal(0.016)}, Pos{})
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("the handler did not run with its guard satisfied")
	}
	if !Equal(door.Attrs["ticks"], IntVal(1)) {
		t.Errorf("ticks = %v, want 1", door.Attrs["ticks"])
	}
}

// A declined guard falls through to the next clause of the same handler, rather
// than the whole group declining at the first `when` that fails.
func TestDeclinedGuardFallsThroughToTheNextClause(t *testing.T) {
	h := NewMapHost()
	door := h.AddObject("door", RefEntity)
	door.Attrs["opening"] = BoolVal(false)
	door.Attrs["state"] = StrVal("")
	h.SetSelf("door")

	in := compile(t, `def on_tick(:tick:, dt float) when @self.attr.opening == true -> null
    @self.attr.state = "opening"
on_tick(:tick:, dt float)
    @self.attr.state = "idle"
end`, h)

	_, matched, err := in.CallAtom("tick", []Value{FloatVal(0.016)}, Pos{})
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("the catch-all clause did not run")
	}
	if !Equal(door.Attrs["state"], StrVal("idle")) {
		t.Errorf("state = %v, want idle", door.Attrs["state"])
	}
}

// A guard declining is only forgiven for a delivery. A direct call that no
// clause accepts is still a mistake worth reporting.
func TestDeclinedGuardIsStillAnErrorForADirectCall(t *testing.T) {
	src := `def f(n int) when n > 10 -> int
    return n
end
def main() -> int
    return f(3)
end`
	err := runErr(t, src, nil)
	if !strings.Contains(err.Error(), "no clause") {
		t.Errorf("diagnostic does not explain the problem: %v", err)
	}
}

// Two groups answering the same atom used to be resolved by ranging over a map,
// so which one ran was Go's map iteration order — random per run. Source order
// is what the index gives instead.
func TestTwoHandlersForOneAtomDispatchInSourceOrder(t *testing.T) {
	h := NewMapHost()
	door := h.AddObject("door", RefEntity)
	door.Attrs["log"] = StrVal("")
	h.SetSelf("door")

	src := `def first(:ping:) -> null
    @self.attr.log = @self.attr.log + "first"
end
def second(:ping:) -> null
    @self.attr.log = @self.attr.log + "second"
end`
	for i := 0; i < 20; i++ {
		door.Attrs["log"] = StrVal("")
		in := compile(t, src, h)
		if _, _, err := in.CallAtom("ping", nil, Pos{}); err != nil {
			t.Fatal(err)
		}
		if !Equal(door.Attrs["log"], StrVal("first")) {
			t.Fatalf("run %d picked %v, want first", i, door.Attrs["log"])
		}
	}
}

// A guarded group that declines hands the atom on to the next group, rather than
// swallowing the message.
func TestDeclinedGuardFallsThroughToTheNextHandler(t *testing.T) {
	h := NewMapHost()
	door := h.AddObject("door", RefEntity)
	door.Attrs["open"] = BoolVal(false)
	door.Attrs["log"] = StrVal("")
	h.SetSelf("door")

	in := compile(t, `def while_open(:ping:) when @self.attr.open == true -> null
    @self.attr.log = "open"
end
def while_shut(:ping:) -> null
    @self.attr.log = "shut"
end`, h)

	if _, matched, err := in.CallAtom("ping", nil, Pos{}); err != nil || !matched {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if !Equal(door.Attrs["log"], StrVal("shut")) {
		t.Errorf("log = %v, want shut", door.Attrs["log"])
	}
}

func TestWorldVerbReachesTheHost(t *testing.T) {
	h := NewMapHost()
	h.AddObject("crate", RefEntity)
	h.Verbs["spawn"] = func(args []Value, pos Pos) (Value, error) { return NullVal{}, nil }

	runWithHost(t, "def main() -> null\n    spawn(@crate, <1.0, 0.0, 0.0>)\nend", h)
	if len(h.Actions) != 1 || h.Actions[0].Name != "spawn" {
		t.Fatalf("actions: %+v", h.Actions)
	}
	if len(h.Actions[0].Args) != 2 {
		t.Errorf("got %d arguments, want 2", len(h.Actions[0].Args))
	}
}

func TestUnimplementedVerbSaysSo(t *testing.T) {
	h := NewMapHost()
	err := runErr(t, expr("play_anim(@a, \"b\", \"c\")"), h)
	if !strings.Contains(err.Error(), "does not provide") {
		t.Errorf("diagnostic does not explain the problem: %v", err)
	}
}

// ---------- linking ----------

type testLoader map[string]string

func (l testLoader) Load(fromFile, importPath string) ([]byte, string, error) {
	src, ok := l[importPath]
	if !ok {
		return nil, "", &RuntimeError{Msg: "no such file: " + importPath}
	}
	return []byte(src), importPath, nil
}

func TestImportsMergeIntoOneNamespace(t *testing.T) {
	loader := testLoader{
		"helpers.flang": "def double(n int) -> int\n    return n * 2\nend",
	}
	prog, errs := Link("main.flang", []byte(`import("helpers.flang")
def main() -> int
    return double(21)
end`), loader)
	if len(errs) > 0 {
		t.Fatalf("link errors: %v", errs)
	}
	in, ierrs := NewInterp(prog, nil, Limits{})
	if len(ierrs) > 0 {
		t.Fatalf("clause errors: %v", ierrs)
	}
	v, err := in.Call("main", nil, Pos{})
	if err != nil {
		t.Fatal(err)
	}
	if !Equal(v, IntVal(42)) {
		t.Errorf("got %v, want 42", v)
	}
}

func TestImportCycleIsReported(t *testing.T) {
	loader := testLoader{
		"a.flang": `import("b.flang")`,
		"b.flang": `import("a.flang")`,
	}
	_, errs := Link("a.flang", []byte(`import("b.flang")`), loader)
	if len(errs) == 0 {
		t.Fatal("an import cycle produced no diagnostic")
	}
	if !strings.Contains(errs[0].Msg, "cycle") {
		t.Errorf("diagnostic does not name the problem: %q", errs[0].Msg)
	}
}

func TestDuplicateDefinitionAcrossFilesIsReported(t *testing.T) {
	// Imports merge flat, so two files defining one name is a conflict rather
	// than a shadowing.
	loader := testLoader{
		"other.flang": "def f() -> int\n    return 1\nend",
	}
	_, errs := Link("main.flang", []byte(`import("other.flang")
def f() -> int
    return 2
end`), loader)
	if len(errs) == 0 {
		t.Fatal("a duplicate definition produced no diagnostic")
	}
	if !strings.Contains(errs[0].Msg, "already defined") {
		t.Errorf("diagnostic does not explain the problem: %q", errs[0].Msg)
	}
}

// ---------- the debugger seam ----------

func TestDebugHookSeesEveryStatement(t *testing.T) {
	// The hook is here from the start even though nothing sets it yet:
	// retrofitting one into a finished evaluator means touching every eval
	// method, while a nil check in the step loop costs nothing.
	in := compile(t, `def main() -> int
    x = 1
    x += 1
    return x
end`, nil)
	var seen []string
	in.SetDebugHook(func(s Stmt, env *Env) {
		switch s.(type) {
		case *AssignStmt:
			seen = append(seen, "assign")
		case *ReturnStmt:
			seen = append(seen, "return")
		}
	})
	if _, err := in.Call("main", nil, Pos{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"assign", "assign", "return"}
	if len(seen) != len(want) {
		t.Fatalf("hook saw %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("statement %d was %q, want %q", i, seen[i], want[i])
		}
	}
}

func TestRuntimeErrorsCarryAPosition(t *testing.T) {
	err := runErr(t, "def main() -> int\n    return 1 / 0\nend", nil)
	re, ok := err.(*RuntimeError)
	if !ok {
		t.Fatalf("got %T, want a *RuntimeError", err)
	}
	if re.Pos.Line != 2 {
		t.Errorf("error is on line %d, want 2", re.Pos.Line)
	}
	if re.Pos.File != "test.flang" {
		t.Errorf("error names %q, want test.flang", re.Pos.File)
	}
}
