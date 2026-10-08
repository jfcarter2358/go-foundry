package render

// foliage_rainforest_trees.go — the temperate rainforest's trees, as they
// grow in a Hoh valley: Sitka spruce, western hemlock, western redcedar,
// Douglas-fir, bigleaf maple, red alder, vine maple and black cottonwood.
//
// They are life-size (a unit is a metre) and built in far more detail than
// the other biomes' trees: trunks of forty sides with bark modelled in their
// relief, buttressed and fluted feet, surface and stilt roots, every limb and
// branchlet a tube, a few thousand species-specific sprays, moss lying along
// the limbs and hanging from them. The Nanite path draws them as a cluster
// DAG down to voxels, so the detail costs only where it is seen; the classic
// path's far mesh and the shadow proxy are the same tree with its twigs left
// out (foliage_rainforest_kit.go).
//
// Two growth habits cover all eight. EXCURRENT (rfConifer): one leader to
// the top, tiers of limbs off it, each limb feathered with branchlets that
// carry the sprays — the conifers, and an alder's straight stem. DECURRENT
// (rfBroadleaf): a trunk that parts into a few great limbs, each forking again
// and again, leaves at the tips — the maples and the cottonwood. Each species
// then adds what it is known by.

import (
	"math"
	"math/rand"

	"github.com/go-gl/mathgl/mgl32"
)

// rfConiferSpec is an excurrent tree.
type rfConiferSpec struct {
	h, r0 float32 // height; trunk radius at its foot, before the flare
	lean  float32 // how far the top stands off the foot, in units
	// base is how high the trunk's own foot is: 0 on the ground, higher for
	// a tree standing on stilt roots or a stump.
	base float32
	// crownBase is where the live limbs start, as a fraction of the height,
	// and crownR the longest limb.
	crownBase, crownR float32
	// profile is a limb's reach (fraction of crownR) by its place up the
	// crown, t 0 at its base and 1 at the top.
	profile func(t float32) float32
	// pitch is a limb's angle above horizontal where it leaves the trunk.
	pitch func(t float32) float32
	// droop bends a limb down along its length, upturn up toward its tip
	// (per unit, as a turn of direction).
	droop, upturn float32
	// spacing is how much trunk there is between one limb (or whorl) and
	// the next; whorl how many limbs a whorl has (1: a spiral).
	spacing float32
	whorl   int
	// branchlets per unit of limb, how long (a fraction of the limb) and
	// how much they hang.
	branchlets, twigReach, pendant float32
	// The sprays: their tile and colouring, their size, the distance
	// between them along a branchlet, and how much they hang (0 lying flat,
	// 1 hanging straight down).
	tile, part                   int
	spray, sprayEvery, sprayHang float32
	bark                         rfBark
	// buttress lobes, how far out and how high; flare widens the foot.
	lobes                  int
	buttress, buttressH    float32
	flare                  float32
	sides                  int
	nod                    float32 // the leader's nod, in units sideways
	stubs                  int     // dead stubs on the bare bole
	moss, drapes           float32 // moss on the limbs; chance of a hanging curtain
	limbTone0, limbTone1   float32
	deadTop                float32 // the top this fraction of the height is a bare, dead spike
	reachJitter, pitchJitt float32
}

// rfTrunk is a trunk built: its axis and radius by height.
type rfTrunk struct {
	h, base, r0, flare, lean float32
	leanDir, nodDir          mgl32.Vec3
	nod                      float32
	seed                     uint32
	// off moves the whole trunk sideways: a leader sprung from a limb.
	off mgl32.Vec3
}

func (t rfTrunk) frac(y float32) float32 { return min(max((y-t.base)/(t.h-t.base), 0), 1) }

// axis is the trunk's centre at height y.
func (t rfTrunk) axis(y float32) mgl32.Vec3 {
	f := t.frac(y)
	w := mgl32.Vec3{noise3(mgl32.Vec3{y * 0.12, 0, 0}, t.seed) - 0.5, 0, noise3(mgl32.Vec3{0, y * 0.12, 3}, t.seed+1) - 0.5}
	p := mgl32.Vec3{t.off[0], y, t.off[2]}.Add(t.leanDir.Mul(t.lean * f * f)).Add(w.Mul(t.r0 * 0.5))
	if t.nod > 0 {
		k := smoothstep32(0.82, 1, f)
		p = p.Add(t.nodDir.Mul(t.nod * k * k)).Sub(yUp.Mul(t.nod * 0.45 * k * k * k))
	}
	return p
}

// radius is the trunk's radius at height y.
func (t rfTrunk) radius(y float32) float32 {
	f := t.frac(y)
	r := t.r0*float32(math.Pow(float64(1-f), 0.78))*0.94 + 0.03
	return r * (1 + t.flare*float32(math.Exp(-float64(max(y-t.base, 0)/(1.4*t.r0)))))
}

// sway is the trunk's sway at height y.
func (t rfTrunk) sway(y float32) float32 {
	return 0.55 * float32(math.Pow(float64(t.frac(y)), 1.5))
}

// addTrunk adds the trunk from its foot (from y0, below ground) to y1 in
// sections, so its top bends on bones of its own; relief and sides as
// given. Returns nothing: the sections are limbs of depth 0.
func (p *rfPlant) addTrunk(t rfTrunk, y0, y1 float32, sides, sections int, relief rfRelief, tone0, tone1 float32) {
	var ys []float32
	for y := y0; y < y1; {
		ys = append(ys, y)
		y += min(0.12+0.012*max(y-t.base, 0), 0.5)
	}
	ys = append(ys, y1)
	n := len(ys)
	s := float32(0)
	for k := 0; k < sections; k++ {
		i0, i1 := k*(n-1)/sections, (k+1)*(n-1)/sections
		if i1 <= i0 {
			continue
		}
		l := rfLimb{depth: 0, sides: sides, part: partBark, relief: relief, s0: s, order: float32(k + 1),
			sway0: t.sway(ys[i0]), sway1: t.sway(ys[i1]),
			tone0: tone0 + (tone1-tone0)*t.frac(ys[i0]), tone1: tone0 + (tone1-tone0)*t.frac(ys[i1])}
		for i := i0; i <= i1; i++ {
			l.pts = append(l.pts, t.axis(ys[i]))
			l.radii = append(l.radii, t.radius(ys[i]))
		}
		for i := 1; i < len(l.pts); i++ {
			s += l.pts[i].Sub(l.pts[i-1]).Len()
		}
		l.cap = k == sections-1
		p.add(l)
	}
}

