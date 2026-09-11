package editor

// editor_entity_editor_waypoints.go — the paths a type carries, in the entity
// editor.
//
// The document holds waypoint animations written in the entity's own local
// space (world/entity_waypoints.go): a blast door's slide shut, a hatch's swing.
// This window IS that space, so a point's numbers are where its arrow is drawn
// here, and every placed ship carries the same numbers out to wherever it
// stands.
//
// Almost nothing about a path is new in this file. The arrows and legs are the
// level editor's drawWaypointPath, the timeline lane is its
// waypointTimelineCanvas behind a host of this window's own, a point is retimed
// by WaypointAnimation.SetPointTime and posed by PoseAt. What is here is what a
// window owns for itself: which path and point are selected (entitySel, with
// Sub as the point), the structural edits against the document's undo stack,
// the panel, and a preview that poses one of the type's dynamic brushes — the
// brush a script will send along the path.
//
// The preview poses the live scratch brush through world.SeekWaypoints, the
// function play and the level editor's preview both use, so what is scrubbed
// here is what a placed ship does. It writes only the brush's run-time
// placement, which no document, undo snapshot or save ever holds; and a re-seed
// of the scratch level brings every brush back at rest on its own (see
// entitySeedGeometry), so there is nothing for an undo to have to put back.

import (
	"fmt"
	"math"
	"strings"

	"foundry/internal/geom"
	"foundry/internal/hooks"
	"foundry/internal/imguibackend"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// defaultEntityWaypointName is what a new path on a type is called.
const defaultEntityWaypointName = "path"

// entityShowWaypoints draws the type's paths: a view toggle beside the others.
var entityShowWaypoints = true

// entityWaypointPreviewState is this window's playback of one of the type's
// paths — the level editor's waypointPreview, over the scratch chunk. Named by
// name and brush ID rather than index, for that one's reason: an undo replaces
// the document's lists wholesale.
type entityWaypointPreviewState struct {
	// Anim is the path's name within the type.
	Anim string
	// Brush is the ID of the scratch brush posed along it, or "" for none —
	// the ghost arrow is then the whole preview.
	Brush string
	// Time is the playhead, in seconds into Anim.
	Time    float32
	Playing bool
	Loop    bool
	// Posed is true while the brush is held somewhere along the path; rest and
	// restPose are then where it stood before, as fields and as a pose.
	Posed    bool
	held     float32
	rest     world.WaypointRest
	restPose world.WaypointPose
}

var entityWaypointPreview entityWaypointPreviewState

// ---------- selection ----------

// entityActiveWaypointAnimation returns the selected path, or nil.
func entityActiveWaypointAnimation() *world.WaypointAnimation {
	if entityDef == nil || entitySel.Kind != entitySelWaypoints ||
		entitySel.Idx < 0 || entitySel.Idx >= len(entityDef.WaypointAnimations) {
		return nil
	}
	return &entityDef.WaypointAnimations[entitySel.Idx]
}

// entityActiveWaypoint returns the selected point, or nil when the path is
// selected whole.
func entityActiveWaypoint() *world.Waypoint {
	a := entityActiveWaypointAnimation()
	if a == nil || entitySel.Sub < 0 || entitySel.Sub >= len(a.Points) {
		return nil
	}
	return &a.Points[entitySel.Sub]
}

// entitySelectWaypoints selects path ai, and point pi inside it — or -1 for the
// path whole.
//
// The geometry selection goes with it: the gizmo prefers a selected brush over
// the outline's selection (entityGizmoOrigin), and a click on an arrow that
// left the hatch selected would drag the hatch rather than the point.
func entitySelectWaypoints(ai, pi int) {
	if entityDef == nil || ai < 0 || ai >= len(entityDef.WaypointAnimations) {
		return
	}
	entityClearGeometrySelection()
	entitySel = entityEditorSelection{Kind: entitySelWaypoints, Idx: ai, Sub: pi}
}

// ---------- outline ----------

// entityOutlineWaypoints lists the paths the type carries.
func entityOutlineWaypoints() {
	imgui.Text("Paths")
	for i := range entityDef.WaypointAnimations {
		a := &entityDef.WaypointAnimations[i]
		imgui.PushID(fmt.Sprintf("outwp%d", i))
		label := fmt.Sprintf("%s  (%d points, %.2f s)", a.ID, len(a.Points), a.EffectiveLength())
		if imgui.SelectableV(label, entitySel.Kind == entitySelWaypoints && entitySel.Idx == i, 0, imgui.Vec2{}) {
			entitySelectWaypoints(i, -1)
		}
		imgui.PopID()
	}
	if len(entityDef.WaypointAnimations) == 0 {
		editorTextMutedWrapped("None. A path here is a waypoint animation every placement carries with it — a door sliding shut, a hatch swinging open.")
	}
	if imgui.Button("Add Path##entaddwp") {
		entityAddWaypointAnimation()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Adds a waypoint animation to the type, in its own space.\n" +
			"Select a Dynamic brush first and use its Paths group instead\n" +
			"to start the path where that brush stands.")
	}
}

