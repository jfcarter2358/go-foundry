package main

// extrusion.go — geometry generation for wall extrusions.
//
// Each extrusion cuts a hole in the wall quad and generates side faces
// connecting the outer polygon (on the wall surface) to the inner polygon
// (at the extruded depth), plus an optional back face.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// ExtrusionBackFace holds per-extrusion back face geometry data
// that should be rendered with the extrusion's custom texture.
type ExtrusionBackFace struct {
	Texture  string
	Vertices [][]float32
	Indices  []int
	Alpha    float32
}

// wallFrame computes the wall's local coordinate frame vectors.
// wallDir is the normalised direction from p1→p2 in XZ.
// wallNorm is the outward-facing normal (perpendicular in XZ, right-hand rule).
func wallFrame(p1, p2 mgl32.Vec2) (wallDir, wallNorm mgl32.Vec2) {
	dx := p2.X() - p1.X()
	dz := p2.Y() - p1.Y()
	l := float32(math.Sqrt(float64(dx*dx + dz*dz)))
	if l < 1e-9 {
		return mgl32.Vec2{1, 0}, mgl32.Vec2{0, -1}
	}
	wallDir = mgl32.Vec2{dx / l, dz / l}
	// Normal: rotate wallDir 90° CW in XZ → outward for CCW-wound sector
	wallNorm = mgl32.Vec2{-wallDir.Y(), wallDir.X()}
	return
}

// wallLocalToWorld converts wall-local (u, v, depth) to world (X, Y, Z).
//
// u: distance along wall (0 at p1, wallLen at p2)
// v: distance from ceiling downward (0 at ceiling, wallH at floor)
// depth: displacement along wall outward normal
//
// Heights are linearly interpolated between the two wall endpoints.
func wallLocalToWorld(
	u, v, depth float32,
	p1, p2 mgl32.Vec2,
	dir, norm mgl32.Vec2,
	wallLen float32,
	floorH1, floorH2, ceilH1, ceilH2 float32,
) (x, y, z float32) {
	t := u / wallLen
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	cH := ceilH1 + (ceilH2-ceilH1)*t
	y = cH - v
	x = p1.X() + dir.X()*u + norm.X()*depth
	z = p1.Y() + dir.Y()*u + norm.Y()*depth
	return
}

// wallLocalVertex builds a [X,Y,Z,U,V] vertex from wall-local coordinates.
func wallLocalVertex(
	u, v, depth float32,
	p1, p2 mgl32.Vec2,
	dir, norm mgl32.Vec2,
	wallLen float32,
	floorH1, floorH2, ceilH1, ceilH2 float32,
) []float32 {
	x, y, z := wallLocalToWorld(u, v, depth, p1, p2, dir, norm, wallLen, floorH1, floorH2, ceilH1, ceilH2)
	return []float32{x, y, z, u, v}
}