// rfConifer builds an excurrent tree from sp.
func rfConifer(r frand, sp rfConiferSpec, seed uint32) (*rfPlant, rfTrunk) {
	p := &rfPlant{farDepth: 1, farEvery: 12, farArea: 0.35, mossSeed: seed}
	t := rfTrunk{h: sp.h, base: sp.base, r0: sp.r0, flare: sp.flare, lean: sp.lean,
		leanDir: yawDir(r.f(0, 2*math.Pi)), nodDir: yawDir(r.f(0, 2*math.Pi)), nod: sp.nod, seed: seed}
	sp.bark.girth = sp.r0
	sp.bark.seed = seed + 11
	relief := rfSum(sp.bark.relief(), rfButtress(sp.lobes, sp.buttress, sp.buttressH, seed+3))
	y0 := sp.base - 0.8
	if sp.base > 0 {
		y0 = sp.base - 0.15
	}
	p.addTrunk(t, y0, sp.h, sp.sides, 4, relief, 0.62, 0.86)
	if sp.limbTone0 == 0 {
		sp.limbTone0, sp.limbTone1 = 0.55, 0.78
	}
	hb := sp.base + sp.crownBase*(sp.h-sp.base)
	top := sp.h * (1 - sp.deadTop)
	az := r.f(0, 2*math.Pi)
	for y := hb; y < top-0.4; y += sp.spacing * r.f(0.75, 1.25) {
		tc := (y - hb) / max(top-hb, 0.1)
		for w := 0; w < max(sp.whorl, 1); w++ {
			if sp.whorl > 1 {
				az += 2*math.Pi/float32(sp.whorl) + r.f(-0.35, 0.35)
			} else {
				az += 2.39996 + r.f(-0.3, 0.3)
			}
			reach := sp.crownR * sp.profile(tc) * (1 + r.f(-1, 1)*max(sp.reachJitter, 0.15))
			if reach < 0.35 {
				continue
			}
			p.coniferLimb(r, t, sp, y, az, reach, tc)
		}
	}
	// Dead stubs on the bare bole below the crown.
	for i := 0; i < sp.stubs; i++ {
		y := sp.base + r.f(0.12, 0.98)*(hb-sp.base)
		if y < sp.base+2 {
			continue
		}
		d := yawDir(r.f(0, 2*math.Pi)).Add(yUp.Mul(r.f(-0.5, 0.15))).Normalize()
		d = d.Add(yUp.Mul(-0.35)).Normalize()
		l := r.f(0.25, 0.9)
		o := t.axis(y)
		lb := limbOf(pathOf(o, d, l+t.radius(y), 3, func(_ float32, _ mgl32.Vec3) mgl32.Vec3 { return yUp.Mul(-0.5) }),
			min(t.radius(y)*0.07, 0.045), 0.01, 0.8, 1, 5, t.sway(y), t.sway(y)+0.05)
		lb.tone0, lb.tone1 = 0.5, 0.62
		lb.cap = true
		p.add(lb)
	}
	// A few sprays crowning the leader.
	if sp.deadTop == 0 {
		// The leader clothed to its tip, so no bare spike shows above the
		// top limbs.
		tip := t.axis(sp.h)
		for k := 0; k < 12; k++ {
			f := float32(k) / 12
			y := sp.h - 0.2 - f*2.8
			d := yUp.Mul(1 - 0.5*f).Add(yawDir(r.f(0, 2*math.Pi)).Mul(0.3 + 0.7*f)).Normalize()
			p.spray(t.axis(y), d, perp(d, r.f(0, math.Pi)), sp.spray*(0.7+0.4*f), sp.tile, sp.part, tip.Sub(yUp.Mul(3)), 0.9, 1.0)
		}
	}
	return p, t
}

