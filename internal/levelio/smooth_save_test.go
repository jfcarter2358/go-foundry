package levelio

// smooth_save_test.go — smoothing and detail settings as *authored* data.
//
// The smoothing maths itself is tested in internal/geom; these two are about
// what a chunk saves and what a DetailSpec implies, so they stay with the
// types they read.

import (
	"foundry/internal/detail"
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

// The displacement-field difference step has to shrink as features get finer,
// and stay bounded at both ends.
func TestDispNormalEps(t *testing.T) {
	flat := &world.DetailSpec{}
	if got := detail.DispNormalEps(flat); got <= 0 || got > 0.25 {
		t.Fatalf("flat spec eps = %v, want a bounded positive default", got)
	}

	coarse := &world.DetailSpec{Waves: [world.DetailMaxWaves]world.DetailWave{{Enabled: true, Frequency: 0.5}}}
	fine := &world.DetailSpec{Waves: [world.DetailMaxWaves]world.DetailWave{{Enabled: true, Frequency: 5}}}
	if detail.DispNormalEps(fine) >= detail.DispNormalEps(coarse) {
		t.Fatalf("finer waves must use a smaller step: fine=%v coarse=%v",
			detail.DispNormalEps(fine), detail.DispNormalEps(coarse))
	}

	// Disabled slots must not drive the step.
	off := &world.DetailSpec{Waves: [world.DetailMaxWaves]world.DetailWave{{Enabled: false, Frequency: 20}}}
	if detail.DispNormalEps(off) != detail.DispNormalEps(flat) {
		t.Fatalf("disabled wave affected eps: %v vs %v", detail.DispNormalEps(off), detail.DispNormalEps(flat))
	}

	// Noise octaves double the frequency each step, so they can be the finest
	// feature present.
	noisy := &world.DetailSpec{Noise: true, NoiseFrequency: 2, NoiseOctaves: 5}
	if detail.DispNormalEps(noisy) >= detail.DispNormalEps(&world.DetailSpec{Noise: true, NoiseFrequency: 2, NoiseOctaves: 1}) {
		t.Fatal("more noise octaves must shrink the step")
	}
	if got := detail.DispNormalEps(&world.DetailSpec{Noise: true, NoiseFrequency: 500, NoiseOctaves: 6}); got < 0.001 {
		t.Fatalf("eps = %v, want it clamped at 0.001", got)
	}
}
