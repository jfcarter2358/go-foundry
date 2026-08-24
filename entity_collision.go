package main

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
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// Engine-wide master switches. Per-entity flags say what *wants* to collide;
// these say whether the engine is listening. Both default on, so turning a
// flag on in a definition is all it takes.
var (
	EntityWorldCollisionEnabled  = true
	EntityEntityCollisionEnabled = true
)

// Entity gravity, in world units per second and per second squared.
//
// These are the player's own GravityAccel/MaxFallSpeed (input.go) converted out
// of its per-frame-at-60fps units, so a dropped crate falls at the rate a
// player does. The player's cannot simply be reused: its velocity is measured
// per frame, an entity's per second.
const (
	EntityGravityAccel  = float32(36.0)
	EntityMaxFallSpeed  = float32(30.0)
	entityGroundEpsilon = float32(0.0001)
)

// collidesWithWorld / collidesWithEntities fold the master switch into the
// per-entity flag, so no caller has to remember to check both.
func (e *Entity) collidesWithWorld() bool {
	return EntityWorldCollisionEnabled && e.Collision.World
}

func (e *Entity) collidesWithEntities() bool {
	return EntityEntityCollisionEnabled && e.Collision.Entities && e.HasBounds()
}

// hasGravity reports whether the entity should fall. An immovable entity never
// does, whatever its flag says — nothing can displace it, gravity included.
func (e *Entity) hasGravity() bool {
	return e.Collision.Gravity && e.Collision.Movable
}

// ---------- gravity ----------

// applyGravity accelerates the entity downward, capped at terminal velocity so
// a long fall cannot tunnel through a floor in one step.
func (e *Entity) applyGravity(dt float32) {
	if !e.hasGravity() {
		return
	}
	v := e.Velocity.Y() - EntityGravityAccel*dt
	if v < -EntityMaxFallSpeed {
		v = -EntityMaxFallSpeed
	}
	e.Velocity[1] = v
}

// ---------- world ----------

