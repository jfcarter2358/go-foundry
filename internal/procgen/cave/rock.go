package cave

// rock.go — the rock the cave is cut from, the hole it makes in the
// terrain, and how open each point of it is to the sky.
//
// # The rock
//
// The visible geometry is the surface of (the ground's solid) minus (the
// void), near the cave. The ground's solid is the terrain's heightfield as a
// first-order distance (as wallsheet's: exact on a plane, close on walls),
// its top pushed by Offset:
//
//   - away from the holes, Hidden (12 cm) UNDER the terrain, so where the
//     terrain's own tiles still are the rock never shows through them;
//   - around a hole, Lip OVER it, falling to Hidden across Band metres: a
//     rim of rock stands over the hole's ragged edge, so the cut tiles and
//     the cave meet with no crack.
//
// The void is shrunk by Proud (2 cm) for the rock: where a wall_sheet shell
// also meshes the void's walls (it subtracts the same void), the cave's
// surface is the one in front, so the two never z-fight and never part.
//
// # The cut
//
// Cut marks every terrain quad the void (dilated by a margin) passes
// through: its corners, its middle, and — on a cliff, where one quad spans
// tens of metres of height — a column of points up its middle. The stage
// drops those quads from the recooked tiles; HoleDistance is how far each
// sample is from a dropped quad, which Offset reads.
//
// # The sky
//
// A cave far from its mouths is dark because little of the sky can be seen
// from it, not because its rock is near: field AO (sdf's, a couple of metres)
// cannot know that. Sky bakes, on a coarse voxel grid over the cave, the
// share of a hemisphere of rays from each voxel of air that reach the open
// sky — out through a mouth, or straight up a shaft — marching through the
// rock (void-aware) and, past the grid, the terrain. A vertex reads it
// trilinearly a little out from its surface; the stage multiplies it into
// the vertex AO, which scales the ambient light.

import (
	"math"

	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/sdf"
	"foundry/internal/procgen/terrain"
)

// Pool is a worker pool (terrain.Pool; procgen.Env is one).
type Pool = terrain.Pool

// Rock is the rock round a cave.
type Rock struct {
	V *Void
	G *terrain.Grid
	// Hidden is how far under the terrain the rock's top lies away from
	// the holes; Lip how far over it round a hole, falling off over Band.
	Hidden, Lip, Band float64
	// Proud is how far the rock stands into the void (see the file comment).
	Proud float64
	// Hole is each terrain sample's distance (m) to the nearest dropped
	// quad (+Inf away from every hole); nil: no holes.
	Hole []float32
	lipN noise.Fractal
}

// NewRock is the rock round v in the ground g, with the holes hole (nil:
// none).
func NewRock(v *Void, g *terrain.Grid, hole []float32) *Rock {
	return &Rock{V: v, G: g, Hidden: 0.12, Lip: 0.4, Band: 2.2, Proud: 0.02, Hole: hole,
		lipN: noise.Fractal{Seed: noise.Hash64(v.S.Seed, 0x4C49_5000), Octaves: 3, Frequency: 1 / 1.6, Gain: 0.5}}
}

// sample is a per-sample field bilinear at (x, z).
func sample(g *terrain.Grid, f []float32, x, z float64) float64 {
	sz := g.S()
	fx := noise.Clamp((x-g.MinX)/g.Cell, 0, float64(sz-1))
	fz := noise.Clamp((z-g.MinZ)/g.Cell, 0, float64(sz-1))
	i, j := min(int(fx), sz-2), min(int(fz), sz-2)
	u, w := fx-float64(i), fz-float64(j)
	k := j*sz + i
	a := float64(f[k]) + (float64(f[k+1])-float64(f[k]))*u
	b := float64(f[k+sz]) + (float64(f[k+sz+1])-float64(f[k+sz]))*u
	return a + (b-a)*w
}

