package geom

// polygon3d.go — triangulating an n-gon that lives in 3D.
//
// EarClip (triangulate.go) is a 2D routine, so anything with a Z has to be
// flattened before it can be cut up. Doing that well is the same three steps
// every time — best-fit plane, a basis on it, project and clip — and getting
// any of them wrong shows up as a face that triangulates inside out or spills
// triangles outside its own outline.
//
// Two meshes need it: the model editor's ModelDoc faces, and a level surface's
// SurfaceMesh faces. They index different vertex pools and carry different
// per-corner data, so what they share is exactly this — a list of positions in,
// a list of corner triples out — and it lives here rather than on either of
// them.

import "github.com/go-gl/mathgl/mgl32"

// PolygonNormal3D returns the unit normal of a 3D polygon by Newell's method,
// or the zero vector when the polygon is degenerate (fewer than three corners,
// or all of them collinear).
//
// Newell rather than a single cross product of the first three corners: an
// n-gon that has been dragged out of plane — which both mesh editors allow —
// has no one true normal, and a cross product would pick whichever one the
// first corner happened to imply. Newell's area-weighted sum gives the best-fit
// plane instead, which is stable under vertex reordering and is the plane a
// projection wants to flatten onto.
func PolygonNormal3D(pts []mgl32.Vec3) mgl32.Vec3 {
	var n mgl32.Vec3
	cnt := len(pts)
	if cnt < 3 {
		return n
	}
	for i := 0; i < cnt; i++ {
		a := pts[i]
		b := pts[(i+1)%cnt]
		n = n.Add(mgl32.Vec3{
			(a.Y() - b.Y()) * (a.Z() + b.Z()),
			(a.Z() - b.Z()) * (a.X() + b.X()),
			(a.X() - b.X()) * (a.Y() + b.Y()),
		})
	}
	if n.Len() < 1e-12 {
		return mgl32.Vec3{}
	}
	return n.Normalize()
}

// TriangulatePolygon3D cuts a 3D polygon into triangles, returning triples of
// *polygon-local* corner indices (0..len(pts)-1) so a caller can look up
// whatever else it keeps per corner — a UV, a vertex index, a bone weight.
//
// The corner order of the result preserves the input's winding, so a polygon
// wound CCW as seen from its own front produces triangles that are too, and
// the caller does not have to reason about which way the projection flipped.
//
// It never fails: a polygon whose plane cannot be determined falls back to a
// fan, which keeps the triangle count right, and a degenerate face has no area
// to get wrong anyway.
func TriangulatePolygon3D(pts []mgl32.Vec3) [][3]int {
	n := len(pts)
	if n < 3 {
		return nil
	}
	if n == 3 {
		return [][3]int{{0, 1, 2}}
	}

	nrm := PolygonNormal3D(pts)
	if nrm.Len() < 0.5 {
		return FanTriangles(n)
	}
	u := PerpAxis(nrm)
	if u.Len() < 1e-6 {
		return FanTriangles(n)
	}
	u = u.Normalize()
	v := nrm.Cross(u).Normalize()

	// Projecting onto (u, v) with v = nrm × u keeps the polygon CCW in the
	// plane, which is the winding EarClip expects and what makes the result's
	// corner order match the input's.
	origin := pts[0]
	poly := make([]mgl32.Vec2, n)
	for i, p := range pts {
		d := p.Sub(origin)
		poly[i] = mgl32.Vec2{d.Dot(u), d.Dot(v)}
	}

	flat := EarClip(poly)
	if len(flat) < 3 {
		return FanTriangles(n)
	}
	tris := make([][3]int, 0, len(flat)/3)
	for i := 0; i+2 < len(flat); i += 3 {
		tris = append(tris, [3]int{flat[i], flat[i+1], flat[i+2]})
	}
	return tris
}

// FanTriangles is the fallback triangulation for a polygon whose plane could
// not be determined: a fan from corner 0. It never fails, and a polygon that
// needs it has no area for the fan to get wrong.
func FanTriangles(n int) [][3]int {
	if n < 3 {
		return nil
	}
	tris := make([][3]int, 0, n-2)
	for i := 1; i < n-1; i++ {
		tris = append(tris, [3]int{0, i, i + 1})
	}
	return tris
}
