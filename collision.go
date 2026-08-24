package main

import (
	"foundry/internal/geom"
	"foundry/internal/world"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// MaxSlopeCos is cos(45°). Floor triangles whose upward normal has
// normalY/|normal| < this value are considered too steep to walk on.
const MaxSlopeCos = float32(0.7071)

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

// adjustHeightForExtrusions checks if xz falls inside any floor/ceiling
// extrusion's outer polygon and offsets the height by the extrusion depth.
// For floors (floor=true): the extruded area is lower (height - depth).
// For ceilings (floor=false): the extruded area is higher (height + depth).
func adjustHeightForExtrusions(xz mgl32.Vec2, height float32, extrusions []world.Extrusion, floor bool) float32 {
	for _, ext := range extrusions {
		if len(ext.Outer) < 3 {
			continue
		}
		poly := make([]mgl32.Vec2, len(ext.Outer))
		for i, p := range ext.Outer {
			poly[i] = mgl32.Vec2{p.U, p.V}
		}
		if geom.PointInPolygon(xz, poly) {
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
func ResolveSlopeCollision(entity *world.Entity, chunk *world.Chunk, sector *world.Sector, oldPos, newPos mgl32.Vec3) mgl32.Vec3 {
	if chunk == nil || sector == nil {
		return newPos
	}
	xz := mgl32.Vec2{newPos.X(), newPos.Z()}
	_, normal, found := world.InterpolateSurfaceHeight(chunk, sector, xz, true)
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
func playerInWallExtrusion(wall *world.Wall, entityMinY, entityMaxY, u, ceilH float32) bool {
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
		if geom.PointInPolygon(mgl32.Vec2{u, entityCenterV}, poly) {
			return true
		}
	}
	return false
}

// getAdjacentSectors returns the sectors connected to sector via portals
// within the same chunk.
func getAdjacentSectors(chunk *world.Chunk, sector *world.Sector) []*world.Sector {
	if chunk == nil || sector == nil {
		return nil
	}
	var adj []*world.Sector
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
	chunk *world.Chunk, s *world.Sector,
	newXZ *mgl32.Vec2, entityMinY, entityMaxY, radius, epsilon float32,
) {
	pts := world.SectorPoints(chunk, s)

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
		si1 := world.FindVertexIdx(pts, wall.StartPoint)
		si2 := world.FindVertexIdx(pts, wall.EndPoint)
		ceilH := s.WallTopHeight(si1)*(1-t) + s.WallTopHeight(si2)*t

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
func ResolveWallCollision(entity *world.Entity, chunk *world.Chunk, sector *world.Sector, newPos mgl32.Vec3) mgl32.Vec3 {
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

// sectorFloorHeightAt returns the highest walkable floor under xz: this
// sector's, plus any portal-adjacent sector whose polygon also covers the
// point. Extrusions are folded in, so a raised step reads as floor.
//
// Split out of ResolveFloorCollision because entity world collision needs the
// same query with a different clamp — see resolveEntityFloorCeiling
// (entity_collision.go). The "highest wins" rule is what keeps something
// standing on a portal boundary from sinking into the lower of the two floors.
func sectorFloorHeightAt(chunk *world.Chunk, sector *world.Sector, xz mgl32.Vec2) float32 {
	floorH, _, _ := world.InterpolateSurfaceHeight(chunk, sector, xz, true)
	if len(sector.Floor.Extrusions) > 0 {
		floorH = adjustHeightForExtrusions(xz, floorH, sector.Floor.Extrusions, true)
	}
	for _, adj := range getAdjacentSectors(chunk, sector) {
		if !geom.PointInPolygon(xz, world.SectorPoints(chunk, adj)) {
			continue
		}
		adjFloorH, _, _ := world.InterpolateSurfaceHeight(chunk, adj, xz, true)
		if len(adj.Floor.Extrusions) > 0 {
			adjFloorH = adjustHeightForExtrusions(xz, adjFloorH, adj.Floor.Extrusions, true)
		}
		if adjFloorH > floorH {
			floorH = adjFloorH
		}
	}
	return floorH
}

// sectorCeilingHeightAt is sectorFloorHeightAt's mirror: the lowest ceiling over
// xz, and whether there is one at all. A sector with no ceiling is open sky, not
// a ceiling at height zero.
func sectorCeilingHeightAt(chunk *world.Chunk, sector *world.Sector, xz mgl32.Vec2) (float32, bool) {
	if !sector.Ceiling.Enabled {
		return 0, false
	}
	ceilH, _, _ := world.InterpolateSurfaceHeight(chunk, sector, xz, false)
	if len(sector.Ceiling.Extrusions) > 0 {
		ceilH = adjustHeightForExtrusions(xz, ceilH, sector.Ceiling.Extrusions, false)
	}
	for _, adj := range getAdjacentSectors(chunk, sector) {
		if !adj.Ceiling.Enabled || !geom.PointInPolygon(xz, world.SectorPoints(chunk, adj)) {
			continue
		}
		adjCeilH, _, _ := world.InterpolateSurfaceHeight(chunk, adj, xz, false)
		if len(adj.Ceiling.Extrusions) > 0 {
			adjCeilH = adjustHeightForExtrusions(xz, adjCeilH, adj.Ceiling.Extrusions, false)
		}
		if adjCeilH < ceilH {
			ceilH = adjCeilH
		}
	}
	return ceilH, true
}

// ResolveFloorCollision clamps pos.Y so the entity rests on the sector floor
// and cannot pass through the ceiling.  Floor and ceiling heights are
// interpolated per-vertex so the entity follows sloped surfaces.
// Returns (hitFloor, hitCeiling) so the caller can reset vertical velocity on
// both landing and head-bonk events.
//
// This is the *player's* clamp, and its two halves do not agree about what
// pos means: the floor treats it as the body centre (pos - BoundHalfY is the
// feet), while the ceiling treats it as the feet and allows a body twice
// BoundHalfY tall above it. That asymmetry is load-bearing tuning for how
// gameplay feels, so it is left alone — placed entities get their own
// symmetric clamp instead (resolveEntityFloorCeiling).
func ResolveFloorCollision(entity *world.Entity, chunk *world.Chunk, sector *world.Sector, pos *mgl32.Vec3) (bool, bool) {
	if sector == nil || chunk == nil {
		return false, false
	}
	hitFloor, hitCeiling := false, false
	xz := mgl32.Vec2{pos[0], pos[2]}

	minY := sectorFloorHeightAt(chunk, sector, xz) + entity.BoundHalfY
	if pos[1] <= minY {
		pos[1] = minY
		hitFloor = true
	}

	if ceilH, ok := sectorCeilingHeightAt(chunk, sector, xz); ok {
		maxY := ceilH - entity.BoundHalfY*2 - 0.05
		if pos[1] >= maxY {
			pos[1] = maxY
			hitCeiling = true
		}
	}
	return hitFloor, hitCeiling
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
