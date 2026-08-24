package main

import (
	"foundry/internal/gfx"
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// editor_spawn_point_test.go — placing a spawn point from the camera.
//
// spawnRotationFromCamera is editor code (editor_spawn_point.go); the spawn
// point itself, and the arrow mesh that draws it, are tested in
// internal/world.

// The arrow the editor draws and the direction gameplay uses must be the same
// vector, so the yaw/pitch read back off a rotation has to match the camera
// angles that produced it — that round trip is the whole contract.
func TestSpawnRotationFromCameraRoundTrips(t *testing.T) {
	cases := []struct{ yaw, pitch float32 }{
		{0, 0},
		{mgl32.DegToRad(90), 0},
		{mgl32.DegToRad(-135), 0},
		{mgl32.DegToRad(30), mgl32.DegToRad(20)},
		{mgl32.DegToRad(170), mgl32.DegToRad(-45)},
	}
	savedYaw, savedPitch := gfx.Cam.Yaw, gfx.Cam.Pitch
	defer func() { gfx.Cam.Yaw, gfx.Cam.Pitch = savedYaw, savedPitch }()

	for _, c := range cases {
		gfx.Cam.Yaw, gfx.Cam.Pitch = c.yaw, c.pitch
		p := world.SpawnPoint{Rotation: spawnRotationFromCamera()}

		gotYaw, gotPitch := world.SpawnYawPitch(&p)
		// Yaw is compared through the direction vector so ±180° wrap doesn't
		// fail an otherwise-correct answer.
		want := gfx.DirectionFromAngles(c.yaw, c.pitch)
		got := gfx.DirectionFromAngles(gotYaw, gotPitch)
		if !nearlyEqual(got.X(), want.X(), 1e-4) ||
			!nearlyEqual(got.Y(), want.Y(), 1e-4) ||
			!nearlyEqual(got.Z(), want.Z(), 1e-4) {
			t.Errorf("yaw %.1f pitch %.1f: got dir %v, want %v",
				mgl32.RadToDeg(c.yaw), mgl32.RadToDeg(c.pitch), got, want)
		}
		if !nearlyEqual(gotPitch, c.pitch, 1e-4) {
			t.Errorf("yaw %.1f pitch %.1f: got pitch %.4f", c.yaw, c.pitch, gotPitch)
		}
	}
}
