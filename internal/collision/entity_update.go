package collision

// entity_update.go — what happens to entities between frames.
//
// Three things, in order: integrate motion, work out where that left them, and
// advance their animations. It runs in the editor as well as in gameplay,
// because an animation you cannot see while placing the entity is most of the
// way to useless — but motion is *gameplay only*, since an entity that drifted
// away from where the author dropped it would make placing one impossible.
//
// Integration is plain Euler with no damping. Velocity and angular velocity are
// state the game sets and the engine carries forward; what should slow a crate
// down is a rule the game has rather than one the engine invents.
//
// What the engine *will* do, when an entity asks for it, is stop it going
// through things — see entity_collision.go for the three flags and what each
// one buys. Nothing is on by default.
//
// The player is not in this list: it is engine-owned and has its own resolve
// path in the main loop. It does take part in entity-against-entity collision,
// which is why EntityUpdate is handed a pointer to it.
//
// Vehicles are the same story. VehicleUpdate already fell, steered and
// resolved them against the world before EntityUpdate ever runs — doing that
// again here would move a vehicle by its velocity twice in one frame and
// resolve it against the world twice, fighting its own steering and setting
// its interior frame transform to two different answers a tick. They do still
// take part in entity-against-entity collision below, same as the player.
//
// Skipped here does NOT mean their Velocity and AngularVelocity are dead: the
// drive model integrates both itself, in the vehicle's own axes for the
// angular one. See vehicle_drive.go.

import (
	"math"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// animFrame counts EntityUpdate calls, for spreading distant entities' posing
// over the frames (world.AnimPoseDue).
var animFrame uint64

// EntityUpdate advances every entity in the level by dt seconds.
//
// motion gates everything physical: the main loop passes false while the editor
// is active, so entities stay exactly where they were placed and no amount of
// gravity or collision drags them off. Animations advance either way.
//
// player may be nil (it is, in tests and before the level finishes loading);
// when it is not, it takes part in the entity-against-entity pass.
//
// The order matters. Every entity falls and moves first, then the whole set is
// separated at once, then anything a separation displaced is settled back
// against the world. Separating as each entity moved would make the result
// depend on the order the level lists them in; skipping the settle would let a
// push shove something through a wall.
//
// Placed scenery is separated before the entity pass rather than inside it
// (scenery_collision.go). A prop is level furniture, so an entity shoved out of
// one should then be separated from whatever else it landed against, and not
// the other way round.
func EntityUpdate(lvl *world.Level, dt float32, motion bool, player *world.Entity) {
	if lvl == nil {
		return
	}
	animFrame++
	// A new tick for the placement broadphase: its first query checks the
	// scenery for edits, and a cooked mesh no body has come near for a while
	// is let go (scenery_index.go).
	Epoch()
	residentTick()
	viewer := gfx.Cam.Position
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if motion && !e.IsVehicle() {
			easeStepOffset(e, dt)
			old := e.Position
			// Whether the ground this entity is drawn from is the same ground
			// as last tick's, read before gravity or the resolve can touch it —
			// see noteLanding.
			wasGrounded := e.Collision.OnGround
			// An entity being eased somewhere (`move_to`) or sent along a
			// waypoint animation does not fall on the way. Its position is the
			// tween's or the path's, so gravity would only pile up a fall speed
			// that nothing spends — and then release all of it the instant the
			// tween or the path let go.
			//
			// The pull is asked for per body rather than taken from a
			// constant: the level sets it, a volume may override it where this
			// entity is standing, and the entity scales whatever comes back
			// (world/gravity.go). noteLanding below needs the same number to
			// turn a landing speed back into a drop, so it is resolved once.
			g := world.GravityFor(lvl, e)
			if !e.Tweens.Easing(world.TweenMove) && e.Waypoints == nil {
				e.ApplyGravity(dt, g)
			}
			// The speed gravity had built up the instant before the resolve can
			// touch the ground and zero it back out — see noteLanding.
			fallSpeed := -e.Velocity.Y()
			e.Integrate(dt)
			// After Integrate, so an eased channel overwrites what its rate did
			// this frame; before the resolve, so a colliding entity eased into a
			// wall is stopped by it (world/tween.go). A path writes every
			// channel on the same terms, and the two never both run: starting
			// either drops the other.
			e.AdvanceTweens(lvl.Frames, dt)
			e.AdvanceWaypoints(lvl, dt)
			if e.CollidesWithWorld() && resolveEntityWorld(lvl, e, old) {
				e.Collision.GroundedNext = true
			}
			if !wasGrounded && e.Collision.GroundedNext {
				noteLanding(e, fallSpeed, g)
			}
			e.UpdateLocation(lvl)
			crossEntityFrames(lvl, e)
		}
		e.UpdateAnimationsPose(dt, world.AnimPoseDue(e, i, animFrame, viewer))
	}
	if !motion {
		return
	}

	// Scenery, before the entity pass: a prop is level furniture, so an entity
	// shoved out of one should then be separated from whatever else it landed
	// against, rather than the other way round.
	for _, i := range ResolveSceneryCollisions(lvl, player) {
		e := &lvl.Entities[i]
		if !e.CollidesWithWorld() {
			continue
		}
		if resolveEntityWorld(lvl, e, e.Position) {
			e.Collision.GroundedNext = true
		}
		e.UpdateLocation(lvl)
	}

	for _, i := range ResolveEntityCollisions(lvl, player) {
		e := &lvl.Entities[i]
		if !e.CollidesWithWorld() {
			continue
		}
		// Settle from where it now is rather than from where it started the
		// frame: the push is the movement being resolved, and handing the slope
		// resolver the frame's original position would have it judge the push
		// as though the entity had walked there.
		if resolveEntityWorld(lvl, e, e.Position) {
			e.Collision.GroundedNext = true
		}
		e.UpdateLocation(lvl)
	}

	// Publish this frame's ground state, now that every push and every settle
	// has contributed to it — and remember where each body was left, so the
	// next update can tell a script's teleport from its own motion.
	for i := range lvl.Entities {
		lvl.Entities[i].CommitGrounded()
		lvl.Entities[i].StepAnchor = lvl.Entities[i].Position
	}
	if player != nil {
		player.CommitGrounded()
	}
}

