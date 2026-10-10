package compile

// bounce.go — light that arrives by way of another surface.
//
// The direct bake (light.go) lights a point by what can see it: the ambient and
// every static light with a clear line to it. A floor in full sun then says
// nothing to the wall beside it, and everything the sun cannot reach is left
// with the ambient alone — flat, and the same colour whatever surrounds it.
// Real surfaces hand on part of what lands on them, coloured by themselves;
// that is what this adds.
//
// # A gather, per vertex
//
// The bake's vertices are already a grid over every surface (SubdivideFaces),
// so they are the patches. Each one looks out over its hemisphere along a
// fixed fan of rays, and each ray that lands on a drawn face picks up that
// face's light at the spot it hit, times the face's colour. For a diffuse
// surface sampled cosine-weighted, the light it receives that way is exactly
// the mean of what its rays found — the π in the reflected radiance and the π
// in the integral cancel — which is the whole formula.
//
// Several bounces are the same gather again over a level whose faces now
// carry direct plus the previous bounce. Only the final gather is taken for
// every answer a face carries (smooth, back); the ones before it only feed the
// next, and bounce off the flat front and back.
//
// A ray that hits nothing adds nothing. The sky is already in the bake as the
// ambient term, and a miss is the direction it came from: counting it again
// would light every open space twice.
//
// # What a ray can hit
//
// Its own tree of the drawn faces (faceBVH), not the BSP: the BSP says whether
// a segment is blocked, not by what, and it is built from hulls a detail
// surface may be carved out of. The seal's slabs are not faces, so a ray that
// crosses the seal goes out into the sky, which is where it went. A
// translucent face is left out entirely — it neither reflects nor blocks —
// because half a wall is not something a per-vertex gather can say anything
// useful about.
//
// # Same level, same bake
//
// The fan is one fixed table, turned about each vertex's normal by a hash of
// the vertex's position. Neighbouring vertices sample different directions,
// which is what hides the fan's structure; the same vertex always samples the
// same ones, so recompiling an unchanged level gives an identical .fbsp rather
// than a new pattern of noise.

import (
	"fmt"
	"math"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/bsp"
	"foundry/internal/gfx"
)

// textureAlbedo is a texture's average colour — a variable so the tests can
// colour surfaces without a texture on disk.
var textureAlbedo = gfx.TextureMeanColor

// untexturedAlbedo is what a face that names no texture reflects. The editor
// paints one with its own normal as a colour (see world's untextured rule), and
// bouncing that would tint a level red, green and blue by which way its walls
// face; a neutral grey is what the author means by "not painted yet".
const untexturedAlbedo = float32(0.5)

