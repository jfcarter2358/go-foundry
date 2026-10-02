package world

// static_lod_seam_test.go — Phase 2 of the Nanite plan: where two compiled
// draw groups meet (two chunks' floors, or two materials), each group's DAG
// keeps every corner the other one has there, at every level, so no cut of
// either can open a gap or a T-junction along the seam.

import (
	"math"
	"testing"

	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// seamFloor is a bumpy grid floor of n×n quads over [-half, half]², each quad
// a surface of its own, the half at x < 0 in chunk "a" and the rest in "b".
func seamFloor(t *testing.T, n int, half float32) *StaticGeometry {
	t.Helper()
	height := func(x, z float32) float32 {
		return 0.4*float32(math.Sin(float64(x)*0.7)*math.Cos(float64(z)*0.5)) + 0.15*float32(math.Sin(float64(x*z)*0.3))
	}
	s := &StaticGeometry{}
	step := 2 * half / float32(n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			x0, z0 := -half+float32(i)*step, -half+float32(j)*step
			x1, z1 := x0+step, z0+step
			if i == n/2 {
				x0 = 0 // bit-exact on the seam
			}
			if i+1 == n/2 {
				x1 = 0
			}
			verts := [][]float32{
				{x0, height(x0, z0), z0, x0, z0}, {x0, height(x0, z1), z1, x0, z1},
				{x1, height(x1, z1), z1, x1, z1}, {x1, height(x1, z0), z0, x1, z0},
			}
			var g gfx.Geometry
			g.CPUOnly = true
			if err := g.Load(verts, []int{0, 1, 2, 0, 2, 3}, "concrete", mgl32.Vec3{}); err != nil {
				t.Fatal(err)
			}
			g.Alpha = 1
			chunk := "a"
			if i >= n/2 {
				chunk = "b"
			}
			s.Surfaces = append(s.Surfaces, StaticSurface{Geometry: g, Chunk: chunk, Normal: mgl32.Vec3{0, 1, 0},
				Lo: mgl32.Vec3{x0, -1, z0}, Hi: mgl32.Vec3{x1, 1, z1}})
		}
	}
	return s
}

func TestStaticLODKeepsTheSeamBetweenGroups(t *testing.T) {
	prev := StaticLOD
	StaticLOD = true
	t.Cleanup(func() { StaticLOD = prev })

	s := seamFloor(t, 40, 20)
	groups, _ := StaticGroups(s)
	if len(groups) != 2 {
		t.Fatalf("%d groups, want the two chunks'", len(groups))
	}
	built := BuildStaticClusters(s)
	// The seam's corners, as every level-0 triangle has them.
	full := map[float32]bool{}
	for gi := range groups {
		pos := StaticGroupPositions(s, &groups[gi])
		for c := 0; c+5 <= len(pos); c += 5 {
			if pos[c] == 0 {
				full[pos[c+2]] = true
			}
		}
	}
	for gi := range groups {
		d := built[gi].DAG
		if d == nil || d.Levels < 3 {
			t.Fatalf("group %d got no DAG worth the name", gi)
		}
		pos := StaticGroupPositions(s, &groups[gi])
		// Every cut, the coarsest included: what any eye could draw.
		for _, thr := range []float32{0, 0.01, 0.05, 0.2, 1, float32(math.Inf(1))} {
			got := map[float32]bool{}
			tris := 0
			for ci := range d.Clusters {
				c := &d.Clusters[ci]
				if !c.Selected(thr) {
					continue
				}
				tris += int(c.TriCount)
				for _, ix := range d.Indices[3*c.TriFirst : 3*(c.TriFirst+c.TriCount)] {
					if pos[5*ix] == 0 {
						got[pos[5*ix+2]] = true
					}
				}
			}
			if len(got) != len(full) {
				t.Errorf("group %d (chunk %s), threshold %g: the cut keeps %d of the seam's %d corners (%d triangles)",
					gi, groups[gi].Key.Chunk, thr, len(got), len(full), tris)
			}
		}
	}
}
