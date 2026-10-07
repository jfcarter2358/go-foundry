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
	"foundry/internal/bsp"

	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// BSPTree is a built partition together with the faces it partitions.
//
// The tree proper lives in internal/bsp, which knows nothing about faces — a
// shipped game reads it without linking the compiler that made it. This is the
// compiler's own view: the same tree, plus the face list its cells index into.
// Embedded rather than a field, so a builder reads t.Leaves and t.FindLeaf the
// way it always did.
type BSPTree struct {
	*bsp.Tree
	// Faces are the compiled faces AFTER the build, which splits any face a
	// chosen plane crosses. A fragment keeps every attribute of the face it
	// came from, so this list is interchangeable with CSG's output as far as
	// the renderer is concerned — there are just more of them.
	Faces []Face

	// reachedVia is how the void fill reached each cell, kept so that a leak
	// can be traced back to the opening it came in through. Set by
	// MarkOutside; nil until it has run.
	reachedVia []outsideStep
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
	t := &BSPTree{Tree: &bsp.Tree{}, Faces: append([]Face(nil), faces...)}
	SortFaces(t.Faces)

	t.Lo, t.Hi = faceSetBounds(t.Faces)
	for i := 0; i < 3; i++ {
		t.Lo[i] -= worldMargin
		t.Hi[i] += worldMargin
	}

	// Only the STRUCTURAL faces are partitioned. A detail face is a bump, a
	// ledge or a reveal carved into a wall's surface (world/brush_detail.go),
	// and letting one into the build would be wrong twice over: its plane
	// would be offered as a knife, cutting the level into cells that describe
	// a moulding rather than a room, and — because solidity here is read off
	// the face normals — the space behind it would be labelled material. The
	// partition and the visibility that comes out of it have to describe the
	// SOLID, which is exactly the hull.
	//
	// So they are inserted afterwards, into the cells the hull made. That is
	// Quake's detail brush and it is the same bargain: detail costs draws, not
	// tree.
	structural := make([]int, 0, len(t.Faces))
	var detail []int
	for i := range t.Faces {
		if t.Faces[i].CoveredBy != nil {
			// Buried while its neighbour is on, drawn past the PVS while it is
			// off: in no cell either way. See Face.CoveredBy.
			continue
		}
		if t.Faces[i].Detail {
			detail = append(detail, i)
			continue
		}
		structural = append(structural, i)
	}
	t.Root = t.build(structural, boxPlanes(t.Lo, t.Hi), 0, false)
	t.attachFacesToLeaves()
	t.attachDetailFaces(detail)
	t.computeNodeBounds(t.Root)
	return t
}

// attachDetailFaces drops each detail face into every empty cell its polygon
// reaches, walking it down from the root the same way a node's coplanar faces
// are pushed down to the leaves they bound.
//
// From the ROOT rather than from a node, because a detail face was never
// assigned to one — it took no part in choosing the planes it is now being
// sorted by. The walk splits it as it descends, so a bump spanning two cells is
// drawn from both.
//
// A detail face that comes to rest in a SOLID cell is the other case, and it is
// most of what detail is for: a recess, a reveal, a panel pressed into a wall
// all sit behind the hull's surface, inside the material the tree was built
// from. Nothing is ever drawn from a solid cell, so those go through
// attachInsetDetail instead.
func (t *BSPTree) attachDetailFaces(detail []int) {
	if len(detail) == 0 {
		return
	}
	var inset []int
	for _, fi := range detail {
		if t.insertDetail(t.Root, fi, t.Faces[fi].Points) {
			inset = append(inset, fi)
		}
	}
	t.attachInsetDetail(inset)
	// markFace appends, so the cells it touched need the same tidy-up
	// attachFacesToLeaves gives the ones it filled.
	for li := range t.Leaves {
		l := &t.Leaves[li]
		sort.Ints(l.Faces)
		l.Faces = dedupInts(l.Faces)
	}
}

