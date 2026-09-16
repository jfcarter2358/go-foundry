package collision

// raycast_accel.go — spatial culling for rays against the level's geometry.
//
// A ray used to walk every triangle of every chunk: fine for the editor's
// click, ruinous for a level of guards looking for the player five times a
// second. What replaces the walk is two levels of bounding boxes, the usual
// ray-tracing arrangement:
//
//   - Per chunk, a bounding-volume hierarchy over its static faces, built in
//     the chunk's own coordinates. A chunk that moves, turns or rides a frame
//     keeps its tree: the ray is carried into the chunk, not the tree out of it.
//   - Across chunks, each chunk's tree root carried into world space through
//     the chunk's live transform, tested before the ray is carried in at all.
//
// A compiled level's static surfaces are already in world space and never
// change, so they get one tree of their own.
//
// # What a box is built from
//
// The vertices a ray is actually tested against — each face's drawn geometry,
// the detail mesh for a detailed brush — and never the hull's corners, which a
// detail mesh may stand proud of. Boxes are padded a hair, because a box is a
// promise that nothing outside it can be hit, and float error on its surface
// must not break that promise for a ray grazing it.
//
// # Brushes that move
//
// A dynamic brush can be anywhere by the next frame, so it is not in any tree.
// Each one keeps its own box in authored coordinates, and the placed box and
// the placement's inverse are cached against the three fields that move it —
// recomputed only on a frame the brush actually moved.
//
// # Knowing when a tree is stale
//
// Every change to a brush's drawn mesh goes through BrushMesh.Load, Release or
// Unload, and each bumps world.BrushMeshGeneration, as does flipping Dynamic.
// A chunk's tree is stamped with the generation it was built at and with the
// identity of the brush slice it indexes; when either differs, a cheap
// fingerprint of the faces decides whether to rebuild or merely re-stamp — so
// an editor drag in one room does not rebuild every other room's tree.
//
// # Nearest hit, and any hit
//
// Only the nearest piece of geometry survives a cast (Raycast truncates at the
// first blocking hit), so traversal visits boxes nearest first and stops
// entering any that start beyond the nearest hit so far. A line-of-sight test
// wants less still — whether anything is hit at all — and LineBlocked stops at
// the first triangle it finds.

import (
	"math"
	"sort"
	"sync"
	"unsafe"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/gfx"
	"foundry/internal/world"
)

// ---------- boxes ----------

type aabb struct{ lo, hi mgl32.Vec3 }

func emptyBox() aabb {
	inf := float32(math.MaxFloat32)
	return aabb{lo: mgl32.Vec3{inf, inf, inf}, hi: mgl32.Vec3{-inf, -inf, -inf}}
}

func (b aabb) valid() bool { return b.lo.X() <= b.hi.X() }

func (b *aabb) addPoint(p mgl32.Vec3) {
	for i := 0; i < 3; i++ {
		if p[i] < b.lo[i] {
			b.lo[i] = p[i]
		}
		if p[i] > b.hi[i] {
			b.hi[i] = p[i]
		}
	}
}

func (b *aabb) addBox(o aabb) {
	if !o.valid() {
		return
	}
	b.addPoint(o.lo)
	b.addPoint(o.hi)
}

// padded grows a box by a margin relative to its size and position, so a ray
// grazing a face on the box's surface is never culled by float error.
func (b aabb) padded() aabb {
	if !b.valid() {
		return b
	}
	m := float32(1e-4)
	for i := 0; i < 3; i++ {
		m = maxf32(m, 1e-5*maxf32(absf(b.lo[i]), absf(b.hi[i])))
	}
	d := mgl32.Vec3{m, m, m}
	return aabb{lo: b.lo.Sub(d), hi: b.hi.Add(d)}
}

