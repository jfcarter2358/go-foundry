package editor

// editor_waypoint_preview.go — playing a waypoint animation on an object in the
// editor, without entering play.
//
// The preview poses its object through the engine's own world.SeekWaypoints,
// which reads the same WaypointAnimation.PoseAt the tick does, so what the
// author scrubs here is what play will do — for a dynamic brush, and for every
// other kind a path can carry.
//
// The one thing a brush never needed and every other kind does is putting the
// object back. A brush is posed through its placement, runtime state laid over
// the brush as authored; a sprite, a light or an entity is posed by writing the
// very Position a level file holds. So the preview keeps what it overwrote,
// gives it back when it lets go, and lends it back for as long as anything
// writes the level down — an undo snapshot, a save, a compile — so that a sprite
// saved mid-preview is not a sprite moved for good.

import (
	"foundry/internal/compile"
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/levelio"
	"foundry/internal/world"
)

// waypointTarget names an object an animation can be previewed on: its kind
// and its ID. By ID, for the reason the preview's animation is — an undo
// rebuilds the level, and an index would name whatever moved into the slot.
type waypointTarget struct {
	Kind hooks.WaypointObject
	ID   string
}

// waypointTargetOf names a follower.
func waypointTargetOf(f world.WaypointFollower) waypointTarget {
	kind, id := world.WaypointSource(f)
	return waypointTarget{Kind: kind, ID: id}
}

// label is how a target reads in a list: its kind, then its ID.
func (t waypointTarget) label() string {
	if t.ID == "" {
		return "(nothing)"
	}
	return hooks.WaypointObjectNames[t.Kind] + " " + t.ID
}

// follower is the object t names, or nil when it has gone — or when it is one
// a script could not send along a path either: a brush that is not dynamic, or
// a vehicle.
func (t waypointTarget) follower(lvl *world.Level) world.WaypointFollower {
	switch t.Kind {
	case hooks.WaypointBrush:
		if _, b := world.FindBrushByID(lvl, t.ID); b == nil || !b.Dynamic {
			return nil
		}
	case hooks.WaypointEntity:
		if e := world.FindEntity(lvl, t.ID); e == nil || e.IsVehicle() {
			return nil
		}
	}
	return world.FindWaypointFollower(lvl, t.Kind, t.ID)
}

// editorWaypointTargets lists every object an animation can be previewed on:
// the dynamic brushes, then the entities that are not vehicles, the sprites,
// emitters, sounds, lights and volumes, each in level order.
func editorWaypointTargets(lvl *world.Level) []waypointTarget {
	if lvl == nil {
		return nil
	}
	var out []waypointTarget
	add := func(kind hooks.WaypointObject, id string) {
		if id != "" {
			out = append(out, waypointTarget{Kind: kind, ID: id})
		}
	}
	for ci := range lvl.Chunks {
		for bi := range lvl.Chunks[ci].Brushes {
			if b := &lvl.Chunks[ci].Brushes[bi]; b.Dynamic {
				add(hooks.WaypointBrush, b.ID)
			}
		}
	}
	for i := range lvl.Entities {
		if !lvl.Entities[i].IsVehicle() {
			add(hooks.WaypointEntity, lvl.Entities[i].ID)
		}
	}
	for i := range lvl.Sprites {
		add(hooks.WaypointSprite, lvl.Sprites[i].ID)
	}
	for i := range lvl.Emitters {
		add(hooks.WaypointEmitter, lvl.Emitters[i].ID)
	}
	for i := range lvl.Sounds {
		add(hooks.WaypointSound, lvl.Sounds[i].ID)
	}
	for i := range lvl.Lights.Directional {
		add(hooks.WaypointLight, lvl.Lights.Directional[i].ID)
	}
	for i := range lvl.Lights.Points {
		add(hooks.WaypointLight, lvl.Lights.Points[i].ID)
	}
	for i := range lvl.Volumes {
		add(hooks.WaypointVolume, lvl.Volumes[i].ID)
	}
	return out
}

