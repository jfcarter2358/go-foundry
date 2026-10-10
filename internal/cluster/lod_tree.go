package cluster

// lod_tree.go — the bounding volume hierarchy over a DAG's cluster groups
// that the GPU's LOD selection walks (render/nanite_cull.go,
// nanite_lod_traverse_comp.glsl). Phase 2 of the Nanite plan
// (docs/nanite/PHASE_2.md), component 5.
//
// The per-cluster test (dag.go) needs nothing but the cluster, so the cut
// could be chosen by testing every cluster of every level. The tree is what
// keeps that from costing the whole DAG every pass: a far-away mesh should
// not read its tens of thousands of finest clusters only to throw each one
// away. A LEAF is a run of clusters made by one group and consumed by one
// (level 0's are made by none), so they share their own error and sphere
// and their parent's exactly, and the LOD test answers alike for every one of
// them — which is what lets the renderer draw a leaf with one command (render's
// LOD tiles). A node bounds what is under it three ways:
//
//   - a CULL sphere around every cluster's own bounds, for the frustum;
//   - the largest PARENT error under it, with a sphere around every parent
//     sphere: if even that, projected from the nearest point of the sphere,
//     is within the threshold, then every cluster under it has a parent fine
//     enough to draw instead, and none of them is in the cut;
//   - the smallest OWN error under it, with a sphere around every own LOD
//     sphere: if even that, projected from the FURTHEST point of the sphere,
//     is beyond the threshold, every cluster under it is too coarse.
//
// Either way the whole subtree is skipped. A leaf that survives is a run of
// clusters the per-cluster test then decides exactly; the tree only ever
// skips what that test would have rejected.
//
// The shape: a root per mesh, under it one node per material slot (a slot
// is its own draw group), under each of those one subtree per DAG level, each
// a 4-ary tree over that level's leaves split at the median of the longest
// axis. Nodes are stored breadth first, so a node's children are one run of
// the array; leaves are numbered depth first, so the leaves under any node
// are one run of numbers too (LeafFirst, LeafCount) — what lets the GPU mark a
// whole subtree visible at once when its queue overflows.

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// MaxLeafClusters is the most clusters a leaf takes.
const MaxLeafClusters = 32

// lodTreeFanout is the most children a level subtree's node has.
const lodTreeFanout = 4

// NoParentError is a node's MaxParentError when a root cluster is under it:
// larger than any projection, so the parent test never skips it.
const NoParentError = math.MaxFloat32

// LODNode is a node of the tree.
type LODNode struct {
	// Center/Radius: the cull sphere.
	Center mgl32.Vec3
	Radius float32
	// The largest parent error under the node, and a sphere around every
	// parent sphere (NoParentError when a root is under it).
	MaxParentError float32
	ParentCenter   mgl32.Vec3
	ParentRadius   float32
	// The smallest own error under it, and a sphere around every own LOD
	// sphere.
	MinError  float32
	OwnCenter mgl32.Vec3
	OwnRadius float32
	// FirstChild/ChildCount: the node's children in the node array (none for
	// a leaf).
	FirstChild, ChildCount int32
	// ClusterFirst/ClusterCount: a leaf's clusters.
	ClusterFirst, ClusterCount int32
	// Leaf is a leaf's number (-1 for an inner node); LeafFirst/LeafCount
	// the leaves under the node.
	Leaf                 int32
	LeafFirst, LeafCount int32
}

// IsLeaf reports whether n is a leaf.
func (n *LODNode) IsLeaf() bool { return n.Leaf >= 0 }

// LeafRanges is every leaf's run of clusters, {first, count}, in cluster
// order.
func LeafRanges(nodes []LODNode) [][2]int32 {
	var out [][2]int32
	for i := range nodes {
		if nodes[i].IsLeaf() {
			out = append(out, [2]int32{nodes[i].ClusterFirst, nodes[i].ClusterCount})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a][0] < out[b][0] })
	return out
}

type treeNode struct {
	kids                 []*treeNode
	first, count         int32 // leaf's clusters
	node                 LODNode
	leafFirst, leafCount int32
}

