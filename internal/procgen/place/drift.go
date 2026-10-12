package place

// drift.go — sand that answers the rocks on it.
//
// A scattered rock is pressed into ground that was generated before it
// existed: the terrain meets it in whatever line the rock's sink cut, flat
// all round, which reads as a rock dropped on a carpet. Real sand piles
// against an obstacle: a fillet in the corner where the two meet, steep at
// the rock (about the angle of repose) and easing out; a long tail in its lee
// where the wind (or, in a wash, the water) slows behind it; a short ramp on
// the side the flow comes from. Drift works out that sand as a rise of the
// height grid, so the terrain's own mesh — not a decal — climbs the rock.
//
// The method, per rock:
//
//  1. COLUMNS. The rock's mesh (placed: rotated, scaled, moved) is
//     rasterised onto the grid's samples, top down: each sample under it
//     gets the lowest and highest Y of the rock above it (RasterColumns).
//     A closed mesh crosses every column it covers at least twice, so the
//     pair is its underside and its top there.
//  2. HOW HIGH. The rock's exposed height E is its tallest column over the
//     ground; under MinHeight it gets nothing (a pebble at a 0.5 m grid
//     would raise a cell-sized pimple). The contact height is Fillet·E,
//     capped at MaxHeight, varied ±15% around the rock by a noise so the
//     sand line is not a contour. An obstacle can bring its own shape
//     (DriftRock.Own): a cliff's foot takes a fixed, deeper ramp, varying
//     along the wall over tens of metres.
//  3. FOOTPRINT. The samples where the rock's underside comes below the
//     sand's new surface (ground + contact height) and its top above the
//     ground: where the sand will meet rock. Its boundary samples are the
//     contact line.
//  4. THE APRON. Every sample near the rock rises by the largest of the
//     boundary samples' profiles, h·(1 − d/L)², L = Spread·h — concave, its
//     slope at the rock 2/Spread (33° at 3) — with d measured in a frame
//     stretched along the tail direction: (1 + Tail) longer downwind,
//     (1 + Windward) shorter upwind. Inside the footprint the ground rises
//     to the contact height (hidden under the rock, and no hole where its
//     underside lifts off). A boundary sample's height is capped at 85% of
//     the rock's own height there, so sand does not climb over a low edge.
//  5. Rocks combine by the largest rise, not the sum: two boulders side by
//     side share one drift between them instead of a double heap.
//
// Then the whole rise fades out on steep ground (sand slides off slopes
// past repose; SlopeFade) and is zero within Edge samples of the grid's
// edge, where the terrain's tiles meet its far backdrop.

import (
	"math"

	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/terrain"
)

// Columns is a rock's cover of the grid: the samples (I0..I0+NI-1,
// J0..J0+NJ-1) under its bounding box, and for each the lowest (Bot) and
// highest (Top) Y of the rock above it; Bot is +Inf where the rock does not
// cover the sample.
type Columns struct {
	I0, J0, NI, NJ int
	Bot, Top       []float32
}

// Covered reports whether window sample (i, j) is under the rock.
func (c *Columns) Covered(i, j int) bool { return !math.IsInf(float64(c.Bot[j*c.NI+i]), 1) }

