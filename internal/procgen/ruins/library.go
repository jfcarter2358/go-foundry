package ruins

// library.go — the building variants: each a shape in its own frame (base
// at y = 0 on the floor it stands on, its front facing +z, centred on the
// origin) with the facts a layout needs (footprint, height, what it is). A
// run meshes each once and places it many times.

import (
	"fmt"
	"math"

	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/sdf"
)

// The variant kinds.
const (
	KindTower    = "tower"
	KindShrine   = "shrine"
	KindPavilion = "pavilion"
	KindGate     = "gate"
	KindWall     = "wall"
	KindColumn   = "column"
	KindFallen   = "fallen"
	KindRubble   = "rubble"
)

// Variant is one building of the library.
type Variant struct {
	Name string
	Kind string
	// HX and HZ are the footprint's half-extents (its base mouldings
	// included); Height its top over its base.
	HX, HZ, Height float64
	// Bury is how far the shape reaches under its base (into the floor).
	Bury float64
	// Cell is the mesher's grid for it at detail 1.
	Cell float64
	// Ruined variants have lost their tops and corners.
	Ruined bool
	// Passage is a gate's walkway half-width (along its z axis); 0 for none.
	Passage float64
	// Family names the material it wears: "temple" or "wall".
	Family string
	// Build makes the shape (seeded by the library's seed).
	Build func() sdf.Shape
}

// Box is the variant's bounds in its own frame.
func (v *Variant) Box() sdf.Box {
	return sdf.Box{Lo: sdf.Vec3{-v.HX - 1, -v.Bury, -v.HZ - 1}, Hi: sdf.Vec3{v.HX + 1, v.Height + 0.5, v.HZ + 1}}
}

// weather is a building's weathering: low swells (no wall is flat after
// two thousand years), a fine grain, and eroded patches where the surface
// has lost a few centimetres.
func weather(s sdf.Shape, seed uint64, amount float64) sdf.Shape {
	if amount <= 0 {
		return s
	}
	swell := sdf.FBM(noise.Hash64(seed, 1), 0.3, 2, 0.5)
	grain := sdf.FBM(noise.Hash64(seed, 2), 2.2, 3, 0.5)
	pits := sdf.FBM(noise.Hash64(seed, 3), 0.9, 3, 0.55)
	f := s.F
	return sdf.Shape{
		F: func(q sdf.Vec3) float64 {
			d := f(q)
			// Far from the surface the noise is not read: the distance is
			// pulled in by its largest reach, so it stays a lower bound.
			if d > 0.5 {
				return d - 0.2*amount
			}
			if d < -0.5 {
				return d + 0.2*amount
			}
			e := 0.05*swell.F(q) + 0.018*grain.F(q)
			if v := pits.F(q) - 0.25; v > 0 {
				e -= 0.16 * v
			}
			return d - amount*e
		},
		Box: s.Box.Grow(0.1 * amount),
		Lip: s.Lip + amount*(0.05*swell.Lip+0.018*grain.Lip+0.16*pits.Lip),
	}
}

// bites cuts n angular, noisy chunks out of the model where rng puts them
// in the box (corners and cornices lost to time), sized r.
func bites(m *Model, rng *noise.RNG, seed uint64, box sdf.Box, n int, r [2]float64) {
	for i := 0; i < n; i++ {
		c := sdf.Vec3{rng.Range(box.Lo[0], box.Hi[0]), rng.Range(box.Lo[1], box.Hi[1]), rng.Range(box.Lo[2], box.Hi[2])}
		// Pushed out to the box's faces, where the corners and edges are.
		for a := 0; a < 3; a += 2 {
			if rng.Bool() {
				if c[a] > (box.Lo[a]+box.Hi[a])/2 {
					c[a] = box.Hi[a]
				} else {
					c[a] = box.Lo[a]
				}
			}
		}
		s := rng.Range(r[0], r[1])
		h := sdf.Vec3{s * rng.Range(0.6, 1.2), s * rng.Range(0.5, 1), s * rng.Range(0.6, 1.2)}
		b := sdf.RoundBox(sdf.Vec3{}, h, 0.05*s)
		b = sdf.Rotate(b, sdf.RotY(rng.Range(0, math.Pi)).Mul(sdf.RotX(rng.Range(-0.5, 0.5))).Mul(sdf.RotZ(rng.Range(-0.5, 0.5))))
		b = sdf.Translate(b, c)
		m.Cut(sdf.Displace(b, sdf.FBM(noise.Hash64(seed, uint64(i), 77), 1/s, 2, 0.5), 0.25*s))
	}
}

