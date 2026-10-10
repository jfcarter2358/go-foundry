package render

// foliage_rainforest_kit.go — what the temperate rainforest's plants
// (foliage_rainforest_trees.go, foliage_rainforest_floor.go) are built from.
//
// # Detail in the geometry
//
// Foliage is lit per vertex and bark has no texture: a trunk is its colour
// times its tone times its light. So the detail of a rainforest giant — a
// Douglas-fir's deep, braided furrows, a spruce's scaly plates, a redcedar's
// stringy strips and fluted foot, the moss lying thick along the top of a
// maple's limbs — is modelled: rfTube is a tube of many sides and rings
// whose surface stands out from its round by a RELIEF (rfBark, rfMoss,
// buttress flutes), with the cavity of every furrow baked into its tone. The
// Nanite path (nfoliage_asset.go) makes that affordable: up close the
// triangles, further off the cluster DAG's simplified levels and voxels.
//
// # One plant, every level of detail
//
// The classic generators seed their far mesh differently from their near
// one, so the two are different plants. A shadow proxy (the far mesh, cast
// for the Nanite path's plant) and a CPU-path tree must be the tree on screen,
// so these are built as an rfPlant — every limb and card, from one seed —
// and only then written out: in full at lod 0, and at lod 1 with the twigs
// left out, fewer sides, no relief, and one card in several grown to cover
// for the rest.
//
// # Bones
//
// foliage_assembly.go makes a bone of each axis primitive in the order they
// are written, up to foliageMaxBones. rfPlant writes its limbs by depth, the
// biggest of each depth first — trunk sections, then the main limbs, then
// branches — so the bones a giant gets are its trunk and its main limbs, and
// every twig rides the limb it grows from.

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// rfRelief is how a tube's surface stands out from its round: at angle a
// round it, s units along it, facing out (unit, the plant's frame), how far
// out as a fraction of the radius, and the tone factor there (a furrow's
// cavity is darker).
type rfRelief func(a, s float32, out mgl32.Vec3) (disp, shade float32)

// rfTube is one tube: a smooth polyline of rings.
type rfTube struct {
	pts               []mgl32.Vec3
	radii, sway, tone []float32
	sides             int
	part              int
	relief            rfRelief
	// cap closes the far end in a short cone.
	cap bool
	// s0 is how far along its whole limb the tube starts, so a trunk built
	// in sections keeps one bark.
	s0 float32
	// bone makes it an axis (a bone of its own) rather than riding, whole,
	// the bone nearest its middle.
	bone bool
}

// rfFrames is a parallel-transport frame along pts: tangent, and the normal
// and binormal a ring is laid out in. Transported rather than built from a
// fixed reference, so a limb that bends past vertical does not twist.
func rfFrames(pts []mgl32.Vec3) (tan, nrm, bin []mgl32.Vec3) {
	n := len(pts)
	tan = make([]mgl32.Vec3, n)
	nrm = make([]mgl32.Vec3, n)
	bin = make([]mgl32.Vec3, n)
	for i := range pts {
		var d mgl32.Vec3
		switch {
		case i == 0:
			d = pts[1].Sub(pts[0])
		case i == n-1:
			d = pts[i].Sub(pts[i-1])
		default:
			d = pts[i+1].Sub(pts[i-1])
		}
		if d.Len() < 1e-7 {
			d = yUp
		}
		tan[i] = d.Normalize()
	}
	ref := mgl32.Vec3{1, 0, 0}
	if math.Abs(float64(tan[0].X())) > 0.9 {
		ref = mgl32.Vec3{0, 0, 1}
	}
	nrm[0] = ref.Sub(tan[0].Mul(ref.Dot(tan[0]))).Normalize()
	for i := 1; i < n; i++ {
		p := nrm[i-1].Sub(tan[i].Mul(nrm[i-1].Dot(tan[i])))
		if p.Len() < 1e-5 {
			p = nrm[i-1]
		}
		nrm[i] = p.Normalize()
	}
	for i := range pts {
		bin[i] = tan[i].Cross(nrm[i])
	}
	return
}

