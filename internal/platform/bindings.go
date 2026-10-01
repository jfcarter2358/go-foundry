package platform

// bindings.go — which key or mouse button each gameplay action answers to.
//
// Every key the player presses to play the game goes through this table, so
// the game's settings screen can rebind any of them. The engine's own switches
// — the F-key debug toggles, the editor's `~`, the 0/8/9 collision and gravity
// debug keys and Escape for the pause menu — are not actions and stay where
// they are handled; ReservedKey keeps a binding from landing on one of them,
// where a single press would do two things.
//
// Each action has two keyboard-and-mouse slots, so crouch can keep both C and
// Left Ctrl, and a third that only a game controller's buttons and sticks go
// in (PadSlot) — its own column, so a pad layout never costs a key one. A pad
// binding is either a press of its input or a hold of it (Input.Hold), and one
// button can carry both — reload on a tap of X, interact on holding it; see
// pad_hold.go for how the two are told apart. The
// editor's fly camera reads DefaultBindings rather than Bindings: a player's
// layout is theirs, and the editor's keybinding reference describes WASD.
//
// Saved to controls.yaml by the app beside the mouse settings, and written
// by a script (bind_key), which is why a change only raises
// ControlSettingsChanged — see app/controls_settings.go.

import (
	"fmt"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// Action is one thing the player can do with a key.
type Action int

const (
	ActMoveForward Action = iota
	ActMoveBack
	ActMoveLeft
	ActMoveRight
	ActJump
	ActLookUp
	ActLookDown
	ActLookLeft
	ActLookRight
	ActSprint
	ActCrouch
	ActInteract
	ActPrimaryFire
	ActSecondaryFire
	ActReload
	ActThrow
	ActNextWeapon
	ActPrevWeapon
	ActWeapon1
	ActWeapon2
	ActWeapon3
	ActWeapon4
	ActWeapon5
	ActRollLeft
	ActRollRight
	ActPitchUp
	ActPitchDown

	ActionCount
)

// BindingSlots is how many inputs one action can answer to: KeySlots of
// keyboard and mouse, then the pad's.
const (
	KeySlots     = 2
	PadSlot      = KeySlots
	BindingSlots = KeySlots + 1
)

// ActionContext says when an action is live, which is what decides whether two
// actions may share an input: on foot and in a vehicle's seat never both.
type ActionContext int

const (
	CtxAlways  ActionContext = iota // walking and driving: the same keys steer both
	CtxOnFoot                       // only out of a driver's seat
	CtxVehicle                      // only in one
)

// ActionInfo is an action's script name, its label and when it is live.
type ActionInfo struct {
	Name    string
	Label   string
	Context ActionContext
}

// Actions is indexed by Action. Names are what scripts and controls.yaml use,
// so renaming one forgets every player's binding for it.
var Actions = [ActionCount]ActionInfo{
	ActMoveForward: {"move_forward", "Move forward", CtxAlways},
	ActMoveBack:    {"move_back", "Move back", CtxAlways},
	ActMoveLeft:    {"move_left", "Move left", CtxAlways},
	ActMoveRight:   {"move_right", "Move right", CtxAlways},
	ActJump:        {"jump", "Jump / brake", CtxAlways},
	ActLookUp:      {"look_up", "Look up", CtxAlways},
	ActLookDown:    {"look_down", "Look down", CtxAlways},
	ActLookLeft:    {"look_left", "Look left", CtxAlways},
	ActLookRight:   {"look_right", "Look right", CtxAlways},
	ActSprint:      {"sprint", "Sprint", CtxOnFoot},
	ActCrouch:      {"crouch", "Crouch", CtxOnFoot},
	// Interact is also how a driver stands up, so it is live in a seat too.
	ActInteract:      {"interact", "Interact", CtxAlways},
	ActPrimaryFire:   {"primary_fire", "Fire", CtxOnFoot},
	ActSecondaryFire: {"secondary_fire", "Aim", CtxOnFoot},
	ActReload:        {"reload", "Reload", CtxOnFoot},
	ActThrow:         {"throw", "Throw grenade", CtxOnFoot},
	ActNextWeapon:    {"next_weapon", "Next weapon", CtxOnFoot},
	ActPrevWeapon:    {"prev_weapon", "Previous weapon", CtxOnFoot},
	ActWeapon1:       {"weapon_1", "Weapon 1", CtxOnFoot},
	ActWeapon2:       {"weapon_2", "Weapon 2", CtxOnFoot},
	ActWeapon3:       {"weapon_3", "Weapon 3", CtxOnFoot},
	ActWeapon4:       {"weapon_4", "Weapon 4", CtxOnFoot},
	ActWeapon5:       {"weapon_5", "Weapon 5", CtxOnFoot},
	ActRollLeft:      {"roll_left", "Roll left", CtxVehicle},
	ActRollRight:     {"roll_right", "Roll right", CtxVehicle},
	ActPitchUp:       {"pitch_up", "Pitch nose up", CtxVehicle},
	ActPitchDown:     {"pitch_down", "Pitch nose down", CtxVehicle},
}

// ActionByName finds an action from its script name.
func ActionByName(name string) (Action, bool) {
	for i, a := range Actions {
		if a.Name == name {
			return Action(i), true
		}
	}
	return 0, false
}

// Input is one key, one mouse button or one game controller input (a button,
// a trigger or a stick direction — see gamepad.go). The zero value is
// "unbound".
//
// Hold makes a pad input answer only once it has been down PadHoldTime, so
// "Hold Pad X" and "Pad X" are two inputs and two actions can have one each.
// It is never set on a key or a mouse button.
type Input struct {
	Key   sdl.Scancode
	Mouse sdl.Button // sdl.ButtonLeft and friends; 0 for a key
	Pad   PadInput
	Hold  bool
}

// KeyInput, MouseInput and GamepadInput build an Input.
func KeyInput(sc sdl.Scancode) Input { return Input{Key: sc} }
func MouseInput(b sdl.Button) Input  { return Input{Mouse: b} }
func GamepadInput(p PadInput) Input  { return Input{Pad: p} }

// HoldInput is a pad input held for PadHoldTime, rather than pressed.
func HoldInput(p PadInput) Input { return Input{Pad: p, Hold: true} }

// Plain is the input without Hold: the physical key, button or stick
// direction, which is what goes down and comes up.
func (in Input) Plain() Input {
	in.Hold = false
	return in
}

// holdPrefix starts a hold input's name and label.
const holdPrefix = "Hold "

// Bound reports whether the input names anything.
func (in Input) Bound() bool { return in.Key != 0 || in.Mouse != 0 || in.Pad != 0 }

// IsPad reports whether the input is a game controller's, which is what
// decides which column of the binding table it may go in.
func (in Input) IsPad() bool { return in.Pad != 0 }

// Label is what a player is shown for the input: Name for a key or a mouse
// button, and for a pad input the words printed on the pad in their hands
// (PadLabel) — "Cross" rather than "Pad A" on a PlayStation pad.
func (in Input) Label() string {
	if in.Pad != 0 {
		if in.Hold {
			return holdPrefix + PadLabel(in.Pad, ActivePadFamily())
		}
		return PadLabel(in.Pad, ActivePadFamily())
	}
	return in.Name()
}

var mouseNames = map[sdl.Button]string{
	sdl.ButtonLeft:   "Mouse Left",
	sdl.ButtonRight:  "Mouse Right",
	sdl.ButtonMiddle: "Mouse Middle",
	sdl.ButtonX1:     "Mouse 4",
	sdl.ButtonX2:     "Mouse 5",
}

// Name is what controls.yaml stores and key_down takes: SDL's own scancode
// name, which is stable across layouts, "Mouse Left" and so on, or a pad
// input's positional name ("Pad A", "Pad Right Stick Up"), with "Hold " in
// front of a hold ("Hold Pad X"). Unbound is the empty string.
func (in Input) Name() string {
	switch {
	case in.Pad != 0 && in.Hold:
		return holdPrefix + PadName(in.Pad)
	case in.Pad != 0:
		return PadName(in.Pad)
	case in.Mouse != 0:
		if n, ok := mouseNames[in.Mouse]; ok {
			return n
		}
		return fmt.Sprintf("Mouse %d", in.Mouse)
	case in.Key != 0:
		return sdl.GetScancodeName(in.Key)
	}
	return ""
}

// ParseInput is Name's inverse. The empty string is unbound, and ok; a name
// that matches nothing is not.
func ParseInput(name string) (Input, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Input{}, true
	}
	if len(name) > len(holdPrefix) && strings.EqualFold(name[:len(holdPrefix)], holdPrefix) {
		// Only a pad input can be held.
		if p, ok := ParsePad(strings.TrimSpace(name[len(holdPrefix):])); ok {
			return HoldInput(p), true
		}
		return Input{}, false
	}
	if p, ok := ParsePad(name); ok {
		return GamepadInput(p), true
	}
	for b, n := range mouseNames {
		if strings.EqualFold(n, name) {
			return MouseInput(b), true
		}
	}
	var b uint8
	if _, err := fmt.Sscanf(name, "Mouse %d", &b); err == nil && b > 0 {
		return MouseInput(sdl.Button(b)), true
	}
	if sc := sdl.GetScancodeFromName(name); sc != sdl.SCANCODE_UNKNOWN {
		return KeyInput(sc), true
	}
	return Input{}, false
}

