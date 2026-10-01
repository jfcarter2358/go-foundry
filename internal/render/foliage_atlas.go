package render

// foliage_atlas.go — the texture foliage cards are cut from.
//
// Leaves, needles, fern fronds and petals are too fine to model and too many
// to draw as geometry, so they are cards: quads showing a cut-out image. The
// images are drawn here, in code, into one atlas of foliageAtlasCols x
// foliageAtlasRows tiles — a spray of broad leaves on a twig, one leaf, a
// needle spray, a fern frond, a ring of petals, fallen needles, ivy, a seed
// head — so the engine ships no foliage art to go missing, and every tint
// comes from the instance: the atlas holds shape (alpha) and grey detail
// (veins, edges), never colour.
//
// # Mipmaps that keep their leaves
//
// A card is cut out where its alpha passes one half. Box-filtered mips blur
// alpha toward grey, the fraction above one half shrinks level by level, and
// a canopy thins to a skeleton with distance. Each mip's alpha is therefore
// rescaled until the fraction of texels that pass matches the full-size
// image's — alpha-coverage preservation — so a tree looks as dense at 200
// units as at 20.

import (
	"image"
	"image/color"
	"math"
	"math/rand"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

const (
	foliageTileSize  = 128
	foliageAtlasCols = 4
	foliageAtlasRows = 4
)

// Atlas tiles.
const (
	tileCluster = iota // broad leaves on a twig
	tileLeaf           // one leaf
	tileNeedles        // a conifer's needle spray
	tileFern           // a fern frond
	tilePetals         // a ring of petals round a dark centre
	tileLitter         // fallen needles and bits
	tileIvy            // a few ivy leaves
	tileSeed           // a seed head / flower spike
	tileMoss           // a fuzzy, irregular patch of moss
	tilePalm           // a palm frond: a rib combed with long, narrow leaflets
	// The far tier's clump cards (foliage_mesh.go buildClump): a whole tuft
	// on one card, for the distance where a blade is under a pixel.
	tileGrassClump     // a tuft of short blades
	tileTallGrassClump // taller, laxer blades with seed heads
	tileFlowerDots     // flower heads scattered over a tuft's height
	tileReedClump      // upright straps with dark catkins
)

// tileUV maps (u, v) in [0,1]^2 across tile i to atlas coordinates, inset
// half a texel so a card's edge never samples its neighbour.
func tileUV(i int, u, v float32) mgl32.Vec2 {
	const inset = 1.5 / foliageTileSize
	u = inset + u*(1-2*inset)
	v = inset + v*(1-2*inset)
	cx, cy := float32(i%foliageAtlasCols), float32(i/foliageAtlasCols)
	return mgl32.Vec2{(cx + u) / foliageAtlasCols, (cy + v) / foliageAtlasRows}
}

// atlasCanvas is a float RGBA canvas for one tile; y = 0 is the tile's
// bottom (v = 0), where a card's base is.
type atlasCanvas struct {
	lum, alpha []float32
	n          int
}

func newAtlasCanvas() *atlasCanvas {
	n := foliageTileSize
	return &atlasCanvas{lum: make([]float32, n*n), alpha: make([]float32, n*n), n: n}
}

// shape paints every texel whose centre lies inside the shape given by the
// signed distance function sd (negative inside, in texels), with an
// antialiased edge, and luminance from lumAt.
func (c *atlasCanvas) shape(x0, y0, x1, y1 float64, sd func(x, y float64) float64, lumAt func(x, y, d float64) float64) {
	for y := max(int(y0)-1, 0); y <= min(int(y1)+1, c.n-1); y++ {
		for x := max(int(x0)-1, 0); x <= min(int(x1)+1, c.n-1); x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			d := sd(px, py)
			a := math.Min(math.Max(0.5-d, 0), 1)
			if a <= 0 {
				continue
			}
			i := y*c.n + x
			l := lumAt(px, py, d)
			// Over-composite, so later shapes lie on top.
			c.lum[i] = float32(l)*float32(a) + c.lum[i]*(1-float32(a))
			c.alpha[i] = float32(a) + c.alpha[i]*(1-float32(a))
		}
	}
}

