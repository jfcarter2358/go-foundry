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
	"foundry/internal/imguibackend"
	"foundry/internal/platform"
	"foundry/internal/world"
	"math"

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
	editorClearSpawnSelection()
	editorClearEntitySelection()
	editorClearVolumeSelection()
	editorClearChunkSelection()
	clearVertexSelection()
	clearFaceSelection()

	Editor.SelectedLightKind = kind
	Editor.SelectedLightIdx = idx
	Editor.PanelFocus = panelFocusLight
}

// editorClearLightSelection drops the light from the selection.
//
// No gizmo to take with it: a point light's position is dragged through the
// ordinary translate gizmo only once it is the selection, and a directional
// light has no position at all.
func editorClearLightSelection() {
	Editor.SelectedLightKind = lightKindNone
	Editor.SelectedLightIdx = -1
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

// ---------- Objects: the list ----------

// editorLightListSection draws the lights as three lists, the way the Chunks
// and Entities sections above it draw theirs: a row per light, selectable, with
// the buttons that act on it riding alongside.
func editorLightListSection(lvl *world.Level) {
	imgui.Separator()
	if !imgui.CollapsingHeaderV("Lights", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	// The global light is not a list — a level has exactly one and it cannot be
	// removed — so it is a single row with the same switch and the same click
	// target the others have.
	globalOn := !lvl.Lights.Global.Disabled
	if imgui.Checkbox("##globallightOn", &globalOn) {
		// Through a temporary, because the field is stored inverted: binding the
		// checkbox straight to Disabled would put a tick on a light that is off.
		EditorPushUndo()
		lvl.Lights.Global.Disabled = !globalOn
		world.UploadLights(lvl.Lights)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Switch the ambient light off without losing its colour\nand brightness. It cannot be removed.")
	}
	imgui.SameLine()
	if imgui.SelectableV("Global Light##globallightrow",
		Editor.SelectedLightKind == lightKindGlobal, 0, imgui.Vec2{}) {
		editorSelectLight(lightKindGlobal, 0)
	}

	editorDirLightList(lvl)
	editorPointLightList(lvl)
}

// editorDirLightList draws the directional lights and the add button.
func editorDirLightList(lvl *world.Level) {
	imgui.Text(fmt.Sprintf("Directional (%d)", len(lvl.Lights.Directional)))
	imgui.SameLine()
	imguibackend.EditorPushDisabled(len(lvl.Lights.Directional) >= world.MaxDirLights)
	if imgui.Button("+##dirlightadd") {
		EditorPushUndo()
		// Angled by default: straight down (pitch -90) lights every
		// upward-facing surface identically and makes yaw a no-op, which reads
		// as "the light can't be aimed".
		nl := world.DirectionalLight{
			Yaw:         world.DefaultDirLightYaw,
			Pitch:       world.DefaultDirLightPitch,
			Color:       mgl32.Vec3{1, 1, 1},
			Brightness:  1.0,
			CastShadows: true,
			// Must be non-zero, or the shadow map is rendered and then
			// multiplied out by (1 - shadow * 0) and the light reads as casting
			// nothing. Matches the point light default.
			ShadowStrength: 0.5,
		}
		nl.SyncDirection()
		lvl.Lights.Directional = append(lvl.Lights.Directional, nl)
		editorSelectLight(lightKindDir, len(lvl.Lights.Directional)-1)
		world.UploadLights(lvl.Lights)
	}
	imguibackend.EditorPopDisabled()
	if len(lvl.Lights.Directional) >= world.MaxDirLights && imgui.IsItemHovered() {
		imgui.SetTooltip(fmt.Sprintf("The shader carries %d directional lights.", world.MaxDirLights))
	}

	remove, duplicate := -1, -1
	for i := range lvl.Lights.Directional {
		dl := &lvl.Lights.Directional[i]
		imgui.PushID(fmt.Sprintf("dirlight%d", i))
		if imgui.Button("x") {
			remove = i
		}
		imgui.SameLine()
		imguibackend.EditorPushDisabled(len(lvl.Lights.Directional) >= world.MaxDirLights)
		if imgui.Button("+") {
			duplicate = i
		}
		imguibackend.EditorPopDisabled()
		imgui.SameLine()
		on := !dl.Disabled
		if imgui.Checkbox("##on", &on) {
			EditorPushUndo()
			dl.Disabled = !on
			world.UploadLights(lvl.Lights)
		}
		imgui.SameLine()
		selected := Editor.SelectedLightKind == lightKindDir && Editor.SelectedLightIdx == i
		if imgui.SelectableV(editorDirLightLabel(dl, i), selected, 0, imgui.Vec2{}) {
			editorSelectLight(lightKindDir, i)
		}
		imgui.PopID()
	}

	if duplicate >= 0 {
		EditorPushUndo()
		copy := lvl.Lights.Directional[duplicate]
		copy.ID = ""
		copy.ScriptData = world.CloneScriptData(copy.ScriptData)
		lvl.Lights.Directional = append(lvl.Lights.Directional, copy)
		world.AssignLightIDs(&lvl.Lights)
		editorSelectLight(lightKindDir, len(lvl.Lights.Directional)-1)
		world.UploadLights(lvl.Lights)
	}
	if remove >= 0 {
		EditorPushUndo()
		lvl.Lights.Directional = append(lvl.Lights.Directional[:remove], lvl.Lights.Directional[remove+1:]...)
		editorLightRemoved(lightKindDir, remove)
		world.UploadLights(lvl.Lights)
	}
}

// editorPointLightList draws the point lights and the add button.
func editorPointLightList(lvl *world.Level) {
	imgui.Text(fmt.Sprintf("Point (%d)", len(lvl.Lights.Points)))
	imgui.SameLine()
	imguibackend.EditorPushDisabled(len(lvl.Lights.Points) >= world.MaxPointLights)
	if imgui.Button("+##ptlightadd") {
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
	imguibackend.EditorPopDisabled()
	if len(lvl.Lights.Points) >= world.MaxPointLights && imgui.IsItemHovered() {
		imgui.SetTooltip(fmt.Sprintf("The shader carries %d point lights.", world.MaxPointLights))
	}

	remove, duplicate := -1, -1
	for i := range lvl.Lights.Points {
		pl := &lvl.Lights.Points[i]
		imgui.PushID(fmt.Sprintf("ptlight%d", i))
		if imgui.Button("x") {
			remove = i
		}
		imgui.SameLine()
		imguibackend.EditorPushDisabled(len(lvl.Lights.Points) >= world.MaxPointLights)
		if imgui.Button("+") {
			duplicate = i
		}
		imguibackend.EditorPopDisabled()
		imgui.SameLine()
		on := !pl.Disabled
		if imgui.Checkbox("##on", &on) {
			EditorPushUndo()
			pl.Disabled = !on
			world.UploadLights(lvl.Lights)
		}
		imgui.SameLine()
		selected := Editor.SelectedLightKind == lightKindPoint && Editor.SelectedLightIdx == i
		if imgui.SelectableV(editorPointLightLabel(pl, i), selected, 0, imgui.Vec2{}) {
			editorSelectLight(lightKindPoint, i)
		}
		imgui.PopID()
	}

	if duplicate >= 0 {
		EditorPushUndo()
		copy := lvl.Lights.Points[duplicate]
		copy.ID = ""
		copy.ScriptData = world.CloneScriptData(copy.ScriptData)
		lvl.Lights.Points = append(lvl.Lights.Points, copy)
		world.AssignLightIDs(&lvl.Lights)
		editorSelectLight(lightKindPoint, len(lvl.Lights.Points)-1)
		world.UploadLights(lvl.Lights)
	}
	if remove >= 0 {
		EditorPushUndo()
		lvl.Lights.Points = append(lvl.Lights.Points[:remove], lvl.Lights.Points[remove+1:]...)
		editorLightRemoved(lightKindPoint, remove)
		world.UploadLights(lvl.Lights)
	}
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

	if dl.ID != "" {
		imgui.Text("ID: " + dl.ID)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("A script reaches this light as @" + dl.ID)
		}
	}

	on := !dl.Disabled
	if imgui.Checkbox(fmt.Sprintf("Enabled##diron%d", i), &on) {
		EditorPushUndo()
		dl.Disabled = !on
		changed = true
	}

	// Yaw/pitch instead of raw vector components: a vector edited one component
	// at a time passes through {0,0,0}, and normalizing that yields NaN.
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

	col := [3]float32{dl.Color.X(), dl.Color.Y(), dl.Color.Z()}
	if imgui.ColorEdit3(fmt.Sprintf("Color##dir%d", i), &col) {
		dl.Color = mgl32.Vec3{col[0], col[1], col[2]}
		changed = true
	}
	if editorDragFloat(fmt.Sprintf("Brightness##dir%d", i), &dl.Brightness, 0, 10, "%.2f") {
		changed = true
	}
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

	imgui.Separator()
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

	if pl.ID != "" {
		imgui.Text("ID: " + pl.ID)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("A script reaches this light as @" + pl.ID)
		}
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
	if imgui.Button(fmt.Sprintf("Move To Camera##pt%d", i)) {
		EditorPushUndo()
		pl.Position = gfx.Cam.Position
		changed = true
	}

	imgui.Separator()
	editorScriptDataUI(&pl.ScriptData, fmt.Sprintf("ptlight%d", i), "This light")

	if changed {
		world.UploadLights(lvl.Lights)
	}
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