// ---------- structural edits ----------

// entityAddWaypointAnimation adds a path with one point, selected. The point
// goes where the preview brush stands, turned and sized as it is — so the path
// begins where the thing it moves already is — or a metre above the origin with
// no brush chosen.
func entityAddWaypointAnimation() {
	if entityDef == nil {
		return
	}
	start := world.NewWaypoint(mgl32.Vec3{0, 1, 0})
	if pose, ok := entityWaypointPreviewRestPose(); ok {
		start.Position, start.Rotation, start.Scale = pose.Position, pose.Rotation, world.ClampBrushScale(pose.Scale)
	}
	entityAddWaypointAnimationFrom(start, defaultEntityWaypointName, entityWaypointPreview.Brush)
}

// entityAddWaypointAnimationFrom adds a path whose first point is start, named
// after base (made unique), selects that point, and points the preview at the
// named brush.
func entityAddWaypointAnimationFrom(start world.Waypoint, base, brush string) {
	if entityDef == nil {
		return
	}
	EntityDefPushUndo()
	a := world.WaypointAnimation{
		ID:     world.UniqueEntityWaypointAnimationName(entityDef.WaypointAnimations, base),
		Points: []world.Waypoint{start},
	}
	entityDef.WaypointAnimations = append(entityDef.WaypointAnimations, a)
	entityWaypointPreviewStop()
	entityWaypointPreview.Brush = brush
	entityWaypointPreview.Anim = a.ID
	entitySelectWaypoints(len(entityDef.WaypointAnimations)-1, 0)
	entityMarkDirty()
	entityStatus = fmt.Sprintf("Added path %q: Ctrl+click in the view, or Add Point, for where it goes next", a.ID)
}

// entityRemoveWaypointAnimation drops one path, putting a brush it was
// previewing on back first.
func entityRemoveWaypointAnimation(ai int) {
	if entityDef == nil || ai < 0 || ai >= len(entityDef.WaypointAnimations) {
		return
	}
	if entityWaypointPreview.Anim == entityDef.WaypointAnimations[ai].ID {
		entityWaypointPreviewStop()
	}
	EntityDefPushUndo()
	entityDef.WaypointAnimations = append(entityDef.WaypointAnimations[:ai:ai], entityDef.WaypointAnimations[ai+1:]...)
	entitySel = entityEditorSelection{}
	entityMarkDirty()
}

// entityDuplicateWaypointAnimation copies a path under a new name, selected. In
// place: it is the same motion, and one to retime or reshape into a second.
func entityDuplicateWaypointAnimation(ai int) {
	if entityDef == nil || ai < 0 || ai >= len(entityDef.WaypointAnimations) {
		return
	}
	EntityDefPushUndo()
	cp := entityDef.WaypointAnimations[ai].Clone()
	cp.ID = world.UniqueEntityWaypointAnimationName(entityDef.WaypointAnimations, cp.ID)
	entityDef.WaypointAnimations = append(entityDef.WaypointAnimations, cp)
	entitySelectWaypoints(len(entityDef.WaypointAnimations)-1, -1)
	entityMarkDirty()
}

// entityRenameWaypointAnimation gives path ai a new name, which has to be free
// within the type. A dot is refused: a placement's ID is joined to the name with
// one, and `@self.door.close` would read as a step too many.
func entityRenameWaypointAnimation(ai int, name string) {
	if entityDef == nil || ai < 0 || ai >= len(entityDef.WaypointAnimations) {
		return
	}
	a := &entityDef.WaypointAnimations[ai]
	name = strings.TrimSpace(name)
	switch {
	case name == a.ID:
	case name == "":
		entityStatus = "A path needs a name for a script to send anything along it"
	case strings.Contains(name, "."):
		entityStatus = "A path's name cannot contain a dot: a placement's ID is joined to it with one"
	case entityDef.WaypointAnimationNamed(name) != nil:
		entityStatus = fmt.Sprintf("This type already has a path called %q", name)
	default:
		EntityDefPushUndo()
		if entityWaypointPreview.Anim == a.ID {
			entityWaypointPreview.Anim = name
		}
		a.ID = name
		entityMarkDirty()
	}
}

// entityInsertWaypointAt adds a point at pos after the selected one — or on the
// end, with the path selected whole — and selects it. The level editor's
// insertWaypointAt against this window's selection and undo stack: the new point
// carries on the turn and scale of the one before, and is timed by
// timeWaypointInsert.
func entityInsertWaypointAt(pos mgl32.Vec3) {
	a := entityActiveWaypointAnimation()
	if a == nil {
		return
	}
	EntityDefPushUndo()
	at := len(a.Points)
	if entitySel.Sub >= 0 && entitySel.Sub < len(a.Points) {
		at = entitySel.Sub + 1
	}
	w := world.NewWaypoint(pos)
	if at > 0 {
		prev := a.Points[at-1]
		w.Rotation, w.Scale = prev.Rotation, prev.Scale
	}
	w.Time = timeWaypointInsert(a, at, pos)
	insertWaypoint(a, at, w)
	entitySel.Sub = at
	entityMarkDirty()
	entityStatus = fmt.Sprintf("Added point %d to %q", at+1, a.ID)
}