// BuildLODTree builds the tree over clusters, whose slots are the runs
// ranges[i] = {first, count} (each laid out as a DAG lays its clusters out:
// by level, then parent). It returns the nodes (the root first) and each
// cluster's leaf number.
func BuildLODTree(clusters []LODCluster, ranges [][2]int32) ([]LODNode, []int32) {
	root := &treeNode{}
	for _, rg := range ranges {
		slot := &treeNode{}
		// Leaves: runs made and consumed by the same groups, at most
		// MaxLeafClusters.
		var leaves []*treeNode
		for i := rg[0]; i < rg[0]+rg[1]; {
			j := i + 1
			for j < rg[0]+rg[1] && j-i < MaxLeafClusters &&
				clusters[j].Level == clusters[i].Level && clusters[j].Parent == clusters[i].Parent &&
				clusters[j].Group == clusters[i].Group {
				j++
			}
			leaves = append(leaves, &treeNode{first: i, count: j - i})
			i = j
		}
		for _, l := range leaves {
			l.node = leafBounds(clusters[l.first : l.first+l.count])
		}
		// One subtree per level.
		for lo := 0; lo < len(leaves); {
			hi := lo
			lvl := clusters[leaves[lo].first].Level
			for hi < len(leaves) && clusters[leaves[hi].first].Level == lvl {
				hi++
			}
			slot.kids = append(slot.kids, buildLevel(leaves[lo:hi]))
			lo = hi
		}
		if len(slot.kids) == 1 {
			slot = slot.kids[0]
		} else if len(slot.kids) > 0 {
			slot.node = mergeNodes(slot.kids)
		}
		if len(slot.kids) > 0 || slot.count > 0 {
			root.kids = append(root.kids, slot)
		}
	}
	if len(root.kids) == 0 {
		return nil, make([]int32, len(clusters))
	}
	root.node = mergeNodes(root.kids)

	// Leaves numbered depth first.
	leafOf := make([]int32, len(clusters))
	next := int32(0)
	var number func(t *treeNode)
	number = func(t *treeNode) {
		t.leafFirst = next
		if len(t.kids) == 0 {
			t.node.Leaf = next
			for c := t.first; c < t.first+t.count; c++ {
				leafOf[c] = next
			}
			next++
		} else {
			t.node.Leaf = -1
			for _, k := range t.kids {
				number(k)
			}
		}
		t.leafCount = next - t.leafFirst
	}
	number(root)

	// Nodes breadth first.
	var out []LODNode
	queue := []*treeNode{root}
	out = append(out, LODNode{})
	for qi := 0; qi < len(queue); qi++ {
		t := queue[qi]
		n := t.node
		n.LeafFirst, n.LeafCount = t.leafFirst, t.leafCount
		if len(t.kids) == 0 {
			n.ClusterFirst, n.ClusterCount = t.first, t.count
		} else {
			n.FirstChild, n.ChildCount = int32(len(out)), int32(len(t.kids))
			for _, k := range t.kids {
				queue = append(queue, k)
				out = append(out, LODNode{})
			}
		}
		out[qi] = n
	}
	return out, leafOf
}

// buildLevel is a 4-ary tree over one level's leaves.
func buildLevel(leaves []*treeNode) *treeNode {
	if len(leaves) == 1 {
		return leaves[0]
	}
	if len(leaves) <= lodTreeFanout {
		t := &treeNode{kids: leaves}
		t.node = mergeNodes(leaves)
		return t
	}
	// Two median splits along the longest axis of the leaves' centres make
	// four parts.
	parts := [][]*treeNode{leaves}
	for round := 0; round < 2; round++ {
		var split [][]*treeNode
		for _, p := range parts {
			if len(p) < 2 {
				split = append(split, p)
				continue
			}
			a, b := medianSplit(p)
			split = append(split, a, b)
		}
		parts = split
	}
	t := &treeNode{}
	for _, p := range parts {
		if len(p) > 0 {
			t.kids = append(t.kids, buildLevel(p))
		}
	}
	t.node = mergeNodes(t.kids)
	return t
}

func medianSplit(p []*treeNode) ([]*treeNode, []*treeNode) {
	lo, hi := p[0].node.Center, p[0].node.Center
	for _, t := range p {
		lo, hi = vmin(lo, t.node.Center), vmax(hi, t.node.Center)
	}
	ext := hi.Sub(lo)
	axis := 0
	if ext[1] > ext[axis] {
		axis = 1
	}
	if ext[2] > ext[axis] {
		axis = 2
	}
	q := append([]*treeNode(nil), p...)
	sort.SliceStable(q, func(i, j int) bool { return q[i].node.Center[axis] < q[j].node.Center[axis] })
	m := len(q) / 2
	return q[:m], q[m:]
}

