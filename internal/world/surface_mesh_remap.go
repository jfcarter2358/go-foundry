package world

// surface_mesh_remap.go — carrying a surface mesh across a change of outline.
//
// A SurfaceMesh is parametric so it can ride its sector: SyncSurfaceBoundary
// handles a corner that *moved*, because the boundary point still knows which
// corner it is and simply reads the new position. What it cannot handle is the
// outline changing *shape* — a wall split in two, a corner inserted or deleted,
// a sector cut in half — because then the corner indices themselves mean
// something different, and a mesh left as it was would have its boundary points
// snapping to whatever corner happened to inherit their index.
//
// That failure mode is not hypothetical. splitTerrainPointsAtU (editor_2d.go)
// exists because exactly one of these edits — splitting a wall — had to be
// handled for terrain points, one edit and one kind of data at a time.
// RemapSurfaceMesh is that generalised: one function every structural edit
// calls, for every mesh.
//
// # What is preserved and what is not
//
// Interior points keep their (U, V) and Depth. That is the whole shape the
// author drew, and it survives every remap unchanged.
//
// Boundary points are re-bound: each is matched to the corner of the *new*
// outline nearest where it used to be, and points whose corner has gone are
// demoted to free points so the faces referencing them stay valid. A demoted
// point stops tracking the sector but keeps the position it had, so a shape
// straddling a deleted corner deforms rather than collapsing.
//
// The alternative — discarding the mesh whenever the outline changes — was
// rejected because these are edits an author makes *while* shaping a room, not
// afterwards. Losing the alcove you carved because you then nudged the room's
// corner is the kind of thing that stops people using a tool at all.

import "github.com/go-gl/mathgl/mgl32"

// SurfaceRemap describes how an outline changed, as a map from old corner index
// to new. A -1 means the corner is gone.
//
// The caller knows what it did — a split inserted a corner here, a join removed
// those two — and saying so directly is both exact and cheaper than having this
// file guess from two point lists. RemapByPosition builds one by matching
// positions, for the callers that genuinely do not know.
type SurfaceRemap struct {
	// Corners[i] is where old corner i went, or -1 if it no longer exists.
	Corners []int
	// NewCount is how many corners the new outline has, which bounds what
	// Corners may map to and lets an entirely-new corner be spotted.
	NewCount int
}

// RemapByPosition builds a remap by matching each old outline corner to the
// nearest new one within tol, which is what an edit that moved geometry around
// without recording its own bookkeeping leaves behind.
//
// Matching is greedy nearest-first rather than in index order: an insertion
// shifts every later index by one, so pairing by index would mis-bind every
// corner after the new one, while pairing by position gets all of them right
// and leaves only the genuinely new corner unmatched.
func RemapByPosition(oldOutline, newOutline []mgl32.Vec2, tol float32) SurfaceRemap {
	r := SurfaceRemap{Corners: make([]int, len(oldOutline)), NewCount: len(newOutline)}
	taken := make([]bool, len(newOutline))
	type cand struct {
		oldIdx, newIdx int
		d2             float32
	}
	var cands []cand
	tol2 := tol * tol
	for i, op := range oldOutline {
		for j, np := range newOutline {
			d2 := op.Sub(np).LenSqr()
			if d2 <= tol2 {
				cands = append(cands, cand{i, j, d2})
			}
		}
	}
	// Nearest pairs first, so a corner with a genuine twin claims it before a
	// merely nearby one can.
	for i := range r.Corners {
		r.Corners[i] = -1
	}
	for len(cands) > 0 {
		best := 0
		for i := 1; i < len(cands); i++ {
			if cands[i].d2 < cands[best].d2 {
				best = i
			}
		}
		c := cands[best]
		cands = append(cands[:best], cands[best+1:]...)
		if r.Corners[c.oldIdx] >= 0 || taken[c.newIdx] {
			continue
		}
		r.Corners[c.oldIdx] = c.newIdx
		taken[c.newIdx] = true
	}
	return r
}

// RemapSurfaceMesh rebinds a mesh's boundary points through r, in place.
//
// It does not move anything: positions are left exactly as they were, and the
// next SyncSurfaceBoundary is what pulls the re-bound points onto their new
// corners. Keeping the two steps apart matters because a structural edit
// usually runs before the new geometry is settled — the sector's points are
// rewritten, then heights are interpolated, then the surfaces rebuild — and a
// remap that also moved things would be reading half-finished geometry.
func RemapSurfaceMesh(m *SurfaceMesh, r SurfaceRemap) {
	if m == nil || len(m.Points) == 0 {
		return
	}
	for i := range m.Points {
		b := m.Points[i].Boundary
		if b < 0 {
			continue
		}
		if b >= len(r.Corners) {
			// A corner the remap does not describe: the outline shrank without
			// saying so. Demote rather than leave it pointing past the end.
			m.Points[i].Boundary = SurfaceFree
			continue
		}
		nb := r.Corners[b]
		if nb < 0 || nb >= r.NewCount {
			m.Points[i].Boundary = SurfaceFree
			continue
		}
		m.Points[i].Boundary = nb
	}
}

