package app

import (
	"math"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/platform"
)

func withLookState(t *testing.T) {
	t.Helper()
	cam, fov := gfx.Cam, gfx.FieldOfView
	sens, smooth, inv := platform.MouseSensitivity, platform.MouseSmoothing, platform.MouseInvertY
	t.Cleanup(func() {
		gfx.Cam, gfx.FieldOfView = cam, fov
		platform.MouseSensitivity, platform.MouseSmoothing, platform.MouseInvertY = sens, smooth, inv
		dropMouseLook()
	})
	gfx.Cam = gfx.Camera{}
	gfx.FieldOfView = gfx.DefaultFieldOfView
	platform.MouseSensitivity = 1
	platform.MouseInvertY = false
	dropMouseLook()
}

// Smoothing changes when a flick is delivered, never how far it turns.
func TestSmoothedLookTurnsAsFarAsRawLook(t *testing.T) {
	withLookState(t)
	want := float64(platform.MouseRotationSpeed) * 200

	for _, smoothing := range []float32{0, 0.35, 1} {
		gfx.Cam.Yaw = 0
		platform.MouseSmoothing = smoothing
		bankMouseLook(200, 0)
		applyMouseLook(1.0 / 60)
		first := float64(gfx.Cam.Yaw)
		for i := 0; i < 120; i++ {
			applyMouseLook(1.0 / 60)
		}
		if got := float64(gfx.Cam.Yaw); math.Abs(got-want) > 1e-4 {
			t.Errorf("smoothing %v turned %v in all, want %v", smoothing, got, want)
		}
		if smoothing == 0 && math.Abs(first-want) > 1e-6 {
			t.Errorf("unsmoothed look delivered %v on the first frame, want all %v", first, want)
		}
		if smoothing > 0 && first >= want {
			t.Errorf("smoothing %v delivered the whole flick on the first frame", smoothing)
		}
	}
}

// Zoomed in, a pixel turns the view less, in proportion to the narrower lens.
func TestZoomedLookTurnsLess(t *testing.T) {
	withLookState(t)
	platform.MouseSmoothing = 0
	gfx.FieldOfView = 30
	bankMouseLook(100, 0)
	applyMouseLook(1.0 / 60)
	full := float64(platform.MouseRotationSpeed) * 100
	ratio := math.Tan(15*math.Pi/180) / math.Tan(22.5*math.Pi/180)
	if got := float64(gfx.Cam.Yaw); math.Abs(got-full*ratio) > 1e-5 {
		t.Errorf("zoomed turn %v, want %v", got, full*ratio)
	}
}

func TestInvertedLookFlipsPitch(t *testing.T) {
	withLookState(t)
	platform.MouseSmoothing = 0
	platform.MouseInvertY = true
	bankMouseLook(0, 10) // mouse moved down
	applyMouseLook(1.0 / 60)
	if gfx.Cam.Pitch <= 0 {
		t.Errorf("inverted: moving the mouse down gave pitch %v, want looking up", gfx.Cam.Pitch)
	}
}

func TestControlSettingsRoundTrip(t *testing.T) {
	withLookState(t)
	platform.MouseSensitivity, platform.MouseSmoothing, platform.MouseInvertY = 2.5, 0.8, true
	data, err := marshalControlSettings()
	if err != nil {
		t.Fatal(err)
	}
	platform.MouseSensitivity, platform.MouseSmoothing, platform.MouseInvertY = 1, 0.35, false
	if err := applyControlSettings(data); err != nil {
		t.Fatal(err)
	}
	if platform.MouseSensitivity != 2.5 || platform.MouseSmoothing != 0.8 || !platform.MouseInvertY {
		t.Errorf("round trip gave %v %v %v", platform.MouseSensitivity, platform.MouseSmoothing, platform.MouseInvertY)
	}
	// A file from before a field existed keeps that field's default.
	platform.MouseSmoothing = 0.35
	if err := applyControlSettings([]byte("mouse_sensitivity: 0.5\n")); err != nil {
		t.Fatal(err)
	}
	if platform.MouseSensitivity != 0.5 || platform.MouseSmoothing != 0.35 {
		t.Errorf("partial file gave %v %v", platform.MouseSensitivity, platform.MouseSmoothing)
	}
}
