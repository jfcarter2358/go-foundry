package render

// foliage_atlas_rainforest.go — the atlas tiles the temperate rainforest's
// plants are cut from (foliage_rainforest_*.go): each conifer's own spray,
// each broadleaf's own leaf, the ferns' fronds, hanging moss, and the
// forest floor's litter.
//
// The tiles follow foliage_atlas.go's rules: shape in alpha, grey detail
// (veins, midribs, the furrow between two needles) in luminance, never
// colour, drawn in a space foliageTileUnits across with y = 0 at the card's
// base. A spray is drawn as the branch end really is at the scale its card
// is placed at — a spruce's needles stand out all round its shoots, a
// hemlock's lie in two flat rows, a cedar's are scales pressed flat along a
// fan of twigs — because at the distance where a tree is mostly cards, that
// texture is what tells one species from another.

import (
	"math"
	"math/rand"
)

// drawRainforestTile draws one of the rainforest's tiles onto c.
func drawRainforestTile(c *atlasCanvas, tile int) {
	n := float64(foliageTileUnits)
	r := rand.New(rand.NewSource(int64(tile)*7919 + 17))
	rf := func(a, b float64) float64 { return a + r.Float64()*(b-a) }
	switch tile {
	case tileSpruceSpray:
		// A branch end from above: a main shoot, side shoots swept toward the
		// tip, and every shoot a bottle-brush of short, stiff needles standing
		// out all round it — so the spray is dense down its middle and bristly
		// at its edges. New growth at the shoot tips is lighter.
		shoot := func(x0, y0, ang, l, w float64) {
			x1, y1 := x0+math.Cos(ang)*l, y0+math.Sin(ang)*l
			c.line(x0, y0, x1, y1, w, 0.36)
			steps := int(l / 0.9)
			for k := 0; k < steps; k++ {
				u := (float64(k) + rf(0, 1)) / float64(steps)
				bx, by := x0+(x1-x0)*u, y0+(y1-y0)*u
				nl := rf(3.6, 5.8) * (1 - 0.25*u)
				tone := rf(0.62, 0.92)
				if u > 0.78 {
					tone += 0.16
				}
				for _, ns := range [3]float64{-1, 1, 0} {
					na := ang + ns*rf(0.55, 1.15)
					if ns == 0 {
						na = ang + rf(-0.35, 0.35)
						if r.Float64() < 0.6 {
							continue
						}
					}
					c.line(bx, by, bx+math.Cos(na)*nl, by+math.Sin(na)*nl, 0.42, tone)
				}
			}
		}
		axis := math.Pi/2 + rf(-0.05, 0.05)
		for i := 0; i < 9; i++ {
			t := 0.07 + float64(i)*0.1
			y := n * t
			sl := (1 - t*0.72) * n * 0.44
			for _, side := range [2]float64{-1, 1} {
				shoot(n/2+math.Cos(axis)*y, y, math.Pi/2+side*rf(0.8, 1.0), sl*rf(0.85, 1.05), 0.75)
			}
		}
		shoot(n/2, 0, axis, n*0.96, 1.3)
	case tileHemlockSpray:
		// Lacy and flat: a main twig nodding at its tip, side twigs and their
		// twiglets alternating, and the needles short, soft and in two flat
		// rows of mixed lengths — which is what makes a hemlock look feathery
		// where a spruce looks bristly.
		var twig func(x0, y0, ang, l, w float64, depth int)
		twig = func(x0, y0, ang, l, w float64, depth int) {
			const segs = 6
			px, py := x0, y0
			a := ang
			for k := 1; k <= segs; k++ {
				// Droop: the twig nods over along its length (the main twig
				// only a little, so the spray stays on its card).
				a += (-math.Pi/2 - a) * 0.018 * float64(2-depth)
				nx, ny := px+math.Cos(a)*l/segs, py+math.Sin(a)*l/segs
				c.line(px, py, nx, ny, w, 0.4)
				steps := int(l / segs / 0.7)
				for s := 0; s < steps; s++ {
					u := (float64(s) + 0.5) / float64(steps)
					bx, by := px+(nx-px)*u, py+(ny-py)*u
					for _, side := range [2]float64{-1, 1} {
						nl := rf(1.8, 3.6)
						na := a + side*rf(1.25, 1.6)
						c.line(bx, by, bx+math.Cos(na)*nl, by+math.Sin(na)*nl, 0.55, rf(0.68, 0.98))
					}
				}
				if depth > 0 && k < segs {
					side := 1.0
					if k%2 == 0 {
						side = -1
					}
					twig(nx, ny, a+side*rf(0.75, 1.0), l*rf(0.4, 0.55)*(1-float64(k)/segs*0.5), w*0.65, depth-1)
				}
				px, py = nx, ny
			}
		}
		twig(n/2, 0, math.Pi/2+rf(-0.08, 0.08), n*0.95, 1.1, 2)
	case tileCedarSpray:
		// A flat fan of scale-leaf sprays: every twig is clothed in tiny
		// overlapping scales (a braid of little ellipses), and branches
		// alternately, again and again, into a frond like a fern's — the
		// redcedar's drooping lace.
		var spray func(x0, y0, ang, l float64, depth int)
		spray = func(x0, y0, ang, l float64, depth int) {
			x1, y1 := x0+math.Cos(ang)*l, y0+math.Sin(ang)*l
			w := 0.55 + 0.45*float64(depth)
			c.line(x0, y0, x1, y1, w, 0.55)
			steps := int(l / 1.1)
			for k := 0; k < steps; k++ {
				u := (float64(k) + 0.5) / float64(steps)
				bx, by := x0+(x1-x0)*u, y0+(y1-y0)*u
				side := 1.0
				if k%2 == 0 {
					side = -1
				}
				off := side * w * 0.55
				c.ellipse(bx-math.Sin(ang)*off, by+math.Cos(ang)*off, w*1.15, w*0.6, ang+side*0.4, rf(0.75, 1.02))
			}
			if depth == 0 {
				return
			}
			kids := 4 + depth
			for k := 0; k < kids; k++ {
				u := 0.18 + 0.8*float64(k)/float64(kids)
				side := 1.0
				if k%2 == 0 {
					side = -1
				}
				bx, by := x0+(x1-x0)*u, y0+(y1-y0)*u
				spray(bx, by, ang+side*rf(0.6, 0.85), l*0.42*(1-0.55*u), depth-1)
			}
		}
		spray(n/2, 0, math.Pi/2+rf(-0.06, 0.06), n*0.94, 3)
	case tileFirSpray:
		// Soft, longer needles standing out round the twig and swept forward,
		// a few side twigs, a pointed bud at every tip: a Douglas-fir's.
		shoot := func(x0, y0, ang, l, w float64) {
			x1, y1 := x0+math.Cos(ang)*l, y0+math.Sin(ang)*l
			c.line(x0, y0, x1, y1, w, 0.4)
			steps := int(l / 1.05)
			for k := 0; k < steps; k++ {
				u := (float64(k) + rf(0, 1)) / float64(steps)
				bx, by := x0+(x1-x0)*u, y0+(y1-y0)*u
				nl := rf(6, 9) * (1 - 0.3*u)
				for _, ns := range [2]float64{-1, 1} {
					na := ang + ns*rf(0.5, 1.05)
					c.line(bx, by, bx+math.Cos(na)*nl, by+math.Sin(na)*nl, 0.62, rf(0.66, 1))
				}
			}
			c.ellipse(x1, y1, 1.2, 2.4, ang+math.Pi/2, 0.45)
		}
		for i := 0; i < 4; i++ {
			t := 0.15 + float64(i)*0.19
			for _, side := range [2]float64{-1, 1} {
				shoot(n/2, n*t, math.Pi/2+side*rf(0.65, 0.85), n*0.38*(1-t*0.6), 0.85)
			}
		}
		shoot(n/2, 0, math.Pi/2, n*0.95, 1.2)
	case tileMapleLeaves:
		// Bigleaf maple: three or four huge, deeply five-lobed leaves on long
		// stalks from a twig at the bottom, overlapping.
		c.line(n/2, 0, n/2, n*0.3, 1.6, 0.42)
		type lf struct{ x, y, a, R float64 }
		leaves := []lf{
			{n * 0.3, n * 0.52, math.Pi/2 + 0.55, n * 0.25},
			{n * 0.72, n * 0.5, math.Pi/2 - 0.5, n * 0.24},
			{n * 0.5, n * 0.7, math.Pi / 2, n * 0.27},
		}
		if r.Float64() < 0.8 {
			leaves = append([]lf{{n * 0.5, n * 0.33, math.Pi/2 + rf(-0.3, 0.3), n * 0.17}}, leaves...)
		}
		for _, l := range leaves {
			c.line(n/2, n*0.3, l.x-math.Cos(l.a)*l.R*0.2, l.y-math.Sin(l.a)*l.R*0.2, 0.8, 0.5)
			c.palmate(l.x, l.y, l.a, l.R, 5, 0.62, rf(0.82, 1), r)
		}
	case tileAlderLeaves:
		// Red alder: a forking twig, and on it alternate ovate leaves with
		// toothed edges and straight, parallel side veins.
		type twig struct{ x0, y0, x1, y1 float64 }
		var twigs []twig
		var grow func(x, y, ang, l float64, depth int)
		grow = func(x, y, ang, l float64, depth int) {
			x1, y1 := x+math.Cos(ang)*l, y+math.Sin(ang)*l
			twigs = append(twigs, twig{x, y, x1, y1})
			if depth == 0 {
				return
			}
			for _, turn := range [2]float64{-1, 1} {
				grow(x1, y1, ang+turn*rf(0.45, 0.75), l*rf(0.62, 0.78), depth-1)
			}
		}
		grow(n/2, 0, math.Pi/2+rf(-0.1, 0.1), n*0.32, 2)
		for _, tw := range twigs {
			c.line(tw.x0, tw.y0, tw.x1, tw.y1, 0.9, 0.42)
		}
		for _, tw := range twigs {
			base := math.Atan2(tw.y1-tw.y0, tw.x1-tw.x0)
			for k := 0; k < 4; k++ {
				u := 0.2 + 0.24*float64(k)
				side := float64(1 - 2*(k%2))
				bx, by := tw.x0+(tw.x1-tw.x0)*u, tw.y0+(tw.y1-tw.y0)*u
				c.toothedLeaf(bx, by, base+side*rf(0.6, 1.0), rf(17, 23), rf(6.5, 8), 0.55, rf(0.78, 1), r)
			}
			c.toothedLeaf(tw.x1, tw.y1, base+rf(-0.2, 0.2), rf(18, 22), 7.5, 0.55, rf(0.82, 1), r)
		}
	case tileVineMapleLeaves:
		// Vine maple: round leaves of seven to nine shallow lobes in opposite
		// pairs, held flat — a shade leaf, spread to catch what light there is.
		c.line(n/2, 0, n/2+rf(-3, 3), n*0.85, 1.0, 0.45)
		for i := 0; i < 4; i++ {
			y := n * (0.16 + float64(i)*0.2)
			R := n * (0.15 - 0.012*float64(i))
			for _, side := range [2]float64{-1, 1} {
				a := math.Pi/2 + side*rf(0.9, 1.2)
				px, py := n/2+math.Cos(a)*R*1.2, y+math.Sin(a)*R*1.2
				c.line(n/2, y, px, py, 0.6, 0.5)
				c.palmate(px+math.Cos(a)*R*0.15, py+math.Sin(a)*R*0.15, a, R, 7+r.Intn(3), 0.25, rf(0.82, 1), r)
			}
		}
		c.palmate(n/2, n*0.88, math.Pi/2, n*0.11, 7, 0.25, 0.95, r)
	case tileCottonwoodLeaves:
		// Black cottonwood: broad, heart-shaped leaves with drawn-out tips,
		// each on a long stalk off an upright twig.
		c.line(n/2, 0, n/2+rf(-2, 2), n*0.92, 1.1, 0.45)
		for i := 0; i < 7; i++ {
			t := 0.12 + float64(i)*0.12
			side := float64(1 - 2*(i%2))
			y := n * t
			a := math.Pi/2 + side*rf(0.7, 1.1)
			pl := rf(8, 12)
			px, py := n/2+math.Cos(a)*pl, y+math.Sin(a)*pl
			c.line(n/2, y, px, py, 0.55, 0.5)
			c.heartLeaf(px, py, a+side*rf(-0.2, 0.4), rf(20, 26)*(1-0.25*t), rf(0.8, 1), r)
		}
		c.heartLeaf(n/2, n*0.9, math.Pi/2, 18, 0.95, r)
	case tileMossDrape:
		// Hanging moss and lichen: strands from the top edge, longest in the
		// middle, each wandering down and furred with short side shoots, so a
		// curtain hung from a limb thins to a ragged fringe at its foot.
		for i := 0; i < 70; i++ {
			// Bunched toward the middle, where the curtain hangs from.
			x := n/2 + (rf(0, 1)-0.5)*rf(0.3, 1)*(n-12)
			edge := math.Abs(x-n/2) / (n / 2)
			l := n * 0.95 * math.Pow(rf(0.05, 1), 0.8) * (1 - 0.85*edge)
			y := n - rf(0, 6)
			w := rf(0.7, 1.1)
			tone := rf(0.6, 1)
			ph, fr := rf(0, 6), rf(0.05, 0.12)
			steps := int(l / 2.4)
			for k := 0; k < steps; k++ {
				nx := x + math.Sin(ph+float64(k)*fr*2.4)*0.9 + rf(-0.5, 0.5)
				ny := y - 2.4
				u := float64(k) / float64(max(steps, 1))
				ww := w * (1 - 0.55*u)
				c.line(x, y, nx, ny, ww, tone*(0.85+0.15*(1-u)))
				for _, side := range [2]float64{-1, 1} {
					if r.Float64() < 0.65 {
						fa := -math.Pi/2 + side*rf(0.5, 1.1)
						fl := rf(1.4, 3.2) * (1 - 0.4*u)
						c.line(x, y, x+math.Cos(fa)*fl, y+math.Sin(fa)*fl, 0.4, tone*rf(0.8, 1.05))
					}
				}
				x, y = nx, ny
			}
		}
	case tileSwordFern:
		// A sword fern frond: a short bare stalk, then a long, narrow blade of
		// leathery pinnae, each lance-shaped with a toothed edge and an ear on
		// its upper side at the base, all nearly the same length until the
		// last third, where they shorten to a point.
		c.line(n/2, 0, n/2, n*0.98, 1.3, 0.42)
		const pairs = 27
		for i := 0; i < pairs; i++ {
			t := 0.12 + 0.85*float64(i)/pairs
			y := n * t
			l := n * 0.33 * math.Pow(min((1-t)/0.38, 1), 0.85) * (0.82 + 0.18*min((t-0.12)/0.2, 1))
			if l < 2.5 {
				continue
			}
			for _, side := range [2]float64{-1, 1} {
				a := math.Pi/2 - side*(math.Pi/2-0.42)
				c.pinna(n/2, y, a, l, l*0.16, 0.55, side, rf(0.8, 1), r)
			}
		}
	case tileLadyFern:
		// Lady fern: widest at its middle and tapering both ways, each pinna
		// itself divided into toothed pinnules — fine lace.
		c.line(n/2, 0, n/2, n*0.99, 1.0, 0.5)
		const pairs = 21
		for i := 0; i < pairs; i++ {
			t := 0.06 + 0.92*float64(i)/pairs
			y := n * t
			l := n * 0.36 * math.Pow(math.Sin(math.Pi*(0.1+0.9*t)), 0.85)
			if l < 2 {
				continue
			}
			for _, side := range [2]float64{-1, 1} {
				a := math.Pi/2 - side*(math.Pi/2-0.55)
				ex, ey := n/2+math.Cos(a)*l, y+math.Sin(a)*l
				c.line(n/2, y, ex, ey, 0.45, 0.55)
				pn := max(int(l/2.6), 2)
				for k := 0; k < pn; k++ {
					u := (float64(k) + 0.5) / float64(pn)
					bx, by := n/2+(ex-n/2)*u, y+(ey-y)*u
					pl := max(l*0.24*(1-0.7*u), 1.2)
					for _, ps := range [2]float64{-1, 1} {
						c.toothedLeaf(bx, by, a+ps*1.05, pl, pl*0.36, 0.0, rf(0.82, 1), r)
					}
				}
			}
		}
	case tileDeerFern:
		// Deer fern: a narrow frond whose pinnae are short, rounded and so
		// close-set they read as the rungs of a ladder, running nearly to the
		// base.
		c.line(n/2, 0, n/2, n*0.98, 1.0, 0.45)
		const pairs = 34
		for i := 0; i < pairs; i++ {
			t := 0.04 + 0.94*float64(i)/pairs
			y := n * t
			l := n * 0.2 * math.Pow(math.Sin(math.Pi*(0.04+0.96*t)), 0.55)
			for _, side := range [2]float64{-1, 1} {
				a := math.Pi/2 - side*(math.Pi/2-0.18)
				c.ellipse(n/2+math.Cos(a)*l*0.52, y+math.Sin(a)*l*0.52, l*0.52, max(n*0.0135, 1.1), a, rf(0.8, 1))
			}
		}
	case tileOxalis:
		// One redwood sorrel leaf from above: three heart-shaped leaflets
		// meeting at the stalk, each creased down its midrib with a pale
		// blotch along it.
		for k := 0; k < 3; k++ {
			a := math.Pi/2 + float64(k)*2*math.Pi/3
			c.obcordate(n/2, n/2, a, n*0.46, n*0.27)
		}
	case tileTwigLitter:
		// The floor under the trees from above: fallen twigs, forked and
		// crossing, flakes of bark, a few cones and loose needles.
		for i := 0; i < 60; i++ {
			x, y := rf(4, n-4), rf(4, n-4)
			a := rf(0, math.Pi)
			l := rf(4, 9)
			c.line(x, y, x+math.Cos(a)*l, y+math.Sin(a)*l, 0.45, rf(0.5, 0.9))
		}
		for i := 0; i < 10; i++ {
			c.ellipse(rf(10, n-10), rf(10, n-10), rf(3, 7), rf(2, 4), rf(0, math.Pi), rf(0.45, 0.75))
		}
		var stick func(x, y, a, l, w float64, depth int)
		stick = func(x, y, a, l, w float64, depth int) {
			const segs = 3
			tone := rf(0.55, 0.9)
			for k := 0; k < segs; k++ {
				a += rf(-0.18, 0.18)
				nx, ny := x+math.Cos(a)*l/segs, y+math.Sin(a)*l/segs
				c.line(x, y, nx, ny, w, tone)
				if depth > 0 && r.Float64() < 0.45 {
					stick(nx, ny, a+rf(0.4, 0.9)*float64(1-2*r.Intn(2)), l*rf(0.35, 0.55), w*0.65, depth-1)
				}
				x, y = nx, ny
			}
		}
		for i := 0; i < 9; i++ {
			stick(rf(10, n-10), rf(10, n-10), rf(0, 2*math.Pi), rf(30, 70), rf(1.2, 2), 2)
		}
		for i := 0; i < 3; i++ {
			cx, cy, a := rf(16, n-16), rf(16, n-16), rf(0, math.Pi)
			c.ellipse(cx, cy, 8, 4.5, a, 0.5)
			for k := -3; k <= 3; k++ {
				c.ellipse(cx+math.Cos(a)*float64(k)*2.2, cy+math.Sin(a)*float64(k)*2.2, 1.6, 3.6, a, 0.68)
			}
		}
	case tileMapleLeaf:
		// One great palmate leaf, the stalk at the bottom: what a bigleaf
		// maple drops, a foot across, and what a devil's club holds up.
		c.line(n/2, 0, n/2, n*0.36, 1.4, 0.5)
		c.palmate(n/2, n*0.36, math.Pi/2, n*0.5, 5, 0.6, 0.95, r)
	case tileOvalLeaf:
		// A leathery oval leaf with a short stalk and a drawn-out tip, finely
		// toothed: salal's, and what alder and cottonwood leaves look like
		// once they lie on the ground.
		c.line(n/2, 0, n/2, n*0.1, 1.5, 0.5)
		c.toothedLeaf(n/2, n*0.07, math.Pi/2, n*0.9, n*0.3, 0.4, 0.95, r)
	case tileHuckleberry:
		// Red huckleberry: bright, angular, zig-zag twigs and small oval
		// leaves spaced along them — an airy shrub you can see through.
		var twig func(x, y, a, l float64, depth int)
		twig = func(x, y, a, l float64, depth int) {
			const segs = 5
			for k := 0; k < segs; k++ {
				zig := 0.35
				if k%2 == 0 {
					zig = -0.35
				}
				na := a + zig
				nx, ny := x+math.Cos(na)*l/segs, y+math.Sin(na)*l/segs
				c.line(x, y, nx, ny, 0.8, 0.6)
				side := zig / 0.35
				la := na + side*rf(0.7, 1.1)
				ll := rf(7, 10)
				c.ellipse(nx+math.Cos(la)*ll*0.5, ny+math.Sin(la)*ll*0.5, ll*0.5, ll*0.3, la, rf(0.8, 1))
				if depth > 0 && k%2 == 1 {
					twig(nx, ny, na-side*rf(0.6, 0.9), l*0.5, depth-1)
				}
				x, y = nx, ny
			}
		}
		twig(n/2, 0, math.Pi/2, n*0.95, 2)
	case tileMossFringe:
		// A moss carpet side on: a dense, dark base and above it a fringe of
		// feathery, stair-stepped fronds of every height, lit at their tips.
		for i := 0; i < 260; i++ {
			c.ellipse(rf(0, n), rf(0, n*0.16), rf(2, 5), rf(2, 4), rf(0, math.Pi), rf(0.4, 0.65))
		}
		for i := 0; i < 70; i++ {
			x := rf(2, n-2)
			h := n * rf(0.25, 0.92) * (1 - 0.35*math.Abs(x-n/2)/(n/2))
			lean := rf(-0.25, 0.25)
			const segs = 9
			px, py := x, 0.0
			for k := 1; k <= segs; k++ {
				t := float64(k) / segs
				nx, ny := x+lean*h*t*t, h*t
				tone := 0.5 + 0.55*t
				c.line(px, py, nx, ny, 0.65, tone)
				for _, side := range [2]float64{-1, 1} {
					bl := rf(2, 4.5) * (1 - 0.5*t)
					ba := math.Pi/2 + side*rf(0.7, 1.1) + lean
					c.line(nx, ny, nx+math.Cos(ba)*bl, ny+math.Sin(ba)*bl, 0.4, tone*rf(0.85, 1.05))
				}
				px, py = nx, ny
			}
		}
	}
}

