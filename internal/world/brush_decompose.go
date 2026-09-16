package world

// brush_decompose.go — splitting a concave BrushMesh into convex pieces.
//
// Collision's GJK and CSG's "inside a solid" test both need a brush's hull to
// be convex (see brush.go's file comment). Rather than make every author split
// a concave shape by hand, DecomposeConvex does the same split automatically.
//
// # Why this builds on planes, not on clipping the mesh
//
// The obvious approach works directly on the mesh: clip every face against an
// offending plane with bsp.SplitPolygon, and stitch the new cut edges into a
// cap. That breaks the moment two faces meet the cut plane without sharing an
// explicit vertex there — a T-vertex, where one face's corner lies partway
// along another face's longer edge — because nothing about mesh-edge
// adjacency says which of the two owns the boundary. And a T-vertex is not a
// rare case here: every split plane this file ever chooses is already one of
// the BRUSH'S OWN face planes (see partitionPlanes below), so multiple
// original faces are routinely flush with it at once.
//
// The fix is to stop reasoning about mesh edges at all. A brush's own face
// planes are a small, fixed set (bsp.PlaneFromPoints per face), and every
// piece DecomposeConvex ever produces is bounded by some subset of exactly
// those planes plus the handful chosen as cuts along the way — an
// intersection of half-spaces, which is convex by construction and has no
// edge-adjacency question to answer. bsp.RegionPolygons already turns a plane
// list into the polygons that bound it (built for BSP leaves, which are the
// same thing); reusing it here means the boundary of a piece is exact and
// T-vertex-proof for the same reason a BSP leaf's portal already is.
//
// What that leaves is classification. Intersecting a subset of the brush's
// planes can produce a region outside the original (concave) solid too — the
// step shape's missing notch is exactly the intersection of "above the
// tread" and "right of the riser" with nothing there. pointInMesh answers
// that the only way a concave shape allows: cast a ray from the candidate
// region's centre and count how many of the ORIGINAL faces it crosses.

import (
	"fmt"

	"foundry/internal/bsp"

	"github.com/go-gl/mathgl/mgl32"
)

// weldEps is how close two points have to be to count as the same vertex when
// a leaf's RegionPolygons boundary is turned into a BrushMesh's shared vertex
// pool.
const weldEps = 1e-4

// decomposeMaxPieces bounds the recursion that chooses split planes. Every
// split consumes one unit regardless of whether it goes on to produce a
// solid or an empty (discarded) leaf, so a brush that never converges — almost
// certainly self-intersecting or non-manifold geometry — errors here instead
// of recursing forever.
const decomposeMaxPieces = 64

