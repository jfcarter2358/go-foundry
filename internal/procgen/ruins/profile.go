// Package ruins is the `ruins` stage's architecture (docs/adventure/
// PHASE_D.md): ancient sandstone buildings as signed-distance shapes — stepped
// temple towers with cornices, carved friezes, domes and finials, pavilions on
// columns, gateways, crenellated walls, fallen columns and rubble — and the
// layout that lays them out along a path down a slope: terraces held by
// retaining walls, stairs between them, plinths under the buildings that stand
// out on the slope.
//
// It is pure computation on internal/procgen/sdf and noise (layering-asserted):
// the stage meshes, bakes, cooks and places what it describes.
//
// # The modelling idea
//
// Temple architecture is horizontal mouldings wrapped round a plan: a base
// of slabs, tori and coves, a wall, a cornice that overhangs on a curved
// underside, and then the same again smaller, tier on tier. So the main
// primitive here is a LATHE over a plan rather than a revolution: a 2D plan
// shape (a rectangle, a stepped cross, a circle) gives every point a plan
// distance u (Chebyshev for the rectilinear plans, so the mouldings stay
// mitred at the corners), and a PROFILE — a polyline in (u, y) — says how far
// out of the plan the surface stands at each height. One profile is the
// whole stack of a tower's mouldings; offsetting in is a tier's setback.
// Carving (niches, blind arches, openings) is subtracted in a FACE frame —
// the plan folded so every face is the +z one — with repetition along the
// face; the small repeated ornaments (dentils, merlons, corner turrets) are
// added the same way. Everything a primitive here returns is a bound on the
// distance (Lipschitz 1 or close), so the mesher's block skipping holds.
package ruins

import (
	"math"
	"sort"

	"foundry/internal/procgen/sdf"
)

// Plan is a 2D signed distance in plan (x, z), negative inside, with the
// half-extents of its bounding rectangle.
type Plan struct {
	D      func(x, z float64) float64
	HX, HZ float64
}

// RectPlan is a rectangle of half-extents hx, hz: the Chebyshev distance
// max(|x|−hx, |z|−hz), so an offset of it keeps square corners.
func RectPlan(hx, hz float64) Plan {
	return Plan{D: func(x, z float64) float64 { return math.Max(math.Abs(x)-hx, math.Abs(z)-hz) }, HX: hx, HZ: hz}
}

// CrossPlan is a rectangle (hx, hz) with a projection in the middle of each
// face: px out from the x faces over a half-width wz, pz out from the z faces
// over a half-width wx — the stepped plan of a temple (its rathas).
func CrossPlan(hx, hz, px, pz, wx, wz float64) Plan {
	return Plan{D: func(x, z float64) float64 {
		ax, az := math.Abs(x), math.Abs(z)
		a := math.Max(ax-hx, az-hz)
		b := math.Max(ax-(hx+px), az-wz)
		c := math.Max(ax-wx, az-(hz+pz))
		return math.Min(a, math.Min(b, c))
	}, HX: hx + px, HZ: hz + pz}
}

// CirclePlan is a disc of radius r.
func CirclePlan(r float64) Plan {
	return Plan{D: func(x, z float64) float64 { return math.Hypot(x, z) - r }, HX: r, HZ: r}
}

// RibbedPlan is a disc of radius r with n rounded ribs standing depth out of
// it (an amalaka's ridges, a column's flutes when depth < 0).
func RibbedPlan(r float64, n int, depth float64) Plan {
	k := float64(n)
	return Plan{D: func(x, z float64) float64 {
		a := math.Atan2(z, x)
		rib := math.Abs(math.Cos(a * k / 2))
		return math.Hypot(x, z) - r - depth*(rib*rib)
	}, HX: r + math.Max(depth, 0), HZ: r + math.Max(depth, 0)}
}

// OctPlan is a regular octagon of inradius r.
func OctPlan(r float64) Plan {
	const c = 0.70710678118654752
	return Plan{D: func(x, z float64) float64 {
		ax, az := math.Abs(x), math.Abs(z)
		return math.Max(math.Max(ax, az), (ax+az)*c) - r
	}, HX: r, HZ: r}
}