// easeStepOffset closes the gap between a walker's drawn body and its real one
// by a tick's worth, or drops the gap outright when it no longer describes a
// stride (world/entity_step.go).
//
// Runs before the entity moves, so a step-up this tick is subtracted from an
// offset that has already had its decay: the body is drawn exactly where the
// last tick left it, and starts closing on the next.
//
// Dropped rather than eased when:
//
//   - Position moved farther between updates than anything but a placement
//     would move it (StepEaseTeleport). A script's `position` write, a spawn
//     into a namesake's slot, a seat swap: none of them should arrive trailing
//     half a stride.
//   - Something other than the entity's own feet owns its position now — a
//     tween or a waypoint path writes Position outright every tick, and a
//     seated body is the vehicle's.
//
// Crossing a frame portal is NOT one of these. The offset is along the frame's
// up axis, a portal only opens between frames whose up axes agree
// (FramesAligned), and StepAnchor is refreshed after the crossing — so a squad
// member stepping up the sill into a hold keeps easing as it boards.
// stepDownMinDrop and stepDownMaxDrop bound what a landing has to fall to
// read as a "step down" rather than an ordinary settle or a real fall: below
// the low end the drop is imperceptible and not worth a clip, above the high
// end the body was airborne long enough that this was a fall off something,
// not a stride down a ledge — left silent, as a future "hard landing" clip's
// business rather than this one's.
const (
	stepDownMinDrop = float32(0.15)
	stepDownMaxDrop = float32(1.0)
)

