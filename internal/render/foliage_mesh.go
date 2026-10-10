package render

// foliage_mesh.go — the meshes foliage is drawn with, built in code.
//
// Every shape (world.FoliageShape) has world.FoliageVariants variants, each
// built at three levels of detail: near, a far one with a fraction of the
// triangles for the distance where the eye can no longer count blades, and
// for ground cover a clump — two or three crossed cards each showing a whole
// tuft (buildClump), for the distance where even the far mesh's blades are
// under a pixel. (Shapes whose far tier is an impostor, and those with none,
// repeat their far mesh there; it is never drawn.) All of them live in one
// vertex buffer, laid out foliageVertexFloats per vertex:
//
//	position (3)  in the plant's own frame: y up its growth axis, unit scale
//	normal (3)    the shading normal — a tree's leaf cards take the normal of
//	              its crown's round shape rather than their own flat one, so
//	              the crown shades as a volume and not as a pile of cards
//	uv (2)        atlas coordinates, for the textured parts
//	sway (1)      how much the wind moves it: 0 at the root, 1 at a blade's tip
//	part (1)      how it is coloured (the part* constants)
//	tone (1)      a brightness baked in — darker at a blade's base and deep
//	              inside a crown, where light does not reach
//
// A plant is modelled the way the thing grows: a grass clump is curved,
// tapered blades leaning out from a common root; a broadleaf tree a bent,
// flaring trunk, forked branches and leaf sprays at their ends; a conifer
// whorls of drooping needle sprays shortening to its tip. Each variant is
// built from its own fixed seed, so it is the same plant every run.

import (
	"math"
	"math/rand"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

const foliageVertexFloats = 11

// How a vertex is coloured.
const (
	partSolid      = 0 // the instance's colour (stone, stems, moss)
	partLeaf       = 1 // an atlas card tinted with the instance's colour
	partAccent     = 2 // the instance's accent colour (flower heads, caps)
	partAccentCard = 3 // an atlas card tinted with the accent colour
	partBark       = 4 // bark: its own brown, whatever the leaves are
	partBlade      = 5 // a grass blade: shaded half as if it were the ground
)

// foliageLODs is how many levels of detail each variant is built at.
const foliageLODs = 3

type fvert struct {
	p, n       mgl32.Vec3
	uv         mgl32.Vec2
	sway, tone float32
	part       int
}

type fmesh struct{ v []float32 }

func (m *fmesh) put(a fvert) {
	m.v = append(m.v, a.p[0], a.p[1], a.p[2], a.n[0], a.n[1], a.n[2], a.uv[0], a.uv[1], a.sway, float32(a.part), a.tone)
}

func (m *fmesh) tri(a, b, c fvert) { m.put(a); m.put(b); m.put(c) }

func (m *fmesh) quad(a, b, c, d fvert) { m.tri(a, b, c); m.tri(a, c, d) }

// card is a textured quad from base, spanning ±right across and up along;
// uvs cover the atlas tile. n is its shading normal (zero: its own).
func (m *fmesh) card(base, right, up mgl32.Vec3, tile int, part int, n mgl32.Vec3, sway0, sway1, tone float32) {
	if n == (mgl32.Vec3{}) {
		n = right.Cross(up).Normalize()
	}
	defer m.recordCard(m.mark(), asmCard{base: base, right: right, up: up, n: n, tile: tile, part: part,
		sway0: sway0, sway1: sway1, tone: tone})
	a := fvert{p: base.Sub(right), n: n, uv: tileUV(tile, 0, 0), sway: sway0, tone: tone, part: part}
	b := fvert{p: base.Add(right), n: n, uv: tileUV(tile, 1, 0), sway: sway0, tone: tone, part: part}
	c := fvert{p: base.Add(right).Add(up), n: n, uv: tileUV(tile, 1, 1), sway: sway1, tone: tone, part: part}
	d := fvert{p: base.Sub(right).Add(up), n: n, uv: tileUV(tile, 0, 1), sway: sway1, tone: tone, part: part}
	m.quad(a, b, c, d)
}

// blade is a curved, tapering strip rising from base: it grows along up for
// height h, bending toward lean by bend*h at its tip, w wide at the root.
func (m *fmesh) blade(base, up, lean mgl32.Vec3, h, w, bend float32, segs int, part int, toneLo, toneHi float32, swayScale float32) {
	defer m.recordAxis(m.mark(), base, base.Add(up.Mul(h)).Add(lean.Mul(bend*h)))
	side := up.Cross(lean)
	if side.Len() < 1e-4 {
		side = mgl32.Vec3{1, 0, 0}
	}
	side = side.Normalize()
	pt := func(t float32) (mgl32.Vec3, mgl32.Vec3) {
		c := base.Add(up.Mul(h * t)).Add(lean.Mul(bend * h * t * t))
		tan := up.Mul(h).Add(lean.Mul(2 * bend * h * t))
		return c, tan
	}
	var prevL, prevR fvert
	for i := 0; i <= segs; i++ {
		t := float32(i) / float32(segs)
		c, tan := pt(t)
		half := w * 0.5 * float32(math.Pow(float64(1-t), 0.7))
		n := side.Cross(tan).Normalize()
		sw := float32(math.Pow(float64(t), 1.5)) * swayScale
		tone := toneLo + (toneHi-toneLo)*t
		l := fvert{p: c.Sub(side.Mul(half)), n: n, uv: mgl32.Vec2{0, t}, sway: sw, tone: tone, part: part}
		r := fvert{p: c.Add(side.Mul(half)), n: n, uv: mgl32.Vec2{1, t}, sway: sw, tone: tone, part: part}
		if i > 0 {
			if i == segs {
				tip := fvert{p: c, n: n, uv: mgl32.Vec2{0.5, 1}, sway: sw, tone: tone, part: part}
				m.tri(prevL, prevR, tip)
			} else {
				m.quad(prevL, prevR, r, l)
			}
		}
		prevL, prevR = l, r
	}
}

// tube is a polyline of rings: pts with radii, sides round.
func (m *fmesh) tube(pts []mgl32.Vec3, radii, sway, tone []float32, sides int, part int) {
	defer m.recordAxis(m.mark(), pts[0], pts[len(pts)-1])
	rings := make([][]fvert, len(pts))
	for i := range pts {
		var dir mgl32.Vec3
		switch {
		case i == 0:
			dir = pts[1].Sub(pts[0])
		case i == len(pts)-1:
			dir = pts[i].Sub(pts[i-1])
		default:
			dir = pts[i+1].Sub(pts[i-1])
		}
		dir = dir.Normalize()
		ref := mgl32.Vec3{0, 1, 0}
		if math.Abs(float64(dir.Y())) > 0.9 {
			ref = mgl32.Vec3{1, 0, 0}
		}
		a := dir.Cross(ref).Normalize()
		b := dir.Cross(a)
		for s := 0; s < sides; s++ {
			ang := float64(s) / float64(sides) * 2 * math.Pi
			n := a.Mul(float32(math.Cos(ang))).Add(b.Mul(float32(math.Sin(ang))))
			rings[i] = append(rings[i], fvert{p: pts[i].Add(n.Mul(radii[i])), n: n, sway: sway[i], tone: tone[i], part: part})
		}
	}
	for i := 1; i < len(pts); i++ {
		for s := 0; s < sides; s++ {
			s2 := (s + 1) % sides
			m.quad(rings[i-1][s], rings[i-1][s2], rings[i][s2], rings[i][s])
		}
	}
}

// blob is an icosphere of radius 1 subdivided sub times, reshaped by
// shape(unit direction) -> point; flat-shaded when flat.
func (m *fmesh) blob(sub int, shape func(d mgl32.Vec3) mgl32.Vec3, flat bool, part int, toneOf func(p mgl32.Vec3) float32, keep func(d mgl32.Vec3) bool) {
	defer m.recordOther(m.mark())
	t := float32((1 + math.Sqrt(5)) / 2)
	vs := []mgl32.Vec3{
		{-1, t, 0}, {1, t, 0}, {-1, -t, 0}, {1, -t, 0},
		{0, -1, t}, {0, 1, t}, {0, -1, -t}, {0, 1, -t},
		{t, 0, -1}, {t, 0, 1}, {-t, 0, -1}, {-t, 0, 1},
	}
	for i := range vs {
		vs[i] = vs[i].Normalize()
	}
	fs := [][3]int{
		{0, 11, 5}, {0, 5, 1}, {0, 1, 7}, {0, 7, 10}, {0, 10, 11},
		{1, 5, 9}, {5, 11, 4}, {11, 10, 2}, {10, 7, 6}, {7, 1, 8},
		{3, 9, 4}, {3, 4, 2}, {3, 2, 6}, {3, 6, 8}, {3, 8, 9},
		{4, 9, 5}, {2, 4, 11}, {6, 2, 10}, {8, 6, 7}, {9, 8, 1},
	}
	for s := 0; s < sub; s++ {
		mid := map[[2]int]int{}
		get := func(a, b int) int {
			k := [2]int{min(a, b), max(a, b)}
			if i, ok := mid[k]; ok {
				return i
			}
			vs = append(vs, vs[a].Add(vs[b]).Normalize())
			mid[k] = len(vs) - 1
			return len(vs) - 1
		}
		var nf [][3]int
		for _, f := range fs {
			a, b, c := get(f[0], f[1]), get(f[1], f[2]), get(f[2], f[0])
			nf = append(nf, [3]int{f[0], a, c}, [3]int{f[1], b, a}, [3]int{f[2], c, b}, [3]int{a, b, c})
		}
		fs = nf
	}
	ps := make([]mgl32.Vec3, len(vs))
	for i, d := range vs {
		ps[i] = shape(d)
	}
	// Smooth normals: summed face normals.
	sn := make([]mgl32.Vec3, len(vs))
	for _, f := range fs {
		n := ps[f[1]].Sub(ps[f[0]]).Cross(ps[f[2]].Sub(ps[f[0]]))
		for _, i := range f {
			sn[i] = sn[i].Add(n)
		}
	}
	for _, f := range fs {
		if keep != nil && !(keep(vs[f[0]]) || keep(vs[f[1]]) || keep(vs[f[2]])) {
			continue
		}
		fn := ps[f[1]].Sub(ps[f[0]]).Cross(ps[f[2]].Sub(ps[f[0]])).Normalize()
		var tri [3]fvert
		for k, i := range f {
			n := fn
			if !flat && sn[i].Len() > 1e-6 {
				n = sn[i].Normalize()
			}
			tri[k] = fvert{p: ps[i], n: n, tone: toneOf(ps[i]), part: part}
		}
		m.tri(tri[0], tri[1], tri[2])
	}
}

// ---------- helpers ----------

type frand struct{ *rand.Rand }

func (r frand) f(a, b float32) float32 { return a + r.Float32()*(b-a) }

func yawDir(a float32) mgl32.Vec3 {
	return mgl32.Vec3{float32(math.Cos(float64(a))), 0, float32(math.Sin(float64(a)))}
}

var yUp = mgl32.Vec3{0, 1, 0}

// rotAxis rotates v about unit axis k by angle a (Rodrigues).
func rotAxis(v, k mgl32.Vec3, a float32) mgl32.Vec3 {
	c, s := float32(math.Cos(float64(a))), float32(math.Sin(float64(a)))
	return v.Mul(c).Add(k.Cross(v).Mul(s)).Add(k.Mul(k.Dot(v) * (1 - c)))
}

func noise3(p mgl32.Vec3, seed uint32) float32 { return world.ValueNoise3(p, seed) }

// leafSpray places a cluster of k crossed cards around c, shaded as part of
// a crown centred on crown with the given radius.
func (m *fmesh) leafSpray(r frand, c mgl32.Vec3, k int, size float32, tile, part int, crown mgl32.Vec3, crownR float32, sway float32) {
	for i := 0; i < k; i++ {
		d := mgl32.Vec3{r.f(-1, 1), r.f(-0.4, 1), r.f(-1, 1)}
		if d.Len() < 1e-3 {
			d = yUp
		}
		d = d.Normalize()
		base := c.Add(mgl32.Vec3{r.f(-0.3, 0.3), r.f(-0.2, 0.2), r.f(-0.3, 0.3)}.Mul(size))
		side := d.Cross(yUp)
		if side.Len() < 1e-3 {
			side = mgl32.Vec3{1, 0, 0}
		}
		side = rotAxis(side.Normalize(), d, r.f(0, math.Pi))
		fromCrown := base.Sub(crown)
		depth := fromCrown.Len() / max(crownR, 1e-3)
		n := fromCrown.Add(yUp.Mul(crownR * 0.35))
		if n.Len() < 1e-3 {
			n = yUp
		}
		tone := 0.55 + 0.5*min(depth, 1)
		m.card(base.Sub(d.Mul(size*0.3)), side.Mul(size*0.5), d.Mul(size), tile, part, n.Normalize(), sway, sway+0.12, tone)
	}
}

// ---------- the shapes ----------

func buildGrass(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(1000 + variant*31 + lod)))}
	m := &fmesh{}
	blades, segs := 6+variant, 3
	if lod > 0 {
		blades, segs = 3, 1
	}
	for i := 0; i < blades; i++ {
		a := r.f(0, 2*math.Pi)
		off := yawDir(a).Mul(r.f(0, 0.14))
		lean := yawDir(a + r.f(-0.6, 0.6))
		h := r.f(0.32, 0.55)
		w := r.f(0.03, 0.045)
		if lod > 0 {
			w *= 2.2
			h *= 0.95
		}
		up := yUp.Add(lean.Mul(r.f(0.05, 0.3))).Normalize()
		m.blade(off, up, lean, h, w, r.f(0.15, 0.45), segs, partBlade, 0.5, 1.12, 1)
	}
	return m.v
}

