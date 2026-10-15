package ruins

// site.go — the ground works as one signed-distance field in world space:
// the terraces (battered retaining walls with a string course and an
// overhanging coping, buttresses down the tall ones, a crenellated parapet
// round the floor opened for the walkway), the stairs (steps with sloped
// cheek walls, their bodies reaching into the ground) and the plinths the
// slope's buildings stand on. A plan grid finds the parts near a point, so
// a site of a hundred parts costs a few of them a sample.

import (
	"math"

	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/sdf"
)

// sitePart is one part of the site.
type sitePart struct {
	box sdf.Box
	f   func(q sdf.Vec3) float64
	// crown marks a parapet: fine work over the floors, weathered at a
	// fraction of the walls' amount (crownWeather) so its merlons keep
	// their corners at the site's coarse cell instead of eroding to lumps.
	crown bool
}

// crownWeather is the share of the site's weathering the parapets take.
const crownWeather = 0.3

// Site is the site's field.
type Site struct {
	parts []sitePart
	cell  float64
	minX  float64
	minZ  float64
	nx    int
	nz    int
	grid  [][]int32
	cuts  []sdf.Shape
	cgrid [][]int32
	box   sdf.Box
	shape sdf.Shape
}

// siteReach is how far round a part the grid finds it, and the bound the
// field answers where no part is that near.
const siteReach = 8.0

// batter is the retaining walls' lean: metres back per metre up.
const batter = 0.06

// retaining is a battered wall's profile from bottom to the floor at top:
// the wall leaning back, a string course, a plain band and an overhanging
// coping with a chamfered top edge.
func retaining(bottom, top float64) *Profile {
	h := top - bottom
	return NewProfile(bottom, batter*math.Max(h-0.9, 0)+0.04).Slope(math.Max(h-0.9, 0.01), 0.04).
		To(0.13).Up(0.12).To(0.04).Up(0.4).To(0.26).Up(0.32).Slope(0.06, 0.2).Build()
}

