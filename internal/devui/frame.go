package devui

// frame.go — where the tools join the engine's frame.
//
// Two entry points, called from internal/app and nowhere else. Tick runs the
// work a submitted line asked for, at a point in the frame where the world is
// whole. Render puts the tools on the screen, after the game's own UI and
// before the buffers are swapped.
//
// Both are no-ops unless FOUNDRY_DEBUG_ENABLED was set, and both are cheap to
// call when they are: the whole cost of the tools in a binary nobody has
// enabled them in is two predictable branches a frame.

import (
	"foundry/internal/ui"
	"foundry/internal/uidraw"
	"foundry/internal/world"
)

// Tick runs whatever the console has been asked to do since the last frame.
//
// lvl is the level as it stands now. It is passed rather than reached for
// because a command may replace it — `level next_one` queues a load — and the
// pointer the frame loop holds is the one that must be acted on.
func Tick(lvl *world.Level) {
	if !enabled {
		return
	}
	applyStickies(lvl)
	if !console.open {
		// The log tail is only followed while the console is up. Nothing is
		// lost by not following it: the ring is still filling, and opening the
		// console catches up to wherever it has got to.
		return
	}
	drainLog()
	runPending(lvl)
}

// runPending executes the queued lines, in the order they were submitted.
//
// The queue is taken before the first line runs rather than ranged over, so
// that a command which queues another (`exec`, running a file of them) is
// answered next frame rather than inside this loop — which is the same reason
// a script's messages are drained rather than delivered recursively.
func runPending(lvl *world.Level) {
	if len(console.pending) == 0 {
		return
	}
	lines := console.pending
	console.pending = nil
	for _, s := range lines {
		run(lvl, s)
	}
	// Whatever a command printed should be on screen, not a page up.
	console.scroll = 0
}

// Render draws the F3 overlay and the console into l and issues them.
//
// The list is a parameter for the reason uidraw.RenderScreen's is: it is the
// caller's, reused across frames, and two of them drawing into one would
// interleave their vertices.
//
// Scale is 1: the tools are laid out in device pixels rather than in the
// game's canvas units, so that they stay the same physical size whatever
// resolution the game is running at. See the note in devui.go.
func Render(l *uidraw.List, winW, winH int32) {
	if !enabled || winW <= 0 || winH <= 0 {
		return
	}
	if !console.open && !overlayVisible() {
		return
	}
	w, h := float32(winW), float32(winH)
	l.Reset(ui.Rect{X: 0, Y: 0, W: w, H: h})
	if overlayVisible() {
		buildOverlay(l)
	}
	if console.open {
		buildConsole(l, w, h)
	}
	uidraw.Render(l, winW, winH, 1)
}
