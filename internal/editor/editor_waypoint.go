package editor

// editor_waypoint.go — authoring waypoint animations: selecting a point,
// placing a path a click at a time, and picking and drawing the arrows.
//
// The panel is editor_waypoint_panel.go, the timeline docked under the view is
// editor_waypoint_timeline.go, and previewing an animation on an object without
// entering play is editor_waypoint_preview.go. The animation itself — what a
// point means to the object that reaches it, and when it is reached — is
// world/waypoint.go and world/waypoint_follow.go.
//
// A selection is an animation and, optionally, one point inside it. With a
// point selected the gizmo moves, turns and scales that point; with only the
// animation selected, the move gizmo carries the whole path, which is how a
// lift route authored in the wrong place gets put in the right one.

import (
	"math"

	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	// waypointMarkerRadius is the clickable disc at every point, in world
	// units — the sound marker's size, so the two feel the same to click.
	waypointMarkerRadius = float32(0.3)
	// waypointPreviewLoopPause is how long a looping preview holds the end of
	// the path before it starts again, so the arrival is there to be seen.
	waypointPreviewLoopPause = float32(0.75)
	// waypointNewLegSpeed is the pace a point added on the end of a path is
	// timed at, in units per second: a brisk walk, slow enough to watch a first
	// preview of the path. Only a starting guess — the timeline retimes it.
	waypointNewLegSpeed = float32(2)
	// waypointMinNewLeg is the shortest leg, in seconds, a point added on the
	// end is given, so one placed on top of the last still has a key of its own
	// on the timeline rather than one hidden under its neighbour's.
	waypointMinNewLeg = float32(0.25)
)

var (
	waypointArrowColor      = mgl32.Vec3{0.3, 0.85, 1}
	waypointDimColor        = mgl32.Vec3{0.2, 0.45, 0.55}
	waypointSelColor        = mgl32.Vec3{1, 1, 0}
	waypointPreviewLegColor = mgl32.Vec3{0.95, 0.55, 0.2}
)

// waypointPreview is the editor's playback of one animation: a playhead, and
// optionally an object posed wherever the playhead has the path.
//
// Both are named by ID rather than by index: an undo rebuilds the level, and a
// preview that outlived one would otherwise be pointing at whatever moved into
// the slot.
type waypointPreview struct {
	// Target is the object the preview poses; its zero value is none.
	Target waypointTarget
	Anim   string
	// Time is the playhead, in seconds into Anim. The timeline draws and scrubs
	// it; the ghost arrow and the object are posed at it.
	Time float32
	// Playing is true while the playhead runs.
	Playing bool
	// Posed is true while the preview holds the object somewhere along the
	// path. It outlasts Playing — a paused or scrubbed preview leaves the object
	// where the playhead is — and only Stop, and anything that rebuilds the
	// level, puts the object back.
	Posed bool
	// Loop starts the animation again from the start once the playhead has
	// held the end for waypointPreviewLoopPause.
	Loop bool
	held float32
	// rest is the target's fields as the preview found them, to give back when
	// it lets go, and restPose the same place in a waypoint's terms — where the
	// path sets off from. Both are valid while Posed.
	rest     world.WaypointRest
	restPose world.WaypointPose
}

// ---------- selection ----------

// activeWaypointAnimation returns the selected animation, or nil.
func activeWaypointAnimation() *world.WaypointAnimation {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveWaypointAnimIdx < 0 || Editor.ActiveWaypointAnimIdx >= len(lvl.WaypointAnimations) {
		return nil
	}
	return &lvl.WaypointAnimations[Editor.ActiveWaypointAnimIdx]
}

// activeWaypoint returns the selected point, or nil when no point is — which
// includes an animation selected as a whole.
func activeWaypoint() *world.Waypoint {
	a := activeWaypointAnimation()
	if a == nil || Editor.ActiveWaypointIdx < 0 || Editor.ActiveWaypointIdx >= len(a.Points) {
		return nil
	}
	return &a.Points[Editor.ActiveWaypointIdx]
}

