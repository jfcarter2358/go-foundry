package main

import (
	"foundry/internal/geom"
	"log"

	"github.com/go-gl/mathgl/mgl32"
)

// Floor represents a sector's floor surface.
type Floor struct {
	ScriptData          // tags, attributes and the attached script
	Enabled             bool
	Hidden              bool // if true, the floor's own texture/geometry is not drawn (or shadow-cast); Enabled/collision/terrain are unaffected. Default false = visible — e.g. for a floor fully covered by a biome ground patch (see biome_ground.go) where the structural texture would otherwise show through at the patch's edges.
	Height              float32
	VertexOffsets       []float32 // per-vertex Y delta from Height; len must equal len(sector.PointIndices)
	Texture             string
	Terrain             bool    // if true, use terrain displacement mesh
	DoubleSided         bool    // if true, floor is rendered from both sides (default true)
	Alpha               float32 // 0.0 = fully transparent, 1.0 = fully opaque
	Extrusions          []Extrusion
	TerrainPoints       []TerrainPoint
	UVOffset            mgl32.Vec2 // UV translation
	UVScale             mgl32.Vec2 // UV scale; zero = {1,1}
	UVRotation          float32    // UV rotation in radians
	Detail              DetailSpec // per-face procedural detail (rocks); disabled by default
	Smooth              bool       // shade from interpolated vertex normals (see smooth.go)
	SmoothAngle         float32    // crease threshold in degrees; 0 = geom.DefaultSmoothAngle
	Geometry            Geometry
	ExtrusionGeometries []Geometry

	// Collision cache for terrain mode (populated by Sector.Load)
	CollisionPts    []mgl32.Vec2 // boundary + terrain points in XZ
	CollisionDepths []float32    // depth offset per point (relative to Height)
	CollisionTris   []int        // triangle index triplets from Delaunay
}

// VertexHeight returns the absolute Y position of floor vertex i.
func (f *Floor) VertexHeight(i int) float32 {
	if i >= 0 && i < len(f.VertexOffsets) {
		return f.Height + f.VertexOffsets[i]
	}
	return f.Height
}

// Ceiling represents a sector's ceiling surface.
type Ceiling struct {
	ScriptData          // tags, attributes and the attached script
	Enabled             bool
	Height              float32
	VertexOffsets       []float32 // per-vertex Y delta from Height; len must equal len(sector.PointIndices)
	Texture             string
	Terrain             bool    // if true, use terrain displacement mesh
	DoubleSided         bool    // if true, ceiling is rendered from both sides (default true)
	Alpha               float32 // 0.0 = fully transparent, 1.0 = fully opaque
	Extrusions          []Extrusion
	TerrainPoints       []TerrainPoint
	UVOffset            mgl32.Vec2 // UV translation
	UVScale             mgl32.Vec2 // UV scale; zero = {1,1}
	UVRotation          float32    // UV rotation in radians
	Detail              DetailSpec // per-face procedural detail (rocks); disabled by default
	Smooth              bool       // shade from interpolated vertex normals (see smooth.go)
	SmoothAngle         float32    // crease threshold in degrees; 0 = geom.DefaultSmoothAngle
	Geometry            Geometry
	ExtrusionGeometries []Geometry

	// Collision cache for terrain mode (populated by Sector.Load)
	CollisionPts    []mgl32.Vec2 // boundary + terrain points in XZ
	CollisionDepths []float32    // depth offset per point (relative to Height)
	CollisionTris   []int        // triangle index triplets from Delaunay
}

// VertexHeight returns the absolute Y position of ceiling vertex i.
func (c *Ceiling) VertexHeight(i int) float32 {
	if i >= 0 && i < len(c.VertexOffsets) {
		return c.Height + c.VertexOffsets[i]
	}
	return c.Height
}

