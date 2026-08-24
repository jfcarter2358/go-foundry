package main

// editor_spawn_point.go — the spawn-point half of the primitive property panel.
// Kept out of spawn_point.go so the spawn maths stays free of imgui and GL and
// can be unit-tested headless.

import (
	"fmt"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorSpawnPointSection draws the controls that replace the texture/appearance
// block for a spawn marker: where it faces, the two "match the fly camera"
// shortcuts, and whether this is the marker gameplay will actually use.
func editorSpawnPointSection(lvl *Level, ch *Chunk, p *Primitive) {
	yaw, pitch := SpawnYawPitch(p)
	imgui.Text(fmt.Sprintf("Facing: yaw %.1f°  pitch %.1f°",
		yaw*180/math.Pi, pitch*180/math.Pi))

	// Authoring a spawn by flying to it and pressing a button is far easier than
	// dialling in three Euler angles, so both shortcuts write the rotation that
	// SpawnYawPitch reads straight back out (see the derivation in spawn_point.go:
	// rotation Y is the negated camera yaw, rotation Z is the pitch).
	if imgui.Button("Face Camera##spawn") {
		EditorPushUndo()
		p.Rotation = spawnRotationFromCamera()
	}
	imgui.SameLine()
	if imgui.Button("Move To Camera##spawn") {
		EditorPushUndo()
		// The camera sits at eye height; the marker sits at the player's feet.
		p.Position = Cam.Position.Sub(mgl32.Vec3{0, eyeHeight, 0})
		p.Rotation = spawnRotationFromCamera()
		rebuildPrimitiveIndices(ch)
	}

	if active := FindSpawnPoint(lvl); active == p {
		imgui.Text("Active spawn for this level.")
	} else if active != nil {
		imgui.Text(fmt.Sprintf("Inactive — %q spawns first.", active.ID))
	}
}

// spawnRotationFromCamera returns the Euler rotation that makes a spawn marker
// face exactly where the camera is looking.
func spawnRotationFromCamera() mgl32.Vec3 {
	return mgl32.Vec3{0, -Cam.Yaw, Cam.Pitch}
}

// editor2DDrawSpawnHeading draws the marker's facing as an arrow on the top-down
// map, scaled to the footprint it grows out of so it stays readable at any zoom.
func editor2DDrawSpawnHeading(p *Primitive, hx, hz float32, col mgl32.Vec3) {
	d := SpawnDirection(p)
	flat := mgl32.Vec2{d.X(), d.Z()}
	if flat.Len() < 1e-4 {
		// Pointing straight up or down has no heading to draw.
		return
	}
	flat = flat.Normalize()

	reach := hx
	if hz > reach {
		reach = hz
	}
	reach *= 2.2

	c := mgl32.Vec3{p.Position.X(), 0, p.Position.Z()}
	tip := c.Add(mgl32.Vec3{flat.X() * reach, 0, flat.Y() * reach})
	GizmoLine(c, tip, col)

	// Barbs at ±140° from the heading, a third of the shaft long.
	const barbAngle = 140 * math.Pi / 180
	barb := reach / 3
	for _, sign := range []float64{1, -1} {
		a := math.Atan2(float64(flat.Y()), float64(flat.X())) + sign*barbAngle
		GizmoLine(tip, tip.Add(mgl32.Vec3{
			float32(math.Cos(a)) * barb, 0, float32(math.Sin(a)) * barb,
		}), col)
	}
}
