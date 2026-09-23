package devui

import (
	"strings"
	"testing"
)

// typeAndComplete types s (with the cursor left at the end) and presses Tab.
func typeAndComplete(s string) string {
	console.line = console.line[:0]
	console.cursor = 0
	resetCompletion()
	insert(s)
	complete(false)
	return string(console.line)
}

func TestTabCompletesAUniqueCommand(t *testing.T) {
	reset(t)
	if got := typeAndComplete("destr"); got != "destroy " {
		t.Fatalf("Tab gave %q, want \"destroy \"", got)
	}
}

func TestTabFillsInAsFarAsTheCandidatesAgree(t *testing.T) {
	reset(t)
	// "re" opens on reload, repl, restart, retry — which agree on "re" and no
	// further, so the line is unchanged and the candidates are listed instead.
	got := typeAndComplete("rel")
	if got != "reload " {
		t.Fatalf("Tab gave %q, want \"reload \"", got)
	}
	got = typeAndComplete("s")
	if !strings.HasPrefix(got, "s") {
		t.Fatalf("Tab gave %q", got)
	}
	if !console.compActive {
		t.Fatal("an ambiguous Tab did not start a cycle")
	}
	if !strings.Contains(output(), "spawn") {
		t.Fatalf("the candidates were not listed:\n%s", output())
	}
}

func TestASecondTabCyclesTheCandidates(t *testing.T) {
	reset(t)
	// The first Tab lists; the next offers the first candidate, and the one
	// after that the second.
	typeAndComplete("s")
	all := append([]string{}, console.compAll...)
	if len(all) < 2 {
		t.Fatalf("only %v start with \"s\" — this test needs at least two", all)
	}
	complete(false)
	if got := string(console.line); got != all[0] {
		t.Fatalf("the first cycling Tab gave %q, want %q", got, all[0])
	}
	complete(false)
	if got := string(console.line); got != all[1] {
		t.Fatalf("the second gave %q, want %q", got, all[1])
	}
	complete(true)
	if got := string(console.line); got != all[0] {
		t.Fatalf("Shift+Tab gave %q, want back to %q", got, all[0])
	}
}

func TestCompletionIsPositionalNotGlobal(t *testing.T) {
	reset(t)
	// The first word is a command; the word after `help` is also a command,
	// and the word after `spawn` is not.
	console.line = []rune("help de")
	console.cursor = len(console.line)
	if got := candidates(tokenIndex(tokenStart()), "de"); len(got) == 0 {
		t.Fatal("no completions after `help`")
	} else if got[0] != "destroy" {
		t.Fatalf("after `help`, candidates = %v", got)
	}

	console.line = []rune("spawn de")
	console.cursor = len(console.line)
	for _, c := range candidates(tokenIndex(tokenStart()), "de") {
		if c == "destroy" {
			t.Fatal("a command was offered as an entity type")
		}
	}
}

func TestTokenIndexCountsWords(t *testing.T) {
	for _, tc := range []struct {
		line string
		want int
	}{
		{"tp ", 1},
		{"tp bea", 1},
		{"spawn grenade 1 ", 3},
		{"", 0},
		{"tp", 0},
	} {
		console.line = []rune(tc.line)
		console.cursor = len(console.line)
		if got := tokenIndex(tokenStart()); got != tc.want {
			t.Errorf("tokenIndex(%q) = %d, want %d", tc.line, got, tc.want)
		}
	}
}

func TestReplaceTokenKeepsWhatFollowsTheCursor(t *testing.T) {
	reset(t)
	console.line = []rune("tp bea con")
	console.cursor = 6 // just after "bea"
	replaceToken(3, "beacon")
	if got := string(console.line); got != "tp beacon con" {
		t.Fatalf("line = %q", got)
	}
	if console.cursor != len("tp beacon") {
		t.Fatalf("cursor = %d", console.cursor)
	}
}

func TestCommonPrefix(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"reload", "repl", "restart"}, "re"},
		{[]string{"spawn"}, "spawn"},
		{[]string{"a", "b"}, ""},
		{nil, ""},
	} {
		if got := commonPrefix(tc.in); got != tc.want {
			t.Errorf("commonPrefix(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTheFlangPromptOffersNamesButNotCommands(t *testing.T) {
	reset(t)
	console.repl = true
	console.line = []rune("des")
	console.cursor = 3
	if got := candidates(0, "des"); got != nil {
		t.Fatalf("the flang prompt offered commands: %v", got)
	}
	// A sigil still completes, which is the case that matters there.
	console.line = []rune("@")
	console.cursor = 1
	_ = candidates(0, "@") // no level in this test; the path must not panic
}
