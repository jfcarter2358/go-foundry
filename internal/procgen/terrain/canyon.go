package terrain

// canyon.go — cutting canyons along spline paths.
//
// A canyon is a path of control points, each with a width (rim to rim) and
// a depth (below the ground around it). Carving it:
//
//  1. DENSIFY. A Catmull-Rom curve through the points, sampled every few
//     metres, carrying width and depth along.
//  2. MEANDER. Each dense point is pushed sideways by noise of arc length
//     (Meander metres, MeanderLength wavelength), and the width breathes by
//     ±20%, so a three-point path becomes a sinuous gorge.
//  3. GRADE. The floor's height along the path is the ground at the rims
//     (sampled on the uncarved surface, smoothed along the path) less the
//     depth, then forced to never rise downstream: a canyon is cut by water,
//     and water does not run uphill. The path's direction is downstream.
//  4. BRANCH. Tributaries leave the path at intervals, heading upstream at
//     an angle and wandering, narrower and shallower, their floors graded
//     down to the main floor where they join (a little above it: a short
//     hanging step, the way side canyons often meet the main one).
//  5. CUT. Every sample within half a width of a path takes
//
//         h = min(h, floor + (h - floor)·(1 - p(u)))
//
//     where u is the distance from the centreline over the half width and
//     p the cross-section: 1 at the centre (all the way to the floor), 0 at
//     the rim (untouched). V, U, slot and stepped are built in; custom is a
//     curve. Floor widens the flat bottom.
//
// The cut is applied row by row in parallel: every path's segments are
// binned by the rows their reach covers, so a row only meets the segments
// near it, and each sample takes the minimum over all of them — the order
// does not matter, so neither does the scheduling.

import (
	"math"

	"foundry/internal/procgen/noise"
)

// CarvedPath is a carved centreline: what the preview draws and what later
// stages (cliff walls along the canyon) follow.
type CarvedPath struct {
	// Points are world XZ, downstream.
	Points [][2]float64
	// Width and Floor are per point: rim-to-rim width and floor height.
	Width, Floor []float64
	// Tributary marks a branch rather than an authored path.
	Tributary bool
}

// densePoint is a point of a densified path.
type densePoint struct {
	x, z, w, d float64
	s          float64 // arc length
	floor      float64
}

// cutPath is a path ready to cut.
type cutPath struct {
	pts  []densePoint
	prof func(u float64) float64
	maxW float64
	trib bool
}

// profileFunc is the cross-section p(u) for c.
func profileFunc(c *Canyon) func(u float64) float64 {
	fl := min(max(c.Floor, 0), 0.95)
	steps := max(c.Steps, 1)
	custom := c.Custom
	kind := c.Profile
	return func(u float64) float64 {
		u = max(0, (u-fl)/(1-fl))
		if u >= 1 {
			return 0
		}
		switch kind {
		case ProfileU:
			return 1 - u*u*u
		case ProfileSlot:
			return 1 - noise.SmoothStep(0.8, 1, u)
		case ProfileStepped:
			x := u * float64(steps)
			k := math.Floor(x)
			return 1 - (k+noise.SmoothStep(0.55, 0.95, x-k))/float64(steps)
		case ProfileCustom:
			if custom != nil {
				return min(max(custom(u), 0), 1)
			}
		}
		return 1 - u
	}
}

// catmull is the Catmull-Rom point between p1 and p2 at t.
func catmull(p0, p1, p2, p3, t float64) float64 {
	t2, t3 := t*t, t*t*t
	return 0.5 * (2*p1 + (-p0+p2)*t + (2*p0-5*p1+4*p2-p3)*t2 + (-p0+3*p1-3*p2+p3)*t3)
}