// Ground is the terrain's first-order signed distance at p (positive above).
func (r *Rock) Ground(p sdf.Vec3) float64 {
	g := r.G
	x, z := p[0], p[2]
	h := sample(g, g.H, x, z)
	e := g.Cell
	gx := (sample(g, g.H, x+e, z) - sample(g, g.H, x-e, z)) / (2 * e)
	gz := (sample(g, g.H, x, z+e) - sample(g, g.H, x, z-e)) / (2 * e)
	return (p[1] - h) / math.Sqrt(1+gx*gx+gz*gz)
}

// HoleDist is how far (x, z) is from a dropped quad (+Inf: far).
func (r *Rock) HoleDist(x, z float64) float64 {
	if r.Hole == nil {
		return math.Inf(1)
	}
	g := r.G
	sz := g.S()
	i := int(math.Round((x - g.MinX) / g.Cell))
	j := int(math.Round((z - g.MinZ) / g.Cell))
	if i < 0 || j < 0 || i >= sz || j >= sz {
		return math.Inf(1)
	}
	if float64(r.Hole[j*sz+i]) > r.Band+2*g.Cell {
		return math.Inf(1)
	}
	return sample(g, r.Hole, x, z)
}

// slope is the ground's slope at (x, z), degrees.
func (r *Rock) slope(x, z float64) float64 {
	g := r.G
	e := g.Cell
	gx := (sample(g, g.H, x+e, z) - sample(g, g.H, x-e, z)) / (2 * e)
	gz := (sample(g, g.H, x, z+e) - sample(g, g.H, x, z-e)) / (2 * e)
	return math.Atan(math.Hypot(gx, gz)) * 180 / math.Pi
}

// Offset is the rock top's offset from the terrain at (x, z) (positive up,
// along the ground's normal).
func (r *Rock) Offset(x, z float64) float64 {
	d := r.HoleDist(x, z)
	if math.IsInf(d, 1) {
		return -r.Hidden
	}
	// A lip is for a cliff's ragged hole. On gentle ground (a mouth's
	// floor, a ledge) the floor follows the ground (Void.Ground) and cuts no
	// hole, and a rubble sill would only be a step to trip on: the rock
	// stays hidden under the ground there.
	w := (1 - noise.SmoothStep(0, r.Band, d)) * noise.SmoothStep(15, 40, r.slope(x, z))
	lip := r.Lip * (0.65 + 0.35*r.lipN.At2(x, z))
	return w*lip - (1-w)*r.Hidden
}

// F is the rock's signed distance at p: negative in the rock.
func (r *Rock) F(p sdf.Vec3) float64 {
	top := r.Ground(p) - r.Offset(p[0], p[2])
	if top > 2 {
		return top
	}
	return math.Max(top, -(r.V.At(p) + r.Proud))
}

// LipBound is F's Lipschitz bound.
func (r *Rock) LipBound() float64 { return math.Max(r.V.Lip, 2) + 1 }

// GroundBelow is g's ground as Void.Ground wants it: at each point the
// lowest ground within radius metres (a min filter, bilinear between
// samples), so the foot of a cliff is the ground all the way to its face.
func GroundBelow(g *terrain.Grid, radius float64) func(x, z float64) float64 {
	sz := g.S()
	r := max(int(math.Round(radius/g.Cell)), 1)
	tmp := make([]float32, sz*sz)
	out := make([]float32, sz*sz)
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			m := g.H[j*sz+i]
			for k := max(i-r, 0); k <= min(i+r, sz-1); k++ {
				m = min(m, g.H[j*sz+k])
			}
			tmp[j*sz+i] = m
		}
	}
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			m := tmp[j*sz+i]
			for k := max(j-r, 0); k <= min(j+r, sz-1); k++ {
				m = min(m, tmp[k*sz+i])
			}
			out[j*sz+i] = m
		}
	}
	return func(x, z float64) float64 { return sample(g, out, x, z) }
}

// ---------- the cut ----------

