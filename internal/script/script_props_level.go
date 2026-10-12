package script

// script_props_level.go — the `.prop` surface on `@level`.
//
// The two load properties are there because a loading screen needs them: a bar
// bound to `@level.prop.load_progress` is the whole reason the load reports
// itself at all. Both are read-only — a script that could write the load
// progress would be lying to the bar rather than telling it anything.
//
// `step_height` is the level's own gameplay tuning rather than a report, and is
// the one thing here a script may write. It lives on `@level` and not on
// `@player` for the same reason it lives on world.Level and not on world.Entity:
// there is one player, the setting is saved in the level file, and putting it in
// the entity table would advertise it on every barrel and door in the level as
// if each carried its own.
//
// The `sky_*` properties are the same idea applied to world.Sky: a level-scoped
// singleton, edited today from the Environment group of the properties panel
// (editor_environment_panel.go), and now writable at runtime for the same reason
// fog volumes are — a script fading from day to dusk, or swapping a cubemap when
// the player steps through a portal, is authoring the same field the editor's
// checkbox and combo do, just on a schedule the level file cannot express. None
// of it is saved: like step_height, a script's writes act on the level in memory
// and the editor's snapshot restores what the level file said once play ends.

import (
	"strings"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/audio"
	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/platform"
	"foundry/internal/world"
)

