package wallsheet

import (
	"context"
	"math"
	"testing"

	"foundry/internal/procgen/sdf"
	"foundry/internal/procgen/terrain"
)

// cliffGrid is 64 m of ground with a 24 m cliff along X = 0 (steep over
// about 5 m), a dune-sized bank at X = -20 (2 m, as steep), flat elsewhere.
func cliffGrid() *terrain.Grid {
	g := terrain.NewGrid(128, 0.5)
	sz := g.S()
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			x := g.X(i)
			h := 24 / (1 + math.Exp(-x/1.2))
			h += 2 / (1 + math.Exp(-(x+20)/0.15))
			g.H[j*sz+i] = float32(h)
		}
	}
	return g
}

func TestWallShellStandsInFrontOfTheTerrain(t *testing.T) {
	g := cliffGrid()
	s := Defaults()
	s.Seed = 9
	f, err := NewField(g, s, terrain.NewPool(context.Background(), 4))
	if err != nil {
		t.Fatal(err)
	}
	if f.Steep(0, 0) < 0.9 || f.Steep(15, 0) > 0.01 {
		t.Errorf("steep mask: %.2f on the cliff, %.2f on the top", f.Steep(0, 0), f.Steep(15, 0))
	}
	if f.Steep(-20, 0) > 0.01 {
		t.Errorf("a 2 m bank (under min_wall) is wall: %.2f", f.Steep(-20, 0))
	}
	secs := f.Sections()
	if len(secs) == 0 {
		t.Fatal("no sections")
	}
	for _, sec := range secs {
		if sec.Lo[0] > 0 || sec.Hi[0] < 0 {
			t.Errorf("a section away from the cliff: %v..%v", sec.Lo, sec.Hi)
		}
	}
	const cell = 0.5
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, sec := range secs {
		m, _, err := sdf.Polygonize(f.Shape(sec, cell), sdf.MeshOptions{Cell: cell, Sharpness: 0.35, Workers: 2})
		if err != nil {
			t.Fatal(err)
		}
		if m.TriangleCount() < 1000 {
			t.Fatalf("%d triangles", m.TriangleCount())
		}
		// The face's stand-off from the terrain varies between ledge and
		// recess. (Vertices more than Hidden/2 under the terrain are the
		// buried rock — the band's sides inside the mesa, the shell under
		// the floor — which the stage drops; the rest is the face.)
		for _, p := range m.Positions {
			if p[0]-sec.Lo[0] < 2*cell || sec.Hi[0]-p[0] < 2*cell || p[2]-sec.Lo[2] < 2*cell || sec.Hi[2]-p[2] < 2*cell || p[1]-sec.Lo[1] < 2*cell {
				continue
			}
			h := float64(g.Sample(p[0], p[2]))
			gx := float64(g.Sample(p[0]+0.5, p[2])-g.Sample(p[0]-0.5, p[2])) / 1
			d := (p[1] - h) / math.Sqrt(1+gx*gx)
			if d < -0.5*Hidden {
				continue
			}
			if f.Steep(p[0], p[2]) > 0.95 {
				lo, hi = math.Min(lo, d), math.Max(hi, d)
			}
		}
		// Nothing stands over the cliff's top: no parapet along the rim.
		for _, p := range m.Positions {
			if top := float64(g.Sample(p[0]+3, p[2])); p[0] > 1 && p[1] > top+RimLip+cell {
				t.Fatalf("vertex %v stands %.2f m over the top", p, p[1]-top)
			}
		}
	}
	if !(hi-lo > 0.5*s.Depth) {
		t.Errorf("the wall face stands off the terrain by %.2f..%.2f m: no relief", lo, hi)
	}
}
