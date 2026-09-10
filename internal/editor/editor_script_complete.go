package editor

// editor_script_complete.go — what the script editor offers while you type.
//
// Split the way the rest of this editor is split: everything here is a function
// of a string and a byte offset, with no ImGui and no window, so the awkward
// half — which context is `@self.prop.vis|` in, and what does accepting an item
// do to the buffer — can be tested directly. The popup that shows the answer is
// editor_script_complete_ui.go.
//
// Three things make a completion list worth having in *this* language, and all
// three are things a plain word-list cannot do:
//
//   - `@name` and `#tag` name objects that exist in the level and nowhere in the
//     buffer, so the only place the answer can come from is the engine (see
//     script.CompletionRefs).
//   - `.prop.` is answerable only once the *kind* of the thing before it is
//     known, and the kind comes from the same walk the linter does — `@sec.floor`
//     is a floor, so its properties are a floor's and not the union of every
//     object's.
//   - A `param` declared at the top of the file is read back as `?name`, so the
//     buffer's own text is the source for a `?` list — the one list that comes
//     from neither the engine nor the level.
//
// Nothing here keeps a copy of a table that belongs to something else. The
// keywords come from flang, the builtins from flang, the properties from
// script, the ids and tags from script. A name that can be written and not
// offered is a bug in one of those tables, not in this file.

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"foundry/internal/flang"
	"foundry/internal/script"
)

// ---------- tokens ----------

// scriptTokKind is what one run of source is, for the purposes of working out
// what the caret is sitting in.
//
// Coarser than flang's own Kind on purpose. This is not a parse — it is the
// least classification that tells `@door` from `door`, a dict's `:` from an
// atom's, and a comment from everything else — and being coarse is what lets it
// run over a half-written line without ever failing.
type scriptTokKind uint8

const (
	tokIdent scriptTokKind = iota
	tokPunct
	tokString
	tokNumber
	tokAtom    // :spawn:, complete with both colons
	tokAddr    // @name
	tokTag     // #name
	tokParam   // ?name
	tokComment // -- to end of line
)

// scriptTok is one token. Text carries the name for the kinds that have one —
// an address without its sigil, an atom without its colons — and the spelling
// for punctuation; Start and End are always the full extent in the source,
// sigils included.
type scriptTok struct {
	Kind       scriptTokKind
	Text       string
	Start, End int
}

// scriptComplTwoCharOps are the operators worth keeping whole. Only the ones a
// completion decision can turn on: `->` introduces a return type, and the
// compound assignments mark their left-hand side as a variable.
var scriptComplTwoCharOps = map[string]bool{
	"->": true, "<-": true, "==": true, "!=": true, "<=": true, ">=": true,
	"+=": true, "-=": true, "*=": true, "/=": true, "%=": true,
}

// scriptComplLex scans src into tokens. It never fails and never skips a byte:
// anything unrecognised comes back as one-character punctuation, which is what
// lets it run on a line that is halfway through being typed.
func scriptComplLex(src string) []scriptTok {
	var out []scriptTok
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++

		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			start := i
			for i < len(src) && src[i] != '\n' {
				i++
			}
			out = append(out, scriptTok{Kind: tokComment, Start: start, End: i})

		case c == '"':
			start := i
			i = scriptComplSkipString(src, i)
			out = append(out, scriptTok{Kind: tokString, Start: start, End: i})

		case c >= '0' && c <= '9':
			start := i
			for i < len(src) && (src[i] >= '0' && src[i] <= '9' || src[i] == '.') {
				i++
			}
			out = append(out, scriptTok{Kind: tokNumber, Start: start, End: i})

		case c == ':':
			// flang's own atom rule, spelled the same way: lowercase and
			// underscores between two colons. A half-typed `:spaw` is
			// deliberately *not* an atom — it lexes as `:` then an identifier,
			// which is exactly the shape the atom completion looks for.
			if w, ok := scriptComplAtomAt(src, i); ok {
				out = append(out, scriptTok{Kind: tokAtom, Text: src[i+1 : i+w-1],
					Start: i, End: i + w})
				i += w
				break
			}
			out = append(out, scriptTok{Kind: tokPunct, Text: ":", Start: i, End: i + 1})
			i++

		case c == '@' || c == '#' || c == '?':
			kind := tokAddr
			switch c {
			case '#':
				kind = tokTag
			case '?':
				kind = tokParam
			}
			if end, ok := scriptComplIdentAt(src, i+1); ok {
				out = append(out, scriptTok{Kind: kind, Text: src[i+1 : end], Start: i, End: end})
				i = end
				break
			}
			out = append(out, scriptTok{Kind: tokPunct, Text: string(c), Start: i, End: i + 1})
			i++

		default:
			if end, ok := scriptComplIdentAt(src, i); ok {
				out = append(out, scriptTok{Kind: tokIdent, Text: src[i:end], Start: i, End: end})
				i = end
				break
			}
			n := 1
			if i+2 <= len(src) && scriptComplTwoCharOps[src[i:i+2]] {
				n = 2
			}
			out = append(out, scriptTok{Kind: tokPunct, Text: src[i : i+n], Start: i, End: i + n})
			i += n
		}
	}
	return out
}

