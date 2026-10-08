// Package biomeground paints the ground textures the built-in biomes lay over
// the terrain they are painted on: ground_<biome>, 512x512 and seamless.
//
// They are engine assets in the way the built-in biomes are engine data: a
// project that never ran cmd/genbiomeground still names ground_meadow from
// the Go table, so the renderer paints it here when the project has no file
// of that name (render/terrain_ground.go). A file in the project always wins
// — this is only what a project gets without one.
//
// Everything here tiles: noise is periodic over the image, cellular patterns
// wrap their cells, and strokes and blobs drawn across an edge come back in
// on the other side. A new ground is a new recipe in `recipes`.
package biomeground

import (
	"image"
	"math"
	"math/rand"
	"sort"
	"strings"
)

// Prefix is what every ground texture's name starts with: ground_<biome>.
const Prefix = "ground_"

// Size is the edge of every ground texture.
const Size = 512

const size = Size

// canvas is linear-ish RGB, 0..1.
type canvas struct{ px []float64 }

func newCanvas(c [3]float64) *canvas {
	cv := &canvas{px: make([]float64, size*size*3)}
	for i := 0; i < size*size; i++ {
		copy(cv.px[i*3:], c[:])
	}
	return cv
}

func wrap(v int) int { return ((v % size) + size) % size }

func (c *canvas) at(x, y int) [3]float64 {
	i := (wrap(y)*size + wrap(x)) * 3
	return [3]float64{c.px[i], c.px[i+1], c.px[i+2]}
}

// blend lays colour col over pixel (x, y) with coverage a.
func (c *canvas) blend(x, y int, col [3]float64, a float64) {
	if a <= 0 {
		return
	}
	a = math.Min(a, 1)
	i := (wrap(y)*size + wrap(x)) * 3
	for k := 0; k < 3; k++ {
		c.px[i+k] = c.px[i+k]*(1-a) + col[k]*a
	}
}

// mul scales pixel (x, y) by f.
func (c *canvas) mul(x, y int, f float64) {
	i := (wrap(y)*size + wrap(x)) * 3
	for k := 0; k < 3; k++ {
		c.px[i+k] *= f
	}
}

// each runs fn over every pixel, with (u, v) in [0, 1).
func (c *canvas) each(fn func(x, y int, u, v float64, p [3]float64) [3]float64) {
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			i := (y*size + x) * 3
			out := fn(x, y, float64(x)/size, float64(y)/size, [3]float64{c.px[i], c.px[i+1], c.px[i+2]})
			copy(c.px[i:], out[:])
		}
	}
}

// ---------- tiling noise ----------

type noise struct {
	grid   []float64
	period int
}

func newNoise(r *rand.Rand, period int) *noise {
	n := &noise{grid: make([]float64, period*period), period: period}
	for i := range n.grid {
		n.grid[i] = r.Float64()
	}
	return n
}

// at is value noise over (u, v) in image units (0..1 across), period cells
// across the image, so it tiles.
func (n *noise) at(u, v float64) float64 {
	p := n.period
	x, y := u*float64(p), v*float64(p)
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	fx, fy := x-float64(x0), y-float64(y0)
	fx, fy = fx*fx*(3-2*fx), fy*fy*(3-2*fy)
	g := func(i, j int) float64 { return n.grid[((j%p+p)%p)*p+((i%p+p)%p)] }
	a := g(x0, y0)*(1-fx) + g(x0+1, y0)*fx
	b := g(x0, y0+1)*(1-fx) + g(x0+1, y0+1)*fx
	return a*(1-fy) + b*fy
}

// fbm is octaves of tiling noise from base cells across, each doubling.
type fbm []*noise

func newFBM(r *rand.Rand, base, octaves int) fbm {
	var f fbm
	for o := 0; o < octaves; o++ {
		f = append(f, newNoise(r, base<<o))
	}
	return f
}