// DecomposeConvex splits m into convex pieces, or returns it unchanged (no
// allocation) when it already is one — the common case, since almost every
// authored brush already is convex and this has to stay cheap for those.
func DecomposeConvex(m BrushMesh) ([]BrushMesh, error) {
	if m.IsConvex() {
		return []BrushMesh{m}, nil
	}

	// Deduplicated: two faces are routinely coplanar (a big wall face split
	// in two so a doorway's frame has somewhere to attach, say), and an
	// H-representation only wants the unique constraint once. A duplicate
	// left in gives RegionPolygons two indices that produce the same
	// boundary polygon, which addFace below would then add as two
	// coincident faces — same shape, counted twice, which is exactly what
	// inflates meshVolume's divergence-theorem sum off the true volume.
	var planes []bsp.Plane
	for fi := range m.Faces {
		pl, ok := bsp.PlaneFromPoints(m.FacePoints(&m.Faces[fi]))
		if !ok {
			continue
		}
		planes = appendPlane(planes, pl)
	}

	var leaves [][]bsp.Plane
	budget := decomposeMaxPieces
	if err := partitionPlanes(m.Verts, planes, nil, &leaves, &budget); err != nil {
		return nil, err
	}

	lo, hi := bsp.Bounds(m.Verts)
	// RegionPolygons clips a quad sized off worldLo/worldHi against every
	// plane of a leaf; padding well past the brush's own extent is what
	// guarantees the brush's own planes, not the quad's edges, are what
	// bound the result.
	pad := hi.Sub(lo).Len() + 1
	padVec := mgl32.Vec3{pad, pad, pad}
	lo, hi = lo.Sub(padVec), hi.Add(padVec)

	var pieces []BrushMesh
	for _, leafPlanes := range leaves {
		// RegionPolygons was built for BSP leaves, whose convention is
		// "inside is IN FRONT of every plane" (see its file comment) — the
		// opposite of a brush face's own outward-normal planes, where inside
		// is BEHIND. Feed it the flip of each plane so its front/back test
		// agrees with what's actually solid here; the polygon it hands back
		// then has that flipped plane's own normal (quadOnPlane's
		// convention), so it comes back wound inward and needs reversing to
		// match every other brush face's CCW-from-outside winding.
		inward := make([]bsp.Plane, len(leafPlanes))
		for i, pl := range leafPlanes {
			inward[i] = pl.Flip()
		}
		var piece BrushMesh
		for _, poly := range bsp.RegionPolygons(inward, lo, hi) {
			if len(poly) >= 3 {
				addFace(&piece, reversePoly(poly))
			}
		}
		if len(piece.Verts) == 0 {
			continue // this combination of planes bounds no real volume
		}
		if pointInMesh(bsp.Centroid(piece.Verts), &m) {
			pieces = append(pieces, piece)
		}
		// Not in the original (possibly concave) solid: this subset of
		// planes carved out one of the shape's own notches, same as the
		// step brush's missing corner. Discarded, not an error — a brush
		// with several reflex edges produces exactly this alongside every
		// real piece.
	}
	if len(pieces) == 0 {
		return nil, fmt.Errorf("brush decomposition produced no valid convex pieces — geometry is likely degenerate")
	}
	return pieces, nil
}

// ConvexBrushPieces is what collision (pushOutOfChunkBrushes) and CSG
// (gatherSolids) read instead of a brush's own BrushMesh directly: the brush
// unchanged, as its one and only element, for the overwhelming common case —
// already convex, or Dynamic and therefore exempt (see brush_dynamic.go and
// this package's decomposition docs on why a script-moved brush can't be
// split) — or its cached DecomposeConvex split otherwise.
//
// Cached against the chunk's own convexPiecesGen/convexPieces, invalidated
// only by BrushMeshGeneration moving on, so the fast (already-convex) path
// costs one IsConvex check per call and the slow path is paid at most once
// per edit.
func (ch *Chunk) ConvexBrushPieces(bi int) []BrushMesh {
	if bi < 0 || bi >= len(ch.Brushes) {
		return nil
	}

	// The whole cache is invalidated together rather than per-brush: cheap,
	// because the fast (already-convex or Dynamic) case below is the
	// overwhelming majority and costs one IsConvex check to re-populate, and
	// simple, because it reuses BrushMeshGeneration rather than a second
	// invalidation channel every mesh edit would have to remember to bump.
	if gen := BrushMeshGeneration(); ch.convexPiecesGen != gen {
		ch.convexPiecesGen = gen
		ch.convexPieces = nil
	}
	if cached, ok := ch.convexPieces[bi]; ok {
		return cached
	}

	b := &ch.Brushes[bi]
	// Cached even for the common case — collision calls this once per solid
	// brush EVERY physics tick, and re-running IsConvex (O(faces·verts)) that
	// often, on top of the box-prefilter's own O(verts) that already runs
	// unconditionally, is exactly the kind of per-tick cost this file's own
	// comments are careful to avoid.
	pieces := []BrushMesh{b.BrushMesh}
	if !b.Dynamic && !b.IsConvex() {
		if p, err := DecomposeConvex(b.BrushMesh); err == nil {
			pieces = p
		}
		// On error (degenerate geometry DecomposeConvex could not resolve),
		// pieces stays the brush as authored — collision and CSG keep
		// working with SOMETHING rather than dropping the brush outright.
		// Validate is what turns this into a compile-time error the author
		// actually sees (see compile/validate.go).
	}

	if ch.convexPieces == nil {
		ch.convexPieces = make(map[int][]BrushMesh)
	}
	ch.convexPieces[bi] = pieces
	return pieces
}