func levelProps() map[string]propAccess {
	m := map[string]propAccess{
		"load_progress": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(float64(hooks.LoadProgress())), true
			},
			Doc: "how far a level load has got, from 0 to 1. 1 when nothing is loading",
		},
		"name": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil || c.lvl == nil {
					return nil, false
				}
				return flang.StrVal(c.lvl.Name), true
			},
			Doc: "the running level's name, as load_level takes it; \"\" for a level that was never loaded from a file",
		},
		"save_mode": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil || c.lvl == nil {
					return nil, false
				}
				return flang.StrVal(world.SaveModeNames[c.lvl.SaveMode]), true
			},
			Doc: "what save_game writes in this level: \"exact\" (where the player stands) or \"checkpoint\" (the last checkpoint reached)",
		},
		"last_checkpoint": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.StrVal(hooks.LastCheckpoint()), true
			},
			Doc: "the checkpoint retry() goes back to, or \"\" when none has been reached since the level was loaded or restarted",
		},
		"load_stage": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.StrVal(hooks.LoadStage()), true
			},
			Doc: "a one-line description of what the load is doing, or empty when nothing is",
		},

		// Which kind of binary is running this (hooks.DevBuild): the
		// development build with the editor in it, or a shipped release. A
		// settings screen reads it to offer what only makes sense while a
		// level is being built and tested — a difficulty that can be changed
		// mid-mission, a debug view — and to leave it out of the release the
		// player gets. Read-only in both, for the obvious reason.
		"dev_build": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.BoolVal(hooks.DevBuild), true
			},
			Doc: "true while the running binary is a development build (the one with the 3D editor), false in a release build",
		},

		// Writable, and it takes effect on the very next frame: MovePlayer asks
		// the level for this once per frame rather than caching it, so a script
		// lowering it for a crawlspace or raising it for a climb needs nothing
		// more than the assignment.
		"step_height": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil || c.lvl == nil {
					return nil, false
				}
				// The number in force, not the raw field: a script reading this
				// on a level that never set one wants the height it is actually
				// walking with, and 0 would be an answer that is never true.
				return flang.FloatVal(float64(
					c.lvl.Player.EffectiveStepHeight())), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("step_height", "num", v)
				}
				if c == nil || c.lvl == nil {
					return nil
				}
				// Anything not positive means the default, the same as an absent
				// `player:` block — see world.PlayerSettings. So `= 0` is how a
				// script hands the setting back rather than a way to freeze the
				// player against every floor seam in the level.
				if f < 0 {
					f = 0
				}
				c.lvl.Player.StepHeight = f
				return nil
			},
			Doc: "how tall a riser the player walks up rather than being stopped by, in world units; 0 restores the default (the player's own height)",
		},

		// The spawn point's two switches, for a script to hand back. They are
		// engine state rather than fields on the level — there is one player —
		// but they sit here beside step_height for the same reason it does.
		// Entering play sets both from the spawn again, so a write is never
		// carried out of the session.
		"player_control": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.BoolVal(platform.PlayerControlEnabled), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("player_control", "bool", v)
				}
				world.SetPlayerControl(b)
				return nil
			},
			Doc: "whether the player's input reaches the game: walking, jumping, driving, interact and looking around",
		},
		"player_gravity": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.BoolVal(platform.GravityEnabled), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("player_gravity", "bool", v)
				}
				world.SetPlayerGravity(b)
				return nil
			},
			Doc: "whether the player falls. Turning it off is weightlessness, not flight: the body keeps the momentum it has and drifts. To fly it, use player_fly",
		},
		"player_fly": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.BoolVal(platform.FlyMode), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("player_fly", "bool", v)
				}
				world.SetPlayerFly(b)
				return nil
			},
			Doc: "free-fly: the jump and sprint keys move the player straight up and down, and nothing pulls on them. Separate from player_gravity, which only says whether they fall",
		},

		// The level's own gravity. A number rather than a switch, because the
		// interesting cases are between on and off: a moon, a ship under
		// thrust, a slow-motion descent. A volume may override it in one region
		// and a body may scale it — see world/gravity.go.
		"gravity": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(world.LevelGravity(c.lvl)), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("gravity", "number", v)
				}
				if c.lvl == nil {
					return nil
				}
				// Written through a fresh pointer rather than into the one the
				// level is holding: the loader and the saver share these
				// pointers across a round trip, and writing through one would
				// edit the authored value as well as the live one.
				c.lvl.Gravity = &f
				return nil
			},
			Doc: "the level's downward acceleration, in world units a second squared. 36 is the engine's default, about 6 a moon, 0 a station with the plating off; a negative number falls upward",
		},

		// The level's far plane. Written onto the level like gravity, so a
		// reload puts the authored distance back and a save keeps a changed one.
		"view_distance": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(world.LevelViewDistance(c.lvl)), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("view_distance", "number", v)
				}
				if c.lvl == nil {
					return nil
				}
				if f <= 0 {
					c.lvl.ViewDistance = 0
					return nil
				}
				c.lvl.ViewDistance = world.ClampViewDistance(f)
				return nil
			},
			Doc: "how far the camera sees, in world units (the level's `view_distance:`, 1000 by default, clamped to 50–50000); 0 goes back to the default",
		},

		// The camera's lens, for a scope or a sprint's widening. Engine state
		// like the two switches above; ScriptRuntimeReset puts the default
		// back, so a zoom held when play ends does not follow the author into
		// the editor.
		"camera_fov": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(gfx.FieldOfView), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("camera_fov", "float", v)
				}
				fov := float32(f)
				if fov < gfx.MinFieldOfView {
					fov = gfx.MinFieldOfView
				} else if fov > gfx.MaxFieldOfView {
					fov = gfx.MaxFieldOfView
				}
				// The later instruction is the one that meant it: a zoom
				// still easing would take the lens straight back.
				gfx.StopCameraZoom()
				gfx.FieldOfView = fov
				return nil
			},
			Doc: "the camera's vertical field of view in degrees (45 by default, clamped to 5–120); lower zooms in",
		},

		// The viewmodel's lens, which is deliberately NOT the camera's: the
		// hands are drawn through their own fixed field of view so that a scope
		// zooming the world does not magnify the arms with it (gfx's
		// projection.go). A script that wants the gun pushed further away, or
		// pulled in, moves this rather than camera_fov.
		"viewmodel_fov": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(gfx.ViewmodelFieldOfView), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("viewmodel_fov", "float", v)
				}
				gfx.ViewmodelFieldOfView = clampFloat32(float32(f), gfx.MinFieldOfView, gfx.MaxFieldOfView)
				return nil
			},
			Doc: "the field of view the first-person viewmodel is drawn through in degrees (55 by default, clamped to 5–120); independent of camera_fov, so aiming does not swell the hands",
		},

		// The player's mouse settings, for a settings screen. Unlike every other
		// level property these are the player's, not the level's: they persist
		// across levels and sessions (app/controls_settings.go saves them once
		// a change has settled), and nothing resets them.
		"mouse_sensitivity": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(platform.MouseSensitivity), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("mouse_sensitivity", "float", v)
				}
				platform.MouseSensitivity = clampFloat32(float32(f), MinMouseSensitivity, MaxMouseSensitivity)
				platform.ControlSettingsChanged = true
				return nil
			},
			Doc: "the player's mouse look sensitivity, a multiplier (1 by default, 0.1–5); saved for every level and session",
		},
		"mouse_smoothing": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(platform.MouseSmoothing), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("mouse_smoothing", "float", v)
				}
				platform.MouseSmoothing = clampFloat32(float32(f), 0, 1)
				platform.ControlSettingsChanged = true
				return nil
			},
			Doc: "how much mouse look is smoothed, 0 (raw) to 1 (heaviest); saved for every level and session",
		},
		"mouse_invert_y": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.BoolVal(platform.MouseInvertY), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("mouse_invert_y", "bool", v)
				}
				platform.MouseInvertY = b
				platform.ControlSettingsChanged = true
				return nil
			},
			Doc: "whether moving the mouse up looks down; saved for every level and session",
		},

		// The player's game controller settings, saved beside the mouse ones.
		"pad_sensitivity_x": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(platform.PadLookSensitivityX), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("pad_sensitivity_x", "float", v)
				}
				platform.PadLookSensitivityX = clampFloat32(float32(f), platform.MinPadSensitivity, platform.MaxPadSensitivity)
				platform.ControlSettingsChanged = true
				return nil
			},
			Doc: "how fast the look stick turns the view left and right, a multiplier (1 by default, 0.1–5); saved for every level and session",
		},
		"pad_sensitivity_y": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(platform.PadLookSensitivityY), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("pad_sensitivity_y", "float", v)
				}
				platform.PadLookSensitivityY = clampFloat32(float32(f), platform.MinPadSensitivity, platform.MaxPadSensitivity)
				platform.ControlSettingsChanged = true
				return nil
			},
			Doc: "how fast the look stick turns the view up and down, a multiplier (1 by default, 0.1–5); saved for every level and session",
		},
		"pad_invert_y": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.BoolVal(platform.PadInvertY), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("pad_invert_y", "bool", v)
				}
				platform.PadInvertY = b
				platform.ControlSettingsChanged = true
				return nil
			},
			Doc: "whether pushing the look stick up looks down; saved for every level and session",
		},
		"pad_dead_zone": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(platform.PadDeadZone), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("pad_dead_zone", "float", v)
				}
				platform.PadDeadZone = clampFloat32(float32(f), 0, platform.MaxPadDeadZone)
				platform.ControlSettingsChanged = true
				return nil
			},
			Doc: "how far a stick moves before it counts, as a fraction of its travel (0.15 by default, 0–0.5); saved for every level and session",
		},
		"pad_look_curve": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(platform.PadLookCurve), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("pad_look_curve", "float", v)
				}
				platform.PadLookCurve = clampFloat32(float32(f), platform.MinPadLookCurve, platform.MaxPadLookCurve)
				platform.ControlSettingsChanged = true
				return nil
			},
			Doc: "the look stick's response curve, an exponent on its travel: 1 is linear, higher gives finer aim near the centre (2 by default, 1–3); saved for every level and session",
		},
		"pad_vibration": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(platform.PadVibration), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("pad_vibration", "float", v)
				}
				platform.PadVibration = clampFloat32(float32(f), 0, 1)
				platform.ControlSettingsChanged = true
				return nil
			},
			Doc: "how strongly the pad rumbles, 0 (off) to 1 (1 by default); saved for every level and session",
		},
		"pad_hold_time": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(platform.PadHoldTime), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("pad_hold_time", "float", v)
				}
				platform.PadHoldTime = clampFloat32(float32(f), platform.MinPadHoldTime, platform.MaxPadHoldTime)
				platform.ControlSettingsChanged = true
				return nil
			},
			Doc: "seconds a pad button must stay down to count as holding it (\"Hold Pad X\") rather than pressing it (0.4 by default, 0.15–1.5); saved for every level and session",
		},
		"aim_assist": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.BoolVal(platform.AimAssist), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("aim_assist", "bool", v)
				}
				platform.AimAssist = b
				platform.ControlSettingsChanged = true
				return nil
			},
			Doc: "whether the look stick slows over an enemy and eases onto one — a pad player only, never the mouse (on by default); saved for every level and session",
		},
		"input_device": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				if platform.LastDevice == platform.DevicePad {
					return flang.StrVal("gamepad"), true
				}
				return flang.StrVal("keyboard"), true
			},
			Doc: "what the player last played with: \"keyboard\" (and mouse) or \"gamepad\" — for showing the right binding, with binding_label",
		},
		"gamepad_type": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.StrVal(platform.ActivePadFamily().String()), true
			},
			Doc: "whose buttons the pad last used wears: \"xbox\", \"playstation\" or \"nintendo\" (anything else reads as xbox), or \"\" with none plugged in",
		},
		"gamepad_connected": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.BoolVal(platform.PadConnected()), true
			},
			Doc: "whether any game controller is plugged in",
		},

		// The player's volume, the same three gains the editor's audio settings
		// write. Player settings like the mouse ones above: saved to audio.yaml
		// once a change has settled (app/controls_settings.go).
		"master_volume": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(audio.MasterGain()), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("master_volume", "float", v)
				}
				audio.SetMasterGain(float32(f))
				audio.SettingsChanged = true
				return nil
			},
			Doc: "the player's overall volume, 0 (silent) to 1 (full), multiplying music and effects both; saved for every level and session",
		},
		"music_volume": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(audio.MusicGain()), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("music_volume", "float", v)
				}
				audio.SetMusicGain(float32(f))
				audio.SettingsChanged = true
				return nil
			},
			Doc: "the player's music volume, 0 to 1, under master_volume; saved for every level and session",
		},
		"effects_volume": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(audio.EffectsGain()), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("effects_volume", "float", v)
				}
				audio.SetEffectsGain(float32(f))
				audio.SettingsChanged = true
				return nil
			},
			Doc: "the player's sound-effects volume, 0 to 1, under master_volume; saved for every level and session",
		},

		// The player's Smooth Lighting setting (gfx.SmoothLighting), a player
		// setting like the volumes above: saved to graphics.yaml once a change
		// has settled (app/graphics_settings.go).
		"smooth_lighting": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.BoolVal(gfx.SmoothLighting), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("smooth_lighting", "bool", v)
				}
				gfx.SetSmoothLighting(b)
				return nil
			},
			Doc: "whether every surface shades smooth — lighting blends across the edges between a surface's faces, up to each face's crease angle — rather than only the ones marked Smooth; on by default, saved for every level and session",
		},

		// The player's foliage settings (gfx.FoliageDistance,
		// gfx.FoliageShadows), saved to graphics.yaml like smooth_lighting.
		"foliage_distance": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(float64(gfx.FoliageDistance)), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("foliage_distance", "float", v)
				}
				gfx.SetFoliageDistance(float32(f))
				return nil
			},
			Doc: "how far painted terrain foliage is drawn, as a multiple of its natural distances, 0.25 to 2 (1 by default); lower is faster; saved for every level and session",
		},
		// The player's Nanite LOD threshold (gfx.NaniteLODError), saved to
		// graphics.yaml like smooth_lighting.
		"nanite_lod_error": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(float64(gfx.NaniteLODError)), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("nanite_lod_error", "float", v)
				}
				gfx.SetNaniteLODError(float32(f))
				return nil
			},
			Doc: "how many pixels a cooked Nanite mesh's simplified detail may stray from the full detail before finer clusters are drawn, 0.25 to 8 (1 by default); higher is faster and coarser; saved for every level and session",
		},
		// The Nanite page pool's budget (gfx.NaniteStreamPoolMB), saved to
		// graphics.yaml like smooth_lighting; the next level load's pool
		// takes it.
		"nanite_stream_pool_mb": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.IntVal(int64(gfx.NaniteStreamPoolMB)), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("nanite_stream_pool_mb", "int", v)
				}
				gfx.SetNaniteStreamPoolMB(int(f))
				return nil
			},
			Doc: "the most video memory, in megabytes, cooked Nanite meshes' geometry may take, 8 to 8192 (256 by default); a level that needs more streams its detail in and out within it; taken at the next level load; saved for every level and session",
		},
		// The player's anti-aliasing mode and TAA sharpening
		// (gfx.AntiAliasMode, gfx.TAASharpen), saved to graphics.yaml like
		// smooth_lighting. Phase 4 of the Nanite plan (docs/nanite/PHASE_4.md).
		"anti_aliasing": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.StrVal(gfx.AntiAliasMode.String()), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				s, ok := v.(flang.StrVal)
				if !ok {
					return propErr("anti_aliasing", "string", v)
				}
				m, ok := gfx.ParseAAMode(string(s))
				if !ok {
					return propErr("anti_aliasing", "taa, msaa or off", v)
				}
				gfx.SetAntiAliasMode(m)
				return nil
			},
			Doc: "the player's anti-aliasing: \"taa\" (temporal: the default, and the mode cooked meshes and compiled levels shade once per pixel in), \"msaa\" (multisampling, the fallback; a game made without it by a TAA start takes it at the next start) or \"off\"; saved for every level and session",
		},
		"taa_sharpen": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(float64(gfx.TAASharpen)), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("taa_sharpen", "float", v)
				}
				gfx.SetTAASharpen(float32(f))
				return nil
			},
			Doc: "how much temporal anti-aliasing's output is sharpened, 0 to 1 (0.25 by default); saved for every level and session",
		},
		// The player's live GI tier (gfx.GIQuality), saved to graphics.yaml
		// like smooth_lighting.
		"gi_quality": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.StrVal(gfx.GIQuality.String()), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				s, ok := v.(flang.StrVal)
				if !ok {
					return propErr("gi_quality", "string", v)
				}
				q, ok := gfx.ParseGIQuality(string(s))
				if !ok {
					return propErr("gi_quality", "off, low, medium or high", v)
				}
				gfx.SetGIQuality(q)
				return nil
			},
			Doc: "the player's live global illumination quality: \"off\", \"low\", \"medium\" or \"high\" — bounce light worked out every frame from the lights as they are; off is the flat ambient and the baked bounce; picked by GPU until set, then saved for every level and session",
		},
		// The player's texture memory settings (gfx.TextureCompression,
		// gfx.TextureSkipMips), saved to graphics.yaml like smooth_lighting.
		// Read as each texture loads, so they take full effect at the next
		// start (gfx/texture_compress_settings.go).
		"texture_compression": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.BoolVal(gfx.TextureCompression), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("texture_compression", "bool", v)
				}
				gfx.SetTextureCompression(b)
				return nil
			},
			Doc: "whether large world textures (256 px and up) are stored block-compressed on the GPU — BC7 for colour, BC5 for normal maps — at a quarter of the video memory; off by default; textures already loaded keep their form, so it takes full effect at the next start; saved for every level and session",
		},
		"texture_skip_mips": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.IntVal(int64(gfx.TextureSkipMips)), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := numericValue(v)
				if !ok {
					return propErr("texture_skip_mips", "int", v)
				}
				gfx.SetTextureSkipMips(int(f))
				return nil
			},
			Doc: "texture quality, 0 to 3 (0 by default): how many times textures 512 px and up are halved before they reach the GPU (never below 256 px), each step taking about three quarters off their video memory; takes full effect at the next start; saved for every level and session",
		},
		"foliage_shadows": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.BoolVal(gfx.FoliageShadows), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("foliage_shadows", "bool", v)
				}
				gfx.SetFoliageShadows(b)
				return nil
			},
			Doc: "whether terrain foliage's trees, shrubs, rocks and ferns cast shadows; on by default, saved for every level and session",
		},

		// The hard pause — see script_pause.go. Engine state rather than a
		// level field, like the two switches above, and ScriptEventsReset
		// lifts it, so neither a level load nor the end of a play session
		// can carry a pause out with it.
		"paused": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.BoolVal(GamePaused()), true
			},
			Set: func(_ *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("paused", "bool", v)
				}
				scriptEvents.paused = b
				return nil
			},
			Doc: "whether the game is paused: the world stands still and the level's scripts get no :tick:, events or timers. Screens and widgets keep running, so a menu can lift it",
		},

		"sky": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil || c.lvl == nil {
					return nil, false
				}
				return flang.BoolVal(c.lvl.Sky != nil), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("sky", "bool", v)
				}
				if c == nil || c.lvl == nil {
					return nil
				}
				if b {
					if c.lvl.Sky == nil {
						sk := world.DefaultSky()
						c.lvl.Sky = &sk
					}
				} else {
					// Same gesture as the editor's own checkbox: gone, not a
					// switched-off block the next save would carry around. A
					// sky has no field of its own for "off" the way VolumeFog
					// has Enabled — existing IS the on/off switch.
					c.lvl.Sky = nil
				}
				return nil
			},
			Doc: "whether the level has a sky at all; off clears it, on restores the defaults if it had none",
		},
		"sky_kind": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil {
					return nil, false
				}
				return flang.AtomVal(world.SkyKindName(skyOf(c.lvl).Kind)), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("sky_kind", "str or atom", v)
				}
				if c == nil || c.lvl == nil {
					return nil
				}
				skySet(c.lvl, func(sk *world.Sky) { sk.Kind = world.SkyKindFromName(s) })
				return nil
			},
			Doc: "gradient or cubemap; a cubemap with any face unset falls back to the gradient rather than five faces and a hole",
		},
		"sky_cubemap": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil {
					return nil, false
				}
				return flang.StrVal(skyCubemapBase(skyOf(c.lvl).Faces)), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("sky_cubemap", "str", v)
				}
				if c == nil || c.lvl == nil {
					return nil
				}
				// The same shortcut the editor's "Fill from base name" button
				// is: "dusk" fills all six slots as dusk_px, dusk_nx, ... and
				// switches the kind to cubemap. Writing "" clears them without
				// switching the kind back, matching SetFacesFromBase itself.
				skySet(c.lvl, func(sk *world.Sky) {
					sk.SetFacesFromBase(s)
					if s != "" {
						sk.Kind = world.SkyCubemap
					}
				})
				return nil
			},
			Doc: "sets all six cubemap faces from one base name (\"dusk\" -> dusk_px, dusk_nx, ...) and switches kind to cubemap; \"\" clears them",
		},
		"sky_zenith": skyColorProp("sky_zenith", func(sk *world.Sky) *mgl32.Vec3 { return &sk.Zenith },
			"the gradient's straight-up stop"),
		"sky_horizon": skyColorProp("sky_horizon", func(sk *world.Sky) *mgl32.Vec3 { return &sk.Horizon },
			"the gradient's stop at the ring around the viewer"),
		"sky_ground": skyColorProp("sky_ground", func(sk *world.Sky) *mgl32.Vec3 { return &sk.Ground },
			"the gradient's straight-down stop, seen where the level has no floor under the camera"),
		"sky_tint": skyColorProp("sky_tint", func(sk *world.Sky) *mgl32.Vec3 { return &sk.Tint },
			"multiplies whichever mode produced the colour, gradient or cubemap alike"),
		"sky_intensity": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil {
					return nil, false
				}
				return flang.FloatVal(float64(skyOf(c.lvl).Intensity)), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("sky_intensity", "number", v)
				}
				if c == nil || c.lvl == nil {
					return nil
				}
				skySet(c.lvl, func(sk *world.Sky) { sk.Intensity = f })
				return nil
			},
			Doc: "brightness, on top of sky_tint",
		},
		"sky_rotation": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil {
					return nil, false
				}
				return flang.FloatVal(float64(skyOf(c.lvl).Rotation)), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("sky_rotation", "number", v)
				}
				if c == nil || c.lvl == nil {
					return nil
				}
				skySet(c.lvl, func(sk *world.Sky) { sk.Rotation = f })
				return nil
			},
			Doc: "degrees about +Y; lines a cubemap's art up with the level's own sun without repainting it",
		},
		"sky_clouds": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil {
					return nil, false
				}
				on := c.lvl != nil && c.lvl.Sky != nil && c.lvl.Sky.Clouds != nil && c.lvl.Sky.Clouds.Enabled
				return flang.BoolVal(on), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("sky_clouds", "bool", v)
				}
				if c == nil {
					return nil
				}
				skyCloudsSet(c.lvl, b, func(d *world.SkyClouds) { d.Enabled = b })
				return nil
			},
			Doc: "whether the sky has its volumetric cloud deck; off keeps the numbers, as fog's switch does",
		},
		"sky_cloud_coverage": skyCloudNumberProp("sky_cloud_coverage",
			func(d *world.SkyClouds) *float32 { return &d.Coverage },
			"how much of the deck is cloud, 0 (clear) to 1 (overcast)"),
		"sky_cloud_density": skyCloudNumberProp("sky_cloud_density",
			func(d *world.SkyClouds) *float32 { return &d.Density },
			"the deck's thickness per world unit at its densest"),
		"sky_cloud_altitude": skyCloudNumberProp("sky_cloud_altitude",
			func(d *world.SkyClouds) *float32 { return &d.Altitude },
			"the height of the deck's base, in world units"),
		"sky_cloud_thickness": skyCloudNumberProp("sky_cloud_thickness",
			func(d *world.SkyClouds) *float32 { return &d.Thickness },
			"how deep the deck is, in world units"),
		"sky_cloud_range": skyCloudNumberProp("sky_cloud_range",
			func(d *world.SkyClouds) *float32 { return &d.Range },
			"how far from the eye the deck is drawn before it fades into the horizon"),
		"sky_cloud_scale": skyCloudNumberProp("sky_cloud_scale",
			func(d *world.SkyClouds) *float32 { return &d.Scale },
			"world units across the deck's largest clouds"),
		"sky_cloud_erosion": skyCloudNumberProp("sky_cloud_erosion",
			func(d *world.SkyClouds) *float32 { return &d.Erosion },
			"how ragged the clouds' edges are, 0 to 1"),
		"sky_cloud_color": skyCloudVecProp("sky_cloud_color",
			func(d *world.SkyClouds) *mgl32.Vec3 { return &d.Color },
			"the clouds' colour as a vec of r, g, b"),
		"sky_cloud_wind": skyCloudVecProp("sky_cloud_wind",
			func(d *world.SkyClouds) *mgl32.Vec3 { return &d.Wind },
			"how fast the clouds drift, in world units per second"),
		"sky_cloud_shadows": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil {
					return nil, false
				}
				return flang.BoolVal(skyCloudsOf(c.lvl).CastShadows), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("sky_cloud_shadows", "bool", v)
				}
				if c == nil {
					return nil
				}
				skyCloudsSet(c.lvl, true, func(d *world.SkyClouds) { d.CastShadows = b })
				return nil
			},
			Doc: "whether the deck casts its shadow on the level",
		},
	}
	// The camera and the pointer: script_props_camera.go.
	for k, v := range cameraLevelProps() {
		m[k] = v
	}
	return m
}

