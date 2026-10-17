package rocks

// presets.go — the seven rock forms, each a builder from a variant's seed to
// a signed-distance shape.
//
// What each is modelled on (the forms of the Colorado Plateau, and the
// granite of Joshua Tree and the Sierra foothills):
//
//   - boulder: a block that fell from a cliff and has lain in the talus
//     since — broken out along two to four fracture faces, its edges and
//     corners rounded by weathering, its faces lumpy, grooved and grained,
//     knocked with a few spalls; a sandstone one keeps its bedding (tilted
//     as it landed), a granite one is rounder (spheroidal weathering).
//   - cliff_chunk: a section of a sandstone wall: beds that stand as cliffs
//     over beds that weather back under them (undercuts and alcoves), cut
//     into blocks by a vertical joint set and a blocky fracture network,
//     fluted, chipped. Its back (−Z) and base are flat, so cliff_wall can
//     stack chunks against a wall and on each other.
//   - mesa_cap: caprock — a thick hard bed over a soft one that has
//     weathered back beneath it, its rim broken into blocks by vertical
//     joints, its top flat and pitted.
//   - ledge_slab: a slab of one hard bed broken out along its joints: a flat
//     top and bottom, a polygonal broken outline, bedding lines on its
//     edges.
//   - pillar: a hoodoo — a column of alternating beds, the soft ones necked
//     in, under a wider caprock, on a flared pedestal.
//   - rubble: a talus fragment: an angular block of a few fracture faces,
//     barely rounded.
//   - pebble: a stream-worn pebble: a flattened ellipsoid, smooth.

import (
	"math"
	"sort"

	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/sdf"
)

// preset is one rock form.
type preset struct {
	name, doc string
	// size is the main dimension (metres) and what it measures; triangles
	// the default budget; lithology the default rock.
	size      float64
	sizeOf    string
	triangles int
	lithology string
	// strata and joints are the default bed thickness and joint spacing,
	// aoRadius the occlusion bake's reach, all as fractions of the size.
	strata, joints, aoRadius float64
	sharpness                float64
	// varnish scales the Varnish setting for the form: a tall face streaks,
	// a pebble does not.
	varnish float64
	// back: a flat back on −Z (placed at z = 0); flat: a flat base.
	back, flat bool
	// collision is what a placement of it should collide as.
	collision string
	build     func(b *build) sdf.Shape
}

var presets = map[string]*preset{}

func register(p *preset) { presets[p.name] = p }

// PresetNames is the presets, in order.
func PresetNames() []string {
	return []string{"boulder", "cliff_chunk", "mesa_cap", "ledge_slab", "pillar", "rubble", "pebble"}
}

// PresetDoc is a preset's one-line description, its default size and what
// the size measures, and its default triangle budget.
func PresetDoc(name string) (doc string, size float64, sizeOf string, triangles int) {
	if p := presets[name]; p != nil {
		return p.doc, p.size, p.sizeOf, p.triangles
	}
	return "", 0, "", 0
}

func init() {
	register(&preset{name: "boulder", size: 1.6, sizeOf: "length", triangles: 15000, lithology: "sandstone",
		strata: 0.14, joints: 0.7, aoRadius: 0.12, sharpness: 0.75, collision: "hulls", varnish: 0.8,
		doc: "a weathered block broken out of a bed: fracture faces, rounded edges, spalls", build: buildBoulder})
	register(&preset{name: "cliff_chunk", size: 8, sizeOf: "height", triangles: 600000, lithology: "sandstone",
		strata: 0.16, joints: 0.3, aoRadius: 0.08, sharpness: 0.85, back: true, flat: true, collision: "mesh", varnish: 1.3,
		doc: "a section of cliff: undercut beds, joints, flutes, a flat back to stack against a wall", build: buildCliff})
	register(&preset{name: "mesa_cap", size: 10, sizeOf: "width", triangles: 300000, lithology: "sandstone",
		strata: 0.25, joints: 0.3, aoRadius: 0.06, sharpness: 0.85, flat: true, collision: "mesh", varnish: 1.4,
		doc: "caprock over a weathered-back soft bed, its rim broken into blocks", build: buildMesaCap})
	register(&preset{name: "ledge_slab", size: 4, sizeOf: "length", triangles: 60000, lithology: "sandstone",
		strata: 0.04, joints: 0.4, aoRadius: 0.06, sharpness: 0.8, flat: true, collision: "mesh", varnish: 0.8,
		doc: "a slab of one bed with a broken outline: a ledge or a fallen flagstone", build: buildLedge})
	register(&preset{name: "pillar", size: 7, sizeOf: "height", triangles: 300000, lithology: "sandstone",
		strata: 0.09, joints: 0.3, aoRadius: 0.05, sharpness: 0.7, flat: true, collision: "mesh", varnish: 0.5,
		doc: "a hoodoo: necked beds under a wide caprock, on a flared pedestal", build: buildPillar})
	register(&preset{name: "rubble", size: 0.4, sizeOf: "length", triangles: 3000, lithology: "sandstone",
		strata: 0.3, joints: 0.6, aoRadius: 0.15, sharpness: 0.9, collision: "hulls", varnish: 0.6,
		doc: "an angular talus fragment", build: buildRubble})
	register(&preset{name: "pebble", size: 0.08, sizeOf: "length", triangles: 800, lithology: "sandstone",
		strata: 0.4, joints: 1, aoRadius: 0.2, sharpness: 0, collision: "none", varnish: 0.2,
		doc: "a stream-worn pebble", build: buildPebble})
}

