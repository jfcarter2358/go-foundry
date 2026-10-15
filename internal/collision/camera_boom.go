package collision

// camera_boom.go — how far a third-person camera's boom may reach before the
// level's shape is in the way.
//
// A thick ray rather than a sphere sweep: the centre line and four more
// around it at the camera's radius, each a plain Raycast against geometry and
// solid props. The nearest hit along any of them, less the radius, is where the
// camera may go. The side rays are what keep the camera off a wall it is
// sliding along: in a slot canyon three metres wide the boom often runs nearly
// parallel to the rock, which the centre ray never meets, and the near plane
// would cut into it.
//
// Small props — a rock at the player's feet, a bush, a pot — do not pull the
// camera in. A camera that lurched forward every time a pebble passed behind
// the player is worse than one that sees through the pebble for a moment; the
// ray is cast again past anything whose box is under BoomIgnoreSize across.
// Entities are not tested at all (a squadmate walking behind the player
// should not shove the camera into the back of its head), and the player is
// never in the level's entity list, so nothing here has to ignore it.

import (
	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/world"
)

// BoomIgnoreSize is the largest extent, in world units, a solid prop may have
// and still be looked through by the camera boom.
const BoomIgnoreSize = float32(1.2)

// boomRetries bounds how many small props one ray may pass through.
const boomRetries = 4

// CameraBoom is how far from `from`, along dir, a camera of the given radius
// can sit without touching the level's shape, at most length. dir need not be
// normalised. The answer is never negative.
func CameraBoom(lvl *world.Level, from, dir mgl32.Vec3, length, radius float32) float32 {
	if lvl == nil || length <= 0 || dir.Len() < 1e-6 {
		return maxf(length, 0)
	}
	dir = dir.Normalize()
	// Two directions across the ray.
	ref := mgl32.Vec3{0, 1, 0}
	if d := dir.Dot(ref); d > 0.95 || d < -0.95 {
		ref = mgl32.Vec3{1, 0, 0}
	}
	u := dir.Cross(ref).Normalize()
	v := u.Cross(dir).Normalize()
	// The side rays start a little inside the radius: the pivot they start
	// round is itself only guaranteed to be `radius` clear of a wall.
	side := radius * 0.8
	offsets := [5]mgl32.Vec3{{}, u.Mul(side), u.Mul(-side), v.Mul(side), v.Mul(-side)}

	reach := length + radius
	best := reach
	for i, off := range offsets {
		if radius <= 0 && i > 0 {
			break
		}
		if t, ok := boomRay(lvl, from.Add(off), dir, reach); ok && t < best {
			best = t
		}
	}
	return clampf(best-radius, 0, length)
}

// boomRay is one of CameraBoom's rays: the distance to the first piece of the
// level's shape, or the first solid prop that is not a small one.
func boomRay(lvl *world.Level, origin, dir mgl32.Vec3, reach float32) (float32, bool) {
	best, hit := reach, false
	q := geometryQuery{lvl: lvl, origin: origin, dir: dir, tMax: reach}
	q.run()
	if q.hit {
		best, hit = q.tMax, true
	}
	var ignore map[string]bool
	for try := 0; try < boomRetries; try++ {
		id, t, _, ok := sceneryRayNearest(lvl, origin, dir, best, ignore, false)
		if !ok || t > best {
			break
		}
		if !smallScenery(lvl, id) {
			best, hit = t, true
			break
		}
		if ignore == nil {
			ignore = map[string]bool{}
		}
		ignore[id] = true
	}
	return best, hit
}

// smallScenery reports whether a placement is small enough for the boom to see
// through.
func smallScenery(lvl *world.Level, id string) bool {
	s := world.FindScenery(lvl, id)
	if s == nil {
		return false
	}
	lo, hi, ok := s.WorldAABB()
	if !ok {
		return false
	}
	d := hi.Sub(lo)
	return d.X() < BoomIgnoreSize && d.Y() < BoomIgnoreSize && d.Z() < BoomIgnoreSize
}

func clampf(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