func (f fbm) at(u, v float64) float64 {
	s, amp, tot := 0.0, 1.0, 0.0
	for _, n := range f {
		s += n.at(u, v) * amp
		tot += amp
		amp *= 0.5
	}
	return s / tot
}

// ---------- cellular ----------

// cells is a jittered point per grid cell, cells across, wrapping.
type cells struct {
	n  int
	pt [][2]float64
	id []float64
}

func newCells(r *rand.Rand, n int) *cells {
	c := &cells{n: n}
	for i := 0; i < n*n; i++ {
		c.pt = append(c.pt, [2]float64{r.Float64(), r.Float64()})
		c.id = append(c.id, r.Float64())
	}
	return c
}

// at is the distances to the nearest two points (in cell units) and the
// nearest one's random id.
func (c *cells) at(u, v float64) (d1, d2, id float64) {
	x, y := u*float64(c.n), v*float64(c.n)
	cx, cy := int(math.Floor(x)), int(math.Floor(y))
	d1, d2 = 9, 9
	for j := -1; j <= 1; j++ {
		for i := -1; i <= 1; i++ {
			gx, gy := cx+i, cy+j
			k := ((gy%c.n+c.n)%c.n)*c.n + (gx%c.n+c.n)%c.n
			px, py := float64(gx)+c.pt[k][0], float64(gy)+c.pt[k][1]
			d := math.Hypot(px-x, py-y)
			if d < d1 {
				d2, d1, id = d1, d, c.id[k]
			} else if d < d2 {
				d2 = d
			}
		}
	}
	return
}

// ---------- marks ----------

// stroke draws a tapered line from (x0,y0) along angle ang, length l and
// width w pixels, wrapping at the edges.
func (c *canvas) stroke(x0, y0, ang, l, w float64, col [3]float64, alpha float64) {
	dx, dy := math.Cos(ang), math.Sin(ang)
	steps := int(l*2) + 1
	for s := 0; s <= steps; s++ {
		t := float64(s) / float64(steps)
		px, py := x0+dx*l*t, y0+dy*l*t
		hw := w * (1 - 0.6*t) / 2
		r := int(math.Ceil(hw + 1))
		for j := -r; j <= r; j++ {
			for i := -r; i <= r; i++ {
				d := math.Hypot(float64(i), float64(j))
				a := math.Min(math.Max(hw+0.5-d, 0), 1) * alpha
				c.blend(int(px)+i, int(py)+j, col, a)
			}
		}
	}
}

// blob draws a soft-edged ellipse with a lit top edge, like a pebble or a leaf.
func (c *canvas) blob(cx, cy, rx, ry, ang float64, col [3]float64, shade float64) {
	r := int(math.Max(rx, ry)) + 2
	ca, sa := math.Cos(ang), math.Sin(ang)
	for j := -r; j <= r; j++ {
		for i := -r; i <= r; i++ {
			x, y := float64(i), float64(j)
			u := (x*ca + y*sa) / rx
			v := (-x*sa + y*ca) / ry
			d := math.Hypot(u, v)
			if d > 1.15 {
				continue
			}
			a := math.Min(math.Max((1-d)*math.Min(rx, ry), 0), 1)
			// Lit from the top-left, shaded toward the bottom-right edge.
			lit := 1 + shade*(-(x+y)/(float64(r)*1.4))*math.Min(d*1.5, 1)
			cc := [3]float64{col[0] * lit, col[1] * lit, col[2] * lit}
			c.blend(int(cx)+i, int(cy)+j, cc, a)
		}
	}
}