// coniferLimb grows one limb of an excurrent tree at height y, toward az,
// reach long, tc of the way up its crown: the limb, its branchlets, their
// sprays, and its moss.
func (p *rfPlant) coniferLimb(r frand, t rfTrunk, sp rfConiferSpec, y, az, reach, tc float32) {
	pitch := sp.pitch(tc) + r.f(-1, 1)*max(sp.pitchJitt, 0.08)
	dir := yawDir(az).Mul(float32(math.Cos(float64(pitch)))).Add(yUp.Mul(float32(math.Sin(float64(pitch)))))
	o := t.axis(y)
	rt := t.radius(y)
	l := reach + rt
	segs := max(int(l/0.7), 4)
	droop, upturn := sp.droop*r.f(0.7, 1.3), sp.upturn*r.f(0.7, 1.3)
	pts := pathOf(o, dir, l, segs, func(u float32, d mgl32.Vec3) mgl32.Vec3 {
		return yUp.Mul(-droop*(1-u) + upturn*u*u)
	})
	r0 := min(rt*0.42, 0.035+0.022*reach)
	sw0 := t.sway(y)
	lb := limbOf(pts, r0, 0.012, 0.9, 1, 9, sw0, sw0+0.55+0.15*reach/sp.crownR)
	if r0 < 0.09 {
		lb.sides = 6
	}
	lb.tone0, lb.tone1 = sp.limbTone0, sp.limbTone1
	if sp.moss > 0 {
		lb.moss = sp.moss * (1.1 - tc) * r.f(0.5, 1.2)
	}
	li := p.add(lb)
	limb := &p.limbs[li]
	crownC := t.axis(y)
	crownW := max(sp.crownR*sp.profile(tc), 0.5)
	n := max(int(l*sp.branchlets), 2)
	for k := 0; k < n; k++ {
		u := 0.14 + 0.84*(float32(k)+r.f(0.2, 0.8))/float32(n)
		bp, bd, br := limb.at(u)
		side := bd.Cross(yUp)
		if side.Len() < 1e-3 {
			side = perp(bd, 0)
		}
		side = side.Normalize()
		if k%2 == 1 {
			side = side.Mul(-1)
		}
		bdir := bd.Mul(0.55).Add(side.Mul(0.85)).Add(yUp.Mul(0.12 - sp.pendant)).Normalize()
		bl := l * sp.twigReach * (1 - 0.55*u) * r.f(0.75, 1.25)
		if bl < 0.15 {
			continue
		}
		hang := sp.pendant
		bpts := pathOf(bp, bdir, bl, 3, func(_ float32, _ mgl32.Vec3) mgl32.Vec3 { return yUp.Mul(-hang * 0.8) })
		sw := limb.swayAt(u)
		tw := limbOf(bpts, max(br*0.5, 0.008), 0.004, 1, 2, 4, sw, sw+0.25)
		tw.tone0, tw.tone1 = sp.limbTone0+0.05, sp.limbTone1
		ti := p.add(tw)
		twig := &p.limbs[ti]
		ns := max(int(bl/sp.sprayEvery), 1)
		for s := 0; s < ns; s++ {
			v := 0.25 + 0.75*(float32(s)+r.f(0.1, 0.9))/float32(ns)
			sp0, sd, _ := twig.at(v)
			hd := sd.Mul(1 - sp.sprayHang).Add(yUp.Mul(-sp.sprayHang)).Add(mgl32.Vec3{r.f(-0.2, 0.2), r.f(-0.15, 0.2), r.f(-0.2, 0.2)})
			if hd.Len() < 1e-3 {
				hd = sd
			}
			hd = hd.Normalize()
			sdSide := hd.Cross(yUp)
			if sp.sprayHang > 0.5 {
				sdSide = side
			}
			sdSide = rotAxis(sdSide.Normalize(), hd, r.f(-0.35, 0.35))
			depth := sp0.Sub(crownC)
			depth[1] = 0
			tone := 0.55 + 0.5*min(depth.Len()/crownW, 1)
			size := sp.spray * r.f(0.8, 1.15) * (1 - 0.25*u)
			p.spray(sp0, hd, sdSide, size, sp.tile, sp.part, crownC, twig.swayAt(v), tone)
			// A second spray crossing it, turned about the twig, so the
			// foliage has depth from every side rather than lying in one
			// sheet.
			p.spray(sp0.Add(hd.Mul(size*0.15)), hd, rotAxis(sdSide, hd, r.f(0.9, 1.3)), size*0.85, sp.tile, sp.part, crownC, twig.swayAt(v), tone*0.93)
		}
	}
	// The limb itself clothed along its length, both sides, so a limb is a
	// mat of foliage and not a bare stick with sprays off it.
	for u := float32(0.2); u < 0.97; u += sp.spray * 0.45 / max(l, 0.1) {
		bp, bd, _ := limb.at(u)
		for _, sgn := range [2]float32{-1, 1} {
			side := bd.Cross(yUp)
			if side.Len() < 1e-3 {
				side = perp(bd, 0)
			}
			d := bd.Mul(0.7).Add(side.Normalize().Mul(0.7 * sgn)).Add(yUp.Mul(-sp.sprayHang * 0.6)).Normalize()
			depth := bp.Sub(crownC)
			depth[1] = 0
			p.spray(bp, d, d.Cross(yUp), sp.spray*r.f(0.75, 1.05), sp.tile, sp.part, crownC, limb.swayAt(u), 0.5+0.45*min(depth.Len()/crownW, 1))
		}
	}
	// The limb's own tip.
	tp, td, _ := limb.at(1)
	for k := 0; k < 2; k++ {
		d := td.Add(mgl32.Vec3{r.f(-0.3, 0.3), r.f(-0.1, 0.2), r.f(-0.3, 0.3)}).Normalize()
		p.spray(tp.Sub(td.Mul(sp.spray*0.3)), d, d.Cross(yUp), sp.spray*r.f(0.9, 1.15), sp.tile, sp.part, crownC, limb.sway1, 1.02)
	}
	if r.Float32() < sp.drapes {
		for k := 0; k < 1+r.Intn(3); k++ {
			u := r.f(0.15, 0.75)
			dp, _, dr := limb.at(u)
			p.drape(dp.Sub(yUp.Mul(dr*0.6)), r.f(0.5, 1.8), r.f(0.45, 0.8), r.f(0, math.Pi), limb.swayAt(u), r.f(0.8, 1.05))
		}
	}
}

// addRoots adds n surface roots round the trunk's foot, running out over the
// ground and diving into it.
func (p *rfPlant) addRoots(r frand, t rfTrunk, n int, reach, rad float32, relief rfRelief) {
	a0 := r.f(0, 2*math.Pi)
	for i := 0; i < n; i++ {
		a := a0 + float32(i)*2*math.Pi/float32(n) + r.f(-0.25, 0.25)
		out := yawDir(a)
		o := t.axis(0.6).Add(out.Mul(t.radius(0.6) * 0.45))
		l := reach * r.f(0.7, 1.3)
		dive := r.f(0.25, 0.45)
		pts := pathOf(o, out.Add(yUp.Mul(-0.35)), l, max(int(l/0.35), 4), func(u float32, d mgl32.Vec3) mgl32.Vec3 {
			// Out along the ground, then down into it.
			return yUp.Mul(0.35*(1-u) - dive*u*2)
		})
		rl := limbOf(pts, rad*r.f(0.7, 1.2), rad*0.15, 1.4, 1, 10, 0, 0)
		rl.relief = relief
		rl.tone0, rl.tone1 = 0.6, 0.5
		p.add(rl)
	}
}

// ---------- the conifers ----------