// insertDetail is markFace for a detail face: it records fi in every empty leaf
// the polygon reaches, and reports whether any real part of it came to rest in
// a solid one instead.
func (t *BSPTree) insertDetail(c bsp.ChildRef, fi int, pts []mgl32.Vec3) (buried bool) {
	if len(pts) < 3 {
		return false
	}
	if c.IsLeaf() {
		l := &t.Leaves[c.Leaf()]
		if !l.Solid {
			l.Faces = append(l.Faces, fi)
			return false
		}
		return bsp.PolygonArea(pts) >= bsp.MinFragmentArea
	}
	n := &t.Nodes[c.Node()]
	switch n.Plane.ClassifyPolygon(pts) {
	case bsp.SideFront:
		return t.insertDetail(n.Front, fi, pts)
	case bsp.SideBack:
		return t.insertDetail(n.Back, fi, pts)
	case bsp.SideOn:
		// Lying on a hull face's plane — which a detail mesh seeded from its
		// hull does, face for face. It is seen from the side it faces. The
		// other side is the hull's own material, and counting that as buried
		// would send every untouched face of a detailed brush down the inset
		// path. A double-sided face is still drawn from both, but only the
		// side it faces decides whether it is buried.
		f := &t.Faces[fi]
		facing, behind := n.Front, n.Back
		if f.Plane.N.Dot(n.Plane.N) < 0 {
			facing, behind = n.Back, n.Front
		}
		if f.DoubleSided {
			t.markFace(behind, fi, pts)
		}
		return t.insertDetail(facing, fi, pts)
	default:
		fr, bk := bsp.SplitPolygon(pts, n.Plane)
		inFront := t.insertDetail(n.Front, fi, fr)
		behind := t.insertDetail(n.Back, fi, bk)
		return inFront || behind
	}
}

// attachInsetDetail gives each detail face that came to rest inside solid
// material to every empty cell its own brush's hull faces bound.
//
// Those are the cells an inset is seen FROM. A recess sunk into a floor is
// looked at through the floor's top face, and the cells in front of that face
// are exactly the ones attachFacesToLeaves would have drawn the undetailed
// floor from. The hull faces are still in the tree for that purpose — a
// detailed brush emits them as NoDraw structure (csg.go) — so they are walked
// down their facing side the same way a drawn node face is, recording the
// inset face rather than themselves.
//
// Every hull face of the brush rather than the one the inset happens to be
// under, because which one that is is not a local question for a recess wall
// or a reveal cut through a corner. The cost is draws, not correctness: a
// cell beside a different face of the brush is handed a surface the depth
// test then hides.
func (t *BSPTree) attachInsetDetail(inset []int) {
	if len(inset) == 0 {
		return
	}
	type brushKey struct{ chunk, brush string }
	byBrush := map[brushKey][]int{}
	for _, fi := range inset {
		k := brushKey{t.Faces[fi].Chunk, t.Faces[fi].Brush}
		byBrush[k] = append(byBrush[k], fi)
	}
	for ni := range t.Nodes {
		n := &t.Nodes[ni]
		for _, hi := range n.Faces {
			h := &t.Faces[hi]
			if !h.NoDraw {
				continue
			}
			faces := byBrush[brushKey{h.Chunk, h.Brush}]
			if len(faces) == 0 {
				continue
			}
			child := n.Front
			if h.Plane.N.Dot(n.Plane.N) < 0 {
				child = n.Back
			}
			for _, fi := range faces {
				t.markFace(child, fi, h.Points)
			}
		}
	}
}

