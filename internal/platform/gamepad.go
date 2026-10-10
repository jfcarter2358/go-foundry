package platform

// gamepad.go — game controllers: what is plugged in, what it is pushing, and
// shaking it.
//
// A pad's buttons and stick directions are Inputs the way keys are (see
// bindings.go), each with its own column in the binding table, so everything
// that reads an action — held, pressed, rebinding, scripts — reads a pad too.
// What a pad adds is travel: a stick or trigger is pushed some way, not just
// down. PadValue is that travel, 0 to 1, after the dead zone; Held is it past
// PadPressThreshold, with a little hysteresis so a stick resting on the line
// does not chatter.
//
// Every pad plugged in is read and merged, the furthest push of each input
// winning, so whichever one the player picks up just works. The one most
// recently touched is the active pad: the one that rumbles, and whose family
// names the buttons (PadLabel — "Cross" on a PlayStation pad, "A" on an Xbox).
//
// SDL is asked (app.go) for positional face buttons: Pad A is the south
// button on every pad, whatever is printed on it, so a layout means the same
// hand position on any controller and only the labels change.
//
// The state here is a snapshot the app refreshes once a frame (PollPads), next
// to KeyStates; the edges it reports (UpdatePadEdges) are what the app routes
// through rebinding, the UI and gameplay, as it does key events.