// rfTube writes t: rings of t.sides points displaced by its relief, normals
// from the displaced surface itself (so every furrow catches the light).
func (m *fmesh) rfTube(t rfTube) {
	if len(t.pts) < 2 {
		return
	}
	start := m.mark()
	n, sides := len(t.pts), t.sides
	tan, nrm, bin := rfFrames(t.pts)
	pos := make([][]mgl32.Vec3, n)
	shade := make([][]float32, n)
	s := t.s0
	for i := 0; i < n; i++ {
		if i > 0 {
			s += t.pts[i].Sub(t.pts[i-1]).Len()
		}
		pos[i] = make([]mgl32.Vec3, sides)
		shade[i] = make([]float32, sides)
		r := max(t.radii[i], 0.0015)
		for j := 0; j < sides; j++ {
			a := float32(j) / float32(sides) * 2 * math.Pi
			out := nrm[i].Mul(float32(math.Cos(float64(a)))).Add(bin[i].Mul(float32(math.Sin(float64(a)))))
			d, sh := float32(0), float32(1)
			if t.relief != nil {
				d, sh = t.relief(a, s, out)
			}
			pos[i][j] = t.pts[i].Add(out.Mul(r * max(1+d, 0.05)))
			shade[i][j] = sh
		}
	}
	norm := func(i, j int) mgl32.Vec3 {
		jp, jm := (j+1)%sides, (j+sides-1)%sides
		ip, im := min(i+1, n-1), max(i-1, 0)
		around := pos[i][jp].Sub(pos[i][jm])
		along := pos[ip][j].Sub(pos[im][j])
		nn := along.Cross(around)
		out := pos[i][j].Sub(t.pts[i])
		if nn.Dot(out) < 0 {
			nn = nn.Mul(-1)
		}
		if nn.Len() < 1e-9 {
			return out.Normalize()
		}
		return nn.Normalize()
	}
	vs := make([][]fvert, n)
	for i := 0; i < n; i++ {
		vs[i] = make([]fvert, sides)
		for j := 0; j < sides; j++ {
			vs[i][j] = fvert{p: pos[i][j], n: norm(i, j), sway: t.sway[i], tone: t.tone[i] * shade[i][j], part: t.part}
		}
	}
	for i := 1; i < n; i++ {
		for j := 0; j < sides; j++ {
			j2 := (j + 1) % sides
			m.quad(vs[i-1][j], vs[i-1][j2], vs[i][j2], vs[i][j])
		}
	}
	if t.cap {
		last := n - 1
		tip := t.pts[last].Add(tan[last].Mul(t.radii[last] * 0.6))
		tv := fvert{p: tip, n: tan[last], sway: t.sway[last], tone: t.tone[last] * 0.8, part: t.part}
		for j := 0; j < sides; j++ {
			m.tri(vs[last][j], vs[last][(j+1)%sides], tv)
		}
	}
	if t.bone {
		m.recordAxis(start, t.pts[0], t.pts[n-1])
	} else {
		// A shell over a limb rides the limb: the bone nearest its middle.
		m.recordRide(start, t.pts[n/2])
	}
}

// ---------- reliefs ----------

// Kinds of bark.
const (
	rfBarkFurrowed = iota // ridges and deep furrows that braid: fir, cottonwood, maple
	rfBarkPlates          // thin scaly plates: spruce
	rfBarkFibrous         // long stringy strips: redcedar
	rfBarkSmooth          // smooth with pale lichen and dark lenticels: alder
	rfBarkScaly           // shallow, flaky ridges: hemlock
)

// rfBark is a bark's relief.
type rfBark struct {
	kind int
	// ridges is how many ridges run round the tube, and depth how far they
	// stand out, as a fraction of the radius.
	ridges float32
	depth  float32
	// twist is how far the ridges wind per unit along.
	twist float32
	// girth is the radius the noise is laid round at, so the bark's grain is
	// the same size up the whole tube.
	girth float32
	seed  uint32
}