// Sector is a convex sub-region of a chunk.
// PointIndices reference positions in the parent Chunk.Points slice.
// Points are listed CCW when viewed from above (XZ plane, Y-up).
// MinBound/MaxBound are 2D (XZ) AABBs used for culling.
// EdgeKinds[i] = "wall" or "portal" for polygon edge i→i+1 (same order as PointIndices).
type Sector struct {
	ID           string
	ScriptData            // tags, attributes and the attached script
	PointIndices []int    // indices into containing Chunk.Points
	EdgeKinds    []string // len == len(PointIndices); "wall" or "portal" for each edge
	// EdgeIDs is index-aligned with PointIndices and EdgeKinds, holding each
	// polygon edge's stable identity — the same arrangement Chunk.PointNames
	// has with Chunk.Points, and for the same reason: Walls and Portals are two
	// separately-compacted slices whose indices shift under nearly every edit,
	// so a name bound to the ring position is the only durable handle. Scripts
	// address an edge by it; see sector_edge_id.go.
	EdgeIDs  []string
	Walls    []Wall
	Portals  []Portal
	Floor    Floor
	Ceiling  Ceiling
	MinBound mgl32.Vec2
	MaxBound mgl32.Vec2
}

// WorldAABB returns the sector's world-space bounds: its XZ polygon AABB
// extended vertically to cover the lowest floor and highest ceiling point,
// including per-vertex offsets, terrain points and extrusions. Used for
// frustum-culling the sector against both the camera and a shadow cascade.
func (s *Sector) WorldAABB() (min, max mgl32.Vec3) {
	minFloor := s.Floor.Height
	for _, off := range s.Floor.VertexOffsets {
		if h := s.Floor.Height + off; h < minFloor {
			minFloor = h
		}
	}
	for _, tp := range s.Floor.TerrainPoints {
		if h := s.Floor.Height + tp.Depth; h < minFloor {
			minFloor = h
		}
	}
	for _, ext := range s.Floor.Extrusions {
		if h := s.Floor.Height - ext.Depth; h < minFloor {
			minFloor = h
		}
	}

	maxCeil := s.Ceiling.Height
	for _, off := range s.Ceiling.VertexOffsets {
		if h := s.Ceiling.Height + off; h > maxCeil {
			maxCeil = h
		}
	}
	for _, tp := range s.Ceiling.TerrainPoints {
		if h := s.Ceiling.Height + tp.Depth; h > maxCeil {
			maxCeil = h
		}
	}
	for _, ext := range s.Ceiling.Extrusions {
		if h := s.Ceiling.Height + ext.Depth; h > maxCeil {
			maxCeil = h
		}
	}

	// Wall tops can sit above a disabled ceiling's plane (see wallTopBase).
	for i := range s.Ceiling.VertexOffsets {
		if h := s.WallTopHeight(i); h > maxCeil {
			maxCeil = h
		}
	}

	return mgl32.Vec3{s.MinBound.X(), minFloor, s.MinBound.Y()},
		mgl32.Vec3{s.MaxBound.X(), maxCeil, s.MaxBound.Y()}
}

// wallTopBase is the flat ceiling plane wall tops hang from. A disabled ceiling
// still positions wall tops; only when it carries no height of its own do walls
// fall back to a fixed height above the floor.
func (s *Sector) wallTopBase() float32 {
	if !s.Ceiling.Enabled && s.Ceiling.Height == s.Floor.Height {
		return s.Floor.Height + 3.0
	}
	return s.Ceiling.Height
}

// WallTopHeight returns the Y position of the top of a wall at sector vertex i.
// Ceiling vertex offsets apply whether or not the ceiling surface itself is
// enabled, so wall tops slope with them either way.
func (s *Sector) WallTopHeight(i int) float32 {
	top := s.wallTopBase()
	if i >= 0 && i < len(s.Ceiling.VertexOffsets) {
		top += s.Ceiling.VertexOffsets[i]
	}
	return top
}

// SectorPoints returns the resolved world-space points for a sector.
func SectorPoints(ch *Chunk, s *Sector) []mgl32.Vec2 {
	pts := make([]mgl32.Vec2, len(s.PointIndices))
	for i, idx := range s.PointIndices {
		if idx >= 0 && idx < len(ch.Points) {
			pts[i] = ch.Points[idx]
		}
	}
	return pts
}

// findVertexIdx returns the index in pts whose position matches pt, or -1.
func findVertexIdx(pts []mgl32.Vec2, pt mgl32.Vec2) int {
	for i, p := range pts {
		if geom.ApproxEq2(p, pt) {
			return i
		}
	}
	return -1
}

