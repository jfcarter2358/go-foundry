package render

// foliage_rainforest_floor.go — what grows on and lies about the floor of a
// temperate rainforest: sword, lady and deer ferns, redwood sorrel,
// huckleberry, salal and devil's club, nurse logs with their rows of young
// hemlocks, mossy snags and root wads, fallen branches, curled leaf litter
// and hummocks of deep moss.
//
// Built with the trees' kit (foliage_rainforest_kit.go) and in the same
// detail: fronds are folded strips bent along their arch, fallen leaves are
// curled sheets rather than flat cards, a log's bark and moss are its
// relief. Lod 1 is the same plant with fewer, coarser pieces.

import (
	"math"
	"math/rand"

	"github.com/go-gl/mathgl/mgl32"
)

// fernFrond writes one arching frond from base toward out, l long, rising
// by rise then arching over, half-width w across the tile, folded by fold.
func (m *fmesh) fernFrond(base, out mgl32.Vec3, l, rise, arch, w float32, tile, part, segs int, fold, sway0 float32) {
	side := out.Cross(yUp)
	if side.Len() < 1e-3 {
		side = mgl32.Vec3{1, 0, 0}
	}
	side = side.Normalize()
	pt := func(t float32) mgl32.Vec3 {
		return base.Add(out.Mul(l * t * (1 - 0.12*t))).Add(yUp.Mul(l * (rise*t - arch*t*t)))
	}
	m.frondPart(pt, func(float32) float32 { return w }, side, segs, tile, part, fold, sway0, 1)
}

func buildSwordFern(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(40000 + variant*131)))}
	m := &fmesh{}
	fronds := []int{44, 28, 34}[variant]
	size := []float32{1.25, 0.85, 1.05}[variant]
	segs := 9
	if lod > 0 {
		fronds, segs = fronds/3, 3
	}
	// The crown: a dark knot of old frond bases.
	if lod == 0 {
		m.blob(1, func(d mgl32.Vec3) mgl32.Vec3 {
			n := 1 + noise3(d.Mul(3), 41)*0.4
			return mgl32.Vec3{d[0] * 0.12 * n, max(d[1], -0.3)*0.08 + 0.02, d[2] * 0.12 * n}
		}, false, partBark, func(p mgl32.Vec3) float32 { return 0.6 + 2*max(p[1], 0) }, nil)
	}
	for i := 0; i < fronds; i++ {
		a := float32(i)*2.39996 + r.f(-0.2, 0.2)
		out := yawDir(a)
		// The inner fronds stand up, the outer arch over and down.
		inner := float32(i%4) / 3
		l := size * r.f(0.7, 1.1) * (1 - 0.25*inner)
		rise := 0.55 + 0.9*inner + r.f(-0.15, 0.15)
		m.fernFrond(out.Mul(0.05), out, l, rise, 0.75+0.25*(1-inner), l*0.11, tileSwordFern, partLeaf, segs, 0.28, 0.05)
	}
	if variant == 2 {
		// A skirt of last year's fronds lying dead round it.
		for i := 0; i < 14-9*lod; i++ {
			a := r.f(0, 2*math.Pi)
			out := yawDir(a)
			l := size * r.f(0.8, 1.1)
			m.fernFrond(out.Mul(0.08), out, l, 0.12, 0.2, l*0.1, tileSwordFern, partAccentCard, max(segs/2, 2), 0.1, 0)
		}
	}
	if variant == 0 && lod == 0 {
		// Fiddleheads uncurling in the middle.
		for i := 0; i < 4; i++ {
			a := r.f(0, 2*math.Pi)
			out := yawDir(a)
			h := r.f(0.2, 0.4)
			var pts []mgl32.Vec3
			for k := 0; k <= 16; k++ {
				t := float32(k) / 16
				if t < 0.6 {
					pts = append(pts, out.Mul(0.04+t*0.05).Add(yUp.Mul(h*t/0.6)))
					continue
				}
				// The crozier: a tightening coil over the top.
				f := (t - 0.6) / 0.4
				ang := f * 5.5
				rad := 0.07 * (1 - 0.8*f)
				c := out.Mul(0.07).Add(yUp.Mul(h - 0.07))
				pts = append(pts, c.Add(out.Mul(rad*float32(math.Sin(float64(ang))))).Add(yUp.Mul(rad*float32(math.Cos(float64(ang))))))
			}
			radii := make([]float32, len(pts))
			sway := make([]float32, len(pts))
			tone := make([]float32, len(pts))
			for k := range pts {
				t := float32(k) / float32(len(pts)-1)
				radii[k] = 0.012 * (1 - 0.6*t)
				sway[k] = t * 0.4
				tone[k] = 0.8 + 0.3*t
			}
			m.tube(pts, radii, sway, tone, 5, partSolid)
		}
	}
	return m.v
}