// scriptComplSkipString returns the offset just past the string starting at i.
// An unterminated one ends at the newline, which is where flang ends it too.
func scriptComplSkipString(src string, i int) int {
	i++ // opening quote
	for i < len(src) && src[i] != '\n' {
		switch src[i] {
		case '\\':
			i += 2
			if i > len(src) {
				return len(src)
			}
		case '"':
			return i + 1
		default:
			i++
		}
	}
	return i
}

// scriptComplAtomAt reports the width of the atom starting at i, if there is
// one. The rule is flang's: lowercase letters and underscores, colons on both
// sides.
func scriptComplAtomAt(src string, i int) (width int, ok bool) {
	j := i + 1
	for j < len(src) && (src[j] >= 'a' && src[j] <= 'z' || src[j] == '_') {
		j++
	}
	if j == i+1 || j >= len(src) || src[j] != ':' {
		return 0, false
	}
	return j + 1 - i, true
}

// scriptComplIdentAt returns the end of the identifier starting at i.
func scriptComplIdentAt(src string, i int) (end int, ok bool) {
	r, w := utf8.DecodeRuneInString(src[i:])
	if w == 0 || !(r == '_' || unicode.IsLetter(r)) {
		return 0, false
	}
	j := i + w
	for j < len(src) {
		r, w := utf8.DecodeRuneInString(src[j:])
		if w == 0 || !(r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			break
		}
		j += w
	}
	return j, true
}

// ---------- the context at the caret ----------

// scriptComplKind is which list the caret is asking for.
type scriptComplKind uint8

const (
	// complNone means offer nothing: inside a comment, inside a string, or in
	// the middle of a number.
	complNone scriptComplKind = iota
	complIdent
	complType
	complParamType
	complAddr
	complParam
	complTag
	complAtom
	complMember
	complProp
	complAttr
)

// scriptComplCtx is what the caret is in the middle of.
//
// Start and Prefix are the two halves of "what is being replaced": Start is the
// first byte of the name being typed — past the `@`, `#` or `:`, because a
// sigil is punctuation the author has already committed to — and Prefix is the
// text from there to the caret.
type scriptComplCtx struct {
	Kind   scriptComplKind
	Prefix string
	Start  int

	// Base is the address a `.` chain hangs off — `self` in `@self.prop.x` —
	// and BaseSigil is `@` or `#`. Both are zero when the chain starts from
	// something that is not an address, which is a variable holding a ref: a
	// perfectly ordinary thing to write, and simply not something this side can
	// resolve.
	Base      string
	BaseSigil byte

	// Path is the structural steps between the base and the caret, with the
	// mode word (`prop`, `attr`) removed — `["floor"]` in
	// `@sec.floor.prop.he|`.
	Path []string

	// Value is set when the chain has already passed through `.prop.` or
	// `.attr.`, which means what is in hand is a value rather than an object —
	// `@self.prop.position.` is a vec, and offering it a sector's members would
	// be offering nonsense.
	Value bool

	// Sigil is true when the caret sits directly after `@`, `#`, `:` or `.`
	// with nothing typed yet. That is a deliberate request for a list, so the
	// popup opens on it; an empty prefix anywhere else is not.
	Sigil bool
}