// shear cuts away everything above a tilted, broken plane at height h: a
// building whose top has fallen. It returns the highest the break can
// leave standing within half-width reach of the axis.
func shear(m *Model, rng *noise.RNG, seed uint64, h, reach float64) float64 {
	tx, tz := rng.Range(-0.14, 0.14), rng.Range(-0.14, 0.14)
	n := sdf.FBM(noise.Hash64(seed, 91), 0.35, 3, 0.55)
	// The break follows the masonry: block by block (courses 0.55 m, blocks
	// 0.9 m, staggered), each block standing or gone by the plane's height
	// at its middle — a stepped, ragged top, not a smooth cut.
	const course, block = 0.55, 0.9
	bseed := noise.Hash64(seed, 92)
	m.Cut(sdf.Shape{
		F: func(q sdf.Vec3) float64 {
			c := math.Floor(q[1] / course)
			off := 0.5 * block * float64(int64(c)&1)
			bx := math.Floor((q[0] + off) / block)
			bz := math.Floor((q[2] + off) / block)
			cx, cz := (bx+0.5)*block-off, (bz+0.5)*block-off
			plane := h + tx*cx + tz*cz + 1.2*n.F(sdf.Vec3{cx, q[1], cz}) +
				0.6*(noise.CellUnit(noise.Hash64(bseed, uint64(int64(bx)), uint64(int64(bz)), uint64(int64(c))))-0.5)
			top := (math.Floor(plane/course) + 1) * course // the block's course stands whole or not at all
			return (top - q[1]) * 0.7
		},
		Box: sdf.Infinite,
		Lip: 1 + 1.2*n.Lip,
	})
	return h + (math.Abs(tx)+math.Abs(tz))*reach + 1.2 + 0.9
}

// towerSpec is a stepped temple tower's proportions.
type towerSpec struct {
	W, P, WP      float64 // plan half-size, projection depth, projection half-width
	K             float64 // the plinth's scale
	Body          float64 // the main storey's wall height
	Door          bool
	Tiers         int
	TierH         float64
	Setback       float64
	Turrets       bool
	Pavilion      bool // an open pavilion with a dome on top instead of a crown
	Crown         float64
	Ruin          float64 // 0 intact; else the share of the height kept
	RoundTiers    bool    // octagonal upper tiers
	BlindArches   bool
	Weather, Bury float64
}

