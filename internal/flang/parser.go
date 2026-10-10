package flang

// parser.go — tokens to a tree.
//
// A Pratt parser, chosen for one specific reason: it distinguishes *prefix*
// position (an expression is expected) from *infix* position (an expression has
// just been read), which is exactly the distinction that makes flang's two
// syntactic ambiguities go away.
//
//	a < b               `<` in infix position: less-than
//	f(<1.0, 0.0, 0.0>)  `<` in prefix position: a vec literal
//	(1, 2)              `(` in prefix position: a tuple
//	f(1, 2)             `(` in infix position: a call
//
// The lexer cannot make either call — both need to know what came before — and
// resolving them by lookahead in an ad-hoc parser is where this kind of grammar
// usually goes wrong. Here it falls out of the structure for free.
//
// Errors are collected rather than fatal. On a bad statement the parser records
// the diagnostic and skips to the next plausible boundary (sync), so one typo
// yields one complaint instead of a cascade, and the editor can underline
// everything at once.

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse parses one source file, returning the tree and every diagnostic found.
// The tree is non-nil even when there are errors: the parts that parsed are
// still worth showing in an outline or a completion list.
func Parse(file string, src []byte) (*File, ErrorList) {
	lx := NewLexer(file, src)
	p := &parser{
		toks: lx.Tokens(),
		file: file,
		errs: lx.Errors(),
	}
	f := p.parseFile()
	return f, p.errs.sortAndDedupe()
}

type parser struct {
	toks []Token
	i    int
	file string
	errs ErrorList
	// group is the name of the definition whose clauses are being read, or "".
	// A bare clause head and a call statement are both `name(`, and the only
	// thing that separates them is that a head repeats the group's name — see
	// looksLikeClauseHead.
	group string
}

// ---------- token access ----------

func (p *parser) cur() Token  { return p.toks[p.i] }
func (p *parser) kind() Kind  { return p.toks[p.i].Kind }
func (p *parser) pos() Pos    { return p.toks[p.i].Pos }
func (p *parser) atEnd() bool { return p.toks[p.i].Kind == EOF }

// peekKind returns the kind n tokens ahead, clamped to the trailing EOF.
func (p *parser) peekKind(n int) Kind {
	j := p.i + n
	if j >= len(p.toks) {
		j = len(p.toks) - 1
	}
	return p.toks[j].Kind
}

// advance consumes one token and returns it.
func (p *parser) advance() Token {
	t := p.toks[p.i]
	if t.Kind != EOF {
		p.i++
	}
	return t
}

// accept consumes one token if it is of kind k, reporting whether it did.
func (p *parser) accept(k Kind) bool {
	if p.kind() == k {
		p.advance()
		return true
	}
	return false
}

// expect consumes a token of kind k, or records a diagnostic and returns the
// token that was there instead. The token is *not* consumed on failure, so the
// caller's recovery can see what stopped it.
func (p *parser) expect(k Kind, what string) Token {
	if p.kind() == k {
		return p.advance()
	}
	p.errorf(p.pos(), "expected %s, found %s", what, p.cur())
	return p.cur()
}