// scriptComplContext works out what the caret at cursor is asking for.
//
// cursor is a byte offset into src, as the text widget reports it.
func scriptComplContext(src string, cursor int) scriptComplCtx {
	if cursor < 0 || cursor > len(src) {
		return scriptComplCtx{}
	}
	toks := scriptComplLex(src)

	// The token the caret is inside or immediately after. Tokens are ordered
	// and disjoint, so at most one can match.
	cur := -1
	for i, t := range toks {
		if t.Start < cursor && cursor <= t.End {
			cur = i
			break
		}
	}
	if cur >= 0 {
		switch toks[cur].Kind {
		case tokComment, tokString, tokNumber:
			// Nothing here is a name. A number in particular has to be refused
			// explicitly, or `1.` would be read as a member access.
			return scriptComplCtx{}
		}
	}

	ctx := scriptComplCtx{Start: cursor}
	dot := -1 // index of the `.` a member chain hangs off
	before := -1

	if cur >= 0 && toks[cur].Kind != tokPunct {
		t := toks[cur]
		switch t.Kind {
		case tokIdent:
			ctx.Start = t.Start
		default: // tokAddr, tokTag, tokParam, tokAtom — the name starts past the sigil
			ctx.Start = t.Start + 1
		}
		end := cursor
		if t.Kind == tokAtom && end > t.End-1 {
			end = t.End - 1 // never take the closing colon into the prefix
		}
		if end < ctx.Start {
			return scriptComplCtx{}
		}
		ctx.Prefix = src[ctx.Start:end]
		before = cur - 1

		switch t.Kind {
		case tokAddr:
			ctx.Kind = complAddr
			return ctx
		case tokParam:
			ctx.Kind = complParam
			return ctx
		case tokTag:
			ctx.Kind = complTag
			return ctx
		case tokAtom:
			ctx.Kind = complAtom
			return ctx
		}
		// A plain identifier: whatever precedes it decides.
		if before >= 0 && toks[before].Kind == tokPunct && toks[before].End == ctx.Start {
			switch toks[before].Text {
			case ".":
				dot = before
			case ":":
				if !scriptComplIsDictColon(toks, before) {
					ctx.Kind = complAtom
					return ctx
				}
			}
		}
	} else if cur >= 0 {
		// An empty prefix, directly after punctuation. Only the trigger
		// characters open a list on their own.
		ctx.Sigil = true
		switch toks[cur].Text {
		case "@":
			ctx.Kind = complAddr
			return ctx
		case "#":
			ctx.Kind = complTag
			return ctx
		case "?":
			ctx.Kind = complParam
			return ctx
		case ":":
			if !scriptComplIsDictColon(toks, cur) {
				ctx.Kind = complAtom
				return ctx
			}
			ctx.Sigil = false
		case ".":
			dot = cur
		default:
			ctx.Sigil = false
		}
		before = cur
	} else {
		// Whitespace. There is no word and no trigger, but the tokens behind
		// the caret still decide whether a type is what belongs here.
		for i, t := range toks {
			if t.End <= cursor {
				before = i
			}
		}
	}

	if dot >= 0 {
		base, sigil, path, _ := scriptComplChain(toks, dot-1)
		ctx.Base, ctx.BaseSigil = base, sigil
		if n := len(path); n > 0 && (path[n-1] == "prop" || path[n-1] == "attr") {
			if path[n-1] == "prop" {
				ctx.Kind = complProp
			} else {
				ctx.Kind = complAttr
			}
			ctx.Path = path[:n-1]
			return ctx
		}
		ctx.Kind = complMember
		ctx.Path = path
		for _, seg := range path {
			if seg == "prop" || seg == "attr" {
				ctx.Value = true
			}
		}
		return ctx
	}

	ctx.Sigil = false
	ctx.Kind = scriptComplIdentKind(toks, before)
	return ctx
}

// scriptComplIsDictColon reports whether the `:` at index i is a dict's key
// separator rather than the opening of an atom.
//
// A dict key is always a quoted string — `{"key": v}` — and an atom never
// follows one, so the token before the colon settles it. Without this,
// `{"speed": va|` would be offered the engine's event names.
func scriptComplIsDictColon(toks []scriptTok, i int) bool {
	return i > 0 && toks[i-1].Kind == tokString
}

// scriptComplChain walks a member chain backwards from the token at j, which is
// whatever sits immediately before the trailing `.`.
//
// It returns the address the chain hangs off and the structural steps between
// that address and the caret, in source order. ok is false when the chain does
// not start from an address — `src.prop.x`, where `src` is a handler's
// parameter, is the ordinary case — and the steps are still returned, because
// the *mode word* is what decides which list to offer and it is knowable even
// when the object is not.
func scriptComplChain(toks []scriptTok, j int) (base string, sigil byte, path []string, ok bool) {
	for j >= 0 {
		t := toks[j]
		switch t.Kind {
		case tokAddr:
			return t.Text, '@', path, true
		case tokTag:
			return t.Text, '#', path, true
		case tokParam:
			return t.Text, '?', path, true
		case tokIdent:
			if j > 0 && toks[j-1].Kind == tokPunct && toks[j-1].Text == "." {
				path = append([]string{t.Text}, path...)
				j -= 2
				continue
			}
			// The root of the chain is a plain name: a local variable, or a
			// call's result. Not an address, and not part of the path.
			return "", 0, path, false
		default:
			return "", 0, path, false
		}
	}
	return "", 0, path, false
}

// scriptComplIdentKind decides what a bare identifier position is asking for:
// an ordinary name, or a type.
//
// Three positions take a type, and all three are unambiguous from the two
// tokens before the caret. Anything else is a name — which is the safe way
// round, since offering names where a type belongs costs a scroll and offering
// types where a name belongs hides everything useful.
func scriptComplIdentKind(toks []scriptTok, before int) scriptComplKind {
	if before < 0 {
		return complIdent
	}
	// `-> |` — a return type.
	if toks[before].Kind == tokPunct && toks[before].Text == "->" {
		return complType
	}
	// `param name |` — a parameter's type, which is the narrower list: a param
	// is stored in a level file, so `list` and `dict` are not offered.
	if before >= 1 && toks[before].Kind == tokIdent &&
		toks[before-1].Kind == tokIdent && toks[before-1].Text == "param" {
		return complParamType
	}
	// `def f(x |` — a handler parameter's type. The `def` is required rather
	// than inferred: `name(` is also a call, and a call's arguments are values.
	if before >= 1 && toks[before].Kind == tokIdent &&
		toks[before-1].Kind == tokPunct &&
		(toks[before-1].Text == "(" || toks[before-1].Text == ",") &&
		scriptComplInDefHead(toks, before-1) {
		return complType
	}
	return complIdent
}