// waypointPreviewRestPose is where the preview's object was authored, in a
// waypoint's terms, and the object — or nil, with nothing to preview on.
func waypointPreviewRestPose(lvl *world.Level) (world.WaypointPose, world.WaypointFollower) {
	p := &Editor.WaypointPreview
	f := p.Target.follower(lvl)
	switch {
	case f == nil:
		return world.WaypointPose{}, nil
	case p.Posed:
		return p.restPose, f
	}
	return f.WaypointPose(lvl), f
}

// editorWaypointPreviewStart plays an animation on an object in the editor
// from the start, the object setting off from where it was authored — the
// place play starts it from.
func editorWaypointPreviewStart(animID string, t waypointTarget) bool {
	lvl := Editor.level
	if t.follower(lvl) == nil {
		switch _, b := world.FindBrushByID(lvl, t.ID); {
		case t.ID == "":
			editorSetStatus("Preview: pick an object to play the animation on")
		case t.Kind == hooks.WaypointBrush && b != nil:
			editorSetStatus("Preview: brush %q is not dynamic, so a script could not move it either", t.ID)
		case t.Kind == hooks.WaypointEntity && world.FindEntity(lvl, t.ID) != nil:
			editorSetStatus("Preview: entity %q is a vehicle, and its drive model owns its motion", t.ID)
		default:
			editorSetStatus("Preview: this level has no %s", t.label())
		}
		return false
	}
	if anim := world.FindWaypointAnimation(lvl, animID); world.WaypointFollowerCarriesAnimation(t.follower(lvl), anim) {
		editorSetStatus("Preview: %s carries %q, so it cannot follow it; preview it on one of its parts", t.label(), animID)
		return false
	}
	editorWaypointPreviewStop()
	Editor.WaypointPreview.Target = t
	return editorWaypointPreviewPlay(animID)
}

// editorWaypointPreviewPlay runs the playhead from where it is — or from the
// start, when it has already reached the end. An object chosen to preview on
// is posed as it goes; with none, the ghost arrow is the preview.
func editorWaypointPreviewPlay(animID string) bool {
	anim := world.FindWaypointAnimation(Editor.level, animID)
	if anim == nil || len(anim.Points) == 0 {
		editorSetStatus("Preview: waypoint animation %q has no points", animID)
		return false
	}
	p := &Editor.WaypointPreview
	if p.Anim != animID {
		editorWaypointPreviewStop()
		p.Anim = animID
	}
	if p.Time >= anim.EffectiveLength() {
		p.Time = 0
	}
	p.Playing, p.held = true, 0
	editorWaypointPreviewPose()
	return true
}

// editorWaypointPreviewPause stops the playhead where it is, and leaves the
// object posed there.
func editorWaypointPreviewPause() { Editor.WaypointPreview.Playing = false }

// editorWaypointPreviewStop ends a preview: the playhead goes back to the start
// and the object back where it was authored. Safe to call with nothing playing.
func editorWaypointPreviewStop() {
	p := &Editor.WaypointPreview
	p.Playing, p.Time, p.held = false, 0, 0
	editorWaypointPreviewRelease()
}

// editorWaypointPreviewRelease puts the preview's object back where it was
// authored and leaves the playhead alone — for choosing another object
// mid-preview, as well as for Stop.
func editorWaypointPreviewRelease() {
	p := &Editor.WaypointPreview
	if !p.Posed {
		return
	}
	p.Posed = false
	// Found without follower's gate: a brush unticked Dynamic mid-preview
	// still has to go back.
	if f := world.FindWaypointFollower(Editor.level, p.Target.Kind, p.Target.ID); f != nil {
		world.StopWaypoints(f)
		world.RestoreWaypointRest(f, p.rest)
		waypointPreviewMoved(Editor.level, p.Target)
	}
}

// editorWaypointPreviewSeek moves the playhead to t, pausing it — a scrub is
// the author taking the playhead in hand — and poses the object there.
func editorWaypointPreviewSeek(t float32) {
	p := &Editor.WaypointPreview
	anim := world.FindWaypointAnimation(Editor.level, p.Anim)
	if anim == nil {
		return
	}
	p.Playing, p.held = false, 0
	p.Time = geom.ClampValue(t, 0, anim.EffectiveLength())
	editorWaypointPreviewPose()
}

