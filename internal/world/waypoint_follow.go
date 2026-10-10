package world

// waypoint_follow.go — anything a waypoint animation can carry.
//
// A path was a dynamic brush's alone at first (brush_waypoints.go), and what it
// needed from the brush turned out to be what any placed object has: where it
// is in the terms a waypoint is written in, a way to be put at such a pose, and
// somewhere to keep its progress. WaypointFollower is those, and the step, the
// seek and the play/stop pair below are written once against it — so a sprite
// on a path arrives at each point at exactly the moment a door would, through
// the same PoseAt, and announces its arrivals the same way.
//
// # What each kind takes from a pose
//
// A point has a position, a rotation and a scale. Each kind reads the channels
// it has and ignores the rest:
//
//	kind                    position                        rotation            scale
//	brush                   its centre, chunk divided out   `rotation`          `scale`
//	entity                  `position`, frame divided out   `rotation`          `scale`
//	chunk                   its origin, frame divided out   `rotation`.Y() only —
//	sprite                  `position`, Attach divided out  `rotation`          width × x, height × y
//	emitter, sound          `position`, Attach divided out  `rotation`          —
//	volume                  `position`, Frame divided out   `rotation`          —
//	point light             `position`                      —                   —
//	directional light       —                               the arrow's aim     —
//
// Position is WORLD space for every kind, because that is where the arrow is
// drawn, and it is carried into whatever space the object is authored in on
// every tick — so an emitter hung on a walking entity still meets each arrow.
//
// An animation an entity carries (entity_waypoints.go) is written in that
// entity's space instead, and travelled there: the start and every pose along
// the way are in the entity's terms, and only the pose handed to the object is
// carried out to the world, through where the entity is this tick. So a door
// sent shut while its ship climbs still closes against the hull, not against
// the pad the ship left.
//
// A directional light has nowhere to be, so it takes the one thing an arrow
// says that it can use: the way the arrow points is the way the light shines.
// Its rotation channel is the Euler turn that points +X — the arrow's shaft —
// along the light (DirLightEuler), which is also what lets "sweep the sun
// across the sky" be written as numbers that keep counting past half a turn.
//
// A chunk's own rotation is a single Y-axis angle (chunk.go), not a full
// Euler triple, so it reads only the Y channel of a point's rotation the same
// way a directional light reads only two of a point's channels — the rest of
// what an author turns on an arrow simply does not apply. Its scale is
// read-only to a script (tween.go), so a path leaves it alone too.
//
// # Where each kind is stepped
//
// Brushes on AdvanceBrushes' tick, chunks on AdvanceChunks' and entities in
// EntityUpdate, where the rates, the re-bucketing and the collision the path
// has to agree with already are. Everything else in AdvanceWaypointFollowers,
// which the frame loop runs once the frames an attached object is divided
// through have been republished.

import (
	"foundry/internal/hooks"

	"github.com/go-gl/mathgl/mgl32"
)

// WaypointFollower is an object a waypoint animation can carry.
//
// Its methods are unexported, so only this package's types are followers; the
// exported functions below are how anything else sends, stops and seeks one.
type WaypointFollower interface {
	// WaypointPose is where the object is now, in the terms a waypoint is
	// written in: a world-space position, and its own rotation and scale.
	// Channels a kind does not have read as nothing — zero, and a scale of one.
	WaypointPose(lvl *Level) WaypointPose
	// waypoints is the object's own Waypoints field.
	waypoints() **WaypointPlayback
	// applyWaypointPose puts the object at p, on the channels its kind has.
	applyWaypointPose(lvl *Level, p WaypointPose)
	// waypointSource is what an arrival is announced as coming from.
	waypointSource() (hooks.WaypointObject, string)
	// dropTweens forgets any blend the object was following (tween.go). A
	// no-op for the kinds nothing eases.
	dropTweens()
	// waypointRest and setWaypointRest read and write the fields a path
	// writes, exactly as the object holds them.
	waypointRest() WaypointRest
	setWaypointRest(r WaypointRest)
}

// WaypointRest is the part of an object a path writes, as the object's own
// fields hold it rather than as a pose: a pose carries a position out to world
// space and a directional light's angles through a direction, and neither
// comes back to the number it started as. It is what the editor's preview
// keeps to put an object back exactly where it was authored.
type WaypointRest struct {
	// Position is Brush.Offset for a brush, and Position for every other kind
	// that has one.
	Position, Rotation, Scale mgl32.Vec3
	// Yaw and Pitch are a directional light's, in degrees.
	Yaw, Pitch float32
}