// transformed is the axis-aligned box around this one carried through m.
func (b aabb) transformed(m mgl32.Mat4) aabb {
	if !b.valid() {
		return b
	}
	out := emptyBox()
	for i := 0; i < 8; i++ {
		c := mgl32.Vec3{b.lo.X(), b.lo.Y(), b.lo.Z()}
		if i&1 != 0 {
			c[0] = b.hi.X()
		}
		if i&2 != 0 {
			c[1] = b.hi.Y()
		}
		if i&4 != 0 {
			c[2] = b.hi.Z()
		}
		r := m.Mul4x1(c.Vec4(1))
		out.addPoint(mgl32.Vec3{r.X(), r.Y(), r.Z()})
	}
	return out.padded()
}

// geometryBox bounds a de-indexed [X,Y,Z,U,V] vertex list.
func geometryBox(g *gfx.Geometry) aabb {
	b := emptyBox()
	v := g.Vertices
	for i := 0; i+4 < len(v); i += 5 {
		b.addPoint(mgl32.Vec3{v[i], v[i+1], v[i+2]})
	}
	return b.padded()
}

// rayDir is a direction prepared for slab tests: its reciprocal, with a huge
// finite stand-in for the reciprocal of zero so no test ever multiplies zero by
// infinity.
type rayDir struct {
	o, inv mgl32.Vec3
}

func newRayDir(o, d mgl32.Vec3) rayDir {
	var inv mgl32.Vec3
	for i := 0; i < 3; i++ {
		switch {
		case d[i] > 1e-12:
			inv[i] = 1 / d[i]
		case d[i] < -1e-12:
			inv[i] = 1 / d[i]
		case math.Signbit(float64(d[i])):
			inv[i] = -1e30
		default:
			inv[i] = 1e30
		}
	}
	return rayDir{o: o, inv: inv}
}

// enter returns where the ray enters the box, clamped to 0 for an origin inside
// it, and whether it does so at or before tMax.
func (r rayDir) enter(lo, hi *[3]float32, tMax float32) (float32, bool) {
	tmin, tmax := float32(0), tMax
	for i := 0; i < 3; i++ {
		t1 := (lo[i] - r.o[i]) * r.inv[i]
		t2 := (hi[i] - r.o[i]) * r.inv[i]
		if t1 > t2 {
			t1, t2 = t2, t1
		}
		if t1 > tmin {
			tmin = t1
		}
		if t2 < tmax {
			tmax = t2
		}
		if tmin > tmax {
			return 0, false
		}
	}
	return tmin, true
}

func (r rayDir) enterBox(b aabb, tMax float32) (float32, bool) {
	if !b.valid() {
		return 0, false
	}
	lo, hi := [3]float32(b.lo), [3]float32(b.hi)
	return r.enter(&lo, &hi, tMax)
}

// ---------- the tree ----------

type bvhNode struct {
	lo, hi [3]float32
	// A leaf has count > 0 and holds items[start : start+count]. An inner node
	// has count 0 and its children at start and start+1.
	start, count int32
}

type bvh struct {
	nodes []bvhNode
	items []int32
}

const bvhLeafSize = 2

// buildBVH builds a tree over boxes, splitting each node at the median centroid
// along its longest axis.
func buildBVH(boxes []aabb) bvh {
	t := bvh{items: make([]int32, 0, len(boxes))}
	for i := range boxes {
		if boxes[i].valid() {
			t.items = append(t.items, int32(i))
		}
	}
	if len(t.items) == 0 {
		return t
	}
	t.nodes = make([]bvhNode, 1, 2*len(t.items))
	type job struct{ node, start, end int32 }
	stack := []job{{0, 0, int32(len(t.items))}}
	for len(stack) > 0 {
		j := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		bounds, cent := emptyBox(), emptyBox()
		for _, it := range t.items[j.start:j.end] {
			bounds.addBox(boxes[it])
			cent.addPoint(boxes[it].lo.Add(boxes[it].hi).Mul(0.5))
		}
		n := &t.nodes[j.node]
		n.lo, n.hi = [3]float32(bounds.lo), [3]float32(bounds.hi)
		if j.end-j.start <= bvhLeafSize {
			n.start, n.count = j.start, j.end-j.start
			continue
		}
		ext := cent.hi.Sub(cent.lo)
		axis := 0
		if ext[1] > ext[axis] {
			axis = 1
		}
		if ext[2] > ext[axis] {
			axis = 2
		}
		span := t.items[j.start:j.end]
		sort.Slice(span, func(a, b int) bool {
			return boxes[span[a]].lo[axis]+boxes[span[a]].hi[axis] < boxes[span[b]].lo[axis]+boxes[span[b]].hi[axis]
		})
		mid := j.start + (j.end-j.start)/2
		left := int32(len(t.nodes))
		t.nodes = append(t.nodes, bvhNode{}, bvhNode{})
		// n may have moved with the append.
		t.nodes[j.node].start, t.nodes[j.node].count = left, 0
		stack = append(stack, job{left, j.start, mid}, job{left + 1, mid, j.end})
	}
	return t
}

