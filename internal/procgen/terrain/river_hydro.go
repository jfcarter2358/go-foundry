package terrain

// river_hydro.go — a river's water surface and its character (river.go,
// steps 2 and 3).
//
// CONFLUENCES first: a run whose mouth lies within the banks (and a cell
// and a half) of another run's centreline ends on it. The runs are then
// solved in dependency order — a main stem before its tributaries — so a
// tributary's mouth can take the main river's surface at the junction. A
// cycle (two runs each ending on the other) is broken by dropping the edge
// that closes it.
//
// THE SURFACE of a run is the ground along its middle, less the bank
// height, turned into a line that never rises downstream:
//
//	z[0]   = min(target[0], the level of the water it leaves)
//	z[i]   = min(z[i−1] − ε·ds, target[i])          (running minimum)
//	z      = min(z, box(z)), then min(z, box½(z))   (smoothed, still monotone:
//	                                                  a box of a non-rising line
//	                                                  does not rise, nor does
//	                                                  the minimum of two)
//
// and then fitted to its mouth: at a confluence or a pond it must end at
// that level — lowered over a ramp of a few widths when it would arrive
// high, held up to it (backwater) when its own ground is lower.
//
// WATERFALLS. The surface's slope is measured over about a width; a run of
// stations steeper than MaxSlope that drops at least MinFall (with a
// width's margin each side) collapses into one step: the surface holds its
// level to the lip and drops to the foot. Below the lip a plunge pool
// deepens the bed (0.35 × the drop + 0.4 m, at most 3 m) over one and a
// half times the larger of the width and the drop. Only a CONCENTRATED drop
// is a fall: a steep stretch longer than three widths and twice its drop
// (a stream down a valley side) stays a sloping surface — RAPIDS, foamy
// (foam at least 0.6) but not stepped, since holding the surface to the top
// and dropping it at the bottom would cut a slot gorge the length of the
// slope.
//
// THE CHARACTER (shapeRiver):
//
//   - pools and riffles alternate along a phase that advances one turn
//     every RiffleSpacing (default six widths): depth × (1 + ½·Pools·pool
//     − ½·Pools·riffle); none within two widths of a fall;
//   - on a bend the thalweg moves toward the outer bank by up to 0.35 of
//     the half-width (curvature × width), and the inside gets a point bar;
//   - the speed is Speed × Q/(width × local depth), Q the width × depth
//     smoothed over ten widths (a discharge held steady along the river):
//     faster in narrows and over riffles, slow in pools; × √(slope / the
//     river's median slope), bounded to 0.75–1.5; clamped to 0.1–3.8 m/s;
//   - the foam: riffles (more where faster), anything over 2 m/s, a fall's
//     lip (rising over a width and a half above it) and foot (the plunge
//     pool), and the water round a confluence;
//   - boulders in the riffles: Boulders per riffle per 4 m of width, each
//     somewhere within the crest's half-width along and 0.7 of the
//     half-width across, its size by the water's depth there.

import (
	"math"
	"sort"

	"foundry/internal/procgen/noise"
)

// solveConfluences finds which run ends on which and returns the order to
// solve them in (each run after the run it ends on).
func solveConfluences(runs []*riverRun) []int {
	for ri, r := range runs {
		if r.intoWater >= 0 {
			continue
		}
		last := r.st[len(r.st)-1]
		best, bestD, bestJ := -1, math.Inf(1), 0
		for mi, m := range runs {
			if mi == ri || m.spec == r.spec && m.specIdx == r.specIdx && m.name == r.name {
				continue
			}
			for j := range m.st {
				s := &m.st[j]
				d := math.Hypot(s.X-last.X, s.Z-last.Z)
				tol := s.Width/2 + last.Width/2 + 1.5*r.ds*2
				if d <= tol && d < bestD {
					best, bestD, bestJ = mi, d, j
				}
			}
		}
		if best >= 0 {
			r.into, r.intoAt, r.intoName = best, bestJ, runs[best].name
		}
	}
	// Depth-first, main stems first; an edge that closes a cycle is
	// dropped.
	state := make([]int, len(runs)) // 0 new, 1 on the stack, 2 done
	var order []int
	var visit func(i int)
	visit = func(i int) {
		state[i] = 1
		if m := runs[i].into; m >= 0 {
			switch state[m] {
			case 1:
				runs[i].into, runs[i].intoName = -1, ""
			case 0:
				visit(m)
			}
		}
		state[i] = 2
		order = append(order, i)
	}
	for i := range runs {
		if state[i] == 0 {
			visit(i)
		}
	}
	return order
}