// ---------- the sky's cloud deck ----------
//
// The sky helpers' shape one level down: reading a deck that is not there
// returns the defaults, writing any sky_cloud_* property gives the level a sky
// and the sky a deck, and switching the deck off where there was none leaves
// the level as it found it. Every write goes through skySet, so it clamps, and
// so an ease_sky armed before it blends it.

// skyCloudsOf is the level's deck, or a default one to read — a COPY.
func skyCloudsOf(lvl *world.Level) world.SkyClouds {
	if lvl == nil || lvl.Sky == nil || lvl.Sky.Clouds == nil {
		return world.DefaultSkyClouds()
	}
	return *lvl.Sky.Clouds
}

// skyCloudsSet applies fn to the level's deck, creating the sky and the deck at
// their defaults first unless create is false.
func skyCloudsSet(lvl *world.Level, create bool, fn func(*world.SkyClouds)) {
	if lvl == nil {
		return
	}
	if !create && (lvl.Sky == nil || lvl.Sky.Clouds == nil) {
		return
	}
	skySet(lvl, func(sk *world.Sky) {
		if sk.Clouds == nil {
			d := world.DefaultSkyClouds()
			sk.Clouds = &d
		}
		fn(sk.Clouds)
	})
}

// skyCloudNumberProp builds one number-valued sky_cloud_* property.
func skyCloudNumberProp(name string, field func(*world.SkyClouds) *float32, doc string) propAccess {
	return propAccess{
		Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
			if c == nil {
				return nil, false
			}
			d := skyCloudsOf(c.lvl)
			return flang.FloatVal(float64(*field(&d))), true
		},
		Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
			f, ok := toFloat32(v)
			if !ok {
				return propErr(name, "number", v)
			}
			if c == nil {
				return nil
			}
			skyCloudsSet(c.lvl, true, func(d *world.SkyClouds) { *field(d) = f })
			return nil
		},
		Doc: doc,
	}
}

