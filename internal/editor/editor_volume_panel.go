package editor

// editor_volume_panel.go — the Volumes section of the editor's side panel.
//
// Structurally this is the entity panel with the entity-specific parts
// removed: an Add-menu entry and the selected volume's properties. What is
// left after taking out definitions, models, animations and collision flags is
// a transform, a shape, and the tags/attributes/script every scriptable object
// shares — which is most of the point of volumes being their own type.
//
// The level's volume list is in the Explorer (editor_panel_explorer.go), with
// everything else the level contains.

import (
	"foundry/internal/levelio"
	"foundry/internal/world"
	"strconv"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorVolumePropertiesSection draws the selected volume's fields. Nothing is
// drawn when no volume is selected, so the caller can call it unconditionally.
func editorVolumePropertiesSection() {
	v := activeVolume()
	if v == nil {
		return
	}
	imgui.Separator()
	editorFocusOpen(panelFocusVolume)
	if !imgui.CollapsingHeaderV("Volume: "+v.ID+"##volhdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusVolume)
	editorVolumeProperties(v)
}

// editorVolumeAddMenu draws the volume half of the Explorer's Add menu: the ID
// to give the next one, and the row that arms placement.
func editorVolumeAddMenu() {
	imgui.SetNextItemWidth(160)
	imgui.InputTextV("ID##newvol", &Editor.NewVolumeID, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Leave empty to name it \"volume\".\nMade unique on placement.")
	}
	imgui.Separator()
	if imgui.MenuItem("Place By Clicking") {
		editorArmVolumePlacement()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Regions a script reacts to. Never drawn during play\nand never solid.")
	}
}

// editorVolumeProperties draws the selected volume's editable state.
func editorVolumeProperties(v *world.Volume) {
	// The same groups an entity gets, in the same order, so the two blocks read
	// as one panel with different middles.
	editorPropGroupOpen("Identity##volidgrp", func() { editorVolumeIdentity(v) })
	editorPropGroupOpen("Transform##voltfgrp", func() { editorVolumeTransform(v) })
	editorPropGroupOpen("Shape##volshapegrp", func() { editorVolumeShape(v) })
	editorPropGroupOpen("Level Exit##volexitgrp", func() { editorVolumeLevelExit(v) })
	editorScriptDataUI(&v.ScriptData, "vol", strconv.Quote(v.ID))
	editorPropGroup("Actions##volactions", 0, func() { editorVolumeActions(v) })
}

// editorVolumeIdentity draws the ID field and the enabled toggle.
//
// The rename is committed on Enter rather than per keystroke: an ID is what a
// script addresses, and renaming through every intermediate spelling would fire
// a uniqueness check against half-typed names.
func editorVolumeIdentity(v *world.Volume) {
	id := v.ID
	if imgui.InputTextV("ID##volid", &id, imgui.InputTextFlagsEnterReturnsTrue, nil) && id != v.ID {
		lvl := Editor.level
		if id == "" {
			editorSetStatus("A volume needs an ID for a script to address it")
		} else if world.LevelIDTaken(lvl, id) {
			editorSetStatus("%q is already used by something else in this level", id)
		} else {
			EditorPushUndo()
			v.ID = id
		}
	}

	if imgui.Checkbox("Enabled##volenabled", &v.Enabled) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A disabled volume contains nothing, so it fires no events.\nScripts switch this to arm and disarm a trigger.")
	}
}

// editorVolumeTransform draws position and rotation. There is no scale — a
// volume's size is its half-extents, edited under Shape.
func editorVolumeTransform(v *world.Volume) {
	pos := [3]float32{v.Position.X(), v.Position.Y(), v.Position.Z()}
	if editorDragFloat3("Position##volpos", &pos, 0, 0, "%.3f") {
		v.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}

	rot := vec3ToDegrees(v.Rotation)
	if editorDragFloat3Step("Rotation (deg)##volrot", &rot, -360, 360, "%.1f", 1) {
		v.Rotation = vec3FromDegrees(rot)
	}
}

// editorVolumeShape draws the shape combo and the half-extents.
//
// Both rebuild the wireframe cage, which is the only thing here that needs one:
// the cage is built in volume-local space, so moving or rotating a volume needs
// no rebuild at all.
func editorVolumeShape(v *world.Volume) {
	if imgui.BeginCombo("Shape##volshape", world.VolumeShapeName(v.Shape)) {
		for i, name := range world.VolumeShapeNames {
			kind := world.VolumeShape(i)
			if imgui.SelectableV(name, kind == v.Shape, 0, imgui.Vec2{}) && kind != v.Shape {
				EditorPushUndo()
				v.Shape = kind
				rebuildActiveVolumeCage()
			}
		}
		imgui.EndCombo()
	}

	half := [3]float32{v.HalfX, v.HalfY, v.HalfZ}
	if editorDragFloat3Step("Half-extents##volhalf", &half, 0.01, 1000, "%.3f", 0.05) {
		v.HalfX, v.HalfY, v.HalfZ = half[0], half[1], half[2]
		rebuildActiveVolumeCage()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Half the region's size on each axis — the box the shape is inscribed in.")
	}
}

// editorVolumeLevelExit draws the level this volume sends the player to.
//
// A combo over the levels that actually exist rather than a free-text field:
// the name is resolved at the moment the player walks in, so a typo shows up as
// a trigger that silently does nothing, in play, in whichever room the author
// was not looking at. "(none)" is the ordinary case and comes first.
func editorVolumeLevelExit(v *world.Volume) {
	current := v.LoadLevel
	if current == "" {
		current = "(none)"
	}
	open := imgui.BeginCombo("Load Level##volloadlevel", current)
	// Re-read the folder as the list drops down rather than on every frame it
	// is held open — it fills the same cache the Open dialog uses, refreshed at
	// the one moment it could be stale.
	if open && !Editor.VolLevelComboOpen {
		Editor.LevelFiles = levelio.ScanLevelFiles(Editor.LevelsDir)
	}
	Editor.VolLevelComboOpen = open
	if open {
		if imgui.SelectableV("(none)", v.LoadLevel == "", 0, imgui.Vec2{}) && v.LoadLevel != "" {
			EditorPushUndo()
			v.LoadLevel = ""
		}
		for _, f := range Editor.LevelFiles {
			name := strings.TrimSuffix(f, levelio.LevelExt)
			if imgui.SelectableV(name, name == v.LoadLevel, 0, imgui.Vec2{}) && name != v.LoadLevel {
				EditorPushUndo()
				v.LoadLevel = name
			}
		}
		imgui.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Walking the player into this volume loads that level,\n" +
			"covered by the `loading` screen. No script needed.\n" +
			"Only the player triggers it, and only while the volume is enabled.")
	}
	if v.LoadLevel != "" && v.Script != "" {
		imgui.Text("Its script still gets `enter`, before the load starts.")
	}
}

// editorVolumeActions draws duplicate and delete.
func editorVolumeActions(v *world.Volume) {
	if imgui.Button("Duplicate##voldup") {
		duplicateActiveVolume()
	}
	imgui.SameLine()
	if imgui.Button("Delete##voldel") {
		deleteActiveVolume()
	}
}