// leaf draws a pointed leaf with a midrib.
func (c *canvas) leaf(cx, cy, l, w, ang float64, col [3]float64) {
	ca, sa := math.Cos(ang), math.Sin(ang)
	r := int(l) + 2
	for j := -r; j <= r; j++ {
		for i := -r; i <= r; i++ {
			x, y := float64(i), float64(j)
			u := x*ca + y*sa
			v := -x*sa + y*ca
			t := u/l + 0.5
			if t < 0 || t > 1 {
				continue
			}
			half := w * math.Pow(math.Sin(math.Pi*t), 0.8) / 2
			d := math.Abs(v) - half
			a := math.Min(math.Max(0.5-d, 0), 1)
			if a <= 0 {
				continue
			}
			cc := col
			if math.Abs(v) < 0.7 {
				cc = [3]float64{col[0] * 0.75, col[1] * 0.75, col[2] * 0.75}
			}
			edge := 0.85 + 0.15*math.Min(-d/2, 1)
			cc = [3]float64{cc[0] * edge, cc[1] * edge, cc[2] * edge}
			c.blend(int(cx)+i, int(cy)+j, cc, a)
		}
	}
}

// ---------- helpers ----------

func mix(a, b [3]float64, t float64) [3]float64 {
	t = math.Min(math.Max(t, 0), 1)
	return [3]float64{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t, a[2] + (b[2]-a[2])*t}
}

func scale(a [3]float64, f float64) [3]float64 { return [3]float64{a[0] * f, a[1] * f, a[2] * f} }

func smooth(e0, e1, x float64) float64 {
	t := math.Min(math.Max((x-e0)/(e1-e0), 0), 1)
	return t * t * (3 - 2*t)
}

func rgb(r, g, b float64) [3]float64 { return [3]float64{r, g, b} }

func jitter(r *rand.Rand, c [3]float64, amt float64) [3]float64 {
	f := 1 + (r.Float64()-0.5)*2*amt
	return scale(c, f)
}

// grain adds per-pixel noise of amplitude amt and fine tiling mottling.
func grain(c *canvas, r *rand.Rand, amt float64) {
	fine := newFBM(r, 64, 2)
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		f := 1 + (r.Float64()-0.5)*amt + (fine.at(u, v)-0.5)*amt
		return scale(p, f)
	})
}

// ---------- the recipes ----------

var recipes = map[string]func(r *rand.Rand) *canvas{
	"meadow":      meadow,
	"forest":      forest,
	"pine":        pine,
	"wetland":     wetland,
	"scrub":       scrub,
	"cliff":       cliff,
	"scree":       scree,
	"jungle":      jungle,
	"desert":      desert,
	"beach":       beach,
	"tundra":      tundra,
	"badlands":    badlands,
	"volcanic":    volcanic,
	"savannah":    savannah,
	"mesa":        mesa,
	"glacier":     glacier,
	"ice_plains":  icePlains,
	"rocky_cliff": rockyCliff,
	"rainforest":  rainforest,
}

// soil is a mottled base between two colours.
func soil(r *rand.Rand, a, b [3]float64, cellsAcross int) *canvas {
	c := newCanvas(a)
	f := newFBM(r, cellsAcross, 4)
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		return mix(a, b, smooth(0.3, 0.7, f.at(u, v)))
	})
	return c
}

// turf strokes n short blades across c.
func turf(c *canvas, r *rand.Rand, n int, lo, hi [3]float64, length, width float64) {
	for i := 0; i < n; i++ {
		col := mix(lo, hi, r.Float64())
		ang := -math.Pi/2 + (r.Float64()-0.5)*1.6
		c.stroke(r.Float64()*size, r.Float64()*size, ang, length*(0.6+0.8*r.Float64()), width, col, 0.9)
	}
}

func pebbles(c *canvas, r *rand.Rand, n int, lo, hi [3]float64, rmin, rmax float64) {
	for i := 0; i < n; i++ {
		rad := rmin + (rmax-rmin)*math.Pow(r.Float64(), 2)
		x, y := r.Float64()*size, r.Float64()*size
		// Contact shadow first, offset down-right.
		c.blob(x+rad*0.3, y+rad*0.3, rad*1.1, rad*0.85, 0, scale(c.at(int(x), int(y)), 0.55), 0)
		c.blob(x, y, rad, rad*(0.6+0.35*r.Float64()), r.Float64()*math.Pi, mix(lo, hi, r.Float64()), 0.35)
	}
}

