package editor

// editor_panel_objects.go — the Objects block: what the level contains.
//
// One list per kind, in a fixed order, drawn identically by both editors. Every
// list is reachable with nothing selected, because placing the first of
// something has to be — which is the one deliberate exception to the rule that
// a panel only shows what the selection can act on.
//
// A list row is the selection: clicking it selects the object, and Properties
// picks it up from there. Nothing in this file draws a field; the buttons
// alongside a row are the ones that act on the object as a whole (delete,
// duplicate, export, switch off), because those are list operations rather than
// properties of the thing.

import (
	"fmt"
	"foundry/internal/fileutil"
	"foundry/internal/world"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorObjectsSection draws every list, in order.
func editorObjectsSection(lvl *world.Level, kind editorPanelKind) {
	imgui.Separator()
	if !imgui.CollapsingHeaderV("Objects##objects", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorSpawnListSection(lvl)
	editorChunkListSection(lvl)
	editorEntitySection(lvl)
	editorVolumeSection(lvl)
	editorLightListSection(lvl)
	editorLevelScriptSection(lvl)
	_ = kind
}

// ---------- 2D map ----------

// editor2DMapSection draws the sector map's own actions: the corner-height
// toggle and the point-joining workflow that turns picked points into edges
// and, closed, into a sector.
//
// Between Context and Objects rather than folded into either: it is not level
// content (Objects) and it is not a selected object's fields (Properties) — it
// is what the map itself does with whatever is currently picked.
func editor2DMapSection() {
	imgui.Separator()
	if !imgui.CollapsingHeaderV("Map##map2d", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	editingFloor := Editor.EditFloorVerts
	if editingFloor {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.4, Z: 0.9, W: 1})
	}
	if imgui.Button("Floor Verts##map2dfloor") {
		Editor.EditFloorVerts = true
	}
	if editingFloor {
		imgui.PopStyleColor()
	}
	imgui.SameLine()
	if !editingFloor {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.9, Y: 0.3, Z: 0.3, W: 1})
	}
	if imgui.Button("Ceiling Verts##map2dceil") {
		Editor.EditFloorVerts = false
	}
	if !editingFloor {
		imgui.PopStyleColor()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Which of a corner's two handles the map picks and drags.")
	}

	editor2DJoinButtons()
}

// ---------- spawn point ----------

// editorSpawnListSection draws the level's one spawn point as a row.
//
// A row rather than a list, because a level has exactly one: there is nothing
// to pick from, and "place" and "move" are the same operation. The checkbox is
// whether the level has one at all — unticking deletes it, ticking puts one
// back at the origin, which is where a new level's starts.
func editorSpawnListSection(lvl *world.Level) {
	has := lvl.Spawn != nil
	on := has
	if imgui.Checkbox("##spawnexists", &on) && on != has {
		EditorPushUndo()
		if on {
			placeSpawnPointAt(mgl32.Vec3{})
		} else {
			lvl.Spawn.Delete()
			lvl.Spawn = nil
			editorClearSpawnSelection()
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Whether this level has a spawn point at all.\n" +
			"Ticking puts one back at the origin.")
	}
	imgui.SameLine()
	if imgui.SelectableV("Player Spawn##spawnrow", Editor.SpawnSelected, 0, imgui.Vec2{}) {
		if lvl.Spawn != nil {
			editorSelectSpawn()
		}
	}
	if lvl.Spawn == nil {
		return
	}

	label := "Move Spawn Point"
	if imgui.Button(label + "##spawnplace") {
		Editor.SpawnPlacementMode = true
	}
	if Editor.SpawnPlacementMode {
		imgui.SameLine()
		imgui.Text("Click to place...")
	}
}

// ---------- chunks ----------