func buildTallGrass(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(2000 + variant*31 + lod)))}
	m := &fmesh{}
	blades, segs, heads := 6, 4, 2+variant
	if lod > 0 {
		blades, segs, heads = 3, 2, 1
	}
	for i := 0; i < blades; i++ {
		a := r.f(0, 2*math.Pi)
		lean := yawDir(a)
		w := r.f(0.03, 0.045)
		if lod > 0 {
			w *= 2
		}
		m.blade(yawDir(a).Mul(r.f(0, 0.12)), yUp.Add(lean.Mul(r.f(0.05, 0.2))).Normalize(), lean,
			r.f(0.7, 1.1), w, r.f(0.2, 0.55), segs, partBlade, 0.5, 1.1, 1)
	}
	for i := 0; i < heads; i++ {
		a := r.f(0, 2*math.Pi)
		lean := yawDir(a)
		h := r.f(0.9, 1.25)
		base := yawDir(a).Mul(r.f(0, 0.1))
		m.blade(base, yUp, lean, h, 0.014, 0.12, segs, partBlade, 0.6, 1.0, 1)
		top := base.Add(yUp.Mul(h)).Add(lean.Mul(0.12 * h))
		side := yawDir(a + math.Pi/2)
		m.card(top.Sub(yUp.Mul(0.02)), side.Mul(0.05), yUp.Add(lean.Mul(0.25)).Normalize().Mul(0.2), tileSeed, partLeaf, mgl32.Vec3{}, 1, 1.1, 1.15)
		if lod == 0 {
			m.card(top.Sub(yUp.Mul(0.02)), lean.Mul(0.05), yUp.Add(lean.Mul(0.25)).Normalize().Mul(0.2), tileSeed, partLeaf, mgl32.Vec3{}, 1, 1.1, 1.15)
		}
	}
	return m.v
}

func buildFlower(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(3000 + variant*31 + lod)))}
	m := &fmesh{}
	stems := 3
	if lod > 0 {
		stems = 1
	}
	// A few basal leaves.
	if lod == 0 {
		for i := 0; i < 4; i++ {
			a := r.f(0, 2*math.Pi)
			lean := yawDir(a)
			m.blade(mgl32.Vec3{}, yUp.Add(lean.Mul(0.8)).Normalize(), lean, r.f(0.12, 0.2), 0.05, 0.6, 2, partBlade, 0.55, 1, 0.6)
		}
	}
	for i := 0; i < stems; i++ {
		a := r.f(0, 2*math.Pi)
		lean := yawDir(a)
		base := yawDir(a).Mul(r.f(0, 0.08))
		switch variant {
		case 0: // daisy: a flat head on a stem
			h := r.f(0.3, 0.45)
			segs := 3
			if lod > 0 {
				segs = 1
			}
			m.blade(base, yUp, lean, h, 0.014, 0.15, segs, partSolid, 0.6, 0.95, 1)
			top := base.Add(yUp.Mul(h)).Add(lean.Mul(0.15 * h))
			face := yUp.Add(lean.Mul(r.f(0.2, 0.5))).Normalize()
			right := face.Cross(lean)
			if right.Len() < 1e-3 {
				right = mgl32.Vec3{1, 0, 0}
			}
			right = right.Normalize()
			fwd := right.Cross(face)
			s := r.f(0.06, 0.08)
			m.card(top.Sub(fwd.Mul(s)), right.Mul(s), fwd.Mul(2*s), tilePetals, partAccentCard, face, 1, 1, 1.1)
		case 1: // a spike of small flowers
			h := r.f(0.45, 0.65)
			segs := 3
			if lod > 0 {
				segs = 1
			}
			m.blade(base, yUp, lean, h, 0.016, 0.08, segs, partSolid, 0.6, 0.95, 1)
			top := base.Add(yUp.Mul(h * 0.62)).Add(lean.Mul(0.03 * h))
			for k := 0; k < 2-lod; k++ {
				side := yawDir(a + float32(k)*math.Pi/2)
				m.card(top, side.Mul(0.05), yUp.Mul(h*0.42), tileSeed, partAccentCard, side.Cross(yUp).Normalize(), 0.8, 1, 1.05)
			}
		default: // nodding bells
			h := r.f(0.28, 0.4)
			segs := 3
			if lod > 0 {
				segs = 2
			}
			m.blade(base, yUp, lean, h, 0.012, 0.5, segs, partSolid, 0.6, 0.95, 1)
			tip := base.Add(yUp.Mul(h)).Add(lean.Mul(0.5 * h))
			// A small cone hanging from the tip.
			sides := 6 - 2*lod
			bell := []mgl32.Vec3{tip, tip.Sub(yUp.Mul(0.035)), tip.Sub(yUp.Mul(0.07))}
			m.tube(bell, []float32{0.008, 0.025, 0.035}, []float32{1, 1, 1}, []float32{0.8, 1, 1.1}, sides, partAccent)
		}
	}
	return m.v
}

