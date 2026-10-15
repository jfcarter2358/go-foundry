package ruins

// layout.go — the ruins laid out along a path down a slope.
//
// The path (x, z, width per point, a Catmull-Rom curve) is cut into
// TERRACES and STAIRS alternately, each a chord of the path, so the end of
// one is exactly the start of the next and the way down is unbroken:
//
//   - A terrace is a level rectangle along its chord, its floor at the
//     highest ground under it plus a clearance (no ground pokes through
//     it), its body reaching down below the lowest ground under it (no
//     terrace floats): on a slope its downhill side is a retaining wall.
//   - A stair between two terraces drops from one floor to the next in
//     risers no taller than the rise limit. The next terrace's floor is
//     found with it: the fewest steps whose drop reaches down to the
//     ground under the next terrace (a gentle slope: a short flight and the
//     floor on the ground), or, where the ground falls faster than a stair
//     can (a cliff), the longest flight allowed and a floor standing on a
//     tall foundation.
//
// Buildings stand on the terraces either side of a clear WALKWAY down the
// middle, their fronts to it; a gateway may stand across the walkway at a
// terrace's top; walls run along terrace edges; more towers and shrines
// stand on the slope beside the path on PLINTHS reaching down into the
// ground (the reference's towers growing out of the cliffs); columns,
// fallen columns and rubble fill the gaps.

import (
	"math"
	"sort"

	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/sdf"
)

// PathPoint is one point of the authored path: plan position and the
// terraces' width there.
type PathPoint struct{ X, Z, Width float64 }

// Settings are a layout's parameters.
type Settings struct {
	Path []PathPoint
	Seed uint64
	// TerraceLength is a terrace's length along the path (min, max).
	TerraceLength [2]float64
	// Walkway is the clear way's (and the stairs') width.
	Walkway float64
	// Rise is the tallest riser; Tread a step's run.
	Rise, Tread float64
	// MaxFlight is the longest stair (m along the path).
	MaxFlight float64
	// Clearance is a floor's height over the highest ground under it.
	Clearance float64
	// Foundation is how far a body reaches under the lowest ground.
	Foundation float64
	// Buildings is how full the terraces' sides are (0..1).
	Buildings float64
	// Gates is the chance a terrace has a gateway across its top.
	Gates float64
	// Walls is the chance a terrace edge carries a wall.
	Walls float64
	// Outliers is how many buildings to try to stand on the slope beside
	// the path, within Spread of its edge.
	Outliers int
	Spread   float64
	// MaxPlinth is the tallest plinth an outlier may stand on.
	MaxPlinth float64
	// Ruin is the share of buildings that are ruined variants.
	Ruin float64
	// Debris is how much rubble and how many fallen columns lie about.
	Debris float64
	// Scale is the buildings' size range.
	Scale [2]float64
	// OutlierScale is the slope buildings' size range (zero: Scale): a
	// city's far towers bigger than the ones on the terraces.
	OutlierScale [2]float64
}

// Defaults are the stage's defaults.
func Defaults() Settings {
	return Settings{TerraceLength: [2]float64{12, 22}, Walkway: 3.6, Rise: 0.18, Tread: 0.34, MaxFlight: 20,
		Clearance: 0.25, Foundation: 1.5, Buildings: 0.75, Gates: 0.35, Walls: 0.5, Outliers: 14, Spread: 40,
		MaxPlinth: 24, Ruin: 0.35, Debris: 0.5, Scale: [2]float64{0.9, 1.15}}
}

// Ground is the terrain's height at a plan point.
type Ground func(x, z float64) float64

// Rect is an oriented rectangle in plan: centre, unit direction of its
// length (A axis), half-length and half-width (B axis, A turned 90° left:
// (−dz, dx)).
type Rect struct {
	CX, CZ float64
	DX, DZ float64
	HA, HB float64
}

// Local is (x, z) in the rectangle's frame (a along, b across).
func (r Rect) Local(x, z float64) (a, b float64) {
	px, pz := x-r.CX, z-r.CZ
	return px*r.DX + pz*r.DZ, -px*r.DZ + pz*r.DX
}

// World is the frame's (a, b) in plan.
func (r Rect) World(a, b float64) (x, z float64) {
	return r.CX + a*r.DX - b*r.DZ, r.CZ + a*r.DZ + b*r.DX
}

