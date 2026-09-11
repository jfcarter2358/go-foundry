package model

// model_topology.go — the edits that change how a mesh is connected without
// adding anything to it:
//
//	JoinFaces     several faces become the one face their outline describes
//	DissolveVerts a point that only bends a line is taken out of it, so the two
//	              segments either side of it become one
//	MergeVerts    several points become one
//
// All three remove things, which is what makes them harder than extrude or
// subdivide: a face or vertex index held by anyone — the editor's selection, a
// brush face's texture, a bone weight — can stop meaning what it meant. So each
// returns a TopologyEdit saying where everything went, and the brush bridge
// (editor_detail_mesh.go) reads FaceSrc to carry a brush face's attributes
// across, the same way it does for the operations that add faces.
//
// The vertex pool is kept compact, as DeleteFaces keeps it: a vertex that no
// face references any more because of the edit is dropped and the rest
// renumbered. A vertex that was already loose is left alone — this edit did not
// orphan it, and a model editor that tidied away points it was never asked
// about would be deleting someone's work.

import (
	"github.com/go-gl/mathgl/mgl32"
)

// TopologyEdit is how an edit renumbered the document, for a caller holding
// indices into the version before it.
type TopologyEdit struct {
	// FaceSrc is index-parallel to the edited document's Faces: FaceSrc[i] is
	// the index, before the edit, of the face that face i came from. A joined
	// face names the first face listed for it; a face a merge split in two
	// appears twice. Nil when the edit changed nothing.
	FaceSrc []int
	// VertRemap[v] is the index vertex v has now, or -1 when the edit removed
	// it. Nil when no vertex was removed, so the indices are unchanged.
	VertRemap []int
}

// Changed reports whether the edit did anything.
func (e TopologyEdit) Changed() bool { return e.FaceSrc != nil }

// Vert is VertRemap as a lookup that is safe on an edit which renumbered
// nothing: v itself, or -1 when it was removed.
func (e TopologyEdit) Vert(v int) int {
	if e.VertRemap == nil {
		return v
	}
	if v < 0 || v >= len(e.VertRemap) {
		return -1
	}
	return e.VertRemap[v]
}

// loopCorner is one corner of a face loop: its vertex and the UV the face gives
// that vertex.
type loopCorner struct {
	v  int
	uv mgl32.Vec2
}

// ---------- join ----------

// JoinFaces merges each edge-connected group of the given faces into a single
// face whose loop is the group's outline. The joined faces' indices in the
// edited document are returned, along with how many groups were refused.
//
// A group is joined only when its outline is ONE closed loop:
//
//   - its faces must be connected by shared edges — two faces touching at a
//     corner, or not at all, are two groups, and a group of one face has
//     nothing to join and is skipped without counting as refused;
//   - the outline must not cross itself or leave a hole. A ring of faces has
//     two outlines, and a face cannot have two, so the ring stays as it is;
//   - no edge may be shared by more than two of its faces.
//
// accept, when non-nil, is asked about each outline before it is committed and
// can refuse it. The model editor passes nil — an n-gon there can bend and be
// concave, since FaceTriangles ear-clips it — and a brush passes a test that
// the outline is flat and convex, which is what a brush face is.
//
// The joined face takes its smoothing from the first face listed for it, and
// keeps per-corner UVs only when every face of its group had them. Interior
// points — the ones only the group's faces used — are removed.
func (m *ModelDoc) JoinFaces(faces []int, accept func(loop []mgl32.Vec3) bool) (joined []int, refused int, edit TopologyEdit) {
	order := m.uniqueFaces(faces)
	if len(order) < 2 {
		return nil, 0, TopologyEdit{}
	}

	type plan struct {
		primary int // the first listed face; the joined face inherits from it
		at      int // the lowest member index, where the joined face goes
		members []int
		loop    []loopCorner
		uv      bool
	}
	var plans []plan
	for _, group := range m.edgeConnectedGroups(order) {
		if len(group) < 2 {
			continue
		}
		loop, ok := m.outlineOf(group)
		if ok && accept != nil {
			pts := make([]mgl32.Vec3, len(loop))
			for i, c := range loop {
				pts[i] = m.Verts[c.v]
			}
			ok = accept(pts)
		}
		if !ok {
			refused++
			continue
		}
		p := plan{primary: group[0], at: group[0], members: group, loop: loop, uv: true}
		for _, fi := range group {
			if fi < p.at {
				p.at = fi
			}
			if f := &m.Faces[fi]; len(f.UVs) != len(f.Verts) {
				p.uv = false
			}
		}
		plans = append(plans, p)
	}
	if len(plans) == 0 {
		return nil, refused, TopologyEdit{}
	}

	planAt := make(map[int]int, len(plans))
	member := map[int]bool{}
	for i, p := range plans {
		planAt[p.at] = i
		for _, fi := range p.members {
			member[fi] = true
		}
	}

	before := m.referencedVerts()
	out := make([]ModelFace, 0, len(m.Faces))
	src := make([]int, 0, len(m.Faces))
	for fi := range m.Faces {
		if i, ok := planAt[fi]; ok {
			p := plans[i]
			nf := ModelFace{Verts: make([]int, len(p.loop)), Smooth: m.Faces[p.primary].Smooth}
			if p.uv {
				nf.UVs = make([]mgl32.Vec2, len(p.loop))
			}
			for j, c := range p.loop {
				nf.Verts[j] = c.v
				if p.uv {
					nf.UVs[j] = c.uv
				}
			}
			joined = append(joined, len(out))
			out = append(out, nf)
			src = append(src, p.primary)
			continue
		}
		if member[fi] {
			continue
		}
		out = append(out, m.Faces[fi])
		src = append(src, fi)
	}
	m.Faces = out

	edit = TopologyEdit{FaceSrc: src, VertRemap: m.removeVerts(orphanedVerts(before, m.referencedVerts(), nil))}
	m.Invalidate()
	return joined, refused, edit
}

