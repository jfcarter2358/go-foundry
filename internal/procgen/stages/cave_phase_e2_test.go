package stages

import (
	"math"
	"testing"

	"foundry/internal/procgen/sdf"
)

// TestCaveDropsFloatingRock: of a section's raw mesh, a piece closed
// inside the box (a fin left hanging in the air) is dropped and a piece the
// box clips (rock that runs on into the next section) is kept.
func TestCaveDropsFloatingRock(t *testing.T) {
	sec := caveSection{Lo: sdf.Vec3{0, 0, 0}, Hi: sdf.Vec3{8, 8, 8}}
	const cell = 0.25
	lo, hi := sec.Lo, sec.Hi
	box := func(q sdf.Vec3) float64 {
		return math.Max(math.Max(lo[0]-q[0], q[0]-hi[0]), math.Max(math.Max(lo[1]-q[1], q[1]-hi[1]), math.Max(lo[2]-q[2], q[2]-hi[2])))
	}
	// A floating ball, and a floor slab clipped by the box's sides.
	ball := sdf.Sphere(sdf.Vec3{4, 6, 4}, 0.9)
	slab := func(q sdf.Vec3) float64 { return q[1] - 1.5 }
	shape := sdf.Shape{
		F: func(q sdf.Vec3) float64 {
			return math.Max(math.Min(ball.F(q), slab(q)), box(q))
		},
		Box: sdf.Box{Lo: lo, Hi: hi},
		Lip: 1,
	}
	m, _, err := sdf.Polygonize(shape, sdf.MeshOptions{Cell: cell, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	before := m.TriangleCount()
	if n := dropFloatingCave(m, sec, cell); n != 1 {
		t.Fatalf("dropped %d pieces, want 1 (the ball)", n)
	}
	after := m.TriangleCount()
	if after == 0 || after >= before {
		t.Fatalf("%d → %d triangles: want the slab kept and the ball gone", before, after)
	}
	for _, ix := range m.Indices {
		if q := m.Positions[ix]; q[1] > 3 {
			t.Fatalf("a vertex of the ball survived at %v", q)
		}
	}
}

// TestPileDomeStacks: a piece rests on the dome of one placed before it,
// highest over its centre and not at all past its rim.
func TestPileDomeStacks(t *testing.T) {
	d := pileDome{x: 0, z: 0, y: 1, r: 2, h: 1}
	if got := d.at(0, 0); math.Abs(got-2) > 1e-9 {
		t.Errorf("over the centre %g, want 2", got)
	}
	if got := d.at(1, 0); got <= 1 || got >= 2 {
		t.Errorf("half way out %g, want between the foot and the top", got)
	}
	if got := d.at(3, 0); !math.IsInf(got, -1) {
		t.Errorf("past the rim %g, want none", got)
	}
}