// root is the tree's whole box, or an invalid one for an empty tree.
func (t *bvh) root() aabb {
	if len(t.nodes) == 0 {
		return emptyBox()
	}
	return aabb{lo: t.nodes[0].lo, hi: t.nodes[0].hi}
}

// walk visits the items of every leaf the ray enters before *tMax, nearest box
// first. visit may lower *tMax as it finds hits, which prunes what is left, or
// return true to stop the walk altogether.
func (t *bvh) walk(r rayDir, tMax *float32, visit func(item int32) bool) bool {
	if len(t.nodes) == 0 {
		return false
	}
	if _, ok := r.enter(&t.nodes[0].lo, &t.nodes[0].hi, *tMax); !ok {
		return false
	}
	var stackBuf [64]int32
	stack := stackBuf[:0]
	stack = append(stack, 0)
	for len(stack) > 0 {
		ni := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := &t.nodes[ni]
		// Re-tested on the way off the stack: the hit that lowered tMax since
		// this node was pushed may have put it out of reach.
		if _, ok := r.enter(&n.lo, &n.hi, *tMax); !ok {
			continue
		}
		if n.count > 0 {
			for _, it := range t.items[n.start : n.start+n.count] {
				if visit(it) {
					return true
				}
			}
			continue
		}
		a, b := n.start, n.start+1
		ta, oka := r.enter(&t.nodes[a].lo, &t.nodes[a].hi, *tMax)
		tb, okb := r.enter(&t.nodes[b].lo, &t.nodes[b].hi, *tMax)
		switch {
		case oka && okb:
			// Push the far one first so the near one is walked first.
			if ta <= tb {
				stack = append(stack, b, a)
			} else {
				stack = append(stack, a, b)
			}
		case oka:
			stack = append(stack, a)
		case okb:
			stack = append(stack, b)
		}
	}
	return false
}

// ---------- a chunk's culling ----------

type faceRef struct{ brush, face int32 }

// looseBrush is a dynamic brush: outside the tree, with its own box.
type looseBrush struct {
	brush int32
	// authored bounds the faces in the brush's authored coordinates; box is
	// them carried through the placement, in chunk coordinates.
	authored aabb
	box      aabb
	// The placement the cached box and inverse were computed for: Offset,
	// Rotation and Scale, as one block (see placementOf).
	placement [9]float32
	atRest    bool
	inv       mgl32.Mat4
	cached    bool
}

type chunkAccel struct {
	// What the tree was built from: the brush slice's identity, whether the
	// chunk was compiled, the mesh generation, and a fingerprint of its faces.
	brushes  unsafe.Pointer
	nBrushes int
	compiled bool
	gen      uint64
	print    uint64

	faces []faceRef
	tree  bvh
	loose []looseBrush

	// The chunk's whole local box — tree and loose brushes — and it carried
	// into world space, for the placement that last put it there. The
	// placement is kept as the fields ModelMatrix is built from, so a chunk
	// that has not moved costs a comparison rather than a matrix with a sine
	// in it.
	local  aabb
	key    placementKey
	inv    mgl32.Mat4
	world  aabb
	hasInv bool
	placed bool
}