// tower builds a stepped temple tower (see the package comment).
func tower(sp towerSpec, seed uint64) (sdf.Shape, float64) {
	m := &Model{Far: 8}
	rng := noise.NewRNG(seed)
	plan := CrossPlan(sp.W, sp.W, sp.P, sp.P, sp.WP, sp.WP)
	pb := NewProfile(-sp.Bury, 0.5*sp.K).Up(sp.Bury).Plinth(0, sp.K)
	yBody := pb.Y()
	// The main storey: a string course halfway, a cornice.
	pb.Up(sp.Body * 0.45).To(0.08).Up(0.18).To(0.02).Up(0.08).To(0).Up(sp.Body*0.55 - 0.26)
	yCorn := pb.Y()
	pb.Cornice(0, 1)
	// Rows on the main storey: the door (z faces) and a deity niche (x
	// faces) in the projections; tall blind niches on the side parts.
	ww := sp.W + sp.P
	if sp.Door {
		dw := math.Min(1.1, sp.WP*0.6)
		m.Cut(Niches(Row{HX: sp.WP, HZ: ww, Period: 1, Count: 1, ZFaces: true}, yBody, dw, math.Min(2.6, sp.Body*0.55), 0.3, sp.P+1.2))
		// The cella behind it.
		cw := sp.W - 1.1
		m.Cut(sdf.Cuboid(sdf.Vec3{0, yBody + sp.Body*0.4, 0}, sdf.Vec3{cw, sp.Body * 0.4, cw}))
	} else {
		m.Cut(Niches(Row{HX: sp.WP, HZ: ww, Period: 1, Count: 1, ZFaces: true}, yBody+0.3, sp.WP*0.5, sp.Body*0.3, 0.3, 0.45))
	}
	m.Cut(Niches(Row{HX: ww, HZ: sp.WP, Period: 1, Count: 1, XFaces: true}, yBody+0.3, sp.WP*0.5, sp.Body*0.3, 0.3, 0.45))
	// Tall blind niches on the side parts (between the projection and the
	// corner), below and above the string course.
	side := Row{HX: sp.W, HZ: sp.W, Period: 1.1, ZFaces: true, XFaces: true}
	for _, band := range [][2]float64{{yBody + 0.35, sp.Body*0.45 - 0.75}, {yBody + sp.Body*0.45 + 0.6, sp.Body*0.55 - 1.0}} {
		if band[1] < 1 {
			continue
		}
		m.Cut(bandNiches(side, sp.WP+0.25, sp.W-0.35, band[0], 0.28, band[1], 0.15, 0.22))
	}
	// Dentils under the main cornice.
	lo, hi := CorniceDentilBand(yCorn, 1)
	m.Add(Blocks(Row{HX: sp.W, HZ: sp.W, Period: 0.32, SMax: sp.W - 0.1, ZFaces: true, XFaces: true}, lo, hi, 0.07, 0.12, 0.1))
	m.Add(Blocks(Row{HX: sp.WP, HZ: ww, Period: 0.32, SMax: sp.WP - 0.05, ZFaces: true}, lo, hi, 0.07, 0.12, 0.1))
	m.Add(Blocks(Row{HX: ww, HZ: sp.WP, Period: 0.32, SMax: sp.WP - 0.05, XFaces: true}, lo, hi, 0.07, 0.12, 0.1))
	// The tiers.
	u := 0.0
	k := 0.85
	type tierTop struct{ y, u, k float64 }
	var tops []tierTop
	if sp.Turrets {
		tops = append(tops, tierTop{pb.Y(), 0, 1})
	}
	for t := 0; t < sp.Tiers; t++ {
		// A tier never steps in to less than a 1.4 m half-width: the stack
		// would end in a spike.
		if sp.W+u-sp.Setback < 1.4 {
			break
		}
		u -= sp.Setback
		pb.To(u+0.16*k).SmallBase(u, k)
		yWall := pb.Y()
		pb.Up(sp.TierH * k)
		yC := pb.Y()
		pb.Cornice(u, k)
		// A row of small blind arches (kudus) on the side parts, a big
		// horseshoe window on each projection.
		hw := sp.W + u
		if hw-0.2 > sp.WP+u+0.3 {
			m.Cut(bandNiches(Row{HX: hw, HZ: hw, Period: 0.75 * k, ZFaces: true, XFaces: true}, sp.WP+u+0.2, hw-0.25, yWall+0.25*k,
				0.2*k, sp.TierH*k*0.42, 0.25, 0.14))
		}
		if pw := sp.WP + u; pw > 0.5 {
			m.Cut(Niches(Row{HX: pw, HZ: ww + u, Period: 1, Count: 1, ZFaces: true}, yWall+0.2*k, math.Min(pw*0.6, 0.9*k), sp.TierH*k*0.4, 0.45, 0.35))
			m.Cut(Niches(Row{HX: ww + u, HZ: pw, Period: 1, Count: 1, XFaces: true}, yWall+0.2*k, math.Min(pw*0.6, 0.9*k), sp.TierH*k*0.4, 0.45, 0.35))
		}
		lo, hi := CorniceDentilBand(yC, k)
		if hw > 0.6 {
			m.Add(Blocks(Row{HX: hw, HZ: hw, Period: 0.3 * k, SMax: hw - 0.1, ZFaces: true, XFaces: true}, lo, hi, 0.06*k, 0.1*k, 0.1))
		}
		if sp.Turrets {
			tops = append(tops, tierTop{pb.Y(), u, k})
		}
		k *= 0.92
	}
	yTop := pb.Y()
	topHalf := sp.W + u + 0.5*k/0.92
	pb.To(u + 0.5*k/0.92 - 0.05)
	m.Add(Lathe(plan, pb.Build(), sdf.Vec3{}))
	// Turrets on each tier's corners and over its projections (not the top's:
	// the crown is there).
	for i, tt := range tops {
		if i == len(tops)-1 && sp.Tiers > 0 {
			break
		}
		th := 2.0 * tt.k
		c := sp.W + tt.u + 0.5*tt.k - 0.42*th/2.4 - 0.05
		m.Add(Mirror4(Turret(sdf.Vec3{c, tt.y, c}, th), 0, 0, 0, 0))
		if pw := sp.WP + tt.u; pw > 0.6 {
			pc := ww + tt.u + 0.25*tt.k - 0.3*tt.k
			m.Add(Mirror4(Turret(sdf.Vec3{0, tt.y, pc}, th*0.85), 0, 0, 0, 0))
			m.Add(Mirror4(Turret(sdf.Vec3{pc, tt.y, 0}, th*0.85), 0, 0, 0, 0))
		}
	}
	height := yTop
	if sp.Pavilion {
		// An open pavilion: walls with an arch through each face, hollow,
		// a cornice, a drum and a dome, a crown.
		ph := math.Max(topHalf-0.3, 1.2)
		pp := NewProfile(yTop-0.05, -0.02).To(0.12).Up(0.2).Slope(0.08, 0).Up(2.8)
		pc := pp.Y()
		pp.Cornice(0, 0.7)
		pp.To(-0.25).Up(0.35).To(-0.35)
		yDome := pp.Y()
		pav := Lathe(RectPlan(ph, ph), pp.Build(), sdf.Vec3{})
		m.Add(pav)
		m.Cut(sdf.Cuboid(sdf.Vec3{0, yTop + 0.25 + 1.4, 0}, sdf.Vec3{ph - 0.4, 1.4, ph - 0.4}))
		aw := ph * 0.5
		m.Cut(Niches(Row{HX: ph, HZ: ph, Period: 1, Count: 1, ZFaces: true, XFaces: true}, yTop+0.25, aw, 2.6-ArchTop(aw, 0, 0.25), 0.25, 1.0))
		lo, hi := CorniceDentilBand(pc, 0.7)
		m.Add(Blocks(Row{HX: ph, HZ: ph, Period: 0.25, SMax: ph - 0.1, ZFaces: true, XFaces: true}, lo, hi, 0.05, 0.08, 0.1))
		dr := ph - 0.35
		dome := NewProfile(yDome-0.02, -0.1).Up(0.4).To(0.06).Up(0.12).To(0).ArcV(dr*0.95, -dr*0.98, 8)
		m.Add(Lathe(RibbedPlan(dr, 24, 0.035*dr), dome.Build(), sdf.Vec3{}))
		yc := dome.Y() - 0.1
		cr := math.Max(dr*0.3, 0.35)
		m.Add(Crown(sdf.Vec3{0, yc, 0}, cr))
		height = yc + CrownHeight(cr)
		m.Add(Mirror4(Turret(sdf.Vec3{ph - 0.15, yDome - 0.36, ph - 0.15}, 1.3), 0, 0, 0, 0))
	} else {
		cr := math.Max(math.Min(sp.Crown, topHalf*0.85), 0.4)
		m.Add(Crown(sdf.Vec3{0, yTop - 0.05, 0}, cr))
		height = yTop + CrownHeight(cr)
	}
	if sp.Ruin > 0 {
		top := shear(m, rng, seed, height*sp.Ruin, ww+0.6)
		bites(m, rng, seed, sdf.Box{Lo: sdf.Vec3{-ww, height * sp.Ruin * 0.5, -ww}, Hi: sdf.Vec3{ww, height * sp.Ruin, ww}}, 7, [2]float64{0.6, 1.8})
		height = math.Min(top, height)
	} else {
		bites(m, rng, seed, sdf.Box{Lo: sdf.Vec3{-ww - 0.3, yBody, -ww - 0.3}, Hi: sdf.Vec3{ww + 0.3, height, ww + 0.3}}, 4, [2]float64{0.25, 0.7})
	}
	return weather(m.Shape(), seed, sp.Weather), height
}