// resolveEntityWorld pushes the entity out of level geometry and reports
// whether it ended up resting on a floor.
//
// The resolvers work on a *proxy* entity placed at the volume's centre with the
// volume's scaled extents, because that is the shape they are written against —
// an entity's own Position is its base, and its bounds can be offset from it
// besides. Whatever the resolvers do to the proxy is applied back as a delta,
// so the offset and the scale never have to be un-done.
func resolveEntityWorld(lvl *Level, e *Entity, oldPos mgl32.Vec3) bool {
	if lvl == nil || !e.HasBounds() {
		return false
	}
	h := e.BoundHalfExtents()
	center := e.BoundCenter()
	proxy := Entity{
		Position:   center,
		BoundHalfX: h.X(),
		BoundHalfY: h.Y(),
		BoundHalfZ: h.Z(),
	}
	// Where that centre travelled from, which the slope resolver needs to tell
	// uphill movement from downhill.
	oldCenter := center.Sub(e.Position.Sub(oldPos))

	chunk, sector := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, center)
	if sector == nil {
		// Outside every sector there is nothing to resolve against. The entity
		// is left where it is rather than being pulled to the nearest floor:
		// an entity over open space is a legitimate thing to be.
		return false
	}

	pos := ResolveWallCollision(&proxy, chunk, sector, center)
	pos = ResolveSlopeCollision(&proxy, chunk, sector, oldCenter, pos)
	// Sliding along a slope can carry the entity into a different sector, so
	// the floor it lands on has to be looked up again from where it ended.
	chunk, sector = FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, pos)
	hitFloor, hitCeiling := resolveEntityFloorCeiling(&proxy, chunk, sector, &pos)

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
func resolveEntityFloorCeiling(proxy *Entity, chunk *Chunk, sector *Sector, pos *mgl32.Vec3) (hitFloor, hitCeiling bool) {
	if chunk == nil || sector == nil {
		return false, false
	}
	xz := mgl32.Vec2{pos[0], pos[2]}

	minY := sectorFloorHeightAt(chunk, sector, xz) + proxy.BoundHalfY
	if pos[1] <= minY+entityGroundEpsilon {
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
func ResolveEntityCollisions(lvl *Level, player *Entity) []int {
	if lvl == nil || !EntityEntityCollisionEnabled {
		return nil
	}
	idx := make([]int, 0, len(lvl.Entities))
	for i := range lvl.Entities {
		if lvl.Entities[i].collidesWithEntities() {
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
			movedA, movedB := resolveEntityOverlap(ea, eb)
			if movedA || movedB {
				// The pair, not just the fact that something moved: the
				// scripting engine's `collide` event needs to know *what* was
				// hit, and this is the only place that knows. Recorded rather
				// than returned so the resolver's signature stays about
				// physics — see collisionPairs.
				recordCollisionPair(ea.ID, eb.ID)
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
			movedP, movedE := resolveEntityOverlap(player, &lvl.Entities[i])
			if movedP || movedE {
				recordCollisionPair(player.ID, lvl.Entities[i].ID)
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
// The push is the minimum translation along whichever axis the boxes overlap
// least, which is what makes something dropped on top of something else settle
// on it rather than squirting out sideways. The volumes are compared as boxes
// whatever their BoundingKind: the kinds refine ContainsPoint, which is the
// query scripts ask, not the push-apart.
func resolveEntityOverlap(a, b *Entity) (movedA, movedB bool) {
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

	var overlap mgl32.Vec3
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
			return false, false // separated on this axis, so not touching
		}
	}

	axis := 0
	for k := 1; k < 3; k++ {
		if overlap[k] < overlap[axis] {
			axis = k
		}
	}

	// Push a away from b along that axis. Centres rather than positions, since
	// a bound offset can put the two on opposite sides of their own origins.
	dir := float32(1)
	if a.BoundCenter()[axis] < b.BoundCenter()[axis] {
		dir = -1
	}

	// Which of the two this push is actually free to move. Movability is the
	// authored half; the other half is that nothing standing on solid ground
	// can be pushed down through it, which is what lets a stack settle exactly
	// instead of sinking a little further every frame — see canDisplace.
	aFree := a.Collision.Movable && canDisplace(a, axis, dir)
	bFree := b.Collision.Movable && canDisplace(b, axis, -dir)

	switch {
	case aFree && bFree:
		// Neither has a mass, so neither has a claim to stay put: split it.
		half := overlap[axis] / 2
		entityPushOut(a, axis, dir*half)
		entityPushOut(b, axis, -dir*half)
		return true, true
	case aFree:
		entityPushOut(a, axis, dir*overlap[axis])
		return true, false
	case bFree:
		entityPushOut(b, axis, -dir*overlap[axis])
		return false, true
	default:
		return false, false
	}
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
func canDisplace(e *Entity, axis int, sign float32) bool {
	return !(axis == 1 && sign < 0 && e.Collision.OnGround)
}

// entityPushOut displaces an entity along one axis and kills the velocity it
// was closing with, so it stops pressing into what it hit instead of building
// up speed against it forever.
func entityPushOut(e *Entity, axis int, delta float32) {
	e.Position[axis] += delta
	// Only the component moving *into* the other body is cancelled. Zeroing the
	// whole axis would also stop something already separating, which is what
	// makes a stack of boxes stick together instead of settling.
	if delta > 0 && e.Velocity[axis] < 0 {
		e.Velocity[axis] = 0
	}
	if delta < 0 && e.Velocity[axis] > 0 {
		e.Velocity[axis] = 0
	}
	// Being pushed up off something counts as standing on it, which is what
	// lets a third box stack on a second. Staged rather than applied, so the
	// pair pass cannot see its own work — see OnGround.
	if axis == 1 && delta > 0 {
		e.Collision.groundedNext = true
	}
}

// commitGrounded promotes this frame's accumulated answer to the readable flag.
// Called once per entity at the end of EntityUpdate, after every push and every
// settle has had its say.
func (e *Entity) commitGrounded() {
	e.Collision.OnGround = e.Collision.groundedNext
	e.Collision.groundedNext = false
}