// bounce adds bounces gathers of bounced light to the direct bake and returns
// the result, in direct's layout. direct itself is not changed.
func (b *baker) bounce(direct []faceSamples, bounces int) []faceSamples {
	t := b.tree
	scale := b.lights.Global.BounceScale
	if scale <= 0 {
		return direct
	}
	samples := b.opt.BounceSamples
	if samples <= 0 {
		samples = DefaultBounceSamples
	}
	maxAlbedo := b.opt.MaxAlbedo
	if maxAlbedo <= 0 {
		maxAlbedo = DefaultMaxAlbedo
	}

	g := &gatherer{
		baker:  b,
		bvh:    b.bvh,
		albedo: faceAlbedos(t.Faces, maxAlbedo),
		fan:    cosineFan(samples),
		scale:  scale,
		groups: b.groups(),
	}
	smooth := b.smooth

	// What a flat front or back gather's rays hit — which face, which side of
	// it, where on its fan — depends on the geometry alone; only the light
	// they pick up there changes from one bounce to the next. So with more
	// than one bounce the first pass keeps what each ray found (rayLog) and
	// the later ones replay it instead of casting the same rays again. A face
	// whose log would not fit in bounceReplayBudget is simply cast again.
	var logs [][2]*rayLog
	var budget atomic.Int64
	if bounces > 1 && replayBounceRays {
		logs = make([][2]*rayLog, len(t.Faces))
		budget.Store(bounceReplayBudget)
	}

	// emit is what each face hands on: its flat front and back light, per
	// group — direct at first, then direct plus the latest bounce.
	emit := direct
	var out []faceSamples
	for pass := 1; pass <= bounces; pass++ {
		final := pass == bounces
		next := make([]faceSamples, len(t.Faces))
		stage := fmt.Sprintf("Light: bounce %d of %d", pass, bounces)
		b.progress.parallelFor(stage, len(t.Faces), func(fi int) {
			f := &t.Faces[fi]
			for k := 0; k < numKinds; k++ {
				if !final && k != kindFront && k != kindBack {
					continue
				}
				normals, side, ok := kindFrame(f, smooth[fi], k)
				if !ok {
					continue
				}
				var indirect []mgl32.Vec3
				li := -1 // which of the face's two logs this kind keeps
				switch {
				case logs == nil:
				case k == kindFront:
					li = 0
				case k == kindBack:
					li = 1
				}
				switch {
				case li >= 0 && logs[fi][li] != nil:
					indirect = g.replayFace(fi, emit, logs[fi][li])
				case li >= 0 && pass == 1:
					var log rayLog
					indirect = g.gatherFace(fi, emit, normals, side, &log)
					if log.ends != nil && budget.Add(-log.bytes()) >= 0 {
						logs[fi][li] = &log
					}
				default:
					indirect = g.gatherFace(fi, emit, normals, side, nil)
				}
				d := direct[fi].kinds[k]
				for i := range indirect {
					indirect[i] = indirect[i].Add(d[i])
				}
				next[fi].kinds[k] = indirect
			}
		})
		emit, out = next, next
	}
	return out
}

// replayBounceRays keeps the first bounce's ray hits for the later ones to
// replay (see bounce); false casts every pass afresh, which the tests compare
// against. bounceReplayBudget caps the memory the kept hits may take, in bytes.
var (
	replayBounceRays         = true
	bounceReplayBudget int64 = 1 << 30
)

// rayLog is what one face's gather found, per point: the rays that landed on
// something able to send light back, in the order they were cast. Point i's
// are hits[ends[i-1]:ends[i]].
type rayLog struct {
	ends []int32
	hits []rayHit
}

func (l *rayLog) bytes() int64 {
	return int64(len(l.ends))*4 + int64(len(l.hits))*int64(unsafe.Sizeof(rayHit{}))
}

// rayHit is one ray's landing: face (one's complement when it arrived at the
// back of a double-sided face) and its fan weights there. The fan's first
// corner is always the polygon's 0 (fanWeights).
type rayHit struct {
	face    int32
	i1, i2  int32
	u, v, w float32
}

// gatherer is everything a gather reads, none of which it writes: faces are
// gathered in parallel, each writing only its own answers.
type gatherer struct {
	*baker
	bvh    *faceBVH
	albedo []mgl32.Vec3
	fan    []mgl32.Vec3
	scale  float32
	groups int
}

