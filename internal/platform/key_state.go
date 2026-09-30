package platform

// key_state.go — which keys and mouse buttons are down, as a script sees them.
//
// Gameplay reads its held actions straight from the KeyStates snapshot and its
// presses from the event handler. A script wants both at once, for any key:
// is Space down, did F go down since the last tick, how long has E been held.
// That needs state kept between ticks, which the snapshot does not have, and
// presses kept in order, which a snapshot cannot give — a tap made and let go
// inside one frame is down in no snapshot at all.
//
// So the input handler reports every press and release that reaches gameplay
// (PressKey, ReleaseKey), and the script tick takes them once per tick
// (AdvanceKeys), which is what "pressed" and "released" are measured against.
// A key the UI took the press of is not down as far as a script is concerned,
// the same way it does not walk the player.
//
// Releases are the exception: the UI may take one too, and a key whose release
// never arrived would read as held for ever. SettleKeys lets the frame's
// snapshot overrule the events for that — whatever is up in it is up.
//
// Headless runs (internal/play) have no snapshot and never settle; they press
// and release through the same two calls.

import (
	"sort"

	"github.com/veandco/go-sdl2/sdl"
)

// KeyEdge is one press or release, as a script tick hands it on.
type KeyEdge struct {
	In   Input
	Down bool
	// Held is, for a release, how long the input had been down in seconds as
	// the ticks count it — 0 for a tap that came and went within one.
	Held float64
}

type keyTrack struct {
	// live is the event-level state: what has been pressed and not released,
	// as of the last event. It is what stops key repeat or a doubled report
	// from being a second press.
	live map[Input]bool
	// pending are the edges since the last AdvanceKeys, oldest first.
	pending []KeyEdge
	// held is the tick-level state: each input that is down, with the clock
	// reading at the tick it went down.
	held map[Input]float64
	// pressed and released are the inputs that went down or up in the edges
	// the last AdvanceKeys took. A tap is in both, and not in held.
	pressed, released map[Input]bool
	clock             float64
}

var tracked = newKeyTrack()

func newKeyTrack() keyTrack {
	return keyTrack{
		live:     map[Input]bool{},
		held:     map[Input]float64{},
		pressed:  map[Input]bool{},
		released: map[Input]bool{},
	}
}

// PressKey records an input going down. Pressing one already down does
// nothing.
func PressKey(in Input) {
	if !in.Bound() || tracked.live[in] {
		return
	}
	tracked.live[in] = true
	tracked.pending = append(tracked.pending, KeyEdge{In: in, Down: true})
}

// ReleaseKey records an input coming up. Releasing one that is not down does
// nothing.
func ReleaseKey(in Input) {
	if !tracked.live[in] {
		return
	}
	delete(tracked.live, in)
	tracked.pending = append(tracked.pending, KeyEdge{In: in})
}

// SettleKeys releases every input the frame's snapshot says is up.
func SettleKeys(keyStates []uint8, buttons sdl.ButtonMask) {
	for in := range tracked.live {
		if !inputDown(in, keyStates, buttons) {
			ReleaseKey(in)
		}
	}
}

func inputDown(in Input, keyStates []uint8, buttons sdl.ButtonMask) bool {
	switch {
	case in.Mouse != 0:
		return buttons&(1<<(in.Mouse-1)) != 0
	case in.Key != 0:
		return int(in.Key) < len(keyStates) && keyStates[in.Key] != 0
	}
	return false
}

// AdvanceKeys is one script tick: the edges since the last become this tick's
// presses and releases, and are returned in the order they happened. A held
// input's time starts at 0 on the tick it went down and grows by dt a tick.
func AdvanceKeys(dt float64) []KeyEdge {
	tracked.clock += dt
	clear(tracked.pressed)
	clear(tracked.released)
	edges := tracked.pending
	tracked.pending = nil
	for i, e := range edges {
		if e.Down {
			tracked.held[e.In] = tracked.clock
			tracked.pressed[e.In] = true
			continue
		}
		if since, ok := tracked.held[e.In]; ok {
			edges[i].Held = tracked.clock - since
		}
		delete(tracked.held, e.In)
		tracked.released[e.In] = true
	}
	return edges
}

// ResetKeys forgets everything: nothing is down, nothing is pending.
func ResetKeys() { tracked = newKeyTrack() }

// KeyDown reports whether an input is down as of the last tick.
func KeyDown(in Input) bool {
	_, ok := tracked.held[in]
	return ok
}

// KeyPressed and KeyReleased report whether an input went down, or came up,
// between the last two ticks.
func KeyPressed(in Input) bool  { return tracked.pressed[in] }
func KeyReleased(in Input) bool { return tracked.released[in] }

// KeyHeldTime is how long an input has been down, in seconds; 0 when it is up.
func KeyHeldTime(in Input) float64 {
	since, ok := tracked.held[in]
	if !ok {
		return 0
	}
	return tracked.clock - since
}

// KeysDown lists every input that is down, by name.
func KeysDown() []Input {
	out := make([]Input, 0, len(tracked.held))
	for in := range tracked.held {
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// ActionDown, ActionPressed, ActionReleased and ActionHeldTime are the same
// questions about an action, on the player's layout: down when any of its
// inputs is, pressed when any went down, released when one came up and none is
// left down, and held for as long as the longest-held one.
func ActionDown(a Action) bool {
	return anyInput(a, KeyDown)
}

func ActionPressed(a Action) bool {
	return anyInput(a, KeyPressed)
}

func ActionReleased(a Action) bool {
	return anyInput(a, KeyReleased) && !ActionDown(a)
}

func ActionHeldTime(a Action) float64 {
	best := 0.0
	for _, in := range Bindings[a] {
		if in.Bound() {
			best = max(best, KeyHeldTime(in))
		}
	}
	return best
}

func anyInput(a Action, f func(Input) bool) bool {
	for _, in := range Bindings[a] {
		if in.Bound() && f(in) {
			return true
		}
	}
	return false
}
