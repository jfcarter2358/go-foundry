package world

// opening.go — holes in a sector's floor or ceiling.
//
// An edge portal connects two sectors side by side. An opening is its vertical
// counterpart: a polygon cut out of a horizontal surface, connecting the sector
// to whatever is directly above or below it. A stairwell, a hatch, a hole in a
// mezzanine, the shaft of a lift.
//
// Layers made it possible for one chunk to hold sectors stacked at the same XZ
// (layer.go); openings are how a body gets between them without leaving the
// chunk. The two are independent — an opening cares only about geometry, and
// works just as well between two sectors an author never filed on separate
// layers — but they are what make a multi-storey chunk actually playable rather
// than merely drawable.
//
// An opening carries no reference to the sector on its far side. Unlike a biome
// sector's DrapeSector, which records a choice that could not be recovered,
// "what is under this hole" is a well-defined geometric question with one
// answer: the nearest surface below it. Deriving it (OpeningTarget) means there
// is no reference to keep valid when the level changes underneath it.
//
// Three systems have to agree about an opening, and each reads it directly:
//
//   - geometry: the surface mesh is triangulated with the hole cut out, so you
//     can see through it (sector_geom.go).
//   - collision: the surface stops clamping bodies inside it, so you can fall
//     or climb through it (collision.go's sectorFloorHeightAt / ...CeilingAt).
//   - culling: the portal BFS walks through it, so the sector on the far side
//     is not culled away while you are looking down the hole (culling.go).

import (
	"foundry/internal/geom"
	"strconv"

	"github.com/go-gl/mathgl/mgl32"
)

// Opening is a polygonal hole in a Floor or Ceiling.
//
// Points are chunk-local and wound CCW seen from above, the same convention as
// a sector's own outline, so an opening moves with its chunk through
// Chunk.ModelMatrix like everything else and needs no handling of its own.
//
// The polygon is expected to lie inside its sector's outline. Nothing enforces
// that — a hole that spills past the sector's edge simply cuts nothing where
// there was no surface — but the editor keeps new ones inside.
type Opening struct {
	ID     string
	Points []mgl32.Vec2

	// Link is the ID of the opening on the other face of the same slab — the
	// ceiling below a floor hole, or the floor above a ceiling one.
	//
	// A hole between two storeys is one hole, but the slab it goes through has
	// two faces and each is a surface of a different sector. Cutting only the
	// one you drew on leaves the other still covering it: opaque geometry is
	// drawn with face culling off, so the room below's ceiling fills the
	// stairwell when seen from above just as solidly as from below.
	//
	// Stored rather than derived, unlike OpeningTarget, because it records a
	// decision and not a fact: these two holes were cut as one and are to be
	// dragged and deleted as one. Deriving the partner from geometry would work
	// right up until an author wanted a hole in only one of the two faces — a
	// ceiling opened onto a shaft whose floor above stays solid — and there
	// would be no way left to say so. Breaking the pair is deleting one twin.
	//
	// Empty means standalone: a skylight with no floor above it, one half of a
	// pair the author split, or a hole cut before pairing existed.
	Link string
}

// Centroid is the opening's average vertex, chunk-local. Used as the probe
// point for "what is on the other side" — the polygon is convex in practice and
// small relative to the sectors it connects, so any interior point would do.
func (op *Opening) Centroid() mgl32.Vec2 {
	if len(op.Points) == 0 {
		return mgl32.Vec2{}
	}
	var c mgl32.Vec2
	for _, p := range op.Points {
		c = c.Add(p)
	}
	return c.Mul(1 / float32(len(op.Points)))
}

// Contains reports whether chunk-local xz falls inside the opening.
func (op *Opening) Contains(xz mgl32.Vec2) bool {
	return len(op.Points) >= 3 && geom.PointInPolygon(xz, op.Points)
}

// SurfaceIsOpenAt reports whether any of the openings covers xz — that is,
// whether the surface has a hole where the point is, and so nothing there to
// stand on, be stopped by, or draw.
//
// A free function over the slice rather than a method on Floor and Ceiling
// again, because the two carry the identical field and every caller already
// has the one it means in hand.
func SurfaceIsOpenAt(openings []Opening, xz mgl32.Vec2) bool {
	for i := range openings {
		if openings[i].Contains(xz) {
			return true
		}
	}
	return false
}

