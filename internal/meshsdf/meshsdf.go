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
	"sync/atomic"

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

// parallelBake splits a bake's triangles across the cores; off, the same
// work runs on one, which a test compares it against.
var parallelBake = true

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

	// Each triangle writes the band round it: the true distance into every
	// voxel within Band cells of it, the smaller where two triangles' bands
	// meet. A dense mesh is many triangles to a cell, so the grid is cut into
	// slabs of z and the slabs shared out across the cores: every triangle
	// is listed under each slab its band reaches, and a worker that takes a
	// slab writes that slab's voxels from its list, straight into the grid —
	// no voxel is any other worker's, so there is nothing to merge, and a
	// voxel's value is the minimum over the same triangles however the slabs
	// were dealt out (a minimum does not care about order). The workers take
	// slabs as they free up, so a slab through the dense middle of a model
	// holds no one up.
	ntri := len(tris) / 3
	boxes := make([]triBox, ntri)
	work := 0 // voxels the boxes hold: what the splat walks, at most
	for t := range boxes {
		tb := newTriBox(tris[3*t], tris[3*t+1], tris[3*t+2], g.Lo, cell, n)
		boxes[t] = tb
		work += (tb.hi[0] - tb.lo[0] + 1) * (tb.hi[1] - tb.lo[1] + 1) * (tb.hi[2] - tb.lo[2] + 1)
	}
	// Shared out only when there is enough of it to be worth a goroutine
	// a core — by voxels, not triangles: a dozen triangles the size of the
	// model are as much work as thousands of small ones.
	workers := min(runtime.GOMAXPROCS(0), work/(1<<17), n)
	if !parallelBake || workers <= 1 {
		for t := range boxes {
			splatTriangle(dist, &boxes[t], tris[3*t:3*t+3], g.Lo, cell, n, boxes[t].lo[2], boxes[t].hi[2])
		}
	} else {
		// The slabs' lists, counted first so they share one allocation.
		count := make([]int32, n+1)
		for t := range boxes {
			for z := boxes[t].lo[2]; z <= boxes[t].hi[2]; z++ {
				count[z+1]++
			}
		}
		for z := 1; z <= n; z++ {
			count[z] += count[z-1]
		}
		list := make([]int32, count[n])
		fill := append([]int32(nil), count[:n]...)
		for t := range boxes {
			for z := boxes[t].lo[2]; z <= boxes[t].hi[2]; z++ {
				list[fill[z]] = int32(t)
				fill[z]++
			}
		}
		var next atomic.Int32
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					z := int(next.Add(1)) - 1
					if z >= n {
						return
					}
					for _, t := range list[count[z]:count[z+1]] {
						splatTriangle(dist, &boxes[t], tris[3*t:3*t+3], g.Lo, cell, n, z, z)
					}
				}
			}()
		}
		wg.Wait()
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

// triBox is what the splat needs of a triangle besides its corners: the
// voxels its band can reach, and two cheap lower bounds on its distance — its
// bounding sphere and its plane — that rule most of those voxels out before
// the exact distance is worked out. (A small triangle's box is 9³ voxels, and
// its band only the ball of radius 4 in the middle of them: under 40%.)
type triBox struct {
	lo, hi [3]int // the voxel box: the triangle's, Band cells wider
	centre mgl32.Vec3
	// reach2 is the bounding sphere's radius plus the band, squared: a voxel
	// further than that from centre is out of the band.
	reach2 float32
	// normal is the unit normal, zero for a degenerate triangle.
	normal mgl32.Vec3
	// a is a corner, the point the plane test measures from.
	a mgl32.Vec3
}

// bandSlack is how far past the band a lower bound must put a voxel before it
// is skipped. A voxel the exact distance puts past the band ends as Band
// whether or not the triangle is counted (the band clamp below; the shell
// test reads only distances under a cell), so skipping it changes no bit —
// provided the bound, worked out in float32 like the distance, cannot wrongly
// exceed the band by its own rounding. A thousandth of the band is orders of
// magnitude more than that rounding.
const bandSlack = 1.001

