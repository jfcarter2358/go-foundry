package main

// script_log.go — where script output goes so a person can read it.
//
// `log()` in a script, a runtime error, a message dropped for a vanished target,
// a budget that tripped: all of it used to go to stderr, which is exactly where
// nobody can see it while the game has the screen. This is the ring buffer the
// log window reads instead.
//
// Three things it has to get right:
//
//   - **Bounded.** A `log()` in a `:tick:` handler produces sixty lines a
//     second. The buffer is a fixed-size ring, so a runaway logger costs memory
//     that is already allocated rather than memory that keeps growing.
//   - **Still on stderr.** Losing the terminal would be a downgrade for anyone
//     running the engine from a shell, so every entry is written there too.
//   - **Positioned where it can be.** A runtime error knows its file and line,
//     and carrying that through means the log window can open the offending
//     script at the offending line rather than making someone go and find it.
//
// The standard logger is teed in as well, tagged as engine output. A script that
// sets a texture which fails to load produces the interesting message in the
// engine's log rather than the script's, and having to watch two places for one
// problem is how a debugging session goes wrong.

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/veandco/go-sdl2/sdl"

	"foundry/internal/flang"
)

// scriptLogCapacity is how many entries the ring holds. Large enough that a
// second or two of per-frame logging does not evict what came before it, small
// enough to be a fixed cost.
const scriptLogCapacity = 4000

// LogLevel is how loud an entry is.
type LogLevel uint8

const (
	// LogInfo is a script's own `log()` output, and ordinary lifecycle notes.
	LogInfo LogLevel = iota
	// LogWarn is something that did not work but did not stop anything: a
	// message dropped for a vanished target, a budget that tripped.
	LogWarn
	// LogError is a script that failed to compile or threw at runtime.
	LogError
)

// String renders a level for the window's filter labels.
func (l LogLevel) String() string {
	switch l {
	case LogWarn:
		return "warn"
	case LogError:
		return "error"
	default:
		return "info"
	}
}

// LogEntry is one line.
type LogEntry struct {
	// Seq is the entry's position in the total stream, so the window can tell
	// whether anything new has arrived without comparing contents.
	Seq   uint64
	Time  uint32 // SDL ticks when it was written
	Level LogLevel
	// Source is the script that produced it, or "" for engine output.
	Source string
	Text   string
	// Pos is where in the source the entry refers to, when it refers anywhere.
	// A zero Line means "no position", which is the common case.
	Pos flang.Pos
}

// HasPos reports whether the entry points at a place in a script.
func (e LogEntry) HasPos() bool { return e.Pos.Line > 0 && e.Pos.File != "" }

// logRing is a bounded, ordered buffer of entries.
//
// Guarded by a mutex because the standard logger can be called from anywhere,
// and a texture or model load on another goroutine writing while the window
// reads would otherwise be a race nobody would find on purpose.
type logRing struct {
	mu      sync.Mutex
	entries []LogEntry
	next    int    // where the following write goes
	filled  bool   // whether the ring has wrapped at least once
	seq     uint64 // total entries ever written
	// dropped counts entries evicted by the ring wrapping, so the window can say
	// so rather than silently showing a partial history.
	dropped uint64
	// echo is where entries are mirrored. Nil sends them nowhere, which is what
	// tests want.
	echo io.Writer
}

// ScriptLog is the engine-wide log buffer.
var ScriptLog = &logRing{
	entries: make([]LogEntry, scriptLogCapacity),
	echo:    os.Stderr,
}

// append records one entry.
//
// echo says whether to mirror it to the terminal. Entries that arrived *through*
// the standard logger are already on their way there — mirroring those would
// print every engine line twice, which is exactly what it did before this
// argument existed.
func (r *logRing) append(e LogEntry, echoIt bool) {
	r.mu.Lock()
	r.seq++
	e.Seq = r.seq
	e.Time = sdl.GetTicks()
	if r.filled {
		r.dropped++
	}
	r.entries[r.next] = e
	r.next = (r.next + 1) % len(r.entries)
	if r.next == 0 {
		r.filled = true
	}
	echo := r.echo
	r.mu.Unlock()

	if echoIt && echo != nil {
		fmt.Fprintln(echo, formatLogEntry(e))
	}
}

// formatLogEntry renders an entry for stderr, in the shape the engine's other
// output already has.
func formatLogEntry(e LogEntry) string {
	var b strings.Builder
	switch e.Source {
	case "":
		b.WriteString("[engine] ")
	default:
		b.WriteString("[script] ")
	}
	if e.Level != LogInfo {
		b.WriteString(e.Level.String())
		b.WriteString(": ")
	}
	if e.HasPos() {
		b.WriteString(e.Pos.String())
		b.WriteString(": ")
	} else if e.Source != "" {
		b.WriteString(e.Source)
		b.WriteString(": ")
	}
	b.WriteString(e.Text)
	return b.String()
}

// Infof, Warnf and Errorf record an entry from a script.
func (r *logRing) Infof(source, format string, args ...any) {
	r.append(LogEntry{Level: LogInfo, Source: source, Text: fmt.Sprintf(format, args...)}, true)
}

func (r *logRing) Warnf(source, format string, args ...any) {
	r.append(LogEntry{Level: LogWarn, Source: source, Text: fmt.Sprintf(format, args...)}, true)
}

func (r *logRing) Errorf(source, format string, args ...any) {
	r.append(LogEntry{Level: LogError, Source: source, Text: fmt.Sprintf(format, args...)}, true)
}