// palmate draws a palmately lobed leaf — lobes leaves radiating from where
// its stalk meets it at (cx, cy), the middle one along ang — of reach R.
// sinus is how deep the cuts between the lobes go (0 a round leaf, 0.7 one
// cut nearly to its middle). Each lobe has its own vein out from the centre
// and a coarsely toothed edge.
func (c *atlasCanvas) palmate(cx, cy, ang, R float64, lobes int, sinus, tone float64, r *rand.Rand) {
	spread := math.Pi * 0.82 / float64(lobes-1) * 2
	if lobes <= 5 {
		spread = 0.95
	}
	type lobe struct{ a, l, w float64 }
	ls := make([]lobe, lobes)
	for k := range ls {
		off := float64(k) - float64(lobes-1)/2
		ls[k] = lobe{
			a: ang + off*spread*(1+0.04*(r.Float64()-0.5)),
			l: R * (1 - 0.32*math.Pow(math.Abs(off)/(float64(lobes-1)/2+1e-9), 1.6)),
			w: R * (0.18 + 0.32*(1-sinus)),
		}
	}
	core := R * (0.3 + 0.55*(1-sinus))
	sd := func(x, y float64) float64 {
		dx, dy := x-cx, y-cy
		d := math.Hypot(dx, dy) - core
		for _, lb := range ls {
			ca, sa := math.Cos(lb.a), math.Sin(lb.a)
			u := dx*ca + dy*sa
			v := -dx*sa + dy*ca
			t := u / lb.l
			var e float64
			switch {
			case t < 0:
				e = math.Hypot(u, v) - lb.w*0.3
			case t > 1:
				e = math.Hypot(u-lb.l, v)
			default:
				half := lb.w * math.Pow(math.Sin(math.Pi*min(0.25+t*0.9, 1)), 0.9)
				// Coarse teeth, a few to a lobe, pointing to its tip.
				half *= 1 + 0.12*(math.Mod(t*7, 1)-0.5)*min(t*2, 1)
				e = math.Abs(v) - half
			}
			d = min(d, e)
		}
		return d
	}
	lumAt := func(x, y, d float64) float64 {
		dx, dy := x-cx, y-cy
		l0 := tone
		best := math.MaxFloat64
		for _, lb := range ls {
			ca, sa := math.Cos(lb.a), math.Sin(lb.a)
			u := dx*ca + dy*sa
			v := -dx*sa + dy*ca
			if u > 0 {
				best = min(best, math.Abs(v)/max(1, 0.12*R))
				// Side veins off the lobe's own, angled to its edge.
				if math.Abs(math.Sin((u-math.Abs(v)*0.8)*6/R*math.Pi)) < 0.1 && math.Abs(v) > 0.6 {
					l0 *= 0.92
				}
			}
		}
		if best < 0.08 {
			l0 *= 0.72
		}
		return l0 * (0.84 + 0.16*min(-d/3, 1))
	}
	box := R * 1.1
	c.shape(cx-box, cy-box, cx+box, cy+box, sd, lumAt)
}