func leaves(c *canvas, r *rand.Rand, n int, palette [][3]float64, lmin, lmax float64) {
	for i := 0; i < n; i++ {
		l := lmin + (lmax-lmin)*r.Float64()
		col := jitter(r, palette[r.Intn(len(palette))], 0.15)
		c.leaf(r.Float64()*size, r.Float64()*size, l, l*(0.35+0.2*r.Float64()), r.Float64()*2*math.Pi, col)
	}
}

func meadow(r *rand.Rand) *canvas {
	c := soil(r, rgb(0.24, 0.30, 0.12), rgb(0.33, 0.40, 0.16), 6)
	speck := newFBM(r, 24, 3)
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		// Bare soil showing through in patches.
		return mix(p, rgb(0.30, 0.24, 0.15), smooth(0.62, 0.75, speck.at(u, v))*0.7)
	})
	turf(c, r, 26000, rgb(0.20, 0.30, 0.09), rgb(0.44, 0.54, 0.20), 7, 1.6)
	grain(c, r, 0.12)
	return c
}

func forest(r *rand.Rand) *canvas {
	c := soil(r, rgb(0.20, 0.14, 0.08), rgb(0.30, 0.21, 0.12), 8)
	leaves(c, r, 5200, [][3]float64{rgb(0.55, 0.30, 0.10), rgb(0.42, 0.26, 0.10), rgb(0.62, 0.42, 0.16), rgb(0.35, 0.22, 0.10), rgb(0.48, 0.38, 0.16)}, 7, 16)
	for i := 0; i < 90; i++ {
		c.stroke(r.Float64()*size, r.Float64()*size, r.Float64()*2*math.Pi, 14+r.Float64()*30, 2.2, rgb(0.24, 0.17, 0.10), 1)
	}
	grain(c, r, 0.14)
	return c
}

func pine(r *rand.Rand) *canvas {
	c := soil(r, rgb(0.30, 0.19, 0.10), rgb(0.40, 0.27, 0.14), 8)
	for i := 0; i < 40000; i++ {
		col := mix(rgb(0.36, 0.20, 0.09), rgb(0.62, 0.42, 0.22), r.Float64())
		c.stroke(r.Float64()*size, r.Float64()*size, r.Float64()*2*math.Pi, 6+r.Float64()*6, 1.1, col, 0.9)
	}
	// A few cones.
	for i := 0; i < 14; i++ {
		c.blob(r.Float64()*size, r.Float64()*size, 7, 4, r.Float64()*math.Pi, rgb(0.34, 0.22, 0.12), 0.5)
	}
	grain(c, r, 0.12)
	return c
}

func wetland(r *rand.Rand) *canvas {
	c := soil(r, rgb(0.14, 0.13, 0.08), rgb(0.24, 0.22, 0.12), 6)
	algae := newFBM(r, 8, 4)
	wet := newFBM(r, 5, 3)
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		p = mix(p, rgb(0.20, 0.30, 0.10), smooth(0.5, 0.7, algae.at(u, v))*0.8)
		// Standing water: darker, smoother.
		return mix(p, rgb(0.10, 0.11, 0.08), smooth(0.58, 0.66, wet.at(u, v))*0.7)
	})
	turf(c, r, 7000, rgb(0.18, 0.28, 0.09), rgb(0.34, 0.44, 0.14), 8, 1.4)
	grain(c, r, 0.1)
	return c
}

func scrub(r *rand.Rand) *canvas {
	c := soil(r, rgb(0.50, 0.40, 0.27), rgb(0.62, 0.52, 0.36), 6)
	pebbles(c, r, 700, rgb(0.48, 0.42, 0.34), rgb(0.70, 0.62, 0.50), 1.5, 5)
	turf(c, r, 3500, rgb(0.50, 0.44, 0.24), rgb(0.70, 0.62, 0.36), 6, 1.2)
	grain(c, r, 0.14)
	return c
}

