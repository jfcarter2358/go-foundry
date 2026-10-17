package stages

// ground_edit_river_flow_test.go — the ribbon's packed flow runs along the
// river (fixes after W5, docs/terrain-paint/PHASE_W5.md): the green/blue
// vertex colour the water shader reads as the current decodes to the
// river's own downstream heading, whichever way the river runs and
// whichever way its spline was drawn, and the ribbon's normal leans the
// way the surface falls.

import (
	"math"
	"testing"

	"foundry/internal/procgen/terrain"
)

// flowValley is a 128 m valley along X falling 5 cm/m toward +X (dir 1) or
// toward −X (dir −1).
func flowValley(dir float64) *terrain.Grid {
	g := terrain.NewGrid(128, 1)
	s := g.S()
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			x, z := g.X(i), g.Z(j)
			g.H[j*s+i] = float32(10 - 0.05*dir*x + 0.08*math.Abs(z) + 0.05*math.Sin(x*0.7)*math.Cos(z*0.9))
		}
	}
	return g
}

// decodeFlow is what water.glsl's waterVertexFlow makes of a ribbon
// vertex colour (flow_map 1): m/s along X and Z.
func decodeFlow(c [4]uint8) (float64, float64) {
	return (float64(c[1])/255*2 - 1) * terrain.RiverFlowMax, (float64(c[2])/255*2 - 1) * terrain.RiverFlowMax
}

func TestRibbonFlowRunsAlongTheRiver(t *testing.T) {
	for _, tc := range []struct {
		name string
		dir  float64 // which way the valley falls along X
		pts  [][2]float64
	}{
		{"west to east, drawn downstream", 1, [][2]float64{{-55, 0}, {-20, 6}, {15, -6}, {55, 0}}},
		{"west to east, drawn upstream", 1, [][2]float64{{55, 0}, {15, -6}, {-20, 6}, {-55, 0}}},
		{"east to west, drawn downstream", -1, [][2]float64{{55, 0}, {15, -6}, {-20, 6}, {-55, 0}}},
		{"east to west, drawn upstream", -1, [][2]float64{{-55, 0}, {-20, 6}, {15, -6}, {55, 0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := flowValley(tc.dir)
			w := make([]float64, len(tc.pts))
			d := make([]float64, len(tc.pts))
			for i := range w {
				w[i], d[i] = 5, 1
			}
			sp := terrain.RiverSpec{Name: "r", Points: tc.pts, Width: w, Depth: d, Shape: terrain.DefaultRiverShape(), Seed: 3}
			res := terrain.CarveRivers(g, []terrain.RiverSpec{sp}, nil, math.Inf(-1))
			if len(res.Rivers) != 1 {
				t.Fatalf("%d rivers", len(res.Rivers))
			}
			st := res.Rivers[0].Stations
			if first, last := st[0], st[len(st)-1]; (last.X-first.X)*tc.dir < 0 {
				t.Fatalf("the river runs from x %.1f to %.1f, uphill", first.X, last.X)
			}
			rv := &groundRivers{res: res}
			s := g.S()
			checked, along := 0, 0
			for k, smp := range res.Samples {
				if !smp.Wet {
					continue
				}
				i, j := int(k)%s, int(k)/s
				x, z := g.X(i), g.Z(j)
				// The nearest station's heading: the spline's tangent there.
				best, bi := math.Inf(1), 0
				for si := range st {
					if dd := math.Hypot(st[si].X-x, st[si].Z-z); dd < best {
						best, bi = dd, si
					}
				}
				fx, fz := decodeFlow(riverVertexColor(smp))
				sp := math.Hypot(fx, fz)
				if sp < 0.15 {
					continue
				}
				checked++
				cos := (fx*st[bi].TX + fz*st[bi].TZ) / sp
				if cos > 0.97 {
					along++
				} else if cos < 0.9 {
					t.Errorf("sample (%.0f, %.0f): flow (%.2f, %.2f) against the heading (%.2f, %.2f): cos %.2f",
						x, z, fx, fz, st[bi].TX, st[bi].TZ, cos)
					return
				}
				// Downstream is downhill: the flow's X goes the way the valley falls.
				if fx*tc.dir <= 0 {
					t.Errorf("sample (%.0f, %.0f): flow X %.2f, the valley falls toward %+.0f", x, z, fx, tc.dir)
					return
				}
				// The ribbon's normal leans downstream (it faces the way the
				// surface falls), or stands straight where the surface is level.
				n := riverSurfaceNormal(rv, s, float32(g.Cell), i, j)
				if n.Y() < 0.9 || float64(n.X())*tc.dir < -1e-3 {
					t.Errorf("sample (%.0f, %.0f): ribbon normal %v on a river falling toward %+.0f", x, z, n, tc.dir)
					return
				}
			}
			if checked < 100 || along < checked*9/10 {
				t.Errorf("%d of %d flowing samples along their station's heading", along, checked)
			}
		})
	}
}
