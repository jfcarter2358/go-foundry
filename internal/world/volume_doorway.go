package world

// volume_doorway.go — the way in and out of a moving thing.
//
// A frame doorway is the boundary between two coordinate systems that move
// relative to each other: a dropship's ramp opening onto the ground, a shuttle's
// airlock onto a hangar deck. Walking through one is not a teleport — it is a
// change of frame, and position, velocity and facing are all carried across it
// (see collision/frame_portal.go, which owns that half).
//
// # Why a volume
//
// A doorway used to be a *portal edge* whose target sector lived in another
// frame, and it was resolved two ways at once: by proximity to the authored
// line where both halves existed, and by containment in the doorway's sector
// where only the moving side did. Portals and sectors are both gone, and of the
// two rules only containment survives the format — a brush face is not a
// boundary between two spaces, it is one side of a solid.
//
// Containment is also the better of the two rules, which is why losing the
// other costs little. Proximity fires while the occupant is still short of the
// line, so the two sides both claim the same ground and a hysteresis rule was
// needed to stop the occupant being traded back and forth once per tick. Inside
// a volume and outside it cannot both be true, so the two directions are
// antisymmetric by construction and no such rule is needed.
//
// What containment gives up is tunnelling protection: there is no "before" at
// which to hand the occupant over early, so a doorway volume has to be deeper
// than one step of whatever walks through it. The shipped dropship's vestibule
// is a little over three metres against a walk of a few centimetres a tick.
// A projectile-speed body would pass straight through, and that is the trade.
//
// # Why a field on Volume rather than a type of its own
//
// The same reason Fog and LoadLevel are fields: everything a doorway needs — a
// shape, a rotation, half-extents, an ID, an `enabled` flag a script can flip
// to lock the ramp, a wireframe cage to author it against, the gizmos to move
// and size it — a volume already has. All a doorway adds is which frame it
// opens onto. A separate type would have been those six things copied so the
// seventh could be new.
//
// One consequence worth stating: a doorway volume is still a volume, so it also
// fires `enter` and `exit`, and disabling it seals the door AND disarms the
// trigger. That is the intended reading — "someone is in the airlock" is
// exactly the event a level wants when there is an airlock to be in.

// FrameDoorway marks a volume as the boundary between its own frame and
// another.
//
// The volume's own Frame says which side it belongs to — a doorway is authored
// on the MOVING side, riding the ship, because that is the side that knows
// where its own ramp is. To names the frame on the other side.
type FrameDoorway struct {
	// To is the frame the doorway opens onto, named as an entity stores it: an
	// entity ID, or "" for the world. The overwhelmingly common case is the
	// world — a ship's ramp opens onto the ground — which is why the zero value
	// is the right default rather than an error.
	//
	// A doorway naming a specific vehicle is a dock: a carrier's hangar that
	// only admits from the shuttle it was built for. Nothing enforces that the
	// named frame exists; a doorway onto a vehicle that has been destroyed
	// simply does not admit, which is the correct behaviour for a hangar whose
	// carrier is gone.
	To string
}

// DoorwayLeadsTo is the frame ID a doorway hands an occupant to. Kept as a
// function rather than read off the field so that "" reliably means the world
// at every call site, whether it was authored as "" or as "world".
func (d *FrameDoorway) DoorwayLeadsTo() string {
	if d == nil {
		return ""
	}
	return FrameID(d.To)
}

// ---------- serialization ----------

// FrameDoorwayFromRaw converts the YAML form. nil in, nil out: a volume with no
// `doorway:` block is not a doorway.
func FrameDoorwayFromRaw(r *RawFrameDoorway) *FrameDoorway {
	if r == nil {
		return nil
	}
	return &FrameDoorway{To: r.To}
}

// FrameDoorwayToRaw is the inverse.
func FrameDoorwayToRaw(d *FrameDoorway) *RawFrameDoorway {
	if d == nil {
		return nil
	}
	return &RawFrameDoorway{To: d.To}
}
