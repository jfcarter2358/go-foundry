package flang

// ast.go — the shape of a parsed script.
//
// Every node carries a Pos, because nearly everything that reads the tree wants
// to say where something is: the parser's own diagnostics, the interpreter's
// runtime errors, the script editor's error gutter, and eventually a debugger's
// breakpoints — the AST node being evaluated is the program counter, so a node
// without a position could not be broken on.
//
// Nodes are structs behind small interfaces rather than one tagged union,
// because the interpreter dispatches on type and a type switch over concrete
// types is both faster and harder to get wrong than a switch over a tag.

import (
	"strconv"
	"strings"
)

// Node is anything in the tree.
type Node interface {
	Position() Pos
}

// Expr is a node that produces a value.
type Expr interface {
	Node
	exprNode()
}

// Stmt is a node that performs an action.
type Stmt interface {
	Node
	stmtNode()
}

// ---------- file ----------

// File is one parsed .flang source file.
type File struct {
	Path    string
	Imports []*Import
	Params  []*ParamDecl
	Funcs   []*FuncGroup
	Pos_    Pos
}

func (f *File) Position() Pos { return f.Pos_ }

// Func returns the group defining name, or nil.
func (f *File) Func(name string) *FuncGroup {
	for _, g := range f.Funcs {
		if g.Name == name {
			return g
		}
	}
	return nil
}

// Import is `import("path")`.
type Import struct {
	Path string
	Pos_ Pos
}

func (i *Import) Position() Pos { return i.Pos_ }

// ---------- parameters ----------

// ParamDecl is a top-level `param name type` — a value the *level* supplies and
// the script reads as `@name`.
//
// This is the seam that makes one script reusable across many objects. A button
// script that hard-codes `@front_door` is a script for one door; the same script
// written against `param door ref` is a script for every door, with each button
// in the level naming its own.
//
// A declaration is not storage. The value lives on whichever object runs the
// script, alongside its tags and attributes, so a hundred buttons sharing one
// .flang file have a hundred separate bindings — see script_params.go on the
// engine side.
//
// Default is restricted to a literal. An expression would need an evaluator and
// a `self` to evaluate it against, and a param's whole purpose is to be known
// before anything runs: the editor has to show the fallback in a widget, which
// means it must be a value rather than a computation.
type ParamDecl struct {
	Name string
	// Type is one of the scalar type names, or "ref". Empty is not allowed: an
	// untyped param would leave the editor with no widget to draw.
	Type string
	// Default is the literal after `=`, or nil when the declaration has none.
	Default Expr
	Pos_    Pos
}

func (d *ParamDecl) Position() Pos { return d.Pos_ }

// ParamTypes are the types a param may declare, in the order the editor lists
// them. Deliberately narrower than the language's: a param has to round-trip
// through a level file and be editable in a panel, and neither is true of a list
// or a dict. Anything compound belongs in `@self.state`.
var ParamTypes = []string{"ref", "int", "float", "bool", "str"}

// IsParamType reports whether name is a type a param may declare.
func IsParamType(name string) bool {
	for _, t := range ParamTypes {
		if t == name {
			return true
		}
	}
	return false
}

// DefaultValue evaluates the declaration's default, which is a literal and so
// needs no environment. The bool is false when there is no default, which reads
// as null.
func (d *ParamDecl) DefaultValue() (Value, bool) {
	if d == nil || d.Default == nil {
		return nil, false
	}
	// literalValue lives in pattern.go, where a literal *parameter* uses it for
	// the same reason: a closed form needs no environment to evaluate.
	return literalValue(d.Default)
}

// ---------- functions ----------

// FuncGroup is one `def` block: a name, a return type, and one or more clauses
// that share both.
//
// Clauses are stored in the order they were written. Dispatch order is decided
// separately, by specificity, and is not this order — see SCRIPTING.md. Keeping
// the source order here means a diagnostic can point at the clause the author
// actually wrote rather than at its rank.
type FuncGroup struct {
	Name     string
	RetType  string   // a type name, "null", or "" when the author wrote none
	RetTuple []string // set instead of RetType when the return is a tuple
	Clauses  []*Clause
	Pos_     Pos
}