// build is a variant's construction: its settings, its random stream, and
// what the colour bake reads back (the beds, the blocks, the bed frame).
type build struct {
	s    Settings
	pr   *preset
	seed uint64
	rng  *noise.RNG
	size float64
	// strata and bedFrame are the beds and the rotation from the shape's
	// frame into theirs (the identity unless the rock was turned after they
	// were laid); blocks is the fracture network, if any.
	strata   *sdf.Strata
	bedFrame sdf.Mat3
	blocks   *sdf.Joints
	// spacing is the finished mesh's vertex spacing (0 while measuring):
	// what is finer than a few of it is left out.
	spacing float64
	// lamShow is how much of the laminations the colour shows (0..1).
	lamShow float64
}

// minGap is the narrowest crack worth cutting: under a vertex spacing it
// would be a dotted line.
func (b *build) minGap() float64 { return 0.9 * b.spacing }

// resolvable is how well detail of period p (metres) shows at the mesh's
// spacing: 0 under three vertices a period, 1 from six.
func (b *build) resolvable(p float64) float64 {
	if b.spacing <= 0 {
		return 1
	}
	return sdf.SmoothStep(3, 6, p/b.spacing)
}

func newBuild(s Settings, pr *preset, seed uint64) *build {
	b := &build{s: s, pr: pr, seed: seed, rng: noise.NewRNG(noise.Hash64(seed, 0x524f434b)), bedFrame: sdf.Identity}
	j := sdf.Clamp(s.SizeJitter, 0, 0.5)
	b.size = s.Size * (1 + b.r(-j, j))
	return b
}

func (b *build) r(lo, hi float64) float64 { return b.rng.Range(lo, hi) }
func (b *build) next() uint64             { return b.rng.Uint64() }

// dir is a uniform random direction.
func (b *build) dir() sdf.Vec3 {
	z := b.r(-1, 1)
	a := b.r(0, 2*math.Pi)
	r := math.Sqrt(1 - z*z)
	return sdf.Vec3{r * math.Cos(a), z, r * math.Sin(a)}
}

// fieldXZ is a 2D fractal of (x, z), constant in y: an outline in plan.
func fieldXZ(seed uint64, freq float64, octaves int) sdf.Field {
	f := noise.Fractal{Seed: seed, Octaves: octaves, Frequency: freq, Gain: 0.5}
	return sdf.Field{F: func(p sdf.Vec3) float64 { return f.At2(p[0], p[2]) }, Lip: 2.5 * freq * 2}
}

// stretched is a fractal read with each axis scaled by k: flutes when y is
// squeezed, flat lumps when it is stretched.
func stretched(f noise.Fractal, k sdf.Vec3) sdf.Field {
	return sdf.Field{F: func(p sdf.Vec3) float64 { return f.At3(p[0]*k[0], p[1]*k[1], p[2]*k[2]) },
		Lip: sdf.FractalField(f).Lip * k.MaxComp()}
}