// bandNiches cuts tall narrow niches along each face's side parts: between
// sMin and sMax from the face's middle (both halves), sill at y0, half-width
// w, height h (springing at h − w), pointedness point, depth.
func bandNiches(r Row, sMin, sMax, y0, w, h, point, depth float64) sdf.Shape {
	n := max(int(math.Floor((sMax-sMin)/r.Period)), 1)
	pitch := (sMax - sMin) / float64(n)
	hs := math.Max(h-w*(1+point*0.6), w)
	top := ArchTop(w, hs, point)
	return sdf.Shape{
		F: func(q sdf.Vec3) float64 {
			f := FoldRect(q, r.CX, r.CZ, r.HX, r.HZ)
			if f.X && !r.XFaces || !f.X && !r.ZFaces {
				return 1
			}
			i := sdf.Clamp(math.Floor((f.S-sMin)/pitch), 0, float64(n-1))
			s := f.S - (sMin + (i+0.5)*pitch)
			return Extrude(Arch2D(s, q[1]-y0, w, hs, point), f.N, -depth, 2)
		},
		Box: sdf.Box{Lo: sdf.Vec3{r.CX - r.HX - 2, y0, r.CZ - r.HZ - 2}, Hi: sdf.Vec3{r.CX + r.HX + 2, y0 + top, r.CZ + r.HZ + 2}},
		Lip: 1,
	}
}

