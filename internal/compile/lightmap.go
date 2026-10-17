package compile

// lightmap.go — the direct light baked per texel instead of per vertex
// (docs/lighting/PLAN.md, phase 5).
//
// The per-vertex bake (light.go) has to cut every face on a grid and refine
// that grid wherever a shadow edge crosses it, because a shadow can only be as
// sharp as the vertices that carry it: level1's 308k vertices became 640k
// that way, and a crate's shadow is still a ramp a metre wide. A lightmap
// carries the direct light in a texture instead — half a metre a texel by
// default, filtered between texels — so the faces are cut only on the coarse
// grid (the bounce and the ambient, which the vertices still carry, are smooth
// enough for it) and the shadows come out as sharp as the texels.
//
// # What a texel holds
//
// Not the lit colour: that depends on the normal it is lit with, and the
// renderer has several (flat, smoothed, normal-mapped). Each texel holds
//
//	rgb  the static lights' direct light OTHER than the baked sun, on the
//	     face's own normal — the lamps, whose light varies over a face in
//	     ways a normal map barely touches;
//	a    the baked sun's VISIBILITY: how much of the sun reaches the texel's
//	     patch of surface, 0 to 1, its shadow alone.
//
// The renderer works the sun's own term out per pixel from that visibility and
// whatever normal it shades with (fragment.glsl), which is also where the
// cascades take over within their reach — exactly the swap the per-vertex
// SunShare made, now with a texel's shadow behind it rather than a vertex's.
//
// # Charts
//
// Every face the CSG produced gets a chart: its plane, projected square on
// two axes in it, at the level's texel size, with a gutter of texels round it
// so filtering never reaches a neighbour's. The pieces the grid then cuts the
// face into (SubdivideFaces) inherit their parent's chart, so a face's pieces
// share one continuous map and the cluster LOD (world/static_clusters.go)
// welds them as before. Charts are packed into lightmapPage-sized pages, tall
// ones first, in shelves.
//
// A texel's light is sampled where the texel's centre is on the face — or,
// for a gutter texel off the face, at the nearest point of the face — and its
// shadow is the coverage of that texel's patch (softshadow.go's probe, a
// texel wide), so a shadow edge through a texel is antialiased, not stepped.
//
// Double-sided and translucent faces keep the per-vertex bake: their back
// side would need a map of its own, and a pane of glass shows nothing a
// lightmap would sharpen.

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// LightmapChart is where a face's light lives in the lightmap pages: page
// Page's texels [X, X+W) × [Y, Y+H), the face's plane mapped onto them by
// Origin + U·s·Texel + V·t·Texel at texel (s, t) of the chart, gutter
// included. Page -1 is a face with no lightmap (lit per vertex).
type LightmapChart struct {
	Page       int32
	X, Y, W, H int32
	Origin     mgl32.Vec3
	U, V       mgl32.Vec3
	Texel      float32
}

// UV is where point p of the face reads its page, in the page's 0..1
// coordinates.
func (c *LightmapChart) UV(p mgl32.Vec3) (u, v float32) {
	d := p.Sub(c.Origin)
	s := d.Dot(c.U) / c.Texel
	t := d.Dot(c.V) / c.Texel
	return (float32(c.X) + s) / lightmapPage, (float32(c.Y) + t) / lightmapPage
}

// LightmapPage is one page of baked texels: lightmapPage on a side, RGBA half
// floats, row by row from the bottom.
type LightmapPage struct {
	Size   int32
	Texels []uint16
}

const (
	// lightmapPage is a page's side in texels.
	lightmapPage = 2048
	// lightmapGutter is the texels round each chart.
	lightmapGutter = 2
	// lightmapMaxChart is the most texels a chart may span either way; a
	// face larger than that at the level's texel size gets coarser texels.
	lightmapMaxChart = 512
	// DefaultLightmapTexel is the texel size, in metres, a level gets unless
	// it says otherwise (RawCompile.LightmapTexel).
	DefaultLightmapTexel = 0.5
	// lightmapBudget is the most texels a level's pages may hold before the
	// texel size is raised to fit: 16 pages, 512 MB at RGBA16F — far past
	// what a sensible level needs, and a guard against one that is not.
	lightmapBudget = 16 * lightmapPage * lightmapPage
)

// LightmapPageSize is a page's side in texels, for the renderer.
const LightmapPageSize = lightmapPage

// lightmapped reports whether face f gets a chart.
func lightmapped(f *Face) bool {
	return !f.NoDraw && !f.DoubleSided && f.Alpha >= 1 && len(f.Points) >= 3
}

