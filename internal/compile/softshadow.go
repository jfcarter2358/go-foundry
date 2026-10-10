package compile

// softshadow.go — a baked shadow as coverage, not as a yes or no.
//
// One ray per vertex answers "lit" or "not", and the renderer interpolates
// those answers across triangles. A shadow edge running diagonally to the
// sample grid then comes out as a staircase: every vertex on one side is fully
// lit, every one on the other fully dark, and the edge can only run between
// them. Antialiasing has the same problem and the same answer: what a vertex
// should hold is how much of the surface around it is lit, and blended across
// the triangles those fractions draw the edge where it actually runs.
//
// So a vertex near an edge casts a small grid of rays spread over the patch of
// surface it stands for, one (finest) grid step each way, weighted by a tent
// (bilinear) filter. That filter is the one linear interpolation between
// neighbouring vertices amounts to, so a straight edge comes out as a straight
// ramp between them. Most vertices are nowhere near an edge, and they are
// found cheaply: the centre ray and the grid's four corners agreeing is taken
// as the whole grid agreeing.

import "github.com/go-gl/mathgl/mgl32"

// softTaps are the grid's positions along each axis, in units of the vertex's
// grid step, and softWeights their tent weights.
var (
	softTaps    = [4]float32{-0.75, -0.25, 0.25, 0.75}
	softWeights = [4]float32{0.25, 0.75, 0.75, 0.25}
)

// shadowProbe answers "how much of this vertex's patch can light k reach"
// for one corner of one face, on one side, remembering each light's answer.
type shadowProbe struct {
	b        *baker
	f        *Face
	self     int
	p        mgl32.Vec3
	side     mgl32.Vec3 // outward normal of the side being lit
	start    mgl32.Vec3 // the corner's own ray start (airStart)
	buried   bool
	r        float32 // how far its patch reaches: baker.softRadius
	tan, bit mgl32.Vec3
	memo     map[int]float32
}

func (b *baker) probe(f *Face, self, i int, side mgl32.Vec3) *shadowProbe {
	start, buried := b.airStart(f, i, side)
	tan, bit := basisAbout(side)
	return &shadowProbe{b: b, f: f, self: self, p: f.Points[i], side: side,
		start: start, buried: buried, r: b.softRadius, tan: tan, bit: bit}
}

// towardLight is where a light is from a ray start: the unit direction and
// how far the ray has to go.
type towardLight func(start mgl32.Vec3) (dir mgl32.Vec3, maxT float32, ok bool)

// lit is the fraction of the corner's patch light key reaches, 0 to 1.
func (s *shadowProbe) lit(key int, to towardLight) float32 {
	if v, ok := s.memo[key]; ok {
		return v
	}
	v := s.coverage(to)
	if s.memo == nil {
		s.memo = map[int]float32{}
	}
	s.memo[key] = v
	return v
}

func (s *shadowProbe) coverage(to towardLight) float32 {
	centre := float32(1)
	if dir, maxT, ok := to(s.start); ok && s.b.bvh.occluded(s.start, dir, maxT, s.self, s.buried) {
		centre = 0
	}
	if s.r <= 0 {
		return centre
	}
	// The corners first: all agreeing with the centre, the patch is taken to
	// be uniform and the other twelve rays are not cast.
	uniform := true
	for _, u := range [2]int{0, 3} {
		for _, v := range [2]int{0, 3} {
			if l, ok := s.tap(u, v, to); ok && l != centre {
				uniform = false
			}
		}
	}
	if uniform {
		return centre
	}
	var sum, weight float32
	for u := range softTaps {
		for v := range softTaps {
			if l, ok := s.tap(u, v, to); ok {
				w := softWeights[u] * softWeights[v]
				sum += l * w
				weight += w
			}
		}
	}
	if weight == 0 {
		return centre
	}
	return sum / weight
}

// tap is one grid ray: 1 lit, 0 shadowed, and ok false when that part of the
// patch is inside a brush — beyond a wall the surface runs into, say — and so
// has nothing to say about this one.
func (s *shadowProbe) tap(u, v int, to towardLight) (float32, bool) {
	q := s.p.Add(s.tan.Mul(softTaps[u] * s.r)).Add(s.bit.Mul(softTaps[v] * s.r))
	start := q.Add(s.side.Mul(s.b.opt.ShadowBias))
	if s.b.solids.inside(start, s.f.CoveredBy) {
		return 0, false
	}
	dir, maxT, ok := to(start)
	if ok && s.b.bvh.occluded(start, dir, maxT, s.self, false) {
		return 0, true
	}
	return 1, true
}
