package collision

// shape.go — convex volumes, described the one way that lets a single overlap
// routine serve every pair of them.
//
// A convex shape is fully characterised by its *support function*: given a
// direction, the farthest point of the shape along it. Every bounding kind the
// engine has answers that in closed form, and GJK/EPA (collide_gjk.go) needs
// nothing else. The alternative — a hand-written test per pair — is fifteen
// routines for four kinds, and the moment a rotated box enters (which a 6DOF
// hull is) most of them stop being closed-form anyway.
//
// The kinds mirror world.BoundingType exactly, including its convention that
// the half-extents are the box the shape is *inscribed in*. So a "sphere" whose
// extents differ per axis is the ellipsoid inside that box, and a cylinder is
// upright with an elliptical cross-section — the same reading world.ShapeContainsPoint
// gives, which is what keeps a script's `enter` and the resolver's push-apart
// talking about the same volume.

import (
	"foundry/internal/geom"
	"foundry/internal/world"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// Shape is a convex volume that can report its farthest point in a direction.
//
// Support need not return a *unique* maximiser — when a direction is
// perpendicular to a flat face, any point on that face will do, and callers
// must not assume otherwise.
type Shape interface {
	Support(dir mgl32.Vec3) mgl32.Vec3
	Center() mgl32.Vec3
}

// ---------- box ----------

// Box is an axis-aligned box. It is kept distinct from an OBB with an identity
// rotation because it is overwhelmingly the common case and its support is
// three sign tests rather than two matrix multiplies.
type Box struct {
	C    mgl32.Vec3
	Half mgl32.Vec3
}

func (b Box) Center() mgl32.Vec3 { return b.C }

func (b Box) Support(dir mgl32.Vec3) mgl32.Vec3 {
	return b.C.Add(mgl32.Vec3{
		signedExtent(dir.X(), b.Half.X()),
		signedExtent(dir.Y(), b.Half.Y()),
		signedExtent(dir.Z(), b.Half.Z()),
	})
}

// signedExtent picks the box face the direction points at. A zero component is
// a tie between two faces; picking the positive one is arbitrary and fine.
func signedExtent(d, half float32) float32 {
	if d < 0 {
		return -half
	}
	return half
}

// ---------- oriented box ----------

// OBB is a box with an arbitrary rotation — what a hull that can pitch and roll
// needs. Rot is the shape's local-to-world rotation; its transpose is its
// inverse, since a rotation matrix is orthonormal.
type OBB struct {
	C    mgl32.Vec3
	Half mgl32.Vec3
	Rot  mgl32.Mat4
}

func (o OBB) Center() mgl32.Vec3 { return o.C }

func (o OBB) Support(dir mgl32.Vec3) mgl32.Vec3 {
	local := rotateInv(o.Rot, dir)
	corner := mgl32.Vec3{
		signedExtent(local.X(), o.Half.X()),
		signedExtent(local.Y(), o.Half.Y()),
		signedExtent(local.Z(), o.Half.Z()),
	}
	return o.C.Add(rotate(o.Rot, corner))
}

// ---------- ellipsoid ----------

// Ellipsoid is the "sphere" kind: the ellipsoid inscribed in the half-extent
// box, optionally rotated. Equal extents make it an actual sphere.
type Ellipsoid struct {
	C    mgl32.Vec3
	Half mgl32.Vec3
	Rot  mgl32.Mat4
}

func (e Ellipsoid) Center() mgl32.Vec3 { return e.C }

// Support solves the ellipsoid case directly. For E = {C + R·M·u : |u| ≤ 1}
// with M = diag(half), the farthest point along d is C + R·M·v̂ where
// v = Mᵀ·Rᵀ·d — that is, scale the direction into the unit sphere's space,
// normalise there, and scale back.
func (e Ellipsoid) Support(dir mgl32.Vec3) mgl32.Vec3 {
	local := rotateInv(e.Rot, dir)
	v := mgl32.Vec3{
		local.X() * e.Half.X(),
		local.Y() * e.Half.Y(),
		local.Z() * e.Half.Z(),
	}
	n := v.Len()
	if n < 1e-12 {
		// A zero direction has no farthest point; the centre is in the shape
		// and so is always a legal answer.
		return e.C
	}
	v = v.Mul(1 / n)
	p := mgl32.Vec3{v.X() * e.Half.X(), v.Y() * e.Half.Y(), v.Z() * e.Half.Z()}
	return e.C.Add(rotate(e.Rot, p))
}

// ---------- cylinder ----------

// Cylinder is upright in its own space: an elliptical cross-section in XZ with
// flat caps in Y, matching world.ShapeContainsPoint's reading of the kind.
type Cylinder struct {
	C    mgl32.Vec3
	Half mgl32.Vec3
	Rot  mgl32.Mat4
}

func (c Cylinder) Center() mgl32.Vec3 { return c.C }

// Support treats the two axes independently, which is what a prism is: the cap
// contributes ±half.Y, and the rim contributes the 2D elliptical support. When
// the direction is purely vertical the whole cap ties, and the cap centre is as
// good an answer as any point on its rim.
func (c Cylinder) Support(dir mgl32.Vec3) mgl32.Vec3 {
	local := rotateInv(c.Rot, dir)

	p := mgl32.Vec3{0, signedExtent(local.Y(), c.Half.Y()), 0}

	vx := local.X() * c.Half.X()
	vz := local.Z() * c.Half.Z()
	if n := float32(math.Hypot(float64(vx), float64(vz))); n > 1e-12 {
		p[0] = (vx / n) * c.Half.X()
		p[2] = (vz / n) * c.Half.Z()
	}
	return c.C.Add(rotate(c.Rot, p))
}

// ---------- convex hull ----------

// ConvexHull is an arbitrary convex solid described by its vertices directly —
// a world.Brush's own Verts, which the brush format requires to all sit on the
// hull (see brush.go's file comment), so no separate hull computation is
// needed: every point IS a candidate support point.
type ConvexHull struct {
	Pts []mgl32.Vec3
	// Xform places Pts, for a brush a script has moved (world.Brush.Placement).
	// The ZERO matrix means "no transform" — not the zero transform, which
	// would collapse the solid to a point — so that the overwhelmingly common
	// ConvexHull{Pts: ...} keeps meaning what it always did.
	//
	// A transform rather than a translation because a dynamic brush can turn as
	// well as slide, and because carrying one costs the same as carrying the
	// other: the support function below transforms two vectors either way.
	Xform mgl32.Mat4
}

// placed reports whether Xform is a transform at all, and returns it.
func (h ConvexHull) placed() (mgl32.Mat4, bool) {
	if h.Xform == (mgl32.Mat4{}) || h.Xform == mgl32.Ident4() {
		return mgl32.Ident4(), false
	}
	return h.Xform, true
}

// Center is the average of the hull's points. Not the exact centroid of the
// solid (which would need to weight by volume), but GJK only uses Center to
// seed its first search direction, where an approximation costs nothing.
func (h ConvexHull) Center() mgl32.Vec3 {
	if len(h.Pts) == 0 {
		return mgl32.Vec3{}
	}
	var sum mgl32.Vec3
	for _, p := range h.Pts {
		sum = sum.Add(p)
	}
	c := sum.Mul(1 / float32(len(h.Pts)))
	if m, ok := h.placed(); ok {
		r := m.Mul4x1(c.Vec4(1))
		return mgl32.Vec3{r.X(), r.Y(), r.Z()}
	}
	return c
}

// Support is the brute-force answer — the farthest point among Pts along dir
// — which is all a convex hull's support function can be without a
// half-edge structure to walk. Brushes are small (a handful to a few dozen
// verts), so this is cheap; a level with many brushes bounds the cost with
// the spatial hash before this is ever called, the same way a sector's wall
// scan used to be bounded to its own polygon.
func (h ConvexHull) Support(dir mgl32.Vec3) mgl32.Vec3 {
	m, placed := h.placed()
	if placed {
		// support_{A·S}(d) = A · support_S(Aᵀ·d), for any invertible linear A.
		// Searching in the brush's own space and carrying the winner out is
		// what keeps a moved brush from needing a transformed copy of its
		// vertex list per query.
		dir = transposeMulDir(m, dir)
	}
	best := h.Pts[0]
	bestDot := best.Dot(dir)
	for _, p := range h.Pts[1:] {
		if d := p.Dot(dir); d > bestDot {
			best, bestDot = p, d
		}
	}
	if placed {
		r := m.Mul4x1(best.Vec4(1))
		return mgl32.Vec3{r.X(), r.Y(), r.Z()}
	}
	return best
}

// ---------- helpers ----------

// transposeMulDir applies the transpose of m's linear part to a direction. For
// a rotation that is the inverse; for a general affine placement it is what the
// support-function identity in ConvexHull.Support asks for, which is not the
// same thing and is deliberately not spelled as an inverse.
func transposeMulDir(m mgl32.Mat4, d mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{
		m[0]*d[0] + m[1]*d[1] + m[2]*d[2],
		m[4]*d[0] + m[5]*d[1] + m[6]*d[2],
		m[8]*d[0] + m[9]*d[1] + m[10]*d[2],
	}
}

// rotate and rotateInv apply a rotation matrix and its inverse to a direction.
// A rotation is orthonormal, so the inverse is the transpose — no matrix
// inversion is involved, which matters because this runs inside GJK's loop.
func rotate(m mgl32.Mat4, v mgl32.Vec3) mgl32.Vec3 {
	r := m.Mul4x1(v.Vec4(0))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

func rotateInv(m mgl32.Mat4, v mgl32.Vec3) mgl32.Vec3 {
	r := m.Transpose().Mul4x1(v.Vec4(0))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// ShapeFor builds the collision shape for an entity's bounding volume.
//
// ok is false when no volume is authored — an entity with no bounds has nothing
// to collide with, and a zero-extent shape would make the support function
// degenerate rather than simply small.
//
// This lives here rather than as a method on world.Entity because world sits
// below collision in the package layering: an entity describes its volume, and
// this package decides what to do about it.
func ShapeFor(e *world.Entity) (Shape, bool) {
	if e == nil || !e.HasBounds() {
		return nil, false
	}
	c := e.BoundCenter()
	h := e.BoundHalfExtents()
	rot, rotated := entityRotation(e)

	switch e.BoundingKind {
	case world.BoundingSphere:
		return Ellipsoid{C: c, Half: h, Rot: rot}, true
	case world.BoundingCylinder:
		return Cylinder{C: c, Half: h, Rot: rot}, true
	default:
		if !rotated {
			return Box{C: c, Half: h}, true
		}
		return OBB{C: c, Half: h, Rot: rot}, true
	}
}

// entityRotation reports an entity's rotation matrix and whether it is actually
// rotated. The flag is what lets the axis-aligned fast paths stay reachable
// rather than being buried under an identity matrix.
func entityRotation(e *world.Entity) (mgl32.Mat4, bool) {
	if e.Rotation == (mgl32.Vec3{}) {
		return mgl32.Ident4(), false
	}
	return world.EulerMatrixYXZ(e.Rotation), true
}

// ShapeAABB is the exact world-space box a convex shape spans, read straight off
// its support function along the six axis directions. Exact for every kind here,
// and the reason no shape needs to carry a bounds method of its own.
func ShapeAABB(s Shape) (min, max mgl32.Vec3) {
	for axis := 0; axis < 3; axis++ {
		var pos, neg mgl32.Vec3
		pos[axis], neg[axis] = 1, -1
		max[axis] = s.Support(pos)[axis]
		min[axis] = s.Support(neg)[axis]
	}
	return min, max
}

// IsAxisAlignedBox reports whether a shape is a plain Box. The push-apart uses
// it to stay on the min-axis path it was tuned against for the overwhelmingly
// common case, rather than routing every crate in every level through GJK.
func IsAxisAlignedBox(s Shape) bool {
	_, ok := s.(Box)
	return ok
}

// boxOf reduces any shape to the axis-aligned box it spans — the conservative
// reading a caller falls back to when it wants extents rather than a surface.
func boxOf(s Shape) (c, half mgl32.Vec3) {
	min, max := ShapeAABB(s)
	c = min.Add(max).Mul(0.5)
	half = mgl32.Vec3{
		geom.Abs32(max.X()-min.X()) * 0.5,
		geom.Abs32(max.Y()-min.Y()) * 0.5,
		geom.Abs32(max.Z()-min.Z()) * 0.5,
	}
	return c, half
}

// Translate returns the same shape moved by d. Shapes are values, so this is a
// copy rather than a mutation — which is what lets a sweep advance a candidate
// position without disturbing the one the caller still holds.
func Translate(s Shape, d mgl32.Vec3) Shape {
	switch v := s.(type) {
	case Box:
		v.C = v.C.Add(d)
		return v
	case OBB:
		v.C = v.C.Add(d)
		return v
	case Ellipsoid:
		v.C = v.C.Add(d)
		return v
	case Cylinder:
		v.C = v.C.Add(d)
		return v
	case ConvexHull:
		// Translating the placement rather than the points when there is one:
		// the points are the caller's authored slice, shared with the brush
		// itself, and rewriting a copy of it per step of a sweep is the cost
		// the placement exists to avoid.
		if m, ok := v.placed(); ok {
			v.Xform = mgl32.Translate3D(d.X(), d.Y(), d.Z()).Mul4(m)
			return v
		}
		pts := make([]mgl32.Vec3, len(v.Pts))
		for i, p := range v.Pts {
			pts[i] = p.Add(d)
		}
		return ConvexHull{Pts: pts}
	}
	return s
}