// Yaw is the placement yaw that turns a model's +z along the rectangle's A
// axis (a placement's rotation: local +z → (sin yaw, cos yaw)).
func (r Rect) Yaw() float64 { return math.Atan2(r.DX, r.DZ) }

// Overlaps reports whether two rectangles (grown by margin) overlap (by
// their separating axes).
func (r Rect) Overlaps(o Rect, margin float64) bool {
	axes := [][2]float64{{r.DX, r.DZ}, {-r.DZ, r.DX}, {o.DX, o.DZ}, {-o.DZ, o.DX}}
	for _, ax := range axes {
		pr := func(q Rect) (float64, float64) {
			c := q.CX*ax[0] + q.CZ*ax[1]
			e := math.Abs(q.DX*ax[0]+q.DZ*ax[1])*(q.HA+margin/2) + math.Abs(-q.DZ*ax[0]+q.DX*ax[1])*(q.HB+margin/2)
			return c - e, c + e
		}
		a0, a1 := pr(r)
		b0, b1 := pr(o)
		if a1 < b0 || b1 < a0 {
			return false
		}
	}
	return true
}

// groundRange is the lowest and highest ground under the rectangle
// (sampled every metre and on its edges).
func groundRange(g Ground, r Rect) (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	na := max(int(math.Ceil(2*r.HA)), 1)
	nb := max(int(math.Ceil(2*r.HB)), 1)
	for i := 0; i <= na; i++ {
		for j := 0; j <= nb; j++ {
			a := -r.HA + 2*r.HA*float64(i)/float64(na)
			b := -r.HB + 2*r.HB*float64(j)/float64(nb)
			x, z := r.World(a, b)
			h := g(x, z)
			lo, hi = math.Min(lo, h), math.Max(hi, h)
		}
	}
	return lo, hi
}

// Terrace is one level of the way down.
type Terrace struct {
	Rect
	Floor, Bottom float64
	// GroundLo and GroundHi are the ground's range under it.
	GroundLo, GroundHi float64
	// Parapet says which edges carry the low parapet: +b, −b (sides), −a
	// (top), +a (bottom), each opened for the walkway at the ends.
	Parapet [4]bool
	// WalkB is the walkway's centre line across the terrace (b): a terrace
	// on a side slope is cut back on its uphill side, so the path need not
	// run down its middle.
	WalkB float64
}

// Stair is a flight from one terrace's floor to the next's, along its
// rectangle's A axis (a = −HA at the top).
type Stair struct {
	Rect
	Top, Bottom float64 // the floors it joins
	Steps       int
	Rise, Tread float64 // per step (Rise negative going up)
	Base        float64 // its body's bottom
}

// StepTop is the stair's walking height at a along it.
func (s Stair) StepTop(a float64) float64 {
	t := a + s.HA
	if s.Steps == 0 || t < 0 {
		return s.Top
	}
	i := math.Min(math.Floor(t/s.Tread), float64(s.Steps-1))
	return s.Top - (i+1)*s.Rise
}

// Plinth is a platform a building on the slope stands on.
type Plinth struct {
	Rect
	Top, Bottom float64
}

// Instance is a building placed: variant, base position, yaw, scale.
type Instance struct {
	Variant string
	X, Y, Z float64
	Yaw     float64
	Scale   float64
	// On is the terrace it stands on (−1: a plinth or the ground).
	On int
}

// Spot is a free point on a floor for later props.
type Spot struct{ X, Y, Z float64 }

// Layout is everything the ruins are.
type Layout struct {
	Terraces  []Terrace
	Stairs    []Stair
	Plinths   []Plinth
	Instances []Instance
	Spots     []Spot
	// Path is the dense path (x, z, width, arc length).
	Path [][4]float64
}

// densePath samples the Catmull-Rom path every step metres (about), with
// cumulative arc length.
func densePath(pts []PathPoint, step float64) [][4]float64 {
	if len(pts) < 2 {
		return nil
	}
	at := func(i int) PathPoint { return pts[min(max(i, 0), len(pts)-1)] }
	var out [][4]float64
	s := 0.0
	var px, pz float64
	for i := 0; i+1 < len(pts); i++ {
		p0, p1, p2, p3 := at(i-1), at(i), at(i+1), at(i+2)
		seg := math.Hypot(p2.X-p1.X, p2.Z-p1.Z)
		n := max(int(math.Ceil(seg/step)), 1)
		for k := 0; k < n || (i+2 == len(pts) && k == n); k++ {
			t := float64(k) / float64(n)
			x := catmull(p0.X, p1.X, p2.X, p3.X, t)
			z := catmull(p0.Z, p1.Z, p2.Z, p3.Z, t)
			w := p1.Width + (p2.Width-p1.Width)*t
			if len(out) > 0 {
				s += math.Hypot(x-px, z-pz)
			}
			out = append(out, [4]float64{x, z, w, s})
			px, pz = x, z
		}
	}
	return out
}

