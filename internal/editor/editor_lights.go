package editor

// editor_lights.go — light selection, the Objects list, and the Properties
// block.
//
// The panel used to draw one "Lights" section holding both: a tree of lights,
// each of which expanded into its own editable fields. Which light the
// direction indicator pointed at was then decided by which tree node happened
// to be open, so collapsing a section deselected what it was showing and
// opening two showed the first.
//
// Split now the way every other object is split — a list under Objects, a
// properties block under Properties, joined by a selection — so a light is
// picked once and stays picked, and the block reads like an entity's or a
// volume's rather than like a tree of its own.
//
// UploadLights() is called immediately on any change, so the 3D view updates
// live.

import (
	"fmt"
	"foundry/internal/gfx"
	"foundry/internal/platform"
	"foundry/internal/world"
	"math"
	"slices"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// lightKind* are the values EditorState.SelectedLightKind takes.
const (
	lightKindNone   = ""
	lightKindGlobal = "global"
	lightKindDir    = "dir"
	lightKindPoint  = "point"
)

// ---------- selection ----------

// editorSelectLight makes one light the selection, which is what opens its
// properties block and — for a directional light — aims the on-screen
// direction indicator.
func editorSelectLight(kind string, idx int) {
	editorClearObjectSelections()

	Editor.SelectedLightKind = kind
	Editor.SelectedLightIdx = idx
	Editor.PanelFocus = panelFocusLight

	// Only a point light gets the gizmo: it is the only light with a position.
	// The global light is everywhere and a directional light is a direction, so
	// there is nothing at a place for arms to be drawn around.
	if kind == lightKindPoint {
		Editor.GizmoKind = gizmoKindLight
	} else if Editor.GizmoKind == gizmoKindLight {
		Editor.GizmoKind = gizmoKindNone
	}
}

// editorClearLightSelection drops the light from the selection, taking the
// gizmo with it so it cannot keep dragging a light that is no longer selected.
func editorClearLightSelection() {
	Editor.SelectedLightKind = lightKindNone
	Editor.SelectedLightIdx = -1
	if Editor.GizmoKind == gizmoKindLight {
		Editor.GizmoKind = gizmoKindNone
	}
}

// activeGizmoPointLight returns the point light the gizmo is attached to, or
// nil. The gizmo kind is checked as well as the selection, the same guard
// activeGizmoVolume applies, so a stale selection left by another tool cannot
// silently be dragged.
func activeGizmoPointLight() *world.PointLight {
	if Editor.GizmoKind != gizmoKindLight {
		return nil
	}
	pl, _ := selectedPointLight()
	return pl
}

// selectedDirLight returns the selected directional light and its index, or
// nil. Used by the properties block and by the direction indicator, so the two
// cannot disagree about which light is being shown.
func selectedDirLight() (*world.DirectionalLight, int) {
	lvl := Editor.level
	if lvl == nil || Editor.SelectedLightKind != lightKindDir {
		return nil, -1
	}
	i := Editor.SelectedLightIdx
	if i < 0 || i >= len(lvl.Lights.Directional) {
		return nil, -1
	}
	return &lvl.Lights.Directional[i], i
}

// selectedPointLight returns the selected point light and its index, or nil.
func selectedPointLight() (*world.PointLight, int) {
	lvl := Editor.level
	if lvl == nil || Editor.SelectedLightKind != lightKindPoint {
		return nil, -1
	}
	i := Editor.SelectedLightIdx
	if i < 0 || i >= len(lvl.Lights.Points) {
		return nil, -1
	}
	return &lvl.Lights.Points[i], i
}

// editorLightSelected reports whether any light is the current selection.
func editorLightSelected() bool {
	if Editor.SelectedLightKind == lightKindGlobal {
		return true
	}
	if dl, _ := selectedDirLight(); dl != nil {
		return true
	}
	pl, _ := selectedPointLight()
	return pl != nil
}

// ---------- Explorer: creating one ----------

// editorAddDirLight appends a directional light and selects it. Called from
// the Explorer's Add menu; the rows themselves live in
// editor_panel_explorer.go, alongside every other object's.
func editorAddDirLight(lvl *world.Level) {
	if len(lvl.Lights.Directional) >= world.MaxDirLights {
		editorSetStatus("The shader carries %d directional lights", world.MaxDirLights)
		return
	}
	EditorPushUndo()
	// Angled by default: straight down (pitch -90) lights every upward-facing
	// surface identically and makes yaw a no-op, which reads as "the light
	// can't be aimed".
	nl := world.DirectionalLight{
		Yaw:         world.DefaultDirLightYaw,
		Pitch:       world.DefaultDirLightPitch,
		Color:       mgl32.Vec3{1, 1, 1},
		Brightness:  1.0,
		CastShadows: true,
		// Must be non-zero, or the shadow map is rendered and then multiplied
		// out by (1 - shadow * 0) and the light reads as casting nothing.
		// Matches the point light default.
		ShadowStrength: 0.5,
	}
	nl.SyncDirection()
	lvl.Lights.Directional = append(lvl.Lights.Directional, nl)
	editorSelectLight(lightKindDir, len(lvl.Lights.Directional)-1)
	world.UploadLights(lvl.Lights)
}

// editorAddPointLight appends a point light at the camera and selects it.
func editorAddPointLight(lvl *world.Level) {
	if len(lvl.Lights.Points) >= world.MaxPointLights {
		editorSetStatus("The shader carries %d point lights", world.MaxPointLights)
		return
	}
	EditorPushUndo()
	lvl.Lights.Points = append(lvl.Lights.Points, world.PointLight{
		Position:       gfx.Cam.Position,
		Color:          mgl32.Vec3{1, 0.9, 0.7},
		Brightness:     2.0,
		Radius:         10.0,
		CastShadows:    true,
		ShadowStrength: 0.5,
	})
	editorSelectLight(lightKindPoint, len(lvl.Lights.Points)-1)
	world.UploadLights(lvl.Lights)
}

// editorLightRemoved fixes up the selection after a light is deleted: the one
// below it slides into its index, so keeping the index would silently select a
// different light than the one that was on screen.
func editorLightRemoved(kind string, removed int) {
	if Editor.SelectedLightKind != kind {
		return
	}
	switch {
	case Editor.SelectedLightIdx == removed:
		editorClearLightSelection()
	case Editor.SelectedLightIdx > removed:
		Editor.SelectedLightIdx--
	}
}

// editorDirLightLabel names a row by the light's ID, falling back to its index
// for a light that has not been through a save yet.
func editorDirLightLabel(dl *world.DirectionalLight, i int) string {
	if dl.ID != "" {
		return dl.ID
	}
	return fmt.Sprintf("Dir Light %d", i)
}

func editorPointLightLabel(pl *world.PointLight, i int) string {
	if pl.ID != "" {
		return pl.ID
	}
	return fmt.Sprintf("Point Light %d", i)
}

// ---------- Properties: the selected light ----------

// editorLightPropertiesSection draws the selected light's fields. Nothing is
// drawn when no light is selected, so the caller can call it unconditionally.
func editorLightPropertiesSection(lvl *world.Level) {
	if lvl == nil || !editorLightSelected() {
		return
	}

	imgui.Separator()
	editorFocusOpen(panelFocusLight)
	title, kind := "Light", Editor.SelectedLightKind
	switch kind {
	case lightKindGlobal:
		title = "Global Light"
	case lightKindDir:
		dl, i := selectedDirLight()
		title = "Directional: " + editorDirLightLabel(dl, i)
	case lightKindPoint:
		pl, i := selectedPointLight()
		title = "Point: " + editorPointLightLabel(pl, i)
	}
	if !imgui.CollapsingHeaderV(title+"##lightprops", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusLight)

	switch kind {
	case lightKindGlobal:
		editorGlobalLightProperties(lvl)
	case lightKindDir:
		editorDirLightProperties(lvl)
	case lightKindPoint:
		editorPointLightProperties(lvl)
	}
}

func editorGlobalLightProperties(lvl *world.Level) {
	editorPropGroupOpen("Light##globallightgrp", func() { editorGlobalLightBody(lvl) })
}

func editorGlobalLightBody(lvl *world.Level) {
	g := &lvl.Lights.Global
	changed := false

	on := !g.Disabled
	if imgui.Checkbox("Enabled##globalon", &on) {
		EditorPushUndo()
		g.Disabled = !on
		changed = true
	}
	col := [3]float32{g.Color.X(), g.Color.Y(), g.Color.Z()}
	if imgui.ColorEdit3("Color##global", &col) {
		g.Color = mgl32.Vec3{col[0], col[1], col[2]}
		changed = true
	}
	if editorDragFloat("Brightness##global", &g.Brightness, 0, 10, "%.2f") {
		changed = true
	}
	if g.Disabled {
		editorTextMutedWrapped("Switched off — the colour and brightness above are kept.")
	}

	if changed {
		world.UploadLights(lvl.Lights)
	}
}

func editorDirLightProperties(lvl *world.Level) {
	dl, i := selectedDirLight()
	if dl == nil {
		return
	}
	changed := false

	// Light, Aim, Shadows and Scripting: four subjects, four groups. Shadows
	// especially — every light carries a pair of shadow fields, and unfolded
	// they doubled the block for something most lights never change. Bake
	// excludes is a fifth, shown only for a static light that still bakes at
	// all (Leave out of bake off) — anything else has no chunk list to offer.
	editorPropGroupOpen("Light##dirlightgrp", func() {
		if dl.ID != "" {
			editorTextMuted("A script reaches this light as @" + dl.ID)
		}
		on := !dl.Disabled
		if imgui.Checkbox(fmt.Sprintf("Enabled##diron%d", i), &on) {
			EditorPushUndo()
			dl.Disabled = !on
			changed = true
		}
		col := [3]float32{dl.Color.X(), dl.Color.Y(), dl.Color.Z()}
		if imgui.ColorEdit3(fmt.Sprintf("Color##dir%d", i), &col) {
			dl.Color = mgl32.Vec3{col[0], col[1], col[2]}
			changed = true
		}
		if editorDragFloat(fmt.Sprintf("Brightness##dir%d", i), &dl.Brightness, 0, 10, "%.2f") {
			changed = true
		}
		if editorLightDynamicCheckbox(fmt.Sprintf("dir%d", i), &dl.Dynamic) {
			changed = true
		}
		if !dl.Dynamic && editorLightNoBakeCheckbox(fmt.Sprintf("dir%d", i), &dl.NoBake) {
			changed = true
		}
	})

	if !dl.Dynamic && !dl.NoBake {
		editorPropGroupOpen(fmt.Sprintf("Bake excludes##dirbakeexcl%d", i), func() {
			if editorLightBakeExcludeUI(fmt.Sprintf("dir%d", i), &dl.BakeExclude) {
				changed = true
			}
		})
	}

	editorPropGroupOpen("Aim##dirlightaim", func() {
		// Yaw/pitch instead of raw vector components: a vector edited one
		// component at a time passes through {0,0,0}, and normalizing that
		// yields NaN.
		rotated := false
		if editorDragFloatStep(fmt.Sprintf("Yaw (deg)##diryaw%d", i), &dl.Yaw, -360, 360, "%.1f", 1) {
			rotated = true
		}
		if editorDragFloatStep(fmt.Sprintf("Pitch (deg)##dirpitch%d", i), &dl.Pitch, -90, 90, "%.1f", 1) {
			rotated = true
		}
		if rotated {
			dl.SyncDirection()
			changed = true
		}
		imgui.Text(fmt.Sprintf("Direction: %.2f, %.2f, %.2f",
			dl.Direction.X(), dl.Direction.Y(), dl.Direction.Z()))
	})

	editorPropGroup("Shadows##dirlightshadow", 0, func() {
		if imgui.Checkbox(fmt.Sprintf("Cast Shadows##dir%d", i), &dl.CastShadows) {
			if dl.CastShadows && dl.ShadowStrength == 0 {
				dl.ShadowStrength = 0.5
			}
			changed = true
		}
		if dl.CastShadows {
			if editorDragFloat(fmt.Sprintf("Shadow Strength##dir%d", i), &dl.ShadowStrength, 0, 1, "%.2f") {
				changed = true
			}
		}
	})

	editorStaticLightWaypointsNote(dl.Dynamic)
	editorObjectWaypointsUI(dl)

	editorScriptDataUI(&dl.ScriptData, fmt.Sprintf("dirlight%d", i), "This light")

	if changed {
		world.UploadLights(lvl.Lights)
	}
}

func editorPointLightProperties(lvl *world.Level) {
	pl, i := selectedPointLight()
	if pl == nil {
		return
	}
	changed := false

	editorPropGroupOpen("Light##ptlightgrp", func() {
		if pl.ID != "" {
			editorTextMuted("A script reaches this light as @" + pl.ID)
		}
		on := !pl.Disabled
		if imgui.Checkbox(fmt.Sprintf("Enabled##pton%d", i), &on) {
			EditorPushUndo()
			pl.Disabled = !on
			changed = true
		}
		col := [3]float32{pl.Color.X(), pl.Color.Y(), pl.Color.Z()}
		if imgui.ColorEdit3(fmt.Sprintf("Color##pt%d", i), &col) {
			pl.Color = mgl32.Vec3{col[0], col[1], col[2]}
			changed = true
		}
		if editorDragFloat(fmt.Sprintf("Brightness##pt%d", i), &pl.Brightness, 0, 10, "%.2f") {
			changed = true
		}
		if editorDragFloat(fmt.Sprintf("Radius##pt%d", i), &pl.Radius, 0.1, 200, "%.1f") {
			changed = true
		}
		if editorLightDynamicCheckbox(fmt.Sprintf("pt%d", i), &pl.Dynamic) {
			if !pl.Attach.IsZero() && !pl.Dynamic {
				// A light riding something moves, and a static light is baked
				// where it stands. Detach it first to make it static.
				pl.Dynamic = true
				editorSetStatus("An attached light has to be dynamic — detach it to bake it")
			}
			changed = true
		}
		if !pl.Dynamic && editorLightNoBakeCheckbox(fmt.Sprintf("pt%d", i), &pl.NoBake) {
			changed = true
		}
	})

	if !pl.Dynamic && !pl.NoBake {
		editorPropGroupOpen(fmt.Sprintf("Bake excludes##ptbakeexcl%d", i), func() {
			if editorLightBakeExcludeUI(fmt.Sprintf("pt%d", i), &pl.BakeExclude) {
				changed = true
			}
		})
	}

	editorPropGroupOpen("Transform##ptlighttf", func() {
		// Position, typed. The translate gizmo in the 3D view edits the same
		// three numbers — a light was previously only movable by the "Move To
		// Camera" button, which meant flying the camera to wherever you wanted
		// the light and losing the view you were lighting.
		pos := [3]float32{pl.Position.X(), pl.Position.Y(), pl.Position.Z()}
		if editorDragFloat3Step(fmt.Sprintf("Position##ptpos%d", i), &pos, 0, 0, "%.3f", 0.05) {
			pl.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
			changed = true
		}
		if imgui.Button(fmt.Sprintf("Move To Camera##pt%d", i)) {
			EditorPushUndo()
			// The camera is in the world; an attached light stores it in its
			// carrier's space.
			pl.SetWorldPosition(lvl, gfx.Cam.Position)
			changed = true
		}
	})

	editorPropGroup("Attach##ptlightattach", 0, func() {
		if editorPointLightAttachUI(lvl, pl, fmt.Sprintf("pt%d", i)) {
			changed = true
		}
	})

	editorPropGroup("Shadows##ptlightshadow", 0, func() {
		if imgui.Checkbox(fmt.Sprintf("Cast Shadows##pt%d", i), &pl.CastShadows) {
			if pl.CastShadows && pl.ShadowStrength == 0 {
				pl.ShadowStrength = 0.5
			}
			changed = true
		}
		if pl.CastShadows {
			if editorDragFloat(fmt.Sprintf("Shadow Strength##pt%d", i), &pl.ShadowStrength, 0, 1, "%.2f") {
				changed = true
			}
		}
	})

	editorStaticLightWaypointsNote(pl.Dynamic)
	editorObjectWaypointsUI(pl)

	editorScriptDataUI(&pl.ScriptData, fmt.Sprintf("ptlight%d", i), "This light")

	if changed {
		world.UploadLights(lvl.Lights)
	}
}

// editorPointLightAttachUI is the Attach group of a point light: the shared
// frame/entity/model/bone rows, plus the one rule lights add — a light that
// rides something moves, so it cannot be baked, so it is made Dynamic.
func editorPointLightAttachUI(lvl *world.Level, pl *world.PointLight, id string) bool {
	at := pl.Attach
	changed := false
	if editorAttachUI(lvl, &at, "ptl"+id) {
		EditorPushUndo()
		// Position is left as typed, as a sprite's is: attached, it is an
		// offset from the carrier, and zero puts the light right on the bone.
		pl.Attach = at
		if !at.IsZero() && !pl.Dynamic {
			pl.Dynamic = true
			editorSetStatus("Attached lights move, so %s is now dynamic", editorPointLightName(pl))
		}
		changed = true
	}
	if pl.Attach.IsZero() {
		editorTextMutedWrapped("Unattached: Position is world-space.")
	} else {
		editorTextMutedWrapped("Position is local to " + pl.Attach.Describe() +
			". Attached, the light is dynamic: it moves with what it rides, and a baked light cannot.")
	}
	return changed
}

// editorPointLightName is how a status line names a point light.
func editorPointLightName(pl *world.PointLight) string {
	if pl.ID != "" {
		return pl.ID
	}
	return "the light"
}

// editorLightDynamicCheckbox draws a light's Dynamic switch, reporting a change.
// Undo is pushed before the flip, like Enabled, because the checkbox is the
// whole edit.
func editorLightDynamicCheckbox(id string, dynamic *bool) bool {
	on := *dynamic
	changed := false
	if imgui.Checkbox("Dynamic##lightdyn"+id, &on) {
		EditorPushUndo()
		*dynamic = on
		changed = true
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A torch carried past, a flickering lamp, a sweeping searchlight:\n" +
			"a script can change this light, and waypoint animations can move it.\n\n" +
			"Off (static), the light is baked into the compiled level's walls and\n" +
			"floors, which is free to draw but frozen as authored. On, it is left\n" +
			"out of the bake and lit live on everything, every frame, with its\n" +
			"shadow maps. Uncompiled levels light both kinds live.")
	}
	return changed
}

