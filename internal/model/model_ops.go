package model

// model_ops.go — the model editor's mesh operations.
//
// Every function here is a pure edit of a *ModelDoc: no GL, no ImGui, no
// Editor state. That is deliberate — it means extrude, subdivide and
// Catmull-Clark can be unit-tested for their actual topology without standing up
// a window, which is where the bugs in this kind of code live.
//
// The shared discipline: read the adjacency index *before* mutating anything
// (it is derived from the face list, so the first edit invalidates it), build
// the result, then assign and call invalidate.

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// uvAt returns the face's UV for corner i, or the zero UV when the face carries
// fewer UVs than vertices (which a hand-written .fmdl or a UV-less OBJ can do).
func (f *ModelFace) UvAt(i int) mgl32.Vec2 {
	if i < 0 || i >= len(f.UVs) {
		return mgl32.Vec2{}
	}
	return f.UVs[i]
}

// faceSet turns a selection slice into a set, dropping out-of-range and
// duplicate indices so callers can pass a raw selection.
func (m *ModelDoc) faceSet(faces []int) map[int]bool {
	set := make(map[int]bool, len(faces))
	for _, fi := range faces {
		if fi >= 0 && fi < len(m.Faces) {
			set[fi] = true
		}
	}
	return set
}

// VertsOfFaces returns the unique vertex indices used by the given faces, in
// ascending order. This is how a face or edge selection becomes something the
// translate gizmo can move.
func (m *ModelDoc) VertsOfFaces(faces []int) []int {
	seen := map[int]bool{}
	for fi := range m.faceSet(faces) {
		for _, v := range m.Faces[fi].Verts {
			seen[v] = true
		}
	}
	return sortedKeys(seen)
}

// VertsOfEdges returns the unique endpoints of the given edges, ascending.
func (m *ModelDoc) VertsOfEdges(edges []EdgeKey) []int {
	seen := map[int]bool{}
	for _, e := range edges {
		if e.A >= 0 && e.A < len(m.Verts) {
			seen[e.A] = true
		}
		if e.B >= 0 && e.B < len(m.Verts) {
			seen[e.B] = true
		}
	}
	return sortedKeys(seen)
}

