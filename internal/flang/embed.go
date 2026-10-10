package flang

// embed.go — evaluating a lone expression, for an authoring tool that is not a
// script.
//
// Everything else in this package reads a *file*: a program of functions and
// clauses, linked and dispatched into. A decision tree (internal/aitree) is a
// different shape of document — its structure is a graph the author drew, and
// the only text in it is the little expressions in a node's fields: the test on
// a branch, the value in an argument. Those want the language, and nothing
// around it.
//
// So this file exposes the two halves that were already there and unexported:
// parse one expression, and evaluate one expression in an environment. No new
// semantics — a field reads exactly the way the same text would read inside a
// handler, `?param`, `@self.prop.x` and all, because it *is* the same parser
// and the same evaluator on the same host.
//
// The env is the caller's, so a tree can bind its event's arguments (`src`,
// `amount`) by name and have a field mention them the way a handler's body
// does.

import "fmt"

// ParseExpr parses a single expression.
//
// file is what positions are reported against — a node's id reads well in a
// diagnostic, since that is what the author clicks on. Trailing text is an
// error rather than being ignored: a field holding `a + b end` is a mistake,
// and silently evaluating `a + b` would hide it.
func ParseExpr(file string, src []byte) (Expr, ErrorList) {
	lx := NewLexer(file, src)
	p := &parser{toks: lx.Tokens(), file: file, errs: lx.Errors()}
	// A blank field is null rather than a parse error: an optional argument
	// left empty is the ordinary case, and the caller decides what to do with
	// the null.
	if p.atEnd() {
		return nil, p.errs.sortAndDedupe()
	}
	e := p.parseExpr()
	if !p.atEnd() {
		p.errs = append(p.errs, Error{
			Pos: p.pos(),
			Msg: fmt.Sprintf("unexpected %s after the expression; a field holds one expression, not a statement", p.cur().Kind),
		})
	}
	return e, p.errs.sortAndDedupe()
}

// Eval evaluates an expression in env.
//
// The step budget is the interpreter's own, so a field that calls a runaway
// function is stopped the same way a handler that does would be. Callers that
// evaluate many fields in one go should ResetSteps once at the top rather than
// before each, which is what makes the budget mean "this event's work".
func (in *Interp) Eval(e Expr, env *Env) (Value, error) {
	if e == nil {
		return NullVal{}, nil
	}
	if env == nil {
		env = NewEnv(in.globals)
	}
	return in.evalExpr(e, env)
}

// NewChildEnv returns an environment over the interpreter's globals, for
// binding a tree node's locals and its event's arguments into.
func (in *Interp) NewChildEnv() *Env { return NewEnv(in.globals) }
