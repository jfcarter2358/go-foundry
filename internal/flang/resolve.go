package flang

// resolve.go — working out, once, what evaluation would otherwise work out on
// every call.
//
// Three things are fixed by a clause's text and so can be settled before it
// ever runs:
//
//   - Where each local lives. flang is function-scoped, so every name a clause
//     can bind — a parameter, an assignment target, a `for` variable, anywhere
//     in the body — is known up front. Each gets a slot in the call's frame,
//     and every IdentExpr naming it records the slot (see env.go).
//   - What each parameter matches against: a literal's value and a type name's
//     Type, rather than evaluating the literal and comparing type names as
//     strings at every call.
//   - Constant values: a negated literal, a vec of literals. (Plain literals are
//     boxed by the parser as it makes them.)
//
// What it writes goes onto the clause's own nodes. That is safe because it is a
// function of the clause's text and nothing else — the same tree resolves the
// same way whichever interpreter asks — and it happens once, under a lock, the
// first time an interpreter over the program is built. Nothing here depends on
// the interpreter, the host or the world; what does (which function a call
// names, which property a member is) is cached per interpreter, at the site —
// see callSite and propSite.
//
// None of this changes what a name means. A slot that has not been assigned
// reads as absent — the same "x is not defined" as before — and a name a clause
// never assigns still falls through to the globals by name.

import "sync"

// resolveMu serialises resolution. Interpreters are built on one goroutine in
// practice, but two built over one program at once must not race on its nodes.
var resolveMu sync.Mutex

// badType is the Type a typed parameter gets when its type name is none the
// language has. It matches no value: TypeMatches compared names, and no value's
// type has that name.
const badType Type = 0xff

// resolveProgram resolves every clause in prog that has not been already.
func resolveProgram(prog *Program) {
	if prog == nil {
		return
	}
	resolveMu.Lock()
	defer resolveMu.Unlock()
	for _, g := range prog.Funcs {
		for _, c := range g.Clauses {
			if !c.resolved {
				resolveClause(c)
			}
			if !c.compiled {
				compileClause(c)
			}
		}
	}
}

// slotTable numbers one clause's locals.
type slotTable struct {
	index map[string]int
	names []string
}

func (t *slotTable) declare(name string) int {
	if i, ok := t.index[name]; ok {
		return i
	}
	i := len(t.names)
	t.index[name] = i
	t.names = append(t.names, name)
	return i
}

// resolveClause lays out one clause's frame and annotates its nodes.
func resolveClause(c *Clause) {
	t := &slotTable{index: map[string]int{}}
	for i := range c.Params {
		p := &c.Params[i]
		switch p.Kind {
		case ParamBind, ParamTyped:
			p.slot = t.declare(p.Name)
		}
		if p.Kind == ParamTyped {
			p.typ = typeByName(p.Type)
		}
		if p.Kind == ParamLiteral {
			p.litVal, p.litOK = literalValue(p.Lit)
		}
	}
	// The head, flattened for matching: what each constrained position must
	// hold, and where each binding position goes.
	c.checks, c.binds = nil, nil
	for i := range c.Params {
		p := &c.Params[i]
		switch p.Kind {
		case ParamAtom:
			c.checks = append(c.checks, headCheck{arg: i, kind: ParamAtom, atom: p.Atom})
		case ParamLiteral:
			ck := headCheck{arg: i, kind: ParamLiteral, lit: p.litVal, litOK: p.litOK}
			if n, ok := p.litVal.(IntVal); ok {
				ck.litInt, ck.litIsInt = int64(n), true
			}
			c.checks = append(c.checks, ck)
		case ParamTyped:
			c.checks = append(c.checks, headCheck{arg: i, kind: ParamTyped, typ: p.typ})
			c.binds = append(c.binds, headBind{arg: i, slot: p.slot})
		default:
			c.binds = append(c.binds, headBind{arg: i, slot: p.slot})
		}
	}
	// Every name the body binds, wherever it binds it: a declaration deep in
	// an `if` is the same local as a read at the top of the clause.
	declareStmts(t, c.Body)
	if c.Guard != nil {
		annotateExpr(t, c.Guard)
	}
	annotateStmts(t, c.Body)
	c.nslots = len(t.names)
	// Clipped, so an Env.Declare of a name outside the table (a debugger's,
	// say) copies rather than appending into the table every call shares.
	c.slotNames = t.names[:len(t.names):len(t.names)]
	c.resolved = true
}

