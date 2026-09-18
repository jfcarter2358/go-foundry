package editor

// editor_waypoint_panel.go — waypoint animations in the side panel: the Add
// menu, the Explorer rows, the selected animation's properties, the preview
// controls, and the way in from the panel of every object a path can carry.
//
// Field edits here are not undoable, on the rule every other properties block
// follows; adding, deleting and reordering points are, since those change what
// the path is rather than tuning it.

import (
	"fmt"
	"strings"

	"foundry/internal/hooks"
	"foundry/internal/imguibackend"
	"foundry/internal/script"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// ---------- Add menu ----------

// editorWaypointAddMenu draws the waypoint half of the Explorer's Add menu.
func editorWaypointAddMenu() {
	imgui.SetNextItemWidth(160)
	imgui.InputTextV("ID##newwpa", &Editor.NewWaypointAnimID, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Leave empty to name it \"waypoints\".\nMade unique on placement.")
	}
	imgui.Separator()
	if imgui.MenuItem("Place By Clicking") {
		// A fresh animation, not more points on whichever one is selected.
		editorClearWaypointSelection()
		editorArmWaypointPlacement()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Places the animation's first point where you click.\n" +
			"Ctrl+click in the view then adds each point after it.\n" +
			"A script sends an object along it with play_waypoints.")
	}
}

// ---------- Explorer ----------

// editorExplorerWaypointNodes draws the level's waypoint animations, each one
// opening onto its points in order.
func editorExplorerWaypointNodes(lvl *world.Level) {
	if !editorExplorerCategory(fmt.Sprintf("Waypoint Animations (%d)##waypointanims", len(lvl.WaypointAnimations)), panelFocusWaypoint) {
		return
	}
	defer imgui.TreePop()

	if len(lvl.WaypointAnimations) == 0 {
		editorTextMuted("(none)")
		return
	}
	del, dup, delPoint := -1, -1, [2]int{-1, -1}
	for ai := range lvl.WaypointAnimations {
		a := &lvl.WaypointAnimations[ai]
		imgui.PushID(fmt.Sprintf("wpa%d", ai))

		label := fmt.Sprintf("%s  (%d points)", a.ID, len(a.Points))
		if waypointCarried(a) {
			label += "  (carried by " + a.FromEntity + ")"
		}
		if editorWaypointPreviewing(a.ID) {
			label += "  (previewing)"
		}
		flags := explorerRowFlags
		selected := ai == Editor.ActiveWaypointAnimIdx
		if selected && Editor.ActiveWaypointIdx < 0 {
			flags |= imgui.TreeNodeFlagsSelected
		}
		// NoTreePushOnOpen and a manual indent, for the chunk rows' reason:
		// the row's popup keeps one id whether the node is open or shut.
		open := imgui.TreeNodeV(label+"##wpanode", flags|imgui.TreeNodeFlagsNoTreePushOnOpen)
		if imgui.IsItemClicked() {
			editorSelectWaypointAnimation(ai)
		}
		editorExplorerMenu("wpactx", func() {
			if t := Editor.WaypointPreview.Target; t.ID != "" {
				if imgui.MenuItem("Preview On " + t.label()) {
					editorWaypointPreviewStart(a.ID, t)
				}
			}
			if imgui.MenuItem("Duplicate") {
				dup = ai
			}
			imgui.Separator()
			if imgui.MenuItem("Delete") {
				del = ai
			}
		})

		if open {
			imgui.Indent()
			for pi := range a.Points {
				row := waypointRowLabel(pi, &a.Points[pi]) + fmt.Sprintf("##wprow%d", pi)
				if editorExplorerLeaf(row, selected && pi == Editor.ActiveWaypointIdx, panelFocusWaypoint) {
					editorSelectWaypoint(ai, pi)
				}
				editorExplorerMenu(fmt.Sprintf("wpctx%d", pi), func() {
					if imgui.MenuItem("Insert After") {
						editorSelectWaypoint(ai, pi)
						addWaypointAhead()
					}
					if imgui.MenuItem("Delete") {
						delPoint = [2]int{ai, pi}
					}
				})
			}
			imgui.Unindent()
		}
		imgui.PopID()
	}
	if dup >= 0 {
		duplicateWaypointAnimation(dup)
	}
	if del >= 0 {
		deleteWaypointAnimation(del)
	}
	if delPoint[0] >= 0 {
		editorSelectWaypoint(delPoint[0], delPoint[1])
		deleteActiveWaypoint()
	}
}

