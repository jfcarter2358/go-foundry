// Package meshsdf bakes a triangle mesh into a small signed distance grid —
// what live GI traces a model against (render/gi_sources.go), and what a Nanite
// mesh carries baked in its cooked file (internal/nanite) so the runtime never
// has to.
//
// It was render/gi_meshsdf.go until the cooker needed it too; the algorithm is
// unchanged.
//
// The bake is narrow-band: each triangle writes the true distance into the
// voxels within Band cells of it, which is all the composite keeps anyway —
// further than that, the field only needs to say "far". The sign comes from a
// flood: every voxel reachable from the grid's border without crossing the
// surface's shell is outside, and every one the flood could not reach is
// inside. A mesh with a hole in it floods through the hole and comes out all
// "outside", which is the safe failure: a shell, not a solid.
package meshsdf

import (
	"math"
	"runtime"
	"sync"

	"github.com/go-gl/mathgl/mgl32"
)

// Band is how many cells out from the surface a bake writes the true
// distance; beyond it a voxel holds the band.
const Band = 4

// Grid is one model's baked distance grid, in its own local space.
type Grid struct {
	// N is the cells on a side. Voxel (x,y,z)'s centre is at
	// Lo + (x,y,z)*Cell in the model's local space.
	N    int
	Lo   mgl32.Vec3
	Cell float32
	// Data holds the signed distance at each voxel centre in cells, x fastest.
	Data []float32
	// Hi is the local-space corner opposite Lo, for the source's bounds.
	Hi mgl32.Vec3
}

// Bake builds the grid for a triangle list (three points a triangle).
// nil for an empty or degenerate mesh.
func Bake(tris []mgl32.Vec3, n int) *Grid {
	if len(tris) < 3 || n < 8 {
		return nil
	}
	lo, hi := tris[0], tris[0]
	for _, p := range tris[1:] {
		lo = mgl32.Vec3{min(lo[0], p[0]), min(lo[1], p[1]), min(lo[2], p[2])}
		hi = mgl32.Vec3{max(hi[0], p[0]), max(hi[1], p[1]), max(hi[2], p[2])}
	}
	ext := max(hi[0]-lo[0], max(hi[1]-lo[1], hi[2]-lo[2]))
	if ext <= 1e-6 {
		return nil
	}
	// Two cells of air on every side, so the flood can get round the model
	// and the shell never touches the grid's edge.
	cell := ext / float32(n-5)
	centre := lo.Add(hi).Mul(0.5)
	half := float32(n-1) * 0.5 * cell
	g := &Grid{N: n, Cell: cell}
	g.Lo = centre.Sub(mgl32.Vec3{half, half, half})
	g.Hi = centre.Add(mgl32.Vec3{half, half, half})

	total := n * n * n
	dist := make([]float32, total)
	for i := range dist {
		dist[i] = math.MaxFloat32
	}
	idx := func(x, y, z int) int { return (z*n+y)*n + x }
	toGrid := func(p mgl32.Vec3) mgl32.Vec3 { return p.Sub(g.Lo).Mul(1 / cell) }

	// Each triangle writes the band round it. A dense mesh is many triangles
	// to a cell, so the triangles are shared out across the cores, each
	// worker into its own grid, and the grids merged by minimum — which is
	// order-free, so the answer is the same however the work was split.
	splat := func(dst []float32, from, to int) {
		for t := from; t+2 < to; t += 3 {
			a, b, c := tris[t], tris[t+1], tris[t+2]
			ga, gb, gc := toGrid(a), toGrid(b), toGrid(c)
			var bmin, bmax [3]int
			for k := 0; k < 3; k++ {
				bmin[k] = max(int(math.Floor(float64(min(ga[k], gb[k], gc[k])))-Band), 0)
				bmax[k] = min(int(math.Ceil(float64(max(ga[k], gb[k], gc[k])))+Band), n-1)
			}
			for z := bmin[2]; z <= bmax[2]; z++ {
				for y := bmin[1]; y <= bmax[1]; y++ {
					for x := bmin[0]; x <= bmax[0]; x++ {
						p := g.Lo.Add(mgl32.Vec3{float32(x), float32(y), float32(z)}.Mul(cell))
						d := PointTriangleDistance(p, a, b, c) / cell
						if i := idx(x, y, z); d < dst[i] {
							dst[i] = d
						}
					}
				}
			}
		}
	}
	ntri := len(tris) / 3
	workers := min(runtime.GOMAXPROCS(0), ntri/1024)
	if workers <= 1 {
		splat(dist, 0, ntri*3)
	} else {
		grids := make([][]float32, workers)
		var wg sync.WaitGroup
		per := (ntri + workers - 1) / workers
		for w := 0; w < workers; w++ {
			grids[w] = make([]float32, total)
			for i := range grids[w] {
				grids[w][i] = math.MaxFloat32
			}
			from, to := w*per*3, min((w+1)*per, ntri)*3
			wg.Add(1)
			go func(dst []float32) {
				defer wg.Done()
				splat(dst, from, to)
			}(grids[w])
		}
		wg.Wait()
		for _, gr := range grids {
			for i, d := range gr {
				if d < dist[i] {
					dist[i] = d
				}
			}
		}
	}

	// The flood: out from every border voxel, through every voxel the shell
	// does not touch. A voxel within a cell of a triangle is the shell.
	const shell = 0.87 // half a cell's diagonal: the surface passes through it
	outside := make([]bool, total)
	queue := make([]int32, 0, total/4)
	push := func(x, y, z int) {
		i := idx(x, y, z)
		if outside[i] || dist[i] <= shell {
			return
		}
		outside[i] = true
		queue = append(queue, int32(i))
	}
	for a := 0; a < n; a++ {
		for b := 0; b < n; b++ {
			push(a, b, 0)
			push(a, b, n-1)
			push(a, 0, b)
			push(a, n-1, b)
			push(0, a, b)
			push(n-1, a, b)
		}
	}
	for len(queue) > 0 {
		i := int(queue[len(queue)-1])
		queue = queue[:len(queue)-1]
		x, y, z := i%n, (i/n)%n, i/(n*n)
		if x > 0 {
			push(x-1, y, z)
		}
		if x < n-1 {
			push(x+1, y, z)
		}
		if y > 0 {
			push(x, y-1, z)
		}
		if y < n-1 {
			push(x, y+1, z)
		}
		if z > 0 {
			push(x, y, z-1)
		}
		if z < n-1 {
			push(x, y, z+1)
		}
	}

	g.Data = make([]float32, total)
	for i, d := range dist {
		d = min(d, Band)
		if !outside[i] && d > shell {
			d = -d // walled in by the shell: inside the solid
		}
		g.Data[i] = d
	}
	return g
}

