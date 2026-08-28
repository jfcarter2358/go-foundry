package world

// drape.go — which structural surface a painted biome polygon sits on.
//
// It lives here, below the biome package, because two very different callers
// need the same answer and must never disagree about it: the biome renderer
// (what height to bake the paint at) and level_resync.go (whether an edit to a
// floor invalidates that bake). Splitting the definition once produced a
// floating chunk that reshaped paint it was nowhere near while sampling that
// paint's height from somewhere else entirely.

import (
	"foundry/internal/geom"

	"github.com/go-gl/mathgl/mgl32"
)

// DrapeTarget resolves what a biome point at owner-local (wx,wz) is actually
// draped on: the chunk and sector whose floor it sits over, or (nil, nil) over
// open ground.
//
// This is THE definition of that question, and both things that ask it go
// through here — the height/normal sample below, and the warp gate in
// CarryBiomesWithPoints. They used to answer it separately and could disagree,
// which is how a chunk floating above painted ground came to reshape that
// paint while sampling its height from somewhere else entirely.
//
// The rule, in order:
//
//  1. **The owning chunk wins.** Paint drapes over the ground it was painted
//     on. A chunk hanging in the air above it is not that ground — that is
//     what made a floating platform lift a lakebed up to its own floor height
//     until the water read as deleted. It also matters the other way: paint on
//     a bridge stays on the bridge rather than dropping to the terrain below.
//
//  2. **Among overlapping candidates, the paint's own recorded surface
//     wins.** Two sectors stacked at the same XZ look identical from above,
//     which is the only view the paint was drawn in, so which one was meant
//     cannot be recovered by looking at geometry — it is recorded at paint
//     time instead (BiomeSector.DrapeSector) and honoured here. When the
//     bound sector is not itself a candidate at this point — paint spanning
//     off it onto a neighbouring surface — the candidate whose floor sits
//     NEAREST the bound one's is taken, which keeps a span on the level it
//     belongs to instead of dropping it to whatever is lowest.
//
//  3. **Failing that, the LOWEST floor wins.** The fallback for unbound paint:
//     written before the binding existed, or bound to a sector since deleted.
//     Sectors only compete when they genuinely overlap in XZ — a point inside
//     exactly one sector resolves to it regardless — so this only decides
//     stacked cases, and there the lower surface is usually the ground while
//     the upper one is a structure above it. Replaces a first-match scan whose
//     answer depended on level-file order.
//
// drape is the asking sector's own DrapeSector ("" when unbound). (wx,wz) is
// in owner's local space; a candidate that is not owner has it
// carried into that candidate's own frame first, since every chunk stores its
// geometry chunk-local and two chunks authored around their own origins would
// otherwise look like they occupy the same place. Frame-owned chunks are
// searched like any other — a vehicle interior's own paint has to be able to
// find its own floor.
func DrapeTarget(level *Level, owner *Chunk, drape string, wx, wz float32) (*Chunk, *Sector) {
	return DrapeTargetWith(level, owner, drape, mgl32.Vec2{wx, wz}, PointInSector)
}