// editorChunkListSection draws the chunk list plus the new/import controls.
// The popups it opens are drawn by editorChunkPopups.
//
// This list is the only way to choose which chunk is active — the toolbar's
// dropdown is gone. Two controls for one piece of state meant the picker and
// the list could disagree about it, and the list is where every other selection
// in the panel is made.
func editorChunkListSection(lvl *world.Level) {
	imgui.Separator()
	if !imgui.CollapsingHeaderV("Chunks##chunks", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	if imgui.Button("+##chunknew") {
		Editor.ShowNewChunk = true
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A new chunk at the camera.")
	}
	imgui.SameLine()
	if imgui.Button("Import##chunkimport") {
		editorOpenImportChunk()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Import a chunk from a file in chunks/.")
	}
	imgui.SameLine()
	imgui.Text(fmt.Sprintf("(%d)", len(lvl.Chunks)))

	remove, duplicate, export := -1, -1, -1
	for i := range lvl.Chunks {
		ch := &lvl.Chunks[i]
		imgui.PushID(fmt.Sprintf("chunkrow%d", i))
		if imgui.Button("x") {
			remove = i
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Delete this chunk.")
		}
		imgui.SameLine()
		if imgui.Button("Export") {
			export = i
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Export this chunk to a file.")
		}
		imgui.SameLine()
		if imgui.Button("+") {
			duplicate = i
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Duplicate this chunk.")
		}
		imgui.SameLine()
		// Selected — not merely active — is what puts the highlight on, so the
		// context line's "(selected)" and this row agree.
		selected := Editor.ChunkSelected && i == Editor.ActiveChunkIdx
		label := ch.ID
		if i == Editor.ActiveChunkIdx && !selected {
			label += "  (active)"
		}
		if imgui.SelectableV(label, selected, 0, imgui.Vec2{}) {
			editorSelectChunk(i)
		}
		imgui.PopID()
	}

	// Acted on outside the loop: each rewrites the slice being walked.
	switch {
	case export >= 0:
		Editor.ActiveChunkIdx = export
		Editor.SaveChunkName = lvl.Chunks[export].ID
		Editor.ShowSaveChunk = true
	case duplicate >= 0:
		EditorPushUndo()
		Editor.ActiveChunkIdx = duplicate
		duplicateActiveChunk()
	case remove >= 0:
		Editor.ActiveChunkIdx = remove
		Editor.ShowDeleteChunk = true
	}
}

// ---------- level scripts ----------

// editorLevelScriptSection draws the level's own .flang handlers.
//
// The level has carried these since scripting arrived and no panel has ever
// shown them, so the only way to attach one was to edit the .yaml by hand. They
// are level-wide rather than hung on an object, which is what a script wants
// when it is about the level itself: what happens on load, what a global timer
// does, what the map's rules are.
func editorLevelScriptSection(lvl *world.Level) {
	imgui.Separator()
	if !imgui.CollapsingHeaderV("Scripts##lvlscripts", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorTextMutedWrapped("Run for the level as a whole, rather than for one object in it.")

	if Editor.ScriptFileNames == nil {
		editorRefreshScriptFiles()
	}

	remove := -1
	for i, path := range lvl.Scripts {
		imgui.PushID(fmt.Sprintf("lvlscript%d", i))
		if imgui.Button("x") {
			remove = i
		}
		imgui.SameLine()
		if imgui.Button("Edit") {
			OpenScriptEditorWindow()
			scriptEditorOpenFile(strings.TrimPrefix(path, fileutil.ScriptsDirName+"/"))
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Open in the script editor.")
		}
		imgui.SameLine()
		imgui.Text(path)
		imgui.PopID()
	}
	if len(lvl.Scripts) == 0 {
		editorTextMuted("(none)")
	}
	if remove >= 0 {
		EditorPushUndo()
		lvl.Scripts = append(lvl.Scripts[:remove], lvl.Scripts[remove+1:]...)
	}

	// The add row is a picker over the scripts folder rather than a text field,
	// for the reason every other script picker is: a mistyped path is a script
	// that silently never runs, which is among the harder things to notice.
	if imgui.BeginCombo("##lvlscriptadd", "Add script...") {
		for _, name := range Editor.ScriptFileNames {
			path := fileutil.ScriptsDirName + "/" + name
			if editorLevelHasScript(lvl, path) {
				continue
			}
			if imgui.SelectableV(name, false, 0, imgui.Vec2{}) {
				EditorPushUndo()
				lvl.Scripts = append(lvl.Scripts, path)
			}
		}
		imgui.EndCombo()
	}
	imgui.SameLine()
	if imgui.Button("Rescan##lvlscriptrescan") {
		editorRefreshScriptFiles()
	}
}

// editorLevelHasScript reports whether the level already runs path, so the add
// picker does not offer a duplicate — running one file twice does nothing the
// first run did not.
func editorLevelHasScript(lvl *world.Level, path string) bool {
	for _, s := range lvl.Scripts {
		if s == path {
			return true
		}
	}
	return false
}
