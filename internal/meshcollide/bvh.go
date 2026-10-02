package meshcollide

// bvh.go — the tree over the collision cut, and the queries the runtime asks
// of it.
//
// A binned surface-area-heuristic build, the textbook one: at each node the
// triangles' centroids are binned along the axis they spread most on, and
// the split between bins that minimises (area × triangles) on each side is
// taken, unless keeping the node a leaf is cheaper. Leaves hold a few
// triangles, which is about where testing them beats descending further.
//
// Nodes are laid out depth first, so an inner node's left child is the node
// after it and only the right child needs an index; the triangles are
// reordered to leaf order, so a leaf is one run of them.
//
// # In mesh-local space
//
// The tree is built and queried in the mesh's own coordinates. A placement
// turns, scales and moves the mesh; carrying the ray into the mesh (by the
// placement's inverse) is one matrix multiply, where carrying the tree out
// would be a rebuild per placement. A ray carried in by an affine inverse
// keeps its parameter: t along the local ray is t along the world one, which
// is why the queries take a direction that need not be unit length.

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	bvhBins     = 12
	bvhLeafMax  = 4
	bvhLeafHard = 16 // a node is split by the median past this, SAH or not
	bvhSAHDepth = 40 // past this depth, median splits only
	bvhStack    = 128
)

type bvhItem struct {
	lo, hi, c mgl32.Vec3
	tri       int32
}

// buildBVH builds the tree over tris (3 indices a triangle into pos), and
// returns it with the triangles' leaf order.
func buildBVH(pos []mgl32.Vec3, tris []uint32) ([]Node, []int32) {
	n := len(tris) / 3
	if n == 0 {
		return nil, nil
	}
	items := make([]bvhItem, n)
	for t := 0; t < n; t++ {
		a, b, c := pos[tris[3*t]], pos[tris[3*t+1]], pos[tris[3*t+2]]
		lo, hi := vmin(vmin(a, b), c), vmax(vmax(a, b), c)
		items[t] = bvhItem{lo: lo, hi: hi, c: lo.Add(hi).Mul(0.5), tri: int32(t)}
	}
	b := bvhBuilder{nodes: make([]Node, 0, 2*n/bvhLeafMax+1)}
	b.build(items, 0, 0)
	order := make([]int32, n)
	for i := range items {
		order[i] = items[i].tri
	}
	return b.nodes, order
}

type bvhBuilder struct {
	nodes []Node
}

// build makes the node for items (which start at triangle first of the final
// order) and its subtree, and returns its index.
func (b *bvhBuilder) build(items []bvhItem, first, depth int) int {
	lo, hi := items[0].lo, items[0].hi
	clo, chi := items[0].c, items[0].c
	for _, it := range items[1:] {
		lo, hi = vmin(lo, it.lo), vmax(hi, it.hi)
		clo, chi = vmin(clo, it.c), vmax(chi, it.c)
	}
	idx := len(b.nodes)
	b.nodes = append(b.nodes, Node{Lo: padLo(lo, hi), Hi: padHi(lo, hi)})
	if len(items) <= bvhLeafMax {
		b.nodes[idx].Right, b.nodes[idx].Count = int32(first), int32(len(items))
		return idx
	}

	axis := 0
	ext := chi.Sub(clo)
	if ext[1] > ext[axis] {
		axis = 1
	}
	if ext[2] > ext[axis] {
		axis = 2
	}
	split := -1
	// Deep enough that a query's fixed stack could overflow: the median from
	// here down, which halves every time.
	if ext[axis] > 0 && depth < bvhSAHDepth {
		split = sahSplit(items, axis, clo[axis], ext[axis], area(lo, hi))
	}
	if split < 0 {
		if len(items) <= bvhLeafHard && ext[axis] > 0 && depth < bvhSAHDepth {
			b.nodes[idx].Right, b.nodes[idx].Count = int32(first), int32(len(items))
			return idx
		}
		// The median, so a node of many coincident centroids still divides.
		sort.SliceStable(items, func(i, j int) bool { return items[i].c[axis] < items[j].c[axis] })
		split = len(items) / 2
	}
	b.build(items[:split], first, depth+1)
	right := b.build(items[split:], first+split, depth+1)
	b.nodes[idx].Right = int32(right)
	return idx
}

