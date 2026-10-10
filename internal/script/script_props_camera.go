package script

// script_props_camera.go — the camera and the pointer, as `@level` properties.
//
// The verbs (script_actions_camera.go) are how a camera is moved over time;
// these are what it is now, and the way to put it somewhere at once. Writing
// any of the placing ones detaches the camera, as the verbs do. Angles are
// degrees, in the camera's convention: yaw 0 looks along +X and a positive
// yaw turns right, pitch is positive up.
//
// Runtime state, like camera_fov: ScriptRuntimeReset gives the view back to
// the player and lets go of the cursor, so neither outlives the play session.

import (
	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
)

func cameraLevelProps() map[string]propAccess {
	angle := func(name string, get func() float32, set func(float32)) propAccess {
		return propAccess{
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(float64(degrees(get()))), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr(name, "float", v)
				}
				set(radians(f))
				return nil
			},
		}
	}
	yaw := angle("camera_yaw",
		func() float32 { y, _ := gfx.AnglesFromDirection(gfx.Cam.Direction); return y },
		func(r float32) { gfx.CameraDetach(); gfx.SetCameraAngles(r, gfx.Rig.Pitch) })
	yaw.Doc = "the way the camera faces, in degrees: 0 looks along +X and it grows turning right. Writing it detaches the camera; orbiting, it swings round the target"
	pitch := angle("camera_pitch",
		func() float32 { _, p := gfx.AnglesFromDirection(gfx.Cam.Direction); return p },
		func(r float32) { gfx.CameraDetach(); gfx.SetCameraAngles(gfx.Rig.Yaw, r) })
	pitch.Doc = "how far the camera looks up, in degrees (negative down, stopping just short of ±90). Writing it detaches the camera"
	roll := angle("camera_roll",
		func() float32 {
			if gfx.CameraDetached() || gfx.CameraReturning() {
				return gfx.Rig.Roll
			}
			return 0
		},
		gfx.SetCameraRoll)
	roll.Doc = "how far the camera is banked, in degrees; positive leans its top to the right. Writing it detaches the camera"

	return map[string]propAccess{
		"camera_detached": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.BoolVal(gfx.CameraDetached()), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("camera_detached", "bool", v)
				}
				if b {
					gfx.CameraDetach()
				} else {
					gfx.CameraAttach(0, gfx.EaseInOut)
				}
				return nil
			},
			Doc: "whether a script owns the camera rather than the player. Writing false snaps it back; camera_attach blends",
		},
		"camera_position": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return vecValue(gfx.Cam.Position), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("camera_position", "vec", v)
				}
				gfx.SetCameraPosition(p)
				return nil
			},
			Doc: "where the camera is, in world space. Writing it detaches the camera and puts it there at once, ending an orbit",
		},
		"camera_direction": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return vecValue(gfx.Cam.Direction), true
			},
			Doc: "the unit vector the camera looks along, in world space. Read-only: use camera_yaw and camera_pitch, or camera_look_at",
		},
		"camera_yaw":   yaw,
		"camera_pitch": pitch,
		"camera_roll":  roll,
		"camera_orbit_distance": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				on, d, _, _ := gfx.CameraOrbiting()
				if !on {
					return flang.NullVal{}, true
				}
				return flang.FloatVal(float64(d)), true
			},
			Doc: "how far the camera is from what it is orbiting, or null when it is not orbiting. Read-only: camera_orbit changes it",
		},
		"look_delta": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return vecValue(gfx.LookDeltaVec()), true
			},
			Doc: "this frame's mouse look in degrees — x to the right, y upward — with the player's sensitivity and smoothing applied. Detached, it turns nothing, and is how a script steers an orbit with the mouse",
		},
		"cursor": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.BoolVal(hooks.ScriptWantsCursor), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("cursor", "bool", v)
				}
				hooks.ScriptWantsCursor = b
				return nil
			},
			Doc: "on, the mouse is a pointer rather than the player's head, with no screen up to ask: it shows, mouse-look stops, and clicks on the world post :pointer_down: and the rest",
		},
		"cursor_position": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return vecValue(cursorPosition()), true
			},
			Doc: "where the pointer is, in window pixels from the top-left (z is 0). The middle of the window while the cursor is captured",
		},
		"cursor_free": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.BoolVal(hooks.Cursor().Free), true
			},
			Doc: "whether the cursor is showing and moving freely — a screen or `cursor` asked for it — rather than captured for mouse-look",
		},
		"cursor_over_ui": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				c := hooks.Cursor()
				return flang.BoolVal(c.Free && c.OverUI), true
			},
			Doc: "whether the pointer is over a widget that would take a click, rather than over the world",
		},
	}
}