// gatherFace is the bounced light at every point of face fi facing normals
// (the plane's when nil) on side, per group, in faceSamples' layout. A non-nil
// log is filled with what every ray hit, for replayFace; it is left empty when
// the face gathers nothing at all.
func (g *gatherer) gatherFace(fi int, emit []faceSamples, normals []mgl32.Vec3, side float32, log *rayLog) []mgl32.Vec3 {
	f := &g.tree.Faces[fi]
	np := len(f.Points)
	out := make([]mgl32.Vec3, g.groups*np)
	allowed := make([]bool, g.groups)
	any := false
	for gi := range allowed {
		allowed[gi] = g.groupAllowedIn(gi, f.Chunk)
		any = any || allowed[gi]
	}
	if !any {
		return out
	}
	if log != nil {
		log.ends = make([]int32, np)
	}
	plane := f.Plane.N.Mul(side)
	acc := make([]mgl32.Vec3, g.groups)
	for i, p := range f.Points {
		n := plane
		if normals != nil {
			n = normals[i].Mul(side)
		}
		origin, _ := g.airStart(f, i, plane)
		tan, bit := basisAbout(n)
		turn := pointTurn(p)
		sin, cos := float32(math.Sin(turn)), float32(math.Cos(turn))
		clear(acc)
		for _, l := range g.fan {
			// The fan's x/y turned about the normal by this point's angle.
			x, y := l.X()*cos-l.Y()*sin, l.X()*sin+l.Y()*cos
			dir := tan.Mul(x).Add(bit.Mul(y)).Add(n.Mul(l.Z()))
			// A smoothed normal leans off the plane, and part of its
			// hemisphere then points into the surface itself. Those rays are
			// mirrored back out rather than sent into the solid behind.
			if d := dir.Dot(plane); d < 0 {
				dir = dir.Sub(plane.Mul(2 * d))
			}
			r, ok := g.traceRay(origin, dir, fi)
			if !ok {
				continue
			}
			if log != nil {
				log.hits = append(log.hits, r)
			}
			g.addRay(r, emit, allowed, acc)
		}
		if log != nil {
			log.ends[i] = int32(len(log.hits))
		}
		inv := g.scale / float32(len(g.fan))
		for gi := range acc {
			out[gi*np+i] = acc[gi].Mul(inv)
		}
	}
	return out
}

// replayFace is gatherFace for a face whose rays were logged on an earlier
// pass: the same hits, picking up this pass's emit, added in the same order —
// so to the bit what casting them again would give.
func (g *gatherer) replayFace(fi int, emit []faceSamples, log *rayLog) []mgl32.Vec3 {
	f := &g.tree.Faces[fi]
	np := len(f.Points)
	out := make([]mgl32.Vec3, g.groups*np)
	allowed := make([]bool, g.groups)
	for gi := range allowed {
		allowed[gi] = g.groupAllowedIn(gi, f.Chunk)
	}
	acc := make([]mgl32.Vec3, g.groups)
	from := int32(0)
	for i := 0; i < np; i++ {
		clear(acc)
		for _, r := range log.hits[from:log.ends[i]] {
			g.addRay(r, emit, allowed, acc)
		}
		from = log.ends[i]
		inv := g.scale / float32(len(g.fan))
		for gi := range acc {
			out[gi*np+i] = acc[gi].Mul(inv)
		}
	}
	return out
}

// traceRay is where the ray lands: the nearest drawn face along it and where
// on that face's fan, or false when it lands on nothing that could send light
// back down it.
func (g *gatherer) traceRay(origin, dir mgl32.Vec3, self int) (rayHit, bool) {
	hit, t, ok := g.bvh.nearest(origin, dir, self)
	if !ok {
		return rayHit{}, false // out to the sky, which the ambient already is
	}
	h := &g.tree.Faces[hit]
	code := int32(hit)
	if h.Plane.N.Dot(dir) > 0 {
		// Arrived from behind: the far side of a double-sided face, or the
		// inside of a solid — which blocks the ray and sends nothing back.
		if !h.DoubleSided {
			return rayHit{}, false
		}
		code = ^code
	}
	_, i1, i2, u, v, w := fanWeights(h.Points, origin.Add(dir.Mul(t)))
	return rayHit{face: code, i1: int32(i1), i2: int32(i2), u: u, v: v, w: w}, true
}

// addRay adds into acc, per allowed group, the light a traced ray's landing
// sends back down it.
func (g *gatherer) addRay(r rayHit, emit []faceSamples, allowed []bool, acc []mgl32.Vec3) {
	hit, k := int(r.face), kindFront
	if r.face < 0 {
		hit, k = int(^r.face), kindBack
	}
	lit := emit[hit].kinds[k]
	if lit == nil {
		return
	}
	np := len(g.tree.Faces[hit].Points)
	alb := g.albedo[hit]
	i1, i2 := int(r.i1), int(r.i2)
	for gi := range acc {
		if !allowed[gi] {
			continue
		}
		base := gi * np
		e := lit[base].Mul(r.u).Add(lit[base+i1].Mul(r.v)).Add(lit[base+i2].Mul(r.w))
		acc[gi] = acc[gi].Add(mgl32.Vec3{e.X() * alb.X(), e.Y() * alb.Y(), e.Z() * alb.Z()})
	}
}

