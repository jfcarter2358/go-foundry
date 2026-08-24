package flang

// lexer.go — source text to tokens.
//
// Three lexical decisions are worth knowing about, because each is a place the
// language could have been ambiguous and is not:
//
//   - `<-` and `->` are single tokens, lexed greedily. Without that, `@door <- x`
//     would read as a comparison against a negation.
//   - `--` starts a comment. That makes `a--b` a comment rather than a double
//     negation, the same hazard Lua has; write `a - (-b)`.
//   - `:name:` is an atom, but a bare `:` is the dict separator. The lexer looks
//     ahead for the closing colon and only forms an atom when it finds one, so
//     `{"k": v}` still separates and `{"k": :on:}` still holds an atom.
//
// What the lexer deliberately does *not* decide is whether `<` opens a vec or
// means less-than, or whether `(` opens a tuple or a call. Both depend on
// whether an expression is expected or has just been read, which is a question
// only the parser can answer — see parser.go.

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lexer turns source into tokens, collecting diagnostics rather than stopping.
type Lexer struct {
	src  string
	file string
	pos  int // byte offset of the next rune
	line int
	col  int
	errs ErrorList
}

// NewLexer returns a lexer over src, whose positions name file.
func NewLexer(file string, src []byte) *Lexer {
	return &Lexer{src: string(src), file: file, line: 1, col: 1}
}

// Errors returns the diagnostics collected so far.
func (l *Lexer) Errors() ErrorList { return l.errs }

// here is the current position.
func (l *Lexer) here() Pos { return Pos{File: l.file, Line: l.line, Col: l.col} }

// errorf records a diagnostic at pos.
func (l *Lexer) errorf(pos Pos, format string, args ...any) {
	l.errs = append(l.errs, Error{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// peek returns the rune at the current offset without consuming it, or 0 at EOF.
func (l *Lexer) peek() rune {
	if l.pos >= len(l.src) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.src[l.pos:])
	return r
}

// peekAt returns the rune n bytes ahead of the current offset, or 0.
func (l *Lexer) peekAt(n int) rune {
	if l.pos+n >= len(l.src) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.src[l.pos+n:])
	return r
}

// next consumes and returns one rune, tracking line and column.
func (l *Lexer) next() rune {
	if l.pos >= len(l.src) {
		return 0
	}
	r, w := utf8.DecodeRuneInString(l.src[l.pos:])
	l.pos += w
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col += w
	}
	return r
}

// skipSpaceAndComments advances past whitespace and `--` comments. Newlines are
// whitespace: flang's blocks are delimited by keywords, never by layout, so a
// statement may wrap however the author likes.
func (l *Lexer) skipSpaceAndComments() {
	for {
		r := l.peek()
		switch {
		case r == 0:
			return
		case unicode.IsSpace(r):
			l.next()
		case r == '-' && l.peekAt(1) == '-':
			for l.peek() != 0 && l.peek() != '\n' {
				l.next()
			}
		default:
			return
		}
	}
}

// Next returns the next token. At end of input it returns EOF indefinitely, so
// a caller that forgets to stop loops harmlessly rather than panicking.
func (l *Lexer) Next() Token {
	l.skipSpaceAndComments()
	start := l.here()
	r := l.peek()
	if r == 0 {
		return Token{Kind: EOF, Pos: start}
	}

	switch {
	case isIdentStart(r):
		return l.lexIdent(start)
	case r >= '0' && r <= '9':
		return l.lexNumber(start)
	case r == '"':
		return l.lexString(start)
	case r == ':':
		return l.lexColon(start)
	}
	return l.lexOperator(start)
}