// entityAddWaypointAhead adds a point after the selected one (or the last)
// without a click, carrying on the way the path was already going.
func entityAddWaypointAhead() {
	a := entityActiveWaypointAnimation()
	if a == nil || len(a.Points) == 0 {
		return
	}
	after := entitySel.Sub
	if after < 0 || after >= len(a.Points) {
		after = len(a.Points) - 1
	}
	entitySel.Sub = after
	entityInsertWaypointAt(waypointAheadPosition(a, after))
}

// entityKeyWaypointAtPlayhead adds a point where the playhead has the path, at
// the playhead's time, and selects it — or selects the point already there.
func entityKeyWaypointAtPlayhead() {
	a := entityActiveWaypointAnimation()
	if a == nil || len(a.Points) == 0 {
		return
	}
	t := entityWaypointPreview.Time
	at := len(a.Points)
	for i := range a.Points {
		if geom.Abs32(a.Points[i].Time-t) <= 1e-4 {
			entitySel.Sub = i
			return
		}
		if a.Points[i].Time > t {
			at = i
			break
		}
	}
	pose := a.PoseAt(entityWaypointPreviewStartPose(a), t)
	EntityDefPushUndo()
	insertWaypoint(a, at, world.Waypoint{
		Position: pose.Position,
		Rotation: pose.Rotation,
		Scale:    world.ClampBrushScale(pose.Scale),
		Time:     t,
	})
	entitySel.Sub = at
	entityMarkDirty()
}

// entityDeleteActiveWaypoint removes the selected point, joining its neighbours —
// or the whole path, when it is selected whole or the point was its last.
func entityDeleteActiveWaypoint() {
	a := entityActiveWaypointAnimation()
	if a == nil {
		return
	}
	pi := entitySel.Sub
	if pi < 0 || pi >= len(a.Points) || len(a.Points) == 1 {
		entityRemoveWaypointAnimation(entitySel.Idx)
		return
	}
	EntityDefPushUndo()
	a.Points = append(a.Points[:pi:pi], a.Points[pi+1:]...)
	// The point before the gap stays selected, so a Ctrl+click puts a
	// replacement straight back into it.
	if pi > 0 {
		pi--
	}
	entitySel.Sub = pi
	entityMarkDirty()
}

// entityMoveActiveWaypoint swaps the selected point with its neighbour by places
// along, leaving the times where they are in the list — moveActiveWaypoint's rule.
func entityMoveActiveWaypoint(by int) {
	a := entityActiveWaypointAnimation()
	pi := entitySel.Sub
	to := pi + by
	if a == nil || pi < 0 || pi >= len(a.Points) || to < 0 || to >= len(a.Points) {
		return
	}
	EntityDefPushUndo()
	ti, tt := a.Points[pi].Time, a.Points[to].Time
	a.Points[pi], a.Points[to] = a.Points[to], a.Points[pi]
	a.Points[pi].Time, a.Points[to].Time = ti, tt
	entitySel.Sub = to
	entityMarkDirty()
}

// entitySetWaypointLength stretches a path to a new length, and the playhead
// with it, so the playhead stays on the same moment of the path.
func entitySetWaypointLength(a *world.WaypointAnimation, l float32) {
	old := a.EffectiveLength()
	a.SetLength(l)
	if p := &entityWaypointPreview; p.Anim == a.ID && old > 0 {
		p.Time = geom.ClampValue(p.Time*a.EffectiveLength()/old, 0, a.EffectiveLength())
	}
}

// ---------- preview ----------

// entityScratchBrush is the scratch chunk's brush with that ID and the chunk, or
// nils.
func entityScratchBrush(id string) (*world.Chunk, *world.Brush) {
	ch := entityGeomChunk()
	if ch == nil || id == "" {
		return nil, nil
	}
	for i := range ch.Brushes {
		if ch.Brushes[i].ID == id {
			return ch, &ch.Brushes[i]
		}
	}
	return nil, nil
}

// entityWaypointPreviewBrush is the brush the preview poses, or nils — also when
// it is not dynamic, since a script could not send it along a path either.
func entityWaypointPreviewBrush() (*world.Chunk, *world.Brush) {
	ch, b := entityScratchBrush(entityWaypointPreview.Brush)
	if b == nil || !b.Dynamic {
		return nil, nil
	}
	return ch, b
}

// entityDynamicBrushIDs lists the type's brushes a path can be previewed on.
func entityDynamicBrushIDs() []string {
	ch := entityGeomChunk()
	if ch == nil {
		return nil
	}
	var out []string
	for i := range ch.Brushes {
		if ch.Brushes[i].Dynamic {
			out = append(out, ch.Brushes[i].ID)
		}
	}
	return out
}

