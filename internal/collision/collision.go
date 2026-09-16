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

// bodyBoxTouchesBrush reports whether the box around a body (centre c,
// half-extents half) overlaps the box around brush b's hull as currently
// placed. Padded by a millimetre so a body resting exactly on a face — the
// usual case, a foot on the floor — still reaches GJK and is credited as
// ground.
func bodyBoxTouchesBrush(c, half mgl32.Vec3, b *world.Brush) bool {
	const pad = float32(1e-3)
	box := emptyBox()
	if b.AtRest() {
		for _, v := range b.Verts {
			box.addPoint(v)
		}
	} else {
		m := b.Placement()
		for _, v := range b.Verts {
			r := m.Mul4x1(v.Vec4(1))
			box.addPoint(mgl32.Vec3{r.X(), r.Y(), r.Z()})
		}
	}
	for i := 0; i < 3; i++ {
		if c[i]+half[i]+pad < box.lo[i] || c[i]-half[i]-pad > box.hi[i] {
			return false
		}
	}
	return true
}

// solidChunksFor is the set of chunks a body of the given half-extents is
// resolved against on a move from `from` to `to`: the frame's SolidChunks over
// the box both ends of the move occupy, padded vertically by the step a
// step-up probe may raise the body through.
func solidChunksFor(lvl *world.Level, frame *world.Frame, from, to, half mgl32.Vec3, step float32, out []*world.Chunk) []*world.Chunk {
	const margin = float32(0.05)
	pad := mgl32.Vec3{half.X() + margin, half.Y() + step + margin, half.Z() + margin}
	lo := mgl32.Vec3{min(from.X(), to.X()), min(from.Y(), to.Y()), min(from.Z(), to.Z())}.Sub(pad)
	hi := mgl32.Vec3{max(from.X(), to.X()), max(from.Y(), to.Y()), max(from.Z(), to.Z())}.Add(pad)
	return frame.SolidChunks(lvl, to, from, lo, hi, out)
}