func buildSitkaSpruce(variant, lod int) []float32 {
	seed := uint32(30000 + variant*131)
	r := frand{rand.New(rand.NewSource(int64(seed)))}
	sp := rfConiferSpec{
		h: 40, r0: 1.0, lean: 0.8, crownBase: 0.32, crownR: 7.5,
		profile: func(t float32) float32 {
			// Broad low down, rounding over to a narrow top; the lowest live
			// limbs shorter, shaded out.
			return float32(math.Pow(float64(1-t), 0.85))*0.92*(0.75+0.25*smoothstep32(0, 0.15, t)) + 0.08
		},
		pitch: func(t float32) float32 { return -0.25 + 0.75*t },
		droop: 0.05, upturn: 0.16,
		spacing: 0.34, whorl: 1,
		branchlets: 2.8, twigReach: 0.32, pendant: 0.18,
		tile: tileSpruceSpray, part: partLeaf, spray: 1.45, sprayEvery: 0.4, sprayHang: 0.15,
		bark:  rfBark{kind: rfBarkPlates, depth: 0.1},
		lobes: 7, buttress: 0.55, buttressH: 3.5, flare: 0.9, sides: 96,
		stubs: 26, moss: 0.6, drapes: 0.35,
	}
	switch variant {
	case 1: // an old giant: bigger, more buttressed, crown broken and hung with moss
		sp.h, sp.r0, sp.crownR, sp.crownBase = 48, 1.45, 8.5, 0.4
		sp.buttress, sp.buttressH, sp.flare = 0.8, 5, 1.1
		sp.moss, sp.drapes, sp.stubs, sp.pendant = 1.0, 0.75, 40, 0.3
		sp.reachJitter = 0.4
	case 2: // colonnade: standing on stilt roots where its nurse log was
		sp.h, sp.r0, sp.base, sp.crownBase = 36, 0.85, 2.6, 0.38
		sp.buttress, sp.flare = 0.3, 0.5
		sp.moss, sp.drapes = 0.7, 0.45
	}
	p, t := rfConifer(r, sp, seed)
	bark := rfBark{kind: rfBarkPlates, depth: 0.08, girth: 0.3, seed: seed + 7}
	if variant == 2 {
		// Five or six roots arching from the raised foot down to the ground,
		// the space under it where the log lay.
		n := 5 + r.Intn(2)
		a0 := r.f(0, 2*math.Pi)
		for i := 0; i < n; i++ {
			a := a0 + float32(i)*2*math.Pi/float32(n) + r.f(-0.3, 0.3)
			out := yawDir(a)
			o := t.axis(sp.base + 0.4)
			// Along the line of the old log they reach further.
			span := r.f(2.2, 3.4)
			if i%2 == 0 {
				span *= 1.25
			}
			var pts []mgl32.Vec3
			for k := 0; k <= 12; k++ {
				u := float32(k) / 12
				q := o.Add(out.Mul(span * float32(math.Sin(float64(u)*math.Pi/2))))
				// Ending a little under the ground.
				q[1] = (sp.base + 0.4) * (1 - u*u*1.08)
				pts = append(pts, q)
			}
			rl := limbOf(pts, sp.r0*r.f(0.35, 0.5), 0.12, 0.7, 1, 12, 0, 0)
			rl.relief = rfSum(bark.relief(), rfMoss(0.15, seed+uint32(i)))
			rl.tone0, rl.tone1 = 0.6, 0.55
			rl.moss = 0.6
			p.add(rl)
		}
	} else {
		p.addRoots(r, t, 5+variant*2, 2.6+float32(variant), 0.32+0.1*float32(variant), bark.relief())
	}
	m := &fmesh{}
	p.write(m, lod)
	return m.v
}

func buildHemlock(variant, lod int) []float32 {
	seed := uint32(31000 + variant*131)
	r := frand{rand.New(rand.NewSource(int64(seed)))}
	sp := rfConiferSpec{
		h: 32, r0: 0.6, lean: 0.6, crownBase: 0.3, crownR: 5.2,
		profile: func(t float32) float32 { return float32(math.Pow(float64(1-t), 0.9))*0.9 + 0.1 },
		pitch:   func(t float32) float32 { return -0.35 + 0.55*t },
		droop:   0.12, upturn: 0.04,
		spacing: 0.3, whorl: 1,
		branchlets: 3.0, twigReach: 0.3, pendant: 0.45,
		tile: tileHemlockSpray, part: partLeaf, spray: 1.35, sprayEvery: 0.36, sprayHang: 0.45,
		bark:  rfBark{kind: rfBarkScaly, ridges: 17, depth: 0.1, twist: 0.02},
		lobes: 5, buttress: 0.25, buttressH: 1.5, flare: 0.5, sides: 72,
		nod: 1.6, stubs: 14, moss: 0.45, drapes: 0.3,
	}
	switch variant {
	case 1: // tall and slender, the crown high
		sp.h, sp.r0, sp.crownBase, sp.crownR = 36, 0.55, 0.45, 4.4
	case 2: // perched on an old cedar stump, roots straddling it
		sp.h, sp.r0, sp.base, sp.crownBase = 26, 0.45, 2.3, 0.3
		sp.buttress, sp.flare = 0.15, 0.3
	}
	p, t := rfConifer(r, sp, seed)
	if variant == 2 {
		const stumpR, stumpH = 1.25, 2.2
		// The stump: weathered, silver-grey, broken unevenly across its top.
		sb := rfBark{kind: rfBarkFibrous, ridges: 26, depth: 0.05, twist: 0.04, girth: stumpR, seed: seed + 5}
		var pts []mgl32.Vec3
		var radii []float32
		for y := float32(-0.6); y <= stumpH; y += 0.2 {
			pts = append(pts, mgl32.Vec3{0, y, 0})
			radii = append(radii, stumpR*(1+0.3*float32(math.Exp(-float64(y+0.6)*1.5))))
		}
		st := rfLimb{pts: pts, radii: radii, depth: 0, sides: 36, part: partBark, relief: rfSum(sb.relief(), rfButtress(6, 0.25, 1.2, seed)),
			tone0: 0.75, tone1: 0.95, cap: true, order: 0.5, moss: 0.7}
		p.add(st)
		// The roots: out over the stump's rim, down its side, into the
		// ground.
		n := 6 + r.Intn(3)
		a0 := r.f(0, 2*math.Pi)
		for i := 0; i < n; i++ {
			a := a0 + float32(i)*2*math.Pi/float32(n) + r.f(-0.2, 0.2)
			out := yawDir(a)
			o := t.axis(sp.base + 0.2)
			var rp []mgl32.Vec3
			wob := r.f(-0.25, 0.25)
			for k := 0; k <= 14; k++ {
				u := float32(k) / 14
				var q mgl32.Vec3
				if u < 0.3 {
					f := u / 0.3
					q = o.Add(out.Mul(stumpR * f)).Add(yUp.Mul(0.15 * (1 - f)))
					q[1] = sp.base + 0.2 - 0.1*f
				} else {
					f := (u - 0.3) / 0.7
					// Down the stump's side, hugging it, splaying at the foot.
					q = rotAxis(out, yUp, wob*f).Mul(stumpR + 0.12 + 0.9*f*f*f)
					q[1] = (sp.base + 0.1) - f*(sp.base+0.6)
				}
				rp = append(rp, q)
			}
			rl := limbOf(rp, r.f(0.11, 0.17), 0.05, 0.6, 1, 9, 0, 0)
			rl.relief = rfBark{kind: rfBarkScaly, ridges: 5, depth: 0.08, girth: 0.12, seed: seed + uint32(i)}.relief()
			rl.tone0, rl.tone1 = 0.6, 0.5
			rl.moss = 0.4
			p.add(rl)
		}
	} else {
		p.addRoots(r, t, 5, 1.8, 0.2, sp.bark.relief())
	}
	m := &fmesh{}
	p.write(m, lod)
	return m.v
}