// densify samples the curve through ctrl every spacing metres or so.
func densify(ctrl []PathPoint, spacing float64) []densePoint {
	if len(ctrl) == 1 {
		p := ctrl[0]
		return []densePoint{{x: p.X, z: p.Z, w: p.Width, d: p.Depth}}
	}
	at := func(k int) PathPoint { return ctrl[min(max(k, 0), len(ctrl)-1)] }
	var out []densePoint
	for k := 0; k+1 < len(ctrl); k++ {
		p0, p1, p2, p3 := at(k-1), at(k), at(k+1), at(k+2)
		l := math.Hypot(p2.X-p1.X, p2.Z-p1.Z)
		n := max(int(math.Ceil(l/spacing)), 1)
		for q := 0; q < n; q++ {
			t := float64(q) / float64(n)
			out = append(out, densePoint{
				x: catmull(p0.X, p1.X, p2.X, p3.X, t),
				z: catmull(p0.Z, p1.Z, p2.Z, p3.Z, t),
				w: p1.Width + (p2.Width-p1.Width)*t,
				d: p1.Depth + (p2.Depth-p1.Depth)*t,
			})
		}
	}
	last := ctrl[len(ctrl)-1]
	out = append(out, densePoint{x: last.X, z: last.Z, w: last.Width, d: last.Depth})
	arcLengths(out)
	return out
}

func arcLengths(pts []densePoint) {
	s := 0.0
	for i := range pts {
		if i > 0 {
			s += math.Hypot(pts[i].x-pts[i-1].x, pts[i].z-pts[i-1].z)
		}
		pts[i].s = s
	}
}

// tangent is the unit direction of pts at i.
func tangent(pts []densePoint, i int) (float64, float64) {
	a, b := pts[max(i-1, 0)], pts[min(i+1, len(pts)-1)]
	dx, dz := b.x-a.x, b.z-a.z
	l := math.Hypot(dx, dz)
	if l == 0 {
		return 1, 0
	}
	return dx / l, dz / l
}

// meander pushes the points sideways by noise of arc length.
func meander(pts []densePoint, amp, wavelength float64, seed uint64) {
	if amp <= 0 || wavelength <= 0 || len(pts) < 3 {
		return
	}
	f := noise.Fractal{Seed: seed, Octaves: 2, Frequency: 1 / wavelength, Gain: 0.4}
	wf := noise.Fractal{Seed: noise.Hash64(seed, 7), Octaves: 2, Frequency: 2 / wavelength}
	type off struct{ dx, dz float64 }
	offs := make([]off, len(pts))
	for i := range pts {
		tx, tz := tangent(pts, i)
		// Taper to nothing at the ends, so a path authored to run off the map
		// edge still leaves it where it was drawn to.
		taper := noise.SmoothStep(0, wavelength*0.5, pts[i].s) * noise.SmoothStep(0, wavelength*0.5, pts[len(pts)-1].s-pts[i].s)
		m := amp * 1.6 * f.At2(pts[i].s, 0.37) * taper
		offs[i] = off{-tz * m, tx * m}
	}
	for i := range pts {
		pts[i].x += offs[i].dx
		pts[i].z += offs[i].dz
		pts[i].w *= 1 + 0.2*wf.At2(pts[i].s, 1.7)
	}
	arcLengths(pts)
}

