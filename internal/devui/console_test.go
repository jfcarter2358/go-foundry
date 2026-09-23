package devui

import (
	"strings"
	"testing"

	"foundry/internal/hooks"

	"github.com/veandco/go-sdl2/sdl"
)

// reset puts the package's one console back to its zero state, and turns the
// tools on. Every test that touches the console starts with it, because the
// state is package-level for the same reason the keyboard is.
func reset(t *testing.T) {
	t.Helper()
	console = consoleState{}
	SetEnabled(true)
	t.Cleanup(func() {
		console = consoleState{}
		SetEnabled(false)
	})
}

// typeLine types s and presses Return.
func typeLine(s string) {
	insert(s)
	submit()
}

func TestTruthyAcceptsTheSpellingsSomeoneWouldType(t *testing.T) {
	for _, s := range []string{"true", "TRUE", " true ", "1", "yes", "on"} {
		if !truthy(s) {
			t.Errorf("truthy(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "false", "0", "no", "off", "maybe"} {
		if truthy(s) {
			t.Errorf("truthy(%q) = true, want false", s)
		}
	}
}

func TestInitReadsTheEnvironmentAndTheBuild(t *testing.T) {
	t.Cleanup(func() { SetEnabled(false) })

	t.Setenv(EnvVar, "true")
	Init()
	// The tools are for the build that has no editor. This test suite runs in
	// both configurations, so what Init should have done depends on which one
	// it is — and that the two disagree is the whole point.
	if got, want := Enabled(), !hooks.DevBuild; got != want {
		t.Fatalf("%s=true gave Enabled()=%v in a build where DevBuild=%v", EnvVar, got, hooks.DevBuild)
	}

	t.Setenv(EnvVar, "")
	Init()
	if Enabled() {
		t.Fatalf("%s unset left the tools enabled", EnvVar)
	}
}

func TestToggleDoesNothingWhileTheToolsAreOff(t *testing.T) {
	SetEnabled(false)
	Toggle()
	if Open() {
		t.Fatal("the console opened with the tools disabled")
	}
}

func TestTypingEditsTheLine(t *testing.T) {
	reset(t)
	insert("spawn grenade")
	if got := string(console.line); got != "spawn grenade" {
		t.Fatalf("line = %q", got)
	}
	// Home, then a word right, then delete the word: the shell motions.
	console.cursor = 0
	console.cursor = wordRight(console.line, console.cursor)
	if got := console.cursor; got != 6 {
		t.Fatalf("wordRight put the cursor at %d, want 6 (past \"spawn \")", got)
	}
	console.cursor = len(console.line)
	deleteWordLeft()
	if got := string(console.line); got != "spawn " {
		t.Fatalf("after deleting a word: %q", got)
	}
}

func TestSubmitQueuesTheLineAndRecordsIt(t *testing.T) {
	reset(t)
	typeLine("help")
	if len(console.pending) != 1 || console.pending[0] != "help" {
		t.Fatalf("pending = %v", console.pending)
	}
	if len(console.history) != 1 || console.history[0] != "help" {
		t.Fatalf("history = %v", console.history)
	}
	// The same line twice is one history entry, so Up walks distinct commands.
	typeLine("help")
	if len(console.history) != 1 {
		t.Fatalf("a repeated line was recorded twice: %v", console.history)
	}
}

func TestNothingRunsUntilTick(t *testing.T) {
	reset(t)
	typeLine("clear")
	if len(console.pending) != 1 {
		t.Fatal("the line ran during the event pump")
	}
}

func TestHistoryWalksAndKeepsTheDraft(t *testing.T) {
	reset(t)
	typeLine("first")
	typeLine("second")
	insert("draft")

	historyStep(-1)
	if got := string(console.line); got != "second" {
		t.Fatalf("Up gave %q, want \"second\"", got)
	}
	historyStep(-1)
	if got := string(console.line); got != "first" {
		t.Fatalf("Up twice gave %q, want \"first\"", got)
	}
	historyStep(1)
	historyStep(1)
	if got := string(console.line); got != "draft" {
		t.Fatalf("Down back to the end gave %q, want the draft", got)
	}
}

func TestABackslashContinuesTheLine(t *testing.T) {
	reset(t)
	typeLine("for r in find(\"lamp\") \\")
	if len(console.pending) != 0 {
		t.Fatalf("a continued line was queued early: %v", console.pending)
	}
	if got := prompt(); !strings.HasPrefix(got, "...") {
		t.Fatalf("prompt = %q, want the continuation prompt", got)
	}
	typeLine("end")
	if len(console.pending) != 1 {
		t.Fatalf("pending = %v", console.pending)
	}
	if !strings.Contains(console.pending[0], "\n") {
		t.Fatalf("the continued lines were not joined: %q", console.pending[0])
	}
}

func TestPastedNewlinesSubmitEachLine(t *testing.T) {
	reset(t)
	insert("pos\ntags\n")
	if len(console.pending) != 2 {
		t.Fatalf("pending = %v, want one entry per pasted line", console.pending)
	}
}

func TestTheKeyThatOpensTheConsoleIsNotAlsoTyped(t *testing.T) {
	reset(t)
	Toggle()
	// SDL delivers the `~` as text right after the key-down that opened the
	// console. It must not land in the line.
	if !console.swallowText {
		t.Fatal("the opening keystroke was not marked for swallowing")
	}
	if !HandleEvent(tilde()) {
		t.Fatal("the console did not consume the text event")
	}
	if len(console.line) != 0 {
		t.Fatalf("line = %q, want empty", string(console.line))
	}
	// And the one after it is ordinary typing again.
	if !HandleEvent(tilde()) {
		t.Fatal("the console did not consume the second text event")
	}
	if got := string(console.line); got != "~" {
		t.Fatalf("line = %q, want a tilde: only the opening keystroke is swallowed", got)
	}
}

// tilde is the text event SDL sends for the ~ key.
func tilde() sdl.TextInputEvent {
	return sdl.TextInputEvent{Type: sdl.TEXTINPUT, Text: "~"}
}

func TestTheConsoleConsumesEveryKeyWhileItIsUp(t *testing.T) {
	reset(t)
	Toggle()
	ev := sdl.KeyboardEvent{Type: sdl.KEYDOWN, State: sdl.PRESSED}
	ev.Keysym.Scancode = sdl.SCANCODE_W
	if !HandleEvent(ev) {
		t.Fatal("a key went past the console into gameplay")
	}
	ev.Type, ev.State = sdl.KEYUP, sdl.RELEASED
	if !HandleEvent(ev) {
		t.Fatal("a key-up went past the console")
	}
}

func TestClosedConsoleTakesNothing(t *testing.T) {
	reset(t)
	ev := sdl.KeyboardEvent{Type: sdl.KEYDOWN, State: sdl.PRESSED}
	ev.Keysym.Scancode = sdl.SCANCODE_W
	if HandleEvent(ev) {
		t.Fatal("a closed console ate a gameplay key")
	}
}

func TestScrollbackIsBounded(t *testing.T) {
	reset(t)
	for i := 0; i < scrollbackLimit+50; i++ {
		printf(kindOut, "line %d", i)
	}
	if len(console.back) > scrollbackLimit {
		t.Fatalf("scrollback grew to %d, past its %d limit", len(console.back), scrollbackLimit)
	}
	if got := console.back[len(console.back)-1].text; got != "line 2049" {
		t.Fatalf("the newest line is %q — the ring dropped the wrong end", got)
	}
}

func TestTildeOpensTheConsoleAheadOfEverythingElse(t *testing.T) {
	reset(t)
	ev := sdl.KeyboardEvent{Type: sdl.KEYDOWN, State: sdl.PRESSED}
	ev.Keysym.Scancode = sdl.SCANCODE_GRAVE

	// A closed console takes this one key and nothing else, so that it opens
	// over a modal screen — which consumes every key it does not recognise,
	// and would otherwise swallow the one that opens this.
	if !HandleEvent(ev) {
		t.Fatal("tilde did not reach the console")
	}
	if !Open() {
		t.Fatal("tilde did not open the console")
	}
	// And closes it again.
	if !HandleEvent(ev) || Open() {
		t.Fatal("tilde did not close the console")
	}
}

func TestTildeDoesNothingWithTheToolsOff(t *testing.T) {
	SetEnabled(false)
	ev := sdl.KeyboardEvent{Type: sdl.KEYDOWN, State: sdl.PRESSED}
	ev.Keysym.Scancode = sdl.SCANCODE_GRAVE
	if HandleEvent(ev) {
		t.Fatal("the console took tilde with the tools disabled")
	}
	if Open() {
		t.Fatal("the console opened with the tools disabled")
	}
}