// NewSite builds the site's field from a layout; weatherAmt as a
// building's.
func NewSite(l *Layout, seed uint64, walkway, weatherAmt float64) *Site {
	s := &Site{cell: 8}
	rng := noise.NewRNG(noise.Hash64(seed, 0x5349_5445))
	for ti := range l.Terraces {
		body, crown := terracePart(l.Terraces[ti], walkway)
		s.parts = append(s.parts, body, crown)
	}
	for _, st := range l.Stairs {
		s.parts = append(s.parts, stairPart(st))
	}
	for _, p := range l.Plinths {
		prof := retaining(p.Bottom, p.Top)
		r := p.Rect
		top := p.Top
		s.parts = append(s.parts, sitePart{box: rectBox(r, p.Bottom, p.Top, batter*(p.Top-p.Bottom)+0.3), f: func(q sdf.Vec3) float64 {
			a, b := r.Local(q[0], q[2])
			aa, ab := math.Abs(a), math.Abs(b)
			d := prof.Dist(math.Max(aa-r.HA, ab-r.HB), q[1])
			return coursed(d, aa-r.HA > ab-r.HB, a, b, q[1], top)
		}})
	}
	if len(s.parts) == 0 {
		return s
	}
	// Bites out of the parapets and copings at terrace corners: masonry lost.
	var cuts []sdf.Shape
	for _, t := range l.Terraces {
		for k := 0; k < 3; k++ {
			ca, cb := t.HA*(2*rng.Float64()-1), t.HB
			if rng.Bool() {
				cb = -cb
			}
			if rng.Chance(0.4) {
				ca, cb = t.HA, (2*rng.Float64()-1)*t.HB
				if math.Abs(cb-t.WalkB) < walkway/2+1.5 {
					continue
				}
			}
			x, z := t.World(ca, cb)
			r := rng.Range(0.5, 1.3)
			b := sdf.RoundBox(sdf.Vec3{}, sdf.Vec3{r, r * 0.8, r}, 0.05)
			b = sdf.Rotate(b, sdf.RotY(rng.Range(0, math.Pi)).Mul(sdf.RotX(rng.Range(-0.5, 0.5))))
			b = sdf.Translate(b, sdf.Vec3{x, t.Floor + rng.Range(-0.3, 0.6), z})
			cuts = append(cuts, sdf.Displace(b, sdf.FBM(noise.Hash64(seed, uint64(len(cuts)), 9), 0.8, 2, 0.5), 0.3*r))
		}
	}
	// The plan grid.
	s.box = s.parts[0].box
	for _, p := range s.parts {
		s.box = s.box.Union(p.box)
	}
	s.minX, s.minZ = s.box.Lo[0]-siteReach, s.box.Lo[2]-siteReach
	s.nx = int(math.Ceil((s.box.Hi[0]+siteReach-s.minX)/s.cell)) + 1
	s.nz = int(math.Ceil((s.box.Hi[2]+siteReach-s.minZ)/s.cell)) + 1
	s.grid = make([][]int32, s.nx*s.nz)
	for i, p := range s.parts {
		i0, i1 := s.ix(p.box.Lo[0]-siteReach), s.ix(p.box.Hi[0]+siteReach)
		j0, j1 := s.iz(p.box.Lo[2]-siteReach), s.iz(p.box.Hi[2]+siteReach)
		for j := j0; j <= j1; j++ {
			for ii := i0; ii <= i1; ii++ {
				s.grid[j*s.nx+ii] = append(s.grid[j*s.nx+ii], int32(i))
			}
		}
	}
	s.cuts = cuts
	s.cgrid = make([][]int32, s.nx*s.nz)
	lip := 1.35
	for i, c := range cuts {
		lip = math.Max(lip, c.Lip)
		i0, i1 := s.ix(c.Box.Lo[0]), s.ix(c.Box.Hi[0])
		j0, j1 := s.iz(c.Box.Lo[2]), s.iz(c.Box.Hi[2])
		for j := j0; j <= j1; j++ {
			for ii := i0; ii <= i1; ii++ {
				s.cgrid[j*s.nx+ii] = append(s.cgrid[j*s.nx+ii], int32(i))
			}
		}
	}
	base := sdf.Shape{F: func(q sdf.Vec3) float64 { return s.cut(q, false) }, Box: s.box, Lip: lip}
	crown := sdf.Shape{F: func(q sdf.Vec3) float64 { return s.cut(q, true) }, Box: s.box, Lip: lip}
	wb := weather(base, noise.Hash64(seed, 0x5349), weatherAmt)
	wc := weather(crown, noise.Hash64(seed, 0x5349), weatherAmt*crownWeather)
	s.shape = sdf.Shape{F: func(q sdf.Vec3) float64 { return math.Min(wb.F(q), wc.F(q)) },
		Box: wb.Box.Union(wc.Box), Lip: math.Max(wb.Lip, wc.Lip)}
	return s
}

func (s *Site) ix(x float64) int { return min(max(int(math.Floor((x-s.minX)/s.cell)), 0), s.nx-1) }
func (s *Site) iz(z float64) int { return min(max(int(math.Floor((z-s.minZ)/s.cell)), 0), s.nz-1) }

// raw is the union of the parts (the parapets, crown, or the rest),
// without weathering.
func (s *Site) raw(q sdf.Vec3, crown bool) float64 {
	d := siteReach
	for _, pi := range s.grid[s.iz(q[2])*s.nx+s.ix(q[0])] {
		p := &s.parts[pi]
		if p.crown != crown || boxDist(q, p.box) >= d {
			continue
		}
		if v := p.f(q); v < d {
			d = v
		}
	}
	return d
}

// cut is raw less the bites near q (Model's rule for skipping them).
func (s *Site) cut(q sdf.Vec3, crown bool) float64 {
	d := s.raw(q, crown)
	for _, ci := range s.cgrid[s.iz(q[2])*s.nx+s.ix(q[0])] {
		c := &s.cuts[ci]
		if bd := boxDist(q, c.Box); bd > 0 && bd >= -d {
			continue
		}
		if v := -c.F(q); v > d {
			d = v
		}
	}
	return d
}

// Empty reports whether the site has no parts.
func (s *Site) Empty() bool { return len(s.parts) == 0 }

// Shape is the site's field, weathered.
func (s *Site) Shape() sdf.Shape { return s.shape }

// Box is the site's bounds.
func (s *Site) Box() sdf.Box { return s.box }

