package flang

// compile.go — clause bodies turned into Go closures, once.
//
// The tree-walker decides what every node is on every evaluation: a type
// switch over twenty cases for each expression, then the operator, then the
// operand types. None of that changes between evaluations of the same node, so
// each clause's guard and body are compiled — when the program is resolved —
// into closures that have already made those decisions: a local is an index
// into the frame, `i + 1` on two ints is an add, a call goes straight to the
// clauses it names.
//
// THE NODE IS STILL THE PROGRAM COUNTER. A compiled block is a list of
// (statement, closure) pairs, and running it is the tree-walker's own loop —
// count the step, check the budget, hand the statement and the frame to the
// debug hook — around the closure instead of around the switch. A breakpoint,
// a step, an inspector see exactly the statements and the names they saw
// before.
//
// NOT A SECOND EVALUATOR. Everything the closures do on anything but the plain
// fast path, they do by calling the tree-walker's own helpers (binaryOp,
// unaryOp, indexValue, readAccess, assignTo, execSend...), and a node with
// nothing to gain from compiling is simply handed to evalExpr or
// execStmtBody, which run it in the same frame. What a script means is still
// written in exactly one place.
//
// The closures take the interpreter as an argument rather than capturing it,
// so they are a property of the program, built once and shared by every
// interpreter over it — like the frame layout in resolve.go, and under the
// same lock. What does depend on the interpreter (which function a call names,
// a property's handle) is still cached at the node, per interpreter.

import "os"

// useCompiled runs clauses through their compiled closures. Off, every clause
// runs on the tree-walker — which is how the tests check that the two agree
// (FLANG_TREEWALK=1 turns it off for a whole run).
var useCompiled = os.Getenv("FLANG_TREEWALK") == ""

// evalFn evaluates one compiled expression.
type evalFn func(in *Interp, env *Env) (Value, error)

// execFn runs one compiled statement, without the per-statement bookkeeping
// runBlock does around it.
type execFn func(in *Interp, env *Env) error

// cstmt is one compiled statement: the node it came from, which is what the
// step limit and the debug hook are given, and what to run.
type cstmt struct {
	node Stmt
	fn   execFn
}

// condFn evaluates a compiled expression for its truth — an `if`, a `while`,
// a guard — without boxing a bool only to unbox it again.
type condFn func(in *Interp, env *Env) (bool, error)

// compileClause compiles a resolved clause's guard and body.
func compileClause(c *Clause) {
	if c.Guard != nil {
		c.guardFn = compileCond(c.Guard)
	}
	c.body = compileBlock(c.Body)
	c.compiled = true
}

// runBlock runs a compiled block: execBlock and execStmt's loop, around
// closures.
func (in *Interp) runBlock(b []cstmt, env *Env) error {
	for i := range b {
		in.steps++
		if in.steps > in.limits.MaxSteps {
			return in.stepLimit(b[i].node)
		}
		if in.debugHook != nil {
			in.debugHook(b[i].node, env)
		}
		if err := b[i].fn(in, env); err != nil {
			return err
		}
	}
	return nil
}

func compileBlock(stmts []Stmt) []cstmt {
	if stmts == nil {
		return nil
	}
	out := make([]cstmt, len(stmts))
	for i, s := range stmts {
		out[i] = cstmt{node: s, fn: compileStmt(s)}
	}
	return out
}

// ---------- statements ----------

