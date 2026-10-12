package place

// spatial.go — a hash grid of footprints, for the Poisson-disk and overlap
// tests: "is there a placed footprint within d of here?" in time
// proportional to the footprints nearby, whatever their sizes.
//
// Each footprint is filed in every cell its disc touches, so a query only
// looks at the cells its own reach touches; a cliff chunk's 8 m footprint
// sits in a few dozen 2 m cells and a pebble's in one.

import "math"

// discGrid is the hash grid.
type discGrid struct {
	cell float64
	// reach is the largest spacing factor any disc is filed or queried
	// with (at least 1): a disc is filed over its radius times it, and a
	// query looks as far as its own radius times it, which finds every pair
	// nearer than max(their spacings) × (their radii's sum).
	reach float64
	cells map[[2]int32][]int32
	discs []Disc
	// spacing is each disc's own spacing factor: a pair is too close when
	// their centres are nearer than max(spacing) × (r1 + r2).
	spacing []float64
}

func newDiscGrid(cell, maxSpacing float64) *discGrid {
	return &discGrid{cell: math.Max(cell, 0.05), reach: math.Max(maxSpacing, 1), cells: map[[2]int32][]int32{}}
}

func (g *discGrid) key(x, z float64) [2]int32 {
	return [2]int32{int32(math.Floor(x / g.cell)), int32(math.Floor(z / g.cell))}
}

// add files a footprint with its spacing factor.
func (g *discGrid) add(d Disc, spacing float64) {
	id := int32(len(g.discs))
	g.discs = append(g.discs, d)
	g.spacing = append(g.spacing, spacing)
	r := d.R * g.reach
	lo := g.key(d.X-r, d.Z-r)
	hi := g.key(d.X+r, d.Z+r)
	for j := lo[1]; j <= hi[1]; j++ {
		for i := lo[0]; i <= hi[0]; i++ {
			k := [2]int32{i, j}
			g.cells[k] = append(g.cells[k], id)
		}
	}
}

// clear reports whether a footprint d with spacing factor sp keeps clear of
// every one filed: for each, the centres at least max(sp, theirs) × (the
// radii's sum) apart.
func (g *discGrid) clear(d Disc, sp float64) bool {
	// A conflict is a centre nearer than f·(r + R) with f <= reach, so the
	// other's filed disc (radius reach·R) comes within reach·r of d's
	// centre: the cells that span covers hold every candidate.
	reach := d.R * g.reach
	lo := g.key(d.X-reach, d.Z-reach)
	hi := g.key(d.X+reach, d.Z+reach)
	for j := lo[1]; j <= hi[1]; j++ {
		for i := lo[0]; i <= hi[0]; i++ {
			for _, id := range g.cells[[2]int32{i, j}] {
				o := g.discs[id]
				f := math.Max(sp, g.spacing[id])
				lim := f * (d.R + o.R)
				dx, dz := d.X-o.X, d.Z-o.Z
				if dx*dx+dz*dz < lim*lim {
					return false
				}
			}
		}
	}
	return true
}

// near calls fn with every filed footprint (and its ID: the order it was
// filed in) whose centre is within r of (x, z).
func (g *discGrid) near(x, z, r float64, fn func(id int, d Disc)) {
	lo := g.key(x-r, z-r)
	hi := g.key(x+r, z+r)
	seen := map[int32]bool{}
	for j := lo[1]; j <= hi[1]; j++ {
		for i := lo[0]; i <= hi[0]; i++ {
			for _, id := range g.cells[[2]int32{i, j}] {
				if seen[id] {
					continue
				}
				seen[id] = true
				o := g.discs[id]
				if (o.X-x)*(o.X-x)+(o.Z-z)*(o.Z-z) <= r*r {
					fn(int(id), o)
				}
			}
		}
	}
}