func buildFern(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(4000 + variant*31 + lod)))}
	m := &fmesh{}
	fronds, segs := 7+variant*2, 4
	if lod > 0 {
		fronds, segs = 4, 2
	}
	for i := 0; i < fronds; i++ {
		a := float32(i)/float32(fronds)*2*math.Pi + r.f(-0.25, 0.25)
		out := yawDir(a)
		side := yawDir(a + math.Pi/2)
		l := r.f(0.55, 0.85)
		rise := r.f(0.5, 0.75)
		pt := func(t float32) mgl32.Vec3 {
			return out.Mul(l * t).Add(yUp.Mul(l * (rise*t - 0.62*t*t)))
		}
		// The frond is one strip, bent as one (foliage_assembly.go).
		start := m.mark()
		for s := 0; s < segs; s++ {
			t0, t1 := float32(s)/float32(segs), float32(s+1)/float32(segs)
			p0, p1 := pt(t0), pt(t1)
			w := func(t float32) float32 { return l * 0.22 * float32(math.Sin(math.Pi*float64(0.08+t*0.92))) }
			tan := p1.Sub(p0).Normalize()
			n := side.Cross(tan).Normalize()
			if n.Y() < 0 {
				n = n.Mul(-1)
			}
			n = n.Add(yUp.Mul(0.4)).Normalize()
			uv0, uv1 := t0, t1
			mk := func(p mgl32.Vec3, u, v, sw float32) fvert {
				return fvert{p: p, n: n, uv: tileUV(tileFern, u, v), sway: sw, tone: 0.6 + 0.5*v, part: partLeaf}
			}
			w0, w1 := max(w(t0), 0.02), max(w(t1), 0.01)
			m.quad(mk(p0.Sub(side.Mul(w0)), 0, uv0, t0), mk(p0.Add(side.Mul(w0)), 1, uv0, t0),
				mk(p1.Add(side.Mul(w1)), 1, uv1, t1), mk(p1.Sub(side.Mul(w1)), 0, uv1, t1))
		}
		m.recordAxis(start, pt(0), pt(1))
	}
	return m.v
}

func buildShrub(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(5000 + variant*31 + lod)))}
	m := &fmesh{}
	crownY := r.f(0.5, 0.7)
	rx, ry := r.f(0.55, 0.75), r.f(0.4, 0.55)
	crown := mgl32.Vec3{0, crownY, 0}
	if lod == 0 {
		for i := 0; i < 4; i++ {
			a := r.f(0, 2*math.Pi)
			d := yawDir(a).Mul(r.f(0.15, 0.35)).Add(yUp.Mul(crownY)).Add(mgl32.Vec3{0, 0.1, 0})
			m.tube([]mgl32.Vec3{{}, d.Mul(0.5), d}, []float32{0.025, 0.018, 0.01}, []float32{0, 0.05, 0.12}, []float32{0.5, 0.6, 0.7}, 3, partBark)
		}
	}
	cards, size := 24+variant*4, r.f(0.42, 0.55)
	if lod > 0 {
		cards, size = 9, size*1.6
	}
	for i := 0; i < cards; i++ {
		// A point in the crown's ellipsoid, biased to its surface.
		d := mgl32.Vec3{r.f(-1, 1), r.f(-0.6, 1), r.f(-1, 1)}
		if d.Len() < 1e-3 {
			continue
		}
		d = d.Normalize().Mul(float32(math.Pow(float64(r.f(0.35, 1)), 0.5)))
		c := crown.Add(mgl32.Vec3{d[0] * rx, d[1] * ry, d[2] * rx})
		m.leafSpray(r, c, 1, size, tileCluster, partLeaf, crown, max(rx, ry), 0.25)
	}
	return m.v
}

func buildBroadleaf(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(6000 + variant*131 + lod)))}
	m := &fmesh{}
	h := r.f(3.0, 4.2)
	lean := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0.1, 0.35))
	sides := 7
	segs := 5
	if lod > 0 {
		sides, segs = 4, 2
	}
	var trunk []mgl32.Vec3
	var radii, sway, tone []float32
	for i := 0; i <= segs; i++ {
		t := float32(i) / float32(segs)
		wob := mgl32.Vec3{r.f(-0.08, 0.08), 0, r.f(-0.08, 0.08)}
		if i == 0 {
			wob = mgl32.Vec3{}
		}
		trunk = append(trunk, yUp.Mul(h*t).Add(lean.Mul(t*t)).Add(wob))
		rad := 0.17 - 0.08*t
		if t < 0.12 {
			rad += (0.12 - t) * 0.9 // root flare
		}
		radii = append(radii, rad)
		sway = append(sway, t*0.05)
		tone = append(tone, 0.55+0.35*t)
	}
	m.tube(trunk, radii, sway, tone, sides, partBark)
	crownC := trunk[len(trunk)-1].Add(yUp.Mul(0.6))
	crownR := r.f(2.1, 2.6)

	mains := 6
	if lod > 0 {
		mains = 4
	}
	var tips []mgl32.Vec3
	for i := 0; i < mains; i++ {
		t := r.f(0.5, 0.85)
		base := trunk[0].Add(trunk[len(trunk)-1].Sub(trunk[0]).Mul(t))
		dir := yawDir(float32(i)/float32(mains)*2*math.Pi + r.f(-0.3, 0.3)).Mul(r.f(0.7, 1)).Add(yUp.Mul(r.f(0.6, 1.1))).Normalize()
		l := r.f(1.3, 1.9)
		mid := base.Add(dir.Mul(l * 0.5)).Add(yUp.Mul(0.1))
		end := base.Add(dir.Mul(l))
		bs := 5
		if lod > 0 {
			bs = 3
		}
		m.tube([]mgl32.Vec3{base, mid, end}, []float32{0.05, 0.03, 0.012}, []float32{0.05, 0.12, 0.2}, []float32{0.6, 0.7, 0.8}, bs, partBark)
		tips = append(tips, end)
		if lod == 0 {
			for k := 0; k < 2; k++ {
				sb := base.Add(dir.Mul(l * r.f(0.45, 0.75)))
				sd := rotAxis(dir, yUp, r.f(-1.1, 1.1)).Add(yUp.Mul(r.f(0, 0.5))).Normalize()
				se := sb.Add(sd.Mul(r.f(0.7, 1.1)))
				m.tube([]mgl32.Vec3{sb, se}, []float32{0.018, 0.006}, []float32{0.12, 0.22}, []float32{0.7, 0.8}, 3, partBark)
				tips = append(tips, se)
			}
		}
	}
	if lod == 0 {
		for _, tp := range tips {
			m.leafSpray(r, tp, 5, r.f(0.9, 1.2), tileCluster, partLeaf, crownC, crownR, 0.3)
		}
		// Fill the crown so it reads as a mass, not as sprays on sticks.
		for i := 0; i < 56; i++ {
			d := mgl32.Vec3{r.f(-1, 1), r.f(-0.5, 1), r.f(-1, 1)}.Normalize().Mul(float32(math.Sqrt(float64(r.f(0.3, 1)))))
			c := crownC.Add(mgl32.Vec3{d[0] * crownR, d[1] * crownR * 0.75, d[2] * crownR})
			m.leafSpray(r, c, 1, r.f(1.0, 1.35), tileCluster, partLeaf, crownC, crownR, 0.3)
		}
	} else {
		for i := 0; i < 16; i++ {
			d := mgl32.Vec3{r.f(-1, 1), r.f(-0.4, 1), r.f(-1, 1)}.Normalize().Mul(r.f(0.55, 0.95))
			c := crownC.Add(mgl32.Vec3{d[0] * crownR, d[1] * crownR * 0.75, d[2] * crownR})
			m.leafSpray(r, c, 1, 2.3, tileCluster, partLeaf, crownC, crownR, 0.3)
		}
	}
	return m.v
}