// buildWallWithExtrusions generates all geometry for a wall that has extrusions.
// It returns (vertices, indices) ready for Geometry.Load.
func buildWallWithExtrusions(
	w *Wall,
	p1, p2 mgl32.Vec2,
	floorH1, floorH2, ceilH1, ceilH2 float32,
) ([][]float32, []int, []ExtrusionBackFace) {
	dir, norm := wallFrame(p1, p2)
	wallLen := mgl32.Vec2{p2.X() - p1.X(), p2.Y() - p1.Y()}.Len()
	wallH1 := ceilH1 - floorH1
	wallH2 := ceilH2 - floorH2
	// Use average wall height for the quad's V extent
	wallH := (wallH1 + wallH2) / 2

	var allVerts [][]float32
	var allIndices []int
	var backFaces []ExtrusionBackFace

	// Helper to add a vertex and return its index
	addVert := func(v []float32) int {
		idx := len(allVerts)
		allVerts = append(allVerts, v)
		return idx
	}

	// Helper to add a triangle
	addTri := func(a, b, c int) {
		allIndices = append(allIndices, a, b, c)
	}

	// --- 1. Front face: wall quad with extrusion holes ---
	outerQuad := []mgl32.Vec2{
		{0, 0},
		{wallLen, 0},
		{wallLen, wallH},
		{0, wallH},
	}

	var holes [][]mgl32.Vec2
	for _, ext := range w.Extrusions {
		hole := make([]mgl32.Vec2, len(ext.Outer))
		for i, p := range ext.Outer {
			hole[i] = mgl32.Vec2{p.U, p.V}
		}
		// Holes must be CW for mergeHoles; reverse if CCW
		if signedArea2D_UV(hole) > 0 {
			reverseVec2Slice(hole)
		}
		holes = append(holes, hole)
	}

	merged, triIdx := earClipWithHoles(outerQuad, holes)
	// Map merged polygon points to world-space vertices
	frontBase := len(allVerts)
	for _, uv := range merged {
		v := wallLocalVertex(uv.X(), uv.Y(), 0, p1, p2, dir, norm, wallLen, floorH1, floorH2, ceilH1, ceilH2)
		addVert(v)
	}
	for _, ti := range triIdx {
		allIndices = append(allIndices, frontBase+ti)
	}

	// --- 2. Back face (if wall is double-sided) ---
	if w.DoubleSided {
		backOuter := []mgl32.Vec2{
			{0, 0},
			{wallLen, 0},
			{wallLen, wallH},
			{0, wallH},
		}

		backMerged, backTriIdx := earClipWithHoles(backOuter, holes)
		backBase := len(allVerts)
		for _, uv := range backMerged {
			v := wallLocalVertex(uv.X(), uv.Y(), 0, p1, p2, dir, norm, wallLen, floorH1, floorH2, ceilH1, ceilH2)
			addVert(v)
		}
		// Reversed winding for back face
		for i := 0; i < len(backTriIdx); i += 3 {
			allIndices = append(allIndices, backBase+backTriIdx[i+2], backBase+backTriIdx[i+1], backBase+backTriIdx[i])
		}
	}

	// --- 3. Per-extrusion geometry: sides + optional back face ---
	for _, ext := range w.Extrusions {
		nPts := len(ext.Outer)
		if nPts < 3 || len(ext.Inner) != nPts {
			continue
		}

		extSideStart := len(allVerts) // track start for UV transform

		// Side faces: quads connecting outer[i]→inner[i]→inner[i+1]→outer[i+1]
		for i := 0; i < nPts; i++ {
			j := (i + 1) % nPts
			outerI := ext.Outer[i]
			outerJ := ext.Outer[j]
			innerI := ext.Inner[i]
			innerJ := ext.Inner[j]

			// Four corners of the side quad
			v0 := wallLocalVertex(outerI.U, outerI.V, 0, p1, p2, dir, norm, wallLen, floorH1, floorH2, ceilH1, ceilH2)
			v1 := wallLocalVertex(outerJ.U, outerJ.V, 0, p1, p2, dir, norm, wallLen, floorH1, floorH2, ceilH1, ceilH2)
			v2 := wallLocalVertex(innerJ.U, innerJ.V, ext.Depth, p1, p2, dir, norm, wallLen, floorH1, floorH2, ceilH1, ceilH2)
			v3 := wallLocalVertex(innerI.U, innerI.V, ext.Depth, p1, p2, dir, norm, wallLen, floorH1, floorH2, ceilH1, ceilH2)

			// UV for side faces: use edge length for U, depth for V
			edgeLen := float32(math.Sqrt(float64(
				(outerJ.U-outerI.U)*(outerJ.U-outerI.U) + (outerJ.V-outerI.V)*(outerJ.V-outerI.V))))
			absDepth := float32(math.Abs(float64(ext.Depth)))

			v0[3], v0[4] = 0, 0
			v1[3], v1[4] = edgeLen, 0
			v2[3], v2[4] = edgeLen, absDepth
			v3[3], v3[4] = 0, absDepth

			i0 := addVert(v0)
			i1 := addVert(v1)
			i2 := addVert(v2)
			i3 := addVert(v3)

			// Winding depends on depth sign — inward extrusions face inward
			if ext.Depth < 0 {
				addTri(i0, i1, i2)
				addTri(i2, i3, i0)
			} else {
				addTri(i0, i3, i2)
				addTri(i2, i1, i0)
			}

			// If double-sided wall with invisible back, emit reverse winding too
			// so the tunnel is visible from both sides
			if w.DoubleSided && !ext.BackVisible {
				if ext.Depth < 0 {
					addTri(i0, i2, i1)
					addTri(i2, i0, i3)
				} else {
					addTri(i0, i2, i3)
					addTri(i2, i0, i1)
				}
			}
		}

		// Back face (the inner polygon at depth) — rendered when the extrusion
		// is visible, or when explicitly requested via BackVisible.
		if ext.Visible || ext.BackVisible {
			innerPoly := make([]mgl32.Vec2, nPts)
			for i, p := range ext.Inner {
				innerPoly[i] = mgl32.Vec2{p.U, p.V}
			}
			backTris := earClip(innerPoly)

			if ext.Texture != "" {
				// Separate geometry for custom extrusion texture
				bf := ExtrusionBackFace{Texture: ext.Texture, Alpha: ext.Alpha}
				for _, p := range ext.Inner {
					v := wallLocalVertex(p.U, p.V, ext.Depth, p1, p2, dir, norm, wallLen, floorH1, floorH2, ceilH1, ceilH2)
					bf.Vertices = append(bf.Vertices, v)
				}
				if ext.Depth < 0 {
					for i := 0; i < len(backTris); i += 3 {
						bf.Indices = append(bf.Indices, backTris[i+2], backTris[i+1], backTris[i])
					}
				} else {
					bf.Indices = append(bf.Indices, backTris...)
				}
				applyUVTransform(bf.Vertices, ext.UVOffset, ext.UVScale, ext.UVRotation)
				backFaces = append(backFaces, bf)
			} else {
				// No custom texture — merge into main geometry
				backBase := len(allVerts)
				for _, p := range ext.Inner {
					v := wallLocalVertex(p.U, p.V, ext.Depth, p1, p2, dir, norm, wallLen, floorH1, floorH2, ceilH1, ceilH2)
					addVert(v)
				}
				if ext.Depth < 0 {
					for i := 0; i < len(backTris); i += 3 {
						allIndices = append(allIndices, backBase+backTris[i+2], backBase+backTris[i+1], backBase+backTris[i])
					}
				} else {
					for i := 0; i < len(backTris); i += 3 {
						allIndices = append(allIndices, backBase+backTris[i], backBase+backTris[i+1], backBase+backTris[i+2])
					}
				}
			}
		}

		// Apply per-extrusion UV transform to side faces and merged back faces
		applyUVTransform(allVerts[extSideStart:], ext.UVOffset, ext.UVScale, ext.UVRotation)
	}

	return allVerts, allIndices, backFaces
}