// RasterColumns rasterises a placed mesh (world positions, triangles) onto
// g's samples. Nil when it covers none.
func RasterColumns(g *terrain.Grid, pos [][3]float64, tris [][3]int) *Columns {
	if len(pos) == 0 || len(tris) == 0 {
		return nil
	}
	sz := g.S()
	lo := [2]float64{math.Inf(1), math.Inf(1)}
	hi := [2]float64{math.Inf(-1), math.Inf(-1)}
	for _, p := range pos {
		lo[0], lo[1] = math.Min(lo[0], p[0]), math.Min(lo[1], p[2])
		hi[0], hi[1] = math.Max(hi[0], p[0]), math.Max(hi[1], p[2])
	}
	i0 := max(int(math.Ceil((lo[0]-g.MinX)/g.Cell)), 0)
	j0 := max(int(math.Ceil((lo[1]-g.MinZ)/g.Cell)), 0)
	i1 := min(int(math.Floor((hi[0]-g.MinX)/g.Cell)), sz-1)
	j1 := min(int(math.Floor((hi[1]-g.MinZ)/g.Cell)), sz-1)
	if i1 < i0 || j1 < j0 {
		return nil
	}
	c := &Columns{I0: i0, J0: j0, NI: i1 - i0 + 1, NJ: j1 - j0 + 1}
	c.Bot = make([]float32, c.NI*c.NJ)
	c.Top = make([]float32, c.NI*c.NJ)
	for k := range c.Bot {
		c.Bot[k], c.Top[k] = float32(math.Inf(1)), float32(math.Inf(-1))
	}
	any := false
	for _, t := range tris {
		a, b, d := pos[t[0]], pos[t[1]], pos[t[2]]
		// The triangle's XZ projection; one seen edge-on covers nothing.
		det := (b[0]-a[0])*(d[2]-a[2]) - (d[0]-a[0])*(b[2]-a[2])
		if math.Abs(det) < 1e-12 {
			continue
		}
		ti0 := max(int(math.Ceil((math.Min(a[0], math.Min(b[0], d[0]))-g.MinX)/g.Cell)), c.I0)
		ti1 := min(int(math.Floor((math.Max(a[0], math.Max(b[0], d[0]))-g.MinX)/g.Cell)), c.I0+c.NI-1)
		tj0 := max(int(math.Ceil((math.Min(a[2], math.Min(b[2], d[2]))-g.MinZ)/g.Cell)), c.J0)
		tj1 := min(int(math.Floor((math.Max(a[2], math.Max(b[2], d[2]))-g.MinZ)/g.Cell)), c.J0+c.NJ-1)
		for j := tj0; j <= tj1; j++ {
			z := g.Z(j)
			for i := ti0; i <= ti1; i++ {
				x := g.X(i)
				// Barycentric weights of (x, z) in the projection.
				u := ((x-a[0])*(d[2]-a[2]) - (d[0]-a[0])*(z-a[2])) / det
				v := ((b[0]-a[0])*(z-a[2]) - (x-a[0])*(b[2]-a[2])) / det
				if u < -1e-9 || v < -1e-9 || u+v > 1+1e-9 {
					continue
				}
				y := float32(a[1] + u*(b[1]-a[1]) + v*(d[1]-a[1]))
				k := (j-c.J0)*c.NI + (i - c.I0)
				c.Bot[k] = min(c.Bot[k], y)
				c.Top[k] = max(c.Top[k], y)
				any = true
			}
		}
	}
	if !any {
		return nil
	}
	return c
}

// DriftRock is one obstacle: its columns and the unit direction its tail
// runs (downwind or downstream; zero for none).
type DriftRock struct {
	Cols *Columns
	Dir  [2]float64
	Seed uint64
	// Own, when set, replaces the settings' shape for this obstacle — a
	// cliff takes a deeper, longer ramp than a boulder, and holds sand on
	// steeper ground at its foot.
	Own *DriftShape
}

// DriftShape is what an obstacle may set for itself (DriftRock.Own).
type DriftShape struct {
	// Height is the contact height (m), whatever the obstacle's own height.
	Height float64
	// Spread and Tail are DriftSettings'. (No slope fade: a cliff's ramp is
	// an absolute surface — cliffRise.)
	Spread, Tail float64
	// Reach is how far out from the terrain's wall its foot runs (m): as
	// far as the rock can stand out of it (wall_sheet's depth), so the
	// ramp's full height meets the rock face wherever that is.
	Reach float64
	// Vary is how far the contact height wanders (±share) along the
	// obstacle, over VaryLength metres.
	Vary, VaryLength float64
}

// DriftSettings shape the sand.
type DriftSettings struct {
	// Fillet is the contact height as a fraction of a rock's exposed
	// height; MaxHeight caps it (m); MinHeight is the exposed height under
	// which a rock gets none (m).
	Fillet, MaxHeight, MinHeight float64
	// Spread is the apron's length per metre of contact height (its slope
	// at the rock is 2/Spread).
	Spread float64
	// Tail lengthens the apron downwind (1 + Tail times), Windward shortens
	// it upwind (1 + Windward times).
	Tail, Windward float64
	// SlopeFade is the ground slope (degrees) over which the sand fades
	// out: none past the second.
	SlopeFade [2]float64
	// Edge is how many samples at the grid's edge stay untouched.
	Edge int
}

// DefaultDrift is the settings a desert boulder field wants.
func DefaultDrift() DriftSettings {
	return DriftSettings{Fillet: 0.3, MaxHeight: 1.2, MinHeight: 0.45, Spread: 3, Tail: 2.5, Windward: 0.4,
		SlopeFade: [2]float64{24, 36}, Edge: 2}
}