// sahSplit partitions items along axis at the best bin boundary and returns
// how many went left, or -1 when a leaf is cheaper.
func sahSplit(items []bvhItem, axis int, c0, ext, parentArea float32) int {
	type bin struct {
		lo, hi mgl32.Vec3
		n      int
	}
	var bins [bvhBins]bin
	for i := range bins {
		bins[i].lo = mgl32.Vec3{math.MaxFloat32, math.MaxFloat32, math.MaxFloat32}
		bins[i].hi = bins[i].lo.Mul(-1)
	}
	binOf := func(c float32) int {
		k := int(float32(bvhBins) * (c - c0) / ext)
		return min(max(k, 0), bvhBins-1)
	}
	for _, it := range items {
		k := binOf(it.c[axis])
		bins[k].n++
		bins[k].lo, bins[k].hi = vmin(bins[k].lo, it.lo), vmax(bins[k].hi, it.hi)
	}
	// Sweep from the left and from the right.
	var leftA, rightA [bvhBins]float32
	var leftN, rightN [bvhBins]int
	lo, hi := bins[0].lo, bins[0].hi
	n := 0
	for i := 0; i < bvhBins; i++ {
		if bins[i].n > 0 {
			lo, hi = vmin(lo, bins[i].lo), vmax(hi, bins[i].hi)
		}
		n += bins[i].n
		leftA[i], leftN[i] = area(lo, hi), n
	}
	lo, hi = bins[bvhBins-1].lo, bins[bvhBins-1].hi
	n = 0
	for i := bvhBins - 1; i >= 0; i-- {
		if bins[i].n > 0 {
			lo, hi = vmin(lo, bins[i].lo), vmax(hi, bins[i].hi)
		}
		n += bins[i].n
		rightA[i], rightN[i] = area(lo, hi), n
	}
	best, bestCost := -1, float32(len(items)) // a leaf's cost
	for i := 0; i < bvhBins-1; i++ {
		if leftN[i] == 0 || rightN[i+1] == 0 {
			continue
		}
		cost := 0.5 + (leftA[i]*float32(leftN[i])+rightA[i+1]*float32(rightN[i+1]))/max(parentArea, 1e-12)
		if cost < bestCost {
			best, bestCost = i, cost
		}
	}
	if best < 0 {
		return -1
	}
	// Partition in place, stable so a build is deterministic.
	left := make([]bvhItem, 0, leftN[best])
	right := make([]bvhItem, 0, len(items)-leftN[best])
	for _, it := range items {
		if binOf(it.c[axis]) <= best {
			left = append(left, it)
		} else {
			right = append(right, it)
		}
	}
	copy(items, left)
	copy(items[len(left):], right)
	return len(left)
}

func area(lo, hi mgl32.Vec3) float32 {
	d := hi.Sub(lo)
	if d[0] < 0 {
		return 0
	}
	return 2 * (d[0]*d[1] + d[1]*d[2] + d[2]*d[0])
}

// padLo and padHi grow a node's box by a hair of its size, so a ray grazing
// a face on the box's surface is never culled by rounding.
func padLo(lo, hi mgl32.Vec3) [3]float32 {
	m := max(hi.Sub(lo).Len()*1e-5, 1e-5)
	return [3]float32{lo[0] - m, lo[1] - m, lo[2] - m}
}

func padHi(lo, hi mgl32.Vec3) [3]float32 {
	m := max(hi.Sub(lo).Len()*1e-5, 1e-5)
	return [3]float32{hi[0] + m, hi[1] + m, hi[2] + m}
}

// ---------- the resident form ----------

// Resident is a mesh's collision decoded for queries: the cut's positions as
// floats and each triangle's normal. internal/collision keeps one for a mesh
// while bodies are near it, and drops it when none have been for a while.
type Resident struct {
	Data    *Data
	Pos     []mgl32.Vec3
	Normals []mgl32.Vec3 // per triangle, unit; zero for a sliver
}

// NewResident decodes d.
func NewResident(d *Data) *Resident {
	r := &Resident{Data: d, Pos: make([]mgl32.Vec3, d.VertexCount()), Normals: make([]mgl32.Vec3, d.TriangleCount())}
	for v := range r.Pos {
		r.Pos[v] = d.Dequantize(uint32(v))
	}
	for t := range r.Normals {
		a, b, c := r.Tri(t)
		n := b.Sub(a).Cross(c.Sub(a))
		if l := n.Len(); l > 1e-20 {
			n = n.Mul(1 / l)
		} else {
			n = mgl32.Vec3{}
		}
		r.Normals[t] = n
	}
	return r
}

// Bytes is what the decoded form holds beyond the cooked data.
func (r *Resident) Bytes() int { return len(r.Pos)*12 + len(r.Normals)*12 }

// Tri is triangle t's corners.
func (r *Resident) Tri(t int) (a, b, c mgl32.Vec3) {
	ix := r.Data.Tris[3*t : 3*t+3]
	return r.Pos[ix[0]], r.Pos[ix[1]], r.Pos[ix[2]]
}

// Raycast is the nearest triangle the ray o + t·dir meets for t in
// [0, tMax], double-sided. dir need not be unit length: t is in its units.
func (r *Resident) Raycast(o, dir mgl32.Vec3, tMax float32) (t float32, tri int, ok bool) {
	return r.cast(o, dir, tMax, false)
}

// AnyHit reports whether the ray meets anything within tMax.
func (r *Resident) AnyHit(o, dir mgl32.Vec3, tMax float32) bool {
	_, _, ok := r.cast(o, dir, tMax, true)
	return ok
}

