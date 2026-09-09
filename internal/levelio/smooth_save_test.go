package levelio

// smooth_save_test.go — smoothing as *authored* data.
//
// The smoothing maths itself is tested in internal/geom; this is about what a
// chunk saves, so it stays with the type it reads.

import (
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"testing"
)

// Smoothing is a per-face authored setting, so it has to survive a save.
func TestSmoothSavesPerSurface(t *testing.T) {
	ch := world.Chunk{
		ID:      "main",
		Scale:   1,
		Brushes: []world.Brush{world.NewBoxBrush("room", mgl32.Vec3{5, 2, 5}, mgl32.Vec3{5, 2, 5}, "concrete")},
	}
	b := &ch.Brushes[0]
	b.Faces[0].Smooth, b.Faces[0].SmoothAngle = true, 35
	b.Faces[2].Smooth, b.Faces[2].SmoothAngle = true, 80

	rc, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatal(err)
	}
	rb := rc.Brushes[0]

	if !rb.Faces[0].Smooth || rb.Faces[0].SmoothAngle != 35 {
		t.Errorf("face 0 = (%v, %v), want (true, 35)", rb.Faces[0].Smooth, rb.Faces[0].SmoothAngle)
	}
	if rb.Faces[1].Smooth {
		t.Error("face 1 picked up face 0's smoothing")
	}

	var smoothed []int
	for i, f := range rb.Faces {
		if f.Smooth {
			smoothed = append(smoothed, i)
		}
	}
	if len(smoothed) != 2 || smoothed[0] != 0 || smoothed[1] != 2 {
		t.Errorf("smoothed faces = %v, want [0 2]", smoothed)
	}
	if rb.Faces[2].SmoothAngle != 80 {
		t.Errorf("face 2 angle = %v, want 80", rb.Faces[2].SmoothAngle)
	}
}
