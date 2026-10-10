package flang

// parser_test.go — the front end, exercised headlessly.
//
// The cases that earn their keep are the ambiguities: `<` as a vec versus a
// comparison, `(` as a tuple versus a call, `--` as a comment versus two
// minuses, and `:` as an atom versus a dict separator. Each of those is a place
// the grammar could have been ambiguous, and each has exactly one right reading.

import (
	"strings"
	"testing"
)

// mustParse parses src and fails the test on any diagnostic.
func mustParse(t *testing.T, src string) *File {
	t.Helper()
	f, errs := Parse("test.flang", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("unexpected errors parsing:\n%s\n\n%v", src, errs)
	}
	return f
}

// parseErrs parses src and returns only the diagnostics.
func parseErrs(t *testing.T, src string) ErrorList {
	t.Helper()
	_, errs := Parse("test.flang", []byte(src))
	return errs
}

// firstBody returns the statements of the first clause of the first function.
func firstBody(t *testing.T, f *File) []Stmt {
	t.Helper()
	if len(f.Funcs) == 0 || len(f.Funcs[0].Clauses) == 0 {
		t.Fatal("no clauses parsed")
	}
	return f.Funcs[0].Clauses[0].Body
}

// firstValue returns the expression of the first clause's `return`.
func firstValue(t *testing.T, src string) Expr {
	t.Helper()
	body := firstBody(t, mustParse(t, src))
	if len(body) == 0 {
		t.Fatal("empty body")
	}
	ret, ok := body[0].(*ReturnStmt)
	if !ok {
		t.Fatalf("first statement is %T, want *ReturnStmt", body[0])
	}
	return ret.Value
}

// ---------- lexer ----------

func TestLexerFormsTwoCharacterOperators(t *testing.T) {
	// `<-` in particular: read as `<` then `-`, every message send in the
	// language would parse as a comparison against a negation.
	lx := NewLexer("t", []byte("<- -> <= >= == != += -= *= /= < > -"))
	want := []Kind{Send, Arrow, LtEq, GtEq, Eq, NotEq, PlusEq, MinusEq, StarEq, SlashEq, Lt, Gt, Minus, EOF}
	got := lx.Tokens()
	if len(got) != len(want) {
		t.Fatalf("got %d tokens, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Kind != want[i] {
			t.Errorf("token %d: got %s, want %s", i, got[i].Kind, want[i])
		}
	}
}

func TestLexerTreatsDoubleDashAsComment(t *testing.T) {
	lx := NewLexer("t", []byte("1 -- everything here is a comment\n2"))
	toks := lx.Tokens()
	if len(toks) != 3 || toks[0].Text != "1" || toks[1].Text != "2" {
		t.Errorf("comment was not skipped: %v", toks)
	}
	if toks[1].Pos.Line != 2 {
		t.Errorf("line tracking is off: got line %d, want 2", toks[1].Pos.Line)
	}
}

func TestLexerDistinguishesAtomFromDictColon(t *testing.T) {
	cases := []struct {
		src   string
		kinds []Kind
	}{
		{`:damage:`, []Kind{Atom, EOF}},
		{`{"k": 1}`, []Kind{LBrace, String, Colon, Int, RBrace, EOF}},
		{`{"k": :on:}`, []Kind{LBrace, String, Colon, Atom, RBrace, EOF}},
	}
	for _, tc := range cases {
		got := NewLexer("t", []byte(tc.src)).Tokens()
		if len(got) != len(tc.kinds) {
			t.Errorf("%s: got %d tokens, want %d (%v)", tc.src, len(got), len(tc.kinds), got)
			continue
		}
		for i := range tc.kinds {
			if got[i].Kind != tc.kinds[i] {
				t.Errorf("%s: token %d is %s, want %s", tc.src, i, got[i].Kind, tc.kinds[i])
			}
		}
	}
}

func TestLexerRejectsUnterminatedString(t *testing.T) {
	lx := NewLexer("t", []byte(`"no closing quote`))
	lx.Tokens()
	if len(lx.Errors()) == 0 {
		t.Error("an unterminated string produced no diagnostic")
	}
}

