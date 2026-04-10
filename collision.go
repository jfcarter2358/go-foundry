package main

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// MaxSlopeCos is cos(45°). Floor triangles whose upward normal has
// normalY/|normal| < this value are considered too steep to walk on.
const MaxSlopeCos = float32(0.7071)

// FindChunk returns the chunk whose 3D AABB contains pos, or nil (O(N) scan).
// Prefer FindChunkAndSector when you need both, as it uses PIP for correctness
// when multiple chunk AABBs overlap.
func FindChunk(chunks []Chunk, pos mgl32.Vec3) *Chunk {
	for i := range chunks {
		c := &chunks[i]
		if pos.X() >= c.MinBound.X() && pos.X() <= c.MaxBound.X() &&
			pos.Y() >= c.MinBound.Y() && pos.Y() <= c.MaxBound.Y() &&
			pos.Z() >= c.MinBound.Z() && pos.Z() <= c.MaxBound.Z() {
			return c
		}
	}
	return nil
}

// FindSector returns the sector in chunk whose XZ polygon contains xz (ray-casting PIP).
func FindSector(chunk *Chunk, xz mgl32.Vec2) *Sector {
	if chunk == nil {
		return nil
	}
	for i := range chunk.Sectors {
		s := &chunk.Sectors[i]
		if pointInPolygon(xz, SectorPoints(chunk, s)) {
			return s
		}
	}
	return nil
}

// FindChunkAndSector scans all chunks and returns the first (chunk, sector) pair
// whose sector XZ polygon contains pos.  Unlike calling FindChunk+FindSector
// separately this is correct even when chunk AABBs overlap, because it uses the
// authoritative point-in-polygon test rather than the approximate AABB test.
func FindChunkAndSector(chunks []Chunk, pos mgl32.Vec3) (*Chunk, *Sector) {
	xz := mgl32.Vec2{pos.X(), pos.Z()}
	for i := range chunks {
		c := &chunks[i]
		// Quick AABB reject (XZ + Y) before the more expensive PIP scan.
		// The Y check enables vertically stacked chunks (rooms-over-rooms).
		if pos.X() < c.MinBound.X() || pos.X() > c.MaxBound.X() ||
			pos.Y() < c.MinBound.Y() || pos.Y() > c.MaxBound.Y() ||
			pos.Z() < c.MinBound.Z() || pos.Z() > c.MaxBound.Z() {
			continue
		}
		for j := range c.Sectors {
			s := &c.Sectors[j]
			if pointInPolygon(xz, SectorPoints(c, s)) {
				return c, s
			}
		}
	}
	return nil, nil
}

// pointInPolygon uses ray-casting to test if pt is inside polygon (XZ coordinates).
func pointInPolygon(pt mgl32.Vec2, polygon []mgl32.Vec2) bool {
	n := len(polygon)
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		xi, yi := polygon[i].X(), polygon[i].Y()
		xj, yj := polygon[j].X(), polygon[j].Y()
		if ((yi > pt.Y()) != (yj > pt.Y())) &&
			(pt.X() < (xj-xi)*(pt.Y()-yi)/(yj-yi)+xi) {
			inside = !inside
		}
		j = i
	}
	return inside
}

// closestPointOnSegment2D returns the point on segment (a,b) closest to p
// and the distance from p to that point.
func closestPointOnSegment2D(p, a, b mgl32.Vec2) (mgl32.Vec2, float32) {
	closest, dist, _ := closestPointOnSegment2DWithT(p, a, b)
	return closest, dist
}

// closestPointOnSegment2DWithT returns the closest point, distance, and
// parametric t (0..1) along segment (a,b).
func closestPointOnSegment2DWithT(p, a, b mgl32.Vec2) (mgl32.Vec2, float32, float32) {
	ab := b.Sub(a)
	lenSq := ab.Dot(ab)
	if lenSq < 1e-12 {
		return a, a.Sub(p).Len(), 0
	}
	t := p.Sub(a).Dot(ab) / lenSq
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	closest := a.Add(ab.Mul(t))
	return closest, closest.Sub(p).Len(), t
}

