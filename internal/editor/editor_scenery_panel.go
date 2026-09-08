package editor

// editor_scenery_panel.go — the brush's controls and a placed prop's fields.
//
// Two blocks, and the split between them is the whole idea of the tool. The
// brush section says what the *next* prop will be — model, facing, size,
// solidity — and is the only section in this panel that stays relevant after
// the click that used it. The properties section says what the *selected* prop
// is, and looks like every other object's: identity, transform, actions.
//
// The level's scenery list lives in the Explorer with everything else the level
// contains (editor_panel_explorer.go); this file is the Add-menu entry, the
// brush block and the selected prop's properties.

import (
	"fmt"
	"foundry/internal/imguibackend"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// ---------- the Add menu's Scenery entry ----------

// editorSceneryAddMenu draws the scenery half of the Explorer's Add menu.
//
// There is no "place by clicking" row here as there is for every other kind,
// because arming the brush *is* that row and the brush is not a one-shot: the
// model picker turns it on, and it stays on until Escape. That asymmetry is
// deliberate and is the one thing worth saying in the menu itself.
func editorSceneryAddMenu() {
	imgui.SetNextItemWidth(240)
	path := Editor.BrushModel
	if modelFileCombo("Model##brushmodel", &path) {
		editorArmBrush(path)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Picking a model arms the brush.\nA ghost follows the cursor; every click places a copy.")
	}
	imgui.Separator()
	if Editor.BrushActive {
		if imgui.MenuItem("Stop Brushing") {
			editorDisarmBrush()
		}
	} else if imgui.MenuItemV("Start Brushing", "", false, Editor.BrushModel != "") {
		editorArmBrush(Editor.BrushModel)
	}
}

// ---------- the brush block ----------

// editorSceneryBrushSection draws the brush's own controls.
//
// Unlike every other properties block in this panel it is not selection-driven:
// it is drawn whenever the brush has a model, because what it shows is about
// the next click rather than about anything currently selected. Hidden entirely
// when there is no model, so a panel that is not scattering anything does not
// carry a block about scattering.
func editorSceneryBrushSection() {
	if Editor.BrushModel == "" && !Editor.BrushActive {
		return
	}
	imgui.Separator()
	// No editorFocusOpen here, unlike every block around it: this one is not
	// about the selection, so a click that selected a prop has no business
	// unfolding it.
	if !imgui.CollapsingHeaderV("Scenery Brush##brushhdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorSceneryBrushBody()
}

// editorSceneryBrushBody is the controls themselves, split out so a test can
// reason about the state they write without an imgui frame around them.
func editorSceneryBrushBody() {
	path := Editor.BrushModel
	if modelFileCombo("Model##brushmodel2", &path) {
		editorArmBrush(path)
	}

	if Editor.BrushActive {
		editorTextColored(imgui.Vec4{X: 0.2, Y: 0.9, Z: 1, W: 1},
			"Armed — click in the 3D view to place")
		if imgui.Button("Stop##brushstop") {
			editorDisarmBrush()
		}
	} else {
		imguibackend.EditorPushDisabled(Editor.BrushModel == "")
		if imgui.Button("Start##brushstart") {
			editorArmBrush(Editor.BrushModel)
		}
		imguibackend.EditorPopDisabled()
	}

	yaw := mgl32.RadToDeg(Editor.BrushRotation.Y())
	if editorDragFloatStep("Yaw (deg)##brushyaw", &yaw, -180, 180, "%.1f", 1) {
		Editor.BrushRotation[1] = mgl32.DegToRad(yaw)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Or [ and ] in the 3D view, in 15-degree steps\n(5 with Shift held).")
	}

	scale := editorBrushScale()
	if editorDragFloat("Scale##brushscale", &scale, 0.05, 100, "%.3f") {
		Editor.BrushScale = scale
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Uniform. Or - and = in the 3D view.\nThe placed prop's own panel has per-axis scale.")
	}

	if imgui.Checkbox("Random yaw per copy##brushrandyaw", &Editor.BrushRandomYaw) {
		// Nothing to rebuild: the ghost keeps the dialled-in facing, and the
		// randomness is applied at the stamp — see brushRotationFor.
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Each copy gets its own facing, so a scattered field\ndoes not read as one model repeated.\nThe ghost still shows the yaw above.")
	}

	imgui.Checkbox("Solid##brushsolid", &Editor.BrushSolid)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Whether copies stop the player and entities.\nCollision is the model's own bounding box.")
	}

	if lvl := Editor.level; lvl != nil {
		editorTextMuted(fmt.Sprintf("%d in level, %d drawn", len(lvl.Scenery), sceneryDrawnCount()))
	}
}

// ---------- the selected prop ----------

// editorSceneryPropertiesSection draws the selected prop's fields. Nothing is
// drawn when none is selected, so the caller can call it unconditionally.
func editorSceneryPropertiesSection() {
	s := activeScenery()
	if s == nil {
		return
	}
	imgui.Separator()
	editorFocusOpen(panelFocusScenery)
	if !imgui.CollapsingHeaderV("Scenery: "+s.ID+"##schdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusScenery)
	editorSceneryProperties(s)
}

// editorSceneryProperties draws the prop's editable state, in the group order
// every other object uses.
func editorSceneryProperties(s *world.Scenery) {
	editorPropGroupOpen("Identity##scidgrp", func() { editorSceneryIdentity(s) })
	editorPropGroupOpen("Transform##sctfgrp", func() { editorSceneryTransform(s) })
	editorPropGroupOpen("Collision##sccolgrp", func() { editorSceneryCollision(s) })
	editorPropGroup("Actions##scactions", 0, func() { editorSceneryActions(s) })
}

// editorSceneryIdentity draws the ID field and the model picker.
//
// The rename commits on Enter rather than per keystroke, for the reason every
// other ID field in this editor does: renaming through every half-typed
// spelling would fire a uniqueness check against each one.
func editorSceneryIdentity(s *world.Scenery) {
	id := s.ID
	if imgui.InputTextV("ID##scid", &id, imgui.InputTextFlagsEnterReturnsTrue, nil) && id != s.ID {
		if id == "" {
			editorSetStatus("A scenery placement needs an ID to be listed by")
		} else if world.LevelIDTaken(Editor.level, id) {
			editorSetStatus("%q is already used by something else in this level", id)
		} else {
			EditorPushUndo()
			s.ID = id
		}
	}

	path := s.Model
	if modelFileCombo("Model##scmodel", &path) {
		// The undo snapshot is of the state being left, so the path is put back
		// before it is taken — the same dance editorEntitySetModelPath does.
		next := path
		s.Model = path
		EditorPushUndo()
		s.Model = next
		editorSceneryReload(s)
	}
	if imgui.Button("Reload##screload") {
		editorSceneryReload(s)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Re-read the mesh from disk — what picks up a .fmdl\nre-saved by the model editor.")
	}
	imgui.SameLine()
	if imgui.Button("Use As Brush##scasbrush") {
		Editor.BrushRotation = s.Rotation
		Editor.BrushScale = s.EffectiveScale().X()
		Editor.BrushSolid = s.Solid
		editorArmBrush(s.Model)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Point the brush at this prop's model, facing and size,\nso the next thirty match the one you just got right.")
	}
}

// editorSceneryTransform draws position, rotation and scale.
func editorSceneryTransform(s *world.Scenery) {
	pos := [3]float32{s.Position.X(), s.Position.Y(), s.Position.Z()}
	if editorDragFloat3("Position##scpos", &pos, 0, 0, "%.3f") {
		s.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}

	rot := vec3ToDegrees(s.Rotation)
	if editorDragFloat3Step("Rotation (deg)##scrot", &rot, -360, 360, "%.1f", 1) {
		s.Rotation = vec3FromDegrees(rot)
	}

	sc := s.EffectiveScale()
	scale := [3]float32{sc.X(), sc.Y(), sc.Z()}
	if editorDragFloat3("Scale##scscale", &scale, 0, 0, "%.3f") {
		s.Scale = mgl32.Vec3{
			world.EntityScaleClamp(scale[0]),
			world.EntityScaleClamp(scale[1]),
			world.EntityScaleClamp(scale[2]),
		}
	}
}

// editorSceneryCollision draws the one flag, and says what the volume behind it
// actually is — a prop's bounds are read off its model rather than authored, so
// there is nothing here to edit and everything to explain.
func editorSceneryCollision(s *world.Scenery) {
	if imgui.Checkbox("Solid##scsolid", &s.Solid) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Stops the player and any entity that collides with\nthe world. Switch off for grass and decals.")
	}
	if !s.HasBounds() {
		editorTextMutedWrapped("No volume: the model is flat on an axis, or has not loaded. Nothing will collide with this.")
		return
	}
	h := s.BoundHalfExtents()
	editorTextMutedWrapped(fmt.Sprintf("Box from the model: %.2f x %.2f x %.2f",
		h.X()*2, h.Y()*2, h.Z()*2))
}

// editorSceneryActions draws the whole-object buttons.
func editorSceneryActions(s *world.Scenery) {
	if imgui.Button("Duplicate##scdup") {
		duplicateActiveScenery()
	}
	imgui.SameLine()
	if imgui.Button("Delete##scdel") {
		deleteActiveScenery()
	}
	_ = s
}
