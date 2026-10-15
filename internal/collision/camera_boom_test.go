package collision

// camera_boom_test.go — the third-person boom against leveltest's room.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/world"
)

func TestCameraBoomReachesFullLengthInTheOpen(t *testing.T) {
	lvl := rayRoom(t)
	got := CameraBoom(lvl, mgl32.Vec3{5, 2, 5}, mgl32.Vec3{1, 0, 0}, 2, 0.3)
	if got != 2 {
		t.Errorf("boom = %v, want the full 2 with nothing in the way", got)
	}
}

func TestCameraBoomIsPulledInRadiusShortOfAWall(t *testing.T) {
	lvl := rayRoom(t)
	from, dir := mgl32.Vec3{5, 2, 5}, mgl32.Vec3{1, 0, 0}
	wall := Raycast(lvl, from, dir, RayFilter{Range: 100, Geometry: true})
	if !wall.Blocked {
		t.Fatal("no wall along +X: the fixture changed")
	}
	wallT := wall.Stop.Sub(from).Len()
	const r = 0.3
	got := CameraBoom(lvl, from, dir, 20, r)
	if d := got - (wallT - r); d > 1e-3 || d < -1e-3 {
		t.Errorf("boom = %v, want %v (the wall at %v less the radius)", got, wallT-r, wallT)
	}
}

func TestCameraBoomSideRaysCatchAWallTheCentreMisses(t *testing.T) {
	// Grazing the floor: aimed slightly down from just above it, the centre
	// ray meets the floor late, but the ray a radius below it meets it far
	// sooner — and that is where a camera of that radius would touch it.
	lvl := rayRoom(t)
	from := mgl32.Vec3{2, 0.6, 5}
	dir := mgl32.Vec3{1, -0.05, 0}
	centre := Raycast(lvl, from, dir, RayFilter{Range: 100, Geometry: true})
	centreT := centre.Stop.Sub(from).Len()
	got := CameraBoom(lvl, from, dir, 50, 0.3)
	if got >= centreT-0.3 {
		t.Errorf("boom = %v, want well short of the centre ray's %v: the side rays should meet the floor first", got, centreT-0.3)
	}
}

func TestCameraBoomNeverGoesNegative(t *testing.T) {
	lvl := rayRoom(t)
	// Pressed right up against the far wall, looking into it.
	wall := Raycast(lvl, mgl32.Vec3{5, 2, 5}, mgl32.Vec3{1, 0, 0}, RayFilter{Range: 100, Geometry: true})
	from := wall.Stop.Sub(mgl32.Vec3{0.05, 0, 0})
	if got := CameraBoom(lvl, from, mgl32.Vec3{1, 0, 0}, 5, 0.3); got != 0 {
		t.Errorf("boom = %v, want 0 against the wall", got)
	}
}

func TestCameraBoomLooksThroughSmallPropsButNotBigOnes(t *testing.T) {
	prop := func(half float32) world.Scenery {
		return world.Scenery{
			ID: "prop", Position: mgl32.Vec3{4, 2, 5}, Scale: mgl32.Vec3{1, 1, 1}, Solid: true,
			LocalMin: mgl32.Vec3{-half, -half, -half}, LocalMax: mgl32.Vec3{half, half, half},
		}
	}
	from, dir := mgl32.Vec3{2, 2, 5}, mgl32.Vec3{1, 0, 0}

	small := rayRoom(t)
	small.Scenery = []world.Scenery{prop(0.3)}
	if got := CameraBoom(small, from, dir, 3, 0.1); got != 3 {
		t.Errorf("boom past a pebble = %v, want the full 3", got)
	}

	big := rayRoom(t)
	big.Scenery = []world.Scenery{prop(1.5)}
	got := CameraBoom(big, from, dir, 3, 0.1)
	// The boulder's near face is at x = 4 - 1.5 = 2.5: half a unit out.
	if got > 0.45 || got < 0.35 {
		t.Errorf("boom against a boulder = %v, want about 0.4", got)
	}
}