// leaf draws a pointed leaf from base (bx, by) along angle ang, length l and
// half-width w (texels), with a darker midrib and veins.
func (c *atlasCanvas) leaf(bx, by, ang, l, w, tone float64) {
	ca, sa := math.Cos(ang), math.Sin(ang)
	sd := func(x, y float64) float64 {
		dx, dy := x-bx, y-by
		u := dx*ca + dy*sa  // along the midrib
		v := -dx*sa + dy*ca // across
		t := u / l
		if t < 0 {
			return math.Hypot(u, v)
		}
		if t > 1 {
			return math.Hypot(u-l, v)
		}
		half := w * math.Pow(math.Sin(math.Pi*math.Min(t*1.1, 1)), 0.75)
		return math.Abs(v) - half
	}
	lumAt := func(x, y, d float64) float64 {
		dx, dy := x-bx, y-by
		u := dx*ca + dy*sa
		v := -dx*sa + dy*ca
		l0 := tone
		if math.Abs(v) < 0.9 {
			l0 *= 0.72 // midrib
		}
		// Side veins angled toward the tip.
		vein := math.Abs(math.Sin((u-math.Abs(v)*0.9)*0.55)) < 0.12
		if vein && math.Abs(v) > 1.5 {
			l0 *= 0.86
		}
		// Darker toward the edge, lighter in the middle.
		l0 *= 0.85 + 0.15*math.Min(-d/3, 1)
		return l0
	}
	r := l + w
	c.shape(bx-r, by-r, bx+r, by+r, sd, lumAt)
}

// line draws a round-capped segment of half-width w.
func (c *atlasCanvas) line(x0, y0, x1, y1, w, tone float64) {
	dx, dy := x1-x0, y1-y0
	ll := dx*dx + dy*dy
	sd := func(x, y float64) float64 {
		t := 0.0
		if ll > 0 {
			t = math.Min(math.Max(((x-x0)*dx+(y-y0)*dy)/ll, 0), 1)
		}
		return math.Hypot(x-x0-t*dx, y-y0-t*dy) - w
	}
	c.shape(math.Min(x0, x1)-w, math.Min(y0, y1)-w, math.Max(x0, x1)+w, math.Max(y0, y1)+w, sd,
		func(x, y, d float64) float64 { return tone })
}

func (c *atlasCanvas) ellipse(cx, cy, rx, ry, ang, tone float64) {
	ca, sa := math.Cos(ang), math.Sin(ang)
	sd := func(x, y float64) float64 {
		dx, dy := x-cx, y-cy
		u := (dx*ca + dy*sa) / rx
		v := (-dx*sa + dy*ca) / ry
		return (math.Hypot(u, v) - 1) * math.Min(rx, ry)
	}
	r := math.Max(rx, ry)
	c.shape(cx-r, cy-r, cx+r, cy+r, sd, func(x, y, d float64) float64 {
		return tone * (0.8 + 0.2*math.Min(-d/2, 1))
	})
}