// CloneOpenings deep-copies an opening list, so a cloned chunk's holes do not
// share point storage with the chunk it came from.
func CloneOpenings(openings []Opening) []Opening {
	if len(openings) == 0 {
		return nil
	}
	out := make([]Opening, len(openings))
	for i := range openings {
		out[i] = Opening{
			ID:     openings[i].ID,
			Link:   openings[i].Link,
			Points: append([]mgl32.Vec2(nil), openings[i].Points...),
		}
	}
	return out
}

// OpeningTarget returns the sector an opening leads to: the nearest surface
// below a floor opening, or above a ceiling one. nil when the hole opens onto
// nothing, which is a hole in the sky or over a void — legal, just not a
// connection.
//
// "Nearest" is measured from the surface the hole is cut in, and the sector the
// hole belongs to is excluded, so a sector whose floor and ceiling are both
// pierced does not answer with itself.
func OpeningTarget(ch *Chunk, s *Sector, op *Opening, isFloor bool) *Sector {
	if ch == nil || s == nil || op == nil {
		return nil
	}
	xz := op.Centroid()
	from := s.Floor.Height
	if !isFloor {
		from = s.Ceiling.Height
	}

	var best *Sector
	var bestY float32
	for i := range ch.Sectors {
		cand := &ch.Sectors[i]
		if cand == s || !PointInSector(ch, cand, xz) {
			continue
		}
		// A floor opening drops onto the candidate's FLOOR; a ceiling opening
		// rises into the candidate's floor too — in both cases the surface a
		// body ends up standing on is the far sector's floor. What differs is
		// which direction from the hole counts.
		y, _, _ := InterpolateSurfaceHeight(ch, cand, xz, true)
		if isFloor {
			if y > from {
				continue // above the hole: not something you fall onto
			}
			if best == nil || y > bestY { // the highest floor under the hole
				best, bestY = cand, y
			}
			continue
		}
		if y < from {
			continue // below the hole: not something you climb into
		}
		if best == nil || y < bestY { // the lowest floor above the hole
			best, bestY = cand, y
		}
	}
	return best
}

// SlabFace finds the surface a hole still has to get through after the one it
// was cut in: the ceiling of the sector below a floor hole, or the floor of the
// sector above a ceiling one.
//
// The rule is "does that surface lie between the hole and the room beyond it".
// For a floor hole at y=4.2 over a room whose ceiling is at 4.0 and floor at 0,
// the ceiling does — so looking down the hole you would see it rather than the
// room, and it has to be cut as well.
//
// Note this does not care how thick the gap is. A hole at y=20 over a room
// ceilinged at 4 still needs that ceiling cut, because it is still the thing
// filling the shaft; the two surfaces being a hand's breadth apart or not
// changes nothing about what you can see through. What it does exclude is a
// sector with no facing surface at all — an open-air room below, a sector whose
// ceiling sits at or under its own floor.
//
// Returns the far sector and whether its facing surface should be cut.
func SlabFace(ch *Chunk, s *Sector, op *Opening, isFloor bool) (*Sector, bool) {
	far := OpeningTarget(ch, s, op, isFloor)
	if far == nil {
		return nil, false
	}
	xz := op.Centroid()
	near := s.Floor.Height
	if !isFloor {
		near = s.Ceiling.Height
	}

	if isFloor {
		// The far sector's ceiling is the underside of our slab.
		if !far.Ceiling.Enabled {
			return far, false
		}
		ceil, _, _ := InterpolateSurfaceHeight(ch, far, xz, false)
		floor, _, _ := InterpolateSurfaceHeight(ch, far, xz, true)
		return far, ceil <= near+coplanarEpsilon && ceil > floor
	}
	// Mirror: the far sector's floor is the top of our slab.
	if !far.Floor.Enabled {
		return far, false
	}
	floor, _, _ := InterpolateSurfaceHeight(ch, far, xz, true)
	return far, floor >= near-coplanarEpsilon
}