func buildForestFern(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(41000 + variant*131)))}
	m := &fmesh{}
	segs := 8
	if lod > 0 {
		segs = 3
	}
	if variant == 0 {
		// Lady fern: a vase of lacy fronds, upright then arching.
		n := 18
		if lod > 0 {
			n = 7
		}
		for i := 0; i < n; i++ {
			a := float32(i)*2.39996 + r.f(-0.2, 0.2)
			out := yawDir(a)
			l := r.f(0.75, 1.1)
			m.fernFrond(out.Mul(0.06), out, l, r.f(1.0, 1.5), r.f(0.7, 0.95), l*0.2, tileLadyFern, partLeaf, segs, 0.2, 0.05)
		}
		return m.v
	}
	// Deer fern: a flat rosette of sterile fronds, and a few narrow fertile
	// ones standing straight up from its middle.
	n, f := 16, 4
	if lod > 0 {
		n, f = 6, 2
	}
	for i := 0; i < n; i++ {
		a := float32(i)*2.39996 + r.f(-0.2, 0.2)
		out := yawDir(a)
		l := r.f(0.35, 0.6)
		m.fernFrond(out.Mul(0.03), out, l, r.f(0.35, 0.6), r.f(0.4, 0.6), l*0.13, tileDeerFern, partLeaf, segs, 0.15, 0.05)
	}
	for i := 0; i < f; i++ {
		out := yawDir(r.f(0, 2*math.Pi))
		l := r.f(0.65, 0.9)
		m.fernFrond(out.Mul(0.02), out, l, r.f(1.25, 1.5), r.f(0.3, 0.45), l*0.045, tileDeerFern, partLeaf, segs, 0, 0.05)
	}
	return m.v
}

func buildOxalis(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(42000 + variant*131)))}
	m := &fmesh{}
	leaves := []int{64, 40}[variant]
	flowers := []int{2, 6}[variant]
	if lod > 0 {
		leaves, flowers = leaves/3, flowers/2
	}
	for i := 0; i < leaves; i++ {
		c := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0, 0.4) * float32(math.Sqrt(float64(r.f(0, 1)))))
		h := r.f(0.06, 0.16)
		lean := yawDir(r.f(0, 2*math.Pi))
		top := c.Add(yUp.Mul(h)).Add(lean.Mul(h * 0.15))
		if lod == 0 {
			m.blade(c, yUp, lean, h, 0.004, 0.15, 2, partSolid, 0.6, 0.9, 0.6)
		}
		s := r.f(0.07, 0.11)
		if lod > 0 {
			s *= 1.3
		}
		a := r.f(0, 2*math.Pi)
		right, fwd := yawDir(a), yawDir(a+math.Pi/2)
		droop := r.f(0.15, 0.35)
		tilt := mgl32.Vec3{r.f(-0.15, 0.15), 0, r.f(-0.15, 0.15)}
		nu := 3
		if lod > 0 {
			nu = 1
		}
		// The leaflets droop from the middle: a shallow tent.
		m.sheet(nu, nu, tileOxalis, partLeaf, func(u, v float32) mgl32.Vec3 {
			w := 2*v - 1
			d := u*u + w*w
			return top.Add(right.Mul(u * s * 0.5)).Add(fwd.Mul(w * s * 0.5)).Add(yUp.Mul(-d * droop * s * 0.5)).Add(tilt.Mul(w * s * 0.5))
		}, yUp, 0.5, 0.65, r.f(0.8, 1.1))
	}
	for i := 0; i < flowers; i++ {
		c := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0, 0.35))
		h := r.f(0.1, 0.17)
		lean := yawDir(r.f(0, 2*math.Pi))
		m.blade(c, yUp, lean, h, 0.003, 0.3, 2, partSolid, 0.7, 0.95, 0.7)
		top := c.Add(yUp.Mul(h)).Add(lean.Mul(h * 0.3))
		// Nodding, cup-shaped: a small cone of petals turned down and out.
		face := lean.Add(yUp.Mul(-0.3)).Normalize()
		right := face.Cross(yUp).Normalize()
		up := right.Cross(face)
		s := float32(0.018)
		m.card(top.Sub(up.Mul(s)), right.Mul(s), up.Mul(2*s), tilePetals, partAccentCard, face, 0.7, 0.7, 1.1)
	}
	return m.v
}