func drawTile(tile int) *atlasCanvas {
	c := newAtlasCanvas()
	n := float64(foliageTileSize)
	r := rand.New(rand.NewSource(int64(tile)*7919 + 17))
	rf := func(a, b float64) float64 { return a + r.Float64()*(b-a) }
	switch tile {
	case tileCluster:
		// A spray of small leaves: a twig forking twice, leaves off every
		// twig, thickest toward the tips — what a branch end looks like from
		// a few metres, where a leaf is a thumbnail, not a hand.
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
				grow(x1, y1, ang+turn*rf(0.5, 0.85), l*rf(0.68, 0.8), depth-1)
			}
		}
		grow(n/2, 0, math.Pi/2+rf(-0.1, 0.1), n*0.3, 3)
		for _, tw := range twigs {
			c.line(tw.x0, tw.y0, tw.x1, tw.y1, 1.0, 0.42)
		}
		for _, tw := range twigs {
			for k := 0; k < 6; k++ {
				t := rf(0.15, 1)
				bx, by := tw.x0+(tw.x1-tw.x0)*t, tw.y0+(tw.y1-tw.y0)*t
				base := math.Atan2(tw.y1-tw.y0, tw.x1-tw.x0)
				side := 1.0
				if k%2 == 0 {
					side = -1
				}
				c.leaf(bx, by, base+side*rf(0.5, 1.1), rf(11, 17), rf(4, 5.5), rf(0.72, 1))
			}
			c.leaf(tw.x1, tw.y1, math.Atan2(tw.y1-tw.y0, tw.x1-tw.x0)+rf(-0.2, 0.2), rf(12, 16), 5, rf(0.8, 1))
		}
		// And the leaves seen through the fork, so the spray is a mass
		// rather than a ring.
		for i := 0; i < 14; i++ {
			c.leaf(n/2+rf(-26, 26), rf(n*0.3, n*0.8), rf(0, 2*math.Pi), rf(11, 16), rf(4, 5.5), rf(0.62, 0.85))
		}
	case tileLeaf:
		c.line(n/2, 0, n/2, 14, 1.5, 0.5)
		c.leaf(n/2, 10, math.Pi/2, n*0.8, n*0.26, 0.95)
	case tileNeedles:
		// A flat spray: a branch with side shoots swept toward its tip, every
		// shoot dense with short needles — a spruce's branch seen from above.
		c.line(n/2, 0, n/2, n*0.97, 1.5, 0.4)
		for i := 0; i < 11; i++ {
			t := 0.06 + float64(i)*0.085
			y := n * t
			sl := (1 - t*0.75) * n * 0.44
			for _, side := range [2]float64{-1, 1} {
				a := math.Pi/2 + side*rf(0.85, 1.05)
				ex, ey := n/2+math.Cos(a)*sl, y+math.Sin(a)*sl
				c.line(n/2, y, ex, ey, 0.9, 0.42)
				for k := 0; k < 22; k++ {
					u := rf(0.05, 1)
					bx, by := n/2+(ex-n/2)*u, y+(ey-y)*u
					nl := rf(5, 9) * (1 - 0.4*u)
					for _, ns := range [2]float64{-1, 1} {
						na := a + ns*rf(0.5, 0.9)
						c.line(bx, by, bx+math.Cos(na)*nl, by+math.Sin(na)*nl, 0.6, rf(0.7, 1))
					}
				}
			}
		}
	case tileFern:
		c.line(n/2, 0, n/2, n*0.98, 1.3, 0.55)
		for i := 0; i < 18; i++ {
			t := float64(i) / 18
			y := n * (0.06 + t*0.9)
			l := (1 - t) * n * 0.42 * (0.55 + 0.45*math.Sin(math.Pi*math.Min(t*1.6, 1)))
			for _, side := range [2]float64{-1, 1} {
				a := math.Pi/2 + side*1.15
				c.leaf(n/2, y, a, l+3, math.Max(l*0.18, 2), rf(0.8, 1))
			}
		}
	case tilePetals:
		for i := 0; i < 14; i++ {
			a := float64(i)/14*2*math.Pi + rf(-0.08, 0.08)
			c.leaf(n/2+math.Cos(a)*8, n/2+math.Sin(a)*8, a, n*0.38, n*0.07, rf(0.9, 1))
		}
		c.ellipse(n/2, n/2, 14, 14, 0, 0.32)
	case tileLitter:
		for i := 0; i < 70; i++ {
			x, y := rf(8, n-8), rf(8, n-8)
			a := rf(0, math.Pi)
			l := rf(8, 20)
			c.line(x, y, x+math.Cos(a)*l, y+math.Sin(a)*l, 0.9, rf(0.55, 1))
		}
		for i := 0; i < 8; i++ {
			c.ellipse(rf(12, n-12), rf(12, n-12), rf(5, 9), rf(3, 5), rf(0, math.Pi), rf(0.5, 0.8))
		}
	case tileIvy:
		c.line(n/2, 0, n/2+4, n, 1.4, 0.45)
		for i := 0; i < 4; i++ {
			t := 0.1 + float64(i)*0.22
			side := 1.0
			if i%2 == 1 {
				side = -1
			}
			a := math.Pi/2 + side*0.9
			// Ivy: a broad, lobed leaf — three overlapping points.
			bx, by := n/2+2, n*t
			for k := -1; k <= 1; k++ {
				c.leaf(bx, by, a+float64(k)*0.55, n*0.3*(1-0.2*math.Abs(float64(k))), n*0.1, rf(0.85, 1))
			}
		}
	case tileMoss:
		// Moss: a ragged patch, dense in the middle and breaking into
		// separate tufts at its edge, each tuft a speckle of tiny fronds.
		for i := 0; i < 420; i++ {
			a := rf(0, 2*math.Pi)
			d := math.Pow(rf(0, 1), 0.6) * n * 0.44
			x, y := n/2+math.Cos(a)*d, n/2+math.Sin(a)*d
			rr := rf(2, 5) * (1.2 - d/(n*0.5))
			c.ellipse(x, y, rr, rr*rf(0.7, 1), rf(0, math.Pi), rf(0.55, 1))
		}
		for i := 0; i < 160; i++ {
			x, y := rf(10, n-10), rf(10, n-10)
			a := rf(0, 2*math.Pi)
			c.line(x, y, x+math.Cos(a)*rf(3, 6), y+math.Sin(a)*rf(3, 6), 0.7, rf(0.8, 1.05))
		}
	case tilePalm:
		// A pinnate frond: a rib up the middle and long, strap-like
		// leaflets swept toward the tip, longest a third of the way up, a
		// few missing or torn short, as the wind leaves them.
		c.line(n/2, 0, n/2, n*0.98, 1.4, 0.55)
		for i := 0; i < 26; i++ {
			t := 0.05 + float64(i)/26*0.92
			y := n * t
			l := n * 0.5 * math.Sin(math.Pi*math.Min(t*1.35+0.08, 1)) * rf(0.8, 1)
			for _, side := range [2]float64{-1, 1} {
				if r.Float64() < 0.08 {
					continue
				}
				a := math.Pi/2 + side*rf(0.55, 0.75)
				c.leaf(n/2, y, a, math.Max(l, 4), rf(1.8, 2.6), rf(0.75, 1))
			}
		}
	case tileGrassClump, tileTallGrassClump, tileReedClump:
		blades, hLo, hHi, lean, w0 := 70, 0.35, 0.95, 0.35, 2.4
		switch tile {
		case tileTallGrassClump:
			blades, hLo, hHi, lean, w0 = 55, 0.5, 0.97, 0.5, 2.0
		case tileReedClump:
			blades, hLo, hHi, lean, w0 = 30, 0.55, 0.97, 0.12, 3.2
		}
		// Back to front, the back ones darker, as the inside of a tuft is.
		for i := 0; i < blades; i++ {
			depth := float64(i) / float64(blades)
			x := rf(10, n-10)
			h := n * rf(hLo, hHi)
			bend := rf(-lean, lean) * h
			// A little fuller in the middle than at the edges — only a
			// little, or a grown tuft far off reads as a round bush.
			h *= 1 - 0.2*math.Pow(math.Abs(x-n/2)/(n/2), 2)
			const segs = 4
			px, py := x, 0.0
			for k := 1; k <= segs; k++ {
				t := float64(k) / segs
				nx, ny := x+bend*t*t, h*t
				tone := (0.45 + 0.55*t) * (0.7 + 0.3*depth)
				c.line(px, py, nx, ny, w0*(1-0.75*t)+0.5, tone)
				px, py = nx, ny
			}
			if tile == tileTallGrassClump && r.Float64() < 0.3 {
				c.ellipse(px, py-4, 2.2, 6, math.Pi/2+bend/h, rf(0.85, 1.05))
			}
			if tile == tileReedClump && r.Float64() < 0.35 {
				c.ellipse(px, py-h*0.12, 2.8, 8, math.Pi/2, 0.22)
			}
		}
	case tileFlowerDots:
		for i := 0; i < 16; i++ {
			x, y := rf(14, n-14), n*rf(0.45, 0.92)
			rr := rf(4, 7)
			for k := 0; k < 6; k++ {
				a := float64(k)/6*2*math.Pi + rf(-0.2, 0.2)
				c.ellipse(x+math.Cos(a)*rr*0.8, y+math.Sin(a)*rr*0.8, rr*0.7, rr*0.4, a, rf(0.85, 1))
			}
			c.ellipse(x, y, rr*0.35, rr*0.35, 0, 0.35)
		}
	case tileSeed:
		c.line(n/2, 0, n/2, n*0.9, 1.2, 0.6)
		for i := 0; i < 34; i++ {
			t := rf(0.25, 0.95)
			w := math.Sin(math.Pi*(t-0.25)/0.7) * n * 0.16
			c.ellipse(n/2+rf(-w, w), n*t, rf(4, 6), rf(6, 9), rf(-0.4, 0.4)+math.Pi/2, rf(0.75, 1))
		}
	}
	return c
}

