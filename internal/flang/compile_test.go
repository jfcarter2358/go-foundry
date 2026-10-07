package flang

// compile_test.go — the compiled closures and the tree-walker agree.
//
// Every test in this package can be run on the tree-walker alone with
// FLANG_TREEWALK=1. This adds the direct comparison: a few thousand random
// programs — arithmetic across ints, floats and the types that refuse it,
// comparisons, `and`/`or`/`not`, locals assigned in branches, loops with
// break and continue, calls with literal-matching clauses and guards, and the
// errors all of those can raise — each run both ways, with the value or the
// error text required to match exactly.

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// progGen writes random flang.
type progGen struct {
	r     *rand.Rand
	vars  []string
	depth int
}

func (g *progGen) pick(xs ...string) string { return xs[g.r.Intn(len(xs))] }

func (g *progGen) literal() string {
	switch g.r.Intn(30) {
	case 0:
		return g.pick(`"a"`, `"bc"`, `""`)
	case 1:
		return "null"
	case 2:
		return g.pick("<1.0, 2.0, 3.0>", "<0.0, -1, 2>", ":atom:", "[1, 2]", "[]", "true")
	case 3, 4, 5, 6, 7, 8, 9:
		return g.pick("0.5", "1.0", "-2.25", "3.0", "0.0")
	case 10, 11, 12, 13:
		return fmt.Sprint(g.r.Intn(3000) - 1000)
	}
	return fmt.Sprint(g.r.Intn(7) - 2)
}

// expr is a value — numbers mostly, so most programs run to the end, with
// now and then a type that makes the arithmetic refuse.
func (g *progGen) expr() string {
	g.depth++
	defer func() { g.depth-- }()
	if g.depth > 3 {
		if g.r.Intn(2) == 0 {
			return g.vars[g.r.Intn(len(g.vars))]
		}
		return g.literal()
	}
	switch g.r.Intn(11) {
	case 0, 1:
		return g.literal()
	case 2, 3:
		if g.r.Intn(60) == 0 {
			return "undefined_name"
		}
		return g.vars[g.r.Intn(len(g.vars))]
	case 4, 5, 6:
		op := g.pick("+", "-", "*", "+", "-", "*", "/", "%")
		return "(" + g.expr() + " " + op + " " + g.expr() + ")"
	case 7:
		return "-" + g.expr()
	case 8:
		return "h(" + g.expr() + ")"
	case 9:
		return "k(" + g.expr() + ", " + g.expr() + ")"
	default:
		return g.pick("abs(", "str(", "max(1, ") + g.expr() + ")"
	}
}

// cond is a test: comparisons joined by `and`, `or` and `not`.
func (g *progGen) cond() string {
	switch g.r.Intn(6) {
	case 0:
		return "(" + g.cond() + " " + g.pick("and", "or") + " " + g.cond() + ")"
	case 1:
		return "not " + g.cmp()
	case 2:
		return g.expr()
	}
	return g.cmp()
}

func (g *progGen) cmp() string {
	return "(" + g.expr() + " " + g.pick("==", "!=", "<", "<=", ">", ">=") + " " + g.expr() + ")"
}

func (g *progGen) stmts(n, indent int) string {
	var b strings.Builder
	pad := strings.Repeat("    ", indent)
	for i := 0; i < n; i++ {
		switch g.r.Intn(9) {
		case 0, 1, 2:
			v := g.pick("a", "b", "c")
			fmt.Fprintf(&b, "%s%s %s %s\n", pad, v, g.pick("=", "=", "+=", "-=", "*="), g.expr())
			g.vars = appendUnique(g.vars, v)
		case 3:
			if indent < 3 {
				fmt.Fprintf(&b, "%sif %s\n%s%selse\n%s%send\n", pad, g.cond(), g.stmts(2, indent+1), pad, g.stmts(1, indent+1), pad)
			}
		case 4:
			if indent < 3 {
				// Bounded: the counter runs out whatever the body does.
				fmt.Fprintf(&b, "%sn%d = 0\n%swhile n%d < 4\n%s    n%d += 1\n%s    if %s\n%s        %s\n%s    end\n%s%send\n",
					pad, indent, pad, indent, pad, indent, pad, g.cond(), pad, g.pick("break", "continue"), pad,
					g.stmts(1, indent+1), pad)
			}
		case 5:
			if indent < 3 {
				fmt.Fprintf(&b, "%sfor it in [%s, %s, %s]\n    %ssum += 1\n%s%send\n", pad, g.literal(), g.literal(), g.literal(),
					pad, g.stmts(1, indent+1), pad)
			}
		case 6:
			fmt.Fprintf(&b, "%sif %s\n%s    return %s\n%send\n", pad, g.cond(), pad, g.expr(), pad)
		case 7:
			fmt.Fprintf(&b, "%sappend(log, %s)\n", pad, g.expr())
		default:
			fmt.Fprintf(&b, "%sx = %s\n", pad, g.expr())
			g.vars = appendUnique(g.vars, "x")
		}
	}
	return b.String()
}

