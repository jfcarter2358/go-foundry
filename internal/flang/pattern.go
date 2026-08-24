package flang

// pattern.go — which clause a call selects.
//
// SCRIPTING.md states the rule as "the strictest case is what ends up being
// matched". This is that rule made total, because "strictest" on its own does
// not order two clauses that are each stricter in a different position.
//
// Each parameter scores: a literal or an atom is 3, a typed binding is 1, an
// untyped binding is 0. Two clauses are compared element-wise, left to right,
// and the first position where the scores differ decides. A guard breaks a tie
// in favour of the guarded clause, since a guard can only ever narrow. What is
// left after that is source order.
//
// Ordering happens once, when the file loads, rather than at every call: it
// depends only on the heads. The same pass rejects two clauses that are
// genuinely indistinguishable, because an ambiguous dispatch is a bug the author
// wants to see rather than a coin the runtime should flip.

import (
	"fmt"
	"sort"
)

// signature is a clause's per-parameter specificity, plus whether it is guarded.
type signature struct {
	scores  []int
	guarded bool
}

// clauseSignature computes a clause's signature.
func clauseSignature(c *Clause) signature {
	s := signature{scores: make([]int, len(c.Params)), guarded: c.Guard != nil}
	for i, p := range c.Params {
		s.scores[i] = p.Specificity()
	}
	return s
}

// stricterThan reports whether a should be tried before b.
func (a signature) stricterThan(b signature) bool {
	n := len(a.scores)
	if len(b.scores) < n {
		n = len(b.scores)
	}
	for i := 0; i < n; i++ {
		if a.scores[i] != b.scores[i] {
			return a.scores[i] > b.scores[i]
		}
	}
	// Identical parameter shapes: a guard can only narrow, so a guarded clause
	// is the stricter of the two and has to be tried first — otherwise the
	// unguarded one would swallow every call and the guard would never run.
	return a.guarded && !b.guarded
}

// indistinguishable reports whether two clauses match exactly the same calls,
// which makes the second unreachable.
//
// Equal specificity is not enough, and getting that wrong is a real hazard:
// `fib(0)` and `fib(1)` both score 3, and `f(n int)` and `f(s str)` both score
// 1, yet each pair matches disjoint sets of calls. Only *identical* constraints
// — the same literal, the same type, the same atom, in every position — mean one
// clause can never be reached past the other.
func indistinguishable(a, b *Clause) bool {
	if len(a.Params) != len(b.Params) || a.Guard != nil || b.Guard != nil {
		return false
	}
	for i := range a.Params {
		if !sameConstraint(a.Params[i], b.Params[i]) {
			return false
		}
	}
	return true
}

// sameConstraint reports whether two parameters accept exactly the same values.
// Names are ignored: `f(a int)` and `f(b int)` constrain identically, and only
// the binding differs.
func sameConstraint(a, b Param) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case ParamAtom:
		return a.Atom == b.Atom
	case ParamTyped:
		return a.Type == b.Type
	case ParamLiteral:
		av, aok := literalValue(a.Lit)
		bv, bok := literalValue(b.Lit)
		return aok && bok && Equal(av, bv)
	default: // ParamBind — both accept anything
		return true
	}
}

// OrderClauses returns g's clauses in dispatch order, and any ambiguity found.
//
// Ordering is stable, so clauses the rule cannot separate keep the order they
// were written in — which is what makes the resulting behaviour reproducible.
func OrderClauses(g *FuncGroup) ([]*Clause, ErrorList) {
	var errs ErrorList
	type entry struct {
		c   *Clause
		sig signature
		idx int
	}
	entries := make([]entry, len(g.Clauses))
	for i, c := range g.Clauses {
		entries[i] = entry{c: c, sig: clauseSignature(c), idx: i}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].sig.stricterThan(entries[j].sig)
	})

	// Two clauses with the same shape and no guard match the same calls, so the
	// second could never run. Report it against the clause that is shadowed.
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if indistinguishable(entries[i].c, entries[j].c) {
				errs = append(errs, Error{
					Pos: entries[j].c.Pos_,
					Msg: fmt.Sprintf("clause %s can never run: %s at %s already matches every call it would",
						entries[j].c.Signature(), entries[i].c.Signature(), entries[i].c.Pos_),
				})
			}
		}
	}

	out := make([]*Clause, len(entries))
	for i, e := range entries {
		out[i] = e.c
	}
	return out, errs
}

// matchClause tries to bind args to c's parameters, returning the bindings when
// every parameter matches.
//
// The guard is *not* evaluated here: it needs an environment built from these
// bindings, and it can call functions, so running it belongs to the interpreter.
func matchClause(c *Clause, args []Value) (map[string]Value, bool) {
	if len(args) != len(c.Params) {
		return nil, false
	}
	var binds map[string]Value
	for i, p := range c.Params {
		switch p.Kind {
		case ParamAtom:
			a, ok := args[i].(AtomVal)
			if !ok || string(a) != p.Atom {
				return nil, false
			}

		case ParamLiteral:
			want, ok := literalValue(p.Lit)
			if !ok || !Equal(want, args[i]) {
				return nil, false
			}

		case ParamTyped:
			if !TypeMatches(args[i], p.Type) {
				return nil, false
			}
			if binds == nil {
				binds = make(map[string]Value, len(c.Params))
			}
			binds[p.Name] = args[i]

		default: // ParamBind
			if binds == nil {
				binds = make(map[string]Value, len(c.Params))
			}
			binds[p.Name] = args[i]
		}
	}
	if binds == nil {
		binds = map[string]Value{}
	}
	return binds, true
}

// literalValue evaluates a literal parameter's expression. Literals are closed
// forms — the parser rejects anything else in that position — so this needs no
// environment and cannot fail at runtime.
func literalValue(e Expr) (Value, bool) {
	switch v := e.(type) {
	case *IntLit:
		return IntVal(v.Value), true
	case *FloatLit:
		return FloatVal(v.Value), true
	case *StringLit:
		return StrVal(v.Value), true
	case *BoolLit:
		return BoolVal(v.Value), true
	case *NullLit:
		return NullVal{}, true
	case *AtomLit:
		return AtomVal(v.Name), true
	case *UnaryExpr:
		if v.Op != Minus {
			return nil, false
		}
		switch inner := v.X.(type) {
		case *IntLit:
			return IntVal(-inner.Value), true
		case *FloatLit:
			return FloatVal(-inner.Value), true
		}
	}
	return nil, false
}
