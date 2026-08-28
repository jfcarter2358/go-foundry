package world

// skirt.go — the vertical face between a stacked sector and what is under it.
//
// A portal's gap fill (RebuildGaps) closes the step between two sectors side by
// side: one floor is higher than its neighbour's, and the riser between them is
// geometry neither sector's own surfaces cover. A skirt is the same idea turned
// through ninety degrees, for sectors stacked one over the other.
//
// The gap it closes is unavoidable rather than a mistake. A storey's walls run
// from its floor to its ceiling, so a building's ground floor is walled from 0
// to its ceiling at 4.0 and the storey above from its floor at 4.2 to 8.2 — and
// the slab between them, 4.0 to 4.2, is walled by neither. Seen from outside,
// that is a slot straight through the building at every floor line. Closing it
// by making the two surfaces meet is exactly the coplanar case that cannot be
// drawn (see SlabThickness), so the band has to be filled with geometry of its
// own.
//
// How far down the skirt reaches is the author's choice, because the two useful
// answers are different shapes:
//
//   - SkirtToCeiling stops at the ceiling of the room below, filling just the
//     slab. This is a building: each storey shows its floor line as a band.
//   - SkirtToFloor carries on down to that room's FLOOR, so the whole height
//     between the two becomes one face. This is an overhang — a shelf of rock
//     jutting over ground that runs on underneath it, where the rock face
//     should reach the ground rather than stopping at a ceiling that is not
//     there.
//
// Skirts are derived geometry, rebuilt by the chunk-level pass alongside portal
// gap fill, and never serialized: only the mode and texture are saved.

import (
	"github.com/go-gl/mathgl/mgl32"
)

// How far down a sector's skirt reaches. Stored as a string so a level file
// reads as what it means and an unknown value degrades to "none" rather than to
// some arbitrary mode.
const (
	SkirtNone      = ""        // no skirt; the default, so existing levels are unchanged
	SkirtToCeiling = "ceiling" // down to the ceiling of the sector below
	SkirtToFloor   = "floor"   // down to the floor of the sector below
)

// skirtInset is how far inside its own outline a vertex is probed when looking
// for the sector underneath.
//
// The vertex itself sits exactly on the boundary, where point-in-polygon is a
// coin toss between the sector inside and whatever abuts it — and for a
// building's perimeter those are the room below and the open ground outside,
// which give completely different answers. Stepping a hair toward the sector's
// own middle asks the question that was meant: what is under THIS sector here.
const skirtInset = float32(0.01)

// RebuildSkirts regenerates every sector's skirt geometry. Called by
// RebuildGaps, which is the chunk-level pass for geometry that depends on more
// than one sector.
func (ch *Chunk) RebuildSkirts() {
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		s.Floor.SkirtGeometry.Vertices = nil
		if s.Floor.Skirt != SkirtToCeiling && s.Floor.Skirt != SkirtToFloor {
			continue
		}
		verts, indices := buildSkirtMesh(ch, s)
		if len(indices) == 0 {
			continue
		}
		tex := s.Floor.SkirtTexture
		if tex == "" {
			tex = s.Floor.Texture
		}
		_ = s.Floor.SkirtGeometry.Load(verts, indices, tex, mgl32.Vec3{})
	}
}

// buildSkirtMesh emits one quad per outline edge, spanning from the sector's
// own floor down to whatever its mode says it should meet.
func buildSkirtMesh(ch *Chunk, s *Sector) ([][]float32, []int) {
	// The mode is checked here as well as in RebuildSkirts, so the function
	// answers correctly on its own rather than only when reached the one way.
	if s.Floor.Skirt != SkirtToCeiling && s.Floor.Skirt != SkirtToFloor {
		return nil, nil
	}
	n := len(s.PointIndices)
	if n < 3 {
		return nil, nil
	}
	pts := SectorPoints(ch, s)
	centre := polygonCentroid(pts)

	var verts [][]float32
	var indices []int
	for ei := 0; ei < n; ei++ {
		a, b := pts[ei], pts[(ei+1)%n]
		segLen := b.Sub(a).Len()
		if segLen < 1e-6 {
			continue
		}
		baseA, okA := skirtBaseAt(ch, s, a, centre)
		baseB, okB := skirtBaseAt(ch, s, b, centre)
		if !okA || !okB {
			continue // nothing under this edge to reach down to
		}
		topA := s.Floor.VertexHeight(ei)
		topB := s.Floor.VertexHeight((ei + 1) % n)
		// Nothing to fill where the surface below is already at or above the
		// floor — an author can leave a skirt on for a whole sector and have it
		// appear only along the edges that actually overhang something.
		if baseA >= topA-1e-4 && baseB >= topB-1e-4 {
			continue
		}
		q, qi := buildGapQuad(a, b, baseA, baseB, topA, topB, segLen)
		base := len(verts)
		verts = append(verts, q...)
		for _, i := range qi {
			indices = append(indices, base+i)
		}
	}
	return verts, indices
}

// skirtBaseAt resolves the height a skirt should reach down to at one of the
// sector's vertices, and whether there is anything there to reach.
func skirtBaseAt(ch *Chunk, s *Sector, vertex, centre mgl32.Vec2) (float32, bool) {
	probe := vertex
	if d := centre.Sub(vertex); d.Len() > skirtInset {
		probe = vertex.Add(d.Normalize().Mul(skirtInset))
	}

	below := highestSectorBelow(ch, s, probe)
	if below == nil {
		return 0, false
	}
	if s.Floor.Skirt == SkirtToFloor {
		h, _, _ := InterpolateSurfaceHeight(ch, below, probe, true)
		return h, true
	}
	// SkirtToCeiling. A sector with no ceiling has no surface for the skirt to
	// meet, so nothing is drawn — rather than falling through to its floor,
	// which would drop a wall across an open shaft the author wanted open.
	if !below.Ceiling.Enabled {
		return 0, false
	}
	h, _, _ := InterpolateSurfaceHeight(ch, below, probe, false)
	return h, true
}

// highestSectorBelow returns the sector under xz whose floor is nearest below
// s's, or nil. Nearest so that a skirt meets the storey directly underneath
// rather than reaching past it to the basement.
func highestSectorBelow(ch *Chunk, s *Sector, xz mgl32.Vec2) *Sector {
	var best *Sector
	var bestY float32
	for i := range ch.Sectors {
		cand := &ch.Sectors[i]
		if cand == s || cand.Floor.Height >= s.Floor.Height-1e-4 {
			continue
		}
		if !PointInSector(ch, cand, xz) {
			continue
		}
		if best == nil || cand.Floor.Height > bestY {
			best, bestY = cand, cand.Floor.Height
		}
	}
	return best
}

// polygonCentroid is the average vertex — an interior point for the convex
// outlines sectors are built from, which is all skirtBaseAt needs it to be.
func polygonCentroid(pts []mgl32.Vec2) mgl32.Vec2 {
	if len(pts) == 0 {
		return mgl32.Vec2{}
	}
	var c mgl32.Vec2
	for _, p := range pts {
		c = c.Add(p)
	}
	return c.Mul(1 / float32(len(pts)))
}

// SkirtDrawable reports whether a sector has skirt geometry to draw, so render
// passes can skip the ones that do not without reaching into the mesh.
func SkirtDrawable(s *Sector) bool {
	return len(s.Floor.SkirtGeometry.Vertices) > 0
}