// PointTriangleDistance is the distance from p to triangle abc (Ericson,
// Real-Time Collision Detection, 5.1.5).
func PointTriangleDistance(p, a, b, c mgl32.Vec3) float32 {
	ab, ac, ap := b.Sub(a), c.Sub(a), p.Sub(a)
	d1, d2 := ab.Dot(ap), ac.Dot(ap)
	if d1 <= 0 && d2 <= 0 {
		return ap.Len()
	}
	bp := p.Sub(b)
	d3, d4 := ab.Dot(bp), ac.Dot(bp)
	if d3 >= 0 && d4 <= d3 {
		return bp.Len()
	}
	vc := d1*d4 - d3*d2
	if vc <= 0 && d1 >= 0 && d3 <= 0 {
		v := d1 / (d1 - d3)
		return p.Sub(a.Add(ab.Mul(v))).Len()
	}
	cp := p.Sub(c)
	d5, d6 := ab.Dot(cp), ac.Dot(cp)
	if d6 >= 0 && d5 <= d6 {
		return cp.Len()
	}
	vb := d5*d2 - d1*d6
	if vb <= 0 && d2 >= 0 && d6 <= 0 {
		w := d2 / (d2 - d6)
		return p.Sub(a.Add(ac.Mul(w))).Len()
	}
	va := d3*d6 - d5*d4
	if va <= 0 && (d4-d3) >= 0 && (d5-d6) >= 0 {
		w := (d4 - d3) / ((d4 - d3) + (d5 - d6))
		return p.Sub(b.Add(c.Sub(b).Mul(w))).Len()
	}
	denom := 1 / (va + vb + vc)
	v, w := vb*denom, vc*denom
	return p.Sub(a.Add(ab.Mul(v)).Add(ac.Mul(w))).Len()
}
