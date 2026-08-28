package world

// surface_mesh_ops.go — the edits a surface mesh supports.
//
// These are ModelDoc's mesh operations (model_ops.go) restated for a parametric
// surface, and deliberately kept recognisable: the level editor's surface mode
// and the model editor are meant to feel like the same tool, and an extrude
// that walls in different edges in the two would be the first place that broke
// down.
//
// What differs is only what a point *is*. A ModelDoc vertex is a free position
// and an extrude moves it along a normal in space; a SurfacePoint is (U, V) on
// the surface plus a Depth, and an extrude moves the Depth. Everything
// topological — which edges are boundary, which get walled in, how a subdivide
// repairs its neighbours — is identical, because topology does not care what
// coordinate system it is drawn in.
//
// Nothing here touches boundary points' U and V. A duplicate made by an extrude
// is always free (SurfaceFree): it has left the outline, and the original stays
// behind pinned to the sector, with the new side face spanning the two. That is
// what makes "extrude the whole floor" produce a sunken room whose walls run up
// to the sector outline, rather than a floor that has quietly detached from the
// sector it belongs to.

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// surfaceEdge is an undirected edge, ordered so the two directions hash alike.
type surfaceEdge struct{ A, B int }

func makeSurfaceEdge(a, b int) surfaceEdge {
	if a > b {
		a, b = b, a
	}
	return surfaceEdge{a, b}
}

// surfaceFaceSet normalises a face-index list into a set, dropping duplicates
// and anything out of range, so every op below can trust what it iterates.
func (m *SurfaceMesh) surfaceFaceSet(faces []int) map[int]bool {
	set := make(map[int]bool, len(faces))
	for _, fi := range faces {
		if fi >= 0 && fi < len(m.Faces) {
			set[fi] = true
		}
	}
	return set
}

