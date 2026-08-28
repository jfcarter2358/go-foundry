package world

// sector_surface.go — a sector's three surfaces as SurfaceFrames.
//
// Everything that knows how to edit, build or query a surface mesh takes a
// SurfaceFrame, and this is the one place that knows how to make one out of a
// sector. Keeping it here rather than at each call site means the wall frame's
// awkward part — that its V axis hangs off the *wall top*, which is not the
// ceiling when the ceiling is disabled — is written down once.

import "github.com/go-gl/mathgl/mgl32"

// FloorFrame is the sector's floor as a surface. points must be the sector's
// chunk-local outline (SectorPoints).
func (s *Sector) FloorFrame(points []mgl32.Vec2) PlanarSurfaceFrame {
	return NewPlanarSurfaceFrame(points, s.Floor.VertexHeight, s.Floor.Height, SurfaceTileScale, true)
}

// CeilingFrame is the sector's ceiling as a surface.
func (s *Sector) CeilingFrame(points []mgl32.Vec2) PlanarSurfaceFrame {
	return NewPlanarSurfaceFrame(points, s.Ceiling.VertexHeight, s.Ceiling.Height, SurfaceTileScale, false)
}

// WallFrameAt is wall i of the sector as a surface.
//
// The wall's top comes from WallTopHeight rather than Ceiling.VertexHeight: a
// disabled ceiling still positions wall tops, and only when it carries no
// height of its own do walls fall back to a fixed height above the floor. A
// mesh keyed to the ceiling plane instead would slide the moment somebody
// turned the ceiling off.
func (s *Sector) WallFrameAt(points []mgl32.Vec2, i int) (WallSurfaceFrame, bool) {
	if i < 0 || i >= len(s.Walls) {
		return WallSurfaceFrame{}, false
	}
	w := &s.Walls[i]
	vi1 := FindVertexIdx(points, w.StartPoint)
	vi2 := FindVertexIdx(points, w.EndPoint)
	return NewWallSurfaceFrame(
		w.StartPoint, w.EndPoint,
		s.Floor.VertexHeight(vi1), s.Floor.VertexHeight(vi2),
		s.WallTopHeight(vi1), s.WallTopHeight(vi2),
	), true
}

// SurfaceTileScale is how many world units one tile of a floor or ceiling
// texture covers. Walls derive their UVs from distance directly, so this
// applies only to the planar surfaces.
const SurfaceTileScale = float32(1.0)

// SurfaceFrameFor resolves a surface named the way the editor and the script
// host name one — "floor", "ceiling", or "wall" with an index — into a frame.
//
// One lookup rather than three at every call site, because almost everything
// that touches a surface mesh is generic over which surface it is: the editor's
// picking, its gizmos, the undo snapshot and the level writer all work the same
// way on all three, and only this function has to know the difference.
func (s *Sector) SurfaceFrameFor(points []mgl32.Vec2, kind string, idx int) (SurfaceFrame, bool) {
	switch kind {
	case "floor":
		return s.FloorFrame(points), true
	case "ceiling":
		return s.CeilingFrame(points), true
	case "wall":
		f, ok := s.WallFrameAt(points, idx)
		if !ok {
			return nil, false
		}
		return f, true
	}
	return nil, false
}

// SurfaceMeshFor resolves the same naming to the mesh itself, as a pointer so
// callers can edit it in place.
func (s *Sector) SurfaceMeshFor(kind string, idx int) *SurfaceMesh {
	switch kind {
	case "floor":
		return &s.Floor.Mesh
	case "ceiling":
		return &s.Ceiling.Mesh
	case "wall":
		if idx >= 0 && idx < len(s.Walls) {
			return &s.Walls[idx].Mesh
		}
	}
	return nil
}

// SurfaceOpeningsFor is the openings cut in a surface — none for a wall, where
// a hole is a mesh face rather than an Opening.
func (s *Sector) SurfaceOpeningsFor(kind string) []Opening {
	switch kind {
	case "floor":
		return s.Floor.Openings
	case "ceiling":
		return s.Ceiling.Openings
	}
	return nil
}