// editorSelectWaypointAnimation selects an animation as a whole: the panel
// shows it, and the move gizmo carries every point at once.
func editorSelectWaypointAnimation(ai int) {
	lvl := Editor.level
	if lvl == nil || ai < 0 || ai >= len(lvl.WaypointAnimations) {
		return
	}
	editorClearObjectSelections()
	Editor.ActiveWaypointAnimIdx = ai
	Editor.ActiveWaypointIdx = -1
	Editor.GizmoKind = waypointGizmoKind(&lvl.WaypointAnimations[ai], gizmoKindWaypointAnim)
	Editor.PanelFocus = panelFocusWaypoint
}

// waypointGizmoKind is the gizmo a selection in a gives: kind, or none for a
// path an entity carries, whose points are the type's and are written in the
// entity's space — a drag here would move them in the wrong space, on a copy
// the next re-stamp throws away.
func waypointGizmoKind(a *world.WaypointAnimation, kind string) string {
	if waypointCarried(a) {
		return gizmoKindNone
	}
	return kind
}

// waypointCarried reports whether a is a path an entity's type carries
// (world/entity_waypoints.go) rather than one the level authored.
func waypointCarried(a *world.WaypointAnimation) bool { return a != nil && a.FromEntity != "" }

// editorWaypointRefuseCarried reports whether a is carried, saying why it cannot
// be edited here when it is. Every edit in this file and the panel's asks first;
// selecting, previewing and scrubbing a carried path are all still allowed.
func editorWaypointRefuseCarried(a *world.WaypointAnimation) bool {
	if !waypointCarried(a) {
		return false
	}
	editorSetStatus("%q is carried by entity %s: edit its type's paths in the entity editor", a.ID, a.FromEntity)
	return true
}

// waypointSelKeep is the level editor's waypoint selection held by animation ID
// across a change to the animation list that is not an undo: an entity's paths
// dropped and materialised again, which shifts every index after them.
type waypointSelKeep struct {
	anim  string
	point int
}

// editorDropEntityWaypointAnimations drops the paths materialised from the named
// entity and puts the waypoint selection back on whatever it named, returning
// what it named so a caller that materialises them again can restore it once
// more.
func editorDropEntityWaypointAnimations(lvl *world.Level, entityID string) waypointSelKeep {
	keep := waypointSelKeep{point: Editor.ActiveWaypointIdx}
	if a := activeWaypointAnimation(); a != nil && lvl == Editor.level {
		keep.anim = a.ID
	}
	if world.DropEntityWaypointAnimations(lvl, entityID) > 0 {
		keep.restore(lvl)
	}
	return keep
}

// restore re-points the selection at the animation k names, quietly — nothing
// else the author had selected is touched — or drops it if that animation has
// gone.
func (k waypointSelKeep) restore(lvl *world.Level) {
	if lvl == nil || lvl != Editor.level || k.anim == "" {
		return
	}
	for ai := range lvl.WaypointAnimations {
		if a := &lvl.WaypointAnimations[ai]; a.ID == k.anim {
			Editor.ActiveWaypointAnimIdx = ai
			Editor.ActiveWaypointIdx = k.point
			if k.point >= len(a.Points) {
				Editor.ActiveWaypointIdx = len(a.Points) - 1
			}
			return
		}
	}
	editorClearWaypointSelection()
}

// editorSelectWaypoint selects one point of an animation, which gives it the
// move, rotate and scale gizmos.
func editorSelectWaypoint(ai, pi int) {
	lvl := Editor.level
	if lvl == nil || ai < 0 || ai >= len(lvl.WaypointAnimations) ||
		pi < 0 || pi >= len(lvl.WaypointAnimations[ai].Points) {
		return
	}
	editorClearObjectSelections()
	Editor.ActiveWaypointAnimIdx = ai
	Editor.ActiveWaypointIdx = pi
	Editor.GizmoKind = waypointGizmoKind(&lvl.WaypointAnimations[ai], gizmoKindWaypoint)
	Editor.PanelFocus = panelFocusWaypoint
}

// editorClearWaypointSelection drops the animation and point from the
// selection, taking the gizmo with them.
func editorClearWaypointSelection() {
	Editor.ActiveWaypointAnimIdx = -1
	Editor.ActiveWaypointIdx = -1
	if Editor.GizmoKind == gizmoKindWaypoint || Editor.GizmoKind == gizmoKindWaypointAnim {
		Editor.GizmoKind = gizmoKindNone
	}
}