// relief is the bark's relief function.
func (b rfBark) relief() rfRelief {
	return func(a, s float32, _ mgl32.Vec3) (float32, float32) {
		ca, sa := float32(math.Cos(float64(a))), float32(math.Sin(float64(a)))
		g := max(b.girth, 0.05)
		at := func(f float32, seed uint32) float32 {
			return noise3(mgl32.Vec3{ca * g * f, sa * g * f, s * f}, seed)
		}
		switch b.kind {
		case rfBarkPlates:
			// Plates: cells of noise, cracked where it crosses its middle.
			v := at(4.4, b.seed)*0.7 + at(9.5, b.seed+1)*0.3
			crack := float32(math.Abs(float64(v-0.5))) * 2
			plate := smoothstep32(0.04, 0.28, crack)
			lift := at(1.1, b.seed+2)
			return b.depth * (plate*(0.6+0.4*lift) - 0.5), 0.45 + 0.65*plate*(0.8+0.4*lift)
		case rfBarkFibrous:
			// Many shallow strips winding up the trunk, wavering.
			w := at(0.35, b.seed)*2.4 + at(1.3, b.seed+1)*0.6
			ph := b.ridges*a + b.twist*s + w
			rd := 0.5 + 0.5*float32(math.Cos(float64(ph)))
			rd2 := 0.5 + 0.5*float32(math.Cos(float64(ph*2.7+w*3)))
			ridge := rd*0.7 + rd2*0.3
			return b.depth * (ridge - 0.5), 0.55 + 0.6*ridge
		case rfBarkSmooth:
			// Smooth, the faintest grain; pale crusts of lichen and dark
			// horizontal lenticels are in the tone.
			lichen := smoothstep32(0.55, 0.7, at(0.9, b.seed)*0.7+at(3.3, b.seed+1)*0.3)
			dots := float32(1)
			if float32(math.Sin(float64(s*38+at(0.8, b.seed+3)*9))) > 0.93 && at(4, b.seed+2) > 0.55 {
				dots = 0.78
			}
			return b.depth * (at(3, b.seed+4) - 0.5), (0.9 + 0.32*lichen) * dots
		case rfBarkScaly:
			v := at(3.2, b.seed)
			ph := b.ridges*a + b.twist*s + at(0.6, b.seed+1)*3
			rd := 0.5 + 0.5*float32(math.Cos(float64(ph)))
			ridge := rd*0.55 + v*0.45
			return b.depth * (ridge - 0.5), 0.68 + 0.4*ridge
		}
		// Furrowed: two sets of ridges, slightly different in count and wind,
		// the higher of them taken — so ridges meet, merge and part again.
		w := at(0.4, b.seed) * 3
		ph1 := b.ridges*a + b.twist*s + w
		ph2 := (b.ridges+1)*a - b.twist*0.6*s + w*1.3 + 1.7
		r1 := 0.5 + 0.5*float32(math.Cos(float64(ph1)))
		r2 := 0.5 + 0.5*float32(math.Cos(float64(ph2)))
		ridge := float32(math.Pow(float64(max(r1, r2)), 2.2))
		// Cross-checks: the ridges break into blocks.
		chk := smoothstep32(0.15, 0.35, at(1.6, b.seed+2))
		ridge *= 0.75 + 0.25*chk
		return b.depth * (ridge - 0.45), 0.38 + 0.78*ridge
	}
}

// rfButtress flares a trunk's foot into lobes: lobes of them round it, the
// foot out by amount (fraction of the radius) at the ground, falling off over
// height units up.
func rfButtress(lobes int, amount, height float32, seed uint32) rfRelief {
	phase := float32(seed%628) / 100
	return func(a, s float32, _ mgl32.Vec3) (float32, float32) {
		if s >= height {
			return 0, 1
		}
		f := 1 - s/height
		f = f * f * f
		// Lobes with sharp ridges and rounded hollows, uneven in size.
		v := 0.5 + 0.5*float32(math.Cos(float64(float32(lobes)*a+phase+0.6*float32(math.Sin(float64(a*2+phase))))))
		v = float32(math.Pow(float64(v), 2.5))
		size := 0.7 + 0.6*noise3(mgl32.Vec3{float32(math.Cos(float64(a))) * 2, float32(math.Sin(float64(a))) * 2, 0}, seed)
		return amount * f * (0.25 + v*size), 1 - 0.25*f*(1-v)
	}
}