// fanWeights is where p falls on a polygon drawn as the (0,i,i+1) fan the
// renderer draws it as: the triangle's corners and their weights, so light
// interpolated this way matches what is on screen at that spot. i0 is always
// 0, which rayHit relies on.
func fanWeights(pts []mgl32.Vec3, p mgl32.Vec3) (i0, i1, i2 int, u, v, w float32) {
	best := float32(-1e30)
	i0, i1, i2, u = 0, 0, 0, 1
	for i := 1; i < len(pts)-1; i++ {
		a, bb, c, ok := barycentric(p, pts[0], pts[i], pts[i+1])
		if !ok {
			continue
		}
		if m := min(a, bb, c); m > best {
			best = m
			i0, i1, i2, u, v, w = 0, i, i+1, a, bb, c
		}
	}
	// A hit a hair outside every triangle (a ray grazing the rim) takes the
	// nearest one's weights, clamped back onto it.
	u, v, w = max(u, 0), max(v, 0), max(w, 0)
	if s := u + v + w; s > 0 {
		u, v, w = u/s, v/s, w/s
	} else {
		u, v, w = 1, 0, 0
	}
	return
}

// faceAlbedos is how much of the light landing on each face it sends on, per
// channel: its texture's average colour, mixed toward its blend texture by the
// mask's average brightness exactly as fragment.glsl mixes them per texel, and
// capped at maxAlbedo.
func faceAlbedos(faces []Face, maxAlbedo float32) []mgl32.Vec3 {
	type key struct{ tex, blend, mask string }
	memo := map[key]mgl32.Vec3{}
	mean := func(name string) (mgl32.Vec3, bool) {
		if name == "" {
			return mgl32.Vec3{}, false
		}
		return textureAlbedo(name)
	}
	out := make([]mgl32.Vec3, len(faces))
	for fi := range faces {
		f := &faces[fi]
		k := key{f.Texture, f.BlendTexture, f.BlendMask}
		a, ok := memo[k]
		if !ok {
			a, ok = mean(f.Texture)
			if !ok {
				a = mgl32.Vec3{untexturedAlbedo, untexturedAlbedo, untexturedAlbedo}
			}
			if blend, ok := mean(f.BlendTexture); ok {
				if mask, ok := mean(f.BlendMask); ok {
					lum := mask.Dot(mgl32.Vec3{0.299, 0.587, 0.114})
					a = a.Mul(1 - lum).Add(blend.Mul(lum))
				}
			}
			for c := 0; c < 3; c++ {
				a[c] = min(max(a[c], 0), maxAlbedo)
			}
			memo[k] = a
		}
		out[fi] = a
	}
	return out
}

// cosineFan is n directions over the +Z hemisphere, spread evenly and weighted
// by the cosine to Z: a Hammersley set, which covers the hemisphere without
// the clumps and gaps of random directions at the same count.
func cosineFan(n int) []mgl32.Vec3 {
	out := make([]mgl32.Vec3, n)
	for i := range out {
		u1 := (float64(i) + 0.5) / float64(n)
		u2 := radicalInverse(uint32(i))
		r := math.Sqrt(u1)
		phi := 2 * math.Pi * u2
		out[i] = mgl32.Vec3{float32(r * math.Cos(phi)), float32(r * math.Sin(phi)), float32(math.Sqrt(1 - u1))}
	}
	return out
}

