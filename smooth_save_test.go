package main

// smooth_save_test.go — smoothing and detail settings as *authored* data.
//
// The smoothing maths itself is tested in internal/geom; these two are about
// what a chunk saves and what a DetailSpec implies, so they stay with the
// types they read.

import (
	"foundry/internal/world"
	"testing"
)

// Smoothing is a per-surface authored setting, so it has to survive a save.
func TestSmoothSavesPerSurface(t *testing.T) {
	ch := squareChunk()
	s := &ch.Sectors[0]
	s.Floor.Smooth, s.Floor.SmoothAngle = true, 35
	s.Walls[2].Smooth, s.Walls[2].SmoothAngle = true, 80

	rc, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatal(err)
	}
	rs := rc.Sectors[0]

	if !rs.Floor.Smooth || rs.Floor.SmoothAngle != 35 {
		t.Errorf("floor = (%v, %v), want (true, 35)", rs.Floor.Smooth, rs.Floor.SmoothAngle)
	}
	if rs.Ceiling.Smooth {
		t.Error("ceiling picked up the floor's smoothing")
	}

	var smoothed []int
	for i, e := range rs.Edges {
		if e.Smooth {
			smoothed = append(smoothed, i)
			if e.SmoothAngle != 80 {
				t.Errorf("edge %d angle = %v, want 80", i, e.SmoothAngle)
			}
		}
	}
	if len(smoothed) != 1 || smoothed[0] != 2 {
		t.Errorf("smoothed edges = %v, want [2]", smoothed)
	}
}

// The displacement-field difference step has to shrink as features get finer,
// and stay bounded at both ends.
func TestDispNormalEps(t *testing.T) {
	flat := &world.DetailSpec{}
	if got := dispNormalEps(flat); got <= 0 || got > 0.25 {
		t.Fatalf("flat spec eps = %v, want a bounded positive default", got)
	}

	coarse := &world.DetailSpec{Waves: [world.DetailMaxWaves]world.DetailWave{{Enabled: true, Frequency: 0.5}}}
	fine := &world.DetailSpec{Waves: [world.DetailMaxWaves]world.DetailWave{{Enabled: true, Frequency: 5}}}
	if dispNormalEps(fine) >= dispNormalEps(coarse) {
		t.Fatalf("finer waves must use a smaller step: fine=%v coarse=%v",
			dispNormalEps(fine), dispNormalEps(coarse))
	}

	// Disabled slots must not drive the step.
	off := &world.DetailSpec{Waves: [world.DetailMaxWaves]world.DetailWave{{Enabled: false, Frequency: 20}}}
	if dispNormalEps(off) != dispNormalEps(flat) {
		t.Fatalf("disabled wave affected eps: %v vs %v", dispNormalEps(off), dispNormalEps(flat))
	}

	// Noise octaves double the frequency each step, so they can be the finest
	// feature present.
	noisy := &world.DetailSpec{Noise: true, NoiseFrequency: 2, NoiseOctaves: 5}
	if dispNormalEps(noisy) >= dispNormalEps(&world.DetailSpec{Noise: true, NoiseFrequency: 2, NoiseOctaves: 1}) {
		t.Fatal("more noise octaves must shrink the step")
	}
	if got := dispNormalEps(&world.DetailSpec{Noise: true, NoiseFrequency: 500, NoiseOctaves: 6}); got < 0.001 {
		t.Fatalf("eps = %v, want it clamped at 0.001", got)
	}
}