func (p *parser) errorf(pos Pos, format string, args ...any) {
	// Two complaints at one position tell the author nothing the first did not,
	// and recovery produces those routinely.
	if n := len(p.errs); n > 0 && p.errs[n-1].Pos == pos {
		return
	}
	p.errs = append(p.errs, Error{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// ---------- recovery ----------

// syncToDef skips forward to the next top-level `def`, `param` or `import`, for
// when a definition is too broken to keep reading.
func (p *parser) syncToDef() {
	for !p.atEnd() {
		if p.kind() == KwDef || p.kind() == KwImport || p.kind() == KwParam {
			return
		}
		p.advance()
	}
}

// syncStmt skips forward to something that could start a statement, or to a
// block terminator. Used when one statement inside a body fails to parse, so the
// rest of the body still gets read.
func (p *parser) syncStmt() {
	for !p.atEnd() {
		switch p.kind() {
		case KwEnd, KwElse, KwElif, KwDef,
			KwIf, KwFor, KwWhile, KwReturn, KwBreak, KwContinue:
			return
		}
		p.advance()
	}
}

// ---------- file ----------

func (p *parser) parseFile() *File {
	f := &File{Path: p.file, Pos_: p.pos()}
	for !p.atEnd() {
		switch p.kind() {
		case KwImport:
			if imp := p.parseImport(); imp != nil {
				f.Imports = append(f.Imports, imp)
			}
		case KwParam:
			if d := p.parseParamDecl(); d != nil {
				f.Params = append(f.Params, d)
			}
		case KwDef:
			if g := p.parseFuncGroup(); g != nil {
				f.Funcs = append(f.Funcs, g)
			}
		default:
			p.errorf(p.pos(), "expected `def`, `param` or `import` at the top level, found %s", p.cur())
			p.syncToDef()
		}
	}
	return f
}

func (p *parser) parseImport() *Import {
	pos := p.advance().Pos // `import`
	p.expect(LParen, "`(` after import")
	t := p.cur()
	if t.Kind != String {
		p.errorf(t.Pos, "import needs a quoted path, found %s", t)
		p.syncToDef()
		return nil
	}
	p.advance()
	p.expect(RParen, "`)` to close import")
	return &Import{Path: t.Text, Pos_: pos}
}

// parseParamDecl reads `param name type` with an optional `= literal`.
//
// The type is mandatory. It is not there for the interpreter — flang checks
// nothing else at parse time — but for the editor, which draws a widget per
// param and has nothing else to choose one from. A param with no type would be
// a text box, which is how `door_ref` becomes a typo nobody notices.
func (p *parser) parseParamDecl() *ParamDecl {
	pos := p.advance().Pos // `param`

	nameTok := p.cur()
	if nameTok.Kind != Ident {
		p.errorf(nameTok.Pos, "expected a parameter name after `param`, found %s", nameTok)
		p.syncToDef()
		return nil
	}
	p.advance()

	typeTok := p.cur()
	if typeTok.Kind != Ident {
		p.errorf(typeTok.Pos, "`param %s` needs a type; write one of %s",
			nameTok.Text, joinWords(ParamTypes))
		p.syncToDef()
		return nil
	}
	p.advance()
	if !IsParamType(typeTok.Text) {
		// Named separately from the language's own type list: a param is stored
		// in the level file and edited in a panel, so `list` and `dict` are
		// wrong here even though they are perfectly good types elsewhere.
		p.errorf(typeTok.Pos, "%q is not a parameter type; a param holds one of %s",
			typeTok.Text, joinWords(ParamTypes))
	}

	d := &ParamDecl{Name: nameTok.Text, Type: typeTok.Text, Pos_: pos}
	if p.accept(Assign) {
		lit := p.parseUnary()
		if lit == nil {
			p.syncToDef()
			return d
		}
		if !isLiteralExpr(lit) {
			p.errorf(lit.Position(), "a parameter's default is a literal value, not an expression")
			return d
		}
		d.Default = lit
	}
	return d
}

// joinWords renders a short list for a diagnostic: `a, b or c`.
func joinWords(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " or " + words[len(words)-1]
}

// ---------- functions ----------

// parseFuncGroup reads `def clause { clause } end`.
//
// Only the first clause carries `def` and the return type; the rest are bare
// heads. That is the collapsed form from SCRIPTING.md, and it is the only form —
// a second `def` starts a new group.
func (p *parser) parseFuncGroup() *FuncGroup {
	pos := p.advance().Pos // `def`

	g := &FuncGroup{Pos_: pos}
	prevGroup := p.group
	defer func() { p.group = prevGroup }()

	first := p.parseClause(g, true)
	if first == nil {
		p.syncToDef()
		return nil
	}
	g.Name = first.Name
	g.Clauses = append(g.Clauses, first)

	for !p.atEnd() && p.kind() != KwEnd {
		// A bare head is an identifier followed by `(`. Anything else here is a
		// statement that escaped its clause body, which means the body was
		// mis-parsed — complain once and give up on the group.
		if p.kind() != Ident || p.peekKind(1) != LParen {
			p.errorf(p.pos(), "expected another clause or `end`, found %s", p.cur())
			p.syncToDef()
			return g
		}
		c := p.parseClause(g, false)
		if c == nil {
			p.syncToDef()
			return g
		}
		if c.Name != g.Name {
			p.errorf(c.Pos_, "clause %q is inside `def %s`; every clause in a group shares one name",
				c.Name, g.Name)
		}
		if len(c.Params) != g.Arity() {
			p.errorf(c.Pos_, "clause %s takes %d parameters but %s takes %d; clauses in a group share an arity",
				c.Signature(), len(c.Params), g.Name, g.Arity())
		}
		g.Clauses = append(g.Clauses, c)
	}
	p.expect(KwEnd, "`end` to close the definition")
	return g
}

// parseClause reads one head and body. The first clause of a group may carry a
// return type, which is recorded on the group rather than on the clause.
func (p *parser) parseClause(g *FuncGroup, first bool) *Clause {
	nameTok := p.cur()
	if nameTok.Kind != Ident {
		p.errorf(nameTok.Pos, "expected a function name, found %s", nameTok)
		return nil
	}
	p.advance()

	c := &Clause{Name: nameTok.Text, Pos_: nameTok.Pos}
	// In scope from here on, so the body below can tell a bare head of this same
	// group from an ordinary call statement.
	p.group = c.Name
	if !p.accept(LParen) {
		p.errorf(p.pos(), "expected `(` after %q, found %s", c.Name, p.cur())
		return nil
	}
	if p.kind() != RParen {
		for {
			prm, ok := p.parseParam()
			if !ok {
				return nil
			}
			c.Params = append(c.Params, prm)
			if !p.accept(Comma) {
				break
			}
		}
	}
	if !p.accept(RParen) {
		p.errorf(p.pos(), "expected `)` to close %q's parameters, found %s", c.Name, p.cur())
		return nil
	}

	if p.accept(KwWhen) {
		c.Guard = p.parseExpr()
	}

	if p.accept(Arrow) {
		ret, tuple := p.parseReturnType()
		if !first {
			// Allowing a per-clause return type would let two clauses of one
			// function disagree about what it returns, which is a contradiction
			// rather than a preference.
			p.errorf(nameTok.Pos, "only the first clause of `def %s` may declare a return type", g.Name)
		} else {
			g.RetType, g.RetTuple = ret, tuple
		}
	}

	c.Body = p.parseBlock()
	return c
}

// parseParam reads one parameter: an atom, a literal, a bare name, or a name
// with a type.
func (p *parser) parseParam() (Param, bool) {
	t := p.cur()
	switch t.Kind {
	case Atom:
		p.advance()
		return Param{Kind: ParamAtom, Atom: t.Text, Pos_: t.Pos}, true

	case Int, Float, String, KwTrue, KwFalse, KwNull, Minus:
		// A literal parameter matches one exact value. Parsed through the unary
		// level so `-1` is a literal rather than a syntax error.
		lit := p.parseUnary()
		if lit == nil {
			return Param{}, false
		}
		if !isLiteralExpr(lit) {
			p.errorf(t.Pos, "a parameter can match a literal value, but not an expression")
			return Param{}, false
		}
		return Param{Kind: ParamLiteral, Lit: lit, Pos_: t.Pos}, true

	case Ident:
		p.advance()
		// A second identifier is the type. `n int` is typed; a bare `n` binds
		// anything. Nothing else can follow a parameter name.
		if p.kind() == Ident {
			typ := p.advance()
			if !IsTypeName(typ.Text) {
				p.errorf(typ.Pos, "%q is not a type; expected one of int, float, str, bool, atom, ref, list, dict, tuple, vec",
					typ.Text)
			}
			return Param{Kind: ParamTyped, Name: t.Text, Type: typ.Text, Pos_: t.Pos}, true
		}
		return Param{Kind: ParamBind, Name: t.Text, Pos_: t.Pos}, true
	}

	p.errorf(t.Pos, "expected a parameter, found %s", t)
	return Param{}, false
}

// isLiteralExpr reports whether e is a value the parser can match against
// directly, including a negated number.
func isLiteralExpr(e Expr) bool {
	switch v := e.(type) {
	case *IntLit, *FloatLit, *StringLit, *BoolLit, *NullLit, *AtomLit:
		return true
	case *UnaryExpr:
		if v.Op != Minus {
			return false
		}
		switch v.X.(type) {
		case *IntLit, *FloatLit:
			return true
		}
	}
	return false
}

// parseReturnType reads the type after `->`: one name, or a parenthesised tuple
// of them.
func (p *parser) parseReturnType() (single string, tuple []string) {
	if p.accept(LParen) {
		for {
			t := p.cur()
			if t.Kind != Ident && t.Kind != KwNull {
				p.errorf(t.Pos, "expected a type name, found %s", t)
				break
			}
			p.advance()
			if t.Kind == Ident && !IsTypeName(t.Text) {
				p.errorf(t.Pos, "%q is not a type", t.Text)
			}
			tuple = append(tuple, t.Text)
			if !p.accept(Comma) {
				break
			}
		}
		p.expect(RParen, "`)` to close the return type")
		return "", tuple
	}

	t := p.cur()
	switch t.Kind {
	case KwNull:
		p.advance()
		return "null", nil
	case Ident:
		p.advance()
		if !IsTypeName(t.Text) {
			p.errorf(t.Pos, "%q is not a type", t.Text)
		}
		return t.Text, nil
	}
	p.errorf(t.Pos, "expected a return type after `->`, found %s", t)
	return "", nil
}

// ---------- statements ----------

// parseBlock reads statements until a block terminator, which it does not
// consume — the caller decides whether `end`, `else` or `elif` is the one it
// wanted.
func (p *parser) parseBlock() []Stmt {
	var out []Stmt
	for !p.atEnd() {
		switch p.kind() {
		case KwEnd, KwElse, KwElif, KwDef:
			return out
		case Ident:
			// A bare head starting the next clause of the same group ends this
			// body. Distinguishing it from a call statement takes real lookahead:
			// both are `name(`. A clause head is one whose closing paren is
			// followed by `when`, `->`, or something that can only start a
			// statement — a call statement's paren is followed by an operator, a
			// terminator, or nothing.
			if p.looksLikeClauseHead() {
				return out
			}
		}
		s := p.parseStmt()
		if s == nil {
			p.syncStmt()
			continue
		}
		out = append(out, s)
	}
	return out
}

// looksLikeClauseHead reports whether the tokens at the cursor begin another
// clause of the enclosing group rather than a statement.
//
// `name(` is both a clause head and a call, so something has to separate them.
// The deciding rule is that a bare head repeats its group's name: inside
// `def fib`, `fib(0)` is a head and `log("x")` is a call. On top of that the
// parser scans to the matching `)` — `when` and `->` are conclusive, and an
// operator afterwards means the call was an expression rather than a head.
//
// The one construction this cannot tell apart is a statement that calls the
// enclosing function purely for effect and discards the result. That is not a
// thing a pure function is ever written to do, and giving it up buys an
// unambiguous collapsed clause form.
func (p *parser) looksLikeClauseHead() bool {
	if p.kind() != Ident || p.peekKind(1) != LParen {
		return false
	}
	if p.group == "" || p.cur().Text != p.group {
		return false
	}
	depth := 0
	for n := 1; ; n++ {
		switch p.peekKind(n) {
		case LParen:
			depth++
		case RParen:
			depth--
			if depth == 0 {
				switch p.peekKind(n + 1) {
				case KwWhen, Arrow:
					return true
				case Assign, PlusEq, MinusEq, StarEq, SlashEq,
					Plus, Minus, Star, Slash, Percent,
					Eq, NotEq, Lt, LtEq, Gt, GtEq, Send,
					Dot, LBracket, LParen, Comma, RParen,
					KwAnd, KwOr:
					return false
				case EOF:
					return false
				default:
					// Something that can only begin a new statement follows, so
					// this is a head whose body starts there.
					return true
				}
			}
		case EOF:
			return false
		}
	}
}

func (p *parser) parseStmt() Stmt {
	switch p.kind() {
	case KwIf:
		return p.parseIf()
	case KwFor:
		return p.parseFor()
	case KwWhile:
		return p.parseWhile()
	case KwReturn:
		pos := p.advance().Pos
		if p.startsExpr() {
			return &ReturnStmt{Value: p.parseExpr(), Pos_: pos}
		}
		return &ReturnStmt{Pos_: pos}
	case KwBreak:
		return &BreakStmt{Pos_: p.advance().Pos}
	case KwContinue:
		return &ContinueStmt{Pos_: p.advance().Pos}
	}
	return p.parseSimpleStmt()
}

// parseSimpleStmt reads an expression and then decides what it was: the target
// of an assignment, the target of a send, or a statement in its own right.
func (p *parser) parseSimpleStmt() Stmt {
	pos := p.pos()
	x := p.parseExpr()
	if x == nil {
		return nil
	}

	switch p.kind() {
	case Assign, PlusEq, MinusEq, StarEq, SlashEq:
		op := p.advance()
		if !isAssignable(x) {
			p.errorf(pos, "cannot assign to this")
		}
		val := p.parseExpr()
		if val == nil {
			return nil
		}
		return &AssignStmt{Target: x, Op: op.Kind, Value: val, Pos_: pos}

	case Send:
		p.advance()
		msg := p.parseExpr()
		if msg == nil {
			return nil
		}
		if delay, rest, isAfter := splitAfter(msg); isAfter {
			if rest == nil {
				p.errorf(msg.Position(), "after takes a delay in milliseconds and a message, like after(500, :open_done:)")
				return &SendStmt{Target: x, Msg: &TupleLit{Pos_: msg.Position()}, Pos_: pos}
			}
			return &SendStmt{Target: x, Msg: rest, Delay: delay, Pos_: pos}
		}
		if _, ok := msg.(*TupleLit); !ok {
			// The first element names the message, so a send always carries a
			// tuple. `@d <- (:open:,)` is the one-element form.
			p.errorf(msg.Position(), "a message is a tuple beginning with an atom, like (:damage:, @self, 10)")
		}
		return &SendStmt{Target: x, Msg: msg, Pos_: pos}
	}

	return &ExprStmt{X: x, Pos_: pos}
}

// splitAfter recognises the delayed send `target <- after(ms, :atom:, args...)`
// and takes it apart into the delay and the message the timer will carry.
//
// `after` is spelled as a call because that is how it reads, but it is not one:
// there is no value a delay could evaluate to, and the whole point of the form
// is that the send around it says who the message goes to. Recognising it here
// means the rest of the compiler never meets it — a delayed send is an ordinary
// send that happens to be holding a delay.
//
// ok is true as soon as the name matches, so a malformed `after` is reported as
// a bad timer rather than mistaken for a message that is not a tuple.
func splitAfter(msg Expr) (delay Expr, rest *TupleLit, ok bool) {
	call, isCall := msg.(*CallExpr)
	if !isCall {
		return nil, nil, false
	}
	name, isName := call.Fn.(*IdentExpr)
	if !isName || name.Name != "after" {
		return nil, nil, false
	}
	if len(call.Args) < 2 {
		return nil, nil, true
	}
	return call.Args[0], &TupleLit{Elems: call.Args[1:], Pos_: call.Args[1].Position()}, true
}

// isAssignable reports whether e can be the target of an assignment: a local, or
// a path into an object.
func isAssignable(e Expr) bool {
	switch e.(type) {
	case *IdentExpr, *MemberExpr, *IndexExpr, *AddrExpr, *TagExpr, *ParamExpr:
		return true
	}
	return false
}

func (p *parser) parseIf() Stmt {
	pos := p.advance().Pos // `if`
	s := &IfStmt{Pos_: pos}
	s.Conds = append(s.Conds, p.parseExpr())
	s.Blocks = append(s.Blocks, p.parseBlock())

	for p.kind() == KwElif {
		p.advance()
		s.Conds = append(s.Conds, p.parseExpr())
		s.Blocks = append(s.Blocks, p.parseBlock())
	}
	if p.accept(KwElse) {
		s.Else = p.parseBlock()
		if s.Else == nil {
			// An empty else is legal but has to stay distinguishable from no
			// else at all, or `else end` would silently vanish.
			s.Else = []Stmt{}
		}
	}
	p.expect(KwEnd, "`end` to close the if")
	return s
}

func (p *parser) parseFor() Stmt {
	pos := p.advance().Pos // `for`
	// expect consumes on success, so the name must not be advanced past again.
	name := p.expect(Ident, "a loop variable")
	p.expect(KwIn, "`in`")
	seq := p.parseExpr()
	body := p.parseBlock()
	p.expect(KwEnd, "`end` to close the for")
	return &ForStmt{Name: name.Text, Seq: seq, Body: body, Pos_: pos}
}

func (p *parser) parseWhile() Stmt {
	pos := p.advance().Pos // `while`
	cond := p.parseExpr()
	body := p.parseBlock()
	p.expect(KwEnd, "`end` to close the while")
	return &WhileStmt{Cond: cond, Body: body, Pos_: pos}
}

// ---------- expressions ----------

// startsExpr reports whether the current token could begin an expression. Used
// to tell `return` from `return x` without lookahead games.
func (p *parser) startsExpr() bool {
	switch p.kind() {
	case Int, Float, String, Atom, Ident,
		KwTrue, KwFalse, KwNull, KwNot,
		Minus, Lt, LParen, LBracket, LBrace, At, Hash, Question:
		return true
	}
	return false
}

// precedence gives each infix operator its binding power. Higher binds tighter.
// Anything absent is not an infix operator, which is how the expression parser
// knows to stop.
func precedence(k Kind) int {
	switch k {
	case KwOr:
		return 1
	case KwAnd:
		return 2
	case Eq, NotEq:
		return 3
	case Lt, LtEq, Gt, GtEq:
		return 4
	case Plus, Minus:
		return 5
	case Star, Slash, Percent:
		return 6
	}
	return 0
}

func (p *parser) parseExpr() Expr { return p.parseBinary(1) }

// parseBinary is the precedence-climbing loop. Everything it reads after the
// first operand is in *infix* position, which is what makes `<` here mean
// less-than while the same token in parseUnary opens a vec.
func (p *parser) parseBinary(minPrec int) Expr {
	left := p.parseUnary()
	if left == nil {
		return nil
	}
	for {
		op := p.kind()
		prec := precedence(op)
		if prec < minPrec {
			return left
		}
		opTok := p.advance()
		// Left-associative: the right operand binds only tighter operators.
		right := p.parseBinary(prec + 1)
		if right == nil {
			return left
		}
		left = &BinaryExpr{Op: op, L: left, R: right, Pos_: opTok.Pos}
	}
}

func (p *parser) parseUnary() Expr {
	switch p.kind() {
	case Minus, KwNot:
		t := p.advance()
		x := p.parseUnary()
		if x == nil {
			return nil
		}
		return &UnaryExpr{Op: t.Kind, X: x, Pos_: t.Pos}
	}
	return p.parsePostfix()
}

// parsePostfix reads a primary and then any chain of member accesses, indexes
// and calls. `(` here is in infix position, so it is a call.
func (p *parser) parsePostfix() Expr {
	x := p.parsePrimary()
	if x == nil {
		return nil
	}
	for {
		switch p.kind() {
		case Dot:
			p.advance()
			// A member name may collide with a keyword — `.end` is not likely
			// but `.in` is plausible — so anything name-shaped is accepted here.
			t := p.cur()
			if t.Kind != Ident && keywords[t.Text] == 0 {
				p.errorf(t.Pos, "expected a name after `.`, found %s", t)
				return x
			}
			p.advance()
			x = &MemberExpr{X: x, Name: t.Text, Pos_: t.Pos}

		case LBracket:
			pos := p.advance().Pos
			idx := p.parseExpr()
			p.expect(RBracket, "`]`")
			x = &IndexExpr{X: x, Index: idx, Pos_: pos}

		case LParen:
			pos := p.advance().Pos
			var args []Expr
			if p.kind() != RParen {
				for {
					a := p.parseExpr()
					if a == nil {
						break
					}
					args = append(args, a)
					if !p.accept(Comma) {
						break
					}
				}
			}
			p.expect(RParen, "`)` to close the arguments")
			x = &CallExpr{Fn: x, Args: args, Pos_: pos}

		default:
			return x
		}
	}
}

func (p *parser) parsePrimary() Expr {
	t := p.cur()
	switch t.Kind {
	case Int:
		p.advance()
		n, err := strconv.ParseInt(t.Text, 10, 64)
		if err != nil {
			p.errorf(t.Pos, "%q does not fit in an int", t.Text)
		}
		return &IntLit{Value: n, Pos_: t.Pos, val: IntVal(n)}

	case Float:
		p.advance()
		f, err := strconv.ParseFloat(t.Text, 64)
		if err != nil {
			p.errorf(t.Pos, "%q is not a valid float", t.Text)
		}
		return &FloatLit{Value: f, Pos_: t.Pos, val: FloatVal(f)}

	case String:
		p.advance()
		return &StringLit{Value: t.Text, Pos_: t.Pos, val: StrVal(t.Text)}

	case Atom:
		p.advance()
		return &AtomLit{Name: t.Text, Pos_: t.Pos, val: AtomVal(t.Text)}

	case KwTrue:
		p.advance()
		return &BoolLit{Value: true, Pos_: t.Pos}
	case KwFalse:
		p.advance()
		return &BoolLit{Value: false, Pos_: t.Pos}
	case KwNull:
		p.advance()
		return &NullLit{Pos_: t.Pos}

	case Ident:
		p.advance()
		return &IdentExpr{Name: t.Text, Pos_: t.Pos}

	case At:
		p.advance()
		n := p.cur()
		if n.Kind != Ident {
			p.errorf(n.Pos, "expected an ID after `@`, found %s", n)
			return &AddrExpr{Pos_: t.Pos}
		}
		p.advance()
		return &AddrExpr{Name: n.Text, Pos_: t.Pos}

	case Hash:
		p.advance()
		n := p.cur()
		if n.Kind != Ident {
			p.errorf(n.Pos, "expected a tag after `#`, found %s", n)
			return &TagExpr{Pos_: t.Pos}
		}
		p.advance()
		return &TagExpr{Name: n.Text, Pos_: t.Pos}

	case Question:
		p.advance()
		n := p.cur()
		if n.Kind != Ident {
			p.errorf(n.Pos, "expected a parameter name after `?`, found %s", n)
			return &ParamExpr{Pos_: t.Pos}
		}
		p.advance()
		return &ParamExpr{Name: n.Text, Pos_: t.Pos}

	case Lt:
		// Prefix position: a vec literal.
		return p.parseVec()

	case LParen:
		// Prefix position: a tuple, or a parenthesised group.
		return p.parseParenOrTuple()

	case LBracket:
		return p.parseList()

	case LBrace:
		return p.parseDict()
	}

	p.errorf(t.Pos, "expected an expression, found %s", t)
	return nil
}

// parseVec reads `<x, y, z>`. Exactly three components: a vec is a point or a
// direction, and a two- or four-element one is a mistake rather than a variant.
func (p *parser) parseVec() Expr {
	pos := p.advance().Pos // `<`
	v := &VecLit{Pos_: pos}
	comps := make([]Expr, 0, 3)
	for {
		// Components are parsed above the ordering operators, so the closing `>`
		// cannot be read as greater-than and swallow the rest of the literal.
		// Arithmetic still works — `<x + 1, y, z>` is fine — but a bare
		// comparison inside a vec has to be parenthesised, which SCRIPTING.md
		// says outright.
		e := p.parseBinary(precedence(Gt) + 1)
		if e == nil {
			return nil
		}
		comps = append(comps, e)
		if !p.accept(Comma) {
			break
		}
	}
	if !p.accept(Gt) {
		p.errorf(p.pos(), "expected `>` to close the vec, found %s", p.cur())
		return nil
	}
	if len(comps) != 3 {
		p.errorf(pos, "a vec has three components, found %d", len(comps))
		for len(comps) < 3 {
			comps = append(comps, &FloatLit{Pos_: pos})
		}
	}
	v.X, v.Y, v.Z = comps[0], comps[1], comps[2]
	return v
}

// parseParenOrTuple reads `(…)`. A comma anywhere inside makes it a tuple; a
// single expression with no comma is just grouping and returns the inner
// expression unwrapped. `(x,)` is the one-tuple.
func (p *parser) parseParenOrTuple() Expr {
	pos := p.advance().Pos // `(`

	if p.accept(RParen) {
		// The empty tuple. Rare, but it is what `()` has to mean, and rejecting
		// it would make the grammar special-case its own smallest case.
		return &TupleLit{Pos_: pos}
	}

	var elems []Expr
	sawComma := false
	for {
		e := p.parseExpr()
		if e == nil {
			return nil
		}
		elems = append(elems, e)
		if !p.accept(Comma) {
			break
		}
		sawComma = true
		if p.kind() == RParen {
			// A trailing comma, which is what makes `(x,)` a one-tuple.
			break
		}
	}
	if !p.accept(RParen) {
		p.errorf(p.pos(), "expected `)`, found %s", p.cur())
		return nil
	}
	if !sawComma && len(elems) == 1 {
		return elems[0] // grouping
	}
	return &TupleLit{Elems: elems, Pos_: pos}
}

func (p *parser) parseList() Expr {
	pos := p.advance().Pos // `[`
	l := &ListLit{Pos_: pos}
	if p.accept(RBracket) {
		return l
	}
	for {
		e := p.parseExpr()
		if e == nil {
			return nil
		}
		l.Elems = append(l.Elems, e)
		if !p.accept(Comma) {
			break
		}
		if p.kind() == RBracket {
			break // trailing comma
		}
	}
	p.expect(RBracket, "`]` to close the list")
	return l
}

// parseDict reads `{"k": v, …}`. Keys must be string literals: a computed key
// would make `{a: 1}` ambiguous between a key and a variable named a, and the
// language has no use for the general case that would buy.
func (p *parser) parseDict() Expr {
	pos := p.advance().Pos // `{`
	d := &DictLit{Pos_: pos}
	if p.accept(RBrace) {
		return d
	}
	for {
		k := p.cur()
		if k.Kind != String {
			p.errorf(k.Pos, "a dict key is a quoted string, found %s", k)
			return nil
		}
		p.advance()
		p.expect(Colon, "`:` after the key")
		v := p.parseExpr()
		if v == nil {
			return nil
		}
		d.Keys = append(d.Keys, k.Text)
		d.Values = append(d.Values, v)
		if !p.accept(Comma) {
			break
		}
		if p.kind() == RBrace {
			break // trailing comma
		}
	}
	p.expect(RBrace, "`}` to close the dict")
	return d
}
