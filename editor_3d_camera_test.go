package main

import (
	"foundry/internal/platform"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// saveEditorCam snapshots the global camera and the 3D view's orbit half, and
// returns a function that puts both back. Every test here drives Cam directly,
// which the rest of the suite also reads.
func saveEditorCam() func() {
	cam := Cam
	orbit := editorCam3D
	return func() {
		Cam = cam
		editorCam3D = orbit
	}
}

// setupEditorCam puts the camera somewhere with a non-trivial yaw and pitch, so
// a bug that only works looking down an axis cannot pass.
func setupEditorCam() {
	Cam.Position = mgl32.Vec3{3, 2, 7}
	Cam.Yaw = mgl32.DegToRad(200)
	Cam.Pitch = mgl32.DegToRad(-15)
	updateCamDirection()
	editorCam3D.Fly = true
	editorCam3D.Dist = 6
}

func nearlyVec(a, b mgl32.Vec3, tol float32) bool {
	return a.Sub(b).Len() <= tol
}

// The whole point of deriving the orbit eye from the look direction: going
// fly -> orbit -> fly must land back where it started, or every toggle would
// shunt the view sideways.
func TestEditorCameraModeSwitchKeepsTheEye(t *testing.T) {
	defer saveEditorCam()()
	setupEditorCam()

	start := Cam.Position
	dir := Cam.Direction

	editorSetFlyCam(false)
	if !nearlyVec(Cam.Position, start, 1e-4) {
		t.Errorf("switching to orbit moved the eye to %v, want %v", Cam.Position, start)
	}
	if !nearlyVec(Cam.Direction, dir, 1e-5) {
		t.Errorf("switching to orbit turned the look to %v, want %v", Cam.Direction, dir)
	}

	editorSetFlyCam(true)
	if !nearlyVec(Cam.Position, start, 1e-4) {
		t.Errorf("round trip left the eye at %v, want %v", Cam.Position, start)
	}
}

// In orbit the eye is a derived value: it must sit exactly Dist behind the
// pivot along the look direction, whatever the angles are.
func TestEditorOrbitEyeLooksAtTheTarget(t *testing.T) {
	defer saveEditorCam()()
	setupEditorCam()
	editorSetFlyCam(false)

	target := editorCam3D.Target
	for _, d := range []struct{ dx, dy int32 }{{0, 0}, {60, 0}, {0, -40}, {-120, 25}} {
		editorCamLook(d.dx, d.dy)
		toTarget := target.Sub(Cam.Position)
		if math.Abs(float64(toTarget.Len()-editorCam3D.Dist)) > 1e-3 {
			t.Errorf("delta %v: eye is %v from the pivot, want %v",
				d, toTarget.Len(), editorCam3D.Dist)
		}
		if !nearlyVec(toTarget.Normalize(), Cam.Direction, 1e-4) {
			t.Errorf("delta %v: eye does not look at the pivot", d)
		}
		if editorCam3D.Target != target {
			t.Fatalf("delta %v: turning moved the pivot to %v", d, editorCam3D.Target)
		}
	}
}

// Flying must be untouched by the orbit machinery: the eye turns in place.
func TestFlyLookKeepsTheEyeStill(t *testing.T) {
	defer saveEditorCam()()
	setupEditorCam()

	before := Cam.Position
	editorCamLook(80, -30)
	if Cam.Position != before {
		t.Errorf("looking around in fly mode moved the eye to %v, want %v",
			Cam.Position, before)
	}
}

// The wheel means two different things per mode, and neither may leak into the
// other: a radius in orbit, a dolly in fly.
func TestEditorCamZoomPerMode(t *testing.T) {
	defer saveEditorCam()()
	setupEditorCam()
	editorSetFlyCam(false)

	target, dist := editorCam3D.Target, editorCam3D.Dist
	editorCamZoom(1)
	if editorCam3D.Dist >= dist {
		t.Errorf("wheel up left the orbit radius at %v (was %v)", editorCam3D.Dist, dist)
	}
	if editorCam3D.Target != target {
		t.Errorf("zooming moved the pivot to %v", editorCam3D.Target)
	}

	editorSetFlyCam(true)
	platform.DeltaTime = 1.0 / 60
	before := Cam.Position
	editorCamZoom(1)
	moved := Cam.Position.Sub(before)
	if moved.Len() < 1e-4 {
		t.Fatal("wheel up did not dolly the flying eye")
	}
	if !nearlyVec(moved.Normalize(), Cam.Direction, 1e-4) {
		t.Errorf("dolly went %v, want along the look direction %v",
			moved.Normalize(), Cam.Direction)
	}
}

// Panning and WASD move whichever of the eye and the pivot is authoritative,
// and the derived one has to follow rather than drift.
func TestEditorCamMoveTargetsTheRightThing(t *testing.T) {
	defer saveEditorCam()()
	setupEditorCam()

	beforePos := Cam.Position
	editorCamPan(40, 15)
	if Cam.Position == beforePos {
		t.Error("panning in fly mode did not move the eye")
	}

	editorSetFlyCam(false)
	target, eye := editorCam3D.Target, Cam.Position
	editorCamPan(40, 15)
	if editorCam3D.Target == target {
		t.Error("panning in orbit mode did not move the pivot")
	}
	// The eye is derived, so it must have followed the pivot rigidly.
	if !nearlyVec(Cam.Position.Sub(eye), editorCam3D.Target.Sub(target), 1e-4) {
		t.Error("the orbit eye did not travel with its pivot")
	}

	target = editorCam3D.Target
	editorCamMove(1, 0, 0)
	if editorCam3D.Target == target {
		t.Error("WASD in orbit mode did not slide the pivot")
	}
	if math.Abs(float64(Cam.Position.Sub(editorCam3D.Target).Len()-editorCam3D.Dist)) > 1e-3 {
		t.Error("sliding the pivot changed the orbit radius")
	}
}

// Framing has to end up looking at the selection from far enough out to see it,
// and it always lands in orbit — the point of framing something is to turn
// around it.
func TestEditorFrameSelectionOrbitsTheSelection(t *testing.T) {
	defer saveEditorCam()()
	defer saveEditorSelection()()
	setupEditorCam()

	Editor.level = &Level{Chunks: []Chunk{{
		ID:       "c0",
		Points:   []mgl32.Vec2{{0, 0}, {4, 0}, {4, 4}, {0, 4}},
		MinBound: mgl32.Vec3{0, 0, 0},
		MaxBound: mgl32.Vec3{4, 3, 4},
		Sectors: []Sector{{
			ID:           "s0",
			PointIndices: []int{0, 1, 2, 3},
			Floor:        Floor{Height: 0},
			Ceiling:      Ceiling{Height: 3},
		}},
	}}}
	Editor.ActiveChunkIdx = 0
	Editor.ActiveSectorIdx = -1
	Editor.SelectedVertices = nil
	editorClearSpawnSelection()

	// Nothing selected: the active chunk is the fallback.
	editorFrameSelection()
	if editorCam3D.Fly {
		t.Fatal("framing left the camera flying")
	}
	if !nearlyVec(editorCam3D.Target, mgl32.Vec3{2, 1.5, 2}, 1e-4) {
		t.Errorf("pivot = %v, want the chunk centre {2 1.5 2}", editorCam3D.Target)
	}
	if !nearlyVec(Cam.Position.Add(Cam.Direction.Mul(editorCam3D.Dist)), editorCam3D.Target, 1e-3) {
		t.Error("framing did not point the eye at the pivot")
	}

	// A vertex selection is finer-grained and wins over the chunk.
	Editor.SelectedVertices = []vertexSel{{chunkIdx: 0, sectorIdx: 0, vertIdx: 1, pointIdx: 1, isFloor: true}}
	editorFrameSelection()
	if !nearlyVec(editorCam3D.Target, mgl32.Vec3{4, 0, 0}, 1e-4) {
		t.Errorf("pivot = %v, want the selected corner {4 0 0}", editorCam3D.Target)
	}
	if editorCam3D.Dist < editorCamMinDist {
		t.Errorf("framing a single point pulled in to %v, past the minimum %v",
			editorCam3D.Dist, editorCamMinDist)
	}
}

// Framing a water body has to reach the plane it produced, not the polygon that
// describes it — a biome sector is flat on paper and the surface is where the
// user is actually looking.
func TestFrameSelectionReachesTheWaterPlane(t *testing.T) {
	defer saveEditorCam()()
	defer saveEditorSelection()()
	setupEditorCam()

	Editor.level = waterLevel()
	Editor.level.Chunks[0].WaterSurfaces[0].BoundHalfX = 3
	Editor.level.Chunks[0].WaterSurfaces[0].BoundHalfY = 0.1
	Editor.level.Chunks[0].WaterSurfaces[0].BoundHalfZ = 3
	Editor.ActiveChunkIdx = 0
	Editor.ActiveSectorIdx = -1
	Editor.ActiveBiomeSectorIdx = 1 // "lake"
	Editor.SelectedVertices = nil
	editorClearSpawnSelection()

	editorFrameSelection()
	if !nearlyVec(editorCam3D.Target, mgl32.Vec3{0, 0, -4}, 1e-4) {
		t.Errorf("pivot = %v, want the water plane's centre {0 0 -4}", editorCam3D.Target)
	}
}