// pavilion builds a columned hall: a moulded plinth with steps up its
// front, nx × nz columns, beams, a roof slab with a cornice, a stepped
// pyramid roof and a small dome; some columns gone and the roof broken when
// ruined.
func pavilion(hx, hz float64, nx, nz int, ruined bool, seed uint64, weatherAmt, bury float64) (sdf.Shape, float64) {
	m := &Model{Far: 8}
	rng := noise.NewRNG(seed)
	kp := 0.85
	pb := NewProfile(-bury, 0.45*kp).Up(bury).Plinth(0, kp)
	yFloor := pb.Y()
	pb.Up(0.02)
	m.Add(Lathe(RectPlan(hx, hz), pb.Build(), sdf.Vec3{}))
	// Steps up the front (+z), six risers.
	steps := 6
	rise := yFloor / float64(steps)
	tread := 0.32
	sw := math.Min(1.6, hx*0.35)
	for i := 0; i < steps; i++ {
		top := float64(i+1) * rise
		run := float64(steps-i) * tread
		m.Add(sdf.Cuboid(sdf.Vec3{0, (top - bury) / 2, hz + 0.45 + run/2 - 0.3}, sdf.Vec3{sw, (top + bury) / 2, run/2 + 0.3}))
	}
	// Columns.
	colH := 3.6
	r := 0.3
	ix, iz := hx-0.75, hz-0.75
	for i := 0; i < nx; i++ {
		for j := 0; j < nz; j++ {
			x := -ix + 2*ix*float64(i)/float64(max(nx-1, 1))
			z := -iz + 2*iz*float64(j)/float64(max(nz-1, 1))
			if ruined && rng.Chance(0.3) {
				// A stump.
				m.Add(Lathe(OctPlan(r), NewProfile(yFloor-0.02, 0.02).Up(rng.Range(0.3, 1.2)).Build(), sdf.Vec3{x, 0, z}))
				continue
			}
			m.Add(Column(sdf.Vec3{x, yFloor, z}, colH, r))
		}
	}
	yBeam := yFloor + colH
	// Beams along the column lines, and the roof slab with its cornice.
	rp := NewProfile(yBeam-0.02, -0.05).Up(0.4).To(0.1).Up(0.1).To(0)
	yc := rp.Y()
	rp.Cornice(0, 0.8).To(-0.3).Up(0.6).SmallBase(-0.9, 0.7).Up(0.5).Cornice(-0.9, 0.6).To(-1.6).Up(0.5).To(-1.8)
	yTop := rp.Y()
	roof := Lathe(RectPlan(hx-0.3, hz-0.3), rp.Build(), sdf.Vec3{})
	lo, hi := CorniceDentilBand(yc, 0.8)
	m.Add(roof, Blocks(Row{HX: hx - 0.3, HZ: hz - 0.3, Period: 0.28, SMax: hx - 0.4, ZFaces: true, XFaces: true}, lo, hi, 0.06, 0.1, 0.1))
	m.Add(Mirror4(Turret(sdf.Vec3{hx - 1.3, yTop - 1.6, hz - 1.3}, 1.6), 0, 0, 0, 0))
	dr := math.Min(hx, hz) - 2.0
	height := yTop
	if dr > 0.6 {
		dome := NewProfile(yTop-0.02, -0.2).Up(0.35).To(0).ArcV(dr*0.9, -dr*0.98, 8)
		m.Add(Lathe(CirclePlan(dr), dome.Build(), sdf.Vec3{}))
		cr := math.Max(dr*0.3, 0.3)
		m.Add(Crown(sdf.Vec3{0, dome.Y() - 0.08, 0}, cr))
		height = dome.Y() + CrownHeight(cr)
	}
	// The ceiling: the hall is open under the roof slab between the
	// columns (the lathe is solid from the beams up, so nothing to cut).
	if ruined {
		bites(m, rng, seed, sdf.Box{Lo: sdf.Vec3{-hx, yBeam, -hz}, Hi: sdf.Vec3{hx, height, hz}}, 6, [2]float64{0.8, 2.0})
		height = math.Min(shear(m, rng, seed, yBeam+1.4, math.Max(hx, hz)), height)
	} else {
		bites(m, rng, seed, sdf.Box{Lo: sdf.Vec3{-hx, 0, -hz}, Hi: sdf.Vec3{hx, height, hz}}, 3, [2]float64{0.25, 0.6})
	}
	return weather(m.Shape(), seed, weatherAmt), height
}

// gate builds a gateway tower (a gopuram): a long plan across the way, a
// passage through it along z (half-width pw, walkable at y = 0), tiers
// stepping in above, and a barrel-vaulted top with finials along its ridge.
func gate(hx, hz, pw float64, tiers int, seed uint64, weatherAmt, bury float64) (sdf.Shape, float64) {
	m := &Model{Far: 8}
	rng := noise.NewRNG(seed)
	plan := CrossPlan(hx, hz, 0.4, 0.5, hx*0.35, hz*0.5)
	pb := NewProfile(-bury, 0.5).Up(bury).Plinth(0, 1)
	yBody := pb.Y()
	body := 5.2
	pb.Up(body)
	yC := pb.Y()
	pb.Cornice(0, 1)
	u := 0.0
	k := 0.85
	var yW []float64
	for t := 0; t < tiers; t++ {
		u -= 0.75
		pb.To(u+0.16*k).SmallBase(u, k)
		yW = append(yW, pb.Y())
		pb.Up(2.0*k).Cornice(u, k)
		k *= 0.92
	}
	yTop := pb.Y()
	pb.To(u + 0.4)
	m.Add(Lathe(plan, pb.Build(), sdf.Vec3{}))
	// The passage, through along z: straight sides, a pointed head.
	m.Cut(sdf.Shape{
		F: func(q sdf.Vec3) float64 {
			return Extrude(Arch2D(q[0], q[1], pw, body+yBody-1.4-pw*1.2, 0.25), q[2], -hz-3, hz+3)
		},
		Box: sdf.Box{Lo: sdf.Vec3{-pw, 0, -hz - 3}, Hi: sdf.Vec3{pw, body + yBody, hz + 3}},
		Lip: 1,
	})
	// Niches flanking the passage on the long faces; dentils; kudus up the
	// tiers.
	m.Cut(bandNiches(Row{HX: hx, HZ: hz, Period: 1.3, ZFaces: true}, hx*0.35+0.3, hx-0.4, yBody+0.4, 0.35, body-1.0, 0.25, 0.25))
	lo, hi := CorniceDentilBand(yC, 1)
	m.Add(Blocks(Row{HX: hx, HZ: hz, Period: 0.32, SMax: hx - 0.1, ZFaces: true, XFaces: true}, lo, hi, 0.07, 0.12, 0.1))
	for i, y := range yW {
		uu := -0.75 * float64(i+1)
		kk := 0.85 * math.Pow(0.92, float64(i))
		m.Cut(bandNiches(Row{HX: hx + uu, HZ: hz + uu, Period: 0.8 * kk, ZFaces: true, XFaces: true}, 0.3, hx+uu-0.3, y+0.2, 0.22*kk, 1.2*kk, 0.3, 0.14))
	}
	// The vault: a half-cylinder along x on the top, finials on its ridge.
	vr := math.Max(hz+u-0.3, 0.8)
	vl := hx + u - 0.4
	m.Add(sdf.Shape{
		F: func(q sdf.Vec3) float64 {
			y := q[1] - yTop
			d2 := math.Max(math.Hypot(q[2], y)-vr, -y)
			return Extrude(d2, q[0], -vl, vl)
		},
		Box: sdf.Box{Lo: sdf.Vec3{-vl, yTop, -vr}, Hi: sdf.Vec3{vl, yTop + vr, vr}},
		Lip: 1,
	})
	for i := -2; i <= 2; i++ {
		x := float64(i) * vl / 2.5
		m.Add(Lathe(CirclePlan(0.25), NewProfile(yTop+vr-0.2, 0.05).ArcH(0.2, 0.15, 3).ArcV(0.3, -0.05, 3).Slope(0.7, -0.22).Build(), sdf.Vec3{x, 0, 0}))
	}
	height := yTop + vr + 1.2
	bites(m, rng, seed, sdf.Box{Lo: sdf.Vec3{-hx, yBody + body, -hz}, Hi: sdf.Vec3{hx, height, hz}}, 5, [2]float64{0.3, 0.9})
	return weather(m.Shape(), seed, weatherAmt), height
}