func buildConifer(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(7000 + variant*131 + lod)))}
	m := &fmesh{}
	h := r.f(7, 9.5)
	base := r.f(1.7, 2.3)
	sides := 6
	if lod > 0 {
		sides = 4
	}
	m.tube([]mgl32.Vec3{{}, yUp.Mul(h * 0.5), yUp.Mul(h)}, []float32{0.2, 0.12, 0.03}, []float32{0, 0.04, 0.1}, []float32{0.55, 0.7, 0.8}, sides, partBark)
	step := float32(0.5)
	per := 6
	if lod > 0 {
		step, per = 1.1, 4
	}
	for y := float32(1.3); y < h-0.3; y += step * r.f(0.85, 1.15) {
		t := (y - 1.3) / (h - 1.6)
		rad := base*float32(math.Pow(float64(1-t), 0.95)) + 0.2
		off := r.f(0, 2*math.Pi)
		for i := 0; i < per; i++ {
			a := off + float32(i)/float32(per)*2*math.Pi + r.f(-0.2, 0.2)
			out := yawDir(a)
			droop := r.f(0.25, 0.45)
			along := out.Add(yUp.Mul(-droop)).Normalize()
			start := yUp.Mul(y)
			side := out.Cross(yUp).Normalize()
			w := 0.28 + rad*0.18
			if lod > 0 {
				w *= 1.5
			}
			n := out.Mul(0.75).Add(yUp.Mul(0.65)).Normalize()
			tone := 0.55 + 0.5*(1-t*0.3)*min(rad/base, 1)
			sw := 0.08 + 0.18*t
			// The spray lies flat, fanned across the branch...
			m.card(start, side.Mul(w), along.Mul(rad*1.05), tileNeedles, partLeaf, n, sw*0.5, sw, tone)
			// ...and a narrower one on edge, so the branch has thickness.
			if lod == 0 {
				vert := side.Cross(along).Normalize()
				m.card(start, vert.Mul(w*0.55), along.Mul(rad), tileNeedles, partLeaf, n, sw*0.5, sw, tone*0.92)
			}
		}
	}
	// The leader.
	for k := 0; k < 2; k++ {
		side := yawDir(float32(k) * math.Pi / 2)
		m.card(yUp.Mul(h-0.6), side.Mul(0.22), yUp.Mul(1.1), tileNeedles, partLeaf, yUp, 0.12, 0.2, 1)
	}
	return m.v
}

func buildRock(variant, lod int) []float32 {
	m := &fmesh{}
	seed := uint32(8000 + variant*97)
	sq := []float32{0.62, 0.8, 0.5, 0.7}[variant%4]
	stretch := []float32{1.15, 0.95, 1.35, 1.0}[variant%4]
	sub := 1
	if lod > 0 {
		sub = 0
	}
	shape := func(d mgl32.Vec3) mgl32.Vec3 {
		n := noise3(d.Mul(1.7), seed)*0.28 + noise3(d.Mul(4.1), seed+1)*0.12
		p := d.Mul(0.5 * (0.8 + n))
		p = mgl32.Vec3{p[0] * stretch, p[1] * sq, p[2]}
		// Faceted: pull toward a few planes so it reads as broken stone.
		if p[1] < -0.12 {
			p[1] = -0.12 + (p[1]+0.12)*0.3
		}
		return p
	}
	m.blob(sub, shape, true, partSolid, func(p mgl32.Vec3) float32 { return 0.62 + 0.55*min(max(p[1]+0.25, 0), 0.7) }, nil)
	return m.v
}

func buildPebbles(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(9000 + variant*31 + lod)))}
	m := &fmesh{}
	count := 7 + variant*2
	if lod > 0 {
		count = 4
	}
	for i := 0; i < count; i++ {
		c := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0, 0.45))
		s := r.f(0.04, 0.09)
		seed := uint32(r.Int31())
		m.blob(0, func(d mgl32.Vec3) mgl32.Vec3 {
			n := noise3(d.Mul(2), seed) * 0.4
			p := d.Mul(s * (0.8 + n))
			return c.Add(mgl32.Vec3{p[0], max(p[1]*0.6, -s*0.2), p[2]})
		}, true, partSolid, func(p mgl32.Vec3) float32 { return r.f(0.75, 1.15) }, nil)
	}
	return m.v
}

func buildMoss(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(10000 + variant*31 + lod)))}
	m := &fmesh{}
	// Flat, ragged patches lying on the surface, overlapping a little in
	// height so they never fight for depth...
	patches := 3 + variant
	if lod > 0 {
		patches = 2
	}
	for i := 0; i < patches; i++ {
		a := r.f(0, 2*math.Pi)
		right, fwd := yawDir(a), yawDir(a+math.Pi/2)
		s := r.f(0.35, 0.6)
		c := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0, 0.2)).Add(yUp.Mul(0.006 + float32(i)*0.004))
		m.card(c.Sub(fwd.Mul(s*0.5)), right.Mul(s*0.5), fwd.Mul(s), tileMoss, partLeaf, yUp, 0, 0, r.f(0.8, 1.1))
	}
	if lod > 0 {
		return m.v
	}
	// ...and a low cushion, so the moss has a silhouette at a grazing angle.
	// One, and coarse: moss is painted over whole cliffs, and it is the
	// most numerous thing drawn after grass.
	c := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0, 0.15))
	rad := r.f(0.07, 0.12)
	hgt := r.f(0.02, 0.035)
	seed := uint32(r.Int31())
	m.blob(0, func(d mgl32.Vec3) mgl32.Vec3 {
		n := noise3(d.Mul(3), seed) * 0.35
		return c.Add(mgl32.Vec3{d[0] * rad * (0.8 + n), max(d[1], 0) * hgt, d[2] * rad * (0.8 + n)})
	}, false, partSolid, func(p mgl32.Vec3) float32 { return 0.8 + 6*max(p[1], 0) },
		func(d mgl32.Vec3) bool { return d[1] > 0.05 })
	return m.v
}

func buildVine(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(11000 + variant*31 + lod)))}
	m := &fmesh{}
	l := []float32{1.3, 1.8, 2.3}[variant%3]
	step := float32(0.11)
	if lod > 0 {
		step = 0.28
	}
	phase := r.f(0, 6)
	pt := func(t float32) mgl32.Vec3 {
		return mgl32.Vec3{float32(math.Sin(float64(t*3+phase))) * 0.06, l * t, float32(math.Cos(float64(t*2.3+phase))) * 0.05}
	}
	// The stem: thin strip, y down the vine (the instance hangs it).
	m.blade(mgl32.Vec3{}, yUp, mgl32.Vec3{1, 0, 0}, l, 0.012, 0, 4, partSolid, 0.7, 0.8, 1)
	side := 1.0
	for y := float32(0.04); y < l; y += step * r.f(0.8, 1.2) {
		t := y / l
		p := pt(t)
		a := r.f(0, 2*math.Pi)
		out := yawDir(a)
		s := r.f(0.12, 0.17) * (1 - 0.35*t)
		if lod > 0 {
			s *= 1.8
		}
		dir := out.Mul(float32(side) * 0.6).Add(yUp.Mul(0.8)).Normalize()
		sw := float32(math.Pow(float64(t), 1.2))
		m.card(p, out.Cross(dir).Normalize().Mul(s*0.5), dir.Mul(s), tileIvy, partLeaf, out, sw, sw, 0.8+0.3*r.f(0, 1))
		side = -side
	}
	return m.v
}

func buildReed(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(12000 + variant*31 + lod)))}
	m := &fmesh{}
	blades, segs, heads := 9, 4, 1+variant
	if lod > 0 {
		blades, segs, heads = 4, 2, 1
	}
	for i := 0; i < blades; i++ {
		a := r.f(0, 2*math.Pi)
		lean := yawDir(a)
		w := r.f(0.025, 0.035)
		if lod > 0 {
			w *= 1.8
		}
		m.blade(yawDir(a).Mul(r.f(0, 0.15)), yUp.Add(lean.Mul(r.f(0.02, 0.15))).Normalize(), lean,
			r.f(1.0, 1.6), w, r.f(0.1, 0.35), segs, partBlade, 0.5, 1.05, 1)
	}
	for i := 0; i < heads; i++ {
		a := r.f(0, 2*math.Pi)
		base := yawDir(a).Mul(r.f(0, 0.08))
		h := r.f(1.3, 1.8)
		m.blade(base, yUp, yawDir(a), h, 0.016, 0.05, segs, partSolid, 0.6, 0.9, 1)
		top := base.Add(yUp.Mul(h * 0.82)).Add(yawDir(a).Mul(0.04 * h))
		m.tube([]mgl32.Vec3{top, top.Add(yUp.Mul(0.18))}, []float32{0.028, 0.026}, []float32{0.9, 1}, []float32{0.9, 1}, 6-2*lod, partAccent)
	}
	return m.v
}

