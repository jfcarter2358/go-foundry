package play

// Package play is the game being played by something that is not a person: a
// short script of moves ("look 90, walk forward two seconds, fire") run against
// a level that is already ticking.
//
// It exists because two very different things want to do the same thing. The
// headless session (`foundry play`, internal/harness) plays a level with no
// window at all, and the editor's own Play mode plays one in the window the
// author is watching. Both had to be able to walk, look, shoot and press —
// and neither can do any of it by faking a keyboard, because a scripted move
// is a direction and a duration rather than a key that is down.
//
// WHAT IS HERE is the vocabulary and the driving: the commands, the parser,
// the runner that spends them over ticks, and the controls they leave held.
// Every verb reaches for the engine's own front door — hooks.Script*Request
// for the actions a key would raise, gfx.Cam for the view, sim.Input for the
// move — so a scripted shot is fired by the same code a clicked one is.
//
// WHAT IS NOT is the tick itself and the picture: whoever owns the frame runs
// it and takes the screenshot. That is Target, which is the two lines of
// difference between a headless run and the editor's window.

import (
	"fmt"
	"math"

	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/platform"
	"foundry/internal/script"
	"foundry/internal/sim"
	"foundry/internal/ui"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// Target is the frame the script is played in: the level it is playing, a log
// to say what it did, and the two things only the frame's owner can do — read
// a watch path and take a picture.
type Target interface {
	// Level is the level being played. It is read every time rather than held,
	// because a `load_level` replaces it under the runner.
	Level() *world.Level
	// Emit records one thing the script did, in the harness's Event shape:
	// a short kind ("LOOK", "FIRE", "SHOT"), a name and a value.
	Emit(kind, name, value string)
	// Read answers a watch path (harness/read.go's syntax), for `read`.
	Read(path string) string
	// Shot takes a picture and answers where it was written. A target with no
	// way to draw returns an error, which is a note in the log rather than the
	// end of the run: a script that walks somewhere and photographs it is
	// still worth running for the walking.
	Shot(name string) (string, error)
}

// Controls are what the script is holding down right now.
//
// They are spent through sim.Input rather than through a key table, which is
// the whole reason this type exists: a keyboard can ask for eight directions
// and a script asks for one vector.
type Controls struct {
	// Forward and Strafe are -1..1 in the player's own view: +Forward is where
	// the camera looks, +Strafe is to its left (which is the sign the engine's
	// own right vector carries — see gfx.moveIntent).
	Forward, Strafe float32
	// Crouch and Sprint are held; Jump is a press, spent by the next tick.
	Crouch, Sprint bool
	Jump           bool
}

// Input turns the controls into the frame's input, exactly as a held key would
// arrive.
//
// The basis is gfx.moveIntent's, down to the un-normalised forward vector: the
// engine's own walk slows as the view pitches down, and a scripted walk that
// quietly did not would cover ground no player could.
func (c *Controls) Input(dt float64) sim.Input {
	in := sim.Input{Crouch: c.Crouch, Sprint: c.Sprint, Jump: c.Jump}
	c.Jump = false
	if c.Forward == 0 && c.Strafe == 0 {
		return in
	}
	dir := gfx.DirectionFromAngles(gfx.Cam.Yaw, gfx.Cam.Pitch)
	forward := mgl32.Vec3{dir.X(), 0, dir.Z()}
	right := mgl32.Vec3{dir.Z(), 0, -dir.X()}
	// The engine measures a key's movement per 1/60 s and scales it by the
	// frame — MovementSpeed is a distance, not a speed. A script's tick has
	// to be scaled the same way or it walks at the tick rate.
	speed := platform.MovementSpeed * float32(dt*60)
	in.Intent = forward.Mul(c.Forward * speed).Add(right.Mul(c.Strafe * speed))
	return in
}

// Stop releases the movement. The held actions (crouch, sprint) are states a
// player leaves set and are deliberately not touched.
func (c *Controls) Stop() { c.Forward, c.Strafe = 0, 0 }

// ---------------------------------------------------------------- the view

// SetLook points the view, in degrees, and clamps the pitch where the engine
// clamps it. Yaw is the engine's: 0 looks down +X, 90 down +Z.
func SetLook(yawDeg, pitchDeg float64) {
	gfx.Cam.Yaw = float32(yawDeg * math.Pi / 180)
	gfx.Cam.Pitch = float32(pitchDeg * math.Pi / 180)
	if gfx.Cam.Pitch > gfx.MaxPitch {
		gfx.Cam.Pitch = gfx.MaxPitch
	}
	if gfx.Cam.Pitch < -gfx.MaxPitch {
		gfx.Cam.Pitch = -gfx.MaxPitch
	}
	gfx.Cam.Direction = gfx.DirectionFromAngles(gfx.Cam.Yaw, gfx.Cam.Pitch)
	gfx.UpdateCamMatrix()
}

// Look is where the view points now, in degrees.
func Look() (yawDeg, pitchDeg float64) {
	return float64(gfx.Cam.Yaw) * 180 / math.Pi, float64(gfx.Cam.Pitch) * 180 / math.Pi
}

// LookAt turns the view towards a point in the world, from the eye.
func LookAt(eye, at mgl32.Vec3) {
	d := at.Sub(eye)
	flat := float64(mgl32.Vec2{d.X(), d.Z()}.Len())
	if flat < 1e-4 && math.Abs(float64(d.Y())) < 1e-4 {
		return
	}
	yaw := math.Atan2(float64(d.Z()), float64(d.X()))
	pitch := math.Atan2(float64(d.Y()), flat)
	SetLook(yaw*180/math.Pi, pitch*180/math.Pi)
}

// Eye is where the player is looking from: their position in the world, at eye
// height. The camera is the same point once a frame has placed it, but a
// command that runs before this tick's sim.Step would be reading the last
// frame's — which, on the tick a teleport lands, is the wrong room.
func Eye(lvl *world.Level) mgl32.Vec3 {
	if world.PlayerEntity == nil {
		return gfx.Cam.Position
	}
	return lvl.Frames.WorldPosition(world.PlayerEntity).Add(mgl32.Vec3{0, world.PlayerEyeHeight(), 0})
}

// ---------------------------------------------------------------- actions

// The actions a key or a mouse button raises. Each one is the hook app/input.go
// calls for that binding and nothing else, so a scripted press and a played one
// are indistinguishable by the time a script sees them.
func Interact()              { hooks.ScriptInteractRequest() }
func PrimaryFire(down bool)  { hooks.ScriptPrimaryFireRequest(down) }
func SecondaryFire(aim bool) { hooks.ScriptSecondaryFireRequest(aim) }
func Reload()                { hooks.ScriptReloadRequest() }
func Throw()                 { hooks.ScriptThrowRequest() }
func WeaponSlot(n int)       { hooks.ScriptWeaponSlotRequest(n) }
func CycleWeapon(step int)   { hooks.ScriptWeaponCycleRequest(step) }

// PointAt frees the pointer and holds it at a window pixel, the way a mouse
// resting there would — what the world's pointer events and `pick` read. It
// stays there until ReleasePointer, whatever the real mouse does.
func PointAt(x, y float32) {
	hooks.PinCursor(hooks.CursorState{X: x, Y: y, Free: true, OverUI: ui.Game.PointerOver(x, y)})
}

// ReleasePointer gives the pointer back to the mouse.
func ReleasePointer() { hooks.UnpinCursor() }

// MouseButton presses or lets go of a mouse button on the world where the
// pointer is. It is refused while the cursor is captured: a button pressed
// then is the fire action, which `fire` and `aim` already are.
func MouseButton(b hooks.PointerButton, down bool) error {
	c := hooks.Cursor()
	if !c.Free {
		return fmt.Errorf("the cursor is captured; free it with `cursor x,y` first")
	}
	kind := hooks.PointerUp
	if down {
		kind = hooks.PointerDown
	}
	hooks.RecordPointer(hooks.PointerEvent{Kind: kind, Button: b, X: c.X, Y: c.Y})
	return nil
}

// Click presses a widget on a screen that is up, as a mouse click on it would.
// With no screen named it takes the topmost screen that has such a widget,
// which is what "click continue" means when a menu is over the HUD.
func Click(screen, widget string) error {
	if screen == "" {
		s, err := screenWithWidget(widget)
		if err != nil {
			return err
		}
		screen = s
	}
	hooks.RecordUIEvent(hooks.UIEvent{Atom: "click", Screen: screen, Widget: widget})
	return nil
}

// Teleport puts the player somewhere, off whatever they were riding — the point
// is in the world, and a rider's position is in their frame's own coordinates.
func Teleport(p mgl32.Vec3) {
	if world.PlayerEntity == nil {
		return
	}
	world.PlayerEntity.Frame, world.PlayerEntity.Seat = "", ""
	world.PlayerEntity.Position = p
}

// Enable switches a chunk or a volume on, standing in for the parts of a level
// a short script does not play through: level1's streaming cores are off until
// a descent twenty minutes in arms them.
func Enable(lvl *world.Level, id string) bool {
	found := false
	for i := range lvl.Chunks {
		if lvl.Chunks[i].ID == id {
			lvl.Chunks[i].Disabled, found = false, true
		}
	}
	for i := range lvl.Volumes {
		if lvl.Volumes[i].ID == id {
			lvl.Volumes[i].Enabled, found = true, true
		}
	}
	return found
}

// Wake switches on the AI controller of, and shows, every entity with a tag —
// what a level's zone scripts do when the player comes near.
func Wake(lvl *world.Level, tag string) int {
	n := 0
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if e.HasTag(tag) && e.AI != nil {
			e.AI.Enabled, e.Visible = true, true
			n++
		}
	}
	return n
}