// chartBasis is two unit axes in the plane of normal n.
func chartBasis(n mgl32.Vec3) (u, v mgl32.Vec3) {
	ref := mgl32.Vec3{0, 1, 0}
	if math.Abs(float64(n.Y())) > 0.7 {
		ref = mgl32.Vec3{0, 0, 1}
	}
	u = ref.Cross(n).Normalize()
	v = n.Cross(u).Normalize()
	return u, v
}

// lightmapChartFor is the chart (without its page or place) of the faces
// sharing a parent: their plane, their bounds on it, at texel.
func lightmapChartFor(faces []*Face, texel float32) LightmapChart {
	n := faces[0].Plane.N
	u, v := chartBasis(n)
	minS, minT := float32(math.Inf(1)), float32(math.Inf(1))
	maxS, maxT := float32(math.Inf(-1)), float32(math.Inf(-1))
	origin := faces[0].Points[0]
	for _, f := range faces {
		for _, p := range f.Points {
			d := p.Sub(origin)
			s, t := d.Dot(u), d.Dot(v)
			minS, maxS = min(minS, s), max(maxS, s)
			minT, maxT = min(minT, t), max(maxT, t)
		}
	}
	spanS, spanT := maxS-minS, maxT-minT
	if span := max(spanS, spanT); span/texel > lightmapMaxChart-2*lightmapGutter-1 {
		texel = span / (lightmapMaxChart - 2*lightmapGutter - 1)
	}
	w := int32(math.Ceil(float64(spanS/texel))) + 1 + 2*lightmapGutter
	h := int32(math.Ceil(float64(spanT/texel))) + 1 + 2*lightmapGutter
	// The chart's texel (0, 0) corner: the gutter out from the bounds.
	o := origin.Add(u.Mul(minS - lightmapGutter*texel)).Add(v.Mul(minT - lightmapGutter*texel))
	return LightmapChart{Page: -1, W: w, H: h, Origin: o, U: u, V: v, Texel: texel}
}

// packCharts gives each chart a page and a place on it, tallest first, in
// shelves. It returns how many pages that took.
func packCharts(charts []*LightmapChart) int32 {
	order := make([]int, len(charts))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ca, cb := charts[order[a]], charts[order[b]]
		if ca.H != cb.H {
			return ca.H > cb.H
		}
		return ca.W > cb.W
	})
	page, x, y, shelf := int32(0), int32(0), int32(0), int32(0)
	for _, i := range order {
		c := charts[i]
		if x+c.W > lightmapPage {
			x, y, shelf = 0, y+shelf, 0
		}
		if y+c.H > lightmapPage {
			page, x, y, shelf = page+1, 0, 0, 0
		}
		c.Page, c.X, c.Y = page, x, y
		x += c.W
		shelf = max(shelf, c.H)
	}
	if len(charts) == 0 {
		return 0
	}
	return page + 1
}

// bakeLightmaps charts, packs and bakes every lightmapped face's direct
// light, filling Face.Lightmap and t.Lightmaps. Run after the per-vertex bake,
// whose shadow test and lights it shares.
func (b *baker) bakeLightmaps(texel float32) {
	t := b.tree
	if texel <= 0 {
		texel = DefaultLightmapTexel
	}

	// The pieces of each parent face (Face.lmParent, set before the grid cut).
	groups := map[int32][]int{}
	var parents []int32
	for fi := range t.Faces {
		f := &t.Faces[fi]
		f.Lightmap = LightmapChart{Page: -1}
		if f.lmParent == 0 || !lightmapped(f) {
			continue
		}
		if _, ok := groups[f.lmParent]; !ok {
			parents = append(parents, f.lmParent)
		}
		groups[f.lmParent] = append(groups[f.lmParent], fi)
	}
	sort.Slice(parents, func(a, b int) bool { return parents[a] < parents[b] })

	// Charts at the level's texel, coarsened until they fit the budget.
	var charts []LightmapChart
	for attempt := 0; ; attempt++ {
		charts = charts[:0]
		total := int64(0)
		for _, p := range parents {
			fs := make([]*Face, 0, len(groups[p]))
			for _, fi := range groups[p] {
				fs = append(fs, &t.Faces[fi])
			}
			c := lightmapChartFor(fs, texel)
			charts = append(charts, c)
			total += int64(c.W) * int64(c.H)
		}
		if total <= lightmapBudget*3/4 || attempt > 8 {
			break
		}
		texel *= 1.5
	}
	ptrs := make([]*LightmapChart, len(charts))
	for i := range charts {
		ptrs[i] = &charts[i]
	}
	pages := packCharts(ptrs)
	if pages == 0 {
		return
	}
	for ci, p := range parents {
		for _, fi := range groups[p] {
			t.Faces[fi].Lightmap = charts[ci]
		}
	}

	out := make([]LightmapPage, pages)
	for i := range out {
		out[i] = LightmapPage{Size: lightmapPage, Texels: make([]uint16, lightmapPage*lightmapPage*4)}
	}
	b.progress.parallelFor("Light: lightmaps", len(parents), func(ci int) {
		b.bakeChart(&charts[ci], groups[parents[ci]], out[charts[ci].Page].Texels)
	})
	t.Lightmaps = out
}

