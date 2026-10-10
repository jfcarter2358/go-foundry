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

import (
	"foundry/internal/bsp"

	"github.com/go-gl/mathgl/mgl32"
)

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
	t.reachedVia = make([]outsideStep, len(t.Leaves))
	for i := range t.reachedVia {
		t.reachedVia[i].from = -1
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
				t.reachedVia[b] = outsideStep{from: li, at: bsp.Centroid(portals[pi].Points)}
				queue = append(queue, b)
			}
		}
	}
	return n
}

// outsideStep is how the fill reached one cell: the cell it came from, and the
// middle of the opening it came through. from is -1 for a cell the fill started
// in, and for every cell it never reached.
type outsideStep struct {
	from int
	at   mgl32.Vec3
}

// leakOpening is roughly where the void gets in on its way to p.
//
// "p is outside the level" is true and no use: the author put p inside the
// level and can see that it is. What they need is the gap. The fill is
// breadth-first, so walking its path back from p's cell retraces the shortest
// way in from the world box, and the gap is where that path last passes from a
// cell running out to the world box's edge — open space by construction, since
// nothing is authored within worldMargin of it — into one that does not. The
// middle of that opening is the answer: not the exact hole, but the doorway
// that stands open onto nothing.
//
// A room with a wall simply missing is the other shape a leak takes: nothing in
// the tree cuts the room's own cell off at the gap, so that cell runs straight
// out to the world box and there is no opening between cells to name. The gap
// is then inside the cell, where it crosses the edge of what the level
// authored, and crossingOutOfLevel finds that instead.
//
// ok is false when p is not in a filled cell, or when p is itself beyond
// everything authored: then it is simply out in the void, with no gap to point
// at, and naming one would send the author looking for a hole that is not there.
func (t *BSPTree) leakOpening(p mgl32.Vec3) (at mgl32.Vec3, ok bool) {
	cur := t.FindLeaf(p)
	if t.reachedVia == nil || !t.Leaves[cur].Outside || !t.withinAuthored(p) {
		return mgl32.Vec3{}, false
	}
	if t.touchesWorldBox(cur) {
		return t.crossingOutOfLevel(cur, p)
	}
	// A breadth-first path visits each cell once, so this bound is a backstop
	// rather than a limit.
	for range t.Leaves {
		step := t.reachedVia[cur]
		if step.from < 0 {
			return mgl32.Vec3{}, false
		}
		if t.touchesWorldBox(step.from) {
			return step.at, true
		}
		cur = step.from
	}
	return mgl32.Vec3{}, false
}

// authoredBounds is the box around everything the level authored: the tree's
// box less the margin BuildBSP added to it.
func (t *BSPTree) authoredBounds() (lo, hi mgl32.Vec3) {
	lo, hi = t.Lo, t.Hi
	for k := 0; k < 3; k++ {
		lo[k] += worldMargin
		hi[k] -= worldMargin
	}
	return lo, hi
}

// withinAuthored reports whether p is inside authoredBounds.
func (t *BSPTree) withinAuthored(p mgl32.Vec3) bool {
	lo, hi := t.authoredBounds()
	for k := 0; k < 3; k++ {
		if p[k] < lo[k] || p[k] > hi[k] {
			return false
		}
	}
	return true
}

// crossingOutOfLevel is where a cell passes out of authoredBounds: the middle of
// its cross-section with a side of that box, taking the side nearest p when the
// cell leaves by more than one. See leakOpening.
func (t *BSPTree) crossingOutOfLevel(leaf int, p mgl32.Vec3) (at mgl32.Vec3, ok bool) {
	lo, hi := t.authoredBounds()
	cell := t.Leaves[leaf].Planes
	var bestDist float32
	for k := 0; k < 3; k++ {
		var axis mgl32.Vec3
		axis[k] = 1
		for _, side := range []bsp.Plane{{N: axis, D: hi[k]}, {N: axis.Mul(-1), D: -lo[k]}} {
			// The polygon RegionPolygons makes for the side's own plane is that
			// plane clipped by every one of the cell's: the cross-section.
			planes := append(append(make([]bsp.Plane, 0, len(cell)+1), cell...), side)
			polys := bsp.RegionPolygons(planes, t.Lo, t.Hi)
			section := polys[len(polys)-1]
			if section == nil {
				continue
			}
			c := bsp.Centroid(section)
			if d := c.Sub(p).Len(); !ok || d < bestDist {
				at, bestDist, ok = c, d, true
			}
		}
	}
	return at, ok
}

// touchesWorldBox reports whether a cell runs out to an edge of the tree's box.
func (t *BSPTree) touchesWorldBox(leaf int) bool {
	const eps = float32(1e-3)
	l := &t.Leaves[leaf]
	for k := 0; k < 3; k++ {
		if l.Lo[k] <= t.Lo[k]+eps || l.Hi[k] >= t.Hi[k]-eps {
			return true
		}
	}
	return false
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
