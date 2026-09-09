package collision

// entity_brushes.go — an entity's own solid, seen from outside it.
//
// An entity's brushes (world/entity_brush.go) materialise into a chunk whose
// FrameOwner is that entity, and a frame-owned chunk is deliberately absent
// from the world spatial hash (world/spatial.go): its geometry is in the
// entity's coordinates, so answering a world-space query with it would be
// subtracting numbers that do not mean the same thing.
//
// That is exactly right for a body that is ABOARD — it is in those coordinates
// too, and every resolver in this package works unchanged — and exactly wrong
// for one that is not, which would walk straight through the hull on its way to
// the inside. This file is the other half: for a body standing in some other
// frame, every entity's brushes are solid, resolved by carrying the body into
// that entity's space, pushing it out there, and carrying it back.
//
// # Why the body is carried rather than the brushes
//
// The alternative is to transform each brush's hull into the body's frame and
// resolve there. It is the same arithmetic once, and much more of it per frame:
// a hull is eight-odd vertices and a body is one point. Carrying the body also
// means the push runs in the same space, against the same ConvexHull, with the
// same epsilon as the aboard case — so the hull a body is stopped by from
// outside and the one it walks on from inside cannot disagree about where the
// wall is.
//
// # The alignment rule, again
//
// Every resolver here assumes down is −Y, and the body's shape is a cylinder
// standing up in its own frame. Carried into a frame that is rolled relative to
// it, that cylinder would be lying on its side and the resolve would be
// nonsense. So the same gate frame_portal.go applies to walking between frames
// applies to being stopped by one: a misaligned entity's brushes are skipped,
// and what stops a body then is the entity's own bounding volume through the
// ordinary entity pass. A stated failure mode with a defined fallback, rather
// than a silently wrong push.