// placementKey is everything Chunk.ModelMatrix reads.
type placementKey struct {
	origin     mgl32.Vec3
	rotation   float32
	scale      float32
	frameScale float32
	xform      mgl32.Mat4
	inFrame    bool
}

// fingerprint hashes what a chunk's tree depends on without reading a single
// vertex: which faces draw, the identity and size of each face's vertex list,
// and which brushes may move.
func fingerprint(ch *world.Chunk) uint64 {
	h := uint64(1469598103934665603)
	mix := func(v uint64) {
		h ^= v
		h *= 1099511628211
	}
	mix(uint64(len(ch.Brushes)))
	for bi := range ch.Brushes {
		b := &ch.Brushes[bi]
		if b.Dynamic {
			mix(1)
		} else {
			mix(2)
		}
		if ch.DrawsBrushLive(b) {
			mix(3)
		}
		faces := b.DrawFaces()
		mix(uint64(len(faces)))
		for fi := range faces {
			v := faces[fi].Geometry.Vertices
			mix(uint64(uintptr(unsafe.Pointer(unsafe.SliceData(v)))))
			mix(uint64(len(v)))
		}
	}
	return h
}

// sync makes a chunk's culling current: its tree, its loose brushes' placed
// boxes, and its world box. It reports whether the chunk has anything a ray
// could hit.
func (ca *chunkAccel) sync(ch *world.Chunk, gen uint64) bool {
	ptr := unsafe.Pointer(unsafe.SliceData(ch.Brushes))
	if ptr != ca.brushes || len(ch.Brushes) != ca.nBrushes || ch.Compiled != ca.compiled || gen != ca.gen {
		print := fingerprint(ch)
		if print != ca.print || ptr != ca.brushes || len(ch.Brushes) != ca.nBrushes || ch.Compiled != ca.compiled {
			ca.build(ch)
			ca.print = print
		}
		ca.brushes, ca.nBrushes, ca.compiled, ca.gen = ptr, len(ch.Brushes), ch.Compiled, gen
	}

	localChanged := false
	for i := range ca.loose {
		lb := &ca.loose[i]
		b := &ch.Brushes[lb.brush]
		pl := placementOf(b)
		if lb.cached && *pl == lb.placement {
			continue
		}
		lb.placement, lb.cached = *pl, true
		lb.atRest = b.AtRest()
		if lb.atRest {
			lb.box, lb.inv = lb.authored, mgl32.Ident4()
		} else {
			p := b.Placement()
			lb.box = lb.authored.transformed(p)
			lb.inv = p.Inv()
		}
		localChanged = true
	}
	if localChanged || !ca.local.valid() && (len(ca.tree.nodes) > 0 || len(ca.loose) > 0) {
		ca.local = ca.tree.root()
		for i := range ca.loose {
			ca.local.addBox(ca.loose[i].box)
		}
		ca.placed = false
	}
	if !ca.local.valid() {
		return false
	}

	key := placementKey{ch.Origin, ch.Rotation, ch.Scale, ch.FrameScale, ch.Xform, ch.InFrame()}
	if !ca.placed || key != ca.key {
		ca.key = key
		m := ch.ModelMatrix()
		ca.inv = m.Inv()
		ca.hasInv = ca.inv != (mgl32.Mat4{})
		ca.world = ca.local.transformed(m)
		ca.placed = true
	}
	return ca.hasInv
}

// build indexes a chunk's live brushes: static faces into the tree, dynamic
// brushes aside.
func (ca *chunkAccel) build(ch *world.Chunk) {
	ca.faces = ca.faces[:0]
	ca.loose = ca.loose[:0]
	var boxes []aabb
	for bi := range ch.Brushes {
		b := &ch.Brushes[bi]
		if !ch.DrawsBrushLive(b) {
			continue
		}
		faces := b.DrawFaces()
		if b.Dynamic || !b.AtRest() {
			lb := looseBrush{brush: int32(bi), authored: emptyBox()}
			for fi := range faces {
				lb.authored.addBox(geometryBox(&faces[fi].Geometry))
			}
			if lb.authored.valid() {
				ca.loose = append(ca.loose, lb)
			}
			continue
		}
		for fi := range faces {
			box := geometryBox(&faces[fi].Geometry)
			if !box.valid() {
				continue
			}
			ca.faces = append(ca.faces, faceRef{brush: int32(bi), face: int32(fi)})
			boxes = append(boxes, box)
		}
	}
	ca.tree = buildBVH(boxes)
	ca.local = emptyBox()
	ca.placed = false
}