// solveSurface sets a run's surface (file comment), its mouth fitted to
// the run it ends on (already solved) or the water it enters.
func solveSurface(r *riverRun, runs []*riverRun, water *WaterResult) {
	st := r.st
	n := len(st)
	sh := r.spec.Shape
	z := make([]float64, n)
	for i := range st {
		t := st[i].Ground - sh.BankHeight
		switch {
		case i == 0:
			z[i] = t
			if !math.IsNaN(r.startCap) {
				z[i] = math.Min(t, r.startCap)
			}
		default:
			z[i] = math.Min(z[i-1]-riverEps*r.ds, t)
		}
	}
	wm := 0.0
	for _, s := range st {
		wm += s.Width
	}
	wm /= float64(n)
	win := max(int(math.Round(noise.Clamp(3*wm, 4, 40)/r.ds/2)), 1)
	z = minSlices(z, boxFilter(z, win))
	z = minSlices(z, boxFilter(z, max(win/2, 1)))

	// The mouth.
	end := math.NaN()
	switch {
	case r.into >= 0:
		end = runs[r.into].st[r.intoAt].Surface
	case r.intoWater >= 0:
		end = r.endLevel
	}
	if !math.IsNaN(end) {
		if z[n-1] > end {
			k := min(max(int(math.Round(noise.Clamp(4*st[n-1].Width, 6, 60)/r.ds)), 1), n-1)
			a := z[n-1-k]
			for i := n - 1 - k; i < n; i++ {
				lin := a + (end-a)*float64(i-(n-1-k))/float64(k)
				z[i] = math.Min(z[i], lin)
			}
		}
		for i := range z {
			z[i] = math.Max(z[i], end)
		}
	}
	// A run leaving standing water starts AT its level and eases down over
	// a few widths: starting at the ground below it left the river half a
	// metre under the pond it drains, a step the pond's surface stood
	// proud of at the outflow.
	if !math.IsNaN(r.startCap) && z[0] <= r.startCap+1e-6 {
		k := min(max(int(math.Round(noise.Clamp(3*st[0].Width, 6, 40)/r.ds)), 1), n-1)
		b := z[k]
		for i := 0; i <= k; i++ {
			z[i] = math.Max(z[i], r.startCap+(b-r.startCap)*float64(i)/float64(k))
		}
	}
	for i := 1; i < n; i++ {
		z[i] = math.Min(z[i], z[i-1])
	}
	r.falls, r.rapids = nil, nil
	r.findFalls(z)
	for i := range st {
		st[i].Surface = z[i]
	}
}

// minSlices is the elementwise minimum.
func minSlices(a, b []float64) []float64 {
	out := make([]float64, len(a))
	for i := range a {
		out[i] = math.Min(a[i], b[i])
	}
	return out
}