// activeGizmoWaypoint returns the point the gizmo drags, or nil. Gated on the
// gizmo kind, as every activeGizmoX is, so a stale index left by another tool
// cannot be dragged.
func activeGizmoWaypoint() *world.Waypoint {
	if Editor.GizmoKind != gizmoKindWaypoint {
		return nil
	}
	return activeWaypoint()
}

// activeGizmoWaypointAnimation returns the animation the gizmo carries whole,
// or nil.
func activeGizmoWaypointAnimation() *world.WaypointAnimation {
	if Editor.GizmoKind != gizmoKindWaypointAnim {
		return nil
	}
	return activeWaypointAnimation()
}

// editorReselectWaypoint puts a waypoint selection back after an undo or redo
// has rebuilt the level: found by the animation's ID rather than its index,
// with the point clamped to what the snapshot holds.
//
// Every other selection is simply dropped by a snapshot swap. This one is kept
// because a path is laid by Ctrl+clicking after the selected point: taking back
// the point just added has to leave the one before it selected, or the next
// Ctrl+click has nothing to add after and the path has to be found again first.
func editorReselectWaypoint(animID string, point int) {
	lvl := Editor.level
	if lvl == nil || animID == "" {
		return
	}
	focus := Editor.PanelFocus
	defer func() { Editor.PanelFocus = focus }()
	for ai := range lvl.WaypointAnimations {
		a := &lvl.WaypointAnimations[ai]
		if a.ID != animID {
			continue
		}
		switch {
		case len(a.Points) == 0:
		case point < 0:
			editorSelectWaypointAnimation(ai)
		default:
			if point >= len(a.Points) {
				point = len(a.Points) - 1
			}
			editorSelectWaypoint(ai, point)
		}
		return
	}
}

// ---------- placement ----------

// editorArmWaypointPlacement makes the next click in the view start a new
// waypoint animation with one point where it lands. One click, like every other
// placement mode: the points after the first are Ctrl+clicks, which need no
// mode at all (editorWaypointCtrlClick).
func editorArmWaypointPlacement() {
	editorDisarmBrush()
	editorDisarmBrushPlacement()
	Editor.EntityPlacementMode = false
	Editor.PrefabPlacementMode = false
	Editor.VolumePlacementMode = false
	Editor.SpritePlacementMode = false
	Editor.EmitterPlacementMode = false
	Editor.SoundPlacementMode = false
	Editor.EffectPlacementMode = false
	Editor.SpawnPlacementMode = false
	Editor.WaypointPlacementMode = true
}

// editorDisarmWaypointPlacement puts the placement mode away.
func editorDisarmWaypointPlacement() { Editor.WaypointPlacementMode = false }

// placeWaypointAt answers the click placement was armed for: a new animation
// with a single point at pos, selected, and the mode put away.
//
// A point goes where the click landed. It is where the brush's CENTRE will be,
// which for a door is somewhere off the floor, but guessing a height would be
// wrong for every path that is not a door; the move gizmo is one drag away.
func placeWaypointAt(pos mgl32.Vec3) {
	Editor.WaypointPlacementMode = false
	addWaypointAnimationAt(pos, Editor.NewWaypointAnimID)
}

// editorWaypointCtrlClick answers a Ctrl+click in the 3D view. With a waypoint
// selected it adds the next point where the click landed, straight after the
// selected one, and selects that — so a path is laid by placing its first point
// and Ctrl+clicking the rest, and Ctrl+clicking with an earlier point selected
// inserts into the middle. Reports whether it took the click: with no waypoint
// selected, Ctrl+click keeps meaning multi-select.
func editorWaypointCtrlClick(mx, my int32) bool {
	if ws().level != Editor.level || activeWaypointAnimation() == nil {
		return false
	}
	return insertWaypointAt(editor3DPointAt(mx, my))
}