// radicalInverse is i's bits mirrored about the binary point: the van der
// Corput sequence, 0, ½, ¼, ¾, ⅛…
func radicalInverse(i uint32) float64 {
	i = (i << 16) | (i >> 16)
	i = ((i & 0x55555555) << 1) | ((i & 0xAAAAAAAA) >> 1)
	i = ((i & 0x33333333) << 2) | ((i & 0xCCCCCCCC) >> 2)
	i = ((i & 0x0F0F0F0F) << 4) | ((i & 0xF0F0F0F0) >> 4)
	i = ((i & 0x00FF00FF) << 8) | ((i & 0xFF00FF00) >> 8)
	return float64(i) / (1 << 32)
}

// pointTurn is the angle, 0..2π, a point turns the fan by: a hash of its
// position's bits, so it is the same every compile and unrelated between
// neighbours.
func pointTurn(p mgl32.Vec3) float64 {
	h := uint64(14695981039346656037)
	for _, c := range p {
		h ^= uint64(math.Float32bits(c))
		h *= 1099511628211
	}
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	return float64(h>>11) / (1 << 53) * 2 * math.Pi
}

// basisAbout is two unit vectors perpendicular to n and to each other (Duff et
// al.'s branchless construction, which has no direction it degenerates at).
func basisAbout(n mgl32.Vec3) (t, b mgl32.Vec3) {
	s := float32(1)
	if n.Z() < 0 {
		s = -1
	}
	a := -1 / (s + n.Z())
	c := n.X() * n.Y() * a
	t = mgl32.Vec3{1 + s*n.X()*n.X()*a, s * c, -s * n.X()}
	b = mgl32.Vec3{c, s + n.Y()*n.Y()*a, -n.Y()}
	return
}

// parallelFor runs fn(i) for every i in [0, n) across the machine's cores.
// fn must not write anything another index reads.
func parallelFor(n int, fn func(i int)) {
	workers := min(runtime.GOMAXPROCS(0), n)
	if workers <= 1 {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				fn(i)
			}
		}()
	}
	wg.Wait()
}

// ---------- the ray tree ----------

// faceBVH is a bounding-volume hierarchy over the faces a bounced ray can hit.
type faceBVH struct {
	faces []Face
	nodes []bvhNode
	items []int32 // face indices, grouped by leaf
	// packed is items' faces as the ray tests read them, parallel to items:
	// a leaf's faces sit side by side and their edges in one array, instead of
	// a stride across the big Face structs and a heap slice of points apiece.
	packed []packedFace
	edges  []packedEdge
}

// packedFace is what rayHitsFaceWithin reads of a face: its plane, its
// sidedness and its edges (edges[first : first+count]).
type packedFace struct {
	plane        bsp.Plane
	first, count int32
	doubleSided  bool
}

// packedEdge is one edge of a face, from corner a along e to the next corner,
// with e's length — the very values rayHitsFaceWithin works out per test,
// worked out once, by the same expressions, so a test of the packed face is
// the same float arithmetic as a test of the Face and gives the same bits.
type packedEdge struct {
	a, e mgl32.Vec3
	len  float32
}

// pack fills packed and edges from items, once the tree is built.
func (bv *faceBVH) pack() {
	bv.packed = make([]packedFace, len(bv.items))
	n := 0
	for _, fi := range bv.items {
		n += len(bv.faces[fi].Points)
	}
	bv.edges = make([]packedEdge, 0, n)
	for j, fi := range bv.items {
		f := &bv.faces[fi]
		pts := f.Points
		bv.packed[j] = packedFace{plane: f.Plane, first: int32(len(bv.edges)), count: int32(len(pts)), doubleSided: f.DoubleSided}
		for i := range pts {
			a, b := pts[i], pts[(i+1)%len(pts)]
			e := b.Sub(a)
			bv.edges = append(bv.edges, packedEdge{a: a, e: e, len: e.Len()})
		}
	}
}

// bvhNode is a box and either its two children (count 0: nodes left and
// right) or its faces (items[left : left+count]).
type bvhNode struct {
	lo, hi             mgl32.Vec3
	left, right, count int32
}

const bvhLeafSize = 4

