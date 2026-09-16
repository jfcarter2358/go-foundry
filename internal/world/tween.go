package world

// tween.go — easing a placement to a target over a set time.
//
// A rate says "keep going"; a tween says "be there in this long". The second is
// what `move_to`, `rotate_to` and `scale_to` give a script, and it is kept here,
// on the object, for the reason a waypoint animation is (brush_waypoints.go):
// a script sends a door and never has to write a position again, and the move
// happens on the same gameplay tick as every other mover — so it pauses with
// the world, and it is where it should be before anything resolves against it.
//
// # What a tween is
//
// One channel — a position, a rotation or a scale — blended in a straight line
// from where it was when the tween started to a target, over a fixed number of
// seconds. The channels are independent: a brush can slide and turn at once,
// each finishing on its own clock, and starting a new tween on a channel
// replaces the old one from wherever it had got to.
//
// A rotation is blended as the three numbers it is, not along the shortest
// arc — the rule waypoint.go gives for the same reason: 0 to 2π is a whole
// turn, which is what makes "spin it round once over two seconds" expressible.
//
// # What it overrides
//
// A channel being eased is written by its tween alone. The rate that would
// otherwise move it — Velocity for a position, AngularVelocity for a rotation,
// and gravity for an entity's position — waits, and takes over again from
// wherever the tween left it. Two things writing one channel would have the
// object arrive somewhere the script did not send it.
//
// A brush or an entity following a waypoint animation stops following it when
// any of its channels is eased, and one sent along an animation drops its
// tweens: the animation writes all three channels, so the two cannot share an
// object, and the later instruction is the one that meant it.
//
// Runtime-only, like the placement it writes: never saved, and gone the moment
// the level is rebuilt.

import "github.com/go-gl/mathgl/mgl32"

// Tween is one channel's blend toward a target.
type Tween struct {
	From, To mgl32.Vec3
	// Seconds is how long the whole blend takes, and Elapsed how much of it has
	// passed.
	Seconds, Elapsed float32
}

// NewTween starts a blend from `from` to `to` over `seconds`. Zero or less —
// and NaN — makes it instantaneous: the first step lands on `to`.
func NewTween(from, to mgl32.Vec3, seconds float32) *Tween {
	if !(seconds > 0) {
		seconds = 0
	}
	return &Tween{From: from, To: to, Seconds: seconds}
}

// Step advances the blend dt seconds and returns the value it has reached and
// whether that is the end. The last step lands on To exactly rather than
// wherever the final fraction of a float left it.
func (t *Tween) Step(dt float32) (mgl32.Vec3, bool) {
	t.Elapsed += dt
	if t.Elapsed >= t.Seconds {
		return t.To, true
	}
	f := t.Elapsed / t.Seconds
	return t.From.Add(t.To.Sub(t.From).Mul(f)), false
}

// TweenChannel names which part of a placement a tween drives.
type TweenChannel int

const (
	TweenMove TweenChannel = iota
	TweenRotate
	TweenScale
	tweenChannels
)

// Tweens is the blend each channel of a brush's or an entity's placement is
// following; nil where a channel is not being eased.
//
// Named fields rather than an array indexed by TweenChannel, which would read
// more simply and cannot be saved: brushes and entities are gob-encoded into a
// compiled level, and gob omits a nil pointer field but refuses outright to
// encode a nil element of an array.
type Tweens struct {
	Move, Rotate, Scale *Tween
}

// slot is the field a channel lives in.
func (ts *Tweens) slot(c TweenChannel) **Tween {
	switch c {
	case TweenMove:
		return &ts.Move
	case TweenRotate:
		return &ts.Rotate
	}
	return &ts.Scale
}

// Easing reports whether a channel is being eased.
func (ts *Tweens) Easing(c TweenChannel) bool { return *ts.slot(c) != nil }

// Active reports whether any channel is.
func (ts *Tweens) Active() bool {
	return ts.Move != nil || ts.Rotate != nil || ts.Scale != nil
}

// step advances every channel being eased, hands each value it reaches to set,
// drops the ones that arrived, and reports whether any was written.
func (ts *Tweens) step(dt float32, set func(TweenChannel, mgl32.Vec3)) bool {
	wrote := false
	for c := TweenMove; c < tweenChannels; c++ {
		t := ts.slot(c)
		if *t == nil {
			continue
		}
		v, done := (*t).Step(dt)
		set(c, v)
		if done {
			*t = nil
		}
		wrote = true
	}
	return wrote
}

// ---------- brushes ----------

// Ease starts one channel of a dynamic brush's placement toward `to` over
// `seconds`, from wherever it is now.
//
// `to` is in the terms the brush's properties are: a position in the space its
// chunk is placed in (the space `@brush.prop.position` reads — world for a
// level's own geometry, the entity's own for a brush an entity carries), and a
// rotation and scale in the brush's own channels. An instantaneous ease is
// applied here and now, re-bucketing included, so it behaves exactly like the
// property write it amounts to.
func (b *Brush) Ease(lvl *Level, ch *Chunk, c TweenChannel, to mgl32.Vec3, seconds float32) {
	b.Waypoints = nil
	if c == TweenScale {
		to = ClampBrushScale(to)
	}
	if !(seconds > 0) {
		*b.Tweens.slot(c) = nil
		b.setTweenChannel(ch, c, to)
		ch.RecomputeBounds()
		RebuildSpatialHash(lvl)
		return
	}
	*b.Tweens.slot(c) = NewTween(b.tweenChannel(ch, c), to, seconds)
}

// tweenChannel is the brush's current value on a channel, in Ease's terms.
func (b *Brush) tweenChannel(ch *Chunk, c TweenChannel) mgl32.Vec3 {
	switch c {
	case TweenMove:
		return ch.LocalToPlacement(b.Position())
	case TweenRotate:
		return b.Rotation
	}
	return b.EffectiveScale()
}

