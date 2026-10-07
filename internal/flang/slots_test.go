package flang

// slots_test.go — locals in numbered frame slots mean exactly what they meant
// as names in a map.
//
// resolve.go settles every local's place before a clause runs, and calls take
// their frames from a reused stack. The tests here pin the corners where that
// could differ from name lookup: reading before the first assignment, a guard
// reading a body's local, the globals underneath, a name bound twice, frames
// surviving the stack growing under them, and the debugger still seeing names.

import (
	"strings"
	"testing"
)

func TestReadingALocalBeforeItIsAssignedIsStillUndefined(t *testing.T) {
	err := runErr(t, `def main() -> int
    y = x
    x = 1
    return y
end`, nil)
	if !strings.Contains(err.Error(), `"x" is not defined`) {
		t.Fatalf("got %v", err)
	}
}

func TestAGuardCannotSeeTheBodysLocals(t *testing.T) {
	in := compile(t, `def f(n int) when y > 0 -> int
    y = n
    return y
end`, nil)
	_, err := in.Call("f", []Value{IntVal(1)}, Pos{})
	if err == nil || !strings.Contains(err.Error(), `"y" is not defined`) {
		t.Fatalf("got %v", err)
	}
}

func TestEachCallStartsWithItsLocalsUnset(t *testing.T) {
	// The second call must not see the first call's x through a reused frame.
	in := compile(t, `def f(first bool) -> int
    if first
        x = 7
    end
    return x
end`, nil)
	if v, err := in.Call("f", []Value{BoolVal(true)}, Pos{}); err != nil || v != IntVal(7) {
		t.Fatalf("first call: %v %v", v, err)
	}
	if _, err := in.Call("f", []Value{BoolVal(false)}, Pos{}); err == nil {
		t.Fatal("the second call read the first call's local")
	}
}

func TestANameBoundTwiceInAHeadHoldsTheLaterArgument(t *testing.T) {
	in := compile(t, `def f(a, a) -> int
    return a
end`, nil)
	if v, err := in.Call("f", []Value{IntVal(1), IntVal(2)}, Pos{}); err != nil || v != IntVal(2) {
		t.Fatalf("got %v %v", v, err)
	}
}

func TestFramesSurviveTheStackGrowingUnderThem(t *testing.T) {
	// Deep recursion grows the slot and argument stacks many times over while
	// every outer frame is still live and still has locals to read back.
	got := run(t, `def down(n int, acc int) -> int
    here = n * 2
    if n == 0
        return acc
    end
    below = down(n - 1, acc + 1)
    return below + here - n * 2
end
def main() -> int
    return down(200, 0)
end`)
	if got != IntVal(200) {
		t.Fatalf("got %v, want 200", got)
	}
}

func TestNestedCallsInArguments(t *testing.T) {
	got := run(t, `def add(a int, b int) -> int
    return a + b
end
def mul(a int, b int) -> int
    return a * b
end
def main() -> int
    return add(mul(add(1, 2), add(3, 4)), add(mul(2, 3), add(add(1, 1), 1)))
end`)
	if got != IntVal(21+9) {
		t.Fatalf("got %v", got)
	}
}

func TestReturnFromInsideLoopsInsideCalls(t *testing.T) {
	got := run(t, `def find(xs list, want int) -> int
    i = 0
    while true
        for x in xs
            if x == want
                return i
            end
            i += 1
        end
        return -1
    end
end
def main() -> list
    out = []
    for w in [3, 9, 5]
        out = out + [find([1, 3, 5], w)]
    end
    return out
end`)
	if got.String() != "[1, -1, 2]" {
		t.Fatalf("got %v", got)
	}
}

func TestBreakAndContinueStillOnlyWorkInLoops(t *testing.T) {
	got := run(t, `def main() -> int
    s = 0
    for x in [1, 2, 3, 4, 5, 6]
        if x == 2
            continue
        end
        if x == 5
            break
        end
        s += x
    end
    return s
end`)
	if got != IntVal(1+3+4) {
		t.Fatalf("got %v", got)
	}
	err := runErr(t, "def main() -> null\n    break\nend", nil)
	if !strings.Contains(err.Error(), "`break` outside a loop") {
		t.Fatalf("got %v", err)
	}
	err = runErr(t, "def main() -> null\n    continue\nend", nil)
	if !strings.Contains(err.Error(), "`continue` outside a loop") {
		t.Fatalf("got %v", err)
	}
}

func TestTheLoopVariableOutlivesTheLoop(t *testing.T) {
	// Function scope: the loop's variable is a local of the function.
	got := run(t, `def main() -> int
    for x in [4, 5, 6]
        y = x
    end
    return x + y
end`)
	if got != IntVal(12) {
		t.Fatalf("got %v", got)
	}
}

func TestGlobalsStillAnswerNamesAClauseDoesNotBind(t *testing.T) {
	in := compile(t, `def read() -> int
    return g
end
def write() -> int
    g = g + 1
    return g
end`, nil)
	in.Globals().Declare("g", IntVal(10))
	if v, err := in.Call("read", nil, Pos{}); err != nil || v != IntVal(10) {
		t.Fatalf("read: %v %v", v, err)
	}
	// An assignment to a name the globals hold writes the global, as Set
	// always did; the clause reads it back the same way.
	if v, err := in.Call("write", nil, Pos{}); err != nil || v != IntVal(11) {
		t.Fatalf("write: %v %v", v, err)
	}
	if v, _ := in.Globals().Get("g"); v != IntVal(11) {
		t.Fatalf("global after write: %v", v)
	}
}