// pointInTriangle2D tests whether p lies inside the 2D triangle (a, b, c).
// Returns (inside, wA, wB, wC) where wA/wB/wC are the barycentric weights
// for vertices A, B, C respectively.
func pointInTriangle2D(p, a, b, c mgl32.Vec2) (bool, float32, float32, float32) {
	v0 := c.Sub(a)
	v1 := b.Sub(a)
	v2 := p.Sub(a)
	d00 := v0.Dot(v0)
	d01 := v0.Dot(v1)
	d02 := v0.Dot(v2)
	d11 := v1.Dot(v1)
	d12 := v1.Dot(v2)
	denom := d00*d11 - d01*d01
	if denom > -1e-12 && denom < 1e-12 {
		return false, 0, 0, 0 // degenerate triangle
	}
	inv := float32(1.0) / denom
	u := (d11*d02 - d01*d12) * inv // weight for C
	v := (d00*d12 - d01*d02) * inv // weight for B
	w := 1 - u - v                 // weight for A
	const eps = float32(-0.001)
	if u >= eps && v >= eps && w >= eps {
		return true, w, v, u
	}
	return false, 0, 0, 0
}

// triangleNormal returns the (unnormalized) cross-product normal of the
// triangle defined by 3D vertices a, b, c.
func triangleNormal(a, b, c mgl32.Vec3) mgl32.Vec3 {
	e1 := b.Sub(a)
	e2 := c.Sub(a)
	return mgl32.Vec3{
		e1.Y()*e2.Z() - e1.Z()*e2.Y(),
		e1.Z()*e2.X() - e1.X()*e2.Z(),
		e1.X()*e2.Y() - e1.Y()*e2.X(),
	}
}

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
		inside, wA, wB, wC := pointInTriangle2D(xz, pts[ai], pts[bi], pts[ci])
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
		normal := triangleNormal(a3, b3, c3)
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
		inside, wA, wB, wC := pointInTriangle2D(xz, pts[0], pts[i], pts[i+1])
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
		normal := triangleNormal(a3, b3, c3)
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

// adjustHeightForExtrusions checks if xz falls inside any floor/ceiling
// extrusion's outer polygon and offsets the height by the extrusion depth.
// For floors (floor=true): the extruded area is lower (height - depth).
// For ceilings (floor=false): the extruded area is higher (height + depth).
func adjustHeightForExtrusions(xz mgl32.Vec2, height float32, extrusions []Extrusion, floor bool) float32 {
	for _, ext := range extrusions {
		if len(ext.Outer) < 3 {
			continue
		}
		poly := make([]mgl32.Vec2, len(ext.Outer))
		for i, p := range ext.Outer {
			poly[i] = mgl32.Vec2{p.U, p.V}
		}
		if pointInPolygon(xz, poly) {
			if floor {
				return height - ext.Depth
			}
			return height + ext.Depth
		}
	}
	return height
}

// ResolveSlopeCollision prevents the entity from walking up floor slopes that
// are too steep (angle from horizontal > ~45°).  Steep slopes act like
// vertical walls: the uphill component of XZ movement is removed so the
// entity slides along the base of the slope instead.
// oldPos is the entity position from the previous frame.
func ResolveSlopeCollision(entity *Entity, chunk *Chunk, sector *Sector, oldPos, newPos mgl32.Vec3) mgl32.Vec3 {
	if chunk == nil || sector == nil {
		return newPos
	}
	xz := mgl32.Vec2{newPos.X(), newPos.Z()}
	_, normal, found := InterpolateSurfaceHeight(chunk, sector, xz, true)
	if !found {
		return newPos
	}
	nLen := normal.Len()
	if nLen < 1e-6 {
		return newPos
	}
	cosAngle := normal.Y() / nLen
	if cosAngle >= MaxSlopeCos {
		return newPos // slope is walkable
	}
	// Slope is too steep.  Check if the entity is moving uphill.
	moveXZ := mgl32.Vec2{newPos.X() - oldPos.X(), newPos.Z() - oldPos.Z()}
	normalXZ := mgl32.Vec2{normal.X(), normal.Z()}
	dot := moveXZ.Dot(normalXZ)
	if dot >= 0 {
		return newPos // moving downhill or parallel — allow
	}
	// Remove the uphill component of XZ movement (slide along the slope base).
	nxzLenSq := normalXZ.Dot(normalXZ)
	if nxzLenSq < 1e-12 {
		return newPos
	}
	correction := normalXZ.Mul(dot / nxzLenSq)
	adjusted := moveXZ.Sub(correction)
	return mgl32.Vec3{
		oldPos.X() + adjusted.X(),
		newPos.Y(),
		oldPos.Z() + adjusted.Y(),
	}
}