// ErrorAt records an error that knows where it happened, so the window can open
// the script there.
func (r *logRing) ErrorAt(source string, pos flang.Pos, format string, args ...any) {
	r.append(LogEntry{
		Level: LogError, Source: source, Pos: pos,
		Text: fmt.Sprintf(format, args...),
	}, true)
}

// WarnAt records a positioned warning: something a script asked for that the
// level could not do, at the line that asked. Positioned rather than plain
// because these are nearly always a wiring mistake — a name that matches
// nothing — and the line is most of the fix.
func (r *logRing) WarnAt(source string, pos flang.Pos, format string, args ...any) {
	r.append(LogEntry{
		Level: LogWarn, Source: source, Pos: pos,
		Text: fmt.Sprintf(format, args...),
	}, true)
}

// Diagnostics records a whole diagnostic list, one entry per problem, so each
// keeps its own position instead of being flattened into one unpositioned blob.
func (r *logRing) Diagnostics(source string, errs flang.ErrorList) {
	for _, e := range errs {
		r.ErrorAt(source, e.Pos, "%s", e.Msg)
	}
}

// Snapshot returns the entries in order, oldest first.
//
// Copied rather than handed out by reference: the window holds the result across
// a frame while the runtime keeps writing, and a slice into the live ring would
// change underneath it.
func (r *logRing) Snapshot() []LogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := r.next
	if r.filled {
		n = len(r.entries)
	}
	out := make([]LogEntry, 0, n)
	if r.filled {
		out = append(out, r.entries[r.next:]...)
	}
	out = append(out, r.entries[:r.next]...)
	return out
}

// Len returns how many entries the ring is holding.
func (r *logRing) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.filled {
		return len(r.entries)
	}
	return r.next
}

// Seq returns the total number of entries ever written. The window compares it
// against what it last drew to know whether to scroll.
func (r *logRing) Seq() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seq
}

// Dropped returns how many entries the ring has evicted.
func (r *logRing) Dropped() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dropped
}

// Clear empties the ring. The sequence counter keeps rising, so a window that
// was following the tail does not jump backwards.
func (r *logRing) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next = 0
	r.filled = false
	r.dropped = 0
}

// Sources returns every script that has logged, sorted, for the filter menu.
func (r *logRing) Sources() []string {
	seen := map[string]bool{}
	for _, e := range r.Snapshot() {
		if e.Source != "" {
			seen[e.Source] = true
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sortStringsAsc(out)
	return out
}

// sortStringsAsc is an insertion sort over the handful of script names a level
// has. Kept local so this file does not pull in sort for one call.
func sortStringsAsc(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ---------- teeing the standard logger ----------

// logTee turns writes to the standard logger into engine-sourced entries.
//
// It is added *beside* the logger's real sinks rather than replacing them: the
// terminal and foundry.log both keep working exactly as they did, and the window
// is a third reader rather than a redirect.
type logTee struct{}

func (logTee) Write(p []byte) (int, error) {
	text := stripLogDecoration(strings.TrimRight(string(p), "\n"))
	if text != "" {
		// Engine output has no level of its own. Anything that reads like a
		// failure is raised so it does not hide among the ordinary notes — a
		// heuristic, but the alternative is every engine line looking equally
		// unimportant.
		level := LogInfo
		if looksLikeFailure(text) {
			level = LogWarn
		}
		// Not echoed: this line came from the standard logger and is already
		// on its way to the terminal and the log file.
		ScriptLog.append(LogEntry{Level: level, Text: text}, false)
	}
	return len(p), nil
}

// looksLikeFailure guesses whether an engine log line is reporting a problem.
func looksLikeFailure(s string) bool {
	l := strings.ToLower(s)
	for _, w := range []string{"error", "fail", "cannot", "could not", "unable", "invalid", "missing"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

// logDecorPrefix is the standard logger's message prefix as it was when the tee
// was installed.
//
// Captured rather than read from log.Prefix() at strip time: the tee runs on
// every log line, and reaching back into global logger state on each one couples
// the two in a way that is invisible from here and awkward to test.
var logDecorPrefix = "[foundry] "

// stripLogDecoration removes the standard logger's own timestamp and prefix, so
// the window shows the message rather than the message plus two things it is
// already displaying in columns of its own.
//
// Shaped for the flags main sets (Ltime | Lmsgprefix), which produce
// "HH:MM:SS <prefix>message". Anything that does not match is left alone: a line
// keeping its decoration is a cosmetic loss, and a line losing its first eight
// characters to an over-eager strip is a real one.
func stripLogDecoration(s string) string {
	if len(s) >= 9 && s[2] == ':' && s[5] == ':' && s[8] == ' ' &&
		isDigits(s[0:2]) && isDigits(s[3:5]) && isDigits(s[6:8]) {
		s = s[9:]
	}
	if logDecorPrefix != "" {
		s = strings.TrimPrefix(s, logDecorPrefix)
	}
	return s
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// InstallLogCapture makes the log window a reader of the standard logger, and
// points the ring's own echo at the same sinks.
//
// base is where the logger was already writing — stderr, and foundry.log when it
// could be created. Passing it in rather than assuming means a script's `log()`
// reaches the log file too, which is what someone reading a crash report after
// the fact actually needs.
func InstallLogCapture(base io.Writer) {
	if base == nil {
		base = os.Stderr
	}
	ScriptLog.mu.Lock()
	ScriptLog.echo = base
	ScriptLog.mu.Unlock()
	logDecorPrefix = log.Prefix()

	// The tee is added to the existing sinks, not substituted for them.
	log.SetOutput(io.MultiWriter(base, logTee{}))
}
