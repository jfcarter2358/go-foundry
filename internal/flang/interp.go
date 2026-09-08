package flang

// interp.go — walking the tree.
//
// A tree-walking interpreter rather than a bytecode VM, for two reasons. It is
// fast enough: scripts run a handful of event handlers a frame, not an inner
// loop. And the AST node being evaluated *is* the program counter, which is what
// makes a debugger tractable — breakpoints, stepping and an inspector all fall
// out of a hook in one place instead of a bytecode-to-source map that has to be
// maintained forever.
//
// That hook is `debugHook`, and it is here from the start even though nothing
// sets it yet. Retrofitting one into a finished evaluator means touching every
// eval method; leaving a nil check in the step loop costs nothing.
//
// Control flow — return, break, continue — is signalled by a sentinel error
// rather than by threading a status through every return value. Go's error
// channel is already there, the sentinels are checked by identity, and the
// alternative makes every eval method three-valued for the benefit of three
// statements.
//
// Call depth is counted rather than left to the host stack. A Go stack overflow
// is a fatal runtime error that `recover` cannot catch, so a runaway recursive
// script would take the whole engine down with no diagnostic. Counting frames
// turns that into an ordinary flang error.

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Limits bound what one call may do. Zero means "use the default", so a
// caller can set one field without having to restate the others.
type Limits struct {
	// MaxCallDepth caps recursion. Mandatory in spirit: see the note above.
	MaxCallDepth int
	// MaxSteps caps the statements one call may execute, which is what catches
	// `while true` inside a single handler.
	MaxSteps int
}

// Defaults for Limits, matching the table in SCRIPTING.md.
const (
	DefaultMaxCallDepth = 256
	DefaultMaxSteps     = 1_000_000
)

func (l Limits) withDefaults() Limits {
	if l.MaxCallDepth <= 0 {
		l.MaxCallDepth = DefaultMaxCallDepth
	}
	if l.MaxSteps <= 0 {
		l.MaxSteps = DefaultMaxSteps
	}
	return l
}

// RuntimeError is a script error: what went wrong, and where in the source.
type RuntimeError struct {
	Pos Pos
	Msg string
}

func (e *RuntimeError) Error() string {
	return fmt.Sprintf("%s: %s", e.Pos, e.Msg)
}

// control signals are returned as errors so they unwind without every eval
// method growing a status return. They are compared by identity, never by
// message.
type controlKind int

const (
	ctrlReturn controlKind = iota
	ctrlBreak
	ctrlContinue
)

type control struct {
	kind  controlKind
	value Value
}

func (c *control) Error() string { return "control flow" }

// Interp evaluates a program.
//
// It knows nothing about Foundry: the world is reached only through Host, and a
// test can supply a fake one. Builtins that touch the world go through the same
// door.
type Interp struct {
	// funcs is every function in scope, already ordered for dispatch.
	funcs map[string][]*Clause
	// handlers indexes the same clauses by the atom they answer, so delivery
	// does not have to walk every function in the program — and, more to the
	// point, does not have to walk them in map order. See buildAtomIndex.
	handlers map[string][]atomHandler
	// params are the program's `param` declarations, by name. Consulted by
	// `@name` before the world is, so a script's own tunable shadows an object
	// that happens to share its name — which is the right way round: the script
	// declared the name, and the level it runs in did not.
	params map[string]*ParamDecl

	// globals underlies every call frame.
	globals *Env
	// host is the world. May be nil, in which case the world builtins report a
	// clean error rather than panicking — which is what lets the pure half of
	// the language be tested with no engine at all.
	host Host

	limits Limits
	depth  int
	steps  int

	// rng is the state behind `random` and `random_range`; see random.go for
	// why it is here rather than in a package-level source, and why it is not
	// math/rand.
	rng uint64

	// debugHook, when set, is called before each statement. It is the seam a
	// debugger attaches to; see the note at the top of the file.
	debugHook func(s Stmt, env *Env)

	// out receives everything `log` prints. Nil sends it nowhere, which is what
	// tests want by default.
	out func(string)
}

