package world

import (
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"log"

	"github.com/go-gl/mathgl/mgl32"
)

// Floor represents a sector's floor surface.
type Floor struct {
	ScriptData    // tags, attributes and the attached script
	Enabled       bool
	Hidden        bool // if true, the floor's own texture/geometry is not drawn (or shadow-cast); Enabled/collision/terrain are unaffected. Default false = visible — e.g. for a floor fully covered by a biome ground patch (see biome_ground.go) where the structural texture would otherwise show through at the patch's edges.
	Height        float32
	VertexOffsets []float32 // per-vertex Y delta from Height; len must equal len(sector.PointIndices)
	Texture       string
	BlendTexture  string  // optional second texture, mixed in by BlendMask's luminance
	BlendMask     string  // grayscale mask texture; 0 = Texture, 1 = BlendTexture
	DoubleSided   bool    // if true, floor is rendered from both sides (default true)
	Alpha         float32 // 0.0 = fully transparent, 1.0 = fully opaque
	// Mesh is the surface's editable geometry: the shape an author gave it in
	// the 3D editor's surface mode. The zero value is the flat surface, which
	// is what nearly every one of these is, so carrying the field costs
	// nothing until somebody shapes it. See surface_mesh.go.
	Mesh              SurfaceMesh
	UVOffset          mgl32.Vec2 // UV translation
	UVScale           mgl32.Vec2 // UV scale; zero = {1,1}
	UVRotation        float32    // UV rotation in radians
	Detail            DetailSpec // per-face procedural detail (rocks); disabled by default
	Smooth            bool       // shade from interpolated vertex normals (see smooth.go)
	SmoothAngle       float32    // crease threshold in degrees; 0 = DefaultSmoothAngle
	Geometry          gfx.Geometry
	SubmeshGeometries []gfx.Geometry

	// Openings are holes cut through the floor, connecting this sector to
	// whatever is under it — a stairwell, a hatch, a hole in a mezzanine. They
	// are the vertical counterpart of an edge portal, and are read by geometry,
	// collision and portal culling alike. See opening.go.
	Openings []Opening

	// Skirt closes the band between this sector's floor and whatever is
	// underneath it — the slab edge of a storey, or the rock face under an
	// overhang. SkirtNone (the default) draws nothing, so a level that never
	// asked for one is unchanged. SkirtGeometry is derived and never
	// serialized; see skirt.go.
	Skirt         string
	SkirtTexture  string // empty = the floor's own texture
	SkirtGeometry gfx.Geometry

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
	ScriptData    // tags, attributes and the attached script
	Enabled       bool
	Height        float32
	VertexOffsets []float32 // per-vertex Y delta from Height; len must equal len(sector.PointIndices)
	Texture       string
	BlendTexture  string  // optional second texture, mixed in by BlendMask's luminance
	BlendMask     string  // grayscale mask texture; 0 = Texture, 1 = BlendTexture
	DoubleSided   bool    // if true, ceiling is rendered from both sides (default true)
	Alpha         float32 // 0.0 = fully transparent, 1.0 = fully opaque
	// Mesh is the surface's editable geometry: the shape an author gave it in
	// the 3D editor's surface mode. The zero value is the flat surface, which
	// is what nearly every one of these is, so carrying the field costs
	// nothing until somebody shapes it. See surface_mesh.go.
	Mesh              SurfaceMesh
	UVOffset          mgl32.Vec2 // UV translation
	UVScale           mgl32.Vec2 // UV scale; zero = {1,1}
	UVRotation        float32    // UV rotation in radians
	Detail            DetailSpec // per-face procedural detail (rocks); disabled by default
	Smooth            bool       // shade from interpolated vertex normals (see smooth.go)
	SmoothAngle       float32    // crease threshold in degrees; 0 = DefaultSmoothAngle
	Geometry          gfx.Geometry
	SubmeshGeometries []gfx.Geometry

	// Openings are holes cut through the ceiling, connecting this sector to
	// whatever is above it. The floor's own Openings field, mirrored — see
	// opening.go.
	Openings []Opening

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

	// Layer is which of the owning chunk's authoring bands this sector was
	// drawn on — 0, the ground layer, unless something put it elsewhere. It is
	// what lets one chunk hold sectors that overlap in XZ (an overhang, a
	// mezzanine) without the 2D map becoming a pile of coincident lines.
	//
	// Editor-only. Nothing about where this sector *is* comes from it: which
	// sector a position resolves to is decided geometrically, by the vertical
	// ranking in spatial.go. See layer.go for why that separation matters.
	Layer int
}