func sortedKeys(set map[int]bool) []int {
	out := make([]int, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// ---------- move / assign ----------

// MoveVerts offsets the given vertices by delta. Moving an edge or a face
// resolves to its vertex set first (VertsOfEdges / VertsOfFaces), so this is the
// single path every drag goes through.
func (m *ModelDoc) MoveVerts(verts []int, delta mgl32.Vec3) {
	if delta.Len() == 0 {
		return
	}
	for _, v := range verts {
		if v >= 0 && v < len(m.Verts) {
			m.Verts[v] = m.Verts[v].Add(delta)
		}
	}
	// Positions only — the topology is untouched, so the adjacency index stays
	// valid and only the GPU buffers need rebuilding.
	m.Touch()
}

// SetVertPos moves one vertex to an absolute position.
func (m *ModelDoc) SetVertPos(v int, p mgl32.Vec3) {
	if v < 0 || v >= len(m.Verts) {
		return
	}
	m.Verts[v] = p
	m.Touch()
}

// SetFaceSmooth toggles smooth shading on the given faces.
func (m *ModelDoc) SetFaceSmooth(faces []int, smooth bool) {
	for fi := range m.faceSet(faces) {
		m.Faces[fi].Smooth = smooth
	}
	m.Touch()
}

// ---------- extrude ----------

// ExtrudeFaces pushes the selected faces along their shared normal, walling in
// the gap with new side quads. It returns the indices of the moved cap faces —
// the same indices that went in, so the caller keeps its selection and pressing
// E then dragging continues the same extrude — and the indices of the side
// quads it created.
//
// The sides are returned because their UVs depend on how far the extrude
// travelled, and the interactive path does not know that yet: it extrudes by
// zero and lets a gizmo drag set the depth. The editor hands them back to
// RefreshExtrudeSideUVs as the drag proceeds.
//
// The selection is treated as one cap moving along one averaged normal, rather
// than each face moving along its own. For the usual case — a flat run of
// coplanar faces — that is the difference between the region lifting as a slab
// and it shearing apart at every internal edge.
//
// Only *boundary* edges of the selection get side quads. An edge shared by two
// selected faces is interior: both its faces move together, so there is no gap
// to wall in, and emitting a quad there would bury a zero-thickness face inside
// the solid.
//
// dist may be 0. That is the normal way to start an interactive extrude: the
// topology is created up front and the drag that follows moves the cap.
func (m *ModelDoc) ExtrudeFaces(faces []int, dist float32) (caps, sides []int) {
	sel := m.faceSet(faces)
	if len(sel) == 0 {
		return nil, nil
	}
	selList := sortedKeys(sel)
	m.EnsureWeights()

	// Average normal of the selection. Read before any mutation.
	var avg mgl32.Vec3
	for _, fi := range selList {
		avg = avg.Add(m.faceNormal(&m.Faces[fi]))
	}
	if avg.Len() < 1e-6 {
		// The selection has no coherent direction — opposing faces, or all
		// degenerate. There is no single cap to move, so refuse rather than
		// collapse the mesh into itself.
		return nil, nil
	}
	avg = avg.Normalize()

	// Boundary edges, with the winding they had in their source face. Counting
	// only within the selection is what distinguishes a boundary edge from an
	// interior one; the document-wide adjacency would also count unselected
	// neighbours and call every edge shared.
	type dirEdge struct {
		a, b int
		face int
	}
	edgeCount := map[EdgeKey]int{}
	var directed []dirEdge
	for _, fi := range selList {
		f := &m.Faces[fi]
		n := len(f.Verts)
		for i := 0; i < n; i++ {
			a, b := f.Verts[i], f.Verts[(i+1)%n]
			edgeCount[MakeEdgeKey(a, b)]++
			directed = append(directed, dirEdge{a, b, fi})
		}
	}

	// Duplicate every vertex the selection touches, offset along the normal.
	// A vertex shared with an unselected face keeps its original: the original
	// stays put with that face and the side quad bridges the two.
	offset := avg.Mul(dist)
	dup := make(map[int]int, len(selList)*4)
	for _, fi := range selList {
		for _, v := range m.Faces[fi].Verts {
			if _, ok := dup[v]; ok {
				continue
			}
			// The duplicate inherits the original's bone binding: an extruded
			// cap belongs to whatever the surface it grew out of belonged to,
			// and the side quads it walls in are spanned by both.
			dup[v] = m.addVertex(m.Verts[v].Add(offset), m.VertexWeights(v))
		}
	}

	// Wall in each boundary edge. [a, b, dup[b], dup[a]] preserves the source
	// face's winding, so the quad's normal points out of the solid — and it is
	// the corner order RefreshExtrudeSideUVs relies on to tell the edge from
	// the depth.
	for _, de := range directed {
		if edgeCount[MakeEdgeKey(de.a, de.b)] != 1 {
			continue // interior to the selection
		}
		src := &m.Faces[de.face]
		sides = append(sides, len(m.Faces))
		m.Faces = append(m.Faces, ModelFace{
			Verts:  []int{de.a, de.b, dup[de.b], dup[de.a]},
			UVs:    make([]mgl32.Vec2, 4),
			Smooth: src.Smooth,
		})
	}

	// Rewire the caps onto the duplicated vertices. Done last: the loop above
	// reads the original indices to build the sides.
	for _, fi := range selList {
		f := &m.Faces[fi]
		for i, v := range f.Verts {
			f.Verts[i] = dup[v]
		}
	}

	m.Invalidate()
	m.RefreshExtrudeSideUVs(sides)
	return selList, sides
}

// RefreshExtrudeSideUVs regenerates extrusion side quads' UVs from where their
// corners actually are: U along the shared edge, V across the depth. That is
// the same convention SurfaceMesh.RefreshSurfaceSideUVs uses for a level
// surface's extrusion sides, so a
// texture reads consistently between a wall and a model.
//
// It has to be re-derivable rather than computed once, because an interactive
// extrude creates the topology *before* the depth exists. Pressing E extrudes by
// zero and a gizmo drag sets the distance afterwards; UVs baked at creation
// would give every side a V range of zero — a texture stretched infinitely
// along V, and one that no amount of V scaling can rescue, since scaling zero
// leaves zero.
//
// Faces that are not four-cornered are skipped: after a subdivide these are no
// longer the quads this convention describes.
func (m *ModelDoc) RefreshExtrudeSideUVs(faces []int) {
	changed := false
	for fi := range m.faceSet(faces) {
		f := &m.Faces[fi]
		if len(f.Verts) != 4 {
			continue
		}
		a, b, c := m.Verts[f.Verts[0]], m.Verts[f.Verts[1]], m.Verts[f.Verts[2]]
		edgeLen := b.Sub(a).Len()
		// Measured corner to corner rather than along the extrude normal, so a
		// cap that was dragged sideways still gets the depth it really has.
		depth := c.Sub(b).Len()
		f.UVs = []mgl32.Vec2{
			{0, 0},
			{edgeLen, 0},
			{edgeLen, depth},
			{0, depth},
		}
		changed = true
	}
	if changed {
		m.Touch()
	}
}

// ---------- inset ----------

// InsetFaces shrinks each selected face within its own plane and walls the gap
// with a ring of quads, returning the shrunken faces (the same indices that
// went in, so a selection survives) and the ring faces it created.
//
// This is the operation that makes a frame: a window reveal, a recessed panel,
// a border around a hatch. Inset first, then extrude the inner face, and the
// ring becomes the sides of the recess.
//
// # Why it is not "pull the corners toward the centre"
//
// That is the cheap version, and it is what the level editor's convexity-safe
// inset does (editor_brush_mesh.go). It has two problems this one does not: the
// border comes out uneven on any face that is not roughly square — inset a 10x1
// strip by a fifth and the border is 1.0 along one axis and 0.1 along the other
// — and it moves the ORIGINAL corners, which drags every neighbouring face that
// shares them. A real inset leaves the outline exactly where it was and builds
// a new, smaller loop inside it.
//
// Each corner of the new loop is offset along its own angle bisector, by enough
// that both of its edges move inward by exactly dist. That is the mitre a
// picture frame has, and it is what makes the border uniform.
//
// dist is an absolute distance in model units rather than a fraction, for the
// same reason: a 5cm frame is 5cm wide wherever it is used. A face too small to
// take one is skipped rather than turned inside out — see insetCorners.
func (m *ModelDoc) InsetFaces(faces []int, dist float32) (inner, ring []int) {
	sel := m.faceSet(faces)
	if len(sel) == 0 || dist <= 0 {
		return nil, nil
	}
	m.EnsureWeights()

	// Ascending, so the result is deterministic and a caller carrying its own
	// per-face data across can walk the same order.
	for _, fi := range sortedKeys(sel) {
		n := len(m.Faces[fi].Verts)
		if n < 3 {
			continue
		}
		pts := make([]mgl32.Vec3, n)
		for i, v := range m.Faces[fi].Verts {
			pts[i] = m.Verts[v]
		}
		offsets, ok := insetCorners(pts, m.faceNormal(&m.Faces[fi]), dist)
		if !ok {
			continue // too small to take this border; left exactly as it was
		}

		// The new inner loop. Each vertex inherits the bone binding of the
		// corner it was offset from, so an inset face keeps following whatever
		// moved the surface it was cut into.
		uvCentre := faceUVCentroid(&m.Faces[fi])
		innerV := make([]int, n)
		innerUV := make([]mgl32.Vec2, n)
		for i := range pts {
			f := &m.Faces[fi]
			// UVs follow the positions by the same proportion, so what is on
			// the inner face is the middle of what was on the whole one.
			t := insetUVFraction(pts, offsets, i)
			innerUV[i] = f.UvAt(i).Add(uvCentre.Sub(f.UvAt(i)).Mul(t))
			innerV[i] = m.addVertex(pts[i].Add(offsets[i]), m.VertexWeights(f.Verts[i]))
		}

		// The ring, one quad per edge, wound the way its source face is so it
		// faces the same way out: [outer i, outer next, inner next, inner i].
		outerV := append([]int(nil), m.Faces[fi].Verts...)
		outerUV := append([]mgl32.Vec2(nil), m.Faces[fi].UVs...)
		smooth := m.Faces[fi].Smooth
		for i := 0; i < n; i++ {
			j := (i + 1) % n
			ring = append(ring, len(m.Faces))
			m.Faces = append(m.Faces, ModelFace{
				Verts:  []int{outerV[i], outerV[j], innerV[j], innerV[i]},
				UVs:    []mgl32.Vec2{uvAtOr(outerUV, i), uvAtOr(outerUV, j), innerUV[j], innerUV[i]},
				Smooth: smooth,
			})
		}

		// The source face becomes the inner one, keeping its index — so the
		// selection survives and inset, inset, extrude is three keypresses
		// rather than three keypresses and two clicks.
		m.Faces[fi].Verts = innerV
		m.Faces[fi].UVs = innerUV
		inner = append(inner, fi)
	}

	if len(inner) == 0 {
		return nil, nil
	}
	m.Invalidate()
	return inner, ring
}

// insetMinScale is how much of its own size the loop must keep. An inset that
// would leave less has nothing useful in the middle, and past the point where
// opposite borders meet it turns the loop inside out — so it is refused rather
// than clamped, which would silently do something other than what was asked.
const insetMinScale = 0.05

// insetCorners returns the offset to apply to each corner of a planar loop so
// that every edge moves inward by dist, and whether the result is a loop worth
// having.
//
// The offset at a corner runs along the bisector of its two edges, and its
// length is dist / sin(theta/2), where theta is the interior angle — the mitre
// that keeps the border a constant width right into the corner. A sharp corner
// therefore travels a long way, which is why the check at the end is on the
// resulting SIZE rather than on dist alone.
func insetCorners(pts []mgl32.Vec3, normal mgl32.Vec3, dist float32) ([]mgl32.Vec3, bool) {
	n := len(pts)
	if n < 3 || normal.Len() < 1e-6 {
		return nil, false
	}
	normal = normal.Normalize()

	var centre mgl32.Vec3
	for _, p := range pts {
		centre = centre.Add(p)
	}
	centre = centre.Mul(1 / float32(n))

	out := make([]mgl32.Vec3, n)
	for i := range pts {
		a := pts[(i-1+n)%n].Sub(pts[i])
		b := pts[(i+1)%n].Sub(pts[i])
		if a.Len() < 1e-9 || b.Len() < 1e-9 {
			return nil, false
		}
		a, b = a.Normalize(), b.Normalize()

		bis := a.Add(b)
		if bis.Len() < 1e-6 {
			// The two edges are opposite: a point on a straight run rather than
			// a corner — which is what a subdivided face's midpoints are. Step
			// straight inward, perpendicular to the run.
			bis = normal.Cross(b)
			if bis.Dot(centre.Sub(pts[i])) < 0 {
				bis = bis.Mul(-1)
			}
			out[i] = bis.Normalize().Mul(dist)
			continue
		}
		bis = bis.Normalize()
		// Aim it at the inside of the face, whichever way the winding runs.
		if bis.Dot(centre.Sub(pts[i])) < 0 {
			bis = bis.Mul(-1)
		}
		// The sine of half the interior angle, read off the bisector against
		// one of its edges.
		sinHalf := bis.Cross(b).Len()
		if sinHalf < 1e-4 {
			return nil, false // a spike; the mitre would run away to infinity
		}
		out[i] = bis.Mul(dist / sinHalf)
	}

	// Refuse an inset that would collapse or invert the loop.
	//
	// The test is on the loop's own signed AREA rather than on how far each
	// corner travelled, because the way this goes wrong is not that a corner
	// moves too far — it is that opposite borders pass through each other and
	// the loop comes out inside out. A 4x0.5 face inset by 1.0 does exactly
	// that: every corner is still a respectable distance from the centre and
	// still on the same side of it diagonally, and the rectangle it forms is
	// wound the other way round. Area sees it; a per-corner test does not.
	inner := make([]mgl32.Vec3, n)
	for i := range pts {
		inner[i] = pts[i].Add(out[i])
	}
	before := signedArea(pts, normal)
	after := signedArea(inner, normal)
	if before <= 0 || after <= before*insetMinScale {
		return nil, false
	}
	return out, true
}

// signedArea is a planar loop's area measured against n, negative when the loop
// is wound the other way round.
func signedArea(pts []mgl32.Vec3, n mgl32.Vec3) float32 {
	var acc mgl32.Vec3
	for i := range pts {
		acc = acc.Add(pts[i].Cross(pts[(i+1)%len(pts)]))
	}
	return acc.Mul(0.5).Dot(n)
}

// insetUVFraction is how far corner i travelled toward the centre as a fraction
// of how far it could have — the number the UV interpolation needs so the
// texture shrinks with the geometry.
func insetUVFraction(pts []mgl32.Vec3, offsets []mgl32.Vec3, i int) float32 {
	var centre mgl32.Vec3
	for _, p := range pts {
		centre = centre.Add(p)
	}
	centre = centre.Mul(1 / float32(len(pts)))
	toCentre := centre.Sub(pts[i]).Len()
	if toCentre < 1e-9 {
		return 0
	}
	if f := offsets[i].Len() / toCentre; f < 1 {
		return f
	}
	return 1
}

// uvAtOr reads a UV that may not be there, for a face whose UV list is short —
// which a hand-written .fmdl or a UV-less OBJ import can produce.
func uvAtOr(uvs []mgl32.Vec2, i int) mgl32.Vec2 {
	if i < 0 || i >= len(uvs) {
		return mgl32.Vec2{}
	}
	return uvs[i]
}

// ---------- midpoint subdivide ----------

// SubdivideFaces splits each selected face at its edge midpoints: a triangle
// becomes four triangles, a quad four quads, an n-gon n quads. Vertex positions
// are plain averages, so the surface does not move — only its density changes.
// Returns the indices of the resulting faces.
//
// Neighbouring faces that were *not* selected also get the new midpoints
// inserted into their vertex loops. Without that repair the mesh cracks: the
// neighbour would still span the un-split edge while the subdivided side now has
// a vertex in the middle of it, and that T-junction shows as a seam that moves
// as soon as anything is dragged.
//
// Those repairs are why the split works on a face's *corners* rather than its
// raw vertex loop. A cube face whose neighbour was already subdivided carries a
// T-junction point mid-edge, so its loop has five entries even though it is
// still visually a quad; fanning that loop from a centre vertex would cut it
// into five wedges with diagonal edges instead of the four quads the user
// asked for. See faceCornerSpans.
func (m *ModelDoc) SubdivideFaces(faces []int) []int {
	sel := m.faceSet(faces)
	if len(sel) == 0 {
		return nil
	}

	// Pass 1: create every midpoint first, so a neighbour repaired in pass 2
	// finds the midpoint of an edge whose own face is processed later. A corner
	// edge that already carries T-junction points is skipped: pass 2 splits it
	// at one of those instead of inventing a vertex on top of them.
	m.EnsureWeights()
	edgeMid := map[EdgeKey]int{}
	for fi := range sel {
		f := &m.Faces[fi]
		spans := m.faceCornerSpans(f)
		n := len(spans)
		for i, s := range spans {
			if len(s.inner) > 0 {
				continue
			}
			a := f.Verts[s.corner]
			b := f.Verts[spans[(i+1)%n].corner]
			e := MakeEdgeKey(a, b)
			if _, ok := edgeMid[e]; ok {
				continue
			}
			edgeMid[e] = m.addVertex(m.Verts[a].Add(m.Verts[b]).Mul(0.5),
				blendWeights(m.WeightsFor(a), m.WeightsFor(b)))
		}
	}

	// Pass 2: rebuild the face list in place, so unselected faces keep their
	// relative order and the caller's other indices stay meaningful.
	out := make([]ModelFace, 0, len(m.Faces)+len(sel)*4)
	var newSel []int
	for fi := range m.Faces {
		if sel[fi] {
			for _, child := range m.splitFace(fi, edgeMid) {
				newSel = append(newSel, len(out))
				out = append(out, child)
			}
			continue
		}
		f := m.Faces[fi].Clone()
		insertMidpoints(&f, edgeMid)
		out = append(out, f)
	}
	m.Faces = out

	m.Invalidate()
	return newSel
}

// cornerSpan is one side of a face: the corner it starts at, plus any loop
// vertices lying on the straight edge running from it to the next corner.
// Indices are face-local (into f.Verts), so they address UVs too.
type cornerSpan struct {
	corner int
	inner  []int
}

// collinearSinEps is the sine of the largest angle a loop vertex may bend
// through and still count as sitting *on* an edge rather than turning it. A
// midpoint is exactly collinear up to float32 rounding, so this only has to
// absorb that; anything a user has actually dragged off the line becomes a real
// corner, which is the behaviour you want when you moved it on purpose.
const collinearSinEps = 1e-4

// faceCornerSpans describes a face as its real corners and the T-junction
// points sitting between them. A face that has no such points — the common
// case — comes back as one span per vertex with no inner entries, so callers
// need no special case for it. The same fallback covers a degenerate loop that
// bends at fewer than three places, which has no meaningful corners at all.
func (m *ModelDoc) faceCornerSpans(f *ModelFace) []cornerSpan {
	n := len(f.Verts)
	if n < 3 {
		return wholeLoopSpans(n)
	}
	isCorner := make([]bool, n)
	corners := 0
	for i := 0; i < n; i++ {
		p := m.Verts[f.Verts[(i-1+n)%n]]
		v := m.Verts[f.Verts[i]]
		q := m.Verts[f.Verts[(i+1)%n]]
		if !onStraightEdge(p, v, q) {
			isCorner[i] = true
			corners++
		}
	}
	if corners < 3 {
		return wholeLoopSpans(n)
	}
	spans := make([]cornerSpan, 0, corners)
	for i := 0; i < n; i++ {
		if !isCorner[i] {
			continue
		}
		s := cornerSpan{corner: i}
		// Terminates: corners >= 3, so some later index is a corner.
		for j := (i + 1) % n; !isCorner[j]; j = (j + 1) % n {
			s.inner = append(s.inner, j)
		}
		spans = append(spans, s)
	}
	return spans
}

// wholeLoopSpans treats every vertex of an n-vertex loop as a corner.
func wholeLoopSpans(n int) []cornerSpan {
	spans := make([]cornerSpan, n)
	for i := range spans {
		spans[i].corner = i
	}
	return spans
}

// onStraightEdge reports whether v lies on the straight run from p to q — the
// signature of a T-junction point inserted by an earlier subdivide.
func onStraightEdge(p, v, q mgl32.Vec3) bool {
	a, b := v.Sub(p), q.Sub(v)
	la, lb := a.Len(), b.Len()
	if la == 0 || lb == 0 {
		return true // a doubled-up vertex never turns the edge
	}
	if a.Dot(b) <= 0 {
		return false // doubles back: a corner, however sharp
	}
	return a.Cross(b).Len() <= collinearSinEps*la*lb
}

// splitFace returns the children of one subdivided face: one quad per corner
// around a centre vertex (a triangle instead splits 1-to-4 without a centre).
// New midpoints come from edgeMid, which the caller has already populated for
// every corner edge that did not already carry a T-junction point.
func (m *ModelDoc) splitFace(fi int, edgeMid map[EdgeKey]int) []ModelFace {
	f := &m.Faces[fi]
	if len(f.Verts) < 3 {
		return []ModelFace{f.Clone()}
	}
	spans := m.faceCornerSpans(f)
	n := len(spans)

	// Where each corner edge splits, and which of its existing T-junction
	// points end up on either side of that split — those stay in the child's
	// loop, or subdividing here would crack the neighbour that put them there.
	midV := make([]int, n)
	midUV := make([]mgl32.Vec2, n)
	head := make([][]int, n) // between the corner and the split point
	tail := make([][]int, n) // between the split point and the next corner
	for i, s := range spans {
		next := spans[(i+1)%n]
		if len(s.inner) == 0 {
			midV[i] = edgeMid[MakeEdgeKey(f.Verts[s.corner], f.Verts[next.corner])]
			midUV[i] = f.UvAt(s.corner).Add(f.UvAt(next.corner)).Mul(0.5)
			continue
		}
		k := m.nearestOnEdgeToMidpoint(f, s.inner, f.Verts[s.corner], f.Verts[next.corner])
		midV[i] = f.Verts[s.inner[k]]
		midUV[i] = f.UvAt(s.inner[k])
		head[i], tail[i] = s.inner[:k], s.inner[k+1:]
	}

	child := func(verts []int, uvs []mgl32.Vec2) ModelFace {
		return ModelFace{Verts: verts, UVs: uvs, Smooth: f.Smooth}
	}
	// walk appends the face's own vertices at the given loop indices, so a
	// child keeps the T-junction points that lie along its share of an edge.
	walk := func(verts []int, uvs []mgl32.Vec2, idxs []int) ([]int, []mgl32.Vec2) {
		for _, li := range idxs {
			verts = append(verts, f.Verts[li])
			uvs = append(uvs, f.UvAt(li))
		}
		return verts, uvs
	}

	if n == 3 {
		// A triangle splits 1-to-4 without a centre vertex: three corner
		// triangles plus the middle one.
		out := make([]ModelFace, 0, 4)
		for i := 0; i < 3; i++ {
			prev := (i + 2) % 3
			verts := []int{f.Verts[spans[i].corner]}
			uvs := []mgl32.Vec2{f.UvAt(spans[i].corner)}
			verts, uvs = walk(verts, uvs, head[i])
			verts = append(verts, midV[i], midV[prev])
			uvs = append(uvs, midUV[i], midUV[prev])
			verts, uvs = walk(verts, uvs, tail[prev])
			out = append(out, child(verts, uvs))
		}
		return append(out, child(
			[]int{midV[0], midV[1], midV[2]},
			[]mgl32.Vec2{midUV[0], midUV[1], midUV[2]},
		))
	}

	// Quads and n-gons take a centre vertex and fan into n quads, one per
	// corner. For n == 4 this is the familiar 1-to-4 quad split. The centre is
	// averaged over the corners alone: letting T-junction points vote would
	// drag it towards whichever edge happens to be the most subdivided.
	var centrePos mgl32.Vec3
	var centreUV mgl32.Vec2
	corners := make([][]ModelVertexWeight, 0, n)
	for _, s := range spans {
		centrePos = centrePos.Add(m.Verts[f.Verts[s.corner]])
		centreUV = centreUV.Add(f.UvAt(s.corner))
		corners = append(corners, m.WeightsFor(f.Verts[s.corner]))
	}
	centre := m.addVertex(centrePos.Mul(1/float32(n)), blendWeights(corners...))
	centreUV = centreUV.Mul(1 / float32(n))

	out := make([]ModelFace, 0, n)
	for i := 0; i < n; i++ {
		prev := (i - 1 + n) % n
		verts := []int{f.Verts[spans[i].corner]}
		uvs := []mgl32.Vec2{f.UvAt(spans[i].corner)}
		verts, uvs = walk(verts, uvs, head[i])
		verts = append(verts, midV[i], centre, midV[prev])
		uvs = append(uvs, midUV[i], centreUV, midUV[prev])
		verts, uvs = walk(verts, uvs, tail[prev])
		out = append(out, child(verts, uvs))
	}
	return out
}

// nearestOnEdgeToMidpoint picks which of an edge's existing T-junction points
// to split at: the one closest to the halfway mark. Reusing a point rather than
// inserting a new one is what keeps the neighbour that owns it watertight —
// a fresh vertex at the same spot would be a duplicate the neighbour never
// hears about.
func (m *ModelDoc) nearestOnEdgeToMidpoint(f *ModelFace, inner []int, a, b int) int {
	mid := m.Verts[a].Add(m.Verts[b]).Mul(0.5)
	best, bestD := 0, float32(math.MaxFloat32)
	for i, li := range inner {
		if d := m.Verts[f.Verts[li]].Sub(mid).LenSqr(); d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// insertMidpoints rewrites an unselected face's loop to include any midpoints
// created on its edges, keeping the mesh watertight. UVs interpolate linearly,
// which is exact because the midpoint is exactly halfway along the edge.
func insertMidpoints(f *ModelFace, edgeMid map[EdgeKey]int) {
	n := len(f.Verts)
	if n < 3 || len(edgeMid) == 0 {
		return
	}
	verts := make([]int, 0, n*2)
	uvs := make([]mgl32.Vec2, 0, n*2)
	changed := false
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		verts = append(verts, f.Verts[i])
		uvs = append(uvs, f.UvAt(i))
		if mid, ok := edgeMid[MakeEdgeKey(f.Verts[i], f.Verts[j])]; ok {
			verts = append(verts, mid)
			uvs = append(uvs, f.UvAt(i).Add(f.UvAt(j)).Mul(0.5))
			changed = true
		}
	}
	if !changed {
		return
	}
	f.Verts = verts
	f.UVs = uvs
}

// ---------- Catmull-Clark ----------

// CatmullClark applies one level of Catmull-Clark subdivision to the whole mesh,
// turning every n-gon into n quads and pulling the surface towards the limit
// surface — so unlike SubdivideFaces, the geometry *does* move and the model
// gets rounder each time.
//
// Boundary edges and boundary vertices use the standard crease rules, so an open
// mesh (a plane, a half-open shell) keeps its silhouette instead of shrinking
// away from its own border.
func (m *ModelDoc) CatmullClark() {
	if len(m.Faces) == 0 {
		return
	}
	m.EnsureWeights()
	adj := m.adjacency()
	nVerts := len(m.Verts)
	nEdges := len(adj.edges)

	edgeIdx := make(map[EdgeKey]int, nEdges)
	for i, e := range adj.edges {
		edgeIdx[e] = i
	}

	// Face points: the centroid of each face.
	facePt := make([]mgl32.Vec3, len(m.Faces))
	for fi := range m.Faces {
		facePt[fi] = m.faceCentroid(&m.Faces[fi])
	}

	// Edge points: the average of the edge's endpoints and its two adjacent
	// face points. A boundary edge has only one face, so it falls back to the
	// plain midpoint — the crease rule.
	edgePt := make([]mgl32.Vec3, nEdges)
	isBoundaryEdge := make([]bool, nEdges)
	for i, e := range adj.edges {
		mids := m.Verts[e.A].Add(m.Verts[e.B]).Mul(0.5)
		fs := adj.edgeFaces[e]
		if len(fs) < 2 {
			edgePt[i] = mids
			isBoundaryEdge[i] = true
			continue
		}
		edgePt[i] = m.Verts[e.A].Add(m.Verts[e.B]).
			Add(facePt[fs[0]]).Add(facePt[fs[1]]).Mul(0.25)
	}

	// Vertex points. Interior: (F + 2R + (n-3)P) / n, where F averages the
	// touching face points, R averages the touching edge midpoints and n is the
	// valence. Boundary: 3/4 P + 1/8 of each neighbouring boundary midpoint.
	vertBoundaryMids := make([][]mgl32.Vec3, nVerts)
	vertEdgeMids := make([][]mgl32.Vec3, nVerts)
	for i, e := range adj.edges {
		mid := m.Verts[e.A].Add(m.Verts[e.B]).Mul(0.5)
		vertEdgeMids[e.A] = append(vertEdgeMids[e.A], mid)
		vertEdgeMids[e.B] = append(vertEdgeMids[e.B], mid)
		if isBoundaryEdge[i] {
			vertBoundaryMids[e.A] = append(vertBoundaryMids[e.A], mid)
			vertBoundaryMids[e.B] = append(vertBoundaryMids[e.B], mid)
		}
	}

	vertPt := make([]mgl32.Vec3, nVerts)
	for v := 0; v < nVerts; v++ {
		p := m.Verts[v]
		if bm := vertBoundaryMids[v]; len(bm) > 0 {
			acc := p.Mul(0.75)
			w := float32(0.25) / float32(len(bm))
			for _, mid := range bm {
				acc = acc.Add(mid.Mul(w))
			}
			vertPt[v] = acc
			continue
		}
		fs := adj.vertFaces[v]
		em := vertEdgeMids[v]
		n := float32(len(fs))
		if len(fs) == 0 || len(em) == 0 {
			vertPt[v] = p // isolated vertex; nothing to average against
			continue
		}
		var favg mgl32.Vec3
		for _, fi := range fs {
			favg = favg.Add(facePt[fi])
		}
		favg = favg.Mul(1 / n)
		var ravg mgl32.Vec3
		for _, mid := range em {
			ravg = ravg.Add(mid)
		}
		ravg = ravg.Mul(1 / float32(len(em)))
		vertPt[v] = favg.Add(ravg.Mul(2)).Add(p.Mul(n - 3)).Mul(1 / n)
	}

	// New vertex pool, laid out so the index of each kind is computable:
	// [vertex points][edge points][face points].
	verts := make([]mgl32.Vec3, 0, nVerts+nEdges+len(m.Faces))
	verts = append(verts, vertPt...)
	verts = append(verts, edgePt...)
	verts = append(verts, facePt...)
	edgeBase := nVerts
	faceBase := nVerts + nEdges

	// Bone bindings follow the same three blocks: an original vertex keeps its
	// own, an edge point blends its two endpoints', a face point blends the
	// whole loop's. Without this a subdivided model would come back unbound and
	// stop following the skeleton.
	weights := make([][]ModelVertexWeight, 0, len(verts))
	for v := 0; v < nVerts; v++ {
		weights = append(weights, m.VertexWeights(v))
	}
	for _, e := range adj.edges {
		weights = append(weights, blendWeights(m.WeightsFor(e.A), m.WeightsFor(e.B)))
	}
	for fi := range m.Faces {
		loop := make([][]ModelVertexWeight, 0, len(m.Faces[fi].Verts))
		for _, v := range m.Faces[fi].Verts {
			loop = append(loop, m.WeightsFor(v))
		}
		weights = append(weights, blendWeights(loop...))
	}

	// Each face fans into one quad per corner. UVs subdivide by the same
	// weights in UV space and stay per-face, so texture seams the user set
	// deliberately survive subdivision.
	out := make([]ModelFace, 0, nEdges*2)
	for fi := range m.Faces {
		f := &m.Faces[fi]
		n := len(f.Verts)
		if n < 3 {
			continue
		}
		faceUV := faceUVCentroid(f)
		for i := 0; i < n; i++ {
			prev := (i - 1 + n) % n
			next := (i + 1) % n
			eNext := edgeBase + edgeIdx[MakeEdgeKey(f.Verts[i], f.Verts[next])]
			ePrev := edgeBase + edgeIdx[MakeEdgeKey(f.Verts[prev], f.Verts[i])]
			out = append(out, ModelFace{
				Verts: []int{f.Verts[i], eNext, faceBase + fi, ePrev},
				UVs: []mgl32.Vec2{
					f.UvAt(i),
					f.UvAt(i).Add(f.UvAt(next)).Mul(0.5),
					faceUV,
					f.UvAt(prev).Add(f.UvAt(i)).Mul(0.5),
				},
				Smooth: f.Smooth,
			})
		}
	}

	m.Verts = verts
	m.Faces = out
	m.Weights = weights
	m.Invalidate()
}

// ---------- delete ----------

// DeleteFaces removes the given faces and drops any vertex left unreferenced,
// remapping the survivors. Returning the mesh to a compact vertex pool matters
// because selection indices are positional — a hole in the array would make
// every stored index ambiguous.
func (m *ModelDoc) DeleteFaces(faces []int) {
	sel := m.faceSet(faces)
	if len(sel) == 0 {
		return
	}
	kept := make([]ModelFace, 0, len(m.Faces)-len(sel))
	for fi := range m.Faces {
		if !sel[fi] {
			kept = append(kept, m.Faces[fi])
		}
	}

	used := map[int]bool{}
	for i := range kept {
		for _, v := range kept[i].Verts {
			used[v] = true
		}
	}
	m.EnsureWeights()
	remap := make(map[int]int, len(used))
	verts := make([]mgl32.Vec3, 0, len(used))
	weights := make([][]ModelVertexWeight, 0, len(used))
	for _, v := range sortedKeys(used) {
		remap[v] = len(verts)
		verts = append(verts, m.Verts[v])
		weights = append(weights, m.Weights[v])
	}
	for i := range kept {
		for j, v := range kept[i].Verts {
			kept[i].Verts[j] = remap[v]
		}
	}

	m.Verts = verts
	m.Faces = kept
	m.Weights = weights
	m.Invalidate()
}
