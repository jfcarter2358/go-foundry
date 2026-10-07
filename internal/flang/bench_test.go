package flang

// bench_test.go — what the interpreter costs on the paths a frame pays for.
//
// Five shapes, chosen because each stresses a different part of the evaluator:
// a call from Go (what delivery does once per message), a call from inside a
// script (frames, argument binding, clause matching), recursion (the same, in
// depth), a loop (statements, locals, arithmetic, no calls), and a `:tick:`
// handler that reads and writes the world through the host (member chains,
// atoms, the host boundary). Run with -benchmem: allocations are most of what
// a tree-walker spends.

import "testing"

// benchInterp compiles src against host, failing the benchmark on any error.
func benchInterp(b *testing.B, src string, host Host) *Interp {
	b.Helper()
	f, errs := Parse("bench.flang", []byte(src))
	if len(errs) > 0 {
		b.Fatalf("parse: %v", errs)
	}
	prog := NewProgram()
	if perrs := prog.Add(f); len(perrs) > 0 {
		b.Fatalf("link: %v", perrs)
	}
	in, ierrs := NewInterp(prog, host, Limits{})
	if len(ierrs) > 0 {
		b.Fatalf("clauses: %v", ierrs)
	}
	return in
}

// BenchmarkCallFromGo is one direct Call of a two-argument function.
func BenchmarkCallFromGo(b *testing.B) {
	in := benchInterp(b, `def add(a int, b int) -> int
    return a + b
end`, nil)
	args := []Value{IntVal(1), IntVal(2)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in.ResetSteps()
		if _, err := in.Call("add", args, Pos{}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCallInScript is a loop of 1000 script-to-script calls.
func BenchmarkCallInScript(b *testing.B) {
	in := benchInterp(b, `def add(a int, b int) -> int
    return a + b
end
def main(n int) -> int
    i = 0
    s = 0
    while i < n
        s = add(s, i)
        i += 1
    end
    return s
end`, nil)
	args := []Value{IntVal(1000)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in.ResetSteps()
		v, err := in.Call("main", args, Pos{})
		if err != nil {
			b.Fatal(err)
		}
		if v != IntVal(499500) {
			b.Fatalf("got %v", v)
		}
	}
}

// BenchmarkFib is naive recursive fib(20): clause dispatch on literals, deep
// recursion, and two calls per frame.
func BenchmarkFib(b *testing.B) {
	in := benchInterp(b, `def fib(n int) -> int
    return fib(n - 1) + fib(n - 2)
fib(1)
    return 1
fib(0)
    return 0
end`, nil)
	args := []Value{IntVal(20)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in.ResetSteps()
		v, err := in.Call("fib", args, Pos{})
		if err != nil {
			b.Fatal(err)
		}
		if v != IntVal(6765) {
			b.Fatalf("got %v", v)
		}
	}
}

// BenchmarkWhileLoop is a 1000-iteration loop of arithmetic and branches, with
// no calls at all.
func BenchmarkWhileLoop(b *testing.B) {
	in := benchInterp(b, `def main(n int) -> int
    i = 0
    s = 0
    f = 0.0
    while i < n
        if i % 3 == 0
            s += i
        elif i % 3 == 1
            s -= 1
        else
            f += 0.5
        end
        i += 1
    end
    return s
end`, nil)
	args := []Value{IntVal(1000)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in.ResetSteps()
		if _, err := in.Call("main", args, Pos{}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkForLoop walks a list with `for`, building a dict.
func BenchmarkForLoop(b *testing.B) {
	in := benchInterp(b, `def main() -> int
    xs = []
    i = 0
    while i < 200
        xs = xs + [i]
        i += 1
    end
    total = 0
    for x in xs
        total += x
    end
    return total
end`, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in.ResetSteps()
		if _, err := in.Call("main", nil, Pos{}); err != nil {
			b.Fatal(err)
		}
	}
}

// tickSrc is a representative `:tick:` handler: a guard on an attribute, a
// property read, an attribute and a property write, a vec, a send.
const tickSrc = `def on_tick(:tick:, dt float) when @self.attr.active -> null
    t = @self.attr.timer + dt
    @self.attr.timer = t
    p = @self.prop.position
    if t > 1000.0
        @self.attr.timer = 0.0
        @self <- (:lap:, t)
    end
    @self.prop.position = <p.x + dt, p.y, p.z>
    @self.attr.count += 1
end
def on_other(:other:, x int) -> null
    return null
end`

// BenchmarkTickHandler delivers `:tick:` to one object through CallAtom, the
// way the runtime does once per scripted object per frame.
func BenchmarkTickHandler(b *testing.B) {
	h := NewMapHost()
	o := h.AddObject("crate", RefEntity)
	o.Attrs["active"] = BoolVal(true)
	o.Attrs["timer"] = FloatVal(0)
	o.Attrs["count"] = IntVal(0)
	o.Props["position"] = VecVal{1, 2, 3}
	h.SetSelf("crate")
	in := benchInterp(b, tickSrc, h)
	args := []Value{FloatVal(1.0 / 60.0)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in.ResetSteps()
		if _, ok, err := in.CallAtom("tick", args, Pos{}); err != nil || !ok {
			b.Fatal(ok, err)
		}
		if len(h.Posted) > 1024 {
			h.Posted = h.Posted[:0]
		}
	}
}

// BenchmarkCallAtomUnanswered is a message no clause answers — what most of a
// broadcast costs.
func BenchmarkCallAtomUnanswered(b *testing.B) {
	in := benchInterp(b, tickSrc, nil)
	args := []Value{FloatVal(1.0 / 60.0)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok, _ := in.CallAtom("nobody", args, Pos{}); ok {
			b.Fatal("answered")
		}
	}
}
