package cluster

// simplify.go — the mesh simplifier the cluster DAG (dag.go) is built with.
// Phase 2 of the Nanite plan (docs/nanite/PHASE_2.md).
//
// Edge collapse under quadric error metrics (Garland & Heckbert), the shape
// meshoptimizer's simplifier has: every collapse moves one POSITION onto a
// neighbouring one (a half-edge collapse), so the simplified mesh uses a
// subset of the input's vertices and never makes a new one. That is what lets
// every level of a Nanite mesh's DAG index the one vertex pool the cook wrote:
// a coarse cluster is only a different index list over the same records.
//
// # What a collapse costs
//
// Each position carries a quadric — the area-weighted sum of the squared
// distances to the planes of the triangles it has absorbed — and the cost of
// moving P onto Q is that sum for both, evaluated at Q, over their area: a
// mean squared distance from where the surface was to where it now is.
// Borders (an edge with one triangle) and attribute seams add a plane through
// the edge, at right angles to its face, so their outline is kept as well as
// the surface.
//
// Attributes are weighed in too (AttrWeights): normals, UVs, a compiled
// level's seven-float baked light record — whatever floats follow x, y, z in
// a vertex. Each triangle fits each attribute as a linear function across
// its plane (a gradient and an offset), and a vertex record's attribute
// quadric is the area-weighted sum of the squared amount by which the value it
// carries misses those functions at its position. A collapse that keeps the
// attribute on the plane its neighbours lay out is free; one that smears a
// highlight or a UV island across the surface costs, in proportion to the
// weight. The weights are in distance units per attribute unit, so the cost
// stays a squared distance and the error it reports a distance.
//
// # What may move
//
// A position is one of:
//
//   - LOCKED: the caller's (a cluster group's border, which its neighbours'
//     simplification must meet exactly — the DAG's crack-freedom rests on it),
//     or one whose neighbourhood is not a disc or half-disc (non-manifold
//     edges, three or more vertex records at one point, a border and a seam
//     crossing). It never moves.
//   - INTERIOR with one vertex record: it may collapse along any edge.
//   - a SEAM: two vertex records at one point (a UV or crease seam), with the
//     seam running through it along exactly two edges. It may collapse only
//     along the seam, onto a position that is a seam too, each record going
//     to the record on its own side — so the seam stays a seam.
//   - a BORDER: an open edge runs through it, along exactly two edges. It may
//     collapse only along the border, so the outline stays put.
//
// A collapse must also keep the surface a manifold (the link condition: the
// two positions share no neighbour but the corners opposite their edge) and
// must not turn a triangle over or flatten it to nothing.

import (
	"container/heap"
	"math"
)

// SimplifyOptions are a simplification's settings.
type SimplifyOptions struct {
	// TargetTriangles is where to stop: the first collapse that leaves this
	// many or fewer triangles is the last.
	TargetTriangles int
	// MaxError, when > 0, stops early rather than make a collapse whose
	// error (a distance) would exceed it.
	MaxError float32
	// AttrWeights weighs each attribute float — the floats after x, y, z in a
	// vertex, in order. Missing or zero weights ignore that attribute.
	AttrWeights []float32
	// Locked, when set, reports vertices that must not move; every vertex at
	// a locked vertex's position is locked with it.
	Locked func(v uint32) bool
}

// SimplifyResult is a simplification's outcome.
type SimplifyResult struct {
	// Indices is the simplified triangle list, over the input's vertices.
	Indices []uint32
	// Error is the largest collapse cost made, as a distance: the square
	// root of the mean squared distance (attributes weighed in) the worst
	// collapse moved the surface by. Zero for collapses that changed nothing
	// a quadric sees — coplanar points with linear attributes.
	Error float32
}

// Simplify collapses edges of the triangle list indices — over vertices,
// stride floats a vertex with x, y, z first — until TargetTriangles is met or
// no collapse is allowed. The input is not modified.
func Simplify(vertices []float32, stride int, indices []uint32, opt SimplifyOptions) SimplifyResult {
	if stride < 3 {
		stride = 3
	}
	s := newSimplifier(vertices, stride, indices, opt)
	s.run()
	return SimplifyResult{Indices: s.output(), Error: float32(math.Sqrt(math.Max(s.maxCost, 0)))}
}