// DriftResult is the rise and what made it.
type DriftResult struct {
	// Rise is metres of sand per sample (row-major like the grid's H).
	Rise []float32
	// Rocks is how many rocks were given sand; Small how many were too low.
	Rocks, Small int
}

// Drift is the sand every rock piles up on g (see the file comment). slope
// is the ground's slope at a sample index, degrees.
func Drift(g *terrain.Grid, slope func(k int) float64, rocks []DriftRock, s DriftSettings, pool terrain.Pool) (*DriftResult, error) {
	sz := g.S()
	type piece struct {
		i0, j0, ni, nj int
		rise           []float32
	}
	pieces := make([]*piece, len(rocks))
	small := make([]bool, len(rocks))
	err := pool.Parallel(len(rocks), func(r int) error {
		rk := &rocks[r]
		c := rk.Cols
		if c == nil || rk.Own != nil {
			return nil // a cliff: cliffRise, below, all of them at once
		}
		// Exposed height: the tallest column over the ground.
		e := 0.0
		for j := 0; j < c.NJ; j++ {
			for i := 0; i < c.NI; i++ {
				if c.Covered(i, j) {
					e = math.Max(e, float64(c.Top[j*c.NI+i]-g.At(c.I0+i, c.J0+j)))
				}
			}
		}
		if e < s.MinHeight {
			small[r] = true
			return nil
		}
		hc := math.Min(s.Fillet*e, s.MaxHeight)
		spread, tail, fade := s.Spread, s.Tail, s.SlopeFade
		vamp, vlen := 0.15, 1.5
		// The footprint: where the sand's new surface meets rock.
		inFoot := make([]bool, c.NI*c.NJ)
		for j := 0; j < c.NJ; j++ {
			for i := 0; i < c.NI; i++ {
				k := j*c.NI + i
				if !c.Covered(i, j) {
					continue
				}
				h0 := float64(g.At(c.I0+i, c.J0+j))
				inFoot[k] = float64(c.Bot[k]) < h0+hc && float64(c.Top[k]) > h0
			}
		}
		foot := func(i, j int) bool { return i >= 0 && j >= 0 && i < c.NI && j < c.NJ && inFoot[j*c.NI+i] }
		// A contact: where, how high its sand stands, and the ground it
		// stands on.
		type contact struct{ x, z, h, g float64 }
		var line []contact
		for j := 0; j < c.NJ; j++ {
			for i := 0; i < c.NI; i++ {
				if !foot(i, j) {
					continue
				}
				// The window's own edge counts as outside: a rock cut off
				// by the grid's edge still has its sand on this side.
				if foot(i-1, j) && foot(i+1, j) && foot(i, j-1) && foot(i, j+1) {
					continue
				}
				k := j*c.NI + i
				gi, gj := c.I0+i, c.J0+j
				own := float64(c.Top[k] - g.At(gi, gj))
				vf := g.Cell / vlen
				vary := 1 + vamp*noise.Value2(rk.Seed, float64(gi)*vf, float64(gj)*vf)
				h := math.Min(hc*vary, 0.85*own)
				if h > 0 {
					line = append(line, contact{g.X(gi), g.Z(gj), h, float64(g.At(gi, gj))})
				}
			}
		}
		if len(line) == 0 {
			return nil
		}
		// The window the apron can reach.
		reach := spread * hc * (1 + math.Max(tail, 0))
		pad := int(math.Ceil(reach/g.Cell)) + 1
		p := &piece{i0: max(c.I0-pad, 0), j0: max(c.J0-pad, 0)}
		p.ni = min(c.I0+c.NI+pad, sz) - p.i0
		p.nj = min(c.J0+c.NJ+pad, sz) - p.j0
		p.rise = make([]float32, p.ni*p.nj)
		dx, dz := rk.Dir[0], rk.Dir[1]
		windward := s.Windward
		if dx == 0 && dz == 0 {
			// No direction: no tail and no windward side, so the frame
			// can be any — the distance is then plain.
			dx, tail, windward = 1, 0, 0
		}
		for j := 0; j < p.nj; j++ {
			gj := p.j0 + j
			z := g.Z(gj)
			for i := 0; i < p.ni; i++ {
				gi := p.i0 + i
				// Sand slides off ground past its fade.
				keep := 1.0
				if slope != nil {
					keep = 1 - noise.SmoothStep(fade[0], fade[1], slope(gj*sz+gi))
				}
				if foot(gi-c.I0, gj-c.J0) {
					// Under the rock the ground rises to the sand line (hidden,
					// and no hole where its underside lifts off).
					p.rise[j*p.ni+i] = float32(hc * keep)
					continue
				}
				x := g.X(gi)
				here := float64(g.At(gi, gj))
				best := 0.0
				for _, q := range line {
					vx, vz := x-q.x, z-q.z
					a := vx*dx + vz*dz
					b := vz*dx - vx*dz
					if a > 0 {
						a /= 1 + tail
					} else {
						a *= 1 + windward
					}
					d := math.Sqrt(a*a + b*b)
					l := spread * q.h
					if d >= l {
						continue
					}
					t := 1 - d/l
					// The sand's surface stands h·t² over its contact's
					// ground: on ground higher than that — over a cliff's
					// rim, up a slope — it rises only to that surface.
					best = math.Max(best, math.Min(q.h*t*t, q.g+q.h*t*t-here))
				}
				p.rise[j*p.ni+i] = float32(best * keep)
			}
		}
		pieces[r] = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := &DriftResult{Rise: make([]float32, sz*sz)}
	if cr, n := cliffRise(g, slope, rocks, pool); n > 0 {
		copy(out.Rise, cr)
		out.Rocks += n
	}
	for r, p := range pieces {
		if small[r] {
			out.Small++
		}
		if p == nil {
			continue
		}
		out.Rocks++
		for j := 0; j < p.nj; j++ {
			row := (p.j0 + j) * sz
			for i := 0; i < p.ni; i++ {
				k := row + p.i0 + i
				out.Rise[k] = max(out.Rise[k], p.rise[j*p.ni+i])
			}
		}
	}
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			k := j*sz + i
			if out.Rise[k] == 0 {
				continue
			}
			if i < s.Edge || j < s.Edge || i >= sz-s.Edge || j >= sz-s.Edge {
				out.Rise[k] = 0
			}
		}
	}
	return out, nil
}

