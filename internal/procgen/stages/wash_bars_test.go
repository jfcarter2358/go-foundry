package stages

import (
	"context"
	"math"
	"testing"

	"foundry/internal/procgen/terrain"
)

// Sand bars are drawn out along the flow: on a plane falling toward +X,
// the bar field changes far more slowly down the slope than across it, and
// it stays off ground that is not a wash.
func TestWashBarsRunAlongTheFlow(t *testing.T) {
	const n = 256
	g := &terrain.Grid{N: n, Cell: 0.5, MinX: -64, MinZ: -64, H: make([]float32, (n+1)*(n+1))}
	flow := make([]float32, len(g.H))
	sz := g.S()
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			g.H[j*sz+i] = float32(-0.05 * g.X(i))
			if j < sz/2 {
				flow[j*sz+i] = 1 // the north half is a wash
			}
		}
	}
	bars, err := washBars(g, flow, 0.1, 28, 4, 1, 7, terrain.NewPool(context.Background(), 4))
	if err != nil {
		t.Fatal(err)
	}
	// The mean squared step between samples 2 m apart, along X and along Z,
	// over the wash's interior.
	var dx, dz, cnt float64
	for j := 8; j < sz/2-8; j++ {
		for i := 8; i < sz-8; i++ {
			a := float64(bars[j*sz+i])
			dx += math.Pow(float64(bars[j*sz+i+4])-a, 2)
			dz += math.Pow(float64(bars[(j+4)*sz+i])-a, 2)
			cnt++
		}
	}
	dx, dz = dx/cnt, dz/cnt
	if !(dz > 3*dx) {
		t.Errorf("bars are not drawn out along the flow: step² along %.4f, across %.4f", dx, dz)
	}
	var on, off float64
	for k, v := range bars {
		if k/sz < sz/2 {
			on += float64(v)
		} else {
			off += float64(v)
		}
	}
	if off != 0 || on == 0 {
		t.Errorf("bars off the wash %.1f, on it %.1f", off, on)
	}
}
