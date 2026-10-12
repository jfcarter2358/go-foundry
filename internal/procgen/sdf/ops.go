package sdf

// ops.go — composing shapes: CSG (hard and smooth), offsets, transforms.
//
// The smooth operators are the polynomial ones (Quilez's smin): within k of
// each other the two distances blend over a fillet of radius about k/4..k/2,
// and the result stays a bound on the distance (Lipschitz 1 in, about 1 out).
// A rock is mostly smooth intersections — a block bounded by its fracture
// faces, each edge rounded by how weathered it is — and smooth subtractions —
// joints and chips cut in with rounded lips.

import "math"

// sminK is the polynomial smooth minimum of a and b over k (min when k ≤ 0).
func sminK(a, b, k float64) float64 {
	if k <= 0 {
		return math.Min(a, b)
	}
	h := math.Max(k-math.Abs(a-b), 0) / k
	return math.Min(a, b) - h*h*k*0.25
}

// smaxK is the smooth maximum.
func smaxK(a, b, k float64) float64 { return -sminK(-a, -b, k) }

// SMin and SMax are the smooth minimum and maximum, for callers composing
// their own functions.
func SMin(a, b, k float64) float64 { return sminK(a, b, k) }
func SMax(a, b, k float64) float64 { return smaxK(a, b, k) }

// Union is the shapes together.
func Union(s ...Shape) Shape { return SmoothUnion(0, s...) }

// SmoothUnion is the shapes together, blended over k where they meet.
func SmoothUnion(k float64, s ...Shape) Shape {
	if len(s) == 1 {
		return s[0]
	}
	fs := make([]func(Vec3) float64, len(s))
	box := s[0].Box
	l := 0.0
	for i := range s {
		fs[i] = s[i].F
		box = box.Union(s[i].Box)
		l = math.Max(l, s[i].Lip)
	}
	return Shape{
		F: func(p Vec3) float64 {
			d := fs[0](p)
			for _, f := range fs[1:] {
				d = sminK(d, f(p), k)
			}
			return d
		},
		Box: box.Grow(k * 0.25),
		Lip: lip(l),
	}
}

// Intersect is where every shape is.
func Intersect(s ...Shape) Shape { return SmoothIntersect(0, s...) }

// SmoothIntersect is where every shape is, the edges rounded over k.
func SmoothIntersect(k float64, s ...Shape) Shape {
	if len(s) == 1 {
		return s[0]
	}
	fs := make([]func(Vec3) float64, len(s))
	box := Infinite
	l := 0.0
	for i := range s {
		fs[i] = s[i].F
		box = box.Intersect(s[i].Box)
		l = math.Max(l, s[i].Lip)
	}
	return Shape{
		F: func(p Vec3) float64 {
			d := fs[0](p)
			for _, f := range fs[1:] {
				d = smaxK(d, f(p), k)
			}
			return d
		},
		Box: box,
		Lip: lip(l),
	}
}

// Subtract is a with b cut out of it.
func Subtract(a, b Shape) Shape { return SmoothSubtract(0, a, b) }

// SmoothSubtract is a with b cut out, the cut's lip rounded over k.
func SmoothSubtract(k float64, a, b Shape) Shape {
	fa, fb := a.F, b.F
	return Shape{
		F:   func(p Vec3) float64 { return smaxK(fa(p), -fb(p), k) },
		Box: a.Box,
		Lip: lip(math.Max(a.Lip, b.Lip)),
	}
}

// Offset grows the shape by r (shrinks it for r < 0): the surface moved r
// along its normal, which for a true distance also rounds its convex edges by
// r.
func Offset(s Shape, r float64) Shape {
	f := s.F
	box := s.Box
	if r > 0 && !box.IsInfinite() {
		box = box.Grow(r)
	}
	return Shape{F: func(p Vec3) float64 { return f(p) - r }, Box: box, Lip: s.Lip}
}

// Translate moves the shape by t.
func Translate(s Shape, t Vec3) Shape {
	f := s.F
	box := s.Box
	if !box.IsInfinite() {
		box = Box{box.Lo.Add(t), box.Hi.Add(t)}
	}
	return Shape{F: func(p Vec3) float64 { return f(p.Sub(t)) }, Box: box, Lip: s.Lip}
}

