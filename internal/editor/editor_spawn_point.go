package editor

// editor_spawn_point.go — authoring the spawn point: selecting it, placing it,
// and its properties panel. Kept out of spawn_point.go so the spawn maths stays
// free of imgui and GL and can be unit-tested headless.

import (
	"fmt"
	"foundry/internal/gfx"
	"foundry/internal/world"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// ---------- selection ----------

// editorSelectSpawn makes the spawn point the current selection, which is what
// gives it the translate gizmo and opens its properties section.
func editorSelectSpawn() {
	editorClearObjectSelections()
	Editor.SpawnSelected = true
	Editor.GizmoKind = gizmoKindSpawn
	Editor.PanelFocus = panelFocusSpawn
}

// editorClearSpawnSelection drops the spawn point from the selection, taking
// the gizmo with it so it cannot keep dragging something no longer selected.
func editorClearSpawnSelection() {
	Editor.SpawnSelected = false
	if Editor.GizmoKind == gizmoKindSpawn {
		Editor.GizmoKind = gizmoKindNone
	}
}

// ---------- placement ----------

// placeSpawnPointAt puts the level's spawn point at pos (the player's feet) and
// selects it. A level holds at most one, so placing again moves the existing
// marker rather than adding a second — which is also why this is the only entry
// point: there is no "add" separate from "move".
//
// Facing starts as the placing camera's yaw only. Placing looks down at the
// floor, and a spawn that pitches the player's view at their own feet is never
// what was meant; the panel's "Face Camera" takes the pitch as well.
func placeSpawnPointAt(pos mgl32.Vec3) {
	Editor.SpawnPlacementMode = false
	lvl := Editor.level
	if lvl == nil {
		return
	}
	EditorPushUndo()
	if lvl.Spawn == nil {
		lvl.Spawn = &world.SpawnPoint{}
	}
	lvl.Spawn.Position = pos
	lvl.Spawn.Rotation = mgl32.Vec3{0, -gfx.Cam.Yaw, 0}
	if err := lvl.Spawn.Load(); err != nil {
		editorSetStatus("Spawn point: %v", err)
	}
	editorSelectSpawn()
}

// spawnRotationFromCamera returns the Euler rotation that makes the spawn
// marker face exactly where the camera is looking. Rotation Y is the negated
// camera yaw and rotation Z is the pitch, which is what SpawnYawPitch reads
// straight back out (see the derivation in spawn_point.go).
func spawnRotationFromCamera() mgl32.Vec3 {
	return mgl32.Vec3{0, -gfx.Cam.Yaw, gfx.Cam.Pitch}
}

// ---------- panel ----------

// editorSpawnPointSection draws the spawn point's properties: where it is, its
// rotation and the facing that falls out of it, and the two "match the fly
// camera" shortcuts.
//
// Selection-driven like every other properties block. It had been written and
// then never wired into a panel, so the marker was the one object in the level
// that could be selected, gizmo-dragged and deleted but never typed into.
func editorSpawnPointSection(lvl *world.Level) {
	s := lvl.Spawn
	if s == nil || !Editor.SpawnSelected {
		return
	}

	imgui.Separator()
	editorFocusOpen(panelFocusSpawn)
	if !imgui.CollapsingHeaderV("Player Spawn##spawnhdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusSpawn)

	editorPropGroupOpen("Transform##spawntfgrp", func() { editorSpawnTransform(s) })
	editorPropGroupOpen("Player##spawnplayergrp", func() { editorSpawnPlayer(s) })
	editorPropGroup("Actions##spawnactions", 0, func() { editorSpawnActions(lvl, s) })
}

// editorSpawnPlayer draws what the player starts able to do. The checkboxes
// read as enabled switches; the spawn stores them as opt-outs so a spawn that
// never touched them saves nothing.
//
// Each toggles a copy: imgui has already written the new value by the time
// Checkbox reports a click, and the undo snapshot has to be of the old one.
func editorSpawnPlayer(s *world.SpawnPoint) {
	control := !s.DisableControl
	if imgui.Checkbox("Player control##spawnctl", &control) {
		EditorPushUndo()
		s.DisableControl = !control
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Off: the player can neither move nor look around until a script\nsets @level.prop.player_control = true.")
	}

	gravity := !s.DisableGravity
	if imgui.Checkbox("Gravity##spawngrav", &gravity) {
		EditorPushUndo()
		s.DisableGravity = !gravity
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Off: the player hangs where they spawn until a script sets\n@level.prop.player_gravity = true.")
	}
}

// editorSpawnTransform draws where the marker is and which way it faces.
func editorSpawnTransform(s *world.SpawnPoint) {
	pos := [3]float32{s.Position.X(), s.Position.Y(), s.Position.Z()}
	if imgui.DragFloat3V("Position##spawn", &pos, 0.05, -1e6, 1e6, "%.2f", 0) {
		s.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}

	// The Euler triple is what the marker actually stores, and the same three
	// world-axis channels the rotate gizmo drags, so typing a number here and
	// dragging a ring in the viewport reach the same field.
	rot := vec3ToDegrees(s.Rotation)
	if editorDragFloat3Step("Rotation (deg)##spawnrot", &rot, -360, 360, "%.1f", 1) {
		s.Rotation = vec3FromDegrees(rot)
	}

	// The facing the rotation works out to, which is what gameplay reads. It is
	// derived rather than editable: the pitch clamp lives in SpawnYawPitch, so a
	// marker aimed straight up shows the angle the player will actually get.
	yaw, pitch := world.SpawnYawPitch(s)
	imgui.Text(fmt.Sprintf("Facing: yaw %.1f°  pitch %.1f°",
		yaw*180/math.Pi, pitch*180/math.Pi))

}

// editorSpawnActions draws the camera shortcuts and the delete.
//
// Authoring a spawn by flying to it and pressing a button is far easier than
// dialling in three Euler angles, so both shortcuts write the rotation that
// SpawnYawPitch reads straight back out.
func editorSpawnActions(lvl *world.Level, s *world.SpawnPoint) {
	if imgui.Button("Face Camera##spawn") {
		EditorPushUndo()
		s.Rotation = spawnRotationFromCamera()
	}
	imgui.SameLine()
	if imgui.Button("Move To Camera##spawn") {
		EditorPushUndo()
		// The camera sits at eye height; the marker sits at the player's feet.
		// SpawnEyeHeight is the gap ApplySpawnPoint puts back, so playing from
		// here starts in exactly this view.
		s.Position = gfx.Cam.Position.Sub(mgl32.Vec3{0, world.SpawnEyeHeight, 0})
		s.Rotation = spawnRotationFromCamera()
	}

	if imgui.Button("Delete Spawn Point##spawn") {
		EditorPushUndo()
		s.Delete()
		lvl.Spawn = nil
		editorClearSpawnSelection()
	}
}