// BindingTable is every action's inputs.
type BindingTable [ActionCount][BindingSlots]Input

func keys(sc ...sdl.Scancode) [BindingSlots]Input {
	var out [BindingSlots]Input
	for i, s := range sc {
		out[i] = KeyInput(s)
	}
	return out
}

// DefaultBindings is the layout the engine ships with, and the one the editor's
// fly camera always uses.
var DefaultBindings = withPadDefaults(BindingTable{
	ActMoveForward:   keys(sdl.SCANCODE_W),
	ActMoveBack:      keys(sdl.SCANCODE_S),
	ActMoveLeft:      keys(sdl.SCANCODE_A),
	ActMoveRight:     keys(sdl.SCANCODE_D),
	ActJump:          keys(sdl.SCANCODE_SPACE),
	ActLookUp:        keys(sdl.SCANCODE_UP),
	ActLookDown:      keys(sdl.SCANCODE_DOWN),
	ActLookLeft:      keys(sdl.SCANCODE_LEFT),
	ActLookRight:     keys(sdl.SCANCODE_RIGHT),
	ActSprint:        keys(sdl.SCANCODE_LSHIFT),
	ActCrouch:        keys(sdl.SCANCODE_C, sdl.SCANCODE_LCTRL),
	ActInteract:      keys(sdl.SCANCODE_E),
	ActPrimaryFire:   {MouseInput(sdl.ButtonLeft)},
	ActSecondaryFire: {MouseInput(sdl.ButtonRight)},
	ActReload:        keys(sdl.SCANCODE_R),
	ActThrow:         keys(sdl.SCANCODE_G),
	ActNextWeapon:    keys(sdl.SCANCODE_Q),
	ActPrevWeapon:    {},
	ActWeapon1:       keys(sdl.SCANCODE_1),
	ActWeapon2:       keys(sdl.SCANCODE_2),
	ActWeapon3:       keys(sdl.SCANCODE_3),
	ActWeapon4:       keys(sdl.SCANCODE_4),
	ActWeapon5:       keys(sdl.SCANCODE_5),
	ActRollLeft:      keys(sdl.SCANCODE_Q),
	ActRollRight:     keys(sdl.SCANCODE_E),
	ActPitchUp:       keys(sdl.SCANCODE_LCTRL),
	ActPitchDown:     keys(sdl.SCANCODE_LSHIFT),
})