func newTriBox(a, b, c mgl32.Vec3, lo mgl32.Vec3, cell float32, n int) triBox {
	toGrid := func(p mgl32.Vec3) mgl32.Vec3 { return p.Sub(lo).Mul(1 / cell) }
	ga, gb, gc := toGrid(a), toGrid(b), toGrid(c)
	var tb triBox
	for k := 0; k < 3; k++ {
		tb.lo[k] = max(int(math.Floor(float64(min(ga[k], gb[k], gc[k])))-Band), 0)
		tb.hi[k] = min(int(math.Ceil(float64(max(ga[k], gb[k], gc[k])))+Band), n-1)
	}
	tb.centre = mgl32.Vec3{
		(min(a[0], b[0], c[0]) + max(a[0], b[0], c[0])) * 0.5,
		(min(a[1], b[1], c[1]) + max(a[1], b[1], c[1])) * 0.5,
		(min(a[2], b[2], c[2]) + max(a[2], b[2], c[2])) * 0.5,
	}
	r := max(a.Sub(tb.centre).Len(), b.Sub(tb.centre).Len(), c.Sub(tb.centre).Len())
	reach := r + Band*cell*bandSlack
	tb.reach2 = reach * reach
	if nrm := b.Sub(a).Cross(c.Sub(a)); nrm.Len() > 1e-12 {
		tb.normal = nrm.Normalize()
	}
	tb.a = a
	return tb
}

// rowSpan narrows the voxels x0..x1 of the row at (y, z) to those the two
// lower bounds leave in play, a voxel wider each side than they say: the
// tests in splatTriangle still decide each voxel, so this only saves walking
// ones they would all have turned away. A triangle the size of the model has
// a box the size of the grid and a band a few voxels thick through it, and
// without this it paid a distance for every voxel of the box. Worked out in
// float64, with the band a thousandth of a cell wider again, so no voxel the
// tests would keep can fall outside it by rounding.
func (tb *triBox) rowSpan(lo mgl32.Vec3, cell float32, y, z, x0, x1 int) (int, int) {
	c := float64(cell)
	band := float64(Band)*c*bandSlack + 1e-3*c
	py := float64(lo[1]) + float64(y)*c
	pz := float64(lo[2]) + float64(z)*c
	px0 := float64(lo[0])
	// The sphere: (px - cx)^2 <= reach^2 - dy^2 - dz^2.
	dy, dz := py-float64(tb.centre[1]), pz-float64(tb.centre[2])
	reach := math.Sqrt(float64(tb.reach2)) + 1e-3*c
	left := reach*reach - dy*dy - dz*dz
	if left < 0 {
		return 1, 0
	}
	half := math.Sqrt(left)
	cx := float64(tb.centre[0])
	x0 = max(x0, int(math.Floor((cx-half-px0)/c))-1)
	x1 = min(x1, int(math.Ceil((cx+half-px0)/c))+1)
	// The plane: |n . (p - a)| <= band, linear in x along the row.
	nx, ny, nz := float64(tb.normal[0]), float64(tb.normal[1]), float64(tb.normal[2])
	if nx == 0 && ny == 0 && nz == 0 {
		return x0, x1
	}
	base := nx*(px0-float64(tb.a[0])) + ny*(py-float64(tb.a[1])) + nz*(pz-float64(tb.a[2]))
	slope := nx * c
	if math.Abs(slope) < 1e-9 {
		if math.Abs(base) > band {
			return 1, 0
		}
		return x0, x1
	}
	ta, tb2 := (-band-base)/slope, (band-base)/slope
	if ta > tb2 {
		ta, tb2 = tb2, ta
	}
	x0 = max(x0, int(math.Floor(ta))-1)
	x1 = min(x1, int(math.Ceil(tb2))+1)
	return x0, x1
}

// splatTriangle writes triangle abc's band into dst's voxels whose z is in
// [z0, z1] (clipped to the triangle's own box): the exact distance, in cells,
// where it is less than what the voxel holds.
func splatTriangle(dst []float32, tb *triBox, abc []mgl32.Vec3, lo mgl32.Vec3, cell float32, n, z0, z1 int) {
	a, b, c := abc[0], abc[1], abc[2]
	band := Band * cell * bandSlack
	for z := max(z0, tb.lo[2]); z <= min(z1, tb.hi[2]); z++ {
		for y := tb.lo[1]; y <= tb.hi[1]; y++ {
			x0, x1 := tb.rowSpan(lo, cell, y, z, tb.lo[0], tb.hi[0])
			for x := x0; x <= x1; x++ {
				p := lo.Add(mgl32.Vec3{float32(x), float32(y), float32(z)}.Mul(cell))
				if p.Sub(tb.centre).LenSqr() > tb.reach2 {
					continue
				}
				// Off the triangle's plane by more than the band. Measured
				// from a corner, so the rounding is the size of the band and
				// not of the model's coordinates.
				if pd := tb.normal.Dot(p.Sub(a)); pd > band || pd < -band {
					continue
				}
				d := PointTriangleDistance(p, a, b, c) / cell
				if i := (z*n+y)*n + x; d < dst[i] {
					dst[i] = d
				}
			}
		}
	}
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
