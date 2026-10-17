package sdf

// prim.go — the primitives: sphere, box, rounded box, ellipsoid, capsule,
// cylinder and the half-space a plane bounds. Each is the exact (or, for the
// ellipsoid, a bounded) distance from Inigo Quilez's catalogue, centred where
// the caller says; rotate one with Rotate (ops.go).

import "math"

// Sphere is a ball of radius r about c.
func Sphere(c Vec3, r float64) Shape {
	return Shape{
		F:   func(p Vec3) float64 { return p.Sub(c).Len() - r },
		Box: BoxAt(c, Vec3{r, r, r}),
		Lip: 1,
	}
}

// Cuboid is an axis-aligned box about c with half-extents h.
func Cuboid(c, h Vec3) Shape { return RoundBox(c, h, 0) }

// RoundBox is a box about c with half-extents h whose edges and corners are
// rounded by r (r ≤ the smallest half-extent; the box keeps its size).
func RoundBox(c, h Vec3, r float64) Shape {
	r = Clamp(r, 0, h.MinComp())
	in := h.Sub(Vec3{r, r, r})
	return Shape{
		F: func(p Vec3) float64 {
			q := p.Sub(c).Abs().Sub(in)
			out := Vec3{math.Max(q[0], 0), math.Max(q[1], 0), math.Max(q[2], 0)}.Len()
			return out + math.Min(q.MaxComp(), 0) - r
		},
		Box: BoxAt(c, h),
		Lip: 1,
	}
}

// Ellipsoid is an axis-aligned ellipsoid about c with radii r: Quilez's
// bounded approximation (exact on the axes, within a few percent elsewhere
// for proportions under 3:1).
func Ellipsoid(c, r Vec3) Shape {
	return Shape{
		F: func(p Vec3) float64 {
			q := p.Sub(c)
			k0 := Vec3{q[0] / r[0], q[1] / r[1], q[2] / r[2]}.Len()
			k1 := Vec3{q[0] / (r[0] * r[0]), q[1] / (r[1] * r[1]), q[2] / (r[2] * r[2])}.Len()
			if k1 == 0 {
				return -r.MinComp()
			}
			return k0 * (k0 - 1) / k1
		},
		Box: BoxAt(c, r),
		Lip: 1.1 * r.MaxComp() / r.MinComp(),
	}
}

// Capsule is the points within r of the segment a–b.
func Capsule(a, b Vec3, r float64) Shape {
	ab := b.Sub(a)
	l2 := ab.Dot(ab)
	return Shape{
		F: func(p Vec3) float64 {
			t := 0.0
			if l2 > 0 {
				t = Clamp(p.Sub(a).Dot(ab)/l2, 0, 1)
			}
			return p.Sub(a.Add(ab.Mul(t))).Len() - r
		},
		Box: Box{a, a}.Union(Box{b, b}).Grow(r),
		Lip: 1,
	}
}

// Cylinder is a vertical cylinder about c, radius r, half-height h.
func Cylinder(c Vec3, r, h float64) Shape {
	return Shape{
		F: func(p Vec3) float64 {
			q := p.Sub(c)
			dx := math.Hypot(q[0], q[2]) - r
			dy := math.Abs(q[1]) - h
			return math.Min(math.Max(dx, dy), 0) + math.Hypot(math.Max(dx, 0), math.Max(dy, 0))
		},
		Box: BoxAt(c, Vec3{r, h, r}),
		Lip: 1,
	}
}

// HalfSpace is the side of the plane through point o with normal n that n
// points away from: inside where (p − o)·n < 0. Infinite on its own; it bounds
// something only by intersection (a fracture face, a flat back).
func HalfSpace(o, n Vec3) Shape {
	n = n.Normalize()
	return Shape{
		F:   func(p Vec3) float64 { return p.Sub(o).Dot(n) },
		Box: Infinite,
		Lip: 1,
	}
}

// Plane is HalfSpace by a normal and an offset: inside where p·n < d.
func Plane(n Vec3, d float64) Shape {
	n = n.Normalize()
	return HalfSpace(n.Mul(d), n)
}
