package editor

// editor_ui.go — top-level ImGui panel orchestration for the level editor.
//
// EditorDrawUI() is called each frame between ImGuiNewFrame and ImGuiRender.
// It draws the toolbar and delegates to the mode-specific side panels.

import (
	"fmt"
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/imguibackend"
	"foundry/internal/levelio"
	"foundry/internal/model"
	"foundry/internal/platform"
	"foundry/internal/video"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// editorTextEdits tracks which fields are in text-input mode and their buffer.
var editorTextEdits = map[string]string{}

// editorActiveToggleButton draws a button highlighted when active, and reports
// whether it was clicked.
//
// The point of the helper is that `active` is a parameter, evaluated once. The
// hazard it removes is real and has bitten: clicking a mode button flips the
// very global the caller is testing, so code shaped like
//
//	if mode { PushStyleColor(...) }
//	if Button(...) { mode = !mode }
//	if mode { PopStyleColor() }   // pops a colour that was never pushed
//
// underflows ImGui's style stack, which asserts rather than degrading — and the
// assert names imgui.h, not the panel that caused it. modelEditorToolbar
// (editor_model_window.go) documents the same rule for its inline copies.
func editorActiveToggleButton(label string, active bool) bool {
	if active {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.85, Y: 0.55, Z: 0.1, W: 1})
	}
	clicked := imgui.Button(label)
	if active {
		imgui.PopStyleColor()
	}
	return clicked
}

// editorActiveToggleButtonColor is editorActiveToggleButton with the highlight
// colour named, for toggles that want to read as their own thing rather than as
// another entry in the orange mode row. Same evaluate-once contract — see the
// hazard documented above.
func editorActiveToggleButtonColor(label string, active bool, col imgui.Vec4) bool {
	if active {
		imgui.PushStyleColor(imgui.StyleColorButton, col)
	}
	clicked := imgui.Button(label)
	if active {
		imgui.PopStyleColor()
	}
	return clicked
}

// editorToolbarGroupSep draws the vertical divider between two groups of
// toolbar buttons: SameLine to stay on the current row, then Separator, which
// Dear ImGui draws as a thin vertical bar rather than its usual horizontal
// rule when it lands mid-row like this. Every toolbar in the editor — the main
// window's, the 2D map's own, the model editor's — calls this between groups
// rather than drawing the divider by hand, so "3D Window | Textures | Model"
// and "Point | Edge | Face" read as the same kind of grouping wherever they
// appear. A caller still needs its own SameLine before the first button of the
// next group; this only draws the line between what came before and what
// comes after.
func editorToolbarGroupSep() {
	imgui.SameLine()
	imgui.Separator()
}

// snapValue rounds v to the nearest multiple of step.
func snapValue(v, step float32) float32 {
	if step <= 0 {
		return v
	}
	return float32(math.Round(float64(v)/float64(step))) * step
}

// editorDragFloat draws a drag field with [-] and [+] increment buttons and
// an inline text-input toggle button.
// The visible label is rendered on a line above the widget so it never clips.
// Layout: label (own line) then [-] [drag field] [=] [+]
func editorDragFloat(label string, value *float32, min, max float32, format string) bool {
	return editorDragFloatStep(label, value, min, max, format, Editor.StepSize)
}