// CaptureWaypointRest reads the fields of f a path writes.
func CaptureWaypointRest(f WaypointFollower) WaypointRest { return f.waypointRest() }

// RestoreWaypointRest writes them back. The caller re-buckets a brush and
// re-uploads a light, as after any other placement write.
func RestoreWaypointRest(f WaypointFollower, r WaypointRest) { f.setWaypointRest(r) }

// WaypointTakesPosition reports whether f reads a pose's position — every kind
// but a directional light, which has no place to be.
func WaypointTakesPosition(f WaypointFollower) bool {
	_, dir := f.(*DirectionalLight)
	return !dir
}

// WaypointSource is the kind and ID f's arrivals are announced as — which is
// also how anything outside this package names f.
func WaypointSource(f WaypointFollower) (hooks.WaypointObject, string) { return f.waypointSource() }

// ---------- sending, stopping, seeking ----------

// PlayWaypoints sends f along anim, starting from wherever it is now.
//
// From where it IS, not from where it was authored, and that includes an
// object that was already partway along another animation: a script changing
// its mind mid-journey gets something that sets off from the spot it had
// reached, rather than one that teleports back first.
//
// Any tween the object was following is dropped: the animation writes every
// channel a tween could, so the two cannot share one (tween.go).
func PlayWaypoints(lvl *Level, f WaypointFollower, anim *WaypointAnimation) {
	f.dropTweens()
	*f.waypoints() = &WaypointPlayback{Animation: anim.ID, Start: anim.localPose(lvl, f.WaypointPose(lvl))}
}

// StopWaypoints leaves f wherever it has got to and reports whether it was
// following an animation at all.
func StopWaypoints(f WaypointFollower) bool {
	slot := f.waypoints()
	was := *slot != nil
	*slot = nil
	return was
}

// FollowedWaypointAnimation is the ID of the animation f is travelling, or "".
func FollowedWaypointAnimation(f WaypointFollower) string {
	if pb := *f.waypoints(); pb != nil {
		return pb.Animation
	}
	return ""
}

// SeekWaypoints puts f, which is following an animation, t seconds into anim
// as though it had travelled there, and reports whether it was following
// anything.
//
// For the editor's timeline, which scrubs a preview back and forth. Nothing is
// announced: a seek is not an arrival, and the points it skips over were never
// reached. The pose is PoseAt's, the same the tick writes.
func SeekWaypoints(lvl *Level, f WaypointFollower, anim *WaypointAnimation, t float32) bool {
	pb := *f.waypoints()
	if pb == nil || anim == nil {
		return false
	}
	if t < 0 {
		t = 0
	}
	pb.Animation, pb.Elapsed, pb.Next = anim.ID, t, 0
	for pb.Next < len(anim.Points) && anim.Points[pb.Next].Time <= t {
		pb.Next++
	}
	f.applyWaypointPose(lvl, anim.worldPose(lvl, anim.PoseAt(pb.Start, t)))
	return true
}

// advanceWaypoints carries f dt seconds further along its animation and
// reports whether its placement was written.
//
// A tick long enough to pass several points announces every one of them, in
// order, so a slow frame costs the object nothing but smoothness.
func advanceWaypoints(lvl *Level, f WaypointFollower, dt float32) bool {
	slot := f.waypoints()
	pb := *slot
	if pb == nil {
		return false
	}
	anim := FindWaypointAnimation(lvl, pb.Animation)
	if anim == nil {
		// Deleted, renamed or undone out from under the object. It stays where
		// it had got to, the same as a stop.
		*slot = nil
		return false
	}
	kind, id := f.waypointSource()

	pb.Elapsed += dt
	for pb.Next < len(anim.Points) && anim.Points[pb.Next].Time <= pb.Elapsed {
		hooks.RecordWaypointEvent(hooks.WaypointEvent{Kind: kind, Object: id, Animation: anim.ID, Index: pb.Next, Events: anim.Points[pb.Next].Events})
		pb.Next++
	}

	n := len(anim.Points)
	if pb.Elapsed >= anim.EffectiveLength() {
		// The length has run out. Set down on the last point exactly, rather
		// than wherever the final fraction of a float left the object.
		if n > 0 {
			f.applyWaypointPose(lvl, anim.worldPose(lvl, anim.Points[n-1].Pose()))
		}
		*slot = nil
		hooks.RecordWaypointEvent(hooks.WaypointEvent{Kind: kind, Object: id, Animation: anim.ID, Index: n - 1, Ended: true})
		return true
	}

	f.applyWaypointPose(lvl, anim.worldPose(lvl, anim.PoseAt(pb.Start, pb.Elapsed)))
	return true
}

