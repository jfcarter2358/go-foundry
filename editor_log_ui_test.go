package main

import (
	"foundry/internal/script"
	"strings"
	"testing"
)

// editor_log_ui_test.go — the log window's filtering.
//
// The ring buffer and the entries in it are internal/script's; which of them
// the window shows is this file's, and the toggles live in editor_log_ui.go.

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

	info := script.LogEntry{Level: script.LogInfo, Source: "a", Text: "x"}
	warn := script.LogEntry{Level: script.LogWarn, Source: "a", Text: "x"}
	bad := script.LogEntry{Level: script.LogError, Source: "a", Text: "x"}

	for _, e := range []script.LogEntry{info, warn, bad} {
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

	if !logEntryVisible(script.LogEntry{Source: "scripts/door.flang", Text: "opened"}) {
		t.Error("an entry from the matching script was hidden")
	}
	if !logEntryVisible(script.LogEntry{Source: "scripts/other.flang", Text: "the door opened"}) {
		t.Error("an entry whose text matches was hidden")
	}
	if logEntryVisible(script.LogEntry{Source: "scripts/other.flang", Text: "nothing here"}) {
		t.Error("a non-matching entry was shown")
	}
}

func TestEngineOutputCanBeHidden(t *testing.T) {
	withLogFilters(t)
	logWindowShowInfo, logWindowShowWarn, logWindowShowError = true, true, true
	logWindowFilter = ""

	engine := script.LogEntry{Level: script.LogInfo, Source: "", Text: "engine line"}
	script := script.LogEntry{Level: script.LogInfo, Source: "a.flang", Text: "script line"}

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
