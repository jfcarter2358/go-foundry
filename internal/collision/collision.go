package collision

// collision.go — resolving a body against solid brush geometry.
//
// The sector model split this into five specialised resolvers — walls,
// slopes, floor/ceiling clamping, and two "entry" refusals for the cases a
// height-field description got wrong (walking onto the outside of a ceiling,
// walking through the inside of a step riser). A brush is actual solid
// geometry rather than a floor/ceiling height pair, so one uniform operation
// — push the body's shape out of whatever brush it overlaps — resolves walls,
// floors and ceilings together: the correction naturally reads as a wall
// push, a floor rest or a ceiling bonk depending on which face was touched,
// with no separate accounting for which kind of surface it was.
//
// What a height field DID give for free and a brush does not: knowing "the
// floor" without touching it, which is what let the old resolvers refuse an
// illegal move before the body arrived. Two things are rebuilt here instead:
// a slope becomes unwalkable by gating which contacts count as "floor" on the
// angle of the face touched (MaxSlopeCos, as before), and a step becomes
// climbable via the classic step-offset technique — retry a blocked
// horizontal move from stepHeight higher, then settle back down onto
// whatever is actually there.
//
// NOTE: this is a first-pass reimplementation against the new solid-geometry
// model, not a tuned port. The old resolvers' exact feel (their doc comments
// call slope/step handling "load-bearing tuning for how gameplay feels") took
// real playtesting to arrive at; this one has not had that pass yet.