// buildFoliageAtlas draws every tile and returns the atlas's mip chain,
// level 0 first, alpha coverage preserved down the chain.
func buildFoliageAtlas() []*image.NRGBA {
	w, h := foliageTileSize*foliageAtlasCols, foliageTileSize*foliageAtlasRows
	lum := make([]float32, w*h)
	alpha := make([]float32, w*h)
	for tile := 0; tile < foliageAtlasCols*foliageAtlasRows; tile++ {
		c := drawTile(tile)
		ox, oy := (tile%foliageAtlasCols)*foliageTileSize, (tile/foliageAtlasCols)*foliageTileSize
		for y := 0; y < foliageTileSize; y++ {
			for x := 0; x < foliageTileSize; x++ {
				i := (oy+y)*w + ox + x
				lum[i] = c.lum[y*foliageTileSize+x]
				alpha[i] = c.alpha[y*foliageTileSize+x]
			}
		}
	}
	// Bleed colour into the transparent texels so filtering at the cut-out
	// edge does not pull in black.
	bleedLum(lum, alpha, w, h)

	coverage := alphaCoverage(alpha, 1)
	var chain []*image.NRGBA
	for {
		chain = append(chain, atlasImage(lum, alpha, w, h))
		if w == 1 && h == 1 {
			break
		}
		nw, nh := max(w/2, 1), max(h/2, 1)
		nl := make([]float32, nw*nh)
		na := make([]float32, nw*nh)
		for y := 0; y < nh; y++ {
			for x := 0; x < nw; x++ {
				var sl, sa float32
				for dy := 0; dy < 2; dy++ {
					for dx := 0; dx < 2; dx++ {
						sx, sy := min(x*2+dx, w-1), min(y*2+dy, h-1)
						sl += lum[sy*w+sx]
						sa += alpha[sy*w+sx]
					}
				}
				nl[y*nw+x], na[y*nw+x] = sl/4, sa/4
			}
		}
		w, h, lum, alpha = nw, nh, nl, na
		// Rescale alpha so as many texels pass one half as at full size.
		lo, hi := float32(0.5), float32(8)
		for it := 0; it < 16; it++ {
			mid := (lo + hi) / 2
			if alphaCoverage(alpha, mid) < coverage {
				lo = mid
			} else {
				hi = mid
			}
		}
		s := (lo + hi) / 2
		for i := range alpha {
			alpha[i] = min(alpha[i]*s, 1)
		}
	}
	return chain
}