// editorDragFloatStep is editorDragFloat with an explicit drag speed / button
// increment, for fields whose unit isn't world distance (e.g. light angles in
// degrees, where the grid step would make dragging uselessly slow).
func editorDragFloatStep(label string, value *float32, min, max float32, format string, step float32) bool {
	changed := false

	// Extract the visible portion of the label (before ##) for display
	visibleLabel := label
	if idx := strings.Index(label, "##"); idx >= 0 {
		visibleLabel = label[:idx]
	}
	if visibleLabel != "" {
		imgui.Text(visibleLabel)
	}

	// Compute button sizes
	btnW := imgui.CalcTextSize("+", false, 0).X + imgui.CurrentStyle().FramePadding().X*2
	spacing := imgui.CurrentStyle().ItemSpacing().X
	editBtnW := imgui.CalcTextSize("=", false, 0).X + imgui.CurrentStyle().FramePadding().X*2

	// [-] button
	imgui.PushButtonRepeat(true)
	if imgui.ButtonV("-##dec"+label, imgui.Vec2{X: btnW, Y: 0}) {
		*value -= step
		if Editor.SnapToGrid {
			*value = snapValue(*value, step)
		}
		*value = geom.ClampValue(*value, min, max)
		changed = true
	}
	imgui.PopButtonRepeat()
	imgui.SameLine()

	// Field width = remaining space minus edit button and [+] button and their spacing
	fieldWidth := imgui.ContentRegionAvail().X - editBtnW - btnW - spacing*2
	if fieldWidth < 40 {
		fieldWidth = 40
	}

	// Use ##-only ID so no inline label is drawn by ImGui after the widget
	hiddenLabel := "##" + label

	if buf, editing := editorTextEdits[label]; editing {
		// Text-input mode
		imgui.SetNextItemWidth(fieldWidth)
		if imgui.InputTextV("##input"+label, &buf, imgui.InputTextFlagsCharsDecimal|imgui.InputTextFlagsEnterReturnsTrue|imgui.InputTextFlagsAutoSelectAll, nil) {
			if f, err := strconv.ParseFloat(buf, 32); err == nil {
				*value = float32(f)
				if Editor.SnapToGrid {
					*value = snapValue(*value, step)
				}
				*value = geom.ClampValue(*value, min, max)
				changed = true
			}
			delete(editorTextEdits, label)
		} else {
			editorTextEdits[label] = buf
		}
		imgui.SameLine()
		if imgui.ButtonV("x##close"+label, imgui.Vec2{X: editBtnW, Y: 0}) {
			delete(editorTextEdits, label)
		}
	} else {
		// Drag mode
		imgui.SetNextItemWidth(fieldWidth)
		if imgui.DragFloatV(hiddenLabel, value, step, min, max, format, 0) {
			if Editor.SnapToGrid {
				*value = snapValue(*value, step)
				*value = geom.ClampValue(*value, min, max)
			}
			changed = true
		}
		imgui.SameLine()
		if imgui.ButtonV("=##edit"+label, imgui.Vec2{X: editBtnW, Y: 0}) {
			editorTextEdits[label] = fmt.Sprintf("%g", *value)
		}
	}

	// [+] button
	imgui.SameLine()
	imgui.PushButtonRepeat(true)
	if imgui.ButtonV("+##inc"+label, imgui.Vec2{X: btnW, Y: 0}) {
		*value += step
		if Editor.SnapToGrid {
			*value = snapValue(*value, step)
		}
		*value = geom.ClampValue(*value, min, max)
		changed = true
	}
	imgui.PopButtonRepeat()

	return changed
}

// editorDragFloat3 draws three drag fields with inline text-input toggle buttons.
func editorDragFloat3(label string, values *[3]float32, min, max float32, format string) bool {
	changed := false
	labels := [3]string{"X", "Y", "Z"}

	imgui.Text(label)
	imgui.PushID(label)
	for i := 0; i < 3; i++ {
		subLabel := fmt.Sprintf("%s##%d", labels[i], i)
		if editorDragFloat(subLabel, &values[i], min, max, format) {
			changed = true
		}
	}
	imgui.PopID()
	return changed
}

