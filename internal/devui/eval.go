package devui

// eval.go — the flang read-eval-print loop.
//
// A console with a fixed list of commands can only do what somebody thought of
// in advance. The script runtime is already in the binary, already bound to the
// running level, and already has a verb for everything a level can be asked to
// do — so the most useful thing this console can offer is a way to type into
// it.
//
// One line is compiled as a whole program and run once, the way the script
// editor's Run button compiles a file (script.RunScriptValue). Two shapes are
// tried, in order:
//
//	def __console()
//	    <value> = <line>       -- an expression: @player.prop.position
//	    return <value>
//	end
//
//	def __console()
//	    <line>                 -- a statement: @door.prop.open = true
//	end
//
// The expression form first, because it is the one with an answer to print.
// Linking happens before anything runs, so a line that is not an expression
// costs a failed parse and nothing else — nothing has happened to the world by
// the time the second form is tried. When both fail it is the second error that
// is reported: a line that is neither an expression nor a statement is almost
// always a statement with a mistake in it, and "expected an expression" would
// point at the wrapper rather than at the line.
//
// The expression form assigns rather than returning directly, and that detail
// is load-bearing. `return` with no expression is legal flang, so
// `return for r in find("x") … end` parses as a bare return followed by a loop
// the function never reaches — it links, it runs, it returns null, and the
// typed line silently does nothing at all. An assignment's right-hand side has
// to be a whole expression, so the same line is a parse error and falls through
// to the statement form, which is where it belonged.
//
// Nothing survives between lines except the world. A local dies with the call
// that made it, as it does in any handler; `@level.state["x"]` is the scratch
// that lasts, and it is the same dict a level script would use.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"foundry/internal/flang"
	"foundry/internal/script"
	"foundry/internal/world"
)

// evalValue evaluates one expression and hands back what it came to, printing
// nothing but the line's own `log()` output.
//
// This is what a command uses when it needs an answer out of the world rather
// than when it is showing one to a person: `pause` reads @level.prop.paused
// through it before deciding what to flip it to.
func evalValue(lvl *world.Level, expr string) (flang.Value, error) {
	if lvl == nil {
		return nil, errNoLevel
	}
	out := func(s string) { printf(kindOut, "%s", s) }
	v, err, linked := tryEval(lvl, exprBody(expr), out)
	if !linked {
		return nil, cleanErr(err)
	}
	return v, cleanErr(err)
}

// evalStatement runs one statement and reports only whether it worked. What it
// did is the command's to describe.
func evalStatement(lvl *world.Level, stmt string) error {
	if lvl == nil {
		return errNoLevel
	}
	out := func(s string) { printf(kindOut, "%s", s) }
	_, err, _ := tryEval(lvl, "    "+stmt, out)
	return cleanErr(err)
}

// errNoLevel is what every eval says when there is nothing to evaluate against.
var errNoLevel = errors.New("no level is loaded")

// cleanErr moves an error's positions onto the line that was typed, so that a
// command reporting one with %v says something a person can act on.
func cleanErr(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(trimPos(err.Error()))
}

// evalFile is the name compiled lines are reported under. It shows up in
// runtime errors, so it is a word rather than a path.
const evalFile = "console"

// evalFlang runs one line of flang against lvl and prints the result.
func evalFlang(lvl *world.Level, line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	if lvl == nil {
		printf(kindErr, "no level is loaded")
		return
	}

	out := func(s string) { printf(kindOut, "%s", s) }

	if v, err, linked := tryEval(lvl, exprBody(line), out); linked {
		report(v, err)
		return
	}
	v, err, linked := tryEval(lvl, indented(line), out)
	if !linked {
		// tryEval only reports "did not link" with the error that says why.
		report(nil, err)
		return
	}
	report(v, err)
}

// tryEval compiles one body and, if it compiled, runs it.
//
// linked false means the source did not get as far as running, which is what
// lets the caller try the other shape without having changed anything.
func tryEval(lvl *world.Level, body string, out func(string)) (v flang.Value, err error, linked bool) {
	src := "def __console()\n" + body + "\nend\n"
	v, err = script.RunScriptValue(lvl, evalFile, src, "__console", out)
	if err != nil && isCompileError(err) {
		return nil, err, false
	}
	return v, err, true
}

// valueName is the local the expression form assigns to. Long and ugly on
// purpose: it shares a scope with nothing, since the typed line is the whole of
// the right-hand side, but a name anyone might use by accident would be a
// puzzle rather than an error.
const valueLocal = "__console_value"

// exprBody is the expression wrapper's body: the line as a right-hand side,
// then the result. See the note at the top of the file for why it is not a
// `return`.
func exprBody(line string) string {
	return "    " + valueLocal + " = " + strings.TrimLeft(indented(line), " ") +
		"\n    return " + valueLocal
}

// indented puts the typed line where the wrapper's body goes. A continued line
// (see the backslash in console.go) arrives as several, and every one of them
// needs it.
func indented(line string) string {
	parts := strings.Split(line, "\n")
	for i, p := range parts {
		parts[i] = "    " + p
	}
	return strings.Join(parts, "\n")
}

// isCompileError says whether an error came from linking rather than from
// running.
//
// RunScriptValue returns both as plain errors, and the difference matters here
// and nowhere else — a runtime error means the line ran and failed, which must
// not be retried in the other shape. A *flang.RuntimeError is the one the
// interpreter raises; everything else at this point is the parser.
func isCompileError(err error) bool {
	if err == nil {
		return false
	}
	var re *flang.RuntimeError
	return !errors.As(err, &re)
}

// report prints what a line came to, or why it did not.
func report(v flang.Value, err error) {
	if err != nil {
		for _, s := range strings.Split(trimPos(err.Error()), "\n") {
			printf(kindErr, "%s", s)
		}
		return
	}
	// A statement has nothing to say, and neither has an expression that
	// evaluated to null: `log("hi")` printing "hi" and then "null" is noise.
	if v == nil || v.Type() == flang.TypeNull {
		return
	}
	printf(kindOK, "%s", v.String())
}

// trimPos rewrites the positions in an error so they point at what was typed.
//
// Everything is compiled inside a one-function wrapper, so the line the person
// typed is the compiler's line 2. Reporting "console:2:9" would be true of a
// file they never saw; "1:9" is true of the line in front of them.
func trimPos(msg string) string {
	out := make([]string, 0, 4)
	for _, line := range strings.Split(msg, "\n") {
		out = append(out, shiftPos(line))
	}
	return strings.Join(out, "\n")
}

// shiftPos rewrites one leading "console:<line>:<col>:" into "<line-1>:<col>:".
func shiftPos(s string) string {
	const prefix = evalFile + ":"
	if !strings.HasPrefix(s, prefix) {
		return s
	}
	rest := s[len(prefix):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return s
	}
	n, err := strconv.Atoi(rest[:colon])
	if err != nil || n < 2 {
		return s
	}
	return fmt.Sprintf("%d%s", n-1, rest[colon:])
}