// skyCloudVecProp builds one vec-valued sky_cloud_* property.
func skyCloudVecProp(name string, field func(*world.SkyClouds) *mgl32.Vec3, doc string) propAccess {
	return propAccess{
		Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
			if c == nil {
				return nil, false
			}
			d := skyCloudsOf(c.lvl)
			return vecValue(*field(&d)), true
		},
		Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
			p, ok := toVec3(v)
			if !ok {
				return propErr(name, "vec", v)
			}
			if c == nil {
				return nil
			}
			skyCloudsSet(c.lvl, true, func(d *world.SkyClouds) { *field(d) = p })
			return nil
		},
		Doc: doc,
	}
}

// skyColorProp builds one of the four vec3 sky properties above. field picks
// out which one of a *world.Sky it reads and writes; the property table entry
// otherwise looks the same for all four, so it is built once here rather than
// four times with the one line that differs buried in the middle of each.
func skyColorProp(name string, field func(*world.Sky) *mgl32.Vec3, doc string) propAccess {
	return propAccess{
		Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
			if c == nil {
				return nil, false
			}
			sk := skyOf(c.lvl)
			return vecValue(*field(&sk)), true
		},
		Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
			p, ok := toVec3(v)
			if !ok {
				return propErr(name, "vec", v)
			}
			if c == nil || c.lvl == nil {
				return nil
			}
			skySet(c.lvl, func(sk *world.Sky) { *field(sk) = p })
			return nil
		},
		Doc: doc,
	}
}