func buildUnderShrub(variant, lod int) []float32 {
	seed := uint32(43000 + variant*131)
	r := frand{rand.New(rand.NewSource(int64(seed)))}
	m := &fmesh{}
	switch variant {
	case 0: // red huckleberry: airy, bright green, angular
		p := &rfPlant{farDepth: 0, farEvery: 3, mossSeed: seed}
		for i := 0; i < 7; i++ {
			out := yawDir(r.f(0, 2*math.Pi))
			l := r.f(1.0, 1.8)
			pts := []mgl32.Vec3{out.Mul(r.f(0, 0.12)).Sub(yUp.Mul(0.1))}
			d := yUp.Add(out.Mul(r.f(0.15, 0.4))).Normalize()
			for k := 0; k < 6; k++ {
				// Zig-zag: each segment turns at its node.
				d = rotAxis(d, perp(d, float32(k)*math.Pi), 0.25*float32(1-2*(k%2))).Normalize()
				pts = append(pts, pts[len(pts)-1].Add(d.Mul(l/6)))
			}
			st := limbOf(pts, 0.016, 0.004, 1, 0, 5, 0, 0.7)
			st.part = partSolid
			st.tone0, st.tone1 = 0.7, 0.9
			si := p.add(st)
			stem := p.limbs[si]
			for k := 0; k < 14; k++ {
				u := 0.2 + 0.8*float32(k)/14
				bp, bd, _ := stem.at(u)
				side := bd.Cross(yUp)
				if side.Len() < 1e-3 {
					side = perp(bd, 0)
				}
				side = side.Normalize()
				if k%2 == 1 {
					side = side.Mul(-1)
				}
				dir := side.Add(yUp.Mul(r.f(0.2, 0.6))).Normalize()
				p.spray(bp, dir, dir.Cross(yUp), r.f(0.45, 0.65), tileHuckleberry, partLeaf, mgl32.Vec3{0, 0.9, 0}, stem.swayAt(u), r.f(0.85, 1.1))
			}
		}
		p.write(m, lod)
		if lod == 0 {
			for i := 0; i < 16; i++ {
				c := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0.1, 0.5)).Add(yUp.Mul(r.f(0.5, 1.5)))
				rr := r.f(0.006, 0.009)
				m.blob(0, func(d mgl32.Vec3) mgl32.Vec3 { return c.Add(d.Mul(rr)) }, false, partAccent,
					func(p mgl32.Vec3) float32 { return 0.8 + 30*max(p[1]-c[1], 0) }, nil)
			}
		}
	case 1: // salal: a dense, low thicket of leathery leaves
		p := &rfPlant{farDepth: 0, farEvery: 4, mossSeed: seed}
		for i := 0; i < 12; i++ {
			out := yawDir(r.f(0, 2*math.Pi))
			l := r.f(0.6, 1.0)
			pts := pathOf(out.Mul(r.f(0, 0.3)).Sub(yUp.Mul(0.05)), yUp.Add(out.Mul(r.f(0.5, 1.0))), l, 5,
				func(u float32, _ mgl32.Vec3) mgl32.Vec3 { return yUp.Mul(-0.6 * u) })
			st := limbOf(pts, 0.012, 0.004, 1, 0, 5, 0, 0.5)
			st.tone0, st.tone1 = 0.65, 0.8
			si := p.add(st)
			stem := p.limbs[si]
			for k := 0; k < 14; k++ {
				u := 0.2 + 0.8*float32(k)/14
				bp, bd, _ := stem.at(u)
				side := perp(bd, float32(k)*2.39996)
				dir := side.Mul(0.8).Add(bd.Mul(0.4)).Add(yUp.Mul(0.25)).Normalize()
				s := r.f(0.09, 0.13)
				nrm := dir.Cross(dir.Cross(yUp))
				if nrm.Y() < 0 {
					nrm = nrm.Mul(-1)
				}
				p.cards = append(p.cards, rfCard{base: bp, right: dir.Cross(yUp).Normalize().Mul(s * 0.5), up: dir.Mul(s),
					n: nrm.Normalize().Add(yUp).Normalize(), tile: tileOvalLeaf, part: partLeaf,
					sway0: stem.swayAt(u), sway1: stem.swayAt(u) + 0.1, tone: r.f(0.75, 1.05)})
			}
			// A raceme of dark berries hanging from the tip.
			if lod == 0 && r.Float32() < 0.5 {
				tp, _, _ := stem.at(1)
				p.extra = append(p.extra, func(m *fmesh, lod int) {
					for k := 0; k < 6; k++ {
						c := tp.Add(mgl32.Vec3{0, -0.025 * float32(k), 0.012 * float32(k%2)})
						m.blob(0, func(d mgl32.Vec3) mgl32.Vec3 { return c.Add(d.Mul(0.007)) }, false, partAccent,
							func(mgl32.Vec3) float32 { return 0.9 }, nil)
					}
				})
			}
		}
		p.write(m, lod)
	default: // devil's club: thick spiny canes under a parasol of huge leaves
		p := &rfPlant{farDepth: 0, farEvery: 1, mossSeed: seed}
		canes := 3 + r.Intn(3)
		var tops []mgl32.Vec3
		for i := 0; i < canes; i++ {
			out := yawDir(r.f(0, 2*math.Pi))
			l := r.f(1.3, 2.1)
			pts := pathOf(out.Mul(r.f(0, 0.25)).Sub(yUp.Mul(0.1)), yUp.Add(out.Mul(r.f(0.1, 0.5))), l, 8,
				func(u float32, _ mgl32.Vec3) mgl32.Vec3 {
					return out.Mul(0.15 * (1 - u)).Add(mgl32.Vec3{r.f(-0.3, 0.3), 0, r.f(-0.3, 0.3)})
				})
			st := limbOf(pts, 0.03, 0.02, 1, 0, 8, 0, 0.45)
			// Spines: ridges of tiny teeth in the relief, darker.
			st.relief = func(a, s float32, _ mgl32.Vec3) (float32, float32) {
				v := float32(math.Sin(float64(a*9))) * float32(math.Sin(float64(s*90)))
				if v > 0.85 {
					return 0.35, 0.7
				}
				return 0, 1
			}
			st.tone0, st.tone1 = 0.7, 0.85
			si := p.add(st)
			tp, _, _ := p.limbs[si].at(1)
			tops = append(tops, tp)
		}
		p.write(m, lod)
		for ci, tp := range tops {
			leaves := 4 + ci%3
			if lod > 0 {
				leaves = 3
			}
			for k := 0; k < leaves; k++ {
				a := float32(k)*2*math.Pi/float32(leaves) + r.f(-0.3, 0.3)
				out := yawDir(a)
				pl := r.f(0.15, 0.3)
				base := tp.Add(out.Mul(pl)).Sub(yUp.Mul(0.05))
				m.blade(tp, out.Add(yUp.Mul(0.4)).Normalize(), out, pl, 0.012, 0.2, 2, partSolid, 0.7, 0.9, 0.4)
				s := r.f(0.45, 0.75)
				right := out.Cross(yUp).Normalize()
				nu := 4
				if lod > 0 {
					nu = 1
				}
				droop := r.f(0.25, 0.45)
				// Held level, its edges and tip drooping.
				m.sheet(nu, nu, tileMapleLeaf, partLeaf, func(u, v float32) mgl32.Vec3 {
					return base.Add(right.Mul(u * s * 0.5)).Add(out.Mul(v * s)).Sub(yUp.Mul(droop * s * (0.5*u*u + 0.7*v*v)))
				}, yUp, 0.45, 0.8, r.f(0.85, 1.05))
			}
			// The berry spike: a cone of scarlet berries above the leaves.
			if lod == 0 && ci%2 == 0 {
				for k := 0; k < 26; k++ {
					t := float32(k) / 26
					c := tp.Add(yUp.Mul(0.05 + 0.22*t)).Add(yawDir(float32(k) * 2.39996).Mul(0.05 * (1 - t)))
					m.blob(0, func(d mgl32.Vec3) mgl32.Vec3 { return c.Add(d.Mul(0.011)) }, false, partAccent,
						func(mgl32.Vec3) float32 { return 1 }, nil)
				}
			}
		}
	}
	return m.v
}

