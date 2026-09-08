package compile

// pvs.go — portals, and what can be seen through them.
//
// The BSP left the world as convex empty cells. Two cells that share part of a
// plane are joined by a PORTAL — the overlap of their two boundary polygons on
// that plane — and every sight line in the level passes through some sequence
// of them. That reduces visibility, which is a question about an infinite set
// of rays, to a question about a finite graph.
//
// The answer is a POTENTIALLY VISIBLE SET: one row of bits per empty cell,
// saying which other cells could possibly be seen from anywhere within it. The
// runtime looks up the row for the cell the camera is in and skips everything
// outside it, before any frustum test, before any draw call. That is what a
// sealed level buys — a room you are not in costs nothing, not even a rejected
// bounding box.
//
// # Conservative, and why that is the right kind of wrong
//
// The set is potentially visible, not exactly visible. Marking a cell visible
// that turns out not to be costs a few wasted draws; failing to mark one that
// is costs a hole in the world. So every approximation here errs toward
// visible, and the two places it does so are called out where they happen.
//
// # How the flow works
//
// From a cell A, look through each portal leaving it. Everything immediately
// beyond is visible. To see further, light must pass through a SECOND portal,
// and the part of that second portal A can actually see through the first is
// found by clipping it against SEPARATING PLANES: planes through an edge of the
// source and a vertex of the pass portal, which no ray from source to target
// can cross. Clip until nothing is left, and the search stops on its own.
//
// This is Quake's algorithm with its optimisations left out. It is the
// expensive stage of the compile and it is meant to be — it runs once, offline,
// and every frame of the shipped game spends the result.