// Profile is a lathe's outline in (u, y): how far out of the plan the surface
// stands at each height, from y0 to y1, closed across the top and the bottom
// (the inside is u < the outline). Built by a ProfileBuilder.
type Profile struct {
	pts    [][2]float64
	segs   [][4]float64 // u0, y0, u1, y1
	y0, y1 float64
	umax   float64
	binH   float64
	bins   [][]int32
}

// far is how far inside the plan the closing segments reach (any plan
// distance is above −far).
const far = 1e4

// cap is the distance beyond which a profile reports a bound rather than
// the exact distance (see Dist).
const profileCap = 8.0

// ProfileBuilder draws a profile from the bottom up.
type ProfileBuilder struct {
	pts [][2]float64
}

// NewProfile starts a profile at height y, u out of the plan.
func NewProfile(y, u float64) *ProfileBuilder { return &ProfileBuilder{pts: [][2]float64{{u, y}}} }

func (b *ProfileBuilder) last() [2]float64 { return b.pts[len(b.pts)-1] }

// U and Y are where the outline has got to.
func (b *ProfileBuilder) U() float64 { return b.last()[0] }
func (b *ProfileBuilder) Y() float64 { return b.last()[1] }

// Up rises h at the current offset.
func (b *ProfileBuilder) Up(h float64) *ProfileBuilder {
	l := b.last()
	b.pts = append(b.pts, [2]float64{l[0], l[1] + h})
	return b
}

// To steps straight out (or in) to offset u at the current height.
func (b *ProfileBuilder) To(u float64) *ProfileBuilder {
	l := b.last()
	if u != l[0] {
		b.pts = append(b.pts, [2]float64{u, l[1]})
	}
	return b
}

// Slope rises h while the offset goes to u (a chamfer, a batter).
func (b *ProfileBuilder) Slope(h, u float64) *ProfileBuilder {
	l := b.last()
	b.pts = append(b.pts, [2]float64{u, l[1] + h})
	return b
}

// ArcV rises h along a quarter ellipse to offset u, leaving vertically and
// arriving horizontally: a dome's shoulder (u < current), a torus's upper half.
func (b *ProfileBuilder) ArcV(h, u float64, n int) *ProfileBuilder {
	l := b.last()
	du := u - l[0]
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n) * math.Pi / 2
		b.pts = append(b.pts, [2]float64{l[0] + du*(1-math.Cos(t)), l[1] + h*math.Sin(t)})
	}
	return b
}

// ArcH rises h along a quarter ellipse to offset u, leaving horizontally and
// arriving vertically: a cornice's curved underside (u > current), a torus's
// lower half.
func (b *ProfileBuilder) ArcH(h, u float64, n int) *ProfileBuilder {
	l := b.last()
	du := u - l[0]
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n) * math.Pi / 2
		b.pts = append(b.pts, [2]float64{l[0] + du*math.Sin(t), l[1] + h*(1-math.Cos(t))})
	}
	return b
}

// Torus is a half-round bead of height h standing out by out.
func (b *ProfileBuilder) Torus(h, out float64, n int) *ProfileBuilder {
	u := b.U()
	b.ArcH(h/2, u+out, n)
	return b.ArcV(h/2, u, n)
}

// Build closes the profile and indexes it.
func (b *ProfileBuilder) Build() *Profile {
	pts := b.pts
	p := &Profile{pts: pts, y0: pts[0][1], y1: pts[len(pts)-1][1]}
	p.segs = append(p.segs, [4]float64{-far, p.y0, pts[0][0], p.y0})
	for i := 1; i < len(pts); i++ {
		a, c := pts[i-1], pts[i]
		if a == c {
			continue
		}
		p.segs = append(p.segs, [4]float64{a[0], a[1], c[0], c[1]})
	}
	l := pts[len(pts)-1]
	p.segs = append(p.segs, [4]float64{l[0], p.y1, -far, p.y1})
	p.umax = math.Inf(-1)
	for _, q := range pts {
		p.umax = math.Max(p.umax, q[0])
	}
	h := math.Max(p.y1-p.y0, 1e-3)
	p.binH = math.Max(h/512, 0.05)
	nb := int(math.Ceil(h/p.binH)) + 1
	p.bins = make([][]int32, nb)
	for si, s := range p.segs {
		lo, hi := math.Min(s[1], s[3]), math.Max(s[1], s[3])
		b0, b1 := p.bin(lo), p.bin(hi)
		for k := b0; k <= b1; k++ {
			p.bins[k] = append(p.bins[k], int32(si))
		}
	}
	return p
}