// findFalls turns the stretches steeper than MaxSlope into steps (file
// comment), z in place, and digs their plunge pools into r.plunge.
func (r *riverRun) findFalls(z []float64) {
	st := r.st
	n := len(st)
	sh := r.spec.Shape
	if sh.MaxSlope <= 0 || n < 5 {
		return
	}
	half := func(i int) int { return max(int(math.Round(math.Max(st[i].Width, 2*r.ds*2)/(2*r.ds))), 1) }
	steep := make([]bool, n)
	for i := range st {
		k := half(i)
		a, b := max(i-k, 0), min(i+k, n-1)
		if b > a && (z[a]-z[b])/(float64(b-a)*r.ds) > sh.MaxSlope {
			steep[i] = true
		}
	}
	for i := 0; i < n; {
		if !steep[i] {
			i++
			continue
		}
		j := i
		// Gaps shorter than a half-width are one fall.
		for j+1 < n && (steep[j+1] || (j+half(j) < n && anyTrue(steep[j+1:min(j+half(j)+1, n)]))) {
			j++
		}
		lip, foot := max(i-half(i), 0), min(j+half(j), n-1)
		drop := z[lip] - z[foot]
		run := float64(foot-lip) * r.ds
		if drop >= math.Max(sh.MinFall, 0.05) && foot > lip && run > math.Max(3*st[lip].Width, 2*drop) {
			// Rapids (file comment), or a cascade where the shape asks for
			// one (river_falls.go).
			if !r.cascade(z, lip, foot) {
				r.rapids = append(r.rapids, [2]float64{st[lip].S, st[foot].S})
			}
		} else if drop >= math.Max(sh.MinFall, 0.05) && foot > lip {
			for k := lip + 1; k <= foot; k++ {
				z[k] = z[foot]
			}
			s := st[lip]
			r.falls = append(r.falls, Waterfall{River: r.name, X: round2(s.X), Z: round2(s.Z), Lip: round3(z[lip]),
				Foot: round3(z[foot]), Drop: round3(drop), DirX: round3(s.TX), DirZ: round3(s.TZ), Width: round2(s.Width),
				S: round2(s.S)})
			extra := math.Min(0.35*drop+0.4, 3)
			pool := 1.5 * math.Max(s.Width, drop)
			for k := lip + 1; k < n && st[k].S-s.S <= pool; k++ {
				f := (st[k].S - s.S) / pool
				r.plunge[k] = math.Max(r.plunge[k], extra*math.Sin(math.Pi*math.Min(f*0.5+0.5*math.Sqrt(f), 1)))
			}
		}
		i = j + 1
	}
}

func anyTrue(b []bool) bool {
	for _, v := range b {
		if v {
			return true
		}
	}
	return false
}

