package main

// script_log_test.go — the log ring and the window's filtering.
//
// The ring is the part worth testing carefully: it is bounded, it wraps, and the
// window reads it while the runtime writes to it. All three are places where a
// buffer quietly loses or reorders entries, and a log that lies about what
// happened is worse than no log.

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"foundry/internal/flang"
)

// newTestRing returns a ring of the given size that echoes nowhere.
func newTestRing(t *testing.T, size int) *logRing {
	t.Helper()
	return &logRing{entries: make([]LogEntry, size)}
}

// withScriptLog swaps the global ring for a test one, restoring it afterwards in
// the style of withTempTextureDir.
func withScriptLog(t *testing.T, size int) *logRing {
	t.Helper()
	prev := ScriptLog
	ScriptLog = newTestRing(t, size)
	t.Cleanup(func() { ScriptLog = prev })
	return ScriptLog
}

func TestLogRingKeepsOrder(t *testing.T) {
	r := newTestRing(t, 10)
	for _, s := range []string{"one", "two", "three"} {
		r.Infof("a.flang", "%s", s)
	}
	got := r.Snapshot()
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
	for i, want := range []string{"one", "two", "three"} {
		if got[i].Text != want {
			t.Errorf("entry %d is %q, want %q", i, got[i].Text, want)
		}
	}
	// Sequence numbers are what the window uses to notice new arrivals, so they
	// have to be monotonic even after the ring wraps.
	for i := 1; i < len(got); i++ {
		if got[i].Seq <= got[i-1].Seq {
			t.Errorf("sequence went backwards: %d then %d", got[i-1].Seq, got[i].Seq)
		}
	}
}

func TestLogRingWrapsAndKeepsTheNewest(t *testing.T) {
	// A `log()` in a :tick: handler produces sixty lines a second. Wrapping is
	// the normal case, not the exceptional one, and what must survive is the
	// recent past rather than the distant one.
	r := newTestRing(t, 4)
	for _, s := range []string{"a", "b", "c", "d", "e", "f"} {
		r.Infof("s", "%s", s)
	}
	got := r.Snapshot()
	if len(got) != 4 {
		t.Fatalf("got %d entries, want the ring's capacity of 4", len(got))
	}
	want := []string{"c", "d", "e", "f"}
	for i := range want {
		if got[i].Text != want[i] {
			t.Errorf("entry %d is %q, want %q", i, got[i].Text, want[i])
		}
	}
	if r.Dropped() != 2 {
		t.Errorf("reported %d evicted, want 2", r.Dropped())
	}
}

func TestLogRingClearKeepsTheSequenceRising(t *testing.T) {
	// The window compares sequence numbers to decide whether to scroll. If Clear
	// reset them, the window would think the log had jumped backwards.
	r := newTestRing(t, 4)
	r.Infof("s", "before")
	before := r.Seq()
	r.Clear()
	if r.Len() != 0 {
		t.Errorf("Clear left %d entries", r.Len())
	}
	r.Infof("s", "after")
	if r.Seq() <= before {
		t.Errorf("sequence went backwards across Clear: %d then %d", before, r.Seq())
	}
}

func TestLogRingEchoesToItsWriter(t *testing.T) {
	// Losing the terminal would be a downgrade for anyone running from a shell.
	var buf bytes.Buffer
	r := newTestRing(t, 4)
	r.echo = &buf
	r.Warnf("door.flang", "something odd")

	out := buf.String()
	if !strings.Contains(out, "something odd") {
		t.Errorf("the message did not reach the echo: %q", out)
	}
	if !strings.Contains(out, "warn") {
		t.Errorf("the level is missing from the echoed line: %q", out)
	}
}

func TestLogRingIsSafeUnderConcurrentWrites(t *testing.T) {
	// The standard logger can be called from anywhere, and the window reads
	// while the runtime writes. Run with -race, this is the test that catches it.
	r := newTestRing(t, 64)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				r.Infof("s", "%d-%d", n, j)
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 100; j++ {
			_ = r.Snapshot()
			_ = r.Len()
		}
	}()
	wg.Wait()

	if r.Seq() != 400 {
		t.Errorf("got %d writes, want 400", r.Seq())
	}
}