func buildRedcedar(variant, lod int) []float32 {
	seed := uint32(32000 + variant*131)
	r := frand{rand.New(rand.NewSource(int64(seed)))}
	sp := rfConiferSpec{
		h: 30, r0: 1.05, lean: 0.5, crownBase: 0.22, crownR: 6.2,
		profile: func(t float32) float32 { return float32(math.Pow(float64(1-t), 0.8))*0.9 + 0.1 },
		// J-shaped limbs: out and down, then sweeping up at the tip.
		pitch: func(t float32) float32 { return -0.45 + 0.6*t },
		droop: 0.1, upturn: 0.42,
		spacing: 0.4, whorl: 1,
		branchlets: 2.8, twigReach: 0.34, pendant: 0.8,
		tile: tileCedarSpray, part: partLeaf, spray: 1.5, sprayEvery: 0.38, sprayHang: 0.75,
		bark:  rfBark{kind: rfBarkFibrous, ridges: 46, depth: 0.1, twist: 0.06},
		lobes: 9, buttress: 0.6, buttressH: 4.5, flare: 1.0, sides: 104,
		stubs: 10, moss: 0.5, drapes: 0.25,
	}
	switch variant {
	case 1: // old candelabra: dead spike top, leaders sprung from the limbs
		sp.h, sp.r0, sp.deadTop, sp.crownBase = 34, 1.5, 0.1, 0.32
		sp.buttress, sp.buttressH = 0.8, 6
		sp.moss, sp.drapes = 0.8, 0.45
	case 2: // younger: denser, conical, limbs to the ground
		sp.h, sp.r0, sp.crownBase, sp.crownR = 22, 0.6, 0.08, 4.6
		sp.buttress = 0.4
		sp.moss = 0.25
	}
	p, t := rfConifer(r, sp, seed)
	if variant == 1 {
		// The dead top: silvered (pale tone) and broken off.
		for i := range p.limbs {
			if p.limbs[i].depth == 0 && p.limbs[i].pts[0].Y() > sp.h*(1-sp.deadTop)-1 {
				p.limbs[i].tone0, p.limbs[i].tone1 = 1.25, 1.4
			}
		}
		// Two or three new leaders sprung from limbs, rising alongside.
		for k := 0; k < 2+r.Intn(2); k++ {
			y := sp.h * r.f(0.55, 0.68)
			a := r.f(0, 2*math.Pi)
			o := t.axis(y)
			out := yawDir(a)
			l := r.f(5, 8)
			pts := pathOf(o, out.Add(yUp.Mul(0.4)), l, 10, func(u float32, d mgl32.Vec3) mgl32.Vec3 { return yUp.Mul(0.5 * (1 - u)) })
			ld := limbOf(pts, t.radius(y)*0.4, 0.05, 0.9, 1, 14, t.sway(y), t.sway(y)+0.5)
			ld.relief = rfBark{kind: rfBarkFibrous, ridges: 14, depth: 0.06, twist: 0.06, girth: 0.3, seed: seed + uint32(k)}.relief()
			li := p.add(ld)
			lp := p.limbs[li]
			for j := 0; j < 10; j++ {
				u := 0.3 + 0.7*float32(j)/10
				bp, _, _ := lp.at(u)
				lt := rfTrunk{h: bp.Y() + 2, base: bp.Y() - 1, r0: 0.04, off: bp, seed: seed + uint32(j)}
				p.coniferLimb(r, lt, sp, bp.Y(), r.f(0, 2*math.Pi), r.f(1.2, 2.6)*(1.2-u), 0.5+0.5*u)
			}
		}
	}
	p.addRoots(r, t, 6, 2.2, 0.3, sp.bark.relief())
	m := &fmesh{}
	p.write(m, lod)
	return m.v
}

func buildDouglasFir(variant, lod int) []float32 {
	seed := uint32(33000 + variant*131)
	r := frand{rand.New(rand.NewSource(int64(seed)))}
	sp := rfConiferSpec{
		h: 44, r0: 1.0, lean: 0.6, crownBase: 0.55, crownR: 6.0,
		profile: func(t float32) float32 { return float32(math.Pow(float64(1-t), 0.75))*0.85 + 0.15 },
		pitch:   func(t float32) float32 { return -0.1 + 0.55*t },
		droop:   0.07, upturn: 0.2,
		spacing: 0.45, whorl: 1,
		branchlets: 2.6, twigReach: 0.34, pendant: 0.1,
		tile: tileFirSpray, part: partLeaf, spray: 1.45, sprayEvery: 0.42, sprayHang: 0.1,
		bark:  rfBark{kind: rfBarkFurrowed, ridges: 21, depth: 0.22, twist: 0.015},
		lobes: 5, buttress: 0.25, buttressH: 2, flare: 0.6, sides: 100,
		stubs: 16, moss: 0.45, drapes: 0.2, reachJitter: 0.35, pitchJitt: 0.15,
	}
	switch variant {
	case 1: // old growth: taller bole, broken flat top, ragged crown
		sp.h, sp.r0, sp.crownBase, sp.crownR = 46, 1.4, 0.62, 7.5
		sp.profile = func(t float32) float32 { return 0.85 - 0.35*t*t }
		sp.reachJitter, sp.moss, sp.drapes = 0.5, 0.7, 0.35
		sp.stubs = 24
	case 2: // younger and fuller
		sp.h, sp.r0, sp.crownBase, sp.crownR = 34, 0.6, 0.3, 4.8
		sp.bark.depth = 0.12
	}
	p, _ := rfConifer(r, sp, seed)
	m := &fmesh{}
	p.write(m, lod)
	return m.v
}

// ---------- the broadleaves ----------