// entityWaypointPreviewRestPose is where the preview brush stands as authored,
// and whether there is one.
func entityWaypointPreviewRestPose() (world.WaypointPose, bool) {
	ch, b := entityWaypointPreviewBrush()
	if b == nil {
		return world.WaypointPose{}, false
	}
	if entityWaypointPreview.Posed {
		return entityWaypointPreview.restPose, true
	}
	return b.WaypointPose(ch), true
}

// entityWaypointPreviewStartPose is where the path sets off from: the preview
// brush as authored, as play would send it, or the first point with none.
func entityWaypointPreviewStartPose(a *world.WaypointAnimation) world.WaypointPose {
	if pose, ok := entityWaypointPreviewRestPose(); ok {
		return pose
	}
	if len(a.Points) > 0 {
		return a.Points[0].Pose()
	}
	return world.WaypointPose{Scale: mgl32.Vec3{1, 1, 1}}
}

// entityWaypointPreviewRelease puts the preview brush back where it was
// authored and leaves the playhead alone.
func entityWaypointPreviewRelease() {
	p := &entityWaypointPreview
	if !p.Posed {
		return
	}
	p.Posed = false
	// Found without the dynamic gate: a brush unticked mid-preview still has to
	// go back.
	if ch, b := entityScratchBrush(p.Brush); b != nil {
		f := world.BrushWaypointFollower(ch, b)
		world.StopWaypoints(f)
		world.RestoreWaypointRest(f, p.rest)
		ch.RecomputeBounds()
	}
}

// entityWaypointPreviewStop ends a preview: playhead to the start, brush back.
func entityWaypointPreviewStop() {
	p := &entityWaypointPreview
	p.Playing, p.Time, p.held = false, 0, 0
	entityWaypointPreviewRelease()
}

// entityWaypointPreviewPlay runs the playhead on the named path from where it is
// — or from the start, when it has already reached the end.
func entityWaypointPreviewPlay(name string) {
	a := entityDef.WaypointAnimationNamed(name)
	if a == nil || len(a.Points) == 0 {
		return
	}
	p := &entityWaypointPreview
	if p.Anim != name {
		entityWaypointPreviewStop()
		p.Anim = name
	}
	if p.Time >= a.EffectiveLength() {
		p.Time = 0
	}
	p.Playing, p.held = true, 0
	entityWaypointPreviewPose()
}

// entityWaypointPreviewSeek moves the playhead to t, pausing it, and poses the
// brush there.
func entityWaypointPreviewSeek(t float32) {
	p := &entityWaypointPreview
	a := entityDef.WaypointAnimationNamed(p.Anim)
	if a == nil {
		return
	}
	p.Playing, p.held = false, 0
	p.Time = geom.ClampValue(t, 0, a.EffectiveLength())
	entityWaypointPreviewPose()
}

// entityWaypointPreviewPose puts the preview brush where the playhead has the
// path, through world.SeekWaypoints — editorWaypointPreviewPose's sequence, on
// the scratch level.
func entityWaypointPreviewPose() {
	p := &entityWaypointPreview
	a := entityDef.WaypointAnimationNamed(p.Anim)
	ch, b := entityWaypointPreviewBrush()
	if a == nil || b == nil || entityGeomLevel == nil {
		return
	}
	f := world.BrushWaypointFollower(ch, b)
	before := world.CaptureWaypointRest(f)
	if !p.Posed || world.FollowedWaypointAnimation(f) == "" {
		if p.Posed {
			world.RestoreWaypointRest(f, p.rest)
		} else {
			p.rest = before
		}
		p.restPose = b.WaypointPose(ch)
		world.PlayWaypoints(entityGeomLevel, f, a)
		p.Posed = true
	}
	world.SeekWaypoints(entityGeomLevel, f, a, p.Time)
	if before != world.CaptureWaypointRest(f) {
		ch.RecomputeBounds()
	}
}

// entityWaypointPreviewTick runs the playhead on by dt and keeps the brush on
// it. Once a frame, after the scratch level has been re-seeded if it needed it.
//
// The preview follows the selected path, as the level editor's does; with no
// path selected — a brush picked to preview from its own panel — it is left on
// whatever it was playing.
func entityWaypointPreviewTick(dt float32) {
	p := &entityWaypointPreview
	if a := entityActiveWaypointAnimation(); a != nil && p.Anim != a.ID {
		entityWaypointPreviewStop()
		p.Anim = a.ID
	}
	a := entityDef.WaypointAnimationNamed(p.Anim)
	if a == nil {
		if p.Playing || p.Posed {
			entityWaypointPreviewStop()
		}
		return
	}
	if p.Playing && dt > 0 {
		switch length := a.EffectiveLength(); {
		case p.Time < length:
			p.Time += dt
			if p.Time > length {
				p.Time = length
			}
		case !p.Loop:
			p.Playing = false
		default:
			p.held += dt
			if p.held >= waypointPreviewLoopPause {
				p.Time, p.held = 0, 0
			}
		}
	}
	if p.Playing || p.Posed {
		entityWaypointPreviewPose()
	}
}