// scriptComplInDefHead reports whether the token at j lies inside the
// parentheses of a `def name(...)` head.
func scriptComplInDefHead(toks []scriptTok, j int) bool {
	depth := 0
	for ; j >= 0; j-- {
		if toks[j].Kind != tokPunct {
			continue
		}
		switch toks[j].Text {
		case ")":
			depth++
		case "(":
			if depth > 0 {
				depth--
				continue
			}
			return j >= 2 && toks[j-1].Kind == tokIdent &&
				toks[j-2].Kind == tokIdent && toks[j-2].Text == "def"
		}
	}
	return false
}

// ---------- what the buffer itself declares ----------

// scriptComplFunc is one `def` in the buffer: its name, the head as written,
// the byte range of the whole definition, and the names bound inside it.
type scriptComplFunc struct {
	Name       string
	Sig        string
	Start, End int
	Locals     []string
}

// scriptComplParam is one top-level `param`, which is read back as `?name`.
type scriptComplParam struct{ Name, Type string }

// scriptComplScan is everything the buffer declares about itself.
type scriptComplScan struct {
	Funcs  []scriptComplFunc
	Params []scriptComplParam
	Atoms  []string
}

// scriptComplScanFile reads the buffer's own declarations.
//
// Deliberately a token walk rather than a use of the parsed tree. The tree is
// recomputed on a debounce — right, for an error list, which should not flicker
// through every half-typed state — but a completion list that lagged the caret
// by a third of a second would suggest the variable you just renamed and not
// the one you are typing. This runs on whatever is in the buffer right now, and
// a file that does not parse still has names in it.
func scriptComplScanFile(src string) scriptComplScan {
	toks := scriptComplLex(src)
	var out scriptComplScan

	// The atoms in their own pass, because the walk below steps over a whole
	// definition at a time and every atom worth completing is inside one.
	seenAtom := map[string]bool{}
	for _, t := range toks {
		if t.Kind == tokAtom && !seenAtom[t.Text] {
			seenAtom[t.Text] = true
			out.Atoms = append(out.Atoms, t.Text)
		}
	}

	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.Kind != tokIdent {
			continue
		}
		switch t.Text {
		case "param":
			// `param name type`, at any depth: a `param` inside a body is a
			// mistake the parser reports, and offering its name meanwhile is
			// not the editor's argument to have.
			if i+1 < len(toks) && toks[i+1].Kind == tokIdent {
				p := scriptComplParam{Name: toks[i+1].Text}
				if i+2 < len(toks) && toks[i+2].Kind == tokIdent {
					p.Type = toks[i+2].Text
				}
				out.Params = append(out.Params, p)
			}
		case "def":
			f, next := scriptComplReadFunc(src, toks, i)
			if f.Name != "" {
				out.Funcs = append(out.Funcs, f)
			}
			i = next - 1
		}
	}
	return out
}

// scriptComplReadFunc reads the definition beginning at the `def` token at i,
// and returns the index just past its `end`.
//
// The block is found by counting the words that open one — `def`, `if`, `for`,
// `while` — against `end`. flang has no nested `def`, so an unbalanced buffer
// simply runs to the end of the file, which is the right answer while the
// closing `end` is still being typed.
func scriptComplReadFunc(src string, toks []scriptTok, i int) (scriptComplFunc, int) {
	f := scriptComplFunc{Start: toks[i].Start, End: len(src)}
	if i+1 >= len(toks) || toks[i+1].Kind != tokIdent {
		return scriptComplFunc{}, i + 1
	}
	f.Name = toks[i+1].Text

	depth := 0
	end := len(toks)
	for j := i; j < len(toks); j++ {
		if toks[j].Kind != tokIdent {
			continue
		}
		switch toks[j].Text {
		case "def", "if", "for", "while":
			depth++
		case "end":
			depth--
			if depth == 0 {
				f.End = toks[j].End
				end = j + 1
			}
		}
		if end != len(toks) {
			break
		}
	}

	body := toks[i:end]
	f.Sig = scriptComplHeadText(src, body)
	f.Locals = scriptComplLocals(body, f.Name)
	return f, end
}

// scriptComplHeadText renders a definition's first clause head — the name, its
// parameters and its return type — as the reference column of a completion row.
func scriptComplHeadText(src string, body []scriptTok) string {
	if len(body) < 2 {
		return ""
	}
	start := body[1].Start
	depth := 0
	for j := 2; j < len(body); j++ {
		t := body[j]
		if t.Kind == tokPunct {
			switch t.Text {
			case "(":
				depth++
				continue
			case ")":
				depth--
				if depth != 0 {
					continue
				}
				// A return type belongs to the head; a guard does not — `when`
				// introduces an expression, and a whole predicate in a one-line
				// column is not a signature.
				if j+1 < len(body) && body[j+1].Kind == tokPunct && body[j+1].Text == "->" {
					if j+2 < len(body) {
						return src[start:body[j+2].End]
					}
				}
				return src[start:t.End]
			}
		}
	}
	return src[start:body[len(body)-1].End]
}