func TestLexerDecodesEscapes(t *testing.T) {
	toks := NewLexer("t", []byte(`"a\nb\t\"c\\"`)).Tokens()
	if want := "a\nb\t\"c\\"; toks[0].Text != want {
		t.Errorf("got %q, want %q", toks[0].Text, want)
	}
}

// ---------- the two ambiguities ----------

func TestVecVersusComparison(t *testing.T) {
	// `<` in prefix position opens a vec; in infix position it is less-than.
	// This is the whole reason the parser is a Pratt parser.
	if v, ok := firstValue(t, "def f() -> vec\n  return <1.0, 2.0, 3.0>\nend").(*VecLit); !ok {
		t.Errorf("prefix `<` did not produce a vec, got %T", v)
	}

	cmp, ok := firstValue(t, "def f(a int, b int) -> bool\n  return a < b\nend").(*BinaryExpr)
	if !ok {
		t.Fatalf("infix `<` did not produce a comparison, got %T", cmp)
	}
	if cmp.Op != Lt {
		t.Errorf("got op %s, want <", cmp.Op)
	}
}

func TestVecAsCallArgument(t *testing.T) {
	// The case that made the rule necessary: a vec where an argument is expected.
	call, ok := firstValue(t, `def f() -> null
  return spawn(@door, <1.0, 0.0, 0.0>)
end`).(*CallExpr)
	if !ok {
		t.Fatalf("got %T, want a call", call)
	}
	if len(call.Args) != 2 {
		t.Fatalf("got %d arguments, want 2", len(call.Args))
	}
	if _, ok := call.Args[1].(*VecLit); !ok {
		t.Errorf("second argument is %T, want a vec", call.Args[1])
	}
}

func TestTupleVersusCall(t *testing.T) {
	// `(` in prefix position opens a tuple; following an expression it is a call.
	tup, ok := firstValue(t, "def f() -> tuple\n  return (1, 2)\nend").(*TupleLit)
	if !ok {
		t.Fatalf("prefix `(` gave %T, want a tuple", tup)
	}
	if len(tup.Elems) != 2 {
		t.Errorf("got %d elements, want 2", len(tup.Elems))
	}

	if call, ok := firstValue(t, "def f() -> int\n  return g(1, 2)\nend").(*CallExpr); !ok {
		t.Errorf("infix `(` gave %T, want a call", call)
	}
}

func TestParenthesisedExpressionIsNotATuple(t *testing.T) {
	// `(x)` is grouping and unwraps; `(x,)` is the one-tuple.
	if got := firstValue(t, "def f() -> int\n  return (1)\nend"); !isIntLit(got, 1) {
		t.Errorf("(1) gave %T, want a bare int", got)
	}
	tup, ok := firstValue(t, "def f() -> tuple\n  return (1,)\nend").(*TupleLit)
	if !ok {
		t.Fatalf("(1,) gave %T, want a tuple", tup)
	}
	if len(tup.Elems) != 1 {
		t.Errorf("(1,) has %d elements, want 1", len(tup.Elems))
	}
}

func isIntLit(e Expr, want int64) bool {
	v, ok := e.(*IntLit)
	return ok && v.Value == want
}

// ---------- precedence ----------

func TestOperatorPrecedence(t *testing.T) {
	// 1 + 2 * 3 must group as 1 + (2 * 3).
	top, ok := firstValue(t, "def f() -> int\n  return 1 + 2 * 3\nend").(*BinaryExpr)
	if !ok {
		t.Fatalf("got %T", top)
	}
	if top.Op != Plus {
		t.Fatalf("top operator is %s, want +", top.Op)
	}
	r, ok := top.R.(*BinaryExpr)
	if !ok || r.Op != Star {
		t.Errorf("multiplication did not bind tighter: right side is %T", top.R)
	}
}

func TestComparisonBindsTighterThanAnd(t *testing.T) {
	top, ok := firstValue(t, "def f(a int, b int) -> bool\n  return a < b and b < 10\nend").(*BinaryExpr)
	if !ok {
		t.Fatalf("got %T", top)
	}
	if top.Op != KwAnd {
		t.Errorf("top operator is %s, want and", top.Op)
	}
}