// edgeConnectedGroups partitions faces into the groups that share edges with
// one another. Each group lists its faces in the order they were given, so its
// first entry is the one the caller named first.
func (m *ModelDoc) edgeConnectedGroups(faces []int) [][]int {
	parent := make(map[int]int, len(faces))
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	owner := map[EdgeKey]int{}
	for _, fi := range faces {
		parent[fi] = fi
	}
	for _, fi := range faces {
		f := &m.Faces[fi]
		n := len(f.Verts)
		for i := 0; i < n; i++ {
			e := MakeEdgeKey(f.Verts[i], f.Verts[(i+1)%n])
			if other, ok := owner[e]; ok {
				if a, b := find(fi), find(other); a != b {
					parent[a] = b
				}
				continue
			}
			owner[e] = fi
		}
	}

	var groups [][]int
	index := map[int]int{}
	for _, fi := range faces {
		root := find(fi)
		gi, ok := index[root]
		if !ok {
			gi = len(groups)
			index[root] = gi
			groups = append(groups, nil)
		}
		groups[gi] = append(groups[gi], fi)
	}
	return groups
}

// outlineOf walks a group's boundary — the edges only one of its faces uses —
// into a single loop, in the winding the faces themselves have. ok is false
// when there is no such single loop.
//
// The walk starts on the group's first face, so a join of faces that were
// already one face's pieces begins where that face began.
func (m *ModelDoc) outlineOf(group []int) ([]loopCorner, bool) {
	uses := map[EdgeKey]int{}
	for _, fi := range group {
		f := &m.Faces[fi]
		n := len(f.Verts)
		for i := 0; i < n; i++ {
			uses[MakeEdgeKey(f.Verts[i], f.Verts[(i+1)%n])]++
		}
	}

	type half struct {
		to int
		uv mgl32.Vec2
	}
	next := map[int]half{}
	var starts []int
	for _, fi := range group {
		f := &m.Faces[fi]
		n := len(f.Verts)
		for i := 0; i < n; i++ {
			a, b := f.Verts[i], f.Verts[(i+1)%n]
			if a == b {
				continue
			}
			switch uses[MakeEdgeKey(a, b)] {
			case 1:
			case 2:
				continue // interior: both its faces are in the group
			default:
				return nil, false // non-manifold: no outline to speak of
			}
			if _, dup := next[a]; dup {
				// Two boundary edges leave the same point: the outline touches
				// itself there, or the faces disagree about which way round
				// they are wound. Either way it is not one simple loop.
				return nil, false
			}
			next[a] = half{to: b, uv: f.UvAt(i)}
			starts = append(starts, a)
		}
	}
	if len(next) < 3 {
		return nil, false
	}

	start := starts[0]
	loop := make([]loopCorner, 0, len(next))
	for cur := start; ; {
		h, ok := next[cur]
		if !ok || len(loop) == len(next) {
			return nil, false
		}
		loop = append(loop, loopCorner{v: cur, uv: h.uv})
		cur = h.to
		if cur == start {
			break
		}
	}
	// Boundary edges the walk never reached belong to a second loop — the rim
	// of a hole, or a second region touching this one only at a corner.
	if len(loop) != len(next) {
		return nil, false
	}
	return loop, true
}