// Scale scales the shape about the origin by k > 0 (the distance with it).
func Scale(s Shape, k float64) Shape {
	f := s.F
	box := s.Box
	if !box.IsInfinite() {
		box = Box{box.Lo.Mul(k), box.Hi.Mul(k)}
	}
	return Shape{F: func(p Vec3) float64 { return f(p.Mul(1/k)) * k }, Box: box, Lip: s.Lip}
}

// Stretch scales the shape about the origin by k per axis. Not a distance any
// more: the Lipschitz bound grows by the ratio of the largest scale to the
// smallest, and the result is scaled by the smallest so it stays a bound.
func Stretch(s Shape, k Vec3) Shape {
	f := s.F
	box := s.Box
	if !box.IsInfinite() {
		box = Box{box.Lo.Hadamard(k), box.Hi.Hadamard(k)}
	}
	lo := k.MinComp()
	inv := Vec3{1 / k[0], 1 / k[1], 1 / k[2]}
	return Shape{F: func(p Vec3) float64 { return f(p.Hadamard(inv)) * lo }, Box: box, Lip: s.Lip * k.MaxComp() / lo}
}

// Mat3 is a 3x3 matrix, row-major: M[r][c].
type Mat3 [3][3]float64

// Identity is the identity matrix.
var Identity = Mat3{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}

// Apply is M·v.
func (m Mat3) Apply(v Vec3) Vec3 {
	return Vec3{
		m[0][0]*v[0] + m[0][1]*v[1] + m[0][2]*v[2],
		m[1][0]*v[0] + m[1][1]*v[1] + m[1][2]*v[2],
		m[2][0]*v[0] + m[2][1]*v[1] + m[2][2]*v[2],
	}
}

// Mul is m·o.
func (m Mat3) Mul(o Mat3) Mat3 {
	var r Mat3
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			r[i][j] = m[i][0]*o[0][j] + m[i][1]*o[1][j] + m[i][2]*o[2][j]
		}
	}
	return r
}

// T is the transpose (the inverse of a rotation).
func (m Mat3) T() Mat3 {
	var r Mat3
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			r[i][j] = m[j][i]
		}
	}
	return r
}

// RotAxis is a rotation by angle a (radians) about the unit axis u.
func RotAxis(u Vec3, a float64) Mat3 {
	u = u.Normalize()
	s, c := math.Sincos(a)
	t := 1 - c
	x, y, z := u[0], u[1], u[2]
	return Mat3{
		{t*x*x + c, t*x*y - s*z, t*x*z + s*y},
		{t*x*y + s*z, t*y*y + c, t*y*z - s*x},
		{t*x*z - s*y, t*y*z + s*x, t*z*z + c},
	}
}

// RotX, RotY and RotZ are rotations about the axes.
func RotX(a float64) Mat3 { return RotAxis(Vec3{1, 0, 0}, a) }
func RotY(a float64) Mat3 { return RotAxis(Vec3{0, 1, 0}, a) }
func RotZ(a float64) Mat3 { return RotAxis(Vec3{0, 0, 1}, a) }

// Rotate turns the shape by m about the origin.
func Rotate(s Shape, m Mat3) Shape {
	f := s.F
	inv := m.T()
	box := s.Box
	if !box.IsInfinite() {
		var nb Box
		for i, c := range box.Corners() {
			q := m.Apply(c)
			if i == 0 {
				nb = Box{q, q}
				continue
			}
			nb = nb.Union(Box{q, q})
		}
		box = nb
	}
	return Shape{F: func(p Vec3) float64 { return f(inv.Apply(p)) }, Box: box, Lip: s.Lip}
}

// Bound limits a shape to a box: outside it the shape is empty. For a shape
// whose function stays negative somewhere it should not (a displaced
// half-space), and to give an infinite one a box.
func Bound(s Shape, b Box) Shape {
	return SmoothIntersect(0, s, Shape{
		F: func(p Vec3) float64 {
			c, h := b.Center(), b.Size().Mul(0.5)
			q := p.Sub(c).Abs().Sub(h)
			out := Vec3{math.Max(q[0], 0), math.Max(q[1], 0), math.Max(q[2], 0)}.Len()
			return out + math.Min(q.MaxComp(), 0)
		},
		Box: b,
		Lip: 1,
	})
}
