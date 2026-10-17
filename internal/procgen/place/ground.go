package place

// ground.go — the ground every generator here stands things on: a terrain's
// heights and masks with the lookups placement needs, and the cliff field
// (how far a point is from the nearest cliff, and whether that cliff rises
// above it — the foot of a wall, where talus gathers).
//
// The masks are fetched once as slices and sampled here directly: the
// terrain's own MaskAt takes a lock a call, and a scatter evaluates its
// rules' masks a few million times.

import (
	"fmt"
	"math"

	"foundry/internal/procgen/terrain"
)

// Ground is a terrain as placement reads it.
type Ground struct {
	Grid *terrain.Grid
	// Lo and Hi are the height range (Height filters are fractions of it).
	Lo, Hi float64
	masks  map[string][]float32
	// cliff is the cliff field, built on first use by CliffField.
	cliff *CliffField
}

// NewGround reads t's heights and the named masks (every mask when names
// is empty).
func NewGround(t *terrain.Terrain, names ...string) (*Ground, error) {
	if t == nil || t.Grid == nil {
		return nil, fmt.Errorf("no terrain")
	}
	if len(names) == 0 {
		names = terrain.MaskNames
	}
	lo, hi := t.Grid.Range()
	g := &Ground{Grid: t.Grid, Lo: float64(lo), Hi: float64(hi), masks: map[string][]float32{}}
	for _, n := range names {
		v, err := t.Mask(n)
		if err != nil {
			return nil, err
		}
		if len(v) != len(t.Grid.H) {
			return nil, fmt.Errorf("mask %s has %d samples, the heights %d", n, len(v), len(t.Grid.H))
		}
		g.masks[n] = v
	}
	return g, nil
}

// Bounds is the ground's XZ rectangle: min x, min z, max x, max z.
func (g *Ground) Bounds() [4]float64 {
	s := g.Grid.Size()
	return [4]float64{g.Grid.MinX, g.Grid.MinZ, g.Grid.MinX + s, g.Grid.MinZ + s}
}

// Contains reports whether (x, z) is on the ground, margin metres in from
// its edge.
func (g *Ground) Contains(x, z, margin float64) bool {
	b := g.Bounds()
	return x >= b[0]+margin && x <= b[2]-margin && z >= b[1]+margin && z <= b[3]-margin
}

// Height is the ground's height at (x, z). The tiles are the same samples
// triangulated, so on a 0.5 m grid the bilinear height and the rendered
// surface differ by millimetres.
func (g *Ground) Height(x, z float64) float64 { return float64(g.Grid.Sample(x, z)) }

// Mask is a mask's bilinear value at (x, z); 0 for a mask not read.
func (g *Ground) Mask(name string, x, z float64) float64 {
	v := g.masks[name]
	if v == nil {
		return 0
	}
	return sampleGrid(v, g.Grid, x, z)
}

// HasMask reports whether the mask was read.
func (g *Ground) HasMask(name string) bool { return g.masks[name] != nil }

// SlopeDeg is the slope at (x, z) in degrees, from the heights over a cell.
func (g *Ground) SlopeDeg(x, z float64) float64 {
	n := g.Normal(x, z, g.Grid.Cell)
	return math.Acos(math.Max(-1, math.Min(1, n[1]))) * 180 / math.Pi
}

// Normal is the unit normal of the plane through the heights on a circle of
// radius r about (x, z) — the ground under a rock of that footprint, its
// bumps averaged away (r at least one cell).
func (g *Ground) Normal(x, z, r float64) [3]float64 {
	r = math.Max(r, g.Grid.Cell)
	// Least squares over eight points on the circle: for a plane
	// h = a + bx + cz on a symmetric ring the slopes are independent sums.
	var sx, sz float64
	for k := 0; k < 8; k++ {
		s, c := math.Sincos(float64(k) * math.Pi / 4)
		h := g.Height(x+r*c, z+r*s)
		sx += h * c
		sz += h * s
	}
	// Σcos² over the ring is 4, so the slope is Σh·cos / (4r).
	dx, dz := sx/(4*r), sz/(4*r)
	return normalize([3]float64{-dx, 1, -dz})
}

// Gradient is the height's gradient at (x, z) over a cell: (∂h/∂x, ∂h/∂z).
func (g *Ground) Gradient(x, z float64) (float64, float64) {
	d := g.Grid.Cell
	return (g.Height(x+d, z) - g.Height(x-d, z)) / (2 * d), (g.Height(x, z+d) - g.Height(x, z-d)) / (2 * d)
}

