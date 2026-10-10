package hooks

// ui_hooks.go — the place the UI records something the scripting engine will
// want to hear about.
//
// The fourth buffer of the same shape as the three in
// script_engine_hooks.go, and for the same reason: internal/ui works out that a
// button was pressed without knowing that scripts exist, and script_events.go
// turns that into a posted message without knowing what a button is. Input code
// stays about input.
//
// Drained rather than read: taking the buffer empties it, so a click fires
// exactly once even if nothing is listening that frame.

// UIEvent is one thing that happened to a widget.
//
// Screen and Widget are names rather than pointers, matching how everything
// else addresses a widget: a screen can be reloaded or an undo can rebuild it
// between the event being recorded and the event being delivered.
type UIEvent struct {
	// Atom is the event's name without its colons: "click", "hover",
	// "unhover", "change", "submit", "finished", "cancel", "shown", "hidden",
	// "bound", "drag_start", "drag", "drop".
	Atom   string
	Screen string
	// Widget is empty for a screen-scoped event such as cancel or shown.
	Widget string

	// Num, Str and Bool carry a change's new value, following the widget's
	// kind: a slider reports Num, a toggle Bool, a text field Str.
	Num  float64
	Str  string
	Bool bool
	// Toggle and Text say which of those a change carries, because the value
	// alone cannot: a toggle switched off is Bool false and Num 0, a text
	// field emptied is Str "" — both indistinguishable from a slider at 0.
	Toggle, Text bool

	// X, Y are where the pointer is for a drag event, in the screen's canvas
	// units, and DX, DY how far it has moved since the last :drag:.
	X, Y, DX, DY float64
	// OtherScreen and Other name the widget a :drop: landed on; both empty
	// when it landed on nothing that takes the pointer.
	OtherScreen, Other string
}

var uiEvents []UIEvent

// RecordUIEvent notes that something happened to a widget.
func RecordUIEvent(ev UIEvent) {
	if ev.Atom == "" {
		return
	}
	uiEvents = append(uiEvents, ev)
}

// TakeUIEvents returns this frame's UI events and clears the buffer.
func TakeUIEvents() []UIEvent {
	out := uiEvents
	uiEvents = nil
	return out
}