// AdvanceWaypointFollowers carries every sprite, emitter, sound, light and
// volume that is following a waypoint animation dt seconds along it, and
// reports whether a light moved: the renderer reads the others where they are
// every frame, but the lights are uniforms, and the caller has to upload them
// again.
//
// Called once the frames have been republished, so an attached object is
// divided through where its carrier is this frame, and before the emitters and
// the mixer read where those are.
func AdvanceWaypointFollowers(lvl *Level, dt float32) (lightsMoved bool) {
	if lvl == nil || dt == 0 {
		return false
	}
	for i := range lvl.Sprites {
		if lvl.Sprites[i].Waypoints != nil {
			advanceWaypoints(lvl, &lvl.Sprites[i], dt)
		}
	}
	for i := range lvl.Emitters {
		if lvl.Emitters[i].Waypoints != nil {
			advanceWaypoints(lvl, &lvl.Emitters[i], dt)
		}
	}
	for i := range lvl.Sounds {
		if lvl.Sounds[i].Waypoints != nil {
			advanceWaypoints(lvl, &lvl.Sounds[i], dt)
		}
	}
	for i := range lvl.Volumes {
		if lvl.Volumes[i].Waypoints != nil {
			advanceWaypoints(lvl, &lvl.Volumes[i], dt)
		}
	}
	for i := range lvl.Lights.Points {
		if lvl.Lights.Points[i].Waypoints != nil && advanceWaypoints(lvl, &lvl.Lights.Points[i], dt) {
			lightsMoved = true
		}
	}
	for i := range lvl.Lights.Directional {
		if lvl.Lights.Directional[i].Waypoints != nil && advanceWaypoints(lvl, &lvl.Lights.Directional[i], dt) {
			lightsMoved = true
		}
	}
	return lightsMoved
}

// AdvanceWaypoints carries the entity dt seconds along the animation it is
// following, if any, and reports whether its placement was written. Called by
// EntityUpdate where it steps the entity's tweens, and for the same reasons.
func (e *Entity) AdvanceWaypoints(lvl *Level, dt float32) bool {
	if e.Waypoints == nil {
		return false
	}
	return advanceWaypoints(lvl, e, dt)
}

// FindWaypointFollower returns the object of the given kind and ID as a
// follower, or nil. A light is looked for among the directional lights first,
// the order a script's ref resolves them in.
func FindWaypointFollower(lvl *Level, kind hooks.WaypointObject, id string) WaypointFollower {
	if lvl == nil || id == "" {
		return nil
	}
	switch kind {
	case hooks.WaypointBrush:
		if ch, b := FindBrushByID(lvl, id); b != nil {
			return brushFollower{ch, b}
		}
	case hooks.WaypointEntity:
		if e := FindEntity(lvl, id); e != nil {
			return e
		}
	case hooks.WaypointChunk:
		if ch := FindChunkByID(lvl, id); ch != nil {
			return ch
		}
	case hooks.WaypointSprite:
		if s := FindSprite(lvl, id); s != nil {
			return s
		}
	case hooks.WaypointEmitter:
		if e := FindEmitter(lvl, id); e != nil {
			return e
		}
	case hooks.WaypointSound:
		if s := FindSound(lvl, id); s != nil {
			return s
		}
	case hooks.WaypointVolume:
		if v := FindVolume(lvl, id); v != nil {
			return v
		}
	case hooks.WaypointLight:
		for i := range lvl.Lights.Directional {
			if lvl.Lights.Directional[i].ID == id {
				return &lvl.Lights.Directional[i]
			}
		}
		for i := range lvl.Lights.Points {
			if lvl.Lights.Points[i].ID == id {
				return &lvl.Lights.Points[i]
			}
		}
	}
	return nil
}