// quadric is a position's: xᵀAx + 2bᵀx + c, over area w.
type quadric struct {
	a00, a01, a02, a11, a12, a22 float64
	b0, b1, b2                   float64
	c                            float64
	w                            float64
}

func (q *quadric) add(o *quadric) {
	q.a00 += o.a00
	q.a01 += o.a01
	q.a02 += o.a02
	q.a11 += o.a11
	q.a12 += o.a12
	q.a22 += o.a22
	q.b0 += o.b0
	q.b1 += o.b1
	q.b2 += o.b2
	q.c += o.c
	q.w += o.w
}

// addPlane adds weight * (n·x + d)²; area is added to w (0 for a border's
// plane, which constrains but is not surface).
func (q *quadric) addPlane(n [3]float64, d, weight, area float64) {
	q.a00 += weight * n[0] * n[0]
	q.a01 += weight * n[0] * n[1]
	q.a02 += weight * n[0] * n[2]
	q.a11 += weight * n[1] * n[1]
	q.a12 += weight * n[1] * n[2]
	q.a22 += weight * n[2] * n[2]
	q.b0 += weight * n[0] * d
	q.b1 += weight * n[1] * d
	q.b2 += weight * n[2] * d
	q.c += weight * d * d
	q.w += area
}

func (q *quadric) eval(p [3]float64) float64 {
	x, y, z := p[0], p[1], p[2]
	r := q.a00*x*x + q.a11*y*y + q.a22*z*z + 2*(q.a01*x*y+q.a02*x*z+q.a12*y*z) +
		2*(q.b0*x+q.b1*y+q.b2*z) + q.c
	return r
}

// attrQuadric is one attribute's quadric for a vertex record: with
// u = (g, d) a triangle's fit of the attribute (value ≈ g·x + d) and
// X = (x, 1), the sum of weight·(u·X − a)² is XᵀMX − 2a(bvec·X) + a²c.
type attrQuadric struct {
	m    [10]float64 // symmetric 4x4: 00 01 02 03 11 12 13 22 23 33
	bvec [4]float64
	c    float64
}

func (q *attrQuadric) add(o *attrQuadric) {
	for i := range q.m {
		q.m[i] += o.m[i]
	}
	for i := range q.bvec {
		q.bvec[i] += o.bvec[i]
	}
	q.c += o.c
}

func (q *attrQuadric) addFit(u [4]float64, weight float64) {
	k := 0
	for i := 0; i < 4; i++ {
		for j := i; j < 4; j++ {
			q.m[k] += weight * u[i] * u[j]
			k++
		}
	}
	for i := 0; i < 4; i++ {
		q.bvec[i] += weight * u[i]
	}
	q.c += weight
}

func (q *attrQuadric) eval(p [3]float64, a float64) float64 {
	X := [4]float64{p[0], p[1], p[2], 1}
	var xmx float64
	k := 0
	for i := 0; i < 4; i++ {
		for j := i; j < 4; j++ {
			v := q.m[k] * X[i] * X[j]
			if i != j {
				v *= 2
			}
			xmx += v
			k++
		}
	}
	bx := q.bvec[0]*X[0] + q.bvec[1]*X[1] + q.bvec[2]*X[2] + q.bvec[3]
	return xmx - 2*a*bx + a*a*q.c
}

type simplifier struct {
	opt    SimplifyOptions
	stride int

	// Local vertex records: global index, position id, attribute values.
	global []uint32
	vpos   []int32
	attrs  []float64 // nattr per vertex
	// attrIdx are the attribute floats weighed (index after x, y, z), and
	// attrW their weights squared.
	attrIdx []int
	attrW2  []float64
	aq      []attrQuadric // nattr per vertex record

	// Positions.
	pos     [][3]float64
	locked  []bool
	posTris [][]int32
	pq      []quadric
	dead    []bool
	stamp   []uint32

	tris  [][3]int32 // local vertex ids
	alive []bool
	live  int

	maxCost float64
	h       collapseHeap
}

