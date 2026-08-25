package collision

// entity_collision.go — making placed entities solid, without a line of script.
//
// Two independent things an entity can opt into, plus the gravity that makes
// the first of them worth having:
//
//	world     resolve against walls, floors, ceilings and slopes — the same
//	          resolvers the player has always used (collision.go)
//	entities  resolve against the other entities that also opted in, and
//	          against the player
//	gravity   fall, and stop when something is underneath
//
// They are per-entity because solidity is a property of the *type*: a barrel is
// solid, a floating quest marker is not, and neither should have to be told so
// by a script every time it is placed. Each also has an engine-wide master
// switch, so the whole system can be turned off to see what it was doing.
//
// Everything is off by default. Entities placed before this existed pass
// through the world, and an engine update must not silently start shoving a
// level's props around.
//
// What this is not: a physics engine. There is no restitution, no friction, no
// angular response and no solver iteration — an overlap is pushed apart along
// the axis it is shallowest on, once per frame. That is enough to stand a crate
// on a floor and stop it inside a wall, which is what "don't make me script it"
// asks for, and it is deliberately cheap.

import (
	"foundry/internal/hooks"
	"foundry/internal/world"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// ---------- gravity ----------

// ---------- world ----------

// resolveEntityWorld pushes the entity out of level geometry and reports
// whether it ended up resting on a floor.
//
// The resolvers work on a *proxy* entity placed at the volume's centre with the
// volume's scaled extents, because that is the shape they are written against —
// an entity's own Position is its base, and its bounds can be offset from it
// besides. Whatever the resolvers do to the proxy is applied back as a delta,
// so the offset and the scale never have to be un-done.
func resolveEntityWorld(lvl *world.Level, e *world.Entity, oldPos mgl32.Vec3) bool {
	if lvl == nil || !e.HasBounds() {
		return false
	}
	h := e.BoundHalfExtents()
	center := e.BoundCenter()
	proxy := world.Entity{
		Position:   center,
		BoundHalfX: h.X(),
		BoundHalfY: h.Y(),
		BoundHalfZ: h.Z(),
	}
	// Where that centre travelled from, which the slope resolver needs to tell
	// uphill movement from downhill.
	oldCenter := center.Sub(e.Position.Sub(oldPos))

	// Everything below is in the entity's own frame. Its position, its bounds
	// and the chunks it resolves against are all expressed there, which is why
	// the resolvers need no idea that frames exist.
	frame := lvl.Frames.FrameFor(e)

	// Walk the frame's travel rather than only resolving where it ended. An
	// entity that moved farther than its own width in one frame would otherwise
	// be looked up somewhere past everything it should have hit — and past every
	// sector, where there is nothing to resolve against at all. Anything moving
	// at ordinary speed takes exactly one step and this is the same single
	// resolve it always was.
	travel := center.Sub(oldCenter)
	steps, travel := SweepPlan(h, travel)
	stepTravel := travel.Mul(1 / float32(steps))

	pos := oldCenter
	var hitFloor, hitCeiling bool
	for i := 0; i < steps; i++ {
		stepFrom := pos
		pos = pos.Add(stepTravel)

		chunk, sector := frame.ChunkAndSectorAt(lvl, pos)
		if sector == nil {
			// Outside every sector there is nothing to resolve against. The
			// entity is left where it is rather than being pulled to the
			// nearest floor: an entity over open space is a legitimate thing
			// to be.
			continue
		}

		pos = ResolveWallCollision(&proxy, chunk, sector, pos)
		pos = ResolveSlopeCollision(&proxy, chunk, sector, stepFrom, pos)
		// Sliding along a slope can carry the entity into a different sector,
		// so the floor it lands on has to be looked up again from where it
		// ended.
		chunk, sector = frame.ChunkAndSectorAt(lvl, pos)
		f, c := resolveEntityFloorCeiling(&proxy, chunk, sector, &pos)
		hitFloor = hitFloor || f
		hitCeiling = hitCeiling || c
	}

	e.Position = e.Position.Add(pos.Sub(center))

	// Landing and head-bonk both kill vertical velocity, or the entity would go
	// on accelerating into the surface and jitter against it every frame.
	if hitFloor && e.Velocity.Y() < 0 {
		e.Velocity[1] = 0
	}
	if hitCeiling && e.Velocity.Y() > 0 {
		e.Velocity[1] = 0
	}
	return hitFloor
}

// resolveEntityFloorCeiling clamps the proxy's Y between the floor under it and
// the ceiling over it, symmetrically about its centre.
//
// Unlike the player's ResolveFloorCollision, both halves agree on what the
// position means — see that function's note on why its own two halves do not.
func resolveEntityFloorCeiling(proxy *world.Entity, chunk *world.Chunk, sector *world.Sector, pos *mgl32.Vec3) (hitFloor, hitCeiling bool) {
	if chunk == nil || sector == nil {
		return false, false
	}
	xz := mgl32.Vec2{pos[0], pos[2]}

	minY := sectorFloorHeightAt(chunk, sector, xz) + proxy.BoundHalfY
	if pos[1] <= minY+world.EntityGroundEpsilon {
		pos[1] = minY
		hitFloor = true
	}
	// Applied after the floor, so in a gap thinner than the entity the ceiling
	// wins. That is the recoverable way round: something pressed down into a
	// floor is pushed back out next frame, whereas something pressed up through
	// a ceiling is gone.
	if ceilH, ok := sectorCeilingHeightAt(chunk, sector, xz); ok {
		maxY := ceilH - proxy.BoundHalfY
		if pos[1] >= maxY {
			pos[1] = maxY
			hitCeiling = true
		}
	}
	return hitFloor, hitCeiling
}

// ---------- entity against entity ----------

// ResolveEntityCollisions pushes apart every overlapping pair of entities that
// opted in, and the player against the same set. It returns the indices of the
// level entities it actually moved, so the caller can settle them back against
// the world afterwards.
//
// The pass runs after every entity has integrated, not as each one moves, so
// the result cannot depend on the order the level happens to list them in.
//
// Pairs are visited bottom-up, and that is load-bearing rather than tidiness.
// There is one solver iteration, so a correction made to an entity is not
// re-propagated to pairs already visited: in a stack of three resolved
// top-down, the top box is separated from the middle one *before* the middle
// one is lifted off the bottom, and ends the frame that lift short. Gravity
// re-opens the same gap every frame, so it never closes — the stack visibly
// sags. Working upward means each entity is already in its final position by
// the time the one above it is resolved against it, and one pass is enough.
//
// It is O(n²) over *participating* entities — the ones with the flag set, not
// the whole level. A level of a few hundred solid entities is nothing; if that
// ever stops being true, the spatial hash the chunks already use is the answer.
func ResolveEntityCollisions(lvl *world.Level, player *world.Entity) []int {
	if lvl == nil || !world.EntityEntityCollisionEnabled {
		return nil
	}
	idx := make([]int, 0, len(lvl.Entities))
	for i := range lvl.Entities {
		if lvl.Entities[i].CollidesWithEntities() {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return nil
	}
	// Lowest volume centre first; ties broken by index so the walk is stable
	// and two runs over the same level always agree.
	sort.SliceStable(idx, func(a, b int) bool {
		return lvl.Entities[idx[a]].BoundCenter().Y() < lvl.Entities[idx[b]].BoundCenter().Y()
	})

	moved := map[int]bool{}
	for a := 0; a < len(idx); a++ {
		for b := a + 1; b < len(idx); b++ {
			ea, eb := &lvl.Entities[idx[a]], &lvl.Entities[idx[b]]
			if ea.Frame != eb.Frame {
				// Two entities in different frames describe their volumes in
				// different coordinate systems, and comparing those boxes
				// directly would be subtracting numbers that do not mean the
				// same thing.
				//
				// Skipping rather than converting is also what the common case
				// wants: the one pair that is always cross-frame is a rider
				// against the vehicle carrying it, and a rider is *inside* that
				// hull by definition — resolving the overlap would eject it
				// through the wall. Two solid things in genuinely different
				// frames not colliding is a real gap, and the place to close it
				// is where a frame portal already has to reconcile two spaces.
				continue
			}
			movedA, movedB := resolveEntityOverlap(ea, eb)
			if movedA || movedB {
				// The pair, not just the fact that something moved: the
				// scripting engine's `collide` event needs to know *what* was
				// hit, and this is the only place that knows. Recorded rather
				// than returned so the resolver's signature stays about
				// physics — see collisionPairs.
				hooks.RecordCollisionPair(ea.ID, eb.ID)
			}
			if movedA {
				moved[idx[a]] = true
			}
			if movedB {
				moved[idx[b]] = true
			}
		}
	}

	// The player takes part on the same terms as anything else, so a solid
	// crate is solid to walk into and — if it is movable — to shove.
	if player != nil && player.HasBounds() {
		for _, i := range idx {
			if player.Frame != lvl.Entities[i].Frame {
				continue // different coordinate systems — see the note above
			}
			movedP, movedE := resolveEntityOverlap(player, &lvl.Entities[i])
			if movedP || movedE {
				hooks.RecordCollisionPair(player.ID, lvl.Entities[i].ID)
			}
			if movedE {
				moved[i] = true
			}
		}
	}

	out := make([]int, 0, len(moved))
	for i := range lvl.Entities {
		if moved[i] {
			out = append(out, i)
		}
	}
	return out
}

// resolveEntityOverlap separates two overlapping entities and reports which of
// them it moved.
//
// The push is the minimum translation vector: the shortest displacement that
// takes the two apart. For two axis-aligned boxes that is the smallest of the
// three axis overlaps, which is what makes something dropped on top of
// something else settle on it rather than squirting out sideways — and that
// case keeps its own arithmetic below, both because it is by far the most
// common and because every stack in every existing level is already tuned
// against it. Anything rotated or round goes through GJK/EPA instead, where the
// MTV is the same idea without the assumption that it lies along an axis.
func resolveEntityOverlap(a, b *world.Entity) (movedA, movedB bool) {
	// A pair with nothing that can move has nothing to resolve, and checking
	// first skips the box maths for every static-against-static pair.
	if !a.Collision.Movable && !b.Collision.Movable {
		return false, false
	}
	amin, amax, ok := a.CollisionAABB()
	if !ok {
		return false, false
	}
	bmin, bmax, ok := b.CollisionAABB()
	if !ok {
		return false, false
	}

	// Boxes first, always. Every shape here is inside its CollisionAABB, so an
	// axis that separates the boxes separates the shapes — and this rejects the
	// overwhelming majority of pairs without building a shape at all, which
	// matters in a routine that runs over every participating pair every frame.
	overlap, touching := boxOverlaps(amin, amax, bmin, bmax)
	if !touching {
		return false, false
	}

	sep, ok := separationFor(a, b, overlap)
	if !ok {
		return false, false
	}

	// What each is actually free to give way to. Movability is the authored
	// half; the other half is that nothing standing on solid ground can be
	// pushed down through it, which is what lets a stack settle exactly instead
	// of sinking a little further every frame — see canDisplace.
	aPush := allowedPush(a, sep)
	bPush := allowedPush(b, sep.Mul(-1))
	aFree := aPush != (mgl32.Vec3{})
	bFree := bPush != (mgl32.Vec3{})

	switch {
	case aFree && bFree:
		// Neither has a mass, so neither has a claim to stay put: split it.
		entityPushOut(a, aPush.Mul(0.5))
		entityPushOut(b, bPush.Mul(0.5))
		return true, true
	case aFree:
		entityPushOut(a, aPush)
		return true, false
	case bFree:
		entityPushOut(b, bPush)
		return false, true
	default:
		return false, false
	}
}

// boxOverlaps reports how deeply two boxes interpenetrate on each axis, and
// whether they interpenetrate at all. A non-positive overlap on any axis is a
// separating axis, and one is enough.
func boxOverlaps(amin, amax, bmin, bmax mgl32.Vec3) (overlap mgl32.Vec3, touching bool) {
	for k := 0; k < 3; k++ {
		lo, hi := amin[k], bmin[k]
		if hi > lo {
			lo = hi
		}
		hiA, hiB := amax[k], bmax[k]
		if hiB < hiA {
			hiA = hiB
		}
		overlap[k] = hiA - lo
		if overlap[k] <= 0 {
			return overlap, false
		}
	}
	return overlap, true
}

// separationFor returns the displacement a must make to come clear of b.
//
// The axis-aligned box case is answered from the overlap the broadphase already
// computed, so the common path costs nothing beyond what it always did. Only a
// pair that is actually rotated or round is worth the general solver.
func separationFor(a, b *world.Entity, overlap mgl32.Vec3) (mgl32.Vec3, bool) {
	if isAxisAlignedBox(a) && isAxisAlignedBox(b) {
		axis := 0
		for k := 1; k < 3; k++ {
			if overlap[k] < overlap[axis] {
				axis = k
			}
		}
		// Push a away from b along that axis. Centres rather than positions,
		// since a bound offset can put the two on opposite sides of their own
		// origins.
		dir := float32(1)
		if a.BoundCenter()[axis] < b.BoundCenter()[axis] {
			dir = -1
		}
		var sep mgl32.Vec3
		sep[axis] = dir * overlap[axis]
		return sep, true
	}

	sa, oka := ShapeFor(a)
	sb, okb := ShapeFor(b)
	if !oka || !okb {
		return mgl32.Vec3{}, false
	}
	// Penetration's normal points from a toward b — the way b must go — so a
	// goes the other way.
	n, depth, ok := Penetration(sa, sb)
	if !ok || depth <= 0 {
		return mgl32.Vec3{}, false
	}
	return n.Mul(-depth), true
}

// isAxisAlignedBox reports whether an entity's volume is a plain box sitting
// square to the world, which is the case the fast path is written for.
func isAxisAlignedBox(e *world.Entity) bool {
	return e.BoundingKind == world.BoundingAABB && e.Rotation == (mgl32.Vec3{})
}

// allowedPush trims a separation to the part the entity can actually take:
// nothing at all if it is immovable, and nothing downward if it is resting on
// something. Trimming per axis rather than refusing outright is what lets a
// grounded entity still slide out of the way sideways when the shortest way out
// of an overlap happens to point down and across.
func allowedPush(e *world.Entity, push mgl32.Vec3) mgl32.Vec3 {
	if !e.Collision.Movable {
		return mgl32.Vec3{}
	}
	for axis := 0; axis < 3; axis++ {
		if push[axis] == 0 {
			continue
		}
		sign := float32(1)
		if push[axis] < 0 {
			sign = -1
		}
		if !canDisplace(e, axis, sign) {
			push[axis] = 0
		}
	}
	return push
}

// canDisplace reports whether a push of the given sign along one axis is
// something the entity can actually give way to.
//
// The only thing it refuses is being driven downward while it is resting on
// something. Splitting an overlap evenly is right for two entities floating
// into each other, but wrong the moment one of them is on the ground: half the
// separation would go into pressing it into the floor, the world resolve would
// push it straight back out, and the pair would never quite come apart —
// a stack of boxes would visibly sag and jitter. Refusing hands the whole
// separation to the one that can take it.
func canDisplace(e *world.Entity, axis int, sign float32) bool {
	return !(axis == 1 && sign < 0 && e.Collision.OnGround)
}

// entityPushOut displaces an entity and kills the velocity it was closing with,
// so it stops pressing into what it hit instead of building up speed against it
// forever.
func entityPushOut(e *world.Entity, delta mgl32.Vec3) {
	e.Position = e.Position.Add(delta)
	for axis := 0; axis < 3; axis++ {
		// Only the component moving *into* the other body is cancelled. Zeroing
		// the whole axis would also stop something already separating, which is
		// what makes a stack of boxes stick together instead of settling.
		if delta[axis] > 0 && e.Velocity[axis] < 0 {
			e.Velocity[axis] = 0
		}
		if delta[axis] < 0 && e.Velocity[axis] > 0 {
			e.Velocity[axis] = 0
		}
	}
	// Being pushed up off something counts as standing on it, which is what
	// lets a third box stack on a second. "Up" has to mean predominantly up
	// rather than merely not-down: a mostly-sideways separation with a sliver
	// of lift in it is being squeezed out of a wall, not landing on a floor.
	// Staged rather than applied, so the pair pass cannot see its own work —
	// see OnGround.
	if delta[1] > 0 && delta[1] >= abs32(delta[0]) && delta[1] >= abs32(delta[2]) {
		e.Collision.GroundedNext = true
	}
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
