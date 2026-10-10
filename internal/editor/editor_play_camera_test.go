package editor

// editor_play_camera_test.go — leaving play puts the 3D view back.
//
// Play moves the camera: onto the spawn point, and then wherever the player
// walks. Coming back out of it used to adopt that camera, so a test run of a
// doorway on the far side of the level dumped the author there with no way back
// to what they were working on.

import (
	"foundry/internal/gfx"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// saveCam3D captures the whole camera — the global eye and the orbit half —
// and puts it back on cleanup, so these tests do not leak into the others that
// drive the gizmo through gfx.Cam.
func saveCam3D(t *testing.T) {
	t.Helper()
	cam, orbit := gfx.Cam, editorCam3D
	t.Cleanup(func() { gfx.Cam, editorCam3D = cam, orbit })
}

func TestTheEditorCameraSurvivesAPlaySession(t *testing.T) {
	saveCam3D(t)

	gfx.Cam.Position = mgl32.Vec3{12, 5, -30}
	gfx.Cam.Yaw, gfx.Cam.Pitch = 1.25, -0.4
	editorCam3D.Fly = false
	editorCam3D.Target = mgl32.Vec3{12, 2, -22}
	editorCam3D.Dist = 8
	saved := editorCamSave()

	// Play walks the body somewhere else entirely.
	gfx.Cam.Position = mgl32.Vec3{-400, 1, 900}
	gfx.Cam.Yaw, gfx.Cam.Pitch = -2.0, 0.3
	editorCam3D.Fly = true
	editorCam3D.Target = mgl32.Vec3{}
	editorCam3D.Dist = 200

	if !editorCamRestore(saved) {
		t.Fatal("a saved camera reported nothing to restore")
	}
	if got := gfx.Cam.Yaw; got != 1.25 {
		t.Errorf("yaw came back as %v, want 1.25", got)
	}
	if got := gfx.Cam.Pitch; got != -0.4 {
		t.Errorf("pitch came back as %v, want -0.4", got)
	}
	if editorCam3D.Fly {
		t.Error("came back in fly mode; the view was orbiting when play started")
	}
	if editorCam3D.Target != (mgl32.Vec3{12, 2, -22}) {
		t.Errorf("orbit pivot came back as %v, want the one play started from", editorCam3D.Target)
	}
	if editorCam3D.Dist != 8 {
		t.Errorf("orbit distance came back as %v, want 8", editorCam3D.Dist)
	}
	// The eye is derived from the pivot while orbiting (editorApplyCamAngles),
	// so it is checked by distance from the pivot rather than by equality.
	if d := gfx.Cam.Position.Sub(editorCam3D.Target).Len(); d < 7.9 || d > 8.1 {
		t.Errorf("the eye sits %v from the pivot, want the saved distance of 8", d)
	}
}

// A camera that was never saved restores nothing, so the very first entry into
// the editor still falls through to adopting wherever the camera is.
func TestAnUnsetPlayCameraRestoresNothing(t *testing.T) {
	saveCam3D(t)
	gfx.Cam.Position = mgl32.Vec3{1, 2, 3}
	if editorCamRestore(editorCamState{}) {
		t.Fatal("an unset camera state claimed to restore something")
	}
	if gfx.Cam.Position != (mgl32.Vec3{1, 2, 3}) {
		t.Errorf("the camera moved to %v anyway", gfx.Cam.Position)
	}
}

// Flying is restored too, not just the orbit: a view left in fly mode must not
// come back orbiting a pivot it never had.
func TestRestoringAFlyingCameraKeepsTheEyeExactly(t *testing.T) {
	saveCam3D(t)
	gfx.Cam.Position = mgl32.Vec3{-3, 9, 14}
	gfx.Cam.Yaw, gfx.Cam.Pitch = 0.5, 0.1
	editorCam3D.Fly = true
	saved := editorCamSave()

	gfx.Cam.Position = mgl32.Vec3{100, 100, 100}
	editorCam3D.Fly = false

	editorCamRestore(saved)
	if !editorCam3D.Fly {
		t.Error("came back orbiting; the view was flying when play started")
	}
	if gfx.Cam.Position != (mgl32.Vec3{-3, 9, 14}) {
		t.Errorf("the eye came back at %v, want exactly where it was left", gfx.Cam.Position)
	}
}