// grade sets each point's floor: rim ground less depth, smoothed, never
// rising downstream; and at least atLeast[end] where a tributary joins.
func grade(pts []densePoint, g *Grid, join float64, hasJoin bool) {
	raw := make([]float64, len(pts))
	for i := range pts {
		tx, tz := tangent(pts, i)
		nx, nz := -tz, tx
		hw := pts[i].w / 2
		sum := 0.0
		for _, f := range []float64{-1, -0.5, 0.5, 1} {
			sum += float64(g.Sample(pts[i].x+nx*hw*f, pts[i].z+nz*hw*f))
		}
		raw[i] = sum/4 - pts[i].d
	}
	// Smooth over about three widths.
	sm := make([]float64, len(pts))
	for i := range pts {
		win := pts[i].w * 1.5
		sum, n := 0.0, 0
		for k := i; k >= 0 && pts[i].s-pts[k].s <= win; k-- {
			sum += raw[k]
			n++
		}
		for k := i + 1; k < len(pts) && pts[k].s-pts[i].s <= win; k++ {
			sum += raw[k]
			n++
		}
		sm[i] = sum / float64(n)
	}
	// Never rising downstream, then smoothed again over about ten widths:
	// a moving average of a non-increasing sequence is non-increasing, so
	// the floor stays graded and loses the steps the running minimum makes.
	lowest := math.Inf(1)
	for i := range pts {
		lowest = min(lowest, sm[i])
		raw[i] = lowest
	}
	for i := range pts {
		win := pts[i].w * 5
		sum, n := 0.0, 0
		for k := i; k >= 0 && pts[i].s-pts[k].s <= win; k-- {
			sum += raw[k]
			n++
		}
		for k := i + 1; k < len(pts) && pts[k].s-pts[i].s <= win; k++ {
			sum += raw[k]
			n++
		}
		pts[i].floor = sum / float64(n)
	}
	// The window is cut short at the ends, which can lift a point a little:
	// once more never rising.
	for i := 1; i < len(pts); i++ {
		pts[i].floor = min(pts[i].floor, pts[i-1].floor)
	}
	if hasJoin && len(pts) > 1 {
		// A tributary never cuts below the floor it drains to, and grades
		// its last 40% down toward the join, ending just above it (a short
		// hanging step); then once more never rising.
		end := pts[len(pts)-1].s
		start := end * 0.6
		target := join + 0.08*pts[len(pts)-1].d
		for i := range pts {
			pts[i].floor = max(pts[i].floor, join)
			if pts[i].s >= start {
				t := noise.SmoothStep(start, end, pts[i].s)
				pts[i].floor = min(pts[i].floor, pts[i].floor+(target-pts[i].floor)*t)
			}
			if i > 0 {
				pts[i].floor = min(pts[i].floor, pts[i-1].floor)
			}
		}
	}
}

// tributaries grows c's branches off the main path pts.
func tributaries(c *Canyon, pts []densePoint, g *Grid, spacing float64) []cutPath {
	if c.Tributaries <= 0 || len(pts) < 4 {
		return nil
	}
	rng := noise.NewRNG(noise.Hash64(c.Seed, 0x5452_4942))
	total := pts[len(pts)-1].s
	side := 1.0
	if rng.Bool() {
		side = -1
	}
	var out []cutPath
	wander := noise.Fractal{Seed: noise.Hash64(c.Seed, 0x5754_4E44), Octaves: 2, Frequency: 1 / 60.0}
	for k := 0; k < c.Tributaries; k++ {
		s := total * (float64(k) + 0.5 + rng.Range(-0.3, 0.3)) / float64(c.Tributaries)
		i := 0
		for i+1 < len(pts) && pts[i].s < s {
			i++
		}
		jp := pts[i]
		tx, tz := tangent(pts, i)
		// Heading upstream from the join, turned off the main line.
		ang := side * rng.Range(35, 70) * math.Pi / 180
		hx, hz := -tx, -tz
		ca, sa := math.Cos(ang), math.Sin(ang)
		hx, hz = hx*ca-hz*sa, hx*sa+hz*ca
		side = -side
		length := max(c.TributaryLength, 1) * rng.Range(0.55, 1)
		// Walk outward from the join, then reverse so the path runs
		// downstream, toward the main canyon.
		var walk []densePoint
		x, z := jp.x, jp.z
		heading := math.Atan2(hz, hx)
		n := max(int(length/spacing), 3)
		for q := 0; q <= n; q++ {
			f := float64(q) / float64(n)
			walk = append(walk, densePoint{
				x: x, z: z,
				w: jp.w * (0.5 - 0.35*f),
				d: jp.d * (0.8 - 0.7*f),
			})
			heading += 0.9 * wander.At2(float64(k)*31.7+float64(q)*spacing/60, 0) * spacing / 25
			x += math.Cos(heading) * spacing
			z += math.Sin(heading) * spacing
		}
		for a, b := 0, len(walk)-1; a < b; a, b = a+1, b-1 {
			walk[a], walk[b] = walk[b], walk[a]
		}
		arcLengths(walk)
		grade(walk, g, jp.floor, true)
		out = append(out, cutPath{pts: walk, prof: profileFunc(c), trib: true})
	}
	return out
}