// cliffRise is the sand banked against the cliffs among rocks (those with
// their own shape), as one field over the grid, and how many cliffs took
// part. (slope is unused: the foot is read off the heights themselves.)
//
// A cliff is not a boulder. The boulders' method — a cone from every sample
// of the contact line, the largest winning — on a wall's long, ragged foot
// gave spikes (each sample's own height, cut cell by cell by the slope
// fade), and since the ground under a cliff's rock must not be raised past
// the terrain's own wall, a trough where the rock flares out over the
// floor. So, for every cliff together:
//
//  1. THE FOOT, from the terrain: gentle, open ground (no rock over it)
//     within Reach of steep, rock-covered ground that rises above it. (Read off the rock instead —
//     where it stands over the ground — it held all the way up the wall,
//     whose every terrain sample then raised a ramp of its own: sand up the
//     whole face, in sawteeth.)
//  2. d, every sample's distance to the foot (a chamfer transform): which
//     samples any ramp can reach.
//  3. The ramp: H·(1 − d/L)², H the ramp height wandering smoothly along
//     the walls (Vary over VaryLength) — each sample takes its nearest foot
//     sample's, so it never creeps back up across a ramp — L = Spread·H,
//     longer by Tail where
//     the direction away from the wall runs downwind. On the foot itself
//     (d = 0, under the rock's flare) it is the full H, so the sand meets
//     the rock with no dip.
//  4. AN ABSOLUTE SURFACE: the highest, at each sample, of every foot
//     sample's ramp — its ground averaged along the wall over about 4 m,
//     plus H·t², t = 1 − dist/L — and everything under it is filled up to
//     it, but by no more than H·t² + 1.5·H·t, an allowance that runs out at
//     the ramp's reach so every ramp meets the ground there. Each sample's own foot ground made the sand rise
//     over a hump at the foot and fall back into the hollow beside the wall
//     (a dip; on a bumpy foot, spikes); the nearest foot's ramp alone
//     stepped like a cliff where the nearest foot changed from a bench to
//     the floor below it, and a ramp ending at its own level rather than the
//     ground's stood a wall of sand where it ran out over lower ground. The
//     highest of these is continuous. It does not climb onto a bench or over
//     a rim: the ground there is above it. The terrain's own wall behind the rock
//     (steep and rock-covered) is not raised at all: raised relative to its
//     own height it pushed out through the rock (spikes); filled to the
//     sand's surface, its 0.5 m grid cut by a level surface showed as a
//     sawtooth along the top of the sand. So no slope fade either: it cut the ramp cell by cell on the uneven ground at a
//     foot (spikes), and softened, it pulled the ramp down beside the wall
//     (a dip).
func cliffRise(g *terrain.Grid, slope func(k int) float64, rocks []DriftRock, pool terrain.Pool) ([]float32, int) {
	sz := g.S()
	var own *DriftShape
	var seed uint64
	var wx, wz float64
	covered := make([]bool, sz*sz)
	n := 0
	for r := range rocks {
		rk := &rocks[r]
		c := rk.Cols
		if rk.Own == nil || c == nil {
			continue
		}
		if own == nil {
			own, seed, wx, wz = rk.Own, rk.Seed, rk.Dir[0], rk.Dir[1]
		}
		n++
		for j := 0; j < c.NJ; j++ {
			for i := 0; i < c.NI; i++ {
				if c.Covered(i, j) {
					covered[(c.J0+j)*sz+c.I0+i] = true
				}
			}
		}
	}
	if n == 0 {
		return nil, 0
	}
	// The terrain's own slope (degrees), from its heights.
	slopeAt := func(i, j int) float64 {
		i0, i1 := max(i-1, 0), min(i+1, sz-1)
		j0, j1 := max(j-1, 0), min(j+1, sz-1)
		gx := float64(g.H[j*sz+i1]-g.H[j*sz+i0]) / (float64(i1-i0) * g.Cell)
		gz := float64(g.H[j1*sz+i]-g.H[j0*sz+i]) / (float64(j1-j0) * g.Cell)
		return math.Atan(math.Hypot(gx, gz)) * 180 / math.Pi
	}
	steep := make([]bool, sz*sz)
	flat := make([]bool, sz*sz)
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			a := slopeAt(i, j)
			steep[j*sz+i], flat[j*sz+i] = a > 50, a < 35
		}
	}
	// THE FOOT: gentle ground within a metre of steep, rock-covered ground
	// that rises above it — the floor at a wall's base, a bench under the
	// next wall up. Not a rim (the steep ground there falls away) and not
	// the wall itself (steep).
	reach := own.Reach
	if reach <= 0 {
		reach = 1
	}
	rr := max(int(math.Ceil(reach/g.Cell)), 1)
	face := make([]bool, sz*sz)
	// flare: gentle ground by a wall but UNDER its rock (the rock's flare
	// over the floor, a ledge, a chute's floor). It starts no ramp — a ledge
	// would start one high up the face — but takes the foot's ramp at full
	// height (it is where the sand meets the rock).
	flare := make([]bool, sz*sz)
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			k := j*sz + i
			if !flat[k] {
				continue
			}
		near:
			for b := max(j-rr, 0); b <= min(j+rr, sz-1); b++ {
				for a := max(i-rr, 0); a <= min(i+rr, sz-1); a++ {
					q := b*sz + a
					if (a-i)*(a-i)+(b-j)*(b-j) > rr*rr {
						continue
					}
					if covered[q] && steep[q] && g.H[q] > g.H[k]+0.5 {
						if covered[k] {
							flare[k] = true
						} else {
							face[k] = true
						}
						break near
					}
				}
			}
		}
	}
	d, near := chamferNearest(face, sz, g.Cell)
	// The floor the sand stands on: the foot's ground averaged along the
	// wall over about 4 m, so a hump or a hollow at the foot does not lift
	// or sink the ramp over it (sand fills the hollows and buries the humps).
	br := max(int(math.Round(4/g.Cell)), 1)
	wsum := make([]float32, sz*sz)
	hsum := make([]float32, sz*sz)
	for k, on := range face {
		if on {
			wsum[k], hsum[k] = 1, g.H[k]
		}
	}
	wsum, hsum = boxSum(wsum, sz, br), boxSum(hsum, sz, br)
	// The foot samples, each with its ramp: the averaged floor it stands on,
	// its height, its run; bucketed on a coarse grid to find them by.
	type footRamp struct{ x, z, base, h, l float64 }
	vf := 1 / math.Max(own.VaryLength, 1)
	var feet []footRamp
	maxRun := 0.0
	for k, on := range face {
		if !on || wsum[k] <= 0 {
			continue
		}
		x, z := g.X(k%sz), g.Z(k/sz)
		h := own.Height * (1 + own.Vary*noise.Value2(seed, x*vf, z*vf))
		if h <= 0 {
			continue
		}
		fr := footRamp{x, z, float64(hsum[k] / wsum[k]), h, own.Spread * h}
		feet = append(feet, fr)
		maxRun = math.Max(maxRun, fr.l*(1+math.Max(own.Tail, 0)))
	}
	const bucket = 4.0
	nb := int(math.Ceil(g.Size()/bucket)) + 1
	buckets := make([][]int32, nb*nb)
	for n, f := range feet {
		bi := min(max(int((f.x-g.MinX)/bucket), 0), nb-1)
		bj := min(max(int((f.z-g.MinZ)/bucket), 0), nb-1)
		buckets[bj*nb+bi] = append(buckets[bj*nb+bi], int32(n))
	}
	reachB := int(math.Ceil((maxRun + reach) / bucket))
	capRise := 1.5 * own.Height * (1 + math.Abs(own.Vary))
	raw := make([]float32, sz*sz)
	_ = pool.Parallel(sz, func(j int) error {
		for i := 0; i < sz; i++ {
			k := j*sz + i
			if near[k] < 0 || float64(d[k]) >= maxRun+reach || (steep[k] && covered[k]) {
				// Out of every ramp's reach, or the terrain's own wall behind
				// the rock (left as it is).
				continue
			}
			x, z := g.X(i), g.Z(j)
			ground := float64(g.H[k])
			bi, bj := int((x-g.MinX)/bucket), int((z-g.MinZ)/bucket)
			best := 0.0
			for b := max(bj-reachB, 0); b <= min(bj+reachB, nb-1); b++ {
				for a := max(bi-reachB, 0); a <= min(bi+reachB, nb-1); a++ {
					for _, n := range buckets[b*nb+a] {
						f := &feet[n]
						vx, vz := x-f.x, z-f.z
						dist := math.Hypot(vx, vz)
						if flare[k] {
							dist = math.Max(0, dist-reach)
						}
						l := f.l
						if own.Tail > 0 && dist > 1e-6 {
							l *= 1 + own.Tail*math.Max(0, (vx*wx+vz*wz)/dist)
						}
						if dist >= l {
							continue
						}
						t := 1 - dist/l
						// Up to the foot's absolute surface (filling a hollow,
						// burying a hump), but never more than the ramp plus an
						// allowance that runs out with it: every ramp meets the
						// ground at the end of its reach, so the highest of
						// them has no step (a bench's ramp, ending at the
						// bench's level over the floor below it, stood a wall
						// of sand there).
						ramp := f.h * t * t
						best = math.Max(best, math.Min(f.base+ramp-ground, ramp+1.5*f.h*t))
					}
				}
			}
			raw[k] = float32(math.Min(best, capRise))
		}
		return nil
	})
	return raw, n
}