// rfMoss is a moss shell's relief, for a tube laid over a limb's own path: it
// lies thick where the limb faces up (where moss collects) and on the
// shaded side, ragged and lumpy, and sinks inside the bark where there is
// none. amount scales it; seed varies it.
func rfMoss(amount float32, seed uint32) rfRelief {
	return func(a, s float32, out mgl32.Vec3) (float32, float32) {
		ca, sa := float32(math.Cos(float64(a))), float32(math.Sin(float64(a)))
		p := mgl32.Vec3{ca * 1.3, sa * 1.3, s * 1.4}
		lump := noise3(p, seed)*0.65 + noise3(p.Mul(3.1), seed+1)*0.35
		cover := out.Y()*0.9 + out.X()*0.15 + (lump-0.5)*1.1 + 0.25
		th := smoothstep32(-0.1, 0.45, cover)
		// The tone is speckled finer than the lumps: moss is a crowd of tufts.
		fine := noise3(p.Mul(7), seed+2) - 0.5
		return -0.14 + amount*th*(0.22+0.5*lump+0.08*fine), 0.72 + 0.35*lump + 0.4*fine
	}
}

// rfSum adds reliefs: displacements add, shades multiply.
func rfSum(rs ...rfRelief) rfRelief {
	return func(a, s float32, out mgl32.Vec3) (float32, float32) {
		d, sh := float32(0), float32(1)
		for _, r := range rs {
			if r == nil {
				continue
			}
			dd, ss := r(a, s, out)
			d += dd
			sh *= ss
		}
		return d, sh
	}
}

func smoothstep32(e0, e1, x float32) float32 {
	t := min(max((x-e0)/(e1-e0), 0), 1)
	return t * t * (3 - 2*t)
}

// ---------- sheets ----------

// sheet is a card bent in three dimensions: a grid nu x nv across the whole
// of tile, its point at (u in [-1,1], v in [0,1]) given by at. Normals come
// from the bent surface, turned toward up. A curled leaf on the ground, a
// devil's club leaf drooping at its edges.
func (m *fmesh) sheet(nu, nv, tile, part int, at func(u, v float32) mgl32.Vec3, up mgl32.Vec3, sway0, sway1, tone float32) {
	start := m.mark()
	g := make([][]mgl32.Vec3, nv+1)
	for j := 0; j <= nv; j++ {
		g[j] = make([]mgl32.Vec3, nu+1)
		for i := 0; i <= nu; i++ {
			g[j][i] = at(-1+2*float32(i)/float32(nu), float32(j)/float32(nv))
		}
	}
	vert := func(i, j int) fvert {
		ip, im := min(i+1, nu), max(i-1, 0)
		jp, jm := min(j+1, nv), max(j-1, 0)
		nn := g[j][ip].Sub(g[j][im]).Cross(g[jp][i].Sub(g[jm][i]))
		if nn.Dot(up) < 0 {
			nn = nn.Mul(-1)
		}
		if nn.Len() < 1e-9 {
			nn = up
		}
		u, v := float32(i)/float32(nu), float32(j)/float32(nv)
		return fvert{p: g[j][i], n: nn.Normalize(), uv: tileUV(tile, u, v), sway: sway0 + (sway1-sway0)*v,
			tone: tone * (0.88 + 0.12*v), part: part}
	}
	for j := 1; j <= nv; j++ {
		for i := 1; i <= nu; i++ {
			m.quad(vert(i-1, j-1), vert(i, j-1), vert(i, j), vert(i-1, j))
		}
	}
	m.recordOther(start)
}

