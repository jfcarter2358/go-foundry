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

import (
	"foundry/internal/world"
)

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
func EntityUpdate(lvl *world.Level, dt float32, motion bool, player *world.Entity) {
	if lvl == nil {
		return
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if motion && !e.IsVehicle() {
			old := e.Position
			e.ApplyGravity(dt)
			e.Integrate(dt)
			if e.CollidesWithWorld() && resolveEntityWorld(lvl, e, old) {
				e.Collision.GroundedNext = true
			}
			e.UpdateLocation(lvl)
		}
		e.UpdateAnimations(dt)
	}
	if !motion {
		return
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
	// has contributed to it.
	for i := range lvl.Entities {
		lvl.Entities[i].CommitGrounded()
	}
	if player != nil {
		player.CommitGrounded()
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