// noteLanding plays step_down when a walker that just touched ground had
// fallen a small, ledge-sized distance to get there.
//
// There is no field tracking how far an entity fell: fallSpeed — the vertical
// speed gravity had built up the tick it landed, read before the resolve zeros
// it back out — is enough to recover that distance without one. A walker's
// Velocity.Y is 0 the tick it leaves the ground (resolveEntityWorld zeros it
// every tick it rests on something, right up to the last one), so the fall
// that followed was ordinary gravity from rest, and standard kinematics
// (v² = 2gd) gives back the distance from the speed alone.
func noteLanding(e *world.Entity, fallSpeed, g float32) {
	if fallSpeed <= 0 || !e.AIDriven() || !e.HasGravity() || e.Seat != "" {
		return
	}
	// g is the pull the fall actually happened under, not the engine's default:
	// on a low-gravity level the same landing speed means a much longer drop,
	// and reading it off a constant would play the ledge-hop clip for a fall of
	// several storeys. A non-positive pull cannot have caused a downward fall,
	// so there is no drop to recover.
	if g <= 0 {
		return
	}
	drop := (fallSpeed * fallSpeed) / (2 * g)
	if drop < stepDownMinDrop || drop > stepDownMaxDrop {
		return
	}
	triggerStepAnim(e, "step_down")
}

func easeStepOffset(e *world.Entity, dt float32) {
	if e.StepOffset == 0 {
		return
	}
	if e.Position.Sub(e.StepAnchor).Len() > world.StepEaseTeleport ||
		e.Waypoints != nil || e.Tweens.Easing(world.TweenMove) || e.Seat != "" {
		e.ClearStepOffset()
		return
	}
	e.EaseStepOffset(dt)
}

// crossEntityFrames hands an entity an AI controller is walking aboard a frame
// it has walked onto, or back out of one it has walked off — the player's
// frame-portal step (app.go), for a body with no camera.
//
// Only walkers: an agent steered by a controller, falling, on its own feet.
// A crate shoved up a ramp or a grenade lobbed into a hold stays in the space
// it was in, as every entity always has; being carried off by a ship is a
// thing a squadmate should do and a prop should not start doing on an update.
//
// The facing goes through the same rotation as the position: an agent that
// walked up a ramp facing into the hold still faces into it once its yaw is
// measured against the hold.
//
// Not while a waypoint animation carries it. A path writes the entity's
// rotation as a raw number in the frame it started in, so a body jumping down
// from a hold along one would turn half round the moment it passed the hull.
// It stays aboard until the path lets go, and steps off then.
func crossEntityFrames(lvl *world.Level, e *world.Entity) {
	if !e.AIDriven() || !e.HasGravity() || e.Seat != "" || !e.CollidesWithWorld() || e.Waypoints != nil {
		return
	}
	c := FindFramePortalCrossing(lvl, e)
	if c == nil || c.Sealed {
		return
	}
	from := lvl.Frames.Frame(c.FromID)
	to := lvl.Frames.Frame(c.ToID)
	if from == nil || to == nil {
		return
	}
	yaw := float64(e.Rotation.Y())
	fwd := from.DirToWorld(mgl32.Vec3{float32(-math.Sin(yaw)), 0, float32(-math.Cos(yaw))})
	CrossFramePortal(lvl, e, c, 0)
	local := to.DirFromWorld(fwd)
	if local.X()*local.X()+local.Z()*local.Z() > 1e-8 {
		e.Rotation[1] = float32(math.Atan2(float64(-local.X()), float64(-local.Z())))
	}
}

// ---------- level-wide helpers ----------

// DeleteEntities releases every entity's GPU buffers. Called when a level is
// replaced, so the outgoing level's meshes do not leak.
func DeleteEntities(lvl *world.Level) {
	if lvl == nil {
		return
	}
	for i := range lvl.Entities {
		lvl.Entities[i].Delete()
	}
}