import (
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// MaxSlopeCos is cos(45°). A contact whose upward normal has normalY/|normal|
// below this is too steep to stand on — it still stops the body (the
// positional correction always applies), but does not count as ground: it
// does not zero falling velocity and does not grant a jump.
const MaxSlopeCos = float32(0.7071)

// chunkLocal and chunkWorld bridge the one gap left once a chunk can carry its
// own Origin/Rotation/Scale: everything in this package resolves a position
// against whichever chunks its frame owns (player.go, entity_collision.go,
// frame_portal.go), and that position arrives already carried out of the
// frame — via Frame.FromWorld, one layer up — but not yet out of the specific
// chunk's own placement within that frame. Brush verts are chunk-local; a
// wall push or floor rest has to run in that same space, so every resolver
// call in this package converts in with chunkLocal, computes, and converts
// back out with chunkWorld.
//
// This is deliberately chunk.LocalPlacementMatrix, not the full ModelMatrix:
// an owning frame's Xform was already divided out by the caller that found
// chunk in the first place (Frame.ChunkAt), so undoing it again here would
// double-subtract it. For an ordinary (non-frame) chunk the two matrices are
// the same thing, since ModelMatrix only layers Xform on top when the chunk
// is frame-owned — so this is exactly WorldToLocal/LocalToWorld for the
// overwhelmingly common case, and only diverges for a frame-owned chunk that
// also has its own local placement.
func chunkLocal(chunk *world.Chunk, p mgl32.Vec3) mgl32.Vec3 {
	if chunk == nil {
		return p
	}
	m := chunk.LocalPlacementMatrix()
	if m == mgl32.Ident4() {
		return p
	}
	r := m.Inv().Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

func chunkWorld(chunk *world.Chunk, p mgl32.Vec3) mgl32.Vec3 {
	if chunk == nil {
		return p
	}
	m := chunk.LocalPlacementMatrix()
	if m == mgl32.Ident4() {
		return p
	}
	r := m.Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// closestPointOnSegment2D returns the point on segment (a,b) closest to p
// and the distance from p to that point.
func closestPointOnSegment2D(p, a, b mgl32.Vec2) (mgl32.Vec2, float32) {
	closest, dist, _ := closestPointOnSegment2DWithT(p, a, b)
	return closest, dist
}

// closestPointOnSegment2DWithT returns the closest point, distance, and
// parametric t (0..1) along segment (a,b).
func closestPointOnSegment2DWithT(p, a, b mgl32.Vec2) (mgl32.Vec2, float32, float32) {
	ab := b.Sub(a)
	lenSq := ab.Dot(ab)
	if lenSq < 1e-12 {
		return a, a.Sub(p).Len(), 0
	}
	t := p.Sub(a).Dot(ab) / lenSq
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	closest := a.Add(ab.Mul(t))
	return closest, closest.Sub(p).Len(), t
}

// pushOutOfChunkBrushes pushes a cylindrical body — centre pos, given
// horizontal radius and vertical half-height, matching the footprint the
// sector-era wall resolver used (a circular cross-section, so corners never
// catch on a wall the way a box's would) — out of every solid brush in chunk,
// one brush at a time.
//
// One pass per brush is the same "one solver iteration is enough" reasoning
// entity_collision.go's pairwise resolve uses: this runs every physics tick,
// and a body embedded in more than one brush at once is rare enough that a
// second tick finishing the job is not visible.
//
// hitFloor/hitCeiling report whether the STRONGEST correction (by vertical
// component) was predominantly up or down — a floor rest or a ceiling bonk —
// which is what the caller zeroes vertical velocity against. A floor contact
// only counts if the face is walkable (see MaxSlopeCos); a steeper one still
// gets the body pushed off it, just not credited as ground.
func pushOutOfChunkBrushes(chunk *world.Chunk, pos mgl32.Vec3, radius, halfHeight float32) (out mgl32.Vec3, hitFloor, hitCeiling bool) {
	out = pos
	if chunk == nil || radius <= 0 || halfHeight <= 0 {
		return
	}
	body := Cylinder{C: out, Half: mgl32.Vec3{radius, halfHeight, radius}, Rot: mgl32.Ident4()}
	bestFloorCos := float32(0)
	for bi := range chunk.Brushes {
		b := &chunk.Brushes[bi]
		if !b.Solid || len(b.Verts) < 4 {
			continue
		}
		n, depth, ok := Penetration(body, ConvexHull{Pts: b.Verts})
		if !ok || depth <= 0 {
			continue
		}
		// Penetration's normal points from the first shape toward the second
		// (body toward brush), so the body's correction is the other way. A
		// small extra margin beyond the reported depth clears EPA's own
		// convergence tolerance — pushing out by exactly depth can leave a
		// sliver of residual penetration that reports as still touching next
		// frame, the same reason the old wall resolver pushed to radius+epsilon
		// rather than exactly radius.
		const separationEpsilon = float32(0.001)
		push := n.Mul(-(depth + separationEpsilon))
		out = out.Add(push)
		body = Translate(body, push).(Cylinder)

		mag := push.Len()
		if mag < 1e-9 {
			continue
		}
		cos := push.Y() / mag
		switch {
		case cos > 0:
			if cos >= MaxSlopeCos && cos > bestFloorCos {
				bestFloorCos = cos
				hitFloor = true
			}
		case cos < 0:
			if -cos >= MaxSlopeCos {
				hitCeiling = true
			}
		}
	}
	return
}

// StepUp retries a horizontal move that pushOutOfChunkBrushes blocked, from
// stepHeight higher — the classic step-offset technique for climbing a curb
// or a stair riser without its vertical face reading as a wall.
//
// from and to are chunk-local, pre- and post-resolve. If raising the probe
// clears the block (the raised attempt lands materially closer to the
// intended XZ than the flat one did) and does not itself hit a ceiling, the
// raised, re-settled result replaces it; otherwise the flat result stands.
//
// stepHeight <= 0 means no allowance at all — MovePlayer resolves the
// level's configured step height (world.PlayerSettings) to a concrete number
// before calling here, so a non-positive value is a deliberate "no
// stepping", not a sentinel to second-guess.
func StepUp(chunk *world.Chunk, intendedXZ mgl32.Vec2, flatResult mgl32.Vec3, radius, halfHeight, stepHeight float32) mgl32.Vec3 {
	if chunk == nil || stepHeight <= 0 {
		return flatResult
	}
	flatXZ := mgl32.Vec2{flatResult.X(), flatResult.Z()}
	if flatXZ.Sub(intendedXZ).Len() < 1e-4 {
		return flatResult // not blocked; nothing to do
	}

	raised := mgl32.Vec3{intendedXZ.X(), flatResult.Y() + stepHeight, intendedXZ.Y()}
	resolved, _, hitCeiling := pushOutOfChunkBrushes(chunk, raised, radius, halfHeight)
	if hitCeiling {
		return flatResult // no headroom to step up into
	}
	raisedXZ := mgl32.Vec2{resolved.X(), resolved.Z()}
	if raisedXZ.Sub(intendedXZ).Len() >= flatXZ.Sub(intendedXZ).Len()-1e-4 {
		return flatResult // stepping up did not actually clear the obstruction
	}

	// Settle back down onto whatever is actually under the stepped-up
	// position, in a handful of substeps rather than the full stepHeight in
	// one jump — a curb an inch tall should not leave the body hovering
	// stepHeight above it for a frame.
	const settleSubsteps = 8
	settled := resolved
	for i := 1; i <= settleSubsteps; i++ {
		probe := settled
		probe[1] = resolved.Y() - stepHeight*float32(i)/settleSubsteps
		s, hitFloor, _ := pushOutOfChunkBrushes(chunk, probe, radius, halfHeight)
		if hitFloor {
			return s
		}
		settled = probe
	}
	return resolved
}
