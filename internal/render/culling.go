package render

import (
	"foundry/internal/geom"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// Frustum holds the six clip planes of the view frustum.
type Frustum struct {
	Planes [6]mgl32.Vec4
}

// ExtractFrustum extracts the six frustum planes from a combined projection×view
// matrix using the Gribb/Hartmann method.
// mgl32.Mat4 is column-major: element [col*4+row].
func ExtractFrustum(projView mgl32.Mat4) Frustum {
	var f Frustum
	m := projView

	// Row accessors (column-major storage):
	//   row 0: m[0], m[4], m[8],  m[12]
	//   row 1: m[1], m[5], m[9],  m[13]
	//   row 2: m[2], m[6], m[10], m[14]
	//   row 3: m[3], m[7], m[11], m[15]

	// Left   = row3 + row0
	f.Planes[0] = mgl32.Vec4{m[3] + m[0], m[7] + m[4], m[11] + m[8], m[15] + m[12]}
	// Right  = row3 - row0
	f.Planes[1] = mgl32.Vec4{m[3] - m[0], m[7] - m[4], m[11] - m[8], m[15] - m[12]}
	// Bottom = row3 + row1
	f.Planes[2] = mgl32.Vec4{m[3] + m[1], m[7] + m[5], m[11] + m[9], m[15] + m[13]}
	// Top    = row3 - row1
	f.Planes[3] = mgl32.Vec4{m[3] - m[1], m[7] - m[5], m[11] - m[9], m[15] - m[13]}
	// Near   = row3 + row2
	f.Planes[4] = mgl32.Vec4{m[3] + m[2], m[7] + m[6], m[11] + m[10], m[15] + m[14]}
	// Far    = row3 - row2
	f.Planes[5] = mgl32.Vec4{m[3] - m[2], m[7] - m[6], m[11] - m[10], m[15] - m[14]}

	return f
}

// AABBInFrustum returns true if the AABB [min, max] is at least partially inside
// the frustum (positive-vertex test — conservative, no false negatives).
func AABBInFrustum(f Frustum, min, max mgl32.Vec3) bool {
	for _, plane := range f.Planes {
		// Pick the positive vertex: the AABB corner most in the plane-normal direction
		px, py, pz := max.X(), max.Y(), max.Z()
		if plane[0] < 0 {
			px = min.X()
		}
		if plane[1] < 0 {
			py = min.Y()
		}
		if plane[2] < 0 {
			pz = min.Z()
		}
		if plane[0]*px+plane[1]*py+plane[2]*pz+plane[3] < 0 {
			return false
		}
	}
	return true
}

// Portal-graph visibility — the BFS through camera-facing sector portals and
// floor/ceiling openings that used to narrow a chunk to the rooms actually
// reachable from the one the camera stood in — went with sectors. A brush is
// solid geometry rather than a room with doorways, so there is no graph to
// walk at authoring time.
//
// What replaces it splits in two, and both halves are ahead of this point in
// the migration: raw (in-editor) play frustum-culls each brush's own AABB,
// which is what BrushInFrustum below is for, and a compiled level gets real
// occlusion from the BSP/PVS the compile step builds (plan Phase 9).

// BrushInFrustum reports whether a brush is at least partially inside the view
// frustum, testing its world-space bounds — the brush counterpart of the
// per-sector test the portal walk used to feed.
func BrushInFrustum(f Frustum, ch *world.Chunk, b *world.Brush) bool {
	min, max := b.WorldAABB(ch)
	return AABBInFrustum(f, min, max)
}

// BrushFaceFacingCamera reports whether a face's outward normal points toward
// camPos, so a solid's back faces can be skipped. pos is in the same space the
// face's own verts are — chunk-local — so the caller carries the camera in
// rather than the geometry out.
func BrushFaceFacingCamera(b *world.Brush, f *world.BrushFace, camLocal mgl32.Vec3) bool {
	pts := b.FacePoints(f)
	if len(pts) < 3 {
		return true
	}
	n := b.FaceNormal(f)
	var mid mgl32.Vec3
	for _, p := range pts {
		mid = mid.Add(p)
	}
	mid = mid.Mul(1 / float32(len(pts)))
	return n.Dot(camLocal.Sub(mid)) > 0
}

// TransformAABB carries an axis-aligned box through a transform and re-fits a
// box around the result.
//
// A rotated box is not a box, so all eight corners are carried and re-bounded.
// Transforming just the two extremes would produce a box that is not merely
// loose but the wrong size — and a culling box that is too small hides geometry
// that is on screen.
func TransformAABB(m mgl32.Mat4, min, max mgl32.Vec3) (mgl32.Vec3, mgl32.Vec3) {
	h := max.Sub(min).Mul(0.5)
	c := min.Add(h)
	var outMin, outMax mgl32.Vec3
	for i := 0; i < 8; i++ {
		corner := mgl32.Vec3{
			h.X() * world.CornerSign(i, 0),
			h.Y() * world.CornerSign(i, 1),
			h.Z() * world.CornerSign(i, 2),
		}
		r := m.Mul4x1(c.Add(corner).Vec4(1))
		p := mgl32.Vec3{r.X(), r.Y(), r.Z()}
		if i == 0 {
			outMin, outMax = p, p
			continue
		}
		outMin, outMax = geom.VecMin(outMin, p), geom.VecMax(outMax, p)
	}
	return outMin, outMax
}