func TestBinaryOperatorsAreLeftAssociative(t *testing.T) {
	// 1 - 2 - 3 must group as (1 - 2) - 3, not 1 - (2 - 3).
	top, ok := firstValue(t, "def f() -> int\n  return 1 - 2 - 3\nend").(*BinaryExpr)
	if !ok {
		t.Fatalf("got %T", top)
	}
	if _, ok := top.L.(*BinaryExpr); !ok {
		t.Errorf("subtraction is right-associative: left side is %T", top.L)
	}
	if !isIntLit(top.R, 3) {
		t.Errorf("right side is %T, want the literal 3", top.R)
	}
}

// ---------- clauses and dispatch data ----------

func TestCollapsedClauseGroup(t *testing.T) {
	f := mustParse(t, `def fib(n int) when n > 1 -> int
    return fib(n - 1) + fib(n - 2)
fib(1)
    return 1
fib(0)
    return 0
end`)
	if len(f.Funcs) != 1 {
		t.Fatalf("got %d functions, want 1", len(f.Funcs))
	}
	g := f.Funcs[0]
	if g.Name != "fib" || g.RetType != "int" {
		t.Errorf("group is %q -> %q, want fib -> int", g.Name, g.RetType)
	}
	if len(g.Clauses) != 3 {
		t.Fatalf("got %d clauses, want 3", len(g.Clauses))
	}
	if g.Clauses[0].Guard == nil {
		t.Error("the guarded clause lost its guard")
	}
	if g.Clauses[1].Params[0].Kind != ParamLiteral {
		t.Errorf("fib(1)'s parameter is kind %d, want ParamLiteral", g.Clauses[1].Params[0].Kind)
	}
}

func TestClauseParameterKinds(t *testing.T) {
	f := mustParse(t, `def h(:damage:, src ref, amount, 0, -1, "go", true) -> null
    return
end`)
	got := f.Funcs[0].Clauses[0].Params
	want := []ParamKind{ParamAtom, ParamTyped, ParamBind, ParamLiteral, ParamLiteral, ParamLiteral, ParamLiteral}
	if len(got) != len(want) {
		t.Fatalf("got %d parameters, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Kind != want[i] {
			t.Errorf("parameter %d is kind %d, want %d", i, got[i].Kind, want[i])
		}
	}
	if got[0].Atom != "damage" {
		t.Errorf("atom parameter is %q, want damage", got[0].Atom)
	}
	if got[1].Type != "ref" {
		t.Errorf("typed parameter is %q, want ref", got[1].Type)
	}
}

func TestParameterSpecificityScores(t *testing.T) {
	// The ordering the dispatcher compares on: literal and atom beat a typed
	// binding, which beats an untyped one.
	f := mustParse(t, "def h(:a:, n int, x, 0) -> null\n  return\nend")
	want := []int{3, 1, 0, 3}
	for i, p := range f.Funcs[0].Clauses[0].Params {
		if got := p.Specificity(); got != want[i] {
			t.Errorf("parameter %d scored %d, want %d", i, got, want[i])
		}
	}
}