// rectBox is a rectangle's world box from y0 to y1, grown by g.
func rectBox(r Rect, y0, y1, g float64) sdf.Box {
	ex := math.Abs(r.DX)*r.HA + math.Abs(r.DZ)*r.HB + g
	ez := math.Abs(r.DZ)*r.HA + math.Abs(r.DX)*r.HB + g
	return sdf.Box{Lo: sdf.Vec3{r.CX - ex, y0, r.CZ - ez}, Hi: sdf.Vec3{r.CX + ex, y1, r.CZ + ez}}
}

// terracePart is a terrace: its battered body, buttresses on the long
// sides where the wall is tall, its parapet.
func terracePart(t Terrace, walkway float64) (body, crown sitePart) {
	prof := retaining(t.Bottom, t.Floor)
	h := t.Floor
	tall := t.Floor-t.GroundLo > 3.5
	nb := max(int(math.Floor(2*t.HA/6.5)), 1)
	open := walkway/2 + 0.35
	const (
		pt = 0.5  // parapet thickness
		ph = 0.62 // its height to the merlons
	)
	f := func(q sdf.Vec3) float64 {
		a, b := t.Local(q[0], q[2])
		y := q[1]
		aa, ab := math.Abs(a), math.Abs(b)
		aw := math.Abs(b - t.WalkB)
		d := prof.Dist(math.Max(aa-t.HA, ab-t.HB), y)
		// Where the walkway leaves the ends (onto a stair, or the next
		// terrace) the body stops square at the floor's edge: no coping or
		// batter standing into the stair's first tread.
		if aw < open {
			d = math.Max(d, aa-t.HA)
		}
		d = coursed(d, aa-t.HA > ab-t.HB, a, b, y, h)
		// Buttresses: piers down the long sides, a sloped top.
		if tall && y < h-1 {
			bo := ab - t.HB
			ar := repeatSym(a, 2*t.HA/float64(nb), nb)
			lean := batter * (h - 0.9 - y)
			pier := math.Max(math.Max(math.Abs(ar)-0.65, bo-lean-0.62), math.Max((y+0.9*(bo-lean)-(h-1.15))/1.35, t.Bottom-y))
			d = math.Min(d, pier)
		}
		return d
	}
	// The parapet: a low wall inside each edge, merlons on it, opened at
	// the ends for the walkway — its own part, weathered lightly (crown).
	// The merlons are big square blocks with a shallow pyramid cap, far
	// enough apart that their gaps read: at the site's 0.1–0.15 m cell a
	// smaller merlon held no corner and read as a cushion.
	const (
		merW   = 0.34 // half width along the parapet
		merH   = 0.5  // height over the parapet's top
		merGap = 1.3  // centre to centre
	)
	pf := func(q sdf.Vec3) float64 {
		a, b := t.Local(q[0], q[2])
		y := q[1]
		aa, ab := math.Abs(a), math.Abs(b)
		aw := math.Abs(b - t.WalkB)
		yr := y - h
		if yr < -0.4 || yr > ph+merH+0.6 {
			return math.Max(-0.4-yr, yr-(ph+merH+0.6)) + 0.3
		}
		walls := 1e9
		wallSeg := func(along, across, halfLen float64) float64 {
			// along: coordinate along the parapet; across: distance in from
			// the edge. The body reaches down into the floor.
			body := Box2(across-pt/2, yr-(ph-0.3)/2, pt/2, (ph+0.3)/2)
			s := along - merGap*math.Round(along/merGap)
			mer := Box2(s, yr-ph-merH/2, merW, merH/2)
			// A shallow pyramid cap: the top falls 7 cm to the edges.
			cap := (yr - ph - merH) + 0.07*(1-math.Max(math.Abs(s)/merW, math.Abs(across-pt/2)/(pt/2)))
			mer = math.Max(mer, cap)
			mer = math.Max(mer, math.Abs(across-pt/2)-pt/2)
			return math.Max(math.Min(body, mer), math.Abs(along)-halfLen)
		}
		if t.Parapet[0] && b > 0 || t.Parapet[1] && b < 0 {
			walls = math.Min(walls, wallSeg(a, t.HB-ab, t.HA))
		}
		if t.Parapet[2] && a < 0 || t.Parapet[3] && a > 0 {
			e := wallSeg(b, t.HA-aa, t.HB)
			e = math.Max(e, open-aw)
			walls = math.Min(walls, e)
		}
		return walls
	}
	body = sitePart{box: rectBox(t.Rect, t.Bottom, t.Floor+0.2, batter*(t.Floor-t.Bottom)+1.0), f: f}
	crown = sitePart{box: rectBox(t.Rect, t.Floor-0.4, t.Floor+ph+merH+0.6, 0.6), f: pf, crown: true}
	return body, crown
}

