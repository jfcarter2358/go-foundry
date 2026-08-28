package biome

// biome_anchor.go — carrying painted biome polygons along when the floor under
// them is edited.
//
// A BiomeSector's polygon is stored chunk-local, deliberately independent of
// structural geometry (see BiomeSector.Points): paint can span several sectors,
// spill past their edges, and outlive the sector it was painted on. That
// independence is right for storage and wrong for editing — dragging a wall in
// left the grass hanging over where the floor used to be.
//
// So the binding is made at edit time rather than persisted. When chunk points
// move, every biome vertex that sat inside one of the affected sectors is
// re-expressed in that sector's *pre-edit* shape and evaluated again in its new
// one, which is what makes the paint sector-relative in practice without
// committing a sector reference to the level file. The coordinates doing the
// re-expressing are Floater's mean value coordinates: a generalised barycentric
// coordinate that is defined on any simple polygon, convex or not, and varies
// smoothly across it, so a vertex a third of the way across a sector stays a
// third of the way across it whichever corner moved.
//
// Vertices outside every edited sector are left where they are — paint spilling
// past the floor has nothing to follow.

import (
	"foundry/internal/geom"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// pointMove records one chunk point an edit has already moved: its index, and
// where it was before the move.
type PointMove struct {
	PointIdx int
	From     mgl32.Vec2
}

// sectorWarp is one sector's shape before and after an edit, index-aligned.
// sector identifies which sector it came from, so a biome vertex can be
// checked against the one it actually drapes on rather than against whichever
// warped polygon happens to contain it in XZ.
type sectorWarp struct {
	sector        *world.Sector
	before, after []mgl32.Vec2
}

// carryBiomesWithPoints re-fits the biome polygons draped over ch to the new
// positions of the points in moves — which the caller has already written into
// ch.Points — and marks every polygon it changes for a mesh rebuild.
func CarryBiomesWithPoints(ch *world.Chunk, moves []PointMove) {
	if ch == nil || len(moves) == 0 {
		return
	}
	was := make(map[int]mgl32.Vec2, len(moves))
	for _, m := range moves {
		if m.PointIdx < 0 || m.PointIdx >= len(ch.Points) {
			continue
		}
		if m.From != ch.Points[m.PointIdx] {
			was[m.PointIdx] = m.From
		}
	}
	if len(was) == 0 {
		return
	}

	var warps []sectorWarp
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		after := world.SectorPoints(ch, s)
		if len(after) < 3 {
			continue
		}
		before := make([]mgl32.Vec2, len(after))
		copy(before, after)
		moved := false
		for i, idx := range s.PointIndices {
			if p, ok := was[idx]; ok {
				before[i] = p
				moved = true
			}
		}
		if moved {
			warps = append(warps, sectorWarp{sector: s, before: before, after: after})
		}
	}
	if len(warps) == 0 {
		return
	}

	// The level the drape resolution needs, via the same seam
	// ForEachBiomeSector reads (level_context.go). nil outside the editor,
	// where nothing edits geometry anyway.
	lvl := world.CurrentLevel()

	// Containment as it was BEFORE this edit: the caller has already written
	// the moved points, so ch's warped sectors have to be tested against their
	// recorded `before` shape. Everything else is untouched and answers from
	// live geometry (allocation-free, which matters — this runs per biome
	// vertex per mouse-move).
	wasInside := func(c *world.Chunk, s *world.Sector, xz mgl32.Vec2) bool {
		if c == ch {
			for i := range warps {
				if warps[i].sector == s {
					return geom.PointInPolygon(xz, warps[i].before)
				}
			}
		}
		return world.PointInSector(c, s, xz)
	}

	world.ForEachBiomeSector(ch, func(owner *world.Chunk, bs *world.BiomeSector) {
		// warps are in ch's local space (they were built from ch's own
		// SectorPoints). A biome sector painted on a DIFFERENT chunk stores
		// its points in that chunk's local space, so it has to be carried
		// into ch's frame to be tested and warped, and carried back to be
		// stored — comparing the two raw would warp paint by an edit it is
		// nowhere near in the world, the same mismatch MarkBiomesDirtyOverBox
		// documents. toEdit/fromEdit collapse to the identity for the
		// overwhelmingly common owner == ch case, which is also the only case
		// that existed before chunks had live transforms.
		toEdit, fromEdit := crossChunkPointMap(owner, ch)
		changed := false
		for i, v := range bs.Points {
			// A vertex follows only the floor it is actually DRAPED on, and
			// DrapeTarget is the single definition of which floor that is —
			// the same one the height sample uses, so the two can never
			// disagree about a given point.
			//
			// Comparing the resolved SECTOR (not just its chunk) is what
			// separates two stacked sectors of one chunk: editing a mezzanine
			// leaves the floor's paint alone, and editing the floor leaves the
			// mezzanine's alone. Chunk-level comparison alone could not tell
			// those apart. Across chunks it is the same test that stops a
			// platform floating overhead from hauling painted ground around
			// (and re-baking its height texture on every mouse-move).
			_, drape := world.DrapeTargetWith(lvl, owner, bs.DrapeSector, v, wasInside)
			if drape == nil || !warpedSector(warps, drape) {
				continue
			}
			moved, ok := warpPoint(toEdit(v), warps)
			if !ok {
				continue
			}
			back := fromEdit(moved)
			if back == v {
				continue
			}
			bs.Points[i] = back
			changed = true
		}
		if changed {
			bs.HeightDirty = true
			bs.GroundDirty = true
		}
	})
}