func TestTheDebugHookStillSeesNames(t *testing.T) {
	in := compile(t, `def main(n int) -> int
    x = n + 1
    y = x * 2
    return y
end`, nil)
	var seen []string
	in.SetDebugHook(func(s Stmt, env *Env) {
		if _, ok := s.(*ReturnStmt); ok {
			x, _ := env.Get("x")
			y, _ := env.Get("y")
			n, _ := env.Get("n")
			seen = append(seen, strings.Join(env.Names(), ","), x.String(), y.String(), n.String())
		}
		if _, ok := s.(*AssignStmt); ok && len(seen) == 0 {
			if _, ok := env.Get("y"); ok {
				t.Error("y was visible before its assignment")
			}
		}
	})
	if _, err := in.Call("main", []Value{IntVal(4)}, Pos{}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen, " ") != "n,x,y 5 10 4" {
		t.Fatalf("hook saw %v", seen)
	}
}

func TestLiteralsAreNotSharedMutably(t *testing.T) {
	// A list literal is built afresh each evaluation, however its scalars are
	// boxed, so appending in one call cannot leak into the next.
	in := compile(t, `def f() -> list
    xs = [1, 2]
    append(xs, 3)
    return xs
end`, nil)
	for i := 0; i < 3; i++ {
		v, err := in.Call("f", nil, Pos{})
		if err != nil || v.String() != "[1, 2, 3]" {
			t.Fatalf("call %d: %v %v", i, v, err)
		}
	}
}

func TestConstantVecsAndNegativesEvaluate(t *testing.T) {
	got := run(t, `def main() -> list
    return [<0.0, -1, 2.5>, -3, -0.5, <1, -2.0, 3>.y]
end`)
	if got.String() != "[<0.0, -1.0, 2.5>, -3, -0.5, -2.0]" {
		t.Fatalf("got %v", got)
	}
}

func TestAMessagesAtomCanBeBound(t *testing.T) {
	// A clause in a handler's group that binds the first argument rather than
	// matching it gets the atom as a value.
	in2 := compile(t, `def on(:hit:, n int) -> atom
    return :hit:
on(what atom, n int)
    return what
end`, nil)
	v, ok, err := in2.CallAtom("hit", []Value{IntVal(1)}, Pos{})
	if err != nil || !ok || v != AtomVal("hit") {
		t.Fatalf("got %v %v %v", v, ok, err)
	}
	v, _, err = in2.Call2("on", []Value{AtomVal("miss"), IntVal(1)})
	if err != nil || v != AtomVal("miss") {
		t.Fatalf("got %v %v", v, err)
	}
}

// Call2 is Call for a test that wants the matched flag too.
func (in *Interp) Call2(name string, args []Value) (Value, bool, error) {
	return in.callClauses(name, in.funcs[name], callArgs{rest: args}, Pos{})
}

// ---------- property handles ----------

// handleHost is MapHost with a PropResolver that counts how often it is asked.
type handleHost struct {
	*MapHost
	asked int
}

type mapPropHandle struct {
	h    *MapHost
	name string
}

func (p mapPropHandle) GetProp(r RefVal) (Value, bool) { return p.h.Prop(r, p.name) }
func (p mapPropHandle) SetProp(r RefVal, v Value) error {
	return p.h.SetProp(r, p.name, v)
}

func (h *handleHost) PropHandle(kind Kind_, name string) PropHandle {
	h.asked++
	if name == "nosuch" {
		return nil
	}
	return mapPropHandle{h: h.MapHost, name: name}
}

func TestPropertyHandlesAreAskedForOncePerSiteAndKind(t *testing.T) {
	h := &handleHost{MapHost: NewMapHost()}
	a := h.AddObject("a", RefEntity)
	a.Props["x"] = IntVal(1)
	b := h.AddObject("b", RefEntity)
	b.Props["x"] = IntVal(2)
	l := h.AddObject("l", RefLight)
	l.Props["x"] = IntVal(3)
	in := compile(t, `def sum(objs list) -> int
    s = 0
    for o in objs
        s += o.prop.x
        o.prop.x = 0
    end
    return s
end`, h)
	v, err := in.Call("sum", []Value{NewList(a.Ref, b.Ref, a.Ref, b.Ref)}, Pos{})
	if err != nil || v != IntVal(3) {
		t.Fatalf("got %v %v", v, err)
	}
	if h.asked != 2 {
		t.Errorf("asked for a handle %d times for one read site and one write site of one kind, want 2", h.asked)
	}
	a.Props["x"], b.Props["x"] = IntVal(1), IntVal(2)
	// Alternating kinds re-resolve, and still read the right object.
	v, err = in.Call("sum", []Value{NewList(a.Ref, l.Ref, b.Ref)}, Pos{})
	if err != nil || v != IntVal(6) {
		t.Fatalf("got %v %v", v, err)
	}
	// No handle falls back to Prop, and its error.
	err = runErr(t, "def main() -> null\n    return @a.prop.nosuch\nend", h)
	if !strings.Contains(err.Error(), `has no property "nosuch"`) {
		t.Fatalf("got %v", err)
	}
}
