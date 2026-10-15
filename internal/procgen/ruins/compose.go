package ruins

// compose.go — composing many parts cheaply: a union that skips the parts
// whose box is further than what it already has, a subtraction that skips
// the cutters that cannot matter, face frames and repetition along a face.

import (
	"math"

	"foundry/internal/procgen/sdf"
)

// boxDist is the distance from q to the box (0 inside).
func boxDist(q sdf.Vec3, b sdf.Box) float64 {
	var s float64
	for k := 0; k < 3; k++ {
		if d := b.Lo[k] - q[k]; d > 0 {
			s += d * d
		} else if d := q[k] - b.Hi[k]; d > 0 {
			s += d * d
		}
	}
	if s == 0 {
		return 0
	}
	return math.Sqrt(s)
}

// Model is a building under construction: solid parts added, cutters
// subtracted (after every part), each with its box.
type Model struct {
	parts []sdf.Shape
	cuts  []sdf.Shape
	// Far is what F answers where no part is near: a positive bound.
	Far float64
}

// Add unions solid parts into the model.
func (m *Model) Add(s ...sdf.Shape) { m.parts = append(m.parts, s...) }

// Cut subtracts cutters from the model.
func (m *Model) Cut(s ...sdf.Shape) { m.cuts = append(m.cuts, s...) }

// Shape is the model as one shape: the union of the parts less the cutters.
// A part whose box is at least the distance found so far cannot lower it and
// is skipped; a cutter is skipped where it cannot change the answer.
func (m *Model) Shape() sdf.Shape {
	parts := append([]sdf.Shape(nil), m.parts...)
	cuts := append([]sdf.Shape(nil), m.cuts...)
	box := parts[0].Box
	lip := 1.0
	for _, p := range parts {
		box = box.Union(p.Box)
		lip = math.Max(lip, p.Lip)
	}
	for _, c := range cuts {
		lip = math.Max(lip, c.Lip)
	}
	farD := m.Far
	if farD <= 0 {
		farD = 4
	}
	return sdf.Shape{
		F: func(q sdf.Vec3) float64 {
			d := farD
			for i := range parts {
				if boxDist(q, parts[i].Box) >= d {
					continue
				}
				if v := parts[i].F(q); v < d {
					d = v
				}
			}
			for i := range cuts {
				// Outside its box a cutter changes nothing (it is positive
				// there, and only a point inside the solid deeper than its
				// box is far could take its value). Inside its box it is
				// always read: answering a mere bound next to the surface
				// would let a later displacement make surface out of it.
				if bd := boxDist(q, cuts[i].Box); bd > 0 && bd >= -d {
					continue
				}
				if v := -cuts[i].F(q); v > d {
					d = v
				}
			}
			return d
		},
		Box: box,
		Lip: lip,
	}
}

// Face is a point folded onto one face of a rectangle of half-extents
// (hx, hz) about (cx, cz): S along the face (from its middle, ≥ 0), N out of
// the face plane, Y up; Half the face's half-length; X tells whether it is an
// x face (±x) rather than a z face.
type Face struct {
	S, N, Y, Half float64
	X             bool
}

// FoldRect folds q onto the nearest face of the rectangle.
func FoldRect(q sdf.Vec3, cx, cz, hx, hz float64) Face {
	ax, az := math.Abs(q[0]-cx), math.Abs(q[2]-cz)
	if ax-hx > az-hz {
		return Face{S: az, N: ax - hx, Y: q[1], Half: hz, X: true}
	}
	return Face{S: ax, N: az - hz, Y: q[1], Half: hx}
}

// RepeatAlong is s brought into one cell of a row of n features spaced
// period apart, centred on the face's middle (s = 0): the offset from the
// nearest feature's centre. n ≤ 0 is unbounded.
func RepeatAlong(s, period float64, n int) float64 {
	if n == 1 {
		return s
	}
	if n <= 0 {
		return s - period*math.Round(s/period)
	}
	// Centres at (i − (n−1)/2)·period; s ≥ 0 by the fold.
	off := float64(n-1) / 2
	i := math.Round(s/period + off)
	i = sdf.Clamp(i, 0, float64(n-1))
	return s - (i-off)*period
}

// Arch2D is the 2D distance to an arch opening in (s, y): half-width w,
// straight sides from y = 0 to the springing at hs, a round head (pointed
// when point > 0: two arcs of radius w·(1 + point) meeting at the apex).
func Arch2D(s, y, w, hs, point float64) float64 {
	as := math.Abs(s)
	// The straight part.
	bx, by := as-w, math.Max(-y, y-hs)
	box := math.Min(math.Max(bx, by), 0) + math.Hypot(math.Max(bx, 0), math.Max(by, 0))
	var head float64
	if point <= 0 {
		head = math.Max(math.Hypot(as, y-hs)-w, hs-y)
	} else {
		r := w * (1 + point)
		// The arc whose centre is on the far side: (w − r) from the middle.
		head = math.Max(math.Hypot(as-(w-r), y-hs)-r, hs-y)
		head = math.Max(head, -as) // both sides are mirrors
	}
	return math.Min(box, head)
}

// ArchTop is the arch's height (its apex over its sill).
func ArchTop(w, hs, point float64) float64 {
	if point <= 0 {
		return hs + w
	}
	r := w * (1 + point)
	return hs + math.Sqrt(r*r-(r-w)*(r-w))
}

// Box2 is the 2D distance to a rectangle of half-extents (hx, hy) about the
// origin.
func Box2(x, y, hx, hy float64) float64 {
	dx, dy := math.Abs(x)-hx, math.Abs(y)-hy
	return math.Min(math.Max(dx, dy), 0) + math.Hypot(math.Max(dx, 0), math.Max(dy, 0))
}

// Extrude is a 2D distance d2 given depth along the third axis: inside where
// n is between lo and hi.
func Extrude(d2, n, lo, hi float64) float64 {
	dn := math.Max(lo-n, n-hi)
	return math.Min(math.Max(d2, dn), 0) + math.Hypot(math.Max(d2, 0), math.Max(dn, 0))
}

// Box3 is an axis-aligned box about c with half-extents h, as a shape
// (sdf.Cuboid's alias for the vocabulary here).
func Box3(c, h sdf.Vec3) sdf.Shape { return sdf.Cuboid(c, h) }