func buildMushroom(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(13000 + variant*31 + lod)))}
	m := &fmesh{}
	count := 3 + variant
	if lod > 0 {
		count = 2
	}
	sides := 8 - 3*lod
	for i := 0; i < count; i++ {
		c := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0, 0.1))
		h := r.f(0.05, 0.12)
		cr := r.f(0.03, 0.06)
		m.tube([]mgl32.Vec3{c, c.Add(yUp.Mul(h))}, []float32{cr * 0.3, cr * 0.25}, []float32{0, 0}, []float32{0.8, 1}, sides, partSolid)
		top := c.Add(yUp.Mul(h))
		// A domed cap: rings from the rim up to the crown.
		cap := []mgl32.Vec3{top.Sub(yUp.Mul(cr * 0.2)), top.Add(yUp.Mul(cr * 0.25)), top.Add(yUp.Mul(cr * 0.55))}
		m.tube(cap, []float32{cr, cr * 0.8, 0.002}, []float32{0, 0, 0}, []float32{0.7, 1, 1.1}, sides, partAccent)
	}
	return m.v
}

func buildLitter(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(14000 + variant*31 + lod)))}
	m := &fmesh{}
	if variant == 1 {
		// Fallen needles: flat mats.
		for i := 0; i < 2-lod; i++ {
			a := r.f(0, 2*math.Pi)
			right, fwd := yawDir(a), yawDir(a+math.Pi/2)
			m.card(fwd.Mul(-0.3).Add(yUp.Mul(0.01+float32(i)*0.004)), right.Mul(0.3), fwd.Mul(0.6), tileLitter, partLeaf, yUp, 0, 0, r.f(0.8, 1))
		}
		return m.v
	}
	count := 9
	if lod > 0 {
		count = 4
	}
	for i := 0; i < count; i++ {
		c := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0, 0.4))
		a := r.f(0, 2*math.Pi)
		fwd := yawDir(a).Add(yUp.Mul(r.f(-0.1, 0.15))).Normalize()
		right := yawDir(a + math.Pi/2).Add(yUp.Mul(r.f(-0.15, 0.15))).Normalize()
		s := r.f(0.07, 0.12)
		if lod > 0 {
			s *= 1.5
		}
		m.card(c.Add(yUp.Mul(0.008)).Sub(fwd.Mul(s*0.5)), right.Mul(s*0.4), fwd.Mul(s), tileLeaf, partAccentCard, yUp, 0.1, 0.15, r.f(0.7, 1.15))
	}
	return m.v
}

func buildDeadwood(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(15000 + variant*31 + lod)))}
	m := &fmesh{}
	l := r.f(1.4, 2.2)
	rad := r.f(0.06, 0.1)
	sides := 6
	segs := 3
	if lod > 0 {
		sides, segs = 4, 1
	}
	var pts []mgl32.Vec3
	var radii, sway, tone []float32
	for i := 0; i <= segs; i++ {
		t := float32(i) / float32(segs)
		pts = append(pts, mgl32.Vec3{(t - 0.5) * l, rad * (1 - 0.3*t), r.f(-0.08, 0.08)})
		radii = append(radii, rad*(1-0.45*t))
		sway = append(sway, 0)
		tone = append(tone, r.f(0.75, 1))
	}
	m.tube(pts, radii, sway, tone, sides, partSolid)
	if lod == 0 {
		for k := 0; k < 2; k++ {
			b := pts[1+k]
			d := mgl32.Vec3{r.f(-0.3, 0.3), r.f(0.3, 0.7), r.f(-1, 1)}.Normalize()
			m.tube([]mgl32.Vec3{b, b.Add(d.Mul(r.f(0.25, 0.45)))}, []float32{rad * 0.45, rad * 0.2}, []float32{0, 0}, []float32{0.8, 0.9}, 4, partSolid)
		}
	}
	return m.v
}

// ribTube is tube with ribs: every other one of its 2*ribs sides stands
// out by depth (a fraction of the radius) and the grooves between are
// shaded darker, the way a cactus is pleated.
func (m *fmesh) ribTube(pts []mgl32.Vec3, radii []float32, ribs int, depth float32, tone []float32, part int) {
	defer m.recordAxis(m.mark(), pts[0], pts[len(pts)-1])
	sides := ribs * 2
	rings := make([][]fvert, len(pts))
	for i := range pts {
		var dir mgl32.Vec3
		switch {
		case i == 0:
			dir = pts[1].Sub(pts[0])
		case i == len(pts)-1:
			dir = pts[i].Sub(pts[i-1])
		default:
			dir = pts[i+1].Sub(pts[i-1])
		}
		dir = dir.Normalize()
		ref := mgl32.Vec3{1, 0, 0}
		if math.Abs(float64(dir.X())) > 0.9 {
			ref = mgl32.Vec3{0, 0, 1}
		}
		a := dir.Cross(ref).Normalize()
		b := dir.Cross(a)
		for s := 0; s < sides; s++ {
			ang := float64(s) / float64(sides) * 2 * math.Pi
			n := a.Mul(float32(math.Cos(ang))).Add(b.Mul(float32(math.Sin(ang))))
			rad, tn := radii[i], tone[i]
			if s%2 == 1 {
				rad *= 1 - depth
				tn *= 0.78
			}
			rings[i] = append(rings[i], fvert{p: pts[i].Add(n.Mul(rad)), n: n, tone: tn, part: part})
		}
	}
	for i := 1; i < len(pts); i++ {
		for s := 0; s < sides; s++ {
			s2 := (s + 1) % sides
			m.quad(rings[i-1][s], rings[i-1][s2], rings[i][s2], rings[i][s])
		}
	}
}

// frond is a leaf strip along the curve pt(t), t in [0,1], w(t) either side
// of it across side, cut from tile. Its two halves fold down from the rib by
// fold (a fraction of the width), so a palm frond or a banana leaf is a
// shallow V rather than a board.
func (m *fmesh) frond(pt func(t float32) mgl32.Vec3, w func(t float32) float32, side mgl32.Vec3, segs, tile int, fold, sway0, sway1 float32) {
	m.frondPart(pt, w, side, segs, tile, partLeaf, fold, sway0, sway1)
}

// frondPart is frond coloured as part (partLeaf, or partAccentCard for a
// dead frond in the accent's brown).
func (m *fmesh) frondPart(pt func(t float32) mgl32.Vec3, w func(t float32) float32, side mgl32.Vec3, segs, tile, part int, fold, sway0, sway1 float32) {
	defer m.recordAxis(m.mark(), pt(0), pt(1))
	for s := 0; s < segs; s++ {
		t0, t1 := float32(s)/float32(segs), float32(s+1)/float32(segs)
		p0, p1 := pt(t0), pt(t1)
		tan := p1.Sub(p0).Normalize()
		n := side.Cross(tan).Normalize()
		if n.Y() < 0 {
			n = n.Mul(-1)
		}
		n = n.Add(yUp.Mul(0.5)).Normalize()
		mk := func(p mgl32.Vec3, u, v, t float32) fvert {
			return fvert{p: p, n: n, uv: tileUV(tile, u, v), sway: sway0 + (sway1-sway0)*t, tone: 0.6 + 0.5*v, part: part}
		}
		w0, w1 := max(w(t0), 0.01), max(w(t1), 0.01)
		d0, d1 := n.Mul(-w0*fold), n.Mul(-w1*fold)
		if fold == 0 {
			m.quad(mk(p0.Sub(side.Mul(w0)), 0, t0, t0), mk(p0.Add(side.Mul(w0)), 1, t0, t0),
				mk(p1.Add(side.Mul(w1)), 1, t1, t1), mk(p1.Sub(side.Mul(w1)), 0, t1, t1))
			continue
		}
		m.quad(mk(p0.Sub(side.Mul(w0)).Add(d0), 0, t0, t0), mk(p0, 0.5, t0, t0),
			mk(p1, 0.5, t1, t1), mk(p1.Sub(side.Mul(w1)).Add(d1), 0, t1, t1))
		m.quad(mk(p0, 0.5, t0, t0), mk(p0.Add(side.Mul(w0)).Add(d0), 1, t0, t0),
			mk(p1.Add(side.Mul(w1)).Add(d1), 1, t1, t1), mk(p1, 0.5, t1, t1))
	}
}

