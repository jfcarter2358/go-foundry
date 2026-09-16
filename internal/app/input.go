package app

import (
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/platform"
	"foundry/internal/render"
	"foundry/internal/script"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/veandco/go-sdl2/sdl"
)

// RefreshWindowSize updates all engine state that depends on the window
// dimensions: viewport, projection matrix, half-width/height, and mouse warp.
func RefreshWindowSize() {
	platform.WinWidth, platform.WinHeight = platform.Window.GetSize()
	platform.WinHalfWidth = platform.WinWidth / 2
	platform.WinHalfHeight = platform.WinHeight / 2
	gl.Viewport(0, 0, platform.WinWidth, platform.WinHeight)
	if platform.WinHeight < 1 {
		platform.WinHeight = 1
	}
	gfx.RebuildProjection()
	if gfx.Cam.MouseLock {
		platform.Window.WarpMouseInWindow(platform.WinHalfWidth, platform.WinHalfHeight)
	}
}

// ToggleFullscreen switches between windowed and borderless fullscreen.
func ToggleFullscreen() {
	if platform.Window.GetFlags()&sdl.WINDOW_FULLSCREEN_DESKTOP != 0 {
		platform.Window.SetFullscreen(uint32(0))
	} else {
		platform.Window.SetFullscreen(uint32(sdl.WINDOW_FULLSCREEN_DESKTOP))
	}
	RefreshWindowSize()
}