// buildFaceBVH is the tree over t's drawn, opaque faces. subdivided says
// SubdivideFaces has already dropped every face the build left unused; without
// it they are still in the list and have to be skipped here.
func buildFaceBVH(t *BSPTree, subdivided bool) *faceBVH {
	var used []bool
	if !subdivided {
		used = t.usedFaces()
	}
	bv := &faceBVH{faces: t.Faces}
	type entry struct {
		fi     int32
		lo, hi mgl32.Vec3
		c      mgl32.Vec3
	}
	var es []entry
	for fi := range t.Faces {
		f := &t.Faces[fi]
		// A cover face is inside a neighbour's solid whenever that neighbour
		// is there to bounce light at all, so it neither blocks nor reflects.
		if f.NoDraw || f.CoveredBy != nil || f.Alpha < 1 || len(f.Points) < 3 || (used != nil && !used[fi]) {
			continue
		}
		lo, hi := bsp.Bounds(f.Points)
		es = append(es, entry{fi: int32(fi), lo: lo, hi: hi, c: lo.Add(hi).Mul(0.5)})
	}
	// A node's two children are made side by side, so the walk, which tests
	// both boxes together, reads one stretch of memory rather than two. Only
	// where the nodes sit changes: the tree, and so every walk of it, is the
	// one a depth-first layout gave.
	var build func(ni int32, es []entry)
	build = func(ni int32, es []entry) {
		lo, hi := es[0].lo, es[0].hi
		clo, chi := es[0].c, es[0].c
		for _, e := range es[1:] {
			for a := 0; a < 3; a++ {
				lo[a], hi[a] = min(lo[a], e.lo[a]), max(hi[a], e.hi[a])
				clo[a], chi[a] = min(clo[a], e.c[a]), max(chi[a], e.c[a])
			}
		}
		// Padded a hair: a box is a promise nothing outside it can be hit, and
		// float error on a flat face's zero-thickness box must not break it.
		pad := mgl32.Vec3{1e-3, 1e-3, 1e-3}
		bv.nodes[ni].lo, bv.nodes[ni].hi = lo.Sub(pad), hi.Add(pad)
		if len(es) <= bvhLeafSize {
			bv.nodes[ni].left, bv.nodes[ni].count = int32(len(bv.items)), int32(len(es))
			for _, e := range es {
				bv.items = append(bv.items, e.fi)
			}
			return
		}
		axis, span := 0, chi[0]-clo[0]
		for a := 1; a < 3; a++ {
			if s := chi[a] - clo[a]; s > span {
				axis, span = a, s
			}
		}
		// Sorted by centre (face index to break ties, so the tree — and with
		// it nothing, since the nearest hit is the nearest hit — is the same
		// every run) and split at the median.
		slices.SortFunc(es, func(x, y entry) int {
			switch {
			case x.c[axis] < y.c[axis]:
				return -1
			case x.c[axis] > y.c[axis]:
				return 1
			}
			return int(x.fi - y.fi)
		})
		mid := len(es) / 2
		left := int32(len(bv.nodes))
		bv.nodes = append(bv.nodes, bvhNode{}, bvhNode{})
		bv.nodes[ni].left, bv.nodes[ni].right = left, left+1
		build(left, es[:mid])
		build(left+1, es[mid:])
	}
	if len(es) > 0 {
		bv.nodes = append(bv.nodes, bvhNode{})
		build(0, es)
	}
	bv.pack()
	return bv
}

