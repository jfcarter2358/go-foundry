package app

import (
	"bytes"
	"errors"
	"log"
	"testing"
)

type failingWriter struct{ calls int }

func (f *failingWriter) Write(p []byte) (int, error) {
	f.calls++
	return 0, errors.New("the handle is invalid")
}

// A windowsgui release has no console, so stderr fails every write. It is the
// first sink, and the log file after it still has to get every line — with
// io.MultiWriter it got none, and the release foundry.log was empty.
func TestTheLogFileGetsEveryLineWhenStderrFails(t *testing.T) {
	stderr := &failingWriter{}
	var file bytes.Buffer
	l := log.New(newLogFanout(stderr, &file), "", 0)
	l.Print("one")
	l.Print("two")
	if got := file.String(); got != "one\ntwo\n" {
		t.Errorf("log file holds %q, want both lines", got)
	}
	if stderr.calls != 1 {
		t.Errorf("the failing sink was tried %d times, want once and then dropped", stderr.calls)
	}
}
