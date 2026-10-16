package terrain

// river_path.go — a river spec's centreline: resampled, meandered (only on
// request), oriented downhill and cut where it meets standing water
// (river.go, step 1).
//
// RESAMPLING. The spec's points are the knots of a uniform Catmull-Rom
// spline (the ends repeated), walked densely and then resampled at an even
// arc-length step of half a cell, so the carve and the ribbon see the same
// smooth curve whatever spacing the author's points had. Width and depth
// are interpolated linearly between the knots by the curve's parameter.
//
// MEANDER. Only when the spec's shape asks (Meander > 0): a lateral offset
// of two sines in no simple ratio, tapered to nothing over half a
// wavelength at each end so the source and the mouth (a confluence) stay
// where they were put. A painted river has none by default: its author drew
// the bends they wanted.
//
// DOWNHILL. The ground under the first and last seventh of the path is
// compared; a path whose start is lower is reversed. The author drags the
// brush whichever way; the water knows which way is down.
//
// STANDING WATER. A station over a pond or lake (the basins' WaterResult)
// is water already: the path is split into the runs between such stretches.
// A run that leaves a body starts at the body's level (its spill), one that
// enters a body ends at its level (river_hydro.go).

import (
	"fmt"
	"math"

	"foundry/internal/procgen/noise"
)

// riverRun is one run of a river being solved.
type riverRun struct {
	name     string
	spec     *RiverSpec
	specIdx  int
	st       []RiverStation
	ds       float64
	reversed bool
	// curv is the smoothed signed curvature per station (1/m, + turning
	// left); plunge the extra depth a plunge pool adds.
	curv, plunge []float64
	// into is the run this one ends on (-1 none), at station intoAt.
	into, intoAt int
	intoName     string
	// fromWater / intoWater are the standing water bodies it leaves and
	// enters (-1 none), their levels startCap / endLevel (NaN none).
	fromWater, intoWater int
	startCap, endLevel   float64
	falls                []Waterfall
	boulders             []Boulder
	// rapids are the steep stretches too long to be falls (S from, to).
	rapids [][2]float64
}

// buildRuns resamples, orients and splits one spec.
func buildRuns(g *Grid, sp *RiverSpec, si int, water *WaterResult) []*riverRun {
	pts := make([][2]float64, 0, len(sp.Points))
	var ws, ds []float64
	for i, p := range sp.Points {
		if n := len(pts); n > 0 && math.Hypot(p[0]-pts[n-1][0], p[1]-pts[n-1][1]) < 1e-6 {
			continue
		}
		pts = append(pts, p)
		ws = append(ws, pick(sp.Width, i, 4))
		ds = append(ds, pick(sp.Depth, i, 1))
	}
	if len(pts) < 2 {
		return nil
	}
	step := g.Cell * 0.5
	st := resampleCurve(pts, ws, ds, step)
	if sp.Shape.Meander > 0 && len(st) > 2 {
		st = meanderStations(st, sp.Shape, sp.Seed, step)
	}
	if len(st) < 3 {
		return nil
	}
	headings(st)
	for i := range st {
		st[i].Ground = channelGround(g, &st[i])
	}
	reversed := false
	n7 := max(len(st)/7, 1)
	var a, b float64
	for i := 0; i < n7; i++ {
		a += st[i].Ground
		b += st[len(st)-1-i].Ground
	}
	if a < b {
		reversed = true
		for i, j := 0, len(st)-1; i < j; i, j = i+1, j-1 {
			st[i], st[j] = st[j], st[i]
		}
		headings(st)
	}
	// Split at standing water.
	bodyAt := func(r *RiverStation) int {
		if water == nil {
			return -1
		}
		i := int(math.Round((r.X - g.MinX) / g.Cell))
		j := int(math.Round((r.Z - g.MinZ) / g.Cell))
		s := g.S()
		if i < 0 || j < 0 || i >= s || j >= s {
			return -1
		}
		return int(water.Body[j*s+i])
	}
	levelOf := func(b int) float64 {
		if b < 0 || water == nil || b >= len(water.Bodies) {
			return math.NaN()
		}
		return water.Bodies[b].Level
	}
	var runs []*riverRun
	from := -1
	for i := 0; i < len(st); {
		if b := bodyAt(&st[i]); b >= 0 {
			from = b
			i++
			continue
		}
		j := i
		for j < len(st) && bodyAt(&st[j]) < 0 {
			j++
		}
		into := -1
		if j < len(st) {
			into = bodyAt(&st[j])
		}
		seg := append([]RiverStation(nil), st[i:j]...)
		if len(seg) >= 3 && float64(len(seg)-1)*step >= 2*g.Cell {
			name := sp.Name
			if len(runs) > 0 {
				name = fmt.Sprintf("%s.%d", sp.Name, len(runs)+1)
			}
			for k := range seg {
				seg[k].S = float64(k) * step
			}
			runs = append(runs, &riverRun{name: name, spec: sp, specIdx: si, st: seg, ds: step, reversed: reversed, into: -1,
				fromWater: from, intoWater: into, startCap: levelOf(from), endLevel: levelOf(into)})
		}
		from = -1
		i = j
	}
	for _, r := range runs {
		r.curv = curvature(r.st, r.ds)
		r.plunge = make([]float64, len(r.st))
	}
	return runs
}