// Kill sends (:damage:, @player, 9999) to every scripted entity with a tag —
// what the player's gun would have done, without the aiming.
func Kill(lvl *world.Level, tag string) int {
	n := 0
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if !e.HasTag(tag) || e.Script == "" {
			continue
		}
		player := flang.RefVal{Kind: flang.RefEntity, A: "player"}
		if err := script.PostEntityEvent(e.ID, "damage", player, flang.IntVal(9999)); err != nil {
			continue
		}
		n++
	}
	return n
}

// ---------------------------------------------------------------- lookups

// Point resolves what a command was aimed at: a literal x,y,z, an entity by id,
// or #tag for the nearest entity carrying it. An entity answers at the height
// a shot would be aimed at rather than at its feet — see world.HitHeight.
func Point(lvl *world.Level, at string, from mgl32.Vec3) (mgl32.Vec3, error) {
	if v, ok := parseVec(at); ok {
		return v, nil
	}
	if len(at) > 1 && at[0] == '#' {
		e := nearestTagged(lvl, at[1:], from)
		if e == nil {
			return mgl32.Vec3{}, fmt.Errorf("no entity tagged %s", at)
		}
		return aimPoint(lvl, e), nil
	}
	if e := world.FindEntity(lvl, at); e != nil {
		return aimPoint(lvl, e), nil
	}
	return mgl32.Vec3{}, fmt.Errorf("%q is not x,y,z, an entity id or a #tag", at)
}