// beds makes a strata stack: count beds about thick metres each, hard and
// soft alternating (now and then two of a kind), soft beds weathering back
// inset (each bed its own share of it), with laminations of lam metres
// etched out in patches.
func (b *build) beds(thick, inset, undercut, lam float64, count int) *sdf.Strata {
	st := &sdf.Strata{Inset: inset, Undercut: undercut, Seed: b.next(), BedJitter: 0.6}
	hard := b.rng.Bool()
	for i := 0; i < count; i++ {
		l := sdf.Layer{Thickness: thick * b.r(0.4, 1.7)}
		if hard {
			l.Hardness = b.r(0.7, 1)
		} else {
			l.Hardness = b.r(0.05, 0.5)
		}
		st.Layers = append(st.Layers, l)
		if b.rng.Chance(0.75) {
			hard = !hard
		}
	}
	per := 0.0
	for _, l := range st.Layers {
		per += l.Thickness
	}
	st.Base = -b.r(0, per)
	st.Dip = [2]float64{b.r(-0.04, 0.04), b.r(-0.04, 0.04)}
	w := sdf.FBM(b.next(), 0.25/thick, 3, 0.5)
	st.Warp, st.WarpAmp = &w, thick*0.12
	v := sdf.FBM(b.next(), 0.4/thick, 3, 0.5)
	st.Variation, st.VariationAmt = &v, 0.6
	if lam > 0 {
		period := sdf.Clamp(thick*0.1, 0.008, 0.12)
		patch := sdf.FBM(b.next(), 0.6/thick, 2, 0.5)
		b.lamShow = b.resolvable(period)
		st.Lamination = lam * b.lamShow
		st.LamPeriod = period
		st.LamPatch = &patch
		if b.s.Lithology == "sandstone" {
			st.CrossBed = 0.45
		}
	}
	b.strata = st
	return st
}

// granite reports whether the variant is granite.
func (b *build) granite() bool { return b.s.Lithology == "granite" }

// finishSurface adds what every rock face has at the small scale, scaled by
// Roughness: shallow swells, the grain (a cellular bump field — sand grains
// and the pits between them, not the creased look of a ridged noise), and
// for granite its coarser grus.
func (b *build) finishSurface(s sdf.Shape, scale float64) sdf.Shape {
	rough := b.s.Roughness
	if rough <= 0 {
		return s
	}
	s = sdf.Displace(s, sdf.FBM(b.next(), 6/scale, 3, 0.5), 0.004*scale*rough)
	s = sdf.Displace(s, sdf.Cellular(b.next(), 45/scale, 1), 0.0012*scale*rough)
	if b.granite() {
		s = sdf.Displace(s, sdf.Cellular(b.next(), 25/scale, 1), -0.0025*scale*rough)
	}
	return s
}

// blocky gives s the relief of broken rock: octaves networks of Voronoi
// blocks (sdf.BlockDisplace), each half the scale of the last, stretched by
// aspect (flat blocks follow the beds), each block standing out by up to
// amp (halving each octave), its steps planar. Scaled by Fracture.
func (b *build) blocky(s sdf.Shape, scale, amp float64, aspect sdf.Vec3, octaves int) sdf.Shape {
	amp *= 0.4 + 0.6*b.s.Fracture
	for o := 0; o < octaves; o++ {
		j := sdf.Joints{Seed: b.next(), Spacing: aspect.Mul(scale), Jitter: 1, Rotation: sdf.RotY(b.r(0, math.Pi))}
		// A step finer than the mesh would alias into a dotted line: such
		// an octave is left out (its seed still drawn), and every step is at
		// least a few vertices wide.
		if b.spacing == 0 || amp > 1.2*b.spacing {
			s = sdf.BlockDisplace(s, j, amp, math.Max(amp*0.4, 2.5*b.spacing))
		}
		scale *= 0.42
		amp *= 0.42
	}
	return s
}

// openField is a patchy field for a joint's openness: positive over about
// share of the rock, in patches of the given size.
func (b *build) openField(patch, share float64) *sdf.Field {
	f := sdf.FBM(b.next(), 1/patch, 3, 0.5)
	bias := (share - 0.5) * 0.9
	return &sdf.Field{F: func(p sdf.Vec3) float64 { return f.F(p) + bias }, Lip: f.Lip}
}

// facet cuts count·Fracture planar spalls of radius lo..hi (metres), depth
// dlo..dhi of the radius, tilted up to tilt, where bias allows.
func (b *build) facet(s sdf.Shape, count int, lo, hi, dlo, dhi, tilt float64, bias func(p, n sdf.Vec3) float64) sdf.Shape {
	n := int(math.Round(float64(count) * (0.35 + 0.65*b.s.Fracture)))
	if n <= 0 {
		return s
	}
	fs := sdf.PlaceFacets(s, sdf.FacetOptions{Seed: b.next(), Count: n, MinRadius: lo, MaxRadius: hi,
		MinDepth: dlo, MaxDepth: dhi, MaxTilt: tilt, Bias: bias})
	return sdf.CutFacets(s, fs, 0.06*lo*(0.3+b.s.Weathering))
}

// ---------- boulder ----------