// ---------- plants ----------

// rfLimb is one limb of a plant: a trunk section, a branch, a twig, a root.
type rfLimb struct {
	pts          []mgl32.Vec3
	radii        []float32
	sway0, sway1 float32
	tone0, tone1 float32
	depth        int
	part         int
	// sides is its sides at full detail.
	sides  int
	relief rfRelief
	// moss lays a moss shell over it (rfMoss) this thick; 0 none.
	moss float32
	// s0 is how far up its trunk a trunk section starts (rfTube.s0).
	s0  float32
	cap bool
	// order sorts limbs of one depth: smaller first. Defaults to minus
	// the limb's length, so the biggest come first.
	order float32
}

// rfCard is one card of a plant: a spray, a leaf, a drape of moss.
type rfCard struct {
	base, right, up, n mgl32.Vec3
	tile, part         int
	sway0, sway1, tone float32
}

// rfPlant is a plant built and not yet written: every limb and card.
type rfPlant struct {
	limbs []rfLimb
	cards []rfCard
	// extra writes anything else (cones, stones, a stump's broken top),
	// given the level of detail.
	extra []func(m *fmesh, lod int)
	// farDepth is the deepest limb the far mesh keeps; farEvery how many
	// cards each far card stands for, and farArea how much of their area it
	// keeps (0 reads as rfFarArea). The far mesh is also the Nanite path's
	// shadow proxy, cast for every tree in a cascade: a crown's cards lie
	// many deep, and the shadow and the far view need only its outline, so
	// a giant keeps a fraction of its card area rather than all of it in
	// fewer, bigger cards — that fill is what a forest's shadow costs.
	farDepth, farEvery int
	farArea            float32
	// mossSeed varies the moss shells, and mossTop, when set, is how high
	// up its trunk the moss reaches: it thins out over the top half of that.
	mossSeed uint32
	mossTop  float32
}

// rfFarArea is how much of its cards' area a far mesh keeps by default.
const rfFarArea = 0.72

// add appends a limb along pts, its radius running r0 to r1 (with the
// power taper shaping it), and returns its index.
func (p *rfPlant) add(l rfLimb) int {
	p.limbs = append(p.limbs, l)
	return len(p.limbs) - 1
}

// limbOf builds a limb's rings along pts, tapering from r0 to r1 by power
// taper, swaying from sw0 to sw1 and toned tn0 to tn1.
func limbOf(pts []mgl32.Vec3, r0, r1, taper float32, depth, sides int, sw0, sw1 float32) rfLimb {
	l := rfLimb{pts: pts, depth: depth, sides: sides, sway0: sw0, sway1: sw1, tone0: 0.62, tone1: 0.82, part: partBark}
	n := len(pts)
	for i := range pts {
		t := float32(i) / float32(max(n-1, 1))
		l.radii = append(l.radii, r1+(r0-r1)*float32(math.Pow(float64(1-t), float64(taper))))
	}
	return l
}

// length is a limb's length along its path.
func (l *rfLimb) length() float32 {
	s := float32(0)
	for i := 1; i < len(l.pts); i++ {
		s += l.pts[i].Sub(l.pts[i-1]).Len()
	}
	return s
}

// at is the point t (0..1 by length) along a limb, its direction there and
// its radius.
func (l *rfLimb) at(t float32) (mgl32.Vec3, mgl32.Vec3, float32) {
	total := l.length()
	want := t * total
	s := float32(0)
	for i := 1; i < len(l.pts); i++ {
		seg := l.pts[i].Sub(l.pts[i-1])
		sl := seg.Len()
		if s+sl >= want || i == len(l.pts)-1 {
			f := float32(0)
			if sl > 0 {
				f = min(max((want-s)/sl, 0), 1)
			}
			p := l.pts[i-1].Add(seg.Mul(f))
			r := l.radii[i-1] + (l.radii[i]-l.radii[i-1])*f
			d := seg
			if d.Len() < 1e-6 {
				d = yUp
			}
			return p, d.Normalize(), r
		}
		s += sl
	}
	return l.pts[0], yUp, l.radii[0]
}

