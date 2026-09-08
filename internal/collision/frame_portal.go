package collision

// frame_portal.go — walking out of a moving thing.
//
// A frame portal is a portal edge whose target sector lives in a different
// frame: a shuttle's ramp opening onto a hangar deck, or onto the ground. The
// two spaces move relative to each other, which is the whole difficulty.
//
// # The gating rule
//
// Every resolver in this package is XZ-planar. It assumes down is −Y, that a
// sector's footprint is a 2D polygon, and that a height is a scalar. Two frames
// can only be reconciled inside those assumptions while their relative transform
// is **gravity-aligned**: the relative rotation is a yaw, and their up vectors
// agree. Under that condition a neighbouring sector transforms into the
// querying frame as a 2D rigid motion — a yaw-rotated polygon is still a
// polygon, a Y offset is still a Y offset — and every existing resolver applies
// unchanged.
//
// A shuttle resting on a deck qualifies. So does one flying straight and level
// alongside. A barrel-rolling shuttle does not, and while it is misaligned the
// portal **seals**: it resolves as a solid wall, so nobody walks out of a ship
// through a doorway that is currently pointing at the sky.
//
// Lifting the rule entirely would mean making collision non-planar, which is a
// rewrite of the engine's oldest assumption rather than a feature. Sealing is a
// stated design rule with a defined failure mode, and a script hears about it.
//
// # The handoff
//
// Crossing is a change of frame, not a teleport: position, velocity *and*
// facing are all carried through the relative transform. Converting all three is
// what makes it seamless — convert only the position and the player arrives
// walking sideways; convert only position and velocity and the view snaps.