// rfBroadSpec is a decurrent tree.
type rfBroadSpec struct {
	h0, r0, lean float32 // trunk height before it parts, radius, lean
	mains        int
	pitch        [2]float32 // the main limbs' angle above horizontal
	mainLen      [2]float32
	splits       int     // how many times the limbs fork after the mains
	kids         int     // children a branch puts out
	fork         float32 // the angle a child turns off its parent
	ratio        float32 // a child's length as a fraction of its parent's
	rise, sag    float32 // per-unit turns up (toward light) and down (weight)
	tile, part   int
	spray        float32 // leaf spray size
	sprays       int     // sprays round each tip
	bark         rfBark
	flare        float32
	lobes        int
	buttress     float32
	sides        int
	moss, drapes float32 // moss on trunk and limbs; chance per limb of hanging moss
	ferns        float32 // licorice ferns per unit of mossy limb
}

// rfBroadleaf builds a decurrent tree from sp.
func rfBroadleaf(r frand, sp rfBroadSpec, seed uint32) (*rfPlant, rfTrunk) {
	p := &rfPlant{farDepth: 2, farEvery: 8, farArea: 0.4, mossSeed: seed}
	t := rfTrunk{h: sp.h0 * 2.2, base: 0, r0: sp.r0, flare: sp.flare, lean: sp.lean,
		leanDir: yawDir(r.f(0, 2*math.Pi)), seed: seed}
	sp.bark.girth, sp.bark.seed = sp.r0, seed+11
	relief := rfSum(sp.bark.relief(), rfButtress(sp.lobes, sp.buttress, 2, seed+3))
	p.addTrunk(t, -0.7, sp.h0, sp.sides, 2, relief, 0.6, 0.75)
	for i := range p.limbs {
		p.limbs[i].moss = sp.moss
		p.limbs[i].cap = false
	}
	top := t.axis(sp.h0)
	crown := top.Add(yUp.Mul(sp.mainLen[1] * 0.7))
	a0 := r.f(0, 2*math.Pi)
	var grow func(parent int, o, d mgl32.Vec3, l, rad, sw float32, depth int)
	grow = func(parent int, o, d mgl32.Vec3, l, rad, sw float32, depth int) {
		segs := max(int(l/0.6), 3)
		if depth > 2 {
			segs = 3
		}
		rise, sag := sp.rise, sp.sag
		pts := pathOf(o, d, l, segs, func(u float32, dd mgl32.Vec3) mgl32.Vec3 {
			return yUp.Mul(rise*(1-u) - sag*u)
		})
		sides := 12
		switch {
		case depth >= 4:
			sides = 4
		case depth == 3:
			sides = 5
		case depth == 2:
			sides = 8
		}
		lb := limbOf(pts, rad, rad*0.45, 1, depth, sides, sw, sw+0.18+0.1*float32(depth))
		if depth <= 2 {
			b := sp.bark
			b.girth, b.seed = rad, seed+uint32(len(p.limbs))
			b.depth *= 0.6
			lb.relief = b.relief()
		}
		lb.tone0, lb.tone1 = 0.6, 0.72
		if depth <= 3 && sp.moss > 0 {
			lb.moss = sp.moss * (1 - 0.2*float32(depth)) * r.f(0.6, 1.2)
		}
		li := p.add(lb)
		limb := p.limbs[li]
		// Moss hanging from it, ferns on its back.
		if depth >= 1 && depth <= 3 {
			if r.Float32() < sp.drapes {
				for k := 0; k < 1+r.Intn(3); k++ {
					u := r.f(0.1, 0.9)
					dp, _, dr := limb.at(u)
					p.drape(dp.Sub(yUp.Mul(dr*0.7)), r.f(0.35, 1.5), r.f(0.35, 0.65), r.f(0, math.Pi), limb.swayAt(u), r.f(0.8, 1.05))
				}
			}
			nf := int(sp.ferns * l * lb.moss)
			for k := 0; k < nf; k++ {
				u := r.f(0.05, 0.9)
				fp, fd, fr := limb.at(u)
				if fd.Y() > 0.75 {
					continue
				}
				fdir := yUp.Add(yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0.2, 0.6))).Normalize()
				s := r.f(0.28, 0.5)
				p.cards = append(p.cards, rfCard{base: fp.Add(yUp.Mul(fr * (1 + lb.moss*0.3))), right: perp(fdir, r.f(0, math.Pi)).Mul(s * 0.3),
					up: fdir.Mul(s), n: yUp, tile: tileFern, part: partLeaf, sway0: limb.swayAt(u), sway1: limb.swayAt(u) + 0.2, tone: r.f(0.7, 0.9)})
			}
		}
		if depth >= sp.splits {
			// The last two orders of branches carry leaves along their
			// length, facing out of the crown, so it fills as a mass.
			for u := float32(0.15); u < 0.9; u += sp.spray * 0.5 / max(l, 0.1) {
				bp, bd, _ := limb.at(u)
				q := bp.Add(mgl32.Vec3{r.f(-1, 1), r.f(-0.4, 0.8), r.f(-1, 1)}.Mul(sp.spray * 0.25))
				out := q.Sub(crown)
				out[1] *= 0.5
				dd := out.Normalize().Add(bd.Mul(0.5)).Add(yUp.Mul(r.f(0, 0.5))).Add(mgl32.Vec3{r.f(-0.5, 0.5), r.f(-0.3, 0.3), r.f(-0.5, 0.5)})
				if dd.Len() < 1e-3 {
					dd = yUp
				}
				dd = dd.Normalize()
				p.spray(q, dd, perp(dd, r.f(0, math.Pi)), sp.spray*r.f(0.75, 1.1), sp.tile, sp.part, crown, limb.swayAt(u), 0.55+0.45*min(q.Sub(crown).Len()/(sp.mainLen[1]*1.2), 1))
			}
		}
		if depth >= sp.splits+1 {
			// A tip: a ball of leaf sprays facing out of the crown.
			tp, td, _ := limb.at(1)
			for k := 0; k < sp.sprays; k++ {
				u := r.f(0.5, 1)
				bp, _, _ := limb.at(u)
				q := bp.Add(mgl32.Vec3{r.f(-1, 1), r.f(-0.5, 1), r.f(-1, 1)}.Mul(sp.spray * 0.35))
				out := q.Sub(crown)
				out[1] *= 0.5
				dd := out.Normalize().Add(td.Mul(0.6)).Add(yUp.Mul(r.f(0.1, 0.6))).Add(mgl32.Vec3{r.f(-0.4, 0.4), r.f(-0.3, 0.3), r.f(-0.4, 0.4)})
				if dd.Len() < 1e-3 {
					dd = yUp
				}
				dd = dd.Normalize()
				p.spray(q, dd, perp(dd, r.f(0, math.Pi)), sp.spray*r.f(0.8, 1.2), sp.tile, sp.part, crown, limb.sway1, 0.6+0.45*min(q.Sub(crown).Len()/(sp.mainLen[1]*1.2), 1))
			}
			_ = tp
			return
		}
		kids := sp.kids
		for k := 0; k < kids; k++ {
			u := 0.45 + 0.55*float32(k+1)/float32(kids)
			if k == kids-1 {
				u = 1
			}
			kp, kd, kr := limb.at(u)
			turn := sp.fork * r.f(0.7, 1.3)
			if k == kids-1 {
				turn *= 0.4 // the lead carries on
			}
			nd := rotAxis(kd, perp(kd, r.f(0, 2*math.Pi)), turn)
			// Spreading out of the crown, a little up.
			out := kp.Sub(crown)
			out[1] = 0
			if out.Len() > 1e-3 {
				nd = nd.Add(out.Normalize().Mul(0.25))
			}
			nd = nd.Add(yUp.Mul(0.1)).Normalize()
			grow(li, kp.Sub(kd.Mul(kr*0.5)), nd, l*sp.ratio*r.f(0.8, 1.15), max(kr*0.7, 0.008), limb.swayAt(u), depth+1)
		}
	}
	for i := 0; i < sp.mains; i++ {
		a := a0 + float32(i)*2*math.Pi/float32(sp.mains) + r.f(-0.4, 0.4)
		pitch := r.f(sp.pitch[0], sp.pitch[1])
		d := yawDir(a).Mul(float32(math.Cos(float64(pitch)))).Add(yUp.Mul(float32(math.Sin(float64(pitch)))))
		o := t.axis(sp.h0 * r.f(0.82, 1))
		grow(0, o, d, r.f(sp.mainLen[0], sp.mainLen[1]), t.radius(sp.h0)*r.f(0.55, 0.75), t.sway(sp.h0), 1)
	}
	return p, t
}

