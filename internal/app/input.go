package app

import (
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/platform"
	"foundry/internal/render"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
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
	gfx.Projection = mgl32.Perspective(mgl32.DegToRad(45.0), float32(platform.WinWidth)/float32(platform.WinHeight), 0.1, 1000.0)
	gl.UniformMatrix4fv(gfx.ProjectionUniform, 1, false, &gfx.Projection[0])
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
		// With the player's control off the view stays where it is. The cursor
		// still goes back to centre, or the motion made meanwhile would all
		// arrive as one lurch the moment control is handed back.
		if !devActive() && !platform.PlayerControlEnabled {
			platform.Window.WarpMouseInWindow(platform.WinHalfWidth, platform.WinHalfHeight)
			break
		}
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
		if t.Type == sdl.KEYDOWN {
			switch t.Keysym.Scancode {
			case sdl.SCANCODE_F11:
				ToggleFullscreen()
			case sdl.SCANCODE_E:
				// The interact key. Recorded as an edge rather than read as a
				// level, so holding it fires once — SCRIPTING.md's `:interact:`
				// is a press, not a state.
				if !devActive() && platform.PlayerControlEnabled {
					hooks.ScriptInteractRequest()
				}

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
			case sdl.SCANCODE_SPACE:
				if !devActive() && platform.PlayerControlEnabled && platform.GravityEnabled && platform.IsOnGround {
					platform.JumpRequested = true
				}
			case sdl.SCANCODE_F3:
				render.DebugToggle()
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
	}
}