func buildNurseLog(variant, lod int) []float32 {
	seed := uint32(44000 + variant*131)
	r := frand{rand.New(rand.NewSource(int64(seed)))}
	p := &rfPlant{farDepth: 0, farEvery: 3, mossSeed: seed}
	decay := rfBark{kind: rfBarkFibrous, ridges: 18, depth: 0.06, twist: 0.02, girth: 0.7, seed: seed}
	m := &fmesh{}
	switch variant {
	case 0:
		// The log, half sunk, bowed a little along its length.
		l := r.f(11, 13)
		rad := r.f(0.65, 0.8)
		var pts []mgl32.Vec3
		for x := -l / 2; x <= l/2+1e-3; x += 0.3 {
			t := x / (l / 2)
			pts = append(pts, mgl32.Vec3{x, rad*0.55 - 0.08*t*t, 0.25 * float32(math.Sin(float64(t*1.3)))})
		}
		lg := limbOf(pts, rad, rad*0.85, 1, 0, 30, 0, 0)
		lg.relief = decay.relief()
		lg.tone0, lg.tone1 = 0.65, 0.7
		lg.moss = 1.3
		lg.cap = true
		li := p.add(lg)
		log := p.limbs[li]
		// A row of young hemlocks along its back, roots straddling it.
		trees := 4 + r.Intn(4)
		for k := 0; k < trees; k++ {
			u := 0.1 + 0.8*(float32(k)+r.f(0.2, 0.8))/float32(trees)
			c, _, _ := log.at(u)
			topY := c.Y() + rad*1.1
			h := r.f(1.2, 3.4)
			at := mgl32.Vec3{c.X(), topY, c.Z()}
			tr := limbOf(pathOf(at, yUp.Add(mgl32.Vec3{r.f(-0.15, 0.15), 0, r.f(-0.15, 0.15)}), h, 8, nil), 0.03+0.015*h, 0.008, 1, 1, 7, 0, 0.6)
			tr.tone0, tr.tone1 = 0.6, 0.75
			p.add(tr)
			for s := 0; s < 4; s++ {
				// Roots: over the log's side and down to the ground.
				side := float32(1)
				if s%2 == 1 {
					side = -1
				}
				dx := r.f(-0.3, 0.3)
				var rp []mgl32.Vec3
				for j := 0; j <= 10; j++ {
					f := float32(j) / 10
					ang := f * 2.1
					rp = append(rp, mgl32.Vec3{c.X() + dx*f, c.Y() + (rad+0.05)*float32(math.Cos(float64(ang))), c.Z() + side*(rad+0.05)*float32(math.Sin(float64(ang)))})
				}
				rl := limbOf(rp, 0.02, 0.008, 1, 2, 5, 0, 0)
				rl.tone0, rl.tone1 = 0.6, 0.55
				p.add(rl)
			}
			for y := h * 0.25; y < h-0.1; y += 0.3 {
				tp, _, _ := p.limbs[len(p.limbs)-5].at((y) / h)
				rr := (1 - y/h) * 0.9 * (0.5 + h*0.15)
				for w := 0; w < 4; w++ {
					a := r.f(0, 2*math.Pi)
					d := yawDir(a).Add(yUp.Mul(-0.15)).Normalize()
					p.spray(tp, d, d.Cross(yUp), rr+0.15, tileHemlockSpray, partLeaf, tp, 0.3+y/h*0.4, r.f(0.85, 1.05))
				}
			}
		}
		// Moss fringe and ferns along its back.
		for k := 0; k < 40; k++ {
			u := r.f(0.02, 0.98)
			c, _, _ := log.at(u)
			a := r.f(-1.3, 1.3)
			out := mgl32.Vec3{0, float32(math.Cos(float64(a))), float32(math.Sin(float64(a)))}
			base := c.Add(out.Mul(rad * 1.12))
			s := r.f(0.18, 0.3)
			tile, part := tileMossFringe, partAccentCard
			if k%4 == 0 {
				tile, part, s = tileFern, partLeaf, r.f(0.3, 0.5)
			}
			p.cards = append(p.cards, rfCard{base: base, right: mgl32.Vec3{s * 0.5, 0, 0}, up: out.Add(yUp).Normalize().Mul(s),
				n: out, tile: tile, part: part, sway0: 0, sway1: 0.15, tone: r.f(0.8, 1.05)})
		}
	case 1:
		// A broken snag: a hollow, splintered stump, moss up its sides,
		// bracket fungi on it.
		h := r.f(3.5, 5)
		rad := r.f(0.8, 1.0)
		var pts []mgl32.Vec3
		var radii []float32
		for y := float32(-0.5); y <= h; y += 0.25 {
			pts = append(pts, mgl32.Vec3{0, y, 0})
			radii = append(radii, rad*(1+0.4*float32(math.Exp(-float64(y+0.5)*1.4))))
		}
		sn := rfLimb{pts: pts, radii: radii, depth: 0, sides: 32, part: partBark, relief: rfSum(decay.relief(), rfButtress(6, 0.3, 1.2, seed)),
			tone0: 0.62, tone1: 0.75, moss: 0.55, cap: true}
		p.add(sn)
		for k := 0; k < 9; k++ {
			a := r.f(0, 2*math.Pi)
			o := yawDir(a).Mul(rad * 0.85).Add(yUp.Mul(h - 0.2))
			sl := limbOf(pathOf(o, yUp.Add(yawDir(a).Mul(r.f(-0.1, 0.25))), r.f(0.4, 1.4), 3, nil), r.f(0.08, 0.16), 0.006, 0.8, 1, 4, 0, 0)
			sl.tone0, sl.tone1 = 0.8, 1.1
			p.add(sl)
		}
		p.extra = append(p.extra, func(m *fmesh, lod int) {
			if lod > 0 {
				return
			}
			for k := 0; k < 5; k++ {
				a := r.f(0, 2*math.Pi)
				y := r.f(0.8, h-0.6)
				c := yawDir(a).Mul(rad * 0.95).Add(yUp.Mul(y))
				out := yawDir(a)
				s := r.f(0.15, 0.3)
				m.blob(1, func(d mgl32.Vec3) mgl32.Vec3 {
					// A shelf: a flattened half disc standing out of the bark.
					q := mgl32.Vec3{d[0] * s, d[1] * s * 0.25, d[2] * s}
					o := out.Mul(max(q.Dot(out), -0.02))
					return c.Add(q.Sub(out.Mul(q.Dot(out))).Add(o))
				}, false, partBark, func(q mgl32.Vec3) float32 { return 1.4 + 2*(q[1]-c[1]) }, nil)
			}
		})
	default:
		// A root wad: the root plate torn up standing on edge, its log lying
		// away from it.
		rad := r.f(1.8, 2.3)
		c := mgl32.Vec3{0, rad * 0.7, 0}
		sd := uint32(r.Int31())
		m.blob(2-lod, func(d mgl32.Vec3) mgl32.Vec3 {
			n := 1 + (noise3(d.Mul(2.2), sd)-0.5)*0.5
			return c.Add(mgl32.Vec3{d[0] * 0.35 * n, d[1] * rad * n, d[2] * rad * n})
		}, false, partBark, func(q mgl32.Vec3) float32 { return 0.45 + 0.1*noise3(q.Mul(3), sd) }, nil)
		for k := 0; k < 22; k++ {
			a := float32(k)/22*2*math.Pi + r.f(-0.15, 0.15)
			out := mgl32.Vec3{0, float32(math.Cos(float64(a))), float32(math.Sin(float64(a)))}
			o := c.Add(out.Mul(rad * 0.4))
			d := out.Add(mgl32.Vec3{r.f(-0.8, -0.2), 0, 0}).Normalize()
			rl := limbOf(pathOf(o, d, rad*r.f(0.7, 1.2), 6, func(u float32, _ mgl32.Vec3) mgl32.Vec3 {
				return mgl32.Vec3{-0.2, -0.15, 0}
			}), r.f(0.04, 0.11), 0.008, 1, 1, 6, 0, 0)
			rl.tone0, rl.tone1 = 0.55, 0.7
			p.add(rl)
		}
		var pts []mgl32.Vec3
		lr := r.f(0.55, 0.7)
		for x := float32(0.2); x <= 8.5; x += 0.3 {
			pts = append(pts, mgl32.Vec3{x, lr*0.6 + 0.4*float32(math.Exp(-float64(x))), 0.15 * float32(math.Sin(float64(x*0.4)))})
		}
		lg := limbOf(pts, lr*1.15, lr*0.9, 1, 0, 28, 0, 0)
		lg.relief = decay.relief()
		lg.moss = 1.1
		lg.cap = true
		p.add(lg)
	}
	p.write(m, lod)
	return m.v
}

