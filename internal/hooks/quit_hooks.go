package hooks

// quit_hooks.go — a script asking the game to end.
//
// Here rather than in internal/platform for the same reason the level-load
// request is here rather than in internal/levelio: internal/script sits below
// both, and a main menu's Quit button is a script calling a verb.
//
// A request rather than a call, exactly like RequestLevelLoad. Tearing the
// window down from inside a script would free the runtime that is executing
// the call — the frame loop takes it between frames instead, where leaving the
// loop is the ordinary thing to do.

var pendingQuit bool

// RequestQuit asks the frame loop to end the game.
func RequestQuit() { pendingQuit = true }

// TakeQuitRequest reports whether a quit was asked for, and clears it.
func TakeQuitRequest() bool {
	q := pendingQuit
	pendingQuit = false
	return q
}