// playerInWallExtrusion returns true if the entity's vertical extent fits
// entirely within one of the wall's extrusion openings at the given u position.
// u is the distance along the wall, ceilH is the interpolated ceiling height
// at this position. Extrusion V is distance from ceiling downward, so world
// Y = ceilH - V.
func playerInWallExtrusion(wall *Wall, entityMinY, entityMaxY, u, ceilH float32) bool {
	for _, ext := range wall.Extrusions {
		if len(ext.Outer) < 3 {
			continue
		}
		// Build UV polygon from extrusion outer points
		poly := make([]mgl32.Vec2, len(ext.Outer))
		for i, p := range ext.Outer {
			poly[i] = mgl32.Vec2{p.U, p.V}
		}

		// Compute V range of the opening
		minV := ext.Outer[0].V
		maxV := ext.Outer[0].V
		for _, p := range ext.Outer[1:] {
			if p.V < minV {
				minV = p.V
			}
			if p.V > maxV {
				maxV = p.V
			}
		}

		// Convert V to world Y: worldY = ceilH - V
		openingMaxY := ceilH - minV // top of opening
		openingMinY := ceilH - maxV // bottom of opening

		// Quick reject: entity doesn't fit vertically.
		// Use a small tolerance so that gravity-induced dip below the floor
		// (before ResolveFloorCollision runs) doesn't block doorway pass-through.
		const vEps = float32(0.1)
		if entityMinY < openingMinY-vEps || entityMaxY > openingMaxY+vEps {
			continue
		}

		// Check if u is within the extrusion's horizontal extent using
		// point-in-polygon at the entity's center V
		entityCenterV := ceilH - (entityMinY+entityMaxY)/2
		if pointInPolygon(mgl32.Vec2{u, entityCenterV}, poly) {
			return true
		}
	}
	return false
}

// getAdjacentSectors returns the sectors connected to sector via portals
// within the same chunk.
func getAdjacentSectors(chunk *Chunk, sector *Sector) []*Sector {
	if chunk == nil || sector == nil {
		return nil
	}
	var adj []*Sector
	for _, p := range sector.Portals {
		for i := range chunk.Sectors {
			if chunk.Sectors[i].ID == p.ToSector {
				adj = append(adj, &chunk.Sectors[i])
				break
			}
		}
	}
	return adj
}

