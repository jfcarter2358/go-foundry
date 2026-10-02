package sim

// Package sim is one frame of the world, without the drawing.
//
// It used to live inline in app.Run, which was fine for as long as the app was
// the only thing that ran a frame. It is not: cmd/levelsim (and the harness
// behind it) runs the same world headlessly, and every such tool that copied
// the loop out of app.go drifted from it — different order, missing passes, and
// in one case a pass run twice, which made every `move_to` in a headless run
// go at double speed. One copy, two callers.
//
// WHAT IS HERE is everything that moves the world: the chunks and brushes that
// carry a velocity, the vehicles, the player's own move, the AI controllers and
// the bodies they steer, the frames, the waypoint followers, the particles, the
// effects, the sound, the sky, the UI's own tick and the script tick.
//
// WHAT IS NOT is the input the frame is driven with (mouse look, recoil, the
// keys themselves), the level load, and every part of drawing. Those stay in
// internal/app, which is the only thing that has a window.
//
// It is safe with no GL context: the two calls in here that would touch one —
// gfx.UpdateCamMatrix and world.UploadLightsTo — each answer "nothing to
// upload" when there is no program, and the profiler is off unless asked for.

import (
	"foundry/internal/ai"
	"foundry/internal/collision"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/platform"
	"foundry/internal/render"
	"foundry/internal/script"
	"foundry/internal/ui"
	"foundry/internal/world"
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

// Gates say which halves of the frame run. They are the conditions app.Run used
// to spell out inline at each call: the editor being open, a pausing screen, a
// script's hard pause, a level load in flight.
type Gates struct {
	// Dev is the editor being open and owning the camera and the input. It
	// stops motion without stopping clips, so an entity cannot drift away from
	// where it was just placed while it is being placed.
	Dev bool
	// GamePaused is a pausing screen, a hard pause or a level load: the
	// player, the vehicles and the entities freeze. Scripts do not — the
	// menu's own handlers have to run.
	GamePaused bool
	// ScriptPaused is `@level.prop.paused`, which stops the entities' clips as
	// well. Implies GamePaused.
	ScriptPaused bool
	// LoadPending holds the script tick back while a level is being built:
	// half the world belongs to the outgoing level and half to the incoming
	// one.
	LoadPending bool
	// ViewW and ViewH are the framebuffer the UI solves its layout against. A
	// headless run can pass whatever resolution it wants to measure.
	ViewW, ViewH float32
}

// Play reports whether the world is actually being played: not in the editor,
// not paused. It is the gate most of the passes below take.
func (g Gates) Play() bool { return !g.Dev && !g.GamePaused }

// Input is what drives the player this frame.
//
// Keys is the real keyboard, and when it is set the frame reads the intent, the
// crouch, the sprint and the jump from it exactly where app.Run always did —
// which matters for more than tidiness: reading the intent is also what fills a
// driven vehicle's controls, and that has to happen before the vehicles
// integrate.
//
// With no Keys the fields below it drive instead, which is how a headless run
// walks a player without a keyboard to fake.
type Input struct {
	Keys   []uint8
	Intent mgl32.Vec3
	Jump   bool
	Crouch bool
	Sprint bool
}

// intent is the movement the player asks for, from whichever source is driving.
func (in Input) intent(lvl *world.Level) mgl32.Vec3 {
	if in.Keys != nil {
		return PlayerMoveIntent(lvl, in.Keys)
	}
	if !platform.PlayerControlEnabled {
		return mgl32.Vec3{}
	}
	return in.Intent
}

// held resolves the two held actions (crouch, sprint) and the jump press. A
// keyboard answers from the bindings; anything else answers from the fields.
func (in Input) held() (crouch, sprint, jump bool) {
	if in.Keys != nil {
		return platform.ActionHeld(platform.ActCrouch, in.Keys),
			platform.ActionHeld(platform.ActSprint, in.Keys) &&
				platform.ActionHeld(platform.ActMoveForward, in.Keys),
			platform.JumpRequested
	}
	return in.Crouch, in.Sprint, in.Jump
}

// Step advances the world by dt.
//
// The order is load-bearing and every reordering of it has been a bug. Each
// pass below says why it sits where it does; the short version is that things
// move, then the frames they are expressed in are republished, then everything
// that reads a position reads it.
func Step(lvl *world.Level, dt float32, g Gates, in Input) {
	render.ProfileBegin("player & vehicles")
	if g.Play() {
		// --- Input -> player -> camera ---
		// The keys say how far to move; the player is what moves, and the
		// camera is placed on it afterwards (down in RefreshFrames' slot). It
		// used to run the other way round — move the camera, then difference it
		// onto the player — which cannot survive the two living in different
		// coordinate systems.
		//
		// While the player is in a driver's seat these keys fill the vehicle's
		// controls instead of the player's move intent, which is why this is
		// read here rather than beside the move below.
		intent := in.intent(lvl)
		crouchHeld, sprintHeld, jump := in.held()

		// Chunks that carry a velocity move first, before anything resolves
		// against them: a lift, a barge, a closing wall. Doing it here means
		// the vehicle pass, the player's own move and the entity pass below all
		// see the geometry where it is this frame rather than where it was last
		// frame, and the re-bucketing it does is already paid for by the time a
		// location verb asks.
		//
		// Marked dirty for the same reason driving is: this writes the level's
		// own authored state without going through the script host, so leaving
		// play would otherwise leave every platform wherever it had drifted to.
		if world.AdvanceChunks(lvl, dt) {
			script.PlaySessionMarkDirty()
		}

		// Then the brushes that carry one: a single door or lift inside an
		// otherwise still chunk. Same tick, same ordering argument, same reason
		// for marking the session dirty — and after the chunks, so that a brush
		// moving inside a chunk that also moved is integrated against this
		// frame's chunk placement rather than last frame's.
		//
		// AdvanceBrushes steps both a dynamic brush's rates AND its tweens, so
		// `move_to` and `scale_to` on a brush are carried here.
		if world.AdvanceBrushes(lvl, dt) {
			script.PlaySessionMarkDirty()
		}

		// Vehicles integrate before anything inside them resolves, and
		// republish their frames as they go. A rider that resolved against last
		// frame's transform would be shoved through the deck every time the
		// ship pitched — this ordering is the fix, and it is why VehicleUpdate
		// is here rather than folded into EntityUpdate.
		//
		// Driving writes straight into the level's own entities rather than
		// going through the script host, so it never trips the runtime's
		// markDirty calls. Mark the play session dirty by hand whenever a
		// vehicle actually moved, or leaving play would leave it wherever it
		// was last driven instead of putting the level back.
		if collision.VehicleUpdate(lvl, dt, true) {
			script.PlaySessionMarkDirty()
		}

		if world.PlayerEntity != nil {
			// Crouch is held, not toggled, on foot only — a driver's Ctrl is
			// the vehicle's, and a seat has its own eye.
			crouch := crouchHeld && crouchable(lvl)
			collision.UpdatePlayerCrouch(world.PlayerEntity, lvl, crouch, dt)
			// Sprint is held too, on foot with gravity on and while pushing
			// forward — in free-fly the same key is "down" (gfx.MoveIntent).
			// Letting go of any of them ends it.
			world.UpdatePlayerSprint(sprintHeld && crouchable(lvl) && !platform.FlyMode)
			switch {
			case world.PlayerCrouched():
				intent[0] *= world.CrouchSpeedScale
				intent[2] *= world.CrouchSpeedScale
			case world.PlayerSprinting():
				intent[0] *= world.SprintSpeedScale
				intent[2] *= world.SprintSpeedScale
			}

			// The pull where the player is standing: the level's `gravity:`,
			// whatever volume overrides it there, and the player's own scale,
			// all resolved by world.GravityFor. Asked every frame rather than
			// cached, because every one of the three can change under the body
			// — a script writing the level's, a low-gravity room walked into,
			// or a script writing the player's own scale.
			move := collision.PlayerMove{
				Intent:       intent,
				Jump:         jump,
				Gravity:      platform.GravityEnabled,
				Fly:          platform.FlyMode,
				Grounded:     platform.IsOnGround,
				DT:           dt,
				GravityAccel: world.GravityFor(lvl, world.PlayerEntity),
				MaxFallSpeed: world.PlayerEntity.Collision.EffectiveMaxFallSpeed(),
				JumpVelocity: world.PlayerJumpVelocity,
			}
			moved := collision.MovePlayer(world.PlayerEntity, lvl, move)
			platform.JumpRequested = false
			// Leaving and touching the ground, for the player's script
			// (`:landed:`, `:left_ground:`). Recorded here because this is the
			// only place that knows both the transition and the speed it
			// happened at — MovePlayer zeroes the fall the instant it reports
			// it. The script tick drains it (script_events.go).
			hooks.RecordPlayerGround(moved.OnGround, moved.Landed, moved.Impact)

			// Standing in a frame portal's doorway hands the player over to the
			// other side — the ship's ramp onto the hangar deck, or back again.
			// Position, velocity and facing all go through the same relative
			// transform, so nothing pops and nobody arrives walking sideways.
			if c := collision.FindFramePortalCrossing(lvl, world.PlayerEntity); c != nil && !c.Sealed {
				gfx.Cam.Yaw = collision.CrossFramePortal(lvl, world.PlayerEntity, c, gfx.Cam.Yaw)
			}
			// Free-fly leaves it alone rather than reporting a fall that is not
			// happening. Gravity being off no longer does: a weightless body
			// still stands on things, and `:landed:` and the jump key both
			// depend on knowing that it does.
			if !platform.FlyMode {
				platform.IsOnGround = moved.OnGround
			}

			// The camera is NOT placed here. It goes on after RefreshFrames
			// below, once every mover this tick has finished and the frames a
			// rider's coordinates are expressed in are current.
		} else {
			// No player yet: the intent still has to go somewhere, or the
			// camera freezes on the loading frame.
			gfx.Cam.Position = gfx.Cam.Position.Add(intent)
			gfx.UpdateCamMatrix()
		}
	}
	render.ProfileEnd() // player & vehicles

	// --- Placed entities ---
	// Outside the play gate so clips keep playing while the editor is open;
	// motion is gated inside EntityUpdate so an entity cannot drift away from
	// where it was just placed.
	//
	// The player is handed in so it takes part in entity-against-entity
	// collision — a solid crate should be solid to walk into. Its ground flag
	// is cleared first and read back after, since being pushed up onto an
	// entity is the same as landing on a floor as far as jumping is concerned.
	if world.PlayerEntity != nil {
		world.PlayerEntity.Collision.OnGround = false
	}
	// A script's pause stops the clips as well. A pausing screen leaves them
	// playing behind it, as it always has; under the hard pause a looping clip
	// would otherwise bank an :anim_end: per cycle for the level's scripts to
	// receive all at once when the pause lifts.
	entityDT := dt
	if g.ScriptPaused {
		entityDT = 0
	}

	// AI controllers, immediately before the pass that moves what they steer. A
	// controller only writes an entity's velocity (and its facing); gravity,
	// walls, pushes and stairs are EntityUpdate's, so an agent goes through
	// exactly the resolve every other body does, this frame rather than next.
	// Gameplay only, and stopped by any pause — a guard that kept deciding
	// behind a menu would be somewhere else when it closed.
	//
	// It writes the level's own entities without going through the script host,
	// so the play session is marked dirty by hand, as driving is.
	if g.Play() {
		render.ProfileBegin("ai")
		// Where the player aims, so a fighter being drawn a bead on can dive
		// out of the way (internal/ai/fight.go).
		if world.PlayerEntity != nil && platform.PlayerControlEnabled {
			ai.SetPlayerAim(gfx.Cam.Position, gfx.Cam.Direction)
		} else {
			ai.ClearPlayerAim()
		}
		if ai.Update(lvl, dt, world.PlayerEntity) {
			script.PlaySessionMarkDirty()
		}
		render.ProfileEnd()
	}
	render.ProfileBegin("entity update")
	collision.EntityUpdate(lvl, entityDT, g.Play(), world.PlayerEntity)
	reportSceneryCollision()
	render.ProfileEnd()
	// The player's own viewmodel is not in level.Entities (see entity.go), so
	// EntityUpdate's walk never reaches it. Ticked on the same entityDT —
	// including the hard-pause zeroing — so a paused clip's clock stops the
	// same way a placed entity's does.
	if world.PlayerEntity != nil {
		world.PlayerEntity.UpdateAnimations(entityDT)
	}
	if !g.Dev && world.PlayerEntity != nil && world.PlayerEntity.Collision.OnGround {
		platform.IsOnGround = true
	}

	// Frames follow whatever carries them, so their transforms are recomputed
	// once the entities that own them have finished moving — and before
	// anything reads them. Everything downstream this frame (the script tick's
	// world-space overlap tests, and every draw of a chunk that lives in a
	// frame) sees where things actually are rather than where they were last
	// frame.
	//
	// Walked parents-first inside RefreshFrames, so a nested frame composes
	// against a parent that is already up to date.
	render.ProfileBegin("frames & waypoints")
	world.RefreshFrames(lvl, lvl.Frames)

	// The camera, now that the frames are current. This is the whole reason it
	// is not placed up beside the move: a rider stores its position in a
	// frame's coordinates, and reading them against last tick's transform puts
	// the eye wherever the ship was rather than where it is — while the hold
	// around it draws from the transform just published here.
	//
	// It also fixes up being shoved out of a solid entity by EntityUpdate.
	if g.Play() {
		PlacePlayerCamera(lvl)
		// A script camera takes the view straight back, so that everything
		// between here and the end of the frame — the AI reading the
		// player's aim, the listener, the script tick's picks and casts —
		// sees the camera the picture is taken from. See gfx.HoldCameraRig.
		gfx.HoldCameraRig()

		// The rest of what a waypoint animation can carry — sprites, emitters,
		// sounds, lights and volumes. Brushes and entities went on their own
		// kinds' ticks above. Here, after RefreshFrames, so one attached to
		// something that moved this frame is divided through where its carrier
		// now is; and before the emitters and the mixer below read where they
		// are.
		//
		// Lights are uniforms rather than something drawn where it stands, so a
		// light on a path is uploaded again. Quietly: the verbose upload logs a
		// line, and this is every frame.
		if world.AdvanceWaypointFollowers(lvl, dt) {
			world.UploadLightsTo(gfx.Program, world.SceneLights(lvl), false)
		}
	}
	render.ProfileEnd() // frames & waypoints

	// Particles, after the frames they may be riding have been republished and
	// before the script tick, so a burst a script fires this frame lands at age
	// 0 and draws this frame.
	//
	// Not gated on the editor being closed, unlike entity motion: an effect
	// that froze the moment the editor opened could not be placed by eye. The
	// gate is handed over anyway so the simulation can tell play from
	// authoring.
	//
	// An effect whose document carries a `light:` flashes light over the level
	// while it goes off — a muzzle flash on the wall beside the gun.
	render.ProfileBegin("emitters")
	if world.EmitterUpdate(lvl, dt, g.Play()) {
		world.UploadLightsTo(gfx.Program, world.SceneLights(lvl), false)
	}
	render.ProfileEnd()

	// Effects, in the emitters' slot and ungated by the editor for their
	// reason, but stopped by a pause: an explosion is a moment, and one that
	// carried on behind a pause menu would be over by the time play resumed.
	// Its flash is a light, and lights are uniforms, so while one is lit — and
	// once more when the last goes out — the scene's lights go up again with
	// the flashes in them.
	render.ProfileBegin("effects")
	if world.EffectUpdate(lvl, dt, !g.GamePaused) {
		world.UploadLightsTo(gfx.Program, world.SceneLights(lvl), false)
	}
	render.ProfileEnd()

	// Decals, in the effects' slot and stopped by a pause for their reason: the
	// marks a firefight leaves are given a lifetime and fade out, and one fading
	// behind a pause menu would be gone by the time play resumed.
	//
	// An expired decal leaves the list. Nothing has to be told: the script
	// runtime's index into the level is rebuilt at the top of every tick and
	// never carried across frames, precisely so that a list changing between
	// them cannot go stale (script_ref.go).
	render.ProfileBegin("decals")
	if !g.GamePaused {
		world.StepDecals(lvl, dt)
	}
	render.ProfileEnd()

	// The listener, published in the same slot and for the same reasons as the
	// emitters above: after RefreshFrames, so anything riding a moving frame is
	// read where it now actually is, and before the script tick, so a sound a
	// script starts this frame is already audible in the block the mixer builds
	// next.
	//
	// Not gated on the editor being closed, again like the emitters: a sound
	// that went silent the moment the editor opened could not be placed by ear,
	// which is most of how a positional sound gets placed.
	//
	// gfx.CamUp() rather than gfx.Cam.Up: the zero value there means "no tilt",
	// and only the accessor knows that.
	render.ProfileBegin("sound")
	world.SoundUpdate(lvl, dt, !g.Dev, gfx.Cam.Position, gfx.Cam.Direction, gfx.CamUp())
	world.MusicUpdate(lvl)
	render.ProfileEnd()

	// Advances a script's ease_sky the same way the lines above advance
	// everything else time-based: RenderSky only ever reads lvl.Sky, so this is
	// what makes the transition's blend actually move.
	render.ProfileBegin("sky")
	world.SkyUpdate(lvl, dt, g.Play())
	render.ProfileEnd()

	// The interact key's vehicle half, before the script tick so a mount made
	// this frame is visible to it. It consumes the press only when it actually
	// did something, so `E` still reaches a scripted door when there is no seat
	// within reach.
	if g.Play() {
		VehicleInteractRequest(lvl)
	}

	// The UI ticks after the world has settled, so a binding reads this frame's
	// values, and before the script tick, so a click made this frame is already
	// in the queue that tick drains to a fixpoint.
	render.ProfileBegin("ui tick")
	if !g.Dev {
		ui.Game.Tick(dt, g.ViewW, g.ViewH)
	}
	render.ProfileEnd()

	// The script event loop, immediately after the world has settled: this
	// frame's overlaps produce this frame's `enter`, and whatever a handler
	// writes lands before the frame draws.
	if !g.LoadPending {
		render.ProfileBegin("scripts")
		script.ScriptTick(lvl, dt, !g.Dev)
		render.ProfileEnd()
	}

	// The script camera (gfx/camera_rig.go), last of all. After the script
	// tick, so a camera verb called this frame moves this frame's picture and
	// an object the camera follows is followed to where it went this frame;
	// after PlacePlayerCamera, so the player's eye is already in the camera
	// for a blend back to it to aim at. Not while a load is building a level
	// the rig has already been reset for, and not in the editor, whose camera
	// is its own.
	//
	// A pause holds the blends (dt 0) but still lays the pose down, so a
	// camera a pause menu's script puts somewhere goes there.
	if !g.Dev && !g.LoadPending {
		script.RefreshCameraTargets(lvl)
		camDT := dt
		if !g.Play() {
			camDT = 0
		}
		hasPlayer := world.PlayerEntity != nil
		gfx.StepCameraRig(camDT, g.Play() && hasPlayer, hasPlayer)
	}
}

// reportSceneryCollision hands the frame's prop-collision numbers to the F3
// overlay: the CPU its queries took this frame — the player's move, the AI's
// rays and the entity pass all ask, so the time is summed where it happens
// (collision.TakeSceneryStats) and shown as rows under "entity update" — and
// the props by mode with how many cooked meshes are resident. Timing is on
// only while the profiler is.
func reportSceneryCollision() {
	collision.ProfileScenery = render.ProfileEnabled
	if !render.ProfileEnabled && !render.DebugOverlayVisible {
		return
	}
	st := collision.TakeSceneryStats()
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	render.ProfileAddCPU("props: resolve", ms(st.ResolveTime))
	render.ProfileAddCPU("props: rays", ms(st.RayTime))
	render.ProfileAddCPU("props: particles", ms(st.ParticleTime))
	render.ProfileAddCPU("props: index", ms(st.BuildTime))
	render.DebugStats.CollisionBox, render.DebugStats.CollisionHulls, render.DebugStats.CollisionMesh = st.Box, st.Hulls, st.Mesh
	render.DebugStats.CollisionResident = st.Resident
	render.DebugStats.CollisionResidentKB = float32(st.ResidentBytes) / 1024
}
