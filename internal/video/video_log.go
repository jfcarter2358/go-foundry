package video

// video_log.go — the one line this package says out loud.
//
// The same arrangement ui.Logf uses, and for the same reason: a decode problem
// that was survived — an unreadable soundtrack, a frame that would not decode —
// belongs in the log window rather than in an error return that would take a
// whole screen down with it. internal/script installs the writer; nil is
// correct for a headless test, which is why nothing here treats a survivable
// problem as fatal.

// Logf reports a problem that was survived rather than failed on.
var Logf func(format string, args ...any)

func logf(format string, args ...any) {
	if Logf != nil {
		Logf("video: "+format, args...)
	}
}
