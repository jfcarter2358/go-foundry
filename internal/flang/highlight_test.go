package flang

// highlight_test.go — the colourist, which is a second scanner over the same
// source and so has to agree with the lexer about every ambiguity it shares.
//
// The cases worth having are the ones where a word's class depends on where it
// sits: `int` as a type against `int` as a variable, `find` as a builtin
// against `find` as a name, `:` as an atom against `:` as a separator, and a
// `--` that is a comment against a `-` that is not.

import (
	"strings"
	"testing"
)

// classOf returns the class Highlight gave the first occurrence of want in src,
// or ClassPlain if no span covers it.
func classOf(t *testing.T, src, want string) Class {
	t.Helper()
	at := strings.Index(src, want)
	if at < 0 {
		t.Fatalf("%q does not appear in the source", want)
	}
	for _, s := range Highlight(src) {
		if s.Start <= at && at < s.End {
			return s.Class
		}
	}
	return ClassPlain
}

// checkClass asserts the class of want's first occurrence.
func checkClass(t *testing.T, src, want string, class Class) {
	t.Helper()
	if got := classOf(t, src, want); got != class {
		t.Errorf("%q in %q: got %v, want %v", want, src, got, class)
	}
}

func TestHighlightBasics(t *testing.T) {
	src := `def go(:spawn:) -> null
    log("hi", 3, 4.5)
end`
	checkClass(t, src, "def", ClassKeyword)
	checkClass(t, src, ":spawn:", ClassAtom)
	checkClass(t, src, "null", ClassKeyword)
	checkClass(t, src, `"hi"`, ClassString)
	checkClass(t, src, "3", ClassNumber)
	checkClass(t, src, "4.5", ClassNumber)
	checkClass(t, src, "log", ClassBuiltin)
	checkClass(t, src, "go", ClassPlain)
	checkClass(t, src, "->", ClassOperator)
}

func TestHighlightComment(t *testing.T) {
	src := "x = 1 -- a comment with \"quotes\" and :atoms:\ny = 2"
	checkClass(t, src, "-- a", ClassComment)
	checkClass(t, src, `"quotes"`, ClassComment)
	// The comment ends at the newline, and does not swallow the next line.
	checkClass(t, src, "y", ClassPlain)
	checkClass(t, src, "2", ClassNumber)
}

func TestHighlightMinusIsNotAComment(t *testing.T) {
	src := "x = a - b"
	checkClass(t, src, "- b", ClassOperator)
}

func TestHighlightTypePositions(t *testing.T) {
	// A parameter declaration, a def head, a later clause of the same function,
	// and a tuple return type: every position where a type name is a type.
	src := `param drop float = 3.0
def hit(n int, s str) -> (int, str)
hit(other float)
end`
	checkClass(t, src, "float = ", ClassType)
	checkClass(t, src, "int,", ClassType)
	checkClass(t, src, "str)", ClassType)
	checkClass(t, src, "(int, str)", ClassOperator) // the opening paren
	checkClass(t, src, "float)", ClassType)
	// The names being typed are just names.
	checkClass(t, src, "drop", ClassPlain)
	checkClass(t, src, "n ", ClassPlain)
}

func TestHighlightTypeNameAsVariable(t *testing.T) {
	// Outside any of those positions, a type name is an ordinary variable.
	src := "x = 1\nlist = 3\nfloat = list + 1"
	checkClass(t, src, "list = 3", ClassPlain)
	checkClass(t, src, "float =", ClassPlain)
	checkClass(t, src, "list + 1", ClassPlain)
}

func TestHighlightBuiltinOnlyWhenCalled(t *testing.T) {
	checkClass(t, "log(1)", "log", ClassBuiltin)
	checkClass(t, "log (1)", "log", ClassBuiltin)
	checkClass(t, "x = log + 1", "log", ClassPlain)
}

func TestHighlightAddresses(t *testing.T) {
	src := "@self.state.n = #breakable\n@door <- (:open:, @self)"
	checkClass(t, src, "@self", ClassAddress)
	checkClass(t, src, "#breakable", ClassAddress)
	checkClass(t, src, "@door", ClassAddress)
	checkClass(t, src, "<-", ClassOperator)
	checkClass(t, src, ".state", ClassOperator) // the dot
	checkClass(t, src, "state", ClassPlain)
}

// A parameter has its own colour, because it has its own sigil: `@door` reaches
// the level and `?door` reaches what the level bound, and a reader who cannot
// tell them apart at a glance is back where the shared sigil left them.
func TestHighlightParamsAreNotAddresses(t *testing.T) {
	src := "param door ref\n\ndef main() -> null\n    ?door.ceil.prop.height = 1.0\n    @door <- (:open:,)\nend"
	checkClass(t, src, "?door", ClassParam)
	checkClass(t, src, "@door", ClassAddress)
	checkClass(t, src, "param", ClassKeyword)
	checkClass(t, src, "ref", ClassType)
}

// A `?` with no name after it is punctuation, not a half-coloured parameter —
// the same rule `@` and `#` follow, so a line being typed never flickers.
func TestHighlightBareQuestionIsOperator(t *testing.T) {
	checkClass(t, "x = ? ", "?", ClassOperator)
}

func TestHighlightAtomAgainstSeparator(t *testing.T) {
	src := `x = {"k": :on:}`
	checkClass(t, src, `": :`, ClassString) // the string, up to its quote
	checkClass(t, src, ": :on:", ClassOperator)
	checkClass(t, src, ":on:", ClassAtom)
}

func TestHighlightUnterminatedString(t *testing.T) {
	src := "log(\"oops)\nx = 1"
	checkClass(t, src, `"oops)`, ClassInvalid)
	// The damage stops at the newline: the next line is scanned normally.
	checkClass(t, src, "x", ClassPlain)
	checkClass(t, src, "1", ClassNumber)
}

func TestHighlightBadNumber(t *testing.T) {
	checkClass(t, "x = 3x", "3x", ClassInvalid)
}

func TestHighlightSpansAreOrderedAndDisjoint(t *testing.T) {
	src := scriptSampleSource
	prev := 0
	for _, s := range Highlight(src) {
		if s.Start < prev {
			t.Fatalf("span %v starts before the previous one ended (%d)", s, prev)
		}
		if s.End <= s.Start || s.End > len(src) {
			t.Fatalf("span %v is not a valid range of %d bytes", s, len(src))
		}
		if s.Class == ClassPlain {
			t.Fatalf("span %v is plain; plain is meant to be the gap between spans", s)
		}
		prev = s.End
	}
}

// scriptSampleSource is a file that uses most of the language at once.
const scriptSampleSource = `-- a sample
import "shared.flang"

param target ref
param drop float = 3.0

def on_interact(:interact:, src ref) -> null
    if @target == null or not @latching
        return
    end
    for e in #breakable
        e.prop.health = e.prop.health - 1
        log("hit ", e.id, " at ", time())
    end
    @target.floor.prop.height = @target.floor.prop.height - @drop
end
`

func TestHighlightNeverPanics(t *testing.T) {
	// Every prefix of a real file is a state the editor will actually be in
	// halfway through a keystroke, and none of them may fail.
	for i := 0; i <= len(scriptSampleSource); i++ {
		Highlight(scriptSampleSource[:i])
	}
	for _, src := range []string{"", "\x00", "\xff\xfe", "\"", ":", "@", "#", "--", "3.", "π = 1"} {
		Highlight(src)
	}
}