func sortedFaceKeys(set map[int]bool) []int {
	out := make([]int, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// dupPoint copies a point as a free one at a new depth.
func (m *SurfaceMesh) dupPoint(src int, depth float32) int {
	p := m.Points[src]
	m.Points = append(m.Points, SurfacePoint{U: p.U, V: p.V, Depth: depth, Boundary: SurfaceFree})
	return len(m.Points) - 1
}

// SurfaceExtrudeFaces pushes the selected faces to depth+delta, walling in the
// gap with new side quads. It returns the cap faces — the same indices that
// went in, so the caller keeps its selection and pressing E again continues the
// same extrude — and the indices of the sides it created.
//
// Only *boundary* edges of the selection get sides. An edge shared by two
// selected faces is interior: both move together, so there is no gap to wall
// in, and a quad there would bury a zero-area face inside the surface.
//
// delta may be 0, and that is the normal way to start an interactive extrude:
// the topology is created up front and the drag that follows sets the depth.
// The sides' UVs depend on how far it travelled, which is not known yet, so
// they are left to RefreshSurfaceSideUVs as the drag proceeds.
func (m *SurfaceMesh) SurfaceExtrudeFaces(faces []int, delta float32) (caps, sides []int) {
	sel := m.surfaceFaceSet(faces)
	if len(sel) == 0 {
		return nil, nil
	}
	selList := sortedFaceKeys(sel)

	type dirEdge struct{ a, b, face int }
	edgeCount := map[surfaceEdge]int{}
	var directed []dirEdge
	for _, fi := range selList {
		f := &m.Faces[fi]
		n := len(f.Verts)
		for i := 0; i < n; i++ {
			a, b := f.Verts[i], f.Verts[(i+1)%n]
			edgeCount[makeSurfaceEdge(a, b)]++
			directed = append(directed, dirEdge{a, b, fi})
		}
	}

	// Duplicate every point the selection touches. A point shared with an
	// unselected face keeps its original: the original stays put with that
	// face and the side quad bridges the two.
	dup := make(map[int]int, len(selList)*4)
	for _, fi := range selList {
		for _, v := range m.Faces[fi].Verts {
			if _, ok := dup[v]; ok {
				continue
			}
			if v < 0 || v >= len(m.Points) {
				return nil, nil
			}
			dup[v] = m.dupPoint(v, m.Points[v].Depth+delta)
		}
	}

	// [a, b, dup[b], dup[a]] preserves the source face's winding, so the quad
	// faces out of the solid — and it is the corner order
	// RefreshSurfaceSideUVs relies on to tell the edge from the depth.
	for _, de := range directed {
		if edgeCount[makeSurfaceEdge(de.a, de.b)] != 1 {
			continue // interior to the selection
		}
		src := &m.Faces[de.face]
		sides = append(sides, len(m.Faces))
		m.Faces = append(m.Faces, SurfaceFace{
			Verts:  []int{de.a, de.b, dup[de.b], dup[de.a]},
			UVs:    make([]mgl32.Vec2, 4),
			Alpha:  src.Alpha,
			Smooth: src.Smooth,
		})
	}

	// Rewire the caps onto the duplicates. Done last: the loop above reads the
	// original indices to build the sides.
	for _, fi := range selList {
		f := &m.Faces[fi]
		for i, v := range f.Verts {
			f.Verts[i] = dup[v]
		}
		for hi := range f.Holes {
			for i, v := range f.Holes[hi] {
				if d, ok := dup[v]; ok {
					f.Holes[hi][i] = d
				}
			}
		}
	}

	m.RefreshSurfaceSideUVs(sides)
	return selList, sides
}

// SurfaceInsetFaces shrinks each selected face towards its own centroid by t
// (0 = unchanged, 1 = collapsed to a point), leaving a ring of new faces
// between the original outline and the inset one, and returns the inset faces.
//
// It is the other half of cutting a doorway. Extrude alone can only push a
// whole face, so a hole in a wall needs a smaller face to push first; inset is
// how you get one without hand-placing four points and connecting them.
func (m *SurfaceMesh) SurfaceInsetFaces(faces []int, t float32) []int {
	if t <= 0 || t >= 1 {
		return nil
	}
	sel := m.surfaceFaceSet(faces)
	if len(sel) == 0 {
		return nil
	}
	var out []int
	for _, fi := range sortedFaceKeys(sel) {
		f := m.Faces[fi]
		n := len(f.Verts)
		if n < 3 {
			continue
		}
		var cu, cv, cd float32
		ok := true
		for _, vi := range f.Verts {
			if vi < 0 || vi >= len(m.Points) {
				ok = false
				break
			}
			p := m.Points[vi]
			cu, cv, cd = cu+p.U, cv+p.V, cd+p.Depth
		}
		if !ok {
			continue
		}
		inv := 1 / float32(n)
		cu, cv, cd = cu*inv, cv*inv, cd*inv

		inner := make([]int, n)
		for i, vi := range f.Verts {
			p := m.Points[vi]
			inner[i] = m.AddPoint(p.U+(cu-p.U)*t, p.V+(cv-p.V)*t, p.Depth+(cd-p.Depth)*t)
		}
		// The ring between the two outlines, one quad per original edge. Wound
		// to match the face it came out of.
		outer := append([]int(nil), f.Verts...)
		for i := 0; i < n; i++ {
			j := (i + 1) % n
			m.Faces = append(m.Faces, SurfaceFace{
				Verts:   []int{outer[i], outer[j], inner[j], inner[i]},
				Texture: f.Texture,
				Alpha:   f.Alpha,
				Smooth:  f.Smooth,
			})
		}
		// The original face becomes the inset one, so a caller's selection
		// still names the face it asked to inset and E can follow straight on.
		// Re-read through the slice: the appends above may have moved it.
		m.Faces[fi].Verts = inner
		m.Faces[fi].UVs = nil
		out = append(out, fi)
	}
	return out
}

// SurfaceSplitFace cuts face fi in two along the line joining two of its own
// corners — "place two points and connect them", once the points are corners.
//
// Returns the index of the new face, or -1 when the cut is not a diagonal: the
// two corners must belong to the face and must not be adjacent, or the result
// would be one face and a degenerate sliver.
func (m *SurfaceMesh) SurfaceSplitFace(fi, a, b int) int {
	if fi < 0 || fi >= len(m.Faces) {
		return -1
	}
	f := &m.Faces[fi]
	n := len(f.Verts)
	if n < 4 {
		return -1
	}
	ia, ib := -1, -1
	for i, v := range f.Verts {
		if v == a && ia < 0 {
			ia = i
		}
		if v == b && ib < 0 {
			ib = i
		}
	}
	if ia < 0 || ib < 0 {
		return -1
	}
	if ia > ib {
		ia, ib = ib, ia
	}
	// Adjacent corners already share an edge; there is nothing to cut.
	if ib-ia < 2 || (ia == 0 && ib == n-1) {
		return -1
	}

	// A face carrying holes cannot be split blind: the cut might pass straight
	// through one, and deciding which side each hole lands on is a containment
	// problem this does not attempt. The editor offers the split only on faces
	// without them.
	if len(f.Holes) > 0 {
		return -1
	}

	first := make([]int, 0, ib-ia+1)
	firstUV := make([]mgl32.Vec2, 0, ib-ia+1)
	for i := ia; i <= ib; i++ {
		first = append(first, f.Verts[i])
		firstUV = append(firstUV, surfaceFaceUVAt(f, i))
	}
	second := make([]int, 0, n-(ib-ia)+1)
	secondUV := make([]mgl32.Vec2, 0, n-(ib-ia)+1)
	for i := ib; i != ia; i = (i + 1) % n {
		second = append(second, f.Verts[i])
		secondUV = append(secondUV, surfaceFaceUVAt(f, i))
	}
	second = append(second, f.Verts[ia])
	secondUV = append(secondUV, surfaceFaceUVAt(f, ia))

	newIdx := len(m.Faces)
	m.Faces = append(m.Faces, SurfaceFace{
		Verts: second, Texture: f.Texture, Alpha: f.Alpha,
		Hole: f.Hole, Smooth: f.Smooth,
	})
	if len(f.UVs) == n {
		m.Faces[newIdx].UVs = secondUV
	}
	// Re-read: the append above may have moved the backing array.
	f = &m.Faces[fi]
	hadUV := len(f.UVs) == n
	f.Verts = first
	if hadUV {
		f.UVs = firstUV
	} else {
		f.UVs = nil
	}
	return newIdx
}

// surfaceFaceUVAt is the face's UV at corner i, or the zero value when the face
// derives its UVs from the frame.
func surfaceFaceUVAt(f *SurfaceFace, i int) mgl32.Vec2 {
	if i >= 0 && i < len(f.UVs) {
		return f.UVs[i]
	}
	return mgl32.Vec2{}
}

// SurfaceAddFacePoint inserts a point inside face fi and fans the face out to
// it, returning the new point's index. This is "place a point on a face": the
// face becomes n triangles around it, every one of them selectable and
// extrudable.
//
// Returns -1 for a face carrying holes, where a fan from an interior point
// would cross them.
func (m *SurfaceMesh) SurfaceAddFacePoint(fi int, u, v float32) int {
	if fi < 0 || fi >= len(m.Faces) {
		return -1
	}
	f := &m.Faces[fi]
	n := len(f.Verts)
	if n < 3 || len(f.Holes) > 0 {
		return -1
	}
	ring := make([]mgl32.Vec2, n)
	depths := make([]float32, n)
	uvs := make([]mgl32.Vec2, n)
	for i, vi := range f.Verts {
		if vi < 0 || vi >= len(m.Points) {
			return -1
		}
		p := m.Points[vi]
		ring[i] = p.Vec2()
		depths[i] = p.Depth
		uvs[i] = surfaceFaceUVAt(f, i)
	}
	// The new point sits *on* the surface it was placed on, so its depth is
	// what the face already has there — dropping it to the base plane would
	// punch a dimple into a sloped face just by clicking on it.
	depth, _ := surfaceFanSample(ring, depths, uvs, mgl32.Vec2{u, v})
	np := m.AddPoint(u, v, depth)

	tmpl := *f
	verts := append([]int(nil), f.Verts...)
	hadUV := len(f.UVs) == n

	for i := 0; i < n; i++ {
		j := (i + 1) % n
		nf := SurfaceFace{
			Verts:   []int{verts[i], verts[j], np},
			Texture: tmpl.Texture, Alpha: tmpl.Alpha,
			Hole: tmpl.Hole, Smooth: tmpl.Smooth,
		}
		if hadUV {
			mid := tmpl.UVs[i].Add(tmpl.UVs[j]).Mul(0.5)
			nf.UVs = []mgl32.Vec2{tmpl.UVs[i], tmpl.UVs[j], mid}
		}
		if i == 0 {
			m.Faces[fi] = nf
			continue
		}
		m.Faces = append(m.Faces, nf)
	}
	return np
}

// SurfaceSubdivideFaces splits each selected face at its edge midpoints: a
// triangle becomes three quads, a quad four, an n-gon n. Point positions are
// plain averages, so the surface does not move — only its density does, which
// is what you want before shaping it.
//
// Neighbouring faces that were *not* selected get the new midpoints inserted
// into their rings too. Without that repair the mesh cracks: the neighbour
// would still span the un-split edge while the subdivided side now has a point
// in the middle of it, and that T-junction shows as a seam the moment anything
// is dragged.
//
// Those repairs are why the split works on a face's *corners* rather than on
// its raw point ring. Subdivide one face of a wall and its neighbours each grow
// a T-junction point mid-edge: they are still visually quads, but their rings
// have five or six entries, and fanning the raw ring from a centre point cut
// them into five or six wedges instead of the four quads the author asked for.
// See surfaceCornerSpans, and ModelDoc.SubdivideFaces, which solves the same
// problem the same way for the model editor.
func (m *SurfaceMesh) SurfaceSubdivideFaces(faces []int) []int {
	sel := m.surfaceFaceSet(faces)
	if len(sel) == 0 {
		return nil
	}
	selList := sortedFaceKeys(sel)

	// Pass 1: every midpoint first, so a neighbour repaired in pass 3 finds the
	// midpoint of an edge whose own face is processed later. A corner edge that
	// already carries T-junction points is skipped: pass 2 splits it at one of
	// those instead of inventing a point on top of them, which is what keeps
	// the neighbour that put them there watertight.
	mid := map[surfaceEdge]int{}
	for _, fi := range selList {
		f := &m.Faces[fi]
		if !m.subdividableSurfaceFace(f) {
			continue
		}
		spans := m.surfaceCornerSpans(f)
		n := len(spans)
		for i, sp := range spans {
			if len(sp.inner) > 0 {
				continue
			}
			a, b := f.Verts[sp.corner], f.Verts[spans[(i+1)%n].corner]
			e := makeSurfaceEdge(a, b)
			if _, ok := mid[e]; ok {
				continue
			}
			pa, pb := m.Points[a], m.Points[b]
			mid[e] = m.AddPoint((pa.U+pb.U)/2, (pa.V+pb.V)/2, (pa.Depth+pb.Depth)/2)
		}
	}

	// Pass 2: split each selected face into a ring of quads around its centre.
	// The first child overwrites the face in place so unselected faces keep
	// their indices, which pass 3 and the caller's other selections rely on.
	var out []int
	for _, fi := range selList {
		f := m.Faces[fi]
		if !m.subdividableSurfaceFace(&f) {
			continue
		}
		children := m.splitSurfaceFace(&f, mid)
		if len(children) == 0 {
			continue
		}
		m.Faces[fi] = children[0]
		out = append(out, fi)
		for _, c := range children[1:] {
			out = append(out, len(m.Faces))
			m.Faces = append(m.Faces, c)
		}
	}

	// Pass 3: repair the unselected neighbours that now have a T-junction. The
	// children appended above are skipped by index rather than by set
	// membership only because their edges cannot be in mid — every one of them
	// ends at a point created in this call.
	if len(mid) == 0 {
		return out
	}
	for fi := range m.Faces {
		if sel[fi] {
			continue
		}
		f := &m.Faces[fi]
		n := len(f.Verts)
		if n < 3 {
			continue
		}
		repaired := make([]int, 0, n)
		var repairedUV []mgl32.Vec2
		hadUV := len(f.UVs) == n
		changed := false
		for i := 0; i < n; i++ {
			a, b := f.Verts[i], f.Verts[(i+1)%n]
			repaired = append(repaired, a)
			if hadUV {
				repairedUV = append(repairedUV, f.UVs[i])
			}
			mp, ok := mid[makeSurfaceEdge(a, b)]
			if !ok {
				continue
			}
			repaired = append(repaired, mp)
			if hadUV {
				repairedUV = append(repairedUV, f.UVs[i].Add(f.UVs[(i+1)%n]).Mul(0.5))
			}
			changed = true
		}
		if !changed {
			continue
		}
		f.Verts = repaired
		if hadUV {
			f.UVs = repairedUV
		}
	}
	return out
}

// subdividableSurfaceFace reports whether a face can be split at all: at least a
// triangle, no holes (a fan from a centre point would cross them), and every
// index in range.
func (m *SurfaceMesh) subdividableSurfaceFace(f *SurfaceFace) bool {
	if len(f.Verts) < 3 || len(f.Holes) > 0 {
		return false
	}
	for _, vi := range f.Verts {
		if vi < 0 || vi >= len(m.Points) {
			return false
		}
	}
	return true
}

// ---------- corners versus ring points ----------

// surfaceCornerSpan is one side of a face: the corner it starts at, plus any
// ring points lying on the straight edge running from it to the next corner.
// Indices are face-local (into f.Verts), so they address UVs too.
type surfaceCornerSpan struct {
	corner int
	inner  []int
}

// surfaceCollinearSinEps is the sine of the largest angle a ring point may bend
// through and still count as sitting *on* an edge rather than turning it. A
// midpoint is exactly collinear up to float32 rounding, so this only has to
// absorb that; anything an author has actually dragged off the line becomes a
// real corner, which is what you want when you moved it on purpose.
const surfaceCollinearSinEps = 1e-4

// surfacePointVec is a point in the face's own (U, V, Depth) space. Depth is
// part of it deliberately: a point pulled out of the surface by an extrude has
// turned the edge even though it is still halfway along it in (U, V).
func (m *SurfaceMesh) surfacePointVec(i int) mgl32.Vec3 {
	p := m.Points[i]
	return mgl32.Vec3{p.U, p.V, p.Depth}
}

// surfaceCornerSpans describes a face as its real corners and the T-junction
// points sitting between them. A face with no such points — the common case —
// comes back as one span per point with no inner entries, so callers need no
// special case for it. The same fallback covers a degenerate ring that bends at
// fewer than three places, which has no meaningful corners at all.
//
// Caller must have checked the face with subdividableSurfaceFace.
func (m *SurfaceMesh) surfaceCornerSpans(f *SurfaceFace) []surfaceCornerSpan {
	n := len(f.Verts)
	if n < 3 {
		return wholeSurfaceRingSpans(n)
	}
	isCorner := make([]bool, n)
	corners := 0
	for i := 0; i < n; i++ {
		p := m.surfacePointVec(f.Verts[(i-1+n)%n])
		v := m.surfacePointVec(f.Verts[i])
		q := m.surfacePointVec(f.Verts[(i+1)%n])
		if !onStraightSurfaceEdge(p, v, q) {
			isCorner[i] = true
			corners++
		}
	}
	if corners < 3 {
		return wholeSurfaceRingSpans(n)
	}
	spans := make([]surfaceCornerSpan, 0, corners)
	for i := 0; i < n; i++ {
		if !isCorner[i] {
			continue
		}
		sp := surfaceCornerSpan{corner: i}
		// Terminates: corners >= 3, so some later index is a corner.
		for j := (i + 1) % n; !isCorner[j]; j = (j + 1) % n {
			sp.inner = append(sp.inner, j)
		}
		spans = append(spans, sp)
	}
	return spans
}

// wholeSurfaceRingSpans treats every point of an n-point ring as a corner.
func wholeSurfaceRingSpans(n int) []surfaceCornerSpan {
	spans := make([]surfaceCornerSpan, n)
	for i := range spans {
		spans[i].corner = i
	}
	return spans
}

// onStraightSurfaceEdge reports whether v lies on the straight run from p to
// q — the signature of a T-junction point inserted by an earlier subdivide.
func onStraightSurfaceEdge(p, v, q mgl32.Vec3) bool {
	a, b := v.Sub(p), q.Sub(v)
	la, lb := a.Len(), b.Len()
	if la == 0 || lb == 0 {
		return true // a doubled-up point never turns the edge
	}
	if a.Dot(b) <= 0 {
		return false // doubles back: a corner, however sharp
	}
	return a.Cross(b).Len() <= surfaceCollinearSinEps*la*lb
}

// splitSurfaceFace returns the children of one subdivided face: one quad per
// CORNER, fanned around a new centre point. New midpoints come from mid, which
// the caller has populated for every corner edge that did not already carry a
// T-junction point.
//
// Returns nil when the face has no three corners to split between, so the
// caller leaves it alone rather than replacing it with something worse.
func (m *SurfaceMesh) splitSurfaceFace(f *SurfaceFace, mid map[surfaceEdge]int) []SurfaceFace {
	spans := m.surfaceCornerSpans(f)
	n := len(spans)
	if n < 3 {
		return nil
	}
	hadUV := len(f.UVs) == len(f.Verts)
	uv := func(i int) mgl32.Vec2 {
		if hadUV {
			return f.UVs[i]
		}
		return mgl32.Vec2{}
	}

	// Where each corner edge splits, and which of its existing T-junction
	// points fall on either side of that split — those stay in the child's
	// ring, or subdividing here would crack the neighbour that put them there.
	midV := make([]int, n)
	midUV := make([]mgl32.Vec2, n)
	head := make([][]int, n) // between the corner and the split point
	tail := make([][]int, n) // between the split point and the next corner
	for i, sp := range spans {
		next := spans[(i+1)%n]
		if len(sp.inner) == 0 {
			mp, ok := mid[makeSurfaceEdge(f.Verts[sp.corner], f.Verts[next.corner])]
			if !ok {
				return nil // pass 1 skipped this face; leave it whole
			}
			midV[i] = mp
			midUV[i] = uv(sp.corner).Add(uv(next.corner)).Mul(0.5)
			continue
		}
		k := m.nearestSurfaceInnerToMidpoint(f, sp.inner, f.Verts[sp.corner], f.Verts[next.corner])
		midV[i] = f.Verts[sp.inner[k]]
		midUV[i] = uv(sp.inner[k])
		head[i], tail[i] = sp.inner[:k], sp.inner[k+1:]
	}

	// The centre is averaged over the corners alone: letting T-junction points
	// vote would drag it towards whichever edge happens to be the most
	// subdivided.
	var cu, cv, cd float32
	var cuv mgl32.Vec2
	for _, sp := range spans {
		p := m.Points[f.Verts[sp.corner]]
		cu, cv, cd = cu+p.U, cv+p.V, cd+p.Depth
		cuv = cuv.Add(uv(sp.corner))
	}
	inv := 1 / float32(n)
	centre := m.AddPoint(cu*inv, cv*inv, cd*inv)
	cuv = cuv.Mul(inv)

	out := make([]SurfaceFace, 0, n)
	for i := 0; i < n; i++ {
		prev := (i - 1 + n) % n
		verts := []int{f.Verts[spans[i].corner]}
		uvs := []mgl32.Vec2{uv(spans[i].corner)}
		// walk keeps the T-junction points lying along this child's share of an
		// edge, so the neighbour that owns them still meets it point for point.
		walk := func(idxs []int) {
			for _, li := range idxs {
				verts = append(verts, f.Verts[li])
				uvs = append(uvs, uv(li))
			}
		}
		walk(head[i])
		verts = append(verts, midV[i], centre, midV[prev])
		uvs = append(uvs, midUV[i], cuv, midUV[prev])
		walk(tail[prev])

		nf := SurfaceFace{
			Verts: verts, Texture: f.Texture, Alpha: f.Alpha,
			Hole: f.Hole, Smooth: f.Smooth,
		}
		if hadUV {
			nf.UVs = uvs
		}
		out = append(out, nf)
	}
	return out
}

// nearestSurfaceInnerToMidpoint picks which of an edge's existing T-junction
// points to split at: the one closest to the halfway mark. Reusing a point
// rather than inserting a new one is what keeps the neighbour that owns it
// watertight — a fresh point at the same spot would be a duplicate the
// neighbour never hears about.
func (m *SurfaceMesh) nearestSurfaceInnerToMidpoint(f *SurfaceFace, inner []int, a, b int) int {
	half := m.surfacePointVec(a).Add(m.surfacePointVec(b)).Mul(0.5)
	best, bestD := 0, float32(math.MaxFloat32)
	for i, li := range inner {
		if d := m.surfacePointVec(f.Verts[li]).Sub(half).LenSqr(); d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// SurfaceDeleteFaces removes faces outright, dropping any point no remaining
// face references and reindexing the rest.
//
// Deleting is not the only way to open a surface — a face can be marked Hole,
// which keeps its ring around so the opening still has a shape to drag. Delete
// is for a face that should stop existing; Hole is for one that should stop
// being drawn.
func (m *SurfaceMesh) SurfaceDeleteFaces(faces []int) {
	sel := m.surfaceFaceSet(faces)
	if len(sel) == 0 {
		return
	}
	kept := m.Faces[:0:0]
	for i := range m.Faces {
		if !sel[i] {
			kept = append(kept, m.Faces[i])
		}
	}
	m.Faces = kept
	m.CompactPoints()
}

// CompactPoints drops every point no face references and rewrites the
// remaining indices. Boundary points are kept regardless: they are the sector's
// corners, and a mesh that had forgotten one would have nothing to re-attach
// when RemapSurfaceMesh came to carry it across an outline change.
func (m *SurfaceMesh) CompactPoints() {
	used := make([]bool, len(m.Points))
	for fi := range m.Faces {
		f := &m.Faces[fi]
		for _, v := range f.Verts {
			if v >= 0 && v < len(used) {
				used[v] = true
			}
		}
		for _, h := range f.Holes {
			for _, v := range h {
				if v >= 0 && v < len(used) {
					used[v] = true
				}
			}
		}
	}
	remap := make([]int, len(m.Points))
	pts := m.Points[:0:0]
	for i, p := range m.Points {
		if !used[i] && p.Boundary < 0 {
			remap[i] = -1
			continue
		}
		remap[i] = len(pts)
		pts = append(pts, p)
	}
	if len(pts) == len(m.Points) {
		return
	}
	m.Points = pts
	for fi := range m.Faces {
		f := &m.Faces[fi]
		for i, v := range f.Verts {
			if v >= 0 && v < len(remap) {
				f.Verts[i] = remap[v]
			}
		}
		for hi := range f.Holes {
			for i, v := range f.Holes[hi] {
				if v >= 0 && v < len(remap) {
					f.Holes[hi][i] = remap[v]
				}
			}
		}
	}
}

// RefreshSurfaceSideUVs regenerates extrusion sides' UVs from where their
// corners actually are: U along the shared edge, V across the depth. That is
// the convention the old extrusion builder used for its side quads, and the one
// ModelDoc.RefreshExtrudeSideUVs uses in the model editor, so a texture reads
// the same on a wall recess and on a model.
//
// It has to be re-derivable rather than computed once, because an interactive
// extrude creates the topology *before* the depth exists. Pressing E extrudes
// by zero and a drag sets the distance afterwards; UVs baked at creation would
// give every side a V range of zero — a texture stretched infinitely along V,
// and one no amount of V scaling can rescue, since scaling zero leaves zero.
//
// Faces that are not four-cornered are skipped: after a subdivide these are no
// longer the quads this convention describes.
func (m *SurfaceMesh) RefreshSurfaceSideUVs(faces []int) {
	for fi := range m.surfaceFaceSet(faces) {
		f := &m.Faces[fi]
		if len(f.Verts) != 4 {
			continue
		}
		ok := true
		for _, v := range f.Verts {
			if v < 0 || v >= len(m.Points) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		a, b, c := m.Points[f.Verts[0]], m.Points[f.Verts[1]], m.Points[f.Verts[2]]
		edgeLen := b.Vec2().Sub(a.Vec2()).Len()
		// Measured corner to corner rather than along the depth axis, so a cap
		// that was also dragged sideways still gets the extent it really has.
		du := c.U - b.U
		dv := c.V - b.V
		dd := c.Depth - b.Depth
		depth := float32(math.Sqrt(float64(du*du + dv*dv + dd*dd)))
		f.UVs = []mgl32.Vec2{{0, 0}, {edgeLen, 0}, {edgeLen, depth}, {0, depth}}
	}
}