// resolveWallsForSector pushes newXZ away from the collidable walls of a
// single sector, respecting wall height and extrusion openings.
func resolveWallsForSector(
	chunk *Chunk, s *Sector,
	newXZ *mgl32.Vec2, entityMinY, entityMaxY, radius, epsilon float32,
) {
	pts := SectorPoints(chunk, s)

	baseCeilH := s.Ceiling.Height
	if !s.Ceiling.Enabled && baseCeilH == s.Floor.Height {
		baseCeilH = s.Floor.Height + 3.0
	}

	for _, wall := range s.Walls {
		if !wall.Collision {
			continue
		}
		closest, dist, t := closestPointOnSegment2DWithT(*newXZ, wall.StartPoint, wall.EndPoint)
		if dist >= radius+epsilon {
			continue
		}

		// Compute wall ceiling height at the closest point.
		// Only skip collision when the entity is entirely above the wall
		// (allows passing over short walls). We do NOT skip when the entity
		// is below the wall floor, because terrain dips can place the player
		// below the wall's endpoint floor heights while still inside the sector.
		si1 := findVertexIdx(pts, wall.StartPoint)
		si2 := findVertexIdx(pts, wall.EndPoint)
		var ceilH float32
		if s.Ceiling.Enabled {
			ceilH = s.Ceiling.VertexHeight(si1)*(1-t) + s.Ceiling.VertexHeight(si2)*t
		} else {
			ceilH = baseCeilH
		}

		if entityMinY >= ceilH {
			continue
		}

		// Check if entity fits inside a wall extrusion opening
		if len(wall.Extrusions) > 0 {
			wallLen := wall.EndPoint.Sub(wall.StartPoint).Len()
			u := t * wallLen
			if playerInWallExtrusion(&wall, entityMinY, entityMaxY, u, ceilH) {
				continue
			}
		}

		// Push player away from the wall
		diff := newXZ.Sub(closest)
		diffLen := diff.Len()
		var away mgl32.Vec2
		if diffLen < 1e-6 {
			edge := wall.EndPoint.Sub(wall.StartPoint)
			perp := mgl32.Vec2{-edge.Y(), edge.X()}
			perpLen := perp.Len()
			if perpLen < 1e-6 {
				continue
			}
			away = perp.Mul(1.0 / perpLen)
		} else {
			away = diff.Mul(1.0 / diffLen)
		}
		*newXZ = closest.Add(away.Mul(radius + epsilon))
	}
}

// ResolveWallCollision keeps the entity at least entity.BoundHalfX away from
// every collidable wall by pushing it back along the wall normal when too close.
// Walls are only checked when the entity's vertical extent overlaps the wall's
// height range, and extrusion openings allow pass-through.
// Checks both the current sector and portal-adjacent sectors.
func ResolveWallCollision(entity *Entity, chunk *Chunk, sector *Sector, newPos mgl32.Vec3) mgl32.Vec3 {
	if sector == nil || chunk == nil {
		return newPos
	}
	newXZ := mgl32.Vec2{newPos.X(), newPos.Z()}
	radius := float32(math.Max(float64(entity.BoundHalfX), float64(entity.BoundHalfZ)))
	const epsilon = float32(0.001)

	entityMinY := newPos.Y() - entity.BoundHalfY
	entityMaxY := newPos.Y() + entity.BoundHalfY

	// Check current sector walls
	resolveWallsForSector(chunk, sector, &newXZ, entityMinY, entityMaxY, radius, epsilon)

	// Check adjacent sector walls
	for _, adj := range getAdjacentSectors(chunk, sector) {
		resolveWallsForSector(chunk, adj, &newXZ, entityMinY, entityMaxY, radius, epsilon)
	}

	return mgl32.Vec3{newXZ.X(), newPos.Y(), newXZ.Y()}
}

