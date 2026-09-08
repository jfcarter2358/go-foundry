package collision

// frame_portal.go — walking out of a moving thing.
//
// A frame doorway is the boundary between two coordinate systems that move
// relative to each other: a dropship's ramp opening onto the ground, or onto a
// hangar deck. The two spaces move relative to each other, which is the whole
// difficulty.
//
// # The gating rule
//
// Every resolver in this package assumes down is −Y and that a height is a
// scalar. Two frames can only be reconciled inside those assumptions while
// their relative transform is **gravity-aligned**: the relative rotation is a
// yaw, and their up vectors agree. Under that condition the far side transforms
// into the querying frame as a rigid motion that leaves "up" alone, and every
// existing resolver applies unchanged.
//
// A shuttle resting on a deck qualifies. So does one flying straight and level
// alongside. A barrel-rolling shuttle does not, and while it is misaligned the
// doorway **seals**: it resolves as solid, so nobody walks out of a ship
// through a hatch that is currently pointing at the sky.
//
// Lifting the rule entirely would mean making collision non-planar, which is a
// rewrite of the engine's oldest assumption rather than a feature. Sealing is a
// stated design rule with a defined failure mode, and a script hears about it.
//
// # The boundary itself
//
// A doorway is an author-placed volume carrying a FrameDoorway (see
// world/volume_doorway.go), riding the moving side. Which side of it an
// occupant is on is settled by CONTAINMENT:
//
//   - Standing inside a ship's doorway volume, whatever frame you are in, is
//     being aboard that ship.
//   - Being aboard, in none of its chunks, and the ship having a doorway at
//     all, is having stepped off it.
//
// The two are antisymmetric by construction — inside a volume and inside no
// chunk of the frame that owns it cannot both be true — so no hysteresis is
// needed. That is the whole reason the format's *other* rule did not survive:
// resolving a doorway by proximity to an authored line fires while the occupant
// is still short of it, so both sides claim the same ground and an occupant
// gets traded back and forth once per tick unless a "must go deeper" rule
// arbitrates. There is no such failure here.
//
// # The handoff
//
// Crossing is a change of frame, not a teleport: position, velocity *and*
// facing are all carried through the relative transform. Converting all three
// is what makes it seamless — convert only the position and the player arrives
// walking sideways; convert only position and velocity and the view snaps.

