// Package sdf is a signed-distance modelling library for the procedural
// generators: shapes are functions from a point to its signed distance from a
// surface (negative inside), composed by CSG, transforms, noise displacement
// and the geological operators a rock wants — strata stacked in layers that
// weather back at different rates (strata.go), joint and fracture networks
// (fracture.go), spalls (chips.go) — and turned into a triangle mesh by a
// multi-threaded dual-contouring mesher on a sparse grid of blocks
// (mesher.go), which also rounds convex edges by curvature as weathering does
// (the mesher's Weathering).
//
// It is pure computation: it imports only procgen/noise (layering-asserted,
// internal/layering_test.go), so a generator can use it on worker goroutines
// without a GL context, and the meshes it makes are plain arrays a stage turns
// into a model.ImportMesh for the cook.
//
// # Shapes
//
// A Shape is the function and two facts about it that the mesher needs:
//
//   - Box: a box the surface lies inside. Outside it the shape is empty (the
//     function may be anything positive there). Shapes only grow their boxes
//     as they compose, so the mesher's grid is bounded.
//   - Lip: a Lipschitz bound — |F(a) − F(b)| ≤ Lip·|a − b|. An exact distance
//     has Lip 1. Displacing it by noise, or insetting it by layers, makes the
//     function steeper than a distance; Lip says how much, which is what lets
//     the mesher skip a whole block of the grid from one sample at its centre
//     (if |F(c)| > Lip·r nothing within r of c can be on the surface).
//
// The function need not be an exact distance anywhere — only its sign decides
// the surface and only its gradient direction the normals — but the closer it
// is the better the smooth operators (their radii are distances) and the
// ambient occlusion bake (bake.go) behave.
//
// Coordinates are metres, float64 throughout: a cliff chunk is tens of metres
// and its detail millimetres, and the noise is float64 already.
package sdf

import "math"

// Vec3 is a point or a direction.
type Vec3 [3]float64

// V is a Vec3.
func V(x, y, z float64) Vec3 { return Vec3{x, y, z} }

func (a Vec3) Add(b Vec3) Vec3      { return Vec3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func (a Vec3) Sub(b Vec3) Vec3      { return Vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func (a Vec3) Mul(s float64) Vec3   { return Vec3{a[0] * s, a[1] * s, a[2] * s} }
func (a Vec3) Hadamard(b Vec3) Vec3 { return Vec3{a[0] * b[0], a[1] * b[1], a[2] * b[2]} }
func (a Vec3) Dot(b Vec3) float64   { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func (a Vec3) Len() float64         { return math.Sqrt(a.Dot(a)) }
func (a Vec3) Abs() Vec3            { return Vec3{math.Abs(a[0]), math.Abs(a[1]), math.Abs(a[2])} }
func (a Vec3) Cross(b Vec3) Vec3 {
	return Vec3{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

// Normalize is a unit vector along a (+Y for the zero vector).
func (a Vec3) Normalize() Vec3 {
	l := a.Len()
	if l == 0 {
		return Vec3{0, 1, 0}
	}
	return a.Mul(1 / l)
}

// MaxComp and MinComp are the largest and smallest component.
func (a Vec3) MaxComp() float64 { return math.Max(a[0], math.Max(a[1], a[2])) }
func (a Vec3) MinComp() float64 { return math.Min(a[0], math.Min(a[1], a[2])) }

// Box is an axis-aligned box. The zero Box is empty.
type Box struct {
	Lo, Hi Vec3
}

// Infinite is the box of a shape that is everywhere (a half-space): it takes
// part in an intersection without bounding anything.
var Infinite = Box{Lo: Vec3{-1e30, -1e30, -1e30}, Hi: Vec3{1e30, 1e30, 1e30}}

// BoxAt is the box centred on c with half-extents h.
func BoxAt(c, h Vec3) Box { return Box{c.Sub(h), c.Add(h)} }

// Empty reports whether the box holds nothing.
func (b Box) Empty() bool { return b.Hi[0] < b.Lo[0] || b.Hi[1] < b.Lo[1] || b.Hi[2] < b.Lo[2] }

// IsInfinite reports whether the box is (part of) Infinite.
func (b Box) IsInfinite() bool { return b.Size().MaxComp() > 1e29 }

// Size is Hi − Lo.
func (b Box) Size() Vec3 { return b.Hi.Sub(b.Lo) }

// Center is the box's middle.
func (b Box) Center() Vec3 { return b.Lo.Add(b.Hi).Mul(0.5) }

// Union is the smallest box holding both.
func (b Box) Union(o Box) Box {
	return Box{
		Vec3{math.Min(b.Lo[0], o.Lo[0]), math.Min(b.Lo[1], o.Lo[1]), math.Min(b.Lo[2], o.Lo[2])},
		Vec3{math.Max(b.Hi[0], o.Hi[0]), math.Max(b.Hi[1], o.Hi[1]), math.Max(b.Hi[2], o.Hi[2])},
	}
}

// Intersect is the overlap of both.
func (b Box) Intersect(o Box) Box {
	return Box{
		Vec3{math.Max(b.Lo[0], o.Lo[0]), math.Max(b.Lo[1], o.Lo[1]), math.Max(b.Lo[2], o.Lo[2])},
		Vec3{math.Min(b.Hi[0], o.Hi[0]), math.Min(b.Hi[1], o.Hi[1]), math.Min(b.Hi[2], o.Hi[2])},
	}
}

// Grow is the box widened by r on every side.
func (b Box) Grow(r float64) Box { return Box{b.Lo.Sub(Vec3{r, r, r}), b.Hi.Add(Vec3{r, r, r})} }

// Corners is the box's eight corners.
func (b Box) Corners() [8]Vec3 {
	var c [8]Vec3
	for i := range c {
		for k := 0; k < 3; k++ {
			if i>>k&1 == 0 {
				c[i][k] = b.Lo[k]
			} else {
				c[i][k] = b.Hi[k]
			}
		}
	}
	return c
}

// Shape is a signed distance function with its bounds (see the package
// comment).
type Shape struct {
	F   func(p Vec3) float64
	Box Box
	Lip float64
}

// At is F(p).
func (s Shape) At(p Vec3) float64 { return s.F(p) }

// Gradient is F's gradient at p by central differences of step e, unit
// length (or +Y where it is flat).
func (s Shape) Gradient(p Vec3, e float64) Vec3 {
	gx := s.F(Vec3{p[0] + e, p[1], p[2]}) - s.F(Vec3{p[0] - e, p[1], p[2]})
	gy := s.F(Vec3{p[0], p[1] + e, p[2]}) - s.F(Vec3{p[0], p[1] - e, p[2]})
	gz := s.F(Vec3{p[0], p[1], p[2] + e}) - s.F(Vec3{p[0], p[1], p[2] - e})
	return Vec3{gx, gy, gz}.Normalize()
}

// lip is a Lipschitz bound read for composition: never below 1.
func lip(l float64) float64 { return math.Max(l, 1) }

// Clamp is x limited to lo..hi.
func Clamp(x, lo, hi float64) float64 { return math.Min(math.Max(x, lo), hi) }

// SmoothStep is the cubic Hermite step from e0 to e1.
func SmoothStep(e0, e1, x float64) float64 {
	if e0 == e1 {
		if x < e0 {
			return 0
		}
		return 1
	}
	t := Clamp((x-e0)/(e1-e0), 0, 1)
	return t * t * (3 - 2*t)
}

// Mix is a + (b − a)·t.
func Mix(a, b, t float64) float64 { return a + (b-a)*t }