// entityWaypointPreviewBrushCombo picks the brush the preview poses. Changing
// brush puts the old one back and keeps the playhead.
func entityWaypointPreviewBrushCombo(label string) {
	p := &entityWaypointPreview
	choose := func(id string) {
		entityWaypointPreviewRelease()
		p.Brush = id
		if p.Playing || p.Time > 0 {
			entityWaypointPreviewPose()
		}
	}
	shown := p.Brush
	if shown == "" {
		shown = "(nothing)"
	}
	if imgui.BeginCombo(label, shown) {
		if imgui.SelectableV("(nothing)", p.Brush == "", 0, imgui.Vec2{}) && p.Brush != "" {
			choose("")
		}
		for _, id := range entityDynamicBrushIDs() {
			if imgui.SelectableV(id, id == p.Brush, 0, imgui.Vec2{}) && id != p.Brush {
				choose(id)
			}
		}
		imgui.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The brush to play the path on — the one a script will send\n" +
			"along it. Only Dynamic brushes are listed.")
	}
}

// ---------- panel ----------

// entityPanelWaypoints draws one path: its name and length, its points, the
// selected point, the preview, and how a script sends something along it.
func entityPanelWaypoints(ai int) {
	a := &entityDef.WaypointAnimations[ai]
	name := a.ID
	imgui.SetNextItemWidth(-90)
	if imgui.InputTextV("##entwpname", &name, imgui.InputTextFlagsEnterReturnsTrue, nil) {
		entityRenameWaypointAnimation(ai, name)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Qualified by the placement in a level, so a script says\n" +
			"@<entity>." + a.ID + " — or @self." + a.ID + " from the type's own script —\n" +
			"and each ship's door closes along its own.")
	}
	imgui.SameLine()
	if imgui.Button("Delete##entwpdel") {
		entityRemoveWaypointAnimation(ai)
		return
	}

	if l := a.EffectiveLength(); editorDragFloatStep("Length (s)##entwplen", &l, waypointTimelineSnap, 3600, "%.2f", waypointTimelineSnap) {
		entityBeginEdit("wplen")
		entitySetWaypointLength(a, l)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How long the whole path takes. Changing it stretches every\n" +
			"point's time with it; a length past the last point holds the brush there.")
	}
	if imgui.Button("Add Point##entwpadd") {
		entityAddWaypointAhead()
	}
	imgui.SameLine()
	if imgui.Button("Key At Playhead##entwpkey") {
		entityKeyWaypointAtPlayhead()
	}
	imgui.SameLine()
	if imgui.Button("Duplicate##entwpdup") {
		entityDuplicateWaypointAnimation(ai)
		return
	}
	editorTextMutedWrapped("Points are in the entity's own space, as its brushes are, and ride with every placement. " +
		"Ctrl+click in the view adds one after the selected point; the timeline along the bottom places each in time.")

	editorPropGroupOpen("Points##entwppts", func() {
		for i := range a.Points {
			row := waypointRowLabel(i, &a.Points[i]) + fmt.Sprintf("##entwprow%d", i)
			if imgui.SelectableV(row, i == entitySel.Sub, 0, imgui.Vec2{}) {
				entitySelectWaypoints(ai, i)
			}
		}
	})
	if p := entityActiveWaypoint(); p != nil {
		idx := entitySel.Sub
		editorPropGroupOpen(fmt.Sprintf("Point %d##entwppt", idx+1), func() { entityPanelWaypoint(a, idx, p) })
		editorPropGroupOpen(fmt.Sprintf("Point %d Events##entwpev", idx+1), func() { editorWaypointEventsUI(p) })
	}
	editorPropGroupOpen("Preview##entwpprev", func() { entityPanelWaypointPreview(a) })
	editorPropGroup("Script##entwpscript", 0, func() { entityWaypointScriptHint(entityWaypointPreview.Brush, a.ID) })
}

