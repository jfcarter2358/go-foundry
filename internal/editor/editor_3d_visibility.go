package editor

// editor_3d_visibility.go — which sector points and edges the camera can
// actually see in the 3D view, and therefore which of them can be clicked.
//
// This is the level editor's answer to editor_model_visibility.go, and it draws
// the same distinction that file does. Solid shading hides the far side of the
// world, so offering a handle on every vertex in range is a lie: half of them
// are behind a wall, and clicking one grabs geometry the user cannot see.
// Wireframe is the opposite contract — nothing occludes anything, so every
// point and edge is fair game, and Editor.Wireframe switches between the two.
//
// The test is a real occlusion test — a ray from the eye to the vertex, blocked
// or not — rather than the cheaper "is a face it belongs to turned towards me",
// because a level is nothing but concave geometry: the cheap test would keep
// offering handles for the room on the other side of a wall.
//
// Two things keep it affordable, and both matter here in a way they do not in
// the model editor, where the occluder set is one mesh:
//
//   - the occluder walk is bounded before it reaches triangles, by testing the
//     ray segment against chunk and sector bounds first, and it stops at the
//     first blocker rather than looking for the closest one;
//   - answers are cached for as long as the eye and the geometry hold still,
//     which is what makes the draw path and every picking entry point able to
//     just ask without coordinating.

import (
	"foundry/internal/gfx"
	"foundry/internal/render"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// editorVisRayBudget caps how many occlusion rays one frame may cast. Past it
// the remaining handles are reported visible — slightly optimistic handles beat
// a viewport that stutters whenever "show all boxes" meets a dense level. The
// cache means the budget is only spent when the eye or the world has moved.
const editorVisRayBudget = 600

// editorVisSlack is how far short of the vertex a blocker has to be to count.
// Every sector vertex lies exactly *on* the floor, ceiling and walls that meet
// there, so without it a point would always be occluded by its own faces.
const editorVisSlack float32 = 1e-2

// editorVis caches the last set of answers, keyed by the eye they were computed
// for. The key does not include the view direction: what a point eye can see
// past opaque geometry depends only on where that eye is, so turning on the
// spot costs nothing to recheck.
var editorVis struct {
	cache  map[vertexSel]bool
	eye    mgl32.Vec3
	wire   bool
	ready  bool
	budget int
}

// editorVisNewFrame drops the cache at the top of each editor frame. Unlike the
// model editor's document revision there is no single counter that catches
// every level mutation — a gizmo drag moves geometry with the camera perfectly
// still — so the cache lives one frame and is rebuilt from scratch.
func editorVisNewFrame() {
	editorVisReset()
}

func editorVisReset() {
	if editorVis.cache == nil {
		editorVis.cache = make(map[vertexSel]bool, 256)
	} else {
		clear(editorVis.cache)
	}
	editorVis.eye = gfx.Cam.Position
	editorVis.wire = Editor.Wireframe
	editorVis.ready = true
	editorVis.budget = editorVisRayBudget
}

// editorVertexVisible reports whether the camera can see the handle at pos.
// Always true in wireframe, where there is nothing to hide behind.
func editorVertexVisible(sel vertexSel, pos mgl32.Vec3) bool {
	if Editor.Wireframe {
		return true
	}
	if !editorVis.ready || editorVis.eye != gfx.Cam.Position || editorVis.wire != Editor.Wireframe {
		editorVisReset()
	}
	if v, ok := editorVis.cache[sel]; ok {
		return v
	}
	if editorVis.budget <= 0 {
		return true
	}
	editorVis.budget--
	v := !editorPointOccluded(gfx.Cam.Position, pos)
	editorVis.cache[sel] = v
	return v
}

// editorEdgeVisible reports whether an edge can be seen. As in the model
// editor, either endpoint being visible is enough: an edge that runs behind a
// wall but comes out at one end is still something the user can see and aim at.
func editorEdgeVisible(a, b vertexSel, pa, pb mgl32.Vec3) bool {
	return editorVertexVisible(a, pa) || editorVertexVisible(b, pb)
}

// ---------- the occlusion test ----------

// editorPointOccluded reports whether any level geometry sits between the eye
// and p. It returns on the first blocker found — unlike pick3D, which has to
// keep going to find the *closest* hit.
//
// Only sector faces count. The other two things a click can land on are
// deliberately not occluders: a water plane is transparent, so what is under it
// is exactly what the user can see and means to grab, and the spawn marker is
// an editor-only arrow drawn over the world rather than part of it.
func editorPointOccluded(eye, p mgl32.Vec3) bool {
	lvl := Editor.level
	if lvl == nil {
		return false
	}
	seg := p.Sub(eye)
	dist := seg.Len()
	if dist < 1e-5 {
		return false
	}
	dir := seg.Mul(1 / dist)
	limit := dist - editorVisSlack
	if limit <= editorVisSlack {
		return false
	}

	// eye/dir are world-space; every chunk's bounds and vertex buffers are
	// chunk-local. Both the bounds rejects and the triangle tests below go
	// through the chunk's own placement, or a moved chunk occludes from where
	// it used to be — offering handles behind a wall and hiding ones in plain
	// sight. The matrix is the identity for an unplaced chunk, which is what
	// keeps this one walk.
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !ch.IsEnabled() {
			continue // draws nothing, so it occludes nothing
		}
		model := ch.ModelMatrix()
		cmin, cmax := ch.WorldBounds()
		if !segmentHitsAABB(eye, dir, limit, cmin, cmax) {
			continue
		}

		for seci := range ch.Sectors {
			s := &ch.Sectors[seci]
			smin, smax := editorSectorBounds(s)
			smin, smax = render.TransformAABB(model, smin, smax)
			if !segmentHitsAABB(eye, dir, limit, smin, smax) {
				continue
			}
			if s.Floor.Enabled && !s.Floor.Hidden {
				if modelGeometryBlocks(eye, dir, limit, s.Floor.Geometry.Vertices, model) ||
					modelGeometriesBlock(eye, dir, limit, s.Floor.SubmeshGeometries, model) {
					return true
				}
			}
			if s.Ceiling.Enabled {
				if modelGeometryBlocks(eye, dir, limit, s.Ceiling.Geometry.Vertices, model) ||
					modelGeometriesBlock(eye, dir, limit, s.Ceiling.SubmeshGeometries, model) {
					return true
				}
			}
			for wi := range s.Walls {
				w := &s.Walls[wi]
				if !w.Visible {
					continue
				}
				if modelGeometryBlocks(eye, dir, limit, w.Geometry.Vertices, model) ||
					modelGeometriesBlock(eye, dir, limit, w.SubmeshGeometries, model) {
					return true
				}
			}
		}
	}

	return false
}

