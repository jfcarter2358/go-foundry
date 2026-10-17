package sdf

// chips.go — spalls: the shallow, sharp-rimmed scoops that come off a rock's
// edges and corners when it is knocked or frost-split (conchoidal flakes on a
// fine-grained stone, ragged spalls on a coarse one).
//
// A chip is a flattened ellipsoid lying along the surface — wide across it,
// thin along the normal — centred a little outside, subtracted with a small
// rounding. Where a chip goes is chosen from the shape itself: candidate
// points are found on the surface by sphere tracing in from random directions,
// and the most CONVEX of them (a Laplacian of the distance) are kept, because
// that is where rock breaks: an edge sticks out to be hit.

import (
	"math"

	"foundry/internal/procgen/noise"
)

// Chip is one spall.
type Chip struct {
	Center Vec3
	// Normal is the surface normal where it was taken; Radius its size
	// across the surface; Depth along the normal.
	Normal        Vec3
	Radius, Depth float64
	// Aspect, when set, is the chip's height across the surface as a
	// fraction of its width (an alcove is wider than it is tall); 0 is 1.
	Aspect float64
	frame  Mat3 // world → chip (normal along z)
}

// ChipOptions place chips.
type ChipOptions struct {
	Seed  uint64
	Count int
	// Radius range across the surface, metres; DepthRatio the depth as a
	// fraction of the radius.
	MinRadius, MaxRadius, DepthRatio float64
	// Candidates is how many surface points to choose among (0: 12 a chip).
	Candidates int
}

// PlaceChips finds where opt.Count chips go on s.
func PlaceChips(s Shape, opt ChipOptions) []Chip {
	if opt.Count <= 0 || s.Box.IsInfinite() {
		return nil
	}
	rng := noise.NewRNG(opt.Seed)
	c := s.Box.Center()
	R := s.Box.Size().Len()
	n := opt.Candidates
	if n <= 0 {
		n = 12 * opt.Count
	}
	type cand struct {
		p, n  Vec3
		convx float64
	}
	var cs []cand
	for i := 0; i < n; i++ {
		// A uniform direction.
		z := rng.Range(-1, 1)
		a := rng.Range(0, 2*math.Pi)
		r := math.Sqrt(1 - z*z)
		dir := Vec3{r * math.Cos(a), z, r * math.Sin(a)}
		p, ok := traceIn(s, c.Add(dir.Mul(R)), dir.Mul(-1), 2*R)
		if !ok {
			continue
		}
		e := 0.02 * R
		nrm := s.Gradient(p, e*0.25)
		cs = append(cs, cand{p, nrm, s.Laplacian(p, e)})
	}
	// The most convex first.
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && cs[j].convx > cs[j-1].convx; j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
	var out []Chip
	for _, k := range cs {
		if len(out) == opt.Count {
			break
		}
		rad := rng.Range(opt.MinRadius, opt.MaxRadius)
		// Keep chips apart.
		near := false
		for _, o := range out {
			if o.Center.Sub(k.p).Len() < (o.Radius+rad)*0.8 {
				near = true
				break
			}
		}
		if near {
			continue
		}
		depth := rad * opt.DepthRatio
		ch := Chip{Normal: k.n, Radius: rad, Depth: depth}
		ch.Center = k.p.Add(k.n.Mul(depth * 0.35))
		ch.frame = frameAlong(k.n, rng.Range(0, 2*math.Pi))
		out = append(out, ch)
	}
	return out
}

// ChipAt is a chip at surface point p of s (its normal from the field):
// radius across the surface, depth along the normal, turned by a — for a
// caller placing its own (an alcove where it wants one).
func ChipAt(s Shape, p Vec3, radius, depth, a float64) Chip {
	n := s.Gradient(p, 0.01*radius)
	return Chip{Center: p.Add(n.Mul(depth * 0.35)), Normal: n, Radius: radius, Depth: depth, frame: frameAlong(n, a)}
}

// TraceIn is the first surface point of s from o along d (unit), within
// far.
func TraceIn(s Shape, o, d Vec3, far float64) (Vec3, bool) { return traceIn(s, o, d, far) }

// frameAlong is a rotation taking n to +Z, turned by a about it.
func frameAlong(n Vec3, a float64) Mat3 {
	n = n.Normalize()
	up := Vec3{0, 1, 0}
	if math.Abs(n[1]) > 0.9 {
		up = Vec3{1, 0, 0}
	}
	u := up.Cross(n).Normalize()
	v := n.Cross(u)
	s, c := math.Sincos(a)
	u, v = u.Mul(c).Add(v.Mul(s)), v.Mul(c).Sub(u.Mul(s))
	return Mat3{{u[0], u[1], u[2]}, {v[0], v[1], v[2]}, {n[0], n[1], n[2]}}
}

// traceIn sphere-traces from o along d (unit) for at most far, and returns
// the first surface point.
func traceIn(s Shape, o, d Vec3, far float64) (Vec3, bool) {
	t := 0.0
	l := math.Max(s.Lip, 1)
	for i := 0; i < 256 && t < far; i++ {
		p := o.Add(d.Mul(t))
		v := s.F(p)
		if v < 1e-4*far {
			return p, true
		}
		t += v / l
	}
	return Vec3{}, false
}