// ---------- the kinds ----------

// unitScale is the scale a pose reports for a kind with no scale of its own.
var unitScale = mgl32.Vec3{1, 1, 1}

func framesOf(lvl *Level) *FrameSet {
	if lvl == nil {
		return nil
	}
	return lvl.Frames
}

// fromAttached carries a world-space point into the space an attached object's
// Position is authored in: the inverse of what Attach.Matrix carries out.
func fromAttached(lvl *Level, a Attach, p mgl32.Vec3) mgl32.Vec3 {
	if a.IsZero() {
		return p
	}
	return mulPoint(a.Matrix(lvl).Inv(), p)
}

// brushFollower is a brush with the chunk its placement is measured in, which
// is the one thing a brush needs that no other kind does.
type brushFollower struct {
	ch *Chunk
	b  *Brush
}

// BrushWaypointFollower is brush b of chunk ch, as a follower.
func BrushWaypointFollower(ch *Chunk, b *Brush) WaypointFollower { return brushFollower{ch, b} }

func (f brushFollower) WaypointPose(*Level) WaypointPose { return f.b.WaypointPose(f.ch) }
func (f brushFollower) waypoints() **WaypointPlayback    { return &f.b.Waypoints }
func (f brushFollower) applyWaypointPose(_ *Level, p WaypointPose) {
	f.b.applyWaypointPose(f.ch, p)
}
func (f brushFollower) waypointSource() (hooks.WaypointObject, string) {
	return hooks.WaypointBrush, f.b.ID
}
func (f brushFollower) dropTweens() { f.b.Tweens = Tweens{} }
func (f brushFollower) waypointRest() WaypointRest {
	return WaypointRest{Position: f.b.Offset, Rotation: f.b.Rotation, Scale: f.b.Scale}
}
func (f brushFollower) setWaypointRest(r WaypointRest) {
	f.b.Offset, f.b.Rotation, f.b.Scale = r.Position, r.Rotation, r.Scale
}

// Entity: all three channels, on the tween's own writers, so the frame an
// entity rides is divided out and its scale clamped the way `move_to` and
// `scale_to` do it.

func (e *Entity) WaypointPose(lvl *Level) WaypointPose {
	return WaypointPose{Position: framesOf(lvl).WorldPosition(e), Rotation: e.Rotation, Scale: e.EffectiveScale()}
}
func (e *Entity) waypoints() **WaypointPlayback { return &e.Waypoints }
func (e *Entity) applyWaypointPose(lvl *Level, p WaypointPose) {
	fs := framesOf(lvl)
	e.setTweenChannel(fs, TweenMove, p.Position)
	e.setTweenChannel(fs, TweenRotate, p.Rotation)
	e.setTweenChannel(fs, TweenScale, p.Scale)
}
func (e *Entity) waypointSource() (hooks.WaypointObject, string) { return hooks.WaypointEntity, e.ID }
func (e *Entity) dropTweens()                                    { e.Tweens = Tweens{} }
func (e *Entity) waypointRest() WaypointRest {
	return WaypointRest{Position: e.Position, Rotation: e.Rotation, Scale: e.Scale}
}
func (e *Entity) setWaypointRest(r WaypointRest) {
	e.Position, e.Rotation, e.Scale = r.Position, r.Rotation, r.Scale
}

// Chunk: its own Origin and Rotation, the two channels a script may move it
// on either (tween.go, `move_to`/`rotate_to`) — scale stays read-only to a
// script, so a path leaves it alone the way it leaves a point light's alone,
// and Rotation is a single Y-axis angle, so only a point's Y channel is read,
// the way a directional light reads only its own two.
//
// applyWaypointPose goes through moveOriginTo/rotateTo, the same two writers
// `move_to`/`rotate_to` use, so a chunk on a path carries its AttachedChunk
// riders exactly as one eased or driven by Velocity does.

