package app

import (
	"io"
	"sync"
)

// logFanout writes every log line to each of its sinks, and one sink failing
// never keeps the line from the others.
//
// io.MultiWriter is the obvious tool and the wrong one: it stops at the first
// writer that errors. A release build on Windows is linked -H windowsgui, so it
// has no console and every write to os.Stderr fails. With stderr listed first,
// no line ever reached foundry.log, and the release log was empty. A script
// error that stopped level1's ground streaming happened, was reported, and was
// never written down.
//
// A sink that fails is dropped, so a missing console costs one failed write
// rather than one per line. The result never reports an error. Nothing that
// logs could act on one, and the standard logger only passes it back to
// callers that ignore it.
type logFanout struct {
	mu    sync.Mutex
	sinks []io.Writer
}

func newLogFanout(sinks ...io.Writer) *logFanout {
	return &logFanout{sinks: sinks}
}

func (f *logFanout) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.sinks[:0]
	for _, w := range f.sinks {
		if n, err := w.Write(p); err == nil && n == len(p) {
			kept = append(kept, w)
		}
	}
	f.sinks = kept
	return len(p), nil
}