// leafBounds is the bounds of a run of clusters.
func leafBounds(cs []LODCluster) LODNode {
	var n LODNode
	cull := make([]sphere, len(cs))
	own := make([]sphere, len(cs))
	var parent []sphere
	n.MinError = float32(math.MaxFloat32)
	for i := range cs {
		c := &cs[i]
		cull[i] = sphere{c.Center, c.Radius}
		own[i] = sphere{c.LODCenter, c.LODRadius}
		n.MinError = min(n.MinError, c.Error)
		if c.ParentError < 0 {
			n.MaxParentError = NoParentError
		} else {
			parent = append(parent, sphere{c.ParentCenter, c.ParentRadius})
			n.MaxParentError = max(n.MaxParentError, c.ParentError)
		}
	}
	n.Center, n.Radius = enclose(cull)
	n.OwnCenter, n.OwnRadius = enclose(own)
	if len(parent) > 0 {
		n.ParentCenter, n.ParentRadius = enclose(parent)
	}
	return n
}

// mergeNodes is the bounds of a node over kids.
func mergeNodes(kids []*treeNode) LODNode {
	var n LODNode
	var cull, own, parent []sphere
	n.MinError = float32(math.MaxFloat32)
	for _, k := range kids {
		c := &k.node
		cull = append(cull, sphere{c.Center, c.Radius})
		own = append(own, sphere{c.OwnCenter, c.OwnRadius})
		n.MinError = min(n.MinError, c.MinError)
		if c.MaxParentError == NoParentError {
			n.MaxParentError = NoParentError
		} else if n.MaxParentError != NoParentError {
			n.MaxParentError = max(n.MaxParentError, c.MaxParentError)
		}
		if c.ParentRadius > 0 {
			parent = append(parent, sphere{c.ParentCenter, c.ParentRadius})
		}
	}
	n.Center, n.Radius = enclose(cull)
	n.OwnCenter, n.OwnRadius = enclose(own)
	if len(parent) > 0 {
		n.ParentCenter, n.ParentRadius = enclose(parent)
	}
	return n
}

type sphere struct {
	c mgl32.Vec3
	r float32
}

// enclose is a sphere around every one of s (the centre of their box, out to
// the furthest), a hair large so rounding never leaves one poking out.
func enclose(s []sphere) (mgl32.Vec3, float32) {
	if len(s) == 0 {
		return mgl32.Vec3{}, 0
	}
	lo, hi := s[0].c, s[0].c
	for _, x := range s {
		r := mgl32.Vec3{x.r, x.r, x.r}
		lo, hi = vmin(lo, x.c.Sub(r)), vmax(hi, x.c.Add(r))
	}
	c := lo.Add(hi).Mul(0.5)
	var r float32
	for _, x := range s {
		r = max(r, x.c.Sub(c).Len()+x.r)
	}
	return c, r*1.001 + 1e-5
}

// LODNearClamp is the nearest an eye is ever taken to be to an LOD sphere:
// inside one, the error is projected from this distance, which asks for the
// finest detail there is.
const LODNearClamp = 1e-3

// LODWithin is the selection's one test, the GPU's lodWithin
// (nanite_cull_common.glsl) on the CPU: an error of errv over the sphere
// (centre, radius) is fine enough for an eye at eye, where k is the
// pixels-per-unit factor over the threshold in pixels (k = viewport height /
// (2·tan(fovy/2)) / threshold). A projected error of errv·k/d pixels-per-
// threshold is within it when errv·k <= d, d the distance to the sphere's
// nearest point.
func LODWithin(errv float32, centre mgl32.Vec3, radius float32, eye mgl32.Vec3, k float32) bool {
	d := max(centre.Sub(eye).Len()-radius, LODNearClamp)
	return errv*k <= d
}

// SelectedFrom is whether cluster c is in the cut an eye at eye chooses (k
// as for LODWithin).
func (c *LODCluster) SelectedFrom(eye mgl32.Vec3, k float32) bool {
	if !LODWithin(c.Error, c.LODCenter, c.LODRadius, eye, k) {
		return false
	}
	return c.ParentError < 0 || !LODWithin(c.ParentError, c.ParentCenter, c.ParentRadius, eye, k)
}

