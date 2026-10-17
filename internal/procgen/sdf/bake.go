package sdf

// bake.go — what a surface point can learn from the field around it, for
// baking into vertex colours: how occluded it is (ambient occlusion), and how
// curved (cavity and edge).
//
// Both read the field rather than the mesh, so they are the same at any
// resolution and see geometry a decimated mesh has lost.
//
//   - AO casts a few short cones over the hemisphere around the normal — the
//     normal itself and a ring tilted 55° — and asks, at a handful of steps
//     out along each, how much room there is: the field's value against the
//     distance travelled. An open face has all of it (the distance grows as
//     fast as the step); a crevice or the underside of an overhang has
//     little. The value at the point itself is subtracted first, so a vertex
//     the mesher's weathering has moved slightly inside the analytic surface
//     is not taken for occluded.
//   - Laplacian is the sum of the field's second derivatives at a radius:
//     positive on convex edges and corners, negative in concave ones, zero on
//     a face. Over a small radius it is the cavity a dirt map shows; over a
//     larger one, the shape's broad hollows and bulges.

import "math"

// Laplacian is (Σ F(p ± r·eᵢ) − 6F(p)) / r: about r times the sum of the
// principal curvatures at the surface (dimensionless: 2r/R on a sphere of
// radius R).
func (s Shape) Laplacian(p Vec3, r float64) float64 {
	f := s.F
	c := f(p)
	sum := f(Vec3{p[0] + r, p[1], p[2]}) + f(Vec3{p[0] - r, p[1], p[2]}) +
		f(Vec3{p[0], p[1] + r, p[2]}) + f(Vec3{p[0], p[1] - r, p[2]}) +
		f(Vec3{p[0], p[1], p[2] + r}) + f(Vec3{p[0], p[1], p[2] - r})
	return (sum - 6*c) / r
}

// aoRing is the tilted cones' directions in the normal's frame: a ring of
// six at 55° from it.
var aoRing = func() [6]Vec3 {
	var r [6]Vec3
	s, c := math.Sincos(55 * math.Pi / 180)
	for i := range r {
		a := float64(i) * math.Pi / 3
		r[i] = Vec3{s * math.Cos(a), s * math.Sin(a), c}
	}
	return r
}()

// AO is the ambient occlusion at surface point p with normal n, out to
// radius: 1 open, 0 enclosed. steps is the samples a cone (0: 5).
func (s Shape) AO(p, n Vec3, radius float64, steps int) float64 {
	if steps <= 0 {
		steps = 5
	}
	n = n.Normalize()
	base := s.F(p)
	frame := frameAlong(n, 0).T() // chip frame → world
	dirs := [7]Vec3{n}
	for i, d := range aoRing {
		dirs[i+1] = frame.Apply(d)
	}
	// Cosine weights: the normal's cone counts most.
	weights := [7]float64{1, 0.57, 0.57, 0.57, 0.57, 0.57, 0.57}
	vis, wsum := 0.0, 0.0
	for di, d := range dirs {
		v := 1.0
		// On an open plane a step t along d gains t·cos(d, n) of distance:
		// room is the share of that it has.
		cos := math.Max(d.Dot(n), 0.2)
		for k := 1; k <= steps; k++ {
			t := radius * float64(k*k) / float64(steps*steps)
			q := p.Add(d.Mul(t))
			room := (s.F(q) - base) / (t * cos)
			v = math.Min(v, Clamp(room, 0, 1))
		}
		vis += weights[di] * v
		wsum += weights[di]
	}
	return vis / wsum
}
