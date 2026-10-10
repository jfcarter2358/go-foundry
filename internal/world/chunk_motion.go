package world

// chunk_motion.go — chunks that move on their own.
//
// A chunk carries a Velocity the same way an entity does, and this is what
// carries it forward: plain Euler integration onto Origin, once per tick of
// play. It exists so a lift, a barge or a closing wall is authored state rather
// than a script writing `@lift.prop.origin` every frame — which worked, but put
// a handler in the tick loop for something the placement already describes.
//
// Only the origin moves. Points, sector heights and biome paint are chunk-local
// and are carried into world space through ModelMatrix (see chunk.go), so a
// move rebuilds no geometry at all. What does not follow on its own is the
// spatial hash, whose keys are world cells — so a chunk that moved is
// re-bucketed here, before anything this tick resolves against it.
//
// A chunk sent along a waypoint animation (`play_waypoints`) is stepped here
// too, ahead of Velocity/AngularVelocity and the two tweens, on the same
// precedence a tween already has: see waypoint_follow.go's Chunk methods.

import "github.com/go-gl/mathgl/mgl32"

// AdvanceChunks integrates every moving chunk in the level by dt seconds and
// returns whether any of them actually moved.
//
// The return is what the caller marks the play session dirty with: a chunk that
// drifted during play is authored state that has been written, and leaving play
// has to put it back where the author left it (see script.PlaySessionMarkDirty).
//
// Gameplay only, and the main loop is what enforces that — a platform that
// slid away from where it was just placed would make placing one impossible,
// exactly the reason entity motion is gated the same way (EntityUpdate's
// `motion`).
//
// A disabled chunk does not move. It is out of the world entirely — nothing
// draws it, nothing collides with it, it is not in the hash — so integrating it
// would be simulating a thing that is not there, and a streaming level would
// find its switched-off areas had wandered off by the time it switched them
// back on.
func AdvanceChunks(lvl *Level, dt float32) bool {
	if lvl == nil || dt == 0 {
		return false
	}
	moved, rehash := false, false
	for i := range lvl.Chunks {
		ch := &lvl.Chunks[i]
		if !ch.IsEnabled() {
			continue
		}
		if ch.Waypoints != nil {
			// A path writes the whole placement, the same as a tween, and
			// takes precedence over Velocity/AngularVelocity on the same
			// grounds: advanceWaypoints calls moveOriginTo/rotateTo itself,
			// so a rider is carried and a wrap the same as any other write.
			if advanceWaypoints(lvl, ch, dt) {
				moved = true
				if ch.FrameOwner == "" {
					rehash = true
				}
			}
			continue
		}
		movingOrigin := ch.Velocity != (mgl32.Vec3{}) || ch.OriginTween != nil
		movingRotation := ch.AngularVelocity != 0 || ch.RotationTween != nil
		if !movingOrigin && !movingRotation {
			continue
		}

		chunkMoved := false
		if movingOrigin {
			// A script easing the origin (`move_to`) is the more specific
			// instruction, and the velocity waits until it arrives — see
			// tween.go.
			target := ch.Origin.Add(ch.Velocity.Mul(dt))
			if t := ch.OriginTween; t != nil {
				p, done := t.Step(dt)
				if done {
					ch.OriginTween = nil
				}
				target = p
			}
			if ch.moveOriginTo(lvl, target) {
				chunkMoved = true
			}
		}
		if movingRotation {
			// The same precedence as movingOrigin above: a `rotate_to` in
			// progress is the more specific instruction, and AngularVelocity
			// waits until it arrives. Wrapped the way AdvanceBrushes wraps a
			// brush's own Rotation, and for the same reason: left spinning
			// long enough, an unwrapped angle's float32 resolution goes
			// coarser than a degree.
			target := WrapAngle(ch.Rotation + ch.AngularVelocity*dt)
			if t := ch.RotationTween; t != nil {
				p, done := t.Step(dt)
				if done {
					ch.RotationTween = nil
				}
				target = p.X()
			}
			if ch.rotateTo(target) {
				chunkMoved = true
			}
		}
		if !chunkMoved {
			continue
		}
		moved = true
		// A frame-owned chunk is deliberately kept out of the spatial hash
		// (chunk.go), so moving one within its carrier costs nothing to index.
		// Only a world chunk's move invalidates a bucket.
		if ch.FrameOwner == "" {
			rehash = true
		}
	}
	if rehash {
		RebuildSpatialHash(lvl)
	}
	return moved
}

// CarryAttachedEntities nudges every entity riding ch (Entity.AttachedChunk)
// by delta — the distance ch's Origin just moved. Called after any edit to
// ch.Origin that should carry its riders along: AdvanceChunks during play,
// and the editor's own chunk-move paths (moveChunk and the properties panel's
// Origin field).
//
// A rider has to be expressed in the same space ch is, or it is left alone —
// see Entity.AttachedChunk.
func CarryAttachedEntities(lvl *Level, ch *Chunk, delta mgl32.Vec3) {
	if lvl == nil || ch == nil || delta == (mgl32.Vec3{}) {
		return
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if e.AttachedChunk == ch.ID && e.Frame == ch.FrameOwner {
			e.Position = e.Position.Add(delta)
		}
	}
}