// addWaypointAnimationAt starts a new animation with one point at pos, named
// after base (made unique), and selects the point.
func addWaypointAnimationAt(pos mgl32.Vec3, base string) {
	lvl := Editor.level
	if lvl == nil {
		return
	}
	EditorPushUndo()
	a := world.WaypointAnimation{
		ID:     world.UniqueWaypointAnimationID(lvl, base),
		Points: []world.Waypoint{world.NewWaypoint(pos)},
	}
	lvl.WaypointAnimations = append(lvl.WaypointAnimations, a)
	editorSelectWaypoint(len(lvl.WaypointAnimations)-1, 0)
	editorSetStatus("Started waypoint animation %q", a.ID)
}

// insertWaypointAt adds a point at pos just after the selected one — or at the
// end, with no point selected — and selects it.
//
// The new point carries on the rotation and scale of the one before it. A path
// is mostly a route, and making every point start from no turn and no scale
// would undo the setting an author just dialled in on the previous one. Its
// time is timeWaypointInsert's.
func insertWaypointAt(pos mgl32.Vec3) bool {
	a := activeWaypointAnimation()
	if a == nil {
		return false
	}
	if editorWaypointRefuseCarried(a) {
		// Still taken: a Ctrl+click aimed at extending the path is not a
		// request to multi-select either.
		return true
	}
	EditorPushUndo()
	at := len(a.Points)
	if Editor.ActiveWaypointIdx >= 0 && Editor.ActiveWaypointIdx < len(a.Points) {
		at = Editor.ActiveWaypointIdx + 1
	}
	w := world.NewWaypoint(pos)
	if at > 0 {
		prev := a.Points[at-1]
		w.Rotation, w.Scale = prev.Rotation, prev.Scale
	}
	w.Time = timeWaypointInsert(a, at, pos)
	insertWaypoint(a, at, w)
	editorSelectWaypoint(Editor.ActiveWaypointAnimIdx, at)
	editorSetStatus("Added point %d to %q", at+1, a.ID)
	return true
}

// timeWaypointInsert is when a point inserted at index at, going to pos, is
// reached — and it lengthens the animation when the point goes on the end.
//
// Between two points it takes the moment halfway between theirs, so no other
// point's timing moves. On the end it is a new leg after the last point, timed
// at waypointNewLegSpeed, and the length grows by the same leg, so a hold the
// author left at the end stays the hold it was.
func timeWaypointInsert(a *world.WaypointAnimation, at int, pos mgl32.Vec3) float32 {
	if at < len(a.Points) {
		lo := float32(0)
		if at > 0 {
			lo = a.Points[at-1].Time
		}
		return (lo + a.Points[at].Time) / 2
	}
	if at == 0 {
		return 0
	}
	last := &a.Points[at-1]
	leg := last.Position.Sub(pos).Len() / waypointNewLegSpeed
	if leg < waypointMinNewLeg {
		leg = waypointMinNewLeg
	}
	a.Length = a.EffectiveLength() + leg
	return last.Time + leg
}

// insertWaypoint puts w into the path at index at.
func insertWaypoint(a *world.WaypointAnimation, at int, w world.Waypoint) {
	a.Points = append(a.Points, world.Waypoint{})
	copy(a.Points[at+1:], a.Points[at:])
	a.Points[at] = w
}

// insertWaypointAtPlayhead adds a point where the playhead has the path, at the
// playhead's time, and selects it: the keyframe gesture — scrub to a moment, key
// it, then move, turn or scale the new point with the gizmo. Keying alone does
// not change the path, since the point lands exactly where the path already
// passes at that moment.
//
// A moment that already has a point selects that point instead of stacking a
// second one on it.
func insertWaypointAtPlayhead(a *world.WaypointAnimation) {
	lvl := Editor.level
	if lvl == nil || a == nil || len(a.Points) == 0 || editorWaypointRefuseCarried(a) {
		return
	}
	t := Editor.WaypointPreview.Time
	at := len(a.Points)
	for i := range a.Points {
		if geom.Abs32(a.Points[i].Time-t) <= 1e-4 {
			editorSelectWaypoint(Editor.ActiveWaypointAnimIdx, i)
			return
		}
		if a.Points[i].Time > t {
			at = i
			break
		}
	}
	pose := a.PoseAt(waypointPreviewStartPose(lvl, a), t)
	EditorPushUndo()
	insertWaypoint(a, at, world.Waypoint{
		Position: pose.Position,
		Rotation: pose.Rotation,
		Scale:    world.ClampBrushScale(pose.Scale),
		Time:     t,
	})
	editorSelectWaypoint(Editor.ActiveWaypointAnimIdx, at)
	editorSetStatus("Keyed point %d of %q at %.2f s", at+1, a.ID, t)
}