// nearest is the first face along the ray from origin in direction dir (unit
// length), other than skip: its index and distance. A face hit from either side
// counts — which side is the caller's question.
func (bv *faceBVH) nearest(origin, dir mgl32.Vec3, skip int) (int, float32, bool) {
	if len(bv.nodes) == 0 {
		return 0, 0, false
	}
	var inv mgl32.Vec3
	for a := 0; a < 3; a++ {
		if dir[a] != 0 {
			inv[a] = 1 / dir[a]
		} else {
			inv[a] = float32(math.Inf(1))
		}
	}
	best, bestT := -1, float32(math.Inf(1))
	// Each node goes on the stack with where the ray enters its box, worked
	// out as it was pushed. Testing the box again when it comes off, against
	// the nearer bestT found since, can only fail by that entry now lying past
	// bestT — the rest of the slab test did not change — so the entry alone
	// is compared: the walk is the same, at a third fewer box tests.
	var stack [64]bvhVisit
	t0, ok := enterBox(origin, inv, bv.nodes[0].lo, bv.nodes[0].hi, bestT)
	if !ok {
		return best, bestT, false
	}
	stack[0] = bvhVisit{0, t0}
	sp := 1
	for sp > 0 {
		sp--
		if stack[sp].enter > bestT {
			continue
		}
		n := &bv.nodes[stack[sp].node]
		if n.count > 0 {
			for j := n.left; j < n.left+n.count; j++ {
				fi := bv.items[j]
				if int(fi) == skip {
					continue
				}
				if t, ok := bv.hits(j, origin, dir, 1e-4, rayMinT); ok && t < bestT {
					best, bestT = int(fi), t
				}
			}
			continue
		}
		// Nearer child last onto the stack, so it is walked first and the far
		// one is usually culled by the hit it finds.
		l, r := &bv.nodes[n.left], &bv.nodes[n.right]
		tl, okl := enterBox(origin, inv, l.lo, l.hi, bestT)
		tr, okr := enterBox(origin, inv, r.lo, r.hi, bestT)
		switch {
		case okl && okr:
			if tl < tr {
				stack[sp], stack[sp+1] = bvhVisit{n.right, tr}, bvhVisit{n.left, tl}
			} else {
				stack[sp], stack[sp+1] = bvhVisit{n.left, tl}, bvhVisit{n.right, tr}
			}
			sp += 2
		case okl:
			stack[sp] = bvhVisit{n.left, tl}
			sp++
		case okr:
			stack[sp] = bvhVisit{n.right, tr}
			sp++
		}
	}
	return best, bestT, best >= 0
}

// bvhVisit is a node waiting on nearest's stack, and where the ray enters it.
type bvhVisit struct {
	node  int32
	enter float32
}

// shadowSlack is how far past a face's edge a shadow ray still counts as
// hitting it. CSG and the grid cut neighbouring faces at points computed
// separately, so a shared edge is two edges a float's width apart, and a ray
// down exactly that line would otherwise see light through solid ground.
const shadowSlack = 0.01

// occluded is whether anything blocks the ray from origin along dir (unit
// length) before maxT, ignoring face skip — the shadow test (light.go).
//
// Only a face met from its front blocks, or a double-sided one met either way.
// Every drawn solid is closed, so a ray from open air always meets a front face
// first, and a back only when it started inside something: a detail face inset
// into its own hull, or a cover face (Face.CoveredBy) under its neighbour's
// solid, whose answer is the light it gets from outside that solid. buried
// says the caller could not find open air to start from (baker.airStart), and
// then a back counts too — the vertex cannot tell the solid it is in from a
// wall between it and the light, and letting light through walls is the worse
// mistake.
//
// A front face is taken from a hair BEHIND the origin, not in front of it as a
// bounce ray's hits are. A vertex where a floor meets a wall sits on the wall's
// plane, and its ray toward a light beyond the wall meets that face at t = 0 —
// skip it and the ray is inside the wall, sees only backs, and comes out lit.
func (bv *faceBVH) occluded(origin, dir mgl32.Vec3, maxT float32, skip int, buried bool) bool {
	if len(bv.nodes) == 0 {
		return false
	}
	var inv mgl32.Vec3
	for a := 0; a < 3; a++ {
		if dir[a] != 0 {
			inv[a] = 1 / dir[a]
		} else {
			inv[a] = float32(math.Inf(1))
		}
	}
	var stack [64]int32
	sp := 0
	stack[sp] = 0
	sp++
	for sp > 0 {
		sp--
		n := &bv.nodes[stack[sp]]
		if _, ok := enterBox(origin, inv, n.lo, n.hi, maxT); !ok {
			continue
		}
		if n.count == 0 {
			stack[sp], stack[sp+1] = n.left, n.right
			sp += 2
			continue
		}
		for j := n.left; j < n.left+n.count; j++ {
			pf := &bv.packed[j]
			if int(bv.items[j]) == skip || (!buried && !pf.doubleSided && pf.plane.N.Dot(dir) >= 0) {
				continue
			}
			if t, ok := bv.hits(j, origin, dir, shadowSlack, -shadowSlack); ok && t < maxT {
				return true
			}
		}
	}
	return false
}