// LocalAABB returns the sector's chunk-local bounds: its XZ polygon AABB
// extended vertically to cover the lowest floor and highest ceiling point,
// including per-vertex offsets and whatever shape its surface meshes have.
// See WorldAABB
// for the world-space box carried through the owning chunk's transform —
// used for frustum-culling the sector against both the camera and a shadow
// cascade, and for bucketing it into the spatial hash.
func (s *Sector) LocalAABB() (min, max mgl32.Vec3) {
	minFloor := s.Floor.Height
	for _, off := range s.Floor.VertexOffsets {
		if h := s.Floor.Height + off; h < minFloor {
			minFloor = h
		}
	}
	// A surface mesh may reach below the floor plane — a pit, a stair well
	// carved into it — and a box that did not cover it would let the sector be
	// culled while part of it was still on screen.
	if d, ok := s.Floor.Mesh.MinDepth(); ok {
		if h := s.Floor.Height + d; h < minFloor {
			minFloor = h
		}
	}

	maxCeil := s.Ceiling.Height
	for _, off := range s.Ceiling.VertexOffsets {
		if h := s.Ceiling.Height + off; h > maxCeil {
			maxCeil = h
		}
	}
	if d, ok := s.Ceiling.Mesh.MaxDepth(); ok {
		if h := s.Ceiling.Height + d; h > maxCeil {
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

// WorldAABB is LocalAABB carried through ch's ModelMatrix — the sector's
// bounds in world space (or in ch's owning frame's space, for a frame-owned
// chunk). ch must be the sector's own owning chunk.
func (s *Sector) WorldAABB(ch *Chunk) (min, max mgl32.Vec3) {
	lo, hi := s.LocalAABB()
	return transformAABB(ch.ModelMatrix(), lo, hi)
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

// SectorPoints returns the resolved chunk-local points for a sector.
func SectorPoints(ch *Chunk, s *Sector) []mgl32.Vec2 {
	pts := make([]mgl32.Vec2, len(s.PointIndices))
	for i, idx := range s.PointIndices {
		if idx >= 0 && idx < len(ch.Points) {
			pts[i] = ch.Points[idx]
		}
	}
	return pts
}

// PointInSector is SectorPoints + geom.PointInPolygon without the slice: the
// same ray-cast, reading each vertex straight out of ch.Points through
// s.PointIndices. Semantics are identical — this exists purely because
// SectorPoints allocates, and the callers below it are hot enough for that to
// dominate.
//
// How hot: a biome sector's height/coverage texture is rebuilt whenever the
// floor under it moves (grass.go's ensureBiomeHeight), and every one of its
// texels — up to 2048x2048 — resolves its floor by scanning every sector in
// the level (FloorHeightNormalUnderLevel -> FindSector). Materializing a
// polygon per sector per texel made a single modest sector's rebuild ~440k
// allocations, which is what a vertex drag over painted ground felt like.
func PointInSector(ch *Chunk, s *Sector, xz mgl32.Vec2) bool {
	if ch == nil || s == nil {
		return false
	}
	// SectorPoints leaves an out-of-range index as the zero vector, so the
	// ray-cast has to see the same thing to agree with it.
	at := func(i int) (x, y float32) {
		idx := s.PointIndices[i]
		if idx < 0 || idx >= len(ch.Points) {
			return 0, 0
		}
		p := ch.Points[idx]
		return p.X(), p.Y()
	}

	n := len(s.PointIndices)
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		xi, yi := at(i)
		xj, yj := at(j)
		if ((yi > xz.Y()) != (yj > xz.Y())) &&
			(xz.X() < (xj-xi)*(xz.Y()-yi)/(yj-yi)+xi) {
			inside = !inside
		}
		j = i
	}
	return inside
}

// FindVertexIdx returns the index in pts whose position matches pt, or -1.
func FindVertexIdx(pts []mgl32.Vec2, pt mgl32.Vec2) int {
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
		si1 := FindVertexIdx(points, p1)
		si2 := FindVertexIdx(points, p2)
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

		frame := NewWallSurfaceFrame(p1, p2, floorH1, floorH2, ceilH1, ceilH2)
		if err := loadSurfaceGeometry(surfaceBuild{
			Frame:       frame,
			Mesh:        &w.Mesh,
			DoubleSided: w.DoubleSided,
			Texture:     w.Texture,
			UVOffset:    w.UVOffset,
			UVScale:     w.UVScale,
			UVRotation:  w.UVRotation,
			Smooth:      w.Smooth,
			SmoothAngle: w.SmoothAngle,
			Geometry:    &w.Geometry,
			Submeshes:   &w.SubmeshGeometries,
		}); err != nil {
			return err
		}
	}

	const tileScale = float32(1.0)

	// Build floor geometry
	if s.Floor.Enabled && len(points) >= 3 {
		frame := s.FloorFrame(points)
		if err := loadSurfaceGeometry(surfaceBuild{
			Frame:       frame,
			Mesh:        &s.Floor.Mesh,
			DoubleSided: s.Floor.DoubleSided,
			Openings:    s.Floor.Openings,
			Texture:     s.Floor.Texture,
			UVOffset:    s.Floor.UVOffset,
			UVScale:     s.Floor.UVScale,
			UVRotation:  s.Floor.UVRotation,
			Smooth:      s.Floor.Smooth,
			SmoothAngle: s.Floor.SmoothAngle,
			Geometry:    &s.Floor.Geometry,
			Submeshes:   &s.Floor.SubmeshGeometries,
		}); err != nil {
			return err
		}
		// The height a body stands on comes from the same triangles that were
		// just drawn, so a bump you can see is a bump you can walk up.
		s.Floor.CollisionPts, s.Floor.CollisionDepths, s.Floor.CollisionTris =
			SurfaceCollisionMesh(frame, &s.Floor.Mesh, s.Floor.Openings)
	}

	// Propagate floor alpha to its geometry (submeshes keep their own face alpha)
	s.Floor.Geometry.Alpha = s.Floor.Alpha
	s.Floor.Geometry.BlendTexture = s.Floor.BlendTexture
	s.Floor.Geometry.BlendMask = s.Floor.BlendMask

	// Build ceiling geometry
	if s.Ceiling.Enabled && len(points) >= 3 {
		frame := s.CeilingFrame(points)
		if err := loadSurfaceGeometry(surfaceBuild{
			Frame:       frame,
			Mesh:        &s.Ceiling.Mesh,
			DoubleSided: s.Ceiling.DoubleSided,
			Openings:    s.Ceiling.Openings,
			Texture:     s.Ceiling.Texture,
			UVOffset:    s.Ceiling.UVOffset,
			UVScale:     s.Ceiling.UVScale,
			UVRotation:  s.Ceiling.UVRotation,
			Smooth:      s.Ceiling.Smooth,
			SmoothAngle: s.Ceiling.SmoothAngle,
			Geometry:    &s.Ceiling.Geometry,
			Submeshes:   &s.Ceiling.SubmeshGeometries,
		}); err != nil {
			return err
		}
		s.Ceiling.CollisionPts, s.Ceiling.CollisionDepths, s.Ceiling.CollisionTris =
			SurfaceCollisionMesh(frame, &s.Ceiling.Mesh, s.Ceiling.Openings)
	}

	// Propagate ceiling alpha to its geometry (submeshes keep their own face alpha)
	s.Ceiling.Geometry.Alpha = s.Ceiling.Alpha
	s.Ceiling.Geometry.BlendTexture = s.Ceiling.BlendTexture
	s.Ceiling.Geometry.BlendMask = s.Ceiling.BlendMask

	// Propagate wall alpha to geometry (submeshes keep their own face alpha)
	for i := range s.Walls {
		s.Walls[i].Geometry.Alpha = s.Walls[i].Alpha
		s.Walls[i].Geometry.BlendTexture = s.Walls[i].BlendTexture
		s.Walls[i].Geometry.BlendMask = s.Walls[i].BlendMask
	}

	// Propagate portal alpha to gap geometry
	for i := range s.Portals {
		s.Portals[i].FloorGap.Alpha = s.Portals[i].Alpha
		s.Portals[i].CeilGap.Alpha = s.Portals[i].Alpha
	}

	return nil
}

// surfaceBuild is everything loadSurfaceGeometry needs about one surface.
//
// A struct rather than a dozen positional arguments because the three surfaces
// differ in only two of these — whether they take openings, and which fields
// they live in — and a call site that reads as a list of named properties is
// one where a floor's smoothing cannot silently be passed as a ceiling's.
type surfaceBuild struct {
	Frame       SurfaceFrame
	Mesh        *SurfaceMesh
	DoubleSided bool
	Openings    []Opening
	Texture     string
	UVOffset    mgl32.Vec2
	UVScale     mgl32.Vec2
	UVRotation  float32
	Smooth      bool
	SmoothAngle float32
	Geometry    *gfx.Geometry
	Submeshes   *[]gfx.Geometry
}

// loadSurfaceGeometry compiles one surface's mesh and uploads it, together with
// a Geometry per face that overrides the surface's texture.
//
// This is the single path all three surfaces now take. It replaced a three-way
// branch per surface — terrain, extruded, or flat — that each had to be kept in
// step with the others and none of which could be combined.
func loadSurfaceGeometry(b surfaceBuild) error {
	verts, indices, subs := BuildSurfaceMesh(b.Frame, b.Mesh, b.DoubleSided, b.Openings)
	ApplyUVTransform(verts, b.UVOffset, b.UVScale, b.UVRotation)
	b.Geometry.Smooth, b.Geometry.SmoothAngle = b.Smooth, b.SmoothAngle
	if err := b.Geometry.Load(verts, indices, b.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
		return err
	}
	geoms, err := loadSurfaceSubmeshes(*b.Submeshes, subs, b)
	if err != nil {
		return err
	}
	*b.Submeshes = geoms
	return nil
}

// loadSurfaceSubmeshes releases the old GPU resources and uploads each
// overriding-texture run as a separate Geometry. Smoothing comes from the
// surface the faces belong to, so a smoothed surface's bevels round off with it
// rather than staying faceted next to it.
func loadSurfaceSubmeshes(old []gfx.Geometry, subs []SurfaceSubmesh, b surfaceBuild) ([]gfx.Geometry, error) {
	for i := range old {
		old[i].Delete()
	}
	if len(subs) == 0 {
		return nil, nil
	}
	geoms := make([]gfx.Geometry, 0, len(subs))
	for _, sub := range subs {
		ApplyUVTransform(sub.Vertices, b.UVOffset, b.UVScale, b.UVRotation)
		var g gfx.Geometry
		g.Smooth, g.SmoothAngle = b.Smooth, b.SmoothAngle
		if err := g.Load(sub.Vertices, sub.Indices, sub.Texture, mgl32.Vec3{0, 0, 0}); err != nil {
			return nil, err
		}
		g.Alpha = sub.Alpha
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
//
// Every coordinate is CHUNK-LOCAL: xz goes in in chunk's own space, and the
// height and normal come back in it. Nothing here consults chunk.ModelMatrix
// — a caller holding a world position converts it first (chunk.WorldToLocal)
// and converts the answer back, and a caller mixing results from two
// different chunks has to carry them into one frame before comparing them.
func InterpolateSurfaceHeight(chunk *Chunk, sector *Sector, xz mgl32.Vec2, floor bool) (float32, mgl32.Vec3, bool) {
	// A shaped surface answers from the triangles it was actually built out of.
	// The fan below is the flat case only — it interpolates the sector's corner
	// heights, which is right for a plane and wrong for everything an author
	// carved into one.
	if floor && len(sector.Floor.CollisionTris) > 0 {
		return interpolateSurfaceMeshHeight(sector, xz, floor)
	}
	if !floor && len(sector.Ceiling.CollisionTris) > 0 {
		return interpolateSurfaceMeshHeight(sector, xz, floor)
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

// interpolateSurfaceMeshHeight is here rather than with the triangle maths it
// uses because it reads a Sector: it is a query about a floor, not about a
// triangle.
//
// It queries the collision cache SurfaceCollisionMesh filled from the surface's
// own mesh — the same triangles that were drawn, so what a body stands on and
// what the eye sees are the same surface. Returns (height, normal, found).
func interpolateSurfaceMeshHeight(sector *Sector, xz mgl32.Vec2, floor bool) (float32, mgl32.Vec3, bool) {
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