// scriptComplLocals returns every name bound inside one definition: its
// clauses' parameters, the variables it assigns, and its loop variables.
//
// A name is offered as soon as it is bound anywhere in the definition, without
// regard for whether the caret is before or after the binding. That is
// deliberate: while a function is being written the binding is as often below
// the caret as above it, and a list that refused to name a variable until it
// had been written would be least useful exactly while the code is being
// drafted.
func scriptComplLocals(body []scriptTok, name string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s == "" || seen[s] || flang.IsTypeName(s) {
			return
		}
		seen[s] = true
		out = append(out, s)
	}

	for j := 0; j < len(body); j++ {
		t := body[j]
		if t.Kind == tokIdent && t.Text == "for" && j+1 < len(body) && body[j+1].Kind == tokIdent {
			add(body[j+1].Text)
			continue
		}
		if t.Kind == tokIdent && j+1 < len(body) && body[j+1].Kind == tokPunct {
			switch body[j+1].Text {
			case "=", "+=", "-=", "*=", "/=", "%=":
				add(t.Text)
				continue
			case "(":
				// A clause head repeats the group's name; anything else with a
				// `(` after it is a call, whose arguments are values rather
				// than bindings.
				if t.Text == name {
					j = scriptComplReadHeadParams(body, j+1, add)
				}
			}
		}
	}
	return out
}

// scriptComplReadHeadParams binds the parameter names in the head whose `(` is
// at index open, and returns the index of the matching `)`.
//
// A parameter is an atom, a literal, a bare name or a name with a type
// (parser.go's parseParam), so the first identifier of each comma-separated
// part — and only the first — is a binding.
func scriptComplReadHeadParams(body []scriptTok, open int, add func(string)) int {
	depth := 0
	first := true
	for j := open; j < len(body); j++ {
		t := body[j]
		if t.Kind == tokPunct {
			switch t.Text {
			case "(":
				depth++
			case ")":
				depth--
				if depth == 0 {
					return j
				}
			case ",":
				if depth == 1 {
					first = true
				}
			}
			continue
		}
		if depth == 1 && first {
			if t.Kind == tokIdent {
				add(t.Text)
			}
			first = false
		}
	}
	return len(body) - 1
}

// LocalsAt returns the names bound in the definition containing cursor.
func (s scriptComplScan) LocalsAt(cursor int) []string {
	for _, f := range s.Funcs {
		if cursor >= f.Start && cursor <= f.End {
			return f.Locals
		}
	}
	return nil
}

// ---------- the items ----------

// scriptComplItem is one row.
//
// Text is what is inserted and Label what is shown, and they differ wherever
// the punctuation around a name is already on screen: an atom's row reads
// `:spawn:` and inserts `spawn` when the colons are already typed.
type scriptComplItem struct {
	Text   string
	Label  string
	Detail string
	Group  string
	// Call marks a name that is called rather than read, so accepting it can
	// bring its parentheses with it.
	Call bool
}

// scriptComplSource is everything the item list is built from.
//
// The level-derived fields are filled by the caller and only for the context
// that needs them: resolving an id to a kind means indexing the whole level,
// which is not work to do on every keystroke of an ordinary identifier.
type scriptComplSource struct {
	Scan   scriptComplScan
	Locals []string
	// Imported are the function and parameter names this file's imports bring
	// into scope. Taken from the last parse rather than from the token scan,
	// because reading another file is not something to do per keystroke.
	ImportedFuncs  []string
	ImportedParams []string

	Refs  []script.CompletionRef
	Tags  []string
	Attrs []string
	Props []script.PropertyDoc
	// Kind is what the object before the `.` turned out to be, which is what
	// makes a member and property list this kind's rather than every kind's.
	// RefNone means it could not be worked out — a handler's `src ref`
	// parameter, or a script parameter the level answers — and the lists fall
	// back to the union.
	Kind flang.Kind_
}

// scriptComplItems builds the unfiltered list for one context.
func scriptComplItems(ctx scriptComplCtx, src scriptComplSource) []scriptComplItem {
	switch ctx.Kind {
	case complAddr:
		return scriptComplAddrItems(src)
	case complParam:
		return scriptComplParamItems(src)
	case complTag:
		return scriptComplTagItems(src)
	case complAtom:
		return scriptComplAtomItems(src)
	case complMember:
		if ctx.Value {
			return scriptComplVecItems()
		}
		return scriptComplMemberItems(src)
	case complProp:
		return scriptComplPropItems(src)
	case complAttr:
		return scriptComplAttrItems(src)
	case complType:
		return scriptComplTypeItems(flang.TypeNames())
	case complParamType:
		return scriptComplTypeItems(flang.ParamTypes)
	case complIdent:
		return scriptComplIdentItems(src)
	}
	return nil
}

