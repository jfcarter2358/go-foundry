package app

// gamepad_input.go — a game controller's presses, routed the way keys are.
//
// SDL reports buttons as events, but triggers and stick directions are
// travel, and their "press" is crossing a threshold. So the pad is read as a
// snapshot once a frame (platform.PollPads) and every input's edge — button,
// trigger or stick direction alike — comes out of that one comparison and is
// routed here, in the same order a key event takes in the event loop:
//
//	a rebind waiting for a press   (platform.CaptureBindingInput)
//	the running game's menus       (uiHandlePad)
//	gameplay                       (pressAction / releaseAction, and Start
//	                                opening the pause menu as Escape does)
//
// Releases skip the first two and always reach gameplay, so a trigger held as
// a menu came up still lets go.
//
// A press whose input also carries a hold binding waits for the release, and a
// hold acts once the input has been down platform.PadHoldTime (padGesture,
// platform/pad_hold.go) — so X can reload on a tap and interact on a hold.
//
// The device events only open and close controllers; nothing reads a press
// from them.

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/devui"
	"foundry/internal/fileutil"
	"foundry/internal/platform"
	"foundry/internal/ui"

	"github.com/veandco/go-sdl2/sdl"
)

// padMappingsFile is an optional SDL_GameControllerDB file in the project's
// resources, for pads SDL does not know out of the box.
const padMappingsFile = "gamecontrollerdb.txt"

// configurePadHints asks SDL for positional face buttons, before it is
// initialised: Pad A is the south button on every pad, so a layout is a hand
// position and only the labels change between families (platform.PadLabel).
func configurePadHints() {
	sdl.SetHint(sdl.HINT_GAMECONTROLLER_USE_BUTTON_LABELS, "0")
}

// startPads loads the project's extra mappings and opens whatever is plugged
// in. Called after the startup drain, which throws the "added" events away.
func startPads() {
	loadPadMappings(filepath.Join(fileutil.ResourcesPath, padMappingsFile))
	platform.OpenConnectedPads()
}

func loadPadMappings(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if sdl.GameControllerAddMapping(line) >= 0 {
			n++
		}
	}
	log.Printf("gamepad: %d mappings from %s", n, path)
}

// padDeviceEvent opens and closes controllers as they come and go, and
// reports whether the event was one of those.
func padDeviceEvent(event sdl.Event) bool {
	t, ok := event.(sdl.ControllerDeviceEvent)
	if !ok {
		return false
	}
	switch t.Type {
	case sdl.CONTROLLERDEVICEADDED:
		// Which is a device index here, and an instance ID for removal.
		platform.OpenPad(int(t.Which))
	case sdl.CONTROLLERDEVICEREMOVED:
		platform.ClosePad(t.Which)
	}
	return true
}