func appendUnique(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

// program is one random script with a `main`, and the helpers it calls.
func (g *progGen) program() string {
	g.vars = []string{"p", "a", "b", "c", "x"}
	body := g.stmts(4, 1)
	return `def h(0) -> int
    return 100
h(n int) when n > 50
    return n - 50
h(n int)
    return n * 2
h(s str)
    return s + "!"
h(v)
    return v
end
def k(a int, b int) -> int
    return a % (b + 7)
k(a, b)
    return [a, b]
end
def main(p) -> null
    log = []
    sum = 0
    a = 1
    b = 2.5
    c = -3
    x = 4
` + body + `    return [log, sum]
end`
}

// runBoth runs src's main(arg) on the compiled closures and on the
// tree-walker, returning what each produced.
func runBoth(t *testing.T, src string, arg Value) (compiled, walked string) {
	t.Helper()
	run := func(compiledMode bool) string {
		prev := useCompiled
		useCompiled = compiledMode
		defer func() { useCompiled = prev }()
		f, errs := Parse("gen.flang", []byte(src))
		if len(errs) > 0 {
			return "parse: " + errs.Error()
		}
		prog := NewProgram()
		prog.Add(f)
		in, ierrs := NewInterp(prog, nil, Limits{MaxSteps: 20000})
		if len(ierrs) > 0 {
			return "interp: " + ierrs.Error()
		}
		v, err := in.Call("main", []Value{arg}, Pos{File: "gen.flang"})
		if err != nil {
			return "error: " + err.Error()
		}
		return "value: " + v.String()
	}
	return run(true), run(false)
}

func TestCompiledAndWalkedAgreeOnRandomPrograms(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	errors, values := 0, 0
	for i := 0; i < 3000; i++ {
		g := &progGen{r: r}
		src := g.program()
		arg := []Value{IntVal(3), FloatVal(1.5), StrVal("s"), NullVal{}, IntVal(0)}[i%5]
		c, w := runBoth(t, src, arg)
		if c != w {
			t.Fatalf("program %d disagrees:\ncompiled: %s\nwalked:   %s\n\n%s", i, c, w, src)
		}
		if strings.HasPrefix(c, "error: ") {
			errors++
		} else if strings.HasPrefix(c, "value: ") {
			values++
		}
	}
	// The generator has to be producing both outcomes for the comparison to
	// mean anything.
	t.Logf("%d errors, %d values", errors, values)
	if errors < 300 || values < 300 {
		t.Fatalf("only %d errors and %d values out of 3000; the generator is lopsided", errors, values)
	}
}

func TestCompiledAndWalkedAgreeOnTheBenchmarks(t *testing.T) {
	// The tick handler, run long enough to take its send branch, both ways.
	tick := func(compiledMode bool) string {
		prev := useCompiled
		useCompiled = compiledMode
		defer func() { useCompiled = prev }()
		h := NewMapHost()
		o := h.AddObject("crate", RefEntity)
		o.Attrs["active"] = BoolVal(true)
		o.Attrs["timer"] = FloatVal(990)
		o.Attrs["count"] = IntVal(0)
		o.Props["position"] = VecVal{1, 2, 3}
		h.SetSelf("crate")
		f, _ := Parse("t.flang", []byte(tickSrc))
		prog := NewProgram()
		prog.Add(f)
		in, _ := NewInterp(prog, h, Limits{})
		for i := 0; i < 700; i++ {
			if _, _, err := in.CallAtom("tick", []Value{FloatVal(1.0 / 60.0)}, Pos{}); err != nil {
				t.Fatal(err)
			}
		}
		return fmt.Sprint(o.Attrs["timer"], o.Attrs["count"], o.Props["position"], h.Posted)
	}
	if c, w := tick(true), tick(false); c != w {
		t.Errorf("tick handler:\ncompiled: %s\nwalked:   %s", c, w)
	}
	for _, src := range []string{
		"def main(p) -> int\n    return fib(15)\nend\ndef fib(n int) -> int\n    return fib(n - 1) + fib(n - 2)\nfib(1)\n    return 1\nfib(0)\n    return 0\nend",
		"def main(p) -> int\n    i = 0\n    s = 0\n    while i < 300\n        if i % 3 == 0\n            s += i\n        elif i % 3 == 1\n            s -= 1\n        end\n        i += 1\n    end\n    return s\nend",
	} {
		if c, w := runBoth(t, src, NullVal{}); c != w || !strings.HasPrefix(c, "value: ") {
			t.Errorf("\ncompiled: %s\nwalked:   %s", c, w)
		}
	}
}