func buildBoulder(b *build) sdf.Shape {
	size := b.size
	a := size / 2
	h := sdf.Vec3{a, a * b.r(0.5, 0.78), a * b.r(0.6, 0.9)}
	wear := b.s.Weathering
	if b.granite() {
		// Spheroidal: granite weathers to round boulders.
		h[1] = a * b.r(0.65, 0.9)
		wear = math.Min(1, wear*1.5+0.25)
	}
	// The block: an ellipsoid's worth of rock broken out along a dozen
	// fracture faces at random — a polyhedron, no two faces alike — its
	// edges rounded by how weathered it is (granite's much more).
	core := sdf.Ellipsoid(sdf.Vec3{}, h.Mul(1.3))
	n := 7 + int(math.Round(6*b.s.Fracture))
	var planes []sdf.Shape
	for i := 0; i < n; i++ {
		d := b.dir()
		support := sdf.Vec3{d[0] * h[0], d[1] * h[1], d[2] * h[2]}.Len()
		planes = append(planes, sdf.HalfSpace(d.Mul(support*b.r(0.66, 0.92)), d))
	}
	s := sdf.Fracture(core, planes, size*sdf.Mix(0.01, 0.08, wear), sdf.FBM(b.next(), 2.5/size, 3, 0.5), 0.006*size)
	s = sdf.Displace(s, sdf.FBM(b.next(), 1.2/size, 3, 0.5), 0.022*size)
	if !b.granite() {
		s = b.blocky(s, 0.5*size, 0.012*size, sdf.Vec3{1.3, 0.6, 1.3}, 1)
	}
	if !b.granite() {
		// Its beds, laid before it fell.
		st := b.beds(b.s.StrataThickness, 0.006*size*b.s.StrataRelief, b.s.Undercut, 0.0009*size, 5)
		s = sdf.StrataInset(s, *st)
	}
	// The many small flat breaks that make broken rock angular.
	s = b.facet(s, 9, 0.15*size, 0.45*size, 0.02, 0.08, 0.5, nil)
	if b.s.Fracture > 0.3 {
		// A crack or two, open in places.
		j := sdf.Joints{Seed: b.next(), MinGap: b.minGap(), Spacing: sdf.Vec3{1, 1, 1}.Mul(b.s.JointSpacing), Jitter: 1,
			Gap: 0.005 * size * b.s.Fracture, Depth: 0.12 * size, Round: 0.002 * size,
			Open: b.openField(0.5*size, 0.45)}
		s = sdf.CutJoints(s, j)
		b.blocks = &j
	}
	// Pits: the honeycomb weathering sandstone takes in places.
	pits := b.openField(0.4*size, 0.35)
	s = sdf.DisplaceWhere(s, sdf.Cellular(b.next(), 9/size, 0.9), -0.006*size*b.s.Roughness, *pits)
	s = b.finishSurface(s, size)
	// It landed tilted.
	rot := sdf.RotAxis(b.dir(), b.r(0, 0.5)).Mul(sdf.RotY(b.r(0, 2*math.Pi)))
	s = sdf.Rotate(s, rot)
	b.bedFrame = rot.T()
	return b.chip(s, size, 6, 0.04, 0.12)
}

// chip knocks Chips·max conchoidal spalls of radius lo..hi (fractions of
// size) off the most convex places of s.
func (b *build) chip(s sdf.Shape, size float64, maxCount int, lo, hi float64) sdf.Shape {
	n := int(math.Round(b.s.Chips * float64(maxCount)))
	if n <= 0 {
		return s
	}
	chips := sdf.PlaceChips(s, sdf.ChipOptions{Seed: b.next(), Count: n, MinRadius: lo * size, MaxRadius: hi * size, DepthRatio: 0.3})
	return sdf.CutChips(s, chips, 0.003*size)
}

// ---------- cliff chunk ----------