func buildPalm(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(16000 + variant*131 + lod)))}
	m := &fmesh{}
	h := r.f(5.6, 7.2)
	// The trunk leans and curves back up toward the light, more for the
	// later variants — a beach palm bowed by years of wind.
	bend := []float32{0.12, 0.3, 0.5}[variant%3]
	lean := yawDir(r.f(0, 2*math.Pi))
	trunkAt := func(t float32) mgl32.Vec3 {
		return yUp.Mul(h * t).Add(lean.Mul(bend * h * t * (1.25 - 0.6*t)))
	}
	segs, sides := 14, 7
	if lod > 0 {
		segs, sides = 4, 4
	}
	var trunk []mgl32.Vec3
	var radii, sway, tone []float32
	for i := 0; i <= segs; i++ {
		t := float32(i) / float32(segs)
		trunk = append(trunk, trunkAt(t))
		rad := 0.15 - 0.04*t
		if t < 0.08 {
			rad += (0.08 - t) * 1.2 // the swollen root bole
		}
		radii = append(radii, rad)
		sway = append(sway, t*t*0.12)
		// Alternate bands: the scars the old fronds left, ring on ring.
		tn := 0.6 + 0.3*t
		if lod == 0 && i%2 == 1 {
			tn *= 0.78
		}
		tone = append(tone, tn)
	}
	// A palm's trunk is pale and grey, not bark-brown: it takes the accent
	// colour, and its coconuts a darker tone of the same.
	m.tube(trunk, radii, sway, tone, sides, partAccent)
	top := trunk[len(trunk)-1]

	fronds, fsegs := 12, 6
	if lod > 0 {
		fronds, fsegs = 7, 3
	}
	for i := 0; i < fronds; i++ {
		a := float32(i)/float32(fronds)*2*math.Pi + r.f(-0.2, 0.2)
		out := yawDir(a)
		side := yawDir(a + math.Pi/2)
		l := r.f(2.6, 3.3)
		// The upper fronds rise before they arch; the lower, older ones
		// hang almost from where they leave the crown.
		rise := r.f(0.2, 0.75)
		if i%3 == 2 {
			rise = r.f(-0.2, 0.1)
		}
		start := top.Add(yUp.Mul(r.f(-0.15, 0.1)))
		pt := func(t float32) mgl32.Vec3 {
			return start.Add(out.Mul(l * t * (1 - 0.15*t))).Add(yUp.Mul(l * (rise*t - 0.75*t*t)))
		}
		w := func(t float32) float32 { return l * 0.24 * float32(math.Sin(math.Pi*float64(0.06+t*0.94))) }
		fold := float32(0.35)
		if lod > 0 {
			fold = 0
		}
		m.frond(pt, w, side, fsegs, tilePalm, fold, 0.14, 0.55)
	}
	// Coconuts, clustered under the crown.
	if lod == 0 && variant != 2 {
		for i := 0; i < 3+variant; i++ {
			c := top.Add(yawDir(r.f(0, 2*math.Pi)).Mul(0.17)).Sub(yUp.Mul(r.f(0.1, 0.25)))
			rr := r.f(0.09, 0.12)
			m.blob(0, func(d mgl32.Vec3) mgl32.Vec3 { return c.Add(d.Mul(rr)) }, false, partAccent,
				func(p mgl32.Vec3) float32 { return 0.55 + 1.5*max(p[1]-c[1], 0) }, nil)
		}
	}
	return m.v
}

func buildCactus(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(17000 + variant*131 + lod)))}
	m := &fmesh{}
	ribs := 8
	if lod > 0 {
		ribs = 3
	}
	// column is an upright ribbed tube from base, rounded off at its top.
	column := func(pts []mgl32.Vec3, rad float32) {
		n := len(pts)
		end := pts[n-1]
		dir := end.Sub(pts[n-2]).Normalize()
		pts = append(pts, end.Add(dir.Mul(rad*0.55)), end.Add(dir.Mul(rad*0.9)))
		radii := make([]float32, len(pts))
		tone := make([]float32, len(pts))
		for i := range pts {
			radii[i] = rad
			tone[i] = 0.7 + 0.35*float32(i)/float32(len(pts)-1)
		}
		radii[len(pts)-2] = rad * 0.8
		radii[len(pts)-1] = rad * 0.15
		m.ribTube(pts, radii, ribs, 0.14, tone, partSolid)
	}
	switch variant {
	case 0: // saguaro
		h := r.f(3.0, 3.8)
		rad := r.f(0.2, 0.25)
		steps := 5
		if lod > 0 {
			steps = 2
		}
		var pts []mgl32.Vec3
		for i := 0; i <= steps; i++ {
			pts = append(pts, yUp.Mul(h*float32(i)/float32(steps)))
		}
		column(pts, rad)
		arms := 1 + r.Intn(3)
		a0 := r.f(0, 2*math.Pi)
		for k := 0; k < arms; k++ {
			out := yawDir(a0 + float32(k)*2*math.Pi/float32(arms) + r.f(-0.4, 0.4))
			y := h * r.f(0.35, 0.6)
			reach := r.f(0.45, 0.65)
			up := r.f(0.8, 1.4)
			ar := rad * r.f(0.6, 0.75)
			base := yUp.Mul(y)
			elbow := base.Add(out.Mul(reach)).Add(yUp.Mul(0.12))
			var apts []mgl32.Vec3
			if lod == 0 {
				apts = []mgl32.Vec3{base, base.Add(out.Mul(reach * 0.6)), elbow, elbow.Add(yUp.Mul(ar * 1.2)), elbow.Add(yUp.Mul(up * 0.6)), elbow.Add(yUp.Mul(up))}
			} else {
				apts = []mgl32.Vec3{base, elbow, elbow.Add(yUp.Mul(up))}
			}
			column(apts, ar)
		}
	case 1: // barrel, crowned with flowers
		h := r.f(0.5, 0.75)
		prof := []float32{0.3, 0.38, 0.4, 0.36, 0.26, 0.08}
		var pts []mgl32.Vec3
		var radii, tone []float32
		for i, p := range prof {
			if lod > 0 && i%2 == 1 {
				continue
			}
			t := float32(i) / float32(len(prof)-1)
			pts = append(pts, yUp.Mul(h*t))
			radii = append(radii, p)
			tone = append(tone, 0.7+0.35*t)
		}
		if lod == 0 {
			ribs = 11
		}
		m.ribTube(pts, radii, ribs, 0.18, tone, partSolid)
		if lod == 0 {
			for i := 0; i < 6; i++ {
				c := yUp.Mul(h * 0.95).Add(yawDir(float32(i) / 6 * 2 * math.Pi).Mul(0.07))
				m.tube([]mgl32.Vec3{c, c.Add(yUp.Mul(0.05)), c.Add(yUp.Mul(0.09))}, []float32{0.012, 0.03, 0.045}, []float32{0, 0, 0}, []float32{0.8, 1, 1.1}, 5, partAccent)
			}
		}
	default: // prickly pear: flat paddles, each growing off another's rim
		type pad struct {
			base, up, face mgl32.Vec3
			size           float32
		}
		var pads []pad
		var grow func(p pad, depth int)
		sub := 1
		if lod > 0 {
			sub = 0
		}
		grow = func(p pad, depth int) {
			pads = append(pads, p)
			if depth == 0 {
				return
			}
			kids := 1 + r.Intn(2)
			for k := 0; k < kids; k++ {
				right := p.face.Cross(p.up).Normalize()
				tilt := r.f(-0.7, 0.7)
				up := rotAxis(p.up, p.face, tilt).Add(yUp.Mul(0.4)).Normalize()
				face := rotAxis(p.face, up, r.f(-0.8, 0.8))
				base := p.base.Add(p.up.Mul(p.size * 1.35)).Add(right.Mul(tilt * p.size * 0.3))
				grow(pad{base, up, face, p.size * r.f(0.75, 0.9)}, depth-1)
			}
		}
		a := r.f(0, 2*math.Pi)
		grow(pad{mgl32.Vec3{}, yUp, yawDir(a), r.f(0.28, 0.34)}, 2-lod)
		for _, p := range pads {
			// up x face, so (right, up, face) is right-handed: the other way
			// the blob is mirrored and its normals point inward.
			right := p.up.Cross(p.face).Normalize()
			c := p.base.Add(p.up.Mul(p.size * 0.72))
			seed := uint32(r.Int31())
			m.blob(sub, func(d mgl32.Vec3) mgl32.Vec3 {
				n := 1 + noise3(d.Mul(2), seed)*0.12
				return c.Add(right.Mul(d[0] * p.size * 0.62 * n)).Add(p.up.Mul(d[1] * p.size * 0.75 * n)).Add(p.face.Mul(d[2] * p.size * 0.1))
			}, false, partSolid, func(q mgl32.Vec3) float32 { return 0.72 + 0.35*min(q[1]/1.2, 1) }, nil)
			if lod == 0 && r.Float32() < 0.6 {
				// A fruit or two on the rim.
				f := c.Add(p.up.Mul(p.size * 0.78)).Add(right.Mul(r.f(-0.3, 0.3) * p.size))
				m.blob(0, func(d mgl32.Vec3) mgl32.Vec3 { return f.Add(d.Mul(0.045)) }, false, partAccent,
					func(mgl32.Vec3) float32 { return 1 }, nil)
			}
		}
	}
	return m.v
}