// boxSum is the sum of v over a (2r+1)² window, edges clamped.
func boxSum(v []float32, sz, r int) []float32 {
	tmp := make([]float32, len(v))
	out := make([]float32, len(v))
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			var s float32
			for q := max(i-r, 0); q <= min(i+r, sz-1); q++ {
				s += v[j*sz+q]
			}
			tmp[j*sz+i] = s
		}
	}
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			var s float32
			for q := max(j-r, 0); q <= min(j+r, sz-1); q++ {
				s += tmp[q*sz+i]
			}
			out[j*sz+i] = s
		}
	}
	return out
}

// chamferNearest is each sample's distance (m) to the nearest set one — a
// two-pass 3-4 chamfer transform, within a few percent of Euclidean — and
// that nearest set sample's index (-1: none).
func chamferNearest(set []bool, sz int, cell float64) (d []float32, near []int32) {
	const inf = math.MaxFloat32 / 4
	d = make([]float32, sz*sz)
	near = make([]int32, sz*sz)
	for k, s := range set {
		if s {
			near[k] = int32(k)
		} else {
			d[k], near[k] = inf, -1
		}
	}
	a, b := float32(cell), float32(cell*math.Sqrt2)
	relax := func(k, i, j int, w float32) {
		if i < 0 || j < 0 || i >= sz || j >= sz {
			return
		}
		if q := j*sz + i; d[q]+w < d[k] {
			d[k], near[k] = d[q]+w, near[q]
		}
	}
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			k := j*sz + i
			relax(k, i-1, j, a)
			relax(k, i, j-1, a)
			relax(k, i-1, j-1, b)
			relax(k, i+1, j-1, b)
		}
	}
	for j := sz - 1; j >= 0; j-- {
		for i := sz - 1; i >= 0; i-- {
			k := j*sz + i
			relax(k, i+1, j, a)
			relax(k, i, j+1, a)
			relax(k, i+1, j+1, b)
			relax(k, i-1, j+1, b)
		}
	}
	return d, near
}