// wall builds a boundary wall segment along x (length 2·hl, thickness 2·ht,
// height h): a moulded base and a cornice on both faces, a row of blind
// arches between, crow-stepped merlons on top; its ends cut square so
// segments butt end to end. Broken: lost sections of its top.
func wall(hl, ht, h float64, broken bool, seed uint64, weatherAmt, bury float64) sdf.Shape {
	m := &Model{Far: 8}
	rng := noise.NewRNG(seed)
	pb := NewProfile(-bury, 0.35).Up(bury+0.35).Slope(0.08, 0.25).Up(0.2).Slope(0.1, 0.08).To(0)
	yb := pb.Y()
	pb.Up(h - yb - 0.75)
	yC := pb.Y()
	pb.Cornice(0, 0.6).To(0.05)
	prof := pb.Build()
	top := prof.Y1()
	m.Add(sdf.Shape{
		F: func(q sdf.Vec3) float64 {
			return math.Max(prof.Dist(math.Abs(q[2])-ht, q[1]), math.Abs(q[0])-hl)
		},
		Box: sdf.Box{Lo: sdf.Vec3{-hl, -bury, -ht - 0.5}, Hi: sdf.Vec3{hl, top, ht + 0.5}},
		Lip: 1,
	})
	row := Row{HX: hl + 10, HZ: ht, Period: 1.0, ZFaces: true}
	n := int(math.Floor(2 * hl / 1.0))
	row.Count = n
	m.Cut(Niches(row, yb+0.25, 0.26, yC-yb-0.9, 0.3, 0.12))
	lo, hi := CorniceDentilBand(yC, 0.6)
	m.Add(Blocks(Row{HX: hl + 10, HZ: ht, Period: 0.25, Count: int(2 * hl / 0.25), ZFaces: true}, lo, hi, 0.05, 0.08, 0.1))
	m.Add(Merlons(Row{HX: hl + 10, HZ: 0, Period: 1.0, Count: n, ZFaces: true}, top-0.02, 0.3, 0.7, 2*ht*0.7, 0))
	if broken {
		for i := 0; i < 3; i++ {
			x := rng.Range(-hl, hl)
			s := rng.Range(0.8, 1.6)
			b := sdf.Translate(sdf.Rotate(sdf.RoundBox(sdf.Vec3{}, sdf.Vec3{s, s * 1.2, ht + 0.6}, 0.1), sdf.RotZ(rng.Range(-0.4, 0.4))), sdf.Vec3{x, top + 0.3 - rng.Range(0, 1.4), 0})
			m.Cut(sdf.Displace(b, sdf.FBM(noise.Hash64(seed, uint64(i), 5), 0.8, 2, 0.5), 0.3))
		}
	}
	bites(m, rng, seed, sdf.Box{Lo: sdf.Vec3{-hl, 0, -ht}, Hi: sdf.Vec3{hl, top, ht}}, 2, [2]float64{0.2, 0.45})
	return weather(m.Shape(), seed, weatherAmt)
}

