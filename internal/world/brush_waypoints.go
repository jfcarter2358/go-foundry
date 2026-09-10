package world

// brush_waypoints.go — a dynamic brush travelling a waypoint animation.
//
// The animation itself is waypoint.go's: what a point means and how long a leg
// takes. The travelling is waypoint_follow.go's, shared with every other kind
// a path can carry. This is the brush's half: its progress through one, and how
// a pose it has reached is written into the same Offset/Rotation/Scale
// placement a script writing `position` would.
//
// That is the whole design, and it is why nothing downstream changed. Drawing,
// shadows, collision, the spatial hash and raycasts all read Brush.Placement
// already (brush_dynamic.go), so a brush on a path is, to every one of them,
// just a dynamic brush that happens to be somewhere else this frame.

// WaypointPlayback is an object's progress through a waypoint animation.
// Runtime-only, like the placement it drives: never saved, and gone the moment
// the level is rebuilt.
//
// The animation is held by ID and looked up every tick rather than by pointer,
// for the reason a script ref is a name: Level.WaypointAnimations is a slice of
// values that undo rebuilds wholesale, and an author dragging a point while the
// editor previews the path is exactly the case a stale pointer gets wrong.
type WaypointPlayback struct {
	// Animation is the ID of the animation being followed.
	Animation string
	// Elapsed is how many seconds the object has been travelling. The pose is
	// read off the animation at that time every tick (PoseAt), so an author
	// retiming a point mid-journey moves the object to where the new timing
	// has it, the same as scrubbing would.
	Elapsed float32
	// Next is the index of the first point not yet announced as reached.
	Next int
	// Start is where the object was when it was sent: the near end of the
	// first leg, which belongs to no point.
	Start WaypointPose
}

// WaypointPose is where the brush is now, in the terms a waypoint is written
// in: its centre carried out to world space, and its own rotation and scale.
func (b *Brush) WaypointPose(ch *Chunk) WaypointPose {
	return WaypointPose{
		Position: ch.LocalToWorld(b.Position()),
		Rotation: b.Rotation,
		Scale:    b.EffectiveScale(),
	}
}

// PlayWaypoints sends the brush along anim, starting from wherever it is now —
// PlayWaypoints for a brush, which needs its chunk rather than the level.
func (b *Brush) PlayWaypoints(ch *Chunk, anim *WaypointAnimation) {
	PlayWaypoints(nil, brushFollower{ch, b}, anim)
}

// StopWaypoints leaves the brush wherever it has got to and reports whether it
// was following an animation at all.
func (b *Brush) StopWaypoints() bool { return StopWaypoints(brushFollower{b: b}) }

// SeekWaypoints puts a brush that is following anim t seconds into it, as
// though it had travelled there — SeekWaypoints for a brush.
func (b *Brush) SeekWaypoints(ch *Chunk, anim *WaypointAnimation, t float32) bool {
	return SeekWaypoints(nil, brushFollower{ch, b}, anim, t)
}

// applyWaypointPose writes a pose into the brush's placement.
//
// The world position is divided through the chunk's full transform every time
// it is applied, rather than once when the brush is sent, so a brush in a chunk
// that is itself moving still meets each arrow where the arrow is drawn.
func (b *Brush) applyWaypointPose(ch *Chunk, p WaypointPose) {
	b.MoveTo(ch.WorldToLocal(p.Position))
	b.Rotation = p.Rotation
	b.Scale = ClampBrushScale(p.Scale)
}

// advanceBrushWaypoints carries one brush dt seconds further along its
// animation and reports whether its placement was written. The caller
// re-buckets.
func advanceBrushWaypoints(lvl *Level, ch *Chunk, b *Brush, dt float32) bool {
	return advanceWaypoints(lvl, brushFollower{ch, b}, dt)
}

// FindBrushByID returns the brush with the given level-unique ID and the chunk
// it lives in, or nils.
func FindBrushByID(lvl *Level, id string) (*Chunk, *Brush) {
	if lvl == nil || id == "" {
		return nil, nil
	}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		for bi := range ch.Brushes {
			if ch.Brushes[bi].ID == id {
				return ch, &ch.Brushes[bi]
			}
		}
	}
	return nil, nil
}
