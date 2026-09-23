package devui

import (
	"testing"

	"foundry/internal/render"
	"foundry/internal/ui"
	"foundry/internal/uidraw"
)

// The drawing is tested headlessly, without a GL context: uidraw's list builder
// is pure geometry, uifont falls back to its compiled-in bitmap face when no
// .ttf loads, and gfx.Textures simply answers 0 for a page nothing uploaded.
// What that proves is not what it looks like — it is that the layout arithmetic
// terminates, stays inside the panel, and produces quads rather than nothing.

func TestTheConsoleDrawsSomething(t *testing.T) {
	reset(t)
	Toggle()
	printf(kindOut, "a line of output")

	var l uidraw.List
	l.Reset(ui.Rect{W: 1280, H: 720})
	buildConsole(&l, 1280, 720)

	if l.Empty() {
		t.Fatal("the console built no geometry")
	}
	r := consoleRect(1280, 720)
	for _, v := range l.Verts {
		if v.X < r.X-1 || v.X > r.X+r.W+1 || v.Y < r.Y-1 || v.Y > r.Y+r.H+1 {
			t.Fatalf("a vertex at %.1f,%.1f is outside the panel %v", v.X, v.Y, r)
		}
	}
	if viewRows < 1 {
		t.Fatalf("the panel made room for %d rows", viewRows)
	}
}

func TestTheConsoleFitsAShortWindow(t *testing.T) {
	reset(t)
	Toggle()
	var l uidraw.List
	for _, h := range []int32{240, 480, 1080, 2160} {
		l.Reset(ui.Rect{W: 640, H: float32(h)})
		buildConsole(&l, 640, float32(h))
		r := consoleRect(640, float32(h))
		if r.H > float32(h) {
			t.Fatalf("at %dpx tall the console wanted %.0f", h, r.H)
		}
		if viewRows < 1 {
			t.Fatalf("at %dpx tall there was no room for a row", h)
		}
	}
}

func TestTheOverlayDrawsItsRows(t *testing.T) {
	reset(t)
	was := render.DebugOverlayVisible
	render.DebugOverlayVisible = true
	t.Cleanup(func() { render.DebugOverlayVisible = was })

	var l uidraw.List
	l.Reset(ui.Rect{W: 1280, H: 720})
	buildOverlay(&l)
	if l.Empty() {
		t.Fatal("the overlay built no geometry")
	}
}

func TestLongLinesWrapAndRewrapOnAResize(t *testing.T) {
	reset(t)
	long := ""
	for i := 0; i < 200; i++ {
		long += "word "
	}
	printf(kindOut, "%s", long)

	rewrap(300)
	narrow := len(console.wrapped)
	rewrap(900)
	wide := len(console.wrapped)
	if narrow <= wide {
		t.Fatalf("a narrow panel wrapped into %d rows and a wide one into %d", narrow, wide)
	}
	if wide < 1 {
		t.Fatal("wrapping produced no rows at all")
	}
}

func TestScrollingStopsAtBothEnds(t *testing.T) {
	reset(t)
	for i := 0; i < 50; i++ {
		printf(kindOut, "line %d", i)
	}
	rewrap(600)
	viewRows = 10

	scrollBy(-5)
	if console.scroll != 0 {
		t.Fatalf("scrolled past the tail to %d", console.scroll)
	}
	scrollBy(10000)
	if max := len(console.wrapped) - viewRows; console.scroll != max {
		t.Fatalf("scroll = %d, want the top at %d", console.scroll, max)
	}
}

func TestAScrolledViewStaysOnWhatItIsReading(t *testing.T) {
	reset(t)
	for i := 0; i < 40; i++ {
		printf(kindOut, "old %d", i)
	}
	rewrap(600)
	viewRows = 10
	scrollBy(15)

	before := visibleTop()
	// A game that logs every frame, arriving while someone reads back.
	for i := 0; i < 5; i++ {
		printf(kindOut, "new %d", i)
	}
	rewrap(600)
	if got := visibleTop(); got != before {
		t.Fatalf("the top row moved from %q to %q while scrolled back", before, got)
	}
}

func TestFollowingTheTailStillFollowsIt(t *testing.T) {
	reset(t)
	for i := 0; i < 40; i++ {
		printf(kindOut, "old %d", i)
	}
	rewrap(600)
	viewRows = 10
	printf(kindOut, "the newest line")
	rewrap(600)
	if console.scroll != 0 {
		t.Fatalf("scroll drifted to %d while following the tail", console.scroll)
	}
	if got := console.wrapped[len(console.wrapped)-1].text; got != "the newest line" {
		t.Fatalf("the bottom row is %q", got)
	}
}

// visibleTop is the first row the panel would draw.
func visibleTop() string {
	end := len(console.wrapped) - console.scroll
	start := end - visibleRows()
	if start < 0 {
		start = 0
	}
	if start >= len(console.wrapped) {
		return ""
	}
	return console.wrapped[start].text
}
