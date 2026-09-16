package world

// frame_rest.go — whether a carried frame is standing still.
//
// Navigation is baked into world space, so it can only take in a frame's floors
// while that frame stays where it was baked: a ship parked on a pad is ground
// an agent can walk up the ramp of, and the same ship in flight is somewhere
// else next tick. The same question tells an AI controller whether an agent
// aboard may walk about, or has to hold on until the thing under it stops.

import "github.com/go-gl/mathgl/mgl32"

// FrameUpTolerance is how far a frame's up may lean from the world's, as a
// cosine, and still be baked as floor — collision.FrameAlignTolerance, restated
// because world sits below collision in the import graph.
const FrameUpTolerance = float32(0.99)

// AtRest reports whether nothing is moving the entity: no velocity, no spin,
// no eased move or turn, and no waypoint animation carrying it.
func (e *Entity) AtRest() bool {
	if e == nil {
		return true
	}
	return e.Velocity == (mgl32.Vec3{}) && e.AngularVelocity == (mgl32.Vec3{}) &&
		!e.Tweens.Easing(TweenMove) && !e.Tweens.Easing(TweenRotate) &&
		e.Waypoints == nil
}

// FrameAtRest reports whether the frame owned by id is standing still in the
// world and standing upright: its owner and every entity it rides are at rest,
// and its up is the world's. The world frame (id "") always is.
func FrameAtRest(lvl *Level, id string) bool {
	if id == "" {
		return true
	}
	if lvl == nil || lvl.Frames == nil {
		return false
	}
	f := lvl.Frames.Frame(id)
	if f == nil || f.IsWorld() {
		return false
	}
	if f.DirToWorld(mgl32.Vec3{0, 1, 0}).Y() < FrameUpTolerance {
		return false
	}
	for depth := 0; id != "" && depth <= MaxFrameDepth; depth++ {
		owner := FindEntity(lvl, id)
		if owner == nil || !owner.AtRest() {
			return false
		}
		id = owner.Frame
	}
	return id == ""
}
