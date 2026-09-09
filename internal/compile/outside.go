package compile

// outside.go — filling in the void.
//
// The BSP partitions a box sixteen units bigger than the level in every
// direction, so most of what it produces is empty cells in the space AROUND the
// level: the far side of every wall, the sky above the roof, the ground under
// the floor. They are geometrically real — they are open air by the tree's own
// rule — and they are also somewhere the player can never be.
//
// Left in, they are ruinous. Portal visibility is a search over paths through
// apertures, and the exterior is one enormous region carved into dozens of
// cells that all see each other through wide-open portals that never clip down
// to nothing. Two hollow rooms and a box around them produced 35 open cells and
// 180 portals, and the flood over them took minutes — not because the algorithm
// is wrong, but because it was being asked a question about a region that has
// no answer worth having.
//
// So the void is filled, exactly as qbsp has always done it: flood outward from
// the corners of the world box through the portals, and every empty cell the
// flood reaches is OUTSIDE. Outside cells take no part in visibility, hold no
// portals, and are never drawn from. What is left is the inside of the level.
//
// # This makes sealing a requirement, and that is the intended bargain
//
// A level with a gap in its shell — a missing wall, two brushes that do not
// quite meet — lets the flood in, and everything it reaches becomes outside:
// the level effectively disappears. That is a LEAK, it is the same leak every
// id-lineage compiler reports, and reporting it is more useful than quietly
// compiling a level whose visibility is meaningless. An open-air level is
// sealed the way an open-air Quake level is: with a shell of sky brushes around
// it.

import "github.com/go-gl/mathgl/mgl32"

// MarkOutside floods from the world box and marks every empty leaf it reaches.
// It returns how many were marked.
//
// The seeds are the eight corners of the tree's own box, pulled in by a hair so
// that a corner exactly on a bounding plane does not classify unpredictably.
// The box is worldMargin past the level's extent on every side, so a corner of
// it is void by construction — there is nothing to author out there.
func MarkOutside(t *BSPTree, portals []Portal) int {
	byLeaf := make([][]int, len(t.Leaves))
	for i := range portals {
		byLeaf[portals[i].A] = append(byLeaf[portals[i].A], i)
	}

	const inset = float32(0.01)
	var queue []int
	seen := make([]bool, len(t.Leaves))
	for c := 0; c < 8; c++ {
		p := mgl32.Vec3{}
		for a := 0; a < 3; a++ {
			if c&(1<<uint(a)) != 0 {
				p[a] = t.Hi[a] - inset
			} else {
				p[a] = t.Lo[a] + inset
			}
		}
		li := t.FindLeaf(p)
		if t.Leaves[li].Solid || seen[li] {
			continue
		}
		seen[li] = true
		queue = append(queue, li)
	}

	n := 0
	for len(queue) > 0 {
		li := queue[0]
		queue = queue[1:]
		t.Leaves[li].Outside = true
		n++
		for _, pi := range byLeaf[li] {
			if b := portals[pi].B; !seen[b] && !t.Leaves[b].Solid {
				seen[b] = true
				queue = append(queue, b)
			}
		}
	}
	return n
}

// InteriorPortals drops every portal with an end in a filled-in cell.
//
// A portal into the void is not an opening the player can look through; it is
// the boundary between the level and the space the level is sitting in. Keeping
// it would put the whole exterior back into the visibility flood, which is the
// thing filling was for.
func InteriorPortals(t *BSPTree, portals []Portal) []Portal {
	out := portals[:0:0]
	for _, p := range portals {
		if t.Leaves[p.A].Outside || t.Leaves[p.B].Outside {
			continue
		}
		out = append(out, p)
	}
	return out
}
