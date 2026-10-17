package noise

// cellular.go — Worley / Voronoi noise.
//
// Every lattice cell holds one feature point at a hashed position inside it
// (jittered by Jitter: 1 is fully random, 0 is a regular grid). A sample's
// answer is the distances to its nearest two feature points, F1 and F2, and
// the ID of the nearest one's cell.
//
// What the generators use each for:
//
//   - F1 alone: craters, cobbles, cracked-mud plates (with the colour from
//     the cell ID).
//   - F2 - F1: the cell borders — zero exactly on a border, which is what a
//     crack network or a Voronoi fracture plane needs.
//   - The cell ID: a stable per-cell random number (unit(ID) is uniform in
//     [0,1)), so each plate, rock face or fracture block can pick its own tint,
//     height or tilt and keep it however the sampling moves.
//
// The 3x3 (or 3x3x3) neighbourhood search is exact for Jitter <= 1: a feature
// point can sit no further than one cell from its own, so the nearest two are
// always among the neighbours.

import "math"

// Metric is how a cellular sample measures distance.
type Metric int

const (
	MetricEuclidean Metric = iota
	MetricManhattan
	MetricChebyshev
)

// MetricNames are the UI spellings, indexed by Metric.
var MetricNames = []string{"euclidean", "manhattan", "chebyshev"}

// Cell is one cellular sample.
type Cell struct {
	F1, F2 float64 // distances to the nearest and second-nearest feature points
	// ID identifies the nearest feature point's cell: equal for every sample
	// in one Voronoi region, and a well-mixed word, so CellUnit(ID) is a random
	// number per region.
	ID uint64
	// Point is the nearest feature point itself (x, y[, z]).
	Point [3]float64
}

// Edge is F2 - F1: zero on a cell border.
func (c Cell) Edge() float64 { return c.F2 - c.F1 }

// CellUnit maps a cell ID to [0, 1).
func CellUnit(id uint64) float64 { return unit(Mix64(id)) }

func metric(m Metric, dx, dy, dz float64) float64 {
	switch m {
	case MetricManhattan:
		return math.Abs(dx) + math.Abs(dy) + math.Abs(dz)
	case MetricChebyshev:
		return math.Max(math.Abs(dx), math.Max(math.Abs(dy), math.Abs(dz)))
	}
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// Cellular2 samples 2D cellular noise at (x, y), one feature point per unit
// cell. jitter in [0, 1].
func Cellular2(seed uint64, x, y, jitter float64, m Metric) Cell {
	ix, _ := floor(x)
	iy, _ := floor(y)
	jitter = Clamp(jitter, 0, 1)
	c := Cell{F1: math.Inf(1), F2: math.Inf(1)}
	for dy := int64(-1); dy <= 1; dy++ {
		for dx := int64(-1); dx <= 1; dx++ {
			cx, cy := ix+dx, iy+dy
			h := hash2(seed, cx, cy)
			px := float64(cx) + 0.5 + (unit(h)-0.5)*jitter
			py := float64(cy) + 0.5 + (unit(Mix64(h))-0.5)*jitter
			d := metric(m, px-x, py-y, 0)
			switch {
			case d < c.F1:
				c.F2 = c.F1
				c.F1 = d
				c.ID = h
				c.Point = [3]float64{px, py, 0}
			case d < c.F2:
				c.F2 = d
			}
		}
	}
	return c
}

// Cellular3 samples 3D cellular noise at (x, y, z).
func Cellular3(seed uint64, x, y, z, jitter float64, m Metric) Cell {
	ix, _ := floor(x)
	iy, _ := floor(y)
	iz, _ := floor(z)
	jitter = Clamp(jitter, 0, 1)
	c := Cell{F1: math.Inf(1), F2: math.Inf(1)}
	for dz := int64(-1); dz <= 1; dz++ {
		for dy := int64(-1); dy <= 1; dy++ {
			for dx := int64(-1); dx <= 1; dx++ {
				cx, cy, cz := ix+dx, iy+dy, iz+dz
				h := hash3(seed, cx, cy, cz)
				h2 := Mix64(h)
				px := float64(cx) + 0.5 + (unit(h)-0.5)*jitter
				py := float64(cy) + 0.5 + (unit(h2)-0.5)*jitter
				pz := float64(cz) + 0.5 + (unit(Mix64(h2))-0.5)*jitter
				d := metric(m, px-x, py-y, pz-z)
				switch {
				case d < c.F1:
					c.F2 = c.F1
					c.F1 = d
					c.ID = h
					c.Point = [3]float64{px, py, pz}
				case d < c.F2:
					c.F2 = d
				}
			}
		}
	}
	return c
}