// setTweenChannel writes a value reached on a channel into the placement.
//
// The position is carried through the chunk's placement every time rather than
// once when the tween starts, so a brush in a chunk that is itself moving still
// arrives where it was sent — the rule applyWaypointPose follows.
func (b *Brush) setTweenChannel(ch *Chunk, c TweenChannel, v mgl32.Vec3) {
	switch c {
	case TweenMove:
		b.MoveTo(ch.PlacementToLocal(v))
	case TweenRotate:
		b.Rotation = v
	case TweenScale:
		b.Scale = ClampBrushScale(v)
	}
}

// advanceBrushTweens steps every channel of the brush being eased and reports
// whether its placement was written. The caller re-buckets.
func advanceBrushTweens(ch *Chunk, b *Brush, dt float32) bool {
	return b.Tweens.step(dt, func(c TweenChannel, v mgl32.Vec3) { b.setTweenChannel(ch, c, v) })
}

// ---------- chunks ----------

// EaseOrigin starts the chunk's Origin toward `to` over `seconds`.
//
// Entities attached to the chunk ride along exactly as they do when anything
// else moves its Origin. An instantaneous ease is applied here and now.
func (ch *Chunk) EaseOrigin(lvl *Level, to mgl32.Vec3, seconds float32) {
	if !(seconds > 0) {
		ch.OriginTween = nil
		if ch.moveOriginTo(lvl, to) && ch.FrameOwner == "" {
			RebuildSpatialHash(lvl)
		}
		return
	}
	ch.OriginTween = NewTween(ch.Origin, to, seconds)
}

// EaseRotation starts the chunk's Rotation toward `to` radians over
// `seconds`. Chunk scale stays read-only to a script — nothing needs it — so
// origin and rotation are the two channels a chunk can be eased on, unlike a
// brush's three.
func (ch *Chunk) EaseRotation(lvl *Level, to float32, seconds float32) {
	if !(seconds > 0) {
		ch.RotationTween = nil
		if ch.rotateTo(to) && ch.FrameOwner == "" {
			RebuildSpatialHash(lvl)
		}
		return
	}
	ch.RotationTween = NewTween(mgl32.Vec3{ch.Rotation, 0, 0}, mgl32.Vec3{to, 0, 0}, seconds)
}

// moveOriginTo places the chunk's Origin at p, carrying its attached entities
// the same distance, and reports whether it moved. The caller re-buckets.
func (ch *Chunk) moveOriginTo(lvl *Level, p mgl32.Vec3) bool {
	delta := p.Sub(ch.Origin)
	if delta == (mgl32.Vec3{}) {
		return false
	}
	ch.Origin = p
	CarryAttachedEntities(lvl, ch, delta)
	return true
}

// rotateTo places the chunk's Rotation at p radians and reports whether it
// changed. The caller re-buckets — same shape as moveOriginTo, minus
// CarryAttachedEntities: an AttachedChunk rider is nudged by the distance the
// chunk's Origin moves, and a turn alone moves no distance, so nothing here
// repositions a rider around the chunk's new orientation. A known gap, not a
// regression — matching the "nothing rides a moving brush" limitation
// brush_dynamic.go already documents — rather than the larger rigid-body
// carrying a rotating attachment would need.
func (ch *Chunk) rotateTo(p float32) bool {
	if p == ch.Rotation {
		return false
	}
	ch.Rotation = p
	return true
}

// ---------- entities ----------

// Ease starts one channel of an entity's placement toward `to` over `seconds`,
// from wherever it is now.
//
// A position is WORLD space, the space `@entity.prop.position` reads, carried
// into the entity's own frame every tick — so an entity aboard something still
// arrives at the world point it was sent to. Rotation and scale are the
// entity's own channels. An instantaneous ease is applied here and now.
//
// Vehicles and the player are not eased: their motion belongs to the drive
// model and to the player's own movement. Refusing them is the caller's job.
func (e *Entity) Ease(fs *FrameSet, c TweenChannel, to mgl32.Vec3, seconds float32) {
	e.Waypoints = nil
	if !(seconds > 0) {
		*e.Tweens.slot(c) = nil
		e.setTweenChannel(fs, c, to)
		return
	}
	*e.Tweens.slot(c) = NewTween(e.tweenChannel(fs, c), to, seconds)
}

func (e *Entity) tweenChannel(fs *FrameSet, c TweenChannel) mgl32.Vec3 {
	switch c {
	case TweenMove:
		return fs.WorldPosition(e)
	case TweenRotate:
		return e.Rotation
	}
	return e.EffectiveScale()
}

func (e *Entity) setTweenChannel(fs *FrameSet, c TweenChannel, v mgl32.Vec3) {
	switch c {
	case TweenMove:
		e.Position = fs.FrameFor(e).FromWorld(v)
	case TweenRotate:
		e.Rotation = v
	case TweenScale:
		e.Scale = mgl32.Vec3{EntityScaleClamp(v.X()), EntityScaleClamp(v.Y()), EntityScaleClamp(v.Z())}
	}
}

// AdvanceTweens steps every channel of the entity being eased, and reports
// whether any was written.
//
// Called by EntityUpdate after the entity has integrated its own rates, so the
// eased channels overwrite what those rates did this frame — which is how the
// rates wait — and before it is resolved against the world, so an entity eased
// into a wall that collides with walls is stopped by it.
func (e *Entity) AdvanceTweens(fs *FrameSet, dt float32) bool {
	return e.Tweens.step(dt, func(c TweenChannel, v mgl32.Vec3) { e.setTweenChannel(fs, c, v) })
}