// editorSetWaypointAnimationLength stretches an animation to a new length, and
// the playhead with it, so the playhead stays on the same moment of the path.
func editorSetWaypointAnimationLength(a *world.WaypointAnimation, l float32) {
	if editorWaypointRefuseCarried(a) {
		return
	}
	old := a.EffectiveLength()
	a.SetLength(l)
	if p := &Editor.WaypointPreview; p.Anim == a.ID && old > 0 {
		p.Time = geom.ClampValue(p.Time*a.EffectiveLength()/old, 0, a.EffectiveLength())
	}
}

// editorWaypointPreviewPose puts the preview object where the playhead has the
// path, through the engine's own world.SeekWaypoints. An object that has gone,
// or has stopped being one a path could carry, is left alone.
//
// What a move needs afterwards is done only when the pose really changed: this
// runs every frame a preview holds its object, and a paused one holding still
// should cost nothing.
func editorWaypointPreviewPose() {
	lvl := Editor.level
	p := &Editor.WaypointPreview
	anim := world.FindWaypointAnimation(lvl, p.Anim)
	f := p.Target.follower(lvl)
	if anim == nil || f == nil || world.WaypointFollowerCarriesAnimation(f, anim) {
		return
	}
	before := world.CaptureWaypointRest(f)
	if !p.Posed || world.FollowedWaypointAnimation(f) == "" {
		// Sent from where it was authored, as play sends it: put back there
		// first if the preview already holds it, and remembered there if not.
		if p.Posed {
			world.RestoreWaypointRest(f, p.rest)
		} else {
			p.rest = before
		}
		p.restPose = f.WaypointPose(lvl)
		world.PlayWaypoints(lvl, f, anim)
		p.Posed = true
	}
	world.SeekWaypoints(lvl, f, anim, p.Time)
	if before != world.CaptureWaypointRest(f) {
		waypointPreviewMoved(lvl, p.Target)
	}
}

// waypointPreviewMoved does what moving an object needs afterwards, for the
// kinds that need anything: a brush's chunk bounds and the spatial hash, which
// key off where the brush is, and the light uniforms, which are not re-read
// every frame.
func waypointPreviewMoved(lvl *world.Level, t waypointTarget) {
	switch t.Kind {
	case hooks.WaypointBrush:
		if ch, _ := world.FindBrushByID(lvl, t.ID); ch != nil {
			ch.RecomputeBounds()
			world.RebuildSpatialHash(lvl)
		}
	case hooks.WaypointLight:
		// Quietly: the verbose upload logs a line, and a playing preview
		// uploads every frame.
		world.UploadLightsTo(gfx.Program, lvl.Lights, false)
	}
}

// editorWaypointPreviewTick runs the playhead on by dt and keeps the object on
// it. Called once a frame from EditorRender, and only does anything in the
// editor: play moves objects itself, on their own kinds' ticks.
//
// The object is re-posed every frame it is held, playing or not, so a point
// dragged or retimed while the preview is paused carries the object with it.
// It also means the object cannot be moved by hand while it is held: whatever
// the gizmo writes, the next frame's pose overwrites.
func editorWaypointPreviewTick(dt float32) {
	lvl := Editor.level
	if lvl == nil || !Editor.Active {
		return
	}
	editorWaypointPreviewFollowSelection()
	p := &Editor.WaypointPreview
	if p.Playing && dt > 0 {
		anim := world.FindWaypointAnimation(lvl, p.Anim)
		if anim == nil {
			editorWaypointPreviewStop()
			return
		}
		switch length := anim.EffectiveLength(); {
		case p.Time < length:
			p.Time += dt
			if p.Time > length {
				p.Time = length
			}
		case !p.Loop:
			// Arrived: the object holds the end, and Play goes round again.
			p.Playing = false
		default:
			p.held += dt
			if p.held >= waypointPreviewLoopPause {
				p.Time, p.held = 0, 0
			}
		}
	}
	if p.Playing || p.Posed {
		editorWaypointPreviewPose()
	}
}