// ResolveFloorCollision clamps pos.Y so the entity rests on the sector floor
// and cannot pass through the ceiling.  Floor and ceiling heights are
// interpolated per-vertex so the entity follows sloped surfaces.
// Returns (hitFloor, hitCeiling) so the caller can reset vertical velocity on
// both landing and head-bonk events.
func ResolveFloorCollision(entity *Entity, chunk *Chunk, sector *Sector, pos *mgl32.Vec3) (bool, bool) {
	if sector == nil || chunk == nil {
		return false, false
	}
	hitFloor, hitCeiling := false, false
	xz := mgl32.Vec2{pos[0], pos[2]}

	floorH, _, _ := InterpolateSurfaceHeight(chunk, sector, xz, true)
	if len(sector.Floor.Extrusions) > 0 {
		floorH = adjustHeightForExtrusions(xz, floorH, sector.Floor.Extrusions, true)
	}

	// Also consider adjacent sector floors (player near portal boundary)
	for _, adj := range getAdjacentSectors(chunk, sector) {
		adjPts := SectorPoints(chunk, adj)
		if !pointInPolygon(xz, adjPts) {
			continue
		}
		adjFloorH, _, _ := InterpolateSurfaceHeight(chunk, adj, xz, true)
		if len(adj.Floor.Extrusions) > 0 {
			adjFloorH = adjustHeightForExtrusions(xz, adjFloorH, adj.Floor.Extrusions, true)
		}
		if adjFloorH > floorH {
			floorH = adjFloorH
		}
	}

	minY := floorH + entity.BoundHalfY
	if pos[1] <= minY {
		pos[1] = minY
		hitFloor = true
	}

	if sector.Ceiling.Enabled {
		ceilH, _, _ := InterpolateSurfaceHeight(chunk, sector, xz, false)
		if len(sector.Ceiling.Extrusions) > 0 {
			ceilH = adjustHeightForExtrusions(xz, ceilH, sector.Ceiling.Extrusions, false)
		}

		// Also consider adjacent sector ceilings
		for _, adj := range getAdjacentSectors(chunk, sector) {
			if !adj.Ceiling.Enabled {
				continue
			}
			adjPts := SectorPoints(chunk, adj)
			if !pointInPolygon(xz, adjPts) {
				continue
			}
			adjCeilH, _, _ := InterpolateSurfaceHeight(chunk, adj, xz, false)
			if len(adj.Ceiling.Extrusions) > 0 {
				adjCeilH = adjustHeightForExtrusions(xz, adjCeilH, adj.Ceiling.Extrusions, false)
			}
			if adjCeilH < ceilH {
				ceilH = adjCeilH
			}
		}

		maxY := ceilH - entity.BoundHalfY*2 - 0.05
		if pos[1] >= maxY {
			pos[1] = maxY
			hitCeiling = true
		}
	}
	return hitFloor, hitCeiling
}

// AABBOverlap returns true when two entity AABBs overlap.
func AABBOverlap(a, b *Entity) bool {
	dx := math.Abs(float64(a.Position.X() - b.Position.X()))
	dy := math.Abs(float64(a.Position.Y() - b.Position.Y()))
	dz := math.Abs(float64(a.Position.Z() - b.Position.Z()))
	return dx < float64(a.BoundHalfX+b.BoundHalfX) &&
		dy < float64(a.BoundHalfY+b.BoundHalfY) &&
		dz < float64(a.BoundHalfZ+b.BoundHalfZ)
}

// EntityOverlap tests whether entity a (at candidatePos) overlaps entity b,
// dispatching based on BoundingKind.
func EntityOverlap(a, b *Entity, candidatePos mgl32.Vec3) bool {
	switch {
	case a.BoundingKind == BoundingSphere && b.BoundingKind == BoundingSphere:
		return sphereOverlap(candidatePos, a, b.Position, b)
	case a.BoundingKind == BoundingCylinder || b.BoundingKind == BoundingCylinder:
		return cylinderOverlap(candidatePos, a, b.Position, b)
	default: // AABB
		dx := math.Abs(float64(candidatePos.X() - b.Position.X()))
		dy := math.Abs(float64(candidatePos.Y() - b.Position.Y()))
		dz := math.Abs(float64(candidatePos.Z() - b.Position.Z()))
		return dx < float64(a.BoundHalfX+b.BoundHalfX) &&
			dy < float64(a.BoundHalfY+b.BoundHalfY) &&
			dz < float64(a.BoundHalfZ+b.BoundHalfZ)
	}
}

func sphereOverlap(posA mgl32.Vec3, a *Entity, posB mgl32.Vec3, b *Entity) bool {
	rA := float64(max32(a.BoundHalfX, max32(a.BoundHalfY, a.BoundHalfZ)))
	rB := float64(max32(b.BoundHalfX, max32(b.BoundHalfY, b.BoundHalfZ)))
	d := posA.Sub(posB)
	distSq := float64(d.X())*float64(d.X()) + float64(d.Y())*float64(d.Y()) + float64(d.Z())*float64(d.Z())
	sumR := rA + rB
	return distSq < sumR*sumR
}