func buildCliff(b *build) sdf.Shape {
	H := b.size
	W := H * b.r(0.6, 0.9)
	D := H * b.r(0.42, 0.55)
	box := sdf.RoundBox(sdf.Vec3{0, H / 2, 0}, sdf.Vec3{W / 2, H / 2, D / 2}, 0.08*H)
	// The plan: bays, buttresses and broken sides (horizontal only, so the
	// faces stay walls); the outline seen from the front (a field of x and
	// y: a ragged top, sides that step in and out); then the face's large
	// bulges.
	s := sdf.Displace(box, fieldXZ(b.next(), 0.9/W, 3), 0.1*W)
	xy := noise.Fractal{Seed: b.next(), Octaves: 4, Frequency: 0.9 / H, Gain: 0.5}
	s = sdf.Displace(s, sdf.Field{F: func(p sdf.Vec3) float64 { return xy.At2(p[0], p[1]) }, Lip: 2.5 * 1.8 / H},
		0.09*H)
	s = sdf.Displace(s, sdf.FBM(b.next(), 0.6/H, 3, 0.5), 0.07*H)
	// A broken top: a few tilted planes take its edges off.
	var tops []sdf.Shape
	for i := 0; i < 2+b.rng.Intn(3); i++ {
		d := sdf.Vec3{b.r(-1, 1), b.r(1.2, 3), b.r(-0.2, 1)}.Normalize()
		tops = append(tops, sdf.HalfSpace(sdf.Vec3{0, H * b.r(0.86, 1.02), 0}.Add(sdf.Vec3{d[0], 0, d[2]}.Mul(0.25*W)), d))
	}
	s = sdf.Fracture(s, tops, 0.01*H, sdf.FBM(b.next(), 2/H, 3, 0.5), 0.006*H)
	// Broken-rock relief: blocks at three scales, flat ones following the
	// beds.
	s = b.blocky(s, 0.3*H, 0.025*H, sdf.Vec3{1.3, 0.7, 1.3}, 2)
	// The beds.
	st := b.beds(b.s.StrataThickness, 0.065*H*b.s.StrataRelief, b.s.Undercut, 0.0025, 6)
	st.MaxSlope = 2.5
	s = sdf.StrataInset(s, *st)
	// Alcoves: big arched scoops out of the face, where a soft patch let a
	// slab go.
	var alcoves []sdf.Chip
	for i := 0; i < b.rng.Intn(3); i++ {
		o := sdf.Vec3{b.r(-0.35, 0.35) * W, b.r(0.2, 0.7) * H, 2 * D}
		r := b.r(0.14, 0.28) * H
		depth := r * b.r(0.18, 0.3)
		if p, ok := sdf.TraceIn(s, o, sdf.Vec3{0, 0, -1}, 4*D); ok {
			ch := sdf.ChipAt(s, p, r, depth, b.r(-0.12, 0.12))
			ch.Aspect = b.r(0.45, 0.7)
			alcoves = append(alcoves, ch)
		}
	}
	s = sdf.CutChips(s, alcoves, 0.01*H)
	// Slab scars on the face (the front, not the back or the base).
	front := func(p, n sdf.Vec3) float64 { return sdf.SmoothStep(-0.4, 0.2, n[2]) * sdf.SmoothStep(-0.8, -0.3, n[1]) }
	s = b.facet(s, 12, 0.08*H, 0.3*H, 0.02, 0.05, 0.3, front)
	// A vertical joint set across the face, and a blocky network, both open
	// only in places.
	if f := b.s.Fracture; f > 0 {
		a := b.r(-0.35, 0.35)
		s = sdf.CutJointSet(s, sdf.JointSet{Seed: b.next(), MinGap: b.minGap(), Normal: sdf.Vec3{math.Cos(a), 0, math.Sin(a)},
			Spacing: b.s.JointSpacing * 1.6, Jitter: 0.7, Waviness: 0.05 * H, Wave: 0.5 / H,
			Gap: 0.012 * H * f, Depth: 0.3 * H, Round: 0.006 * H, Open: b.openField(0.35*H, 0.6), OpenFade: 0.4})
		j := sdf.Joints{Seed: b.next(), MinGap: b.minGap(), Spacing: sdf.Vec3{1.2, 1.7, 1}.Mul(b.s.JointSpacing), Jitter: 0.9,
			Gap: 0.0028 * H * f, Depth: 0.05 * H, Round: 0.0015 * H, Rotation: sdf.RotY(b.r(0, math.Pi)),
			Open: b.openField(0.2*H, 0.35)}
		s = sdf.CutJoints(s, j)
		b.blocks = &j
	}
	// Vertical flutes where water runs down.
	s = sdf.Displace(s, stretched(noise.Fractal{Seed: b.next(), Octaves: 3, Frequency: 1.4 / H, Gain: 0.5, Shape: noise.ShapeRidged},
		sdf.Vec3{1, 0.15, 1}), -0.01*H*b.s.Roughness)
	s = b.finishSurface(s, H*0.3)
	s = b.chip(s, H, 14, 0.018, 0.05)
	// The flat back and base.
	return sdf.SmoothIntersect(0.004*H, s,
		sdf.HalfSpace(sdf.Vec3{0, 0, -D * 0.3}, sdf.Vec3{0, 0, -1}),
		sdf.HalfSpace(sdf.Vec3{0, 0.002 * H, 0}, sdf.Vec3{0, -1, 0}))
}

