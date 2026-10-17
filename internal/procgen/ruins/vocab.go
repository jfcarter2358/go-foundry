package ruins

// vocab.go — the architectural vocabulary: moulding sequences on a profile,
// rows of carved niches and blind arches, rows of added ornament (dentils,
// merlons), the miniature shrines that crown a tier's corners, a tower's
// crown (neck, ribbed amalaka, pot, finial) and columns.
//
// Every dimension is in metres times k, the vocabulary's scale, so the same
// cornice serves a tower's main storey (k = 1) and its turrets (k ≈ 0.3).

import (
	"math"

	"foundry/internal/procgen/sdf"
)

// Plinth draws a temple base from the current point (which should be about
// 0.5k out of the wall u0): a slab, a chamfer, a torus, a recessed band, a
// slab and a sloped fillet into the wall. 1.4k tall.
func (b *ProfileBuilder) Plinth(u0, k float64) *ProfileBuilder {
	b.Up(0.3*k).Slope(0.08*k, u0+0.4*k).To(u0+0.28*k).Torus(0.32*k, 0.14*k, 4).To(u0 + 0.1*k).Up(0.28 * k)
	b.To(u0+0.34*k).Up(0.14*k).Slope(0.18*k, u0+0.12*k).Up(0.1 * k).To(u0)
	return b
}

// SmallBase is a tier's base: a fillet and a chamfer into the wall. 0.25k.
func (b *ProfileBuilder) SmallBase(u0, k float64) *ProfileBuilder {
	return b.To(u0+0.16*k).Up(0.15*k).Slope(0.1*k, u0)
}

// Cornice draws a cornice from the wall u0: an architrave band, a recessed
// band (where dentils go: CorniceDentilBand says where), and a cavetto — a
// cove sweeping out to an overhang — with a drip edge and a sloped top,
// ending at the top's inner edge (u0 + 0.5k). About 1.0k tall.
func (b *ProfileBuilder) Cornice(u0, k float64) *ProfileBuilder {
	b.To(u0 + 0.06*k).Up(0.28 * k).To(u0).Up(0.2 * k).To(u0 + 0.1*k)
	b.ArcV(0.34*k, u0+0.62*k, 6).Up(0.12 * k).To(u0 + 0.5*k).Up(0.06 * k)
	return b
}

// CorniceDentilBand is the recessed band of a Cornice started at y: its
// bottom and top.
func CorniceDentilBand(y, k float64) (lo, hi float64) { return y + 0.28*k, y + 0.48*k }

// CorniceHeight is a Cornice's height.
func CorniceHeight(k float64) float64 { return 1.0 * k }

// PlinthHeight is a Plinth's height.
func PlinthHeight(k float64) float64 { return 1.4 * k }

// Row is a row of features along the faces of a rectangle (half-extents HX,
// HZ about CX, CZ): Count per face (0: as many as fit) spaced Period apart,
// within SMax of each face's middle, on the z faces, the x faces or both.
type Row struct {
	CX, CZ, HX, HZ float64
	Period         float64
	Count          int
	SMax           float64
	ZFaces, XFaces bool
}

func (r Row) count(half float64) int {
	if r.Count > 0 {
		return r.Count
	}
	sm := r.SMax
	if sm <= 0 {
		sm = half
	}
	return max(int(math.Floor(2*sm/r.Period)), 1)
}

// fold is q on the row's faces: the offset from the nearest feature's
// centre along the face, the distance out of the face, and false where the
// face is not in the row.
func (r Row) fold(q sdf.Vec3) (s, n float64, ok bool) {
	f := FoldRect(q, r.CX, r.CZ, r.HX, r.HZ)
	if f.X && !r.XFaces || !f.X && !r.ZFaces {
		return 0, 0, false
	}
	n0 := r.count(f.Half)
	return RepeatAlong(f.S, r.Period, n0), f.N, true
}

