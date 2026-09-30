package collision

// terrain.go — bodies against a chunk's heightfield ground.
//
// A terrain is not resolved like a brush. Brushes are closed convex solids and
// GJK against each is the right question to ask of them; a heightfield is one
// continuous surface, and cutting it into per-triangle solids would hand GJK
// the internal edges between neighbouring cells — a body sliding across a
// seam finds the side face of the next cell's solid as close as its top, and
// catches on flat ground. So the ground is asked the question a heightfield
// can answer exactly: how high is it under the body's feet.
//
// The body's footprint (its centre and a ring at its radius) is sampled.
// Every sample whose ground is above the body's soles is a contact:
//
//   - on walkable ground (normal within MaxSlopeCos of up) the body is lifted
//     straight up until it clears, and that counts as floor, the same credit a
//     brush floor gives. Straight up, not along the normal, so a body stood on
//     a slope stays put rather than creeping downhill a push at a time;
//   - on steeper ground it is pushed out along the surface normal, exactly the
//     push a steep brush face gives, and not credited as ground — so a cliff
//     sculpted into the terrain is a wall, and a curb-height one is climbed by
//     StepUp like any other.

import (
	"math"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// terrainRing is how many samples ring the footprint's centre.
const terrainRing = 8

// terrainDepth is how far below the surface a body's soles may be and still
// be pushed back out, in multiples of its height. Below that it is treated as
// having fallen through (or as walking a tunnel under the ground, brushes
// through a hole), and the surface overhead is left alone.
const terrainDepth = 1

// pushOutOfTerrain resolves a cylindrical body — centre pos, radius, vertical
// half-height, all in the chunk's local space — against t.
func pushOutOfTerrain(t *world.Terrain, pos mgl32.Vec3, radius, halfHeight float32) (out mgl32.Vec3, hitFloor bool) {
	out = pos
	if t == nil {
		return
	}
	// Coarse reject on the grid's box: nothing to do for a body nowhere near
	// the ground.
	lo, hi := t.LocalBounds()
	if pos.X()+radius < lo.X() || pos.X()-radius > hi.X() ||
		pos.Z()+radius < lo.Z() || pos.Z()-radius > hi.Z() ||
		pos.Y()-halfHeight > hi.Y()+1e-3 {
		return
	}
	const separationEpsilon = float32(0.001)
	// A few rounds: a push off a steep face can move the footprint onto ground
	// that now needs lifting.
	for round := 0; round < 3; round++ {
		soles := out.Y() - halfHeight
		lift := float32(0)
		var steepPush mgl32.Vec3
		steepDepth := float32(0)
		for i := -1; i < terrainRing; i++ {
			sx, sz := out.X(), out.Z()
			if i >= 0 {
				a := float64(i) * 2 * math.Pi / terrainRing
				sx += radius * float32(math.Cos(a))
				sz += radius * float32(math.Sin(a))
			}
			h, n, ok := t.HeightAt(sx, sz)
			if !ok {
				continue
			}
			pen := h - soles
			if pen <= 0 || pen > 2*halfHeight*terrainDepth+radius {
				continue
			}
			if n.Y() >= MaxSlopeCos {
				if pen > lift {
					lift = pen
				}
				continue
			}
			// Distance from the sole point to the plane, along its normal.
			if d := pen * n.Y(); d > steepDepth {
				steepDepth = d
				steepPush = n.Mul(d + separationEpsilon)
			}
		}
		if lift == 0 && steepDepth == 0 {
			break
		}
		if lift > 0 {
			out[1] += lift + separationEpsilon
			hitFloor = true
			continue
		}
		out = out.Add(steepPush)
	}
	return
}

// terrainBelow reports whether local point p has t's ground under it: the
// terrain's counterpart of entityGeometryUnderfoot's downward ray, which
// meets any face below. A point a little under the surface still counts, for
// the reason that function's rays are double-sided — a body mid-step-up or
// spawned into the ground is standing on it.
func terrainBelow(t *world.Terrain, p mgl32.Vec3) bool {
	if t == nil {
		return false
	}
	h, _, ok := t.HeightAt(p.X(), p.Z())
	return ok && h <= p.Y()+1
}

// terrainProbe is the vertical particle probe (probeChunkSurface) against t,
// in chunk-local space: the distance along dir (straight down, or straight up
// when !descending) from origin to the surface.
func terrainProbe(t *world.Terrain, origin mgl32.Vec3, descending bool) (dist float32, n mgl32.Vec3, ok bool) {
	if t == nil {
		return 0, mgl32.Vec3{}, false
	}
	h, n, ok := t.HeightAt(origin.X(), origin.Z())
	if !ok {
		return 0, mgl32.Vec3{}, false
	}
	if descending {
		dist = origin.Y() - h
	} else {
		dist = h - origin.Y()
	}
	return dist, n, dist >= 0
}