func newSimplifier(vertices []float32, stride int, indices []uint32, opt SimplifyOptions) *simplifier {
	s := &simplifier{opt: opt, stride: stride}
	for i, w := range opt.AttrWeights {
		if w > 0 && 3+i < stride {
			s.attrIdx = append(s.attrIdx, 3+i)
			s.attrW2 = append(s.attrW2, float64(w)*float64(w))
		}
	}
	na := len(s.attrIdx)

	local := make(map[uint32]int32, len(indices)/2)
	posOf := make(map[[3]uint32]int32, len(indices)/2)
	vertex := func(g uint32) int32 {
		if v, ok := local[g]; ok {
			return v
		}
		v := int32(len(s.global))
		local[g] = v
		s.global = append(s.global, g)
		b := int(g) * stride
		key := [3]uint32{math.Float32bits(vertices[b]), math.Float32bits(vertices[b+1]), math.Float32bits(vertices[b+2])}
		p, ok := posOf[key]
		if !ok {
			p = int32(len(s.pos))
			posOf[key] = p
			s.pos = append(s.pos, [3]float64{float64(vertices[b]), float64(vertices[b+1]), float64(vertices[b+2])})
			s.locked = append(s.locked, false)
		}
		s.vpos = append(s.vpos, p)
		for _, ai := range s.attrIdx {
			s.attrs = append(s.attrs, float64(vertices[b+ai]))
		}
		if opt.Locked != nil && opt.Locked(g) {
			s.locked[p] = true
		}
		return v
	}
	for t := 0; t+2 < len(indices); t += 3 {
		a, b, c := vertex(indices[t]), vertex(indices[t+1]), vertex(indices[t+2])
		if s.vpos[a] == s.vpos[b] || s.vpos[b] == s.vpos[c] || s.vpos[a] == s.vpos[c] {
			// Two corners at one point: it draws nothing, and would only
			// confuse the neighbourhoods below.
			continue
		}
		s.tris = append(s.tris, [3]int32{a, b, c})
	}
	np := len(s.pos)
	s.posTris = make([][]int32, np)
	s.pq = make([]quadric, np)
	s.dead = make([]bool, np)
	s.stamp = make([]uint32, np)
	s.alive = make([]bool, len(s.tris))
	s.live = len(s.tris)
	s.aq = make([]attrQuadric, len(s.global)*na)
	for t, tr := range s.tris {
		s.alive[t] = true
		for k := 0; k < 3; k++ {
			p := s.vpos[tr[k]]
			s.posTris[p] = append(s.posTris[p], int32(t))
		}
	}

	// Face quadrics, and each record's attribute fits.
	for _, tr := range s.tris {
		p0, p1, p2 := s.pos[s.vpos[tr[0]]], s.pos[s.vpos[tr[1]]], s.pos[s.vpos[tr[2]]]
		e1, e2 := sub3(p1, p0), sub3(p2, p0)
		n := cross3(e1, e2)
		l := len3(n)
		if l < 1e-30 {
			continue
		}
		area := l / 2
		n = scale3(n, 1/l)
		d := -dot3(n, p0)
		var fq quadric
		fq.addPlane(n, d, area, area)
		for k := 0; k < 3; k++ {
			s.pq[s.vpos[tr[k]]].add(&fq)
		}
		if na == 0 {
			continue
		}
		// The attribute's gradient across the triangle: g in its plane with
		// g·e1 = s1 − s0 and g·e2 = s2 − s0.
		g11, g12, g22 := dot3(e1, e1), dot3(e1, e2), dot3(e2, e2)
		det := g11*g22 - g12*g12
		if math.Abs(det) < 1e-30 {
			continue
		}
		for ai := 0; ai < na; ai++ {
			s0 := s.attrs[int(tr[0])*na+ai]
			s1 := s.attrs[int(tr[1])*na+ai]
			s2 := s.attrs[int(tr[2])*na+ai]
			r1, r2 := s1-s0, s2-s0
			alpha := (r1*g22 - r2*g12) / det
			beta := (r2*g11 - r1*g12) / det
			g := add3(scale3(e1, alpha), scale3(e2, beta))
			u := [4]float64{g[0], g[1], g[2], s0 - dot3(g, p0)}
			for k := 0; k < 3; k++ {
				s.aq[int(tr[k])*na+ai].addFit(u, area*s.attrW2[ai])
			}
		}
	}

	// Border and seam edges: a plane through the edge, at right angles to
	// its face, so the outline is kept.
	type edgeUse struct {
		t    int32
		a, b int32 // the local records at its ends, in the triangle's order
	}
	edges := map[[2]int32][]edgeUse{}
	// The edges in the order the triangles first name them. The planes are
	// summed into each end's quadric in this order, and a float sum depends
	// on its order: walking the map instead (random order, run to run) made
	// the same mesh simplify to a last-bit different error, and now and then
	// a different collapse, on every build.
	var keys [][2]int32
	for t, tr := range s.tris {
		for k := 0; k < 3; k++ {
			a, b := tr[k], tr[(k+1)%3]
			pa, pb := s.vpos[a], s.vpos[b]
			key := [2]int32{min(pa, pb), max(pa, pb)}
			if _, ok := edges[key]; !ok {
				keys = append(keys, key)
			}
			edges[key] = append(edges[key], edgeUse{int32(t), a, b})
		}
	}
	for _, key := range keys {
		uses := edges[key]
		seam := false
		if len(uses) == 2 {
			// The same two records at each end, or not.
			u, w := uses[0], uses[1]
			seam = !(u.a == w.b && u.b == w.a)
		}
		if len(uses) != 1 && !seam {
			continue
		}
		pa, pb := s.pos[key[0]], s.pos[key[1]]
		e := sub3(pb, pa)
		el := len3(e)
		if el < 1e-30 {
			continue
		}
		for _, u := range uses {
			tr := s.tris[u.t]
			fn := cross3(sub3(s.pos[s.vpos[tr[1]]], s.pos[s.vpos[tr[0]]]), sub3(s.pos[s.vpos[tr[2]]], s.pos[s.vpos[tr[0]]]))
			pn := cross3(e, fn)
			pl := len3(pn)
			if pl < 1e-30 {
				continue
			}
			pn = scale3(pn, 1/pl)
			d := -dot3(pn, pa)
			var bq quadric
			bq.addPlane(pn, d, el*el, 0)
			s.pq[key[0]].add(&bq)
			s.pq[key[1]].add(&bq)
		}
	}
	return s
}