// placementOf is a brush's Offset, Rotation and Scale read as one array, so a
// brush that has not moved costs one block comparison per ray rather than nine
// field comparisons. The three are declared consecutively in world.Brush;
// TestBrushPlacementFieldsAreContiguous keeps them so.
func placementOf(b *world.Brush) *[9]float32 {
	return (*[9]float32)(unsafe.Pointer(&b.Offset))
}

// ---------- the level's culling ----------

type levelAccel struct {
	chunks   unsafe.Pointer
	nChunks  int
	perChunk []chunkAccel

	static  unsafe.Pointer
	nStatic int
	stree   bvh
}

var (
	accelMu sync.Mutex
	accels  = map[*world.Level]*levelAccel{}
)

// maxCachedLevels bounds the cache: the editor has a level and the entity
// editor's scratch level open at once, and a level replaced by a load leaves
// its entry behind to be evicted.
const maxCachedLevels = 4

// accelFor returns the level's culling, rebuilding what has gone stale. The
// caller holds accelMu.
func accelFor(lvl *world.Level) *levelAccel {
	la := accels[lvl]
	if la == nil {
		if len(accels) >= maxCachedLevels {
			for k := range accels {
				delete(accels, k)
				break
			}
		}
		la = &levelAccel{}
		accels[lvl] = la
	}
	if ptr := unsafe.Pointer(unsafe.SliceData(lvl.Chunks)); ptr != la.chunks || len(lvl.Chunks) != la.nChunks {
		la.chunks, la.nChunks = ptr, len(lvl.Chunks)
		// Chunks keep their own stamps, so a slice that merely grew rebuilds
		// only what differs.
		if cap(la.perChunk) >= len(lvl.Chunks) {
			la.perChunk = la.perChunk[:len(lvl.Chunks)]
		} else {
			grown := make([]chunkAccel, len(lvl.Chunks))
			copy(grown, la.perChunk)
			la.perChunk = grown
		}
	}
	var sptr unsafe.Pointer
	n := 0
	if lvl.Static != nil {
		sptr, n = unsafe.Pointer(unsafe.SliceData(lvl.Static.Surfaces)), len(lvl.Static.Surfaces)
	}
	if sptr != la.static || n != la.nStatic {
		la.static, la.nStatic = sptr, n
		boxes := make([]aabb, n)
		for i := 0; i < n; i++ {
			boxes[i] = geometryBox(&lvl.Static.Surfaces[i].Geometry)
		}
		la.stree = buildBVH(boxes)
	}
	return la
}

// WarmRaycasts builds every acceleration structure a raycast into lvl would
// otherwise build on demand: the compiled surfaces' tree and every chunk's,
// switched off or not.
//
// For a level install, behind the loading screen. Built lazily, the first
// sight line an AI agent casts in a streamed level paid for the whole compiled
// tree — 117 ms over level1's 67,000 baked surfaces, on the frame the ground's
// enemies woke — and each chunk's on its first cast after being switched on.
func WarmRaycasts(lvl *world.Level) {
	if lvl == nil {
		return
	}
	accelMu.Lock()
	defer accelMu.Unlock()
	la := accelFor(lvl)
	gen := world.BrushMeshGeneration()
	for ci := range lvl.Chunks {
		la.perChunk[ci].sync(&lvl.Chunks[ci], gen)
	}
}

// ---------- queries ----------

