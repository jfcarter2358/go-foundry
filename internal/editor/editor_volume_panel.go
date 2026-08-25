package editor

// editor_volume_panel.go — the Volumes section of the editor's side panel.
//
// Structurally this is the entity panel with the entity-specific parts removed:
// a place button, a list, and the selected volume's properties. What is left
// after taking out definitions, models, animations and collision flags is a
// transform, a shape, and the tags/attributes/script every scriptable object
// shares — which is most of the point of volumes being their own type.

import (
	"fmt"
	"foundry/internal/world"
	"strconv"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorVolumeSection draws the place button and the volume list — the
// Objects half. The selected volume's own fields are editorVolumePropertiesSection,
// drawn separately under Properties, the same split editorEntitySection and
// editorEntityProperties keep.
func editorVolumeSection(lvl *world.Level) {
	imgui.Separator()
	if !imgui.CollapsingHeaderV("Volumes", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	editorTextWrapped("Regions a script reacts to. Never drawn during play and never solid.")

	editorVolumePlacement()
	imgui.Separator()
	editorVolumeList(lvl)
}

// editorVolumePropertiesSection draws the selected volume's fields. Nothing is
// drawn when no volume is selected, so the caller can call it unconditionally.
func editorVolumePropertiesSection() {
	v := activeVolume()
	if v == nil {
		return
	}
	imgui.Separator()
	editorFocusOpen(panelFocusVolume)
	if !imgui.CollapsingHeaderV("Volume: "+v.ID, imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusVolume)
	editorVolumeProperties(v)
}

// editorVolumePlacement draws the ID field and the arm-placement button.
func editorVolumePlacement() {
	imgui.InputTextV("ID##newvol", &Editor.NewVolumeID, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Leave empty to name it \"volume\".\nMade unique on placement.")
	}

	if Editor.VolumePlacementMode {
		if imgui.Button("Cancel Placement##volplace") {
			Editor.VolumePlacementMode = false
		}
		imgui.SameLine()
		imgui.Text("Click in the viewport to place")
		return
	}
	if imgui.Button("Place Volume##volplace") {
		editorArmVolumePlacement()
	}
}

// editorVolumeList draws every volume in the level as a selectable row.
func editorVolumeList(lvl *world.Level) {
	if lvl == nil || len(lvl.Volumes) == 0 {
		imgui.Text("(none in this level)")
		return
	}
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		label := v.ID
		if label == "" {
			label = "(unnamed)"
		}
		if !v.Enabled {
			// A disarmed volume is still there and still selectable; saying so
			// in the list saves hunting for why a trigger stopped firing.
			label += "  (disabled)"
		}
		if imgui.SelectableV(fmt.Sprintf("%s##vol%d", label, i), i == Editor.ActiveVolumeIdx, 0, imgui.Vec2{}) {
			clearVertexSelection()
			editorClearSpawnSelection()
			editorClearEntitySelection()
			editorClearChunkSelection()
			editorClearLightSelection()
			editorSelectVolume(i)
		}
	}
}

// editorVolumeProperties draws the selected volume's editable state.
func editorVolumeProperties(v *world.Volume) {
	editorVolumeIdentity(v)
	imgui.Separator()
	editorVolumeTransform(v)
	imgui.Separator()
	editorVolumeShape(v)
	imgui.Separator()
	editorScriptDataUI(&v.ScriptData, "vol", strconv.Quote(v.ID))
	imgui.Separator()
	editorVolumeActions(v)
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