func alphaCoverage(alpha []float32, scale float32) float32 {
	n := 0
	for _, a := range alpha {
		if a*scale >= 0.5 {
			n++
		}
	}
	return float32(n) / float32(len(alpha))
}

// bleedLum spreads opaque texels' luminance outward into transparent ones,
// a few rings deep.
func bleedLum(lum, alpha []float32, w, h int) {
	known := make([]bool, len(lum))
	for i, a := range alpha {
		known[i] = a > 0.01
	}
	for pass := 0; pass < 6; pass++ {
		next := append([]bool(nil), known...)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				i := y*w + x
				if known[i] {
					continue
				}
				var s float32
				c := 0
				for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					nx, ny := x+d[0], y+d[1]
					if nx < 0 || ny < 0 || nx >= w || ny >= h || !known[ny*w+nx] {
						continue
					}
					s += lum[ny*w+nx]
					c++
				}
				if c > 0 {
					lum[i] = s / float32(c)
					next[i] = true
				}
			}
		}
		known = next
	}
}

func atlasImage(lum, alpha []float32, w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			l := uint8(min(max(lum[i], 0), 1)*255 + 0.5)
			a := uint8(min(max(alpha[i], 0), 1)*255 + 0.5)
			img.SetNRGBA(x, y, color.NRGBA{l, l, l, a})
		}
	}
	return img
}

// uploadFoliageAtlas uploads the chain as a mipmapped texture. Rows go up
// as stored, so tile row 0 is at v = 0 and a card's base is its bottom.
func uploadFoliageAtlas(chain []*image.NRGBA) uint32 {
	var tex uint32
	gl.GenTextures(1, &tex)
	gl.BindTexture(gl.TEXTURE_2D, tex)
	for lvl, img := range chain {
		b := img.Bounds()
		gl.TexImage2D(gl.TEXTURE_2D, int32(lvl), gl.RGBA8, int32(b.Dx()), int32(b.Dy()), 0,
			gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(img.Pix))
	}
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAX_LEVEL, int32(len(chain)-1))
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	return tex
}