// geometryQuery is one cast through the level's geometry.
type geometryQuery struct {
	lvl    *world.Level
	origin mgl32.Vec3
	dir    mgl32.Vec3
	ignore map[string]bool
	// tMax is how far the ray still reaches: its range, lowered to the nearest
	// hit as they are found.
	tMax float32
	// any stops at the first hit rather than finding the nearest.
	any bool
	hit bool
	// hits collects every hit no farther than tMax at the time it was found.
	hits []RayHit
}

func (q *geometryQuery) record(kind RayHitKind, chunk, id string, t float32) bool {
	if t > q.tMax {
		return false
	}
	q.hit = true
	if q.any {
		return true
	}
	q.tMax = t
	q.hits = append(q.hits, RayHit{Kind: kind, Chunk: chunk, ID: id, T: t, Point: q.origin.Add(q.dir.Mul(t))})
	return false
}

// run casts the query through the compiled surfaces and every chunk.
func (q *geometryQuery) run() {
	accelMu.Lock()
	defer accelMu.Unlock()
	lvl := q.lvl
	la := accelFor(lvl)

	if lvl.Static != nil && len(la.stree.nodes) > 0 {
		r := newRayDir(q.origin, q.dir)
		if la.stree.walk(r, &q.tMax, func(it int32) bool {
			surf := &lvl.Static.Surfaces[it]
			if (len(q.ignore) > 0 && q.ignore[surf.Brush]) || lvl.Static.Hidden(int(it)) {
				return false
			}
			t, ok := rayGeometry(q.origin, q.dir, &surf.Geometry)
			return ok && q.record(RayHitBrush, surf.Chunk, surf.Brush, t)
		}) {
			return
		}
	}

	gen := world.BrushMeshGeneration()
	wr := newRayDir(q.origin, q.dir)
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		// A compiled chunk's static brushes are the surfaces above; only its
		// dynamic ones are drawn live, and the chunk's tree already knows that.
		if !ch.IsEnabled() {
			continue
		}
		ca := &la.perChunk[ci]
		if !ca.sync(ch, gen) {
			continue
		}
		if _, ok := wr.enterBox(ca.world, q.tMax); !ok {
			continue
		}
		// Moving the ray into the chunk rather than the geometry into the world
		// leaves t alone — origin and direction are transformed together.
		lo := ca.inv.Mul4x1(q.origin.Vec4(1))
		ld := ca.inv.Mul4x1(q.dir.Vec4(0))
		lOrigin := mgl32.Vec3{lo.X(), lo.Y(), lo.Z()}
		lDir := mgl32.Vec3{ld.X(), ld.Y(), ld.Z()}
		local := newRayDir(lOrigin, lDir)

		if ca.tree.walk(local, &q.tMax, func(it int32) bool {
			ref := ca.faces[it]
			b := &ch.Brushes[ref.brush]
			if len(q.ignore) > 0 && q.ignore[b.ID] {
				return false
			}
			faces := b.DrawFaces()
			if int(ref.face) >= len(faces) {
				return false
			}
			t, ok := rayGeometry(lOrigin, lDir, &faces[ref.face].Geometry)
			return ok && q.record(RayHitBrush, ch.ID, b.ID, t)
		}) {
			return
		}

		for li := range ca.loose {
			lb := &ca.loose[li]
			if _, ok := local.enterBox(lb.box, q.tMax); !ok {
				continue
			}
			b := &ch.Brushes[lb.brush]
			if len(q.ignore) > 0 && q.ignore[b.ID] {
				continue
			}
			// The uploaded meshes are in the brush's authored coordinates, so a
			// brush a script has moved takes the ray one space further in.
			bOrigin, bDir := lOrigin, lDir
			if !lb.atRest {
				bo := lb.inv.Mul4x1(lOrigin.Vec4(1))
				bd := lb.inv.Mul4x1(lDir.Vec4(0))
				bOrigin = mgl32.Vec3{bo.X(), bo.Y(), bo.Z()}
				bDir = mgl32.Vec3{bd.X(), bd.Y(), bd.Z()}
			}
			faces := b.DrawFaces()
			for fi := range faces {
				t, ok := rayGeometry(bOrigin, bDir, &faces[fi].Geometry)
				if ok && q.record(RayHitBrush, ch.ID, b.ID, t) {
					return
				}
			}
		}
	}
}