// ---------- mesa cap ----------

func buildMesaCap(b *build) sdf.Shape {
	W := b.size
	T := W * b.r(0.18, 0.26)
	L := W * b.r(0.7, 1.0)
	slab := sdf.RoundBox(sdf.Vec3{0, T / 2, 0}, sdf.Vec3{W / 2, T / 2, L / 2}, 0.06*T)
	// The outline in plan: bays and points.
	s := sdf.Displace(slab, fieldXZ(b.next(), 0.35/W, 4), 0.1*W)
	s = sdf.Displace(s, sdf.FBM(b.next(), 0.8/W, 3, 0.5), 0.04*T)
	s = b.blocky(s, 1.2*T, 0.05*T, sdf.Vec3{2.5, 0.4, 2.5}, 2)
	// Caprock over a soft bed weathered back beneath it.
	soft := T * b.r(0.3, 0.42)
	st := &sdf.Strata{
		Layers: []sdf.Layer{{Thickness: soft, Hardness: 0.12}, {Thickness: T - soft, Hardness: 0.95}},
		Base:   0, Inset: 0.42 * T * b.s.StrataRelief, Undercut: math.Max(b.s.Undercut, 0.5), MaxSlope: 3,
		Lamination: 0.003, LamPeriod: sdf.Clamp(T*0.05, 0.015, 0.1), CrossBed: 0.4, Seed: b.next(),
	}
	v := sdf.FBM(b.next(), 0.6/W, 2, 0.5)
	st.Variation, st.VariationAmt = &v, 0.5
	w := sdf.FBM(b.next(), 0.5/W, 2, 0.5)
	st.Warp, st.WarpAmp = &w, 0.04*T
	lp := sdf.FBM(b.next(), 0.8/T, 2, 0.5)
	st.LamPatch = &lp
	b.strata = st
	s = sdf.StrataInset(s, *st)
	// Spalls round the rim.
	rim := func(p, n sdf.Vec3) float64 { return sdf.SmoothStep(0.7, 0.3, math.Abs(n[1])) }
	s = b.facet(s, 30, 0.15*T, 0.6*T, 0.03, 0.1, 0.4, rim)
	// The rim's blocks, their joints open in places.
	if f := b.s.Fracture; f > 0 {
		j := sdf.Joints{Seed: b.next(), MinGap: b.minGap(), Spacing: sdf.Vec3{b.s.JointSpacing, 6 * T, b.s.JointSpacing}, Jitter: 0.95,
			Gap: 0.03 * T * f, Depth: 0.5 * T, Round: 0.008 * T, Open: b.openField(0.25*W, 0.55)}
		s = sdf.CutJoints(s, j)
		b.blocks = &j
	}
	// Potholes on top.
	top := sdf.Field{F: func(p sdf.Vec3) float64 { return sdf.SmoothStep(T*0.75, T, p[1]) }, Lip: 1.5 / (0.25 * T)}
	s = sdf.DisplaceWhere(s, sdf.Cellular(b.next(), 0.5/T, 1), -0.025*T, top)
	s = b.finishSurface(s, T*1.2)
	s = b.chip(s, T, 12, 0.05, 0.15)
	return sdf.SmoothIntersect(0.003*W, s, sdf.HalfSpace(sdf.Vec3{0, 0.002 * T, 0}, sdf.Vec3{0, -1, 0}))
}

// ---------- ledge slab ----------