import (
	"math"

	"foundry/internal/geom"
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
// leads.
type FramePortalCrossing struct {
	// Doorway is the volume that admitted, or nil for a step off the end of a
	// frame — which is a crossing with no doorway to stand in, only a frame to
	// leave.
	Doorway  *world.Volume
	FromID   string // frame being left
	ToID     string // frame being entered
	Sealed   bool   // the frames are misaligned; this is a wall today
	Relative mgl32.Mat4
}

// FindFramePortalCrossing looks for a frame boundary the entity is currently
// standing on, in whatever frame it is standing in.
func FindFramePortalCrossing(lvl *world.Level, e *world.Entity) *FramePortalCrossing {
	if lvl == nil || e == nil {
		return nil
	}
	// Strapped in is not walking. A seat is a constraint rather than a place —
	// and one authored outside the interior's own chunks, a cockpit perched on
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

// steppedIntoFrame hands an occupant standing inside another frame's doorway
// volume aboard that frame.
//
// The scan is over doorway volumes rather than over frames, because a level has
// a handful of doorways and any number of frames — and a doorway that admits is
// the whole of what has to be found.
func steppedIntoFrame(lvl *world.Level, e *world.Entity) *FramePortalCrossing {
	from := lvl.Frames.FrameFor(e)
	if from == nil {
		return nil
	}
	wp := from.ToWorld(e.Position)

	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		if v.Doorway == nil || !v.Enabled || !v.HasBounds() {
			continue
		}
		// A doorway rides the frame it belongs to, and admits INTO it. One
		// belonging to the frame the occupant is already in is the way out,
		// not the way in — that is steppedOffFrame's business.
		if v.Frame == e.Frame {
			continue
		}
		// And it only admits from the side it opens onto. A ship's ramp names
		// the world, so an occupant aboard a second ship parked inside the
		// first cannot walk through it sideways.
		if v.Doorway.DoorwayLeadsTo() != e.Frame {
			continue
		}
		to := lvl.Frames.Frame(v.Frame)
		if to == nil || to.OwnerID == e.ID {
			continue // the frame is gone, or is the occupant's own
		}
		if !v.ContainsWorldPoint(lvl, wp) {
			continue
		}
		if !FramesAligned(from, to) {
			// A hatch pointing at the sky is not a way in. Nothing is sealed
			// here in the ResolveSealedPortals sense — the occupant is outside
			// the ship, and the ship's own hull is what stops them — so the
			// boundary simply does not admit.
			continue
		}
		if wouldCycle(lvl, e.ID, to.OwnerID) {
			continue // a ship cannot be carried into its own hangar
		}
		return &FramePortalCrossing{
			Doorway:  v,
			FromID:   e.Frame,
			ToID:     to.OwnerID,
			Relative: to.WorldMatrix().Inv().Mul4(from.WorldMatrix()),
		}
	}
	return nil
}

// steppedOffFrame hands an occupant who is aboard a frame but inside none of
// its chunks out into the space that frame is itself in.
//
// Being in a frame is standing on its floors; off the end of them there is
// nothing to stand on and nothing to resolve against, and staying would mean
// being dragged along by a ship the occupant is no longer in. The outer space
// having no floor there either is not a reason to hold on to them — that is a
// fall, and it should be a fall in the coordinates they fell in.
//
// The doorway is not tested for containment on the way out, only for existence.
// An occupant leaving through the ramp is inside the doorway volume, and a
// doorway that admitted on the way in must not refuse on the way out or they
// would be stuck in it; what actually ends the crossing is leaving the ship's
// chunks entirely. A frame with no doorway at all is sealed, and its contents
// stay in it however far they wander off its floors.
func steppedOffFrame(lvl *world.Level, e *world.Entity) *FramePortalCrossing {
	if e.Frame == "" || e.CurrentChunk != "" {
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
	// A doorway leading where the occupant would land. A ship whose only hatch
	// opens onto a specific carrier does not spill its crew onto the ground.
	var door *world.Volume
	for _, v := range world.FrameDoorways(lvl, e.Frame) {
		if v.Doorway.DoorwayLeadsTo() == to.OwnerID {
			door = v
			break
		}
	}
	if door == nil {
		return nil
	}
	return &FramePortalCrossing{
		Doorway:  door,
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

// SealedDoorways returns the doorway volumes an occupant in frameID currently
// cannot walk through — those whose far side is not gravity-aligned — so the
// resolve below can treat each as solid.
//
// A doorway has no geometry of its own, which is the whole reason this exists:
// the brush wall it is cut through has a hole in it, and without this a
// misaligned hatch is simply that hole.
func SealedDoorways(lvl *world.Level, frameID string) []*world.Volume {
	if lvl == nil {
		return nil
	}
	frame := lvl.Frames.Frame(frameID)
	var out []*world.Volume
	for _, v := range world.FrameDoorways(lvl, frameID) {
		if to := lvl.Frames.Frame(v.Doorway.DoorwayLeadsTo()); !FramesAligned(frame, to) {
			out = append(out, v)
		}
	}
	return out
}

// ResolveSealedPortals pushes a body out of any sealed doorway in its frame,
// treating each as solid.
//
// It runs after the ordinary brush resolve rather than inside it: a sealed
// doorway is a temporary condition of the level rather than a property of the
// geometry, and folding it into the brush resolver would mean handing that
// function a level and a frame it otherwise has no use for.
//
// The push is out through the nearest face of the volume's own box, which is
// the honest reading of "this doorway is currently a wall": the volume IS the
// opening, so its boundary is where the wall would be.
func ResolveSealedPortals(lvl *world.Level, e *world.Entity, pos mgl32.Vec3) mgl32.Vec3 {
	if lvl == nil || e == nil || e.Frame == "" {
		return pos
	}
	sealed := SealedDoorways(lvl, e.Frame)
	if len(sealed) == 0 {
		return pos
	}

	radius := geom.Abs32(e.BoundHalfX)
	if h := geom.Abs32(e.BoundHalfZ); h > radius {
		radius = h
	}
	const epsilon = float32(0.001)

	// pos is in the entity's own frame, which is the doorway's too — a doorway
	// rides the frame it belongs to, and this only looks at that frame's own.
	for _, v := range sealed {
		out, moved := pushOutOfVolumeXZ(v, pos, radius+epsilon)
		if moved {
			pos = out
		}
	}
	return pos
}

// pushOutOfVolumeXZ moves p horizontally out of v's region, keeping a body of
// the given radius clear of it, and reports whether it had to.
//
// XZ only, and out through the nearest side: a sealed doorway is a wall, and a
// wall does not lift anyone off the floor. The nearest side is the one the body
// most recently came in through for any step small compared with the volume,
// which is every step a walking body takes.
func pushOutOfVolumeXZ(v *world.Volume, p mgl32.Vec3, radius float32) (mgl32.Vec3, bool) {
	// Measured in the volume's own space so a rotated doorway is the shape it
	// was authored as, then carried back — the same reasoning ContainsPoint's
	// callers follow.
	rot := world.EulerMatrixYXZ(v.Rotation)
	inv := rot.Inv()
	rel4 := inv.Mul4x1(p.Sub(v.Position).Vec4(1))
	rel := mgl32.Vec3{rel4.X(), rel4.Y(), rel4.Z()}

	h := v.HalfExtents()
	if geom.Abs32(rel.X()) >= h.X()+radius ||
		geom.Abs32(rel.Y()) >= h.Y() ||
		geom.Abs32(rel.Z()) >= h.Z()+radius {
		return p, false
	}

	// The shallower of the two horizontal overlaps is the way out.
	overX := h.X() + radius - geom.Abs32(rel.X())
	overZ := h.Z() + radius - geom.Abs32(rel.Z())
	if overX <= overZ {
		rel[0] = geom.Sign32(rel.X()) * (h.X() + radius)
	} else {
		rel[2] = geom.Sign32(rel.Z()) * (h.Z() + radius)
	}

	out4 := rot.Mul4x1(rel.Vec4(1))
	return v.Position.Add(mgl32.Vec3{out4.X(), out4.Y(), out4.Z()}), true
}