// signedArea2D_UV computes the signed area of a 2D polygon in UV space.
// Positive = CCW; negative = CW.
func signedArea2D_UV(pts []mgl32.Vec2) float32 {
	n := len(pts)
	area := float32(0)
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		area += pts[i].X()*pts[j].Y() - pts[j].X()*pts[i].Y()
	}
	return area / 2
}

// reverseVec2Slice reverses a slice of Vec2 in place.
func reverseVec2Slice(s []mgl32.Vec2) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// ---------- Floor/ceiling extrusion geometry ----------

// buildFloorWithExtrusions generates floor geometry with extrusion holes.
// points: sector polygon vertices in XZ (world space).
// heightFunc: returns the Y height for a given point index (-1 for non-boundary pts).
// baseHeight: default height for non-boundary points.
// normalUp: true for floors (normal +Y), false for ceilings (normal -Y).
// doubleSided: whether the surface is double-sided.
// tileScale: UV tiling scale.
func buildFloorWithExtrusions(
	points []mgl32.Vec2,
	heightFunc func(int) float32,
	baseHeight float32,
	normalUp bool,
	doubleSided bool,
	tileScale float32,
	extrusions []Extrusion,
) ([][]float32, []int, []ExtrusionBackFace) {
	var allVerts [][]float32
	var allIndices []int
	var backFaces []ExtrusionBackFace

	addVert := func(v []float32) int {
		idx := len(allVerts)
		allVerts = append(allVerts, v)
		return idx
	}
	addTri := func(a, b, c int) {
		allIndices = append(allIndices, a, b, c)
	}

	// Build outer polygon in XZ
	outer := make([]mgl32.Vec2, len(points))
	copy(outer, points)

	// Build holes from extrusion outer polygons
	var holes [][]mgl32.Vec2
	for _, ext := range extrusions {
		hole := make([]mgl32.Vec2, len(ext.Outer))
		for i, p := range ext.Outer {
			hole[i] = mgl32.Vec2{p.U, p.V}
		}
		// Holes must be CW for floors (CCW boundary), or CCW for ceilings (CW boundary after reversal)
		if normalUp {
			// Floor: outer is CCW, holes must be CW
			if signedArea2D_UV(hole) > 0 {
				reverseVec2Slice(hole)
			}
		} else {
			// Ceiling: outer is CW (reversed), holes must be CCW
			if signedArea2D_UV(hole) < 0 {
				reverseVec2Slice(hole)
			}
		}
		holes = append(holes, hole)
	}

	// Ensure outer polygon has correct winding
	if normalUp {
		// Floor: CCW
		if signedArea2D_UV(outer) < 0 {
			reverseVec2Slice(outer)
		}
	} else {
		// Ceiling: CW (so face points downward)
		if signedArea2D_UV(outer) > 0 {
			reverseVec2Slice(outer)
		}
	}

	merged, triIdx := earClipWithHoles(outer, holes)
	base := len(allVerts)
	for _, pt := range merged {
		// Find matching boundary point for height
		y := baseHeight
		for pi, bp := range points {
			dx := bp.X() - pt.X()
			dz := bp.Y() - pt.Y()
			if dx*dx+dz*dz < 1e-6 {
				y = heightFunc(pi)
				break
			}
		}
		allVerts = append(allVerts, []float32{pt.X(), y, pt.Y(), pt.X() / tileScale, pt.Y() / tileScale})
	}
	for _, ti := range triIdx {
		allIndices = append(allIndices, base+ti)
	}

	// Double-sided: emit reverse winding
	if doubleSided {
		revBase := len(allVerts)
		for i := base; i < base+len(merged); i++ {
			v := make([]float32, 5)
			copy(v, allVerts[i])
			allVerts = append(allVerts, v)
		}
		for i := 0; i < len(triIdx); i += 3 {
			allIndices = append(allIndices, revBase+triIdx[i+2], revBase+triIdx[i+1], revBase+triIdx[i])
		}
	}

	// Per-extrusion side faces and back face
	for _, ext := range extrusions {
		nPts := len(ext.Outer)
		if nPts < 3 || len(ext.Inner) != nPts {
			continue
		}

		extSideStart := len(allVerts)

		depthDir := float32(1)
		if !normalUp {
			depthDir = -1 // ceilings: depth goes upward (positive Y)
		}
		depthY := ext.Depth * depthDir

		// Side faces
		for i := 0; i < nPts; i++ {
			j := (i + 1) % nPts
			outerI := ext.Outer[i]
			outerJ := ext.Outer[j]
			innerI := ext.Inner[i]
			innerJ := ext.Inner[j]

			v0 := []float32{outerI.U, baseHeight, outerI.V, outerI.U / tileScale, outerI.V / tileScale}
			v1 := []float32{outerJ.U, baseHeight, outerJ.V, outerJ.U / tileScale, outerJ.V / tileScale}
			v2 := []float32{innerJ.U, baseHeight - depthY, innerJ.V, innerJ.U / tileScale, innerJ.V / tileScale}
			v3 := []float32{innerI.U, baseHeight - depthY, innerI.V, innerI.U / tileScale, innerI.V / tileScale}

			i0 := addVert(v0)
			i1 := addVert(v1)
			i2 := addVert(v2)
			i3 := addVert(v3)

			if normalUp {
				// Floor: depth goes downward → side normals face inward
				addTri(i0, i1, i2)
				addTri(i2, i3, i0)
			} else {
				addTri(i0, i3, i2)
				addTri(i2, i1, i0)
			}

			// Double-sided sides for cutthrough
			if doubleSided && !ext.BackVisible {
				if normalUp {
					addTri(i0, i2, i1)
					addTri(i2, i0, i3)
				} else {
					addTri(i0, i2, i3)
					addTri(i2, i0, i1)
				}
			}
		}

		// Back face — rendered when the extrusion is visible, or when
		// explicitly requested via BackVisible.
		if ext.Visible || ext.BackVisible {
			innerPoly := make([]mgl32.Vec2, nPts)
			for i, p := range ext.Inner {
				innerPoly[i] = mgl32.Vec2{p.U, p.V}
			}
			backTris := earClip(innerPoly)

			if ext.Texture != "" {
				// Separate geometry for custom extrusion texture
				bf := ExtrusionBackFace{Texture: ext.Texture, Alpha: ext.Alpha}
				for _, p := range ext.Inner {
					bf.Vertices = append(bf.Vertices, []float32{p.U, baseHeight - depthY, p.V, p.U / tileScale, p.V / tileScale})
				}
				if normalUp {
					for i := 0; i < len(backTris); i += 3 {
						bf.Indices = append(bf.Indices, backTris[i+2], backTris[i+1], backTris[i])
					}
				} else {
					bf.Indices = append(bf.Indices, backTris...)
				}
				applyUVTransform(bf.Vertices, ext.UVOffset, ext.UVScale, ext.UVRotation)
				backFaces = append(backFaces, bf)
			} else {
				// No custom texture — merge into main geometry
				backBase := len(allVerts)
				for _, p := range ext.Inner {
					allVerts = append(allVerts, []float32{p.U, baseHeight - depthY, p.V, p.U / tileScale, p.V / tileScale})
				}
				if normalUp {
					for i := 0; i < len(backTris); i += 3 {
						allIndices = append(allIndices, backBase+backTris[i+2], backBase+backTris[i+1], backBase+backTris[i])
					}
				} else {
					for i := 0; i < len(backTris); i += 3 {
						allIndices = append(allIndices, backBase+backTris[i], backBase+backTris[i+1], backBase+backTris[i+2])
					}
				}
			}
		}

		// Apply per-extrusion UV transform to side faces and merged back faces
		applyUVTransform(allVerts[extSideStart:], ext.UVOffset, ext.UVScale, ext.UVRotation)
	}

	return allVerts, allIndices, backFaces
}