// faceSetBounds is the box around every compiled face. An empty level still
// gets a unit box rather than a degenerate one, so that boxPlanes below
// produces six usable planes and the tree has a root at all.
func faceSetBounds(faces []Face) (lo, hi mgl32.Vec3) {
	if len(faces) == 0 {
		return mgl32.Vec3{-1, -1, -1}, mgl32.Vec3{1, 1, 1}
	}
	lo, hi = bsp.Bounds(faces[0].Points)
	for _, f := range faces[1:] {
		l, h := bsp.Bounds(f.Points)
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
func boxPlanes(lo, hi mgl32.Vec3) []bsp.Plane {
	return []bsp.Plane{
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
func (t *BSPTree) build(faceIdx []int, region []bsp.Plane, depth int, solid bool) bsp.ChildRef {
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
		switch t.sideOf(fi, pl) {
		case bsp.SideOn:
			onNode = append(onNode, fi)
		case bsp.SideFront:
			front = append(front, fi)
		case bsp.SideBack:
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
	t.Nodes = append(t.Nodes, bsp.Node{Plane: pl, Faces: onNode})
	// The children are built after the node is appended so that a subtree can
	// grow t.Nodes underneath it; the index is stable, the pointer would not be.
	f := t.build(front, appendPlane(region, pl), depth+1, false)
	b := t.build(back, appendPlane(region, pl.Flip()), depth+1, true)
	t.Nodes[ni].Front, t.Nodes[ni].Back = f, b
	return bsp.NodeRef(ni)
}

// sideOf is where face fi lies relative to a splitting plane.
//
// A face whose own plane IS the splitter goes on the node, whatever its corners
// say. A face that is not quite flat — a brush whose corners have been dragged
// out of line, so that a quad bends across its own plane — has corners on both
// sides of that plane, and classifying them would split it. Both halves keep the
// plane they came from, so the next level down offers the same plane again,
// which splits them again: the same cut down one branch until maxBSPDepth, with
// a copy of the plane in every cell beneath it. Portals are found pairwise over
// cell boundaries, and a hundred copies of one plane on each side of a boundary
// made millions of them — enough that the visibility stage asked for terabytes.
//
// Consumed at the node, the face stops recursing the first time its plane is
// used, and the cells it bounds are the cells a flat face would have made.
func (t *BSPTree) sideOf(fi int, pl bsp.Plane) bsp.Side {
	own := t.Faces[fi].Plane
	if own.SameAs(pl) || own.SameAs(pl.Flip()) {
		return bsp.SideOn
	}
	return pl.ClassifyPolygon(t.Faces[fi].Points)
}

// appendPlane copies rather than appending in place. A region slice is handed
// to two children, and an in-place append would let the front child's plane
// overwrite the back child's in the shared backing array.
func appendPlane(region []bsp.Plane, pl bsp.Plane) []bsp.Plane {
	out := make([]bsp.Plane, len(region), len(region)+1)
	copy(out, region)
	return append(out, pl)
}

func (t *BSPTree) addLeaf(solid bool, region []bsp.Plane) bsp.ChildRef {
	l := bsp.Leaf{Solid: solid, Planes: region}
	l.Lo, l.Hi = bsp.RegionBounds(region, t.Lo, t.Hi)
	t.Leaves = append(t.Leaves, l)
	return bsp.LeafRef(len(t.Leaves) - 1)
}

// splitFace cuts face fi with a plane, appends the halves and returns their
// indices; either is -1 when that half came back degenerate.
//
// The fragments inherit everything about the original but its points — the
// texture, the UVs, the source names — so a fragment is still fully drawable
// and still says which authored face it came from.
func (t *BSPTree) splitFace(fi int, pl bsp.Plane) (front, back int) {
	f, b := bsp.SplitPolygon(t.Faces[fi].Points, pl)
	front, back = -1, -1
	if len(f) >= 3 && bsp.PolygonArea(f) >= bsp.MinFragmentArea {
		g := t.Faces[fi]
		g.Points = f
		g.dropLight() // no longer parallel to Points; the bake refills it
		t.Faces = append(t.Faces, g)
		front = len(t.Faces) - 1
	}
	if len(b) >= 3 && bsp.PolygonArea(b) >= bsp.MinFragmentArea {
		g := t.Faces[fi]
		g.Points = b
		g.dropLight()
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
func (t *BSPTree) chooseSplitter(faceIdx []int) (bsp.Plane, bool) {
	var (
		best      bsp.Plane
		bestScore = int(^uint(0) >> 1)
		found     bool
		tried     []bsp.Plane
	)
	// The candidates are the first distinct planes in face order — which ones
	// does not depend on how they score, so they are all picked first.
	for _, fi := range faceIdx {
		if len(tried) >= maxSplitterCandidates {
			break
		}
		pl := t.Faces[fi].Plane
		if planeSeen(tried, pl) {
			continue
		}
		tried = append(tried, pl)
	}
	// Counting each candidate's sides is the work: candidates times faces. The
	// counts are integers and each candidate's are its own, so counting them
	// in parallel and then scoring in candidate order below picks exactly the
	// plane the serial loop did, ties and all.
	counts := make([][3]int, len(tried))
	count := func(c int) {
		pl := tried[c]
		front, back, splits := 0, 0, 0
		for _, oi := range faceIdx {
			switch t.sideOf(oi, pl) {
			case bsp.SideFront:
				front++
			case bsp.SideBack:
				back++
			case bsp.SideSpanning:
				splits++
			}
		}
		counts[c] = [3]int{front, back, splits}
	}
	if parallelSplitterScore && len(faceIdx) >= parallelSplitterFaces {
		parallelFor(len(tried), count)
	} else {
		for c := range tried {
			count(c)
		}
	}
	for c, pl := range tried {
		front, back, splits := counts[c][0], counts[c][1], counts[c][2]
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

// parallelSplitterScore counts the splitter candidates' sides across cores at
// a node holding at least parallelSplitterFaces faces; below that the
// goroutines cost more than they save. The switch is for the tests.
var parallelSplitterScore = true

var parallelSplitterFaces = 2048

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
		if bsp.Abs(bsp.Abs(n[i])-1) < 1e-4 {
			return true
		}
	}
	return false
}

func planeSeen(seen []bsp.Plane, pl bsp.Plane) bool {
	for _, s := range seen {
		if s.SameAs(pl) {
			return true
		}
	}
	return false
}

// attachFacesToLeaves gives every empty leaf the list of faces that bound it.
//
// A node's coplanar faces are pushed down from the ROOT and land in the leaves
// they actually touch. Pushing rather than testing every leaf against every
// face is what keeps this linear-ish: a face visits only the cells its own
// polygon reaches.
//
// From the root rather than from the face's own node. The build filed the face
// by the exact side of every plane above it, and a plane a hair off the face's
// own (see attachEps) can leave the face's node on the far side of it from the
// open air the face looks into, with only a zero-volume sliver in front of the
// face on its own side. From the root, markFace hands the face across such a
// plane, and at its own node it goes to both sides, where the side behind is
// its own material and the solid filter drops it.
func (t *BSPTree) attachFacesToLeaves() {
	for ni := range t.Nodes {
		n := &t.Nodes[ni]
		for _, fi := range n.Faces {
			if t.Faces[fi].NoDraw {
				// Structure only: it chose planes and decided solidity on the
				// way down, and a cell's face list is a DRAW list. See
				// Face.NoDraw.
				continue
			}
			t.markFace(t.Root, fi, t.Faces[fi].Points)
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
func (t *BSPTree) markFace(c bsp.ChildRef, fi int, pts []mgl32.Vec3) {
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
	switch classifyForAttach(n.Plane, pts) {
	case bsp.SideFront:
		t.markFace(n.Front, fi, pts)
	case bsp.SideBack:
		t.markFace(n.Back, fi, pts)
	case bsp.SideOn:
		// Coplanar with a node deeper in the tree. It bounds cells on both
		// sides of it, and the empty-leaf filter above drops whichever of those
		// turns out to be material.
		t.markFace(n.Front, fi, pts)
		t.markFace(n.Back, fi, pts)
	default:
		f, b := bsp.SplitPolygon(pts, n.Plane)
		t.markFace(n.Front, fi, f)
		t.markFace(n.Back, fi, b)
	}
}

// attachEps is how far off a splitting plane a face may lie and still be
// handed to the cells on both sides of it (markFace).
//
// Two neighbouring faces that are flat to the eye can have planes that differ
// by less than a centimetre across the face and still not be SameAs: a hair's
// tilt, 90 m from the origin, moves D by far more than bsp.OnPlaneEps. The
// build then cuts the space between them into a paper-thin cell with no
// volume, and so no portals, and a face lying a hair behind the other plane
// was filed only there. level1's ground_cliff_040 had its top split into two
// such triangles; one of them was drawn from that dead cell and no other, so
// it was missing from the landing zone. Handing a face that is within this of
// a plane to both sides costs at most a draw from an extra cell.
const attachEps = float32(0.05)

// classifyForAttach is where markFace sends a polygon. One that reaches past
// attachEps on both sides is split, as ClassifyPolygon would. One that reaches
// past it on one side and does not stray behind the plane on the other goes to
// that side. Anything else goes to both (SideOn): a face within attachEps of the
// plane, or grazing it — including a slightly bent face at its own node, which
// can sit a few millimetres behind its own plane.
func classifyForAttach(pl bsp.Plane, pts []mgl32.Vec3) bsp.Side {
	farFront, farBack, front, back := false, false, false, false
	for _, p := range pts {
		d := pl.Distance(p)
		farFront = farFront || d > attachEps
		farBack = farBack || d < -attachEps
		front = front || d > bsp.OnPlaneEps
		back = back || d < -bsp.OnPlaneEps
	}
	switch {
	case farFront && farBack:
		return bsp.SideSpanning
	case farFront && !back:
		return bsp.SideFront
	case farBack && !front:
		return bsp.SideBack
	}
	return bsp.SideOn
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
func (t *BSPTree) computeNodeBounds(c bsp.ChildRef) (lo, hi mgl32.Vec3) {
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