// swayAt is a limb's sway t of the way along it.
func (l *rfLimb) swayAt(t float32) float32 { return l.sway0 + (l.sway1-l.sway0)*t }

// write writes the plant at level of detail lod.
func (p *rfPlant) write(m *fmesh, lod int) {
	idx := make([]int, len(p.limbs))
	keys := make([]float32, len(p.limbs))
	for i := range p.limbs {
		idx[i] = i
		keys[i] = p.limbs[i].order
		if keys[i] == 0 {
			keys[i] = -p.limbs[i].length()
		}
	}
	sort.SliceStable(idx, func(a, b int) bool {
		la, lb := &p.limbs[idx[a]], &p.limbs[idx[b]]
		if la.depth != lb.depth {
			return la.depth < lb.depth
		}
		return keys[idx[a]] < keys[idx[b]]
	})
	// Limbs, as bones in that order; then their moss.
	for _, i := range idx {
		l := &p.limbs[i]
		if lod > 0 && l.depth > p.farDepth {
			continue
		}
		m.rfTube(p.tubeOf(l, lod, false))
	}
	for _, i := range idx {
		l := &p.limbs[i]
		if l.moss <= 0 || (lod > 0 && l.depth > min(p.farDepth, 1)) {
			continue
		}
		m.rfTube(p.tubeOf(l, lod, true))
	}
	every := max(p.farEvery, 1)
	area := p.farArea
	if area <= 0 {
		area = rfFarArea
	}
	grow := float32(math.Sqrt(float64(float32(every) * area)))
	for i, c := range p.cards {
		if lod == 0 {
			m.card(c.base, c.right, c.up, c.tile, c.part, c.n, c.sway0, c.sway1, c.tone)
			continue
		}
		if i%every != 0 {
			continue
		}
		g := max(grow, 1)
		// Grown about its middle, so the far crown keeps its outline.
		mid := c.base.Add(c.up.Mul(0.5))
		m.card(mid.Sub(c.up.Mul(0.5*g)), c.right.Mul(g), c.up.Mul(g), c.tile, c.part, c.n, c.sway0, c.sway1, c.tone)
	}
	for _, f := range p.extra {
		f(m, lod)
	}
}

// tubeOf is limb l as a tube at lod: its bark, or its moss shell.
func (p *rfPlant) tubeOf(l *rfLimb, lod int, moss bool) rfTube {
	pts, radii := l.pts, l.radii
	sides := l.sides
	if lod > 0 {
		// Every third ring (keeping both ends), a sixth of the sides: the
		// far mesh is also the shadow proxy, cast for a whole forest.
		var np []mgl32.Vec3
		var nr []float32
		for i := range pts {
			if i%3 == 0 || i == len(pts)-1 {
				np = append(np, pts[i])
				nr = append(nr, radii[i])
			}
		}
		pts, radii = np, nr
		sides = max(sides/6, 3)
		if l.depth == 0 {
			sides = max(sides, 8)
		}
	}
	n := len(pts)
	t := rfTube{pts: pts, radii: radii, sides: sides, part: l.part, cap: l.cap, bone: !moss, s0: l.s0}
	for i := 0; i < n; i++ {
		f := float32(i) / float32(max(n-1, 1))
		t.sway = append(t.sway, l.sway0+(l.sway1-l.sway0)*f)
		t.tone = append(t.tone, l.tone0+(l.tone1-l.tone0)*f)
	}
	if moss {
		t.part = partAccent
		t.cap = false
		t.sides = max(sides*3/4, 3)
		seed := p.mossSeed + uint32(len(l.pts))*7919 + uint32(l.radii[0]*1000)
		if l.depth == 0 {
			// One trunk, one moss, whatever section it is in.
			seed = p.mossSeed
		}
		t.relief = rfMoss(l.moss, seed)
		if l.depth == 0 && p.mossTop > 0 {
			top, moss := p.mossTop, t.relief
			t.relief = func(a, s float32, out mgl32.Vec3) (float32, float32) {
				d, sh := moss(a, s, out)
				f := 1 - smoothstep32(top*0.5, top, s)
				// Past its reach the shell sinks back inside the bark.
				return -0.14 + (d+0.14)*f, sh
			}
		}
		for i := range t.tone {
			t.tone[i] = 0.75 + 0.2*float32(i)/float32(max(n-1, 1))
		}
		return t
	}
	if lod == 0 {
		t.relief = l.relief
	} else if l.depth == 0 && l.relief != nil {
		// The far trunk keeps its buttresses and its colouring, not its
		// furrows: only the low-frequency half of the relief.
		rel := l.relief
		t.relief = func(a, s float32, out mgl32.Vec3) (float32, float32) {
			d, sh := rel(a, s, out)
			return d * 0.5, 0.5 + 0.5*sh
		}
	}
	return t
}

