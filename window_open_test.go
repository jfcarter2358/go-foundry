package main

// window_open_test.go — the deferred-open queue.
//
// The bug this guards produced a hard segfault, not a wrong pixel: opening a
// window from inside another window's Draw makes NewUIWindow restore the *main*
// imgui context, and the caller's very next widget call runs against a context
// with no frame in progress.
//
// The queue itself is testable headlessly even though the thing it defers is
// not, which is most of why the deferral is a queue rather than a flag.

import (
	"foundry/internal/uiwin"
	"testing"
)

func TestWindowOpensAreDeferredUntilDrained(t *testing.T) {
	prev := uiwin.PendingWindowOpens
	t.Cleanup(func() { uiwin.PendingWindowOpens = prev })
	uiwin.PendingWindowOpens = nil

	opened := 0
	uiwin.RequestWindowOpen(func() { opened++ })
	if opened != 0 {
		t.Fatal("the request ran immediately; it has to wait for the main loop")
	}
	uiwin.DrainWindowOpens()
	if opened != 1 {
		t.Errorf("ran %d times after draining, want 1", opened)
	}
	// Draining again must not repeat it: the queue is consumed, not replayed.
	uiwin.DrainWindowOpens()
	if opened != 1 {
		t.Errorf("ran %d times after a second drain, want 1", opened)
	}
}

func TestWindowOpensRunInOrder(t *testing.T) {
	prev := uiwin.PendingWindowOpens
	t.Cleanup(func() { uiwin.PendingWindowOpens = prev })
	uiwin.PendingWindowOpens = nil

	var order []string
	uiwin.RequestWindowOpen(func() { order = append(order, "first") })
	uiwin.RequestWindowOpen(func() { order = append(order, "second") })
	uiwin.DrainWindowOpens()

	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("ran %v, want [first second]", order)
	}
}

func TestRequestingFromInsideADrainIsSafe(t *testing.T) {
	// A window whose Open asks for another one. The queue is swapped out before
	// running anything, so the nested request lands in the next batch rather
	// than mutating the slice being iterated.
	prev := uiwin.PendingWindowOpens
	t.Cleanup(func() { uiwin.PendingWindowOpens = prev })
	uiwin.PendingWindowOpens = nil

	inner := 0
	uiwin.RequestWindowOpen(func() {
		uiwin.RequestWindowOpen(func() { inner++ })
	})
	uiwin.DrainWindowOpens()
	if inner != 0 {
		t.Error("a nested request ran in the same drain")
	}
	uiwin.DrainWindowOpens()
	if inner != 1 {
		t.Errorf("the nested request ran %d times, want 1", inner)
	}
}

func TestNilRequestIsIgnored(t *testing.T) {
	prev := uiwin.PendingWindowOpens
	t.Cleanup(func() { uiwin.PendingWindowOpens = prev })
	uiwin.PendingWindowOpens = nil

	uiwin.RequestWindowOpen(nil)
	if len(uiwin.PendingWindowOpens) != 0 {
		t.Error("a nil request was queued and would panic on drain")
	}
	uiwin.DrainWindowOpens()
}