func buildLedge(b *build) sdf.Shape {
	L := b.size
	Wd := L * b.r(0.45, 0.7)
	T := L * b.r(0.08, 0.13)
	slab := sdf.RoundBox(sdf.Vec3{0, T / 2, 0}, sdf.Vec3{L / 2, T / 2, Wd / 2}, 0.2*T)
	// Broken out along its joints: a polygon in plan.
	n := 5 + b.rng.Intn(3)
	var planes []sdf.Shape
	a0 := b.r(0, 2*math.Pi)
	for i := 0; i < n; i++ {
		a := a0 + 2*math.Pi*(float64(i)+b.r(-0.3, 0.3))/float64(n)
		d := sdf.Vec3{math.Cos(a), b.r(-0.15, 0.15), math.Sin(a)}.Normalize()
		support := math.Abs(d[0])*L/2 + math.Abs(d[2])*Wd/2
		planes = append(planes, sdf.HalfSpace(sdf.Vec3{d[0], 0, d[2]}.Mul(support*b.r(0.7, 0.95)).Add(sdf.Vec3{0, T / 2, 0}), d))
	}
	s := sdf.Fracture(slab, planes, T*sdf.Mix(0.05, 0.35, b.s.Weathering), sdf.FBM(b.next(), 3/L, 3, 0.5), 0.012*T)
	// A gentle warp of the bed, and its bedding planes on the edges.
	s = sdf.Displace(s, fieldXZ(b.next(), 0.6/L, 2), 0.12*T)
	s = sdf.Displace(s, sdf.FBM(b.next(), 2/L, 3, 0.5), 0.03*T)
	s = b.blocky(s, 1.5*T, 0.06*T, sdf.Vec3{2.5, 0.35, 2.5}, 1)
	st := b.beds(math.Max(b.s.StrataThickness, T/4), 0.05*T*b.s.StrataRelief, b.s.Undercut*0.5, 0.0015, 4)
	st.Base = 0
	s = sdf.StrataInset(s, *st)
	edge := func(p, n sdf.Vec3) float64 { return sdf.SmoothStep(0.75, 0.35, math.Abs(n[1])) }
	s = b.facet(s, 16, 0.6*T, 2*T, 0.04, 0.12, 0.45, edge)
	if f := b.s.Fracture; f > 0.5 {
		s = sdf.CutJointSet(s, sdf.JointSet{Seed: b.next(), MinGap: b.minGap(), Normal: sdf.Vec3{b.r(-1, 1), 0, b.r(-1, 1)},
			Spacing: L * 0.6, Jitter: 0.5, Gap: 0.012 * T * f, Depth: 0.5 * T, Round: 0.01 * T, Open: b.openField(0.3*L, 0.6)})
	}
	s = b.finishSurface(s, L*0.4)
	return b.chip(s, L, 8, 0.02, 0.06)
}

// ---------- pillar (hoodoo) ----------

func buildPillar(b *build) sdf.Shape {
	H := b.size
	R := H * b.r(0.1, 0.13)
	// The column: lumps stacked, each its own girth and a little off the
	// last, leaning as a whole.
	lean := sdf.Vec3{b.r(-0.05, 0.05), 0, b.r(-0.05, 0.05)}
	n := 4 + b.rng.Intn(3)
	top := H * 0.86
	var parts []sdf.Shape
	prev := sdf.Vec3{}
	for i := 0; i < n; i++ {
		y := top * float64(i+1) / float64(n)
		c := sdf.Vec3{lean[0] * y, y, lean[2] * y}.Add(sdf.Vec3{b.r(-0.15, 0.15) * R, 0, b.r(-0.15, 0.15) * R})
		parts = append(parts, sdf.Capsule(prev, c, R*b.r(0.7, 1.15)))
		prev = c
	}
	pedestal := sdf.Ellipsoid(sdf.Vec3{}, sdf.Vec3{2.4 * R, 0.24 * H, 2.1 * R})
	s := sdf.SmoothUnion(0.6*R, append(parts, pedestal)...)
	// Not round in section: squeezed one way, turned.
	s = sdf.Rotate(sdf.Stretch(s, sdf.Vec3{b.r(1.0, 1.25), 1, b.r(0.75, 0.95)}), sdf.RotY(b.r(0, math.Pi)))
	// The caprock: a broken block, wider than the neck under it.
	capT := H * b.r(0.06, 0.09)
	ch := sdf.Vec3{R * b.r(1.4, 1.9), capT, R * b.r(1.3, 1.7)}
	capRock := sdf.RoundBox(sdf.Vec3{}, ch, capT*0.35)
	var planes []sdf.Shape
	for i := 0; i < 4; i++ {
		d := b.dir()
		d[1] *= 0.4
		d = d.Normalize()
		support := math.Abs(d[0])*ch[0] + math.Abs(d[1])*ch[1] + math.Abs(d[2])*ch[2]
		planes = append(planes, sdf.HalfSpace(d.Mul(support*b.r(0.7, 0.9)), d))
	}
	capRock = sdf.Fracture(capRock, planes, 0.08*R, sdf.FBM(b.next(), 3/R, 3, 0.5), 0.03*R)
	capRock = sdf.Rotate(capRock, sdf.RotAxis(b.dir(), 0.12).Mul(sdf.RotY(b.r(0, math.Pi))))
	capRock = sdf.Translate(capRock, prev.Add(sdf.Vec3{0, R*0.4 + capT*0.4, 0}))
	s = sdf.SmoothUnion(0.2*R, s, capRock)
	s = sdf.Displace(s, sdf.FBM(b.next(), 2.2/H, 3, 0.5), 0.3*R)
	// The beds: soft ones necked in, rounded (a low slope limit).
	st := b.beds(b.s.StrataThickness, 0.55*R*b.s.StrataRelief, b.s.Undercut*0.4, 0.002, 7)
	st.MaxSlope = 1.0
	st.WarpAmp *= 4
	s = sdf.StrataInset(s, *st)
	// Vertical fluting, spalls.
	s = sdf.Displace(s, stretched(noise.Fractal{Seed: b.next(), Octaves: 3, Frequency: 4 / H, Gain: 0.5, Shape: noise.ShapeRidged},
		sdf.Vec3{1, 0.12, 1}), -0.16*R*b.s.Roughness)
	s = sdf.Displace(s, sdf.FBM(b.next(), 7/H, 3, 0.5), 0.06*R)
	side := func(p, n sdf.Vec3) float64 { return sdf.SmoothStep(0.8, 0.4, math.Abs(n[1])) }
	s = b.facet(s, 5, 0.35*R, 0.8*R, 0.04, 0.1, 0.4, side)
	if f := b.s.Fracture; f > 0 {
		s = sdf.CutJointSet(s, sdf.JointSet{Seed: b.next(), MinGap: b.minGap(), Normal: sdf.Vec3{b.r(-0.2, 0.2), 1, b.r(-0.2, 0.2)},
			Spacing: H * 0.3, Jitter: 0.8, Waviness: 0.02 * H, Wave: 2 / H, Gap: 0.02 * R * f, Depth: 0.25 * R, Round: 0.01 * R,
			Open: b.openField(0.3*H, 0.5)})
	}
	s = b.finishSurface(s, R*2)
	s = b.chip(s, R*2, 10, 0.04, 0.12)
	return sdf.SmoothIntersect(0.002*H, s, sdf.HalfSpace(sdf.Vec3{0, 0.001 * H, 0}, sdf.Vec3{0, -1, 0}))
}