// pick is v[i], the last entry past the end, def when empty; never below
// a small positive floor.
func pick(v []float64, i int, def float64) float64 {
	if len(v) == 0 {
		return def
	}
	return math.Max(v[min(i, len(v)-1)], 0.05)
}

// resampleCurve walks the Catmull-Rom spline through pts densely and
// resamples it every step metres.
func resampleCurve(pts [][2]float64, ws, ds []float64, step float64) []RiverStation {
	n := len(pts)
	at := func(i int) [2]float64 { return pts[min(max(i, 0), n-1)] }
	type dense struct{ x, z, w, d float64 }
	var dn []dense
	for i := 0; i+1 < n; i++ {
		p0, p1, p2, p3 := at(i-1), at(i), at(i+1), at(i+2)
		segLen := math.Hypot(p2[0]-p1[0], p2[1]-p1[1])
		m := max(int(math.Ceil(segLen/(step*0.25))), 1)
		for k := 0; k < m; k++ {
			t := float64(k) / float64(m)
			x := catmull(p0[0], p1[0], p2[0], p3[0], t)
			z := catmull(p0[1], p1[1], p2[1], p3[1], t)
			dn = append(dn, dense{x, z, ws[i] + (ws[i+1]-ws[i])*t, ds[i] + (ds[i+1]-ds[i])*t})
		}
	}
	dn = append(dn, dense{pts[n-1][0], pts[n-1][1], ws[n-1], ds[n-1]})
	out := []RiverStation{{X: dn[0].x, Z: dn[0].z, Width: dn[0].w, Depth: dn[0].d}}
	acc := 0.0 // arc length since the last station
	for k := 1; k < len(dn); k++ {
		a, b := dn[k-1], dn[k]
		l := math.Hypot(b.x-a.x, b.z-a.z)
		for l > 0 && acc+l >= step {
			t := (step - acc) / l
			x, z := a.x+(b.x-a.x)*t, a.z+(b.z-a.z)*t
			w, d := a.w+(b.w-a.w)*t, a.d+(b.d-a.d)*t
			out = append(out, RiverStation{X: x, Z: z, Width: w, Depth: d})
			a = dense{x, z, w, d}
			l = math.Hypot(b.x-a.x, b.z-a.z)
			acc = 0
		}
		acc += l
	}
	// The last point itself, unless the last station is almost on it.
	last := dn[len(dn)-1]
	if p := out[len(out)-1]; math.Hypot(last.x-p.X, last.z-p.Z) > step*0.25 {
		out = append(out, RiverStation{X: last.x, Z: last.z, Width: last.w, Depth: last.d})
	}
	for i := range out {
		out[i].S = float64(i) * step
	}
	return out
}

// (catmull is canyon.go's.)

// headings sets each station's unit heading (central differences).
func headings(st []RiverStation) {
	n := len(st)
	for i := range st {
		a, b := st[max(i-1, 0)], st[min(i+1, n-1)]
		dx, dz := b.X-a.X, b.Z-a.Z
		if l := math.Hypot(dx, dz); l > 1e-9 {
			st[i].TX, st[i].TZ = dx/l, dz/l
		} else {
			st[i].TX, st[i].TZ = 1, 0
		}
	}
}