// ---------- dissolve ----------

// LineNeighbours reports the two points vi sits between when it is a point on
// a line: exactly two edges meet at it. Those are the points a dissolve would
// join directly.
//
// A mesh corner — a cube's, where three edges meet — is not one, and neither
// is the centre of a subdivided face: removing either would change which faces
// exist, not how finely a line is cut. The corner of an open sheet is one: two
// edges meet there, and dissolving it cuts the corner off.
func (m *ModelDoc) LineNeighbours(vi int) (a, b int, ok bool) {
	if vi < 0 || vi >= len(m.Verts) {
		return -1, -1, false
	}
	return m.lineNeighboursIn(vi, m.FacesAtVertex(vi))
}

// lineNeighboursIn is LineNeighbours over a stated face list, so a dissolve can
// keep asking while it edits the loops the adjacency index was built from.
// Faces already collapsed below three corners are ignored: they are about to
// be dropped, and a two-corner face reports the same point on both sides.
func (m *ModelDoc) lineNeighboursIn(vi int, faces []int) (a, b int, ok bool) {
	a, b = -1, -1
	seen := 0
	for _, fi := range faces {
		f := &m.Faces[fi]
		n := len(f.Verts)
		if n < 3 {
			continue
		}
		for i, v := range f.Verts {
			if v != vi {
				continue
			}
			prev, next := f.Verts[(i+n-1)%n], f.Verts[(i+1)%n]
			if prev == next || prev == vi || next == vi {
				return -1, -1, false
			}
			for _, nb := range [2]int{prev, next} {
				switch {
				case nb == a || nb == b:
				case a < 0:
					a = nb
				case b < 0:
					b = nb
				default:
					return -1, -1, false // a third neighbour
				}
			}
			seen++
		}
	}
	if seen == 0 || a < 0 || b < 0 {
		return -1, -1, false
	}
	if b < a {
		a, b = b, a
	}
	return a, b, true
}

// DissolveVerts takes each of the given points out of every face loop it is in,
// where LineNeighbours says it is a point on a line, and returns the ones it
// took out (as indices from before the edit). The rest are left where they are.
//
// They are handled one at a time against the loops as they stand, so a run of
// points along one edge dissolves all the way down to its two ends: taking out
// the first leaves the second between the same two neighbours it had. A face
// that drops below three corners — a triangle whose tip was dissolved — is
// removed, and its neighbour's edge takes its place.
func (m *ModelDoc) DissolveVerts(verts []int) (dissolved []int, edit TopologyEdit) {
	order := m.uniqueVerts(verts)
	if len(order) == 0 {
		return nil, TopologyEdit{}
	}
	// Read before anything moves: removing a point from a loop only ever
	// deletes that point's own entries, so the index stays true for every
	// other point for the rest of the edit.
	vertFaces := m.adjacency().vertFaces
	before := m.referencedVerts()

	touched := map[int]bool{}
	gone := map[int]bool{}
	for _, v := range order {
		if _, _, ok := m.lineNeighboursIn(v, vertFaces[v]); !ok {
			continue
		}
		for _, fi := range vertFaces[v] {
			removeCorner(&m.Faces[fi], v)
			touched[fi] = true
		}
		gone[v] = true
		dissolved = append(dissolved, v)
	}
	if len(dissolved) == 0 {
		return nil, TopologyEdit{}
	}

	src := m.cleanFaces(touched)
	edit = TopologyEdit{FaceSrc: src, VertRemap: m.removeVerts(orphanedVerts(before, m.referencedVerts(), gone))}
	m.Invalidate()
	return dissolved, edit
}