// sortedTextureNames is the scanned texture list in a stable order, shared by
// every picker that offers it — the combo below and the context menus, which
// must not disagree about what exists or in what order.
func sortedTextureNames() []string {
	names := make([]string, 0, len(gfx.TextureNames))
	for n := range gfx.TextureNames {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// textureCombo draws a dropdown of texture names discovered from the
// textures/ directory (see ScanTextures in main.go) rather than a hard-coded
// list. If the current value isn't among the scanned textures (e.g. a name
// from a level authored before the file was added or renamed), it is still
// shown as the selected/preview entry so it isn't silently lost.
// When allowEmpty is true, a "(none)" entry lets the field be cleared.
// Returns true if the selection changed.
func textureCombo(label string, tex *string, allowEmpty bool) bool {
	names := sortedTextureNames()

	preview := *tex
	if preview == "" {
		preview = "(none)"
	}

	changed := false
	if imguibackend.BeginCombo(label, preview) {
		if allowEmpty {
			if imguibackend.SelectableV("(none)", *tex == "", 0, imgui.Vec2{}) && *tex != "" {
				*tex = ""
				changed = true
			}
		}
		known := false
		for _, n := range names {
			if n == *tex {
				known = true
			}
			if imguibackend.SelectableV(n, n == *tex, 0, imgui.Vec2{}) && n != *tex {
				_ = gfx.EnsureTextureLoaded(n)
				*tex = n
				changed = true
			}
		}
		if !known && *tex != "" {
			imguibackend.SelectableV(*tex+" (missing)", true, 0, imgui.Vec2{})
		}
		imguibackend.EndCombo()
	}
	return changed
}

// videoFileCombo lists what is under video/.
//
// The rescan-on-close rule is soundFileCombo's: a clip dropped in while the
// editor is open has to appear without a restart, and rescanning every frame
// would be a directory read per frame of a menu nobody has opened.
func videoFileCombo(label string, clip *string) bool {
	preview := *clip
	if preview == "" {
		preview = "(none)"
	}
	changed := false
	if imguibackend.BeginCombo(label, preview) {
		if Editor.VideoFileNames == nil {
			Editor.VideoFileNames = video.ScanVideoNames()
		}
		if imguibackend.SelectableV("(none)", *clip == "", 0, imgui.Vec2{}) && *clip != "" {
			*clip = ""
			changed = true
		}
		known := false
		for _, n := range Editor.VideoFileNames {
			if n == *clip {
				known = true
			}
			if imguibackend.SelectableV(n, n == *clip, 0, imgui.Vec2{}) && n != *clip {
				*clip = n
				changed = true
			}
		}
		if !known && *clip != "" {
			// A screen authored against a clip since renamed says so, rather
			// than silently reverting to whatever happens to be first.
			imguibackend.SelectableV(*clip+" (missing)", true, 0, imgui.Vec2{})
		}
		imguibackend.EndCombo()
	} else {
		Editor.VideoFileNames = nil
	}
	return changed
}

// modelFileNames lists what a model picker can offer: every .fmdl and .obj
// under models/, written as the install-relative paths an attachment stores.
//
// Both extensions are offered because both load (loadModelDocument picks the
// reader), and an author who has only ever exported OBJs should not have to
// round-trip through the model editor to hang one on an entity.
func modelFileNames() []string {
	files := append(model.ScanModelFiles(), model.ScanOBJFiles()...)
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, model.ModelsDirName+"/"+f)
	}
	sort.Strings(names)
	return names
}

// modelFileCombo draws a picker over those files, writing the chosen path into
// *path and reporting whether it changed.
//
// "(none)" is always offered: an attachment with no mesh is legitimate — it is
// what one on a freshly placed entity starts as — and clearing the path is how
// a model is taken off again. A path that is not among the files is still shown
// as the selection, so a level authored against a mesh since renamed says so
// rather than silently reverting to something that happens to exist.
//
// The directory is scanned only while the dropdown is open. That is a syscall
// per frame for the few frames a menu is down, and it means the list is always
// what is on disk rather than what was on disk when the panel first drew.
func modelFileCombo(label string, path *string) bool {
	preview := modelDisplayName(*path)
	if preview == "" {
		preview = "(none)"
	}
	if !imguibackend.BeginCombo(label, preview) {
		return false
	}
	defer imguibackend.EndCombo()

	changed := false
	if imguibackend.SelectableV("(none)", *path == "", 0, imgui.Vec2{}) && *path != "" {
		*path = ""
		changed = true
	}
	known := false
	for _, n := range modelFileNames() {
		if n == *path {
			known = true
		}
		if imguibackend.SelectableV(modelDisplayName(n), n == *path, 0, imgui.Vec2{}) && n != *path {
			*path = n
			changed = true
		}
	}
	if !known && *path != "" {
		imguibackend.SelectableV(modelDisplayName(*path)+" (missing)", true, 0, imgui.Vec2{})
	}
	return changed
}

// modelDisplayName is how a picker shows a stored model path: every model
// lives under models/, so the prefix is noise to an author and is dropped.
// The stored path keeps it — only the label changes.
func modelDisplayName(path string) string {
	return strings.TrimPrefix(path, model.ModelsDirName+"/")
}