// editorLightNoBakeCheckbox is the no_bake toggle, offered only on a static
// light (a dynamic one is never baked anyway).
func editorLightNoBakeCheckbox(id string, noBake *bool) bool {
	on := *noBake
	changed := false
	if imgui.Checkbox("Leave out of bake##lightnobake"+id, &on) {
		EditorPushUndo()
		*noBake = on
		changed = true
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("For a light that belongs to geometry the compiler does not bake,\n" +
			"such as a hangar marked Moves. The light stays static but is lit\n" +
			"live instead of being baked onto whatever it shines on, so it does\n" +
			"not stay behind after that geometry has gone. A script may switch\n" +
			"it on and off, and change nothing else.")
	}
	return changed
}

// editorLightBakeExcludeUI draws a static light's list of chunks its bake
// skips, and its add row. Offered only alongside editorLightNoBakeCheckbox,
// where "Leave out of bake" is off — that flag already means the light bakes
// nowhere, which makes naming individual chunks to leave out moot.
//
// A plain add/remove list rather than editorTagsUI: these are chunk IDs, not
// `#tag`s, and NormalizeTags' sort-and-lowercase would silently mismatch a
// chunk whose ID is not already lowercase.
func editorLightBakeExcludeUI(id string, list *[]string) bool {
	changed := false
	for _, c := range *list {
		imgui.Text(c)
		imgui.SameLine()
		if imgui.Button("x##bakeexcl" + id + c) {
			EditorPushUndo()
			for i, cur := range *list {
				if cur == c {
					*list = append((*list)[:i], (*list)[i+1:]...)
					break
				}
			}
			changed = true
			// The list being iterated has just been rewritten; leave now and
			// draw the rest of it next frame, as editorTagsUIBody does.
			return changed
		}
	}
	if len(*list) == 0 {
		imgui.Text("(none)")
	}
	imgui.InputTextV("##newbakeexcl"+id, &Editor.NewBakeExcludeText, 0, nil)
	imgui.SameLine()
	if imgui.Button("Exclude Chunk##bakeexcladd"+id) && Editor.NewBakeExcludeText != "" {
		if !slices.Contains(*list, Editor.NewBakeExcludeText) {
			EditorPushUndo()
			*list = append(*list, Editor.NewBakeExcludeText)
			changed = true
		}
		Editor.NewBakeExcludeText = ""
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A chunk ID (the Explorer panel's chunk list) this light's bake\n" +
			"skips, while it keeps lighting that chunk live and keeps baking\n" +
			"normally everywhere else. For a chunk with no lasting reason to\n" +
			"carry this light baked in — a hangar the level disables for good,\n" +
			"say, lit for the moment by the sun the ground outside bakes.")
	}
	return changed
}