// shapeRiver sets the pools and riffles, the thalweg offset and point bars,
// the speed, the foam and the boulders (file comment).
func shapeRiver(r *riverRun) {
	st := r.st
	n := len(st)
	sh := r.spec.Shape
	// Near a fall: no riffle character, and the foam of the lip and pool.
	nearFall := make([]float64, n) // 0..1, 1 at a lip or in its pool
	for _, f := range r.falls {
		for i := range st {
			d := st[i].S - f.S
			w := math.Max(f.Width, 1)
			switch {
			case d <= 0 && d > -1.5*w:
				nearFall[i] = math.Max(nearFall[i], 0.9*(1+d/(1.5*w)))
			case d > 0 && d < 1.5*math.Max(w, f.Drop):
				nearFall[i] = math.Max(nearFall[i], 1-0.7*d/(1.5*math.Max(w, f.Drop)))
			}
		}
	}
	quiet := make([]bool, n)
	for _, f := range r.falls {
		for i := range st {
			if math.Abs(st[i].S-f.S) < 2*math.Max(f.Width, 1)+1.5*math.Max(f.Width, f.Drop) {
				quiet[i] = true
			}
		}
	}
	// Pools and riffles.
	rng := noise.Hash64(r.spec.Seed, 0x52494646, uint64(len(r.name)))
	phase := float64(rng%6283) / 1000
	turns := make([]int, n)
	depth0 := make([]float64, n) // the spec's depth, before the character
	for i := range st {
		depth0[i] = st[i].Depth
		lam := sh.RiffleSpacing
		if lam <= 0 {
			lam = 6 * st[i].Width
		}
		lam = math.Max(lam, 2)
		if i > 0 {
			phase += 2 * math.Pi * r.ds / lam
		}
		turns[i] = int(math.Floor(phase / (2 * math.Pi)))
		c := math.Cos(phase)
		riffle := noise.SmoothStep(0.2, 0.9, c)
		pool := noise.SmoothStep(0.2, 0.9, -c)
		if quiet[i] {
			riffle, pool = 0, 0
		}
		st[i].Riffle = riffle
		st[i].Depth = st[i].Depth*(1+0.5*sh.Pools*pool-0.5*sh.Pools*riffle) + r.plunge[i]
	}
	// The thalweg and the point bars.
	for i := range st {
		o := noise.Clamp(-r.curv[i]*st[i].Width*1.2, -0.35, 0.35)
		st[i].Offset = o * st[i].Width / 2
		st[i].Bar = noise.SmoothStep(0.05, 0.3, math.Abs(o))
	}
	// The speed: a discharge held steady along the river.
	q := make([]float64, n)
	for i := range st {
		q[i] = st[i].Width * depth0[i]
	}
	qs := boxFilter(q, max(int(math.Round(10*meanWidth(st)/r.ds)), 1))
	slope := make([]float64, n)
	k := max(int(math.Round(meanWidth(st)/r.ds)), 1)
	for i := range st {
		a, b := max(i-k, 0), min(i+k, n-1)
		if b > a {
			slope[i] = math.Max(st[a].Surface-st[b].Surface, 0) / (float64(b-a) * r.ds)
		}
	}
	srt := append([]float64(nil), slope...)
	sort.Float64s(srt)
	med := srt[len(srt)/2]
	for i := range st {
		v := sh.Speed * qs[i] / math.Max(st[i].Width*math.Max(st[i].Depth-r.plunge[i], 0.05), 1e-3)
		v *= noise.Clamp(math.Sqrt((slope[i]+1e-4)/(med+1e-4)), 0.75, 1.5)
		st[i].Speed = noise.Clamp(v, 0.1, 3.8)
		foam := 0.55 * sh.Pools * st[i].Riffle * math.Min(st[i].Speed/math.Max(sh.Speed, 0.1), 2) * 0.8
		foam += 0.25 * math.Max(st[i].Speed-2, 0)
		for _, rp := range r.rapids {
			if st[i].S >= rp[0] && st[i].S <= rp[1] {
				foam = math.Max(foam, 0.6)
			}
		}
		st[i].Foam = noise.Clamp(math.Max(foam, nearFall[i]), 0, 1)
	}
	// Boulders on the riffle crests.
	r.boulders = nil
	if sh.Boulders > 0 {
		for i := 1; i < n; i++ {
			if turns[i] == turns[i-1] || quiet[i] {
				continue
			}
			s := st[i]
			h := noise.Hash64(r.spec.Seed, 0xb0, uint64(i), uint64(len(r.name)))
			cnt := sh.Boulders * s.Width / 4
			m := int(cnt)
			if unit(h) < cnt-float64(m) {
				m++
			}
			for b := 0; b < m; b++ {
				hb := noise.Hash64(h, uint64(b)+1)
				along := (unit(hb) - 0.5) * s.Width
				across := (unit(noise.Hash64(hb, 2))*2 - 1) * 0.7 * s.Width / 2
				size := (0.45 + 0.4*unit(noise.Hash64(hb, 3))) * noise.Clamp(s.Depth+0.35, 0.5, 1.8)
				r.boulders = append(r.boulders, Boulder{River: r.name,
					X: s.X + s.TX*along - s.TZ*across, Z: s.Z + s.TZ*along + s.TX*across, Size: size})
			}
		}
	}
}

func meanWidth(st []RiverStation) float64 {
	w := 0.0
	for _, s := range st {
		w += s.Width
	}
	return w / float64(max(len(st), 1))
}

// unit is a hash as a number in [0, 1).
func unit(h uint64) float64 { return float64(h>>11) / float64(1<<53) }
