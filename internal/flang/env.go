package flang

// env.go — variable scope.
//
// flang has no `let`: the first assignment to a name declares it, and the scope
// is the enclosing *function*, not the enclosing block. That makes
//
//	if cond
//	    x = 1
//	end
//	return x
//
// work, which is what an author writing gameplay logic expects and what the
// alternative — block scoping with an explicit declaration — would have made an
// error for no benefit at this size of language.
//
// A chain of scopes still exists, because a function call must not see its
// caller's locals. Env therefore has exactly two levels in practice: the
// per-call frame, and the shared globals underneath it.
//
// This is also where a debugger will read from: the chain is the scope stack,
// and rendering it is most of an inspector.
//
// SLOTS, NOT A MAP. Function scope means every local a clause can ever have is
// known before it runs: its parameters, every name it assigns, every `for`
// variable. resolve.go numbers them once per clause, and a call's frame is just
// those values in a row, taken from a stack the interpreter reuses — so a call
// allocates nothing for its scope, and reading a local is an index rather than a
// hash. The names stay beside the values (the clause's slot table), which is
// what keeps Get, Names and the debug hook speaking names: a debugger sees
// exactly what it saw when this was a map.
//
// A scope built by hand — a decision tree binding its event's arguments,
// NewChildEnv — is the same structure with names appended as they are
// declared. Those hold a handful of names, so finding one by a walk is as fast
// as hashing it was, and there is no map to allocate.

// Env is one scope, optionally nested inside another.
type Env struct {
	// names and vals are parallel: vals[i] is the value of names[i], or unset
	// for a local its clause declares but has not assigned yet — which reads
	// as absent, exactly as a name never assigned always has.
	names  []string
	vals   []Value
	parent *Env

	// base is where a pooled call frame's values start on the interpreter's
	// slot stack; see Interp.pushFrame.
	base int
}

// unsetVal marks a declared slot that has not been assigned. Never visible to
// a script: every read treats it as the name being absent.
type unsetVal struct{}

func (unsetVal) Type() Type     { return TypeNull }
func (unsetVal) String() string { return "<unset>" }

// unset is the one unsetVal, compared by value.
var unset Value = unsetVal{}

// NewEnv returns a scope nested inside parent, which may be nil.
func NewEnv(parent *Env) *Env {
	return &Env{parent: parent}
}

// index finds name in this scope alone, set or not, or -1.
func (e *Env) index(name string) int {
	for i, n := range e.names {
		if n == name {
			return i
		}
	}
	return -1
}

// Get looks a name up through the chain, reporting whether it was found. A name
// that was never assigned is *not* null — it is absent, and reading one is an
// error, because a typo that silently yielded null is a bug that hides.
func (e *Env) Get(name string) (Value, bool) {
	for s := e; s != nil; s = s.parent {
		if i := s.index(name); i >= 0 && s.vals[i] != unset {
			return s.vals[i], true
		}
	}
	return nil, false
}

// Set assigns name in the scope that already holds it, or declares it here.
//
// Assigning through to an enclosing scope is what makes a loop body able to
// update a variable declared before the loop — which, given that blocks do not
// introduce scopes, is the only behaviour that could be right.
func (e *Env) Set(name string, v Value) {
	for s := e; s != nil; s = s.parent {
		if i := s.index(name); i >= 0 && s.vals[i] != unset {
			s.vals[i] = v
			return
		}
	}
	e.Declare(name, v)
}

// Declare assigns name in this scope regardless of any outer binding. Used for
// parameters and for a `for` loop's variable, which shadow rather than assign.
func (e *Env) Declare(name string, v Value) {
	if i := e.index(name); i >= 0 {
		e.vals[i] = v
		return
	}
	// A frame's names are its clause's slot table, shared by every call of
	// it; their capacity is clipped, so this append copies rather than
	// writing into the table.
	e.names = append(e.names, name)
	e.vals = append(e.vals, v)
}

// Has reports whether name is bound anywhere in the chain.
func (e *Env) Has(name string) bool {
	_, ok := e.Get(name)
	return ok
}

// Names returns the names bound in this scope alone, for an inspector — the
// ones that hold a value, not the locals a clause has yet to assign.
func (e *Env) Names() []string {
	out := make([]string, 0, len(e.names))
	for i, n := range e.names {
		if e.vals[i] != unset {
			out = append(out, n)
		}
	}
	return out
}

// Parent returns the enclosing scope, or nil.
func (e *Env) Parent() *Env { return e.parent }