// ---------- Terrain displacement mesh ----------

// buildTerrainMesh generates a Delaunay-triangulated displacement mesh for a surface.
// boundary: the polygon boundary in 2D (XZ for floors/ceilings, UV for walls).
// boundaryHeights: height (Y for floors, or depth=0 for walls) for each boundary point.
// terrainPts: additional interior points with depth displacement.
// toVertex: converts (2D point, height/depth) → [X,Y,Z,U,V] vertex.
// normalUp: affects winding order.
// doubleSided: emit both windings.
func buildTerrainMesh(
	boundary []mgl32.Vec2,
	boundaryDepths []float32,
	terrainPts []TerrainPoint,
	toVertex func(pt mgl32.Vec2, depth float32) []float32,
	normalUp bool,
	doubleSided bool,
) ([][]float32, []int) {
	// Collect all points: boundary + terrain
	nBoundary := len(boundary)
	nTotal := nBoundary + len(terrainPts)
	allPts := make([]mgl32.Vec2, nTotal)
	allDepths := make([]float32, nTotal)
	copy(allPts, boundary)
	copy(allDepths, boundaryDepths)
	for i, tp := range terrainPts {
		allPts[nBoundary+i] = mgl32.Vec2{tp.U, tp.V}
		allDepths[nBoundary+i] = tp.Depth
	}

	// Delaunay triangulate with boundary clipping
	triIdx := delaunayTriangulate(allPts, boundary)

	// Build vertices
	var allVerts [][]float32
	for i, pt := range allPts {
		allVerts = append(allVerts, toVertex(pt, allDepths[i]))
	}

	// Build index list with correct winding
	var allIndices []int
	if normalUp {
		allIndices = append(allIndices, triIdx...)
	} else {
		// Reverse winding for downward-facing surfaces
		for i := 0; i < len(triIdx); i += 3 {
			allIndices = append(allIndices, triIdx[i], triIdx[i+2], triIdx[i+1])
		}
	}

	// Double-sided: emit reverse winding
	if doubleSided {
		for i := 0; i < len(triIdx); i += 3 {
			if normalUp {
				allIndices = append(allIndices, triIdx[i], triIdx[i+2], triIdx[i+1])
			} else {
				allIndices = append(allIndices, triIdx[i], triIdx[i+1], triIdx[i+2])
			}
		}
	}

	return allVerts, allIndices
}