// bakeChart fills one chart's texels in its page.
func (b *baker) bakeChart(c *LightmapChart, pieces []int, page []uint16) {
	t := b.tree
	parent := &t.Faces[pieces[0]]
	n := parent.Plane.N
	// Each piece's polygon on the chart's axes, to find which piece a texel
	// centre is on (its own face is the one its shadow rays skip) and, off
	// every piece, the nearest point of the face.
	type poly2 struct {
		fi  int
		pts [][2]float32
	}
	polys := make([]poly2, len(pieces))
	for i, fi := range pieces {
		f := &t.Faces[fi]
		pp := make([][2]float32, len(f.Points))
		for k, p := range f.Points {
			d := p.Sub(c.Origin)
			pp[k] = [2]float32{d.Dot(c.U) / c.Texel, d.Dot(c.V) / c.Texel}
		}
		polys[i] = poly2{fi, pp}
	}
	acc := make([]float32, 3)
	for ty := int32(0); ty < c.H; ty++ {
		for tx := int32(0); tx < c.W; tx++ {
			s, tt := float32(tx)+0.5, float32(ty)+0.5
			owner, cs, ct := -1, s, tt
			best := float32(math.Inf(1))
			for _, pl := range polys {
				if inside2(pl.pts, s, tt) {
					owner, cs, ct = pl.fi, s, tt
					best = 0
					break
				}
				qs, qt := nearestOnPoly2(pl.pts, s, tt)
				if d := (qs-s)*(qs-s) + (qt-tt)*(qt-tt); d < best {
					best, owner, cs, ct = d, pl.fi, qs, qt
				}
			}
			if owner < 0 {
				continue
			}
			p := c.Origin.Add(c.U.Mul(cs * c.Texel)).Add(c.V.Mul(ct * c.Texel))
			acc[0], acc[1], acc[2] = 0, 0, 0
			sunVis := b.sampleTexel(&t.Faces[owner], owner, p, n, c.Texel, acc)
			i := ((c.Y+ty)*lightmapPage + (c.X + tx)) * 4
			page[i+0] = halfBits(acc[0])
			page[i+1] = halfBits(acc[1])
			page[i+2] = halfBits(acc[2])
			page[i+3] = halfBits(sunVis)
		}
	}
}

