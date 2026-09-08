package flang

// highlight.go — source text to classified spans, for an editor's colouring.
//
// This is a second, deliberately separate pass over the source rather than a
// reuse of Lexer, because the three things a colourist needs are precisely the
// three things the lexer throws away: comments (skipped as whitespace), the
// source spelling of a string (Token.Text carries the decoded value), and the
// byte extent of a token (a Token knows where it starts and nothing about where
// it ends). Reconstructing those from a token stream would be more work, and
// more guessing, than scanning again — so the rules the two passes share, the
// keyword table and the atom lookahead among them, are shared as code, and the
// scanning is not.
//
// It never fails. A file being typed into is wrong more often than it is right,
// and a colourist that gave up at the first bad byte would go blank exactly when
// it is being read most closely. Anything unrecognised becomes ClassInvalid and
// the scan carries on at the next byte.

import (
	"unicode"
	"unicode/utf8"
)

// Class is what a run of source is, as far as colour is concerned.
type Class uint8

const (
	// ClassPlain is the default: identifiers, and anything with no opinion.
	// It is never emitted — a caller draws whatever falls between two spans in
	// its own default colour.
	ClassPlain Class = iota
	ClassComment
	ClassString
	ClassNumber
	ClassKeyword
	ClassType     // a type name, where a type is what the grammar expects
	ClassAtom     // :spawn:
	ClassBuiltin  // a library function, where it is being called
	ClassAddress  // @id, #tag
	ClassParam    // ?name — a script parameter
	ClassOperator // punctuation
	ClassInvalid  // what nothing else would accept
)

// className is how each class reads in a test failure.
var className = map[Class]string{
	ClassPlain: "plain", ClassComment: "comment", ClassString: "string",
	ClassNumber: "number", ClassKeyword: "keyword", ClassType: "type",
	ClassAtom: "atom", ClassBuiltin: "builtin", ClassAddress: "address",
	ClassParam:    "param",
	ClassOperator: "operator", ClassInvalid: "invalid",
}

// String renders a class by name.
func (c Class) String() string {
	if n, ok := className[c]; ok {
		return n
	}
	return "class(?)"
}

// Span is a run of source bytes sharing one class, as the half-open byte range
// [Start, End). Spans come back in source order and never overlap.
type Span struct {
	Start, End int
	Class      Class
}

// Highlight classifies src. Whitespace and plain identifiers produce no span.
func Highlight(src string) []Span {
	h := highlighter{src: src}
	for h.pos < len(h.src) {
		h.step()
	}
	return h.spans
}

// highlighter is the scan's state.
//
// The three context flags are what let `int` be a type in `param n int` and a
// perfectly ordinary variable in `int = 3`. Nothing here is a parse: they are
// the least state that distinguishes the positions where the grammar expects a
// type from every other place the same word can appear.
type highlighter struct {
	src   string
	pos   int
	spans []Span

	prevIdent  bool // the last significant token was a plain identifier
	parenDepth int  // open parentheses, so a juxtaposed `name type` can be told from a statement
	paramSlot  int  // 1 after `param` (expecting a name), 2 after that name (expecting its type)
	inRet      bool // reading a return type, after `->`
}

// step scans one token from pos.
func (h *highlighter) step() {
	start := h.pos
	r, w := utf8.DecodeRuneInString(h.src[h.pos:])
	switch {
	case unicode.IsSpace(r):
		// Whitespace deliberately leaves the context flags alone: flang's
		// blocks are delimited by keywords, so a declaration may wrap.
		h.pos += w
	case r == '-' && h.byteAt(h.pos+1) == '-':
		h.scanComment(start)
	case r == '"':
		h.scanString(start)
	case isDigit(r):
		h.scanNumber(start)
	case r == ':':
		h.scanColon(start)
	case r == '@' || r == '#':
		h.scanAddress(start, w, ClassAddress)
	case r == '?':
		h.scanAddress(start, w, ClassParam)
	case isIdentStart(r):
		h.scanIdent(start)
	default:
		h.scanOperator(start, w)
	}
}