// defaultPad is each action's controller binding, the familiar shooter
// layout: left stick walks and right stick looks, the triggers aim and fire,
// the left bumper throws on foot and the bumpers roll in a cockpit. X reloads
// on a tap and interacts on a hold (defaultPadHolds) — interact is live in a
// seat too (it is how a driver stands up), so it could not share a bumper with
// a roll anyway. Pitch is unbound: in the air the look stick's pitch already
// flies the nose (sim.ReadVehicleControls).
var defaultPad = [ActionCount]PadInput{
	ActMoveForward:   PadLSUp,
	ActMoveBack:      PadLSDown,
	ActMoveLeft:      PadLSLeft,
	ActMoveRight:     PadLSRight,
	ActJump:          PadA,
	ActLookUp:        PadRSUp,
	ActLookDown:      PadRSDown,
	ActLookLeft:      PadRSLeft,
	ActLookRight:     PadRSRight,
	ActSprint:        PadLeftStick,
	ActCrouch:        PadB,
	ActInteract:      PadX,
	ActPrimaryFire:   PadRT,
	ActSecondaryFire: PadLT,
	ActReload:        PadX,
	ActThrow:         PadLB,
	ActNextWeapon:    PadY,
	ActWeapon1:       PadDPadUp,
	ActWeapon2:       PadDPadRight,
	ActWeapon3:       PadDPadDown,
	ActWeapon4:       PadDPadLeft,
	ActRollLeft:      PadLB,
	ActRollRight:     PadRB,
}

// defaultPadHolds are the actions whose default pad binding is a hold of it.
var defaultPadHolds = [...]Action{ActInteract}

func withPadDefaults(t BindingTable) BindingTable {
	for a, p := range defaultPad {
		if p != PadNone {
			t[a][PadSlot] = GamepadInput(p)
		}
	}
	for _, a := range defaultPadHolds {
		t[a][PadSlot] = HoldInput(defaultPad[a])
	}
	return t
}

// Bindings is the player's layout, which gameplay input reads.
var Bindings = DefaultBindings