func buildBranchLitter(variant, lod int) []float32 {
	seed := uint32(45000 + variant*131)
	r := frand{rand.New(rand.NewSource(int64(seed)))}
	p := &rfPlant{farDepth: 1, farEvery: 3, mossSeed: seed}
	m := &fmesh{}
	lying := func(o, d mgl32.Vec3, l, rad float32, depth, sides int) int {
		// A stick lying along the ground, bowed and touching it at its ends.
		pts := pathOf(o, d, l, max(int(l/0.25), 3), func(u float32, dd mgl32.Vec3) mgl32.Vec3 {
			return mgl32.Vec3{r.f(-0.3, 0.3), 0, r.f(-0.3, 0.3)}
		})
		for i := range pts {
			t := float32(i) / float32(len(pts)-1)
			pts[i][1] = o.Y() + rad*0.6 + 0.04*float32(math.Sin(float64(t)*math.Pi))
		}
		lb := limbOf(pts, rad, rad*0.4, 1, depth, sides, 0, 0)
		lb.relief = rfBark{kind: rfBarkScaly, ridges: 5, depth: 0.12, girth: rad, seed: seed + uint32(len(p.limbs))}.relief()
		lb.tone0, lb.tone1 = r.f(0.55, 0.75), r.f(0.7, 0.9)
		lb.cap = true
		return p.add(lb)
	}
	switch variant {
	case 0: // a fallen conifer branch, its needles gone red-brown
		l := r.f(2.4, 3.4)
		d := mgl32.Vec3{1, 0, r.f(-0.2, 0.2)}.Normalize()
		o := d.Mul(-l / 2)
		mi := lying(o, d, l, 0.05, 0, 9)
		main := p.limbs[mi]
		for k := 0; k < 12; k++ {
			u := 0.12 + 0.86*float32(k)/12
			bp, bd, _ := main.at(u)
			side := bd.Cross(yUp).Normalize()
			if k%2 == 1 {
				side = side.Mul(-1)
			}
			bdir := side.Add(bd.Mul(0.7)).Add(yUp.Mul(r.f(0, 0.35))).Normalize()
			bl := r.f(0.4, 0.9) * (1.1 - 0.5*u)
			tw := limbOf(pathOf(bp, bdir, bl, 3, func(_ float32, _ mgl32.Vec3) mgl32.Vec3 { return yUp.Mul(-0.6) }), 0.015, 0.004, 1, 1, 4, 0, 0.1)
			tw.tone0, tw.tone1 = 0.6, 0.7
			ti := p.add(tw)
			twig := p.limbs[ti]
			for s := 0; s < 3; s++ {
				v := 0.25 + 0.75*float32(s)/3
				sp0, sd, _ := twig.at(v)
				dd := sd.Add(mgl32.Vec3{r.f(-0.3, 0.3), r.f(-0.05, 0.1), r.f(-0.3, 0.3)}).Normalize()
				p.cards = append(p.cards, rfCard{base: sp0, right: dd.Cross(yUp).Normalize().Mul(0.2), up: dd.Mul(0.5),
					n: yUp, tile: tileHemlockSpray, part: partAccentCard, sway0: 0.05, sway1: 0.12, tone: r.f(0.7, 1)})
			}
		}
	case 1: // a scatter of sticks, crossing
		for k := 0; k < 9; k++ {
			o := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0, 0.8)).Add(yUp.Mul(float32(k) * 0.012))
			d := yawDir(r.f(0, 2*math.Pi))
			l := r.f(0.3, 1.3)
			rad := r.f(0.008, 0.028)
			si := lying(o, d, l, rad, 0, 6)
			st := p.limbs[si]
			if r.Float32() < 0.6 {
				u := r.f(0.3, 0.7)
				fp, fd, fr := st.at(u)
				fdir := rotAxis(fd, yUp, r.f(0.4, 0.8)*float32(1-2*r.Intn(2)))
				lying(fp.Sub(yUp.Mul(fr*0.6)), fdir, l*r.f(0.3, 0.5), fr*0.7, 1, 4)
			}
		}
	default: // a mossy fallen limb, ferns along its back
		l := r.f(2.2, 3.2)
		d := mgl32.Vec3{1, 0, r.f(-0.2, 0.2)}.Normalize()
		mi := lying(d.Mul(-l/2), d, l, 0.12, 0, 14)
		p.limbs[mi].moss = 1.2
		main := p.limbs[mi]
		for k := 0; k < 14; k++ {
			u := r.f(0.05, 0.95)
			c, _, rr := main.at(u)
			base := c.Add(yUp.Mul(rr * 1.15))
			s := r.f(0.15, 0.28)
			tile, part := tileMossFringe, partAccentCard
			if k%3 == 0 {
				tile, part, s = tileFern, partLeaf, r.f(0.25, 0.42)
			}
			up := yUp.Add(mgl32.Vec3{r.f(-0.4, 0.4), 0, r.f(-0.4, 0.4)}).Normalize()
			p.cards = append(p.cards, rfCard{base: base, right: perp(up, r.f(0, math.Pi)).Mul(s * 0.45), up: up.Mul(s),
				n: yUp, tile: tile, part: part, sway0: 0, sway1: 0.2, tone: r.f(0.8, 1.05)})
		}
	}
	p.write(m, lod)
	return m.v
}