// waterControls draws the wave and reflection settings for a water surface.
//
// Edits are pushed straight to the already-uploaded Geometry rather than going
// through Primitive.Load — the water mesh is a 48×48 grid, and rebuilding and
// re-uploading it on every slider frame would stall the drag. None of these
// values affect vertex positions on the CPU side; they are shader uniforms.
func waterControls(idSuffix string, w *gfx.WaterParams, g *gfx.Geometry) {
	if !imgui.CollapsingHeaderV(fmt.Sprintf("Water##%swaterhdr", idSuffix), imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	editorDragFloat(fmt.Sprintf("Wave Amplitude##%swamp", idSuffix), &w.WaveAmplitude, 0, 10, "%.3f")
	editorDragFloat(fmt.Sprintf("Wave Length##%swlen", idSuffix), &w.WaveLength, 0.05, 500, "%.2f")
	editorDragFloat(fmt.Sprintf("Wave Speed##%swspd", idSuffix), &w.WaveSpeed, 0, 10, "%.2f")
	editorDragFloat(fmt.Sprintf("Reflectivity##%swrefl", idSuffix), &w.Reflectivity, 0, 1, "%.2f")

	col := [3]float32{w.ReflectColor.X(), w.ReflectColor.Y(), w.ReflectColor.Z()}
	if imgui.ColorEdit3(fmt.Sprintf("Reflect Color##%swcol", idSuffix), &col) {
		w.ReflectColor = mgl32.Vec3{col[0], col[1], col[2]}
	}

	w.Enabled = true
	g.Water = *w
}

// editorUVControls draws collapsible UV offset/scale/rotation controls.
// Returns true if any value changed.
func editorUVControls(label string, offset *mgl32.Vec2, scale *mgl32.Vec2, rotation *float32) bool {
	changed := false
	editorPropGroup(fmt.Sprintf("Texture UV##%sUV", label), 0, func() {
		changed = editorUVControlsBody(label, offset, scale, rotation)
	})
	return changed
}

func editorUVControlsBody(label string, offset *mgl32.Vec2, scale *mgl32.Vec2, rotation *float32) bool {
	changed := false
	uvOff := [2]float32{offset.X(), offset.Y()}
	if editorDragFloat(fmt.Sprintf("UV Offset U##%suvou", label), &uvOff[0], 0, 0, "%.3f") ||
		editorDragFloat(fmt.Sprintf("UV Offset V##%suvov", label), &uvOff[1], 0, 0, "%.3f") {
		*offset = mgl32.Vec2{uvOff[0], uvOff[1]}
		changed = true
	}
	uvSc := *scale
	if uvSc == (mgl32.Vec2{}) {
		uvSc = mgl32.Vec2{1, 1}
	}
	uvScArr := [2]float32{uvSc.X(), uvSc.Y()}
	if editorDragFloat(fmt.Sprintf("UV Scale U##%suvsu", label), &uvScArr[0], 0.01, 100, "%.3f") ||
		editorDragFloat(fmt.Sprintf("UV Scale V##%suvsv", label), &uvScArr[1], 0.01, 100, "%.3f") {
		*scale = mgl32.Vec2{uvScArr[0], uvScArr[1]}
		changed = true
	}
	rotDeg := *rotation * 180 / math.Pi
	if editorDragFloat(fmt.Sprintf("UV Rotation (deg)##%suvrot", label), &rotDeg, -360, 360, "%.1f") {
		*rotation = rotDeg * math.Pi / 180
		changed = true
	}
	return changed
}

// editorSmoothPanel draws the per-surface smooth-shading toggle and its crease
// angle. surfaceLabel disambiguates ImGui IDs across the Floor/Ceiling/Wall and
// primitive panels. toggled is the checkbox changing; changed is either
// control, and means the surface must be rebuilt — the normals are generated
// at upload time, so the caller has to reload the geometry.
func editorSmoothPanel(surfaceLabel string, smooth *bool, angle *float32) (toggled, changed bool) {
	toggled = imgui.Checkbox(fmt.Sprintf("Always Smooth##%ssm", surfaceLabel), smooth)
	changed = toggled
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Shade smooth even when the player turns Smooth Lighting off.\n" +
			"With it on (the default) every surface already shades smooth\n" +
			"up to the crease angle.")
	}
	// The angle is shown either way: the player's Smooth Lighting setting
	// smooths by it whether or not the box is ticked.
	//
	// Materialize the default so the control shows the angle actually in use;
	// 0 already means "default", so this changes nothing about the shading.
	if *angle == 0 {
		*angle = geom.DefaultSmoothAngle
	}
	imgui.Indent()
	if editorDragFloat(fmt.Sprintf("Crease Angle (deg)##%ssma", surfaceLabel), angle, 1, 179, "%.0f") {
		changed = true
	}
	imgui.Unindent()
	return toggled, changed
}