// MouseButtons is SDL's mouse button mask for this frame, refreshed beside
// KeyStates so a held action bound to a button reads the same way a key does.
var MouseButtons sdl.ButtonMask

// Held reports whether any of an action's inputs is down in this frame's
// snapshot — a key, a mouse button, or its pad input pushed past
// PadPressThreshold (and down for PadHoldTime, for a hold). A press that
// shares its pad input with a hold is never held: it is a tap, which fires on
// the release (pad_hold.go), so it has no level to read.
func (t *BindingTable) Held(a Action, keyStates []uint8) bool {
	if t.KeyHeld(a, keyStates) {
		return true
	}
	in := t[a][PadSlot]
	return t.padLive(in) && PadHeld(in.Pad)
}

// KeyHeld is Held for the keyboard and mouse slots alone. The look keys turn
// the view at a fixed rate and the look stick by how far it is pushed, so the
// arrow-key path reads only this one.
func (t *BindingTable) KeyHeld(a Action, keyStates []uint8) bool {
	for _, in := range t[a][:KeySlots] {
		switch {
		case in.Mouse != 0:
			if MouseButtons&(1<<(in.Mouse-1)) != 0 {
				return true
			}
		case in.Key != 0:
			if int(in.Key) < len(keyStates) && keyStates[in.Key] != 0 {
				return true
			}
		}
	}
	return false
}

// PadValue is how far the action's pad input is pushed, 0 to 1 — 0 for a hold
// not yet held long enough, and for a tap (Held).
func (t *BindingTable) PadValue(a Action) float32 {
	in := t[a][PadSlot]
	if !t.padLive(in) {
		return 0
	}
	return PadValue(in.Pad)
}

// padLive reports whether a pad binding reads as its input's level right now:
// a press does unless a hold shares its input, and a hold once held long
// enough.
func (t *BindingTable) padLive(in Input) bool {
	switch {
	case in.Pad == PadNone:
		return false
	case in.Hold:
		return PadHoldReached(in.Pad)
	}
	return !t.HasHold(in.Pad)
}

// HasHold reports whether any action is bound to holding a pad input, which
// turns the input's presses into taps.
func (t *BindingTable) HasHold(p PadInput) bool {
	if p == PadNone {
		return false
	}
	h := HoldInput(p)
	for a := range t {
		if t[a][PadSlot] == h {
			return true
		}
	}
	return false
}

// Value is how far an action is pushed, 0 to 1: all the way for a held key or
// button, the travel for a stick or trigger. Movement and driving read this, so
// a stick pushed halfway walks at half speed.
func (t *BindingTable) Value(a Action, keyStates []uint8) float32 {
	if t.KeyHeld(a, keyStates) {
		return 1
	}
	return t.PadValue(a)
}

// ActionsFor lists the actions an input triggers, in action order.
func (t *BindingTable) ActionsFor(in Input) []Action {
	if !in.Bound() {
		return nil
	}
	var out []Action
	for a := range t {
		for _, b := range t[a] {
			if b == in {
				out = append(out, Action(a))
				break
			}
		}
	}
	return out
}

// ActionHeld is Held on the player's layout.
func ActionHeld(a Action, keyStates []uint8) bool { return Bindings.Held(a, keyStates) }

// ActionValue is Value on the player's layout.
func ActionValue(a Action, keyStates []uint8) float32 { return Bindings.Value(a, keyStates) }

// contextsOverlap reports whether two actions can be live at the same moment.
func contextsOverlap(a, b ActionContext) bool {
	return a == CtxAlways || b == CtxAlways || a == b
}

// Bind puts an input in one slot of an action, and takes it off any other
// action that could be live at the same time — so pressing a key never does two
// things, while Q can still be both "next weapon" on foot and "roll left" in a
// cockpit. Binding the zero Input clears the slot. A pad input goes only in
// PadSlot and a key or mouse button only in the others; one offered to the
// wrong column is refused.
func (t *BindingTable) Bind(a Action, slot int, in Input) {
	if a < 0 || a >= ActionCount || slot < 0 || slot >= BindingSlots {
		return
	}
	if in.Bound() && in.IsPad() != (slot == PadSlot) {
		return
	}
	if in.Bound() {
		for other := range t {
			if !contextsOverlap(Actions[a].Context, Actions[other].Context) {
				continue
			}
			for s := range t[other] {
				if t[other][s] == in && (Action(other) != a || s != slot) {
					t[other][s] = Input{}
				}
			}
		}
	}
	t[a][slot] = in
}

