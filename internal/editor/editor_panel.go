package editor

// editor_panel.go — the side panel's frame, shared by both level editors.
//
// Every panel is one column in one order:
//
//	Context -> Explorer -> Properties -> View -> Editor
//
// so a section keeps its place regardless of which mode put it there, and the
// two editors read as the same panel with different middles. Context says what
// is selected and in what mode; the Explorer is one tree over everything the
// level contains, and is always reachable, because it is how the first of
// anything gets placed and how any of it gets picked; Properties is entirely
// selection-driven and draws the selected object's fields and nothing else;
// View is about the viewport; Editor is about the editor rather than the level.
//
// Nothing here decides *what* a section holds — that is
// editor_panel_explorer.go and editor_panel_properties.go. This file decides
// the order, and which of the two editors is asking.
//
// See SETTINGS_LAYOUT.md for the layout this implements.

import (
	"fmt"

	"foundry/internal/audio"
	"foundry/internal/platform"
	"foundry/internal/render"
	"foundry/internal/uiwin"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorPanelKind is which editor's panel is being drawn. Sections that differ
// between the two switch on it rather than being written twice.
type editorPanelKind int

const (
	panelKind3D editorPanelKind = iota
	panelKind2D
)

// editorPanelBody draws the whole panel, in order. The caller owns the
// imgui.Begin/End around it and the modal popups after it.
func editorPanelBody(lvl *world.Level, kind editorPanelKind) {
	editorContextSection(lvl, kind)
	if kind == panelKind2D {
		editor2DMapSection()
	}
	editorExplorerSection(lvl, kind)
	editorPropertiesSection(lvl, kind)
	editorViewSection(kind)
	editorSettingsSection()

	// Every section has had its chance to open and scroll itself.
	Editor.PanelFocus = panelFocusNone
}

// ---------- Context ----------

// editorContextSection draws the running answer to "where am I and what am I
// holding".
//
// Not collapsible, and first: it is the line you read before deciding whether
// the panel below is showing you what you think it is. It is also where the
// active chunk is reported now that the toolbar has no picker for it.
func editorContextSection(lvl *world.Level, kind editorPanelKind) {
	name := "3D"
	if kind == panelKind2D {
		name = "Sector Map"
	}
	editorTextMuted("Editor: " + name)

	imgui.Text(fmt.Sprintf("Mode: %s select   Gizmo: %s", Editor.SelMode, Editor.GizmoTool))
	imgui.Text("Selection: " + editorPanelSelectionSummary(kind))

	if activeChunkIndexValid() {
		chunkLine := "Chunk: " + lvl.Chunks[Editor.ActiveChunkIdx].ID
		if Editor.ChunkSelected {
			chunkLine += "  (selected)"
		}
		imgui.Text(chunkLine)
	} else {
		imgui.Text("Chunk: (none)")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The chunk being edited: the map draws it, a new sector\n" +
			"joins it, the biome layer paints it.\nPick another in the Explorer's Chunks list.")
	}

	if kind == panelKind2D {
		imgui.Text(fmt.Sprintf("Zoom: %.0f px/unit", Editor.Zoom2D))
		which := "floor"
		if !Editor.EditFloorVerts {
			which = "ceiling"
		}
		imgui.Text("Editing: " + which + " corners")
		editorTextMutedWrapped("Scroll zooms, MMB drags, shift+click adds a point, " +
			"ctrl+click multi-selects, J joins, RMB opens the menu.")
	}

	if Editor.StatusMsg != "" {
		editorTextMutedWrapped(Editor.StatusMsg)
	}
}

// editorPanelSelectionSummary names the selection for the context line. The
// context menus already name it for their headings, and the panel says the same
// thing so the two cannot describe one selection differently.
func editorPanelSelectionSummary(kind editorPanelKind) string {
	if Editor.SelectedLightKind != lightKindNone && editorLightSelected() {
		switch Editor.SelectedLightKind {
		case lightKindGlobal:
			return "Global light"
		case lightKindDir:
			dl, i := selectedDirLight()
			return "Directional light " + editorDirLightLabel(dl, i)
		case lightKindPoint:
			pl, i := selectedPointLight()
			return "Point light " + editorPointLightLabel(pl, i)
		}
	}
	if ch := selectedChunk(); ch != nil {
		return "Chunk " + ch.ID
	}
	if kind == panelKind2D {
		return editor2DSelectionSummary()
	}
	return editor3DSelectionSummary()
}

// ---------- View ----------

// editorViewSection draws the controls that change how the viewport looks
// without changing the level.
func editorViewSection(kind editorPanelKind) {
	imgui.Separator()
	if !imgui.CollapsingHeader("View##view") {
		return
	}
	if kind == panelKind2D {
		editorView2DControls()
		return
	}
	editorView3DControls()
}