// Cut marks the terrain's quads the void dilated by margin passes through:
// quad (i, j) spans samples (i, j)..(i+1, j+1), index j·N + i. It also
// returns how many were cut.
func Cut(v *Void, g *terrain.Grid, margin float64, pool Pool) ([]bool, int, error) {
	n := g.N
	cut := make([]bool, n*n)
	if v.Empty() {
		return cut, 0, nil
	}
	box := v.Box()
	i0 := max(int(math.Floor((box.Lo[0]-g.MinX)/g.Cell))-1, 0)
	i1 := min(int(math.Ceil((box.Hi[0]-g.MinX)/g.Cell))+1, n-1)
	j0 := max(int(math.Floor((box.Lo[2]-g.MinZ)/g.Cell))-1, 0)
	j1 := min(int(math.Ceil((box.Hi[2]-g.MinZ)/g.Cell))+1, n-1)
	if i1 < i0 || j1 < j0 {
		return cut, 0, nil
	}
	counts := make([]int, j1-j0+1)
	reach := v.reach + 1 + margin
	err := pool.Parallel(j1-j0+1, func(jj int) error {
		if err := pool.Err(); err != nil {
			return err
		}
		j := j0 + jj
		for i := i0; i <= i1; i++ {
			x0, z0 := g.X(i), g.Z(j)
			cx, cz := x0+g.Cell/2, z0+g.Cell/2
			if v.PlanDistance(cx, cz) > reach {
				continue
			}
			h := [4]float64{float64(g.At(i, j)), float64(g.At(i+1, j)), float64(g.At(i, j+1)), float64(g.At(i+1, j+1))}
			lo, hi := math.Min(math.Min(h[0], h[1]), math.Min(h[2], h[3])), math.Max(math.Max(h[0], h[1]), math.Max(h[2], h[3]))
			if hi < box.Lo[1]-margin || lo > box.Hi[1]+margin {
				continue
			}
			pts := []sdf.Vec3{{x0, h[0], z0}, {x0 + g.Cell, h[1], z0}, {x0, h[2], z0 + g.Cell}, {x0 + g.Cell, h[3], z0 + g.Cell},
				{cx, (h[0] + h[1] + h[2] + h[3]) / 4, cz}}
			hit := false
			for _, p := range pts {
				if v.cuts(p, margin) {
					hit = true
					break
				}
			}
			// A cliff: the quad is a sliver tens of metres high; walk up it.
			if !hit && hi-lo > g.Cell {
				step := math.Max(0.2, margin)
				for y := lo; y <= hi && !hit; y += step {
					hit = v.cuts(sdf.Vec3{cx, y, cz}, margin)
				}
			}
			if hit {
				cut[j*n+i] = true
				counts[jj]++
			}
		}
		return nil
	})
	total := 0
	for _, c := range counts {
		total += c
	}
	return cut, total, err
}

// FloorKeep is how far over the cave's floor the ground may lie and still
// be kept: ground at (or under) the floor IS the floor carried out of a
// mouth, and is never cut.
const FloorKeep = 0.5

// cuts reports whether ground at p must go: in the void (dilated by margin)
// and more than FloorKeep over its floor.
func (v *Void) cuts(p sdf.Vec3, margin float64) bool {
	l, ok := v.Local(p)
	if !ok || l.B < FloorKeep {
		return false
	}
	return v.at(p, l) < margin
}

// HoleDistance is each terrain sample's distance (m) to the nearest corner
// of a cut quad, out to reach (+Inf beyond).
func HoleDistance(g *terrain.Grid, cut []bool, reach float64) []float32 {
	sz := g.S()
	n := g.N
	out := make([]float32, sz*sz)
	for k := range out {
		out[k] = float32(math.Inf(1))
	}
	r := int(math.Ceil(reach / g.Cell))
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			if !cut[j*n+i] {
				continue
			}
			for _, c := range [4][2]int{{i, j}, {i + 1, j}, {i, j + 1}, {i + 1, j + 1}} {
				for dj := -r; dj <= r; dj++ {
					jj := c[1] + dj
					if jj < 0 || jj >= sz {
						continue
					}
					for di := -r; di <= r; di++ {
						ii := c[0] + di
						if ii < 0 || ii >= sz {
							continue
						}
						d := float32(g.Cell * math.Hypot(float64(di), float64(dj)))
						if d < out[jj*sz+ii] {
							out[jj*sz+ii] = d
						}
					}
				}
			}
		}
	}
	return out
}

