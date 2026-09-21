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
// Horizontal motion is not tested. A particle over no chunk at all simply
// stops being asked about, which is cheap and, for the effects
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
	descending := to.Y() <= from.Y()

	// Every chunk the vertical probe passes through — a particle over the
	// seam between two chunks lands on whichever floor is under it — asked
	// once for the step, not per sample.
	lo := mgl32.Vec3{to.X(), min(from.Y(), to.Y()) - particleProbeDepth, to.Z()}
	hi := mgl32.Vec3{to.X(), max(from.Y(), to.Y()) + particleProbeDepth, to.Z()}
	var buf [4]*world.Chunk
	chunks := world.ChunksOverlapping(lvl, lo, hi, buf[:0])

	found := false
	var hitPoint, hitNormal mgl32.Vec3
	for _, ch := range chunks {
		p, n, ok := probeChunkSurface(ch, from, to, descending)
		if !ok {
			continue
		}
		// The first surface the probe meets, over all of them: the highest
		// when it runs down, the lowest when it runs up.
		if !found || (descending && p.Y() > hitPoint.Y()) || (!descending && p.Y() < hitPoint.Y()) {
			found, hitPoint, hitNormal = true, p, n
		}
	}
	if !found {
		return to, mgl32.Vec3{}, false
	}
	if descending {
		if to.Y()-r > hitPoint.Y() {
			return to, mgl32.Vec3{}, false // hasn't reached the surface yet
		}
		return mgl32.Vec3{to.X(), hitPoint.Y() + r, to.Z()}, hitNormal, true
	}
	if to.Y()+r < hitPoint.Y() {
		return to, mgl32.Vec3{}, false
	}
	return mgl32.Vec3{to.X(), hitPoint.Y() - r, to.Z()}, hitNormal, true
}

// probeChunkSurface runs the vertical probe through one chunk's brushes, in the
// chunk's local space, and returns the world point and unit normal of the
// first solid face it meets. A chunk only turns about Y and scales uniformly,
// so a vertical probe stays vertical in its space.
func probeChunkSurface(ch *world.Chunk, from, to mgl32.Vec3, descending bool) (point, normal mgl32.Vec3, ok bool) {
	lFrom := chunkLocal(ch, from)
	lTo := chunkLocal(ch, to)

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
	pc := brushBoxesFor(ch)
	if pc == nil {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	test := func(b *world.Brush, f *world.BrushFace) {
		pts := b.PlacedFacePoints(f)
		for i := 1; i+1 < len(pts); i++ {
			t, hit := rayTriangle(origin, dir, pts[0], pts[i], pts[i+1])
			if hit && (bestT < 0 || t < bestT) {
				bestT, bestNormal = t, b.PlacedFaceNormal(f)
			}
		}
	}
	// The fixed brushes under the column, face boxes first.
	for _, bi := range pc.fixedUnder(lTo) {
		b := &ch.Brushes[bi]
		if !b.Solid || !pc.boxes[bi].inColumn(lTo) {
			continue
		}
		for fi := range b.Faces {
			if pc.faceBoxes[bi][fi].inColumn(lTo) {
				test(b, &b.Faces[fi])
			}
		}
	}
	// And the loose ones, boxed where they are now.
	for _, bi := range pc.loose {
		b := &ch.Brushes[bi]
		if !b.Solid || !bodyBoxTouchesBrush(mgl32.Vec3{lTo.X(), 0, lTo.Z()}, mgl32.Vec3{0, 1e9, 0}, b) {
			continue
		}
		for fi := range b.Faces {
			test(b, &b.Faces[fi])
		}
	}
	if bestT < 0 {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	point = chunkWorld(ch, origin.Add(dir.Mul(bestT)))
	normal = ch.LocalToWorldDirection(bestNormal)
	if n := normal.Len(); n > 1e-9 {
		normal = normal.Mul(1 / n)
	}
	return point, normal, true
}