func buildForestLitter(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(46000 + variant*131)))}
	m := &fmesh{}
	// curled writes a fallen leaf of tile size s at c turned by a, its
	// edges and tip curling up, lifted by lift so overlapping leaves stack.
	curled := func(c mgl32.Vec3, a, s, lift float32, tile int, nu int) {
		right, fwd := yawDir(a), yawDir(a+math.Pi/2)
		curl := r.f(0.1, 0.4)
		tipc := r.f(-0.1, 0.35)
		m.sheet(nu, nu, tile, partAccentCard, func(u, v float32) mgl32.Vec3 {
			h := lift + s*(curl*u*u*0.5+tipc*v*v*0.4)
			return c.Add(right.Mul(u * s * 0.5)).Add(fwd.Mul((v - 0.5) * s)).Add(yUp.Mul(h))
		}, yUp, 0.05, 0.1, r.f(0.7, 1.1))
	}
	nu := 3
	if lod > 0 {
		nu = 1
	}
	switch variant {
	case 0: // bigleaf maple leaves
		n := 14
		if lod > 0 {
			n = 5
		}
		for i := 0; i < n; i++ {
			c := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0, 0.42))
			curled(c, r.f(0, 2*math.Pi), r.f(0.18, 0.3), 0.006+0.004*float32(i), tileMapleLeaf, nu)
		}
	case 1: // conifer litter: needle mat, sprigs, cones
		for i := 0; i < 2-lod; i++ {
			a := r.f(0, 2*math.Pi)
			right, fwd := yawDir(a), yawDir(a+math.Pi/2)
			m.card(fwd.Mul(-0.32).Add(yUp.Mul(0.008+float32(i)*0.004)), right.Mul(0.32), fwd.Mul(0.64), tileLitter, partAccentCard, yUp, 0, 0, r.f(0.8, 1))
		}
		a := r.f(0, 2*math.Pi)
		right, fwd := yawDir(a), yawDir(a+math.Pi/2)
		m.card(fwd.Mul(-0.28).Add(yUp.Mul(0.018)), right.Mul(0.28), fwd.Mul(0.56), tileTwigLitter, partAccentCard, yUp, 0, 0, r.f(0.8, 1))
		if lod == 0 {
			for i := 0; i < 4; i++ {
				c := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0, 0.35))
				curled(c, r.f(0, 2*math.Pi), r.f(0.14, 0.22), 0.02+0.003*float32(i), tileCedarSpray, 2)
			}
			for i := 0; i < 5; i++ {
				// A cone lying on its side: a Douglas-fir's or a spruce's.
				c := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0, 0.4))
				d := yawDir(r.f(0, 2*math.Pi))
				l := r.f(0.05, 0.09)
				rr := l * 0.32
				pts := []mgl32.Vec3{c.Add(yUp.Mul(rr * 0.8)), c.Add(d.Mul(l * 0.5)).Add(yUp.Mul(rr)), c.Add(d.Mul(l)).Add(yUp.Mul(rr * 0.8))}
				m.ribTube(pts, []float32{rr * 0.7, rr, rr * 0.4}, 6, 0.3, []float32{0.7, 0.85, 0.8}, partBark)
			}
		}
	default: // oval leaves: alder, cottonwood, salal
		n := 16
		if lod > 0 {
			n = 5
		}
		for i := 0; i < n; i++ {
			c := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0, 0.42))
			curled(c, r.f(0, 2*math.Pi), r.f(0.09, 0.15), 0.006+0.003*float32(i), tileOvalLeaf, nu)
		}
	}
	return m.v
}