func (ch *Chunk) WaypointPose(*Level) WaypointPose {
	return WaypointPose{
		Position: ch.LocalToWorld(mgl32.Vec3{}),
		Rotation: mgl32.Vec3{0, ch.Rotation, 0},
		Scale:    unitScale,
	}
}
func (ch *Chunk) waypoints() **WaypointPlayback { return &ch.Waypoints }
func (ch *Chunk) applyWaypointPose(lvl *Level, p WaypointPose) {
	ch.moveOriginTo(lvl, ch.worldToOrigin(p.Position))
	ch.rotateTo(p.Rotation.Y())
}
func (ch *Chunk) waypointSource() (hooks.WaypointObject, string) { return hooks.WaypointChunk, ch.ID }
func (ch *Chunk) dropTweens()                                    { ch.OriginTween, ch.RotationTween = nil, nil }
func (ch *Chunk) waypointRest() WaypointRest {
	return WaypointRest{Position: ch.Origin, Rotation: mgl32.Vec3{0, ch.Rotation, 0}}
}
func (ch *Chunk) setWaypointRest(r WaypointRest) {
	ch.Origin, ch.Rotation = r.Position, r.Rotation.Y()
}

// worldToOrigin is WaypointPose's Position, inverted: the value to write to
// Origin so that LocalToWorld(zero) comes back to p. Undoes the owning
// frame's Xform and the scale it contributes (FrameScale) — the two things
// outside the chunk's own placement — and nothing else, since a translation
// alone does not read the chunk's own Rotation or Scale either.
func (ch *Chunk) worldToOrigin(p mgl32.Vec3) mgl32.Vec3 {
	if ch.InFrame() && ch.Xform != (mgl32.Mat4{}) {
		r := ch.Xform.Inv().Mul4x1(p.Vec4(1))
		p = mgl32.Vec3{r.X(), r.Y(), r.Z()}
	}
	if k := ch.EffectiveFrameScale(); k != 1 {
		p = p.Mul(1 / k)
	}
	return p
}

// Sprite, emitter and sound: a position that rides whatever they are attached
// to, and a rotation. A sprite also has a size to grow, so it takes the scale
// too, on its Scale channel (the quad's width by X, its height by Y).

func (s *Sprite) WaypointPose(lvl *Level) WaypointPose {
	return WaypointPose{Position: s.WorldPosition(lvl), Rotation: s.Rotation, Scale: s.EffectiveScale()}
}
func (s *Sprite) waypoints() **WaypointPlayback { return &s.Waypoints }
func (s *Sprite) applyWaypointPose(lvl *Level, p WaypointPose) {
	s.Position, s.Rotation = fromAttached(lvl, s.Attach, p.Position), p.Rotation
	s.Scale = ClampBrushScale(p.Scale)
}
func (s *Sprite) waypointSource() (hooks.WaypointObject, string) { return hooks.WaypointSprite, s.ID }
func (s *Sprite) dropTweens()                                    {}
func (s *Sprite) waypointRest() WaypointRest {
	return WaypointRest{Position: s.Position, Rotation: s.Rotation, Scale: s.Scale}
}
func (s *Sprite) setWaypointRest(r WaypointRest) {
	s.Position, s.Rotation, s.Scale = r.Position, r.Rotation, r.Scale
}

func (e *Emitter) WaypointPose(lvl *Level) WaypointPose {
	return WaypointPose{Position: e.WorldPosition(lvl), Rotation: e.Rotation, Scale: unitScale}
}
func (e *Emitter) waypoints() **WaypointPlayback { return &e.Waypoints }
func (e *Emitter) applyWaypointPose(lvl *Level, p WaypointPose) {
	e.Position, e.Rotation = fromAttached(lvl, e.Attach, p.Position), p.Rotation
}
func (e *Emitter) waypointSource() (hooks.WaypointObject, string) {
	return hooks.WaypointEmitter, e.ID
}
func (e *Emitter) dropTweens() {}
func (e *Emitter) waypointRest() WaypointRest {
	return WaypointRest{Position: e.Position, Rotation: e.Rotation}
}
func (e *Emitter) setWaypointRest(r WaypointRest) { e.Position, e.Rotation = r.Position, r.Rotation }