// toothedLeaf is leaf with a finely toothed edge and straight side veins: a
// pointed oval from (bx, by) along ang, l long and w half-wide, widest at
// widest (0 the middle, toward 1 its base).
func (c *atlasCanvas) toothedLeaf(bx, by, ang, l, w, widest, tone float64, r *rand.Rand) {
	ca, sa := math.Cos(ang), math.Sin(ang)
	teeth := 10 + r.Float64()*6
	local := func(x, y float64) (float64, float64) {
		dx, dy := x-bx, y-by
		return dx*ca + dy*sa, -dx*sa + dy*ca
	}
	sd := func(x, y float64) float64 {
		u, v := local(x, y)
		t := u / l
		if t < 0 {
			return math.Hypot(u, v)
		}
		if t > 1 {
			return math.Hypot(u-l, v)
		}
		// Widest a little below the middle, drawn out to a point.
		s := math.Pow(t, 1-0.45*widest)
		half := w * math.Pow(math.Sin(math.Pi*s), 0.8)
		half *= 1 - 0.07*math.Abs(math.Sin(t*teeth*math.Pi))
		return math.Abs(v) - half
	}
	lumAt := func(x, y, d float64) float64 {
		u, v := local(x, y)
		l0 := tone
		if math.Abs(v) < max(0.45, w*0.05) {
			l0 *= 0.75
		}
		if math.Abs(math.Sin((u-math.Abs(v)*0.75)*9/l*math.Pi)) < 0.09 && math.Abs(v) > 0.8 {
			l0 *= 0.88
		}
		return l0 * (0.85 + 0.15*min(-d/2.5, 1))
	}
	rr := l + w
	c.shape(bx-rr, by-rr, bx+rr, by+rr, sd, lumAt)
}

