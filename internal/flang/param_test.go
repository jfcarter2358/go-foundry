package flang

// param_test.go — `param` declarations and the `?name` reads that use them.
//
// The behaviour worth pinning down is the precedence chain — what the host bound
// beats the declared default, which beats null — and the separation of the two
// sigils: `?name` is the declaration and `@name` is the level's object, and a
// word may be both at once without either winning. Everything else about params
// is storage, which is the engine's half.

import (
	"strings"
	"testing"
)

// paramInterp compiles src and returns an interpreter over a MapHost, so a test
// can bind params and call in.
func paramInterp(t *testing.T, src string) (*Interp, *MapHost) {
	t.Helper()
	f, errs := Parse("param_test.flang", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors:\n%s\n\n%v", src, errs)
	}
	prog := NewProgram()
	if perrs := prog.Add(f); len(perrs) > 0 {
		t.Fatalf("link errors: %v", perrs)
	}
	h := NewMapHost()
	in, ierrs := NewInterp(prog, h, Limits{})
	if len(ierrs) > 0 {
		t.Fatalf("clause errors: %v", ierrs)
	}
	return in, h
}

func callMain(t *testing.T, in *Interp) Value {
	t.Helper()
	v, err := in.Call("main", nil, Pos{File: "param_test.flang"})
	if err != nil {
		t.Fatalf("running: %v", err)
	}
	return v
}

func TestParamDeclarationParses(t *testing.T) {
	f, errs := Parse("t.flang", []byte(`
param door ref
param speed float = 2.5
param name str = "front"
param count int = -3
param on bool = true

def main() -> null
end
`))
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(f.Params) != 5 {
		t.Fatalf("got %d params, want 5", len(f.Params))
	}
	if f.Params[0].Name != "door" || f.Params[0].Type != "ref" {
		t.Errorf("first param = %+v", f.Params[0])
	}
	if f.Params[0].Default != nil {
		t.Errorf("a param with no `=` should have no default")
	}
	if v, ok := f.Params[3].DefaultValue(); !ok || v != IntVal(-3) {
		t.Errorf("count default = %v (ok=%v), want -3", v, ok)
	}
	if v, ok := f.Params[1].DefaultValue(); !ok || v != FloatVal(2.5) {
		t.Errorf("speed default = %v (ok=%v), want 2.5", v, ok)
	}
}

func TestParamRejectsBadDeclarations(t *testing.T) {
	cases := map[string]string{
		"no type":       "param door\n\ndef main() -> null\nend\n",
		"unknown type":  "param door list\n\ndef main() -> null\nend\n",
		"no name":       "param 3 int\n\ndef main() -> null\nend\n",
		"expr default":  "param n int = 1 + 2\n\ndef main() -> null\nend\n",
		"non-scalar ty": "param d dict\n\ndef main() -> null\nend\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, errs := Parse("t.flang", []byte(src)); len(errs) == 0 {
				t.Errorf("expected a diagnostic from:\n%s", src)
			}
		})
	}
}

func TestParamReadsBoundValue(t *testing.T) {
	in, h := paramInterp(t, `
param speed float = 2.0

def main() -> float
    return ?speed
end
`)
	if got := callMain(t, in); got != FloatVal(2.0) {
		t.Errorf("unbound = %v, want the declared default 2.0", got)
	}
	h.SetParam("speed", FloatVal(7.5))
	if got := callMain(t, in); got != FloatVal(7.5) {
		t.Errorf("bound = %v, want 7.5", got)
	}
}

// A param with no default and no binding is null, so `if ?door == null` is the
// check an author writes — and it has to be writable without throwing.
func TestParamWithoutDefaultIsNull(t *testing.T) {
	in, _ := paramInterp(t, `
param door ref

def main() -> bool
    return ?door == null
end
`)
	if got := callMain(t, in); got != BoolVal(true) {
		t.Errorf("got %v, want true", got)
	}
}

// A `ref` param is stored as the ID it names, and resolved on each read — so a
// param pointing at an object that does not exist yet starts working when it
// does, rather than being frozen wrong.
func TestRefParamResolvesFromID(t *testing.T) {
	in, h := paramInterp(t, `
param door ref

def main() -> ref
    return ?door
end
`)
	h.SetParam("door", StrVal("front_door"))
	if got := callMain(t, in); got != (NullVal{}) {
		t.Errorf("naming a missing object = %v, want null", got)
	}
	h.AddObject("front_door", RefEntity)
	got := callMain(t, in)
	r, ok := got.(RefVal)
	if !ok || r.A != "front_door" {
		t.Fatalf("got %v, want @front_door", got)
	}
}