// partitionPlanes recursively picks a splitting plane from planes — one
// violated by some vertex of the CURRENT subset, i.e. exactly
// firstConcavePlane's test generalized to an arbitrary subset — and refines
// verts into the two sides. splits accumulates the chosen planes, correctly
// oriented per branch; when no plane is violated, verts' subset is already
// convex and (planes + splits) is recorded as one leaf's full boundary.
//
// planes is walked in full at every node, unfiltered by what is already in
// splits: a plane already satisfied by the current subset (because it was
// the very split that produced this branch, or because — the common case —
// the original solid never violated it in the first place, so it was never a
// candidate at all) simply will not be picked again. Every leaf's H-
// representation is (all of the brush's own planes) + (this branch's splits)
// rather than the splits alone, because most of a brush's planes bound every
// piece without ever being the ONE that forced a split — see the file
// comment's step-shape example, where only the tread's plane is ever chosen,
// and every other original plane still has to be part of both resulting
// pieces' boundary for RegionPolygons to close them correctly.
func partitionPlanes(verts []mgl32.Vec3, planes, splits []bsp.Plane, leaves *[][]bsp.Plane, budget *int) error {
	offender, ok := firstViolatedPlane(verts, planes)
	if !ok {
		leaf := append(append([]bsp.Plane(nil), planes...), splits...)
		*leaves = append(*leaves, leaf)
		return nil
	}
	if *budget <= 0 {
		return fmt.Errorf("brush decomposition exceeded %d pieces — geometry is likely self-intersecting or non-manifold", decomposeMaxPieces)
	}
	*budget--

	var front, back []mgl32.Vec3
	for _, v := range verts {
		switch d := offender.Distance(v); {
		case d > bsp.OnPlaneEps:
			front = append(front, v)
		case d < -bsp.OnPlaneEps:
			back = append(back, v)
		default:
			// On the cut exactly: relevant to whatever further splitting
			// either side still needs, so it goes to both.
			front = append(front, v)
			back = append(back, v)
		}
	}

	// The violating vertices (front) lie in front of offender's own outward
	// normal, so a piece containing them is bounded behind offender's
	// FLIPPED plane instead; the compliant side (back) already satisfies
	// offender as authored, unflipped — see the file comment.
	frontSplits := appendPlane(splits, offender.Flip())
	backSplits := appendPlane(splits, offender)

	// offender is dropped from the candidate list handed to EITHER child.
	// front's own points violate it by definition (that is what put them in
	// front), so leaving it in would have firstViolatedPlane pick the exact
	// same plane again next call, forever, rather than ever reaching a
	// budget check on a converging path. It is not lost information: front's
	// H-representation now carries its flip instead, which every one of
	// front's points already satisfies.
	remaining := removePlane(planes, offender)

	if err := partitionPlanes(front, remaining, frontSplits, leaves, budget); err != nil {
		return err
	}
	return partitionPlanes(back, remaining, backSplits, leaves, budget)
}

// removePlane returns planes without the first one SameAs pl.
func removePlane(planes []bsp.Plane, pl bsp.Plane) []bsp.Plane {
	out := make([]bsp.Plane, 0, len(planes))
	removed := false
	for _, p := range planes {
		if !removed && p.SameAs(pl) {
			removed = true
			continue
		}
		out = append(out, p)
	}
	return out
}

// appendPlane returns splits with pl added, skipping it if an equivalent
// plane (bsp.Plane.SameAs) is already present — the offending plane is
// routinely already one of the brush's own original faces, and RegionPolygons
// would otherwise build the same boundary polygon twice under two indices.
func appendPlane(splits []bsp.Plane, pl bsp.Plane) []bsp.Plane {
	for _, s := range splits {
		if s.SameAs(pl) {
			return splits
		}
	}
	return append(append([]bsp.Plane(nil), splits...), pl)
}

// firstViolatedPlane is firstConcavePlane generalized to an arbitrary vertex
// subset against an arbitrary plane list, for partitionPlanes' recursion.
func firstViolatedPlane(verts []mgl32.Vec3, planes []bsp.Plane) (bsp.Plane, bool) {
	for _, pl := range planes {
		for _, v := range verts {
			if pl.Distance(v) > concaveEps {
				return pl, true
			}
		}
	}
	return bsp.Plane{}, false
}