func HandleEvent(event sdl.Event) {
	switch t := event.(type) {
	case sdl.QuitEvent:
		platform.Running = false

	case sdl.MouseMotionEvent:
		if !gfx.Cam.MouseLock {
			break
		}
		// Warp-to-center approach: measure delta from the window centre.
		// After processing we warp the cursor back to centre; that warp itself
		// generates another MOUSEMOTION event whose position IS the centre
		// (dx==0, dy==0), so we skip it here.  This avoids every pitfall of
		// SDL relative-mouse mode on X11/WSL2.
		dx := t.X - platform.WinHalfWidth
		dy := t.Y - platform.WinHalfHeight
		if dx == 0 && dy == 0 {
			break
		}
		// With the player's control off, or a script holding the game paused,
		// the view stays where it is. The cursor still goes back to centre, or
		// the motion made meanwhile would all arrive as one lurch the moment
		// control is handed back.
		if !devActive() && (!platform.PlayerControlEnabled || script.GamePaused()) {
			dropMouseLook()
			platform.Window.WarpMouseInWindow(platform.WinHalfWidth, platform.WinHalfHeight)
			break
		}
		if devActive() {
			gfx.Cam.Yaw += platform.MouseRotationSpeed * float32(dx)
			gfx.Cam.Pitch -= platform.MouseRotationSpeed * float32(dy)
			if gfx.Cam.Pitch > gfx.MaxPitch {
				gfx.Cam.Pitch = gfx.MaxPitch
			}
			if gfx.Cam.Pitch < -gfx.MaxPitch {
				gfx.Cam.Pitch = -gfx.MaxPitch
			}
			// Recompute direction immediately so movement this frame uses the
			// correct look direction (no one-frame rotation lag).
			gfx.Cam.Direction = gfx.DirectionFromAngles(gfx.Cam.Yaw, gfx.Cam.Pitch)
			gfx.UpdateCamMatrix()
		} else {
			// Gameplay look is banked and spent by the frame loop, with the
			// player's sensitivity and smoothing — see mouse_look.go.
			bankMouseLook(dx, dy)
		}
		// Return cursor to centre for next delta measurement.
		platform.Window.WarpMouseInWindow(platform.WinHalfWidth, platform.WinHalfHeight)

	case sdl.WindowEvent:
		// A secondary UIWindow (texture editor, etc.) reports its own size/
		// focus events too; only the main window's should drive the 3D
		// viewport and mouse-capture state. Its own close/resize/redraw are
		// handled by RouteWindowEvent (window.go) before HandleEvent ever
		// sees them — see the dispatch in main.go.
		if mw, err := platform.Window.GetID(); err != nil || t.WindowID != mw {
			break
		}
		switch t.Event {
		case sdl.WINDOWEVENT_CLOSE:
			// The main window is the game and the 3D editor both, so its close
			// button means "quit" — and quitting takes the sub-editor windows
			// with it (devShutdown). SDL only synthesises a QuitEvent when the
			// *last* window goes, so with a 2D map or a script editor open this
			// is the only notice we get; without this case the close button did
			// nothing at all whenever a sub-editor was on screen.
			platform.Running = false
		case sdl.WINDOWEVENT_FOCUS_LOST:
			// Release capture but remember whether the current mode wanted it.
			gfx.ApplyMouseLock(false)
			// The button-up that would have ended a drag goes to whatever took
			// focus, so end them here instead — otherwise a camera turn
			// interrupted by an alt-tab is still running on the way back.
			devEndDrags()
		case sdl.WINDOWEVENT_SIZE_CHANGED:
			RefreshWindowSize()
		case sdl.WINDOWEVENT_FOCUS_GAINED:
			// Restore only what the mode asked for.  Editor 2D/surface
			// modes never want capture, and a stray focus cycle from a right or
			// middle click must not hand them a grabbed cursor.  The warp inside
			// ApplyMouseLock also makes the first motion event after regaining
			// focus measure from the correct origin.
			gfx.ApplyMouseLock(gfx.Cam.MouseLockWanted)
		}

	case sdl.KeyboardEvent:
		// A gameplay key is an action, looked up in the player's layout —
		// see platform/bindings.go. The switch below is only the engine's own
		// keys, which platform.ReservedKey keeps any binding off.
		if t.Type == sdl.KEYUP {
			for _, a := range platform.Bindings.ActionsFor(platform.KeyInput(t.Keysym.Scancode)) {
				releaseAction(a)
			}
			break
		}
		if t.Type == sdl.KEYDOWN {
			for _, a := range platform.Bindings.ActionsFor(platform.KeyInput(t.Keysym.Scancode)) {
				pressAction(a, t.Repeat != 0)
			}
			switch t.Keysym.Scancode {
			case sdl.SCANCODE_F11:
				ToggleFullscreen()
			case sdl.SCANCODE_ESCAPE:
				// The pause menu. Escape reaches this handler only when the
				// screen stack did not take it, which is precisely when there
				// is no modal screen up to close — so this is the "open" half
				// of the pair and internal/ui owns the "close" half. See
				// uiOpenPauseMenu.
				if !devActive() {
					uiOpenPauseMenu()
				}

			case sdl.SCANCODE_GRAVE: // tilde (~) key toggles level editor
				// A release build has no editor to toggle; devToggle is a stub
				// there and the key does nothing.
				devToggle()
			case sdl.SCANCODE_F3:
				render.DebugToggle()
			case sdl.SCANCODE_F4:
				// Volumetric quality: half resolution, full, off. A cost
				// switch like the F5-F9 ones, read against the F3 overlay's
				// frame time — see render.VolumetricSettings.
				render.CycleVolumetricQuality()
			// Biome lighting cost switches — see BiomeShadowSettings. Bound to
			// keys, not editor checkboxes, so they can be flipped while flying
			// around with the mouse captured and the F3 overlay's frame time
			// read directly against each one.
			case sdl.SCANCODE_F5:
				render.BiomeShadows.Receive = !render.BiomeShadows.Receive
			case sdl.SCANCODE_F6:
				render.BiomeShadows.CastRockMoss = !render.BiomeShadows.CastRockMoss
			case sdl.SCANCODE_F7:
				render.BiomeShadows.CastFoliage = !render.BiomeShadows.CastFoliage
			case sdl.SCANCODE_F8:
				// Cycle 0..ShadowCascades: 0 disables scatter casting entirely
				// without losing the per-kind switches above.
				render.BiomeShadows.CastCascades = (render.BiomeShadows.CastCascades + 1) % (render.ShadowCascades + 1)
			case sdl.SCANCODE_F9:
				// Cycle 1..3: how many LOD tiers (Near/Mid/Far) cast real
				// per-instance shadows — see BiomeShadowSettings.CastLODTiers.
				render.BiomeShadows.CastLODTiers = render.BiomeShadows.CastLODTiers%3 + 1
			case sdl.SCANCODE_F10:
				// Window-level multisample anti-aliasing — see antialias.go.
				render.ToggleAntiAlias()
			case sdl.SCANCODE_F12:
				// Wait for the display on swap, or don't — see vsync.go. Next
				// to the other two presentation switches (F10, F11) because it
				// is the same kind of thing: how the window is put on screen,
				// not what is drawn into it.
				platform.ToggleVSync()
			case sdl.SCANCODE_0:
				if !devActive() {
					platform.GravityEnabled = !platform.GravityEnabled
					if !platform.GravityEnabled {
						platform.IsOnGround = false
						platform.JumpRequested = false
					}
				}
			case sdl.SCANCODE_8:
				// Engine-wide entity collision switches. Debug switches, not
				// gameplay ones: they say whether the engine honours the
				// per-entity flags at all, which is how you tell "this entity
				// is not solid" from "collision is off".
				if !devActive() {
					world.EntityWorldCollisionEnabled = !world.EntityWorldCollisionEnabled
				}
			case sdl.SCANCODE_9:
				if !devActive() {
					world.EntityEntityCollisionEnabled = !world.EntityEntityCollisionEnabled
				}
			}
		}

	case sdl.MouseWheelEvent:
		// Weapon cycling. Wheel up is the previous weapon and down the next,
		// the way a list scrolls. Only reached when the UI did not consume the
		// wheel (uiHandleEvent), so a scrolling menu never switches guns.
		if !devActive() && platform.PlayerControlEnabled && !script.GamePaused() {
			if t.Y > 0 {
				hooks.ScriptWeaponCycleRequest(-1)
			} else if t.Y < 0 {
				hooks.ScriptWeaponCycleRequest(1)
			}
		}

	case sdl.MouseButtonEvent:
		// Mouse buttons are actions like keys are — fire and aim by default.
		// Gameplay-only — editor picking uses mouse buttons too, but through
		// devActive's own handlers, never this one, so the two never fight
		// over a click.
		for _, a := range platform.Bindings.ActionsFor(platform.MouseInput(t.Button)) {
			if t.Type == sdl.MOUSEBUTTONDOWN {
				pressAction(a, false)
			} else {
				releaseAction(a)
			}
		}
	}
}