func cliff(r *rand.Rand) *canvas {
	c := soil(r, rgb(0.38, 0.37, 0.34), rgb(0.52, 0.51, 0.47), 5)
	cl := newCells(r, 7)
	moss := newFBM(r, 6, 5)
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		d1, d2, id := cl.at(u, v)
		p = scale(p, 0.9+0.2*id)
		// Cracks between slabs.
		p = scale(p, 0.55+0.45*smooth(0.0, 0.08, d2-d1))
		return mix(p, mix(rgb(0.20, 0.32, 0.10), rgb(0.34, 0.44, 0.14), moss.at(u*3, v*3)), smooth(0.48, 0.62, moss.at(u, v)))
	})
	grain(c, r, 0.14)
	return c
}

func scree(r *rand.Rand) *canvas {
	c := soil(r, rgb(0.34, 0.33, 0.31), rgb(0.44, 0.43, 0.40), 8)
	pebbles(c, r, 2600, rgb(0.36, 0.35, 0.33), rgb(0.66, 0.64, 0.60), 2, 11)
	grain(c, r, 0.12)
	return c
}

func jungle(r *rand.Rand) *canvas {
	c := soil(r, rgb(0.13, 0.10, 0.06), rgb(0.20, 0.16, 0.09), 8)
	leaves(c, r, 3600, [][3]float64{rgb(0.30, 0.22, 0.10), rgb(0.24, 0.26, 0.10), rgb(0.40, 0.28, 0.12), rgb(0.18, 0.22, 0.08), rgb(0.34, 0.30, 0.12)}, 12, 30)
	moss := newFBM(r, 10, 4)
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		return mix(p, rgb(0.16, 0.26, 0.08), smooth(0.58, 0.72, moss.at(u, v))*0.8)
	})
	grain(c, r, 0.14)
	return c
}

// rainforest is a Hoh valley floor: dark, wet humus under a near-continuous
// carpet of moss, broken by needles, maple leaves and twigs.
func rainforest(r *rand.Rand) *canvas {
	c := soil(r, rgb(0.12, 0.09, 0.05), rgb(0.19, 0.14, 0.08), 8)
	// Needles and fallen leaves on the humus.
	for i := 0; i < 9000; i++ {
		col := mix(rgb(0.30, 0.18, 0.09), rgb(0.48, 0.30, 0.14), r.Float64())
		c.stroke(r.Float64()*size, r.Float64()*size, r.Float64()*2*math.Pi, 4+5*r.Float64(), 0.7, col, 0.85)
	}
	leaves(c, r, 900, [][3]float64{rgb(0.50, 0.36, 0.14), rgb(0.40, 0.28, 0.12), rgb(0.56, 0.44, 0.18)}, 12, 26)
	for i := 0; i < 70; i++ {
		col := mix(rgb(0.22, 0.16, 0.10), rgb(0.36, 0.28, 0.18), r.Float64())
		c.stroke(r.Float64()*size, r.Float64()*size, r.Float64()*2*math.Pi, 20+40*r.Float64(), 1.6+1.4*r.Float64(), col, 1)
	}
	// The moss: deep green cushions over most of it, brighter where it is
	// thickest, the litter showing through only in the gaps.
	moss := newFBM(r, 7, 5)
	tuft := newFBM(r, 48, 3)
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		m := smooth(0.38, 0.55, moss.at(u, v))
		t := tuft.at(u, v)
		green := mix(rgb(0.12, 0.22, 0.05), rgb(0.30, 0.42, 0.10), smooth(0.3, 0.8, t))
		return mix(p, green, m*0.92)
	})
	turf(c, r, 14000, rgb(0.14, 0.24, 0.06), rgb(0.34, 0.46, 0.12), 4, 1.2)
	grain(c, r, 0.16)
	return c
}