// Tokens lexes the whole input, ending with an EOF token. Convenient for tests
// and for the parser, which wants random access for its one-token lookahead.
func (l *Lexer) Tokens() []Token {
	var out []Token
	for {
		t := l.Next()
		out = append(out, t)
		if t.Kind == EOF {
			return out
		}
	}
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// lexIdent reads a name, mapping reserved words to their keyword kinds.
func (l *Lexer) lexIdent(start Pos) Token {
	from := l.pos
	for isIdentRune(l.peek()) {
		l.next()
	}
	text := l.src[from:l.pos]
	if k, ok := keywords[text]; ok {
		return Token{Kind: k, Text: text, Pos: start}
	}
	return Token{Kind: Ident, Text: text, Pos: start}
}

// lexNumber reads an int or a float. A float must contain a decimal point with
// a digit on both sides: `1.` and `.5` are rejected, so `x.5` cannot be read as
// a float where a member access was meant, and `1.` cannot swallow the `.` of a
// method call.
func (l *Lexer) lexNumber(start Pos) Token {
	from := l.pos
	for isDigit(l.peek()) {
		l.next()
	}
	kind := Int
	if l.peek() == '.' && isDigit(l.peekAt(1)) {
		kind = Float
		l.next() // '.'
		for isDigit(l.peek()) {
			l.next()
		}
	}
	// An identifier character straight after a number is always a typo — `3x`
	// is not two tokens in any reading of this grammar.
	if isIdentStart(l.peek()) {
		l.errorf(l.here(), "unexpected character %q after number", string(l.peek()))
		for isIdentRune(l.peek()) {
			l.next()
		}
		return Token{Kind: Invalid, Text: l.src[from:l.pos], Pos: start}
	}
	return Token{Kind: kind, Text: l.src[from:l.pos], Pos: start}
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// lexString reads a double-quoted string, decoding escapes. The token's Text is
// the decoded value; a diagnostic that wants the source spelling has the
// position to find it.
func (l *Lexer) lexString(start Pos) Token {
	l.next() // opening quote
	var b strings.Builder
	for {
		r := l.peek()
		switch r {
		case 0:
			l.errorf(start, "unterminated string")
			return Token{Kind: Invalid, Text: b.String(), Pos: start}
		case '\n':
			// A string may not span lines: the overwhelmingly likelier reading
			// of a missing quote is that the author forgot one, and letting it
			// run would swallow the rest of the file into one bad token.
			l.errorf(start, "unterminated string")
			return Token{Kind: Invalid, Text: b.String(), Pos: start}
		case '"':
			l.next()
			return Token{Kind: String, Text: b.String(), Pos: start}
		case '\\':
			l.next()
			esc := l.next()
			switch esc {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			default:
				l.errorf(l.here(), "unknown escape %q", "\\"+string(esc))
				b.WriteRune(esc)
			}
		default:
			b.WriteRune(l.next())
		}
	}
}

// lexColon distinguishes an atom from the dict separator by looking ahead for a
// closing colon over a run of atom characters.
func (l *Lexer) lexColon(start Pos) Token {
	if name, width, ok := l.scanAtom(); ok {
		for i := 0; i < width; i++ {
			l.next()
		}
		return Token{Kind: Atom, Text: name, Pos: start}
	}
	l.next()
	return Token{Kind: Colon, Text: ":", Pos: start}
}

// scanAtom looks ahead from a leading `:` for `[a-z_]+:`. It reports the atom's
// name and how many bytes the whole `:name:` spans, without consuming anything.
func (l *Lexer) scanAtom() (name string, width int, ok bool) {
	w, ok := scanAtomAt(l.src, l.pos)
	if !ok {
		return "", 0, false
	}
	return l.src[l.pos+1 : l.pos+w-1], w, true
}

// scanAtomAt reports how many bytes the `:name:` beginning at i spans. Shared
// with the highlighter (highlight.go), which scans the source separately and
// has to draw the atom/separator line in exactly the same place this does.
func scanAtomAt(src string, i int) (width int, ok bool) {
	j := i + 1 // past the opening ':'
	for j < len(src) {
		c := src[j]
		if c >= 'a' && c <= 'z' || c == '_' {
			j++
			continue
		}
		break
	}
	if j == i+1 || j >= len(src) || src[j] != ':' {
		return 0, false
	}
	return j + 1 - i, true
}

// lexOperator reads punctuation, preferring the longest match so `<=`, `<-`,
// `->`, `==`, `!=`, `+=` and friends are each one token.
func (l *Lexer) lexOperator(start Pos) Token {
	two := ""
	if l.pos+1 < len(l.src) {
		two = l.src[l.pos : l.pos+2]
	}
	if k, ok := twoCharOps[two]; ok {
		l.next()
		l.next()
		return Token{Kind: k, Text: two, Pos: start}
	}

	r := l.next()
	if k, ok := oneCharOps[r]; ok {
		return Token{Kind: k, Text: string(r), Pos: start}
	}
	l.errorf(start, "unexpected character %q", string(r))
	return Token{Kind: Invalid, Text: string(r), Pos: start}
}

var twoCharOps = map[string]Kind{
	"==": Eq, "!=": NotEq, "<=": LtEq, ">=": GtEq,
	"<-": Send, "->": Arrow,
	"+=": PlusEq, "-=": MinusEq, "*=": StarEq, "/=": SlashEq,
}

var oneCharOps = map[rune]Kind{
	'+': Plus, '-': Minus, '*': Star, '/': Slash, '%': Percent,
	'=': Assign, '<': Lt, '>': Gt,
	'.': Dot, ',': Comma,
	'(': LParen, ')': RParen,
	'[': LBracket, ']': RBracket,
	'{': LBrace, '}': RBrace,
	'@': At, '#': Hash,
}