func editorView3DControls() {
	// Latched before the button for the reason editorActiveToggleButton
	// documents: clicking flips the very global the caller is testing.
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
	imgui.SameLine()
	if imgui.Button("Frame Selection [F]##viewframe") {
		editorFrameSelection()
	}

	imgui.Checkbox("Show All Point/Edge Boxes##viewboxes", &Editor.ShowPointSelectRadius)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Wireframe already draws them all; this is the same\nfor a solid-shaded view.")
	}

	editorTextMutedWrapped("Right- or middle-drag turns, shift+middle pans, the wheel " +
		"zooms. WASD flies (orbit: slides the pivot), space and shift go up and down. " +
		"1/2/3 pick points, edges or faces; ctrl+click adds to the selection; " +
		"right-click without dragging opens the selection's actions.")
}

func editorView2DControls() {
	imgui.Checkbox("Show All Point Boxes##view2dboxes", &Editor.ShowPointSelectRadius)
	if editorDragFloatStep("Zoom (px/unit)##view2dzoom", &Editor.Zoom2D, 1, 400, "%.0f", 1) {
		// Nothing derived to rebuild — the map reads Zoom2D straight.
		_ = Editor.Zoom2D
	}
	if imgui.Button("Frame Chunk##view2dframe") {
		editor2DFrameActiveChunk()
	}
	imgui.SameLine()
	if imgui.Button("Switch to 3D Editor##view2dto3d") {
		Editor.Mode = Mode3D
	}
}

// ---------- Editor ----------

// editorSettingsSection draws what is about the editor rather than about the
// level. Same controls in the same place in every mode of both editors: these
// are the ones you reach for mid-task, and hunting for them per mode is what
// this panel's rework was for.
func editorSettingsSection() {
	imgui.Separator()
	if !imgui.CollapsingHeaderV("Editor##editorsettings", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	if imgui.TreeNodeV("Grid##editorgrid", imgui.TreeNodeFlagsDefaultOpen) {
		editorGridSettings()
		imgui.TreePop()
	}
	if imgui.TreeNode("Engine##editorengine") {
		editorEngineSettings()
		imgui.TreePop()
	}
	if imgui.TreeNode("Audio##editoraudio") {
		editorAudioSettings()
		imgui.TreePop()
	}
	if imgui.TreeNode("Rendering##editorrender") {
		editorRenderingSettings()
		imgui.TreePop()
	}
	if imgui.TreeNode("Panel##editorpanel") {
		editorPanelSettings()
		imgui.TreePop()
	}
}

// editorEngineSettings draws the engine-wide switches. They are not level data
// and do not save with it — they say whether the engine honours the per-object
// flags at all, which is how you tell "this entity is not solid" from
// "collision is off".
func editorEngineSettings() {
	imgui.Checkbox("Entity/world collision  [8]##engworld", &world.EntityWorldCollisionEnabled)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Engine-wide. Each entity still has to opt in;\nthis says whether the engine listens.")
	}
	imgui.Checkbox("Entity/entity collision  [9]##engent", &world.EntityEntityCollisionEnabled)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Engine-wide. Each entity still has to opt in;\nthis says whether the engine listens.")
	}
	imgui.Checkbox("Gravity  [0]##enggrav", &platform.GravityEnabled)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Off is fly mode. Applies to the player during play.")
	}
	editorTextMutedWrapped("Entities only move while the editor is closed.")
}

// editorRenderingSettings draws the biome shadow cost switches and the window
// -level toggles.
//
// These have only ever been function keys, which is right for measuring one
// against the frame time next to it and wrong for finding out they exist. Both
// routes write the same globals, so the checkboxes track the keys.
func editorRenderingSettings() {
	imgui.Checkbox("Biome scatter receives shadows  [F5]##rndrecv", &render.BiomeShadows.Receive)
	imgui.Checkbox("Rock / moss cast shadows  [F6]##rndrock", &render.BiomeShadows.CastRockMoss)
	imgui.Checkbox("Foliage casts shadows  [F7]##rndfoliage", &render.BiomeShadows.CastFoliage)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Expensive: re-instances every blade into every cascade,\n" +
			"and a blade thinner than a shadow texel aliases into speckle.\n" +
			"Worth measuring before shipping on.")
	}

	cascades := int32(render.BiomeShadows.CastCascades)
	if imgui.SliderInt("Cast cascades  [F8]##rndcasc", &cascades, 0, int32(render.ShadowCascades)) {
		render.BiomeShadows.CastCascades = int(cascades)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How many cascades the scatter is drawn into, nearest first.\n0 switches scatter casting off without losing the per-kind switches.")
	}

	tiers := int32(render.BiomeShadows.CastLODTiers)
	if imgui.SliderInt("Cast LOD tiers  [F9]##rndlod", &tiers, 1, 3) {
		render.BiomeShadows.CastLODTiers = int(tiers)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How many LOD tiers (Near/Mid/Far) cast at all, nearest first.\nKinds with one tier ignore it.")
	}

	if editorDragFloatStep("Cast range##rndrange", &render.BiomeShadows.CastRange, 0, 400, "%.0f", 1) {
		if render.BiomeShadows.CastRange < 0 {
			render.BiomeShadows.CastRange = 0
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("An extra cap on the scatter ring for the shadow passes.\n0 = whatever the shadow map itself already reaches.")
	}

	imgui.Separator()
	aa := render.AntiAliasEnabled
	if imgui.Checkbox("Anti-aliasing  [F10]##rndaa", &aa) {
		// Through ToggleAntiAlias so the GL state is applied, not just the flag.
		render.ToggleAntiAlias()
	}
	if render.AntiAliasGrantedSamples <= 0 {
		editorTextMutedWrapped("The window was not granted multisampling, so this has nothing to toggle.")
	}
	imgui.Checkbox("Debug overlay  [F3]##rnddebug", &render.DebugOverlayVisible)
	fullscreen := platform.IsFullscreen()
	if imgui.Checkbox("Fullscreen  [F11]##rndfull", &fullscreen) {
		platform.RequestToggleFullscreen()
	}
}