// noteKeyboardMouse marks the keyboard and mouse as the device in use when the
// player touches them. A mouse has to actually travel — a nudged desk is not
// a change of mind.
func noteKeyboardMouse(event sdl.Event) {
	switch t := event.(type) {
	case sdl.KeyboardEvent:
		if t.Type == sdl.KEYDOWN {
			platform.LastDevice = platform.DeviceKeyboard
		}
	case sdl.MouseButtonEvent:
		if t.Type == sdl.MOUSEBUTTONDOWN {
			platform.LastDevice = platform.DeviceKeyboard
		}
	case sdl.MouseWheelEvent:
		platform.LastDevice = platform.DeviceKeyboard
	case sdl.MouseMotionEvent:
		if abs32(t.XRel)+abs32(t.YRel) >= 6 {
			platform.LastDevice = platform.DeviceKeyboard
		}
	}
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// pollPadInput reads the pads for this frame and routes every edge. With the
// developer console up the pad is let go of entirely, the way the keyboard
// snapshot is blanked.
func pollPadInput(dt float32) {
	var edges []platform.PadEdge
	if devui.Open() {
		edges = platform.BlankPad()
	} else {
		edges = platform.PollPads()
	}
	for _, e := range edges {
		routePadEdge(e)
	}
	for _, p := range platform.AdvancePadHolds(dt) {
		routePadHold(p)
	}
	// A stick pushed far enough is the pad being used even before it crosses
	// the press threshold of anything bound.
	for _, p := range []platform.PadInput{platform.PadRSLeft, platform.PadRSRight, platform.PadRSUp, platform.PadRSDown} {
		if platform.PadValue(p) > 0.3 {
			platform.LastDevice = platform.DevicePad
		}
	}
	repeatPadNav(dt)
}

// padGesture is what one pad input's current press is doing in gameplay.
type padGesture struct {
	armed    bool // the press reached gameplay: no rebind or menu took it
	deferred bool // a hold shares the input, so its press waits to be a tap
	held     bool // down PadHoldTime: its hold actions have been pressed
}

var padGestures [platform.PadInputCount]padGesture

// routePadEdge is one pad input going down or coming up.
func routePadEdge(e platform.PadEdge) {
	in := platform.GamepadInput(e.In)
	if !e.Down {
		g := padGestures[e.In]
		padGestures[e.In] = padGesture{}
		if padNav.in == e.In {
			padNav = padNavRepeat{}
		}
		if platform.CapturePadReleased(e.In) {
			return
		}
		if !devActive() {
			platform.ReleaseKey(in)
		}
		switch {
		case g.held:
			for _, a := range platform.Bindings.ActionsFor(platform.HoldInput(e.In)) {
				releaseAction(a)
			}
		case g.deferred:
			// Let go before it became a hold: the press, as a tap.
			if g.armed {
				for _, a := range platform.Bindings.ActionsFor(in) {
					pressAction(a, false)
					releaseAction(a)
				}
			}
		default:
			for _, a := range platform.Bindings.ActionsFor(in) {
				releaseAction(a)
			}
		}
		return
	}
	platform.LastDevice = platform.DevicePad
	// The editor has its own input and the pad has no part in it, beyond the
	// fly camera reading the left stick through DefaultBindings.
	if devActive() || devui.Open() {
		return
	}
	if platform.CaptureBindingInput(in) {
		return
	}
	if uiHandlePad(e.In) {
		return
	}
	platform.PressKey(in)
	g := padGesture{armed: true, deferred: platform.Bindings.HasHold(e.In)}
	padGestures[e.In] = g
	if !g.deferred {
		for _, a := range platform.Bindings.ActionsFor(in) {
			pressAction(a, false)
		}
	}
	if e.In == platform.PadStart {
		// The pad's Escape: Start reaches here only when no modal screen took
		// it to close itself.
		escapeToMenu()
	}
}

// routePadHold is a pad input that has now been down platform.PadHoldTime: a
// rebind waiting on it takes the hold, and otherwise, if its press reached
// gameplay, the actions bound to holding it act.
func routePadHold(p platform.PadInput) {
	if platform.CapturePadHeld(p) {
		return
	}
	g := &padGestures[p]
	if !g.armed || g.held {
		return
	}
	g.held = true
	for _, a := range platform.Bindings.ActionsFor(platform.HoldInput(p)) {
		pressAction(a, false)
	}
}

// ---------- menus ----------

// padNavRepeat is a held direction stepping focus on, the way a held arrow key
// repeats.
type padNavRepeat struct {
	in   platform.PadInput
	dir  ui.Dir
	wait float32
}

var padNav padNavRepeat

const (
	padNavFirstRepeat = float32(0.4)
	padNavRepeatEvery = float32(0.11)
)

// padNavDir is the direction a d-pad press or a left stick push means.
func padNavDir(p platform.PadInput) (ui.Dir, bool) {
	switch p {
	case platform.PadDPadUp, platform.PadLSUp:
		return ui.DirUp, true
	case platform.PadDPadDown, platform.PadLSDown:
		return ui.DirDown, true
	case platform.PadDPadLeft, platform.PadLSLeft:
		return ui.DirLeft, true
	case platform.PadDPadRight, platform.PadLSRight:
		return ui.DirRight, true
	}
	return 0, false
}

// uiHandlePad offers a pad press to the running game's screens, and reports
// whether they took it. Only a modal screen takes pad input: with nothing but
// the HUD up, the left stick walks and the d-pad picks weapons whatever has
// focus, which is not true of the arrow keys only because nobody aims with
// them.
//
// Under one, the d-pad and the left stick move focus, A presses (or submits a
// text field), B and Start close the screen as Escape does, and anything else
// is swallowed so it cannot fire behind the menu.
func uiHandlePad(p platform.PadInput) bool {
	if devActive() || !ui.Game.CapturesInput() {
		return false
	}
	if dir, ok := padNavDir(p); ok {
		ui.Game.Navigate(dir)
		padNav = padNavRepeat{in: p, dir: dir, wait: padNavFirstRepeat}
		return true
	}
	switch p {
	case platform.PadA:
		if ui.Game.EditText(ui.EditSubmit) {
			return true
		}
		ui.Game.Activate()
	case platform.PadB, platform.PadStart:
		ui.Game.Cancel()
	}
	return true
}

// repeatPadNav steps focus on while a direction is held under a menu.
func repeatPadNav(dt float32) {
	if padNav.in == platform.PadNone {
		return
	}
	if !platform.PadHeld(padNav.in) || devActive() || !ui.Game.CapturesInput() {
		padNav = padNavRepeat{}
		return
	}
	padNav.wait -= dt
	if padNav.wait > 0 {
		return
	}
	padNav.wait = padNavRepeatEvery
	ui.Game.Navigate(padNav.dir)
}