// removeCorner deletes every corner of f that is vertex v, and its UV with it.
func removeCorner(f *ModelFace, v int) {
	uv := len(f.UVs) == len(f.Verts)
	verts := f.Verts[:0]
	var uvs []mgl32.Vec2
	if uv {
		uvs = f.UVs[:0]
	}
	for i, fv := range f.Verts {
		if fv == v {
			continue
		}
		if uv {
			uvs = append(uvs, f.UVs[i])
		}
		verts = append(verts, fv)
	}
	f.Verts = verts
	if uv {
		f.UVs = uvs
	}
}

// ---------- merge ----------

// MergeVerts collapses the given points into one, placed at at, and returns
// that point's index in the edited document — -1 when fewer than two valid
// points were given, which is not a merge.
//
// The survivor is the first point listed, so a caller that says "merge at the
// first selected" and one that says "at the centre" differ only in at. It takes
// the blend of every merged point's bone weights, the way a vertex an operation
// invents between others does.
//
// Every face loop that ran through two of the points now runs through the same
// one twice. Where the two were neighbours that is a collapsed edge, and the
// corner simply goes; where they were not, the loop has been pinched into two,
// and it is split there. Any loop left with fewer than three corners — the
// triangle whose edge was merged away — is removed.
func (m *ModelDoc) MergeVerts(verts []int, at mgl32.Vec3) (kept int, edit TopologyEdit) {
	order := m.uniqueVerts(verts)
	if len(order) < 2 {
		return -1, TopologyEdit{}
	}
	keep := order[0]
	into := make(map[int]bool, len(order)-1)
	lists := make([][]ModelVertexWeight, 0, len(order))
	m.EnsureWeights()
	for _, v := range order {
		lists = append(lists, m.Weights[v])
		if v != keep {
			into[v] = true
		}
	}
	m.Verts[keep] = at
	m.Weights[keep] = blendWeights(lists...)

	before := m.referencedVerts()
	touched := map[int]bool{}
	for fi := range m.Faces {
		f := &m.Faces[fi]
		for i, v := range f.Verts {
			if into[v] {
				f.Verts[i] = keep
				touched[fi] = true
			}
		}
	}

	src := m.cleanFaces(touched)
	drop := orphanedVerts(before, m.referencedVerts(), into)
	delete(drop, keep)
	edit = TopologyEdit{FaceSrc: src, VertRemap: m.removeVerts(drop)}
	m.Invalidate()
	return edit.Vert(keep), edit
}

// VertsCentroid is the average position of the given points — where a merge
// "at the centre" puts them.
func (m *ModelDoc) VertsCentroid(verts []int) mgl32.Vec3 {
	var sum mgl32.Vec3
	n := 0
	for _, v := range m.uniqueVerts(verts) {
		sum = sum.Add(m.Verts[v])
		n++
	}
	if n == 0 {
		return sum
	}
	return sum.Mul(1 / float32(n))
}

// ---------- shared bookkeeping ----------

// cleanFaces tidies the loops of the touched faces after corners were removed
// or rewritten: a corner repeating the one before it goes, a loop that visits
// one point twice is split there, and whatever is left with fewer than three
// corners is dropped. Untouched faces pass through as they are. Returns
// FaceSrc for the resulting face list.
func (m *ModelDoc) cleanFaces(touched map[int]bool) []int {
	out := make([]ModelFace, 0, len(m.Faces))
	src := make([]int, 0, len(m.Faces))
	for fi := range m.Faces {
		f := m.Faces[fi]
		if !touched[fi] {
			out = append(out, f)
			src = append(src, fi)
			continue
		}
		uv := len(f.UVs) == len(f.Verts)
		corners := make([]loopCorner, len(f.Verts))
		for i, v := range f.Verts {
			corners[i] = loopCorner{v: v, uv: f.UvAt(i)}
		}
		for _, loop := range splitLoop(dedupeCorners(corners)) {
			nf := ModelFace{Verts: make([]int, len(loop)), Smooth: f.Smooth}
			if uv {
				nf.UVs = make([]mgl32.Vec2, len(loop))
			}
			for i, c := range loop {
				nf.Verts[i] = c.v
				if uv {
					nf.UVs[i] = c.uv
				}
			}
			out = append(out, nf)
			src = append(src, fi)
		}
	}
	m.Faces = out
	return src
}

