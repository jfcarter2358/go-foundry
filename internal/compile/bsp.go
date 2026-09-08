package compile

// bsp.go — partitioning the compiled faces into convex cells.
//
// After CSG the level is a soup of world-space polygons with no structure: to
// draw it you would submit all of it, and to ask "what can be seen from here"
// you would have nowhere to start. The BSP gives that structure. Every face
// plane is a candidate knife; recursively cutting the world with the chosen
// ones leaves a tree whose leaves are CONVEX CELLS, each one either wholly
// inside a solid or wholly empty.
//
// Two things fall out of that and both are what the later stages run on:
//
//   - A point can be located in the tree in O(depth) — which leaf am I in — by
//     comparing it against one plane per level. That is how the runtime finds
//     the camera's cell every frame, and how collision could ask the same
//     question.
//   - Two empty cells that share part of a plane are connected by a PORTAL, and
//     the whole of visibility is a question about paths through portals. pvs.go
//     is that; it needs the cells to be convex, which is exactly what a BSP
//     leaf is.
//
// # Why the face normals decide solidity
//
// CSG leaves every surviving face pointing OUT of the solid it belongs to, so
// "in front of a face plane" is empty space and "behind it" is material. The
// build carries that down: descending into a splitter's back child means
// stepping inside a solid, descending into its front child means stepping into
// open air, and a region that runs out of faces to split with is a leaf whose
// solidity is whichever of those it last did. No separate inside test is
// needed, and none would be as reliable — a level is only sealed where the
// author sealed it.
//
// # Where a coplanar face lives
//
// A face lying ON the splitting plane belongs to the NODE, not to either child.
// Handing it to a child would either lose it (it is not strictly on either
// side) or duplicate it (if handed to both), and it would be re-split forever
// against its own plane. Node faces are attached to the empty leaves they bound
// afterwards, in attachFacesToLeaves, which is what gives each leaf the list of
// surfaces to draw when the camera is in it.

