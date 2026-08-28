package collision

import (
	"foundry/internal/geom"
	"foundry/internal/world"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// MaxSlopeCos is cos(45°). Floor triangles whose upward normal has
// normalY/|normal| < this value are considered too steep to walk on.
const MaxSlopeCos = float32(0.7071)

// chunkLocal and chunkWorld bridge the one gap left once a chunk can carry its
// own Origin/Rotation/Scale: everything in this package resolves a position
// against whichever chunks its frame owns (player.go, entity_collision.go,
// frame_portal.go), and that position arrives already carried out of the
// frame — via Frame.FromWorld, one layer up — but not yet out of the specific
// chunk's own placement within that frame. Points, walls and portals
// (SectorPoints and friends) are chunk-local; a wall push or floor sample has
// to run in that same space, so every resolver call in this package converts
// in with chunkLocal, computes, and converts back out with chunkWorld.
//
// This is deliberately chunk.LocalPlacementMatrix, not the full ModelMatrix:
// an owning frame's Xform was already divided out by the caller that found
// chunk in the first place (Frame.ChunkAndSectorAt), so undoing it again here
// would double-subtract it. For an ordinary (non-frame) chunk the two matrices
// are the same thing, since ModelMatrix only layers Xform on top when the
// chunk is frame-owned — so this is exactly WorldToLocal/LocalToWorld for the
// overwhelmingly common case, and only diverges for a frame-owned chunk that
// also has its own local placement.
func chunkLocal(chunk *world.Chunk, p mgl32.Vec3) mgl32.Vec3 {
	if chunk == nil {
		return p
	}
	m := chunk.LocalPlacementMatrix()
	if m == mgl32.Ident4() {
		return p
	}
	r := m.Inv().Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

func chunkWorld(chunk *world.Chunk, p mgl32.Vec3) mgl32.Vec3 {
	if chunk == nil {
		return p
	}
	m := chunk.LocalPlacementMatrix()
	if m == mgl32.Ident4() {
		return p
	}
	r := m.Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
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

// ResolveCeilingEntry stops a body walking sideways into a sector whose ceiling
// is below its feet — onto the OUTSIDE of that ceiling, where there is nothing
// to stand on.
//
// Without it the body is admitted and then rescued by the ceiling clamp in
// ResolveFloorCollision, which yanks it down to just under the ceiling in a
// single frame. That clamp is right for the case it was written for — a jump
// inside a room, where the body is under the ceiling and meets it going up —
// but it is applied wherever the body happens to be, and a body arriving from
// above the ceiling is not bonking its head on anything. It is standing on top
// of a roof and being teleported into the room beneath.
//
// The rule is the whole of it: if the body's FEET are at or above the target
// sector's ceiling, the body is entirely above that surface and the move is
// refused. A ceiling's upper face is a wall when approached from outside, the
// same way a floor's underside already is.
//
// Only the horizontal component is refused, so falling is untouched: a body
// dropping through a hatch is moving in Y, and the sector below a proper hatch
// has its own opening there anyway (see world/opening.go), which makes
// sectorCeilingHeightAt report no ceiling at all.
//
// oldPos is where the body was before this step, in the same chunk-local space
// as newPos — the same contract ResolveSlopeCollision takes.
func ResolveCeilingEntry(entity *world.Entity, chunk *world.Chunk, sector *world.Sector, oldPos, newPos mgl32.Vec3) mgl32.Vec3 {
	if chunk == nil || sector == nil || entity == nil {
		return newPos
	}
	// A move with no horizontal component cannot be an entry: whatever this is,
	// it is falling or rising, and the vertical clamps own it.
	if newPos.X() == oldPos.X() && newPos.Z() == oldPos.Z() {
		return newPos
	}
	if ceilingAdmits(chunk, sector, oldPos, newPos) {
		return newPos
	}
	// Blocked. Try each axis on its own before giving up, so a body walking
	// diagonally at the edge slides along it instead of sticking — the same
	// courtesy the wall resolver extends. Each candidate is resolved against
	// whatever sector IT lands in, which need not be the one that refused.
	slideX := mgl32.Vec3{newPos.X(), newPos.Y(), oldPos.Z()}
	if ceilingAdmitsAt(chunk, oldPos, slideX) {
		return slideX
	}
	slideZ := mgl32.Vec3{oldPos.X(), newPos.Y(), newPos.Z()}
	if ceilingAdmitsAt(chunk, oldPos, slideZ) {
		return slideZ
	}
	// Refuse the horizontal move outright, keeping whatever vertical motion it
	// had — a body denied entry is still falling if it was falling.
	return mgl32.Vec3{oldPos.X(), newPos.Y(), oldPos.Z()}
}

// ceilingAdmits reports whether a body travelling from oldPos may enter dest.
//
// The test is between two SURFACES — the floor the body is standing on and the
// ceiling it would be walking under — never the body's own height. Its
// instantaneous Y is no use here: gravity has already pulled it a fraction of a
// unit down the frame this runs in, so a body standing squarely on a roof
// measures as being a hair underneath it, and any test against feet-versus-
// ceiling reads exactly backwards. The surfaces do not move.
func ceilingAdmits(chunk *world.Chunk, dest *world.Sector, oldPos, newPos mgl32.Vec3) bool {
	newXZ := mgl32.Vec2{newPos.X(), newPos.Z()}
	ceilH, ok := sectorCeilingHeightAt(chunk, dest, newXZ)
	if !ok {
		return true // open sky, or a hole cut through the ceiling here
	}

	oldXZ := mgl32.Vec2{oldPos.X(), oldPos.Z()}
	if world.PointInSector(chunk, dest, oldXZ) {
		// Already inside: this is not an entry, and the ceiling above is the
		// body's own. Jumping into it is a head bonk, which is the clamp's job
		// and must not stop the body moving horizontally — otherwise a room
		// with little headroom would freeze anything that jumped in it.
		return true
	}

	from := world.FindSectorAt(chunk, oldXZ, oldPos.Y())
	if from == nil {
		return true
	}
	standing, ok := sectorFloorHeightAt(chunk, from, oldXZ)
	if !ok {
		return true // over a hole: falling, and the clamps can have it
	}
	// The floor being left is at or above the ceiling being entered, so the
	// body is not stepping into that room — it is stepping onto its roof.
	return standing < ceilH-ceilingEntryEpsilon
}

// ceilingAdmitsAt is ceilingAdmits for a candidate whose destination sector is
// not already known. A candidate in no sector is admitted: there is no ceiling
// there to be on top of, and whether it is a legal place to stand is not this
// resolver's question.
func ceilingAdmitsAt(chunk *world.Chunk, oldPos, pos mgl32.Vec3) bool {
	s := world.FindSectorAt(chunk, mgl32.Vec2{pos.X(), pos.Z()}, pos.Y())
	if s == nil {
		return true
	}
	return ceilingAdmits(chunk, s, oldPos, pos)
}

// ceilingEntryEpsilon decides which way exact equality falls, and "on the roof"
// is the safe answer: a floor level with the ceiling beside it is the shape
// this resolver exists for — an unfinished staircase topping out at the ceiling
// of the room it climbs past — and a body there has no headroom to enter with.
const ceilingEntryEpsilon = float32(1e-3)

// wallWindowAdmits reports whether the entity's vertical extent fits entirely
// inside one of the wall's pass-through windows at the given u position — a
// doorway, an arch, a crawlspace.
//
// A window is a face of the wall's surface mesh marked Hole (SurfaceWallWindows).
// The wall's collision plane itself stays flat: the mesh can push the wall's
// visible surface in and out as far as an author likes, and what stops a body
// is still the wall's straight segment on the 2D map, with these windows
// punched through it. Bumps you walk into, holes you walk through.
//
// That is the same contract DetailSpec's displacement already ships with, and
// it is what keeps wall collision a segment test rather than a swept query
// against an arbitrary mesh. It also means an author cannot accidentally make a
// wall passable by shaping it — only by opening it.
//
// u is the distance along the wall, ceilH the interpolated wall top at this
// position. A window's V is distance from the wall top downward, so world
// Y = ceilH - V.
func wallWindowAdmits(windows [][]mgl32.Vec2, entityMinY, entityMaxY, u, ceilH float32) bool {
	for _, poly := range windows {
		if len(poly) < 3 {
			continue
		}
		minV, maxV := poly[0].Y(), poly[0].Y()
		for _, p := range poly[1:] {
			if p.Y() < minV {
				minV = p.Y()
			}
			if p.Y() > maxV {
				maxV = p.Y()
			}
		}
		openingMaxY := ceilH - minV // top of the window
		openingMinY := ceilH - maxV // bottom of it

		// Quick reject: the entity does not fit vertically. The tolerance keeps
		// a gravity-induced dip below the floor — which happens before
		// ResolveFloorCollision runs — from blocking a doorway.
		const vEps = float32(0.1)
		if entityMinY < openingMinY-vEps || entityMaxY > openingMaxY+vEps {
			continue
		}
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

		// Check whether the entity fits through a doorway cut in the wall.
		if windows := world.SurfaceWallWindows(&wall.Mesh); len(windows) > 0 {
			wallLen := wall.EndPoint.Sub(wall.StartPoint).Len()
			u := t * wallLen
			if wallWindowAdmits(windows, entityMinY, entityMaxY, u, ceilH) {
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
// ok is false when there is no floor under xz at all — every surface that
// covers the point has an opening there (see opening.go). That is what makes a
// stairwell a stairwell rather than a decal: a body over the hole is clamped by
// nothing and falls, and the sector lookup hands it to whatever is below as
// soon as it drops past that floor's height.
//
// An adjacent sector is judged on its OWN openings, not this one's. Two sectors
// sharing a portal each cover part of the boundary, and a hole cut in one of
// them should not be filled in by the other unless that other genuinely has
// floor there.
//
// Split out of ResolveFloorCollision because entity world collision needs the
// same query with a different clamp — see resolveEntityFloorCeiling
// (entity_collision.go). The "highest wins" rule is what keeps something
// standing on a portal boundary from sinking into the lower of the two floors.
func sectorFloorHeightAt(chunk *world.Chunk, sector *world.Sector, xz mgl32.Vec2) (float32, bool) {
	var floorH float32
	found := false
	if !world.SurfaceIsOpenAt(sector.Floor.Openings, xz) {
		floorH, _, _ = world.InterpolateSurfaceHeight(chunk, sector, xz, true)
		found = true
	}
	for _, adj := range getAdjacentSectors(chunk, sector) {
		if world.SurfaceIsOpenAt(adj.Floor.Openings, xz) {
			continue
		}
		if !geom.PointInPolygon(xz, world.SectorPoints(chunk, adj)) {
			continue
		}
		adjFloorH, _, _ := world.InterpolateSurfaceHeight(chunk, adj, xz, true)
		if !found || adjFloorH > floorH {
			floorH, found = adjFloorH, true
		}
	}
	return floorH, found
}

// sectorCeilingHeightAt is sectorFloorHeightAt's mirror: the lowest ceiling over
// xz, and whether there is one at all. A sector with no ceiling is open sky, not
// a ceiling at height zero — and neither is a ceiling with a hole cut in it,
// which is what lets a body climb a stair up through the storey above.
func sectorCeilingHeightAt(chunk *world.Chunk, sector *world.Sector, xz mgl32.Vec2) (float32, bool) {
	if !sector.Ceiling.Enabled || world.SurfaceIsOpenAt(sector.Ceiling.Openings, xz) {
		return 0, false
	}
	ceilH, _, _ := world.InterpolateSurfaceHeight(chunk, sector, xz, false)
	for _, adj := range getAdjacentSectors(chunk, sector) {
		if !adj.Ceiling.Enabled || world.SurfaceIsOpenAt(adj.Ceiling.Openings, xz) ||
			!geom.PointInPolygon(xz, world.SectorPoints(chunk, adj)) {
			continue
		}
		adjCeilH, _, _ := world.InterpolateSurfaceHeight(chunk, adj, xz, false)
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

	// No floor under the point means an opening: nothing to land on, so the
	// body keeps whatever vertical motion it had and MovePlayer's caller is
	// told it is not on the ground. Falling through the hole is then the
	// ordinary fall it already was.
	if floorH, ok := sectorFloorHeightAt(chunk, sector, xz); ok {
		minY := floorH + entity.BoundHalfY
		if pos[1] <= minY {
			pos[1] = minY
			hitFloor = true
		}
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

// ResolveStepEntry stops a body walking into a riser it has no way to climb —
// the vertical face a portal's gap fill draws between two floors at different
// heights (world.RebuildGaps).
//
// A portal is not a wall, so nothing in resolveWallsForSector ever looks at it,
// and the riser under it was geometry only. Walking at a shoulder-high step
// therefore admitted the body into the sector on the far side, where
// ResolveFloorCollision immediately clamped it up onto that floor: the body
// passed straight through the wall it was looking at and arrived on top of it.
//
// The rule is the mirror of ResolveCeilingEntry's. That one refuses a move onto
// the OUTSIDE of a ceiling; this one refuses a move into the INSIDE of a floor.
// A body may enter a sector whose floor is above the one it is leaving only
// while that floor is within its own reach — no higher than its head. A shorter
// riser is a step, and stepping up onto it is the behaviour this engine has
// always had; a taller one is a wall.
//
// Reach is measured from the higher of two things, and both are needed:
//
//   - the floor being left, because the body's instantaneous Y is a fraction of
//     a unit below it by the time this runs (gravity has already been applied
//     this frame) — the same reason ceilingAdmits weighs surfaces rather than
//     the body;
//   - the body's own feet, so a jump onto a ledge is still admitted at the top
//     of its arc. Without that this resolver would seal off every ledge higher
//     than a step, which is exactly the geometry jumping exists for.
//
// The body's own height is the threshold because it is the only figure the
// engine currently has; NEXT.md's "configurable step height that player can
// walk over" is the field that belongs here when it exists, and nothing else
// about this resolver changes when it arrives.
//
// oldPos is where the body was before this step, in the same chunk-local space
// as newPos — the contract ResolveSlopeCollision and ResolveCeilingEntry take.
func ResolveStepEntry(entity *world.Entity, chunk *world.Chunk, sector *world.Sector, oldPos, newPos mgl32.Vec3) mgl32.Vec3 {
	if chunk == nil || sector == nil || entity == nil {
		return newPos
	}
	// No horizontal component means this is a fall or a rise, and the vertical
	// clamps own it.
	if newPos.X() == oldPos.X() && newPos.Z() == oldPos.Z() {
		return newPos
	}
	if stepAdmits(entity, chunk, sector, oldPos, newPos) {
		return newPos
	}
	// Blocked. Each axis on its own before giving up, so a body walking
	// diagonally into the riser slides along it rather than sticking — the same
	// courtesy the wall and ceiling resolvers extend.
	slideX := mgl32.Vec3{newPos.X(), newPos.Y(), oldPos.Z()}
	if stepAdmitsAt(entity, chunk, oldPos, slideX) {
		return slideX
	}
	slideZ := mgl32.Vec3{oldPos.X(), newPos.Y(), newPos.Z()}
	if stepAdmitsAt(entity, chunk, oldPos, slideZ) {
		return slideZ
	}
	return mgl32.Vec3{oldPos.X(), newPos.Y(), oldPos.Z()}
}

// stepAdmits reports whether a body travelling from oldPos may enter dest,
// given how far above its own footing that sector's floor is.
func stepAdmits(entity *world.Entity, chunk *world.Chunk, dest *world.Sector, oldPos, newPos mgl32.Vec3) bool {
	newXZ := mgl32.Vec2{newPos.X(), newPos.Z()}
	destFloor, ok := sectorFloorHeightAt(chunk, dest, newXZ)
	if !ok {
		return true // a hole where it is going: falling through is not entering
	}

	oldXZ := mgl32.Vec2{oldPos.X(), oldPos.Z()}
	if world.PointInSector(chunk, dest, oldXZ) {
		return true // already inside; this is not an entry
	}

	from := world.FindSectorAt(chunk, oldXZ, oldPos.Y())
	if from == nil {
		return true
	}
	reach, ok := sectorFloorHeightAt(chunk, from, oldXZ)
	if !ok {
		return true // over a hole: falling, and the clamps can have it
	}
	if feet := newPos.Y() - entity.BoundHalfY; feet > reach {
		reach = feet
	}
	return destFloor <= reach+entity.BoundHalfY*2+stepEntryEpsilon
}

// stepAdmitsAt is stepAdmits for a candidate whose destination sector is not
// already known. A candidate in no sector is admitted: there is no floor there
// to be shut out by.
func stepAdmitsAt(entity *world.Entity, chunk *world.Chunk, oldPos, pos mgl32.Vec3) bool {
	s := world.FindSectorAt(chunk, mgl32.Vec2{pos.X(), pos.Z()}, pos.Y())
	if s == nil {
		return true
	}
	return stepAdmits(entity, chunk, s, oldPos, pos)
}

// stepEntryEpsilon decides which way exact equality falls, and "admit" is the
// safe answer here: a riser of exactly the body's height is the boundary case
// the report describes, and refusing it would also refuse a body standing on a
// floor level with the one it is walking onto once rounding is involved.
const stepEntryEpsilon = float32(1e-3)