func cylinderOverlap(posA mgl32.Vec3, a *Entity, posB mgl32.Vec3, b *Entity) bool {
	// XZ circle test
	rA := float64(max32(a.BoundHalfX, a.BoundHalfZ))
	rB := float64(max32(b.BoundHalfX, b.BoundHalfZ))
	dx := float64(posA.X() - posB.X())
	dz := float64(posA.Z() - posB.Z())
	distSqXZ := dx*dx + dz*dz
	sumR := rA + rB
	if distSqXZ >= sumR*sumR {
		return false
	}
	// Y AABB test
	dy := math.Abs(float64(posA.Y() - posB.Y()))
	return dy < float64(a.BoundHalfY+b.BoundHalfY)
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// ResolveEntityCollision pushes entity a out of entity b in the XZ plane.
func ResolveEntityCollision(a, b *Entity, pos mgl32.Vec3) mgl32.Vec3 {
	dx := pos.X() - b.Position.X()
	dz := pos.Z() - b.Position.Z()
	dist := float32(math.Sqrt(float64(dx*dx + dz*dz)))
	if dist < 1e-6 {
		// Exactly overlapping — push along X
		dx = 1
		dist = 1
	}
	// Compute combined radius in push direction
	rA := max32(a.BoundHalfX, a.BoundHalfZ)
	rB := max32(b.BoundHalfX, b.BoundHalfZ)
	overlap := (rA + rB) - dist
	if overlap <= 0 {
		return pos
	}
	const epsilon = float32(0.001)
	pushX := (dx / dist) * (overlap + epsilon)
	pushZ := (dz / dist) * (overlap + epsilon)
	return mgl32.Vec3{pos.X() + pushX, pos.Y(), pos.Z() + pushZ}
}

// SegmentIntersect2D tests whether segments (a1,a2) and (b1,b2) intersect.
// Returns (hit, t) where t is the parametric position along (a1,a2).
func SegmentIntersect2D(a1, a2, b1, b2 mgl32.Vec2) (bool, float32) {
	d1 := a2.Sub(a1)
	d2 := b2.Sub(b1)
	cross := d1.X()*d2.Y() - d1.Y()*d2.X()
	if math.Abs(float64(cross)) < 1e-6 {
		return false, 0
	}
	diff := b1.Sub(a1)
	t := (diff.X()*d2.Y() - diff.Y()*d2.X()) / cross
	u := (diff.X()*d1.Y() - diff.Y()*d1.X()) / cross
	if t >= 0 && t <= 1 && u >= 0 && u <= 1 {
		return true, t
	}
	return false, 0
}

// ResolvePrimitiveCollision pushes the entity out of any overlapping primitives
// in the current sector and portal-adjacent sectors.
func ResolvePrimitiveCollision(entity *Entity, chunk *Chunk, sector *Sector, newPos mgl32.Vec3) mgl32.Vec3 {
	if chunk == nil || sector == nil {
		return newPos
	}
	// Check current sector
	for _, pi := range sector.PrimitiveIndices {
		prim := &chunk.Primitives[pi]
		if !prim.Collision {
			continue
		}
		tmp := primAsEntity(prim)
		if EntityOverlap(entity, tmp, newPos) {
			newPos = ResolveEntityCollision(entity, tmp, newPos)
		}
	}
	// Check adjacent sectors
	for _, adj := range getAdjacentSectors(chunk, sector) {
		for _, pi := range adj.PrimitiveIndices {
			prim := &chunk.Primitives[pi]
			if !prim.Collision {
				continue
			}
			tmp := primAsEntity(prim)
			if EntityOverlap(entity, tmp, newPos) {
				newPos = ResolveEntityCollision(entity, tmp, newPos)
			}
		}
	}
	return newPos
}