// aimPoint is where a shot at an entity is aimed: three quarters of the way up
// the body it is presenting, which is the AI's own chest point (ai.chestOf) and
// so the height the level's own soldiers shoot each other at. A crouched body
// ducks it, because HitScale is what it ducks.
func aimPoint(lvl *world.Level, e *world.Entity) mgl32.Vec3 {
	c := lvl.Frames.WorldBoundCenter(e)
	if e.HasBounds() {
		h := e.BoundHalfExtents().Y()
		c[1] += -h + 2*h*e.HitScale()*0.75
	}
	return c
}

func nearestTagged(lvl *world.Level, tag string, from mgl32.Vec3) *world.Entity {
	var best *world.Entity
	bestD := float32(math.MaxFloat32)
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if !e.HasTag(tag) {
			continue
		}
		if d := lvl.Frames.WorldPosition(e).Sub(from).Len(); d < bestD {
			best, bestD = e, d
		}
	}
	return best
}

// screenWithWidget finds the topmost screen carrying a widget, so `click ok`
// means the OK on the menu that is in front rather than one on the HUD behind
// it.
func screenWithWidget(widget string) (string, error) {
	screens := ui.Game.Screens()
	for i := len(screens) - 1; i >= 0; i-- {
		if screens[i].Root.Find(widget) != nil {
			return screens[i].ID, nil
		}
	}
	return "", fmt.Errorf("no screen that is up has a widget %q", widget)
}
