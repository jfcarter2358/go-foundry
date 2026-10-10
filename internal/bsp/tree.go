package bsp

// tree.go — the partition itself, and the questions a running game asks it.
//
// This package is deliberately the STRUCTURES and not the builder. Building a
// tree means running CSG over a level's brushes, which needs to know what a
// level is; asking one where the camera is does not. Splitting them is what
// lets internal/world hold a compiled level's tree without importing the
// compiler, and it is also the honest description of the dev/release seam — a
// shipped game reads these, a development build also has the code that made
// them (internal/compile).
//
// # What a leaf means
//
// Three states, and the difference between the last two matters:
//
//   - SOLID: inside brush material. Nothing is drawn there and nothing can be
//     there.
//   - OUTSIDE: open space, but in the void AROUND the level rather than inside
//     it. Filled in by the compiler so that visibility is a tractable question
//     at all — see internal/compile/outside.go.
//   - neither: interior open space, somewhere the player can stand. These are
//     the cells visibility has anything to say about.

import "github.com/go-gl/mathgl/mgl32"

// ChildRef points at either a node or a leaf. Non-negative values index
// Tree.Nodes; negative values encode a leaf as ^i, so leaf 0 is -1 and there is
// no ambiguous zero. One int rather than a pair of "index plus kind" fields
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

// Node is one split of space.
type Node struct {
	Plane       Plane
	Front, Back ChildRef
	// Faces are the compiled faces lying ON this node's plane, as indices into
	// whatever face list the tree's owner holds. The tree itself does not know
	// what a face is; it only ever hands the numbers back.
	Faces []int
	// Lo, Hi bound everything below this node, for a runtime frustum reject
	// that skips a whole subtree.
	Lo, Hi mgl32.Vec3
}

// Leaf is a convex cell.
type Leaf struct {
	// Solid is true for a cell inside brush material.
	Solid bool
	// Outside is true for a cell in the void around the level rather than
	// inside it. See the file comment.
	Outside bool
	// Planes bound the cell: a point is inside when it is in FRONT of every one
	// of them. This is the cell as a convex polytope, and it is what portal
	// generation clips against.
	Planes []Plane
	// Faces are the faces that bound this cell and face into it — what to draw
	// when the camera is here. Sorted.
	Faces  []int
	Lo, Hi mgl32.Vec3
}

// Tree is a built partition.
type Tree struct {
	Nodes  []Node
	Leaves []Leaf
	Root   ChildRef
	// Lo, Hi are the world box the tree partitions: the level's own bounds
	// grown by a margin, so the outermost cells are closed rather than running
	// to infinity.
	Lo, Hi mgl32.Vec3
}

// FindLeaf is which cell a point is in: one plane comparison per level of the
// tree, and no geometry touched at all.
func (t *Tree) FindLeaf(p mgl32.Vec3) int {
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

// LineOfSight reports whether the segment from a to b passes through nothing
// solid.
//
// The test is against the TREE, not against any faces: a segment is blocked
// exactly when it enters a solid cell, which is one plane comparison per level
// and no polygon touched at all. Intersecting faces would be the same answer
// computed the expensive way, and would have to special-case a segment that
// grazes a shared edge — which the cell test cannot get wrong, because a cell
// is either solid or it is not.
func (t *Tree) LineOfSight(a, b mgl32.Vec3) bool {
	return !t.blocked(t.Root, a, b)
}

func (t *Tree) blocked(c ChildRef, p1, p2 mgl32.Vec3) bool {
	if c.IsLeaf() {
		return t.Leaves[c.Leaf()].Solid
	}
	n := &t.Nodes[c.Node()]
	d1, d2 := n.Plane.Distance(p1), n.Plane.Distance(p2)
	switch {
	case d1 >= 0 && d2 >= 0:
		return t.blocked(n.Front, p1, p2)
	case d1 < 0 && d2 < 0:
		return t.blocked(n.Back, p1, p2)
	}
	// The segment crosses this plane: split it there and walk the near half
	// first, so a blocked segment stops at the first thing it hits rather than
	// descending the whole tree.
	mid := p1.Add(p2.Sub(p1).Mul(d1 / (d1 - d2)))
	if d1 >= 0 {
		return t.blocked(n.Front, p1, mid) || t.blocked(n.Back, mid, p2)
	}
	return t.blocked(n.Back, p1, mid) || t.blocked(n.Front, mid, p2)
}

// EmptyLeafCount is how many cells are open space, void included.
func (t *Tree) EmptyLeafCount() int {
	n := 0
	for i := range t.Leaves {
		if !t.Leaves[i].Solid {
			n++
		}
	}
	return n
}

// InteriorLeafCount is how many cells are open space the player can actually
// occupy — the ones visibility has something to say about.
func (t *Tree) InteriorLeafCount() int {
	n := 0
	for i := range t.Leaves {
		if !t.Leaves[i].Solid && !t.Leaves[i].Outside {
			n++
		}
	}
	return n
}

// LeaksAt reports whether a point the level cares about — a spawn, an entity —
// is in the void, which is what a leak looks like from the inside: a gap in the
// shell let the fill in, and everything it reached stopped being part of the
// level.
func (t *Tree) LeaksAt(p mgl32.Vec3) bool {
	l := &t.Leaves[t.FindLeaf(p)]
	return l.Outside && !l.Solid
}

// Interior reports whether a cell is somewhere to stand: open, and inside the
// level. The predicate the renderer gates on, so that a camera clipped into a
// wall or floated out of the world is recognised rather than trusted.
func (t *Tree) Interior(leaf int) bool {
	if leaf < 0 || leaf >= len(t.Leaves) {
		return false
	}
	l := &t.Leaves[leaf]
	return !l.Solid && !l.Outside
}
