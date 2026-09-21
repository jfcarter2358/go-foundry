package compile

// smooth.go — carrying a face's smoothed normals onto its fragments.
//
// A brush face smooths by averaging each corner's normal with the faces across
// its edges (world.BrushMesh.CornerNormals), and the live renderer interpolates
// those corner normals across the face's triangle fan. A compiled face is not
// that polygon any more: CSG, the BSP's splits and the light grid have cut it
// into fragments whose points are mostly nowhere near a corner. So the source
// face's corners and normals ride along on every fragment (Face.SmoothPoly,
// Face.SmoothCorner), and each fragment point takes the normal the live fan
// would have had there — same fan, same barycentric interpolation — which is
// what keeps a compiled cylinder shading exactly as the one in the editor did.

import (
	"github.com/go-gl/mathgl/mgl32"
)

// flatCornerDot is how close to the plane every corner's normal has to be for
// a face to count as flat and store nothing.
const flatCornerDot = 0.9999

// worldCornerNormals carries a face's mesh-space corner normals out through
// the chunk placement, alongside its already-world-space corners, and returns
// both — or nils when every corner's normal is the plane's.
func worldCornerNormals(pts, corners []mgl32.Vec3, planeN mgl32.Vec3, model mgl32.Mat4) ([]mgl32.Vec3, []mgl32.Vec3) {
	if len(corners) != len(pts) || len(pts) < 3 {
		return nil, nil
	}
	// Normals go through the inverse transpose, as in vertex.glsl: a chunk
	// placement can carry per-axis scale.
	nm := model.Mat3().Inv().Transpose()
	out := make([]mgl32.Vec3, len(corners))
	flat := true
	for i, n := range corners {
		w := nm.Mul3x1(n)
		if w.Len() < 1e-9 {
			w = planeN
		}
		out[i] = w.Normalize()
		if out[i].Dot(planeN) < flatCornerDot {
			flat = false
		}
	}
	if flat {
		return nil, nil
	}
	return append([]mgl32.Vec3(nil), pts...), out
}

// VertexNormals is the smoothed normal at each of the fragment's Points, or nil
// when the face is flat (no SmoothCorner). Each point takes the normal the
// source face's fan triangulation interpolates to there — the fan
// world.BuildBrushFaceMesh draws, (0, i, i+1).
func (f *Face) VertexNormals() []mgl32.Vec3 {
	poly, ns := f.SmoothPoly, f.SmoothCorner
	if len(ns) == 0 || len(ns) != len(poly) || len(poly) < 3 {
		return nil
	}
	out := make([]mgl32.Vec3, len(f.Points))
	for pi, p := range f.Points {
		best, bestMin := mgl32.Vec3{}, float32(-1e30)
		for i := 1; i < len(poly)-1; i++ {
			u, v, w, ok := barycentric(p, poly[0], poly[i], poly[i+1])
			if !ok {
				continue
			}
			// The triangle the point is most inside of: on a shared fan edge
			// either answer is the same, and a point a hair outside every
			// triangle (a cut landing on the rim) takes the nearest one.
			if m := min(u, v, w); m > bestMin {
				bestMin = m
				best = ns[0].Mul(u).Add(ns[i].Mul(v)).Add(ns[i+1].Mul(w))
			}
		}
		if best.Len() < 1e-9 {
			best = f.Plane.N
		}
		out[pi] = best.Normalize()
	}
	return out
}

// barycentric is p's weights on triangle abc, measured in the triangle's own
// plane; ok is false for a degenerate triangle.
func barycentric(p, a, b, c mgl32.Vec3) (u, v, w float32, ok bool) {
	v0, v1, v2 := b.Sub(a), c.Sub(a), p.Sub(a)
	d00, d01, d11 := v0.Dot(v0), v0.Dot(v1), v1.Dot(v1)
	d20, d21 := v2.Dot(v0), v2.Dot(v1)
	den := d00*d11 - d01*d01
	if den <= 1e-12 {
		return 0, 0, 0, false
	}
	v = (d11*d20 - d01*d21) / den
	w = (d00*d21 - d01*d20) / den
	return 1 - v - w, v, w, true
}

// dropLight forgets every baked answer, for a face whose points have changed
// under it: none of them is parallel to Points any more.
func (f *Face) dropLight() {
	f.Light, f.SmoothLight, f.BackLight, f.SmoothBackLight = nil, nil, nil, nil
	f.SunShare, f.BackSunShare = nil, nil
}