func buildAcacia(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(18000 + variant*131 + lod)))}
	m := &fmesh{}
	fork := r.f(1.0, 1.6)
	top := r.f(3.6, 4.4)
	sides := 6
	if lod > 0 {
		sides = 4
	}
	lean := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0.05, 0.25))
	forkP := yUp.Mul(fork).Add(lean)
	m.tube([]mgl32.Vec3{{}, forkP.Mul(0.5).Add(mgl32.Vec3{r.f(-0.05, 0.05), 0, r.f(-0.05, 0.05)}), forkP},
		[]float32{0.2, 0.15, 0.13}, []float32{0, 0.01, 0.03}, []float32{0.55, 0.65, 0.7}, sides, partBark)
	// Limbs fan out from the fork, rising steeply then leaning out, each
	// ending in a flat pad of foliage; the pads run together into the wide,
	// flat-topped canopy an acacia is known by.
	limbs := 3 + variant%2
	a0 := r.f(0, 2*math.Pi)
	type clump struct {
		c mgl32.Vec3
		r float32
	}
	var clumps []clump
	for i := 0; i < limbs; i++ {
		out := yawDir(a0 + float32(i)/float32(limbs)*2*math.Pi + r.f(-0.35, 0.35))
		reach := r.f(1.3, 2.0)
		end := yUp.Mul(top + r.f(-0.35, 0.25)).Add(out.Mul(reach))
		mid := forkP.Add(end.Sub(forkP).Mul(0.5)).Add(out.Mul(-0.2)).Add(yUp.Mul(0.15))
		bs := 5
		if lod > 0 {
			bs = 3
		}
		m.tube([]mgl32.Vec3{forkP, mid, end}, []float32{0.1, 0.07, 0.035}, []float32{0.03, 0.08, 0.15}, []float32{0.6, 0.7, 0.8}, bs, partBark)
		clumps = append(clumps, clump{end.Add(yUp.Mul(0.2)), r.f(1.4, 1.9)})
		if lod == 0 && variant == 2 {
			// A lower tier off the limb, the layered look of an old tree.
			lt := forkP.Add(end.Sub(forkP).Mul(0.55))
			le := lt.Add(rotAxis(out, yUp, r.f(-0.8, 0.8)).Mul(r.f(0.8, 1.1))).Sub(yUp.Mul(0.1))
			m.tube([]mgl32.Vec3{lt, le}, []float32{0.04, 0.02}, []float32{0.08, 0.15}, []float32{0.65, 0.75}, 3, partBark)
			clumps = append(clumps, clump{le.Add(yUp.Mul(0.1)), r.f(0.8, 1.1)})
		}
	}
	var centre mgl32.Vec3
	for _, c := range clumps {
		centre = centre.Add(c.c)
	}
	centre = centre.Mul(1 / float32(len(clumps)))
	cards, size := 22, float32(0.95)
	if lod > 0 {
		cards, size = 7, 1.7
	}
	for _, cl := range clumps {
		n := int(float32(cards) * cl.r / 1.6)
		for i := 0; i < n; i++ {
			d := yawDir(r.f(0, 2*math.Pi)).Mul(cl.r * float32(math.Sqrt(float64(r.f(0, 1)))))
			edge := d.Len() / cl.r
			// A pad is thin — thickest in its middle, domed a little on top.
			y := r.f(-0.22, 0.3) * (1 - 0.5*edge*edge)
			p := cl.c.Add(d).Add(yUp.Mul(y))
			a := r.f(0, 2*math.Pi)
			right := yawDir(a)
			along := yawDir(a + math.Pi/2).Add(yUp.Mul(r.f(0.05, 0.45))).Normalize()
			nrm := p.Sub(centre).Add(yUp.Mul(2.2)).Normalize()
			tone := 0.55 + 0.5*min(max((y+0.22)/0.5, 0), 1)
			s := size * r.f(0.85, 1.15)
			m.card(p.Sub(along.Mul(s*0.5)), right.Mul(s*0.5), along.Mul(s), tileCluster, partLeaf, nrm, 0.2, 0.28, tone)
		}
	}
	return m.v
}

func buildJunglePlant(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(19000 + variant*131 + lod)))}
	m := &fmesh{}
	leaves, segs := 7, 4
	if lod > 0 {
		leaves, segs = 4, 2
	}
	base := mgl32.Vec3{}
	if variant == 1 {
		// A banana's pseudostem, the leaves bursting from its top.
		h := r.f(0.7, 1.0)
		sides := 6 - 2*lod
		m.tube([]mgl32.Vec3{{}, yUp.Mul(h * 0.5), yUp.Mul(h)}, []float32{0.09, 0.075, 0.06}, []float32{0, 0.02, 0.05}, []float32{0.6, 0.7, 0.8}, sides, partSolid)
		base = yUp.Mul(h)
		leaves = 6 - 2*lod
	}
	for i := 0; i < leaves; i++ {
		a := float32(i)/float32(leaves)*2*math.Pi + r.f(-0.3, 0.3)
		out := yawDir(a)
		side := yawDir(a + math.Pi/2)
		if variant == 0 {
			// Elephant ear: a long stalk arching out, a broad, heart-shaped
			// blade hanging from its end.
			sl := r.f(0.55, 1.0)
			lean := r.f(0.3, 0.7)
			up := yUp.Add(out.Mul(lean)).Normalize()
			ssegs := 3 - lod
			m.blade(base, up, out, sl, 0.04, 0.35, ssegs, partSolid, 0.6, 0.9, 0.5)
			tip := base.Add(up.Mul(sl)).Add(out.Mul(0.35 * sl))
			ll := r.f(0.7, 1.05)
			droop := r.f(0.25, 0.6)
			pt := func(t float32) mgl32.Vec3 {
				return tip.Add(out.Mul(ll * t)).Add(yUp.Mul(ll * (0.15*t - droop*t*t)))
			}
			// tileLeaf's blade is half the card wide, its widest a third up.
			w := func(t float32) float32 { return ll * 0.7 }
			fold := float32(0.12)
			if lod > 0 {
				fold = 0
			}
			m.frond(pt, w, side, segs, tileLeaf, fold, 0.2, 0.5)
			continue
		}
		// Banana: long paddles rising, then arching over.
		ll := r.f(1.3, 1.8)
		rise := r.f(0.7, 1.2)
		pt := func(t float32) mgl32.Vec3 {
			return base.Add(out.Mul(ll * t * 0.85)).Add(yUp.Mul(ll * (rise*t - 1.05*t*t)))
		}
		w := func(t float32) float32 { return ll * 0.32 }
		fold := float32(0.15)
		if lod > 0 {
			fold = 0
		}
		m.frond(pt, w, side, segs+1, tileLeaf, fold, 0.15, 0.55)
	}
	return m.v
}

func buildDeadBush(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(20000 + variant*131 + lod)))}
	m := &fmesh{}
	stems, depth := 6, 2
	if lod > 0 {
		stems, depth = 4, 1
	}
	var tips []mgl32.Vec3
	var grow func(p, dir mgl32.Vec3, l, rad float32, d int)
	grow = func(p, dir mgl32.Vec3, l, rad float32, d int) {
		// A crooked twig: two segments with a kink.
		kink := dir.Add(mgl32.Vec3{r.f(-0.3, 0.3), r.f(-0.1, 0.2), r.f(-0.3, 0.3)}).Normalize()
		mid := p.Add(dir.Mul(l * 0.5))
		end := mid.Add(kink.Mul(l * 0.5))
		sw := func(q mgl32.Vec3) float32 { return min(q.Y()*0.1, 0.2) }
		m.tube([]mgl32.Vec3{p, mid, end}, []float32{rad, rad * 0.8, rad * 0.55}, []float32{sw(p), sw(mid), sw(end)},
			[]float32{0.6 + 0.3*p.Y(), 0.65 + 0.3*mid.Y(), 0.7 + 0.3*end.Y()}, 3, partSolid)
		if d == 0 {
			tips = append(tips, end)
			return
		}
		for k := 0; k < 2; k++ {
			nd := rotAxis(kink, yUp, r.f(-1.2, 1.2)).Add(yUp.Mul(r.f(0.1, 0.5))).Normalize()
			grow(end, nd, l*r.f(0.55, 0.75), rad*0.6, d-1)
		}
	}
	for i := 0; i < stems; i++ {
		a := float32(i)/float32(stems)*2*math.Pi + r.f(-0.3, 0.3)
		dir := yawDir(a).Mul(r.f(0.4, 0.9)).Add(yUp).Normalize()
		grow(yawDir(a).Mul(r.f(0, 0.05)), dir, r.f(0.35, 0.5), 0.022, depth)
	}
	// Sagebrush keeps a few tufts of grey leaves at its tips.
	if variant == 1 {
		for i, tp := range tips {
			if lod > 0 && i%2 == 1 {
				continue
			}
			m.leafSpray(r, tp, 2-lod, r.f(0.2, 0.28)*float32(1+lod), tileCluster, partAccentCard, mgl32.Vec3{0, 0.5, 0}, 0.6, 0.2)
		}
	}
	return m.v
}