// addWaypointAhead adds a point after the selected one (or the last) without a
// click: carrying on in the direction and at the spacing the path was already
// going, or two units along +X from a path of one point.
func addWaypointAhead() {
	a := activeWaypointAnimation()
	if a == nil || len(a.Points) == 0 {
		return
	}
	after := Editor.ActiveWaypointIdx
	if after < 0 || after >= len(a.Points) {
		after = len(a.Points) - 1
	}
	Editor.ActiveWaypointIdx = after
	insertWaypointAt(waypointAheadPosition(a, after))
}

// waypointAheadPosition is where addWaypointAhead puts its point.
func waypointAheadPosition(a *world.WaypointAnimation, after int) mgl32.Vec3 {
	p := a.Points[after].Position
	if after > 0 {
		if step := p.Sub(a.Points[after-1].Position); step.Len() > 1e-4 {
			return p.Add(step)
		}
	}
	return p.Add(mgl32.Vec3{2, 0, 0})
}

// ---------- editing ----------

// deleteActiveWaypoint removes the selected point, joining the point before it
// straight to the point after — or removes the whole animation when it is
// selected as a whole or the point was its only one: an animation with no
// points sends a brush nowhere, and leaving one in the level would be a row in
// the Explorer that does nothing.
func deleteActiveWaypoint() {
	a := activeWaypointAnimation()
	if a == nil || editorWaypointRefuseCarried(a) {
		return
	}
	ai, pi := Editor.ActiveWaypointAnimIdx, Editor.ActiveWaypointIdx
	if pi < 0 || pi >= len(a.Points) || len(a.Points) == 1 {
		deleteWaypointAnimation(ai)
		return
	}
	EditorPushUndo()
	// The path is the order of the list, so taking one entry out is the whole
	// of re-linking it: the point before now leads to the point after.
	a.Points = append(a.Points[:pi], a.Points[pi+1:]...)
	editorSetStatus("Deleted point %d from %q", pi+1, a.ID)
	// The point before the gap is left selected, so a Ctrl+click puts a
	// replacement straight back into it. Deleting the first point leaves the
	// new first one selected instead.
	if pi > 0 {
		pi--
	}
	editorSelectWaypoint(ai, pi)
}

// deleteWaypointAnimation removes an animation outright.
func deleteWaypointAnimation(ai int) {
	lvl := Editor.level
	if lvl == nil || ai < 0 || ai >= len(lvl.WaypointAnimations) || editorWaypointRefuseCarried(&lvl.WaypointAnimations[ai]) {
		return
	}
	id := lvl.WaypointAnimations[ai].ID
	if Editor.WaypointPreview.Anim == id {
		// Put the object back first. The preview would otherwise stop itself
		// the next frame — the animation is gone — but leave the object
		// wherever it had got to.
		editorWaypointPreviewStop()
	}
	EditorPushUndo()
	lvl.WaypointAnimations = append(lvl.WaypointAnimations[:ai], lvl.WaypointAnimations[ai+1:]...)
	editorClearWaypointSelection()
	editorSetStatus("Deleted waypoint animation %q", id)
}

// duplicateWaypointAnimation copies an animation a unit along +X, so the copy
// is visibly a second path rather than one drawn over the first.
func duplicateWaypointAnimation(ai int) {
	lvl := Editor.level
	if lvl == nil || ai < 0 || ai >= len(lvl.WaypointAnimations) || editorWaypointRefuseCarried(&lvl.WaypointAnimations[ai]) {
		return
	}
	EditorPushUndo()
	cp := lvl.WaypointAnimations[ai].Clone()
	cp.ID = world.UniqueWaypointAnimationID(lvl, cp.ID)
	for i := range cp.Points {
		cp.Points[i].Position = cp.Points[i].Position.Add(mgl32.Vec3{1, 0, 0})
	}
	lvl.WaypointAnimations = append(lvl.WaypointAnimations, cp)
	editorSelectWaypointAnimation(len(lvl.WaypointAnimations) - 1)
	editorSetStatus("Duplicated waypoint animation as %q", cp.ID)
}