import (
	"foundry/internal/geom"
	"foundry/internal/world"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// FrameAlignTolerance is how far two frames' up vectors may diverge and still be
// joined, as the cosine of the angle between them. About eight degrees — enough
// for a ship holding station in atmosphere to wobble, tight enough that nobody
// walks out onto a wall.
const FrameAlignTolerance = float32(0.99)

// FramesAligned reports whether two frames can currently be walked between:
// their up vectors agree, so the relative transform is a yaw and a translation.
func FramesAligned(a, b *world.Frame) bool {
	up := mgl32.Vec3{0, 1, 0}
	ua := a.DirToWorld(up)
	ub := b.DirToWorld(up)
	return ua.Dot(ub) >= FrameAlignTolerance
}

// FramePortalCrossing is a portal an occupant is standing in the doorway of,
// and where it leads.
type FramePortalCrossing struct {
	Portal   *world.Portal
	FromID   string // frame being left
	ToID     string // frame being entered
	Sealed   bool   // the frames are misaligned; this is a wall today
	Relative mgl32.Mat4
}

// portalCrossingBand is how close to a portal's line an occupant must be for the
// crossing to fire, in world units. Wide enough that a fast step cannot land
// past it in one frame, narrow enough that standing beside a doorway does not
// count as being in it.
//
// It is also a constraint on how the two sides are AUTHORED, which is the part
// that bites. Crossing on proximity means arriving up to this far short of the
// line — so the far side must be a place the occupant can stand at that
// distance, in every direction. Two portals in the same place leave them in
// neither sector; the two spaces have to overlap by more than this band, and
// the far sector has to be wider than the doorway by it. See DESIGN.md's Frame
// portals section, and the shipped dropship for the shape that satisfies it.
const portalCrossingBand = float32(0.6)

// FindFramePortalCrossing looks for a frame boundary the entity is currently
// standing on, in whatever frame it is standing in.
//
// Two rules answer, in order. An AUTHORED doorway — both halves written into
// the level, the two lines a couple of metres apart — is resolved by proximity,
// which is what it has always been. Where only the moving side is authored, the
// UNPARTNERED rule takes over and resolves by containment instead. See the
// "Unpartnered doorways" section below for why they cannot be the same rule.
func FindFramePortalCrossing(lvl *world.Level, e *world.Entity) *FramePortalCrossing {
	if lvl == nil || e == nil {
		return nil
	}
	if c := authoredCrossing(lvl, e); c != nil {
		return c
	}
	// Strapped in is not walking. A seat is a constraint rather than a place —
	// and one authored outside the interior's own sectors, a cockpit perched on
	// a nose, would otherwise read as its occupant having stepped off the deck
	// on every single tick.
	if e.Seat != "" {
		return nil
	}
	if c := steppedOffFrame(lvl, e); c != nil {
		return c
	}
	return steppedIntoFrame(lvl, e)
}

// authoredCrossing is the proximity rule: a doorway whose two halves are both
// written into the level, resolved by how close the occupant is to the line.
func authoredCrossing(lvl *world.Level, e *world.Entity) *FramePortalCrossing {
	if e.CurrentSector == "" {
		return nil
	}
	frame := lvl.Frames.FrameFor(e)
	chunk, sector := frame.ChunkAndSectorAt(lvl, e.Position)
	if chunk == nil || sector == nil {
		return nil
	}
	// Portal StartPoint/EndPoint are chunk-local; e.Position is only carried as
	// far as chunk's frame (see chunkLocal's own doc comment).
	local := chunkLocal(chunk, e.Position)
	xz := mgl32.Vec2{local.X(), local.Z()}

	for i := range sector.Portals {
		p := &sector.Portals[i]
		if p.ToFrame == "" || world.FrameID(p.ToFrame) == e.Frame {
			continue
		}
		_, dist := closestPointOnSegment2D(xz, p.StartPoint, p.EndPoint)
		if dist > portalCrossingBand {
			continue
		}
		to := lvl.Frames.Frame(p.ToFrame)
		if to == nil {
			continue // the far side's frame is gone
		}
		c := &FramePortalCrossing{Portal: p, FromID: e.Frame, ToID: world.FrameID(p.ToFrame)}
		if !FramesAligned(frame, to) {
			c.Sealed = true
			return c
		}
		if !crossingSettles(lvl, frame, to, e.Frame, e.Position, dist) {
			continue
		}
		c.Relative = to.WorldMatrix().Inv().Mul4(frame.WorldMatrix())
		return c
	}
	return nil
}

// crossingSettles reports whether handing an occupant through this portal
// actually leaves them somewhere better than where they stand.
//
// Crossing fires on proximity, so when the two halves of a doorway are closer
// together than the band they BOTH claim the same spot: the occupant is within
// the band of the line they are leaving and, once carried over, within the band
// of the line that leads straight back. Nothing in the geometry breaks — they
// simply get traded across once per tick, forever.
//
// What that looks like is not a movement bug, which is why it is worth naming
// here. Portal-visibility culling starts its search from the occupant's
// CurrentChunk (see the render loop in internal/app), and crossing rewrites it;
// a player oscillating between a ship and the bay it is parked in has each
// interior culled on alternate frames, and the screen strobes between the two.
//
// The rule is that a crossing has to go DEEPER: the occupant must end up
// farther from the way back than they currently are from the line they are
// crossing. It is antisymmetric, so the reverse crossing cannot also pass and
// the trade cannot start. Where the two sides overlap by more than the band —
// the arrangement DESIGN.md asks for — every crossing satisfies it and this
// costs one sector lookup. Where they do not, the doorway holds until a step
// carries the occupant far enough in to settle, instead of strobing.
//
// A destination with no way back is infinitely deep and always admits, which
// covers both the one-way portal and the arrival that lands in no sector at all
// — walking out of a doorway with nothing under it is the OTHER thing this
// geometry does wrong, it has its own diagnosis, and it is not this function's
// to refuse. Stepping off a hovering ship into open air is the same shape and
// is meant to work.
func crossingSettles(lvl *world.Level, from, to *world.Frame, fromID string, pos mgl32.Vec3, dist float32) bool {
	dstPos := to.FromWorld(from.ToWorld(pos))
	dstChunk, dstSector := to.ChunkAndSectorAt(lvl, dstPos)
	if dstChunk == nil || dstSector == nil {
		return true
	}
	// Portal StartPoint/EndPoint are chunk-local, the same as the outgoing
	// measurement this is compared against.
	local := chunkLocal(dstChunk, dstPos)
	xz := mgl32.Vec2{local.X(), local.Z()}
	partnered := false
	for i := range dstSector.Portals {
		q := &dstSector.Portals[i]
		if q.ToFrame == "" || world.FrameID(q.ToFrame) != fromID {
			continue
		}
		partnered = true
		if _, back := closestPointOnSegment2D(xz, q.StartPoint, q.EndPoint); back <= dist {
			return false
		}
	}
	if partnered {
		return true
	}
	// Nothing authored on the far side, so the way back is not a line at all —
	// it is the unpartnered rule below, under which standing inside a doorway
	// sector of the frame being left IS being aboard it. Refuse while that is
	// still true, or proximity hands the occupant out on one tick and
	// containment pulls them back on the next: the same trade, one rule apart.
	//
	// The practical effect is that an unpartnered doorway is resolved by
	// containment in BOTH directions. Proximity still governs the moment the
	// arrival lands in no sector at all — stepping out of a doorway with
	// nothing under it, which is a fall rather than a crossing.
	_, srcSector := from.ChunkAndSectorAt(lvl, pos)
	return framePortalTo(srcSector, to.OwnerID) == nil
}

// ---------- unpartnered doorways ----------
//
// A frame portal is authored in two halves, and until now both had to exist: a
// ship's ramp names the world, and the world needs an edge of its own naming
// the ship, parked so the two lines land within a couple of metres of each
// other. That is a DOCK. It cannot describe a ship that sets down wherever the
// pilot puts it, because the level would have to have guessed the spot.
//
// So the moving side's half stands on its own. A doorway sector — one carrying
// a portal that names another frame — is the boundary, and which side of it an
// occupant is on is settled by CONTAINMENT rather than by proximity to a line:
//
//   - Standing inside a ship's doorway sector, whatever frame you are in, is
//     being aboard that ship.
//   - Being aboard and inside none of its sectors is having stepped off it.
//
// Containment is what makes it work with no second half to measure against.
// Proximity needs two lines: it fires while the occupant is still short of the
// one they are crossing, which is only safe when the far side reaches past it.
// With a single shared boundary there is nothing to reach past, and a band
// around one line claims the same ground from both sides — the strobe
// crossingSettles exists to prevent. Containment has no such failure: inside a
// sector and inside no sector cannot both be true, so the two directions are
// antisymmetric by construction and no hysteresis is needed.
//
// What is given up is tunnelling protection. Proximity hands an occupant over
// before they reach the line so a fast step cannot jump it; containment cannot,
// because there is no "before". A doorway sector therefore has to be deeper
// than one step of whatever walks through it — the shipped vestibule is 3.3m,
// against a walk of a few centimetres a tick — and a projectile-speed body
// would pass straight through. That is the trade for a ship that can land
// anywhere, and it is why the authored path is still the one a dock uses.

// framePortalTo returns the sector's doorway onto a named frame, or nil when it
// has none. The frame is named as an entity stores it, so "" is the world.
func framePortalTo(s *world.Sector, frameID string) *world.Portal {
	if s == nil {
		return nil
	}
	for i := range s.Portals {
		p := &s.Portals[i]
		if p.ToFrame != "" && world.FrameID(p.ToFrame) == frameID {
			return p
		}
	}
	return nil
}

// frameHasDoorwayTo reports whether any sector a frame owns opens onto a named
// frame. A frame with no doorway at all is sealed, and its contents stay in it
// however far they wander off its floors.
func frameHasDoorwayTo(lvl *world.Level, f *world.Frame, frameID string) bool {
	if lvl == nil || f == nil {
		return false
	}
	for _, ci := range f.ChunkIdx {
		if ci < 0 || ci >= len(lvl.Chunks) {
			continue
		}
		ch := &lvl.Chunks[ci]
		if !ch.IsEnabled() {
			continue
		}
		for si := range ch.Sectors {
			if framePortalTo(&ch.Sectors[si], frameID) != nil {
				return true
			}
		}
	}
	return false
}

// steppedIntoFrame hands an occupant standing inside another frame's doorway
// sector aboard that frame.
//
// The scan is over frames rather than over geometry because a frame owns a
// handful of chunks and ChunkAndSectorAt rejects a distant one on its bounds
// before testing a single polygon — so a level full of parked ships costs one
// box test each.
func steppedIntoFrame(lvl *world.Level, e *world.Entity) *FramePortalCrossing {
	from := lvl.Frames.FrameFor(e)
	if from == nil {
		return nil
	}
	// Where the occupant already stands decides whether this rule applies at
	// all: a sector that names the target frame is an AUTHORED half, and the
	// proximity rule above owns that boundary. Containment is the fallback for
	// a join the level never wrote, not a second opinion on one it did.
	_, here := from.ChunkAndSectorAt(lvl, e.Position)

	wp := from.ToWorld(e.Position)
	for _, to := range lvl.Frames.Order() {
		if to == nil || to.OwnerID == e.Frame || to.OwnerID == e.ID {
			continue
		}
		if framePortalTo(here, to.OwnerID) != nil {
			continue // authored; not this rule's business
		}
		if !FramesAligned(from, to) {
			// A doorway pointing at the sky is not a way in. Nothing is sealed
			// here in the ResolveSealedPortals sense — there is no wall on this
			// side to be pushed off — so the boundary simply does not admit.
			continue
		}
		if wouldCycle(lvl, e.ID, to.OwnerID) {
			continue // a ship cannot be carried into its own hangar
		}
		_, sector := to.ChunkAndSectorAt(lvl, to.FromWorld(wp))
		p := framePortalTo(sector, e.Frame)
		if p == nil {
			continue
		}
		return &FramePortalCrossing{
			Portal:   p,
			FromID:   e.Frame,
			ToID:     to.OwnerID,
			Relative: to.WorldMatrix().Inv().Mul4(from.WorldMatrix()),
		}
	}
	return nil
}

// steppedOffFrame hands an occupant who is aboard a frame but inside none of
// its sectors out into the space that frame is itself in.
//
// Being in a frame is standing on its floors; off the end of them there is
// nothing to stand on and nothing to resolve against, and staying would mean
// being dragged along by a ship the occupant is no longer in. The outer space
// having no floor there either is not a reason to hold on to them — that is a
// fall, and it should be a fall in the coordinates they fell in.
func steppedOffFrame(lvl *world.Level, e *world.Entity) *FramePortalCrossing {
	if e.Frame == "" || e.CurrentSector != "" {
		return nil
	}
	from := lvl.Frames.FrameFor(e)
	if from == nil || from.IsWorld() {
		return nil
	}
	owner := world.FindEntity(lvl, e.Frame)
	if owner == nil {
		return nil // the carrier is gone; Dismount owns that case
	}
	to := lvl.Frames.Frame(owner.Frame)
	if to == nil || !FramesAligned(from, to) {
		return nil
	}
	if !frameHasDoorwayTo(lvl, from, owner.Frame) {
		return nil
	}
	return &FramePortalCrossing{
		FromID:   e.Frame,
		ToID:     to.OwnerID,
		Relative: to.WorldMatrix().Inv().Mul4(from.WorldMatrix()),
	}
}

// CrossFramePortal moves an entity from one frame to the other, carrying its
// position, velocity and facing through the relative transform.
//
// yaw is the entity's look angle in the frame it is leaving; the returned yaw is
// the same facing expressed in the frame it has entered. A caller with no look
// angle to carry may pass and ignore zero.
func CrossFramePortal(lvl *world.Level, e *world.Entity, c *FramePortalCrossing, yaw float32) (newYaw float32) {
	if lvl == nil || e == nil || c == nil || c.Sealed {
		return yaw
	}
	from := lvl.Frames.Frame(c.FromID)
	to := lvl.Frames.Frame(c.ToID)

	e.Position = to.FromWorld(from.ToWorld(e.Position))
	e.Velocity = to.DirFromWorld(from.DirToWorld(e.Velocity))

	// The facing goes through the same rotation. Measured as a direction rather
	// than as an angle difference, so it stays right when the yaw wraps.
	dir := from.DirToWorld(mgl32.Vec3{
		float32(math.Cos(float64(yaw))), 0, float32(math.Sin(float64(yaw))),
	})
	local := to.DirFromWorld(dir)
	newYaw = float32(math.Atan2(float64(local.Z()), float64(local.X())))

	e.Frame = c.ToID
	e.Seat = ""
	// Leaving a vehicle's frame through a doorway is leaving the vehicle.
	if v := world.FindEntity(lvl, c.FromID); v != nil && v.VState != nil {
		v.VState.RemoveOccupant(e.ID)
	}
	// And arriving in one is boarding it.
	if v := world.FindEntity(lvl, c.ToID); v != nil && v.IsVehicle() {
		v.EnsureVehicleState().AddOccupant(e.ID, "")
	}
	e.UpdateLocation(lvl)
	return newYaw
}

// SealedPortalSegments returns the portal edges in a sector that are currently
// sealed — frame portals whose far side is not gravity-aligned — so the wall
// resolve can treat them as solid.
//
// A sealed portal has no wall geometry of its own to collide with, which is the
// whole reason this exists: without it a misaligned doorway is simply a hole.
func SealedPortalSegments(lvl *world.Level, frameID string, sector *world.Sector) [][2]mgl32.Vec2 {
	if lvl == nil || sector == nil {
		return nil
	}
	frame := lvl.Frames.Frame(frameID)
	var out [][2]mgl32.Vec2
	for i := range sector.Portals {
		p := &sector.Portals[i]
		if p.ToFrame == "" || world.FrameID(p.ToFrame) == frameID {
			continue
		}
		if to := lvl.Frames.Frame(p.ToFrame); !FramesAligned(frame, to) {
			out = append(out, [2]mgl32.Vec2{p.StartPoint, p.EndPoint})
		}
	}
	return out
}

// ResolveSealedPortals pushes a body out of any sealed frame portal in its
// sector, treating each as a wall.
//
// It runs after the ordinary wall resolve rather than inside it: a sealed portal
// is a temporary condition of the level rather than a property of the geometry,
// and folding it into resolveWallsForSector would mean handing that function a
// level and a frame it otherwise has no use for.
func ResolveSealedPortals(lvl *world.Level, e *world.Entity, pos mgl32.Vec3) mgl32.Vec3 {
	if lvl == nil || e == nil || e.CurrentSector == "" {
		return pos
	}
	frame := lvl.Frames.FrameFor(e)
	chunk, sector := frame.ChunkAndSectorAt(lvl, pos)
	if sector == nil {
		return pos
	}
	segs := SealedPortalSegments(lvl, e.Frame, sector)
	if len(segs) == 0 {
		return pos
	}

	radius := geom.Abs32(e.BoundHalfX)
	if h := geom.Abs32(e.BoundHalfZ); h > radius {
		radius = h
	}
	const epsilon = float32(0.001)

	// Segment endpoints are chunk-local (they come straight from portal
	// StartPoint/EndPoint), so pos is carried in the same step as
	// FindFramePortalCrossing's own lookup above.
	local := chunkLocal(chunk, pos)
	xz := mgl32.Vec2{local.X(), local.Z()}
	for _, seg := range segs {
		closest, dist := closestPointOnSegment2D(xz, seg[0], seg[1])
		if dist >= radius+epsilon {
			continue
		}
		away := xz.Sub(closest)
		if away.Len() < 1e-6 {
			edge := seg[1].Sub(seg[0])
			away = mgl32.Vec2{-edge.Y(), edge.X()}
			if away.Len() < 1e-6 {
				continue
			}
		}
		away = away.Normalize()
		xz = closest.Add(away.Mul(radius + epsilon))
	}
	return chunkWorld(chunk, mgl32.Vec3{xz.X(), local.Y(), xz.Y()})
}