func catmull(p0, p1, p2, p3, t float64) float64 {
	t2, t3 := t*t, t*t*t
	return 0.5 * (2*p1 + (-p0+p2)*t + (2*p0-5*p1+4*p2-p3)*t2 + (-p0+3*p1-3*p2+p3)*t3)
}

// pathAt is the dense path's point at arc length s (clamped).
func pathAt(d [][4]float64, s float64) (x, z, w float64) {
	n := len(d)
	if s <= 0 {
		return d[0][0], d[0][1], d[0][2]
	}
	if s >= d[n-1][3] {
		return d[n-1][0], d[n-1][1], d[n-1][2]
	}
	k := sort.Search(n, func(i int) bool { return d[i][3] >= s })
	a, b := d[k-1], d[k]
	t := 0.0
	if b[3] > a[3] {
		t = (s - a[3]) / (b[3] - a[3])
	}
	return a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t, a[2] + (b[2]-a[2])*t
}

// chord is the rectangle along the path from arc length s0 to s1, of
// half-width hb (0: the path's width there).
func chord(d [][4]float64, s0, s1, hb float64) Rect {
	x0, z0, w0 := pathAt(d, s0)
	x1, z1, w1 := pathAt(d, s1)
	dx, dz := x1-x0, z1-z0
	l := math.Hypot(dx, dz)
	if l < 1e-9 {
		dx, dz, l = 1, 0, 1
	}
	if hb <= 0 {
		hb = (w0 + w1) / 4
	}
	return Rect{CX: (x0 + x1) / 2, CZ: (z0 + z1) / 2, DX: dx / l, DZ: dz / l, HA: l / 2, HB: hb}
}