// buildFloorCollisionData returns the raw Delaunay triangulation data for a
// floor or ceiling with terrain points, suitable for collision queries.
// Returns (pts, depths, tris) where pts are XZ positions, depths are offsets
// from baseHeight, and tris are triangle index triplets.
func buildFloorCollisionData(
	points []mgl32.Vec2,
	heightFunc func(int) float32,
	baseHeight float32,
	terrainPts []TerrainPoint,
) ([]mgl32.Vec2, []float32, []int) {
	boundary := make([]mgl32.Vec2, len(points))
	boundaryDepths := make([]float32, len(points))
	copy(boundary, points)
	for i := range points {
		boundaryDepths[i] = heightFunc(i) - baseHeight
	}

	// Ensure CCW winding (Delaunay expects this for clipping)
	if signedArea2D_UV(boundary) < 0 {
		reverseVec2Slice(boundary)
		for i, j := 0, len(boundaryDepths)-1; i < j; i, j = i+1, j-1 {
			boundaryDepths[i], boundaryDepths[j] = boundaryDepths[j], boundaryDepths[i]
		}
	}

	// Collect all points: boundary + terrain
	nBoundary := len(boundary)
	nTotal := nBoundary + len(terrainPts)
	allPts := make([]mgl32.Vec2, nTotal)
	allDepths := make([]float32, nTotal)
	copy(allPts, boundary)
	copy(allDepths, boundaryDepths)
	for i, tp := range terrainPts {
		allPts[nBoundary+i] = mgl32.Vec2{tp.U, tp.V}
		allDepths[nBoundary+i] = tp.Depth
	}

	tris := delaunayTriangulate(allPts, boundary)
	return allPts, allDepths, tris
}

