package main

import (
	"log"

	"github.com/go-gl/mathgl/mgl32"
)

// Floor represents a sector's floor surface.
type Floor struct {
	Enabled       bool
	Height        float32
	VertexOffsets []float32 // per-vertex Y delta from Height; len must equal len(sector.PointIndices)
	Texture       string
	Terrain       bool // if true, use terrain displacement mesh
	DoubleSided   bool // if true, floor is rendered from both sides (default true)
	Extrusions          []Extrusion
	TerrainPoints       []TerrainPoint
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
	Enabled       bool
	Height        float32
	VertexOffsets []float32 // per-vertex Y delta from Height; len must equal len(sector.PointIndices)
	Texture       string
	Terrain       bool // if true, use terrain displacement mesh
	DoubleSided   bool // if true, ceiling is rendered from both sides (default true)
	Extrusions          []Extrusion
	TerrainPoints       []TerrainPoint
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
	PointIndices []int    // indices into containing Chunk.Points
	EdgeKinds    []string // len == len(PointIndices); "wall" or "portal" for each edge
	Walls        []Wall
	Portals      []Portal
	Floor        Floor
	Ceiling      Ceiling
	MinBound     mgl32.Vec2
	MaxBound     mgl32.Vec2
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
		if approxEq2(p, pt) {
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

	// Fallback ceiling height for wall tops when ceiling is disabled
	baseCeilH := s.Ceiling.Height
	if !s.Ceiling.Enabled && baseCeilH == s.Floor.Height {
		baseCeilH = s.Floor.Height + 3.0
	}
	ceilEnabled := s.Ceiling.Enabled

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
		var ceilH1, ceilH2 float32
		if ceilEnabled {
			ceilH1 = s.Ceiling.VertexHeight(si1)
			ceilH2 = s.Ceiling.VertexHeight(si2)
		} else {
			ceilH1 = baseCeilH
			ceilH2 = baseCeilH
		}

		if w.Terrain && len(w.TerrainPoints) > 0 {
			// Wall terrain mode — Delaunay displacement mesh
			verts, indices := buildWallTerrainMesh(w, p1, p2, floorH1, floorH2, ceilH1, ceilH2)
			if err := w.Geometry.Load(verts, indices, w.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
		} else if len(w.Extrusions) > 0 {
			// Wall has extrusions — generate complex geometry with holes
			verts, indices, bfList := buildWallWithExtrusions(w, p1, p2, floorH1, floorH2, ceilH1, ceilH2)
			if err := w.Geometry.Load(verts, indices, w.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
			geoms, err := loadExtrusionGeometries(w.ExtrusionGeometries, bfList)
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

			if err := w.Geometry.Load(
				[][]float32{a, b, c, d},
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
			if err := s.Floor.Geometry.Load(verts, indices, s.Floor.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
			// Cache collision mesh for terrain height queries
			s.Floor.CollisionPts, s.Floor.CollisionDepths, s.Floor.CollisionTris = buildFloorCollisionData(points, s.Floor.VertexHeight, s.Floor.Height, s.Floor.TerrainPoints)
		} else if len(s.Floor.Extrusions) > 0 {
			// Floor with extrusions
			verts, indices, bfList := buildFloorWithExtrusions(points, s.Floor.VertexHeight, s.Floor.Height, true, s.Floor.DoubleSided, tileScale, s.Floor.Extrusions)
			if err := s.Floor.Geometry.Load(verts, indices, s.Floor.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
			geoms, err := loadExtrusionGeometries(s.Floor.ExtrusionGeometries, bfList)
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
			indices := make([]int, 0, (len(points)-2)*3)
			for i := 1; i < len(points)-1; i++ {
				indices = append(indices, 0, i, i+1)
			}
			if err := s.Floor.Geometry.Load(verts, indices, s.Floor.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
		}
	}

	// Build ceiling geometry
	if s.Ceiling.Enabled && len(points) >= 3 {
		if s.Ceiling.Terrain && len(s.Ceiling.TerrainPoints) > 0 {
			// Ceiling terrain mode — Delaunay displacement mesh
			verts, indices := buildFloorTerrainMesh(points, s.Ceiling.VertexHeight, s.Ceiling.Height, false, s.Ceiling.DoubleSided, tileScale, s.Ceiling.TerrainPoints)
			if err := s.Ceiling.Geometry.Load(verts, indices, s.Ceiling.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
			// Cache collision mesh for terrain height queries
			s.Ceiling.CollisionPts, s.Ceiling.CollisionDepths, s.Ceiling.CollisionTris = buildFloorCollisionData(points, s.Ceiling.VertexHeight, s.Ceiling.Height, s.Ceiling.TerrainPoints)
		} else if len(s.Ceiling.Extrusions) > 0 {
			// Ceiling with extrusions
			verts, indices, bfList := buildFloorWithExtrusions(points, s.Ceiling.VertexHeight, s.Ceiling.Height, false, s.Ceiling.DoubleSided, tileScale, s.Ceiling.Extrusions)
			if err := s.Ceiling.Geometry.Load(verts, indices, s.Ceiling.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
			geoms, err := loadExtrusionGeometries(s.Ceiling.ExtrusionGeometries, bfList)
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
			indices := make([]int, 0, (len(points)-2)*3)
			for i := len(points) - 2; i >= 1; i-- {
				indices = append(indices, 0, i+1, i)
			}
			if err := s.Ceiling.Geometry.Load(verts, indices, s.Ceiling.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
				return err
			}
		}
	}

	return nil
}

// loadExtrusionGeometries releases old GPU resources and uploads each
// ExtrusionBackFace as a separate Geometry.
func loadExtrusionGeometries(old []Geometry, bfList []ExtrusionBackFace) ([]Geometry, error) {
	for i := range old {
		old[i].Delete()
	}
	if len(bfList) == 0 {
		return nil, nil
	}
	geoms := make([]Geometry, 0, len(bfList))
	for _, bf := range bfList {
		var g Geometry
		if err := g.Load(bf.Vertices, bf.Indices, bf.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
			return nil, err
		}
		geoms = append(geoms, g)
	}
	return geoms, nil
}

// max0 returns i if i >= 0, else 0.
func max0(i int) int {
	if i < 0 {
		return 0
	}
	return i
}