// scriptComplIdentItems is the list for an ordinary name position: what can be
// called, and what holds a value.
//
// Ordered by how close the name is to the caret — this function's own
// variables, then this file's functions, then what it imported, then the
// language. Ranking by proximity rather than alphabetically is what makes the
// first row usually the right one.
func scriptComplIdentItems(src scriptComplSource) []scriptComplItem {
	var out []scriptComplItem
	seen := map[string]bool{}
	add := func(it scriptComplItem) {
		if seen[it.Text] {
			return
		}
		seen[it.Text] = true
		if it.Label == "" {
			it.Label = it.Text
		}
		out = append(out, it)
	}

	for _, n := range src.Locals {
		add(scriptComplItem{Text: n, Detail: "local", Group: "var"})
	}
	for _, f := range src.Scan.Funcs {
		add(scriptComplItem{Text: f.Name, Detail: f.Sig, Group: "fn", Call: true})
	}
	for _, n := range src.ImportedFuncs {
		add(scriptComplItem{Text: n, Detail: "imported", Group: "fn", Call: true})
	}
	for _, d := range flang.BuiltinDocs() {
		add(scriptComplItem{Text: d.Name, Detail: d.Sig + " -> " + d.Ret,
			Group: "fn", Call: true})
	}
	for _, k := range flang.KeywordNames() {
		add(scriptComplItem{Text: k, Detail: "keyword", Group: "kw"})
	}
	return out
}

// scriptComplAddrItems is the list after `@`: what the engine always provides,
// and what the open level contains.
//
// Parameters are deliberately absent — they are `?name`, and offering them here
// would re-teach the confusion the two sigils exist to end.
func scriptComplAddrItems(src scriptComplSource) []scriptComplItem {
	var out []scriptComplItem
	seen := map[string]bool{}
	add := func(name, detail, group string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, scriptComplItem{Text: name, Label: name, Detail: detail, Group: group})
	}

	add("self", "the object this script is attached to", "builtin")
	add("player", "the player", "builtin")
	add("level", "the level itself", "builtin")
	for _, r := range src.Refs {
		add(r.ID, r.Kind.String(), "ref")
	}
	return out
}

// scriptComplParamItems is the list after `?`: what this file declared with
// `param`, then what its imports declared.
//
// This file's come first for the reason scriptComplIdentItems orders by
// proximity: the parameter an author is reaching for is nearly always one they
// can see.
func scriptComplParamItems(src scriptComplSource) []scriptComplItem {
	var out []scriptComplItem
	seen := map[string]bool{}
	add := func(name, detail string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, scriptComplItem{Text: name, Label: "?" + name,
			Detail: detail, Group: "param"})
	}
	for _, p := range src.Scan.Params {
		detail := "param"
		if p.Type != "" {
			detail = "param " + p.Type
		}
		add(p.Name, detail)
	}
	for _, n := range src.ImportedParams {
		add(n, "param, imported")
	}
	return out
}

// scriptComplTagItems is the list after `#`.
func scriptComplTagItems(src scriptComplSource) []scriptComplItem {
	out := make([]scriptComplItem, 0, len(src.Tags))
	for _, t := range src.Tags {
		out = append(out, scriptComplItem{Text: t, Label: "#" + t,
			Detail: "tag", Group: "tag"})
	}
	return out
}

// scriptComplAtomItems is the list after `:` — the events the engine posts,
// then whatever atoms this file already mentions.
//
// The buffer's own atoms are in the list because a custom atom needs no
// declaration: `:unlock:` exists because something sent it, and the second
// place it is written is exactly where a typo would go unnoticed.
func scriptComplAtomItems(src scriptComplSource) []scriptComplItem {
	var out []scriptComplItem
	seen := map[string]bool{}
	add := func(name, detail string) {
		if seen[name] {
			return
		}
		seen[name] = true
		out = append(out, scriptComplItem{Text: name, Label: ":" + name + ":",
			Detail: detail, Group: "atom"})
	}
	for _, e := range script.EventDocs() {
		// EventDoc writes an atom as it is matched, colons and all; the name
		// alone is what a completion inserts.
		add(strings.Trim(e.Atom, ":"), e.PostedTo+" — "+e.When)
	}
	for _, a := range src.Scan.Atoms {
		add(a, "used in this file")
	}
	return out
}

// scriptComplMemberItems is the list after a `.` on an object: the two access
// modes, the members every ref answers, and the structural steps the kind in
// hand actually has.
func scriptComplMemberItems(src scriptComplSource) []scriptComplItem {
	var out []scriptComplItem
	add := func(name, detail string) {
		out = append(out, scriptComplItem{Text: name, Label: name, Detail: detail,
			Group: "member"})
	}
	add("prop", "an engine field: .prop.<name>")
	add("attr", "a user-defined value: .attr.<name>")
	add("state", "scratch dict, @self only")
	add("id", "str — this object's id")
	add("kind", "atom — what sort of object this is")
	for _, m := range scriptComplStructuralOf(src.Kind) {
		add(m.Name, m.Doc)
	}
	return out
}