// moveActiveWaypoint swaps the selected point with its neighbour `by` places
// along — the order is the path, so this is re-routing it.
func moveActiveWaypoint(by int) {
	a := activeWaypointAnimation()
	pi := Editor.ActiveWaypointIdx
	to := pi + by
	if a == nil || pi < 0 || pi >= len(a.Points) || to < 0 || to >= len(a.Points) || editorWaypointRefuseCarried(a) {
		return
	}
	EditorPushUndo()
	// The times stay where they are in the list rather than travelling with the
	// points: the list is the path, and the times are when each place along it
	// is reached, so swapping two places must not make time run backwards.
	ti, tt := a.Points[pi].Time, a.Points[to].Time
	a.Points[pi], a.Points[to] = a.Points[to], a.Points[pi]
	a.Points[pi].Time, a.Points[to].Time = ti, tt
	editorSelectWaypoint(Editor.ActiveWaypointAnimIdx, to)
}

// renameActiveWaypointAnimation gives the selected animation a new ID, which
// has to be free across the whole level for `@id` to keep meaning one thing.
func renameActiveWaypointAnimation(id string) {
	lvl := Editor.level
	a := activeWaypointAnimation()
	if lvl == nil || a == nil || id == a.ID || editorWaypointRefuseCarried(a) {
		return
	}
	if id == "" {
		editorSetStatus("A waypoint animation needs an ID for a script to name it")
		return
	}
	if world.LevelIDTaken(lvl, id) {
		editorSetStatus("%q is already used by something else in this level", id)
		return
	}
	EditorPushUndo()
	// A running preview follows the rename rather than losing its animation.
	// Its object's own playback needs nothing: every pose is a seek, and a seek
	// names the animation it was handed.
	if p := &Editor.WaypointPreview; p.Anim == a.ID {
		p.Anim = id
	}
	a.ID = id
}

// moveActiveWaypointToCamera puts the selected point where the fly camera is,
// which is the quick way to hang one in mid-air.
func moveActiveWaypointToCamera() {
	p := activeWaypoint()
	if p == nil || editorWaypointRefuseCarried(activeWaypointAnimation()) {
		return
	}
	EditorPushUndo()
	p.Position = gfx.Cam.Position
}

// snapActiveWaypoints rounds the selected point — or, with the animation
// selected whole, every point of it — onto the grid.
func snapActiveWaypoints(step float32) {
	a := activeWaypointAnimation()
	if a == nil || step <= 0 || editorWaypointRefuseCarried(a) {
		return
	}
	EditorPushUndo()
	if p := activeWaypoint(); p != nil {
		p.Position = snapVec3(p.Position, step)
		editorSetStatus("Snapped point %d of %q to the grid", Editor.ActiveWaypointIdx+1, a.ID)
		return
	}
	for i := range a.Points {
		a.Points[i].Position = snapVec3(a.Points[i].Position, step)
	}
	editorSetStatus("Snapped %q to the grid", a.ID)
}

// eulerAddedAbout is eulerRotatedAbout without the wrap, for a waypoint.
//
// Every other rotation the gizmo writes is wrapped into ±180°, because for them
// only the orientation matters. A waypoint's angles are interpolated as the
// numbers they are, so turning a point past 180° has to keep counting: a point
// dragged from 170° to 200° means "carry on turning", where wrapping it to
// −160° would send the brush the long way back round.
func eulerAddedAbout(start, axis mgl32.Vec3, delta float32) mgl32.Vec3 {
	out := start
	switch axis {
	case mgl32.Vec3{1, 0, 0}:
		out[0] += delta
	case mgl32.Vec3{0, 1, 0}:
		out[1] += delta
	case mgl32.Vec3{0, 0, 1}:
		out[2] += delta
	}
	return out
}

// ---------- picking ----------