func (p *Profile) bin(y float64) int {
	k := int(math.Floor((y - p.y0) / p.binH))
	return min(max(k, 0), len(p.bins)-1)
}

// Y0, Y1 and UMax are the profile's height range and its furthest offset.
func (p *Profile) Y0() float64   { return p.y0 }
func (p *Profile) Y1() float64   { return p.y1 }
func (p *Profile) UMax() float64 { return p.umax }

// OffsetAt is the outline's offset at height y (between y0 and y1): on a
// horizontal step, the larger.
func (p *Profile) OffsetAt(y float64) float64 {
	pts := p.pts
	k := sort.Search(len(pts), func(i int) bool { return pts[i][1] > y })
	if k <= 0 {
		return pts[0][0]
	}
	if k >= len(pts) {
		return pts[len(pts)-1][0]
	}
	a, c := pts[k-1], pts[k]
	if c[1] == a[1] {
		return math.Max(a[0], c[0])
	}
	t := (y - a[1]) / (c[1] - a[1])
	return a[0] + (c[0]-a[0])*t
}

// Dist is the signed distance from (u, y) to the outline (negative inside),
// exact within profileCap and a lower bound beyond.
func (p *Profile) Dist(u, y float64) float64 {
	in := y > p.y0 && y < p.y1
	best := profileCap
	var off float64
	if in {
		off = p.OffsetAt(y)
		best = fmin(best, fabs(u-off))
		// The closing segments across the top and the bottom.
		if u < p.pts[len(p.pts)-1][0] {
			best = fmin(best, p.y1-y)
		}
		if u < p.pts[0][0] {
			best = fmin(best, y-p.y0)
		}
	} else if y <= p.y0 {
		dx := fmax(u-p.pts[0][0], 0)
		best = fmin(best, math.Sqrt(dx*dx+(p.y0-y)*(p.y0-y)))
	} else {
		dx := fmax(u-p.pts[len(p.pts)-1][0], 0)
		best = fmin(best, math.Sqrt(dx*dx+(y-p.y1)*(y-p.y1)))
	}
	b0, b1 := p.bin(y-best), p.bin(y+best)
	b2 := best * best
	for k := b0; k <= b1; k++ {
		for _, si := range p.bins[k] {
			s := &p.segs[si]
			if d := segDist2(u, y, s); d < b2 {
				b2 = d
			}
		}
	}
	d := math.Sqrt(b2)
	if in && u < off {
		return -d
	}
	return d
}

func segDist2(u, y float64, s *[4]float64) float64 {
	dx, dy := s[2]-s[0], s[3]-s[1]
	px, py := u-s[0], y-s[1]
	l := dx*dx + dy*dy
	t := 0.0
	if l > 0 {
		t = (px*dx + py*dy) / l
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
	}
	ex, ey := px-dx*t, py-dy*t
	return ex*ex + ey*ey
}

func fmin(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func fmax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func fabs(a float64) float64 {
	if a < 0 {
		return -a
	}
	return a
}

// Lathe is the solid the profile sweeps round the plan, centred at c (the
// profile's y is relative to c's).
func Lathe(plan Plan, prof *Profile, c sdf.Vec3) sdf.Shape {
	d := plan.D
	r := prof.UMax()
	return sdf.Shape{
		F: func(q sdf.Vec3) float64 {
			return prof.Dist(d(q[0]-c[0], q[2]-c[2]), q[1]-c[1])
		},
		Box: sdf.Box{Lo: sdf.Vec3{c[0] - plan.HX - r, c[1] + prof.Y0(), c[2] - plan.HZ - r},
			Hi: sdf.Vec3{c[0] + plan.HX + r, c[1] + prof.Y1(), c[2] + plan.HZ + r}},
		Lip: 1,
	}
}