// editorStaticLightWaypointsNote explains, above the Waypoints group, why a
// static light's path can be previewed here and never played in game.
// Only once the level has an animation to pick, which is when the group shows.
func editorStaticLightWaypointsNote(dynamic bool) {
	if dynamic || Editor.level == nil || len(Editor.level.WaypointAnimations) == 0 {
		return
	}
	editorTextMutedWrapped("Static: baked into the level, so play_waypoints and script " +
		"writes refuse this light. Tick Dynamic to change it in game.")
}

// ---------- selected directional light indicator ----------

// dirLightIndicatorSize is the width/height of the indicator card in pixels.
const dirLightIndicatorSize float32 = 128

// EditorDirLightIndicator draws a small compass/elevation card in the top-right
// of the viewport (just left of the side panel) showing which way the selected
// directional light points. Yaw alone is hard to read off three numbers, and the
// light itself has no position to hang a world-space gizmo on.
//
// Drawn on the foreground draw list, so it must be called between
// ImGuiNewFrame and ImGuiRender, and after the side panel that sets the
// selection.
func EditorDirLightIndicator() {
	dl, i := selectedDirLight()
	if dl == nil {
		return
	}

	dList := imgui.ForegroundDrawList()

	const pad float32 = 12
	sz := dirLightIndicatorSize
	x1 := float32(platform.WinWidth) - Editor.PanelWidth - pad
	x0 := x1 - sz
	y0 := 28 + pad
	y1 := y0 + sz + 46 // extra room for the title, elevation view, and readout

	bg := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.05, Y: 0.05, Z: 0.07, W: 0.78})
	border := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.5, Y: 0.5, Z: 0.55, W: 0.9})
	dim := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.45, Y: 0.45, Z: 0.5, W: 0.9})
	label := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.8, Y: 0.8, Z: 0.85, W: 0.95})
	// Tint the arrows with the light's own color so multiple lights are
	// distinguishable; fall back to a warm yellow for near-black lights.
	c := dl.Color
	if c.Len() < 0.15 {
		c = mgl32.Vec3{1, 0.85, 0.3}
	} else {
		c = c.Normalize()
	}
	arrow := imgui.PackedColorFromVec4(imgui.Vec4{X: c.X(), Y: c.Y(), Z: c.Z(), W: 1})

	dList.AddRectFilledV(imgui.Vec2{X: x0, Y: y0}, imgui.Vec2{X: x1, Y: y1}, bg, 6, imgui.DrawFlagsRoundCornersAll)
	dList.AddRectV(imgui.Vec2{X: x0, Y: y0}, imgui.Vec2{X: x1, Y: y1}, border, 6, imgui.DrawFlagsRoundCornersAll, 1)
	dList.AddText(imgui.Vec2{X: x0 + 8, Y: y0 + 5}, label, editorDirLightLabel(dl, i))

	yaw := float64(mgl32.DegToRad(dl.Yaw))
	pitch := float64(mgl32.DegToRad(dl.Pitch))

	// --- Compass (top-down): +X right, +Z down, matching the 2D editor view ---
	r := sz*0.5 - 30
	cx := x0 + sz*0.5
	cy := y0 + 26 + r
	dList.AddCircleV(imgui.Vec2{X: cx, Y: cy}, r, dim, 24, 1)
	dList.AddLine(imgui.Vec2{X: cx - r, Y: cy}, imgui.Vec2{X: cx + r, Y: cy}, dim)
	dList.AddLine(imgui.Vec2{X: cx, Y: cy - r}, imgui.Vec2{X: cx, Y: cy + r}, dim)
	dList.AddText(imgui.Vec2{X: cx + r + 3, Y: cy - 7}, dim, "+X")
	dList.AddText(imgui.Vec2{X: cx - 8, Y: cy + r + 1}, dim, "+Z")
	// Horizontal component only — shortened as the light nears vertical, so a
	// straight-down light shows a dot rather than a misleading heading.
	horiz := float32(math.Cos(pitch))
	drawIndicatorArrow(dList, cx, cy,
		float32(math.Cos(yaw))*horiz*r, float32(math.Sin(yaw))*horiz*r, arrow)

	// --- Elevation (side view): horizon line, arrow tilted by pitch ---
	ey := cy + r + 18
	dList.AddLine(imgui.Vec2{X: x0 + 10, Y: ey}, imgui.Vec2{X: x1 - 10, Y: ey}, dim)
	ex := x0 + 22
	drawIndicatorArrow(dList, ex, ey,
		float32(math.Cos(pitch))*r, -float32(math.Sin(pitch))*r, arrow)

	dList.AddText(imgui.Vec2{X: x0 + 8, Y: y1 - 16}, label,
		fmt.Sprintf("yaw %.0f°  pitch %.0f°", dl.Yaw, dl.Pitch))
}

// drawIndicatorArrow draws a line from (ox, oy) along (dx, dy) with a filled
// head at the tip. A near-zero vector degenerates to a dot.
func drawIndicatorArrow(dList imgui.DrawList, ox, oy, dx, dy float32, col imgui.PackedColor) {
	length := float32(math.Hypot(float64(dx), float64(dy)))
	if length < 1.5 {
		dList.AddCircleFilled(imgui.Vec2{X: ox, Y: oy}, 3, col)
		return
	}
	ux, uy := dx/length, dy/length
	tipX, tipY := ox+dx, oy+dy
	dList.AddLineV(imgui.Vec2{X: ox, Y: oy}, imgui.Vec2{X: tipX, Y: tipY}, col, 2)
	const head float32 = 8
	baseX, baseY := tipX-ux*head, tipY-uy*head
	// Perpendicular in screen space.
	px, py := -uy*head*0.45, ux*head*0.45
	dList.AddTriangleFilled(
		imgui.Vec2{X: tipX, Y: tipY},
		imgui.Vec2{X: baseX + px, Y: baseY + py},
		imgui.Vec2{X: baseX - px, Y: baseY - py},
		col,
	)
}