func (g *FuncGroup) Position() Pos { return g.Pos_ }

// Arity returns the number of parameters the group's clauses take. Every clause
// in a group shares it — the parser rejects a group where they do not.
func (g *FuncGroup) Arity() int {
	if len(g.Clauses) == 0 {
		return 0
	}
	return len(g.Clauses[0].Params)
}

// Clause is one head-and-body pair.
type Clause struct {
	Name   string
	Params []Param
	Guard  Expr // nil when the clause has no `when`
	Body   []Stmt
	Pos_   Pos
}

func (c *Clause) Position() Pos { return c.Pos_ }

// ParamKind is how one parameter matches.
type ParamKind int

const (
	// ParamBind matches anything and binds it.
	ParamBind ParamKind = iota
	// ParamTyped matches a value of one type and binds it.
	ParamTyped
	// ParamLiteral matches one exact value and binds nothing.
	ParamLiteral
	// ParamAtom matches one atom and binds nothing. Separate from ParamLiteral
	// because an atom in the first position is what makes a clause an event
	// handler, and the dispatcher indexes on it.
	ParamAtom
)

// Param is one parameter in a clause head.
type Param struct {
	Kind ParamKind
	Name string // ParamBind, ParamTyped
	Type string // ParamTyped
	Lit  Expr   // ParamLiteral
	Atom string // ParamAtom
	Pos_ Pos
}

func (p Param) Position() Pos { return p.Pos_ }

// Specificity scores how tightly a parameter constrains its argument. Higher is
// stricter, and the dispatcher compares clauses element-wise on these — see
// SCRIPTING.md's "Which clause wins".
func (p Param) Specificity() int {
	switch p.Kind {
	case ParamLiteral, ParamAtom:
		return 3
	case ParamTyped:
		return 1
	default:
		return 0
	}
}

// Signature renders a clause head for diagnostics: `on_damage(:damage:, n int)`.
func (c *Clause) Signature() string {
	var b strings.Builder
	b.WriteString(c.Name)
	b.WriteByte('(')
	for i, p := range c.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		switch p.Kind {
		case ParamAtom:
			b.WriteString(":" + p.Atom + ":")
		case ParamTyped:
			b.WriteString(p.Name + " " + p.Type)
		case ParamLiteral:
			b.WriteString(exprText(p.Lit))
		default:
			b.WriteString(p.Name)
		}
	}
	b.WriteByte(')')
	return b.String()
}

// ---------- statements ----------

// AssignStmt is `target = value`, including the compound forms. Op is Assign for
// a plain assignment and PlusEq/MinusEq/StarEq/SlashEq otherwise.
type AssignStmt struct {
	Target Expr
	Op     Kind
	Value  Expr
	Pos_   Pos
}

// SendStmt is `target <- (:atom:, args...)`, or `target <- after(ms, :atom:,
// args...)` when the message is on a timer.
//
// Msg is kept as a tuple expression rather than being split into an atom and
// arguments, because the atom may be computed — `@d <- (pick(), 1)` is legal —
// and the runtime resolves it when the message is posted.
//
// Delay holds the `after` form's millisecond expression and is nil for an
// immediate send. The parser strips the `after(...)` wrapper and leaves the
// message as the same tuple the immediate form writes by hand, so everything
// that walks a send — the linter's `@self` atom check, the interpreter's target
// resolution — sees one shape whether or not there is a delay on it.
type SendStmt struct {
	Target Expr
	Msg    Expr
	Delay  Expr // nil for an immediate send
	Pos_   Pos
}

// IfStmt is `if / elif… / else / end`. Conds and Blocks are index-aligned: entry
// i is the i-th condition and the body it guards, with Else holding the trailing
// unguarded block.
type IfStmt struct {
	Conds  []Expr
	Blocks [][]Stmt
	Else   []Stmt // nil when there is no else
	Pos_   Pos
}