// InsertSurfaceBoundaryCorner rebinds a mesh for an outline that gained a
// corner at position at, and splices a matching point into every face edge that
// spanned the two corners it went between.
//
// Rebinding alone would leave a T-junction: the new corner exists on the sector
// but no face has a point there, so the surface still spans the old straight
// edge while the sector's outline now bends at the new corner. On a wall split
// that is a visible crack down the join.
//
// prev and next are the old corner indices the new one was inserted between.
func InsertSurfaceBoundaryCorner(m *SurfaceMesh, prev, next, at int, pos mgl32.Vec2, depth float32) {
	if m == nil || len(m.Points) == 0 {
		return
	}
	// Everything at or past the insertion point shifts up one.
	for i := range m.Points {
		b := m.Points[i].Boundary
		if b >= at {
			m.Points[i].Boundary = b + 1
		}
	}

	np := len(m.Points)
	m.Points = append(m.Points, SurfacePoint{U: pos.X(), V: pos.Y(), Depth: depth, Boundary: at})

	// Splice it into any face edge running directly between the two corners it
	// separates, so the mesh bends where the outline now does.
	prevAfter, nextAfter := prev, next
	if prevAfter >= at {
		prevAfter++
	}
	if nextAfter >= at {
		nextAfter++
	}
	for fi := range m.Faces {
		f := &m.Faces[fi]
		f.Verts, f.UVs = spliceSurfaceEdge(m.Points, f.Verts, f.UVs, prevAfter, nextAfter, np)
		for hi := range f.Holes {
			f.Holes[hi], _ = spliceSurfaceEdge(m.Points, f.Holes[hi], nil, prevAfter, nextAfter, np)
		}
	}
}

// spliceSurfaceEdge inserts np into ring wherever the ring steps directly
// between the boundary corners a and b, in either direction. Any per-corner UVs
// are interpolated so the texture does not jump at the new point.
func spliceSurfaceEdge(points []SurfacePoint, ring []int, uvs []mgl32.Vec2, a, b, np int) ([]int, []mgl32.Vec2) {
	n := len(ring)
	if n < 3 {
		return ring, uvs
	}
	hasUV := len(uvs) == n
	out := make([]int, 0, n+1)
	var outUV []mgl32.Vec2
	if hasUV {
		outUV = make([]mgl32.Vec2, 0, n+1)
	}
	changed := false
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		out = append(out, ring[i])
		if hasUV {
			outUV = append(outUV, uvs[i])
		}
		bi := surfaceBoundaryOf(points, ring[i])
		bj := surfaceBoundaryOf(points, ring[j])
		if (bi == a && bj == b) || (bi == b && bj == a) {
			out = append(out, np)
			if hasUV {
				outUV = append(outUV, uvs[i].Add(uvs[j]).Mul(0.5))
			}
			changed = true
		}
	}
	if !changed {
		return ring, uvs
	}
	return out, outUV
}

func surfaceBoundaryOf(points []SurfacePoint, i int) int {
	if i < 0 || i >= len(points) {
		return SurfaceFree
	}
	return points[i].Boundary
}

// RemoveSurfaceBoundaryCorner rebinds a mesh for an outline that lost corner
// at, and drops the matching point out of every face ring that used it.
//
// Dropping rather than demoting: the corner is gone from the sector, so a face
// still stepping through where it was would put a kink in a surface whose
// outline no longer has one. A ring that would fall below three corners is left
// alone and its face is marked Hole instead — better an opening the author can
// see and fix than a degenerate face that triangulates into nothing.
func RemoveSurfaceBoundaryCorner(m *SurfaceMesh, at int) {
	if m == nil || len(m.Points) == 0 {
		return
	}
	var doomed []int
	for i := range m.Points {
		b := m.Points[i].Boundary
		switch {
		case b == at:
			doomed = append(doomed, i)
		case b > at:
			m.Points[i].Boundary = b - 1
		}
	}
	if len(doomed) == 0 {
		return
	}
	drop := make(map[int]bool, len(doomed))
	for _, d := range doomed {
		drop[d] = true
	}
	for fi := range m.Faces {
		f := &m.Faces[fi]
		verts, uvs, ok := dropSurfaceRingPoints(f.Verts, f.UVs, drop)
		if !ok {
			f.Hole = true
			continue
		}
		f.Verts, f.UVs = verts, uvs
		kept := f.Holes[:0:0]
		for _, h := range f.Holes {
			hv, _, hok := dropSurfaceRingPoints(h, nil, drop)
			if hok {
				kept = append(kept, hv)
			}
		}
		f.Holes = kept
	}
	// The points themselves go only once nothing references them, which
	// CompactPoints decides — a doomed point may still be held by a hole ring
	// that was left alone.
	for _, d := range doomed {
		m.Points[d].Boundary = SurfaceFree
	}
	m.CompactPoints()
}