// DrapeTargetWith is DrapeTarget with the containment test injected, so the
// same resolution can be run against geometry as it was BEFORE an edit.
//
// CarryBiomesWithPoints needs that: its caller has already written the moved
// points, but the paint it is deciding about is still where the pre-edit floor
// put it. Resolving against live geometry would ask "which sector contains
// this vertex now" when the question is "which sector was it draped on" — and
// for a whole-sector move the answer is "none", which silently stopped the
// paint following its own floor.
func DrapeTargetWith(level *Level, owner *Chunk, drape string, xz mgl32.Vec2,
	contains func(*Chunk, *Sector, mgl32.Vec2) bool) (*Chunk, *Sector) {

	// The recorded surface's own height, when it still exists, is the
	// reference every candidate below is judged against.
	refH, haveRef := drapeReferenceHeight(level, drape)

	// The owning chunk, in its own frame: no conversion, and it outranks
	// anything else that happens to overlap. Deliberately before the nil-level
	// check — the owning chunk is enough to answer for paint over its own
	// floors, which is the whole answer outside the editor and in any test
	// that has not installed a level (the same fallback ForEachBiomeSector
	// makes, for the same reason).
	if owner != nil {
		if s := bestSectorAt(owner, xz, contains, refH, haveRef); s != nil {
			return owner, s
		}
	}
	if level == nil {
		return nil, nil
	}

	// Paint spilling past its own chunk's floors. Candidates are compared by
	// WORLD floor height, since each one's own Floor.Height is in its own
	// frame and those are not comparable numbers.
	var bestChunk *Chunk
	var bestSector *Sector
	var bestWorldY float32
	for ci := range level.Chunks {
		c := &level.Chunks[ci]
		if c == owner {
			continue // already tested, above
		}
		cxz := DrapePointIn(owner, c, xz)
		s := bestSectorAt(c, cxz, contains, refH, haveRef)
		if s == nil {
			continue
		}
		y := c.LocalToWorld(mgl32.Vec3{cxz.X(), s.Floor.Height, cxz.Y()}).Y()
		if bestSector == nil || betterCandidate(y, bestWorldY, refH, haveRef) {
			bestChunk, bestSector, bestWorldY = c, s, y
		}
	}
	return bestChunk, bestSector
}

// drapeReferenceHeight is the WORLD Y of the floor the paint is bound to, or
// (0,false) when it is unbound or its sector has since been deleted. World Y
// so that candidates in other chunks are comparable against it.
func drapeReferenceHeight(level *Level, drape string) (float32, bool) {
	if drape == "" || level == nil {
		return 0, false
	}
	for ci := range level.Chunks {
		c := &level.Chunks[ci]
		for si := range c.Sectors {
			s := &c.Sectors[si]
			if s.ID != drape {
				continue
			}
			mid := s.MinBound.Add(s.MaxBound).Mul(0.5)
			return c.LocalToWorld(mgl32.Vec3{mid.X(), s.Floor.Height, mid.Y()}).Y(), true
		}
	}
	return 0, false
}

// betterCandidate reports whether floor height a beats b: nearest the bound
// surface when there is one, else the lower of the two. Both are world Y.
func betterCandidate(a, b, refH float32, haveRef bool) bool {
	if haveRef {
		return geom.Abs32(a-refH) < geom.Abs32(b-refH)
	}
	return a < b
}

// bestSectorAt returns the sector of ch containing chunk-local xz that the
// paint is draped on: the one it is BOUND to when that is among them, else the
// best by betterCandidate. nil when no sector contains it.
//
// The comparison only ever runs for sectors that genuinely overlap in XZ;
// everywhere else the single containing sector is returned unchanged, which is
// every point in a level with no stacked geometry.
func bestSectorAt(ch *Chunk, xz mgl32.Vec2,
	contains func(*Chunk, *Sector, mgl32.Vec2) bool,
	refH float32, haveRef bool) *Sector {

	var best *Sector
	var bestY float32
	for i := range ch.Sectors {
		s := &ch.Sectors[i]
		if !contains(ch, s, xz) {
			continue
		}
		y := ch.LocalToWorld(mgl32.Vec3{xz.X(), s.Floor.Height, xz.Y()}).Y()
		if haveRef && y == refH {
			// The bound surface itself (or one flush with it): nothing can be
			// a better answer, so stop looking.
			return s
		}
		if best == nil || betterCandidate(y, bestY, refH, haveRef) {
			best, bestY = s, y
		}
	}
	return best
}

// DrapePointIn carries an XZ point from `from`'s local space into `to`'s.
// Identity when the two chunks share a placement, which is the common case and
// the only one that existed before chunk transforms were live.
func DrapePointIn(from, to *Chunk, xz mgl32.Vec2) mgl32.Vec2 {
	if from == nil || to == nil || from.ModelMatrix() == to.ModelMatrix() {
		return xz
	}
	w := from.LocalToWorld(mgl32.Vec3{xz.X(), 0, xz.Y()})
	l := to.WorldToLocal(w)
	return mgl32.Vec2{l.X(), l.Z()}
}