// ForStmt is `for name in seq`.
type ForStmt struct {
	Name string
	Seq  Expr
	Body []Stmt
	Pos_ Pos
}

// WhileStmt is `while cond`.
type WhileStmt struct {
	Cond Expr
	Body []Stmt
	Pos_ Pos
}

// ReturnStmt is `return` with an optional value.
type ReturnStmt struct {
	Value Expr // nil for a bare `return`, which yields null
	Pos_  Pos
}

// BreakStmt and ContinueStmt need no payload.
type BreakStmt struct{ Pos_ Pos }
type ContinueStmt struct{ Pos_ Pos }

// ExprStmt is an expression evaluated for its effect — in practice a call.
type ExprStmt struct {
	X    Expr
	Pos_ Pos
}

func (s *AssignStmt) Position() Pos   { return s.Pos_ }
func (s *SendStmt) Position() Pos     { return s.Pos_ }
func (s *IfStmt) Position() Pos       { return s.Pos_ }
func (s *ForStmt) Position() Pos      { return s.Pos_ }
func (s *WhileStmt) Position() Pos    { return s.Pos_ }
func (s *ReturnStmt) Position() Pos   { return s.Pos_ }
func (s *BreakStmt) Position() Pos    { return s.Pos_ }
func (s *ContinueStmt) Position() Pos { return s.Pos_ }
func (s *ExprStmt) Position() Pos     { return s.Pos_ }

func (*AssignStmt) stmtNode()   {}
func (*SendStmt) stmtNode()     {}
func (*IfStmt) stmtNode()       {}
func (*ForStmt) stmtNode()      {}
func (*WhileStmt) stmtNode()    {}
func (*ReturnStmt) stmtNode()   {}
func (*BreakStmt) stmtNode()    {}
func (*ContinueStmt) stmtNode() {}
func (*ExprStmt) stmtNode()     {}

// ---------- expressions ----------

// IntLit, FloatLit, StringLit, BoolLit, NullLit and AtomLit are the scalars.
type IntLit struct {
	Value int64
	Pos_  Pos
}
type FloatLit struct {
	Value float64
	Pos_  Pos
}
type StringLit struct {
	Value string
	Pos_  Pos
}
type BoolLit struct {
	Value bool
	Pos_  Pos
}
type NullLit struct{ Pos_ Pos }
type AtomLit struct {
	Name string
	Pos_ Pos
}

// VecLit is `<x, y, z>`, recognised only where an expression is expected.
type VecLit struct {
	X, Y, Z Expr
	Pos_    Pos
}

// ListLit is `[a, b, c]`.
type ListLit struct {
	Elems []Expr
	Pos_  Pos
}

// DictLit is `{"k": v, …}`. Keys are parallel to Values.
type DictLit struct {
	Keys   []string
	Values []Expr
	Pos_   Pos
}

// TupleLit is `(a, b)` — a parenthesised group with at least one comma. A
// one-tuple is written `(x,)`; plain `(x)` is grouping and produces no node of
// its own.
type TupleLit struct {
	Elems []Expr
	Pos_  Pos
}

// IdentExpr is a bare name: a local, or a builtin being called.
type IdentExpr struct {
	Name string
	Pos_ Pos
}

// AddrExpr is `@name` — one object by ID. `@self` and `@level` are ordinary
// names here; the interpreter gives them their meaning.
type AddrExpr struct {
	Name string
	Pos_ Pos
}

// TagExpr is `#name` — every object carrying a tag, as a list.
type TagExpr struct {
	Name string
	Pos_ Pos
}

// ParamExpr is `?name` — one declared script parameter, as the level bound it.
//
// Spelled with its own sigil rather than sharing `@` because the two ask
// different questions: `@` names an object in the level, `?` names a value the
// level was asked to supply. A `ref` param answers with an object, which is why
// the confusion was possible at all, but a `float` one never did.
type ParamExpr struct {
	Name string
	Pos_ Pos
}

// UnaryExpr is `-x` or `not x`.
type UnaryExpr struct {
	Op   Kind
	X    Expr
	Pos_ Pos
}