import (
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// ChildRef points at either a node or a leaf. Non-negative values index
// BSPTree.Nodes; negative values encode a leaf as ^i, so leaf 0 is -1 and there
// is no ambiguous zero. One int rather than a pair of "index plus kind" fields
// because the tree is walked in the innermost loop of every runtime query.
type ChildRef int32

// LeafRef encodes a leaf index as a child reference.
func LeafRef(i int) ChildRef { return ChildRef(^i) }

// NodeRef encodes a node index as a child reference.
func NodeRef(i int) ChildRef { return ChildRef(i) }

// IsLeaf reports whether this reference points at a leaf rather than a node.
func (c ChildRef) IsLeaf() bool { return c < 0 }

// Leaf is the leaf index. Only meaningful when IsLeaf.
func (c ChildRef) Leaf() int { return int(^c) }

// Node is the node index. Only meaningful when !IsLeaf.
func (c ChildRef) Node() int { return int(c) }

// BSPNode is one split of space.
type BSPNode struct {
	Plane       Plane
	Front, Back ChildRef
	// Faces are the compiled faces lying ON this node's plane, as indices into
	// BSPTree.Faces. See the file comment on why they are here and not in a
	// child.
	Faces []int
	// Lo, Hi bound everything below this node. Handy for a runtime frustum
	// reject that skips a whole subtree.
	Lo, Hi mgl32.Vec3
}

// BSPLeaf is a convex cell.
type BSPLeaf struct {
	// Solid is true for a cell inside brush material. Nothing is drawn there
	// and nothing can see out of it; the PVS skips solid leaves entirely.
	Solid bool
	// Planes bound the cell: a point is inside when it is in FRONT of every one
	// of them. This is the cell as a convex polytope, and it is what portal
	// generation clips against.
	Planes []Plane
	// Faces are the compiled faces that bound this cell and face into it —
	// what to draw when the camera is here. Indices into BSPTree.Faces, sorted.
	Faces  []int
	Lo, Hi mgl32.Vec3
}

// BSPTree is the built partition.
type BSPTree struct {
	Nodes  []BSPNode
	Leaves []BSPLeaf
	// Faces are the compiled faces AFTER the build, which splits any face a
	// chosen plane crosses. A fragment keeps every attribute of the face it
	// came from, so this list is interchangeable with CSG's output as far as
	// the renderer is concerned — there are just more of them.
	Faces []Face
	Root  ChildRef
	// Lo, Hi are the world box the tree partitions, the level's own bounds
	// grown by a margin so that the outermost cells are closed rather than
	// running to infinity.
	Lo, Hi mgl32.Vec3
}

// Build limits. Depth is a safety net rather than a tuning knob: the recursion
// terminates on its own because every split removes at least the faces coplanar
// with the splitter, and a level that reaches this depth has something wrong
// with it — a near-degenerate face producing a plane nothing quite lies on.
const (
	maxBSPDepth = 128
	// maxSplitterCandidates caps how many distinct planes the heuristic scores
	// at one node. Scoring is O(candidates x faces), so an unbounded search is
	// quadratic in a big level's face count for a choice that is barely better
	// than the one from the first few dozen. Faces arrive in a deterministic
	// order (SortFaces), so which candidates the cap admits is deterministic
	// too.
	maxSplitterCandidates = 64
	// worldMargin is how far past the level's own extent the root box sits.
	worldMargin = float32(16)
)

// BuildBSP partitions the compiled faces.
//
// The input is not modified: the tree takes its own copy, because the build
// splits faces and the caller's slice would otherwise stop describing what was
// compiled.
func BuildBSP(faces []Face) *BSPTree {
	t := &BSPTree{Faces: append([]Face(nil), faces...)}
	SortFaces(t.Faces)

	t.Lo, t.Hi = faceSetBounds(t.Faces)
	for i := 0; i < 3; i++ {
		t.Lo[i] -= worldMargin
		t.Hi[i] += worldMargin
	}

	idx := make([]int, len(t.Faces))
	for i := range idx {
		idx[i] = i
	}
	t.Root = t.build(idx, boxPlanes(t.Lo, t.Hi), 0, false)
	t.attachFacesToLeaves()
	t.computeNodeBounds(t.Root)
	return t
}

// faceSetBounds is the box around every compiled face. An empty level still
// gets a unit box rather than a degenerate one, so that boxPlanes below
// produces six usable planes and the tree has a root at all.
func faceSetBounds(faces []Face) (lo, hi mgl32.Vec3) {
	if len(faces) == 0 {
		return mgl32.Vec3{-1, -1, -1}, mgl32.Vec3{1, 1, 1}
	}
	lo, hi = boundsOf(faces[0].Points)
	for _, f := range faces[1:] {
		l, h := boundsOf(f.Points)
		for i := 0; i < 3; i++ {
			if l[i] < lo[i] {
				lo[i] = l[i]
			}
			if h[i] > hi[i] {
				hi[i] = h[i]
			}
		}
	}
	return lo, hi
}

// boxPlanes is a box as six planes facing INWARD, matching the convention a
// leaf's Planes use: inside is in front of all of them.
func boxPlanes(lo, hi mgl32.Vec3) []Plane {
	return []Plane{
		{N: mgl32.Vec3{1, 0, 0}, D: lo.X()},
		{N: mgl32.Vec3{-1, 0, 0}, D: -hi.X()},
		{N: mgl32.Vec3{0, 1, 0}, D: lo.Y()},
		{N: mgl32.Vec3{0, -1, 0}, D: -hi.Y()},
		{N: mgl32.Vec3{0, 0, 1}, D: lo.Z()},
		{N: mgl32.Vec3{0, 0, -1}, D: -hi.Z()},
	}
}

// build recurses over a set of faces and the convex region containing them.
//
// solid is what the region IS if it turns out to have no faces left: the side
// of the last splitter we descended into. See the file comment.
func (t *BSPTree) build(faceIdx []int, region []Plane, depth int, solid bool) ChildRef {
	if len(faceIdx) == 0 || depth >= maxBSPDepth {
		return t.addLeaf(solid, region)
	}
	pl, ok := t.chooseSplitter(faceIdx)
	if !ok {
		// Every remaining face has a degenerate plane. Nothing can cut with
		// them, so the region is as convex as it is going to get.
		return t.addLeaf(solid, region)
	}

	var onNode, front, back []int
	for _, fi := range faceIdx {
		switch pl.ClassifyPolygon(t.Faces[fi].Points) {
		case SideOn:
			onNode = append(onNode, fi)
		case SideFront:
			front = append(front, fi)
		case SideBack:
			back = append(back, fi)
		default:
			fIdx, bIdx := t.splitFace(fi, pl)
			if fIdx >= 0 {
				front = append(front, fIdx)
			}
			if bIdx >= 0 {
				back = append(back, bIdx)
			}
		}
	}

	ni := len(t.Nodes)
	t.Nodes = append(t.Nodes, BSPNode{Plane: pl, Faces: onNode})
	// The children are built after the node is appended so that a subtree can
	// grow t.Nodes underneath it; the index is stable, the pointer would not be.
	f := t.build(front, appendPlane(region, pl), depth+1, false)
	b := t.build(back, appendPlane(region, pl.Flip()), depth+1, true)
	t.Nodes[ni].Front, t.Nodes[ni].Back = f, b
	return NodeRef(ni)
}

// appendPlane copies rather than appending in place. A region slice is handed
// to two children, and an in-place append would let the front child's plane
// overwrite the back child's in the shared backing array.
func appendPlane(region []Plane, pl Plane) []Plane {
	out := make([]Plane, len(region), len(region)+1)
	copy(out, region)
	return append(out, pl)
}

func (t *BSPTree) addLeaf(solid bool, region []Plane) ChildRef {
	l := BSPLeaf{Solid: solid, Planes: region}
	l.Lo, l.Hi = regionBounds(region, t.Lo, t.Hi)
	t.Leaves = append(t.Leaves, l)
	return LeafRef(len(t.Leaves) - 1)
}

// splitFace cuts face fi with a plane, appends the halves and returns their
// indices; either is -1 when that half came back degenerate.
//
// The fragments inherit everything about the original but its points — the
// texture, the UVs, the source names — so a fragment is still fully drawable
// and still says which authored face it came from.
func (t *BSPTree) splitFace(fi int, pl Plane) (front, back int) {
	f, b := SplitPolygon(t.Faces[fi].Points, pl)
	front, back = -1, -1
	if len(f) >= 3 && PolygonArea(f) >= minFragmentArea {
		g := t.Faces[fi]
		g.Points = f
		g.Light = nil // no longer parallel to Points; the bake refills it
		t.Faces = append(t.Faces, g)
		front = len(t.Faces) - 1
	}
	if len(b) >= 3 && PolygonArea(b) >= minFragmentArea {
		g := t.Faces[fi]
		g.Points = b
		g.Light = nil
		t.Faces = append(t.Faces, g)
		back = len(t.Faces) - 1
	}
	return front, back
}

// chooseSplitter picks the plane to cut with.
//
// The score is the classic one: how unbalanced the split is, plus a heavy
// penalty per face the plane would cut in two. Splits are penalised much harder
// than imbalance because each one is a permanent new face — it costs a draw for
// the rest of the level's life — whereas imbalance costs only tree depth, which
// is a logarithm.
//
// Axial planes are given a small bonus. In a level blocked out of boxes almost
// every plane is axial, and preferring them keeps the cells boxy, which makes
// the portals between them rectangles rather than slivers.
func (t *BSPTree) chooseSplitter(faceIdx []int) (Plane, bool) {
	var (
		best      Plane
		bestScore = int(^uint(0) >> 1)
		found     bool
		tried     []Plane
	)
	for _, fi := range faceIdx {
		if len(tried) >= maxSplitterCandidates {
			break
		}
		pl := t.Faces[fi].Plane
		if planeSeen(tried, pl) {
			continue
		}
		tried = append(tried, pl)

		front, back, splits := 0, 0, 0
		for _, oi := range faceIdx {
			switch pl.ClassifyPolygon(t.Faces[oi].Points) {
			case SideFront:
				front++
			case SideBack:
				back++
			case SideSpanning:
				splits++
			}
		}
		if front == 0 && back == 0 && splits == 0 {
			// Everything left is coplanar with this plane: it separates
			// nothing, but it does consume those faces onto the node, which is
			// progress. Take it only if nothing better turns up.
			if !found {
				best, bestScore, found = pl, bestScore-1, true
			}
			continue
		}
		score := front - back
		if score < 0 {
			score = -score
		}
		score += splits * splitPenalty
		if isAxial(pl.N) {
			score -= axialBonus
		}
		if score < bestScore {
			best, bestScore, found = pl, score, true
		}
	}
	return best, found
}

const (
	// splitPenalty weights one cut face against one unit of imbalance. See
	// chooseSplitter on why it is this lopsided.
	splitPenalty = 8
	// axialBonus is small enough that it only breaks ties between otherwise
	// comparable planes.
	axialBonus = 2
)

func isAxial(n mgl32.Vec3) bool {
	for i := 0; i < 3; i++ {
		if geomAbs(geomAbs(n[i])-1) < 1e-4 {
			return true
		}
	}
	return false
}

func planeSeen(seen []Plane, pl Plane) bool {
	for _, s := range seen {
		if s.SameAs(pl) {
			return true
		}
	}
	return false
}

// attachFacesToLeaves gives every empty leaf the list of faces that bound it.
//
// A node's coplanar faces are pushed down the subtree on the side they FACE —
// which is the empty side, since CSG leaves normals pointing out of solids —
// and land in the leaves they actually touch. Pushing rather than testing every
// leaf against every face is what keeps this linear-ish: a face visits only the
// cells its own polygon reaches.
func (t *BSPTree) attachFacesToLeaves() {
	for ni := range t.Nodes {
		n := &t.Nodes[ni]
		for _, fi := range n.Faces {
			child := n.Front
			if t.Faces[fi].Plane.N.Dot(n.Plane.N) < 0 {
				child = n.Back
			}
			t.markFace(child, fi, t.Faces[fi].Points)
		}
	}
	for li := range t.Leaves {
		l := &t.Leaves[li]
		sort.Ints(l.Faces)
		l.Faces = dedupInts(l.Faces)
	}
}

// markFace walks a polygon down a subtree, recording the face in every empty
// leaf it reaches.
func (t *BSPTree) markFace(c ChildRef, fi int, pts []mgl32.Vec3) {
	if len(pts) < 3 {
		return
	}
	if c.IsLeaf() {
		l := &t.Leaves[c.Leaf()]
		if !l.Solid {
			l.Faces = append(l.Faces, fi)
		}
		return
	}
	n := &t.Nodes[c.Node()]
	switch n.Plane.ClassifyPolygon(pts) {
	case SideFront:
		t.markFace(n.Front, fi, pts)
	case SideBack:
		t.markFace(n.Back, fi, pts)
	case SideOn:
		// Coplanar with a node deeper in the tree. It bounds cells on both
		// sides of it, and the empty-leaf filter above drops whichever of those
		// turns out to be material.
		t.markFace(n.Front, fi, pts)
		t.markFace(n.Back, fi, pts)
	default:
		f, b := SplitPolygon(pts, n.Plane)
		t.markFace(n.Front, fi, f)
		t.markFace(n.Back, fi, b)
	}
}

func dedupInts(s []int) []int {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

// computeNodeBounds fills each node's box from its children's, bottom-up.
func (t *BSPTree) computeNodeBounds(c ChildRef) (lo, hi mgl32.Vec3) {
	if c.IsLeaf() {
		l := &t.Leaves[c.Leaf()]
		return l.Lo, l.Hi
	}
	n := &t.Nodes[c.Node()]
	flo, fhi := t.computeNodeBounds(n.Front)
	blo, bhi := t.computeNodeBounds(n.Back)
	for i := 0; i < 3; i++ {
		n.Lo[i] = minf(flo[i], blo[i])
		n.Hi[i] = maxf(fhi[i], bhi[i])
	}
	return n.Lo, n.Hi
}

// FindLeaf is which cell a point is in: one plane comparison per level of the
// tree, and no geometry touched at all.
func (t *BSPTree) FindLeaf(p mgl32.Vec3) int {
	c := t.Root
	for !c.IsLeaf() {
		n := &t.Nodes[c.Node()]
		if n.Plane.Distance(p) >= 0 {
			c = n.Front
		} else {
			c = n.Back
		}
	}
	return c.Leaf()
}

// EmptyLeafCount is how many cells are open space — the number of rows the PVS
// will have anything to say about.
func (t *BSPTree) EmptyLeafCount() int {
	n := 0
	for i := range t.Leaves {
		if !t.Leaves[i].Solid {
			n++
		}
	}
	return n
}

func minf(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