func buildBigleafMaple(variant, lod int) []float32 {
	seed := uint32(34000 + variant*131)
	r := frand{rand.New(rand.NewSource(int64(seed)))}
	sp := rfBroadSpec{
		h0: 3.6, r0: 0.75, lean: 0.4, mains: 5, pitch: [2]float32{0.55, 0.95}, mainLen: [2]float32{9, 11.5},
		splits: 3, kids: 3, fork: 0.6, ratio: 0.64, rise: 0.06, sag: 0.05,
		tile: tileMapleLeaves, part: partLeaf, spray: 1.9, sprays: 9,
		bark:  rfBark{kind: rfBarkFurrowed, ridges: 18, depth: 0.12, twist: 0.02},
		flare: 0.5, lobes: 5, buttress: 0.25, sides: 72,
		moss: 1.0, drapes: 0.65, ferns: 1.3,
	}
	switch variant {
	case 1: // leaning, multi-stemmed, sprawling
		sp.h0, sp.lean, sp.mains, sp.pitch = 2.4, 1.2, 5, [2]float32{0.5, 1.0}
		sp.mainLen = [2]float32{7.5, 10}
		sp.sag = 0.09
	case 2: // younger, less moss
		sp.h0, sp.r0, sp.mainLen = 4.5, 0.5, [2]float32{7, 9}
		sp.moss, sp.drapes, sp.ferns = 0.45, 0.25, 0.5
	}
	p, t := rfBroadleaf(r, sp, seed)
	p.addRoots(r, t, 5, 1.6, 0.22, sp.bark.relief())
	m := &fmesh{}
	p.write(m, lod)
	return m.v
}

func buildRedAlder(variant, lod int) []float32 {
	seed := uint32(35000 + variant*131)
	r := frand{rand.New(rand.NewSource(int64(seed)))}
	sp := rfConiferSpec{
		h: 18, r0: 0.28, lean: 0.4, crownBase: 0.45, crownR: 3.6,
		profile: func(t float32) float32 {
			return float32(math.Pow(math.Sin(math.Pi*float64(0.12+0.88*t)), 0.7))*0.9 + 0.1
		},
		pitch: func(t float32) float32 { return 0.55 + 0.35*t },
		droop: 0.06, upturn: 0.02,
		spacing: 0.45, whorl: 1,
		branchlets: 2.4, twigReach: 0.42, pendant: 0.05,
		tile: tileAlderLeaves, part: partLeaf, spray: 1.25, sprayEvery: 0.36, sprayHang: 0,
		bark:  rfBark{kind: rfBarkSmooth, depth: 0.015},
		lobes: 4, buttress: 0.12, buttressH: 0.8, flare: 0.45, sides: 40,
		stubs: 8, limbTone0: 0.75, limbTone1: 0.85, reachJitter: 0.3, pitchJitt: 0.15,
	}
	if variant == 1 {
		sp.lean = 2.2
	}
	p, t := rfConifer(r, sp, seed)
	p.mossTop = 3.5
	// Pale bark: the trunk a shade lighter than its limbs, moss at the foot.
	for i := range p.limbs {
		if p.limbs[i].depth == 0 {
			p.limbs[i].tone0, p.limbs[i].tone1 = 0.9, 1.0
			p.limbs[i].moss = 0.5
		}
	}
	if variant == 2 {
		// A twin stem from the same foot.
		sp2 := sp
		sp2.h, sp2.r0, sp2.lean = 15, 0.22, 1.8
		q, _ := rfConifer(frand{rand.New(rand.NewSource(int64(seed + 1)))}, sp2, seed+1)
		off := yawDir(r.f(0, 2*math.Pi)).Mul(0.3)
		for _, l := range q.limbs {
			for i := range l.pts {
				l.pts[i] = l.pts[i].Add(off)
			}
			if l.depth == 0 {
				l.order += 10
				l.tone0, l.tone1 = 0.9, 1.0
			}
			p.limbs = append(p.limbs, l)
		}
		for _, c := range q.cards {
			c.base = c.base.Add(off)
			p.cards = append(p.cards, c)
		}
	}
	p.addRoots(r, t, 4, 1.0, 0.1, sp.bark.relief())
	m := &fmesh{}
	p.write(m, lod)
	return m.v
}