// scriptComplVecItems is the list after a `.` on something that came out of a
// property or an attribute. The only member a value answers is a vector's
// components, and a script reads those constantly — `@self.prop.position.y` is
// how a script asks how high something is.
func scriptComplVecItems() []scriptComplItem {
	out := make([]scriptComplItem, 0, 3)
	for _, c := range []string{"x", "y", "z"} {
		out = append(out, scriptComplItem{Text: c, Label: c,
			Detail: "float — vector component", Group: "member"})
	}
	return out
}

// scriptComplPropItems is the list after `.prop.`.
func scriptComplPropItems(src scriptComplSource) []scriptComplItem {
	out := make([]scriptComplItem, 0, len(src.Props))
	for _, p := range src.Props {
		detail := p.Doc
		if !p.Writable {
			detail = "read-only — " + p.Doc
		}
		out = append(out, scriptComplItem{Text: p.Name, Label: p.Name,
			Detail: detail, Group: "prop"})
	}
	return out
}

// scriptComplAttrItems is the list after `.attr.`.
//
// Only what the named object actually carries. An attribute that does not exist
// yet is still writable — writing one creates it — so this list is a
// convenience rather than a contract, which is why an empty one is not reported
// as anything.
func scriptComplAttrItems(src scriptComplSource) []scriptComplItem {
	out := make([]scriptComplItem, 0, len(src.Attrs))
	for _, a := range src.Attrs {
		out = append(out, scriptComplItem{Text: a, Label: a, Detail: "attribute",
			Group: "attr"})
	}
	return out
}

// scriptComplTypeItems is the list where the grammar expects a type.
func scriptComplTypeItems(names []string) []scriptComplItem {
	out := make([]scriptComplItem, 0, len(names))
	for _, n := range names {
		out = append(out, scriptComplItem{Text: n, Label: n, Detail: "type", Group: "type"})
	}
	return out
}

// ---------- filtering and ranking ----------

// The match ranks, best first. A prefix is what someone typing a name they
// already know produces, so it outranks everything; a subsequence is what
// someone typing the memorable letters of a name they half-remember produces,
// which is worth offering and not worth offering first.
const (
	rankPrefixExact = iota
	rankPrefixFold
	rankContains
	rankSubsequence
	rankNone
)

// scriptComplRank scores name against what has been typed.
func scriptComplRank(prefix, name string) int {
	if prefix == "" {
		return rankPrefixExact
	}
	if strings.HasPrefix(name, prefix) {
		return rankPrefixExact
	}
	lowN, lowP := strings.ToLower(name), strings.ToLower(prefix)
	switch {
	case strings.HasPrefix(lowN, lowP):
		return rankPrefixFold
	case strings.Contains(lowN, lowP):
		return rankContains
	case scriptComplSubsequence(lowP, lowN):
		return rankSubsequence
	}
	return rankNone
}

// scriptComplSubsequence reports whether every byte of a appears in b, in
// order — `sptx` finding `set_prop_texture`.
func scriptComplSubsequence(a, b string) bool {
	i := 0
	for j := 0; i < len(a) && j < len(b); j++ {
		if a[i] == b[j] {
			i++
		}
	}
	return i == len(a)
}

// scriptComplFilter keeps the items that match prefix, best match first.
//
// The sort is stable within a rank, so the order scriptComplItems built — this
// function's variables before this file's functions before the standard library
// — survives as the tie-break. That ordering is the useful one: two names that
// match equally well are separated by which is closer to the caret.
func scriptComplFilter(items []scriptComplItem, prefix string) []scriptComplItem {
	type scored struct {
		it   scriptComplItem
		rank int
	}
	out := make([]scored, 0, len(items))
	for _, it := range items {
		if r := scriptComplRank(prefix, it.Text); r != rankNone {
			out = append(out, scored{it, r})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].rank < out[j].rank })

	list := make([]scriptComplItem, len(out))
	for i, s := range out {
		list[i] = s.it
	}
	return list
}

// ---------- accepting one ----------

// scriptComplEdit is what accepting an item does to the buffer: replace
// [Start, End) with Text, and leave the caret at Caret.
type scriptComplEdit struct {
	Start, End int
	Text       string
	Caret      int
}

