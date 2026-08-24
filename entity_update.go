package main

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

import (
	"foundry/internal/hooks"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// twoPi is the wrap period for Euler angles under angular velocity.
const twoPi = float32(2 * math.Pi)

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
func EntityUpdate(lvl *Level, dt float32, motion bool, player *Entity) {
	if lvl == nil {
		return
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if motion {
			old := e.Position
			e.applyGravity(dt)
			e.Integrate(dt)
			if e.collidesWithWorld() && resolveEntityWorld(lvl, e, old) {
				e.Collision.groundedNext = true
			}
			e.updateLocation(lvl)
		}
		e.UpdateAnimations(dt)
	}
	if !motion {
		return
	}

	for _, i := range ResolveEntityCollisions(lvl, player) {
		e := &lvl.Entities[i]
		if !e.collidesWithWorld() {
			continue
		}
		// Settle from where it now is rather than from where it started the
		// frame: the push is the movement being resolved, and handing the slope
		// resolver the frame's original position would have it judge the push
		// as though the entity had walked there.
		if resolveEntityWorld(lvl, e, e.Position) {
			e.Collision.groundedNext = true
		}
		e.updateLocation(lvl)
	}

	// Publish this frame's ground state, now that every push and every settle
	// has contributed to it.
	for i := range lvl.Entities {
		lvl.Entities[i].commitGrounded()
	}
	if player != nil {
		player.commitGrounded()
	}
}

// Integrate advances position by velocity and rotation by angular velocity.
//
// Rotation wraps into [-π, π] rather than growing without bound: an entity
// spinning at 1 rad/s for an hour would otherwise reach an angle whose float32
// resolution is coarser than a degree, and the spin would visibly quantise.
func (e *Entity) Integrate(dt float32) {
	if e.Velocity != (mgl32.Vec3{}) {
		e.Position = e.Position.Add(e.Velocity.Mul(dt))
	}
	if e.AngularVelocity != (mgl32.Vec3{}) {
		r := e.Rotation.Add(e.AngularVelocity.Mul(dt))
		e.Rotation = mgl32.Vec3{wrapAngle(r.X()), wrapAngle(r.Y()), wrapAngle(r.Z())}
	}
}

// wrapAngle folds an angle into [-π, π].
func wrapAngle(a float32) float32 {
	if a >= -math.Pi && a <= math.Pi {
		return a
	}
	a = float32(math.Mod(float64(a), float64(twoPi)))
	if a > math.Pi {
		a -= twoPi
	} else if a < -math.Pi {
		a += twoPi
	}
	return a
}

// updateLocation refreshes which chunk and sector the entity is standing in.
//
// This is derived state that scripts read — which room is the guard in — and
// that a future entity-aware collision pass would start from. It narrows the
// same way the player already does, through the same spatial hash.
func (e *Entity) updateLocation(lvl *Level) {
	chunk, sector := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, e.Position)
	if chunk != nil {
		e.CurrentChunk = chunk.ID
	} else {
		e.CurrentChunk = ""
	}
	if sector != nil {
		e.CurrentSector = sector.ID
	} else {
		e.CurrentSector = ""
	}
}

// UpdateAnimations advances every attachment's clip.
func (e *Entity) UpdateAnimations(dt float32) {
	for i := range e.Models {
		e.Models[i].Update(dt, e.ID)
	}
}

// PlayAnimation starts a named animation on one attachment and reports whether
// it resolved. An empty model name plays on every attachment that has an
// animation by that name, which is what a script asking a whole entity to
// "walk" means when the walk is split across a body and a pair of legs.
func (e *Entity) PlayAnimation(model, animation string) bool {
	if model != "" {
		m := e.Model(model)
		if m == nil {
			return false
		}
		if !m.Play(animation) {
			return false
		}
		// The clip has begun. Emitted here rather than in EntityModel.Play
		// because an attachment has no way back to its entity, and a clip
		// restarted from its own end has no clock change for Update to notice.
		hooks.RecordAnimEvent(hooks.AnimEvent{Entity: e.ID, Model: m.Name, Animation: animation})
		return true
	}
	any := false
	for i := range e.Models {
		if e.Models[i].Play(animation) {
			hooks.RecordAnimEvent(hooks.AnimEvent{Entity: e.ID, Model: e.Models[i].Name, Animation: animation})
			any = true
		}
	}
	return any
}

// StopAnimations returns every attachment to its rest pose.
func (e *Entity) StopAnimations() {
	for i := range e.Models {
		e.Models[i].Stop()
	}
}

// ---------- level-wide helpers ----------

// EntitiesContaining returns every entity whose bounding volume holds p — the
// query behind the scripting engine's `enter`/`exit` events, and what an
// "interact" key press resolves through.
func EntitiesContaining(lvl *Level, p mgl32.Vec3) []*Entity {
	if lvl == nil {
		return nil
	}
	var out []*Entity
	for i := range lvl.Entities {
		if lvl.Entities[i].ContainsPoint(p) {
			out = append(out, &lvl.Entities[i])
		}
	}
	return out
}

// DeleteEntities releases every entity's GPU buffers. Called when a level is
// replaced, so the outgoing level's meshes do not leak.
func DeleteEntities(lvl *Level) {
	if lvl == nil {
		return
	}
	for i := range lvl.Entities {
		lvl.Entities[i].Delete()
	}
}