// editorGridSettings draws the step-size and snap-to-grid controls.
func editorGridSettings() {
	imgui.Checkbox("Snap to Grid", &Editor.SnapToGrid)
	imgui.SameLine()

	label := "Step##gridStep"
	stepDelta := float32(0.05)
	btnW := imgui.CalcTextSize("+", false, 0).X + imgui.CurrentStyle().FramePadding().X*2
	editBtnW := imgui.CalcTextSize("=", false, 0).X + imgui.CurrentStyle().FramePadding().X*2

	// [-] button
	imgui.PushButtonRepeat(true)
	if imgui.ButtonV("-##decStep", imgui.Vec2{X: btnW, Y: 0}) {
		Editor.StepSize -= stepDelta
		if Editor.StepSize < 0.01 {
			Editor.StepSize = 0.01
		}
	}
	imgui.PopButtonRepeat()
	imgui.SameLine()

	if buf, editing := editorTextEdits[label]; editing {
		imgui.SetNextItemWidth(60)
		if imgui.InputTextV("##inputStep", &buf, imgui.InputTextFlagsCharsDecimal|imgui.InputTextFlagsEnterReturnsTrue|imgui.InputTextFlagsAutoSelectAll, nil) {
			if f, err := strconv.ParseFloat(buf, 32); err == nil {
				v := float32(f)
				if v < 0.01 {
					v = 0.01
				}
				if v > 10.0 {
					v = 10.0
				}
				Editor.StepSize = v
			}
			delete(editorTextEdits, label)
		} else {
			editorTextEdits[label] = buf
		}
		imgui.SameLine()
		if imgui.ButtonV("x##closeStep", imgui.Vec2{X: editBtnW, Y: 0}) {
			delete(editorTextEdits, label)
		}
	} else {
		imgui.SetNextItemWidth(60)
		imgui.DragFloatV(label, &Editor.StepSize, 0.01, 0.01, 10.0, "%.2f", 0)
		imgui.SameLine()
		if imgui.ButtonV("=##editStep", imgui.Vec2{X: editBtnW, Y: 0}) {
			editorTextEdits[label] = fmt.Sprintf("%g", Editor.StepSize)
		}
	}

	// [+] button
	imgui.SameLine()
	imgui.PushButtonRepeat(true)
	if imgui.ButtonV("+##incStep", imgui.Vec2{X: btnW, Y: 0}) {
		Editor.StepSize += stepDelta
		if Editor.StepSize > 10.0 {
			Editor.StepSize = 10.0
		}
	}
	imgui.PopButtonRepeat()

	imgui.Separator()
}