// CutChips subtracts the chips from s, their rims rounded over round.
func CutChips(s Shape, chips []Chip, round float64) Shape {
	if len(chips) == 0 {
		return s
	}
	f := s.F
	return Shape{
		F: func(p Vec3) float64 {
			d := f(p)
			for i := range chips {
				ch := &chips[i]
				q := p.Sub(ch.Center)
				if q.Dot(q) > (ch.Radius+round)*(ch.Radius+round)*1.2 {
					continue
				}
				l := ch.frame.Apply(q)
				// The flattened ellipsoid, as a bounded distance.
				ry := ch.Radius
				if ch.Aspect > 0 {
					ry *= ch.Aspect
				}
				r := Vec3{ch.Radius, ry, ch.Depth}
				k0 := Vec3{l[0] / r[0], l[1] / r[1], l[2] / r[2]}.Len()
				k1 := Vec3{l[0] / (r[0] * r[0]), l[1] / (r[1] * r[1]), l[2] / (r[2] * r[2])}.Len()
				e := -ch.Depth
				if k1 > 0 {
					e = k0 * (k0 - 1) / k1
				}
				d = smaxK(d, -e, round)
			}
			return d
		},
		Box: s.Box,
		Lip: lip(s.Lip) * 1.5,
	}
}

// Facet is a PLANAR spall: a flat-floored scar where a slab came away along
// a fracture plane — on a cliff a smooth new face metres across with a
// curved, sharp wall where the slab broke off; on a boulder one of the many
// small flat faces that make broken rock angular. It is the rock beyond a
// plane (tilted from the surface by up to the options' MaxTilt) within a
// ball about a surface point, cut away.
type Facet struct {
	Point, Normal Vec3 // a point on the plane, and its normal (out of the rock)
	Center        Vec3 // the ball's centre (on the surface)
	Radius        float64
	// Seed wavers the scar's outline: its wall is the ball's surface pushed
	// in and out by a third of the radius, so no two scars are circles.
	Seed uint64
}

// FacetOptions place facets.
type FacetOptions struct {
	Seed  uint64
	Count int
	// Radius range of the scar, metres; Depth range as a fraction of the
	// radius; MaxTilt the plane's largest tilt from the surface (radians).
	MinRadius, MaxRadius float64
	MinDepth, MaxDepth   float64
	MaxTilt              float64
	// Bias, when set, weighs where facets go: a candidate point is kept
	// with probability Bias(p, n) (0..1) — a cliff's scars on its front,
	// not its back.
	Bias func(p, n Vec3) float64
}

// PlaceFacets finds where opt.Count facets go on s.
func PlaceFacets(s Shape, opt FacetOptions) []Facet {
	if opt.Count <= 0 || s.Box.IsInfinite() {
		return nil
	}
	rng := noise.NewRNG(opt.Seed)
	c := s.Box.Center()
	R := s.Box.Size().Len()
	var out []Facet
	for tries := 0; tries < opt.Count*20 && len(out) < opt.Count; tries++ {
		z := rng.Range(-1, 1)
		a := rng.Range(0, 2*math.Pi)
		r := math.Sqrt(1 - z*z)
		dir := Vec3{r * math.Cos(a), z, r * math.Sin(a)}
		p, ok := traceIn(s, c.Add(dir.Mul(R)), dir.Mul(-1), 2*R)
		if !ok {
			continue
		}
		n := s.Gradient(p, 0.002*R)
		if opt.Bias != nil && rng.Float64() > opt.Bias(p, n) {
			continue
		}
		rad := rng.Range(opt.MinRadius, opt.MaxRadius)
		depth := rad * rng.Range(opt.MinDepth, opt.MaxDepth)
		// The plane: the surface's tangent plane, tilted about a random
		// axis in it, lowered by depth.
		axis := n.Cross(Vec3{rng.Range(-1, 1), rng.Range(-1, 1), rng.Range(-1, 1)}).Normalize()
		pn := RotAxis(axis, rng.Range(0, opt.MaxTilt)).Apply(n).Normalize()
		out = append(out, Facet{Point: p.Sub(n.Mul(depth)), Normal: pn, Center: p, Radius: rad, Seed: rng.Uint64()})
	}
	return out
}

// CutFacets cuts the facets out of s, the scars' walls and floors meeting
// over round.
func CutFacets(s Shape, facets []Facet, round float64) Shape {
	if len(facets) == 0 {
		return s
	}
	f := s.F
	return Shape{
		F: func(p Vec3) float64 {
			d := f(p)
			for i := range facets {
				fa := &facets[i]
				q := p.Sub(fa.Center)
				lim := fa.Radius*1.4 + round
				if q.Dot(q) > lim*lim {
					continue
				}
				beyond := -p.Sub(fa.Point).Dot(fa.Normal) // < 0 beyond the plane
				k := 1.1 / fa.Radius
				wobble := 0.75*noise.Gradient3(fa.Seed, q[0]*k, q[1]*k, q[2]*k) + 0.25*noise.Gradient3(fa.Seed+1, q[0]*k*2.7, q[1]*k*2.7, q[2]*k*2.7)
				ball := q.Len() - fa.Radius*(1+0.28*wobble)
				cut := math.Max(beyond, ball)
				d = smaxK(d, -cut, round)
			}
			return d
		},
		Box: s.Box,
		Lip: lip(s.Lip) * 1.5,
	}
}