// editorWaypointPreviewFollowSelection points the preview at the selected
// animation. The timeline shows the selected one, so a playhead left on another
// would be scrubbing something off screen; switching puts the old animation's
// object back and starts the new one from the top. With no animation selected
// the preview is left alone — an object's own panel previews without one.
func editorWaypointPreviewFollowSelection() {
	a := activeWaypointAnimation()
	if p := &Editor.WaypointPreview; a != nil && p.Anim != a.ID {
		editorWaypointPreviewStop()
		p.Anim = a.ID
	}
}

// editorWaypointPreviewing reports whether a preview of animID is running or
// holding its object.
func editorWaypointPreviewing(animID string) bool {
	p := &Editor.WaypointPreview
	return (p.Playing || p.Posed) && p.Anim == animID
}

// addWaypointAnimationFromObject starts a new animation whose first point is
// the object as it was authored — where it stands, turned and scaled as it is —
// so the path begins where the object already is and every point after it is
// somewhere to go, each a Ctrl+click away. The preview is pointed at the object,
// ready to play.
//
// A directional light stands nowhere, so its first point goes a few units in
// front of the camera, turned the way the light shines.
func addWaypointAnimationFromObject(t waypointTarget) {
	lvl := Editor.level
	f := t.follower(lvl)
	if f == nil {
		return
	}
	pose := f.WaypointPose(lvl)
	p := &Editor.WaypointPreview
	if p.Posed && p.Target == t {
		pose = p.restPose
	}
	if !world.WaypointTakesPosition(f) {
		pose.Position = gfx.Cam.Position.Add(gfx.Cam.Direction.Mul(4))
	}
	// Whatever the preview held goes back first: the new selection would
	// otherwise move the preview on to the new animation without releasing it.
	editorWaypointPreviewStop()
	addWaypointAnimationAt(pose.Position, t.ID+"_path")
	if a := activeWaypointAnimation(); a != nil && len(a.Points) == 1 {
		a.Points[0].Rotation = pose.Rotation
		a.Points[0].Scale = world.ClampBrushScale(pose.Scale)
		p.Target, p.Anim = t, a.ID
	}
}

// ---------- writing the level down ----------

// withWaypointPreviewAtRest runs fn with the preview's object back where it was
// authored, then poses it again: for anything that writes the level down. A
// brush needs no lending — its placement is never written — so only the other
// kinds are.
func withWaypointPreviewAtRest(fn func()) {
	p := &Editor.WaypointPreview
	var f world.WaypointFollower
	if p.Posed && p.Target.Kind != hooks.WaypointBrush {
		f = world.FindWaypointFollower(Editor.level, p.Target.Kind, p.Target.ID)
	}
	if f == nil {
		fn()
		return
	}
	posed := world.CaptureWaypointRest(f)
	world.RestoreWaypointRest(f, p.rest)
	defer world.RestoreWaypointRest(f, posed)
	fn()
}

// editorLevelToRaw is levelio.LevelToRaw for the open level, as authored.
func editorLevelToRaw() (world.RawLevel, error) {
	var raw world.RawLevel
	var err error
	withWaypointPreviewAtRest(func() { raw, err = levelio.LevelToRaw(Editor.level) })
	return raw, err
}

// editorSaveLevel is levelio.SaveLevel, as authored.
func editorSaveLevel(lvl *world.Level, path string) error {
	var err error
	withWaypointPreviewAtRest(func() { err = levelio.SaveLevel(lvl, path) })
	return err
}

// editorCompile is levelio.CompileLevel with the default options, as authored:
// a point light held on a path would otherwise be baked where the preview had
// it.
func editorCompile(lvl *world.Level) (*levelio.CompiledLevel, error) {
	var c *levelio.CompiledLevel
	var err error
	withWaypointPreviewAtRest(func() { c, err = levelio.CompileLevel(lvl, compile.DefaultOptions()) })
	return c, err
}