// EditorDrawUI draws all ImGui panels for the current editor mode.
func EditorDrawUI() {
	lvl := Editor.level
	if lvl == nil {
		return
	}

	// ---- Top bar ----
	//
	// Menus above (editor_menubar.go lays out the part every editor shares),
	// the 3D view's tools below.
	editorTopBarBegin("##toolbar", float32(platform.WinWidth))

	// Snapshot mode before rendering so Push/Pop stay matched.
	currentMode := Editor.Mode

	if imgui.BeginMenuBar() {
		editorLevelFileMenu(lvl)
		editorLevelEditMenu()
		if currentMode == Mode3D {
			editorLevelViewMenu()
		}
		editorLevelBuildMenu()
		editorMenuBarShared(editorKindLevel)
		editorMenuBarDoc(filepath.Base(Editor.SavePath), editorLevelDirty())
		imgui.EndMenuBar()
	}

	// ---- Row two: the 3D view's tools ----
	//
	// What a click selects, which gizmo acts on it, and how the viewport itself
	// looks — all 3D-only. The display toggles are in the View menu as well;
	// they stay here too because they are flipped mid-task, and a menu is two
	// clicks where a button is one.
	if currentMode == Mode3D {
		// ---- Group: edit context ----
		//
		// Ahead of the selection modes because it decides whether those mean
		// anything at all: in object mode a click picks up a whole brush and
		// the three unit buttons below are not consulted.
		if editorActiveToggleButton(
			fmt.Sprintf("%s [Tab]##editctx", Editor.EditContext),
			editorInEditMode() || editorInDetailMode()) {
			editorToggleEditContext()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Tab — Object mode moves a whole brush; Edit mode reshapes one.\n" +
				"Pressing 1, 2 or 3 goes straight into Edit mode.\n\n" +
				"Detail mode shapes the surface drawn over one brush without\n" +
				"moving the solid: Ctrl+Shift+click a brush to go in, Tab or\n" +
				"Escape to come back.")
		}

		// ---- Group: select mode ----
		//
		// In edit mode and in detail mode: both are made of points, edges and
		// faces, of the hull and of the detail mesh respectively. Not in object
		// mode, where a row of buttons that select nothing is worse than no row
		// at all.
		if editorInEditMode() || editorInDetailMode() {
			editorToolbarGroupSep()
			for _, mode := range editorSelModes {
				m := mode
				imgui.SameLine()
				if editorActiveToggleButton(
					fmt.Sprintf("%s [%s]##selmode", m, editorSelModeKey(m)), Editor.SelMode == m) {
					editorSetSelMode(m)
				}
				if imgui.IsItemHovered() {
					imgui.SetTooltip(editorSelModeTooltip(m))
				}
			}
		}

		// ---- Group: gizmo tool ----
		editorToolbarGroupSep()
		editorGizmoToolButtons()

		// ---- Group: viewport display ----
		editorToolbarGroupSep()
		imgui.SameLine()
		wire := Editor.Wireframe
		if editorActiveToggleButtonColor(editorShadingLabel(), wire,
			imgui.Vec4{X: 0.2, Y: 0.45, Z: 0.7, W: 1}) {
			editorSetWireframe(!wire)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(editorShadingTooltip)
		}

		imgui.SameLine()
		editorBoundsToggleButton()

		imgui.SameLine()
		flying := editorCam3D.Fly
		if editorActiveToggleButtonColor(editorCameraLabel(), flying,
			imgui.Vec4{X: 0.2, Y: 0.55, Z: 0.35, W: 1}) {
			editorSetFlyCam(!flying)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(editorCameraTooltip(flying))
		}
	}

	// While the compiled preview is up, say so where the eye already is and
	// offer the way out: the Build menu's checkmark is not somewhere anyone
	// looks while the view seems to have stopped taking edits.
	if EditorPreviewingCompiled() {
		if currentMode == Mode3D {
			editorToolbarGroupSep()
			imgui.SameLine()
		}
		if editorActiveToggleButtonColor("Previewing Compiled - Exit", true,
			imgui.Vec4{X: 0.2, Y: 0.45, Z: 0.6, W: 1}) {
			editorTogglePreviewCompiled()
		}
	}

	// No chunk picker here — which chunk is active is a selection like any
	// other, made in the Chunks list under Objects and reported on the panel's
	// Context line, rather than in a second control out of reach of the list
	// that owns it. This just keeps the index in bounds: RebuildIndexes and
	// undo both replace lvl.Chunks wholesale, and every use of ActiveChunkIdx
	// below assumes it still names something.
	if Editor.ActiveChunkIdx < 0 && len(lvl.Chunks) > 0 {
		Editor.ActiveChunkIdx = 0
	}
	if Editor.ActiveChunkIdx >= len(lvl.Chunks) {
		Editor.ActiveChunkIdx = len(lvl.Chunks) - 1
	}

	// Status message (shown for 3 seconds after save/load)
	if Editor.StatusMsg != "" {
		if sdl.GetTicks()-Editor.StatusTime < 3000 {
			if currentMode == Mode3D || EditorPreviewingCompiled() {
				imgui.SameLine()
				editorToolbarGroupSep()
				imgui.SameLine()
			}
			imgui.Text(Editor.StatusMsg)
		} else {
			Editor.StatusMsg = ""
		}
	}
	// --- Level Picker popup ---
	imguibackend.WithModalPopup("Level Picker", &Editor.ShowLevelPicker, func() {
		imgui.Text("Select a level to open:")
		imgui.Separator()
		for _, f := range Editor.LevelFiles {
			if imguibackend.SelectableV(f, false, 0, imgui.Vec2{}) {
				// Behind a progress bar; the status line reports the result.
				EditorBeginLoadLevel(filepath.Join(Editor.LevelsDir, f))
				imgui.CloseCurrentPopup()
			}
		}
		imgui.Separator()
		imgui.Text("New Level:")
		imgui.SetNextItemWidth(200)
		imgui.InputText("##newlevel", &Editor.NewLevelName)
		imgui.SameLine()
		if imgui.Button("Create") && Editor.NewLevelName != "" {
			name := Editor.NewLevelName
			if !strings.HasSuffix(name, ".yaml") {
				name += ".yaml"
			}
			path := filepath.Join(Editor.LevelsDir, name)
			if err := levelio.WriteBlankLevel(path); err != nil {
				Editor.StatusMsg = fmt.Sprintf("Create failed: %v", err)
			} else {
				EditorBeginLoadLevel(path)
			}
			Editor.StatusTime = sdl.GetTicks()
			imgui.CloseCurrentPopup()
		}
		imgui.Separator()
		if imgui.Button("Cancel") {
			imgui.CloseCurrentPopup()
		}
	})

	// --- Save As popup ---
	imguibackend.WithModalPopup("Save As", &Editor.ShowSaveAs, func() {
		imgui.Text("Save level as:")
		imgui.SetNextItemWidth(200)
		imgui.InputText("##saveas", &Editor.SaveAsName)
		if imgui.Button("Save") && Editor.SaveAsName != "" {
			name := Editor.SaveAsName
			if !strings.HasSuffix(name, ".yaml") {
				name += ".yaml"
			}
			path := filepath.Join(Editor.LevelsDir, name)
			if err := editorSaveLevel(lvl, path); err != nil {
				Editor.StatusMsg = fmt.Sprintf("Save failed: %v", err)
			} else {
				Editor.SavePath = path
				Editor.StatusMsg = fmt.Sprintf("Saved to %s", name)
			}
			Editor.StatusTime = sdl.GetTicks()
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##saveas") {
			imgui.CloseCurrentPopup()
		}
	})

	editorProjectDialog()

	// The toolbar has no Exit button. Quitting is the window's own close
	// button, the same as every other window the editor opens — a red button
	// sitting a few pixels from the mode toggles is a level lost to a misclick,
	// and the title bar is where anyone looks for it anyway.

	imgui.End()

	// ---- Mode-specific side panel ----
	if Editor.Mode == Mode3D {
		Editor3DPanel()
		// Along the bottom of the viewport, beside the panel, while a waypoint
		// animation is selected (editor_waypoint_timeline.go).
		editorWaypointTimelineWindow()
		// Behind every window, so the panel and the timeline cover a label.
		editorWaypointDistanceLabels()
		// After the panel — it sets the selection the indicator reads.
		EditorDirLightIndicator()
		// Last, so the popup opens over the panel as well as the viewport.
		editor3DContextMenu()
	}
}

// editorGizmoToolButtons draws the move/rotate/scale toggles and UV mode.
func editorGizmoToolButtons() {
	for _, tool := range editorGizmoTools {
		tl := tool
		imgui.SameLine()
		if editorActiveToggleButtonColor(
			fmt.Sprintf("%s [%s]##gizmotool", tl, editorGizmoToolKey(tl)),
			Editor.GizmoTool == tl, imgui.Vec4{X: 0.55, Y: 0.35, Z: 0.15, W: 1}) {
			editorSetGizmoTool(tl)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(editorGizmoToolTooltip(tl))
		}
	}
	imgui.SameLine()
	if editorActiveToggleButtonColor("UV [U]##gizmouvmode", Editor.UVMode,
		imgui.Vec4{X: 0.45, Y: 0.25, Z: 0.55, W: 1}) {
		editorToggleUVMode()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip(uvModeTooltip)
	}
}
