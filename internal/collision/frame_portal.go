package collision

// frame_portal.go — walking out of a moving thing.
//
// TODO(brush migration, Phase 5): this used to detect a doorway between two
// frames (a shuttle's ramp opening onto a hangar deck) by proximity to an
// authored world.Portal edge, or by containment in a sector that named the
// far frame. Both world.Portal and world.Sector are gone. The plan's
// replacement is a Volume.FrameDoorway field (mirroring Volume.Fog) — an
// author-placed trigger volume at the doorway, tested by containment/entry
// exactly the way every other volume trigger already is — but that field
// does not exist yet (Volumes are a later phase of the same migration).
//
// Until it lands, this file keeps the same public shape (so callers compile
// and the frame math below — FramesAligned, CrossFramePortal's handoff — is
// ready to be driven by the real thing) but FindFramePortalCrossing always
// reports no crossing: nobody currently boards or disembarks a moving frame
// by walking through its doorway. The dropship entity (and anything else
// that relies on this) needs Phase 5 before boarding works again.

import (
	"math"

	"foundry/internal/world"

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

// FramePortalCrossing is a doorway an occupant is standing in, and where it
// leads. Portal is nil until Phase 5's Volume.FrameDoorway replaces it.
type FramePortalCrossing struct {
	FromID   string // frame being left
	ToID     string // frame being entered
	Sealed   bool   // the frames are misaligned; this is a wall today
	Relative mgl32.Mat4
}

// FindFramePortalCrossing looks for a frame boundary the entity is currently
// standing on, in whatever frame it is standing in.
//
// Always nil for now — see the file comment.
func FindFramePortalCrossing(lvl *world.Level, e *world.Entity) *FramePortalCrossing {
	return nil
}

// CrossFramePortal moves an entity from one frame to the other, carrying its
// position, velocity and facing through the relative transform.
//
// yaw is the entity's look angle in the frame it is leaving; the returned yaw is
// the same facing expressed in the frame it has entered. A caller with no look
// angle to carry may pass and ignore zero. Kept ready for Phase 5 even though
// FindFramePortalCrossing never currently returns a crossing to feed it.
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

// ResolveSealedPortals pushes a body out of any sealed frame doorway.
//
// A no-op for now — see the file comment. It stays in the call chain
// (player.go calls it unconditionally) so Phase 5 only has to fill this in,
// not thread a new call site through.
func ResolveSealedPortals(lvl *world.Level, e *world.Entity, pos mgl32.Vec3) mgl32.Vec3 {
	return pos
}