// emit records a span, unless it is empty or plain.
func (h *highlighter) emit(start, end int, c Class) {
	if end <= start || c == ClassPlain {
		return
	}
	h.spans = append(h.spans, Span{Start: start, End: end, Class: c})
}

// byteAt returns the byte at i, or 0 past the end. Byte-wise lookahead is safe
// against UTF-8: every byte of a multi-byte rune is >= 0x80, so it can never be
// mistaken for one of the ASCII characters this scan asks about.
func (h *highlighter) byteAt(i int) byte {
	if i < 0 || i >= len(h.src) {
		return 0
	}
	return h.src[i]
}

// literal marks the end of a value: a literal can neither be a parameter's name
// nor the type that follows one.
func (h *highlighter) literal() {
	h.prevIdent = false
	h.paramSlot = 0
	h.inRet = false
}

// scanComment reads `--` to the end of the line. A comment leaves the context
// flags alone, so it can sit inside a declaration without breaking it.
func (h *highlighter) scanComment(start int) {
	for h.pos < len(h.src) && h.src[h.pos] != '\n' {
		h.pos++
	}
	h.emit(start, h.pos, ClassComment)
}

// scanString reads a double-quoted string, including its quotes.
//
// An unterminated one is coloured as invalid rather than as a string: a missing
// closing quote is the one lexical mistake whose damage runs past the token
// that caused it, and the colour saying so at the point it happens is most of
// what highlighting is for.
func (h *highlighter) scanString(start int) {
	h.pos++ // opening quote
	for h.pos < len(h.src) {
		switch h.src[h.pos] {
		case '\n':
			h.emit(start, h.pos, ClassInvalid)
			h.literal()
			return
		case '\\':
			h.pos++
			if h.pos < len(h.src) && h.src[h.pos] != '\n' {
				h.pos++
			}
		case '"':
			h.pos++
			h.emit(start, h.pos, ClassString)
			h.literal()
			return
		default:
			h.pos++
		}
	}
	h.emit(start, h.pos, ClassInvalid)
	h.literal()
}

// scanNumber reads an int or a float, on the lexer's rules: a float needs a
// digit on both sides of the point, and an identifier character straight after
// a number is always a mistake.
func (h *highlighter) scanNumber(start int) {
	for isDigit(rune(h.byteAt(h.pos))) {
		h.pos++
	}
	if h.byteAt(h.pos) == '.' && isDigit(rune(h.byteAt(h.pos+1))) {
		h.pos++
		for isDigit(rune(h.byteAt(h.pos))) {
			h.pos++
		}
	}
	class := ClassNumber
	if r, _ := utf8.DecodeRuneInString(h.src[h.pos:]); isIdentStart(r) {
		class = ClassInvalid
		h.skipIdent()
	}
	h.emit(start, h.pos, class)
	h.literal()
}

// scanColon tells an atom from the dict separator with the lexer's own
// lookahead, so the two passes cannot disagree about `{"k": :on:}`.
func (h *highlighter) scanColon(start int) {
	if w, ok := scanAtomAt(h.src, h.pos); ok {
		h.pos += w
		h.emit(start, h.pos, ClassAtom)
		h.literal()
		return
	}
	h.pos++
	h.emit(start, h.pos, ClassOperator)
	h.literal()
}

// scanAddress reads a sigil and the name after it as one span, sigil included:
// the sigil is what makes the name an address rather than a variable, and
// colouring them apart would only say that twice.
//
// class is what the sigil makes it — `@` and `#` reach the level, `?` reaches
// the binding the level supplied — which is the distinction worth a colour.
func (h *highlighter) scanAddress(start, sigilWidth int, class Class) {
	h.pos += sigilWidth
	if r, _ := utf8.DecodeRuneInString(h.src[h.pos:]); isIdentStart(r) {
		h.skipIdent()
		h.emit(start, h.pos, class)
	} else {
		h.emit(start, h.pos, ClassOperator)
	}
	h.literal()
}

