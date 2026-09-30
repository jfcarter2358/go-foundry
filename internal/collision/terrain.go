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

import (
	"math"

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
// half-height, all in the chunk's local space — against t.
func pushOutOfTerrain(t *world.Terrain, pos mgl32.Vec3, radius, halfHeight float32) (out mgl32.Vec3, hitFloor, hitCeiling bool) {
	out = pos
	if t == nil || radius <= 0 || halfHeight <= 0 {
		return
	}
	half := mgl32.Vec3{radius, halfHeight, radius}
	// Nothing to do for a body nowhere near a solid block — which is nearly
	// every body, nearly every tick, and costs a handful of byte reads.
	if !t.SolidNear(pos.Sub(half), pos.Add(half)) {
		return
	}
	const eps = float32(0.001)
	down, up := mgl32.Vec3{0, -1, 0}, mgl32.Vec3{0, 1, 0}
	var dirs [terrainRing]mgl32.Vec3
	for i := range dirs {
		a := float64(i) * 2 * math.Pi / terrainRing
		dirs[i] = mgl32.Vec3{float32(math.Cos(a)), 0, float32(math.Sin(a))}
	}
	// A few rounds: a push off a wall can move the footprint onto ground that
	// now needs lifting, and a lift can bring the head up to a roof.
	for round := 0; round < 3; round++ {
		moved := false

		// Walls.
		soles, head := out.Y()-halfHeight, out.Y()+halfHeight
		shin := soles + min(terrainShin, halfHeight)
		heights := [3]float32{shin, out.Y(), head - min(0.1, halfHeight*0.2)}
		var push mgl32.Vec3
		deepest := float32(0)
		for hi, h := range heights {
			if hi > 0 && h <= heights[hi-1]+1e-3 {
				continue
			}
			o := mgl32.Vec3{out.X(), h, out.Z()}
			for _, d := range dirs {
				hit, ok := t.Raycast(o, d, radius)
				if !ok || terrainAbs(hit.Normal.Y()) >= MaxSlopeCos {
					continue
				}
				hn := mgl32.Vec3{hit.Normal.X(), 0, hit.Normal.Z()}
				if l := hn.Len(); l > 1e-4 {
					hn = hn.Mul(1 / l)
				} else {
					hn = d.Mul(-1)
				}
				// The ray's overshoot, measured along the face's normal.
				depth := (radius - hit.T) * max(-d.Dot(hn), 0.2)
				if depth > deepest {
					deepest = depth
					push = hn.Mul(depth + eps)
				}
			}
		}
		if deepest > 0 {
			out = out.Add(push)
			moved = true
		}

		// Floor.
		lift := float32(0)
		for i := -1; i < terrainRing; i++ {
			o := mgl32.Vec3{out.X(), out.Y(), out.Z()}
			if i >= 0 {
				o = o.Add(dirs[i].Mul(radius * 0.8))
			}
			hit, ok := t.Raycast(o, down, halfHeight)
			if !ok || hit.Normal.Y() < MaxSlopeCos {
				continue
			}
			lift = max(lift, halfHeight-hit.T)
		}
		if lift > 0 {
			out[1] += lift + eps
			hitFloor = true
			moved = true
		}

		// Ceiling.
		if hit, ok := t.Raycast(out, up, halfHeight); ok && hit.Normal.Y() < 0 {
			out[1] -= halfHeight - hit.T + eps
			hitCeiling = true
			moved = true
		}
		if !moved {
			break
		}
	}
	return
}

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
	dir := mgl32.Vec3{0, -1, 0}
	if !descending {
		dir = mgl32.Vec3{0, 1, 0}
	}
	hit, ok := t.Raycast(origin, dir, 1e5)
	if !ok {
		return 0, mgl32.Vec3{}, false
	}
	return hit.T, hit.Normal, true
}