// OpeningPartner resolves an opening's other face — the sector and the opening
// on it that Link names. ok is false for a standalone hole, or one whose
// partner has since been removed.
func OpeningPartner(ch *Chunk, s *Sector, op *Opening, isFloor bool) (*Sector, *Opening, bool) {
	if ch == nil || op == nil || op.Link == "" {
		return nil, nil, false
	}
	for i := range ch.Sectors {
		cand := &ch.Sectors[i]
		if cand == s {
			continue
		}
		// A floor hole's partner is on a ceiling, and the other way round.
		list := cand.Ceiling.Openings
		if !isFloor {
			list = cand.Floor.Openings
		}
		for j := range list {
			if list[j].ID == op.Link {
				return cand, &list[j], true
			}
		}
	}
	return nil, nil, false
}

// NextOpeningID mints an ID for a new opening on a surface, unique among the
// ones already there. Openings are addressed only within their own surface, so
// unlike a sector ID this needs no level-wide uniqueness.
func NextOpeningID(openings []Opening, prefix string) string {
	used := make(map[string]bool, len(openings))
	for i := range openings {
		used[openings[i].ID] = true
	}
	for n := 1; ; n++ {
		id := prefix + "_" + strconv.Itoa(n)
		if !used[id] {
			return id
		}
	}
}

// ---------- coplanar stacked surfaces ----------
//
// Two storeys built directly on top of each other put a ceiling and a floor at
// the same height over the same ground. Both get drawn — opaque world geometry
// renders with face culling off — so they z-fight, and the flicker is not a
// depth-precision problem that a bias would settle: at every pixel the ceiling
// is the right answer seen from below and the floor is the right answer seen
// from above, and one draw can only be one of them.
//
// The fix is to stop them being coplanar, which is what SlabThickness is for.
// AddLayer leaves that gap now, but a level built before it did — or one whose
// heights were typed in by hand — can still have the pair, so the editor finds
// them and offers to separate them.

// CoplanarPair is a ceiling and the floor sitting exactly on it: Lower's
// ceiling and Upper's floor, at the same height, over ground they both cover.
type CoplanarPair struct {
	Lower  *Sector // its Ceiling is the offending surface
	Upper  *Sector // its Floor is
	Height float32
}

// coplanarEpsilon is how close two surfaces have to be to fight. Well under any
// slab an author would build deliberately, and well over the rounding in a
// height typed as a decimal.
const coplanarEpsilon = float32(1e-3)

// CoplanarStackedSurfaces finds the ceiling/floor pairs in ch that are drawn at
// the same height over the same ground.
//
// Overlap is judged by each polygon's centroid falling inside the other, not by
// their bounding boxes: two rooms side by side on different storeys can easily
// have touching boxes without sharing any ground, and reporting those would
// make the warning noise rather than information.
func CoplanarStackedSurfaces(ch *Chunk) []CoplanarPair {
	if ch == nil {
		return nil
	}
	var out []CoplanarPair
	for i := range ch.Sectors {
		lower := &ch.Sectors[i]
		if !lower.Ceiling.Enabled {
			continue
		}
		for j := range ch.Sectors {
			if i == j {
				continue
			}
			upper := &ch.Sectors[j]
			if !upper.Floor.Enabled {
				continue
			}
			d := lower.Ceiling.Height - upper.Floor.Height
			if d > coplanarEpsilon || d < -coplanarEpsilon {
				continue
			}
			if !sectorsShareGround(ch, lower, upper) {
				continue
			}
			out = append(out, CoplanarPair{Lower: lower, Upper: upper, Height: lower.Ceiling.Height})
		}
	}
	return out
}

// sectorsShareGround reports whether two sectors cover any of the same XZ,
// approximated by either one's centroid falling inside the other.
func sectorsShareGround(ch *Chunk, a, b *Sector) bool {
	ca := a.MinBound.Add(a.MaxBound).Mul(0.5)
	cb := b.MinBound.Add(b.MaxBound).Mul(0.5)
	return PointInSector(ch, b, ca) || PointInSector(ch, a, cb)
}