// editorPanelSettings draws the panel's own geometry and the way into the
// keybinding reference.
func editorPanelSettings() {
	w := Editor.PanelWidth
	maxW := float32(platform.WinWidth) - 20
	if maxW < panelMinWidth {
		maxW = panelMinWidth
	}
	if editorDragFloatStep("Panel width##panelw", &w, panelMinWidth, maxW, "%.0f", 5) {
		Editor.PanelWidth = w
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Also draggable from the panel's left edge.")
	}
	h := Editor.ExplorerHeight
	if editorDragFloatStep("Explorer height##panelexph", &h, explorerMinHeight, 800, "%.0f", 5) {
		Editor.ExplorerHeight = h
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How tall the Explorer's tree is before it scrolls.\n" +
			"The rest of the panel is the selected object's properties.")
	}
	if imgui.Button("Keybindings  [F1]##panelkeys") {
		uiwin.KeybindHelpToggle()
	}
}

// editorGridSettings (the step-size / snap-to-grid controls, with their own
// [-]/drag/[+]/[=] widget) lives in editor_ui.go — the surface editor's panel
// draws only that part of the Editor block, so it stays its own function
// rather than folding into editorSettingsSection here.

// editorAudioSettings draws the three gains.
//
// Engine-wide rather than level data, so it sits with the other switches under
// Editor Settings rather than in a properties block. Unlike the rest of them
// these persist: they are written to <exe>/audio.yaml when a slider is
// released, because a player who turns the music down expects it to stay down.
//
// Every label is Basic Latin. The default ImGui bitmap font renders anything
// else as a literal "?", which SETTINGS_LAYOUT.md is explicit about.
func editorAudioSettings() {
	if !audio.Available() {
		// A section with nothing to act on says so in one line rather than
		// being greyed out, the way the rendering block reports a window that
		// was not granted multisampling.
		editorTextMutedWrapped("No audio device. Sliders would have nothing to act on.")
		return
	}

	master := audio.MasterGain()
	if editorDragFloatStep("Master gain##audmaster", &master, 0, 1, "%.2f", 0.05) {
		audio.SetMasterGain(master)
	}
	editorSaveAudioSettingsOnRelease()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Multiplies everything, music included.")
	}

	music := audio.MusicGain()
	if editorDragFloatStep("Music gain##audmusic", &music, 0, 1, "%.2f", 0.05) {
		audio.SetMusicGain(music)
	}
	editorSaveAudioSettingsOnRelease()

	effects := audio.EffectsGain()
	if editorDragFloatStep("Effects gain##audsfx", &effects, 0, 1, "%.2f", 0.05) {
		audio.SetEffectsGain(effects)
	}
	editorSaveAudioSettingsOnRelease()

	// The mixer's own numbers, which are the only way to tell "the clip is
	// silent" from "the sound is not playing at all" without guessing.
	st := audio.Snapshot()
	editorTextMuted(fmt.Sprintf("%d voice(s), peak %.2f", st.Voices, st.Peak))
	if st.Underruns > 0 {
		editorTextMutedWrapped(fmt.Sprintf("%d underrun(s): the mixer fell behind the device.", st.Underruns))
	}
}

// editorSaveAudioSettingsOnRelease writes the gains once a drag ends, rather
// than on every frame of it — a drag is one file write, not two hundred.
func editorSaveAudioSettingsOnRelease() {
	if imgui.IsItemDeactivatedAfterEdit() {
		if err := audio.SaveSettings(); err != nil {
			editorSetStatus("Could not save audio settings: %v", err)
		}
	}
}