// BinaryExpr is any infix operator.
type BinaryExpr struct {
	Op   Kind
	L, R Expr
	Pos_ Pos
}

// CallExpr is `f(args…)`.
type CallExpr struct {
	Fn   Expr
	Args []Expr
	Pos_ Pos
}

// IndexExpr is `x[i]`.
type IndexExpr struct {
	X, Index Expr
	Pos_     Pos
}

// MemberExpr is `x.name` — the whole of the `.attr.`, `.prop.`, `.state`,
// `.floor`, `.edge.<id>` surface. It is left as a chain of plain member accesses
// rather than being folded into a dedicated node, because the interpreter needs
// to walk the chain anyway to know whether `.attr` is followed by a name.
type MemberExpr struct {
	X    Expr
	Name string
	Pos_ Pos
}

func (e *IntLit) Position() Pos     { return e.Pos_ }
func (e *FloatLit) Position() Pos   { return e.Pos_ }
func (e *StringLit) Position() Pos  { return e.Pos_ }
func (e *BoolLit) Position() Pos    { return e.Pos_ }
func (e *NullLit) Position() Pos    { return e.Pos_ }
func (e *AtomLit) Position() Pos    { return e.Pos_ }
func (e *VecLit) Position() Pos     { return e.Pos_ }
func (e *ListLit) Position() Pos    { return e.Pos_ }
func (e *DictLit) Position() Pos    { return e.Pos_ }
func (e *TupleLit) Position() Pos   { return e.Pos_ }
func (e *IdentExpr) Position() Pos  { return e.Pos_ }
func (e *AddrExpr) Position() Pos   { return e.Pos_ }
func (e *TagExpr) Position() Pos    { return e.Pos_ }
func (e *ParamExpr) Position() Pos  { return e.Pos_ }
func (e *UnaryExpr) Position() Pos  { return e.Pos_ }
func (e *BinaryExpr) Position() Pos { return e.Pos_ }
func (e *CallExpr) Position() Pos   { return e.Pos_ }
func (e *IndexExpr) Position() Pos  { return e.Pos_ }
func (e *MemberExpr) Position() Pos { return e.Pos_ }

func (*IntLit) exprNode()     {}
func (*FloatLit) exprNode()   {}
func (*StringLit) exprNode()  {}
func (*BoolLit) exprNode()    {}
func (*NullLit) exprNode()    {}
func (*AtomLit) exprNode()    {}
func (*VecLit) exprNode()     {}
func (*ListLit) exprNode()    {}
func (*DictLit) exprNode()    {}
func (*TupleLit) exprNode()   {}
func (*IdentExpr) exprNode()  {}
func (*AddrExpr) exprNode()   {}
func (*TagExpr) exprNode()    {}
func (*ParamExpr) exprNode()  {}
func (*UnaryExpr) exprNode()  {}
func (*BinaryExpr) exprNode() {}
func (*CallExpr) exprNode()   {}
func (*IndexExpr) exprNode()  {}
func (*MemberExpr) exprNode() {}

// exprText renders an expression compactly, for a clause signature or a
// diagnostic. It handles the literal forms a parameter can hold and falls back
// to a placeholder for anything larger — a signature is a label, not a
// pretty-printer.
func exprText(e Expr) string {
	switch v := e.(type) {
	case *IntLit:
		return strconv.FormatInt(v.Value, 10)
	case *FloatLit:
		return strconv.FormatFloat(v.Value, 'g', -1, 64)
	case *StringLit:
		return strconv.Quote(v.Value)
	case *BoolLit:
		if v.Value {
			return "true"
		}
		return "false"
	case *NullLit:
		return "null"
	case *AtomLit:
		return ":" + v.Name + ":"
	case *IdentExpr:
		return v.Name
	case *AddrExpr:
		return "@" + v.Name
	case *TagExpr:
		return "#" + v.Name
	case *ParamExpr:
		return "?" + v.Name
	case *UnaryExpr:
		return v.Op.String() + exprText(v.X)
	case nil:
		return "<missing>"
	}
	return "…"
}