// Lay lays the ruins out on the ground (see the file comment). The
// library's variants supply the footprints.
func Lay(st Settings, g Ground, lib []Variant) *Layout {
	out := &Layout{}
	d := densePath(st.Path, 0.5)
	if len(d) < 2 {
		return out
	}
	out.Path = d
	total := d[len(d)-1][3]
	rng := noise.NewRNG(noise.Hash64(st.Seed, 0x4C41_594F))
	tread := math.Max(st.Tread, 0.2)
	rise := math.Max(st.Rise, 0.05)
	maxSteps := max(int(math.Floor(st.MaxFlight/tread)), 1)
	clear := math.Max(st.Clearance, 0.05)

	// walkHigh is the highest ground under a chord's walkway strip: what a
	// floor must clear.
	walkHigh := func(r Rect) float64 {
		w := r
		w.HB = math.Min(r.HB, st.Walkway/2+1)
		_, hi := groundRange(g, w)
		return hi
	}
	// terraceOn is the terrace on chord r at floor: each side reaches out
	// as far as the ground under it stays under the floor (a side slope's
	// uphill side is cut back), at least the walkway.
	terraceOn := func(r Rect, floor float64) Terrace {
		ext := [2]float64{}
		for si, sg := range []float64{1, -1} {
			e := math.Min(st.Walkway/2+0.6, r.HB)
			for b := e + 0.5; b <= r.HB+1e-9; b += 0.5 {
				ok := true
				na := max(int(math.Ceil(2*r.HA)), 1)
				for i := 0; i <= na && ok; i++ {
					x, z := r.World(-r.HA+2*r.HA*float64(i)/float64(na), sg*b)
					ok = g(x, z) <= floor-clear*0.5
				}
				if !ok {
					break
				}
				e = b
			}
			ext[si] = e
		}
		shift := (ext[0] - ext[1]) / 2
		nr := r
		nr.CX, nr.CZ = r.World(0, shift)
		nr.HB = (ext[0] + ext[1]) / 2
		lo, hi := groundRange(g, nr)
		return Terrace{Rect: nr, Floor: floor, GroundLo: lo, GroundHi: hi, Bottom: math.Min(lo, floor) - st.Foundation,
			Parapet: [4]bool{true, true, true, true}, WalkB: -shift}
	}
	// Terraces and stairs.
	s := 0.0
	lt := rng.Range(st.TerraceLength[0], st.TerraceLength[1])
	lt = math.Min(lt, total)
	r := chord(d, 0, lt, 0)
	floor := walkHigh(r) + clear
	for {
		out.Terraces = append(out.Terraces, terraceOn(r, floor))
		s += lt
		if total-s < 4 {
			break
		}
		// The next terrace: its length drawn now, its start found by the
		// stair.
		nl := rng.Range(st.TerraceLength[0], st.TerraceLength[1])
		// Steep ground ahead (falling near a stair's own slope) wants short
		// landings between flights, or the floors would stand ever higher
		// over it.
		{
			x0, z0, _ := pathAt(d, s)
			x1, z1, _ := pathAt(d, math.Min(s+25, total))
			if run := math.Hypot(x1-x0, z1-z0); run > 1 {
				fall := (g(x0, z0) - g(x1, z1)) / run
				nl *= sdf.Clamp(1-fall/(rise/tread)*0.6, 0.5, 1)
			}
		}
		if total-s-nl < st.TerraceLength[0]*0.5 {
			nl = total - s
		}
		var steps int
		var nr Rect
		var nhi, nfloor float64
		for steps = 0; steps <= 400; steps++ {
			run := float64(steps) * tread
			if s+run+2 > total {
				break
			}
			e := math.Min(s+run+nl, total)
			nr = chord(d, s+run, e, 0)
			nhi = walkHigh(nr)
			want := nhi + clear
			drop := floor - want
			switch {
			case math.Abs(drop) < 0.06:
				nfloor = floor
			case math.Abs(drop) <= float64(steps)*rise:
				nfloor = want
			case drop > 0 && steps >= maxSteps:
				// The ground falls faster than a stair can: the floor stands
				// on a foundation.
				nfloor = floor - float64(steps)*rise
			default:
				continue
			}
			break
		}
		if steps > 400 || s+float64(steps)*tread+2 > total {
			break
		}
		if steps > 0 {
			sr := chord(d, s, s+float64(steps)*tread, st.Walkway/2)
			drop := floor - nfloor
			sl := Stair{Rect: sr, Top: floor, Bottom: nfloor, Steps: steps, Rise: drop / float64(steps),
				Tread: 2 * sr.HA / float64(steps)}
			slo, _ := groundRange(g, sr)
			sl.Base = math.Min(slo, math.Min(floor, nfloor)) - st.Foundation
			out.Stairs = append(out.Stairs, sl)
		}
		s += float64(steps) * tread
		lt = math.Min(s+nl, total) - s
		r, floor = nr, nfloor
		// A floor under the ground (a stair could not climb to it) is raised:
		// the ground never comes through a floor.
		if floor < nhi+clear*0.5 {
			floor = nhi + clear
			if k := len(out.Stairs) - 1; k >= 0 && steps > 0 {
				sl := &out.Stairs[k]
				sl.Bottom = floor
				sl.Rise = (sl.Top - floor) / float64(sl.Steps)
			}
		}
	}

	// Buildings on the terraces.
	byKind := map[string][]*Variant{}
	for i := range lib {
		byKind[lib[i].Kind] = append(byKind[lib[i].Kind], &lib[i])
	}
	pick := func(kinds []string, fit float64, ruined float64) *Variant {
		var c []*Variant
		for _, k := range kinds {
			for _, v := range byKind[k] {
				// (Scaled down to 0.8 if need be.)
				if v.HZ*2*0.8 <= fit && v.HX*2 <= fit*2.6 {
					c = append(c, v)
				}
			}
		}
		if len(c) == 0 {
			return nil
		}
		// Ruined variants by the ruin share.
		var want []*Variant
		wantRuin := rng.Chance(ruined)
		for _, v := range c {
			if v.Ruined == wantRuin {
				want = append(want, v)
			}
		}
		if len(want) == 0 {
			want = c
		}
		return want[rng.Intn(len(want))]
	}
	scale := func() float64 { return rng.Range(st.Scale[0], math.Max(st.Scale[0], st.Scale[1])) }
	for ti := range out.Terraces {
		t := &out.Terraces[ti]
		aLo := -t.HA + 1.0
		// A gateway across the top of the terrace, on the walkway.
		if gv := byKind[KindGate]; len(gv) > 0 && ti > 0 && rng.Chance(st.Gates) {
			v := gv[rng.Intn(len(gv))]
			sc := math.Min(1, (t.HB-math.Abs(t.WalkB)-0.6)/v.HX)
			if sc > 0.75 && v.Passage*sc*2 >= st.Walkway && 2*v.HZ*sc < t.HA {
				a := -t.HA + v.HZ*sc + 0.4
				x, z := t.World(a, t.WalkB)
				out.Instances = append(out.Instances, Instance{Variant: v.Name, X: x, Y: t.Floor, Z: z, Yaw: t.Yaw(), Scale: sc, On: ti})
				aLo = a + v.HZ*sc + 0.8
				t.Parapet[2] = false
			}
		}
		var walled [2]bool
		// Walls along the long edges, segment by segment.
		if wv := byKind[KindWall]; len(wv) > 0 {
			for si, side := range []float64{1, -1} {
				if !rng.Chance(st.Walls) {
					continue
				}
				t.Parapet[si] = false
				walled[si] = true
				n := max(int(math.Round(2*t.HA/8)), 1)
				segL := 2 * t.HA / float64(n)
				for k := 0; k < n; k++ {
					if rng.Chance(0.12) {
						continue // a gap
					}
					v := wv[0]
					for _, w := range wv {
						if w.Ruined == rng.Chance(st.Ruin) {
							v = w
							break
						}
					}
					a := -t.HA + segL*(float64(k)+0.5)
					x, z := t.World(a, side*(t.HB-0.55))
					out.Instances = append(out.Instances, Instance{Variant: v.Name, X: x, Y: t.Floor, Z: z,
						Yaw: math.Atan2(-t.DZ, t.DX) + 0, Scale: segL / 8, On: ti})
				}
			}
		}
		// A colonnade down both sides of the walkway, some columns fallen.
		colonnade := false
		if cv := byKind[KindColumn]; len(cv) > 0 && t.HA > 6 && rng.Chance(0.55) {
			colonnade = true
			n := int(math.Floor((2*t.HA - 3) / 3.4))
			fv := byKind[KindFallen]
			for k := 0; k <= n; k++ {
				a := -t.HA + 1.5 + 3.4*float64(k)
				if a < aLo-0.5 {
					continue
				}
				for _, side := range []float64{-1, 1} {
					x, z := t.World(a, t.WalkB+side*(st.Walkway/2+0.7))
					switch {
					case rng.Chance(st.Ruin * 0.5):
						if len(fv) > 0 && rng.Chance(0.5) {
							// Fallen outward, away from the walkway.
							x2, z2 := t.World(a, t.WalkB+side*(st.Walkway/2+2.6)) // the drums start 2.2 m back from the origin
							out.Instances = append(out.Instances, Instance{Variant: fv[rng.Intn(len(fv))].Name, X: x2, Y: t.Floor, Z: z2,
								Yaw: math.Atan2(-side*t.DX, -side*t.DZ) + rng.Range(-0.4, 0.4), Scale: 1, On: ti})
						}
					default:
						out.Instances = append(out.Instances, Instance{Variant: cv[0].Name, X: x, Y: t.Floor, Z: z, Yaw: t.Yaw(), Scale: 1, On: ti})
					}
				}
			}
		}
		for si, side := range []float64{-1, 1} {
			bIn := side*t.WalkB + st.Walkway/2 + 0.9
			if colonnade {
				bIn += 0.9
			}
			bOut := t.HB - 0.8
			if walled[1-si] {
				bOut = t.HB - 1.6
			}
			fit := bOut - bIn
			a := aLo + rng.Range(0, 2)
			for a < t.HA-1.5 {
				if !rng.Chance(st.Buildings) {
					sp := rng.Range(3, 7)
					x, z := t.World(a+sp/2, side*(bIn+fit/2))
					out.Spots = append(out.Spots, Spot{x, t.Floor, z})
					a += sp
					continue
				}
				v := pick([]string{KindTower, KindTower, KindShrine, KindPavilion, KindPavilion}, fit, st.Ruin)
				if v == nil {
					break
				}
				sc := math.Min(scale(), fit/(2*v.HZ))
				if a+2*v.HX*sc > t.HA-0.8 {
					// The last one does not fit: a column or rubble.
					v = pick([]string{KindColumn, KindRubble}, fit, 0.5)
					if v == nil || a+2*v.HX > t.HA-0.8 {
						break
					}
					sc = 1
				}
				ha, hb := v.HX*sc, v.HZ*sc
				b := side * (bIn + hb)
				x, z := t.World(a+ha, b)
				// Front (+z) to the walkway: local +z along −side·B.
				fx, fz := t.DZ*side, -t.DX*side
				out.Instances = append(out.Instances, Instance{Variant: v.Name, X: x, Y: t.Floor, Z: z, Yaw: math.Atan2(fx, fz), Scale: sc, On: ti})
				a += 2*ha + rng.Range(0.8, 3.5)
			}
		}
	}

	// Buildings on the slope, on plinths.
	occupied := func(r Rect) bool {
		for _, t := range out.Terraces {
			if r.Overlaps(t.Rect, 3) {
				return true
			}
		}
		for _, s := range out.Stairs {
			if r.Overlaps(s.Rect, 3) {
				return true
			}
		}
		for _, p := range out.Plinths {
			if r.Overlaps(p.Rect, 2) {
				return true
			}
		}
		return false
	}
	for tries := 0; tries < st.Outliers*12 && countOutliers(out) < st.Outliers; tries++ {
		sAt := rng.Range(0, total)
		px, pz, pw := pathAt(d, sAt)
		r0 := chord(d, math.Max(sAt-1, 0), math.Min(sAt+1, total), 1)
		side := 1.0
		if rng.Bool() {
			side = -1
		}
		v := pick([]string{KindTower, KindTower, KindShrine, KindTower}, 1e9, st.Ruin)
		if v == nil {
			break
		}
		sc := scale()
		if st.OutlierScale[0] > 0 {
			sc = rng.Range(st.OutlierScale[0], math.Max(st.OutlierScale[0], st.OutlierScale[1]))
		}
		dist := pw/2 + 3 + v.HZ*sc + rng.Range(0, st.Spread)
		cx, cz := px-r0.DZ*side*dist, pz+r0.DX*side*dist
		// Front toward the path.
		fx, fz := r0.DZ*side, -r0.DX*side
		pr := Rect{CX: cx, CZ: cz, DX: -fz, DZ: fx, HA: v.HX*sc + 0.7, HB: v.HZ*sc + 0.7}
		// (A axis = local x after the yaw turns +z to the front.)
		if occupied(pr) {
			continue
		}
		lo, hi := groundRange(g, pr)
		top := hi + clear + 0.3
		if top-lo > st.MaxPlinth {
			continue
		}
		out.Plinths = append(out.Plinths, Plinth{Rect: pr, Top: top, Bottom: lo - st.Foundation})
		out.Instances = append(out.Instances, Instance{Variant: v.Name, X: cx, Y: top, Z: cz, Yaw: math.Atan2(fx, fz), Scale: sc, On: -1})
	}

	// Debris: rubble and fallen columns in the free spots and at the feet
	// of ruined buildings.
	if st.Debris > 0 {
		rub := append(append([]*Variant(nil), byKind[KindRubble]...), byKind[KindFallen]...)
		if len(rub) > 0 {
			var keep []Spot
			for _, sp := range out.Spots {
				if rng.Chance(st.Debris * 0.6) {
					v := rub[rng.Intn(len(rub))]
					out.Instances = append(out.Instances, Instance{Variant: v.Name, X: sp.X, Y: sp.Y, Z: sp.Z, Yaw: rng.Range(0, 2*math.Pi), Scale: rng.Range(0.8, 1.1), On: -2})
					continue
				}
				keep = append(keep, sp)
			}
			out.Spots = keep
			n := len(out.Instances)
			for i := 0; i < n; i++ {
				in := out.Instances[i]
				v := ByName(lib, in.Variant)
				if v == nil || !v.Ruined || v.Kind != KindTower || in.On != -1 || !rng.Chance(st.Debris) {
					continue
				}
				// At a plinth's foot, on the ground, half buried.
				a := rng.Range(0, 2*math.Pi)
				rr := v.HX*in.Scale + 2.5
				x, z := in.X+rr*math.Cos(a), in.Z+rr*math.Sin(a)
				rv := byKind[KindRubble]
				if len(rv) == 0 {
					break
				}
				out.Instances = append(out.Instances, Instance{Variant: rv[rng.Intn(len(rv))].Name, X: x, Y: g(x, z) - 0.25, Z: z,
					Yaw: rng.Range(0, 2*math.Pi), Scale: rng.Range(0.9, 1.3), On: -2})
			}
		}
	}
	return out
}

func countOutliers(l *Layout) int { return len(l.Plinths) }