func desert(r *rand.Rand) *canvas {
	c := newCanvas(rgb(0, 0, 0))
	warp := newFBM(r, 4, 3)
	tone := newFBM(r, 5, 4)
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		// Wind ripples: a whole number of waves across the tile so it
		// wraps, bent by noise.
		ph := (v + 0.06*(warp.at(u, v)-0.5)) * 2 * math.Pi * 22
		rip := math.Pow(0.5+0.5*math.Sin(ph+2*math.Sin(u*2*math.Pi*2)), 2)
		base := mix(rgb(0.74, 0.58, 0.38), rgb(0.86, 0.70, 0.48), tone.at(u, v))
		return scale(base, 0.9+0.16*rip)
	})
	pebbles(c, r, 120, rgb(0.55, 0.42, 0.30), rgb(0.72, 0.56, 0.40), 1.2, 3)
	grain(c, r, 0.1)
	return c
}

func beach(r *rand.Rand) *canvas {
	c := soil(r, rgb(0.80, 0.72, 0.56), rgb(0.90, 0.83, 0.67), 6)
	wet := newFBM(r, 4, 3)
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		return mix(p, rgb(0.66, 0.58, 0.44), smooth(0.6, 0.75, wet.at(u, v))*0.6)
	})
	grain(c, r, 0.18)
	// Shell fragments and the odd dark grain.
	pebbles(c, r, 260, rgb(0.86, 0.82, 0.76), rgb(0.98, 0.94, 0.88), 1.2, 3.2)
	pebbles(c, r, 400, rgb(0.40, 0.36, 0.30), rgb(0.55, 0.50, 0.42), 0.8, 1.6)
	return c
}

func tundra(r *rand.Rand) *canvas {
	c := soil(r, rgb(0.34, 0.30, 0.22), rgb(0.44, 0.40, 0.30), 6)
	lichen := newFBM(r, 12, 4)
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		return mix(p, rgb(0.62, 0.64, 0.54), smooth(0.56, 0.66, lichen.at(u, v))*0.85)
	})
	turf(c, r, 6000, rgb(0.40, 0.38, 0.22), rgb(0.58, 0.54, 0.34), 5, 1.2)
	pebbles(c, r, 260, rgb(0.40, 0.40, 0.38), rgb(0.58, 0.58, 0.54), 2, 6)
	grain(c, r, 0.12)
	return c
}

func badlands(r *rand.Rand) *canvas {
	c := soil(r, rgb(0.56, 0.30, 0.18), rgb(0.70, 0.42, 0.26), 5)
	cl := newCells(r, 11)
	band := newFBM(r, 3, 3)
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		d1, d2, id := cl.at(u, v)
		// Sediment bands, then the crust cracked into plates.
		p = mix(p, rgb(0.76, 0.56, 0.38), smooth(0.55, 0.7, band.at(u, v))*0.5)
		p = scale(p, 0.92+0.14*id)
		return scale(p, 0.5+0.5*smooth(0.0, 0.06, d2-d1))
	})
	pebbles(c, r, 300, rgb(0.46, 0.24, 0.14), rgb(0.66, 0.38, 0.24), 1.5, 4)
	grain(c, r, 0.12)
	return c
}

func volcanic(r *rand.Rand) *canvas {
	c := soil(r, rgb(0.09, 0.09, 0.09), rgb(0.16, 0.15, 0.14), 8)
	cl := newCells(r, 40)
	rust := newFBM(r, 6, 3)
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		d1, d2, id := cl.at(u, v)
		// Cinders: rounded grains with dark gaps between.
		p = scale(p, 0.7+0.7*id)
		p = scale(p, 0.4+0.6*smooth(0.0, 0.18, d2-d1))
		return mix(p, rgb(0.24, 0.12, 0.08), smooth(0.64, 0.76, rust.at(u, v))*0.6)
	})
	pebbles(c, r, 500, rgb(0.10, 0.10, 0.10), rgb(0.24, 0.23, 0.22), 2, 7)
	grain(c, r, 0.16)
	return c
}