func compileStmt(s Stmt) execFn {
	switch st := s.(type) {
	case *ExprStmt:
		f := compileExpr(st.X)
		return func(in *Interp, env *Env) error {
			_, err := f(in, env)
			return err
		}

	case *ReturnStmt:
		if st.Value == nil {
			return func(in *Interp, env *Env) error {
				in.retVal = NullVal{}
				return errReturn
			}
		}
		f := compileExpr(st.Value)
		return func(in *Interp, env *Env) error {
			v, err := f(in, env)
			if err != nil {
				return err
			}
			in.retVal = v
			return errReturn
		}

	case *BreakStmt:
		return func(*Interp, *Env) error { return errBreak }

	case *ContinueStmt:
		return func(*Interp, *Env) error { return errContinue }

	case *AssignStmt:
		return compileAssign(st)

	case *IfStmt:
		conds := make([]condFn, len(st.Conds))
		blocks := make([][]cstmt, len(st.Conds))
		for i := range st.Conds {
			conds[i] = compileCond(st.Conds[i])
			blocks[i] = compileBlock(st.Blocks[i])
		}
		els := compileBlock(st.Else)
		hasElse := st.Else != nil
		return func(in *Interp, env *Env) error {
			for i, cond := range conds {
				c, err := cond(in, env)
				if err != nil {
					return err
				}
				if c {
					return in.runBlock(blocks[i], env)
				}
			}
			if hasElse {
				return in.runBlock(els, env)
			}
			return nil
		}

	case *WhileStmt:
		cond := compileCond(st.Cond)
		body := compileBlock(st.Body)
		return func(in *Interp, env *Env) error {
			for {
				c, err := cond(in, env)
				if err != nil {
					return err
				}
				if !c {
					return nil
				}
				if err := in.runBlock(body, env); err != nil {
					if stop, done := loopSignal(err); done {
						return stop
					}
				}
			}
		}

	case *ForStmt:
		seq := compileExpr(st.Seq)
		body := compileBlock(st.Body)
		slot := st.slot
		return func(in *Interp, env *Env) error {
			v, err := seq(in, env)
			if err != nil {
				return err
			}
			items, err := in.iterable(v, st.Position())
			if err != nil {
				return err
			}
			for _, item := range items {
				// The loop variable shadows rather than assigns, so a name
				// reused from an outer scope is not clobbered by the loop.
				if slot > 0 && slot <= len(env.vals) {
					env.vals[slot-1] = item
				} else {
					env.Declare(st.Name, item)
				}
				if err := in.runBlock(body, env); err != nil {
					if stop, done := loopSignal(err); done {
						return stop
					}
				}
			}
			return nil
		}
	}
	// A send, or anything new: the tree-walker's own statement, in the same
	// frame.
	return func(in *Interp, env *Env) error { return in.execStmtBody(s, env) }
}

// compileAssign compiles `target = value` and the compound forms, with a
// local as the target taking the fast path.
func compileAssign(st *AssignStmt) execFn {
	val := compileExpr(st.Value)
	var cur evalFn
	op := compoundOp(st.Op)
	if st.Op != Assign {
		// Read back with the same compiled read any expression gets, so an
		// unassigned target is the same error reading it would be.
		cur = compileExpr(st.Target)
	}
	id, isLocal := st.Target.(*IdentExpr)
	if !isLocal || id.slot <= 0 {
		target := st.Target
		return func(in *Interp, env *Env) error {
			v, err := val(in, env)
			if err != nil {
				return err
			}
			if cur != nil {
				c, err := cur(in, env)
				if err != nil {
					return err
				}
				if v, err = in.binaryOp(op, c, v, st.Pos_); err != nil {
					return err
				}
			}
			return in.assignTo(target, v, env)
		}
	}
	i := id.slot - 1
	return func(in *Interp, env *Env) error {
		v, err := val(in, env)
		if err != nil {
			return err
		}
		if cur != nil {
			c, err := cur(in, env)
			if err != nil {
				return err
			}
			if v, err = arith(in, op, c, v, st.Pos_); err != nil {
				return err
			}
		}
		if i < len(env.vals) && env.vals[i] != unset {
			env.vals[i] = v
			return nil
		}
		return in.assignTo(id, v, env)
	}
}

// ---------- expressions ----------

func compileExpr(e Expr) evalFn {
	switch x := e.(type) {
	case nil:
		return func(*Interp, *Env) (Value, error) { return NullVal{}, nil }

	case *IntLit, *FloatLit, *StringLit, *BoolLit, *NullLit, *AtomLit:
		// A literal is its value: evaluate it once, here, and hand that back.
		v, _ := (*Interp)(nil).evalLiteral(e)
		return func(*Interp, *Env) (Value, error) { return v, nil }

	case *IdentExpr:
		return compileIdent(x)

	case *VecLit:
		if x.cval != nil {
			v := x.cval
			return func(*Interp, *Env) (Value, error) { return v, nil }
		}
		cx, cy, cz := compileExpr(x.X), compileExpr(x.Y), compileExpr(x.Z)
		return func(in *Interp, env *Env) (Value, error) {
			var out VecVal
			for i, f := range [3]evalFn{cx, cy, cz} {
				v, err := f(in, env)
				if err != nil {
					return nil, err
				}
				n, ok := numeric(v)
				if !ok {
					return nil, in.errorf(x.Pos_, "a vec component is a number, not %s", typeName(v))
				}
				out[i] = n
			}
			return out, nil
		}

	case *ListLit:
		elems := compileExprs(x.Elems)
		return func(in *Interp, env *Env) (Value, error) {
			vals, err := evalAll(in, env, elems)
			if err != nil {
				return nil, err
			}
			return NewList(vals...), nil
		}

	case *TupleLit:
		elems := compileExprs(x.Elems)
		return func(in *Interp, env *Env) (Value, error) {
			vals, err := evalAll(in, env, elems)
			if err != nil {
				return nil, err
			}
			return NewTuple(vals...), nil
		}

	case *UnaryExpr:
		if x.cval != nil {
			v := x.cval
			return func(*Interp, *Env) (Value, error) { return v, nil }
		}
		f := compileExpr(x.X)
		return func(in *Interp, env *Env) (Value, error) {
			v, err := f(in, env)
			if err != nil {
				return nil, err
			}
			return in.unaryOp(x, v)
		}

	case *BinaryExpr:
		return compileBinary(x)

	case *IndexExpr:
		cx, ci := compileExpr(x.X), compileExpr(x.Index)
		return func(in *Interp, env *Env) (Value, error) {
			c, err := cx(in, env)
			if err != nil {
				return nil, err
			}
			i, err := ci(in, env)
			if err != nil {
				return nil, err
			}
			return in.indexValue(x, c, i)
		}

	case *CallExpr:
		return compileCall(x)

	case *MemberExpr:
		return compileMember(x)
	}
	// Addresses, tags, params, dicts — and anything added later — evaluate
	// as the tree-walker does, in the same frame.
	return func(in *Interp, env *Env) (Value, error) { return in.evalExpr(e, env) }
}