func TestClauseSignatureRendering(t *testing.T) {
	f := mustParse(t, `def on_damage(:damage:, src ref, amount int) -> null
    return
end`)
	want := "on_damage(:damage:, src ref, amount int)"
	if got := f.Funcs[0].Clauses[0].Signature(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---------- statements ----------

func TestParseSendStatement(t *testing.T) {
	body := firstBody(t, mustParse(t, `def f() -> null
    @door <- (:open:, @self, 20)
end`))
	s, ok := body[0].(*SendStmt)
	if !ok {
		t.Fatalf("got %T, want a send", body[0])
	}
	if a, ok := s.Target.(*AddrExpr); !ok || a.Name != "door" {
		t.Errorf("target is %T, want @door", s.Target)
	}
	tup, ok := s.Msg.(*TupleLit)
	if !ok {
		t.Fatalf("message is %T, want a tuple", s.Msg)
	}
	if len(tup.Elems) != 3 {
		t.Errorf("message has %d elements, want 3", len(tup.Elems))
	}
	if at, ok := tup.Elems[0].(*AtomLit); !ok || at.Name != "open" {
		t.Errorf("message does not begin with the atom :open:")
	}
}

// The delayed form is taken apart in the parser, so what the rest of the
// compiler meets is an ordinary send carrying a delay — no `after` call
// survives to be evaluated.
func TestParseDelayedSend(t *testing.T) {
	body := firstBody(t, mustParse(t, `def f() -> null
    @door <- after(500, :open:, @self, 20)
end`))
	s, ok := body[0].(*SendStmt)
	if !ok {
		t.Fatalf("got %T, want a send", body[0])
	}
	if a, ok := s.Target.(*AddrExpr); !ok || a.Name != "door" {
		t.Errorf("target is %T, want @door", s.Target)
	}
	if n, ok := s.Delay.(*IntLit); !ok || n.Value != 500 {
		t.Fatalf("delay is %v, want 500", s.Delay)
	}
	tup, ok := s.Msg.(*TupleLit)
	if !ok {
		t.Fatalf("message is %T, want a tuple", s.Msg)
	}
	if len(tup.Elems) != 3 {
		t.Errorf("message has %d elements, want 3", len(tup.Elems))
	}
	if at, ok := tup.Elems[0].(*AtomLit); !ok || at.Name != "open" {
		t.Errorf("message does not begin with the atom :open:")
	}
}

// A one-message timer still reads as a send of a one-tuple, without the comma
// the literal form needs.
func TestParseDelayedSendWithNoArguments(t *testing.T) {
	body := firstBody(t, mustParse(t, "def f() -> null\n  #lamps <- after(250, :flicker:)\nend"))
	s, ok := body[0].(*SendStmt)
	if !ok {
		t.Fatalf("got %T, want a send", body[0])
	}
	if tag, ok := s.Target.(*TagExpr); !ok || tag.Name != "lamps" {
		t.Errorf("target is %T, want #lamps", s.Target)
	}
	tup, ok := s.Msg.(*TupleLit)
	if !ok || len(tup.Elems) != 1 {
		t.Fatalf("message is %v, want a one-tuple", s.Msg)
	}
}

// An `after` with nothing to schedule is reported as a broken timer, not
// mistaken for a message that failed to be a tuple.
func TestDelayedSendNeedsAnAtom(t *testing.T) {
	errs := parseErrs(t, "def f() -> null\n  @door <- after(500)\nend")
	if len(errs) == 0 {
		t.Fatal("a message-less after produced no diagnostic")
	}
	if !strings.Contains(errs[0].Msg, "after") {
		t.Errorf("diagnostic does not mention after: %q", errs[0].Msg)
	}
}

func TestParseTagBroadcast(t *testing.T) {
	body := firstBody(t, mustParse(t, "def f() -> null\n  #lamps <- (:off:,)\nend"))
	s, ok := body[0].(*SendStmt)
	if !ok {
		t.Fatalf("got %T, want a send", body[0])
	}
	if tag, ok := s.Target.(*TagExpr); !ok || tag.Name != "lamps" {
		t.Errorf("target is %T, want #lamps", s.Target)
	}
}

func TestSendRejectsNonTupleMessage(t *testing.T) {
	// The first element names the message, so a bare value cannot be one.
	errs := parseErrs(t, "def f() -> null\n  @door <- 5\nend")
	if len(errs) == 0 {
		t.Fatal("a non-tuple message produced no diagnostic")
	}
	if !strings.Contains(errs[0].Msg, "tuple") {
		t.Errorf("diagnostic does not explain the problem: %q", errs[0].Msg)
	}
}

func TestParseAssignmentForms(t *testing.T) {
	body := firstBody(t, mustParse(t, `def f() -> null
    x = 1
    x += 2
    @self.attr.health -= 10
    #lamps.prop.brightness = 0.0
    @self.state["open"] = true
end`))
	if len(body) != 5 {
		t.Fatalf("got %d statements, want 5", len(body))
	}
	wantOps := []Kind{Assign, PlusEq, MinusEq, Assign, Assign}
	for i, s := range body {
		a, ok := s.(*AssignStmt)
		if !ok {
			t.Fatalf("statement %d is %T, want an assignment", i, s)
		}
		if a.Op != wantOps[i] {
			t.Errorf("statement %d uses %s, want %s", i, a.Op, wantOps[i])
		}
	}
	// The attribute path should be a member chain rooted at @self.
	a := body[2].(*AssignStmt)
	m, ok := a.Target.(*MemberExpr)
	if !ok || m.Name != "health" {
		t.Fatalf("target is %T, want a member access ending in health", a.Target)
	}
	inner, ok := m.X.(*MemberExpr)
	if !ok || inner.Name != "attr" {
		t.Errorf("target's parent is %T, want `.attr`", m.X)
	}
}

func TestParseControlFlow(t *testing.T) {
	body := firstBody(t, mustParse(t, `def f(n int) -> null
    if n > 0
        return
    elif n == 0
        n = 1
    else
        n = -1
    end
    for x in [1, 2, 3]
        continue
    end
    while n < 10
        n += 1
        break
    end
end`))
	if len(body) != 3 {
		t.Fatalf("got %d statements, want 3", len(body))
	}
	ifs, ok := body[0].(*IfStmt)
	if !ok {
		t.Fatalf("first statement is %T, want an if", body[0])
	}
	if len(ifs.Conds) != 2 || len(ifs.Blocks) != 2 || ifs.Else == nil {
		t.Errorf("if has %d conditions, %d blocks, else==nil:%v",
			len(ifs.Conds), len(ifs.Blocks), ifs.Else == nil)
	}
	if _, ok := body[1].(*ForStmt); !ok {
		t.Errorf("second statement is %T, want a for", body[1])
	}
	if _, ok := body[2].(*WhileStmt); !ok {
		t.Errorf("third statement is %T, want a while", body[2])
	}
}

func TestCallStatementIsNotMistakenForAClauseHead(t *testing.T) {
	// Both are `name(`. Telling them apart is what looksLikeClauseHead does, and
	// getting it wrong would silently truncate every body containing a call.
	f := mustParse(t, `def f() -> null
    log("one")
    log("two")
    return
end`)
	if n := len(f.Funcs[0].Clauses); n != 1 {
		t.Fatalf("got %d clauses, want 1 — calls were read as clause heads", n)
	}
	if n := len(f.Funcs[0].Clauses[0].Body); n != 3 {
		t.Errorf("body has %d statements, want 3", n)
	}
}

// ---------- literals ----------

func TestParseCompoundLiterals(t *testing.T) {
	list, ok := firstValue(t, `def f() -> list
    return [1, "two", :three:, <1.0, 2.0, 3.0>]
end`).(*ListLit)
	if !ok {
		t.Fatalf("got %T, want a list", list)
	}
	if len(list.Elems) != 4 {
		t.Fatalf("list has %d elements, want 4", len(list.Elems))
	}

	dict, ok := firstValue(t, `def f() -> dict
    return {"a": 1, "b": :on:}
end`).(*DictLit)
	if !ok {
		t.Fatalf("got %T, want a dict", dict)
	}
	if len(dict.Keys) != 2 || dict.Keys[0] != "a" || dict.Keys[1] != "b" {
		t.Errorf("dict keys are %v, want [a b]", dict.Keys)
	}
}

func TestTrailingCommasAreAccepted(t *testing.T) {
	mustParse(t, `def f() -> null
    x = [1, 2,]
    y = {"a": 1,}
    return
end`)
}

func TestDictKeyMustBeAString(t *testing.T) {
	errs := parseErrs(t, "def f() -> dict\n  return {a: 1}\nend")
	if len(errs) == 0 {
		t.Error("a bare dict key produced no diagnostic")
	}
}

func TestVecMustHaveThreeComponents(t *testing.T) {
	errs := parseErrs(t, "def f() -> vec\n  return <1.0, 2.0>\nend")
	if len(errs) == 0 {
		t.Fatal("a two-component vec produced no diagnostic")
	}
	if !strings.Contains(errs[0].Msg, "three") {
		t.Errorf("diagnostic does not say what is wrong: %q", errs[0].Msg)
	}
}

// ---------- imports and diagnostics ----------

func TestParseImport(t *testing.T) {
	f := mustParse(t, `import("shared/helpers.flang")
def f() -> null
    return
end`)
	if len(f.Imports) != 1 || f.Imports[0].Path != "shared/helpers.flang" {
		t.Errorf("imports are %v", f.Imports)
	}
}

func TestErrorsCarryFileAndPosition(t *testing.T) {
	_, errs := Parse("door.flang", []byte("def f() -> null\n    x = \nend"))
	if len(errs) == 0 {
		t.Fatal("no diagnostic for a missing right-hand side")
	}
	if errs[0].Pos.File != "door.flang" {
		t.Errorf("diagnostic names %q, want door.flang", errs[0].Pos.File)
	}
	if errs[0].Pos.Line != 2 && errs[0].Pos.Line != 3 {
		t.Errorf("diagnostic is on line %d, want the line with the mistake", errs[0].Pos.Line)
	}
}

func TestParserRecoversAndKeepsReading(t *testing.T) {
	// One broken definition must not cost the ones after it — the editor
	// underlines everything at once, and an outline should still list `good`.
	f, errs := Parse("t.flang", []byte(`def broken( -> null
    return
end
def good() -> int
    return 1
end`))
	if len(errs) == 0 {
		t.Fatal("the broken definition produced no diagnostic")
	}
	if f.Func("good") == nil {
		t.Error("recovery lost the definition after the broken one")
	}
}

func TestClausesInAGroupMustAgreeOnArity(t *testing.T) {
	errs := parseErrs(t, `def f(a int) -> int
    return a
f(a int, b int)
    return a
end`)
	if len(errs) == 0 {
		t.Fatal("mismatched arity produced no diagnostic")
	}
	if !strings.Contains(errs[0].Msg, "arity") {
		t.Errorf("diagnostic does not mention arity: %q", errs[0].Msg)
	}
}

func TestOnlyTheFirstClauseDeclaresAReturnType(t *testing.T) {
	errs := parseErrs(t, `def f(a int) -> int
    return a
f(0) -> str
    return "zero"
end`)
	if len(errs) == 0 {
		t.Fatal("a second return type produced no diagnostic")
	}
}

func TestUnknownTypeNameIsReported(t *testing.T) {
	errs := parseErrs(t, "def f(a widget) -> null\n  return\nend")
	if len(errs) == 0 {
		t.Fatal("an unknown parameter type produced no diagnostic")
	}
	if !strings.Contains(errs[0].Msg, "widget") {
		t.Errorf("diagnostic does not name the offending type: %q", errs[0].Msg)
	}
}

func TestTypeNamesAreNotReserved(t *testing.T) {
	// `int` is a type in a parameter's second position and an ordinary name
	// everywhere else, so this must parse.
	mustParse(t, "def f(int int) -> int\n  return int + 1\nend")
}

// ---------- the spec's own example ----------

func TestSpecDoorExampleParses(t *testing.T) {
	// The worked example from SCRIPTING.md. If the spec's own script does not
	// parse, one of the two is wrong.
	mustParse(t, `-- resources/scripts/door.flang

def on_enter(:enter:, other ref) -> null
    if @self.attr.locked
        return
    end
    if @self.state["open"] == true
        return
    end
    @self.state["open"] = true
    play_anim(@door_frame, "panel", "open")
    #door_lights.prop.color = <0.0, 1.0, 0.0>
end

def on_exit(:exit:, other ref) -> null
    if @self.state["open"] != true
        return
    end
    @self.state["open"] = false
    play_anim(@door_frame, "panel", "close")
end

def on_anim(:anim_end:, src ref, model str, anim str) when anim == "close"
    -> null
    #door_lights.prop.color = <1.0, 0.0, 0.0>
    @self.attr.uses += 1
end`)
}