// sampleTexel is one texel's content: the direct light of every static light
// but the baked sun, on normal n, into out (rgb); and the baked sun's
// visibility over the texel's patch, which it returns — 1 where the sun is not
// baked or casts no shadow, 0 under a caster.
func (b *baker) sampleTexel(f *Face, self int, p, n mgl32.Vec3, texel float32, out []float32) float32 {
	start, buried := b.airStartAt(f, p, n)
	tan, bit := basisAbout(n)
	pr := &shadowProbe{b: b, f: f, self: self, p: p, side: n, start: start, buried: buried,
		r: texel * 0.5, tan: tan, bit: bit}
	g := b.lights.Global
	sunVis := float32(1)
	for i := range b.lights.Directional {
		dl := &b.lights.Directional[i]
		bright := dl.EffectiveBrightness()
		if bright <= 0 || dl.LitLive() || containsString(dl.BakeExclude, f.Chunk) {
			continue
		}
		d := dl.Direction
		if d.Len() < 1e-6 {
			continue
		}
		toLight := d.Normalize().Mul(-1)
		kept := float32(1)
		cos := n.Dot(toLight)
		if dl.CastsShadows() {
			if i == b.sun && cos < -0.35 {
				// Turned well away: its own face shadows it, whatever the
				// normal it is shaded with does.
				kept = 1 - dl.ShadowStrength
			} else if cos > -0.35 {
				lit := pr.lit(i, func(st mgl32.Vec3) (mgl32.Vec3, float32, bool) {
					reach := b.reach
					if e, ok := sealExit(b.seal, st, toLight); ok {
						reach = e.Sub(st).Len()
					}
					return toLight, reach, true
				})
				kept = 1 - dl.ShadowStrength*(1-lit)
			}
		}
		if i == b.sun {
			sunVis = kept
			continue
		}
		c := dl.Color.Mul(bright * g.LightWrap(cos) * kept)
		out[0], out[1], out[2] = out[0]+c[0], out[1]+c[1], out[2]+c[2]
	}
	for i := range b.lights.Points {
		pl := &b.lights.Points[i]
		bright := pl.EffectiveBrightness()
		if bright <= 0 || pl.Radius <= 0 || pl.LitLive() || containsString(pl.BakeExclude, f.Chunk) {
			continue
		}
		toLight := pl.Position.Sub(p)
		dist := toLight.Len()
		if dist >= pl.Radius || dist < 1e-6 {
			continue
		}
		at := 1 - dist/pl.Radius
		c := pl.Color.Mul(bright * g.LightWrap(n.Dot(toLight.Mul(1/dist))) * at * at)
		if c == (mgl32.Vec3{}) {
			continue
		}
		if pl.CastsShadows() {
			pos := pl.Position
			lit := pr.lit(len(b.lights.Directional)+i, func(st mgl32.Vec3) (mgl32.Vec3, float32, bool) {
				ray := pos.Sub(st)
				l := ray.Len()
				if l <= 1e-6 {
					return mgl32.Vec3{}, 0, false
				}
				return ray.Mul(1 / l), l, true
			})
			c = c.Mul(1 - pl.ShadowStrength*(1-lit))
		}
		out[0], out[1], out[2] = out[0]+c[0], out[1]+c[1], out[2]+c[2]
	}
	return sunVis
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// inside2 is whether (s, t) is inside the convex polygon pts (either winding).
func inside2(pts [][2]float32, s, t float32) bool {
	sign := float32(0)
	for i := range pts {
		a, b := pts[i], pts[(i+1)%len(pts)]
		cross := (b[0]-a[0])*(t-a[1]) - (b[1]-a[1])*(s-a[0])
		if math.Abs(float64(cross)) < 1e-6 {
			continue
		}
		if sign == 0 {
			sign = cross
		} else if (cross > 0) != (sign > 0) {
			return false
		}
	}
	return true
}

// nearestOnPoly2 is the nearest point of polygon pts's boundary to (s, t).
func nearestOnPoly2(pts [][2]float32, s, t float32) (float32, float32) {
	bs, bt := pts[0][0], pts[0][1]
	best := float32(math.Inf(1))
	for i := range pts {
		a, b := pts[i], pts[(i+1)%len(pts)]
		ex, ey := b[0]-a[0], b[1]-a[1]
		l2 := ex*ex + ey*ey
		k := float32(0)
		if l2 > 1e-12 {
			k = min(max(((s-a[0])*ex+(t-a[1])*ey)/l2, 0), 1)
		}
		qs, qt := a[0]+ex*k, a[1]+ey*k
		if d := (qs-s)*(qs-s) + (qt-t)*(qt-t); d < best {
			best, bs, bt = d, qs, qt
		}
	}
	return bs, bt
}

// halfBits is f as an IEEE half float's bits, rounded to nearest; past the
// half's range it is its largest finite value, and NaN is zero.
func halfBits(f float32) uint16 {
	if f != f {
		return 0
	}
	bits := math.Float32bits(f)
	sign := uint16(bits>>16) & 0x8000
	a := math.Abs(float64(f))
	switch {
	case a >= 65504:
		return sign | 0x7bff
	case a < 6.103515625e-05: // below the smallest normal half: subnormal
		m := uint16(math.Round(a / 5.960464477539063e-08))
		return sign | m
	}
	exp := int32((bits>>23)&0xff) - 127 + 15
	mant := bits & 0x7fffff
	// Round the 23-bit mantissa to 10 bits, to nearest, ties to even.
	r := mant >> 13
	rem := mant & 0x1fff
	if rem > 0x1000 || (rem == 0x1000 && r&1 == 1) {
		r++
		if r == 0x400 {
			r = 0
			exp++
		}
	}
	if exp >= 31 {
		return sign | 0x7bff
	}
	return sign | uint16(exp)<<10 | uint16(r)
}