// fallen builds a fallen column: its drums lying where they rolled, and its
// bracket capital on its side.
func fallen(seed uint64, weatherAmt float64) sdf.Shape {
	m := &Model{Far: 8}
	rng := noise.NewRNG(seed)
	r := 0.3
	x := -2.2
	yaw := 0.0
	for i := 0; i < 3; i++ {
		l := rng.Range(0.7, 1.2)
		drum := Lathe(RibbedPlan(r, 16, -0.035), NewProfile(-l, -0.03).To(0).Up(2*l-0.04).To(-0.03).Up(0.02).Build(), sdf.Vec3{})
		yaw += rng.Range(-0.35, 0.35)
		s := sdf.Rotate(drum, sdf.RotY(yaw).Mul(sdf.RotZ(math.Pi/2)))
		m.Add(sdf.Translate(s, sdf.Vec3{x + l, r - 0.06, rng.Range(-0.3, 0.3)}))
		x += 2*l + rng.Range(0.1, 0.5)
	}
	cap := Column(sdf.Vec3{0, -3.3, 0}, 3.9, r)
	capB := sdf.Intersect(cap, sdf.Cuboid(sdf.Vec3{0, 0.3, 0}, sdf.Vec3{1, 0.6, 1}))
	m.Add(sdf.Translate(sdf.Rotate(capB, sdf.RotX(math.Pi/2*0.9).Mul(sdf.RotY(0.4))), sdf.Vec3{x + 0.6, 0.35, 0.2}))
	bites(m, rng, seed, sdf.Box{Lo: sdf.Vec3{-2.2, 0, -0.4}, Hi: sdf.Vec3{x, 0.6, 0.4}}, 2, [2]float64{0.15, 0.3})
	return weather(m.Shape(), seed, weatherAmt)
}

// rubble builds a pile of fallen masonry about 2·hr across: squared
// blocks, slabs and a piece of cornice, heaped highest in the middle.
func rubble(hr float64, seed uint64, weatherAmt float64) (sdf.Shape, float64) {
	m := &Model{Far: 8}
	rng := noise.NewRNG(seed)
	n := int(8 + 10*hr)
	height := 0.4
	for i := 0; i < n; i++ {
		a := rng.Range(0, 2*math.Pi)
		d := hr * math.Sqrt(rng.Float64()) * 0.85
		x, z := d*math.Cos(a), d*math.Sin(a)
		s := rng.Range(0.22, 0.6) * (1 + 0.6*(1-d/hr))
		h := sdf.Vec3{s * rng.Range(0.9, 1.6), s * rng.Range(0.5, 0.9), s * rng.Range(0.7, 1.1)}
		y := (1 - d/hr) * hr * 0.45 * rng.Range(0.4, 1)
		b := sdf.RoundBox(sdf.Vec3{}, h, 0.04)
		b = sdf.Rotate(b, sdf.RotY(rng.Range(0, math.Pi)).Mul(sdf.RotX(rng.Range(-0.4, 0.4))).Mul(sdf.RotZ(rng.Range(-0.4, 0.4))))
		m.Add(sdf.Translate(b, sdf.Vec3{x, y, z}))
		height = math.Max(height, y+h[1])
	}
	// A broken length of cornice on the heap.
	cp := NewProfile(-0.3, 0).Up(0.3).Cornice(0, 0.7).Build()
	cor := sdf.Shape{F: func(q sdf.Vec3) float64 {
		return math.Max(cp.Dist(q[2], q[1]), math.Abs(q[0])-0.9)
	}, Box: sdf.Box{Lo: sdf.Vec3{-0.9, -0.3, -2}, Hi: sdf.Vec3{0.9, 0.8, 0.5}}, Lip: 1}
	cor = sdf.Intersect(cor, sdf.Cuboid(sdf.Vec3{0, 0.2, -0.4}, sdf.Vec3{1, 0.7, 0.9}))
	m.Add(sdf.Translate(sdf.Rotate(cor, sdf.RotY(rng.Range(0, math.Pi)).Mul(sdf.RotZ(0.25))), sdf.Vec3{hr * 0.2, hr * 0.25, -hr * 0.1}))
	// Buried a little: the floor cuts the heap's bottom.
	sh := sdf.Intersect(m.Shape(), sdf.Cuboid(sdf.Vec3{0, height/2 - 0.15, 0}, sdf.Vec3{hr + 1, height/2 + 0.15, hr + 1}))
	return weather(sh, seed, weatherAmt), height
}