func (s *Sound) WaypointPose(lvl *Level) WaypointPose {
	return WaypointPose{Position: s.WorldPosition(lvl), Rotation: s.Rotation, Scale: unitScale}
}
func (s *Sound) waypoints() **WaypointPlayback { return &s.Waypoints }
func (s *Sound) applyWaypointPose(lvl *Level, p WaypointPose) {
	s.Position, s.Rotation = fromAttached(lvl, s.Attach, p.Position), p.Rotation
}
func (s *Sound) waypointSource() (hooks.WaypointObject, string) { return hooks.WaypointSound, s.ID }
func (s *Sound) dropTweens()                                    {}
func (s *Sound) waypointRest() WaypointRest {
	return WaypointRest{Position: s.Position, Rotation: s.Rotation}
}
func (s *Sound) setWaypointRest(r WaypointRest) { s.Position, s.Rotation = r.Position, r.Rotation }

// Volume: a position that rides whatever it is attached to, and a rotation. Its
// cage is built in its own space, so moving it needs no rebuild.

func (v *Volume) WaypointPose(lvl *Level) WaypointPose {
	return WaypointPose{Position: v.WorldPosition(lvl), Rotation: v.Rotation, Scale: unitScale}
}
func (v *Volume) waypoints() **WaypointPlayback { return &v.Waypoints }
func (v *Volume) applyWaypointPose(lvl *Level, p WaypointPose) {
	v.Position, v.Rotation = fromAttached(lvl, v.Attach, p.Position), p.Rotation
}
func (v *Volume) waypointSource() (hooks.WaypointObject, string) { return hooks.WaypointVolume, v.ID }
func (v *Volume) dropTweens()                                    {}
func (v *Volume) waypointRest() WaypointRest {
	return WaypointRest{Position: v.Position, Rotation: v.Rotation}
}
func (v *Volume) setWaypointRest(r WaypointRest) { v.Position, v.Rotation = r.Position, r.Rotation }

// PointLight: a position, which is all a point light has.

func (pl *PointLight) WaypointPose(lvl *Level) WaypointPose {
	return WaypointPose{Position: pl.WorldPosition(lvl), Scale: unitScale}
}
func (pl *PointLight) waypoints() **WaypointPlayback { return &pl.Waypoints }
func (pl *PointLight) applyWaypointPose(lvl *Level, p WaypointPose) {
	pl.Position = fromAttached(lvl, pl.Attach, p.Position)
}
func (pl *PointLight) waypointSource() (hooks.WaypointObject, string) {
	return hooks.WaypointLight, pl.ID
}
func (pl *PointLight) dropTweens()                    {}
func (pl *PointLight) waypointRest() WaypointRest     { return WaypointRest{Position: pl.Position} }
func (pl *PointLight) setWaypointRest(r WaypointRest) { pl.Position = r.Position }

// DirectionalLight: an aim, read off the arrow's rotation.

func (dl *DirectionalLight) WaypointPose(*Level) WaypointPose {
	return WaypointPose{Rotation: DirLightEuler(dl.Yaw, dl.Pitch), Scale: unitScale}
}
func (dl *DirectionalLight) waypoints() **WaypointPlayback { return &dl.Waypoints }
func (dl *DirectionalLight) applyWaypointPose(_ *Level, p WaypointPose) {
	m := EulerMatrixYXZ(p.Rotation)
	dl.Yaw, dl.Pitch = DirLightYawPitch(mgl32.Vec3{m[0], m[1], m[2]})
	dl.SyncDirection()
}
func (dl *DirectionalLight) waypointSource() (hooks.WaypointObject, string) {
	return hooks.WaypointLight, dl.ID
}
func (dl *DirectionalLight) dropTweens() {}
func (dl *DirectionalLight) waypointRest() WaypointRest {
	return WaypointRest{Yaw: dl.Yaw, Pitch: dl.Pitch}
}
func (dl *DirectionalLight) setWaypointRest(r WaypointRest) {
	dl.Yaw, dl.Pitch = r.Yaw, r.Pitch
	dl.SyncDirection()
}

// DirLightEuler is the rotation, in radians, that turns +X — the axis a
// waypoint's arrow points along — to shine the way a directional light with
// this yaw and pitch (in degrees) does.
//
// EulerMatrixYXZ carries +X to (cos z·cos y, sin z, −cos z·sin y) when the X
// turn is zero, and the light shines along (cos p·cos yaw, sin p, cos p·sin
// yaw), so the pitch is the Z turn and the yaw is the Y turn negated.
func DirLightEuler(yaw, pitch float32) mgl32.Vec3 {
	return mgl32.Vec3{0, -mgl32.DegToRad(yaw), mgl32.DegToRad(pitch)}
}