// heartLeaf is a deltoid, heart-based leaf from its stalk at (bx, by) along
// ang, l long, with a drawn-out tip and a fine-toothed edge.
func (c *atlasCanvas) heartLeaf(bx, by, ang, l, tone float64, r *rand.Rand) {
	ca, sa := math.Cos(ang), math.Sin(ang)
	local := func(x, y float64) (float64, float64) {
		dx, dy := x-bx, y-by
		return dx*ca + dy*sa, -dx*sa + dy*ca
	}
	w := l * 0.5
	sd := func(x, y float64) float64 {
		u, v := local(x, y)
		t := u / l
		if t < -0.12 {
			return math.Hypot(u+0.12*l, v) - w*0.3
		}
		if t > 1 {
			return math.Hypot(u-l, v)
		}
		// Two rounded shoulders either side of the stalk, then straight
		// sides drawing in to the tip.
		tt := max(t, 0)
		half := w * math.Pow(1-tt, 0.85) * math.Min(1, (t+0.12)*4)
		half *= 1 - 0.05*math.Abs(math.Sin(t*30))
		return math.Abs(v) - half
	}
	lumAt := func(x, y, d float64) float64 {
		u, v := local(x, y)
		l0 := tone
		if math.Abs(v) < 0.5 && u > 0 {
			l0 *= 0.74
		}
		if math.Abs(math.Sin((u-math.Abs(v)*0.6)*8/l*math.Pi)) < 0.09 && math.Abs(v) > 0.8 {
			l0 *= 0.89
		}
		return l0 * (0.85 + 0.15*min(-d/2.5, 1))
	}
	rr := l * 1.2
	c.shape(bx-rr, by-rr, bx+rr, by+rr, sd, lumAt)
}

