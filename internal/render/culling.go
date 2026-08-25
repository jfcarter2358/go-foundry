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

// PortalFacingCamera returns true when the portal opening's inward normal
// (same left-hand convention as WallFacingCamera) points toward camPos.
// Used to cull gap-fill geometry attached to portals.
func PortalFacingCamera(start, end mgl32.Vec2, camPos mgl32.Vec3) bool {
	dx := end.X() - start.X()
	dz := end.Y() - start.Y()
	nx := -dz
	nz := dx
	mx := (start.X() + end.X()) * 0.5
	mz := (start.Y() + end.Y()) * 0.5
	toCamX := camPos.X() - mx
	toCamZ := camPos.Z() - mz
	return nx*toCamX+nz*toCamZ > 0
}

// portalBFS performs a BFS from startSectorID through portals and returns the
// set of reachable sector IDs. If portalFilter is non-nil, only portals for
// which portalFilter returns true are traversed.
// Returns nil if startSectorID is empty or not found.
func portalBFS(chunk *world.Chunk, startSectorID string, portalFilter func(p *world.Portal) bool) map[string]bool {
	if startSectorID == "" {
		return nil
	}

	idToIdx := make(map[string]int, len(chunk.Sectors))
	for i := range chunk.Sectors {
		idToIdx[chunk.Sectors[i].ID] = i
	}

	startIdx, ok := idToIdx[startSectorID]
	if !ok {
		return nil
	}

	result := map[string]bool{startSectorID: true}
	queue := []int{startIdx}

	for len(queue) > 0 {
		idx := queue[0]
		queue = queue[1:]
		sec := &chunk.Sectors[idx]

		for k := range sec.Portals {
			p := &sec.Portals[k]
			if p.ToSector == "" || result[p.ToSector] {
				continue
			}
			if p.ToFrame != "" {
				// A frame portal leads out of this chunk entirely, into a space
				// that is moving relative to it. Walking the graph across that
				// boundary would mean carrying a transform through the BFS and
				// testing each sector's facing in its own coordinates.
				//
				// Not doing it costs nothing but efficiency: portal culling only
				// ever *removes* sectors from what the frustum already accepted,
				// so the far side simply falls back to frustum-only culling and
				// is still drawn. You see the hangar through the open ramp; the
				// engine just did not get to skip anything behind it.
				continue
			}
			if portalFilter != nil && !portalFilter(p) {
				continue
			}
			result[p.ToSector] = true
			if ni, ok := idToIdx[p.ToSector]; ok {
				queue = append(queue, ni)
			}
		}
	}

	return result
}

// PortalVisibleSectors performs a BFS from startSectorID through camera-facing
// portals and returns the set of sector IDs reachable (and therefore visible).
// Returns nil if startSectorID is empty or not found, signalling the caller to
// fall back to frustum-only culling.
func PortalVisibleSectors(chunk *world.Chunk, startSectorID string, camPos mgl32.Vec3) map[string]bool {
	return portalBFS(chunk, startSectorID, func(p *world.Portal) bool {
		return PortalFacingCamera(p.StartPoint, p.EndPoint, camPos)
	})
}

// PortalConnectedSectors returns the set of all sector IDs reachable from
// startSectorID through portals, ignoring camera direction. This identifies
// which sectors participate in the portal graph so that disconnected sectors
// (e.g. nested sectors with no shared edges) can bypass portal culling.
// Returns nil if startSectorID is empty or not found.
func PortalConnectedSectors(chunk *world.Chunk, startSectorID string) map[string]bool {
	return portalBFS(chunk, startSectorID, nil)
}

// WallFacingCamera returns true when the wall's outward normal (left-hand normal
// of its direction vector, CCW interior convention) points toward camPos.
func WallFacingCamera(wall world.Wall, camPos mgl32.Vec3) bool {
	dx := wall.EndPoint.X() - wall.StartPoint.X()
	dz := wall.EndPoint.Y() - wall.StartPoint.Y()
	// Left-hand normal of (dx, dz) in XZ plane
	nx := -dz
	nz := dx
	// Midpoint of wall
	mx := (wall.StartPoint.X() + wall.EndPoint.X()) * 0.5
	mz := (wall.StartPoint.Y() + wall.EndPoint.Y()) * 0.5
	toCamX := camPos.X() - mx
	toCamZ := camPos.Z() - mz
	return nx*toCamX+nz*toCamZ > 0
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