func savannah(r *rand.Rand) *canvas {
	c := soil(r, rgb(0.52, 0.40, 0.24), rgb(0.64, 0.50, 0.30), 6)
	turf(c, r, 16000, rgb(0.56, 0.46, 0.24), rgb(0.80, 0.68, 0.40), 8, 1.4)
	pebbles(c, r, 160, rgb(0.46, 0.38, 0.28), rgb(0.62, 0.52, 0.38), 1.5, 4)
	grain(c, r, 0.12)
	return c
}

// mesa is sediment in bands — cream, orange, rust, brown — so a cliff face
// (walls: true) reads as strata. The bands wrap: their edges are a sorted set
// of positions in [0, 1).
func mesa(r *rand.Rand) *canvas {
	palette := [][3]float64{rgb(0.86, 0.66, 0.46), rgb(0.78, 0.46, 0.26), rgb(0.66, 0.32, 0.18), rgb(0.72, 0.52, 0.36), rgb(0.58, 0.36, 0.24), rgb(0.88, 0.74, 0.56)}
	n := 18
	var edges []float64
	for i := 0; i < n; i++ {
		// Even-ish, with thick and thin bands, never a long plain stretch.
		edges = append(edges, (float64(i)+0.15+0.7*r.Float64())/float64(n))
	}
	sort.Float64s(edges)
	cols := make([][3]float64, n)
	for i := range cols {
		cols[i] = jitter(r, palette[r.Intn(len(palette))], 0.08)
	}
	warp := newFBM(r, 4, 3)
	tone := newFBM(r, 16, 3)
	c := newCanvas(rgb(0, 0, 0))
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		w := v + 0.035*(warp.at(u, v)-0.5)
		w -= math.Floor(w)
		band := n - 1
		for i, e := range edges {
			if w < e {
				band = (i + n - 1) % n
				break
			}
		}
		return scale(cols[band], 0.86+0.26*tone.at(u, v))
	})
	cl := newCells(r, 9)
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		d1, d2, _ := cl.at(u, v)
		return scale(p, 0.84+0.16*smooth(0.0, 0.05, d2-d1))
	})
	grain(c, r, 0.12)
	return c
}

func glacier(r *rand.Rand) *canvas {
	flow := newFBM(r, 3, 4)
	snow := newFBM(r, 6, 4)
	crev := newFBM(r, 4, 3)
	c := newCanvas(rgb(0, 0, 0))
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		// Ice streaked along its flow: noise sampled stretched across u.
		f := flow.at(u, v*0.25+0.5*u)
		col := mix(rgb(0.52, 0.72, 0.84), rgb(0.78, 0.90, 0.96), f)
		// Crevasses: thin dark-blue lines where a warped wave crosses zero.
		ph := (u + 0.12*(crev.at(u, v)-0.5)) * 2 * math.Pi * 7
		line := 1 - smooth(0.0, 0.06, math.Abs(math.Sin(ph)))
		col = mix(col, rgb(0.14, 0.30, 0.44), line*smooth(0.45, 0.6, crev.at(v, u))*0.9)
		// Old snow lying in patches.
		return mix(col, rgb(0.92, 0.95, 0.97), smooth(0.55, 0.68, snow.at(u, v)))
	})
	pebbles(c, r, 120, rgb(0.30, 0.30, 0.30), rgb(0.45, 0.44, 0.42), 0.8, 2)
	grain(c, r, 0.06)
	return c
}