// pickWaypointMarker returns the animation and point whose marker a picking ray
// crosses nearest, and the distance, or -1s and +Inf.
func pickWaypointMarker(origin, dir, camPos mgl32.Vec3, lvl *world.Level) (ai, pi int, t float32) {
	ai, pi, t = -1, -1, float32(math.MaxFloat32)
	if lvl == nil {
		return
	}
	for a := range lvl.WaypointAnimations {
		anim := &lvl.WaypointAnimations[a]
		space := anim.SpaceMatrix(lvl)
		for p := range anim.Points {
			at := waypointXform(space, anim.Points[p].Position)
			if tt := pickMarkerDiscRayT(origin, dir, at, camPos, waypointMarkerRadius); tt < t {
				ai, pi, t = a, p, tt
			}
		}
	}
	return
}

// waypointWorldPosition is a point of a carried out of the space its points are
// written in: itself for a level's path, and where the entity is for one an
// entity carries.
func waypointWorldPosition(lvl *world.Level, a *world.WaypointAnimation, p mgl32.Vec3) mgl32.Vec3 {
	if !waypointCarried(a) {
		return p
	}
	return waypointXform(a.SpaceMatrix(lvl), p)
}

// ---------- drawing ----------

// waypointArrowLines are the arrow's strokes in its own space: a shaft along
// +X — the axis the spawn marker points along, so an unturned point faces the
// way an unturned camera does — a four-fin head, and a short stalk up +Y so a
// roll about the shaft shows as well.
var waypointArrowLines = [...][2]mgl32.Vec3{
	{{-0.35, 0, 0}, {0.35, 0, 0}},
	{{0.35, 0, 0}, {0.17, 0.1, 0}},
	{{0.35, 0, 0}, {0.17, -0.1, 0}},
	{{0.35, 0, 0}, {0.17, 0, 0.1}},
	{{0.35, 0, 0}, {0.17, 0, -0.1}},
	{{0, 0, 0}, {0, 0.18, 0}},
}

// RenderWaypointMarkers draws every waypoint animation: an arrow at each point,
// turned and sized the way a brush arriving there will be, and a line from each
// point to the next with a chevron on it saying which way the path runs.
//
// Editor only, like every marker here. Nothing in play draws a waypoint.
//
// A path an entity carries is drawn where the entity is, through the same
// matrix play carries it by (WaypointAnimation.SpaceMatrix), so its arrows sit
// on the ship's hatch rather than at the level's origin.
func RenderWaypointMarkers(lvl *world.Level) {
	if lvl == nil {
		return
	}
	for ai := range lvl.WaypointAnimations {
		a := &lvl.WaypointAnimations[ai]
		col := waypointDimColor
		if ai == Editor.ActiveWaypointAnimIdx {
			col = waypointArrowColor
		}
		sel := -1
		if ai == Editor.ActiveWaypointAnimIdx {
			sel = Editor.ActiveWaypointIdx
		}
		drawWaypointPath(a.SpaceMatrix(lvl), a, col, sel)
	}
	drawWaypointPreviewLeg(lvl)
	drawWaypointPlayhead(lvl)
}

// drawWaypointPath draws one animation's arrows and the legs between them,
// through space — the identity for a path written in the space it is drawn in —
// with point sel in the selection colour.
func drawWaypointPath(space mgl32.Mat4, a *world.WaypointAnimation, col mgl32.Vec3, sel int) {
	for pi := range a.Points {
		p := &a.Points[pi]
		if pi > 0 {
			drawWaypointLeg(waypointXform(space, a.Points[pi-1].Position), waypointXform(space, p.Position), col)
		}
		arrow := col
		if pi == sel {
			arrow = waypointSelColor
		}
		drawWaypointArrow(space, p, arrow)
	}
}

// drawWaypointArrow draws one point's arrow through its own transform, carried
// through space.
func drawWaypointArrow(space mgl32.Mat4, p *world.Waypoint, col mgl32.Vec3) {
	m := space.Mul4(p.Matrix())
	for _, l := range waypointArrowLines {
		gizmo.GizmoLine(waypointXform(m, l[0]), waypointXform(m, l[1]), col)
	}
	// A small unscaled cross at the point itself, so a point scaled down to
	// almost nothing is still there to find and click.
	const c = float32(0.06)
	at := waypointXform(space, p.Position)
	gizmo.GizmoLine(at.Sub(mgl32.Vec3{c, 0, 0}), at.Add(mgl32.Vec3{c, 0, 0}), col)
	gizmo.GizmoLine(at.Sub(mgl32.Vec3{0, c, 0}), at.Add(mgl32.Vec3{0, c, 0}), col)
	gizmo.GizmoLine(at.Sub(mgl32.Vec3{0, 0, c}), at.Add(mgl32.Vec3{0, 0, c}), col)
}