// gameplayInput reports whether the player's presses reach the game: not in the
// editor, not with control handed away, not under a script's pause.
func gameplayInput() bool {
	return !devActive() && platform.PlayerControlEnabled && !script.GamePaused()
}

// onFoot reports whether the player is out of any seat, which is when the keys
// a vehicle borrows (Q rolls, Left Shift pitches) are the player's own again.
func onFoot() bool {
	return world.PlayerEntity == nil || world.PlayerEntity.Seat == ""
}

// pressAction is an action's key or button going down. repeat is the
// keyboard's auto-repeat, which only the actions that make sense held accept.
//
// The held actions — moving, looking, crouching, rolling and pitching — are not
// here: they are read as state each frame (platform.ActionHeld), not as edges.
func pressAction(a platform.Action, repeat bool) {
	if !gameplayInput() {
		return
	}
	switch a {
	case platform.ActInteract:
		// Recorded as an edge rather than read as a level, so holding it fires
		// once — SCRIPTING.md's `:interact:` is a press, not a state.
		if !repeat {
			hooks.ScriptInteractRequest()
		}
	case platform.ActJump:
		// Not while a script holds the game paused (gameplayInput): MovePlayer
		// is not running to spend the request, so it would wait and fire on
		// the first frame after the pause lifted.
		if platform.GravityEnabled && platform.IsOnGround {
			platform.JumpRequested = true
		}
	case platform.ActThrow:
		hooks.ScriptThrowRequest()
	case platform.ActWeapon1, platform.ActWeapon2, platform.ActWeapon3, platform.ActWeapon4, platform.ActWeapon5:
		hooks.ScriptWeaponSlotRequest(int(a-platform.ActWeapon1) + 1)
	case platform.ActSprint:
		// Sprint, as a toggle. Only with gravity on: in free-fly the sprint key
		// is "down", held (gfx.MoveIntent).
		if !repeat && platform.GravityEnabled && world.PlayerEntity != nil && onFoot() {
			world.SetPlayerSprinting(!world.PlayerSprinting())
		}
	case platform.ActReload:
		if !repeat {
			hooks.ScriptReloadRequest()
		}
	case platform.ActNextWeapon, platform.ActPrevWeapon:
		// Not on key repeat, or holding the key spins through every weapon at
		// the keyboard's repeat rate. Nor while seated: Q is a vehicle's
		// control there.
		if !repeat && onFoot() {
			dir := 1
			if a == platform.ActPrevWeapon {
				dir = -1
			}
			hooks.ScriptWeaponCycleRequest(dir)
		}
	case platform.ActPrimaryFire:
		if !repeat {
			hooks.ScriptPrimaryFireRequest(true)
		}
	case platform.ActSecondaryFire:
		if !repeat {
			hooks.ScriptSecondaryFireRequest(true)
		}
	}
}

// releaseAction is an action's key or button coming back up. Only the fire
// buttons care, and they care whatever else is going on: a trigger let go of
// while a menu opened must not stay held.
func releaseAction(a platform.Action) {
	if devActive() {
		return
	}
	switch a {
	case platform.ActPrimaryFire:
		hooks.ScriptPrimaryFireRequest(false)
	case platform.ActSecondaryFire:
		hooks.ScriptSecondaryFireRequest(false)
	}
}

// captureBindingEvent hands a press to a rebind in progress (bind_key), ahead
// of the UI and gameplay both, and reports whether it took it. It has to come
// first: the key being bound must not also act, Escape cancelling the rebind
// must not also close the settings screen, and a mouse button must not also
// click whatever is under the pointer.
//
// Only presses: the release of the click or Enter that started the capture
// arrives afterwards and must not be what gets bound. Key repeat is ignored for
// the same reason.
func captureBindingEvent(event sdl.Event) bool {
	if !platform.BindingCaptureActive() {
		return false
	}
	switch t := event.(type) {
	case sdl.KeyboardEvent:
		if t.Type != sdl.KEYDOWN {
			return false
		}
		if t.Repeat != 0 {
			return true
		}
		return platform.CaptureBindingInput(platform.KeyInput(t.Keysym.Scancode))
	case sdl.MouseButtonEvent:
		if t.Type != sdl.MOUSEBUTTONDOWN {
			return false
		}
		return platform.CaptureBindingInput(platform.MouseInput(t.Button))
	}
	return false
}