// enterBox is where a ray enters a box (0 if it starts inside), and whether it
// does so before tMax.
func enterBox(o, inv, lo, hi mgl32.Vec3, tMax float32) (float32, bool) {
	t0, t1 := float32(0), tMax
	for a := 0; a < 3; a++ {
		n, f := (lo[a]-o[a])*inv[a], (hi[a]-o[a])*inv[a]
		if n > f {
			n, f = f, n
		}
		// NaN from 0·Inf (a ray lying in a slab's face) compares false, which
		// leaves t0/t1 alone: the ray is inside that slab.
		if n > t0 {
			t0 = n
		}
		if f < t1 {
			t1 = f
		}
		if t0 > t1 {
			return 0, false
		}
	}
	return t0, true
}

// rayMinT is the nearest a hit may be. Anything closer is the surface the ray
// set out from, or one meeting it at the corner the ray started in.
const rayMinT = 1e-3

// rayHitsFace is where a ray meets a convex face, if it does.
func rayHitsFace(f *Face, o, d mgl32.Vec3) (float32, bool) {
	return rayHitsFaceWithin(f, o, d, 1e-4, rayMinT)
}

// hits is rayHitsFaceWithin for the packed face j (items[j]): the same test in
// the same arithmetic, read from packed and edges.
func (bv *faceBVH) hits(j int32, o, d mgl32.Vec3, slack, minT float32) (float32, bool) {
	pf := &bv.packed[j]
	den := pf.plane.N.Dot(d)
	if den > -1e-7 && den < 1e-7 {
		return 0, false // along the plane
	}
	t := -pf.plane.Distance(o) / den
	if t < minT {
		return 0, false
	}
	p := o.Add(d.Mul(t))
	pos, neg := false, false
	for _, ed := range bv.edges[pf.first : pf.first+pf.count] {
		s := ed.e.Cross(p.Sub(ed.a)).Dot(pf.plane.N)
		tol := slack * ed.len
		if s > tol {
			pos = true
		} else if s < -tol {
			neg = true
		}
		if pos && neg {
			return 0, false
		}
	}
	return t, true
}

// rayHitsFaceWithin is rayHitsFace counting a ray that passes within slack
// (world units) outside an edge as a hit, and taking hits from minT on.
func rayHitsFaceWithin(f *Face, o, d mgl32.Vec3, slack, minT float32) (float32, bool) {
	den := f.Plane.N.Dot(d)
	if den > -1e-7 && den < 1e-7 {
		return 0, false // along the plane
	}
	t := -f.Plane.Distance(o) / den
	if t < minT {
		return 0, false
	}
	p := o.Add(d.Mul(t))
	// Inside every edge. The winding is taken as it comes: the sign every edge
	// agrees on is the inside, whichever way round the corners run.
	pts := f.Points
	pos, neg := false, false
	for i := range pts {
		a, b := pts[i], pts[(i+1)%len(pts)]
		e := b.Sub(a)
		s := e.Cross(p.Sub(a)).Dot(f.Plane.N)
		tol := slack * e.Len() // s is the distance from the edge times its length
		if s > tol {
			pos = true
		} else if s < -tol {
			neg = true
		}
		if pos && neg {
			return 0, false
		}
	}
	return t, true
}