// SeparateCoplanarSurfaces gives every coplanar pair in ch a slab to be either
// side of, and returns how many it moved.
//
// The CEILING is what moves, always. A floor is walked on and built against —
// moving one changes where everything in the room above stands, and would drag
// its own ceiling's spacing with it. Dropping the ceiling underneath by a slab
// changes nothing but the height of a room's lid.
//
// Callers must reload the sectors this touches; it only edits heights.
func SeparateCoplanarSurfaces(ch *Chunk) int {
	pairs := CoplanarStackedSurfaces(ch)
	moved := map[*Sector]bool{}
	for _, p := range pairs {
		if moved[p.Lower] {
			continue // one ceiling can be under several floors
		}
		p.Lower.Ceiling.Height -= SlabThickness
		moved[p.Lower] = true
	}
	return len(moved)
}

// PairOpeningsThroughSlabs gives every unpaired opening in ch the far-side hole
// it needs, and returns how many holes it had to cut.
//
// This is repair, for holes made before an opening cut both faces of its slab —
// they look right from the side they were drawn on and are covered from the
// other. Nothing calls it automatically: it edits geometry, and a level should
// not quietly change shape because it was loaded by a newer build.
//
// An unpaired hole whose far surface ALREADY has a hole over the same ground is
// linked to that one rather than given a second. Two authors' halves of the
// same shaft, cut separately, are one shaft.
//
// Callers must reload the sectors this touches.
func PairOpeningsThroughSlabs(ch *Chunk) int {
	if ch == nil {
		return 0
	}
	cut := 0
	// Collected first, applied after: appending to a far sector's list while
	// ranging over a near one's would invalidate the pointers in flight, and a
	// sector can be both.
	type job struct {
		near    *Sector
		nearIdx int
		isFloor bool
		far     *Sector
	}
	var jobs []job
	for i := range ch.Sectors {
		s := &ch.Sectors[i]
		for _, isFloor := range []bool{true, false} {
			list := s.Floor.Openings
			if !isFloor {
				list = s.Ceiling.Openings
			}
			for oi := range list {
				if list[oi].Link != "" {
					continue
				}
				far, ok := SlabFace(ch, s, &list[oi], isFloor)
				if !ok {
					continue
				}
				jobs = append(jobs, job{s, oi, isFloor, far})
			}
		}
	}

	for _, j := range jobs {
		nearList := &j.near.Floor.Openings
		farList := &j.far.Ceiling.Openings
		if !j.isFloor {
			nearList = &j.near.Ceiling.Openings
			farList = &j.far.Floor.Openings
		}
		if j.nearIdx >= len(*nearList) {
			continue
		}
		op := &(*nearList)[j.nearIdx]
		if op.Link != "" {
			continue // paired by an earlier job from the other side
		}

		// Adopt an existing hole over the same ground before cutting a new one.
		if existing := openingCovering(*farList, op.Centroid()); existing != nil && existing.Link == "" {
			op.Link, existing.Link = existing.ID, op.ID
			continue
		}

		twin := Opening{
			ID:     NextOpeningID(*farList, "opening"),
			Points: append([]mgl32.Vec2(nil), op.Points...),
			Link:   op.ID,
		}
		op.Link = twin.ID
		*farList = append(*farList, twin)
		cut++
	}
	return cut
}

// UnpairedOpenings counts the openings in ch that still need a far-side hole —
// what the editor shows before offering to cut them.
func UnpairedOpenings(ch *Chunk) int {
	if ch == nil {
		return 0
	}
	n := 0
	for i := range ch.Sectors {
		s := &ch.Sectors[i]
		for _, isFloor := range []bool{true, false} {
			list := s.Floor.Openings
			if !isFloor {
				list = s.Ceiling.Openings
			}
			for oi := range list {
				if list[oi].Link != "" {
					continue
				}
				if _, ok := SlabFace(ch, s, &list[oi], isFloor); ok {
					n++
				}
			}
		}
	}
	return n
}

// openingCovering returns the opening whose polygon contains xz, or nil.
func openingCovering(openings []Opening, xz mgl32.Vec2) *Opening {
	for i := range openings {
		if openings[i].Contains(xz) {
			return &openings[i]
		}
	}
	return nil
}
