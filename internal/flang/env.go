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

// Env is one scope, optionally nested inside another.
type Env struct {
	vars   map[string]Value
	parent *Env
}

// NewEnv returns a scope nested inside parent, which may be nil.
func NewEnv(parent *Env) *Env {
	return &Env{vars: map[string]Value{}, parent: parent}
}

// Get looks a name up through the chain, reporting whether it was found. A name
// that was never assigned is *not* null — it is absent, and reading one is an
// error, because a typo that silently yielded null is a bug that hides.
func (e *Env) Get(name string) (Value, bool) {
	for s := e; s != nil; s = s.parent {
		if v, ok := s.vars[name]; ok {
			return v, true
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
		if _, ok := s.vars[name]; ok {
			s.vars[name] = v
			return
		}
	}
	e.vars[name] = v
}

// Declare assigns name in this scope regardless of any outer binding. Used for
// parameters and for a `for` loop's variable, which shadow rather than assign.
func (e *Env) Declare(name string, v Value) {
	e.vars[name] = v
}

// Has reports whether name is bound anywhere in the chain.
func (e *Env) Has(name string) bool {
	_, ok := e.Get(name)
	return ok
}

// Names returns the names bound in this scope alone, for an inspector.
func (e *Env) Names() []string {
	out := make([]string, 0, len(e.vars))
	for n := range e.vars {
		out = append(out, n)
	}
	return out
}

// Parent returns the enclosing scope, or nil.
func (e *Env) Parent() *Env { return e.parent }