// pinna is a fern pinna from the rachis at (bx, by) along ang: a lance l
// long and w half-wide with a toothed edge, an ear (a small lobe) on the side
// toward the frond's tip at its base, and a midvein. side is which side of
// the rachis it is on, so the ear points up the frond.
func (c *atlasCanvas) pinna(bx, by, ang, l, w, ear, side, tone float64, r *rand.Rand) {
	ca, sa := math.Cos(ang), math.Sin(ang)
	local := func(x, y float64) (float64, float64) {
		dx, dy := x-bx, y-by
		return dx*ca + dy*sa, (-dx*sa + dy*ca) * side
	}
	teeth := 12 + r.Float64()*4
	sd := func(x, y float64) float64 {
		u, v := local(x, y)
		t := u / l
		if t < 0 {
			return math.Hypot(u, v) - w*0.4
		}
		if t > 1 {
			return math.Hypot(u-l, v)
		}
		half := w * math.Pow(min(t*5, 1), 0.5) * math.Pow(1-t, 0.7)
		half *= 1 - 0.1*math.Abs(math.Sin(t*teeth*math.Pi))
		d := math.Abs(v) - half
		// The ear: a bump on the up-frond side, a fifth of the way out.
		if v > 0 && ear > 0 {
			eu, ev := u-l*0.08, v-w*0.95
			d = min(d, math.Hypot(eu*0.9, ev*1.3)-w*ear*0.9)
		}
		return d
	}
	lumAt := func(x, y, d float64) float64 {
		u, v := local(x, y)
		l0 := tone
		if math.Abs(v) < 0.4 && u > 0 {
			l0 *= 0.76
		}
		return l0 * (0.84 + 0.16*min(-d/1.5, 1))
	}
	rr := l + w*2
	c.shape(bx-rr, by-rr, bx+rr, by+rr, sd, lumAt)
}