// collapse is a candidate: position p onto q, each of p's records going to
// the record of q at the same index of to.
type collapse struct {
	cost  float64
	p, q  int32
	stamp uint32
}

type collapseHeap []collapse

func (h collapseHeap) Len() int { return len(h) }
func (h collapseHeap) Less(i, j int) bool {
	if h[i].cost != h[j].cost {
		return h[i].cost < h[j].cost
	}
	return h[i].p < h[j].p
}
func (h collapseHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *collapseHeap) Push(x any)   { *h = append(*h, x.(collapse)) }
func (h *collapseHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// neighbourhood is what a position's live triangles say about it.
type neighbourhood struct {
	tris []int32
	// records is the distinct vertex records at the position.
	records []int32
	// edges: per neighbour position, the triangles along the edge to it.
	nbrs  []int32
	edgeT [][]int32
}

func (s *simplifier) around(p int32, n *neighbourhood) {
	n.tris = n.tris[:0]
	n.records = n.records[:0]
	n.nbrs = n.nbrs[:0]
	for i := range n.edgeT {
		n.edgeT[i] = n.edgeT[i][:0]
	}
	list := s.posTris[p][:0]
	for _, t := range s.posTris[p] {
		if !s.alive[t] {
			continue
		}
		list = append(list, t)
	}
	s.posTris[p] = list
	for _, t := range list {
		n.tris = append(n.tris, t)
		tr := s.tris[t]
		for k := 0; k < 3; k++ {
			v := tr[k]
			if s.vpos[v] == p {
				found := false
				for _, r := range n.records {
					if r == v {
						found = true
					}
				}
				if !found {
					n.records = append(n.records, v)
				}
				continue
			}
			x := s.vpos[v]
			ei := -1
			for i, nb := range n.nbrs {
				if nb == x {
					ei = i
				}
			}
			if ei < 0 {
				ei = len(n.nbrs)
				n.nbrs = append(n.nbrs, x)
				if len(n.edgeT) < len(n.nbrs) {
					n.edgeT = append(n.edgeT, nil)
				}
			}
			n.edgeT[ei] = append(n.edgeT[ei], t)
		}
	}
}

// recordAt is the record triangle t uses at position p, or -1.
func (s *simplifier) recordAt(t int32, p int32) int32 {
	for _, v := range s.tris[t] {
		if s.vpos[v] == p {
			return v
		}
	}
	return -1
}

// plan works out p → q: whether it is allowed, its cost, and where each of
// p's records goes (to[i] for nb.records[i]).
func (s *simplifier) plan(p, q int32, nb *neighbourhood, nq *neighbourhood, to []int32) (float64, bool) {
	if s.locked[p] {
		return 0, false
	}
	ei := -1
	for i, x := range nb.nbrs {
		if x == q {
			ei = i
		}
	}
	if ei < 0 {
		return 0, false
	}
	// p's kind.
	border, seamEdges := 0, 0
	for i := range nb.nbrs {
		ts := nb.edgeT[i]
		switch len(ts) {
		case 1:
			border++
		case 2:
			if s.recordAt(ts[0], p) != s.recordAt(ts[1], p) {
				seamEdges++
			}
		default:
			return 0, false // non-manifold
		}
	}
	along := nb.edgeT[ei]
	switch {
	case len(nb.records) == 1 && border == 0:
		// Interior: any edge; its one record goes to the record of q the
		// edge's triangles use, which must agree.
		if len(along) != 2 {
			return 0, false
		}
		qa, qb := s.recordAt(along[0], q), s.recordAt(along[1], q)
		if qa != qb {
			return 0, false
		}
		to[0] = qa
	case len(nb.records) == 1 && border == 2:
		// Border: along the border only.
		if len(along) != 1 {
			return 0, false
		}
		to[0] = s.recordAt(along[0], q)
	case len(nb.records) == 2 && border == 0 && seamEdges == 2:
		// Seam: along the seam only, each side's record to that side's.
		if len(along) != 2 {
			return 0, false
		}
		pa, pb := s.recordAt(along[0], p), s.recordAt(along[1], p)
		qa, qb := s.recordAt(along[0], q), s.recordAt(along[1], q)
		if pa == pb || qa == qb {
			return 0, false
		}
		for i, r := range nb.records {
			switch r {
			case pa:
				to[i] = qa
			case pb:
				to[i] = qb
			default:
				return 0, false
			}
		}
	default:
		return 0, false
	}

	// The link condition: p and q share no neighbour but the corners
	// opposite their edge.
	s.around(q, nq)
	for _, x := range nb.nbrs {
		if x == q {
			continue
		}
		shared := false
		for _, y := range nq.nbrs {
			if y == x {
				shared = true
				break
			}
		}
		if !shared {
			continue
		}
		opposite := false
		for _, t := range along {
			for _, v := range s.tris[t] {
				if s.vpos[v] == x {
					opposite = true
				}
			}
		}
		if !opposite {
			return 0, false
		}
	}
	// A triangle may not turn over or flatten: compare each surviving
	// triangle's normal before and after.
	qp := s.pos[q]
	for _, t := range nb.tris {
		tr := s.tris[t]
		hasQ := false
		for _, v := range tr {
			if s.vpos[v] == q {
				hasQ = true
			}
		}
		if hasQ {
			continue
		}
		var before, after [3][3]float64
		for k, v := range tr {
			before[k] = s.pos[s.vpos[v]]
			after[k] = before[k]
			if s.vpos[v] == p {
				after[k] = qp
			}
		}
		n0 := cross3(sub3(before[1], before[0]), sub3(before[2], before[0]))
		n1 := cross3(sub3(after[1], after[0]), sub3(after[2], after[0]))
		l0, l1 := len3(n0), len3(n1)
		if l1 < 1e-12*max(l0, 1e-30) || l1 == 0 {
			return 0, false
		}
		if dot3(n0, n1) < 0.25*l0*l1 {
			return 0, false
		}
	}

	// The cost.
	pq := s.pq[p]
	pq.add(&s.pq[q])
	cost := pq.eval(qp)
	na := len(s.attrIdx)
	for i, r := range nb.records {
		tq := to[i]
		for ai := 0; ai < na; ai++ {
			aq := s.aq[int(r)*na+ai]
			aq.add(&s.aq[int(tq)*na+ai])
			cost += aq.eval(qp, s.attrs[int(tq)*na+ai])
		}
	}
	if pq.w > 0 {
		cost /= pq.w
	}
	// A sliver's attribute fit (a near-zero determinant) can overflow the
	// quadric into Inf − Inf: such a collapse is not costed but refused, or
	// one NaN would become the whole DAG's error and the LOD test would never
	// select the mesh (a generated rock went invisible; docs/canyon/PHASE_2.md).
	// Every collapse that costed a number before costs the same now.
	if math.IsNaN(cost) || math.IsInf(cost, 0) {
		return 0, false
	}
	return max(cost, 0), true
}

// best is p's cheapest allowed collapse.
func (s *simplifier) best(p int32, nb, nq *neighbourhood, to []int32) (int32, float64, bool) {
	if s.dead[p] || s.locked[p] {
		return -1, 0, false
	}
	s.around(p, nb)
	if len(nb.records) > len(to) {
		return -1, 0, false
	}
	bestQ, bestCost := int32(-1), math.Inf(1)
	nbrs := append([]int32(nil), nb.nbrs...)
	for _, q := range nbrs {
		c, ok := s.plan(p, q, nb, nq, to)
		if ok && (c < bestCost || (c == bestCost && q < bestQ)) {
			bestQ, bestCost = q, c
		}
	}
	return bestQ, bestCost, bestQ >= 0
}

func (s *simplifier) run() {
	if s.live <= s.opt.TargetTriangles || s.live == 0 {
		return
	}
	var nb, nq neighbourhood
	to := make([]int32, 2)
	maxCost := math.Inf(1)
	if s.opt.MaxError > 0 {
		maxCost = float64(s.opt.MaxError) * float64(s.opt.MaxError)
	}
	push := func(p int32) {
		s.stamp[p]++
		if _, c, ok := s.best(p, &nb, &nq, to); ok {
			heap.Push(&s.h, collapse{cost: c, p: p, stamp: s.stamp[p]})
		}
	}
	for p := range s.pos {
		push(int32(p))
	}
	for s.live > s.opt.TargetTriangles && s.h.Len() > 0 {
		c := heap.Pop(&s.h).(collapse)
		if s.dead[c.p] || c.stamp != s.stamp[c.p] {
			continue
		}
		// Re-plan: the neighbourhood may have changed in ways that did not
		// re-stamp p (a neighbour's neighbour collapsed).
		q, cost, ok := s.best(c.p, &nb, &nq, to)
		if !ok {
			continue
		}
		if cost > c.cost*(1+1e-9)+1e-30 {
			s.stamp[c.p]++
			heap.Push(&s.h, collapse{cost: cost, p: c.p, stamp: s.stamp[c.p]})
			continue
		}
		if cost > maxCost {
			break
		}
		// best left nb as p's neighbourhood and to as the mapping for its
		// last plan, which may not have been q's: plan q again.
		s.around(c.p, &nb)
		if _, ok := s.plan(c.p, q, &nb, &nq, to); !ok {
			continue
		}
		s.apply(c.p, q, &nb, to)
		s.maxCost = max(s.maxCost, cost)
		// Everything around q may cost differently now.
		s.around(q, &nq)
		around := append([]int32{q}, nq.nbrs...)
		for _, x := range around {
			push(x)
		}
	}
}

// apply makes the collapse p → q that plan approved.
func (s *simplifier) apply(p, q int32, nb *neighbourhood, to []int32) {
	na := len(s.attrIdx)
	for i, r := range nb.records {
		tq := to[i]
		for ai := 0; ai < na; ai++ {
			s.aq[int(tq)*na+ai].add(&s.aq[int(r)*na+ai])
		}
	}
	s.pq[q].add(&s.pq[p])
	for _, t := range nb.tris {
		tr := &s.tris[t]
		hasQ := false
		for _, v := range tr {
			if s.vpos[v] == q {
				hasQ = true
			}
		}
		if hasQ {
			s.alive[t] = false
			s.live--
			continue
		}
		for k, v := range tr {
			if s.vpos[v] != p {
				continue
			}
			for i, r := range nb.records {
				if r == v {
					tr[k] = to[i]
				}
			}
		}
		s.posTris[q] = append(s.posTris[q], t)
	}
	s.dead[p] = true
	s.posTris[p] = nil
}

func (s *simplifier) output() []uint32 {
	out := make([]uint32, 0, s.live*3)
	for t, tr := range s.tris {
		if !s.alive[t] {
			continue
		}
		out = append(out, s.global[tr[0]], s.global[tr[1]], s.global[tr[2]])
	}
	return out
}

func sub3(a, b [3]float64) [3]float64 { return [3]float64{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func add3(a, b [3]float64) [3]float64 { return [3]float64{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func scale3(a [3]float64, s float64) [3]float64 {
	return [3]float64{a[0] * s, a[1] * s, a[2] * s}
}
func dot3(a, b [3]float64) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func cross3(a, b [3]float64) [3]float64 {
	return [3]float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}
func len3(a [3]float64) float64 { return math.Sqrt(dot3(a, a)) }