// nearest returns the hits tied for nearest, the only geometry a cast keeps.
func (q *geometryQuery) nearest() []RayHit {
	out := q.hits[:0]
	for _, h := range q.hits {
		if h.T <= q.tMax {
			out = append(out, h)
		}
	}
	return out
}

// LineBlocked reports whether the level's geometry — its brushes, compiled
// surfaces and solid scenery — lies anywhere on the segment from one point to
// another. It is Raycast's Blocked for a cast of geometry alone, answered by
// stopping at the first thing found rather than finding the nearest, which is
// all a line-of-sight test needs. ignore names brushes and scenery the segment
// passes through.
func LineBlocked(lvl *world.Level, from, to mgl32.Vec3, ignore map[string]bool) bool {
	if lvl == nil {
		return false
	}
	d := to.Sub(from)
	dist := d.Len()
	if dist < 1e-6 {
		return false
	}
	dir := d.Mul(1 / dist)
	q := geometryQuery{lvl: lvl, origin: from, dir: dir, ignore: ignore, tMax: dist, any: true}
	q.run()
	if q.hit {
		return true
	}
	limit := dist
	for i := range lvl.Scenery {
		if t, ok := sceneryHit(&lvl.Scenery[i], from, dir, limit, ignore); ok && t <= limit {
			return true
		}
	}
	return false
}

// ---------- bodies ----------

// sphereReaches reports whether a ray's first tMax units pass within radius of
// centre — the cheap test that spares most bodies a matrix inverse.
func sphereReaches(origin, dir, centre mgl32.Vec3, radius, tMax float32) bool {
	oc := centre.Sub(origin)
	t := oc.Dot(dir)
	if t < 0 {
		t = 0
	} else if t > tMax {
		t = tMax
	}
	closest := oc.Sub(dir.Mul(t))
	return closest.Dot(closest) <= radius*radius
}

// maxColumnScale is the largest factor a matrix stretches any axis by.
func maxColumnScale(m mgl32.Mat4) float32 {
	return maxf32(m.Col(0).Vec3().Len(), maxf32(m.Col(1).Vec3().Len(), m.Col(2).Vec3().Len()))
}

// sceneryHit tests one prop as a blocker, through the same proxy box a body is
// stopped by.
func sceneryHit(s *world.Scenery, origin, dir mgl32.Vec3, tMax float32, ignore map[string]bool) (float32, bool) {
	if !s.Solid || !s.HasBounds() || (len(ignore) > 0 && ignore[s.ID]) {
		return 0, false
	}
	// A sphere about the placement's origin wide enough for the box at any
	// turn: the distance to the box's centre, which a rotation cannot change,
	// plus the box's own half-diagonal. No matrix, no sine.
	// Summed rather than square-rooted: the sum of a vector's components'
	// sizes is never less than its length, so the sphere stays wide enough.
	sc := s.EffectiveScale()
	c := s.LocalMin.Add(s.LocalMax).Mul(0.5)
	h := s.LocalMax.Sub(s.LocalMin).Mul(0.5)
	var reach float32
	for i := 0; i < 3; i++ {
		k := absf(sc[i])
		reach += (absf(c[i]) + h[i]) * k
	}
	if !sphereReaches(origin, dir, s.Position, reach*1.001+1e-4, tMax) {
		return 0, false
	}
	proxy, ok := sceneryProxy(s)
	if !ok {
		return 0, false
	}
	m := mgl32.Translate3D(proxy.Position.X(), proxy.Position.Y(), proxy.Position.Z()).
		Mul4(world.EulerMatrixYXZ(proxy.Rotation))
	return rayBounds(origin, dir, m, proxy.BoundHalfExtents(), proxy.BoundingKind)
}

func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func maxf32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