// scriptComplAccept works out the splice for accepting it.
//
// Two pieces of punctuation are added by this rather than typed: an atom's
// closing colon, and a function's parentheses. Both are added only when they
// are not already there, so accepting a completion while correcting a name that
// is already written does not double them.
func scriptComplAccept(ctx scriptComplCtx, it scriptComplItem, src string, cursor int) scriptComplEdit {
	e := scriptComplEdit{Start: ctx.Start, End: cursor, Text: it.Text}
	if e.Start < 0 || e.End > len(src) || e.Start > e.End {
		return scriptComplEdit{Start: cursor, End: cursor, Caret: cursor}
	}
	// A name half-typed in the middle of a longer one replaces the whole of it:
	// the caret is where it is because that is where the author is editing, and
	// leaving the tail behind would produce a name neither of them meant.
	e.End = scriptComplWordEnd(src, cursor)
	e.Caret = e.Start + len(e.Text)

	switch ctx.Kind {
	case complAtom:
		if e.End >= len(src) || src[e.End] != ':' {
			e.Text += ":"
		}
		e.Caret = e.Start + len(e.Text)
		if e.End < len(src) && src[e.End] == ':' {
			e.Caret++ // past the colon that is already there
		}
	default:
		if it.Call && !scriptComplFollowedBy(src, e.End, '(') {
			e.Text += "()"
			e.Caret = e.Start + len(e.Text) - 1
		}
	}
	return e
}

// scriptComplWordEnd returns the end of the identifier the caret is inside, so
// that accepting replaces the whole word rather than splicing into it.
func scriptComplWordEnd(src string, cursor int) int {
	j := cursor
	for j < len(src) {
		r, w := utf8.DecodeRuneInString(src[j:])
		if w == 0 || !(r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			break
		}
		j += w
	}
	return j
}

// scriptComplFollowedBy reports whether the next non-space byte at or after i is
// c, on the same line.
func scriptComplFollowedBy(src string, i int, c byte) bool {
	for ; i < len(src); i++ {
		switch src[i] {
		case ' ', '\t':
		case c:
			return true
		default:
			return false
		}
	}
	return false
}

// ---------- the structural members, per kind ----------

// scriptComplMember is one step that is part of an object's shape rather than
// its data.
type scriptComplMember struct{ Name, Doc string }

// scriptComplStructuralTable is what each kind answers, mirroring
// levelHost.Member. A step added there and not here is offered by nothing,
// which is why the two are worth reading side by side.
var scriptComplStructuralTable = map[flang.Kind_][]scriptComplMember{
	// A brush has one structural step. Its faces have no IDs of their own, so
	// there is nothing to address one by — see levelHost.Member, whose switch
	// this table mirrors, and script_props_geom.go's brushProps.
	flang.RefBrush: {
		{"chunk", "the chunk this brush belongs to"},
	},
	flang.RefEntity: {
		{"model", "one model attachment by name: .model.<name>"},
		{"models", "list — every model attachment on the entity"},
		{"geometry", "the one chunk the entity's brushes materialise into"},
		{"brushes", "list — every brush the entity carries"},
		{"emitters", "list — every particle effect the entity carries"},
	},
	flang.RefModel: {{"entity", "the entity this attachment belongs to"}},
	flang.RefChunk: {
		{"brushes", "list — every brush in the chunk"},
	},
	flang.RefLevel: {
		{"entities", "list — every entity in the level"},
		{"volumes", "list — every volume in the level"},
	},
}

// scriptComplStructuralOf returns the structural steps for one kind.
//
// An unknown kind gets the union rather than nothing. That is the case for a
// handler's `src ref` and for every script parameter, which between them are
// most of the refs a script actually holds — offering nothing there would make
// the list useless exactly where it is needed, and an offer that turns out not
// to apply is reported by the linter on the next parse.
func scriptComplStructuralOf(kind flang.Kind_) []scriptComplMember {
	if kind != flang.RefNone {
		return scriptComplStructuralTable[kind]
	}
	seen := map[string]bool{}
	var out []scriptComplMember
	for _, k := range []flang.Kind_{flang.RefBrush, flang.RefChunk, flang.RefLevel, flang.RefEntity} {
		for _, m := range scriptComplStructuralTable[k] {
			if !seen[m.Name] {
				seen[m.Name] = true
				out = append(out, m)
			}
		}
	}
	return out
}

// scriptComplStepKind walks one structural step, mirroring the linter's kindOf.
//
// It used to have a good deal more to say: a sector's floor and ceiling were
// each a kind of their own, and an edge was a third, so walking `@room.floor.`
// changed what was in hand twice over. A brush's only structural step is back
// up to its chunk.
func scriptComplStepKind(k flang.Kind_, seg string) (flang.Kind_, bool) {
	switch k {
	case flang.RefBrush:
		if seg == "chunk" {
			return flang.RefChunk, true
		}
	case flang.RefEntity:
		if seg == "model" {
			return flang.RefModel, true
		}
	case flang.RefModel:
		// The attachment name completing `@guard.model.`, or the step back out
		// to the entity — the linter's kindOf reads the same way.
		if seg == "entity" {
			return flang.RefEntity, true
		}
		return flang.RefModel, true
	}
	return flang.RefNone, false
}

// scriptComplPathKind walks a whole chain of structural steps from a base kind.
// A step it cannot follow gives up rather than guessing, and an unknown kind is
// what makes the lists fall back to the union.
func scriptComplPathKind(base flang.Kind_, path []string) flang.Kind_ {
	k := base
	for _, seg := range path {
		next, ok := scriptComplStepKind(k, seg)
		if !ok {
			return flang.RefNone
		}
		k = next
	}
	return k
}