// Niches cuts a row of arched recesses: sill at y0, half-width w, springing
// hs over the sill, pointedness point, depth into the face (through: the
// cut reaches depth and beyond, for an opening through a wall).
func Niches(r Row, y0, w, hs, point, depth float64) sdf.Shape {
	top := ArchTop(w, hs, point)
	return sdf.Shape{
		F: func(q sdf.Vec3) float64 {
			s, n, ok := r.fold(q)
			if !ok {
				return 1
			}
			return Extrude(Arch2D(s, q[1]-y0, w, hs, point), n, -depth, 2)
		},
		Box: sdf.Box{Lo: sdf.Vec3{r.CX - r.HX - 2, y0, r.CZ - r.HZ - 2}, Hi: sdf.Vec3{r.CX + r.HX + 2, y0 + top, r.CZ + r.HZ + 2}},
		Lip: 1,
	}
}

// Blocks adds a row of boxes standing out of the faces: half-width w along
// the face, from y0 to y1, from depth in to out out of the face.
func Blocks(r Row, y0, y1, w, out, depth float64) sdf.Shape {
	hy := (y1 - y0) / 2
	return sdf.Shape{
		F: func(q sdf.Vec3) float64 {
			s, n, ok := r.fold(q)
			if !ok {
				return 1
			}
			return Extrude(Box2(s, q[1]-y0-hy, w, hy), n, -depth, out)
		},
		Box: sdf.Box{Lo: sdf.Vec3{r.CX - r.HX - out, y0, r.CZ - r.HZ - out}, Hi: sdf.Vec3{r.CX + r.HX + out, y1, r.CZ + r.HZ + out}},
		Lip: 1,
	}
}

// Merlons adds stepped (crow-step) merlons along the faces, standing from
// y0: half-width w, height h, in two steps, thickness t centred on the face
// line (n = −t/2 .. t/2 about the face's offset off).
func Merlons(r Row, y0, w, h, t, off float64) sdf.Shape {
	return sdf.Shape{
		F: func(q sdf.Vec3) float64 {
			s, n, ok := r.fold(q)
			if !ok {
				return 1
			}
			y := q[1] - y0
			lower := Box2(s, y-h*0.3, w, h*0.3)
			upper := Box2(s, y-h*0.8, w*0.6, h*0.2)
			return Extrude(math.Min(lower, upper), n, off-t/2, off+t/2)
		},
		Box: sdf.Box{Lo: sdf.Vec3{r.CX - r.HX - math.Abs(off) - t, y0, r.CZ - r.HZ - math.Abs(off) - t},
			Hi: sdf.Vec3{r.CX + r.HX + math.Abs(off) + t, y0 + h, r.CZ + r.HZ + math.Abs(off) + t}},
		Lip: 1,
	}
}

// Mirror4 places shape s (modelled about the corner (cx+dx, ·, cz+dz)) at
// the four corners (cx±dx, cz±dz): s is evaluated with the point folded into
// the +x +z quadrant. Far from the corner it answers the distance to s's box.
func Mirror4(s sdf.Shape, cx, cz, dx, dz float64) sdf.Shape {
	f := s.F
	b := s.Box
	return sdf.Shape{
		F: func(q sdf.Vec3) float64 {
			p := sdf.Vec3{cx + math.Abs(q[0]-cx), q[1], cz + math.Abs(q[2]-cz)}
			if d := boxDist(p, b); d > 0.25 {
				return d
			}
			return f(p)
		},
		Box: sdf.Box{Lo: sdf.Vec3{2*cx - b.Hi[0], b.Lo[1], 2*cz - b.Hi[2]}, Hi: sdf.Vec3{b.Hi[0], b.Hi[1], b.Hi[2]}},
		Lip: s.Lip,
	}
}

// Turret is a miniature shrine standing at c (its base): a square body of
// half-side h·0.42 with a small base and cornice, a cushion dome and a
// finial; h is its height.
func Turret(c sdf.Vec3, h float64) sdf.Shape {
	k := h / 2.4
	hs := 0.42 * k
	pb := NewProfile(0, 0.16*k).SmallBase(0, k).Up(0.6 * k)
	pb.Cornice(0, 0.5*k).To(0.1*k).ArcV(0.7*k, -hs, 5)
	body := Lathe(RectPlan(hs, hs), pb.Build(), c)
	top := pb.Y()
	fin := NewProfile(top-0.05*k, -hs+0.02*k).To(-hs+0.08*k).Up(0.08*k).ArcH(0.1*k, -hs+0.14*k, 3).ArcV(0.25*k, -hs, 4)
	finial := Lathe(CirclePlan(hs), fin.Build(), c)
	m := &Model{Far: 4}
	m.Add(body, finial)
	return m.Shape()
}