func icePlains(r *rand.Rand) *canvas {
	drift := newFBM(r, 5, 4)
	glaze := newFBM(r, 4, 4)
	c := newCanvas(rgb(0, 0, 0))
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		// Sastrugi: wind-cut ridges, a whole number of waves across so it
		// tiles, bent by the drift.
		ph := (v + 0.08*(drift.at(u, v)-0.5)) * 2 * math.Pi * 14
		rid := math.Pow(0.5+0.5*math.Sin(ph+1.5*math.Sin(u*2*math.Pi*3)), 3)
		col := scale(mix(rgb(0.84, 0.88, 0.94), rgb(0.95, 0.97, 0.99), drift.at(u, v)), 0.93+0.09*rid)
		// Blue glaze where the snow has blown off the ice.
		return mix(col, rgb(0.62, 0.78, 0.88), smooth(0.62, 0.72, glaze.at(u, v))*0.75)
	})
	pebbles(c, r, 60, rgb(0.40, 0.40, 0.42), rgb(0.56, 0.56, 0.58), 1, 2.5)
	grain(c, r, 0.05)
	return c
}

func rockyCliff(r *rand.Rand) *canvas {
	c := soil(r, rgb(0.40, 0.39, 0.37), rgb(0.60, 0.59, 0.56), 4)
	big := newFBM(r, 3, 4)
	cl := newCells(r, 9)
	fine := newCells(r, 23)
	lichen := newFBM(r, 14, 4)
	c.each(func(x, y int, u, v float64, p [3]float64) [3]float64 {
		// Broad weathering first, so the rock is not an even grey.
		p = scale(p, 0.8+0.4*big.at(u, v))
		// Joints: a few long fractures and finer ones, both faint — rock,
		// not paving.
		d1, d2, id := cl.at(u, v)
		p = scale(p, 0.94+0.12*id)
		p = scale(p, 0.72+0.28*smooth(0.0, 0.035, d2-d1))
		f1, f2, _ := fine.at(u, v)
		p = scale(p, 0.9+0.1*smooth(0.0, 0.05, f2-f1))
		// Mineral grains.
		g := r.Float64()
		if g < 0.05 {
			p = scale(p, 0.65)
		} else if g > 0.96 {
			p = scale(p, 1.3)
		}
		l := lichen.at(u, v)
		p = mix(p, rgb(0.62, 0.64, 0.50), smooth(0.64, 0.7, l)*0.7)
		return mix(p, rgb(0.78, 0.56, 0.24), smooth(0.77, 0.8, lichen.at(v, u))*0.6)
	})
	grain(c, r, 0.1)
	return c
}

// Names are the biomes there is a recipe for, sorted.
func Names() []string {
	out := make([]string, 0, len(recipes))
	for n := range recipes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Has reports whether texture (a texture name, ground_<biome>) is one this
// package can paint.
func Has(texture string) bool {
	_, ok := recipe(texture)
	return ok
}

func recipe(texture string) (func(r *rand.Rand) *canvas, bool) {
	name, ok := strings.CutPrefix(texture, Prefix)
	if !ok {
		return nil, false
	}
	fn, ok := recipes[name]
	return fn, ok
}

// Image paints texture (ground_<biome>), or returns nil if there is no recipe
// for it. Seeded by name, so it is the same image every time and repainting
// one biome leaves the rest alone. Pure CPU, safe off the main thread; a
// fraction of a second each.
func Image(texture string) *image.RGBA {
	fn, ok := recipe(texture)
	if !ok {
		return nil
	}
	name := strings.TrimPrefix(texture, Prefix)
	seed := int64(0)
	for _, ch := range name {
		seed = seed*131 + int64(ch)
	}
	c := fn(rand.New(rand.NewSource(seed)))
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	q := func(v float64) uint8 { return uint8(math.Min(math.Max(v, 0), 1)*255 + 0.5) }
	for i := 0; i < size*size; i++ {
		img.Pix[i*4+0] = q(c.px[i*3+0])
		img.Pix[i*4+1] = q(c.px[i*3+1])
		img.Pix[i*4+2] = q(c.px[i*3+2])
		img.Pix[i*4+3] = 255
	}
	return img
}