// ---------- level sky property helpers ----------
//
// A level's sky is a pointer, and a level authored before skies existed — or
// one that simply does not want one — has none. Reading one of those is a
// fair question with a dull answer, so it returns the defaults rather than
// erroring, the same shape volumeFogOf gives a plain trigger's fog.

// skyOf is the level's sky, or a default one to read fields off. The returned
// value is a COPY, so nothing written through it reaches the level by
// accident.
func skyOf(lvl *world.Level) world.Sky {
	if lvl == nil || lvl.Sky == nil {
		return world.DefaultSky()
	}
	return *lvl.Sky
}

// skySet applies fn to the level's sky, creating one at the defaults first if
// there was none — see volumeFogSet's note on why a write should not have to
// be preceded by switching the feature on: `@level.prop.sky_tint = ...` on a
// level with no sky yet has exactly one sensible reading, the same as
// `@cellar.prop.fog_density = ...` on a volume with no fog block.
func skySet(lvl *world.Level, fn func(*world.Sky)) {
	if lvl == nil {
		return
	}
	if lvl.Sky == nil {
		sk := world.DefaultSky()
		lvl.Sky = &sk
	}
	fn(lvl.Sky)
	lvl.Sky.Clamp()
}

// skyCubemapBase reconstructs the base name SetFacesFromBase would have taken
// to produce these six faces, or "" when they do not agree with one — hand
// picked one face at a time, say, or never set at all. Best-effort rather than
// stored, because the base name is a convenience SetFacesFromBase offers and
// not something a Sky actually keeps; what it keeps is six names.
func skyCubemapBase(faces [world.SkyFaces]string) string {
	suffix := "_" + world.SkyFaceSuffix[0]
	if faces[0] == "" || !strings.HasSuffix(faces[0], suffix) {
		return ""
	}
	base := strings.TrimSuffix(faces[0], suffix)
	var probe world.Sky
	probe.SetFacesFromBase(base)
	if probe.Faces == faces {
		return base
	}
	return ""
}

// The range @level.prop.mouse_sensitivity accepts.
const (
	MinMouseSensitivity = float32(0.1)
	MaxMouseSensitivity = float32(5)
)

func clampFloat32(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
