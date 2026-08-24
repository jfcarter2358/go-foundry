package main

// biome_anchor.go — carrying painted biome polygons along when the floor under
// them is edited.
//
// A BiomeSector's polygon is stored in world space, deliberately independent of
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
type pointMove struct {
	pointIdx int
	from     mgl32.Vec2
}

// sectorWarp is one sector's shape before and after an edit, index-aligned.
type sectorWarp struct {
	before, after []mgl32.Vec2
}

// carryBiomesWithPoints re-fits the biome polygons draped over ch to the new
// positions of the points in moves — which the caller has already written into
// ch.Points — and marks every polygon it changes for a mesh rebuild.
func carryBiomesWithPoints(ch *world.Chunk, moves []pointMove) {
	if ch == nil || len(moves) == 0 {
		return
	}
	was := make(map[int]mgl32.Vec2, len(moves))
	for _, m := range moves {
		if m.pointIdx < 0 || m.pointIdx >= len(ch.Points) {
			continue
		}
		if m.from != ch.Points[m.pointIdx] {
			was[m.pointIdx] = m.from
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
			warps = append(warps, sectorWarp{before: before, after: after})
		}
	}
	if len(warps) == 0 {
		return
	}

	world.ForEachBiomeSector(ch, func(bs *world.BiomeSector) {
		changed := false
		for i, v := range bs.Points {
			moved, ok := warpPoint(v, warps)
			if !ok || moved == v {
				continue
			}
			bs.Points[i] = moved
			changed = true
		}
		if changed {
			bs.HeightDirty = true
			bs.GroundDirty = true
		}
	})
}

// warpPoint carries v through the first warp whose pre-edit polygon contains
// it. A vertex sits on one floor, so overlapping sectors do not each get to
// move it.
func warpPoint(v mgl32.Vec2, warps []sectorWarp) (mgl32.Vec2, bool) {
	for _, w := range warps {
		if !geom.PointInPolygon(v, w.before) {
			continue
		}
		coords := meanValueCoords(v, w.before)
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

// meanValueCoords returns Floater's mean value coordinates of v with respect to
// poly: weights that sum to 1 and reproduce v as Σ wᵢ·polyᵢ, and go on placing
// the point the same way relative to the polygon once the polygon is deformed.
// Returns nil when the polygon is too small or too degenerate to bind to.
//
// wᵢ = (tan(αᵢ₋₁/2) + tan(αᵢ/2)) / rᵢ, where αᵢ is the angle at v subtended by
// edge i→i+1 and rᵢ is the distance from v to vertex i. The half-angle tangent
// comes from the cross and dot products of the two spokes without a trig call:
// tan(α/2) = (1-cos α)/sin α = (rᵢrⱼ - sᵢ·sⱼ) / (sᵢ × sⱼ).
func meanValueCoords(v mgl32.Vec2, poly []mgl32.Vec2) []float32 {
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

// floorHeightUnderLevel returns the structural floor height (accounting for
// terrain-mode slope/displacement, via the same code path player collision
// uses) at world XZ (wx,wz), or 0 over open ground with no sector.
func floorHeightUnderLevel(level *world.Level, wx, wz float32) float32 {
	h, _, _ := floorHeightNormalUnderLevel(level, wx, wz)
	return h
}

// floorHeightNormalUnderLevel is floorHeightUnderLevel's full form: also
// returns the flat floor's normal and the owning Sector (nil over open
// ground with no sector), so callers that need to know about e.g. that
// sector's procedural displacement detail (biome_displace.go) don't have to
// re-run the same chunk/sector search.
func floorHeightNormalUnderLevel(level *world.Level, wx, wz float32) (float32, mgl32.Vec3, *world.Sector) {
	if level == nil {
		return 0, mgl32.Vec3{0, 1, 0}, nil
	}
	xz := mgl32.Vec2{wx, wz}
	for ci := range level.Chunks {
		c := &level.Chunks[ci]
		if s := world.FindSector(c, xz); s != nil {
			h, n, _ := world.InterpolateSurfaceHeight(c, s, xz, true)
			return h, n, s
		}
	}
	return 0, mgl32.Vec3{0, 1, 0}, nil
}
