package collision

// terrain.go — bodies against a chunk's block terrain.
//
// A terrain is not resolved like a brush. Brushes are closed convex solids
// and GJK against each is the right question to ask of them; a terrain's
// surface is one continuous sheet (world/terrain_mesh.go), and cutting it
// into per-face solids would hand GJK the internal edges between
// neighbouring faces — a body sliding across a seam finds the side of the
// next face's solid as close as its top, and catches on flat ground. So the
// surface is asked the questions a sheet answers exactly, with short rays
// from inside the body:
//
//   - walls: rays out to the body's radius, round it at the height of its
//     shins, its middle and its head. A steep face one meets pushes the body
//     back out along the face's level normal. Below the shins is left to the
//     floor and to StepUp, so a curb is stepped onto rather than walked into;
//   - floor: rays straight down from the body's middle to its soles, at its
//     centre and a ring inside its radius. Walkable ground (normal within
//     MaxSlopeCos of up) above the soles lifts the body straight up and
//     counts as floor, the credit a brush floor gives — straight up, so a
//     body stood on a slope stays put rather than creeping downhill;
//   - ceiling: one ray up from the middle to the head, so a cave roof or an
//     overhang bonks.
//
// The rays cast against whichever surface the terrain draws — its blocks or
// its smooth surface — so what a body stands on is what is drawn.
//
// The probes themselves are surface.go's, behind an interface: a cooked
// Nanite mesh in mesh mode is a sheet too, and is asked the same questions
// (scenery_mesh.go).

import (
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// terrainRing is how many directions the wall rays take, and how many ring
// samples the floor takes around the centre.
const terrainRing = 8

// terrainShin is the height above the soles of the lowest wall rays: faces
// lower than that are floor (a slope) or a riser for StepUp.
const terrainShin = float32(0.3)

// pushOutOfTerrain resolves a cylindrical body — centre pos, radius, vertical
// half-height, all in the chunk's local space — against t. The probes are
// surface.go's, which a cooked mesh's collision cut answers too.
func pushOutOfTerrain(t *world.Terrain, pos mgl32.Vec3, radius, halfHeight float32) (out mgl32.Vec3, hitFloor, hitCeiling bool) {
	if t == nil {
		return pos, false, false
	}
	return pushOutOfSurface(terrainSurface{t}, pos, radius, halfHeight)
}

// terrainSurface is a terrain as a probeSurface, in its chunk's local space.
type terrainSurface struct{ t *world.Terrain }

func (s terrainSurface) probe(o, d mgl32.Vec3, maxT float32) (float32, mgl32.Vec3, bool) {
	hit, ok := s.t.Raycast(o, d, maxT)
	return hit.T, hit.Normal, ok
}

func (s terrainSurface) near(lo, hi mgl32.Vec3) bool { return s.t.SolidNear(lo, hi) }

func terrainAbs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
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
	_, ok := t.Raycast(p.Add(mgl32.Vec3{0, 1, 0}), mgl32.Vec3{0, -1, 0}, 1e5)
	return ok
}

// terrainProbe is the vertical particle probe (probeChunkSurface) against t,
// in chunk-local space: the distance along dir (straight down, or straight up
// when !descending) from origin to the surface.
func terrainProbe(t *world.Terrain, origin mgl32.Vec3, descending bool) (dist float32, n mgl32.Vec3, ok bool) {
	if t == nil {
		return 0, mgl32.Vec3{}, false
	}
	return surfaceProbe(terrainSurface{t}, origin, descending, 1e5)
}