// waypointRowLabel is how a point reads in a list — its number, where it is,
// when it is reached, and the events it posts — without an imgui id.
func waypointRowLabel(i int, p *world.Waypoint) string {
	row := fmt.Sprintf("%d.  <%.2f, %.2f, %.2f>  %.2f s", i+1,
		p.Position.X(), p.Position.Y(), p.Position.Z(), p.Time)
	for _, atom := range p.Events {
		row += "  :" + atom + ":"
	}
	return row
}

// ---------- properties ----------

// editorWaypointPropertiesSection draws the selected animation, and the
// selected point inside it.
func editorWaypointPropertiesSection() {
	a := activeWaypointAnimation()
	if a == nil {
		return
	}
	imgui.Separator()
	editorFocusOpen(panelFocusWaypoint)
	if !imgui.CollapsingHeaderV("Waypoint Animation: "+a.ID+"##wpahdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusWaypoint)

	// A path an entity carries is shown and previewed here, but its fields are
	// the type's and are written in the entity's space: greyed out, with the way
	// to where they can be edited. The point list and the preview stay live.
	carried := waypointCarried(a)
	if carried {
		editorWaypointCarriedNote(a)
	}
	imguibackend.EditorPushDisabled(carried)
	editorPropGroupOpen("Animation##wpagrp", func() { editorWaypointAnimationIdentity(a) })
	imguibackend.EditorPopDisabled()
	editorPropGroupOpen("Points##wpptsgrp", func() { editorWaypointPointList(a) })
	imguibackend.EditorPushDisabled(carried)
	if p := activeWaypoint(); p != nil {
		idx := Editor.ActiveWaypointIdx
		// The label's number follows the selection; the ## id does not, so
		// the group stays open as the author clicks from point to point.
		editorPropGroupOpen(fmt.Sprintf("Point %d##wpptgrp", idx+1), func() { editorWaypointPointFields(a, idx, p) })
		editorPropGroupOpen(fmt.Sprintf("Point %d Events##wpevgrp", idx+1), func() { editorWaypointEventsUI(p) })
		editorPropGroupOpen(fmt.Sprintf("Point %d AI##wpaigrp", idx+1), func() {
			editorWaypointAIUI(Editor.level, a, idx, p, func(string) {})
		})
	}
	imguibackend.EditorPopDisabled()
	editorPropGroupOpen("Preview##wpprevgrp", func() { editorWaypointPreviewUI(a) })
	if carried {
		return
	}
	// Last, because both of these change what `a` points at, and nothing
	// after them in the frame reads it.
	editorPropGroup("Actions##wpaactgrp", 0, func() { editorWaypointAnimationActions() })
}

// editorWaypointCarriedNote says whose a path is and opens that type in the
// entity editor, which is where its points are edited.
func editorWaypointCarriedNote(a *world.WaypointAnimation) {
	typeName := ""
	if e := world.FindEntity(Editor.level, a.FromEntity); e != nil {
		typeName = e.Type
	}
	if typeName == "" {
		editorTextMutedWrapped("Carried by entity " + a.FromEntity + ", in its own space. Its points move with it and are not edited here.")
		return
	}
	editorTextMutedWrapped("Carried by entity " + a.FromEntity + ": part of type " + typeName +
		", written in the entity's own space. It moves with the entity, and every placement of the type has its own copy.")
	if imgui.Button("Edit Type##wpaedittype") {
		OpenEntityEditorOn(typeName)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Open " + typeName + " in the entity editor, where its paths are edited.\nSaving it re-stamps every placement in this level.")
	}
}

// editorWaypointAnimationIdentity draws the ID, what the animation adds up to,
// and the two ways to add points.
func editorWaypointAnimationIdentity(a *world.WaypointAnimation) {
	id := a.ID
	if imgui.InputTextV("ID##wpaid", &id, imgui.InputTextFlagsEnterReturnsTrue, nil) && id != a.ID {
		renameActiveWaypointAnimation(id)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("What a script names: play_waypoints(@door, @" + a.ID + ")")
	}

	imgui.Text(fmt.Sprintf("%d point(s)", len(a.Points)))
	if l := a.EffectiveLength(); editorDragFloatStep("Length (s)##wpalen", &l, waypointTimelineSnap, 3600, "%.2f", waypointTimelineSnap) {
		editorSetWaypointAnimationLength(a, l)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How long the whole animation takes. Changing it stretches every\n" +
			"point's time with it; a length past the last point holds the object there.")
	}

	editorTextMutedWrapped("Ctrl+click in the view to add a point after the selected one. " +
		"Del removes a point and joins the ones either side. " +
		"The timeline along the bottom of the view places each point in time.")
	if imgui.Button("Add Point##wpaadd") {
		addWaypointAhead()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A point after the selected one, carrying on the way the\npath was already going.")
	}
}

