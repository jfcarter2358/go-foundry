package cluster

// degenerate_test.go — a triangle with two corners at one point (what a
// cook's quantisation makes of a sliver) must not turn the DAG's error into
// NaN: a NaN error is never "under the threshold", so the LOD test would
// never select the mesh and it would not draw at all (docs/canyon/PHASE_2.md
// found a generated mesa cap invisible this way).

import (
	"math"
	"testing"
)

func TestDeviationIgnoresDegenerateTriangles(t *testing.T) {
	// A unit square as two triangles (and a third over its middle point),
	// against the square plus a sliver with two corners at one point: the
	// middle point's distance to the sliver is 0/0 in the closest-point
	// test's edge case.
	m := Mesh{Stride: 3, Positions: []float32{0, 0, 0, 1, 0, 0, 1, 1, 0, 0, 1, 0, 0.5, 0.5, 0}}
	orig := []uint32{0, 1, 2, 0, 2, 3, 4, 1, 2}
	simp := []uint32{0, 1, 2, 0, 2, 3, 0, 0, 1}
	if d := pointTriDist2([3]float64{0.5, 0.5, 0}, [3]float64{}, [3]float64{}, [3]float64{1, 0, 0}); !math.IsNaN(d) {
		t.Logf("the sliver's distance is %g (the test no longer reaches the 0/0 case)", d)
	}
	if d := deviation(&m, orig, simp); math.IsNaN(float64(d)) || d > 1e-6 {
		t.Fatalf("deviation %g, want 0", d)
	}
}