// editorSectorBounds is a sector's CHUNK-LOCAL AABB, grown vertically so a
// displaced or extruded surface that pushes past the stored floor/ceiling
// heights still gets its triangles tested. The caller carries it out through
// the owning chunk's ModelMatrix before testing a world-space ray against it.
func editorSectorBounds(s *world.Sector) (mgl32.Vec3, mgl32.Vec3) {
	const margin = 2
	lo, hi := s.Floor.Height, s.Ceiling.Height
	if hi < lo {
		lo, hi = hi, lo
	}
	return mgl32.Vec3{s.MinBound.X(), lo - margin, s.MinBound.Y()},
		mgl32.Vec3{s.MaxBound.X(), hi + margin, s.MaxBound.Y()}
}

// segmentHitsAABB reports whether the segment from origin running limit units
// along dir meets the box. Being inside the box counts: rayAABBIntersect
// returns the *exit* distance there, which can sit past a short segment that is
// nevertheless entirely enclosed.
func segmentHitsAABB(origin, dir mgl32.Vec3, limit float32, bmin, bmax mgl32.Vec3) bool {
	inside := true
	for i := 0; i < 3; i++ {
		if origin[i] < bmin[i] || origin[i] > bmax[i] {
			inside = false
			break
		}
	}
	if inside {
		return true
	}
	hit, t := rayAABBIntersect(origin, dir, bmin, bmax)
	return hit && t <= limit
}

// geometryBlocks reports whether any triangle of a world-space [X,Y,Z,U,V]
// vertex buffer crosses the segment. Both sides count: a wall seen from behind
// still hides what is beyond it.
func geometryBlocks(origin, dir mgl32.Vec3, limit float32, verts []float32) bool {
	for i := 0; i+14 < len(verts); i += 15 {
		v0 := mgl32.Vec3{verts[i+0], verts[i+1], verts[i+2]}
		v1 := mgl32.Vec3{verts[i+5], verts[i+6], verts[i+7]}
		v2 := mgl32.Vec3{verts[i+10], verts[i+11], verts[i+12]}
		if hit, t, _ := rayTriangleIntersect(origin, dir, v0, v1, v2); hit &&
			t > editorVisSlack && t < limit {
			return true
		}
	}
	return false
}

// modelGeometryBlocks is geometryBlocks against model-space vertices, the same
// move pickModelT makes: the ray is rewritten in model space with its direction
// transformed as a vector rather than renormalised, which leaves the parameter
// t — and so the world-space limit — unchanged. Returns false when the matrix
// is not invertible.
func modelGeometryBlocks(origin, dir mgl32.Vec3, limit float32, verts []float32, model mgl32.Mat4) bool {
	if len(verts) == 0 {
		return false
	}
	inv := model.Inv()
	if inv == (mgl32.Mat4{}) {
		return false
	}
	o4 := inv.Mul4x1(mgl32.Vec4{origin.X(), origin.Y(), origin.Z(), 1})
	d4 := inv.Mul4x1(mgl32.Vec4{dir.X(), dir.Y(), dir.Z(), 0})
	return geometryBlocks(
		mgl32.Vec3{o4.X(), o4.Y(), o4.Z()},
		mgl32.Vec3{d4.X(), d4.Y(), d4.Z()},
		limit, verts,
	)
}

// modelGeometriesBlock is modelGeometryBlocks across a slice, the model-space
// counterpart of geometriesBlock.
func modelGeometriesBlock(origin, dir mgl32.Vec3, limit float32, geoms []gfx.Geometry, model mgl32.Mat4) bool {
	for i := range geoms {
		if modelGeometryBlocks(origin, dir, limit, geoms[i].Vertices, model) {
			return true
		}
	}
	return false
}