func buildVineMaple(variant, lod int) []float32 {
	seed := uint32(36000 + variant*131)
	r := frand{rand.New(rand.NewSource(int64(seed)))}
	p := &rfPlant{farDepth: 1, farEvery: 3, mossSeed: seed}
	stems := 5 + variant*2
	a0 := r.f(0, 2*math.Pi)
	crown := mgl32.Vec3{0, 3, 0}
	for i := 0; i < stems; i++ {
		a := a0 + float32(i)*2*math.Pi/float32(stems) + r.f(-0.4, 0.4)
		out := yawDir(a)
		// Each stem rises steeply, then arches out and over under its own
		// weight; some come back down nearly to the ground.
		l := r.f(5, 8)
		lean := r.f(0.25, 0.6)
		sag := r.f(0.12, 0.3)
		pts := pathOf(out.Mul(r.f(0, 0.35)).Sub(yUp.Mul(0.4)), yUp.Add(out.Mul(lean)), l, max(int(l/0.4), 8),
			func(u float32, d mgl32.Vec3) mgl32.Vec3 { return out.Mul(0.08).Sub(yUp.Mul(sag * u)) })
		st := limbOf(pts, r.f(0.05, 0.08), 0.015, 0.8, 0, 12, 0, 0.75)
		st.relief = rfBark{kind: rfBarkSmooth, depth: 0.02, girth: 0.07, seed: seed + uint32(i)}.relief()
		st.tone0, st.tone1 = 0.7, 0.85
		st.moss = r.f(0.5, 1.0)
		st.order = float32(i + 1)
		si := p.add(st)
		stem := p.limbs[si]
		// Side shoots held level, leaves laid flat on them in layers.
		for k := 0; k < 14; k++ {
			u := 0.2 + 0.78*float32(k)/14
			bp, bd, br := stem.at(u)
			side := bd.Cross(yUp)
			if side.Len() < 1e-3 {
				side = perp(bd, 0)
			}
			side = side.Normalize()
			if k%2 == 1 {
				side = side.Mul(-1)
			}
			bdir := side.Add(bd.Mul(0.4)).Add(yUp.Mul(-0.05))
			bdir[1] *= 0.3
			bdir = bdir.Normalize()
			bl := r.f(0.8, 1.6) * (1.1 - 0.4*u)
			tw := limbOf(pathOf(bp, bdir, bl, 3, nil), max(br*0.5, 0.01), 0.004, 1, 1, 4, stem.swayAt(u), stem.swayAt(u)+0.3)
			tw.tone0, tw.tone1 = 0.7, 0.8
			ti := p.add(tw)
			twig := p.limbs[ti]
			for s := 0; s < 7; s++ {
				v := 0.1 + 0.9*float32(s)/7
				sp0, sd, _ := twig.at(v)
				// Flat: the spray lies in a near-level plane.
				hd := sd.Add(mgl32.Vec3{r.f(-0.4, 0.4), r.f(-0.05, 0.08), r.f(-0.4, 0.4)})
				hd[1] *= 0.25
				hd = hd.Normalize()
				// Layered, but each spray tipped a little, so a clump
				// seen side on is not a stack of edges.
				side2 := rotAxis(hd.Cross(yUp).Normalize(), hd, r.f(-0.6, 0.6))
				p.cards = append(p.cards, rfCard{base: sp0.Sub(hd.Mul(0.06)), right: side2.Mul(0.32), up: hd.Mul(0.65),
					n: yUp.Add(sp0.Sub(crown).Mul(0.15)).Normalize(), tile: tileVineMapleLeaves, part: partLeaf,
					sway0: twig.swayAt(v), sway1: twig.swayAt(v) + 0.2, tone: 0.65 + 0.4*min(sp0.Sub(crown).Len()/4, 1)})
			}
		}
	}
	m := &fmesh{}
	p.write(m, lod)
	return m.v
}

func buildCottonwood(variant, lod int) []float32 {
	seed := uint32(37000 + variant*131)
	r := frand{rand.New(rand.NewSource(int64(seed)))}
	sp := rfBroadSpec{
		h0: 10, r0: 0.75, lean: 0.6, mains: 6, pitch: [2]float32{0.6, 1.05}, mainLen: [2]float32{9, 12},
		splits: 3, kids: 3, fork: 0.7, ratio: 0.64, rise: 0.07, sag: 0.03,
		tile: tileCottonwoodLeaves, part: partLeaf, spray: 1.8, sprays: 9,
		bark:  rfBark{kind: rfBarkFurrowed, ridges: 19, depth: 0.18, twist: 0.02},
		flare: 0.45, lobes: 5, buttress: 0.2, sides: 80,
		moss: 0.3, drapes: 0.12, ferns: 0.2,
	}
	switch variant {
	case 1: // tall and narrow
		sp.h0, sp.pitch, sp.mainLen = 13, [2]float32{0.9, 1.2}, [2]float32{9, 11}
		sp.fork = 0.55
	case 2: // old, with dead limbs
		sp.r0, sp.mainLen = 1.0, [2]float32{8, 12}
		sp.moss = 0.5
	}
	p, t := rfBroadleaf(r, sp, seed)
	if variant == 2 {
		// A few bare, dead limbs among the live ones.
		for k := 0; k < 3; k++ {
			y := r.f(7, 10)
			d := yawDir(r.f(0, 2*math.Pi)).Add(yUp.Mul(r.f(0.2, 0.7))).Normalize()
			lb := limbOf(pathOf(t.axis(y), d, r.f(3, 5), 6, func(u float32, _ mgl32.Vec3) mgl32.Vec3 { return yUp.Mul(-0.04) }), 0.16, 0.03, 0.8, 1, 8, t.sway(y), t.sway(y)+0.2)
			lb.tone0, lb.tone1 = 0.85, 1.0
			lb.cap = true
			p.add(lb)
		}
	}
	p.addRoots(r, t, 5, 1.8, 0.24, sp.bark.relief())
	m := &fmesh{}
	p.write(m, lod)
	return m.v
}