// entityPanelWaypoint draws the selected point's pose and time, the leg
// arriving at it, and its own actions.
func entityPanelWaypoint(a *world.WaypointAnimation, idx int, p *world.Waypoint) {
	pos := [3]float32{p.Position.X(), p.Position.Y(), p.Position.Z()}
	if editorDragFloat3Step("Position##entwppos", &pos, -1e5, 1e5, "%.3f", 0.05) {
		entityBeginEdit("wppos")
		p.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("In the entity's own space: where a brush's centre goes.")
	}
	// ±3600 for the level panel's reason: eased as written, so 720 is two turns.
	rot := vec3ToDegrees(p.Rotation)
	if editorDragFloat3Step("Rotation (deg)##entwprot", &rot, -3600, 3600, "%.1f", 1) {
		entityBeginEdit("wprot")
		p.Rotation = vec3FromDegrees(rot)
	}
	s := p.EffectiveScale()
	sc := [3]float32{s.X(), s.Y(), s.Z()}
	if editorDragFloat3Step("Scale##entwpscale", &sc, world.MinBrushScale, 1000, "%.2f", 0.01) {
		entityBeginEdit("wpscale")
		p.Scale = world.ClampBrushScale(mgl32.Vec3{sc[0], sc[1], sc[2]})
	}
	if t := p.Time; editorDragFloatStep("Time (s)##entwptime", &t, 0, 3600, "%.2f", waypointTimelineSnap) {
		entityBeginEdit("wptime")
		a.SetPointTime(idx, t)
	}
	editorTextMuted(entityWaypointLegSummary(a, idx))

	imguibackend.EditorPushDisabled(idx == 0)
	if imgui.Button("Move Earlier##entwpup") {
		entityMoveActiveWaypoint(-1)
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	imguibackend.EditorPushDisabled(idx >= len(a.Points)-1)
	if imgui.Button("Move Later##entwpdown") {
		entityMoveActiveWaypoint(1)
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	if imgui.Button("Delete Point##entwpptdel") {
		entityDeleteActiveWaypoint()
	}
}

// entityPanelWaypointPreview draws the brush picker and a compact transport.
func entityPanelWaypointPreview(a *world.WaypointAnimation) {
	p := &entityWaypointPreview
	entityWaypointPreviewBrushCombo("Brush##entwpprevbrush")
	if len(entityDynamicBrushIDs()) == 0 {
		editorTextMutedWrapped("None of the type's brushes is Dynamic yet. Tick Dynamic on the door " +
			"(or whatever the path moves) for a script to be allowed to move it.")
	}
	playing := p.Anim == a.ID && (p.Playing || p.Posed)
	imguibackend.EditorPushDisabled(len(a.Points) == 0)
	if playing {
		if imgui.Button("Stop##entwpprevstop") {
			entityWaypointPreviewStop()
		}
	} else if imgui.Button("Play##entwpprevplay") {
		entityWaypointPreviewPlay(a.ID)
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	imgui.Checkbox("Loop##entwpprevloop", &p.Loop)
	editorTextMutedWrapped("The brush sets off from where it was authored, as it does in play, and goes back " +
		"when the preview stops. Nothing it does here is saved.")
}

// entityWaypointScriptHint spells out the call that sends brush along path from
// the type's own script.
func entityWaypointScriptHint(brush, path string) {
	if brush == "" {
		brush = "<brush>"
	}
	editorTextMutedWrapped("From the type's script:  play_waypoints(@self." + brush + ", @self." + path + ")")
	editorTextMutedWrapped("The brush must be Dynamic. It hears :waypoint: at each point and :waypoints_end: " +
		"when the length runs out, and a point's Events are posted to it as it arrives.")
}

// entityWaypointLegSummary describes the leg arriving at point idx:
// waypointLegSummary over this window's preview brush.
func entityWaypointLegSummary(a *world.WaypointAnimation, idx int) string {
	to := a.Points[idx]
	from, fromT := mgl32.Vec3{}, float32(0)
	lead := "Leg"
	if idx > 0 {
		from, fromT = a.Points[idx-1].Position, a.Points[idx-1].Time
	} else {
		pose, ok := entityWaypointPreviewRestPose()
		if !ok {
			return fmt.Sprintf("Leg: %.2f s from wherever the brush is when it is sent.", to.Time)
		}
		from, lead = pose.Position, "Leg from "+entityWaypointPreview.Brush
	}
	secs, dist := to.Time-fromT, to.Position.Sub(from).Len()
	if secs <= 0 {
		return fmt.Sprintf("%s: %.2f units, instant.", lead, dist)
	}
	return fmt.Sprintf("%s: %.2f units in %.2f s (%.2f/s)", lead, dist, secs, dist/secs)
}

// entityObjectWaypointsUI is editorObjectWaypointsUI in this window: a brush of
// the type, previewed along the type's own paths, or the start of a new one.
func entityObjectWaypointsUI(t waypointTarget) {
	if t.Kind != hooks.WaypointBrush || entityDef == nil {
		return
	}
	ch, b := entityScratchBrush(t.ID)
	if b == nil {
		return
	}
	editorPropGroup("Paths##entobjwpgrp", 0, func() {
		if !b.Dynamic {
			editorTextMutedWrapped("Tick Dynamic for a script to send this brush along one of the type's paths — " +
				"a door sliding shut, a hatch swinging open.")
			return
		}
		p := &entityWaypointPreview
		cur := p.Anim
		if entityDef.WaypointAnimationNamed(cur) == nil {
			cur = ""
		}
		shown := cur
		if shown == "" {
			shown = "(none)"
		}
		if imgui.BeginCombo("Path##entobjwpanim", shown) {
			for i := range entityDef.WaypointAnimations {
				id := entityDef.WaypointAnimations[i].ID
				if imgui.SelectableV(id, id == cur, 0, imgui.Vec2{}) && id != cur {
					entityWaypointPreviewStop()
					p.Anim = id
					cur = id
				}
			}
			imgui.EndCombo()
		}
		playing := (p.Playing || p.Posed) && p.Brush == b.ID && p.Anim == cur
		imguibackend.EditorPushDisabled(cur == "")
		if playing {
			if imgui.Button("Stop##entobjwpstop") {
				entityWaypointPreviewStop()
			}
		} else if imgui.Button("Preview##entobjwpplay") {
			entityWaypointPreviewStop()
			p.Brush = b.ID
			entityWaypointPreviewPlay(cur)
		}
		imguibackend.EditorPopDisabled()

		if imgui.Button("New Path From Here##entobjwpnew") {
			pose := b.WaypointPose(ch)
			if p.Posed && p.Brush == b.ID {
				pose = p.restPose
			}
			start := world.Waypoint{Position: pose.Position, Rotation: pose.Rotation, Scale: world.ClampBrushScale(pose.Scale)}
			entityAddWaypointAnimationFrom(start, b.ID+"_"+defaultEntityWaypointName, b.ID)
			return
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("A new path whose first point is this brush where it stands.\n" +
				"Ctrl+click in the view, or Add Point, for where it goes next.")
		}
		if cur != "" {
			entityWaypointScriptHint(b.ID, cur)
		}
	})
}

// ---------- the timeline ----------

// entityWaypointTimelineHost is this window's host for the shared lane.
func entityWaypointTimelineHost(a *world.WaypointAnimation) waypointTimelineHost {
	playhead := float32(0)
	if entityWaypointPreview.Anim == a.ID {
		playhead = entityWaypointPreview.Time
	}
	return waypointTimelineHost{
		selected:    entitySel.Sub,
		playhead:    playhead,
		selectPoint: func(i int) { entitySel.Sub = i },
		seek:        entityWaypointPreviewSeek,
		pushUndo: func() {
			EntityDefPushUndo()
			entityMarkDirty()
		},
		tip: func(i int) string {
			return fmt.Sprintf("Point %d at %.2f s\n%s", i+1, a.Points[i].Time, entityWaypointLegSummary(a, i))
		},
	}
}

// entityWaypointTimelineWindow docks the timeline along the bottom of the
// viewport, between the outline and the panel, while a path is selected — the
// level editor's strip, for its reasons.
func entityWaypointTimelineWindow() {
	w := entityEditorWindow
	a := entityActiveWaypointAnimation()
	if w == nil || a == nil {
		return
	}
	width := float32(w.W) - entityOutlineWidth - entityPanelWidth
	if width < waypointTimelineMinW {
		return
	}
	height := waypointTimelineHeight()
	imgui.SetNextWindowPos(imgui.Vec2{X: entityOutlineWidth, Y: float32(w.H) - height})
	imgui.SetNextWindowSize(imgui.Vec2{X: width, Y: height})
	if !imgui.BeginV("Path Timeline: "+a.ID+"###entwptimeline", nil,
		imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize|imgui.WindowFlagsNoCollapse|imgui.WindowFlagsNoScrollbar) {
		imgui.End()
		return
	}
	withUndoRedirect(entityPushUndoDirty, func() {
		entityWaypointTransportRow(a)
		// The row can delete the path along with its last point.
		if a = entityActiveWaypointAnimation(); a != nil {
			waypointTimelineCanvas(a, entityWaypointTimelineHost(a))
		}
	})
	imgui.End()
}

// entityWaypointTransportRow is the transport, the length, the preview brush and
// the two key actions.
func entityWaypointTransportRow(a *world.WaypointAnimation) {
	p := &entityWaypointPreview
	length := a.EffectiveLength()
	playhead := float32(0)
	if p.Anim == a.ID {
		playhead = p.Time
	}

	if imgui.Button("|<##entwptl") {
		entityWaypointPreviewSeek(0)
	}
	waypointTimelineTip("Back to the start")
	imgui.SameLine()
	if imgui.Button("<##entwptl") {
		entityWaypointPreviewSeek(waypointKeyTimeBefore(a, playhead))
	}
	waypointTimelineTip("The point before the playhead")
	imgui.SameLine()
	playing := p.Playing && p.Anim == a.ID
	label := "Play##entwptl"
	if playing {
		label = "Pause##entwptl"
	}
	if editorActiveToggleButton(label, playing) {
		if playing {
			p.Playing = false
		} else {
			entityWaypointPreviewPlay(a.ID)
		}
	}
	imgui.SameLine()
	if imgui.Button(">##entwptl") {
		entityWaypointPreviewSeek(waypointKeyTimeAfter(a, playhead))
	}
	waypointTimelineTip("The point after the playhead")
	imgui.SameLine()
	if imgui.Button(">|##entwptl") {
		entityWaypointPreviewSeek(length)
	}
	waypointTimelineTip("To the end")
	imgui.SameLine()
	if imgui.Button("Stop##entwptl") {
		entityWaypointPreviewStop()
	}
	waypointTimelineTip("Back to the start, and the preview brush back where it was authored")
	imgui.SameLine()
	imgui.Checkbox("Loop##entwptl", &p.Loop)

	imgui.SameLine()
	imgui.Text(fmt.Sprintf("  %.2f / %.2f s  ", playhead, length))

	imgui.SameLine()
	imgui.SetNextItemWidth(80)
	if l := length; editorDragFloatStep("Length##entwptllen", &l, waypointTimelineSnap, 3600, "%.2f s", waypointTimelineSnap) {
		entityBeginEdit("wplen")
		entitySetWaypointLength(a, l)
	}

	imgui.SameLine()
	imgui.SetNextItemWidth(130)
	entityWaypointPreviewBrushCombo("Brush##entwptlbrush")

	imgui.SameLine()
	if imgui.Button("Key At Playhead##entwptlkey") {
		entityKeyWaypointAtPlayhead()
		return
	}
	waypointTimelineTip("A point where the playhead has the path, at the playhead's time.\n" +
		"Move, turn or scale it with the gizmo afterwards.")
	imgui.SameLine()
	imguibackend.EditorPushDisabled(entityActiveWaypoint() == nil)
	if imgui.Button("Delete Point##entwptldel") {
		entityDeleteActiveWaypoint()
	}
	imguibackend.EditorPopDisabled()
}

// ---------- the viewport ----------

// drawEntityWaypointMarkers draws every path's arrows and legs where they are —
// this window being the entity's space — and, for the selected path, the leg
// from the preview brush and the ghost arrow at the playhead.
func drawEntityWaypointMarkers() {
	if entityDef == nil {
		return
	}
	active := entityActiveWaypointAnimation()
	for ai := range entityDef.WaypointAnimations {
		a := &entityDef.WaypointAnimations[ai]
		col, sel := waypointDimColor, -1
		if a == active {
			col, sel = waypointArrowColor, entitySel.Sub
		}
		drawWaypointPath(mgl32.Ident4(), a, col, sel)
	}
	if active == nil || len(active.Points) == 0 {
		return
	}
	if pose, ok := entityWaypointPreviewRestPose(); ok {
		drawWaypointLeg(pose.Position, active.Points[0].Position, waypointPreviewLegColor)
	}
	if p := &entityWaypointPreview; p.Anim == active.ID && (p.Time > 0 || p.Playing) {
		pose := active.PoseAt(entityWaypointPreviewStartPose(active), p.Time)
		ghost := world.Waypoint{Position: pose.Position, Rotation: pose.Rotation, Scale: pose.Scale}
		drawWaypointArrow(mgl32.Ident4(), &ghost, waypointPreviewLegColor)
	}
}

// entityPickWaypointMarker returns the path and point whose marker the ray under
// the cursor crosses nearest.
func entityPickWaypointMarker(mx, my int32) (ai, pi int, ok bool) {
	ai, pi = -1, -1
	origin, dir, got := entityCamRay(mx, my)
	if !got || entityDef == nil {
		return ai, pi, false
	}
	best := float32(math.MaxFloat32)
	cam := entityCam.position()
	for a := range entityDef.WaypointAnimations {
		pts := entityDef.WaypointAnimations[a].Points
		for p := range pts {
			if t := pickMarkerDiscRayT(origin, dir, pts[p].Position, cam, waypointMarkerRadius); t < best {
				ai, pi, best = a, p, t
			}
		}
	}
	return ai, pi, ai >= 0
}

// entityWaypointClick answers a left click that missed the gizmo, before the
// geometry sees it. With a path selected, Ctrl+click adds the next point where
// the cursor meets the hull — the level editor's gesture for laying a path. A
// plain click on an arrow selects that point; arrows are drawn over the hull,
// so they win the click the way they do in the 3D view. Reports whether it took
// the click.
func entityWaypointClick(mx, my int32, ctrl, shift bool) bool {
	if entityDef == nil || !entityShowWaypoints || entityWS.BrushPlaceActive {
		return false
	}
	if ctrl && !shift && entityActiveWaypointAnimation() != nil {
		entityInsertWaypointAt(workspacePointAt(mx, my))
		return true
	}
	if ctrl {
		return false
	}
	if ai, pi, ok := entityPickWaypointMarker(mx, my); ok {
		entitySelectWaypoints(ai, pi)
		return true
	}
	return false
}

// entityCtxWaypointItems is the right-click menu for a path selection.
func entityCtxWaypointItems() []ctxMenuItem {
	a := entityActiveWaypointAnimation()
	if a == nil {
		return nil
	}
	ai := entitySel.Idx
	items := []ctxMenuItem{
		{Label: "Add point", Shortcut: "Ctrl+click", Do: entityAddWaypointAhead,
			Tooltip: "After the selected point, carrying on the way the path was going."},
		{Label: "Key at playhead", Do: entityKeyWaypointAtPlayhead},
		{Label: "Duplicate path", Do: func() { entityDuplicateWaypointAnimation(ai) }},
		{Separator: true},
	}
	if entityActiveWaypoint() != nil {
		items = append(items, ctxMenuItem{Label: "Delete point", Shortcut: "Del", Do: entityDeleteActiveWaypoint})
	}
	items = append(items, ctxMenuItem{Label: "Delete path", Do: func() { entityRemoveWaypointAnimation(ai) }})
	return items
}