// carveCanyons cuts every canyon into g and returns the paths as carved.
func carveCanyons(s *Settings, g *Grid, pool Pool) ([]CarvedPath, error) {
	var paths []cutPath
	var carved []CarvedPath
	for ci := range s.Canyons {
		c := &s.Canyons[ci]
		if len(c.Path) < 2 {
			continue
		}
		minW := math.Inf(1)
		for _, p := range c.Path {
			minW = min(minW, p.Width)
		}
		spacing := min(max(minW/4, g.Cell), 4)
		pts := densify(c.Path, spacing)
		meander(pts, c.Meander, c.MeanderLength, noise.Hash64(c.Seed, 0x4D45_414E))
		grade(pts, g, 0, false)
		paths = append(paths, cutPath{pts: pts, prof: profileFunc(c)})
		paths = append(paths, tributaries(c, pts, g, spacing)...)
	}
	for pi := range paths {
		p := &paths[pi]
		cp := CarvedPath{Tributary: p.trib}
		for _, q := range p.pts {
			p.maxW = max(p.maxW, q.w)
			cp.Points = append(cp.Points, [2]float64{q.x, q.z})
			cp.Width = append(cp.Width, q.w)
			cp.Floor = append(cp.Floor, q.floor)
		}
		carved = append(carved, cp)
	}
	if len(paths) == 0 {
		return nil, nil
	}
	// Bin segments by the rows they reach.
	type segRef struct{ path, seg int32 }
	s1 := g.S()
	binned := make([][]segRef, s1)
	for pi := range paths {
		p := &paths[pi]
		for k := 0; k+1 < len(p.pts); k++ {
			a, b := p.pts[k], p.pts[k+1]
			r := max(a.w, b.w) / 2
			j0 := int(math.Floor((min(a.z, b.z) - r - g.MinZ) / g.Cell))
			j1 := int(math.Ceil((max(a.z, b.z) + r - g.MinZ) / g.Cell))
			for j := max(j0, 0); j <= min(j1, s1-1); j++ {
				binned[j] = append(binned[j], segRef{int32(pi), int32(k)})
			}
		}
	}
	err := rows(pool, s1, func(j int) {
		z := g.Z(j)
		row := g.H[j*s1 : (j+1)*s1]
		for _, ref := range binned[j] {
			p := &paths[ref.path]
			a, b := p.pts[ref.seg], p.pts[ref.seg+1]
			r := max(a.w, b.w) / 2
			i0 := int(math.Floor((min(a.x, b.x) - r - g.MinX) / g.Cell))
			i1 := int(math.Ceil((max(a.x, b.x) + r - g.MinX) / g.Cell))
			dx, dz := b.x-a.x, b.z-a.z
			l2 := dx*dx + dz*dz
			for i := max(i0, 0); i <= min(i1, s1-1); i++ {
				x := g.X(i)
				t := 0.0
				if l2 > 0 {
					t = min(max(((x-a.x)*dx+(z-a.z)*dz)/l2, 0), 1)
				}
				px, pz := a.x+dx*t, a.z+dz*t
				w := a.w + (b.w-a.w)*t
				d := math.Hypot(x-px, z-pz)
				if d >= w/2 || w <= 0 {
					continue
				}
				pr := p.prof(d / (w / 2))
				if pr <= 0 {
					continue
				}
				floor := a.floor + (b.floor-a.floor)*t
				h := float64(row[i])
				target := floor + (h-floor)*(1-pr)
				if target < h {
					row[i] = float32(target)
				}
			}
		}
	})
	return carved, err
}
