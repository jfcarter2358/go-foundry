package place

// orient_test.go — Rule.Orient and OrientYaw (docs/terrain-paint/
// PHASE_P4.md): a log's long side follows the direction field within the
// jitter, a weak field blends back toward the random yaw, and orienting a
// rule moves no rock.

import (
	"math"
	"testing"
)

func TestOrientYawAlignsAndBlends(t *testing.T) {
	east := func(x, z float64) (float64, float64, float64) { return 1, 0, 1 }
	for _, yaw := range []float64{0.1, 1.7, 3.3, 5.9} {
		got := OrientYaw(east, 0, 0, yaw, 0.05, 0, false)
		// Model +Z along +X (atan2(1, 0) = π/2), either sense, ±jitter.
		if d := math.Abs(math.Remainder(got-math.Pi/2, math.Pi)); d > 0.05+1e-9 {
			t.Fatalf("yaw %.1f → %.3f, %.3f off the field", yaw, got, d)
		}
		if long := OrientYaw(east, 0, 0, yaw, 0, 0, true); math.Abs(math.Remainder(long, math.Pi)) > 1e-9 {
			t.Fatalf("a long-X model turned to %.3f, want its X along the field", long)
		}
	}
	none := func(x, z float64) (float64, float64, float64) { return 0, 0, 0 }
	if OrientYaw(none, 0, 0, 2.5, 0.1, 0, false) != 2.5 {
		t.Fatal("no field changed the yaw")
	}
	half := func(x, z float64) (float64, float64, float64) { return 0, 1, 0.5 }
	d := math.Abs(math.Remainder(OrientYaw(half, 0, 0, 1.0, 0, 0, false), math.Pi))
	if d < 0.3 || d > 0.7 {
		t.Fatalf("a half-strength field left the yaw %.3f off it (the random yaw is 1.0)", d)
	}
}

func TestScatterOrientsLogsAlongTheField(t *testing.T) {
	g := groundOf(t, 64, 1, func(x, z float64) float64 { return 0.01 * x })
	logs := boulders(2)
	for i := range logs {
		logs[i].Extent = [3]float64{3, 0.4, 0.5} // long along X
	}
	r := DefaultRule()
	r.Variants, r.Density, r.Tilt, r.Align, r.Cluster = logs, 6, 0, 0, 0
	r.Scale = [2]float64{0.4, 0.5}
	plain := scatterOnce(t, g, 1, r)
	r.Orient = func(x, z float64) (float64, float64, float64) { return 0, 1, 1 } // along +Z
	r.OrientJitter = 0.08
	or := scatterOnce(t, g, 1, r)
	if len(or.Instances) < 10 || len(or.Instances) != len(plain.Instances) {
		t.Fatalf("%d oriented, %d plain", len(or.Instances), len(plain.Instances))
	}
	for i, in := range or.Instances {
		if in.Pos[0] != plain.Instances[i].Pos[0] || in.Pos[2] != plain.Instances[i].Pos[2] {
			t.Fatalf("instance %d moved", i)
		}
		x := in.Rot.Apply([3]float64{1, 0, 0})
		// The model's X (its length) along world ±Z, within the jitter.
		if a := math.Atan2(math.Abs(x[0]), math.Abs(x[2])); a > 0.08+1e-6 {
			t.Fatalf("log %d lies %.3f rad off the field", i, a)
		}
	}
}