func (r *Resident) cast(o, dir mgl32.Vec3, tMax float32, any bool) (float32, int, bool) {
	nodes := r.Data.Nodes
	if len(nodes) == 0 {
		return 0, -1, false
	}
	var inv mgl32.Vec3
	for k := 0; k < 3; k++ {
		if dir[k] != 0 {
			inv[k] = 1 / dir[k]
		} else {
			inv[k] = float32(math.Inf(1))
		}
	}
	best, bestTri := tMax, -1
	var stack [bvhStack]int32
	sp := 0
	if _, hit := slabEnter(&nodes[0], o, inv, best); !hit {
		return 0, -1, false
	}
	stack[sp] = 0
	sp++
	for sp > 0 {
		sp--
		n := &nodes[stack[sp]]
		if n.IsLeaf() {
			for t := n.Right; t < n.Right+n.Count; t++ {
				a, b, c := r.Tri(int(t))
				if th, ok := rayTri(o, dir, a, b, c); ok && th <= best {
					best, bestTri = th, int(t)
					if any {
						return best, bestTri, true
					}
				}
			}
			continue
		}
		li, ri := stack[sp]+1, n.Right
		tl, hl := slabEnter(&nodes[li], o, inv, best)
		tr, hr := slabEnter(&nodes[ri], o, inv, best)
		// Nearer child on top, so it is visited first and shrinks best.
		if hl && hr {
			if tl < tr {
				li, ri = ri, li
			}
			if sp+2 <= len(stack) {
				stack[sp], stack[sp+1] = li, ri
				sp += 2
			}
		} else if hl {
			stack[sp] = li
			sp++
		} else if hr {
			stack[sp] = ri
			sp++
		}
	}
	return best, bestTri, bestTri >= 0
}

// slabEnter is where the ray enters a node's box, if it does within tMax.
func slabEnter(n *Node, o, inv mgl32.Vec3, tMax float32) (float32, bool) {
	t0, t1 := float32(0), tMax
	for k := 0; k < 3; k++ {
		a := (n.Lo[k] - o[k]) * inv[k]
		b := (n.Hi[k] - o[k]) * inv[k]
		if math.IsNaN(float64(a)) || math.IsNaN(float64(b)) {
			// A zero direction exactly on a slab's face: inside it.
			if o[k] < n.Lo[k] || o[k] > n.Hi[k] {
				return 0, false
			}
			continue
		}
		if a > b {
			a, b = b, a
		}
		t0, t1 = max(t0, a), min(t1, b)
		if t0 > t1 {
			return 0, false
		}
	}
	return t0, true
}

// rayTri is Möller–Trumbore, double-sided, for any direction length.
func rayTri(o, d, a, b, c mgl32.Vec3) (float32, bool) {
	e1, e2 := b.Sub(a), c.Sub(a)
	p := d.Cross(e2)
	det := e1.Dot(p)
	if det > -1e-12 && det < 1e-12 {
		return 0, false
	}
	inv := 1 / det
	s := o.Sub(a)
	u := s.Dot(p) * inv
	if u < 0 || u > 1 {
		return 0, false
	}
	q := s.Cross(e1)
	v := d.Dot(q) * inv
	if v < 0 || u+v > 1 {
		return 0, false
	}
	t := e2.Dot(q) * inv
	if t < 0 {
		return 0, false
	}
	return t, true
}

// NearBox reports whether any leaf of the tree touches the box [lo, hi]: the
// cheap "is there any surface near this body" test a probe pass starts with.
func (r *Resident) NearBox(lo, hi mgl32.Vec3) bool {
	found := false
	r.walkBox(lo, hi, func(n *Node) bool {
		found = true
		return false
	})
	return found
}

// TrianglesInBox calls fn with every triangle whose leaf touches the box,
// until fn returns false.
func (r *Resident) TrianglesInBox(lo, hi mgl32.Vec3, fn func(tri int) bool) {
	r.walkBox(lo, hi, func(n *Node) bool {
		for t := n.Right; t < n.Right+n.Count; t++ {
			if !fn(int(t)) {
				return false
			}
		}
		return true
	})
}

func (r *Resident) walkBox(lo, hi mgl32.Vec3, leaf func(*Node) bool) {
	nodes := r.Data.Nodes
	if len(nodes) == 0 {
		return
	}
	var stack [bvhStack]int32
	stack[0] = 0
	sp := 1
	for sp > 0 {
		sp--
		i := stack[sp]
		n := &nodes[i]
		if n.Lo[0] > hi[0] || n.Hi[0] < lo[0] || n.Lo[1] > hi[1] || n.Hi[1] < lo[1] || n.Lo[2] > hi[2] || n.Hi[2] < lo[2] {
			continue
		}
		if n.IsLeaf() {
			if !leaf(n) {
				return
			}
			continue
		}
		if sp+2 <= len(stack) {
			stack[sp], stack[sp+1] = n.Right, i+1
			sp += 2
		}
	}
}
