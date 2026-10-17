package editor

// editor_menubar.go — the top bar every editor window shares.
//
// Each editor's top bar is two rows. The first is an ImGui menu bar laid out
// the same way everywhere:
//
//	File  Edit  View  [editor-specific]  Window | Level Texture Model … UI     name  [Saved]
//
// File/Edit/View hold that editor's own actions; Window reaches the tool
// windows (Log, Git, Release, Unused Assets); the strip after it switches to any other
// editor, lit for the one this is; and the document's name and save chip sit
// at the right edge. The second row is the tools the mouse uses — selection
// units, gizmo tools, the handful of toggles worth one click — plus the status
// line. What used to be a row of file buttons lives in the menus now.
//
// Every switch or window open goes through uiwin.RequestWindowOpen, including
// from the main window, where calling the Open function directly would be
// safe: one route is less to get wrong, and a frame's delay is invisible.

import (
	"foundry/internal/material"
	"foundry/internal/platform"
	"foundry/internal/textureui"
	"foundry/internal/uiwin"
	"foundry/internal/world"
	"strings"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorTopBarH is the height of every editor's top bar: the menu bar plus one
// row of tool buttons. What sits under it — side panels, the model editor's
// outline, the level editor's properties panel — starts at this Y.
const editorTopBarH = float32(54)

// editorTopBarFlags are the window flags of a top bar drawn as its own window
// (the level, model, entity, particle and UI editors). The text editors draw
// their menu bar on their full-window root instead, with the same menus.
const editorTopBarFlags = imgui.WindowFlagsNoTitleBar | imgui.WindowFlagsNoResize |
	imgui.WindowFlagsNoMove | imgui.WindowFlagsNoScrollbar | imgui.WindowFlagsMenuBar |
	imgui.WindowFlagsNoSavedSettings

// editorKind names an editor the switcher can reach.
type editorKind int

const (
	editorKindLevel editorKind = iota
	editorKindTexture
	editorKindModel
	editorKindEntity
	editorKindScript
	editorKindYAML
	editorKindParticles
	editorKindMaterial
	editorKindUI
	editorKindAI
	editorKindTracker
	editorKindScore
	editorKindGenerator
	editorKindPolyHaven
)

// editorSwitchTarget is one entry of the switcher strip.
type editorSwitchTarget struct {
	kind  editorKind
	label string
	isUp  func() bool
	show  func()
	tip   string
}

// editorSwitchTargets lists the editors in strip order. Built per call rather
// than held in a package var: the level entry's show closes over nothing, but
// keeping them together as a function keeps the order and the tooltips in one
// place without an init-order question.
func editorSwitchTargets() []editorSwitchTarget {
	return []editorSwitchTarget{
		{editorKindLevel, "Level", func() bool { return true }, raiseMainWindow,
			"The level editor — the main window."},
		{editorKindTexture, "Texture", textureui.TextureEditorOpen, textureui.OpenTextureEditor,
			"Paint textures, their frames, layers and bump maps."},
		{editorKindModel, "Model", ModelEditorOpen, OpenModelEditorWindow,
			"Model meshes, bones, skin weights and animation clips (.fmdl),\n" +
				"and the shared skeletons (.fskl) several models can be rigged to."},
		{editorKindEntity, "Entity", EntityEditorOpen, OpenEntityEditorWindow,
			"Author entity types (" + world.EntitiesDirName + "/*.yaml) — models,\n" +
				"carried chunk geometry, a script and a vehicle block.\nPlace them from Explorer -> Add -> Entity."},
		{editorKindScript, "Script", ScriptEditorOpen, OpenScriptEditorWindow,
			"Edit .flang scripts, with completion and live checking."},
		{editorKindYAML, "YAML", LevelYAMLEditorOpen, OpenLevelYAMLEditor,
			"Edit this level's file as text — find/replace, and completion\n" +
				"and documentation for every key. Saving rebuilds the level here\n" +
				"from it, as one step Ctrl+Z takes back."},
		{editorKindParticles, "Particles", ParticleEditorOpen, OpenParticleEditorWindow,
			"Author particle effects (.fpar). Place them in the\nlevel as emitters from Explorer -> Add -> Emitter."},
		{editorKindMaterial, "Material", MaterialEditorOpen, OpenMaterialEditorWindow,
			"Author materials (" + material.MaterialsDirName + "/*" + material.MaterialExt + ") — the shader\n" +
				"effects a surface, sprite, particle layer or sky wears.\nName one from any texture picker's Material row."},
		{editorKindUI, "UI", UIEditorOpen, OpenUIEditorWindow,
			"Author screens (.fui) — menus, HUDs and loading screens.\nAttach them to the level from Properties -> Level -> UI."},
		{editorKindAI, "AI", AIEditorOpen, OpenAIEditorWindow,
			"Draw behaviours as decision trees (.fai) — what an agent does about\n" +
				"what happens to it. Give one to an entity type with `ai_tree:`;\n" +
				"the `ai:` block beside it still says how it moves."},
		{editorKindTracker, "Tracker", TrackerEditorOpen, OpenTrackerEditorWindow,
			"Compose music (.ftrk) — patterns, instruments and samples, with\n" +
				"sections and params a script can steer. Play one with play_music."},
		{editorKindScore, "Score", ScoreEditorOpen, OpenScoreEditorWindow,
			"Write music in notation (.fscore) — staves, chords, voices, tuplets,\n" +
				"entered the way Sibelius or MuseScore enters them, played through\n" +
				"the same instruments as the Tracker."},
		{editorKindGenerator, "Generator", GeneratorEditorOpen, OpenGeneratorEditorWindow,
			"Procedural generators (generators/*.fgen) — stages that build terrain,\n" +
				"rocks and scatter, previewed live and run into a level's scenery."},
		{editorKindPolyHaven, "Poly Haven", PolyHavenBrowserOpen, OpenPolyHavenBrowser,
			"Browse and import Poly Haven's CC0 scans — rocks, cliffs, plants and\n" +
				"ground textures — into models/polyhaven/ and textures/ph_*."},
	}
}

// raiseMainWindow brings the level editor's window forward. It is always
// open — it is the application — so switching to it never creates anything.
func raiseMainWindow() {
	if platform.Window != nil {
		platform.Window.Raise()
	}
}

// editorMenuBarShared draws the part of the menu bar that is the same in every
// editor: the Window menu, then the switcher strip. Called inside
// BeginMenuBar/EndMenuBar, after the editor's own menus.
func editorMenuBarShared(current editorKind) {
	editorWindowMenu()

	// A thin divider between "menus" and "places to go". Separator lays out
	// vertically inside a horizontal menu bar, as it does in a toolbar row.
	imgui.Separator()
	for _, t := range editorSwitchTargets() {
		here := t.kind == current
		// Editors with no window up are drawn dim, so a glance says which
		// windows exist without hunting for them on the taskbar. Latched, like
		// every conditional style push: the click can change the answer.
		dim := !here && !t.isUp()
		if dim {
			imgui.PushStyleColor(imgui.StyleColorText, imgui.CurrentStyle().Color(imgui.StyleColorTextDisabled))
		}
		if imgui.MenuItemV(t.label+"##switch", "", here, true) && !here {
			uiwin.RequestWindowOpen(t.show)
		}
		if dim {
			imgui.PopStyleColor()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(t.tip)
		}
	}
}

// editorWindowMenu is the Window menu: the tool windows, which are not editors
// of a document and so are not in the switcher strip.
func editorWindowMenu() {
	if !imgui.BeginMenu("Window") {
		return
	}
	if imgui.MenuItemV("Log", "", LogWindowOpen(), true) {
		uiwin.RequestWindowOpen(OpenLogWindow)
	}
	editorMenuTooltip("Live script output, errors and engine messages.")

	claudeLabel := "Claude Code"
	if line := claudeStatusLine(); line != "" {
		claudeLabel = "Claude Code — " + line
	}
	if imgui.MenuItemV(claudeLabel+"##claudewin", "", ClaudeWindowOpen(), true) {
		uiwin.RequestWindowOpen(OpenClaudeWindow)
	}
	editorMenuTooltip("Ask Claude to write scripts, build levels and generate assets in\nthis project. It sees the project's files and the editor's state.")

	gitLabel := "Git"
	if GitOperationRunning() {
		gitLabel = "Git (running...)"
	}
	if imgui.MenuItemV(gitLabel+"##gitwin", "", GitWindowOpen(), true) {
		uiwin.RequestWindowOpen(OpenGitWindow)
	}
	editorMenuTooltip("Version control for resources/: see what changed, commit it,\nswitch branches, and push and pull.")

	relLabel := "Release"
	if ReleaseBuildRunning() {
		relLabel = "Release (building...)"
	}
	if imgui.MenuItemV(relLabel+"##relwin", "", ReleaseWindowOpen(), true) {
		uiwin.RequestWindowOpen(OpenReleaseWindow)
	}
	editorMenuTooltip("Build a release of this project: compile every level, pack the\ncontent beside the game binary, and archive it for each platform.")

	if imgui.MenuItemV("Unused Assets##unusedwin", "", UnusedWindowOpen(), true) {
		uiwin.RequestWindowOpen(OpenUnusedWindow)
	}
	editorMenuTooltip("List the files in resources/ that nothing in the game uses,\nand delete the ones you pick.")

	if imgui.MenuItem("Cook Nanite Meshes##nanitecook") {
		editorCookAllNanite()
	}
	editorMenuTooltip("Cook every glTF (.gltf, .glb) under resources/ that has changed\ninto a Nanite mesh (.fnm) beside it, ready to place as scenery.")

	imgui.Separator()
	// The editors again, by name, for anyone who reaches for a menu before
	// they read the strip.
	for _, t := range editorSwitchTargets() {
		if imgui.MenuItemV(t.label+"##winmenu", "", t.kind != editorKindLevel && t.isUp(), true) {
			uiwin.RequestWindowOpen(t.show)
		}
	}
	imgui.EndMenu()
}

// editorSaveAllMenuItem is Save All as a File-menu entry: disabled while
// nothing open has anything to save, naming what would be saved when not.
func editorSaveAllMenuItem() {
	names := unsavedEditorNames()
	if imgui.MenuItemV("Save All", "", false, len(names) > 0) {
		uiwin.RequestWindowOpen(EditorSaveAll)
	}
	if imgui.IsItemHoveredV(imgui.HoveredFlagsAllowWhenDisabled) {
		if len(names) == 0 {
			imgui.SetTooltip("Every open editor is saved.")
		} else {
			imgui.SetTooltip("Save every open editor with unsaved changes:\n" + strings.Join(names, ", "))
		}
	}
}

// editorUndoRedoItems is the top of every Edit menu.
func editorUndoRedoItems(canUndo, canRedo bool, undo, redo func()) {
	if imgui.MenuItemV("Undo", "Ctrl+Z", false, canUndo) {
		undo()
	}
	if imgui.MenuItemV("Redo", "Ctrl+Y", false, canRedo) {
		redo()
	}
}

// editorMenuToggle is a checkmarked menu item that flips *v.
func editorMenuToggle(label, shortcut string, v *bool) bool {
	if imgui.MenuItemV(label, shortcut, *v, true) {
		*v = !*v
		return true
	}
	return false
}

// editorMenuTooltip is the hover text for the item just drawn.
func editorMenuTooltip(tip string) {
	if imgui.IsItemHovered() {
		imgui.SetTooltip(tip)
	}
}

// editorMenuBarDoc draws the document's name and save chip at the right edge
// of the menu bar. dirty adds the " *" as well as turning the chip red, so the
// name alone still says it when the chip is off-screen in a narrow window.
//
// Right-aligned by measuring, not by layout: ImGui has no right-alignment, and
// the strip before it is of variable width. When the window is too narrow for
// both, the name follows the strip instead of overlapping it.
func editorMenuBarDoc(name string, dirty bool) {
	if dirty {
		name += " *"
	}
	style := imgui.CurrentStyle()
	chip := "Saved"
	if dirty {
		chip = "Unsaved Changes"
	}
	w := imgui.CalcTextSize(name, false, 0).X + style.ItemSpacing().X +
		imgui.CalcTextSize(chip, false, 0).X + style.FramePadding().X*2
	x := imgui.WindowWidth() - w - style.WindowPadding().X
	if x > imgui.CursorPosX() {
		imgui.SameLineV(x, 0)
	}
	imgui.Text(name)
	imgui.SameLine()
	editorSaveChip(dirty)
}

// editorTopBarBegin opens a top bar drawn as its own window, width w, at the
// top of whichever window is current. The caller fills the menu bar between
// BeginMenuBar/EndMenuBar, then row two, then calls imgui.End.
func editorTopBarBegin(id string, w float32) {
	imgui.SetNextWindowPos(imgui.Vec2{})
	imgui.SetNextWindowSize(imgui.Vec2{X: w, Y: editorTopBarH})
	imgui.BeginV(id, nil, editorTopBarFlags)
}

// editorCloseWindowItem is the last File-menu entry of a pop-out editor. The
// document stays in memory; the switcher brings the window back.
func editorCloseWindowItem(close func()) {
	imgui.Separator()
	if imgui.MenuItem("Close Window") {
		close()
	}
	editorMenuTooltip("Close this window. Its document stays open —\nthe editor strip brings it back as you left it.")
}

// editorStatusText draws a status message on row two, after a divider, when
// there is one.
func editorStatusText(msg string) {
	if msg == "" {
		return
	}
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	editorTextMuted(msg)
}