// sampleGrid is a bilinear lookup of per-sample values on grid's samples.
func sampleGrid(v []float32, gr *terrain.Grid, x, z float64) float64 {
	s := gr.N + 1
	fx := (x - gr.MinX) / gr.Cell
	fz := (z - gr.MinZ) / gr.Cell
	fx = math.Max(0, math.Min(float64(s-1), fx))
	fz = math.Max(0, math.Min(float64(s-1), fz))
	i, j := min(int(fx), s-2), min(int(fz), s-2)
	if s < 2 {
		return float64(v[0])
	}
	u, w := fx-float64(i), fz-float64(j)
	k := j*s + i
	a := float64(v[k]) + (float64(v[k+1])-float64(v[k]))*u
	b := float64(v[k+s]) + (float64(v[k+s+1])-float64(v[k+s]))*u
	return a + (b-a)*w
}

// ---------- the cliff field ----------

// CliffField is, per terrain sample, the distance to the nearest cliff
// sample (the cliff mask at or over its threshold) and which one it is. A
// point at the FOOT of a wall has its nearest cliff uphill of it — the
// direction to the cliff runs with the cliff's gradient — and one on the rim
// downhill; Below tells them apart.
type CliffField struct {
	gr *terrain.Grid
	// Dist is metres to the nearest cliff sample (+Inf: none).
	Dist []float32
	// Near is that sample's index (-1: none).
	Near []int32
	// gx, gz are each cliff sample's height gradient, unit length in XZ.
	gx, gz []float32
	// threshold is the cliff mask level it was built at.
	threshold float64
}

// CliffField is the ground's cliff field at the mask threshold thr (built
// once; later calls with another threshold rebuild it).
func (g *Ground) CliffField(thr float64) *CliffField {
	if g.cliff != nil && g.cliff.threshold == thr {
		return g.cliff
	}
	g.cliff = buildCliffField(g, thr)
	return g.cliff
}

// buildCliffField runs a two-pass 8-neighbour chamfer that carries each
// sample's nearest seed (Danielsson-style "dead reckoning"): exact enough —
// within a cell or so — for a talus apron's falloff, and linear in the
// samples.
func buildCliffField(g *Ground, thr float64) *CliffField {
	gr := g.Grid
	s := gr.N + 1
	n := s * s
	cl := g.masks[terrain.MaskCliff]
	c := &CliffField{gr: gr, Dist: make([]float32, n), Near: make([]int32, n), gx: make([]float32, n), gz: make([]float32, n),
		threshold: thr}
	inf := float32(math.Inf(1))
	for k := 0; k < n; k++ {
		c.Dist[k], c.Near[k] = inf, -1
		if cl != nil && float64(cl[k]) >= thr {
			c.Dist[k], c.Near[k] = 0, int32(k)
			i, j := k%s, k/s
			hx := gr.At(i+1, j) - gr.At(i-1, j)
			hz := gr.At(i, j+1) - gr.At(i, j-1)
			l := float32(math.Hypot(float64(hx), float64(hz)))
			if l > 0 {
				c.gx[k], c.gz[k] = hx/l, hz/l
			}
		}
	}
	try := func(k, from int) {
		f := c.Near[from]
		if f < 0 {
			return
		}
		dx := float64(k%s - int(f)%s)
		dz := float64(k/s - int(f)/s)
		d := float32(math.Sqrt(dx*dx+dz*dz) * gr.Cell)
		if d < c.Dist[k] {
			c.Dist[k], c.Near[k] = d, f
		}
	}
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			k := j*s + i
			if i > 0 {
				try(k, k-1)
			}
			if j > 0 {
				try(k, k-s)
				if i > 0 {
					try(k, k-s-1)
				}
				if i < s-1 {
					try(k, k-s+1)
				}
			}
		}
	}
	for j := s - 1; j >= 0; j-- {
		for i := s - 1; i >= 0; i-- {
			k := j*s + i
			if i < s-1 {
				try(k, k+1)
			}
			if j < s-1 {
				try(k, k+s)
				if i < s-1 {
					try(k, k+s+1)
				}
				if i > 0 {
					try(k, k+s-1)
				}
			}
		}
	}
	return c
}

// At is the distance (metres) from (x, z) to the nearest cliff sample, and
// how far that cliff stands ABOVE the point: 1 when the way to it runs
// straight uphill along its gradient (the foot of the wall), 0 across it or
// downhill (its rim). +Inf and 0 when there is no cliff.
func (c *CliffField) At(x, z float64) (dist, below float64) {
	gr := c.gr
	s := gr.N + 1
	i := int(math.Round((x - gr.MinX) / gr.Cell))
	j := int(math.Round((z - gr.MinZ) / gr.Cell))
	i = min(max(i, 0), s-1)
	j = min(max(j, 0), s-1)
	k := j*s + i
	f := c.Near[k]
	if f < 0 {
		return math.Inf(1), 0
	}
	fx := gr.X(int(f) % s)
	fz := gr.Z(int(f) / s)
	dx, dz := fx-x, fz-z
	d := math.Hypot(dx, dz)
	if d < 1e-9 {
		return 0, 1
	}
	cos := (dx*float64(c.gx[f]) + dz*float64(c.gz[f])) / d
	return d, smooth(0.1, 0.6, cos)
}
