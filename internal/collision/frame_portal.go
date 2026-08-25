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
const portalCrossingBand = float32(0.6)

// FindFramePortalCrossing looks for a frame portal the entity is currently in
// the doorway of, in whatever frame it is standing in.
func FindFramePortalCrossing(lvl *world.Level, e *world.Entity) *FramePortalCrossing {
	if lvl == nil || e == nil || e.CurrentSector == "" {
		return nil
	}
	frame := lvl.Frames.FrameFor(e)
	chunk, sector := frame.ChunkAndSectorAt(lvl, e.Position)
	if chunk == nil || sector == nil {
		return nil
	}
	xz := mgl32.Vec2{e.Position.X(), e.Position.Z()}

	for i := range sector.Portals {
		p := &sector.Portals[i]
		if p.ToFrame == "" || world.FrameID(p.ToFrame) == e.Frame {
			continue
		}
		if _, dist := closestPointOnSegment2D(xz, p.StartPoint, p.EndPoint); dist > portalCrossingBand {
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
		c.Relative = to.WorldMatrix().Inv().Mul4(frame.WorldMatrix())
		return c
	}
	return nil
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
	_, sector := frame.ChunkAndSectorAt(lvl, pos)
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

	xz := mgl32.Vec2{pos.X(), pos.Z()}
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
	return mgl32.Vec3{xz.X(), pos.Y(), xz.Y()}
}