// Library is the building variants for a seed: four towers (two intact,
// one with an open pavilion top, one fallen), a small shrine, two
// pavilions (one ruined), a gateway, two walls (one broken), a column, a
// fallen column and two rubble heaps. Weathering scales every surface's
// erosion; bury is how far each reaches under its base.
func Library(seed uint64, weatherAmt float64) []Variant {
	const bury = 0.6
	vs := []Variant{}
	add := func(v Variant) {
		v.Bury = bury
		vs = append(vs, v)
	}
	sd := func(i uint64) uint64 { return noise.Hash64(seed, 0x5255_494E, i) }
	towers := []struct {
		name string
		sp   towerSpec
	}{
		{"tower_a", towerSpec{W: 3.6, P: 0.7, WP: 1.6, K: 1, Body: 5.2, Door: true, Tiers: 5, TierH: 2.2, Setback: 0.62, Turrets: true, Crown: 1.5}},
		{"tower_b", towerSpec{W: 3.2, P: 0.6, WP: 1.4, K: 0.9, Body: 4.6, Tiers: 5, TierH: 2.0, Setback: 0.36, Turrets: true, Pavilion: true, Crown: 1.2}},
		{"tower_c", towerSpec{W: 4.2, P: 0.8, WP: 1.9, K: 1.1, Body: 6.0, Door: true, Tiers: 7, TierH: 2.3, Setback: 0.55, Turrets: true, Crown: 1.8}},
		{"tower_ruin", towerSpec{W: 3.8, P: 0.7, WP: 1.7, K: 1, Body: 5.6, Door: true, Tiers: 5, TierH: 2.2, Setback: 0.6, Turrets: true, Crown: 1.5, Ruin: 0.62}},
		{"shrine", towerSpec{W: 1.7, P: 0.35, WP: 0.8, K: 0.6, Body: 2.6, Tiers: 2, TierH: 1.3, Setback: 0.42, Turrets: true, Crown: 0.9}},
	}
	for i, t := range towers {
		t := t
		sp := t.sp
		sp.Weather, sp.Bury = weatherAmt, bury
		_, h := tower(sp, sd(uint64(i)))
		kind := KindTower
		if t.name == "shrine" {
			kind = KindShrine
		}
		half := sp.W + sp.P + 0.5*sp.K + 0.1
		i := i
		add(Variant{Name: t.name, Kind: kind, HX: half, HZ: half, Height: h, Cell: 0.065, Ruined: sp.Ruin > 0, Family: "temple",
			Build: func() sdf.Shape { s, _ := tower(sp, sd(uint64(i))); return s }})
	}
	{
		_, h := pavilion(4.6, 4.6, 4, 4, false, sd(10), weatherAmt, bury)
		add(Variant{Name: "pavilion", Kind: KindPavilion, HX: 5.2, HZ: 7.0, Height: h, Cell: 0.05, Family: "temple",
			Build: func() sdf.Shape { s, _ := pavilion(4.6, 4.6, 4, 4, false, sd(10), weatherAmt, bury); return s }})
		_, h = pavilion(4.2, 3.4, 4, 3, true, sd(11), weatherAmt, bury)
		add(Variant{Name: "pavilion_ruin", Kind: KindPavilion, HX: 4.8, HZ: 5.8, Height: h, Cell: 0.05, Ruined: true, Family: "temple",
			Build: func() sdf.Shape { s, _ := pavilion(4.2, 3.4, 4, 3, true, sd(11), weatherAmt, bury); return s }})
	}
	{
		_, h := gate(6.5, 3.2, 1.9, 3, sd(12), weatherAmt, bury)
		add(Variant{Name: "gate", Kind: KindGate, HX: 7.4, HZ: 4.1, Height: h, Cell: 0.06, Passage: 1.9, Family: "temple",
			Build: func() sdf.Shape { s, _ := gate(6.5, 3.2, 1.9, 3, sd(12), weatherAmt, bury); return s }})
	}
	add(Variant{Name: "wall", Kind: KindWall, HX: 4, HZ: 0.9, Height: 3.6, Cell: 0.045, Family: "wall",
		Build: func() sdf.Shape { return wall(4, 0.5, 2.9, false, sd(20), weatherAmt, bury) }})
	add(Variant{Name: "wall_broken", Kind: KindWall, HX: 4, HZ: 0.9, Height: 3.6, Cell: 0.045, Ruined: true, Family: "wall",
		Build: func() sdf.Shape { return wall(4, 0.5, 2.9, true, sd(21), weatherAmt, bury) }})
	add(Variant{Name: "column", Kind: KindColumn, HX: 0.6, HZ: 0.6, Height: 4.2, Cell: 0.03, Family: "temple",
		Build: func() sdf.Shape {
			m := &Model{Far: 8}
			m.Add(Column(sdf.Vec3{0, -bury, 0}, 4.2+bury, 0.3))
			return weather(m.Shape(), sd(30), weatherAmt)
		}})
	add(Variant{Name: "column_fallen", Kind: KindFallen, HX: 4.5, HZ: 1.2, Height: 0.9, Cell: 0.03, Ruined: true, Family: "temple",
		Build: func() sdf.Shape { return fallen(sd(31), weatherAmt) }})
	for i, hr := range []float64{1.6, 2.6} {
		i, hr := i, hr
		_, h := rubble(hr, sd(40+uint64(i)), weatherAmt)
		add(Variant{Name: fmt.Sprintf("rubble_%d", i), Kind: KindRubble, HX: hr + 0.6, HZ: hr + 0.6, Height: h, Cell: 0.035, Ruined: true, Family: "temple",
			Build: func() sdf.Shape { s, _ := rubble(hr, sd(40+uint64(i)), weatherAmt); return s }})
	}
	return vs
}

// ByName is the variant of that name, or nil.
func ByName(vs []Variant, name string) *Variant {
	for i := range vs {
		if vs[i].Name == name {
			return &vs[i]
		}
	}
	return nil
}