// pointInMeshDirs are the ray directions pointInMesh casts, deliberately
// distinct and none of them axis-aligned or symmetric in x/y/z. Every leaf
// DecomposeConvex classifies is an intersection of the ORIGINAL brush's own
// (routinely axis-aligned, grid-snapped) planes, so its centre is exactly the
// kind of point liable to sit on a line that grazes one of the original
// mesh's vertices or edges — a single ray direction hit that on this file's
// own test fixture, silently undercounting crossings for a genuinely inside
// point. A majority vote across several unrelated directions only fails when
// most of them are simultaneously degenerate, which independent, dissimilar
// directions make vanishingly unlikely.
var pointInMeshDirs = []mgl32.Vec3{
	{0.8123, 0.3591, 0.4671},
	{-0.2871, 0.7734, 0.5658},
	{0.5219, -0.6103, 0.5964},
	{0.1187, 0.4432, -0.8887},
	{-0.6702, -0.3318, 0.6635},
}

// pointInMesh reports whether p is inside m's boundary, by casting several
// rays from p and counting how many of each cross m's faces — odd means
// inside for that ray, and the majority verdict across pointInMeshDirs is the
// answer. Not the local "behind every plane" test IsConvex uses, because m
// may be concave; a ray crossing count is the one test that still works when
// it is.
func pointInMesh(p mgl32.Vec3, m *BrushMesh) bool {
	votes := 0
	for _, dir := range pointInMeshDirs {
		crossings := 0
		for fi := range m.Faces {
			pts := m.FacePoints(&m.Faces[fi])
			for i := 1; i < len(pts)-1; i++ {
				if rayHitsTriangle(p, dir, pts[0], pts[i], pts[i+1]) {
					crossings++
				}
			}
		}
		if crossings%2 == 1 {
			votes++
		}
	}
	return votes*2 > len(pointInMeshDirs)
}

// rayHitsTriangle is the Möller–Trumbore ray/triangle test: does the ray from
// origin along dir cross triangle (v0,v1,v2) at a positive distance.
func rayHitsTriangle(origin, dir, v0, v1, v2 mgl32.Vec3) bool {
	const eps = 1e-7
	e1 := v1.Sub(v0)
	e2 := v2.Sub(v0)
	h := dir.Cross(e2)
	a := e1.Dot(h)
	if a > -eps && a < eps {
		return false // parallel to the triangle's plane
	}
	f := 1 / a
	s := origin.Sub(v0)
	u := f * s.Dot(h)
	if u < 0 || u > 1 {
		return false
	}
	q := s.Cross(e1)
	v := f * dir.Dot(q)
	if v < 0 || u+v > 1 {
		return false
	}
	t := f * e2.Dot(q)
	return t > eps
}

// addFace appends pts to m as a new face, welding each point against m's
// existing vertex pool within weldEps rather than adding a duplicate. Texture
// and every other draw-only field are left at their zero value: a piece's
// faces are read by IsConvex/firstConcavePlane and by CSG's plane list, never
// drawn — see world.Chunk.ConvexBrushPieces.
func addFace(m *BrushMesh, pts []mgl32.Vec3) {
	indices := make([]int, len(pts))
	for i, p := range pts {
		indices[i] = weldVert(m, p)
	}
	m.Faces = append(m.Faces, NewBrushFace(indices, ""))
}

// reversePoly returns pts in the opposite order.
func reversePoly(pts []mgl32.Vec3) []mgl32.Vec3 {
	out := make([]mgl32.Vec3, len(pts))
	for i, p := range pts {
		out[len(pts)-1-i] = p
	}
	return out
}

// weldVert returns the index of p in m.Verts, adding it if no existing vertex
// is within weldEps.
func weldVert(m *BrushMesh, p mgl32.Vec3) int {
	for i, v := range m.Verts {
		if v.Sub(p).Len() < weldEps {
			return i
		}
	}
	m.Verts = append(m.Verts, p)
	return len(m.Verts) - 1
}
