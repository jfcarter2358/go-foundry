package devui

import (
	"strings"
	"testing"

	"foundry/internal/world"
)

func TestAnExpressionPrintsItsValue(t *testing.T) {
	reset(t)
	lvl := &world.Level{}
	evalFlang(lvl, "1 + 2")
	if got := strings.TrimSpace(output()); got != "3" {
		t.Fatalf("output = %q, want \"3\"", got)
	}
	if lastKind() != kindOK {
		t.Errorf("a value was not reported as one")
	}
}

func TestAStatementRunsAndPrintsNothing(t *testing.T) {
	reset(t)
	lvl := &world.Level{}
	// An assignment is not an expression: the expression wrapper fails to
	// parse, and the statement wrapper has to be tried without anything
	// having run in between.
	evalFlang(lvl, "x = 1 + 1")
	if got := strings.TrimSpace(output()); got != "" {
		t.Fatalf("a statement printed %q", got)
	}
}

func TestLogFromAnEvaluatedLineLandsInTheConsole(t *testing.T) {
	reset(t)
	evalFlang(&world.Level{}, `log("from the prompt")`)
	if !strings.Contains(output(), "from the prompt") {
		t.Fatalf("log output did not reach the scrollback:\n%s", output())
	}
	// And the null that log() returns is not printed after it.
	if strings.Contains(output(), "null") {
		t.Fatalf("a null result was printed:\n%s", output())
	}
}

func TestASyntaxErrorIsReportedAgainstTheTypedLine(t *testing.T) {
	reset(t)
	evalFlang(&world.Level{}, "1 +")
	out := output()
	if lastKind() != kindErr {
		t.Fatalf("a broken line was not reported as an error:\n%s", out)
	}
	// The wrapper puts the typed line on the compiler's line 2. Reporting
	// "console:2" would name a file nobody wrote.
	if strings.Contains(out, "console:2") {
		t.Fatalf("the wrapper's line number leaked out:\n%s", out)
	}
}

func TestAMultiLineEntryEvaluates(t *testing.T) {
	reset(t)
	evalFlang(&world.Level{}, "n = 0\nfor i in [1, 2, 3]\n    n = n + i\nend\nlog(n)")
	if !strings.Contains(output(), "6") {
		t.Fatalf("a continued entry did not run:\n%s", output())
	}
}

func TestEvalWithNoLevelSaysSo(t *testing.T) {
	reset(t)
	evalFlang(nil, "1 + 1")
	if !strings.Contains(output(), "no level") {
		t.Fatalf("output = %q", output())
	}
}

func TestShiftPosMovesLineNumbersOntoTheTypedLine(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"console:2:9: something went wrong", "1:9: something went wrong"},
		{"console:5:1: and here", "4:1: and here"},
		{"console:1:1: the wrapper itself", "console:1:1: the wrapper itself"},
		{"nothing to do with a position", "nothing to do with a position"},
	} {
		if got := shiftPos(tc.in); got != tc.want {
			t.Errorf("shiftPos(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStateSurvivesBetweenLines(t *testing.T) {
	reset(t)
	lvl := &world.Level{}
	// A local dies with the call that made it, as it does in any handler. The
	// level's scratch is what a prompt can keep something in — and the console
	// evaluates as the level, so both spellings reach the same dict.
	evalFlang(lvl, `@self.state["probe"] = 7`)
	console.back = console.back[:0]
	evalFlang(lvl, `@level.state["probe"]`)
	if got := strings.TrimSpace(output()); got != "7" {
		t.Fatalf("output = %q, want \"7\" — the level's scratch did not survive the line", got)
	}
}

func TestARefFromFindCanBeWrittenThrough(t *testing.T) {
	reset(t)
	lvl := levelWithPlayer(t)
	evalFlang(lvl, "for r in find(\"marker\")\n    r.prop.position = <9.0, 9.0, 9.0>\nend")
	if lastKind() == kindErr {
		t.Fatalf("writing through a loop variable failed:\n%s", output())
	}
	if got := lvl.Entities[0].Position; got[0] != 9 {
		t.Fatalf("the entity is at %v, want it moved", got)
	}
}

func TestALoopTypedAtThePromptActuallyRuns(t *testing.T) {
	reset(t)
	lvl := levelWithPlayer(t)
	// The regression this guards: `return` with no expression is legal flang,
	// so wrapping a loop in `return <line>` used to parse as a bare return
	// followed by unreachable code — linking, running, returning null, and
	// doing nothing whatever. It failed silently, which is the worst way for a
	// prompt to fail.
	evalFlang(lvl, "for r in find(\"marker\")\n    log(\"reached\")\nend")
	if !strings.Contains(output(), "reached") {
		t.Fatalf("the loop body never ran:\n%s", output())
	}
}

func TestABlockStatementReportsItsOwnErrors(t *testing.T) {
	reset(t)
	evalFlang(&world.Level{}, "for r in [1, 2\n    log(r)\nend")
	if lastKind() != kindErr {
		t.Fatalf("a broken loop was not reported:\n%s", output())
	}
}