import (
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// entityGeometry is one entity's materialised solid, ready to resolve against:
// the chunk holding its brushes and the frame those brushes live in.
type entityGeometry struct {
	Chunk *world.Chunk
	Frame *world.Frame
}

// entityGeometries is every entity-owned solid in the level.
//
// Found by walking the chunks rather than the entities because the chunk is
// what actually holds the geometry, and a chunk that failed to materialise
// simply is not here — there is no "the entity says it has brushes but nothing
// was built" case to guard against.
//
// A disabled chunk is skipped for the reason the spatial hash skips one: a
// switched-off chunk is not in the world, and collision, location and the
// script location verbs all have to agree about that.
func entityGeometries(lvl *world.Level) []entityGeometry {
	if lvl == nil || lvl.Frames == nil {
		return nil
	}
	var out []entityGeometry
	for i := range lvl.Chunks {
		ch := &lvl.Chunks[i]
		if ch.FromEntity == "" || !ch.InFrame() || !ch.IsEnabled() {
			continue
		}
		f := lvl.Frames.Frame(ch.FrameOwner)
		if f == nil {
			continue
		}
		out = append(out, entityGeometry{Chunk: ch, Frame: f})
	}
	return out
}

// resolveAgainstEntityBrushes pushes a body out of every entity's solid it is
// not itself standing inside.
//
// pos is in the body's own frame (bodyFrame) and comes back in it. radius and
// halfHeight describe the body the same way pushOutOfChunkBrushes wants them.
// hitFloor/hitCeiling accumulate the same way the chunk resolve's do, so
// standing on a ship's roof lands and stops a fall exactly as standing on a
// level's does.
//
// The body's OWN frame is skipped: a body aboard a ship resolves against that
// ship's brushes through the ordinary chunk path, in the ship's coordinates,
// and doing it a second time here would push it twice. Its own geometry is
// skipped for the same reason a body is not solid to itself.
func resolveAgainstEntityBrushes(
	lvl *world.Level, body *world.Entity, bodyFrame *world.Frame,
	pos mgl32.Vec3, radius, halfHeight float32,
) (out mgl32.Vec3, hitFloor, hitCeiling bool) {
	out = pos
	if lvl == nil || bodyFrame == nil || radius <= 0 || halfHeight <= 0 {
		return
	}
	geos := entityGeometries(lvl)
	if len(geos) == 0 {
		return
	}

	bodyID := ""
	bodyFrameID := ""
	if body != nil {
		bodyID, bodyFrameID = body.ID, body.Frame
	}

	for _, g := range geos {
		owner := g.Chunk.FrameOwner
		if owner == bodyID || owner == bodyFrameID {
			continue // its own solid, or the one it is standing inside
		}
		if !FramesAligned(bodyFrame, g.Frame) {
			continue // see the file comment: the cylinder would be on its side
		}

		// Body frame → world → the entity's frame → that chunk's own local
		// space, which is where the brush verts are and where the push runs.
		local := chunkLocal(g.Chunk, g.Frame.FromWorld(bodyFrame.ToWorld(out)))
		resolved, f, c := pushOutOfChunkBrushes(g.Chunk, local, radius, halfHeight)
		if resolved == local {
			continue // no contact; skip the round trip back
		}
		out = bodyFrame.FromWorld(g.Frame.ToWorld(chunkWorld(g.Chunk, resolved)))
		hitFloor = hitFloor || f
		hitCeiling = hitCeiling || c
	}
	return
}

// ---------- frame entry, without an authored doorway ----------

// aboardEntityGeometry reports whether a point is somewhere an entity's solid
// is CARRYING it: inside the bounds of that solid, and with some part of it
// directly underfoot.
//
// p is in the entity frame's own coordinates, which is the space ChunkContains
// reads and the space down is -Y in.
//
// The bounds alone used to be the whole test, and they are wrong for any
// entity whose solid does not fill its own box. A ship with wings has a chunk
// AABB spanning the wingspan, most of which is the open air a body walks
// through on its way to the ramp — and being handed aboard out there means the
// world's own chunks stop being consulted at all (Frame.ChunkAt), so the
// ground under the body's feet ceases to exist. Walking up to a winged ship
// dropped the player through the level.
//
// Underfoot is what distinguishes the two. Being aboard means the entity is
// what holds you up: its deck, its ramp, its hold's floor — the exact set of
// places where its motion should carry you and where its brushes, not the
// world's, are the ones you resolve against. Beside it, or under an outstretched
// wing, the ground is still the world's and so is the body standing on it.
//
// The corollary is that an entity with no floor under a body does not take it
// aboard — a fence, a hoop, a canopy on legs. That is the right answer for the
// same reason: nothing about it is carrying anybody, and a body inside it that
// stopped colliding with the ground would be the bug above with a different
// shape.
func aboardEntityGeometry(ch *world.Chunk, p mgl32.Vec3) bool {
	return world.ChunkContains(ch, p) && entityGeometryUnderfoot(ch, p)
}

// entityGeometryUnderfoot reports whether a straight-down ray from p meets any
// face of any solid brush in the chunk.
//
// p arrives in the chunk owner's space and the walk runs in the chunk's own
// local space, where the brush verts are — which is why down is carried in too
// rather than assumed: a chunk placed with a rotation of its own inside its
// frame has its own idea of which way -Y points.
//
// Every face is tested rather than only downward-facing ones. A body embedded
// in a brush — mid-step-up, or spawned in the floor — is above that brush's
// underside and would read as unsupported if the ray were fussy about which
// side of a face it arrived at; rayTriangle is deliberately double-sided for
// the same reason.
func entityGeometryUnderfoot(ch *world.Chunk, p mgl32.Vec3) bool {
	if ch == nil {
		return false
	}
	origin := chunkLocal(ch, p)
	down := ch.PlacementToLocalDirection(mgl32.Vec3{0, -1, 0})
	for bi := range ch.Brushes {
		b := &ch.Brushes[bi]
		if !b.Solid || len(b.Verts) < 4 {
			continue
		}
		// Where the brush IS, not where it was authored: a hold's floor that a
		// script has raised is what a body standing on it is standing on.
		m := b.Placement()
		at := func(i int) mgl32.Vec3 {
			if i < 0 || i >= len(b.Verts) {
				return mgl32.Vec3{}
			}
			v := b.Verts[i]
			if m == mgl32.Ident4() {
				return v
			}
			r := m.Mul4x1(v.Vec4(1))
			return mgl32.Vec3{r.X(), r.Y(), r.Z()}
		}
		for fi := range b.Faces {
			idx := b.Faces[fi].Indices
			for i := 1; i+1 < len(idx); i++ {
				if _, ok := rayTriangle(origin, down, at(idx[0]), at(idx[i]), at(idx[i+1])); ok {
					return true
				}
			}
		}
	}
	return false
}

// steppedIntoEntityGeometry hands a body that has walked onto an entity's own
// solid aboard that entity's frame.
//
// This is the implicit counterpart to the authored doorway volume
// (frame_portal.go): a ship with a ramp names where its ramp is and what it
// opens onto, and an ordinary entity built out of brushes — a hut, a container,
// a lift cage — should not have to. The admitting region is aboardEntityGeometry
// above: inside the geometry chunk's bounds, standing on the geometry.
//
// It is exactly antisymmetric with stepping off, because steppedOffFrame asks
// the same question and hands a body back out the moment the answer changes —
// see steppedOffEntityGeometry. Inside and not-inside cannot both be true, so
// there is no hysteresis rule to invent and no once-a-tick ping-pong to
// arbitrate.
//
// Padding that region — admitting a little before the boundary, the way a
// proximity test would — is exactly what must NOT be done: the pad would be
// ground both tests claim, and a body standing in it would be traded back and
// forth every tick. What makes the unpadded version safe is that a body only
// ever reaches an entity's floors through an actual opening in it, because
// resolveAgainstEntityBrushes keeps an outside body clear of the hull.
func steppedIntoEntityGeometry(lvl *world.Level, e *world.Entity) *FramePortalCrossing {
	if lvl == nil || e == nil {
		return nil
	}
	from := lvl.Frames.FrameFor(e)
	if from == nil {
		return nil
	}
	wp := from.ToWorld(e.Position)

	for _, g := range entityGeometries(lvl) {
		owner := g.Chunk.FrameOwner
		if owner == e.Frame || owner == e.ID {
			continue // already aboard it, or it is the body's own solid
		}
		if !aboardEntityGeometry(g.Chunk, g.Frame.FromWorld(wp)) {
			continue
		}
		to := lvl.Frames.Frame(owner)
		if to == nil {
			continue
		}
		if !FramesAligned(from, to) {
			// Rolled over: not a way in. Nothing is sealed in the
			// ResolveSealedPortals sense — the body is outside the entity, and
			// its own bounding volume is what stops it.
			continue
		}
		if wouldCycle(lvl, e.ID, to.OwnerID) {
			continue // a ship cannot be carried into its own hangar
		}
		return &FramePortalCrossing{
			FromID:   e.Frame,
			ToID:     to.OwnerID,
			Relative: to.WorldMatrix().Inv().Mul4(from.WorldMatrix()),
		}
	}
	return nil
}

// steppedOffEntityGeometry reports whether a body aboard an entity's solid has
// left it while still inside its bounds.
//
// The general way off a frame is walking off the end of its chunks
// (steppedOffFrame), and for an entity whose solid fills its own box that is
// the same event as this one. For one that does not, it is not: walking down a
// winged ship's ramp onto the ground puts a body in the open air under a wing,
// inside the geometry chunk's bounds with nothing of the ship underfoot, and a
// body left aboard there is a body for whom the world's ground does not exist.
// The way in and the way out have to be the same boundary or the ship is a
// trap on one side of it.
//
// Only the entity's OWN geometry chunk is stepped off this way. A frame that
// also carries authored interior chunks — a vehicle naming them under
// `interiors` — keeps the general rule for those: they are rooms in their own
// right and being in one is being aboard however they are floored.
func steppedOffEntityGeometry(lvl *world.Level, e *world.Entity) bool {
	if lvl == nil || e == nil || e.Frame == "" || lvl.Frames == nil {
		return false
	}
	f := lvl.Frames.FrameFor(e)
	if f == nil {
		return false
	}
	ch := f.ChunkAt(lvl, e.Position)
	if ch == nil || ch.FromEntity != e.Frame || !ch.InFrame() {
		return false
	}
	return !aboardEntityGeometry(ch, e.Position)
}

// entityHasImplicitDoorway reports whether frameID belongs to an entity whose
// solid admits by containment — which is every entity that carries brushes.
//
// steppedOffFrame consults it so a body can get back OUT of one: a frame with
// no way out at all keeps its contents however far they wander off its floors,
// and an implicit doorway has to be as good a way out as an authored one or
// walking into a hut would be a trap.
func entityHasImplicitDoorway(lvl *world.Level, frameID string) bool {
	if lvl == nil || frameID == "" {
		return false
	}
	for i := range lvl.Chunks {
		if lvl.Chunks[i].FromEntity == frameID && lvl.Chunks[i].InFrame() {
			return true
		}
	}
	return false
}