// warpedSector reports whether s is one of the sectors this edit is reshaping.
func warpedSector(warps []sectorWarp, s *world.Sector) bool {
	for i := range warps {
		if warps[i].sector == s {
			return true
		}
	}
	return false
}

// crossChunkPointMap returns the pair of XZ conversions carrying a point from
// owner's local space into edited's, and back. Both are the identity when the
// two are the same chunk (or either is nil), so the ordinary same-chunk edit
// pays nothing.
func crossChunkPointMap(owner, edited *world.Chunk) (toEdited, fromEdited func(mgl32.Vec2) mgl32.Vec2) {
	identity := func(p mgl32.Vec2) mgl32.Vec2 { return p }
	if owner == nil || edited == nil || owner == edited {
		return identity, identity
	}
	toEdited = func(p mgl32.Vec2) mgl32.Vec2 {
		w := owner.LocalToWorld(mgl32.Vec3{p.X(), 0, p.Y()})
		l := edited.WorldToLocal(w)
		return mgl32.Vec2{l.X(), l.Z()}
	}
	fromEdited = func(p mgl32.Vec2) mgl32.Vec2 {
		w := edited.LocalToWorld(mgl32.Vec3{p.X(), 0, p.Y()})
		l := owner.WorldToLocal(w)
		return mgl32.Vec2{l.X(), l.Z()}
	}
	return toEdited, fromEdited
}

// warpPoint carries v through the first warp whose pre-edit polygon contains
// it. A vertex sits on one floor, so overlapping sectors do not each get to
// move it.
func warpPoint(v mgl32.Vec2, warps []sectorWarp) (mgl32.Vec2, bool) {
	for _, w := range warps {
		if !geom.PointInPolygon(v, w.before) {
			continue
		}
		coords := MeanValueCoords(v, w.before)
		if coords == nil {
			return v, false
		}
		var out mgl32.Vec2
		for k, weight := range coords {
			out = out.Add(w.after[k].Mul(weight))
		}
		return out, true
	}
	return v, false
}

