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
// Each action has two slots, so crouch can keep both C and Left Ctrl. The
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

// BindingSlots is how many inputs one action can answer to.
const BindingSlots = 2

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

// Input is one key or one mouse button. The zero value is "unbound".
type Input struct {
	Key   sdl.Scancode
	Mouse sdl.Button // sdl.ButtonLeft and friends; 0 for a key
}

// KeyInput and MouseInput build an Input.
func KeyInput(sc sdl.Scancode) Input { return Input{Key: sc} }
func MouseInput(b sdl.Button) Input  { return Input{Mouse: b} }

// Bound reports whether the input names anything.
func (in Input) Bound() bool { return in.Key != 0 || in.Mouse != 0 }

var mouseNames = map[sdl.Button]string{
	sdl.ButtonLeft:   "Mouse Left",
	sdl.ButtonRight:  "Mouse Right",
	sdl.ButtonMiddle: "Mouse Middle",
	sdl.ButtonX1:     "Mouse 4",
	sdl.ButtonX2:     "Mouse 5",
}

// Name is what a settings screen shows and controls.yaml stores: SDL's own
// scancode name, which is stable across layouts, or "Mouse Left" and so on.
// Unbound is the empty string.
func (in Input) Name() string {
	switch {
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
var DefaultBindings = BindingTable{
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
}

// Bindings is the player's layout, which gameplay input reads.
var Bindings = DefaultBindings

// MouseButtons is SDL's mouse button mask for this frame, refreshed beside
// KeyStates so a held action bound to a button reads the same way a key does.
var MouseButtons sdl.ButtonMask

// Held reports whether any of an action's inputs is down in this frame's
// snapshot.
func (t *BindingTable) Held(a Action, keyStates []uint8) bool {
	for _, in := range t[a] {
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

// contextsOverlap reports whether two actions can be live at the same moment.
func contextsOverlap(a, b ActionContext) bool {
	return a == CtxAlways || b == CtxAlways || a == b
}

// Bind puts an input in one slot of an action, and takes it off any other
// action that could be live at the same time — so pressing a key never does two
// things, while Q can still be both "next weapon" on foot and "roll left" in a
// cockpit. Binding the zero Input clears the slot.
func (t *BindingTable) Bind(a Action, slot int, in Input) {
	if a < 0 || a >= ActionCount || slot < 0 || slot >= BindingSlots {
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

// ---------- listening for a new binding ----------

// bindCapture is a rebind waiting for the player's next press.
type bindCapture struct {
	active bool
	action Action
	slot   int
	done   func()
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
// Escape cancels, Backspace and Delete clear the slot, a reserved key is
// swallowed and ignored, and anything else is bound.
func CaptureBindingInput(in Input) bool {
	if !capture.active {
		return false
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
	Bindings.Bind(capture.action, capture.slot, in)
	ControlSettingsChanged = true
	EndBindingCapture()
	return true
}
