package collision

// particle_collide.go — the world-collision seam particle.Collide is bound to.
//
// A particle is not an entity: it has no bounding volume worth resolving
// against walls, it does not push anything, and there can be tens of
// thousands of them. So this is deliberately NOT the entity resolver with a
// smaller box. It is one question — did this step cross a brush face —
// answered with a single vertical probe through the destination XZ, which is
// what makes it cheap: a particle moving fast enough to cross more than a few
// units vertically in one frame is moving fast enough that exactly which
// surface it lands on does not matter.
//
// Horizontal motion is not tested. A particle that leaves the chunk it was in
// simply stops being asked about, which is cheap and, for the effects
// collision is for (sparks skipping off a floor, dust settling),
// indistinguishable from the right answer. Doing it properly would mean a
// swept test against every brush face the step crosses, per particle, per
// frame — which is the cost that made this a separate function in the first
// place. If an effect ever needs walls, that is the place to add them, and
// the seam does not change.

import (
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// particleProbeDepth is how far above (when falling) or below (when rising)
// the destination the vertical probe reaches — generous enough to catch a
// floor just under a fast-falling particle's landing point or a ceiling just
// over a rising one.
const particleProbeDepth = float32(4.0)

// ParticleCollide sweeps a particle of radius r from `from` to `to` against
// the level, returning where it first touched a solid brush face and the
// surface normal there.
//
// Installed into particle.Collide by internal/app; see the note on that
// variable for why it is a seam rather than an import.
func ParticleCollide(lvl *world.Level, from, to mgl32.Vec3, r float32) (pos, normal mgl32.Vec3, hit bool) {
	if lvl == nil {
		return to, mgl32.Vec3{}, false
	}
	// Which chunk the step is in is asked once, from where the particle
	// started, not per sample — the same "close enough" the old height-field
	// version made.
	ch := lvl.Frames.World().ChunkAt(lvl, from)
	if ch == nil {
		return to, mgl32.Vec3{}, false
	}

	lFrom := chunkLocal(ch, from)
	lTo := chunkLocal(ch, to)
	descending := lTo.Y() <= lFrom.Y()

	var origin, dir mgl32.Vec3
	if descending {
		origin = mgl32.Vec3{lTo.X(), lFrom.Y() + particleProbeDepth, lTo.Z()}
		dir = mgl32.Vec3{0, -1, 0}
	} else {
		origin = mgl32.Vec3{lTo.X(), lFrom.Y() - particleProbeDepth, lTo.Z()}
		dir = mgl32.Vec3{0, 1, 0}
	}

	bestT := float32(-1)
	var bestNormal mgl32.Vec3
	for bi := range ch.Brushes {
		b := &ch.Brushes[bi]
		if !b.Solid {
			continue
		}
		// The probe is a vertical line, so a brush whose box it misses in XZ
		// has no face it can meet — and a big chunk is mostly such brushes.
		if !bodyBoxTouchesBrush(mgl32.Vec3{lTo.X(), 0, lTo.Z()}, mgl32.Vec3{0, 1e9, 0}, b) {
			continue
		}
		for fi := range b.Faces {
			f := &b.Faces[fi]
			pts := b.PlacedFacePoints(f)
			for i := 1; i+1 < len(pts); i++ {
				t, ok := rayTriangle(origin, dir, pts[0], pts[i], pts[i+1])
				if !ok {
					continue
				}
				if bestT < 0 || t < bestT {
					bestT, bestNormal = t, b.PlacedFaceNormal(f)
				}
			}
		}
	}
	if bestT < 0 {
		return to, mgl32.Vec3{}, false
	}

	hitY := origin.Y() + dir.Y()*bestT
	if descending {
		if lTo.Y()-r > hitY {
			return to, mgl32.Vec3{}, false // hasn't reached the surface yet
		}
		lTo = mgl32.Vec3{lTo.X(), hitY + r, lTo.Z()}
	} else {
		if lTo.Y()+r < hitY {
			return to, mgl32.Vec3{}, false
		}
		lTo = mgl32.Vec3{lTo.X(), hitY - r, lTo.Z()}
	}

	worldNormal := ch.LocalToWorldDirection(bestNormal)
	if n := worldNormal.Len(); n > 1e-9 {
		worldNormal = worldNormal.Mul(1 / n)
	}
	return chunkWorld(ch, lTo), worldNormal, true
}