// dropSurfaceRingPoints removes the dropped points from a ring, reporting
// whether what is left is still a polygon.
func dropSurfaceRingPoints(ring []int, uvs []mgl32.Vec2, drop map[int]bool) ([]int, []mgl32.Vec2, bool) {
	hasUV := len(uvs) == len(ring)
	out := make([]int, 0, len(ring))
	var outUV []mgl32.Vec2
	for i, v := range ring {
		if drop[v] {
			continue
		}
		out = append(out, v)
		if hasUV {
			outUV = append(outUV, uvs[i])
		}
	}
	if len(out) == len(ring) {
		return ring, uvs, true
	}
	if len(out) < 3 {
		return ring, uvs, false
	}
	return out, outUV, true
}

// SplitWallSurfaceMesh divides a wall's mesh at splitU, for a wall being cut in
// two on the 2D map. The second half's U coordinates are rebased so that the
// split point becomes its origin, which is the convention a wall's (U, V) uses:
// distance from the wall's own start point.
//
// This is the generalisation of splitTerrainPointsAtU, which did the same job
// for one kind of loose point back when that was the only thing a wall carried.
//
// A face is assigned by its centroid, and takes all of its points with it even
// where one has strayed past the cut. Clipping the faces properly would mean
// re-triangulating both halves and inventing points along the seam, and the
// result would be worse: a doorway cut exactly on the join would become two
// half-doorways in two different meshes, neither of which the author could drag
// as one. Assigning whole and letting a straddling shape overhang slightly is
// visible and adjustable; a shape sliced in half is neither.
//
// Boundary bindings survive untouched. A wall's outline corners are the same
// four in both halves — start-top, end-top, end-bottom, start-bottom — and the
// split only moves where two of them are, which SyncSurfaceBoundary picks up on
// the next rebuild.
func SplitWallSurfaceMesh(m *SurfaceMesh, splitU float32) (first, second SurfaceMesh) {
	if m.IsEmpty() {
		return SurfaceMesh{}, SurfaceMesh{}
	}
	firstSide := make([]bool, len(m.Faces))
	for fi := range m.Faces {
		f := &m.Faces[fi]
		if len(f.Verts) == 0 {
			continue
		}
		var sum float32
		n := 0
		for _, v := range f.Verts {
			if v >= 0 && v < len(m.Points) {
				sum += m.Points[v].U
				n++
			}
		}
		firstSide[fi] = n == 0 || sum/float32(n) <= splitU
	}
	first = splitWallHalf(m, firstSide, true, splitU)
	second = splitWallHalf(m, firstSide, false, splitU)
	return first, second
}

// splitWallHalf copies the faces on one side of the cut, together with the
// points they use, reindexed.
func splitWallHalf(m *SurfaceMesh, firstSide []bool, wantFirst bool, splitU float32) SurfaceMesh {
	remap := make([]int, len(m.Points))
	for i := range remap {
		remap[i] = -1
	}
	var out SurfaceMesh
	take := func(v int) int {
		if v < 0 || v >= len(m.Points) {
			return -1
		}
		if remap[v] >= 0 {
			return remap[v]
		}
		p := m.Points[v]
		if !wantFirst {
			p.U -= splitU
		}
		remap[v] = len(out.Points)
		out.Points = append(out.Points, p)
		return remap[v]
	}
	takeRing := func(ring []int) ([]int, bool) {
		got := make([]int, 0, len(ring))
		for _, v := range ring {
			nv := take(v)
			if nv < 0 {
				return nil, false
			}
			got = append(got, nv)
		}
		return got, len(got) >= 3
	}

	for fi := range m.Faces {
		if firstSide[fi] != wantFirst {
			continue
		}
		src := &m.Faces[fi]
		verts, ok := takeRing(src.Verts)
		if !ok {
			continue
		}
		nf := src.Clone()
		nf.Verts = verts
		nf.Holes = nil
		for _, h := range src.Holes {
			if hv, ok := takeRing(h); ok {
				nf.Holes = append(nf.Holes, hv)
			}
		}
		out.Faces = append(out.Faces, nf)
	}
	if len(out.Faces) == 0 {
		return SurfaceMesh{}
	}
	return out
}