// Crown is a tower's crown standing at c: a neck, a ribbed amalaka of
// radius r, a pot and a finial spike. Its height is about 2.6r.
func Crown(c sdf.Vec3, r float64) sdf.Shape {
	// The amalaka: a ribbed cushion on a neck.
	ne := NewProfile(0, -0.45*r).Up(0.25*r).To(-0.3*r).ArcH(0.32*r, 0, 4).ArcV(0.32*r, -0.42*r, 4).Up(0.08 * r)
	amalaka := Lathe(RibbedPlan(r, 28, 0.06*r), ne.Build(), c)
	// The pot (kalasha) and the spike, on a plain disc.
	y := ne.Y()
	pr := r * 0.5
	pot := NewProfile(y-0.02*r, -0.15*pr).To(0.1*pr).Up(0.06*r).To(-0.3*pr).ArcH(0.25*r, 0.15*pr, 4).ArcV(0.3*r, -0.6*pr, 4).
		Slope(0.12*r, -0.75*pr).To(-0.5*pr).Up(0.06*r).To(-0.85*pr).Slope(0.55*r, -0.97*pr)
	potS := Lathe(CirclePlan(pr), pot.Build(), c)
	m := &Model{Far: 4}
	m.Add(amalaka, potS)
	return m.Shape()
}

// CrownHeight is a Crown's height for radius r.
func CrownHeight(r float64) float64 { return 0.98*r + 0.06*r + 0.71*r + 0.18*r + 0.55*r }

// Column is a column standing at c, height h, shaft radius r: a square
// base with mouldings, an octagonal lower shaft, a fluted round upper shaft,
// a cushion capital and a cross-shaped bracket. Its top is at c.y + h.
func Column(c sdf.Vec3, h, r float64) sdf.Shape {
	k := r / 0.3
	base := NewProfile(0, 0.18*k).Up(0.25*k).Slope(0.08*k, 0.1*k).Up(0.5*k).Slope(0.06*k, 0.02*k).Up(0.05 * k)
	baseS := Lathe(RectPlan(r, r), base.Build(), c)
	y1 := base.Y()
	oct := NewProfile(y1-0.02, -0.02*k).Up(h*0.32).Slope(0.08*k, -0.08*k).Up(0.04 * k)
	octS := Lathe(OctPlan(r*0.98), oct.Build(), c)
	y2 := oct.Y()
	capY := h - 0.75*k
	shaft := NewProfile(y2-0.02, -0.12*k).To(-0.04*k).Up(0.08*k).To(-0.1*k).Slope(capY-y2-0.2*k, -0.16*k).
		To(-0.06 * k).Up(0.08 * k).To(-0.14 * k).Up(0.04 * k)
	shaftS := Lathe(RibbedPlan(r*0.98, 16, -0.035*k), shaft.Build(), c)
	cap := NewProfile(capY-0.02, -0.12*k).ArcH(0.14*k, 0.12*k, 4).ArcV(0.16*k, -0.05*k, 4).Up(0.04 * k)
	capS := Lathe(CirclePlan(r), cap.Build(), c)
	// The bracket: a cross of corbels with sloped undersides, and an abacus.
	yb := cap.Y()
	br := NewProfile(yb-0.02, -0.25*r).Slope(0.22*k, 0.35*k).Up(0.18 * k).To(0.45 * k).Up(h - yb - 0.4*k + 0.02)
	brS := Lathe(CrossPlan(r*0.6, r*0.6, r*0.6, r*0.6, r*0.45, r*0.45), br.Build(), c)
	m := &Model{Far: 4}
	m.Add(baseS, octS, shaftS, capS, brS)
	return m.Shape()
}