// meanderStations displaces the path sideways (file comment) and
// resamples it.
func meanderStations(st []RiverStation, sh RiverShape, seed uint64, step float64) []RiverStation {
	headings(st)
	L := math.Max(sh.MeanderLength, 4)
	total := st[len(st)-1].S
	r := noise.Hash64(seed, 0x6d65616e)
	ph1 := float64(r%6283) / 1000
	ph2 := float64((r>>16)%6283) / 1000
	pts := make([][2]float64, len(st))
	ws := make([]float64, len(st))
	ds := make([]float64, len(st))
	for i, s := range st {
		taper := noise.SmoothStep(0, L/2, s.S) * noise.SmoothStep(0, L/2, total-s.S)
		a := math.Min(sh.Meander, 1.5*s.Width)
		off := a * taper * (math.Sin(2*math.Pi*s.S/L+ph1) + 0.4*math.Sin(2*math.Pi*s.S/(0.53*L)+ph2)) / 1.4
		// Left of the heading is (-TZ, TX) (the sign convention of
		// lateral distances throughout).
		pts[i] = [2]float64{s.X - s.TZ*off, s.Z + s.TX*off}
		ws[i], ds[i] = s.Width, s.Depth
	}
	return resamplePolyline(pts, ws, ds, step)
}

// resamplePolyline is resampleCurve along straight segments (the points
// are already dense).
func resamplePolyline(pts [][2]float64, ws, ds []float64, step float64) []RiverStation {
	out := []RiverStation{{X: pts[0][0], Z: pts[0][1], Width: ws[0], Depth: ds[0]}}
	acc := 0.0
	for k := 1; k < len(pts); k++ {
		ax, az, aw, ad := pts[k-1][0], pts[k-1][1], ws[k-1], ds[k-1]
		bx, bz := pts[k][0], pts[k][1]
		l := math.Hypot(bx-ax, bz-az)
		for l > 0 && acc+l >= step {
			t := (step - acc) / l
			ax, az = ax+(bx-ax)*t, az+(bz-az)*t
			aw, ad = aw+(ws[k]-aw)*t, ad+(ds[k]-ad)*t
			out = append(out, RiverStation{X: ax, Z: az, Width: aw, Depth: ad})
			l = math.Hypot(bx-ax, bz-az)
			acc = 0
		}
		acc += l
	}
	for i := range out {
		out[i].S = float64(i) * step
	}
	return out
}

// channelGround is the lowest ground across the middle of the channel at a
// station: the line the water follows.
func channelGround(g *Grid, s *RiverStation) float64 {
	lo := math.Inf(1)
	for _, f := range [5]float64{-0.3, -0.15, 0, 0.15, 0.3} {
		o := f * s.Width
		lo = math.Min(lo, float64(g.Sample(s.X-s.TZ*o, s.Z+s.TX*o)))
	}
	return lo
}

// curvature is the signed curvature per station (1/m, + turning left),
// smoothed over about a width.
func curvature(st []RiverStation, step float64) []float64 {
	n := len(st)
	raw := make([]float64, n)
	for i := 1; i+1 < n; i++ {
		a, b := st[i-1], st[i+1]
		raw[i] = (a.TX*b.TZ - a.TZ*b.TX) / (2 * step)
	}
	if n > 2 {
		raw[0], raw[n-1] = raw[1], raw[n-2]
	}
	w := 0.0
	for _, s := range st {
		w += s.Width
	}
	w /= float64(n)
	return boxFilter(raw, max(int(math.Round(w/step)), 1))
}

// boxFilter is v under a centred box of half-width r samples, the ends
// clamped.
func boxFilter(v []float64, r int) []float64 {
	n := len(v)
	out := make([]float64, n)
	if n == 0 {
		return out
	}
	pre := make([]float64, n+1)
	for i, x := range v {
		pre[i+1] = pre[i] + x
	}
	for i := range v {
		a, b := i-r, i+r
		sum := 0.0
		// Clamped ends: the samples past an end repeat it.
		if a < 0 {
			sum += float64(-a) * v[0]
			a = 0
		}
		if b > n-1 {
			sum += float64(b-(n-1)) * v[n-1]
			b = n - 1
		}
		sum += pre[b+1] - pre[a]
		out[i] = sum / float64(2*r+1)
	}
	return out
}