func compileExprs(es []Expr) []evalFn {
	out := make([]evalFn, len(es))
	for i, e := range es {
		out[i] = compileExpr(e)
	}
	return out
}

// evalAll evaluates fns in order into a fresh slice.
func evalAll(in *Interp, env *Env, fns []evalFn) ([]Value, error) {
	out := make([]Value, 0, len(fns))
	for _, f := range fns {
		v, err := f(in, env)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// evalLiteral is a literal node's value, boxed as the parser boxed it.
func (in *Interp) evalLiteral(e Expr) (Value, error) {
	switch x := e.(type) {
	case *IntLit:
		if x.val != nil {
			return x.val, nil
		}
		return IntVal(x.Value), nil
	case *FloatLit:
		if x.val != nil {
			return x.val, nil
		}
		return FloatVal(x.Value), nil
	case *StringLit:
		if x.val != nil {
			return x.val, nil
		}
		return StrVal(x.Value), nil
	case *BoolLit:
		return BoolVal(x.Value), nil
	case *AtomLit:
		if x.val != nil {
			return x.val, nil
		}
		return AtomVal(x.Name), nil
	}
	return NullVal{}, nil
}

// compileIdent reads a local from its slot, falling back to the scope chain
// by name exactly where evalExpr does.
func compileIdent(x *IdentExpr) evalFn {
	name, pos := x.Name, x.Pos_
	if x.slot <= 0 {
		return func(in *Interp, env *Env) (Value, error) {
			if v, ok := env.Get(name); ok {
				return v, nil
			}
			return nil, in.errorf(pos, "%q is not defined", name)
		}
	}
	i := x.slot - 1
	return func(in *Interp, env *Env) (Value, error) {
		if i < len(env.vals) {
			if v := env.vals[i]; v != unset {
				return v, nil
			}
		}
		if v, ok := env.Get(name); ok {
			return v, nil
		}
		return nil, in.errorf(pos, "%q is not defined", name)
	}
}

// compileBinary compiles an infix operator. `and` and `or` short-circuit;
// everything else evaluates both sides and goes through arith, which takes
// two ints or two floats straight to the answer and hands anything else to
// binaryOp.
func compileBinary(x *BinaryExpr) evalFn {
	l, r := compileExpr(x.L), compileExpr(x.R)
	op, pos := x.Op, x.Pos_
	switch op {
	case KwAnd:
		return func(in *Interp, env *Env) (Value, error) {
			lv, err := l(in, env)
			if err != nil {
				return nil, err
			}
			if !Truthy(lv) {
				return BoolVal(false), nil
			}
			rv, err := r(in, env)
			if err != nil {
				return nil, err
			}
			return BoolVal(Truthy(rv)), nil
		}
	case KwOr:
		return func(in *Interp, env *Env) (Value, error) {
			lv, err := l(in, env)
			if err != nil {
				return nil, err
			}
			if Truthy(lv) {
				return BoolVal(true), nil
			}
			rv, err := r(in, env)
			if err != nil {
				return nil, err
			}
			return BoolVal(Truthy(rv)), nil
		}
	}
	if rc, ok := constOf(x.R); ok {
		// `n - 1`, `i % 3`, `x * 0.5`: the right side is already a value.
		return func(in *Interp, env *Env) (Value, error) {
			lv, err := l(in, env)
			if err != nil {
				return nil, err
			}
			return arith(in, op, lv, rc, pos)
		}
	}
	return func(in *Interp, env *Env) (Value, error) {
		lv, err := l(in, env)
		if err != nil {
			return nil, err
		}
		rv, err := r(in, env)
		if err != nil {
			return nil, err
		}
		return arith(in, op, lv, rv, pos)
	}
}

// constOf is the value of an expression that is a constant — a literal, a
// negated number, a vec of numbers — or false.
func constOf(e Expr) (Value, bool) {
	switch x := e.(type) {
	case *IntLit, *FloatLit, *StringLit, *BoolLit, *NullLit, *AtomLit:
		v, _ := (*Interp)(nil).evalLiteral(e)
		return v, true
	case *UnaryExpr:
		if x.cval != nil {
			return x.cval, true
		}
	case *VecLit:
		if x.cval != nil {
			return x.cval, true
		}
	}
	return nil, false
}

// compileCond compiles an expression whose truth is all that is wanted.
// Comparisons of two ints or two floats answer as a bool directly, `and`,
// `or` and `not` combine bools, and anything else is evaluated and tested with
// Truthy — the same answer the tree-walker's Truthy(evalExpr) gives, without
// the BoolVal in between.
func compileCond(e Expr) condFn {
	switch x := e.(type) {
	case *BinaryExpr:
		switch x.Op {
		case KwAnd:
			l, r := compileCond(x.L), compileCond(x.R)
			return func(in *Interp, env *Env) (bool, error) {
				lv, err := l(in, env)
				if err != nil || !lv {
					return false, err
				}
				return r(in, env)
			}
		case KwOr:
			l, r := compileCond(x.L), compileCond(x.R)
			return func(in *Interp, env *Env) (bool, error) {
				lv, err := l(in, env)
				if err != nil {
					return false, err
				}
				if lv {
					return true, nil
				}
				return r(in, env)
			}
		case Eq, NotEq, Lt, LtEq, Gt, GtEq:
			return compileCompare(x)
		}
	case *UnaryExpr:
		if x.Op == KwNot {
			inner := compileCond(x.X)
			return func(in *Interp, env *Env) (bool, error) {
				v, err := inner(in, env)
				if err != nil {
					return false, err
				}
				return !v, nil
			}
		}
	}
	f := compileExpr(e)
	return func(in *Interp, env *Env) (bool, error) {
		v, err := f(in, env)
		if err != nil {
			return false, err
		}
		return Truthy(v), nil
	}
}

// compileCompare compiles a comparison for its truth.
func compileCompare(x *BinaryExpr) condFn {
	l := compileExpr(x.L)
	r := compileExpr(x.R)
	rc, rConst := constOf(x.R)
	op, pos := x.Op, x.Pos_
	return func(in *Interp, env *Env) (bool, error) {
		lv, err := l(in, env)
		if err != nil {
			return false, err
		}
		rv := rc
		if !rConst {
			if rv, err = r(in, env); err != nil {
				return false, err
			}
		}
		if a, ok := lv.(IntVal); ok {
			if b, ok := rv.(IntVal); ok {
				return compareFloats(op, float64(a), float64(b)), nil
			}
		} else if a, ok := lv.(FloatVal); ok {
			if b, ok := rv.(FloatVal); ok {
				return compareFloats(op, float64(a), float64(b)), nil
			}
		}
		v, err := in.binaryOp(op, lv, rv, pos)
		if err != nil {
			return false, err
		}
		return Truthy(v), nil
	}
}

// compareFloats is a comparison operator on two numbers as Equal and compare
// see them.
func compareFloats(op Kind, a, b float64) bool {
	switch op {
	case Eq:
		return a == b
	case NotEq:
		return a != b
	}
	return orderResult(op, cmpFloat(a, b))
}

// arith is binaryOp with the two commonest cases taken first: two ints, and
// two floats. Each answer is the one binaryOp would give — comparisons and
// equality go through float64 just as numeric() made them, and the operators
// that can fail (division, modulo) are left to binaryOp along with every other
// pairing of types.
func arith(in *Interp, op Kind, l, r Value, pos Pos) (Value, error) {
	switch a := l.(type) {
	case IntVal:
		if b, ok := r.(IntVal); ok {
			switch op {
			case Plus:
				return boxInt(int64(a + b)), nil
			case Minus:
				return boxInt(int64(a - b)), nil
			case Star:
				return boxInt(int64(a * b)), nil
			case Percent:
				if b != 0 {
					return boxInt(int64(a % b)), nil
				}
			case Slash:
				if b != 0 {
					return boxInt(int64(a / b)), nil
				}
			case Eq:
				return BoolVal(float64(a) == float64(b)), nil
			case NotEq:
				return BoolVal(float64(a) != float64(b)), nil
			case Lt, LtEq, Gt, GtEq:
				return BoolVal(orderResult(op, cmpFloat(float64(a), float64(b)))), nil
			}
		}
	case FloatVal:
		if b, ok := r.(FloatVal); ok {
			switch op {
			case Plus:
				return a + b, nil
			case Minus:
				return a - b, nil
			case Star:
				return a * b, nil
			case Eq:
				return BoolVal(a == b), nil
			case NotEq:
				return BoolVal(a != b), nil
			case Lt, LtEq, Gt, GtEq:
				return BoolVal(orderResult(op, cmpFloat(float64(a), float64(b)))), nil
			}
		}
	}
	return in.binaryOp(op, l, r, pos)
}

// compileCall compiles a call by name. Which function the name means is still
// settled per interpreter, at the site (callSite), because the program's own
// functions shadow the builtins and a site can be shared.
func compileCall(x *CallExpr) evalFn {
	name, ok := x.Fn.(*IdentExpr)
	if !ok {
		return func(in *Interp, env *Env) (Value, error) {
			return nil, in.errorf(x.Pos_, "only a named function can be called")
		}
	}
	args := compileExprs(x.Args)
	fname, pos := name.Name, x.Pos_
	return func(in *Interp, env *Env) (Value, error) {
		site := &x.site
		if site.owner != in {
			in.resolveCall(site, fname)
		}
		if site.kind == siteScript {
			// See evalCall: a script call's arguments go on the argument
			// stack, appended one at a time.
			base := len(in.argStack)
			for _, a := range args {
				v, err := a(in, env)
				if err != nil {
					in.dropArgs(base)
					return nil, err
				}
				in.argStack = append(in.argStack, v)
			}
			av := in.argStack[base:len(in.argStack):len(in.argStack)]
			v, matched, err := in.callClauses(fname, site.clauses, callArgs{rest: av}, pos)
			if err == nil && !matched {
				err = in.errorf(pos, "no clause of %q matches %s", fname, argTypes(av))
			}
			in.dropArgs(base)
			if err != nil {
				return nil, err
			}
			return v, nil
		}
		vals, err := evalAll(in, env, args)
		if err != nil {
			return nil, err
		}
		if site.kind == siteBuiltin {
			return site.builtin(in, vals, pos)
		}
		return nil, in.errorf(pos, "no function named %q", fname)
	}
}

// compileMember compiles one step of a member chain: an `.attr.<name>` or
// `.prop.<name>` read off one object, or a structural step.
func compileMember(x *MemberExpr) evalFn {
	if isModeWord(x.Name) {
		// `@self.attr` with no name after it: an error, which evalMember
		// words.
		return func(in *Interp, env *Env) (Value, error) { return in.evalMember(x, env) }
	}
	if inner, ok := x.X.(*MemberExpr); ok {
		if mode := modeOf(inner.Name); mode != modeNone {
			if _, isTag := inner.X.(*TagExpr); isTag {
				// Reading through a tag is refused; evalMember says so.
				return func(in *Interp, env *Env) (Value, error) { return in.evalMember(x, env) }
			}
			if addr, isAddr := inner.X.(*AddrExpr); isAddr {
				// `@self.attr.x`, `@door.prop.open`: the ref straight to the
				// read, never boxed.
				return func(in *Interp, env *Env) (Value, error) {
					pb, err := in.addrPathBase(addr)
					if err != nil {
						return nil, err
					}
					return in.readAccess(pb, mode, x, x.Pos_)
				}
			}
			base := compileExpr(inner.X)
			basePos := inner.X.Position()
			return func(in *Interp, env *Env) (Value, error) {
				v, err := base(in, env)
				if err != nil {
					return nil, err
				}
				pb, err := in.pathBaseFrom(v, basePos)
				if err != nil {
					return nil, err
				}
				return in.readAccess(pb, mode, x, x.Pos_)
			}
		}
	}
	recv := compileExpr(x.X)
	return func(in *Interp, env *Env) (Value, error) {
		v, err := recv(in, env)
		if err != nil {
			return nil, err
		}
		return in.readStructural(v, x.Name, x.Pos_)
	}
}
