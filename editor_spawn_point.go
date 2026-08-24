package main

// editor_spawn_point.go — authoring the spawn point: selecting it, placing it,
// and its properties panel. Kept out of spawn_point.go so the spawn maths stays
// free of imgui and GL and can be unit-tested headless.

import (
	"fmt"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// ---------- selection ----------

// editorSelectSpawn makes the spawn point the current selection, which is what
// gives it the translate gizmo and opens its properties section.
func editorSelectSpawn() {
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
		lvl.Spawn = &SpawnPoint{}
	}
	lvl.Spawn.Position = pos
	lvl.Spawn.Rotation = mgl32.Vec3{0, -Cam.Yaw, 0}
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
	return mgl32.Vec3{0, -Cam.Yaw, Cam.Pitch}
}

// ---------- panel ----------

// editorSpawnPointSection draws the spawn point's properties: where it is,
// where it faces, and the two "match the fly camera" shortcuts.
func editorSpawnPointSection(lvl *Level) {
	s := lvl.Spawn
	if s == nil {
		return
	}

	pos := [3]float32{s.Position.X(), s.Position.Y(), s.Position.Z()}
	if imgui.DragFloat3V("Position##spawn", &pos, 0.05, -1e6, 1e6, "%.2f", 0) {
		s.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}

	yaw, pitch := SpawnYawPitch(s)
	imgui.Text(fmt.Sprintf("Facing: yaw %.1f°  pitch %.1f°",
		yaw*180/math.Pi, pitch*180/math.Pi))

	// Authoring a spawn by flying to it and pressing a button is far easier than
	// dialling in three Euler angles, so both shortcuts write the rotation that
	// SpawnYawPitch reads straight back out.
	if imgui.Button("Face Camera##spawn") {
		EditorPushUndo()
		s.Rotation = spawnRotationFromCamera()
	}
	imgui.SameLine()
	if imgui.Button("Move To Camera##spawn") {
		EditorPushUndo()
		// The camera sits at eye height; the marker sits at the player's feet.
		s.Position = Cam.Position.Sub(mgl32.Vec3{0, eyeHeight, 0})
		s.Rotation = spawnRotationFromCamera()
	}

	if imgui.Button("Delete Spawn Point##spawn") {
		EditorPushUndo()
		s.Delete()
		lvl.Spawn = nil
		editorClearSpawnSelection()
	}
}

// ---------- 2D map ----------

// editor2DDrawSpawnHeading draws the marker's facing as an arrow on the top-down
// map, scaled to the footprint it grows out of so it stays readable at any zoom.
func editor2DDrawSpawnHeading(s *SpawnPoint, hx, hz float32, col mgl32.Vec3) {
	d := SpawnDirection(s)
	editor2DDrawHeading(
		mgl32.Vec2{s.Position.X(), s.Position.Z()},
		mgl32.Vec2{d.X(), d.Z()}, hx, hz, col)
}