// buildWallTerrainMesh generates a terrain displacement mesh for a wall.
func buildWallTerrainMesh(
	w *Wall,
	p1, p2 mgl32.Vec2,
	floorH1, floorH2, ceilH1, ceilH2 float32,
) ([][]float32, []int) {
	dir, norm := wallFrame(p1, p2)
	wallLen := mgl32.Vec2{p2.X() - p1.X(), p2.Y() - p1.Y()}.Len()
	wallH1 := ceilH1 - floorH1
	wallH2 := ceilH2 - floorH2
	wallH := (wallH1 + wallH2) / 2

	// Wall quad boundary in UV space
	boundary := []mgl32.Vec2{
		{0, 0},
		{wallLen, 0},
		{wallLen, wallH},
		{0, wallH},
	}
	boundaryDepths := []float32{0, 0, 0, 0}

	toVertex := func(pt mgl32.Vec2, depth float32) []float32 {
		return wallLocalVertex(pt.X(), pt.Y(), depth, p1, p2, dir, norm, wallLen, floorH1, floorH2, ceilH1, ceilH2)
	}

	return buildTerrainMesh(boundary, boundaryDepths, w.TerrainPoints, toVertex, true, w.DoubleSided)
}

// buildFloorTerrainMesh generates a terrain displacement mesh for a floor or ceiling.
func buildFloorTerrainMesh(
	points []mgl32.Vec2,
	heightFunc func(int) float32,
	baseHeight float32,
	normalUp bool,
	doubleSided bool,
	tileScale float32,
	terrainPts []TerrainPoint,
) ([][]float32, []int) {
	boundary := make([]mgl32.Vec2, len(points))
	boundaryDepths := make([]float32, len(points))
	copy(boundary, points)
	// Boundary depths are the per-vertex height offsets relative to baseHeight
	for i := range points {
		boundaryDepths[i] = heightFunc(i) - baseHeight
	}

	// Ensure CCW winding for the boundary (Delaunay expects this for clipping)
	if signedArea2D_UV(boundary) < 0 {
		reverseVec2Slice(boundary)
		// Also reverse depths
		for i, j := 0, len(boundaryDepths)-1; i < j; i, j = i+1, j-1 {
			boundaryDepths[i], boundaryDepths[j] = boundaryDepths[j], boundaryDepths[i]
		}
	}

	toVertex := func(pt mgl32.Vec2, depth float32) []float32 {
		y := baseHeight + depth
		return []float32{pt.X(), y, pt.Y(), pt.X() / tileScale, pt.Y() / tileScale}
	}

	return buildTerrainMesh(boundary, boundaryDepths, terrainPts, toVertex, normalUp, doubleSided)
}