// obcordate is one heart-shaped leaflet with its notch outward, from (cx,
// cy) along ang, l long and w wide, creased down its middle with a pale
// blotch either side of the crease.
func (c *atlasCanvas) obcordate(cx, cy, ang, l, w float64) {
	ca, sa := math.Cos(ang), math.Sin(ang)
	local := func(x, y float64) (float64, float64) {
		dx, dy := x-cx, y-cy
		return dx*ca + dy*sa, -dx*sa + dy*ca
	}
	sd := func(x, y float64) float64 {
		u, v := local(x, y)
		// Two round lobes at the outer end, overlapping but for the notch
		// between them, and a wedge in to the centre.
		lobe := math.Hypot(u-l*0.66, math.Abs(v)-w*0.3) - w*0.46
		var wedge float64
		if u < 0 {
			wedge = math.Hypot(u, v)
		} else {
			wedge = math.Abs(v) - u/l*w*1.25
			wedge = max(wedge, u-l*0.72)
		}
		return min(lobe, wedge)
	}
	lumAt := func(x, y, d float64) float64 {
		u, v := local(x, y)
		l0 := 0.9
		if math.Abs(v) < 0.6 && u < l*0.72 {
			l0 = 0.62 // the crease
		} else if math.Hypot((u-l*0.45)/(l*0.28), v/(w*0.32)) < 1 {
			l0 = 1.06 // the pale blotch
		}
		return l0 * (0.85 + 0.15*min(-d/2, 1))
	}
	rr := l * 1.15
	c.shape(cx-rr, cy-rr, cx+rr, cy+rr, sd, lumAt)
}