// Load computes the sector AABB and uploads geometry for all walls, floor, and ceiling.
// points is the resolved slice of world-space coordinates (from SectorPoints).
func (s *Sector) Load(points []mgl32.Vec2) error {
	// Compute 2D AABB from sector points
	if len(points) > 0 {
		s.MinBound = points[0]
		s.MaxBound = points[0]
		for _, pt := range points[1:] {
			if pt.X() < s.MinBound.X() {
				s.MinBound[0] = pt.X()
			}
			if pt.Y() < s.MinBound.Y() {
				s.MinBound[1] = pt.Y()
			}
			if pt.X() > s.MaxBound.X() {
				s.MaxBound[0] = pt.X()
			}
			if pt.Y() > s.MaxBound.Y() {
				s.MaxBound[1] = pt.Y()
			}
		}
	}

	// Ensure VertexOffsets are the right length (pad with zeros if needed)
	for len(s.Floor.VertexOffsets) < len(points) {
		s.Floor.VertexOffsets = append(s.Floor.VertexOffsets, 0)
	}
	for len(s.Ceiling.VertexOffsets) < len(points) {
		s.Ceiling.VertexOffsets = append(s.Ceiling.VertexOffsets, 0)
	}

	// Smoothing is generated inside Geometry.Load, so each surface's setting has
	// to be on its Geometry before the mesh is uploaded.
	s.Floor.Geometry.Smooth, s.Floor.Geometry.SmoothAngle = s.Floor.Smooth, s.Floor.SmoothAngle
	s.Ceiling.Geometry.Smooth, s.Ceiling.Geometry.SmoothAngle = s.Ceiling.Smooth, s.Ceiling.SmoothAngle
	for i := range s.Walls {
		s.Walls[i].Geometry.Smooth = s.Walls[i].Smooth
		s.Walls[i].Geometry.SmoothAngle = s.Walls[i].SmoothAngle
	}

	// Build wall geometry — each wall is a quad with 4 independent corner heights
	for i := range s.Walls {
		w := &s.Walls[i]
		p1 := w.StartPoint
		p2 := w.EndPoint

		// Find the vertex indices for the two wall endpoints
		si1 := findVertexIdx(points, p1)
		si2 := findVertexIdx(points, p2)
		if si1 < 0 {
			log.Printf("sector %q wall %d: start point not found in sector points; using vertex 0", s.ID, i)
		}
		if si2 < 0 {
			log.Printf("sector %q wall %d: end point not found in sector points; using vertex 0", s.ID, i)
		}

		floorH1 := s.Floor.VertexHeight(si1)
		floorH2 := s.Floor.VertexHeight(si2)
		ceilH1 := s.WallTopHeight(si1)
		ceilH2 := s.WallTopHeight(si2)

		if w.Terrain && len(w.TerrainPoints) > 0 {
			// Wall terrain mode — Delaunay displacement mesh
			verts, indices := buildWallTerrainMesh(w, p1, p2, floorH1, floorH2, ceilH1, ceilH2)
			applyUVTransform(verts, w.UVOffset, w.UVScale, w.UVRotation)
			if err := w.Geometry.Load(verts, indices, w.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
		} else if len(w.Extrusions) > 0 {
			// Wall has extrusions — generate complex geometry with holes
			verts, indices, bfList := buildWallWithExtrusions(w, p1, p2, floorH1, floorH2, ceilH1, ceilH2)
			applyUVTransform(verts, w.UVOffset, w.UVScale, w.UVRotation)
			if err := w.Geometry.Load(verts, indices, w.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
			geoms, err := loadExtrusionGeometries(w.ExtrusionGeometries, bfList, w.Smooth, w.SmoothAngle)
			if err != nil {
				return err
			}
			w.ExtrusionGeometries = geoms
		} else {
			// Simple quad (original path)
			wallLen := mgl32.Vec2{p2.X() - p1.X(), p2.Y() - p1.Y()}.Len()
			wallH1 := ceilH1 - floorH1
			wallH2 := ceilH2 - floorH2

			a := []float32{p1.X(), ceilH1, p1.Y(), 0.0, 0.0}
			b := []float32{p2.X(), ceilH2, p2.Y(), wallLen, 0.0}
			c := []float32{p2.X(), floorH2, p2.Y(), wallLen, wallH2}
			d := []float32{p1.X(), floorH1, p1.Y(), 0.0, wallH1}

			verts := [][]float32{a, b, c, d}
			applyUVTransform(verts, w.UVOffset, w.UVScale, w.UVRotation)
			if err := w.Geometry.Load(
				verts,
				[]int{0, 1, 2, 2, 3, 0},
				w.Texture,
				mgl32.Vec3{0, 0, 0},
			); err != nil {
				return err
			}
		}
	}

	const tileScale = float32(1.0)

	// Build floor geometry
	if s.Floor.Enabled && len(points) >= 3 {
		if s.Floor.Terrain && len(s.Floor.TerrainPoints) > 0 {
			// Floor terrain mode — Delaunay displacement mesh
			verts, indices := buildFloorTerrainMesh(points, s.Floor.VertexHeight, s.Floor.Height, true, s.Floor.DoubleSided, tileScale, s.Floor.TerrainPoints)
			applyUVTransform(verts, s.Floor.UVOffset, s.Floor.UVScale, s.Floor.UVRotation)
			if err := s.Floor.Geometry.Load(verts, indices, s.Floor.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
			// Cache collision mesh for terrain height queries
			s.Floor.CollisionPts, s.Floor.CollisionDepths, s.Floor.CollisionTris = buildFloorCollisionData(points, s.Floor.VertexHeight, s.Floor.Height, s.Floor.TerrainPoints)
		} else if len(s.Floor.Extrusions) > 0 {
			// Floor with extrusions
			verts, indices, bfList := buildFloorWithExtrusions(points, s.Floor.VertexHeight, s.Floor.Height, true, s.Floor.DoubleSided, tileScale, s.Floor.Extrusions)
			applyUVTransform(verts, s.Floor.UVOffset, s.Floor.UVScale, s.Floor.UVRotation)
			if err := s.Floor.Geometry.Load(verts, indices, s.Floor.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
			geoms, err := loadExtrusionGeometries(s.Floor.ExtrusionGeometries, bfList, s.Floor.Smooth, s.Floor.SmoothAngle)
			if err != nil {
				return err
			}
			s.Floor.ExtrusionGeometries = geoms
		} else {
			// Simple triangle fan (original path)
			verts := make([][]float32, len(points))
			for i, pt := range points {
				y := s.Floor.VertexHeight(i)
				verts[i] = []float32{pt.X(), y, pt.Y(), pt.X() / tileScale, pt.Y() / tileScale}
			}
			applyUVTransform(verts, s.Floor.UVOffset, s.Floor.UVScale, s.Floor.UVRotation)
			indices := make([]int, 0, (len(points)-2)*3)
			for i := 1; i < len(points)-1; i++ {
				indices = append(indices, 0, i, i+1)
			}
			if err := s.Floor.Geometry.Load(verts, indices, s.Floor.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
		}
	}

	// Propagate floor alpha to its geometry (extrusion geometries keep their own per-extrusion alpha)
	s.Floor.Geometry.Alpha = s.Floor.Alpha

	// Build ceiling geometry
	if s.Ceiling.Enabled && len(points) >= 3 {
		if s.Ceiling.Terrain && len(s.Ceiling.TerrainPoints) > 0 {
			// Ceiling terrain mode — Delaunay displacement mesh
			verts, indices := buildFloorTerrainMesh(points, s.Ceiling.VertexHeight, s.Ceiling.Height, false, s.Ceiling.DoubleSided, tileScale, s.Ceiling.TerrainPoints)
			applyUVTransform(verts, s.Ceiling.UVOffset, s.Ceiling.UVScale, s.Ceiling.UVRotation)
			if err := s.Ceiling.Geometry.Load(verts, indices, s.Ceiling.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
			// Cache collision mesh for terrain height queries
			s.Ceiling.CollisionPts, s.Ceiling.CollisionDepths, s.Ceiling.CollisionTris = buildFloorCollisionData(points, s.Ceiling.VertexHeight, s.Ceiling.Height, s.Ceiling.TerrainPoints)
		} else if len(s.Ceiling.Extrusions) > 0 {
			// Ceiling with extrusions
			verts, indices, bfList := buildFloorWithExtrusions(points, s.Ceiling.VertexHeight, s.Ceiling.Height, false, s.Ceiling.DoubleSided, tileScale, s.Ceiling.Extrusions)
			applyUVTransform(verts, s.Ceiling.UVOffset, s.Ceiling.UVScale, s.Ceiling.UVRotation)
			if err := s.Ceiling.Geometry.Load(verts, indices, s.Ceiling.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
			geoms, err := loadExtrusionGeometries(s.Ceiling.ExtrusionGeometries, bfList, s.Ceiling.Smooth, s.Ceiling.SmoothAngle)
			if err != nil {
				return err
			}
			s.Ceiling.ExtrusionGeometries = geoms
		} else {
			// Simple triangle fan (original path, reversed winding)
			verts := make([][]float32, len(points))
			for i, pt := range points {
				y := s.Ceiling.VertexHeight(i)
				verts[i] = []float32{pt.X(), y, pt.Y(), pt.X() / tileScale, pt.Y() / tileScale}
			}
			applyUVTransform(verts, s.Ceiling.UVOffset, s.Ceiling.UVScale, s.Ceiling.UVRotation)
			indices := make([]int, 0, (len(points)-2)*3)
			for i := len(points) - 2; i >= 1; i-- {
				indices = append(indices, 0, i+1, i)
			}
			if err := s.Ceiling.Geometry.Load(verts, indices, s.Ceiling.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
		}
	}

	// Propagate ceiling alpha to its geometry (extrusion geometries keep their own per-extrusion alpha)
	s.Ceiling.Geometry.Alpha = s.Ceiling.Alpha

	// Propagate wall alpha to geometry (extrusion geometries keep their own per-extrusion alpha)
	for i := range s.Walls {
		s.Walls[i].Geometry.Alpha = s.Walls[i].Alpha
	}

	// Propagate portal alpha to gap geometry
	for i := range s.Portals {
		s.Portals[i].FloorGap.Alpha = s.Portals[i].Alpha
		s.Portals[i].CeilGap.Alpha = s.Portals[i].Alpha
	}

	return nil
}

// loadExtrusionGeometries releases old GPU resources and uploads each
// ExtrusionBackFace as a separate Geometry. smooth/smoothAngle come from the
// surface the extrusion was cut into, so a smoothed surface's bevels round off
// with it rather than staying faceted next to it.
func loadExtrusionGeometries(old []Geometry, bfList []ExtrusionBackFace, smooth bool, smoothAngle float32) ([]Geometry, error) {
	for i := range old {
		old[i].Delete()
	}
	if len(bfList) == 0 {
		return nil, nil
	}
	geoms := make([]Geometry, 0, len(bfList))
	for _, bf := range bfList {
		var g Geometry
		g.Smooth, g.SmoothAngle = smooth, smoothAngle
		if err := g.Load(bf.Vertices, bf.Indices, bf.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
			return nil, err
		}
		g.Alpha = bf.Alpha
		geoms = append(geoms, g)
	}
	return geoms, nil
}

// InterpolateSurfaceHeight is a query about a surface, not a collision: the
// biome mesher and the editor both ask it with nothing colliding.
// InterpolateSurfaceHeight finds which triangle of the sector's floor or
// ceiling fan contains xz, then returns the barycentric-interpolated height
// and the triangle's surface normal.  If floor is true it operates on the
// floor surface, otherwise the ceiling.
// Returns (height, normal, found).  When found is false the caller should
// fall back to the flat base height.
func InterpolateSurfaceHeight(chunk *Chunk, sector *Sector, xz mgl32.Vec2, floor bool) (float32, mgl32.Vec3, bool) {
	// Use cached terrain mesh when available
	if floor && sector.Floor.Terrain && len(sector.Floor.CollisionTris) > 0 {
		return interpolateTerrainHeight(sector, xz, floor)
	}
	if !floor && sector.Ceiling.Terrain && len(sector.Ceiling.CollisionTris) > 0 {
		return interpolateTerrainHeight(sector, xz, floor)
	}

	pts := SectorPoints(chunk, sector)
	n := len(pts)
	if n < 3 {
		if floor {
			return sector.Floor.Height, mgl32.Vec3{0, 1, 0}, false
		}
		return sector.Ceiling.Height, mgl32.Vec3{0, -1, 0}, false
	}
	// Triangle fan: hub = pts[0], triangles (0, i, i+1)
	for i := 1; i < n-1; i++ {
		inside, wA, wB, wC := geom.PointInTriangle2D(xz, pts[0], pts[i], pts[i+1])
		if !inside {
			continue
		}
		var hA, hB, hC float32
		if floor {
			hA = sector.Floor.VertexHeight(0)
			hB = sector.Floor.VertexHeight(i)
			hC = sector.Floor.VertexHeight(i + 1)
		} else {
			hA = sector.Ceiling.VertexHeight(0)
			hB = sector.Ceiling.VertexHeight(i)
			hC = sector.Ceiling.VertexHeight(i + 1)
		}
		height := wA*hA + wB*hB + wC*hC
		a3 := mgl32.Vec3{pts[0].X(), hA, pts[0].Y()}
		b3 := mgl32.Vec3{pts[i].X(), hB, pts[i].Y()}
		c3 := mgl32.Vec3{pts[i+1].X(), hC, pts[i+1].Y()}
		normal := geom.TriangleNormal(a3, b3, c3)
		// Ensure floor normals point upward, ceiling normals point downward
		if floor && normal.Y() < 0 {
			normal = normal.Mul(-1)
		} else if !floor && normal.Y() > 0 {
			normal = normal.Mul(-1)
		}
		return height, normal, true
	}
	// Fallback: point wasn't inside any fan triangle
	if floor {
		return sector.Floor.Height, mgl32.Vec3{0, 1, 0}, false
	}
	return sector.Ceiling.Height, mgl32.Vec3{0, -1, 0}, false
}

// interpolateTerrainHeight is here rather than with the triangle maths it uses
// because it reads a Sector: it is a query about a floor, not about a triangle.
// interpolateTerrainHeight queries the cached Delaunay collision mesh for a
// terrain-displaced floor or ceiling.  Returns (height, normal, found).
func interpolateTerrainHeight(sector *Sector, xz mgl32.Vec2, floor bool) (float32, mgl32.Vec3, bool) {
	var pts []mgl32.Vec2
	var depths []float32
	var tris []int
	var baseH float32
	if floor {
		pts = sector.Floor.CollisionPts
		depths = sector.Floor.CollisionDepths
		tris = sector.Floor.CollisionTris
		baseH = sector.Floor.Height
	} else {
		pts = sector.Ceiling.CollisionPts
		depths = sector.Ceiling.CollisionDepths
		tris = sector.Ceiling.CollisionTris
		baseH = sector.Ceiling.Height
	}

	for i := 0; i+2 < len(tris); i += 3 {
		ai, bi, ci := tris[i], tris[i+1], tris[i+2]
		if ai >= len(pts) || bi >= len(pts) || ci >= len(pts) {
			continue
		}
		inside, wA, wB, wC := geom.PointInTriangle2D(xz, pts[ai], pts[bi], pts[ci])
		if !inside {
			continue
		}
		hA := baseH + depths[ai]
		hB := baseH + depths[bi]
		hC := baseH + depths[ci]
		height := wA*hA + wB*hB + wC*hC

		a3 := mgl32.Vec3{pts[ai].X(), hA, pts[ai].Y()}
		b3 := mgl32.Vec3{pts[bi].X(), hB, pts[bi].Y()}
		c3 := mgl32.Vec3{pts[ci].X(), hC, pts[ci].Y()}
		normal := geom.TriangleNormal(a3, b3, c3)
		if floor && normal.Y() < 0 {
			normal = normal.Mul(-1)
		} else if !floor && normal.Y() > 0 {
			normal = normal.Mul(-1)
		}
		return height, normal, true
	}

	// Fallback
	if floor {
		return baseH, mgl32.Vec3{0, 1, 0}, false
	}
	return baseH, mgl32.Vec3{0, -1, 0}, false
}
