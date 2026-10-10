package hooks

// pointer_hooks.go — the mouse as a pointer at the world, rather than a way of
// turning the view.
//
// While mouse-look has the cursor captured, the mouse is the player's head and
// its buttons are actions (fire, aim). With the cursor free — a screen that
// shows one, or a script asking for one with `@level.prop.cursor` — it is a
// pointer, and a press that no widget took is a press on whatever is under it
// in the world. The input handler records those presses here, with where the
// cursor was; script_events.go picks through each one and turns it into
// :pointer_down:, :pointer_click:, :pointer_drag_start: and the rest.
//
// Same shape as every buffer in this package, for the same reason: the code
// that reads SDL knows nothing about scripts, and the code that posts messages
// knows nothing about SDL. A headless run (internal/play) drives it without a
// window at all.

// PointerButton is which mouse button, numbered as SDL numbers them.
type PointerButton uint8

const (
	PointerLeft   PointerButton = 1
	PointerMiddle PointerButton = 2
	PointerRight  PointerButton = 3
)

// Atom is the button as a script matches it.
func (b PointerButton) Atom() string {
	switch b {
	case PointerLeft:
		return "left"
	case PointerMiddle:
		return "middle"
	case PointerRight:
		return "right"
	}
	return "other"
}

// PointerKind is what a recorded pointer event is.
type PointerKind uint8

const (
	PointerDown PointerKind = iota
	PointerUp
	PointerWheel
)

// PointerEvent is one press, release or wheel turn that reached the world, at
// a position in window pixels from the top-left.
type PointerEvent struct {
	Kind   PointerKind
	Button PointerButton
	X, Y   float32
	// Wheel is the wheel's travel for a PointerWheel: positive away from the
	// user.
	Wheel float32
}

// CursorState is where the pointer is and what it is over, refreshed by the
// frame loop once a frame.
type CursorState struct {
	X, Y float32
	// Free says the cursor is showing and moving freely. Captured, the pointer
	// is the centre of the view and X, Y say so.
	Free bool
	// OverUI says a widget is under the pointer: what is behind it in the world
	// is not what the player is pointing at.
	OverUI bool
}

var (
	pointerEvents []PointerEvent
	cursor        CursorState
	// pinned is a cursor something other than the mouse is holding — a
	// scripted run (internal/play) pointing at the world. While it is set the
	// frame loop's own SetCursor is ignored, so the real mouse sitting still
	// over the window does not undo every move the script makes.
	pinned *CursorState
)

// RecordPointer notes a pointer event that reached the world.
func RecordPointer(ev PointerEvent) { pointerEvents = append(pointerEvents, ev) }

// TakePointerEvents returns the events since the last call and clears them.
func TakePointerEvents() []PointerEvent {
	out := pointerEvents
	pointerEvents = nil
	return out
}

// SetCursor records the pointer's state for this frame. Ignored while the
// cursor is pinned.
func SetCursor(c CursorState) {
	if pinned == nil {
		cursor = c
	}
}

// Cursor is the pointer's state as the frame loop last recorded it, or the
// pinned one.
func Cursor() CursorState {
	if pinned != nil {
		return *pinned
	}
	return cursor
}

// PinCursor holds the pointer at c, whatever the mouse does, until
// UnpinCursor.
func PinCursor(c CursorState) { pinned = &c }

// UnpinCursor hands the pointer back to the mouse.
func UnpinCursor() { pinned = nil }

// ResetPointer forgets pending events and the cursor. A level load or the end
// of play: a press made on the last level must not land on this one. A pin is
// kept — a scripted run carries on across the level it loads.
func ResetPointer() {
	pointerEvents = nil
	cursor = CursorState{}
}

// ScriptWantsCursor is `@level.prop.cursor`: a script asking for the cursor
// free with no screen up to ask for it — a strategy view, a build mode. The
// frame loop joins it to the screen stack's own request (app/ui_input.go).
var ScriptWantsCursor bool