// NewInterp returns an interpreter over the given program.
//
// The program's clauses are ordered here rather than at each call, and any
// ambiguity between them is reported now rather than being discovered when the
// wrong one runs.
func NewInterp(prog *Program, host Host, limits Limits) (*Interp, ErrorList) {
	in := &Interp{
		funcs:   map[string][]*Clause{},
		params:  map[string]*ParamDecl{},
		globals: NewEnv(nil),
		host:    host,
		limits:  limits.withDefaults(),
		rng:     newRandomSeed(),
	}
	var errs ErrorList
	if prog != nil {
		for name, d := range prog.Params {
			in.params[name] = d
		}
		for name, g := range prog.Funcs {
			ordered, gerrs := OrderClauses(g)
			errs = append(errs, gerrs...)
			in.funcs[name] = ordered
		}
		in.buildAtomIndex(prog)
	}
	return in, errs
}

// atomHandler is one function group that can answer a message: the clauses
// dispatch will try, and the name a diagnostic should blame.
type atomHandler struct {
	name    string
	arity   int
	clauses []*Clause
}

// buildAtomIndex groups the program's clauses by the atom in their first
// parameter.
//
// Built once, at load, and in source order. Both halves matter. Resolving a
// handler by ranging over a map meant paying for every function in the program
// on every message — but worse, it meant that a program with two groups
// answering the same atom picked between them by Go's map iteration order,
// which is deliberately random. That is a bug that shows up as "it worked
// yesterday".
func (in *Interp) buildAtomIndex(prog *Program) {
	in.handlers = map[string][]atomHandler{}
	names := make([]string, 0, len(prog.Funcs))
	for name := range prog.Funcs {
		names = append(names, name)
	}
	// Source order, so the file reads the way it dispatches. Ties break by name
	// only to keep the order total.
	sort.Slice(names, func(i, j int) bool {
		a, b := prog.Funcs[names[i]].Pos_, prog.Funcs[names[j]].Pos_
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Col != b.Col {
			return a.Col < b.Col
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		clauses := in.funcs[name]
		seen := map[string]bool{}
		for _, c := range clauses {
			if len(c.Params) == 0 || c.Params[0].Kind != ParamAtom || seen[c.Params[0].Atom] {
				continue
			}
			atom := c.Params[0].Atom
			seen[atom] = true
			in.handlers[atom] = append(in.handlers[atom], atomHandler{
				name: name, arity: len(c.Params), clauses: clauses,
			})
		}
	}
}

// SetOutput directs `log` at fn.
func (in *Interp) SetOutput(fn func(string)) { in.out = fn }

// SetDebugHook installs a per-statement callback. Passing nil removes it.
func (in *Interp) SetDebugHook(fn func(Stmt, *Env)) { in.debugHook = fn }

// Globals returns the shared scope, for a host that wants to seed it.
func (in *Interp) Globals() *Env { return in.globals }

// Has reports whether the program defines a function by that name.
func (in *Interp) Has(name string) bool { return len(in.funcs[name]) > 0 }

// ResetSteps clears the per-call step budget. The runtime calls this between
// messages so one long handler does not starve the next.
func (in *Interp) ResetSteps() { in.steps = 0 }

// ---------- calling ----------

// Call invokes a function by name, selecting the clause that matches args.
//
// A call that no clause accepts is an error. That is the difference between a
// call and a delivery: writing `fib(-1)` when every clause declines is a
// mistake worth reporting, whereas a message an object chose not to handle is
// the normal case. See CallAtom.
func (in *Interp) Call(name string, args []Value, pos Pos) (Value, error) {
	clauses := in.funcs[name]
	if len(clauses) == 0 {
		return nil, in.errorf(pos, "no function named %q", name)
	}
	v, matched, err := in.callClauses(name, clauses, args, pos)
	if err != nil {
		return nil, err
	}
	if !matched {
		return nil, in.errorf(pos, "no clause of %q matches %s", name, argTypes(args))
	}
	return v, nil
}

// CallAtom invokes whichever function has a clause matching an atom-headed
// message. It is what delivery resolves through: a handler is picked by the
// message's atom rather than by a name the sender had to know.
//
// Reports ok=false when nothing matched, which is not an error — a broadcast to
// a tag whose members mostly do not care is the normal case.
//
// A guard that declines counts as "did not match", exactly like an absent
// handler. This is the whole point of writing one on an event handler:
//
//	def on_tick(:tick:, dt float) when @self.attr.opening -> null
//
// says "tick me while I am opening", and the frames where it is not opening are
// the expected case rather than an error. Treating them as errors made a guard
// on a handler unusable — it logged every frame and tripped the disable-after-N
// -errors rule within a second — and would have forced every guarded handler to
// carry a do-nothing catch-all clause beside it.
func (in *Interp) CallAtom(atom string, args []Value, pos Pos) (Value, bool, error) {
	full := append([]Value{AtomVal(atom)}, args...)
	for _, h := range in.handlers[atom] {
		if h.arity != len(full) {
			continue
		}
		v, matched, err := in.callClauses(h.name, h.clauses, full, pos)
		if err != nil {
			return nil, true, err
		}
		if matched {
			return v, true, nil
		}
	}
	return NullVal{}, false, nil
}

// callClauses tries each clause in dispatch order, running the first whose head
// matches and whose guard holds.
//
// matched is false when every clause declined — no head fitted, or the ones that
// did were guarded and their guards said no. What that means is the caller's to
// decide: an error for a direct Call, an unhandled message for CallAtom.
func (in *Interp) callClauses(name string, clauses []*Clause, args []Value, pos Pos) (Value, bool, error) {
	if in.depth >= in.limits.MaxCallDepth {
		return nil, false, in.errorf(pos, "call depth limit of %d reached calling %q; this is usually runaway recursion",
			in.limits.MaxCallDepth, name)
	}
	in.depth++
	defer func() { in.depth-- }()

	for _, c := range clauses {
		binds, ok := matchClause(c, args)
		if !ok {
			continue
		}
		env := NewEnv(in.globals)
		for k, v := range binds {
			env.Declare(k, v)
		}
		if c.Guard != nil {
			g, err := in.evalExpr(c.Guard, env)
			if err != nil {
				return nil, false, err
			}
			if !Truthy(g) {
				continue
			}
		}
		if err := in.execBlock(c.Body, env); err != nil {
			if ctl, isCtl := err.(*control); isCtl {
				if ctl.kind == ctrlReturn {
					if ctl.value == nil {
						return NullVal{}, true, nil
					}
					return ctl.value, true, nil
				}
				// break or continue outside a loop unwound all the way here.
				return nil, true, in.errorf(c.Pos_, "`%s` outside a loop", ctlName(ctl.kind))
			}
			return nil, true, err
		}
		// A body that ran off its end returns null, so a handler need not write
		// `return` just to finish.
		return NullVal{}, true, nil
	}
	return NullVal{}, false, nil
}

func ctlName(k controlKind) string {
	if k == ctrlBreak {
		return "break"
	}
	return "continue"
}

// argTypes renders a call's argument types, for the no-matching-clause message.
func argTypes(args []Value) string {
	if len(args) == 0 {
		return "no arguments"
	}
	parts := make([]string, len(args))
	for i, a := range args {
		if a == nil {
			parts[i] = "null"
			continue
		}
		if at, ok := a.(AtomVal); ok {
			// The atom is the interesting part of a message, not its type.
			parts[i] = at.String()
			continue
		}
		parts[i] = a.Type().String()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func (in *Interp) errorf(pos Pos, format string, args ...any) error {
	return &RuntimeError{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

// ---------- statements ----------

func (in *Interp) execBlock(stmts []Stmt, env *Env) error {
	for _, s := range stmts {
		if err := in.execStmt(s, env); err != nil {
			return err
		}
	}
	return nil
}

func (in *Interp) execStmt(s Stmt, env *Env) error {
	in.steps++
	if in.steps > in.limits.MaxSteps {
		return in.errorf(s.Position(), "step limit of %d reached; this is usually a loop that never ends",
			in.limits.MaxSteps)
	}
	if in.debugHook != nil {
		in.debugHook(s, env)
	}

	switch st := s.(type) {
	case *ExprStmt:
		_, err := in.evalExpr(st.X, env)
		return err

	case *ReturnStmt:
		var v Value = NullVal{}
		if st.Value != nil {
			got, err := in.evalExpr(st.Value, env)
			if err != nil {
				return err
			}
			v = got
		}
		return &control{kind: ctrlReturn, value: v}

	case *BreakStmt:
		return &control{kind: ctrlBreak}

	case *ContinueStmt:
		return &control{kind: ctrlContinue}

	case *AssignStmt:
		return in.execAssign(st, env)

	case *IfStmt:
		for i, cond := range st.Conds {
			c, err := in.evalExpr(cond, env)
			if err != nil {
				return err
			}
			if Truthy(c) {
				return in.execBlock(st.Blocks[i], env)
			}
		}
		if st.Else != nil {
			return in.execBlock(st.Else, env)
		}
		return nil

	case *WhileStmt:
		for {
			c, err := in.evalExpr(st.Cond, env)
			if err != nil {
				return err
			}
			if !Truthy(c) {
				return nil
			}
			if err := in.execLoopBody(st.Body, env); err != nil {
				if stop, done := loopSignal(err); done {
					return stop
				}
			}
		}

	case *ForStmt:
		return in.execFor(st, env)

	case *SendStmt:
		return in.execSend(st, env)
	}
	return in.errorf(s.Position(), "cannot execute %T", s)
}

// execLoopBody runs a loop body, letting `continue` fall through.
func (in *Interp) execLoopBody(body []Stmt, env *Env) error {
	return in.execBlock(body, env)
}

// loopSignal interprets an error coming out of a loop body. It reports the error
// to propagate and whether the loop should stop.
func loopSignal(err error) (error, bool) {
	ctl, ok := err.(*control)
	if !ok {
		return err, true // a real error, or a return: unwind
	}
	switch ctl.kind {
	case ctrlBreak:
		return nil, true
	case ctrlContinue:
		return nil, false
	default: // ctrlReturn
		return ctl, true
	}
}

func (in *Interp) execFor(st *ForStmt, env *Env) error {
	seq, err := in.evalExpr(st.Seq, env)
	if err != nil {
		return err
	}
	items, err := in.iterable(seq, st.Position())
	if err != nil {
		return err
	}
	for _, item := range items {
		// The loop variable shadows rather than assigns, so a name reused from
		// an outer scope is not clobbered by the loop.
		env.Declare(st.Name, item)
		if err := in.execLoopBody(st.Body, env); err != nil {
			if stop, done := loopSignal(err); done {
				return stop
			}
		}
	}
	return nil
}

// iterable returns what `for … in` walks over. A dict yields its keys, which is
// the only choice that lets `for k in d` then `d[k]` work.
func (in *Interp) iterable(v Value, pos Pos) ([]Value, error) {
	switch t := v.(type) {
	case *ListVal:
		// Copied, so appending to the list inside the loop cannot make it
		// iterate forever.
		return append([]Value(nil), t.Elems...), nil
	case TupleVal:
		return append([]Value(nil), t.Elems...), nil
	case *DictVal:
		keys := sortedKeys(t)
		out := make([]Value, len(keys))
		for i, k := range keys {
			out[i] = StrVal(k)
		}
		return out, nil
	}
	return nil, in.errorf(pos, "cannot iterate over %s", typeName(v))
}

func sortedKeys(d *DictVal) []string {
	out := make([]string, 0, len(d.M))
	for k := range d.M {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func typeName(v Value) string {
	if v == nil {
		return "null"
	}
	return v.Type().String()
}

// ---------- assignment ----------

func (in *Interp) execAssign(st *AssignStmt, env *Env) error {
	val, err := in.evalExpr(st.Value, env)
	if err != nil {
		return err
	}

	if st.Op != Assign {
		// A compound assignment reads the target, applies the operator, and
		// writes the result back — so `x += 1` on an unassigned x is the same
		// error as reading x would be.
		cur, err := in.evalExpr(st.Target, env)
		if err != nil {
			return err
		}
		val, err = in.binaryOp(compoundOp(st.Op), cur, val, st.Pos_)
		if err != nil {
			return err
		}
	}
	return in.assignTo(st.Target, val, env)
}

// compoundOp maps `+=` to `+`, and so on.
func compoundOp(k Kind) Kind {
	switch k {
	case PlusEq:
		return Plus
	case MinusEq:
		return Minus
	case StarEq:
		return Star
	case SlashEq:
		return Slash
	}
	return k
}

// assignTo writes val to a target expression: a local, an index, or a path into
// an object.
func (in *Interp) assignTo(target Expr, val Value, env *Env) error {
	switch t := target.(type) {
	case *IdentExpr:
		env.Set(t.Name, val)
		return nil

	case *IndexExpr:
		container, err := in.evalExpr(t.X, env)
		if err != nil {
			return err
		}
		idx, err := in.evalExpr(t.Index, env)
		if err != nil {
			return err
		}
		return in.setIndex(container, idx, val, t.Pos_)

	case *MemberExpr:
		return in.assignMember(t, val, env)

	case *TagExpr:
		// `#tag.prop.x = v` writes to every member. The parser allows a bare
		// `#tag = v`, which has no meaning — reject it here rather than
		// complicating the grammar.
		return in.errorf(t.Pos_, "a tag is not assignable on its own; write #%s.attr.<name> or #%s.prop.<name>",
			t.Name, t.Name)

	case *ParamExpr:
		// A param is what the *level* said, so a script writing one would be
		// arguing with its own configuration. Point at the two places that
		// state actually belongs.
		return in.errorf(t.Pos_, "?%s is a script parameter, which the level sets; write @self.attr.<name> or @self.state[\"%s\"] for state the script owns",
			t.Name, t.Name)

	case *AddrExpr:
		return in.errorf(t.Pos_, "an object is not assignable on its own; write @%s.attr.<name> or @%s.prop.<name>",
			t.Name, t.Name)
	}
	return in.errorf(target.Position(), "cannot assign to this")
}

func (in *Interp) setIndex(container, idx, val Value, pos Pos) error {
	switch c := container.(type) {
	case *ListVal:
		n, ok := idx.(IntVal)
		if !ok {
			return in.errorf(pos, "a list index is an int, not %s", typeName(idx))
		}
		if n < 0 || int(n) >= len(c.Elems) {
			return in.errorf(pos, "index %d is outside the list, which has %d elements", int64(n), len(c.Elems))
		}
		c.Elems[n] = val
		return nil

	case *DictVal:
		k, ok := idx.(StrVal)
		if !ok {
			return in.errorf(pos, "a dict key is a str, not %s", typeName(idx))
		}
		c.M[string(k)] = val
		return nil
	}
	if _, isVec := container.(VecVal); isVec {
		return in.errorf(pos, "a vec is a value, so nothing in it can be assigned; build a new one, like v = <1.0, v.y, v.z>")
	}
	return in.errorf(pos, "cannot index %s", typeName(container))
}

// ---------- expressions ----------

func (in *Interp) evalExpr(e Expr, env *Env) (Value, error) {
	switch x := e.(type) {
	case *IntLit:
		return IntVal(x.Value), nil
	case *FloatLit:
		return FloatVal(x.Value), nil
	case *StringLit:
		return StrVal(x.Value), nil
	case *BoolLit:
		return BoolVal(x.Value), nil
	case *NullLit:
		return NullVal{}, nil
	case *AtomLit:
		return AtomVal(x.Name), nil

	case *IdentExpr:
		if v, ok := env.Get(x.Name); ok {
			return v, nil
		}
		// A name that was never assigned is absent rather than null: a typo that
		// silently produced null is a bug that hides for a long time.
		return nil, in.errorf(x.Pos_, "%q is not defined", x.Name)

	case *VecLit:
		return in.evalVec(x, env)

	case *ListLit:
		elems := make([]Value, 0, len(x.Elems))
		for _, el := range x.Elems {
			v, err := in.evalExpr(el, env)
			if err != nil {
				return nil, err
			}
			elems = append(elems, v)
		}
		return NewList(elems...), nil

	case *TupleLit:
		elems := make([]Value, 0, len(x.Elems))
		for _, el := range x.Elems {
			v, err := in.evalExpr(el, env)
			if err != nil {
				return nil, err
			}
			elems = append(elems, v)
		}
		return NewTuple(elems...), nil

	case *DictLit:
		d := NewDict()
		for i, k := range x.Keys {
			v, err := in.evalExpr(x.Values[i], env)
			if err != nil {
				return nil, err
			}
			d.M[k] = v
		}
		return d, nil

	case *UnaryExpr:
		return in.evalUnary(x, env)

	case *BinaryExpr:
		return in.evalBinary(x, env)

	case *IndexExpr:
		return in.evalIndex(x, env)

	case *CallExpr:
		return in.evalCall(x, env)

	case *AddrExpr:
		return in.evalAddr(x, env)

	case *TagExpr:
		return in.evalTag(x, env)

	case *ParamExpr:
		return in.evalParam(x, env)

	case *MemberExpr:
		return in.evalMember(x, env)
	}
	if e == nil {
		return NullVal{}, nil
	}
	return nil, in.errorf(e.Position(), "cannot evaluate %T", e)
}

func (in *Interp) evalVec(x *VecLit, env *Env) (Value, error) {
	var out VecVal
	for i, comp := range []Expr{x.X, x.Y, x.Z} {
		v, err := in.evalExpr(comp, env)
		if err != nil {
			return nil, err
		}
		f, ok := numeric(v)
		if !ok {
			return nil, in.errorf(x.Pos_, "a vec component is a number, not %s", typeName(v))
		}
		out[i] = f
	}
	return out, nil
}

// vecComponent reads `v.x`, `v.y` or `v.z`.
//
// Read-only, and deliberately so. A vec is a value, not a container — Add and
// the rest hand back new ones, and a vec read out of a property is a copy — so
// `@self.prop.position.y = 1.0` would write to that copy and be a silent no-op
// on the object. Rebuilding the whole vec is the one spelling that always means
// what it looks like, and assignVecComponent says so.
func vecComponent(in *Interp, v VecVal, name string, pos Pos) (Value, error) {
	if i := vecAxis(name); i >= 0 {
		return FloatVal(v[i]), nil
	}
	return nil, in.errorf(pos, "a vec has components x, y and z, not %q", name)
}

// vecAxis maps a component name to its index, or -1.
func vecAxis(name string) int {
	switch name {
	case "x":
		return 0
	case "y":
		return 1
	case "z":
		return 2
	}
	return -1
}

func (in *Interp) evalUnary(x *UnaryExpr, env *Env) (Value, error) {
	v, err := in.evalExpr(x.X, env)
	if err != nil {
		return nil, err
	}
	switch x.Op {
	case KwNot:
		return BoolVal(!Truthy(v)), nil
	case Minus:
		switch n := v.(type) {
		case IntVal:
			return -n, nil
		case FloatVal:
			return -n, nil
		case VecVal:
			return n.Mul(-1), nil
		}
		return nil, in.errorf(x.Pos_, "cannot negate %s", typeName(v))
	}
	return nil, in.errorf(x.Pos_, "unknown unary operator %s", x.Op)
}

func (in *Interp) evalBinary(x *BinaryExpr, env *Env) (Value, error) {
	// `and` and `or` short-circuit, so the right side must not be evaluated
	// before the left has been tested.
	switch x.Op {
	case KwAnd:
		l, err := in.evalExpr(x.L, env)
		if err != nil {
			return nil, err
		}
		if !Truthy(l) {
			return BoolVal(false), nil
		}
		r, err := in.evalExpr(x.R, env)
		if err != nil {
			return nil, err
		}
		return BoolVal(Truthy(r)), nil

	case KwOr:
		l, err := in.evalExpr(x.L, env)
		if err != nil {
			return nil, err
		}
		if Truthy(l) {
			return BoolVal(true), nil
		}
		r, err := in.evalExpr(x.R, env)
		if err != nil {
			return nil, err
		}
		return BoolVal(Truthy(r)), nil
	}

	l, err := in.evalExpr(x.L, env)
	if err != nil {
		return nil, err
	}
	r, err := in.evalExpr(x.R, env)
	if err != nil {
		return nil, err
	}
	return in.binaryOp(x.Op, l, r, x.Pos_)
}

func (in *Interp) binaryOp(op Kind, l, r Value, pos Pos) (Value, error) {
	switch op {
	case Eq:
		return BoolVal(Equal(l, r)), nil
	case NotEq:
		return BoolVal(!Equal(l, r)), nil
	case Lt, LtEq, Gt, GtEq:
		return in.compare(op, l, r, pos)
	}

	// `+` is overloaded onto strings and lists as well as numbers, which is what
	// makes "foo" + "bar" and [1] + [2] read the way an author expects.
	if op == Plus {
		switch a := l.(type) {
		case StrVal:
			if b, ok := r.(StrVal); ok {
				return a + b, nil
			}
		case *ListVal:
			if b, ok := r.(*ListVal); ok {
				out := make([]Value, 0, len(a.Elems)+len(b.Elems))
				out = append(out, a.Elems...)
				out = append(out, b.Elems...)
				return NewList(out...), nil
			}
		}
	}

	// Vectors: component-wise add and subtract, scale by a number either way.
	if a, ok := l.(VecVal); ok {
		switch op {
		case Plus:
			if b, ok := r.(VecVal); ok {
				return a.Add(b), nil
			}
		case Minus:
			if b, ok := r.(VecVal); ok {
				return a.Sub(b), nil
			}
		case Star:
			if s, ok := numeric(r); ok {
				return a.Mul(s), nil
			}
		case Slash:
			if s, ok := numeric(r); ok {
				if s == 0 {
					return nil, in.errorf(pos, "division by zero")
				}
				return a.Mul(1 / s), nil
			}
		}
		return nil, in.errorf(pos, typeError(op.String(), l, r))
	}
	if b, ok := r.(VecVal); ok && op == Star {
		if s, ok := numeric(l); ok {
			return b.Mul(s), nil
		}
	}

	return in.arithmetic(op, l, r, pos)
}

// arithmetic applies a numeric operator, promoting to float when either operand
// is one. Two ints stay an int, so counters and indices do not drift.
func (in *Interp) arithmetic(op Kind, l, r Value, pos Pos) (Value, error) {
	li, lIsInt := l.(IntVal)
	ri, rIsInt := r.(IntVal)
	if lIsInt && rIsInt {
		a, b := int64(li), int64(ri)
		switch op {
		case Plus:
			return IntVal(a + b), nil
		case Minus:
			return IntVal(a - b), nil
		case Star:
			return IntVal(a * b), nil
		case Slash:
			if b == 0 {
				return nil, in.errorf(pos, "division by zero")
			}
			return IntVal(a / b), nil
		case Percent:
			if b == 0 {
				return nil, in.errorf(pos, "division by zero")
			}
			return IntVal(a % b), nil
		}
		return nil, in.errorf(pos, "unknown operator %s", op)
	}

	lf, lok := numeric(l)
	rf, rok := numeric(r)
	if !lok || !rok {
		return nil, in.errorf(pos, typeError(op.String(), l, r))
	}
	switch op {
	case Plus:
		return FloatVal(lf + rf), nil
	case Minus:
		return FloatVal(lf - rf), nil
	case Star:
		return FloatVal(lf * rf), nil
	case Slash:
		if rf == 0 {
			return nil, in.errorf(pos, "division by zero")
		}
		return FloatVal(lf / rf), nil
	case Percent:
		if rf == 0 {
			return nil, in.errorf(pos, "division by zero")
		}
		return FloatVal(math.Mod(lf, rf)), nil
	}
	return nil, in.errorf(pos, "unknown operator %s", op)
}

// compare orders two values. Numbers compare numerically and strings
// lexicographically; anything else is an error, because ordering values whose
// order nobody defined is a mistake rather than a preference.
func (in *Interp) compare(op Kind, l, r Value, pos Pos) (Value, error) {
	if lf, ok := numeric(l); ok {
		if rf, ok := numeric(r); ok {
			return BoolVal(orderResult(op, cmpFloat(lf, rf))), nil
		}
	}
	if ls, ok := l.(StrVal); ok {
		if rs, ok := r.(StrVal); ok {
			return BoolVal(orderResult(op, strings.Compare(string(ls), string(rs)))), nil
		}
	}
	return nil, in.errorf(pos, "cannot order %s against %s", typeName(l), typeName(r))
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func orderResult(op Kind, c int) bool {
	switch op {
	case Lt:
		return c < 0
	case LtEq:
		return c <= 0
	case Gt:
		return c > 0
	default: // GtEq
		return c >= 0
	}
}

func (in *Interp) evalIndex(x *IndexExpr, env *Env) (Value, error) {
	container, err := in.evalExpr(x.X, env)
	if err != nil {
		return nil, err
	}
	idx, err := in.evalExpr(x.Index, env)
	if err != nil {
		return nil, err
	}
	switch c := container.(type) {
	case *ListVal:
		n, ok := idx.(IntVal)
		if !ok {
			return nil, in.errorf(x.Pos_, "a list index is an int, not %s", typeName(idx))
		}
		if n < 0 || int(n) >= len(c.Elems) {
			return nil, in.errorf(x.Pos_, "index %d is outside the list, which has %d elements",
				int64(n), len(c.Elems))
		}
		return c.Elems[n], nil

	case TupleVal:
		n, ok := idx.(IntVal)
		if !ok {
			return nil, in.errorf(x.Pos_, "a tuple index is an int, not %s", typeName(idx))
		}
		if n < 0 || int(n) >= len(c.Elems) {
			return nil, in.errorf(x.Pos_, "index %d is outside the tuple, which has %d elements",
				int64(n), len(c.Elems))
		}
		return c.Elems[n], nil

	case *DictVal:
		k, ok := idx.(StrVal)
		if !ok {
			return nil, in.errorf(x.Pos_, "a dict key is a str, not %s", typeName(idx))
		}
		// A missing key yields null rather than failing — see SCRIPTING.md.
		return c.Get(string(k)), nil

	case StrVal:
		n, ok := idx.(IntVal)
		if !ok {
			return nil, in.errorf(x.Pos_, "a string index is an int, not %s", typeName(idx))
		}
		if n < 0 || int(n) >= len(c) {
			return nil, in.errorf(x.Pos_, "index %d is outside the string, which is %d long",
				int64(n), len(c))
		}
		return StrVal(c[n : n+1]), nil
	}
	if _, isVec := container.(VecVal); isVec {
		// One spelling for one idea: a vec's three components are named, not
		// numbered, so `v[0]` is pointed at `v.x` rather than quietly allowed
		// as a second way to write it.
		return nil, in.errorf(x.Pos_, "a vec is not indexed; write v.x, v.y or v.z")
	}
	return nil, in.errorf(x.Pos_, "cannot index %s", typeName(container))
}

func (in *Interp) evalCall(x *CallExpr, env *Env) (Value, error) {
	name, ok := x.Fn.(*IdentExpr)
	if !ok {
		// There are no first-class functions: everything callable is named.
		return nil, in.errorf(x.Pos_, "only a named function can be called")
	}
	args := make([]Value, 0, len(x.Args))
	for _, a := range x.Args {
		v, err := in.evalExpr(a, env)
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}

	// A script's own definitions shadow the builtins, so a game can define its
	// own `distance` without the engine's getting in the way.
	if in.Has(name.Name) {
		return in.Call(name.Name, args, x.Pos_)
	}
	if fn, ok := builtins[name.Name]; ok {
		return fn(in, args, x.Pos_)
	}
	return nil, in.errorf(x.Pos_, "no function named %q", name.Name)
}

// sortStrings is a tiny insertion sort, used where pulling in sort would be the
// only reason this file imported it. Key sets here are small by construction.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