// ---------- the sky ----------

// Sky is the share of the sky seen from each voxel of air round a cave.
type Sky struct {
	Lo         sdf.Vec3
	Cell       float64
	NX, NY, NZ int
	// Vis is 0..1 per voxel, -1 where it was not baked (rock, or air far
	// from the cave).
	Vis []float32
}

// skyDirs is a Fibonacci hemisphere of directions, a little below the
// horizon too, and each one's weight (more for the higher ones: the sky
// overhead is the brighter).
func skyDirs(n int) ([]sdf.Vec3, []float64) {
	dirs := make([]sdf.Vec3, 0, n)
	ws := make([]float64, 0, n)
	ga := math.Pi * (3 - math.Sqrt(5))
	for k := 0; k < n; k++ {
		y := -0.12 + 1.12*(float64(k)+0.5)/float64(n)
		r := math.Sqrt(math.Max(1-y*y, 0))
		a := ga * float64(k)
		dirs = append(dirs, sdf.Vec3{r * math.Cos(a), y, r * math.Sin(a)})
		ws = append(ws, 0.35+0.65*math.Max(y, 0))
	}
	return dirs, ws
}

// BakeSky bakes the sky's visibility on a grid of cell metres over the
// cave, reaching out reach metres round it.
func BakeSky(v *Void, g *terrain.Grid, cell, reach float64, pool Pool) (*Sky, error) {
	if v.Empty() {
		return &Sky{Cell: cell}, nil
	}
	box := v.Box()
	lo := sdf.Vec3{box.Lo[0] - reach, box.Lo[1] - 2, box.Lo[2] - reach}
	hiX, hiZ := box.Hi[0]+reach, box.Hi[2]+reach
	// The grid's top: the highest ground over it.
	top := box.Hi[1] + 2
	for z := lo[2]; z <= hiZ; z += g.Cell * 4 {
		for x := lo[0]; x <= hiX; x += g.Cell * 4 {
			top = math.Max(top, float64(g.Sample(x, z))+2)
		}
	}
	sk := &Sky{Lo: lo, Cell: cell}
	sk.NX = int(math.Ceil((hiX-lo[0])/cell)) + 1
	sk.NY = int(math.Ceil((top-lo[1])/cell)) + 1
	sk.NZ = int(math.Ceil((hiZ-lo[2])/cell)) + 1
	nx, ny, nz := sk.NX, sk.NY, sk.NZ
	solid := make([]bool, nx*ny*nz)
	want := make([]bool, nx*ny*nz)
	near := v.reach + 2
	err := pool.Parallel(nz, func(k int) error {
		if err := pool.Err(); err != nil {
			return err
		}
		for i := 0; i < nx; i++ {
			x, z := lo[0]+float64(i)*cell, lo[2]+float64(k)*cell
			h := float64(g.Sample(x, z))
			close := v.PlanDistance(x, z) < near
			for j := 0; j < ny; j++ {
				y := lo[1] + float64(j)*cell
				idx := (k*ny+j)*nx + i
				in := y < h
				if close && y >= box.Lo[1]-1 && y <= box.Hi[1]+1 {
					if v.At(sdf.Vec3{x, y, z}) < 0 {
						in = false
						want[idx] = true
					}
				}
				solid[idx] = in
				if !in && close && y < h+6 {
					want[idx] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	dirs, ws := skyDirs(48)
	wsum := 0.0
	for _, w := range ws {
		wsum += w
	}
	sk.Vis = make([]float32, nx*ny*nz)
	gtop := top
	const maxDist = 160.0
	err = pool.Parallel(nz, func(k int) error {
		if err := pool.Err(); err != nil {
			return err
		}
		for j := 0; j < ny; j++ {
			for i := 0; i < nx; i++ {
				idx := (k*ny+j)*nx + i
				if !want[idx] || solid[idx] {
					sk.Vis[idx] = -1
					continue
				}
				o := sdf.Vec3{lo[0] + float64(i)*cell, lo[1] + float64(j)*cell, lo[2] + float64(k)*cell}
				vis := 0.0
				for di, d := range dirs {
					open := true
					step := cell * 0.7
					for t := step; t < maxDist; t += step {
						p := o.Add(d.Mul(t))
						if p[1] > gtop {
							break
						}
						fi := int(math.Round((p[0] - lo[0]) / cell))
						fj := int(math.Round((p[1] - lo[1]) / cell))
						fk := int(math.Round((p[2] - lo[2]) / cell))
						if fi >= 0 && fj >= 0 && fk >= 0 && fi < nx && fj < ny && fk < nz {
							if solid[(fk*ny+fj)*nx+fi] {
								open = false
								break
							}
							continue
						}
						// Past the grid: the open terrain.
						if p[1] < float64(g.Sample(p[0], p[2])) {
							open = false
							break
						}
						step = math.Min(step*1.15, 4)
					}
					if open {
						vis += ws[di]
					}
				}
				sk.Vis[idx] = float32(vis / wsum)
			}
		}
		return nil
	})
	return sk, err
}

// At is the baked visibility at p, trilinear over the baked voxels round it
// (the nearest baked one within two cells when none of the eight is), or -1.
func (sk *Sky) At(p sdf.Vec3) float64 {
	if len(sk.Vis) == 0 {
		return -1
	}
	fx := (p[0] - sk.Lo[0]) / sk.Cell
	fy := (p[1] - sk.Lo[1]) / sk.Cell
	fz := (p[2] - sk.Lo[2]) / sk.Cell
	i0, j0, k0 := int(math.Floor(fx)), int(math.Floor(fy)), int(math.Floor(fz))
	u, v, w := fx-float64(i0), fy-float64(j0), fz-float64(k0)
	sum, wt := 0.0, 0.0
	for c := 0; c < 8; c++ {
		i, j, k := i0+c&1, j0+(c>>1)&1, k0+(c>>2)&1
		if i < 0 || j < 0 || k < 0 || i >= sk.NX || j >= sk.NY || k >= sk.NZ {
			continue
		}
		val := sk.Vis[(k*sk.NY+j)*sk.NX+i]
		if val < 0 {
			continue
		}
		a := 1 - u
		if c&1 == 1 {
			a = u
		}
		b := 1 - v
		if (c>>1)&1 == 1 {
			b = v
		}
		e := 1 - w
		if (c>>2)&1 == 1 {
			e = w
		}
		f := a * b * e
		sum += f * float64(val)
		wt += f
	}
	if wt > 1e-6 {
		return sum / wt
	}
	best, bd := -1.0, math.Inf(1)
	ic, jc, kc := int(math.Round(fx)), int(math.Round(fy)), int(math.Round(fz))
	for dk := -2; dk <= 2; dk++ {
		for dj := -2; dj <= 2; dj++ {
			for di := -2; di <= 2; di++ {
				i, j, k := ic+di, jc+dj, kc+dk
				if i < 0 || j < 0 || k < 0 || i >= sk.NX || j >= sk.NY || k >= sk.NZ {
					continue
				}
				val := sk.Vis[(k*sk.NY+j)*sk.NX+i]
				if val < 0 {
					continue
				}
				if d := float64(di*di + dj*dj + dk*dk); d < bd {
					bd, best = d, float64(val)
				}
			}
		}
	}
	return best
}