func TestPositionedErrorsAreClickable(t *testing.T) {
	r := newTestRing(t, 8)
	r.ErrorAt("door.flang", flang.Pos{File: "door.flang", Line: 12, Col: 4}, "boom")
	r.Infof("door.flang", "ordinary")

	got := r.Snapshot()
	if !got[0].HasPos() {
		t.Error("a positioned error reported no position")
	}
	if got[1].HasPos() {
		t.Error("an ordinary message claimed a position")
	}
}

func TestDiagnosticsBecomeOneEntryEach(t *testing.T) {
	// One entry per problem rather than one blob: each keeps its own position,
	// which is what makes the window able to open the right line.
	r := newTestRing(t, 8)
	r.Diagnostics("a.flang", flang.ErrorList{
		{Pos: flang.Pos{File: "a.flang", Line: 1}, Msg: "first"},
		{Pos: flang.Pos{File: "a.flang", Line: 9}, Msg: "second"},
	})
	got := r.Snapshot()
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].Pos.Line != 1 || got[1].Pos.Line != 9 {
		t.Errorf("positions are %d and %d, want 1 and 9", got[0].Pos.Line, got[1].Pos.Line)
	}
}

// ---------- the standard-logger tee ----------

func TestLogDecorationIsStripped(t *testing.T) {
	// The window shows the source and the time in columns of its own, so the
	// logger's copies would be noise.
	cases := []struct{ in, want string }{
		{"12:34:56 [foundry] scanned 4 textures", "scanned 4 textures"},
		{"[foundry] no time prefix", "no time prefix"},
		{"neither", "neither"},
		// A line that only looks like a timestamp must not lose its first
		// characters: a cosmetic loss is fine, a real one is not.
		{"ab:cd:ef not a time", "ab:cd:ef not a time"},
	}
	for _, tc := range cases {
		if got := stripLogDecoration(tc.in); got != tc.want {
			t.Errorf("stripLogDecoration(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEngineFailuresAreRaisedToWarnings(t *testing.T) {
	// Every engine line looking equally unimportant is how a real problem hides.
	if !looksLikeFailure("could not load texture") {
		t.Error("a failure was not recognised")
	}
	if looksLikeFailure("scanned 12 textures") {
		t.Error("an ordinary line was treated as a failure")
	}
}

func TestTeeRecordsAsEngineOutput(t *testing.T) {
	r := withScriptLog(t, 8)
	if _, err := (logTee{}).Write([]byte("hello from the engine\n")); err != nil {
		t.Fatal(err)
	}
	got := r.Snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if got[0].Source != "" {
		t.Errorf("source is %q, want empty (engine output)", got[0].Source)
	}
	if got[0].Text != "hello from the engine" {
		t.Errorf("text is %q", got[0].Text)
	}
}

// ---------- the window's filtering ----------

// withLogFilters restores the window's filter state after a test.
func withLogFilters(t *testing.T) {
	t.Helper()
	prev := struct {
		info, warn, errs, engine bool
		filter                   string
	}{logWindowShowInfo, logWindowShowWarn, logWindowShowError, logWindowShowEngine, logWindowFilter}
	t.Cleanup(func() {
		logWindowShowInfo, logWindowShowWarn = prev.info, prev.warn
		logWindowShowError, logWindowShowEngine = prev.errs, prev.engine
		logWindowFilter = prev.filter
	})
}

func TestLogFiltersByLevel(t *testing.T) {
	withLogFilters(t)
	logWindowShowInfo, logWindowShowWarn, logWindowShowError, logWindowShowEngine = true, true, true, true
	logWindowFilter = ""

	info := LogEntry{Level: LogInfo, Source: "a", Text: "x"}
	warn := LogEntry{Level: LogWarn, Source: "a", Text: "x"}
	bad := LogEntry{Level: LogError, Source: "a", Text: "x"}

	for _, e := range []LogEntry{info, warn, bad} {
		if !logEntryVisible(e) {
			t.Errorf("%s was hidden with every filter on", e.Level)
		}
	}
	logWindowShowInfo = false
	if logEntryVisible(info) {
		t.Error("info is still visible with its filter off")
	}
	if !logEntryVisible(warn) {
		t.Error("turning info off also hid warnings")
	}
}

func TestLogFilterMatchesSourceAsWellAsText(t *testing.T) {
	// Filtering by a script name should show everything that script said, not
	// only the lines that happen to repeat its own name.
	withLogFilters(t)
	logWindowShowInfo, logWindowShowWarn, logWindowShowError, logWindowShowEngine = true, true, true, true
	logWindowFilter = "door"

	if !logEntryVisible(LogEntry{Source: "scripts/door.flang", Text: "opened"}) {
		t.Error("an entry from the matching script was hidden")
	}
	if !logEntryVisible(LogEntry{Source: "scripts/other.flang", Text: "the door opened"}) {
		t.Error("an entry whose text matches was hidden")
	}
	if logEntryVisible(LogEntry{Source: "scripts/other.flang", Text: "nothing here"}) {
		t.Error("a non-matching entry was shown")
	}
}

func TestEngineOutputCanBeHidden(t *testing.T) {
	withLogFilters(t)
	logWindowShowInfo, logWindowShowWarn, logWindowShowError = true, true, true
	logWindowFilter = ""

	engine := LogEntry{Level: LogInfo, Source: "", Text: "engine line"}
	script := LogEntry{Level: LogInfo, Source: "a.flang", Text: "script line"}

	logWindowShowEngine = true
	if !logEntryVisible(engine) {
		t.Error("engine output was hidden with its filter on")
	}
	logWindowShowEngine = false
	if logEntryVisible(engine) {
		t.Error("engine output is still visible with its filter off")
	}
	if !logEntryVisible(script) {
		t.Error("hiding engine output also hid a script's")
	}
}

func TestEmptyMessageDistinguishesTheTwoCauses(t *testing.T) {
	// "Nothing logged" and "everything filtered out" look identical on screen
	// and have opposite fixes.
	if got := logEmptyMessage(0); !strings.Contains(got, "Nothing logged") {
		t.Errorf("empty-buffer message is %q", got)
	}
	if got := logEmptyMessage(12); !strings.Contains(got, "hidden by the filters") {
		t.Errorf("all-filtered message is %q", got)
	}
}

func TestShortSourceTrimsThePrefix(t *testing.T) {
	if got := logShortSource("scripts/door.flang"); got != "door" {
		t.Errorf("got %q, want %q", got, "door")
	}
	if got := logShortSource("scripts/rules/very/deep/win_condition.flang"); got != "win_condition" {
		t.Errorf("got %q, want %q", got, "win_condition")
	}
}

// ---------- the runtime writes to it ----------

func TestScriptLogReceivesScriptOutput(t *testing.T) {
	// The end of the chain: a script's `log()` has to reach the buffer the
	// window reads, attributed to the script that said it.
	r := withScriptLog(t, 32)
	root := withTempScriptDir(t)
	path := writeScript(t, root, "talker.flang", `def on_spawn(:spawn:) -> null
    log("hello from the script")
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, Entity{
		ID: "crate", Script: path, Visible: true, Attributes: AttrSet{},
	})
	runTicks(t, lvl, 1)

	var found *LogEntry
	for _, e := range r.Snapshot() {
		if strings.Contains(e.Text, "hello from the script") {
			found = &e
			break
		}
	}
	if found == nil {
		t.Fatalf("the script's output never reached the log: %+v", r.Snapshot())
	}
	if found.Source != path {
		t.Errorf("attributed to %q, want %q", found.Source, path)
	}
	if found.Level != LogInfo {
		t.Errorf("logged at %s, want info", found.Level)
	}
}

func TestScriptErrorsReachTheLogWithAPosition(t *testing.T) {
	r := withScriptLog(t, 32)
	root := withTempScriptDir(t)
	path := writeScript(t, root, "boom.flang", `def on_spawn(:spawn:) -> null
    x = 1 / 0
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, Entity{
		ID: "crate", Script: path, Visible: true, Attributes: AttrSet{},
	})
	runTicks(t, lvl, 1)

	for _, e := range r.Snapshot() {
		if e.Level == LogError && e.HasPos() && strings.Contains(e.Text, "zero") {
			return
		}
	}
	t.Errorf("no positioned error reached the log: %+v", r.Snapshot())
}

func TestBrokenScriptDiagnosticsReachTheLog(t *testing.T) {
	r := withScriptLog(t, 32)
	root := withTempScriptDir(t)
	path := writeScript(t, root, "broken.flang", "def oops( -> null\n    return\nend")

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, Entity{
		ID: "crate", Script: path, Visible: true, Attributes: AttrSet{},
	})
	runTicks(t, lvl, 1)

	for _, e := range r.Snapshot() {
		if e.Level == LogError && e.Source == path {
			return
		}
	}
	t.Errorf("a broken script produced no log entry: %+v", r.Snapshot())
}
