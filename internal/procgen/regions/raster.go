package regions

// raster.go — the weights sampled over a grid.
//
// A Grid is a world-aligned lattice of samples: sample (i, j) sits at the
// CENTRE of cell (i, j), MinX + (i + 0.5)·Cell, MinZ + (j + 0.5)·Cell, the
// way an image pixel covers its square (procgen.ImageLayer's convention), so
// a raster drawn over Grid.Rect lines up with the world.
//
// Raster keeps one dense float32 plane per region. That is the simple form
// the preview overlay and the per-environment weight masks want; phase 2's
// union grid at 0.5 m will want a sparse form (a few regions per sample) and
// can sample Weights itself per tile, which is what makes Weights pure.

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
)

// Grid is a lattice of W×H samples, Cell metres apart, from (MinX, MinZ).
type Grid struct {
	MinX, MinZ, Cell float64
	W, H             int
}

// Rect is the area the grid's cells cover.
func (g Grid) Rect() Rect {
	return Rect{g.MinX, g.MinZ, g.MinX + float64(g.W)*g.Cell, g.MinZ + float64(g.H)*g.Cell}
}

// At is sample (i, j)'s world position.
func (g Grid) At(i, j int) (x, z float64) {
	return g.MinX + (float64(i)+0.5)*g.Cell, g.MinZ + (float64(j)+0.5)*g.Cell
}

// GridOver is a grid covering r with cells of at most maxCell metres and at
// most maxSide samples along the longer side (the cell grows to fit).
func GridOver(r Rect, maxCell float64, maxSide int) Grid {
	w, h := r.MaxX-r.MinX, r.MaxZ-r.MinZ
	cell := maxCell
	if long := max(w, h); maxSide > 0 && long/cell > float64(maxSide) {
		cell = long / float64(maxSide)
	}
	if cell <= 0 {
		cell = 1
	}
	return Grid{MinX: r.MinX, MinZ: r.MinZ, Cell: cell,
		W: max(1, int(w/cell+0.999999)), H: max(1, int(h/cell+0.999999))}
}

// Raster is every region's weight at every sample of a grid.
type Raster struct {
	Grid Grid
	// Planes[r][j*W+i] is region r's weight at sample (i, j).
	Planes [][]float32
}

// At is region r's weight at sample (i, j).
func (r *Raster) At(region, i, j int) float32 { return r.Planes[region][j*r.Grid.W+i] }

// Raster samples the set over g, rows in parallel on up to workers
// goroutines (0: GOMAXPROCS). ctx may be nil; a cancelled one returns its
// error.
func (s *Set) Raster(ctx context.Context, g Grid, workers int) (*Raster, error) {
	out := &Raster{Grid: g, Planes: make([][]float32, len(s.Regions))}
	for i := range out.Planes {
		out.Planes[i] = make([]float32, g.W*g.H)
	}
	if len(s.Regions) == 0 || g.W <= 0 || g.H <= 0 {
		return out, nil
	}
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	var cancelled atomic.Bool
	for w := 0; w < min(workers, g.H); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var buf []W
			for {
				j := int(next.Add(1) - 1)
				if j >= g.H {
					return
				}
				if ctx != nil && ctx.Err() != nil {
					cancelled.Store(true)
					return
				}
				for i := 0; i < g.W; i++ {
					x, z := g.At(i, j)
					buf = s.Weights(x, z, buf)
					for _, wt := range buf {
						out.Planes[wt.Region][j*g.W+i] = float32(wt.Weight)
					}
				}
			}
		}()
	}
	wg.Wait()
	if cancelled.Load() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("cancelled")
	}
	return out, nil
}