import (
	"math"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// PadInput is one button, trigger or stick direction. The zero value is none.
type PadInput uint8

const (
	PadNone PadInput = iota
	// Buttons, in SDL's order (sdl.CONTROLLER_BUTTON_* + 1).
	PadA
	PadB
	PadX
	PadY
	PadBack
	PadGuide
	PadStart
	PadLeftStick
	PadRightStick
	PadLB
	PadRB
	PadDPadUp
	PadDPadDown
	PadDPadLeft
	PadDPadRight
	PadMisc
	PadPaddle1
	PadPaddle2
	PadPaddle3
	PadPaddle4
	PadTouchpad
	// Analog, each a direction of travel from 0 to 1.
	PadLT
	PadRT
	PadLSUp
	PadLSDown
	PadLSLeft
	PadLSRight
	PadRSUp
	PadRSDown
	PadRSLeft
	PadRSRight

	PadInputCount
)

// padButtonCount is how many of the inputs are SDL buttons.
const padButtonCount = int(PadTouchpad)

// IsAnalog reports whether an input has travel rather than being on or off.
func (p PadInput) IsAnalog() bool { return p >= PadLT && p < PadInputCount }

// padNames are what controls.yaml stores and key_down takes: positional,
// family-free and stable. A rename forgets every player's pad binding to it.
var padNames = [PadInputCount]string{
	PadA: "Pad A", PadB: "Pad B", PadX: "Pad X", PadY: "Pad Y",
	PadBack: "Pad Back", PadGuide: "Pad Guide", PadStart: "Pad Start",
	PadLeftStick: "Pad Left Stick", PadRightStick: "Pad Right Stick",
	PadLB: "Pad LB", PadRB: "Pad RB",
	PadDPadUp: "Pad Up", PadDPadDown: "Pad Down", PadDPadLeft: "Pad Left", PadDPadRight: "Pad Right",
	PadMisc: "Pad Misc", PadPaddle1: "Pad Paddle 1", PadPaddle2: "Pad Paddle 2",
	PadPaddle3: "Pad Paddle 3", PadPaddle4: "Pad Paddle 4", PadTouchpad: "Pad Touchpad",
	PadLT: "Pad LT", PadRT: "Pad RT",
	PadLSUp: "Pad Left Stick Up", PadLSDown: "Pad Left Stick Down",
	PadLSLeft: "Pad Left Stick Left", PadLSRight: "Pad Left Stick Right",
	PadRSUp: "Pad Right Stick Up", PadRSDown: "Pad Right Stick Down",
	PadRSLeft: "Pad Right Stick Left", PadRSRight: "Pad Right Stick Right",
}

// PadName is an input's stored name, "" for none.
func PadName(p PadInput) string {
	if p >= PadInputCount {
		return ""
	}
	return padNames[p]
}

// ParsePad is PadName's inverse, in any case.
func ParsePad(name string) (PadInput, bool) {
	for p := PadA; p < PadInputCount; p++ {
		if strings.EqualFold(padNames[p], name) {
			return p, true
		}
	}
	return PadNone, false
}

// ---------- families and labels ----------

// PadFamily is whose button names a pad wears.
type PadFamily int

const (
	PadFamilyNone PadFamily = iota // nothing plugged in
	PadFamilyXbox                  // and anything SDL cannot place
	PadFamilyPlayStation
	PadFamilyNintendo
)

// PadFamilyNames are the script names (`@level.prop.gamepad_type`).
var PadFamilyNames = [...]string{"", "xbox", "playstation", "nintendo"}

func (f PadFamily) String() string {
	if f < 0 || int(f) >= len(PadFamilyNames) {
		return ""
	}
	return PadFamilyNames[f]
}

// padFamilyOf places an SDL controller type. Numbers past the ones go-sdl2
// names are SDL 2.24's (Luna 8, Stadia 9, Shield 10, Joy-Cons 11–13).
func padFamilyOf(t sdl.GameControllerType) PadFamily {
	switch int(t) {
	case 3, 4, 7: // PS3, PS4, PS5
		return PadFamilyPlayStation
	case 5, 11, 12, 13: // Switch Pro, Joy-Con L / R / pair
		return PadFamilyNintendo
	}
	return PadFamilyXbox
}

// The short labels a settings screen fits on a binding button, per family.
// Faces are positional (south, east, west, north), so a Nintendo pad's south
// button — Pad A — is the one printed B.
var (
	padLabelsXbox = map[PadInput]string{
		PadA: "A", PadB: "B", PadX: "X", PadY: "Y",
		PadBack: "View", PadGuide: "Xbox", PadStart: "Menu",
		PadLeftStick: "LS", PadRightStick: "RS", PadLB: "LB", PadRB: "RB", PadLT: "LT", PadRT: "RT",
		PadMisc: "Share",
	}
	padLabelsPlayStation = map[PadInput]string{
		PadA: "Cross", PadB: "Circle", PadX: "Square", PadY: "Triangle",
		PadBack: "Share", PadGuide: "PS", PadStart: "Options",
		PadLeftStick: "L3", PadRightStick: "R3", PadLB: "L1", PadRB: "R1", PadLT: "L2", PadRT: "R2",
		PadMisc: "Mute",
	}
	padLabelsNintendo = map[PadInput]string{
		PadA: "B", PadB: "A", PadX: "Y", PadY: "X",
		PadBack: "-", PadGuide: "Home", PadStart: "+",
		PadLeftStick: "LS", PadRightStick: "RS", PadLB: "L", PadRB: "R", PadLT: "ZL", PadRT: "ZR",
		PadMisc: "Capture",
	}
	// Shared by every family.
	padLabelsCommon = map[PadInput]string{
		PadDPadUp: "D-pad Up", PadDPadDown: "D-pad Down", PadDPadLeft: "D-pad Left", PadDPadRight: "D-pad Right",
		PadPaddle1: "Paddle 1", PadPaddle2: "Paddle 2", PadPaddle3: "Paddle 3", PadPaddle4: "Paddle 4",
		PadTouchpad: "Touchpad",
		PadLSUp:     "LS Up", PadLSDown: "LS Down", PadLSLeft: "LS Left", PadLSRight: "LS Right",
		PadRSUp: "RS Up", PadRSDown: "RS Down", PadRSLeft: "RS Left", PadRSRight: "RS Right",
	}
)

// PadLabel is what to show a player for an input, in the words printed on
// their pad — the active one's family, or Xbox's with none plugged in.
func PadLabel(p PadInput, f PadFamily) string {
	var faces map[PadInput]string
	switch f {
	case PadFamilyPlayStation:
		faces = padLabelsPlayStation
	case PadFamilyNintendo:
		faces = padLabelsNintendo
	default:
		faces = padLabelsXbox
	}
	if s, ok := faces[p]; ok {
		return s
	}
	if s, ok := padLabelsCommon[p]; ok {
		return s
	}
	return strings.TrimPrefix(PadName(p), "Pad ")
}

// ---------- the player's pad settings ----------

// Saved to controls.yaml with the mouse settings and written by a settings
// screen through @level.prop.pad_*, which raise ControlSettingsChanged.
var (
	// PadLookSensitivityX and Y multiply the stick's turn rate.
	PadLookSensitivityX = float32(1)
	PadLookSensitivityY = float32(1)
	// PadInvertY flips stick pitch.
	PadInvertY = false
	// PadDeadZone is how far a stick moves before it counts, as a fraction of
	// its travel; the rest is rescaled so the edge of the zone is 0, not a jump.
	PadDeadZone = float32(0.15)
	// PadLookCurve is the exponent on the look stick's travel: 1 is linear,
	// higher gives finer aim near the centre and the same top speed.
	PadLookCurve = float32(2)
	// PadVibration scales every rumble; 0 turns it off.
	PadVibration = float32(1)
	// AimAssist slows the look stick over a target and eases it onto one.
	// Pad aiming only — a mouse never gets it.
	AimAssist = true
	// PadHoldTime is how long, in seconds, a pad input has to stay down to be
	// a hold ("Hold Pad X") rather than a press of it. See pad_hold.go.
	PadHoldTime = float32(0.4)
)

// Pad setting ranges, for the script setters and controls.yaml.
const (
	MinPadSensitivity = float32(0.1)
	MaxPadSensitivity = float32(5)
	MaxPadDeadZone    = float32(0.5)
	MinPadLookCurve   = float32(1)
	MaxPadLookCurve   = float32(3)
	MinPadHoldTime    = float32(0.15)
	MaxPadHoldTime    = float32(1.5)
)

// PadPressThreshold is the travel at which an analog input counts as held;
// padReleaseThreshold is where it lets go again.
const (
	PadPressThreshold   = float32(0.5)
	padReleaseThreshold = float32(0.35)
	// triggerDeadZone is a trigger's own: it rests at 0 already, and only
	// needs to ignore a finger resting on it.
	triggerDeadZone = float32(0.05)
)

// ---------- the snapshot ----------

// PadAxes is one pad's raw sticks and triggers: sticks -1..1 with +Y down the
// way SDL reports them, triggers 0..1.
type PadAxes struct {
	LX, LY, RX, RY, LT, RT float32
}

// padValues turns raw axes and buttons into every input's travel.
func padValues(ax PadAxes, buttons *[padButtonCount]bool, dead float32) [PadInputCount]float32 {
	var v [PadInputCount]float32
	for i, down := range buttons {
		if down {
			v[PadInput(i+1)] = 1
		}
	}
	lx, ly := radialDeadZone(ax.LX, ax.LY, dead)
	rx, ry := radialDeadZone(ax.RX, ax.RY, dead)
	v[PadLSLeft], v[PadLSRight] = max(-lx, 0), max(lx, 0)
	v[PadLSUp], v[PadLSDown] = max(-ly, 0), max(ly, 0)
	v[PadRSLeft], v[PadRSRight] = max(-rx, 0), max(rx, 0)
	v[PadRSUp], v[PadRSDown] = max(-ry, 0), max(ry, 0)
	v[PadLT] = linearDeadZone(ax.LT, triggerDeadZone)
	v[PadRT] = linearDeadZone(ax.RT, triggerDeadZone)
	return v
}

// radialDeadZone zeroes a stick inside dead of the centre and stretches the
// rest back over 0..1, along the stick's own direction so a diagonal stays a
// diagonal. The result never leaves the unit circle.
func radialDeadZone(x, y, dead float32) (float32, float32) {
	m := float32(math.Hypot(float64(x), float64(y)))
	if m <= dead || m == 0 {
		return 0, 0
	}
	scaled := min((m-dead)/(1-dead), 1)
	return x / m * scaled, y / m * scaled
}

func linearDeadZone(v, dead float32) float32 {
	if v <= dead {
		return 0
	}
	return min((v-dead)/(1-dead), 1)
}

// padState is the merged snapshot every reader sees.
type padState struct {
	values [PadInputCount]float32
	// down is the held state with hysteresis, which Held and the edges read.
	down [PadInputCount]bool
	// heldFor is how long each held input has been down, in seconds
	// (AdvancePadHolds); 0 for one that is up.
	heldFor [PadInputCount]float32
}

var pad padState

// PadValue is how far an input is pushed this frame, 0 to 1.
func PadValue(p PadInput) float32 {
	if p == PadNone || p >= PadInputCount {
		return 0
	}
	return pad.values[p]
}

// PadHeld reports whether an input is held this frame.
func PadHeld(p PadInput) bool {
	if p == PadNone || p >= PadInputCount {
		return false
	}
	return pad.down[p]
}

// PadEdge is an input going down or coming up.
type PadEdge struct {
	In   PadInput
	Down bool
}

// SetPadValues installs a frame's travel and returns the edges since the last,
// in input order. Exposed for tests and for anything driving a pad without SDL.
func SetPadValues(v [PadInputCount]float32) []PadEdge {
	var edges []PadEdge
	pad.values = v
	for p := PadA; p < PadInputCount; p++ {
		was := pad.down[p]
		now := was
		if was {
			now = v[p] >= padReleaseThreshold
		} else {
			now = v[p] >= PadPressThreshold
		}
		if now != was {
			pad.down[p] = now
			pad.heldFor[p] = 0
			edges = append(edges, PadEdge{In: p, Down: now})
		}
	}
	return edges
}

// BlankPad lets go of everything, as an edge list — for the developer console
// taking the input, and for a pad being unplugged mid-press.
func BlankPad() []PadEdge { return SetPadValues([PadInputCount]float32{}) }

// ---------- devices ----------

type openPad struct {
	gc     *sdl.GameController
	id     sdl.JoystickID
	family PadFamily
	last   [PadInputCount]float32
}

var (
	pads      []*openPad
	activePad sdl.JoystickID = -1
)

// OpenPad opens the controller at an SDL device index, once.
func OpenPad(index int) {
	if !sdl.IsGameController(index) {
		return
	}
	id := sdl.JoystickGetDeviceInstanceID(index)
	for _, p := range pads {
		if p.id == id {
			return
		}
	}
	gc := sdl.GameControllerOpen(index)
	if gc == nil {
		return
	}
	pads = append(pads, &openPad{gc: gc, id: id, family: padFamilyOf(gc.Type())})
	if activePad < 0 {
		activePad = id
	}
}

// OpenConnectedPads opens every controller already plugged in. The app drains
// SDL's queue at startup, which takes the added events with it.
func OpenConnectedPads() {
	for i := 0; i < sdl.NumJoysticks(); i++ {
		OpenPad(i)
	}
}

// ClosePad forgets a controller by its instance ID.
func ClosePad(id sdl.JoystickID) {
	for i, p := range pads {
		if p.id != id {
			continue
		}
		p.gc.Close()
		pads = append(pads[:i], pads[i+1:]...)
		if activePad == id {
			activePad = -1
			if len(pads) > 0 {
				activePad = pads[0].id
			}
			rumble = rumbleState{}
		}
		return
	}
}

// PadConnected reports whether any controller is plugged in.
func PadConnected() bool { return len(pads) > 0 }

// ActivePadFamily is the family of the pad last touched, or none.
func ActivePadFamily() PadFamily {
	if p := active(); p != nil {
		return p.family
	}
	return PadFamilyNone
}

func active() *openPad {
	for _, p := range pads {
		if p.id == activePad {
			return p
		}
	}
	return nil
}

func axisValue(gc *sdl.GameController, a sdl.GameControllerAxis) float32 {
	return max(float32(gc.Axis(a))/32767, -1)
}

// PollPads reads every open controller and returns the edges since the last
// poll. A pad that moved becomes the active one.
func PollPads() []PadEdge {
	var merged [PadInputCount]float32
	for _, p := range pads {
		var buttons [padButtonCount]bool
		for b := range buttons {
			buttons[b] = p.gc.Button(sdl.GameControllerButton(b)) != 0
		}
		v := padValues(PadAxes{
			LX: axisValue(p.gc, sdl.CONTROLLER_AXIS_LEFTX),
			LY: axisValue(p.gc, sdl.CONTROLLER_AXIS_LEFTY),
			RX: axisValue(p.gc, sdl.CONTROLLER_AXIS_RIGHTX),
			RY: axisValue(p.gc, sdl.CONTROLLER_AXIS_RIGHTY),
			LT: axisValue(p.gc, sdl.CONTROLLER_AXIS_TRIGGERLEFT),
			RT: axisValue(p.gc, sdl.CONTROLLER_AXIS_TRIGGERRIGHT),
		}, &buttons, PadDeadZone)
		for i := range v {
			merged[i] = max(merged[i], v[i])
			// A real change of input, not noise inside the dead zone (which
			// padValues has already zeroed).
			if math.Abs(float64(v[i]-p.last[i])) > 0.2 {
				if p.id != activePad {
					activePad = p.id
					rumble = rumbleState{}
				}
			}
		}
		p.last = v
	}
	return SetPadValues(merged)
}

// ---------- which device the player is using ----------

// InputDevice is what the player last played with.
type InputDevice int

const (
	DeviceKeyboard InputDevice = iota // and mouse
	DevicePad
)

// LastDevice is the device the player last used, which decides whether aim
// assist runs, whether the menu cursor shows, and what a script showing a
// button's name should name.
var LastDevice = DeviceKeyboard

// ---------- rumble ----------

// rumbleState is what the active pad is doing now, so a weak buzz arriving
// during a strong one does not cut it short: SDL replaces the running effect
// with every call, so overlapping requests are merged here first.
type rumbleState struct {
	low, high, left, right float32
	until, triggersUntil   uint32 // SDL ticks
}

var rumble rumbleState

// Rumble shakes the active pad: low is the heavy motor, high the light one,
// both 0..1, for seconds. Scaled by PadVibration. Merged with whatever is
// running: the stronger of each motor, until the later end.
func Rumble(low, high, seconds float32) {
	p := active()
	if p == nil || PadVibration <= 0 || seconds <= 0 {
		return
	}
	now := sdl.GetTicks()
	end := now + uint32(seconds*1000)
	if now >= rumble.until {
		rumble.low, rumble.high = 0, 0
	}
	rumble.low = max(rumble.low, clampUnit(low))
	rumble.high = max(rumble.high, clampUnit(high))
	rumble.until = max(rumble.until, end)
	_ = p.gc.Rumble(motor(rumble.low), motor(rumble.high), rumble.until-now)
}

// RumbleTriggers shakes the impulse triggers, on pads that have them.
func RumbleTriggers(left, right, seconds float32) {
	p := active()
	if p == nil || PadVibration <= 0 || seconds <= 0 || !p.gc.HasRumbleTriggers() {
		return
	}
	now := sdl.GetTicks()
	end := now + uint32(seconds*1000)
	if now >= rumble.triggersUntil {
		rumble.left, rumble.right = 0, 0
	}
	rumble.left = max(rumble.left, clampUnit(left))
	rumble.right = max(rumble.right, clampUnit(right))
	rumble.triggersUntil = max(rumble.triggersUntil, end)
	_ = p.gc.RumbleTriggers(motor(rumble.left), motor(rumble.right), rumble.triggersUntil-now)
}

// StopRumble stops every pad — a menu coming up, the editor taking over.
func StopRumble() {
	if rumble == (rumbleState{}) {
		return
	}
	rumble = rumbleState{}
	for _, p := range pads {
		_ = p.gc.Rumble(0, 0, 0)
		if p.gc.HasRumbleTriggers() {
			_ = p.gc.RumbleTriggers(0, 0, 0)
		}
	}
}

func motor(v float32) uint16 { return uint16(clampUnit(v*PadVibration) * 65535) }

func clampUnit(v float32) float32 { return min(max(v, 0), 1) }