// editorWaypointPointList draws one selectable row per point, in path order.
func editorWaypointPointList(a *world.WaypointAnimation) {
	if len(a.Points) == 0 {
		editorTextMuted("(none)")
		return
	}
	ai := Editor.ActiveWaypointAnimIdx
	for i := range a.Points {
		row := waypointRowLabel(i, &a.Points[i]) + fmt.Sprintf("##wplist%d", i)
		if imgui.SelectableV(row, i == Editor.ActiveWaypointIdx, 0, imgui.Vec2{}) {
			editorSelectWaypoint(ai, i)
		}
	}
}

// editorWaypointPointFields draws the selected point's pose and time, the leg
// arriving at it, and the point's own actions.
func editorWaypointPointFields(a *world.WaypointAnimation, idx int, p *world.Waypoint) {
	pos := [3]float32{p.Position.X(), p.Position.Y(), p.Position.Z()}
	if editorDragFloat3("Position##wppos", &pos, -1e6, 1e6, "%.3f") {
		p.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("World space: where the object goes — a brush's centre, anything\n" +
			"else's position. A directional light has none; it only turns.")
	}

	// Wider than the ±360 every other rotation field allows: these numbers are
	// interpolated as written, so 720 is two whole turns rather than a typo.
	rot := vec3ToDegrees(p.Rotation)
	if editorDragFloat3Step("Rotation (deg)##wprot", &rot, -3600, 3600, "%.1f", 1) {
		p.Rotation = vec3FromDegrees(rot)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The object's own turn at this point; a directional light shines\n" +
			"the way the arrow points, and a point light has no turn. Eased\n" +
			"toward as the numbers shown: 0 to 270 is three quarters of a\n" +
			"turn, 0 to 720 two whole ones.")
	}

	s := p.EffectiveScale()
	sc := [3]float32{s.X(), s.Y(), s.Z()}
	if editorDragFloat3Step("Scale##wpscale", &sc, world.MinBrushScale, 1000, "%.2f", 0.01) {
		p.Scale = world.ClampBrushScale(mgl32.Vec3{sc[0], sc[1], sc[2]})
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Multiplies a brush's or an entity's authored size, or a sprite's\n" +
			"width (X) and height (Y). Nothing else has a scale to read.")
	}

	if t := p.Time; editorDragFloatStep("Time (s)##wptime", &t, 0, 3600, "%.2f", waypointTimelineSnap) {
		a.SetPointTime(idx, t)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("When the object arrives here, in seconds from being sent. Held\n" +
			"between the points either side. The first point's time is how\n" +
			"long the object takes to reach it from wherever it was.")
	}
	editorTextMuted(waypointLegSummary(a, idx))

	if imgui.Button("Move To Camera##wpcam") {
		moveActiveWaypointToCamera()
	}
	imgui.SameLine()
	if imgui.Button("Insert After##wpins") {
		addWaypointAhead()
		return
	}

	imguibackend.EditorPushDisabled(idx == 0)
	if imgui.Button("Move Earlier##wpup") {
		moveActiveWaypoint(-1)
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	imguibackend.EditorPushDisabled(idx >= len(a.Points)-1)
	if imgui.Button("Move Later##wpdown") {
		moveActiveWaypoint(1)
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	if imgui.Button("Delete Point##wpdel") {
		deleteActiveWaypoint()
	}
}

// ---------- events ----------

// editorWaypointEventsUI draws the selected point's events — the atoms posted
// to the object as it arrives — each with a remove button, then the add row.
//
// Adding and removing are undoable, on the tag list's rule: they change what a
// script will hear rather than tuning a number.
func editorWaypointEventsUI(p *world.Waypoint) {
	for i, atom := range p.Events {
		imgui.Text(":" + atom + ":")
		imgui.SameLine()
		if imgui.Button(fmt.Sprintf("x##wpevdel%d", i)) {
			removeWaypointEvent(p, i)
			// The list being drawn has just been rewritten; draw the rest of
			// it next frame.
			return
		}
	}
	if len(p.Events) == 0 {
		editorTextMuted("(none)")
	}

	imgui.SetNextItemWidth(160)
	entered := imgui.InputTextV("##wpevnew", &Editor.NewWaypointEventText, imgui.InputTextFlagsEnterReturnsTrue, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("An atom name: lowercase letters and underscores.\nThe colons are optional.")
	}
	imgui.SameLine()
	if imgui.Button("Add Event##wpevadd") || entered {
		addWaypointEvent(p, Editor.NewWaypointEventText)
	}
	editorTextMutedWrapped("Posted to the object when it reaches this point, after :waypoint:, " +
		"as (:atom:, animation ref) — handle it with def f(:atom:, anim ref).")
}

// addWaypointEvent adds an event to a point from what the author typed, and
// reports whether it did. Refused, with the reason in the status bar: a name
// flang cannot match, one the point already posts, and an engine event — a
// handler for `:tick:` expects a dt, not an animation.
func addWaypointEvent(p *world.Waypoint, text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	atom, ok := world.NormalizeWaypointEventAtom(text)
	if !ok {
		editorSetStatus("%q is not an atom: an event name is lowercase letters and underscores", text)
		return false
	}
	for _, engine := range script.EventAtoms() {
		if engine == ":"+atom+":" {
			editorSetStatus(":%s: is an event the engine posts; give this one a name of its own", atom)
			return false
		}
	}
	for _, e := range p.Events {
		if e == atom {
			editorSetStatus("This point already posts :%s:", atom)
			return false
		}
	}
	EditorPushUndo()
	p.Events = append(p.Events, atom)
	Editor.NewWaypointEventText = ""
	editorSetStatus("The point now posts :%s:", atom)
	return true
}

// removeWaypointEvent takes event i off a point. Into a fresh list, so nothing
// still holding the old one sees it shift under it.
func removeWaypointEvent(p *world.Waypoint, i int) {
	if i < 0 || i >= len(p.Events) {
		return
	}
	EditorPushUndo()
	atom := p.Events[i]
	events := append(append([]string(nil), p.Events[:i]...), p.Events[i+1:]...)
	if len(events) == 0 {
		events = nil
	}
	p.Events = events
	editorSetStatus("The point no longer posts :%s:", atom)
}

// waypointLegSummary describes the leg arriving at point idx: how far it goes,
// in how long, and so how fast.
//
// The first leg starts wherever the object is, so its distance can only be
// measured for the object chosen to preview on — from where that object was
// authored, which is where the preview (and play) start it.
func waypointLegSummary(a *world.WaypointAnimation, idx int) string {
	lvl := Editor.level
	to := a.Points[idx]
	to.Position = waypointWorldPosition(lvl, a, to.Position)
	from, fromT := mgl32.Vec3{}, float32(0)
	lead := "Leg"
	if idx > 0 {
		from, fromT = waypointWorldPosition(lvl, a, a.Points[idx-1].Position), a.Points[idx-1].Time
	} else {
		pose, f := waypointPreviewRestPose(lvl)
		if f == nil || !world.WaypointTakesPosition(f) {
			return fmt.Sprintf("Leg: %.2f s from wherever the object is when it is sent.", to.Time)
		}
		from, lead = pose.Position, "Leg from "+Editor.WaypointPreview.Target.ID
	}
	secs, dist := to.Time-fromT, to.Position.Sub(from).Len()
	if secs <= 0 {
		return fmt.Sprintf("%s: %.2f units, instant.", lead, dist)
	}
	return fmt.Sprintf("%s: %.2f units in %.2f s (%.2f/s)", lead, dist, secs, dist/secs)
}

// editorWaypointAnimationActions draws the whole-animation actions.
func editorWaypointAnimationActions() {
	if imgui.Button("Duplicate##wpadup") {
		duplicateWaypointAnimation(Editor.ActiveWaypointAnimIdx)
		return
	}
	imgui.SameLine()
	if imgui.Button("Delete Animation##wpadel") {
		deleteWaypointAnimation(Editor.ActiveWaypointAnimIdx)
	}
}

// ---------- preview ----------

// editorWaypointPreviewUI draws the object picker and the transport. The
// timeline along the bottom of the view has the same controls and a playhead to
// scrub.
func editorWaypointPreviewUI(a *world.WaypointAnimation) {
	p := &Editor.WaypointPreview
	editorWaypointPreviewTargetCombo("Object##wpprevobj")
	if len(editorWaypointTargets(Editor.level)) == 0 {
		editorTextMutedWrapped("Nothing in this level can follow a path yet. Tick Dynamic on a brush, " +
			"or place an entity, sprite, emitter, sound, light or volume.")
	}

	playing := editorWaypointPreviewing(a.ID)
	imguibackend.EditorPushDisabled(p.Target.ID == "" || len(a.Points) == 0)
	if playing {
		if imgui.Button("Stop##wpprevstop") {
			editorWaypointPreviewStop()
		}
	} else if imgui.Button("Play##wpprevplay") {
		editorWaypointPreviewStart(a.ID, p.Target)
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	imgui.Checkbox("Loop##wpprevloop", &p.Loop)

	if playing {
		editorTextMuted(fmt.Sprintf("%.2f s of %.2f s", p.Time, a.EffectiveLength()))
	}
	editorTextMutedWrapped("The object sets off from where it was authored, as it does in play, " +
		"and goes back there when the preview stops. Points can be dragged and retimed while it runs; " +
		"the object itself is held on the path until then.")
}

// editorWaypointPreviewTargetCombo picks the object the preview poses — or
// nothing, which leaves the ghost arrow as the preview. Shared by the Preview
// group and the timeline.
//
// Changing object puts the old one back but keeps the playhead, so the new one
// picks up at the same moment of the path.
func editorWaypointPreviewTargetCombo(label string) {
	p := &Editor.WaypointPreview
	choose := func(t waypointTarget) {
		editorWaypointPreviewRelease()
		p.Target = t
		if p.Playing || p.Time > 0 {
			editorWaypointPreviewPose()
		}
	}
	if imgui.BeginCombo(label, p.Target.label()) {
		if imgui.SelectableV("(nothing)", p.Target.ID == "", 0, imgui.Vec2{}) && p.Target.ID != "" {
			choose(waypointTarget{})
		}
		for _, t := range editorWaypointTargets(Editor.level) {
			if imgui.SelectableV(t.label(), t == p.Target, 0, imgui.Vec2{}) && t != p.Target {
				choose(t)
			}
		}
		imgui.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("What to play the animation on: a dynamic brush, a chunk, an entity,\n" +
			"a sprite, an emitter, a sound, a light or a volume. Each takes the parts\n" +
			"of a point it has — a chunk only its position and Y rotation, a point\n" +
			"light only moves, a directional light only turns.")
	}
}

// editorObjectWaypointsUI is an object's own way into waypoint animations:
// preview one on it, or start a new one from where it is. Drawn in the panel of
// every kind a path can carry; nothing for one a script could not send either.
//
// The entity editor shares the brush panel, and there the paths on offer are the
// type's own (entityObjectWaypointsUI).
func editorObjectWaypointsUI(f world.WaypointFollower) {
	lvl := Editor.level
	t := waypointTargetOf(f)
	if entityGeomLevel != nil && ws().level == entityGeomLevel {
		entityObjectWaypointsUI(t)
		return
	}
	if lvl == nil || ws().level != lvl || t.follower(lvl) == nil {
		return
	}
	editorPropGroup("Waypoints##objwpgrp", 0, func() {
		p := &Editor.WaypointPreview
		cur := p.Anim
		if world.FindWaypointAnimation(lvl, cur) == nil {
			cur = ""
		}
		shown := cur
		if shown == "" {
			shown = "(none)"
		}
		if imgui.BeginCombo("Animation##brushwpanim", shown) {
			for i := range lvl.WaypointAnimations {
				id := lvl.WaypointAnimations[i].ID
				if imgui.SelectableV(id, id == cur, 0, imgui.Vec2{}) && id != cur {
					editorWaypointPreviewStop()
					p.Anim = id
					cur = id
				}
			}
			imgui.EndCombo()
		}

		playing := (p.Playing || p.Posed) && p.Target == t && p.Anim == cur
		imguibackend.EditorPushDisabled(cur == "")
		if playing {
			if imgui.Button("Stop##objwpstop") {
				editorWaypointPreviewStop()
			}
		} else if imgui.Button("Preview##objwpplay") {
			editorWaypointPreviewStart(cur, t)
		}
		imguibackend.EditorPopDisabled()
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Play the animation on this " + hooks.WaypointObjectNames[t.Kind] + " without entering play.")
		}

		if imgui.Button("New Animation From Here##objwpnew") {
			addWaypointAnimationFromObject(t)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("A new waypoint animation whose first point is this " + hooks.WaypointObjectNames[t.Kind] + ",\n" +
				"where it is and turned as it is. Ctrl+click in the view afterwards\n" +
				"to add the next point.")
		}
	})
}

// ---------- context menu ----------

// editor3DWaypointMenuItems is the right-click menu for a waypoint selection.
func editor3DWaypointMenuItems() []ctxMenuItem {
	a := activeWaypointAnimation()
	if a == nil {
		return nil
	}
	id := a.ID
	items := []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(panelFocusWaypoint) }},
		{Label: "Add point here", Shortcut: "Ctrl+click", Do: func() { insertWaypointAt(editor3DMenuPoint) },
			Tooltip: "After the selected point, where the ray under the cursor met the world."},
	}
	if t := Editor.WaypointPreview.Target; t.ID != "" {
		items = append(items, ctxMenuItem{Label: "Preview on " + t.label(),
			Do: func() { editorWaypointPreviewStart(id, t) }})
	}
	items = append(items,
		ctxMenuItem{Separator: true},
		ctxMenuItem{Label: "Snap to grid", Disabled: !editor3DCanSnap(), Do: editor3DSnapSelectionToGrid},
	)
	if activeWaypoint() != nil {
		items = append(items, ctxMenuItem{Label: "Delete point", Shortcut: "Del", Do: deleteActiveWaypoint})
	}
	items = append(items, ctxMenuItem{Label: "Delete animation",
		Do: func() { deleteWaypointAnimation(Editor.ActiveWaypointAnimIdx) }})
	return items
}