// drawWaypointLeg draws the line between two points and a chevron halfway
// along it pointing at the second.
func drawWaypointLeg(from, to, col mgl32.Vec3) {
	gizmo.GizmoLine(from, to, col)
	d := to.Sub(from)
	l := d.Len()
	if l < 1e-4 {
		return
	}
	dir := d.Mul(1 / l)
	side := dir.Cross(mgl32.Vec3{0, 1, 0})
	if side.Len() < 1e-3 {
		side = dir.Cross(mgl32.Vec3{1, 0, 0})
	}
	side = side.Normalize()
	size := float32(math.Min(0.2, float64(l)*0.25))
	mid := from.Add(d.Mul(0.5))
	back := mid.Sub(dir.Mul(size))
	gizmo.GizmoLine(back.Add(side.Mul(size)), mid, col)
	gizmo.GizmoLine(back.Sub(side.Mul(size)), mid, col)
}

// drawWaypointPreviewLeg draws the first leg of the selected animation for the
// object chosen to preview it on: from where the object was authored to the
// first point.
//
// That leg belongs to no point — it starts wherever the object is — so nothing
// else on screen shows it, and it is the one an author most often gets wrong:
// a first point placed on the floor sends a door into it. A directional light
// has no position, so it has no leg to draw.
func drawWaypointPreviewLeg(lvl *world.Level) {
	a := activeWaypointAnimation()
	if a == nil || len(a.Points) == 0 {
		return
	}
	pose, f := waypointPreviewRestPose(lvl)
	if f == nil || !world.WaypointTakesPosition(f) {
		return
	}
	drawWaypointLeg(pose.Position, waypointWorldPosition(lvl, a, a.Points[0].Position), waypointPreviewLegColor)
}

// drawWaypointPlayhead draws an arrow where the timeline's playhead has the
// selected path: the pose an object would be in at that moment. It is what
// makes scrubbing readable with nothing chosen to preview on, and with
// something chosen it shows through it.
//
// Not at the very start of a stopped preview, where it would sit on the first
// point's arrow and hide the selection colour.
func drawWaypointPlayhead(lvl *world.Level) {
	a := activeWaypointAnimation()
	p := &Editor.WaypointPreview
	if a == nil || len(a.Points) == 0 || p.Anim != a.ID || (p.Time <= 0 && !p.Playing) {
		return
	}
	pose := a.PoseAt(waypointPreviewStartPose(lvl, a), p.Time)
	ghost := world.Waypoint{Position: pose.Position, Rotation: pose.Rotation, Scale: pose.Scale}
	drawWaypointArrow(a.SpaceMatrix(lvl), &ghost, waypointPreviewLegColor)
}

// waypointPreviewStartPose is where the preview's path sets off from, in the
// space its points are written in: the preview object as it was authored, as
// play would send it — or, with nothing chosen, the first point, so the path is
// shown from where it begins.
//
// A directional light has no position to set off from, so its path is shown
// starting on the first point, and its first leg only turns it.
func waypointPreviewStartPose(lvl *world.Level, a *world.WaypointAnimation) world.WaypointPose {
	if pose, f := waypointPreviewRestPose(lvl); f != nil {
		if !world.WaypointTakesPosition(f) && len(a.Points) > 0 {
			pose.Position = a.Points[0].Position
		} else if waypointCarried(a) {
			// The object stands in the world; a carried path is travelled in
			// its entity's space, which is what PoseAt interpolates in.
			pose.Position = waypointXform(a.SpaceMatrix(lvl).Inv(), pose.Position)
		}
		return pose
	}
	if len(a.Points) > 0 {
		return a.Points[0].Pose()
	}
	return world.WaypointPose{Scale: mgl32.Vec3{1, 1, 1}}
}

// waypointXform applies an affine matrix to a point.
func waypointXform(m mgl32.Mat4, p mgl32.Vec3) mgl32.Vec3 {
	r := m.Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}
