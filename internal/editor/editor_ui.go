package editor

// editor_ui.go — top-level ImGui panel orchestration for the level editor.
//
// EditorDrawUI() is called each frame between ImGuiNewFrame and ImGuiRender.
// It draws the toolbar and delegates to the mode-specific side panels.

import (
	"fmt"
	"foundry/internal/biome"
	"foundry/internal/detail"
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/imguibackend"
	"foundry/internal/levelio"
	"foundry/internal/model"
	"foundry/internal/platform"
	"foundry/internal/textureui"
	"foundry/internal/world"
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
	if imgui.BeginCombo(label, preview) {
		if allowEmpty {
			if imgui.SelectableV("(none)", *tex == "", 0, imgui.Vec2{}) && *tex != "" {
				*tex = ""
				changed = true
			}
		}
		known := false
		for _, n := range names {
			if n == *tex {
				known = true
			}
			if imgui.SelectableV(n, n == *tex, 0, imgui.Vec2{}) && n != *tex {
				_ = gfx.EnsureTextureLoaded(n)
				*tex = n
				changed = true
			}
		}
		if !known && *tex != "" {
			imgui.SelectableV(*tex+" (missing)", true, 0, imgui.Vec2{})
		}
		imgui.EndCombo()
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
	preview := *path
	if preview == "" {
		preview = "(none)"
	}
	if !imgui.BeginCombo(label, preview) {
		return false
	}
	defer imgui.EndCombo()

	changed := false
	if imgui.SelectableV("(none)", *path == "", 0, imgui.Vec2{}) && *path != "" {
		*path = ""
		changed = true
	}
	known := false
	for _, n := range modelFileNames() {
		if n == *path {
			known = true
		}
		if imgui.SelectableV(n, n == *path, 0, imgui.Vec2{}) && n != *path {
			*path = n
			changed = true
		}
	}
	if !known && *path != "" {
		imgui.SelectableV(*path+" (missing)", true, 0, imgui.Vec2{})
	}
	return changed
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
// primitive panels. Returns true when the surface must be rebuilt — the normals
// are generated at upload time, so the caller has to reload the geometry.
func editorSmoothPanel(surfaceLabel string, smooth *bool, angle *float32) bool {
	changed := imgui.Checkbox(fmt.Sprintf("Smooth Shading##%ssm", surfaceLabel), smooth)
	if !*smooth {
		return changed
	}
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
	return changed
}

// detailPresetNameEdits holds the preset-name text buffer per surface panel, so
// a half-typed name survives across frames. Keyed by surfaceLabel.
var detailPresetNameEdits = map[string]string{}

// detailPresetNameSeen is the preset tag each surface panel showed last frame.
// When it changes the panel is looking at a different face (or a preset was
// applied), so the typed buffer is dropped rather than following the user onto
// unrelated geometry.
var detailPresetNameSeen = map[string]string{}

// detailPresetStatus is the last preset save/delete result, shown inline under
// the row for a few seconds.
var (
	detailPresetStatus     string
	detailPresetStatusTime uint32
)

func detailPresetSetStatus(format string, args ...interface{}) {
	detailPresetStatus = fmt.Sprintf(format, args...)
	detailPresetStatusTime = sdl.GetTicks()
}

// editorDetailPresetSaveRow draws the name field and Save/Update/Delete buttons
// that persist the face's current wave, noise, taper and scatter settings as a
// named preset under <exe>/detail_presets. Returns true if the face changed
// (only its Preset label ever does here — the parameters are read, not written).
func editorDetailPresetSaveRow(surfaceLabel string, d *world.DetailSpec) bool {
	changed := false

	if seen, ok := detailPresetNameSeen[surfaceLabel]; !ok || seen != d.Preset {
		detailPresetNameSeen[surfaceLabel] = d.Preset
		delete(detailPresetNameEdits, surfaceLabel)
	}

	// The buffer starts from whatever preset the face is tagged with, so the
	// common case — tweak a preset, save it back — is one click.
	name, editing := detailPresetNameEdits[surfaceLabel]
	if !editing {
		name = d.Preset
	}
	imgui.SetNextItemWidth(140)
	if imgui.InputText(fmt.Sprintf("##%sdetPreName", surfaceLabel), &name) {
		detailPresetNameEdits[surfaceLabel] = name
	}
	imgui.SameLine()

	clean := detail.DetailPresetName(name)
	label := "Save Preset"
	if _, exists := world.DetailPresets[clean]; exists && clean != "" {
		label = "Update Preset"
	}
	imguibackend.EditorPushDisabled(clean == "")
	if imgui.Button(fmt.Sprintf("%s##%sdetPreSave", label, surfaceLabel)) && clean != "" {
		if err := detail.SaveDetailPreset(clean, *d); err != nil {
			detailPresetSetStatus("Preset save failed: %v", err)
		} else {
			d.Preset = clean
			detailPresetSetStatus("Saved preset %q", clean)
			changed = true
		}
	}
	imguibackend.EditorPopDisabled()

	// Only user presets are deletable; the built-ins are compiled in.
	if detail.DetailUserPresets[clean] {
		imgui.SameLine()
		if imgui.Button(fmt.Sprintf("Delete##%sdetPreDel", surfaceLabel)) {
			if err := detail.DeleteDetailPreset(clean); err != nil {
				detailPresetSetStatus("Preset delete failed: %v", err)
			} else {
				detailPresetSetStatus("Deleted preset %q", clean)
			}
		}
	}

	if detailPresetStatus != "" {
		if sdl.GetTicks()-detailPresetStatusTime < 4000 {
			imgui.Text(detailPresetStatus)
		} else {
			detailPresetStatus = ""
		}
	}
	return changed
}

// editorDetailPanel draws the per-face procedural-detail controls (wave/noise
// displacement + scatter). surfaceLabel disambiguates ImGui IDs across the
// Floor/Ceiling/Wall panels. Returns true if any value changed.
func editorDetailPanel(surfaceLabel string, d *world.DetailSpec) bool {
	changed := false
	editorPropGroup(fmt.Sprintf("Detail##%sdet", surfaceLabel), 0, func() {
		changed = editorDetailPanelBody(surfaceLabel, d)
	})
	return changed
}

func editorDetailPanelBody(surfaceLabel string, d *world.DetailSpec) bool {
	changed := false
	changed = imgui.Checkbox(fmt.Sprintf("Enabled##%sdetEn", surfaceLabel), &d.Enabled) || changed
	if !d.Enabled {
		return changed
	}

	// Preset picker — copies a ready-made parameter set in.
	preset := d.Preset
	if preset == "" {
		preset = "(custom)"
	}
	if imgui.BeginCombo(fmt.Sprintf("Preset##%sdetPre", surfaceLabel), preset) {
		for _, name := range world.DetailPresetNames {
			if imgui.SelectableV(name, name == d.Preset, 0, imgui.Vec2{}) {
				world.ApplyDetailPreset(d, name)
				changed = true
			}
		}
		// User presets from <exe>/detail_presets, below the built-ins. Names that
		// shadow a built-in already appeared above.
		first := true
		for _, name := range detail.DetailUserPresetNames {
			if _, builtin := detail.DetailBuiltinPresets[name]; builtin {
				continue
			}
			if first {
				imgui.Separator()
				first = false
			}
			if imgui.SelectableV(name, name == d.Preset, 0, imgui.Vec2{}) {
				world.ApplyDetailPreset(d, name)
				changed = true
			}
		}
		imgui.EndCombo()
	}

	changed = editorDetailPresetSaveRow(surfaceLabel, d) || changed

	// Deterministic seed.
	seed := int32(d.Seed)
	if imgui.InputInt(fmt.Sprintf("Seed##%sdetSeed", surfaceLabel), &seed) {
		d.Seed = int64(seed)
		changed = true
	}
	imgui.SameLine()
	if imgui.Button(fmt.Sprintf("Randomize##%sdetRnd", surfaceLabel)) {
		d.Seed = int64(sdl.GetTicks())&0x7fffffff | 1
		changed = true
	}

	// --- Displacement: up to DetailMaxWaves sine layers + optional noise ---
	if imgui.Checkbox(fmt.Sprintf("Displace surface##%sdetDisp", surfaceLabel), &d.Displace) {
		if d.Displace {
			d.EnsureDisplaceDefaults()
		}
		changed = true
	}
	if d.Displace {
		imgui.Indent()
		for i := range d.Waves {
			w := &d.Waves[i]
			id := fmt.Sprintf("%sdetW%d", surfaceLabel, i)
			if imgui.Checkbox(fmt.Sprintf("Wave %d##%sEn", i+1, id), &w.Enabled) {
				if w.Enabled && w.Frequency == 0 {
					*w = world.DetailDefaultWave(i)
				}
				changed = true
			}
			if w.Enabled {
				imgui.Indent()
				changed = editorDragFloat(fmt.Sprintf("Amplitude##%sAmp", id), &w.Amplitude, 0, 20, "%.3f") || changed
				changed = editorDragFloat(fmt.Sprintf("Frequency##%sFreq", id), &w.Frequency, 0, 20, "%.3f") || changed
				changed = editorDragFloat(fmt.Sprintf("Rotation (deg)##%sRot", id), &w.Rotation, -360, 360, "%.1f") || changed
				changed = editorDragFloat(fmt.Sprintf("Tessellation##%sTess", id), &w.TessLevel, 1, 64, "%.0f") || changed
				imgui.Unindent()
			}
		}

		// Noise layer — added on top of the summed waves.
		if imgui.Checkbox(fmt.Sprintf("Noise##%sdetNoise", surfaceLabel), &d.Noise) {
			if d.Noise {
				d.EnsureNoiseDefaults()
			}
			changed = true
		}
		if d.Noise {
			imgui.Indent()
			changed = editorDragFloat(fmt.Sprintf("Amplitude##%sdetNAmp", surfaceLabel), &d.NoiseAmplitude, 0, 20, "%.3f") || changed
			changed = editorDragFloat(fmt.Sprintf("Frequency##%sdetNFreq", surfaceLabel), &d.NoiseFrequency, 0.01, 20, "%.3f") || changed
			oct := int32(d.NoiseOctaves)
			if imgui.SliderInt(fmt.Sprintf("Octaves##%sdetNOct", surfaceLabel), &oct, 1, 6) {
				d.NoiseOctaves = int(oct)
				changed = true
			}
			changed = editorDragFloat(fmt.Sprintf("Tessellation##%sdetNTess", surfaceLabel), &d.NoiseTessLevel, 1, 64, "%.0f") || changed
			imgui.Unindent()
		}

		// Tessellation is per-patch, so the active layers share the highest level.
		imgui.Text(fmt.Sprintf("Effective tessellation: %.0f", d.TessLevelFor()))
		changed = editorDragFloat(fmt.Sprintf("Edge Taper##%sdetTaper", surfaceLabel), &d.EdgeTaper, 0, 10, "%.2f") || changed
		imgui.Unindent()
	}

	// --- Scatter ---
	changed = imgui.Checkbox(fmt.Sprintf("Scatter meshes##%sdetScat", surfaceLabel), &d.Scatter) || changed
	if d.Scatter {
		imgui.Indent()
		if textureCombo(fmt.Sprintf("Rock Texture##%sdetScTex", surfaceLabel), &d.ScatterTex, false) {
			changed = true
		}
		changed = editorDragFloat(fmt.Sprintf("Density##%sdetScDen", surfaceLabel), &d.ScatterDensity, 0, 1, "%.2f") || changed
		changed = editorDragFloat(fmt.Sprintf("Spacing##%sdetScSp", surfaceLabel), &d.ScatterSpacing, 0.05, 10, "%.2f") || changed
		changed = editorDragFloat(fmt.Sprintf("Scale##%sdetScScl", surfaceLabel), &d.ScatterScale, 0.02, 5, "%.2f") || changed
		changed = editorDragFloat(fmt.Sprintf("Jitter##%sdetScJit", surfaceLabel), &d.ScatterJitter, 0, 1, "%.2f") || changed
		changed = editorDragFloat(fmt.Sprintf("Draw Range##%sdetScRng", surfaceLabel), &d.ScatterRange, 4, 400, "%.0f") || changed
		if d.ScatterMesh == "" {
			d.ScatterMesh = "rock"
		}
		imgui.Unindent()
	}

	if changed {
		d.Clamp()
	}
	return changed
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

	// ---- Top toolbar ----
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: 0})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(platform.WinWidth), Y: 28})
	imgui.BeginV("##toolbar", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|
			imgui.WindowFlagsNoMove|imgui.WindowFlagsNoScrollbar)

	// Snapshot mode before rendering so Push/Pop stay matched. The main window
	// is always the 3D view now — the sector map has a window of its own — so
	// there is no 2D/3D toggle here, and this button is the only way into it.
	currentMode := Editor.Mode

	// ---- Group: sub-editor windows ----
	// Snapshot before the button: clicking it opens the window, so re-testing
	// Editor2DPoppedOut() afterwards would pop a color that was never pushed
	// and unbalance imgui's global style stack (it asserts on the next
	// push/pop anywhere). Same reason drawToolsPanel latches its selection.
	editor2DOpen := Editor2DPoppedOut()
	if editor2DOpen {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.6, Z: 0.2, W: 1})
	}
	if imgui.Button("2D Window") {
		OpenEditor2DWindow()
	}
	if editor2DOpen {
		imgui.PopStyleColor()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Open the top-down sector map in its own window.")
	}

	imgui.SameLine()
	if imgui.Button("Textures") {
		textureui.OpenTextureEditor()
	}

	imgui.SameLine()
	// Snapshot before the button, for the same reason "2D Window" above does:
	// clicking it opens the window, so re-testing ModelEditorOpen() afterwards
	// would pop a color that was never pushed.
	modelOpen := ModelEditorOpen()
	if modelOpen {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.5, Z: 0.7, W: 1})
	}
	if imgui.Button("Model") {
		OpenModelEditorWindow()
	}
	if modelOpen {
		imgui.PopStyleColor()
	}

	imgui.SameLine()
	// Latched before the button, for the same reason "Model" above is.
	scriptOpen := ScriptEditorOpen()
	if scriptOpen {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.5, Z: 0.7, W: 1})
	}
	if imgui.Button("Script") {
		OpenScriptEditorWindow()
	}
	if scriptOpen {
		imgui.PopStyleColor()
	}

	imgui.SameLine()
	logOpen := LogWindowOpen()
	if logOpen {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.5, Z: 0.7, W: 1})
	}
	if imgui.Button("Log") {
		OpenLogWindow()
	}
	if logOpen {
		imgui.PopStyleColor()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Live script output, errors and engine messages.")
	}

	// The three groups below are what a click selects, which gizmo acts on it,
	// and how the viewport itself looks — all 3D-only, since the 2D map has no
	// shading or camera mode of its own and carries its own copy of the first
	// two on its own toolbar (editor2DWindowToolbar), for the same reason: what
	// a click selects and which gizmo acts on it are shared editor-wide state,
	// so both toolbars offer them wherever the mouse happens to be.
	if currentMode == Mode3D {
		// ---- Group: select mode ----
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
			imgui.SetTooltip("Z — Solid hides the far side of the world, and only the\n" +
				"points and edges you can see can be clicked.\n" +
				"Wireframe draws it as lines and lets you click any of them.")
		}

		imgui.SameLine()
		flying := editorCam3D.Fly
		if editorActiveToggleButtonColor(editorCameraLabel(), flying,
			imgui.Vec4{X: 0.2, Y: 0.55, Z: 0.35, W: 1}) {
			editorSetFlyCam(!flying)
		}
		if imgui.IsItemHovered() {
			if flying {
				imgui.SetTooltip("RMB/MMB drag to look, WASD fly, Space/Shift up and down,\n" +
					"wheel dollies, F frames the selection.")
			} else {
				imgui.SetTooltip("RMB/MMB drag to orbit, Shift+MMB pan, wheel zoom,\n" +
					"WASD slides the pivot, F frames the selection.")
			}
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

	// ---- Group: level ----
	editorToolbarGroupSep()
	imgui.SameLine()
	imgui.Text(fmt.Sprintf("Level: %s", filepath.Base(Editor.SavePath)))
	imgui.SameLine()

	if imgui.Button("Open") {
		Editor.LevelFiles = levelio.ScanLevelFiles(Editor.LevelsDir)
		Editor.NewLevelName = ""
		Editor.ShowLevelPicker = true
	}
	imgui.SameLine()

	if imgui.Button("Save") {
		if err := levelio.SaveLevel(lvl, Editor.SavePath); err != nil {
			Editor.StatusMsg = fmt.Sprintf("Save failed: %v", err)
		} else {
			Editor.StatusMsg = fmt.Sprintf("Saved to %s", Editor.SavePath)
		}
		Editor.StatusTime = sdl.GetTicks()
	}
	imgui.SameLine()

	imguibackend.EditorPushDisabled(!EditorCanUndo())
	if imgui.Button("Undo##lvl") {
		EditorUndo()
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()

	imguibackend.EditorPushDisabled(!EditorCanRedo())
	if imgui.Button("Redo##lvl") {
		EditorRedo()
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()

	if imgui.Button("Save As...") {
		Editor.SaveAsName = filepath.Base(Editor.SavePath)
		Editor.ShowSaveAs = true
	}
	imgui.SameLine()

	// Status message (shown for 3 seconds after save/load)
	if Editor.StatusMsg != "" {
		if sdl.GetTicks()-Editor.StatusTime < 3000 {
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
			if imgui.SelectableV(f, false, 0, imgui.Vec2{}) {
				path := filepath.Join(Editor.LevelsDir, f)
				if err := EditorLoadLevel(path); err != nil {
					Editor.StatusMsg = fmt.Sprintf("Load failed: %v", err)
				} else {
					Editor.StatusMsg = fmt.Sprintf("Loaded %s", f)
				}
				Editor.StatusTime = sdl.GetTicks()
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
			} else if err := EditorLoadLevel(path); err != nil {
				Editor.StatusMsg = fmt.Sprintf("Load failed: %v", err)
			} else {
				Editor.StatusMsg = fmt.Sprintf("Created %s", name)
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
			if err := levelio.SaveLevel(lvl, path); err != nil {
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

	// The toolbar has no Exit button. Quitting is the window's own close
	// button, the same as every other window the editor opens — a red button
	// sitting a few pixels from the mode toggles is a level lost to a misclick,
	// and the title bar is where anyone looks for it anyway.

	imgui.End()

	// ---- Mode-specific side panel ----
	if Editor.Mode == Mode3D {
		Editor3DPanel()
		// After the panel — it sets the selection the indicator reads.
		EditorDirLightIndicator()
		// Last, so the popup opens over the panel as well as the viewport.
		editor3DContextMenu()
	}
}

// Editor2DPanel draws the 2D mode side panel: Context, the map's own actions,
// Objects, Properties, View, Editor — see editor_panel.go for the shared frame
// this and Editor3DPanel both draw through.
func Editor2DPanel() {
	lvl := Editor.level
	if lvl == nil {
		return
	}

	// Sized to whichever window is hosting the 2D view — the main one, or the
	// pop-out (editor_2d_window.go).
	viewW, viewH := editor2DViewportSize()
	imgui.SetNextWindowPos(imgui.Vec2{X: viewW - Editor.PanelWidth, Y: 28})
	imgui.SetNextWindowSize(imgui.Vec2{X: Editor.PanelWidth, Y: viewH - 28})
	imgui.BeginV("2D Sector Editor", nil, imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize)

	editorPanelBody(lvl, panelKind2D)

	imgui.End()
	editorChunkPopups(lvl)
}

// editor2DSectorSections draws the selected sector's properties and, under
// them, the selected corner.
//
// The sector block itself is the one the 3D panel draws — see
// editor_sector_panel.go — so the two editors cannot disagree about what a
// wall or a floor carries. What is left here is the corner, which only the map
// can pick: a position in the plan, and the two per-corner height offsets that
// tilt the floor and the ceiling above it.
func editor2DSectorSections(lvl *world.Level, ch *world.Chunk) {
	if Editor.ActiveSectorIdx < 0 || Editor.ActiveSectorIdx >= len(ch.Sectors) {
		return
	}
	s := &ch.Sectors[Editor.ActiveSectorIdx]

	imgui.Separator()
	editorFocusOpen(panelFocusSector, panelFocusFloor, panelFocusCeiling, panelFocusWall)
	if !imgui.CollapsingHeaderV("Sector: "+s.ID+"##sechdr2d", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusSector)
	editorSectorPropertiesBody(lvl, ch, s, "2d")
	editor2DVertexGroup(ch, s)
}

// editor2DVertexGroup draws the selected corner of the selected sector.
func editor2DVertexGroup(ch *world.Chunk, s *world.Sector) {
	si, vi := Editor.ActiveVertexSI, Editor.ActiveVertexVI
	sectorPts := world.SectorPoints(ch, s)
	if si != Editor.ActiveSectorIdx || si < 0 || vi < 0 || vi >= len(sectorPts) {
		return
	}

	editorFocusOpen(panelFocusVertex)
	editorPropGroup(fmt.Sprintf("Corner %d##grpvert2d", vi), imgui.TreeNodeFlagsDefaultOpen, func() {
		editorFocusScroll(panelFocusVertex)
		x := sectorPts[vi].X()
		z := sectorPts[vi].Y()
		xChanged := editorDragFloat(fmt.Sprintf("X##vx%d", vi), &x, -1e6, 1e6, "%.3f")
		zChanged := editorDragFloat(fmt.Sprintf("Z##vz%d", vi), &z, -1e6, 1e6, "%.3f")
		if xChanged || zChanged {
			if vi < len(s.PointIndices) {
				pIdx := s.PointIndices[vi]
				was := ch.Points[pIdx]
				ch.Points[pIdx] = mgl32.Vec2{x, z}
				biome.CarryBiomesWithPoints(ch, []biome.PointMove{{PointIdx: pIdx, From: was}})
			}
			rebuildSectorEdgePoints(ch, s)
			reloadSectorsUsingPoint(ch, s.PointIndices[vi])
		}

		// The two per-corner height deltas, which is what makes a ramp.
		if vi < len(s.Floor.VertexOffsets) {
			fOff := s.Floor.VertexOffsets[vi]
			if editorDragFloat(fmt.Sprintf("Floor Offset##fo%d", vi), &fOff, -100, 100, "%.3f") {
				s.Floor.VertexOffsets[vi] = fOff
				_ = world.ReloadSector(ch, s)
			}
		}
		if vi < len(s.Ceiling.VertexOffsets) {
			cOff := s.Ceiling.VertexOffsets[vi]
			if editorDragFloat(fmt.Sprintf("Ceil Offset##co%d", vi), &cOff, -100, 100, "%.3f") {
				s.Ceiling.VertexOffsets[vi] = cOff
				_ = world.ReloadSector(ch, s)
			}
		}
	})
}

// convertEdge converts an edge between wall and portal types.
func convertEdge(ch *world.Chunk, s *world.Sector, fromKind string, idx int, toKind string) {
	if fromKind == toKind {
		return
	}

	// Find the polygon edge index BEFORE modifying any arrays, so the
	// wall/portal sub-array index still maps correctly to EdgeKinds.
	polyIdx := polyEdgeIdx(s, fromKind, idx)
	if polyIdx < 0 || polyIdx >= len(s.EdgeKinds) {
		return
	}

	// Compute the correct insertion position in the target sub-array so
	// that the ordering stays consistent with EdgeKinds.
	insertIdx := 0
	for ei := 0; ei < polyIdx; ei++ {
		if s.EdgeKinds[ei] == toKind {
			insertIdx++
		}
	}

	// The edge keeps its ID across the conversion. This is the whole reason
	// identity lives on the edge rather than on the wall or portal record: a
	// script addressing @sector.edge.<id> must not lose its target because the
	// author turned that edge into a doorway.
	world.EnsureEdgeIDs(s)
	edgeID := s.EdgeIDs[polyIdx]

	if fromKind == "wall" && idx < len(s.Walls) {
		w := s.Walls[idx]
		p := world.Portal{ID: edgeID, StartPoint: w.StartPoint, EndPoint: w.EndPoint, Visible: true, DoubleSided: w.DoubleSided}
		s.Walls = append(s.Walls[:idx], s.Walls[idx+1:]...)
		// Insert portal at the correct position
		s.Portals = append(s.Portals, world.Portal{})
		copy(s.Portals[insertIdx+1:], s.Portals[insertIdx:])
		s.Portals[insertIdx] = p
		Editor.ActiveEdgeKind = "portal"
		Editor.ActiveEdgeIdx = insertIdx
	} else if fromKind == "portal" && idx < len(s.Portals) {
		p := s.Portals[idx]
		_ = gfx.EnsureTextureLoaded("concrete")
		w := world.Wall{ID: edgeID, StartPoint: p.StartPoint, EndPoint: p.EndPoint, Texture: "concrete", Collision: true, Visible: true, Pinned: true, DoubleSided: p.DoubleSided, Alpha: 1.0}
		s.Portals = append(s.Portals[:idx], s.Portals[idx+1:]...)
		// Insert wall at the correct position
		s.Walls = append(s.Walls, world.Wall{})
		copy(s.Walls[insertIdx+1:], s.Walls[insertIdx:])
		s.Walls[insertIdx] = w
		Editor.ActiveEdgeKind = "wall"
		Editor.ActiveEdgeIdx = insertIdx
	}

	s.EdgeKinds[polyIdx] = toKind
	world.SyncEdgeRecordIDs(s)
	_ = world.ReloadSector(ch, s)
}

// polyEdgeIdx returns the polygon edge index for the wall/portal at kind+idx.
func polyEdgeIdx(s *world.Sector, kind string, idx int) int {
	count := 0
	for ei, k := range s.EdgeKinds {
		if k == kind {
			if count == idx {
				return ei
			}
			count++
		}
	}
	return -1
}

// editorGizmoToolButtons draws the move/rotate/scale toggles. Shared by the
// main window's toolbar and the 2D pop-out's, so the tool can be switched
// wherever the view being edited happens to live.
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
}

// editor2DJoinButtons draws the map's join actions. They are the panel half of
// the pair of rows the right-click menu carries: picking points and then saying
// what to do with them is the map's way of drawing a sector, and a workflow
// that lives only in a context menu is a workflow nobody finds.
func editor2DJoinButtons() {
	can := editorCanJoinPoints()
	imguibackend.EditorPushDisabled(!can)
	if imgui.Button(editorJoinLabel(false) + "##join") {
		EditorJoinSelectedPoints(false)
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("J — Draw an edge between each picked point and the next,\n" +
			"in the order you picked them. Ctrl+click points to pick them.")
	}

	imgui.SameLine()
	imguibackend.EditorPushDisabled(!can)
	if imgui.Button(editorJoinLabel(true) + "##joinclose") {
		EditorJoinSelectedPoints(true)
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Shift+J — The same, plus the edge back to the first point,\n" +
			"which closes the outline and turns it into a sector.")
	}

	if len(Editor.PendingEdges) > 0 {
		if imgui.Button(fmt.Sprintf("Discard %d Unjoined Edge(s)##discardpending",
			len(Editor.PendingEdges))) {
			EditorDiscardPendingEdges()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Edges drawn but not yet closed into a sector.\n" +
				"The points themselves stay.")
		}
	}
}