// scanIdent reads a name and decides what kind of name it is.
func (h *highlighter) scanIdent(start int) {
	h.skipIdent()
	text := h.src[start:h.pos]

	switch {
	case isKeyword(text):
		h.emit(start, h.pos, ClassKeyword)
		h.prevIdent = false
		h.inRet = false
		// `param` opens a declaration; every other keyword closes whatever one
		// was open.
		if text == "param" {
			h.paramSlot = 1
		} else {
			h.paramSlot = 0
		}

	case IsTypeName(text) && h.typeExpected():
		h.emit(start, h.pos, ClassType)
		h.prevIdent = false
		h.paramSlot = 0
		h.inRet = false

	case IsBuiltin(text) && h.callFollows():
		h.emit(start, h.pos, ClassBuiltin)
		h.prevIdent = false
		h.paramSlot = 0
		h.inRet = false

	default:
		// A plain name gets no span, and is what a following type name needs to
		// have seen to be read as a type.
		h.prevIdent = true
		h.inRet = false
		if h.paramSlot == 1 {
			h.paramSlot = 2
		} else {
			h.paramSlot = 0
		}
	}
}

// typeExpected reports whether a type name here is a type rather than a
// variable that happens to share the spelling.
//
// Three positions, and no more: the type of a declared `param`, the type in a
// return position after `->`, and a name juxtaposed with another name inside
// parentheses — which is a parameter's `name type` in a `def` head or in one of
// its later clauses, and is not a legal expression anywhere else. Outside
// parentheses `list = 3` is left alone, because at the top level of a statement
// the far likelier reading is a variable.
func (h *highlighter) typeExpected() bool {
	return h.paramSlot == 2 || h.inRet || (h.prevIdent && h.parenDepth > 0)
}

// callFollows reports whether the name just read is being called, which is what
// separates the builtin `find` from a variable someone named `find`.
func (h *highlighter) callFollows() bool {
	i := h.pos
	for i < len(h.src) {
		r, w := utf8.DecodeRuneInString(h.src[i:])
		if !unicode.IsSpace(r) {
			return r == '('
		}
		i += w
	}
	return false
}

// scanOperator reads punctuation, preferring the two-character form so `->` and
// `<=` are each one span.
func (h *highlighter) scanOperator(start, runeWidth int) {
	text := ""
	if h.pos+1 < len(h.src) {
		text = h.src[h.pos : h.pos+2]
	}
	if _, ok := twoCharOps[text]; ok {
		h.pos += 2
	} else {
		r, _ := utf8.DecodeRuneInString(h.src[h.pos:])
		h.pos += runeWidth
		if _, ok := oneCharOps[r]; !ok {
			h.emit(start, h.pos, ClassInvalid)
			h.literal()
			return
		}
		text = string(r)
	}
	h.emit(start, h.pos, ClassOperator)

	switch text {
	case "->":
		h.inRet = true
	case "(":
		h.parenDepth++
	case ")":
		if h.parenDepth > 0 {
			h.parenDepth--
		}
		h.inRet = false
	case ",":
		// Left alone: a comma separates the members of a tuple return type as
		// readily as it separates arguments.
	default:
		h.inRet = false
	}
	h.prevIdent = false
	h.paramSlot = 0
}

// skipIdent advances past the rest of an identifier.
func (h *highlighter) skipIdent() {
	for h.pos < len(h.src) {
		r, w := utf8.DecodeRuneInString(h.src[h.pos:])
		if !isIdentRune(r) {
			return
		}
		h.pos += w
	}
}

// isKeyword reports whether s is reserved.
func isKeyword(s string) bool { _, ok := keywords[s]; return ok }