// dedupeCorners drops each corner that repeats the vertex of the corner before
// it, around the loop's wrap as well.
func dedupeCorners(cs []loopCorner) []loopCorner {
	out := make([]loopCorner, 0, len(cs))
	for _, c := range cs {
		if len(out) > 0 && out[len(out)-1].v == c.v {
			continue
		}
		out = append(out, c)
	}
	for len(out) > 1 && out[0].v == out[len(out)-1].v {
		out = out[:len(out)-1]
	}
	return out
}

// splitLoop cuts a loop that visits some point more than once into the simple
// loops it is made of, and keeps those with at least three corners. The input
// has no consecutive repeats (dedupeCorners), and neither piece of a cut does.
func splitLoop(cs []loopCorner) [][]loopCorner {
	at := make(map[int]int, len(cs))
	for i, c := range cs {
		j, seen := at[c.v]
		if !seen {
			at[c.v] = i
			continue
		}
		inner := append([]loopCorner(nil), cs[j:i]...)
		rest := append(append([]loopCorner(nil), cs[:j]...), cs[i:]...)
		return append(splitLoop(inner), splitLoop(rest)...)
	}
	if len(cs) < 3 {
		return nil
	}
	return [][]loopCorner{cs}
}

// uniqueFaces is faces with duplicates and out-of-range indices dropped, in
// the order given.
func (m *ModelDoc) uniqueFaces(faces []int) []int {
	seen := make(map[int]bool, len(faces))
	out := make([]int, 0, len(faces))
	for _, fi := range faces {
		if fi < 0 || fi >= len(m.Faces) || seen[fi] {
			continue
		}
		seen[fi] = true
		out = append(out, fi)
	}
	return out
}

// uniqueVerts is uniqueFaces for vertex indices.
func (m *ModelDoc) uniqueVerts(verts []int) []int {
	seen := make(map[int]bool, len(verts))
	out := make([]int, 0, len(verts))
	for _, v := range verts {
		if v < 0 || v >= len(m.Verts) || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// referencedVerts is the set of vertices some face uses.
func (m *ModelDoc) referencedVerts() map[int]bool {
	used := make(map[int]bool, len(m.Verts))
	for fi := range m.Faces {
		for _, v := range m.Faces[fi].Verts {
			used[v] = true
		}
	}
	return used
}

// orphanedVerts is what an edit leaves to be removed: the points a face used
// before it and none uses after, plus any the edit took out by name.
func orphanedVerts(before, after, named map[int]bool) map[int]bool {
	drop := map[int]bool{}
	for v := range before {
		if !after[v] {
			drop[v] = true
		}
	}
	for v := range named {
		drop[v] = true
	}
	return drop
}

// removeVerts deletes the given vertices — which no face may still use — and
// renumbers the survivors, their weights and every face loop to match. Returns
// the old-to-new mapping, or nil when there was nothing to remove.
func (m *ModelDoc) removeVerts(drop map[int]bool) []int {
	if len(drop) == 0 {
		return nil
	}
	m.EnsureWeights()
	remap := make([]int, len(m.Verts))
	verts := make([]mgl32.Vec3, 0, len(m.Verts))
	weights := make([][]ModelVertexWeight, 0, len(m.Verts))
	for v := range m.Verts {
		if drop[v] {
			remap[v] = -1
			continue
		}
		remap[v] = len(verts)
		verts = append(verts, m.Verts[v])
		weights = append(weights, m.Weights[v])
	}
	for fi := range m.Faces {
		f := &m.Faces[fi]
		// A fresh slice: an untouched face still shares its loop's backing
		// array with the document an undo snapshot or a bridge was built from.
		loop := make([]int, len(f.Verts))
		for i, v := range f.Verts {
			loop[i] = v
			if v >= 0 && v < len(remap) {
				loop[i] = remap[v]
			}
		}
		f.Verts = loop
	}
	m.Verts = verts
	m.Weights = weights
	return remap
}