// headCheck is one constrained parameter of a clause head.
type headCheck struct {
	arg      int
	kind     ParamKind
	typ      Type   // ParamTyped
	atom     string // ParamAtom
	lit      Value  // ParamLiteral
	litOK    bool
	litInt   int64 // the literal, when it is an int
	litIsInt bool
}

// headBind is one parameter that binds its argument to a slot.
type headBind struct {
	arg, slot int
}

// typeByName maps a type name to its Type, or badType.
func typeByName(name string) Type {
	for i, n := range typeStrings {
		if n == name {
			return Type(i)
		}
	}
	return badType
}

// declareStmts gives a slot to every name a statement list binds.
func declareStmts(t *slotTable, stmts []Stmt) {
	for _, s := range stmts {
		switch st := s.(type) {
		case *AssignStmt:
			if id, ok := st.Target.(*IdentExpr); ok {
				t.declare(id.Name)
			}
		case *ForStmt:
			t.declare(st.Name)
			declareStmts(t, st.Body)
		case *WhileStmt:
			declareStmts(t, st.Body)
		case *IfStmt:
			for _, b := range st.Blocks {
				declareStmts(t, b)
			}
			declareStmts(t, st.Else)
		}
	}
}

// annotateStmts records slots on every node in a statement list.
func annotateStmts(t *slotTable, stmts []Stmt) {
	for _, s := range stmts {
		switch st := s.(type) {
		case *ExprStmt:
			annotateExpr(t, st.X)
		case *ReturnStmt:
			annotateExpr(t, st.Value)
		case *AssignStmt:
			annotateExpr(t, st.Target)
			annotateExpr(t, st.Value)
		case *IfStmt:
			for i, cond := range st.Conds {
				annotateExpr(t, cond)
				annotateStmts(t, st.Blocks[i])
			}
			annotateStmts(t, st.Else)
		case *WhileStmt:
			annotateExpr(t, st.Cond)
			annotateStmts(t, st.Body)
		case *ForStmt:
			st.slot = t.index[st.Name] + 1
			annotateExpr(t, st.Seq)
			annotateStmts(t, st.Body)
		case *SendStmt:
			annotateExpr(t, st.Target)
			annotateExpr(t, st.Msg)
			annotateExpr(t, st.Delay)
		}
	}
}

// annotateExpr records slots and constants on every node in an expression.
func annotateExpr(t *slotTable, e Expr) {
	switch x := e.(type) {
	case nil:
	case *IdentExpr:
		if i, ok := t.index[x.Name]; ok {
			x.slot = i + 1
		} else {
			x.slot = -1
		}
	case *VecLit:
		annotateExpr(t, x.X)
		annotateExpr(t, x.Y)
		annotateExpr(t, x.Z)
		x.cval = constVec(x)
	case *ListLit:
		for _, el := range x.Elems {
			annotateExpr(t, el)
		}
	case *TupleLit:
		for _, el := range x.Elems {
			annotateExpr(t, el)
		}
	case *DictLit:
		for _, v := range x.Values {
			annotateExpr(t, v)
		}
	case *UnaryExpr:
		annotateExpr(t, x.X)
		if x.Op == Minus {
			switch lit := x.X.(type) {
			case *IntLit:
				x.cval = IntVal(-lit.Value)
			case *FloatLit:
				x.cval = FloatVal(-lit.Value)
			}
		}
	case *BinaryExpr:
		annotateExpr(t, x.L)
		annotateExpr(t, x.R)
	case *CallExpr:
		annotateExpr(t, x.Fn)
		for _, a := range x.Args {
			annotateExpr(t, a)
		}
	case *IndexExpr:
		annotateExpr(t, x.X)
		annotateExpr(t, x.Index)
	case *MemberExpr:
		annotateExpr(t, x.X)
	}
}

// constVec is a vec literal's value when all three components are numbers
// written out — `<0.0, 1.0, 0.0>`, `<-1, 0, 2.5>` — or nil.
func constVec(x *VecLit) Value {
	var out VecVal
	for i, comp := range []Expr{x.X, x.Y, x.Z} {
		v, ok := literalValue(comp)
		if !ok {
			return nil
		}
		f, ok := numeric(v)
		if !ok {
			return nil
		}
		out[i] = f
	}
	return out
}