func buildMossMound(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(47000 + variant*131)))}
	m := &fmesh{}
	sub := 2 - lod
	sd := uint32(r.Int31())
	var core func(d mgl32.Vec3) mgl32.Vec3
	var height float32
	switch variant {
	case 0: // a hummock of deep moss
		rx, h := r.f(0.45, 0.6), r.f(0.3, 0.42)
		height = h
		core = func(d mgl32.Vec3) mgl32.Vec3 {
			n := 1 + (noise3(d.Mul(2.4), sd)-0.5)*0.45 + (noise3(d.Mul(6), sd+1)-0.5)*0.15
			return mgl32.Vec3{d[0] * rx * n, max(d[1], -0.2) * h * n, d[2] * rx * n}
		}
		m.blob(sub, core, false, partSolid, func(p mgl32.Vec3) float32 {
			// Speckled: the cushion is a crowd of tufts, light and shade.
			return 0.6 + 0.9*max(p[1], 0) + (noise3(p.Mul(14), sd+5)-0.5)*0.45
		}, nil)
	case 1: // a stone capped with moss
		rx, h := r.f(0.4, 0.55), r.f(0.3, 0.45)
		height = h
		stone := func(d mgl32.Vec3) mgl32.Vec3 {
			n := 0.85 + noise3(d.Mul(1.7), sd)*0.28 + noise3(d.Mul(4.1), sd+1)*0.1
			return mgl32.Vec3{d[0] * rx * n, max(d[1], -0.25) * h * n, d[2] * rx * 0.85 * n}
		}
		core = stone
		m.blob(sub, stone, true, partBark, func(p mgl32.Vec3) float32 { return 0.75 + 0.5*max(p[1], 0) }, nil)
		// The moss: the stone's top, a little proud of it, ragged at its edge.
		m.blob(sub, func(d mgl32.Vec3) mgl32.Vec3 {
			return stone(d).Mul(1.05 + 0.05*noise3(d.Mul(5), sd+2))
		}, false, partSolid, func(p mgl32.Vec3) float32 { return 0.7 + 0.7*max(p[1], 0) },
			func(d mgl32.Vec3) bool { return d[1]+(noise3(d.Mul(2.5), sd+3)-0.5)*0.8 > 0.05 })
	default: // a stub of old wood buried in moss
		rr, h := r.f(0.2, 0.3), r.f(0.35, 0.6)
		height = h
		p := &rfPlant{mossSeed: sd}
		var pts []mgl32.Vec3
		for y := float32(-0.2); y <= h; y += 0.08 {
			pts = append(pts, mgl32.Vec3{0, y, 0})
		}
		st := limbOf(pts, rr*1.2, rr, 1, 0, 20, 0, 0)
		st.relief = rfBark{kind: rfBarkFibrous, ridges: 14, depth: 0.08, twist: 0.1, girth: rr, seed: sd}.relief()
		st.moss = 1.4
		st.cap = true
		p.add(st)
		p.write(m, lod)
		core = func(d mgl32.Vec3) mgl32.Vec3 { return mgl32.Vec3{d[0] * rr * 1.2, max(d[1], 0) * h, d[2] * rr * 1.2} }
	}
	// The fringe: short fronds standing out of the surface, so the moss has
	// a soft, furred silhouette rather than a hard one.
	n := 150
	if lod > 0 {
		n = 24
	}
	for i := 0; i < n; i++ {
		d := mgl32.Vec3{r.f(-1, 1), r.f(0.1, 1), r.f(-1, 1)}.Normalize()
		at := core(d)
		if variant == 2 {
			at = mgl32.Vec3{d[0], 0, d[2]}.Normalize().Mul(core(d).Len()*0.9 + 0.04)
			at[1] = r.f(0, height)
		}
		out := mgl32.Vec3{d[0], d[1] * 1.5, d[2]}.Normalize()
		up := out.Add(yUp).Normalize()
		s := r.f(0.12, 0.22)
		if lod > 0 {
			s *= 1.8
		}
		m.card(at.Sub(up.Mul(s*0.3)), perp(up, r.f(0, math.Pi)).Mul(s*0.6), up.Mul(s), tileMossFringe, partLeaf, out, 0, 0.12, r.f(0.8, 1.05))
	}
	return m.v
}