// ---------- rubble ----------

func buildRubble(b *build) sdf.Shape {
	size := b.size
	r0 := size / 2
	k := sdf.Vec3{1, b.r(0.55, 0.9), b.r(0.65, 1)}
	n := 7 + b.rng.Intn(5)
	parts := []sdf.Shape{sdf.Sphere(sdf.Vec3{}, r0*1.25)}
	for i := 0; i < n; i++ {
		d := b.dir()
		support := r0 * sdf.Vec3{math.Abs(d[0]) * k[0], math.Abs(d[1]) * k[1], math.Abs(d[2]) * k[2]}.Len()
		parts = append(parts, sdf.HalfSpace(d.Mul(support*b.r(0.55, 0.85)), d))
	}
	s := sdf.SmoothIntersect(size*sdf.Mix(0.01, 0.07, b.s.Weathering), parts...)
	s = sdf.Displace(s, sdf.FBM(b.next(), 3/size, 3, 0.5), 0.015*size)
	s = b.blocky(s, 0.5*size, 0.02*size, sdf.Vec3{1, 1, 1}, 1)
	if !b.granite() {
		st := b.beds(b.s.StrataThickness, 0.015*size*b.s.StrataRelief, 0, 0.0015*size, 3)
		s = sdf.StrataInset(s, *st)
	}
	s = b.facet(s, 5, 0.15*size, 0.4*size, 0.03, 0.1, 0.5, nil)
	s = b.finishSurface(s, size)
	rot := sdf.RotAxis(b.dir(), b.r(0, math.Pi))
	s = sdf.Rotate(s, rot)
	b.bedFrame = rot.T()
	return b.chip(s, size, 3, 0.06, 0.16)
}

// ---------- pebble ----------

func buildPebble(b *build) sdf.Shape {
	size := b.size
	r := sdf.Vec3{0.5, b.r(0.3, 0.42), b.r(0.17, 0.3)}.Mul(size)
	// Lying on its flat side, turned at random.
	s := sdf.Ellipsoid(sdf.Vec3{}, sdf.Vec3{r[0], r[2], r[1]})
	s = sdf.Displace(s, sdf.FBM(b.next(), 1.2/size, 2, 0.5), 0.05*size)
	s = sdf.Displace(s, sdf.FBM(b.next(), 6/size, 2, 0.5), 0.004*size*b.s.Roughness)
	rot := sdf.RotY(b.r(0, 2*math.Pi)).Mul(sdf.RotAxis(b.dir(), b.r(0, 0.2)))
	b.bedFrame = rot.T()
	return sdf.Rotate(s, rot)
}

// sortedPresets is the registry's names sorted, for errors.
func sortedPresets() []string {
	var out []string
	for n := range presets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