// MeanValueCoords returns Floater's mean value coordinates of v with respect to
// poly: weights that sum to 1 and reproduce v as Σ wᵢ·polyᵢ, and go on placing
// the point the same way relative to the polygon once the polygon is deformed.
// Returns nil when the polygon is too small or too degenerate to bind to.
//
// wᵢ = (tan(αᵢ₋₁/2) + tan(αᵢ/2)) / rᵢ, where αᵢ is the angle at v subtended by
// edge i→i+1 and rᵢ is the distance from v to vertex i. The half-angle tangent
// comes from the cross and dot products of the two spokes without a trig call:
// tan(α/2) = (1-cos α)/sin α = (rᵢrⱼ - sᵢ·sⱼ) / (sᵢ × sⱼ).
func MeanValueCoords(v mgl32.Vec2, poly []mgl32.Vec2) []float32 {
	n := len(poly)
	if n < 3 {
		return nil
	}
	const eps = 1e-6

	spoke := make([]mgl32.Vec2, n)
	dist := make([]float32, n)
	for i, p := range poly {
		spoke[i] = p.Sub(v)
		dist[i] = spoke[i].Len()
		if dist[i] < eps {
			// v is that corner, so it simply goes where the corner goes.
			w := make([]float32, n)
			w[i] = 1
			return w
		}
	}

	tanHalf := make([]float32, n)
	for i := range poly {
		j := (i + 1) % n
		cross := spoke[i].X()*spoke[j].Y() - spoke[i].Y()*spoke[j].X()
		dot := spoke[i].Dot(spoke[j])
		if geom.Abs32(cross) < eps {
			if dot < 0 {
				// v lies on edge i→j, where the half-angle tangent blows up:
				// the edge's two ends carry it between them.
				w := make([]float32, n)
				total := dist[i] + dist[j]
				w[i] = dist[j] / total
				w[j] = dist[i] / total
				return w
			}
			continue // collinear but off the far end: the edge subtends nothing
		}
		tanHalf[i] = (dist[i]*dist[j] - dot) / cross
	}

	w := make([]float32, n)
	var sum float32
	for i := range poly {
		prev := (i - 1 + n) % n
		w[i] = (tanHalf[prev] + tanHalf[i]) / dist[i]
		sum += w[i]
	}
	if geom.Abs32(sum) < eps {
		return nil
	}
	for i := range w {
		w[i] /= sum
	}
	return w
}

// ---------- floor sampling ----------
//
// What a biome polygon drapes over. Used by the scatter renderer, the ground
// patch mesher and the water plane alike, so it sits with the biome data
// rather than inside any one of them.

// FloorHeightUnderLevel returns the structural floor height (accounting for
// terrain-mode slope/displacement, via the same code path player collision
// uses) at owner-local XZ (wx,wz), or 0 over open ground with no sector. See
// FloorHeightNormalUnderLevel for what owner means and why it matters.
func FloorHeightUnderLevel(level *world.Level, owner *world.Chunk, drape string, wx, wz float32) float32 {
	h, _, _ := FloorHeightNormalUnderLevel(level, owner, drape, wx, wz)
	return h
}

// FloorHeightNormalUnderLevel is FloorHeightUnderLevel's full form: also
// returns the flat floor's normal and the Sector resolved by DrapeTarget (nil
// over open ground), so callers that need to know about e.g. that sector's
// procedural displacement detail (biome_displace.go) don't have to re-run the
// same search.
//
// owner is the chunk the asking biome sector was painted on and (wx,wz) is in
// THAT chunk's local space; the height and normal come back in that space too,
// carried across when the resolved sector belongs to a different chunk.
func FloorHeightNormalUnderLevel(level *world.Level, owner *world.Chunk, drape string, wx, wz float32) (float32, mgl32.Vec3, *world.Sector) {
	c, s := world.DrapeTarget(level, owner, drape, wx, wz)
	if s == nil {
		return 0, mgl32.Vec3{0, 1, 0}, nil
	}
	xz := mgl32.Vec2{wx, wz}
	cxz := world.DrapePointIn(owner, c, xz)
	h, n, _ := world.InterpolateSurfaceHeight(c, s, cxz, true)

	// Both come back in c's local space and the caller measures everything in
	// owner's. The normal goes through the direction form (no translation) and
	// is renormalized, since a scale difference between the two chunks would
	// otherwise change its length and the slope gating downstream reads its
	// angle.
	if owner != nil && c != owner && owner.ModelMatrix() != c.ModelMatrix() {
		w := c.LocalToWorld(mgl32.Vec3{cxz.X(), h, cxz.Y()})
		h = owner.WorldToLocal(w).Y()
		n = owner.WorldToLocalDirection(c.LocalToWorldDirection(n))
		if l := n.Len(); l > 1e-8 {
			n = n.Mul(1 / l)
		} else {
			n = mgl32.Vec3{0, 1, 0}
		}
	}
	return h, n, s
}
