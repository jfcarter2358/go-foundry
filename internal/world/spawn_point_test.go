package world

import (
	"foundry/internal/gfx"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func nearlyEqual(a, b, tol float32) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// An unrotated marker must point the way an unrotated camera looks, or every
// spawn authored by hand would be 90° out.
func TestSpawnDirectionDefaultsToYawZero(t *testing.T) {
	p := SpawnPoint{}
	d := SpawnDirection(&p)
	want := gfx.DirectionFromAngles(0, 0)
	if !nearlyEqual(d.X(), want.X(), 1e-5) || !nearlyEqual(d.Y(), want.Y(), 1e-5) ||
		!nearlyEqual(d.Z(), want.Z(), 1e-5) {
		t.Errorf("got %v, want %v", d, want)
	}
}

// A marker aimed at the sky must not hand gameplay a gimbal-flipped camera.
func TestSpawnPitchClampedToMouseLookLimit(t *testing.T) {
	p := SpawnPoint{Rotation: mgl32.Vec3{0, 0, float32(math.Pi / 2)}}
	_, pitch := SpawnYawPitch(&p)
	if pitch > gfx.MaxPitch || pitch < gfx.MaxPitch-0.02 {
		t.Errorf("straight-up marker: got pitch %.4f, want ≈%.4f", pitch, gfx.MaxPitch)
	}
}

// The arrow is the only mesh whose bounds are not derived from its own vertices,
// so the culling box has to be checked against the geometry by hand.
func TestSpawnArrowFitsItsBounds(t *testing.T) {
	verts, idx := generateSpawnArrow()
	if len(verts) == 0 || len(idx)%3 != 0 {
		t.Fatalf("degenerate arrow mesh: %d verts, %d indices", len(verts), len(idx))
	}
	for _, i := range idx {
		if i < 0 || i >= len(verts) {
			t.Fatalf("index %d out of range (len=%d)", i, len(verts))
		}
	}

	hx, hy, hz := SpawnPointBounds()
	for _, v := range verts {
		if len(v) != 5 {
			t.Fatalf("vertex %v: want 5 floats (xyz + uv)", v)
		}
		if v[0] < -hx || v[0] > hx || v[1] < -hy || v[1] > hy || v[2] < -hz || v[2] > hz {
			t.Errorf("vertex %v escapes bounds (%.2f, %.2f, %.2f)", v[:3], hx, hy, hz)
		}
	}
}

// The mesh sits above the primitive's origin because that origin is the player's
// feet — a marker flush with the floor would z-fight with it.
func TestSpawnArrowClearsTheFloor(t *testing.T) {
	verts, _ := generateSpawnArrow()
	minY := float32(math.MaxFloat32)
	for _, v := range verts {
		if v[1] < minY {
			minY = v[1]
		}
	}
	if minY <= 0 {
		t.Errorf("arrow dips to y=%.3f, want it clear of the floor plane", minY)
	}
}
