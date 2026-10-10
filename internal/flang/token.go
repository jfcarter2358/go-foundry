package flang

// token.go — the lexical vocabulary of flang.
//
// This package is deliberately free of any Foundry type: no GL, no SDL, no
// imgui, and nothing from package main. The language can therefore be built and
// tested headlessly, which matters because a parser is exactly the kind of thing
// that wants a hundred small tests and none of them want a window. The engine
// reaches the language through the Host interface (host.go); the language never
// reaches back. This follows the internal/texdoc precedent — see
// RESTRUCTURE_PROPOSAL.md §5 Phase B.

import (
	"fmt"
	"sort"
)

// Kind identifies a token's lexical class.
type Kind int

const (
	EOF Kind = iota
	Invalid

	// Literals and names.
	Int
	Float
	String
	Atom  // :damage:
	Ident // a name, or a keyword the parser will recognise by Text

	// Keywords.
	KwDef
	KwEnd
	KwReturn
	KwIf
	KwElif
	KwElse
	KwFor
	KwIn
	KwWhile
	KwBreak
	KwContinue
	KwWhen
	KwAnd
	KwOr
	KwNot
	KwTrue
	KwFalse
	KwNull
	KwImport
	KwParam

	// Operators and punctuation.
	Plus
	Minus
	Star
	Slash
	Percent
	Assign   // =
	PlusEq   // +=
	MinusEq  // -=
	StarEq   // *=
	SlashEq  // /=
	Eq       // ==
	NotEq    // !=
	Lt       // <
	LtEq     // <=
	Gt       // >
	GtEq     // >=
	Send     // <-
	Arrow    // ->
	Dot      // .
	Comma    // ,
	Colon    // :
	LParen   // (
	RParen   // )
	LBracket // [
	RBracket // ]
	LBrace   // {
	RBrace   // }
	At       // @
	Hash     // #
	Question // ?
)

// kindNames is how each kind reads in an error message. Indexed by the kind, so
// adding one means adding its name here and nowhere else.
var kindNames = map[Kind]string{
	EOF: "end of file", Invalid: "invalid token",
	Int: "int", Float: "float", String: "string", Atom: "atom", Ident: "identifier",
	KwDef: "def", KwEnd: "end", KwReturn: "return", KwIf: "if", KwElif: "elif",
	KwElse: "else", KwFor: "for", KwIn: "in", KwWhile: "while", KwBreak: "break",
	KwContinue: "continue", KwWhen: "when", KwAnd: "and", KwOr: "or", KwNot: "not",
	KwTrue: "true", KwFalse: "false", KwNull: "null", KwImport: "import",
	KwParam: "param",
	Plus:    "+", Minus: "-", Star: "*", Slash: "/", Percent: "%",
	Assign: "=", PlusEq: "+=", MinusEq: "-=", StarEq: "*=", SlashEq: "/=",
	Eq: "==", NotEq: "!=", Lt: "<", LtEq: "<=", Gt: ">", GtEq: ">=",
	Send: "<-", Arrow: "->", Dot: ".", Comma: ",", Colon: ":",
	LParen: "(", RParen: ")", LBracket: "[", RBracket: "]",
	LBrace: "{", RBrace: "}", At: "@", Hash: "#", Question: "?",
}

// String renders a kind for diagnostics.
func (k Kind) String() string {
	if n, ok := kindNames[k]; ok {
		return n
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// keywords maps reserved words to their kinds. A name not in here lexes as an
// Ident, which is what lets `end` be a keyword while `ending` is a variable.
var keywords = map[string]Kind{
	"def": KwDef, "end": KwEnd, "return": KwReturn,
	"if": KwIf, "elif": KwElif, "else": KwElse,
	"for": KwFor, "in": KwIn, "while": KwWhile,
	"break": KwBreak, "continue": KwContinue, "when": KwWhen,
	"and": KwAnd, "or": KwOr, "not": KwNot,
	"true": KwTrue, "false": KwFalse, "null": KwNull,
	"import": KwImport, "param": KwParam,
}

// typeNames are the type names a parameter or return type may use. They are not
// keywords: `int` is a perfectly good variable name, and only a parameter's
// second position or a return type reads one as a type.
var typeNames = map[string]bool{
	"int": true, "float": true, "str": true, "bool": true,
	"atom": true, "ref": true, "list": true, "dict": true,
	"tuple": true, "vec": true, "null": true,
}

// IsTypeName reports whether s names a flang type.
func IsTypeName(s string) bool { return typeNames[s] }

// KeywordNames returns every reserved word, sorted.
//
// Exported for the same reason BuiltinNames is: the script editor's completion
// list reads this table rather than keeping a copy of it, so a keyword added to
// the language is offerable on the day it is added and cannot be forgotten.
func KeywordNames() []string {
	out := make([]string, 0, len(keywords))
	for k := range keywords {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TypeNames returns every name that may be written where the grammar expects a
// type — a parameter's second position or a return type — sorted.
//
// Wider than ParamTypes on purpose: that is the subset a *level* can store, and
// this is the subset the *language* will parse. A completion list offered while
// writing `def f(xs list)` wants this one.
func TypeNames() []string {
	out := make([]string, 0, len(typeNames))
	for n := range typeNames {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Pos is a source position. File is carried on every position rather than being
// implied by context, because an imported file's errors have to name themselves
// rather than whatever was being parsed when the import ran.
type Pos struct {
	File string
	Line int // 1-based
	Col  int // 1-based, in bytes
}

// String renders a position as file:line:col.
func (p Pos) String() string {
	if p.File == "" {
		return fmt.Sprintf("%d:%d", p.Line, p.Col)
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// Token is one lexeme: its class, its source text, and where it came from.
//
// Text carries the *source* spelling rather than a decoded value — the parser
// decodes numbers and unescapes strings, so a diagnostic can quote what the
// author actually wrote.
type Token struct {
	Kind Kind
	Text string
	Pos  Pos
}

// String renders a token for diagnostics, quoting the text where it adds
// something the kind name does not already say.
func (t Token) String() string {
	switch t.Kind {
	case Int, Float, String, Atom, Ident:
		return fmt.Sprintf("%s %q", t.Kind, t.Text)
	default:
		return t.Kind.String()
	}
}