// pushOutOfChunksBrushes is pushOutOfChunkBrushes over several chunks, each in
// its own local space, one after another — the same single pass a chunk's
// own brushes get, with the chunks' brushes read as one list.
func pushOutOfChunksBrushes(chunks []*world.Chunk, pos mgl32.Vec3, radius, halfHeight float32) (out mgl32.Vec3, hitFloor, hitCeiling bool) {
	out = pos
	for _, ch := range chunks {
		local, f, c := pushOutOfChunkBrushes(ch, chunkLocal(ch, out), radius, halfHeight)
		out = chunkWorld(ch, local)
		hitFloor = hitFloor || f
		hitCeiling = hitCeiling || c
	}
	return
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
		// Boxes first. GJK is by far the dearest thing in a physics tick and
		// a chunk is routinely hundreds of brushes, nearly all nowhere near
		// the body: without this a 700-brush canyon cost half a millisecond
		// per call, and an AI agent's step-up probes make several calls a
		// frame each. Exact, not a heuristic — two convex shapes whose boxes
		// are apart cannot overlap. The box is re-read from the corners every
		// call rather than cached, so nothing can go stale under an edit.
		// body moves as pushes land, so it is re-boxed from body.C.
		if !bodyBoxTouchesBrush(body.C, body.Half, b) {
			continue
		}
		// Pts are the brush's AUTHORED corners and Xform is where a script has
		// moved it to (world/brush_dynamic.go) — the identity for the static
		// brushes a level is mostly made of, so nothing here pays for the
		// feature until a level uses it.
		n, depth, ok := Penetration(body, ConvexHull{Pts: b.Verts, Xform: b.Placement()})
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

// SolidResolve is one pass of "push this body out of everything it is inside",
// for a body of a size the function already knows about: a position in, the
// position it may actually occupy out, and whether the correction that moved
// it there was a floor rest or a ceiling bonk.
//
// It exists so that a caller can bundle several kinds of solid — a chunk's
// brushes and an entity's, resolved in different coordinate spaces — into the
// single question everything walking asks of them. See MovePlayer, which builds
// one per substep.
type SolidResolve func(pos mgl32.Vec3) (out mgl32.Vec3, hitFloor, hitCeiling bool)

// StepUp retries a horizontal move that a flat resolve blocked, from higher
// up — the classic step-offset technique for climbing a curb or a stair riser
// without its vertical face reading as a wall.
//
// intendedXZ and flatResult are where the substep was aiming and where the flat
// resolve left it, both in the space resolve works in. If raising the probe
// clears the block (the raised attempt lands materially closer to the
// intended XZ than the flat one did) from a height the body could actually
// have risen to, the raised, re-settled result replaces it; otherwise the flat
// result stands.
//
// stepHeight <= 0 means no allowance at all — MovePlayer resolves the
// level's configured step height (world.PlayerSettings) to a concrete number
// before calling here, so a non-positive value is a deliberate "no
// stepping", not a sentinel to second-guess.
//
// resolve is what the body is solid against, and it is a function rather than
// a chunk because the answer is not one chunk's worth of brushes. A body
// walking into a ship is stopped by the chunk it is standing in AND by that
// ship's own solid, which is in the ship's coordinates and in none of this
// frame's chunks (entity_brushes.go) — so a step-up that retried against only
// the chunk would find the raised probe unobstructed, take the step, and be
// pushed straight back out by geometry it never consulted. A threshold at a
// ship's door is a riser like any other, and it has to be the same set of
// solids on the retry as on the attempt that failed.
func StepUp(resolve SolidResolve, intendedXZ mgl32.Vec2, flatResult mgl32.Vec3, stepHeight float32) mgl32.Vec3 {
	if resolve == nil || stepHeight <= 0 {
		return flatResult
	}
	flatXZ := mgl32.Vec2{flatResult.X(), flatResult.Z()}
	if flatXZ.Sub(intendedXZ).Len() < 1e-4 {
		return flatResult // not blocked; nothing to do
	}

	// A step-up is a teleport, not a sweep: the body is lifted straight up and
	// re-resolved there, with nothing checking what it passed through on the
	// way. That is sound only while the raised probe lands in FREE SPACE, and
	// a single raise of the full stepHeight often does not — so the lift is
	// tried at descending heights and the first one that fits is the one taken.
	//
	// Two ways a raise fails to fit, and they used to be handled as one:
	//
	//   - Solid above it (hitCeiling). The old code refused the step outright,
	//     which meant no stepping at all in any room shorter than stepHeight
	//     plus the body — a 2.5-unit room with the default 1.8 allowance could
	//     not be walked over a curb.
	//
	//   - Solid AROUND it, reported as a push UP. This is the dangerous one,
	//     and it is what let a player walk out through a ceiling. Pressed
	//     against a wall, the full raise lands the body straddling the ceiling
	//     slab, more than half of the way through it; the push-out is a
	//     minimum translation with no memory of which side the body came from,
	//     so once it is more than half through, the shortest way out is the FAR
	//     side. The body is ejected upward, settles on the roof, and has
	//     crossed geometry it never touched.
	//
	// Either way the answer is the same — that lift does not fit, try a
	// shorter one — and a lift that fits is one the body could have risen to
	// without passing through anything.
	const stepProbes = 4
	for probeIdx := 0; probeIdx < stepProbes; probeIdx++ {
		lift := stepHeight * float32(stepProbes-probeIdx) / float32(stepProbes)
		raised := mgl32.Vec3{intendedXZ.X(), flatResult.Y() + lift, intendedXZ.Y()}
		resolved, raisedIntoSolid, hitCeiling := resolve(raised)
		if hitCeiling || raisedIntoSolid {
			continue // this lift does not fit; a shorter one might
		}
		raisedXZ := mgl32.Vec2{resolved.X(), resolved.Z()}
		if raisedXZ.Sub(intendedXZ).Len() >= flatXZ.Sub(intendedXZ).Len()-1e-4 {
			// This lift did not clear the obstruction. Try a shorter one.
			//
			// It used to give up here, on the reasoning that a shorter lift
			// cannot clear what a taller one could not — true of a plain riser,
			// and false the moment anything overhangs it. What blocked the tall
			// probe may not be the riser at all: a doorway's own lintel, a
			// ship's roof over its threshold, the edge of a ceiling slab the
			// raised body straddles. A push out of THAT says nothing about
			// whether the body could have stepped over the sill underneath it,
			// and giving up on it meant a doorway with a sill and a low header
			// could not be walked through at all — which is most of the doorways
			// there are, since a hull's roof is right above its deck.
			continue
		}

		// Settle back down onto whatever is actually under the stepped-up
		// position, in a handful of substeps rather than the whole lift in one
		// jump — a curb an inch tall should not leave the body hovering the
		// full step height above it for a frame.
		const settleSubsteps = 8
		settled := resolved
		lowestFree := resolved
		touchedNonFloor := false
		for i := 1; i <= settleSubsteps; i++ {
			probe := settled
			probe[1] = resolved.Y() - lift*float32(i)/settleSubsteps
			s, hitFloor, _ := resolve(probe)
			if hitFloor {
				return s
			}
			if s.Sub(probe).Len() > 1e-5 {
				// Touching something that is not a floor on the way down.
				touchedNonFloor = true
				break
			}
			lowestFree = probe
			settled = probe
		}
		if touchedNonFloor {
			// The lift came down onto something too steep to stand on, not a
			// floor. Keeping it would leave the body up there, and next frame
			// the same lift is taken from the new height. That is how a player
			// climbed a canyon wall that leans back: every raised probe slid off
			// the steep face, stopped on it, and kept the height. A lift that
			// lands on nothing walkable is not a step, so try a shorter one.
			continue
		}
		// Nothing to land ON: go to the lowest probe that was still in free
		// space, never simply the top of the lift.
		//
		// Returning the lifted position here made every slope bounce. On any
		// sloped floor the flat resolve pushes the body out along the tilted
		// normal, which leaves it a hair short of its target in XZ and so
		// reads as "blocked"; the raised probe then clears that hair, and every
		// settle probe is free down to the last one, which sits exactly where
		// the body already rested — just clear of the floor, so no floor
		// contact — and the body was left the full step height up, to fall back
		// down over the next ten frames and do it again. On a curb the probes
		// stop being free just below its top, so the body is left just over it
		// and lands on it, as before.
		return lowestFree
	}
	return flatResult
}
