package collision

// particle_collide.go — the world-collision seam particle.Collide is bound to.
//
// A particle is not an entity: it has no bounding volume worth resolving
// against walls, it does not push anything, and there can be tens of thousands
// of them. So this is deliberately NOT the entity resolver with a smaller box.
// It is one question — did this step cross the floor or the ceiling — answered
// from the same height queries the entity clamp uses, so a spark lands on
// exactly the surface a crate would rest on.
//
// Walls are not tested. A particle that leaves the sector it was in simply
// stops being asked about, which is cheap and, for the effects collision is for
// (sparks skipping off a floor, dust settling), indistinguishable from the
// right answer. Doing it properly would mean a swept test against every wall of
// every sector the step crosses, per particle, per frame — which is the cost
// that made this a separate function in the first place. If an effect ever
// needs walls, that is the place to add them, and the seam does not change.

import (
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// ParticleCollide sweeps a particle of radius r from `from` to `to` against the
// level, returning where it first touched and the surface normal there.
//
// Installed into particle.Collide by internal/app; see the note on that
// variable for why it is a seam rather than an import.
func ParticleCollide(lvl *world.Level, from, to mgl32.Vec3, r float32) (pos, normal mgl32.Vec3, hit bool) {
	if lvl == nil {
		return to, mgl32.Vec3{}, false
	}
	// Which sector the step is in is asked once, at the start of the step, not
	// per sample: a particle moving fast enough to cross a sector boundary in
	// one frame is moving fast enough that it does not matter which of the two
	// floors it lands on.
	ch, sec := lvl.Frames.World().ChunkAndSectorAt(lvl, from)
	if ch == nil || sec == nil {
		return to, mgl32.Vec3{}, false
	}

	xz := mgl32.Vec2{to.X(), to.Z()}

	if to.Y() <= from.Y() {
		// Descending: the floor is what it can meet.
		if h, ok := sectorFloorHeightAt(ch, sec, xz); ok && to.Y()-r <= h {
			return mgl32.Vec3{to.X(), h + r, to.Z()}, floorNormalAt(ch, sec, xz), true
		}
		return to, mgl32.Vec3{}, false
	}

	// Ascending: the ceiling, if the sector has one.
	if h, ok := sectorCeilingHeightAt(ch, sec, xz); ok && to.Y()+r >= h {
		return mgl32.Vec3{to.X(), h - r, to.Z()}, mgl32.Vec3{0, -1, 0}, true
	}
	return to, mgl32.Vec3{}, false
}

// floorNormalAt is the floor's surface normal under xz, so a particle bouncing
// on a slope leaves along the slope rather than straight up.
//
// Sampled by finite difference rather than read from the mesh: the height query
// above is the authority on where the floor is (it already resolves openings,
// adjacent sectors and the highest-wins rule), and taking the normal from
// anywhere else would let a particle bounce off a surface at a height nothing
// put it at.
func floorNormalAt(ch *world.Chunk, sec *world.Sector, xz mgl32.Vec2) mgl32.Vec3 {
	const eps = 0.05
	h, ok := sectorFloorHeightAt(ch, sec, xz)
	if !ok {
		return mgl32.Vec3{0, 1, 0}
	}
	hx, okx := sectorFloorHeightAt(ch, sec, mgl32.Vec2{xz.X() + eps, xz.Y()})
	hz, okz := sectorFloorHeightAt(ch, sec, mgl32.Vec2{xz.X(), xz.Y() + eps})
	if !okx || !okz {
		return mgl32.Vec3{0, 1, 0}
	}
	// The cross product of the two tangents, which for a height field reduces
	// to this.
	n := mgl32.Vec3{-(hx - h) / eps, 1, -(hz - h) / eps}
	if n.LenSqr() < 1e-8 {
		return mgl32.Vec3{0, 1, 0}
	}
	return n.Normalize()
}