// TreeSkips is whether the tree may skip node n for an eye at eye: what the
// GPU's traversal asks of every node it visits, with the same slack.
func TreeSkips(n *LODNode, eye mgl32.Vec3, k float32) bool {
	if n.MaxParentError != NoParentError {
		// Every parent fine enough: nothing under n is in the cut.
		d := max(n.ParentCenter.Sub(eye).Len()-n.ParentRadius, LODNearClamp)
		if n.MaxParentError*k < d*(1-1e-4) {
			return true
		}
	}
	// Every own error too coarse, even from the sphere's far side.
	far := max(n.OwnCenter.Sub(eye).Len()+n.OwnRadius, LODNearClamp)
	return n.MinError*k > far*(1+1e-4)+1e-6
}

// SelectThroughTree is the cut an eye chooses, found the way the GPU finds
// it: walk the tree, skip what TreeSkips allows, test the clusters of every
// leaf reached. For the tests, which check it against testing every cluster.
func SelectThroughTree(nodes []LODNode, clusters []LODCluster, eye mgl32.Vec3, k float32) []int {
	if len(nodes) == 0 {
		return nil
	}
	var out []int
	stack := []int32{0}
	for len(stack) > 0 {
		n := &nodes[stack[len(stack)-1]]
		stack = stack[:len(stack)-1]
		if TreeSkips(n, eye, k) {
			continue
		}
		if n.IsLeaf() {
			for c := n.ClusterFirst; c < n.ClusterFirst+n.ClusterCount; c++ {
				if clusters[c].SelectedFrom(eye, k) {
					out = append(out, int(c))
				}
			}
			continue
		}
		for c := n.FirstChild; c < n.FirstChild+n.ChildCount; c++ {
			stack = append(stack, c)
		}
	}
	sort.Ints(out)
	return out
}

// RefreshErrors recomputes every node's MinError and MaxParentError from the
// clusters' errors as own and parent give them (a parent error < 0 is a
// root), keeping the tree's shape and spheres: what a streamed mesh does when
// what is resident changes which of its groups may be refined
// (internal/nanite's residency.go). The spheres stay right, since only the
// errors move and every error a refresh hands in is no larger than the
// cooked one.
func RefreshErrors(nodes []LODNode, own, parent func(c int32) float32) {
	// Breadth first: a node's children come after it, so the reverse order
	// meets every child before its parent.
	for i := len(nodes) - 1; i >= 0; i-- {
		n := &nodes[i]
		n.MinError = float32(math.MaxFloat32)
		n.MaxParentError = 0
		if n.IsLeaf() {
			for c := n.ClusterFirst; c < n.ClusterFirst+n.ClusterCount; c++ {
				n.MinError = min(n.MinError, own(c))
				if p := parent(c); p < 0 {
					n.MaxParentError = NoParentError
				} else if n.MaxParentError != NoParentError {
					n.MaxParentError = max(n.MaxParentError, p)
				}
			}
			continue
		}
		for k := n.FirstChild; k < n.FirstChild+n.ChildCount; k++ {
			c := &nodes[k]
			n.MinError = min(n.MinError, c.MinError)
			if c.MaxParentError == NoParentError {
				n.MaxParentError = NoParentError
			} else if n.MaxParentError != NoParentError {
				n.MaxParentError = max(n.MaxParentError, c.MaxParentError)
			}
		}
	}
}

// The renderer draws a DAG's clusters as RANGE WINDOWS: runs of up to
// WindowMaxLeaves whole leaves and WindowMaxClusters clusters, each window a
// few indirect commands (render/nanite_cull.go, tileRange). The rule lives
// here because the cooked mesh's pages (internal/nanite's pages.go) are
// packed from the same windows, so a page boundary never splits one — a
// window that straddled two pages would have to be split into two, and on
// the GPUs measured every window's command slots cost whether they draw or
// not (docs/nanite/PHASE_3.md).
const (
	WindowMaxLeaves   = 3
	WindowMaxClusters = MaxLeafClusters
)

// RangeWindows packs consecutive leaves, {first, count} in cluster order,
// into windows: greedily, up to WindowMaxLeaves whole leaves and
// WindowMaxClusters clusters, never across a break in the clusters, nor
// across two leaves that apart says must stay apart (nil: none).
func RangeWindows(leaves [][2]int32, apart func(a, b int) bool) [][2]int32 {
	var out [][2]int32
	for i := 0; i < len(leaves); {
		w := leaves[i]
		n := 1
		for i+n < len(leaves) && n < WindowMaxLeaves {
			next := leaves[i+n]
			if next[0] != w[0]+w[1] || w[1]+next[1] > WindowMaxClusters {
				break
			}
			if apart != nil && apart(i, i+n) {
				break
			}
			w[1] += next[1]
			n++
		}
		out = append(out, w)
		i += n
	}
	return out
}