// The host may answer with the ref itself rather than its name; both are
// accepted, so a host with an index of its own need not stringify.
func TestRefParamAcceptsRefValue(t *testing.T) {
	in, h := paramInterp(t, `
param door ref

def main() -> ref
    return ?door
end
`)
	o := h.AddObject("front_door", RefEntity)
	h.SetParam("door", o.Ref)
	if got := callMain(t, in); got != o.Ref {
		t.Errorf("got %v, want %v", got, o.Ref)
	}
}

// An empty ref binding is "nothing chosen", not a lookup for the empty name.
func TestRefParamEmptyStringIsNull(t *testing.T) {
	in, h := paramInterp(t, `
param door ref

def main() -> ref
    return ?door
end
`)
	h.SetParam("door", StrVal(""))
	if got := callMain(t, in); got != (NullVal{}) {
		t.Errorf("got %v, want null", got)
	}
}

// The two sigils are two namespaces. Before `?` existed a declaration shadowed
// the world, which meant a script could not name an object whose id happened to
// match one of its own tunables; now neither hides the other.
func TestParamAndObjectDoNotShadowEachOther(t *testing.T) {
	in, h := paramInterp(t, `
param speed float = 1.0

def param_read() -> float
    return ?speed
end

def object_read() -> ref
    return @speed
end
`)
	o := h.AddObject("speed", RefEntity)
	v, err := in.Call("param_read", nil, Pos{})
	if err != nil || v != FloatVal(1.0) {
		t.Errorf("?speed = %v (err %v), want the param's 1.0", v, err)
	}
	v, err = in.Call("object_read", nil, Pos{})
	if err != nil || v != o.Ref {
		t.Errorf("@speed = %v (err %v), want the object", v, err)
	}
}

// `@name` for a declared param, in a level with no such object, is the mistake
// the old shared sigil left behind. It says which sigil to write rather than
// reading null and behaving like a param nobody wired up.
func TestAddrNamingAParamPointsAtTheSigil(t *testing.T) {
	in, _ := paramInterp(t, `
param speed float = 1.0

def main() -> ref
    return @speed
end
`)
	_, err := in.Call("main", nil, Pos{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "?speed") {
		t.Errorf("error %q should name the sigil to write", err)
	}
}

// An undeclared `?name` is an error rather than a null: unlike an object there
// is nothing else it could be, and the set of declarations is closed at link
// time.
func TestUndeclaredParamErrors(t *testing.T) {
	in, _ := paramInterp(t, `
param speed float = 1.0

def main() -> float
    return ?spede
end
`)
	_, err := in.Call("main", nil, Pos{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "?speed") {
		t.Errorf("error %q should list what is declared", err)
	}
}

// A binding of the wrong type is an authoring error worth hearing about: the
// editor writes params through a typed widget, so a mismatch means the level was
// hand-edited or the declaration changed under it.
func TestParamTypeMismatchErrors(t *testing.T) {
	in, h := paramInterp(t, `
param speed float = 1.0

def main() -> float
    return ?speed
end
`)
	h.SetParam("speed", StrVal("fast"))
	_, err := in.Call("main", nil, Pos{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "declared float") {
		t.Errorf("error %q should name the declared type", err)
	}
}

// A param is what the level said, so a script writing one would be arguing with
// its own configuration.
func TestParamIsNotAssignable(t *testing.T) {
	f, errs := Parse("t.flang", []byte(`
param speed float = 1.0

def main() -> null
    ?speed = 3.0
end
`))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	prog := NewProgram()
	prog.Add(f)
	in, _ := NewInterp(prog, NewMapHost(), Limits{})
	_, err := in.Call("main", nil, Pos{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "script parameter") {
		t.Errorf("error %q should say what ?speed is", err)
	}
}

// Two files declaring one name is a conflict rather than a shadowing, for the
// same reason two files defining one function is: imports merge flat.
func TestDuplicateParamAcrossFilesErrors(t *testing.T) {
	a, _ := Parse("a.flang", []byte("param speed float\n\ndef main() -> null\nend\n"))
	b, _ := Parse("b.flang", []byte("param speed int\n\ndef other() -> null\nend\n"))
	prog := NewProgram()
	prog.Add(a)
	errs := prog.Add(b)
	if len(errs) == 0 {
		t.Fatal("expected a diagnostic")
	}
	if !strings.Contains(errs[0].Msg, "already declared") {
		t.Errorf("got %q", errs[0].Msg)
	}
}

// ParamList is what the editor draws its rows from, so its order is the author's
// rather than a map's.
func TestParamListIsSourceOrder(t *testing.T) {
	f, _ := Parse("t.flang", []byte(`
param zebra int
param apple int
param middle int

def main() -> null
end
`))
	prog := NewProgram()
	prog.Add(f)
	got := prog.ParamList()
	want := []string{"zebra", "apple", "middle"}
	if len(got) != len(want) {
		t.Fatalf("got %d params, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("param %d = %q, want %q", i, got[i].Name, name)
		}
	}
}