// The retaining walls' masonry: courses courseH tall of blocks blockL
// long, each course's joints half a block on from the last's, the joints cut
// in as V grooves joint deep.
const (
	courseH = 0.78
	blockL  = 1.55
	joint   = 0.075
)

// coursed cuts the masonry joints into a battered wall's face: d is the
// solid's distance, endFace whether the nearest face is an end (a) face, a
// and b the frame coordinates, y the height, floor the top (the coping and
// the band under it are left plain).
func coursed(d float64, endFace bool, a, b, y, floor float64) float64 {
	if d > 0.3 || d < -0.3 || y > floor-0.95 {
		return d
	}
	along := a
	if endFace {
		along = b
	}
	c := math.Floor(y / courseH)
	jy := fabs(y - math.Round(y/courseH)*courseH)
	off := 0.5 * blockL * float64(int64(c)&1)
	js := fabs(along + off - math.Round((along+off)/blockL)*blockL)
	g := joint - fmin(jy, js)
	if g <= 0 {
		return d
	}
	return d + g
}

// repeatSym is a brought into the cell of n features spaced period apart
// centred on 0, keeping its sign.
func repeatSym(a, period float64, n int) float64 {
	off := float64(n-1) / 2
	i := sdf.Clamp(math.Round(a/period+off), 0, float64(n-1))
	return a - (i-off)*period
}

// stairPart is a flight: its steps (a box per tread, the first reaching
// back into the terrace above and the last on into the one below), sloped
// cheek walls either side.
func stairPart(s Stair) sitePart {
	const (
		cheekW = 0.45
		cheekH = 0.8
	)
	n := s.Steps
	T := s.Tread
	L := 2 * s.HA
	f := func(q sdf.Vec3) float64 {
		a, b := s.Local(q[0], q[2])
		t := a + s.HA
		y := q[1]
		ab := math.Abs(b)
		k := int(math.Floor(t / T))
		d := math.Inf(1)
		for i := k - 1; i <= k+1; i++ {
			var t0, t1, top float64
			switch {
			case i < 0:
				t0, t1, top = -0.6, 0, s.Top
			case i >= n:
				continue
			default:
				t0, t1 = float64(i)*T, float64(i+1)*T
				top = s.Top - float64(i+1)*s.Rise
				if i == n-1 {
					t1 = L + 0.6
				}
			}
			bx := Box2(t-(t0+t1)/2, y-(top+s.Base)/2, (t1-t0)/2, (top-s.Base)/2)
			d = math.Min(d, Extrude(bx, b, -s.HB, s.HB))
		}
		// The first and last boxes reach beyond the neighbours above.
		if k < -1 || k > n {
			// Far beyond the ends: the end boxes alone.
			for _, i := range []int{-1, n - 1} {
				var t0, t1, top float64
				if i < 0 {
					t0, t1, top = -0.6, 0, s.Top
				} else {
					t0, t1, top = float64(i)*T, L+0.6, s.Bottom
				}
				bx := Box2(t-(t0+t1)/2, y-(top+s.Base)/2, (t1-t0)/2, (top-s.Base)/2)
				d = math.Min(d, Extrude(bx, b, -s.HB, s.HB))
			}
		}
		// Cheeks: from the top of the flight to its foot, their tops a
		// sloped line cheekH over the steps' nosings.
		slope := (s.Top - s.Bottom) / L
		line := s.Top - slope*t + cheekH
		ny := (y - line) / math.Sqrt(1+slope*slope)
		ct := math.Max(math.Max(-0.3-t, t-(L+0.3)), math.Max(ny, s.Base-y))
		cheek := Extrude(ct, ab, s.HB, s.HB+cheekW)
		return math.Min(d, cheek)
	}
	top := math.Max(s.Top, s.Bottom) + cheekH + 0.2
	return sitePart{box: rectBox(s.Rect, s.Base, top, cheekW+1.0), f: f}
}

// Touches reports whether any part's box (grown by g) meets the box.
func (s *Site) Touches(b sdf.Box, g float64) bool {
	for _, p := range s.parts {
		if !p.box.Grow(g).Intersect(b).Empty() {
			return true
		}
	}
	return false
}