// ReservedKey reports whether a key belongs to the engine rather than to an
// action: Escape cancels a rebind and opens the pause menu, Backspace clears a
// binding, and the rest are the switches input.go handles before any action.
func ReservedKey(sc sdl.Scancode) bool {
	switch sc {
	case sdl.SCANCODE_ESCAPE, sdl.SCANCODE_BACKSPACE, sdl.SCANCODE_GRAVE,
		sdl.SCANCODE_0, sdl.SCANCODE_8, sdl.SCANCODE_9:
		return true
	}
	return sc >= sdl.SCANCODE_F1 && sc <= sdl.SCANCODE_F12
}

// ReservedPad is ReservedKey for a controller: Start is the pause menu (the
// pad's Escape) and cancels a rebind, Back clears one (its Backspace), and
// Guide belongs to the operating system.
func ReservedPad(p PadInput) bool {
	return p == PadStart || p == PadBack || p == PadGuide
}

// ---------- listening for a new binding ----------

// bindCapture is a rebind waiting for the player's next press.
type bindCapture struct {
	active bool
	action Action
	slot   int
	done   func()
	// padDown is a pad input pressed for the pad slot, which is bound when it
	// either comes up (a press) or stays down PadHoldTime (a hold).
	padDown PadInput
}

var capture bindCapture

// BeginBindingCapture makes the next key or mouse button press become the
// binding for an action's slot. done runs when the capture ends, however it
// ends — bound, cleared or cancelled — so a settings screen can redraw its
// labels. Starting a second capture abandons the first, and still calls its
// done.
func BeginBindingCapture(a Action, slot int, done func()) {
	EndBindingCapture()
	capture = bindCapture{active: true, action: a, slot: slot, done: done}
}

// EndBindingCapture abandons a capture in progress, if there is one.
func EndBindingCapture() {
	if !capture.active {
		return
	}
	done := capture.done
	capture = bindCapture{}
	if done != nil {
		done()
	}
}

// BindingCaptureActive reports whether a rebind is waiting for a press.
func BindingCaptureActive() bool { return capture.active }

// CaptureBindingInput offers one press to a capture in progress, and reports
// whether it was taken — a taken press must go no further, or the key the
// player just bound would also act (or Escape would also close the menu).
//
// Escape or the pad's Start cancels, Backspace, Delete or the pad's Back clear
// the slot, a reserved key is swallowed and ignored, and anything else is bound
// — if it belongs in the slot's column. A pad input waits to be let go of
// (CapturePadReleased, binding the press) or held (CapturePadHeld, binding the
// hold), which is how a player picks between the two. A key pressed while the pad slot is
// listening (or a pad button while a key slot is) is swallowed, so a player
// can still back out with whichever they are holding.
func CaptureBindingInput(in Input) bool {
	if !capture.active {
		return false
	}
	if in.IsPad() {
		switch in.Pad {
		case PadStart:
			EndBindingCapture()
			return true
		case PadBack:
			if capture.slot == PadSlot {
				Bindings.Bind(capture.action, capture.slot, Input{})
				ControlSettingsChanged = true
			}
			EndBindingCapture()
			return true
		}
		if ReservedPad(in.Pad) || capture.slot != PadSlot {
			return true
		}
		capture.padDown = in.Pad
		return true
	}
	if in.Mouse == 0 {
		switch in.Key {
		case sdl.SCANCODE_ESCAPE:
			EndBindingCapture()
			return true
		case sdl.SCANCODE_BACKSPACE, sdl.SCANCODE_DELETE:
			Bindings.Bind(capture.action, capture.slot, Input{})
			ControlSettingsChanged = true
			EndBindingCapture()
			return true
		}
		if ReservedKey(in.Key) {
			return true
		}
	}
	if capture.slot == PadSlot {
		return true
	}
	Bindings.Bind(capture.action, capture.slot, in)
	ControlSettingsChanged = true
	EndBindingCapture()
	return true
}

// CapturePadReleased finishes a capture waiting on a pad input that came up
// before it was held: the press is bound. It reports whether it did.
func CapturePadReleased(p PadInput) bool {
	return finishPadCapture(p, GamepadInput(p))
}

// CapturePadHeld finishes a capture waiting on a pad input that has now been
// held PadHoldTime: the hold is bound. It reports whether it did.
func CapturePadHeld(p PadInput) bool {
	return finishPadCapture(p, HoldInput(p))
}

func finishPadCapture(p PadInput, in Input) bool {
	if !capture.active || p == PadNone || capture.padDown != p {
		return false
	}
	Bindings.Bind(capture.action, capture.slot, in)
	ControlSettingsChanged = true
	EndBindingCapture()
	return true
}
