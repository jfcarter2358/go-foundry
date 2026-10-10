package collision

// surface.go — a body against a continuous sheet, by short rays from inside it.
//
// terrain.go explains why a sheet is not resolved as convex solids: cutting
// it into per-face solids hands GJK the internal edges between neighbouring
// faces, and a body sliding across a seam catches on flat ground. The
// questions it asks instead — walls by rays out at the shins, the middle and
// the head; floor by rays down inside the footprint; ceiling by one ray up —
// need nothing of the surface but a ray cast and a cheap "is anything near".
// That is probeSurface, and two things answer it: a chunk's block terrain,
// and a cooked Nanite mesh's collision cut in mesh mode (scenery_mesh.go),
// whose rays run through its tree in mesh-local space.
//
// A probe returns the hit's normal facing back along the ray — into the air
// the body is in — which is what every push below is built from.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// probeSurface is a sheet a body is resolved against by rays.
type probeSurface interface {
	// probe casts from o along unit d for up to maxT, and returns the
	// distance to the first surface and its unit normal there.
	probe(o, d mgl32.Vec3, maxT float32) (t float32, n mgl32.Vec3, ok bool)
	// near reports whether any surface may lie in the box [lo, hi]: the cheap
	// reject that spares a body nowhere near it every ray.
	near(lo, hi mgl32.Vec3) bool
}

// pushOutOfSurface resolves a cylindrical body — centre pos, radius, vertical
// half-height, in the surface's space — against s, by terrain.go's probes.
func pushOutOfSurface(s probeSurface, pos mgl32.Vec3, radius, halfHeight float32) (out mgl32.Vec3, hitFloor, hitCeiling bool) {
	out = pos
	if s == nil || radius <= 0 || halfHeight <= 0 {
		return
	}
	half := mgl32.Vec3{radius, halfHeight, radius}
	// Nothing to do for a body nowhere near a solid block — which is nearly
	// every body, nearly every tick, and costs a handful of byte reads.
	if !s.near(pos.Sub(half), pos.Add(half)) {
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
				hitT, hitN, ok := s.probe(o, d, radius)
				if !ok || terrainAbs(hitN.Y()) >= MaxSlopeCos {
					continue
				}
				hn := mgl32.Vec3{hitN.X(), 0, hitN.Z()}
				if l := hn.Len(); l > 1e-4 {
					hn = hn.Mul(1 / l)
				} else {
					hn = d.Mul(-1)
				}
				// The ray's overshoot, measured along the face's normal.
				depth := (radius - hitT) * max(-d.Dot(hn), 0.2)
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
			hitT, hitN, ok := s.probe(o, down, halfHeight)
			if !ok || hitN.Y() < MaxSlopeCos {
				continue
			}
			lift = max(lift, halfHeight-hitT)
		}
		if lift > 0 {
			out[1] += lift + eps
			hitFloor = true
			moved = true
		}

		// Ceiling.
		if hitT, hitN, ok := s.probe(out, up, halfHeight); ok && hitN.Y() < 0 {
			out[1] -= halfHeight - hitT + eps
			hitCeiling = true
			moved = true
		}
		if !moved {
			break
		}
	}
	return
}

// surfaceProbe is the vertical particle probe against s: the distance from
// origin straight down (or straight up when !descending) to the surface,
// within maxT.
func surfaceProbe(s probeSurface, origin mgl32.Vec3, descending bool, maxT float32) (dist float32, n mgl32.Vec3, ok bool) {
	dir := mgl32.Vec3{0, -1, 0}
	if !descending {
		dir = mgl32.Vec3{0, 1, 0}
	}
	return s.probe(origin, dir, maxT)
}