func buildIce(variant, lod int) []float32 {
	r := frand{rand.New(rand.NewSource(int64(21000 + variant*131 + lod)))}
	m := &fmesh{}
	sub := 1
	if lod > 0 {
		sub = 0
	}
	switch variant {
	case 0:
		m.iceChunk(mgl32.Vec3{}, mgl32.Vec3{0.55, 0.42, 0.5}, mgl32.Vec3{}, uint32(r.Int31()), sub)
	case 1:
		// A serac: a tall slab of ice leaning out over its foot, with a
		// broken block shouldering it — a slab, not a spike, because ice
		// splits along planes.
		lean := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0.12, 0.25))
		m.iceChunk(mgl32.Vec3{0, 0.2, 0}, mgl32.Vec3{0.62, r.f(1.9, 2.4), 0.42}, lean, uint32(r.Int31()), sub)
		m.iceChunk(yawDir(r.f(0, 2*math.Pi)).Mul(0.6), mgl32.Vec3{0.5, 0.55, 0.45}, mgl32.Vec3{}, uint32(r.Int31()), 0)
	default:
		n := 6
		if lod > 0 {
			n = 3
		}
		for i := 0; i < n; i++ {
			c := yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0, 0.8))
			s := r.f(0.12, 0.3)
			// Broken pieces, some tipped up on edge.
			tall := s
			if r.f(0, 1) < 0.4 {
				tall = s * 2.2
			}
			m.iceChunk(c, mgl32.Vec3{s * 1.3, tall, s * 0.8}, yawDir(r.f(0, 2*math.Pi)).Mul(r.f(0, 0.3)), uint32(r.Int31()), 0)
		}
	}
	return m.v
}

// iceChunk is a faceted, flat-shaded lump of ice — quantised toward a few
// planes rather than left round like stone — size across, up and deep,
// sheared by lean per unit of height so a serac can lean out over its foot.
func (m *fmesh) iceChunk(c, size, lean mgl32.Vec3, seed uint32, sub int) {
	m.blob(sub, func(d mgl32.Vec3) mgl32.Vec3 {
		n := noise3(d.Mul(1.3), seed)*0.35 + noise3(d.Mul(3.1), seed+1)*0.1
		p := d.Mul(0.8 + n)
		y := max(p[1], -0.25) * size[1]
		return c.Add(mgl32.Vec3{p[0] * size[0], y, p[2] * size[2]}).Add(lean.Mul(y))
	}, true, partSolid, func(p mgl32.Vec3) float32 {
		// Brighter up top where snow and light catch it, bluer and darker
		// low down.
		return 0.8 + 0.35*min(max((p[1]-c[1])/max(size[1], 0.01), 0), 1)
	}, nil)
}

// buildClump is ground cover's far tier: k cards crossed about the growth
// axis, each halfW either side of it and h tall, showing tile. They shade as
// the ground does (their normal is up), as a tuft this far off is a patch of
// the meadow, not a thing standing on it.
func buildClump(variant, k int, halfW, h float32, tile, part int, sway float32) []float32 {
	m := &fmesh{}
	turn := float32(variant) * 0.7
	for i := 0; i < k; i++ {
		a := turn + float32(i)*math.Pi/float32(k)
		// A little darker than a blade: a tuft's inside is in its own shade,
		// and the card shows the whole tuft, inside and all.
		m.card(mgl32.Vec3{}, yawDir(a).Mul(halfW), yUp.Mul(h), tile, part, yUp, 0, sway, 0.85)
	}
	return m.v
}

// buildFoliageTemplate builds one shape's variant at one level of detail.
func buildFoliageTemplate(shape world.FoliageShape, variant, lod int) []float32 {
	if lod == 2 {
		switch shape {
		case world.FoliageGrass:
			return buildClump(variant, 2, 0.34, 0.55, tileGrassClump, partLeaf, 0.6)
		case world.FoliageTallGrass:
			return buildClump(variant, 2, 0.6, 1.25, tileTallGrassClump, partLeaf, 0.8)
		case world.FoliageFlower:
			m := buildClump(variant, 2, 0.24, 0.45, tileGrassClump, partLeaf, 0.6)
			return append(m, buildClump(variant+1, 2, 0.24, 0.7, tileFlowerDots, partAccentCard, 0.6)...)
		case world.FoliageReed:
			return buildClump(variant, 2, 0.42, 1.9, tileReedClump, partLeaf, 0.4)
		case world.FoliageFern:
			m := &fmesh{}
			for i := 0; i < 3; i++ {
				d := yawDir(float32(variant)*0.9 + float32(i)*2*math.Pi/3)
				up := d.Mul(0.55).Add(yUp.Mul(0.5)).Normalize().Mul(0.82)
				m.card(mgl32.Vec3{}, d.Cross(yUp).Normalize().Mul(0.22), up, tileFern, partLeaf, yUp, 0, 0.5, 1)
			}
			return m.v
		}
		lod = 1
	}
	switch shape {
	case world.FoliageGrass:
		return buildGrass(variant, lod)
	case world.FoliageTallGrass:
		return buildTallGrass(variant, lod)
	case world.FoliageFlower:
		return buildFlower(variant, lod)
	case world.FoliageFern:
		return buildFern(variant, lod)
	case world.FoliageShrub:
		return buildShrub(variant, lod)
	case world.FoliageBroadleaf:
		return buildBroadleaf(variant, lod)
	case world.FoliageConifer:
		return buildConifer(variant, lod)
	case world.FoliageRock:
		return buildRock(variant, lod)
	case world.FoliagePebbles:
		return buildPebbles(variant, lod)
	case world.FoliageMoss:
		return buildMoss(variant, lod)
	case world.FoliageVine:
		return buildVine(variant, lod)
	case world.FoliageReed:
		return buildReed(variant, lod)
	case world.FoliageMushroom:
		return buildMushroom(variant, lod)
	case world.FoliageLitter:
		return buildLitter(variant, lod)
	case world.FoliageDeadwood:
		return buildDeadwood(variant, lod)
	case world.FoliagePalm:
		return buildPalm(variant, lod)
	case world.FoliageCactus:
		return buildCactus(variant, lod)
	case world.FoliageAcacia:
		return buildAcacia(variant, lod)
	case world.FoliageJunglePlant:
		return buildJunglePlant(variant, lod)
	case world.FoliageDeadBush:
		return buildDeadBush(variant, lod)
	case world.FoliageIce:
		return buildIce(variant, lod)
	case world.FoliageSitkaSpruce:
		return buildSitkaSpruce(variant, lod)
	case world.FoliageHemlock:
		return buildHemlock(variant, lod)
	case world.FoliageRedcedar:
		return buildRedcedar(variant, lod)
	case world.FoliageDouglasFir:
		return buildDouglasFir(variant, lod)
	case world.FoliageBigleafMaple:
		return buildBigleafMaple(variant, lod)
	case world.FoliageRedAlder:
		return buildRedAlder(variant, lod)
	case world.FoliageVineMaple:
		return buildVineMaple(variant, lod)
	case world.FoliageCottonwood:
		return buildCottonwood(variant, lod)
	case world.FoliageSwordFern:
		return buildSwordFern(variant, lod)
	case world.FoliageForestFern:
		return buildForestFern(variant, lod)
	case world.FoliageOxalis:
		return buildOxalis(variant, lod)
	case world.FoliageUnderShrub:
		return buildUnderShrub(variant, lod)
	case world.FoliageNurseLog:
		return buildNurseLog(variant, lod)
	case world.FoliageBranchLitter:
		return buildBranchLitter(variant, lod)
	case world.FoliageForestLitter:
		return buildForestLitter(variant, lod)
	case world.FoliageMossMound:
		return buildMossMound(variant, lod)
	}
	return nil
}