// pathOf is a smooth path from p0 along dir for length l in segs segments,
// bending by the force f(t) (a direction per unit length) as it goes: a limb
// drooping under its weight and turning up at the tip, a root diving into
// the ground.
func pathOf(p0, dir mgl32.Vec3, l float32, segs int, f func(t float32, d mgl32.Vec3) mgl32.Vec3) []mgl32.Vec3 {
	pts := []mgl32.Vec3{p0}
	d := dir.Normalize()
	step := l / float32(segs)
	p := p0
	for i := 0; i < segs; i++ {
		t := (float32(i) + 0.5) / float32(segs)
		if f != nil {
			d = d.Add(f(t, d).Mul(step)).Normalize()
		}
		p = p.Add(d.Mul(step))
		pts = append(pts, p)
	}
	return pts
}

// perp is a unit vector square to d, turned round d by a.
func perp(d mgl32.Vec3, a float32) mgl32.Vec3 {
	ref := yUp
	if math.Abs(float64(d.Y())) > 0.9 {
		ref = mgl32.Vec3{1, 0, 0}
	}
	s := d.Cross(ref).Normalize()
	return rotAxis(s, d.Normalize(), a)
}

// spray adds a card at base growing along dir, size long and half as wide,
// laid in the plane that holds dir and side; its shading normal blends the
// crown's (out from centre) with up.
func (p *rfPlant) spray(base, dir, side mgl32.Vec3, size float32, tile, part int, centre mgl32.Vec3, sway, tone float32) {
	dir = dir.Normalize()
	side = side.Sub(dir.Mul(side.Dot(dir)))
	if side.Len() < 1e-4 {
		side = perp(dir, 0)
	}
	side = side.Normalize()
	n := base.Add(dir.Mul(size * 0.5)).Sub(centre)
	n = mgl32.Vec3{n[0], n[1] * 0.6, n[2]}.Add(yUp.Mul(max(n.Len()*0.5, 0.3)))
	if n.Len() < 1e-4 {
		n = yUp
	}
	p.cards = append(p.cards, rfCard{base: base.Sub(dir.Mul(size * 0.08)), right: side.Mul(size * 0.5), up: dir.Mul(size),
		n: n.Normalize(), tile: tile, part: part, sway0: sway, sway1: sway + 0.15, tone: tone})
}

// drape hangs a curtain of moss (tileMossDrape, accent coloured) l long from
// point top: two crossed cards turned by a.
func (p *rfPlant) drape(top mgl32.Vec3, l, w, a, sway, tone float32) {
	for k := 0; k < 2; k++ {
		side := yawDir(a + float32(k)*math.Pi/2)
		p.cards = append(p.cards, rfCard{base: top.Sub(yUp.Mul(l)), right: side.Mul(w * 0.5), up: yUp.Mul(l),
			n: side.Cross(yUp).Normalize().Add(yUp.Mul(0.3)).Normalize(), tile: tileMossDrape, part: partAccentCard,
			sway0: sway + 0.35, sway1: sway, tone: tone})
	}
}