import (
	"math/bits"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// Portal is an opening between two empty cells: the polygon they share, seen
// from A looking toward B.
//
// Portals come in pairs — every A->B has a B->A with the same polygon and the
// opposite plane — because the flow always walks forward through one and a
// single undirected record would have to be re-oriented at every step.
type Portal struct {
	Points []mgl32.Vec3
	// Plane faces from A into B, so "in front of this plane" is the direction
	// the flow is travelling.
	Plane Plane
	A, B  int
}

// BuildPortals finds every opening between two empty cells.
//
// Pairwise over the empty leaves, which is quadratic, and deliberately so for
// now: the bounding-box reject in front of the plane test throws out almost
// every pair in a level of any size, and the honest expensive stage is the
// visibility flood below rather than this. A leaf's boundary polygons are
// computed once each rather than per pair, which is the part that would
// actually hurt.
func BuildPortals(t *BSPTree) []Portal {
	polys := make([][][]mgl32.Vec3, len(t.Leaves))
	for i := range t.Leaves {
		if t.Leaves[i].Solid {
			continue
		}
		polys[i] = regionPolygons(t.Leaves[i].Planes, t.Lo, t.Hi)
	}

	var out []Portal
	for a := range t.Leaves {
		if t.Leaves[a].Solid {
			continue
		}
		for b := a + 1; b < len(t.Leaves); b++ {
			if t.Leaves[b].Solid {
				continue
			}
			if !boxesOverlap(t.Leaves[a].Lo, t.Leaves[a].Hi, t.Leaves[b].Lo, t.Leaves[b].Hi) {
				continue
			}
			for i, pa := range polys[a] {
				if pa == nil {
					continue
				}
				pl := t.Leaves[a].Planes[i]
				for j, pb := range polys[b] {
					if pb == nil || !t.Leaves[b].Planes[j].SameAs(pl.Flip()) {
						continue
					}
					shared := intersectCoplanar(pa, pl.N, pb, pl.N.Mul(-1))
					if len(shared) < 3 || PolygonArea(shared) < minPortalArea {
						continue
					}
					out = append(out,
						Portal{Points: shared, Plane: pl.Flip(), A: a, B: b},
						Portal{Points: shared, Plane: pl, A: b, B: a})
				}
			}
		}
	}
	return out
}

// minPortalArea is the smallest opening worth treating as one. Two cells
// touching along an edge or at a corner produce a sliver of a shared polygon
// that nothing can actually see through, and admitting it would connect
// everything to everything through the level's own grid lines.
const minPortalArea = float32(1e-4)

// intersectCoplanar is the overlap of two convex polygons on the same plane.
//
// a is clipped by b's edges rather than either being triangulated: both are
// convex, so the intersection is a's winding cut by b's half-planes, which is
// the same ClipPolygon every other stage uses.
//
// Each polygon is wound about its OWN normal — a leaf's boundary polygon faces
// out of that leaf, so the two sides of a shared plane wind opposite ways — so
// the normal to build b's edge planes from is passed in rather than derived.
func intersectCoplanar(a []mgl32.Vec3, aN mgl32.Vec3, b []mgl32.Vec3, bN mgl32.Vec3) []mgl32.Vec3 {
	out := a
	for i := range b {
		if out == nil {
			return nil
		}
		p, q := b[i], b[(i+1)%len(b)]
		e := q.Sub(p)
		n := bN.Cross(e)
		if n.Len() < 1e-9 {
			continue
		}
		n = n.Normalize()
		out = ClipPolygon(out, Plane{N: n, D: n.Dot(p)})
	}
	return out
}

// PVS is the visibility bit array: one row per leaf, one bit per leaf.
//
// Rows are addressed by LEAF index, including solid leaves, whose rows are
// simply empty. Compacting to empty leaves only would save a little space and
// cost every lookup a translation table, and the runtime lookup is the thing
// that has to be cheap.
type PVS struct {
	Leaves   int
	RowBytes int
	Bits     []byte
}

// NewPVS is an all-invisible set.
func NewPVS(leaves int) *PVS {
	rb := (leaves + 7) / 8
	return &PVS{Leaves: leaves, RowBytes: rb, Bits: make([]byte, leaves*rb)}
}

// Set marks to as visible from.
func (v *PVS) Set(from, to int) {
	if from < 0 || to < 0 || from >= v.Leaves || to >= v.Leaves {
		return
	}
	v.Bits[from*v.RowBytes+to/8] |= 1 << uint(to%8)
}

// Visible reports whether anything in leaf to could be seen from leaf from.
func (v *PVS) Visible(from, to int) bool {
	if from < 0 || to < 0 || from >= v.Leaves || to >= v.Leaves {
		return false
	}
	return v.Bits[from*v.RowBytes+to/8]&(1<<uint(to%8)) != 0
}

// Row is the raw bits for one leaf, for a runtime that wants to walk them
// directly rather than call Visible once per leaf.
func (v *PVS) Row(leaf int) []byte {
	if leaf < 0 || leaf >= v.Leaves {
		return nil
	}
	return v.Bits[leaf*v.RowBytes : (leaf+1)*v.RowBytes]
}

// VisibleCount is how many leaves are visible from one, which is the number
// worth watching when tuning a level: a PVS where every row is nearly the whole
// level means the level is not sealed.
func (v *PVS) VisibleCount(leaf int) int {
	n := 0
	for i := 0; i < v.Leaves; i++ {
		if v.Visible(leaf, i) {
			n++
		}
	}
	return n
}

// maxFlowDepth bounds the portal chain the flood will follow, and maxFlowSteps
// bounds the total work one source portal may do.
//
// Neither should ever fire. The clipping and the mightSee pruning below both
// terminate on their own, and a level that reaches either has something
// pathological about it — an enormous open region carved into hundreds of cells
// that all see each other. They are backstops against a compile that never
// finishes, and both fall back to plain reachability rather than to silence.
const (
	maxFlowDepth = 64
	maxFlowSteps = 2000000
)

// visState is the whole of the visibility computation.
//
// Note that the bookkeeping is over PORTALS, not cells. That is the difference
// between a flood that finishes and one that does not: cells are what the
// answer is about, but a sight line is characterised by the apertures it passed
// through, and it is the shrinking set of still-reachable apertures that lets a
// branch be abandoned. Marking cells alone gives no such handle, and the search
// then explores every distinct PATH through the portal graph — which for a few
// dozen cells in an open region is already astronomically many.
type visState struct {
	portals []Portal
	byLeaf  [][]int
	vis     *PVS

	portalBytes int
	// mightSee is the coarse answer: for each portal, which portals could
	// conceivably be seen through it, by orientation and connectivity alone.
	// It is what every finer step is a subset of.
	mightSee []byte
	// baseSeen is the portals the source portal currently being flooded has
	// been shown to see. Reset per source.
	baseSeen []byte
	// portalVis is baseSeen kept, per portal, once that portal's own flood has
	// finished, and done says which of those are filled in. A finished portal's
	// exact set is far tighter than its mightSee, and using it to bound a later
	// flood is the single biggest saving here — which is why portals are
	// processed cheapest-first, so that the tight answers exist before the
	// expensive floods need them.
	portalVis []byte
	done      []bool
	// scratch holds one might-set per recursion depth, so the inner loop does
	// not allocate. Depth d's buffer is only ever the "might" of depth d+1, and
	// that call has its own, so reusing it across candidates at one depth is
	// safe.
	scratch [][]byte
	budget  int
	// aborted latches once a source portal has exhausted its budget: the whole
	// flood for that source unwinds without doing any more work, having already
	// fallen back to plain reachability. Without the latch the give-up path
	// runs at every level on the way out, which costs more than the flood it
	// was supposed to cut short.
	aborted    bool
	reachSeen  []bool
	reachQueue []int
}

func bitSet(b []byte, i int)      { b[i>>3] |= 1 << uint(i&7) }
func bitGet(b []byte, i int) bool { return b[i>>3]&(1<<uint(i&7)) != 0 }

// BuildPVS floods visibility through the portals.
func BuildPVS(t *BSPTree, portals []Portal) *PVS {
	v := NewPVS(len(t.Leaves))
	for i := range t.Leaves {
		// Every cell can see itself; the flood below only ever adds to that.
		// A filled-in exterior cell is not one of them: nothing is ever there,
		// and giving it a row would invite a runtime to draw from it.
		if !t.Leaves[i].Solid && !t.Leaves[i].Outside {
			v.Set(i, i)
		}
	}
	if len(portals) == 0 {
		return v
	}

	byLeaf := make([][]int, len(t.Leaves))
	for i := range portals {
		byLeaf[portals[i].A] = append(byLeaf[portals[i].A], i)
	}
	s := &visState{
		portals:     portals,
		byLeaf:      byLeaf,
		vis:         v,
		portalBytes: (len(portals) + 7) / 8,
	}
	s.reachSeen = make([]bool, len(t.Leaves))
	s.scratch = make([][]byte, maxFlowDepth+1)
	for i := range s.scratch {
		s.scratch[i] = make([]byte, s.portalBytes)
	}
	s.buildMightSee()
	s.portalVis = make([]byte, len(portals)*s.portalBytes)
	s.done = make([]bool, len(portals))

	might := make([]byte, s.portalBytes)
	for _, pi := range s.floodOrder() {
		v.Set(portals[pi].A, portals[pi].B)
		s.baseSeen = s.portalVis[pi*s.portalBytes : (pi+1)*s.portalBytes]
		bitSet(s.baseSeen, pi)
		s.budget, s.aborted = maxFlowSteps, false
		copy(might, s.mightSee[pi*s.portalBytes:(pi+1)*s.portalBytes])
		s.flow(pi, portals[pi].B, might, portals[pi].Points, portals[pi].Points, 1)
		s.done[pi] = true
	}
	return v
}

// floodOrder is the order to flood the portals in: fewest candidates first.
//
// A portal with a small mightSee is cheap to resolve exactly, and its exact
// answer then bounds every later flood that passes through it. Doing the
// expensive ones first would mean every one of them ran against nothing but
// coarse bounds. Ties break on the portal index so the compile stays
// reproducible.
func (s *visState) floodOrder() []int {
	order := make([]int, len(s.portals))
	counts := make([]int, len(s.portals))
	for i := range order {
		order[i] = i
		row := s.mightSee[i*s.portalBytes : (i+1)*s.portalBytes]
		for _, b := range row {
			counts[i] += bits.OnesCount8(b)
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return counts[order[a]] < counts[order[b]] })
	return order
}

// buildMightSee fills the coarse per-portal candidate sets.
//
// Two filters, and they are cheap on purpose — this runs over every ordered
// pair of portals, so it has to be. First orientation: a portal entirely behind
// the source's plane is behind it, and one whose own plane the source is
// entirely in front of faces the wrong way. Then connectivity: flood forward
// from the source's far cell through the surviving candidates and keep only
// what the flood actually reaches, which is what stops a sealed room from ever
// being a candidate for anything outside it.
func (s *visState) buildMightSee() {
	n := len(s.portals)
	pairs := make([]byte, n*s.portalBytes)
	for i := range s.portals {
		row := pairs[i*s.portalBytes : (i+1)*s.portalBytes]
		for j := range s.portals {
			if i == j {
				continue
			}
			if s.portals[i].Plane.ClassifyPolygon(s.portals[j].Points) == SideBack {
				continue
			}
			if s.portals[j].Plane.ClassifyPolygon(s.portals[i].Points) == SideFront {
				continue
			}
			bitSet(row, j)
		}
	}

	s.mightSee = make([]byte, n*s.portalBytes)
	seen := make([]bool, len(s.byLeaf))
	for i := range s.portals {
		cand := pairs[i*s.portalBytes : (i+1)*s.portalBytes]
		out := s.mightSee[i*s.portalBytes : (i+1)*s.portalBytes]
		for j := range seen {
			seen[j] = false
		}
		s.simpleFlood(s.portals[i].B, cand, out, seen)
	}
}

// simpleFlood marks every candidate portal reachable from a cell, ignoring
// geometry entirely.
func (s *visState) simpleFlood(leaf int, cand, out []byte, seen []bool) {
	if seen[leaf] {
		return
	}
	seen[leaf] = true
	for _, qi := range s.byLeaf[leaf] {
		if !bitGet(cand, qi) {
			continue
		}
		bitSet(out, qi)
		s.simpleFlood(s.portals[qi].B, cand, out, seen)
	}
}

// flow walks forward from a source portal, marking what its own cell can see.
//
// base is the portal the flood started at, and base.A the cell whose row is
// being filled. source is that portal's polygon, narrowed by everything passed
// through so far; pass is the polygon of the portal just stepped through,
// likewise narrowed. might is the set of portals still reachable along this
// path.
//
// The pruning that makes this finish is the "nothing new" test: a candidate
// that has already been seen from this source, and through which nothing
// unseen is still reachable, is skipped without descending. Without it the
// search enumerates paths rather than reachable sets, and an open region of a
// few dozen cells takes minutes.
func (s *visState) flow(base, leaf int, might []byte, source, pass []mgl32.Vec3, depth int) {
	if s.aborted {
		return
	}
	s.vis.Set(s.portals[base].A, leaf)
	if s.budget <= 0 {
		s.aborted = true
		s.markReachable(s.portals[base].A, s.portals[base].A)
		return
	}
	if depth >= maxFlowDepth {
		s.markReachable(s.portals[base].A, leaf)
		return
	}
	s.budget--

	next := s.scratch[depth]
	for _, qi := range s.byLeaf[leaf] {
		if !bitGet(might, qi) {
			continue
		}
		more := false
		// A portal whose own flood has finished bounds this one exactly; one
		// that has not falls back to its coarse candidate set.
		qRow := s.mightSee[qi*s.portalBytes : (qi+1)*s.portalBytes]
		if s.done[qi] {
			qRow = s.portalVis[qi*s.portalBytes : (qi+1)*s.portalBytes]
		}
		for j := 0; j < s.portalBytes; j++ {
			next[j] = might[j] & qRow[j]
			if next[j]&^s.baseSeen[j] != 0 {
				more = true
			}
		}
		if !more && bitGet(s.baseSeen, qi) {
			continue
		}

		// Only ever move away from the source and forward through the next
		// portal, and only through the part of each that the other can
		// actually reach. Both tests demand a STRICTLY forward part: a target
		// merely touching the source's plane is coplanar with it, and two
		// apertures in the same plane see each other edge-on, which is to say
		// not at all. That case is not exotic — a U-bend has both its corners
		// on one plane, and admitting it makes the two ends of the U visible
		// to each other through a wall.
		if side := s.portals[base].Plane.ClassifyPolygon(s.portals[qi].Points); side == SideBack || side == SideOn {
			continue
		}
		target := ClipPolygon(s.portals[qi].Points, s.portals[base].Plane)
		if len(target) < 3 {
			continue
		}
		if side := s.portals[qi].Plane.ClassifyPolygon(source); side == SideFront || side == SideOn {
			continue
		}
		narrowed := ClipPolygon(source, s.portals[qi].Plane.Flip())
		if len(narrowed) < 3 {
			continue
		}

		// What of the target can the source see through the aperture just
		// stepped through? Clipped from both ends: a separating plane derived
		// from the source and the aperture bounds the target, and one derived
		// from the target and the aperture bounds the source the same way, so
		// a pair that only appears to see each other from one end is caught.
		// The narrowed source is what carries forward, which is what makes a
		// long corridor close off rather than merely getting no wider.
		target = clipToSeparators(narrowed, pass, target)
		if len(target) < 3 {
			continue
		}
		narrowed = clipToSeparators(target, pass, narrowed)
		if len(narrowed) < 3 {
			continue
		}

		bitSet(s.baseSeen, qi)
		s.vis.Set(s.portals[base].A, s.portals[qi].B)
		s.flow(base, s.portals[qi].B, next, narrowed, target, depth+1)
		// next is scratch[depth] and the recursion used scratch[depth+1], so it
		// still holds this candidate's set — but the next candidate overwrites
		// it from the top, so nothing has to be restored.
	}
}

// markReachable is the give-up path, used when the flood hits its depth or step
// bound: mark every cell reachable from here through any portal at all.
//
// That is the answer visibility would give if no wall ever blocked anything, so
// it is the most conservative thing that is still finite. It costs draws, never
// holes, and a level that triggers it is one to look at rather than one that is
// broken.
//
// The reused scratch is not an optimisation for the common case — it is for the
// pathological one, where this is called at every node of a flood that has run
// out of budget and a freshly allocated map each time is most of the compile.
func (s *visState) markReachable(from, leaf int) {
	for i := range s.reachSeen {
		s.reachSeen[i] = false
	}
	s.reachSeen[leaf] = true
	queue := append(s.reachQueue[:0], leaf)
	for len(queue) > 0 {
		l := queue[0]
		queue = queue[1:]
		s.vis.Set(from, l)
		for _, qi := range s.byLeaf[l] {
			if n := s.portals[qi].B; !s.reachSeen[n] {
				s.reachSeen[n] = true
				queue = append(queue, n)
			}
		}
	}
	s.reachQueue = queue[:0]
}

// clipToSeparators cuts target down to the part a ray from source through pass
// could reach.
//
// For each edge of source and each vertex of pass, the plane through the two is
// a candidate SEPARATOR: source is convex and planar and the plane contains one
// of its edges, so all of source lies on one side of it. If all of pass lies on
// the other side, then no ray leaving source and crossing pass can come back
// across that plane, and the part of target behind it is unreachable.
//
// A candidate that pass STRADDLES separates nothing and is skipped — that is
// where the conservatism lives, and it is the right way round: a skipped
// candidate leaves target larger than it strictly has to be.
func clipToSeparators(source, pass, target []mgl32.Vec3) []mgl32.Vec3 {
	out := target
	for i := range source {
		a, b := source[i], source[(i+1)%len(source)]
		for _, p := range pass {
			if len(out) < 3 {
				return nil
			}
			pl, ok := separatorPlane(a, b, p, source, pass)
			if !ok {
				continue
			}
			out = ClipPolygon(out, pl)
		}
	}
	if len(out) < 3 {
		return nil
	}
	return out
}

// separatorPlane builds the plane through edge a->b and point p, oriented so
// that pass is in FRONT of it, and reports whether it separates at all.
func separatorPlane(a, b, p mgl32.Vec3, source, pass []mgl32.Vec3) (Plane, bool) {
	n := b.Sub(a).Cross(p.Sub(a))
	l := n.Len()
	if l < 1e-6 {
		return Plane{}, false // the edge and the point are collinear
	}
	n = n.Mul(1 / l)
	pl := Plane{N: n, D: n.Dot(a)}

	// Orient so source is behind. Source cannot straddle — the plane contains
	// one of its edges and it is convex and planar — but it can lie in the
	// plane entirely, which separates nothing.
	sf, sb := sidesOf(pl, source)
	switch {
	case sf > 0 && sb > 0:
		return Plane{}, false
	case sf > 0:
		pl = pl.Flip()
	case sb == 0:
		return Plane{}, false
	}

	// A separator has all of pass strictly on the other side from source.
	pf, pb := sidesOf(pl, pass)
	if pb > 0 || pf == 0 {
		return Plane{}, false
	}
	return pl, true
}

// sidesOf counts how many of pts are in front of and behind a plane; points ON
// it count for neither.
func sidesOf(pl Plane, pts []mgl32.Vec3) (front, back int) {
	for _, p := range pts {
		switch pl.ClassifyPoint(p) {
		case SideFront:
			front++
		case SideBack:
			back++
		}
	}
	return front, back
}

// ComputeVisibility runs the whole visibility stage for a built tree: portals,
// then the void fill that makes the flood tractable, then the flood.
//
// The tree is modified — MarkOutside labels its leaves — which is why this
// takes a pointer and why the returned portals are the interior ones only.
func ComputeVisibility(t *BSPTree) ([]Portal, *PVS) {
	portals := BuildPortals(t)
	MarkOutside(t, portals)
	portals = InteriorPortals(t, portals)
	return portals, BuildPVS(t, portals)
}
