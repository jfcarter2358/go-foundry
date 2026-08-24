package world

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestSyncDirectionAlwaysUnit(t *testing.T) {
	cases := []struct{ yaw, pitch float32 }{
		{0, 0}, {45, -50}, {-720, 0}, {90, -90}, {180, 90}, {37.5, -12.25},
	}
	for _, c := range cases {
		dl := DirectionalLight{Yaw: c.yaw, Pitch: c.pitch}
		dl.SyncDirection()
		l := dl.Direction.Len()
		if math.Abs(float64(l)-1) > 1e-5 {
			t.Errorf("yaw=%v pitch=%v: direction %v has length %v, want 1", c.yaw, c.pitch, dl.Direction, l)
		}
	}
}

func TestSyncDirectionClampsPitch(t *testing.T) {
	dl := DirectionalLight{Pitch: -140}
	dl.SyncDirection()
	if dl.Pitch != -90 {
		t.Errorf("pitch clamped to %v, want -90", dl.Pitch)
	}
	if dl.Direction.Y() > -0.999 {
		t.Errorf("pitch -90 should point straight down, got %v", dl.Direction)
	}
}

func TestDirLightYawPitchRoundTrip(t *testing.T) {
	cases := []struct{ yaw, pitch float32 }{
		{0, 0}, {45, -50}, {135, 30}, {-90, -12}, {179, 89},
	}
	for _, c := range cases {
		dl := DirectionalLight{Yaw: c.yaw, Pitch: c.pitch}
		dl.SyncDirection()
		yaw, pitch := DirLightYawPitch(dl.Direction)
		if math.Abs(float64(yaw-c.yaw)) > 0.01 || math.Abs(float64(pitch-c.pitch)) > 0.01 {
			t.Errorf("round trip of yaw=%v pitch=%v gave yaw=%v pitch=%v", c.yaw, c.pitch, yaw, pitch)
		}
	}
}

// A zero-length or NaN direction — what the old vector editor produced when a
// component was dragged through zero — must fall back to the defaults instead
// of propagating into the shader uniform and shadow matrix.
func TestDirLightYawPitchRejectsDegenerateVectors(t *testing.T) {
	nan := float32(math.NaN())
	inf := float32(math.Inf(1))
	for _, d := range []mgl32.Vec3{{0, 0, 0}, {nan, nan, nan}, {0, nan, 0}, {inf, 0, 0}} {
		yaw, pitch := DirLightYawPitch(d)
		if yaw != DefaultDirLightYaw || pitch != DefaultDirLightPitch {
			t.Errorf("DirLightYawPitch(%v) = (%v, %v), want defaults", d, yaw, pitch)
		}
	}
}
