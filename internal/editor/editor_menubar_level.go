package editor

// editor_menubar_level.go — the level editor's own menus: File, Edit, View and
// Build. The rest of its menu bar is editor_menubar.go's, shared with every
// other editor.

import (
	"fmt"
	"path/filepath"
	"strings"

	"foundry/internal/gfx"
	"foundry/internal/levelio"
	"foundry/internal/render"
	"foundry/internal/uiwin"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorShadingTooltip explains the Solid/Wireframe toggle, wherever it is
// offered — the tool row's button and the View menu's item.
const editorShadingTooltip = "Z — Solid hides the far side of the world, and only the\n" +
	"points and edges you can see can be clicked.\n" +
	"Wireframe draws it as lines and lets you click any of them."

// editorCameraTooltip explains the camera toggle for the mode it is in.
func editorCameraTooltip(flying bool) string {
	if flying {
		return "RMB/MMB drag to look, WASD fly, Space/Shift up and down,\n" +
			"wheel dollies, Caps Lock flies 4x faster,\n" +
			"F frames the selection."
	}
	return "RMB/MMB drag to orbit, Shift+MMB pan, wheel zoom,\n" +
		"WASD slides the pivot, Caps Lock 4x faster,\n" +
		"F frames the selection."
}

func editorLevelFileMenu(lvl *world.Level) {
	if !imgui.BeginMenu("File") {
		return
	}
	if imgui.MenuItem("Open Level...") {
		Editor.LevelFiles = levelio.ScanLevelFiles(Editor.LevelsDir)
		Editor.NewLevelName = ""
		Editor.ShowLevelPicker = true
	}
	editorMenuTooltip("Open a level from levels/, or create a new one.")
	imgui.Separator()
	if imgui.MenuItem("Save") {
		if err := editorSaveLevel(lvl, Editor.SavePath); err != nil {
			editorSetStatus("Save failed: %v", err)
		} else {
			editorSetStatus("Saved to %s", Editor.SavePath)
		}
	}
	if imgui.MenuItem("Save As...") {
		Editor.SaveAsName = filepath.Base(Editor.SavePath)
		Editor.ShowSaveAs = true
	}
	editorSaveAllMenuItem()
	imgui.Separator()
	editorReloadTexturesMenuItem()
	imgui.Separator()
	editorProjectMenuItems()
	// No Exit. Quitting is the window's own close button, the same as every
	// other window the editor opens — an Exit a few pixels from Save is a
	// level lost to a misclick, and the title bar is where anyone looks for it.
	imgui.EndMenu()
}

func editorLevelEditMenu() {
	if !imgui.BeginMenu("Edit") {
		return
	}
	editorUndoRedoItems(EditorCanUndo(), EditorCanRedo(), EditorUndo, EditorRedo)
	imgui.EndMenu()
}

// editorLevelViewMenu is the 3D view's display state, which the tool row
// below also offers as buttons.
func editorLevelViewMenu(lvl *world.Level) {
	if !imgui.BeginMenu("View") {
		return
	}
	if imgui.MenuItemV("Wireframe", "Z", Editor.Wireframe, true) {
		editorSetWireframe(!Editor.Wireframe)
	}
	editorMenuTooltip(editorShadingTooltip)
	if imgui.MenuItemV("Bounds", "B", Editor.ShowBounds, true) {
		editorSetShowBounds(!Editor.ShowBounds)
	}
	editorMenuTooltip(editorBoundsTooltip)
	flying := editorCam3D.Fly
	if imgui.MenuItemV("Fly Camera", "V", flying, true) {
		editorSetFlyCam(!flying)
	}
	editorMenuTooltip(editorCameraTooltip(flying))
	editorLiveGIMenu()
	editorLightingMenu()
	imgui.Separator()
	editorStylizeMenuItem(lvl)
	imgui.EndMenu()
}

// editorLiveGIMenu is the viewport's GI quality — the player setting, so the
// editor shows what a player at that tier would — and GI's debug views.
func editorLiveGIMenu() {
	if !imgui.BeginMenu("Live GI") {
		return
	}
	for i, name := range gfx.GIQualityNames() {
		q := gfx.GIQualityLevel(i)
		if imgui.MenuItemV(strings.ToUpper(name[:1])+name[1:]+"##giq", "", gfx.GIQuality == q, true) {
			gfx.SetGIQuality(q)
		}
	}
	editorMenuTooltip("Live bounce light, at the tier a player picks in the game's\n" +
		"settings. Off is the flat ambient (and, on a compiled level,\n" +
		"the baked bounce).")
	imgui.Separator()
	for _, m := range []render.GIDebugMode{render.GIDebugOff, render.GIDebugLighting, render.GIDebugProbes, render.GIDebugField} {
		label := map[render.GIDebugMode]string{
			render.GIDebugOff: "Normal view", render.GIDebugLighting: "Lighting only",
			render.GIDebugProbes: "Probes", render.GIDebugField: "Distance field",
		}[m]
		if imgui.MenuItemV(label+"##gidbg", "", render.GIDebug == m, true) {
			render.GIDebug = m
		}
	}
	editorMenuTooltip("What GI sees: the light alone on white surfaces, each probe\n" +
		"as a dot of its own light (red where buried in a wall), or the\n" +
		"distance field it traces against.")
	imgui.EndMenu()
}

// editorLevelBuildMenu is compiling the level, looking at the result, and
// shipping it — the three steps in the order they are taken.
func editorLevelBuildMenu() {
	if !imgui.BeginMenu("Build") {
		return
	}
	if imgui.MenuItemV("Compile", "", false, !EditorCompiling()) {
		editorBeginCompile(compileToFile)
	}
	editorMenuTooltip("Build the level's BSP, visibility and baked lighting,\n" +
		"and write it beside the level file. Reports leaks.")
	editorLevelLightingMenu()

	// The numbers Compile prints say whether the level is sealed; only the
	// eye catches a seam or a surface CSG should not have taken, and without
	// this the first sight of a compiled level is the shipped build.
	if imgui.MenuItemV("Preview Compiled", "", EditorPreviewingCompiled(), !EditorCompiling()) {
		editorTogglePreviewCompiled()
	}
	editorMenuTooltip("Draw the level the way a release build would:\n" +
		"compiled surfaces, culled by the compiled visibility set.\n" +
		"Any edit turns it off.")

	imgui.Separator()
	relLabel := "Release..."
	if ReleaseBuildRunning() {
		relLabel = "Release (building...)"
	}
	if imgui.MenuItemV(relLabel+"##buildrel", "", ReleaseWindowOpen(), true) {
		uiwin.RequestWindowOpen(OpenReleaseWindow)
	}
	editorMenuTooltip("Build a release of this project: compile every level, pack the\n" +
		"content beside the game binary, and archive it for each platform.")
	imgui.EndMenu()
}

// editorLevelLightingMenu is what Compile does about light: bake it, or ship
// the level lit live (Level.NoBake).
func editorLevelLightingMenu() {
	lvl := Editor.level
	if lvl == nil || !imgui.BeginMenu("Lighting") {
		return
	}
	if imgui.MenuItemV("Full bake", "", !lvl.NoBake, true) && lvl.NoBake {
		editorSetNoBake(lvl, false)
	}
	editorMenuTooltip("Bake every static light, the ambient and its bounce into the\n" +
		"surfaces. Lit the same on every machine; GI, where the player\n" +
		"has it on, replaces the baked ambient and bounce.")
	if imgui.MenuItemV("No bake (GI only)", "", lvl.NoBake, true) && !lvl.NoBake {
		editorSetNoBake(lvl, true)
	}
	editorMenuTooltip("Skip the light bake: the level ships lit live, as the editor\n" +
		"shows it. Bounce light comes from GI alone, so a player with GI\n" +
		"off sees the flat ambient. Every light counts against the live\n" +
		"limits (4 directional, 8 point). Compiles faster.")
	imgui.EndMenu()
}

// editorSetNoBake switches the level's bake, as an undoable edit, and warns
// when an unbaked level has more lights than can be lit live at once.
func editorSetNoBake(lvl *world.Level, on bool) {
	EditorPushUndo()
	lvl.NoBake = on
	dropCompiledPreview()
	if !on {
		editorSetStatus("Lighting: full bake")
		return
	}
	if w := noBakeLightWarning(lvl.Lights); w != "" {
		editorSetStatus("Lighting: no bake — %s", w)
		return
	}
	editorSetStatus("Lighting: no bake — the level ships lit live, with GI")
}

// noBakeLightWarning names what an unbaked level loses to the live light
// limits: lights past world.MaxDirLights / MaxPointLights are not drawn.
func noBakeLightWarning(ls world.LightSet) string {
	var parts []string
	if n := len(ls.Directional); n > world.MaxDirLights {
		parts = append(parts, fmt.Sprintf("%d directional lights, %d are lit", n, world.MaxDirLights))
	}
	if n := len(ls.Points); n > world.MaxPointLights {
		parts = append(parts, fmt.Sprintf("%d point lights, %d are lit", n, world.MaxPointLights))
	}
	if len(parts) == 0 {
		return ""
	}
	return "over the live light limit: " + strings.Join(parts, "; ")
}

// editorTogglePreviewCompiled flips the compiled preview and reports how it
// went on the status line.
//
// Turning it on is a compile, so that runs behind the progress bar and reports
// when it finishes.
func editorTogglePreviewCompiled() {
	if !EditorPreviewingCompiled() {
		editorBeginCompile(compileToPreview)
		return
	}
	msg, err := EditorSetCompiledPreview(false)
	if err != nil {
		editorSetStatus("Preview failed: %v", err)
	} else {
		editorSetStatus("%s", msg)
	}
}

// editorLightingMenu is the lighting pass's player settings
// (gfx/lighting_settings.go), so the viewport shows what a player with them
// would see.
func editorLightingMenu() {
	if !imgui.BeginMenu("Lighting Quality") {
		return
	}
	for _, b := range []struct {
		label string
		v     bool
		set   func(bool)
		tip   string
	}{
		{"Specular", gfx.SpecularLighting, gfx.SetSpecularLighting, "Highlights from every light, by each material's roughness and metalness."},
		{"Ambient Occlusion", gfx.AmbientOcclusion, gfx.SetAmbientOcclusion, "Darkens ambient light in creases and where things touch."},
		{"Bloom", gfx.Bloom, gfx.SetBloom, "Bright light glows into the pixels round it (TAA frames)."},
		{"Contact Shadows", gfx.ContactShadows, gfx.SetContactShadows, "Short screen-space sun shadows under small things."},
		{"Soft Shadows", gfx.SoftShadows, gfx.SetSoftShadows, "Sun shadows soften with distance from the caster."},
	} {
		if imgui.MenuItemV(b.label+"##lq", "", b.v, true) {
			b.set(!b.v)
		}
		editorMenuTooltip(b.tip)
	}
	imgui.Separator()
	for i, name := range gfx.ReflectionQualityNames() {
		q := gfx.ReflectionQuality(i)
		if imgui.MenuItemV("Reflections: "+strings.ToUpper(name[:1])+name[1:]+"##rq", "", gfx.Reflections == q, true) {
			gfx.SetReflections(q)
		}
	}
	editorMenuTooltip("Off: highlights only. Low: the sky and reflection volumes,\n" +
		"scaled by the GI probes. High: plus screen-space reflections.")
	imgui.EndMenu()
}
