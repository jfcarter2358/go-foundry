package world

import (
	"foundry/internal/geom"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// SpatialHash provides O(1) lookup for chunk/sector pairs by XZ position.
// Each cell in the grid stores references to all (chunk, sector) pairs whose
// XZ AABB overlaps that cell.
type SpatialHash struct {
	CellSize float32
	InvCell  float32 // 1.0 / CellSize
	Cells    map[[2]int][]ChunkSectorRef
}

// ChunkSectorRef is a (chunkIndex, sectorIndex) pair stored in the spatial hash.
type ChunkSectorRef struct {
	ChunkIdx  int
	SectorIdx int
}

// SpatialCellSize is the hash's cell size in world units. One constant so that
// every rebuild buckets into the same grid — a hash built at one size and
// queried against another silently finds nothing.
const SpatialCellSize = float32(8.0)

// RebuildSpatialHash re-buckets lvl's chunks into a fresh hash.
//
// MUST be called whenever a chunk's Origin, Rotation or Scale changes. The hash
// keys are WORLD cells derived from ch.ModelMatrix (see BuildSpatialHash), so
// moving a chunk invalidates every cell its sectors were filed under: a query
// at the chunk's new position finds no refs, and one at its old position finds
// stale refs that then fail the exact test (which uses the *new* matrix). The
// chunk simply stops existing as far as collision, entity location and the
// script location verbs are concerned, until something unrelated happens to
// rebuild. This could not happen before chunk transforms were live — Origin was
// consumed at load and never changed again — which is why the editor's
// transform edits had no invalidation to begin with.
//
// Cheap enough to call on every frame of a drag: ~40us on a 56-sector level.
func RebuildSpatialHash(lvl *Level) {
	if lvl == nil {
		return
	}
	lvl.Spatial = BuildSpatialHash(lvl.Chunks, SpatialCellSize)
}

// BuildSpatialHash creates a spatial hash from all chunks and their sectors.
//
// Each sector is bucketed by its WORLD-space footprint — s.WorldAABB(c) rather
// than the chunk-local s.MinBound/MaxBound — so a chunk that has been moved,
// rotated or scaled still lands in the cells a world-space query will actually
// look in. Bucketing is necessarily conservative for a rotated sector (its
// axis-aligned world box is bigger than the sector itself); FindChunkAndSectorSH
// resolves each candidate exactly, in the sector's own local space.
//
// Because the keys depend on each chunk's live transform, the result is only
// valid until one of those transforms changes — see RebuildSpatialHash.
func BuildSpatialHash(chunks []Chunk, cellSize float32) *SpatialHash {
	if cellSize <= 0 {
		cellSize = SpatialCellSize
	}
	sh := &SpatialHash{
		CellSize: cellSize,
		InvCell:  1.0 / cellSize,
		Cells:    make(map[[2]int][]ChunkSectorRef),
	}
	for ci := range chunks {
		c := &chunks[ci]
		// A frame-owned chunk's geometry is in its frame's coordinates, not the
		// world's. Indexing it here would answer world-space queries with
		// sectors from inside a moving ship — see Chunk.FrameOwner.
		if c.InFrame() {
			continue
		}
		for si := range c.Sectors {
			s := &c.Sectors[si]
			wmin, wmax := s.WorldAABB(c)
			minCellX := int(geom.Floor32(wmin.X() * sh.InvCell))
			minCellZ := int(geom.Floor32(wmin.Z() * sh.InvCell))
			maxCellX := int(geom.Floor32(wmax.X() * sh.InvCell))
			maxCellZ := int(geom.Floor32(wmax.Z() * sh.InvCell))
			ref := ChunkSectorRef{ChunkIdx: ci, SectorIdx: si}
			for cx := minCellX; cx <= maxCellX; cx++ {
				for cz := minCellZ; cz <= maxCellZ; cz++ {
					key := [2]int{cx, cz}
					sh.Cells[key] = append(sh.Cells[key], ref)
				}
			}
		}
	}
	return sh
}

// FindChunkAndSectorSH looks up the spatial hash for the cell containing pos,
// then runs the authoritative point-in-polygon test on candidates — each in
// its own chunk's local space, since a bucketed candidate may be placed by a
// different transform than the next one in the same cell.
//
// Candidates are ranked rather than taken first-come (see sectorFit), so two
// sectors stacked at the same XZ — an overhang over the ground it shades, a
// mezzanine over a hall — resolve to the one the position is actually standing
// on. Ranking runs across chunks as well as within one, which means the fits
// have to be comparable between them: each candidate's floor is carried out to
// world space before it is weighed against the next.
func FindChunkAndSectorSH(sh *SpatialHash, chunks []Chunk, pos mgl32.Vec3) (*Chunk, *Sector) {
	if sh == nil {
		return FindChunkAndSector(chunks, pos)
	}
	cx := int(geom.Floor32(pos.X() * sh.InvCell))
	cz := int(geom.Floor32(pos.Z() * sh.InvCell))
	refs := sh.Cells[[2]int{cx, cz}]
	if len(refs) == 0 {
		return nil, nil
	}

	// Cache the last chunk's local point rather than recomputing a matrix
	// inverse once per sector when several of a chunk's sectors share a cell.
	lastChunkIdx := -1
	var local mgl32.Vec3
	var best sectorFit
	var bestChunk *Chunk
	var bestSector *Sector
	for _, ref := range refs {
		c := &chunks[ref.ChunkIdx]
		if ref.ChunkIdx != lastChunkIdx {
			local = c.WorldToLocal(pos)
			lastChunkIdx = ref.ChunkIdx
		}
		// Y-bounds check enables vertically stacked chunks (rooms-over-rooms).
		// Chunks that overlap in XZ but occupy different Y ranges are
		// disambiguated by the entity's vertical position.
		if !chunkContainsY(c, local.Y()) {
			continue
		}
		s := &c.Sectors[ref.SectorIdx]
		xz := mgl32.Vec2{local.X(), local.Z()}
		if !PointInSector(c, s, xz) {
			continue
		}
		fit := sectorFitAt(c, s, xz, local.Y()).toWorld(c, xz)
		if bestSector == nil || fit.beats(best) {
			best, bestChunk, bestSector = fit, c, s
		}
	}
	return bestChunk, bestSector
}

// ---------- vertical sector choice ----------
//
// Which of several sectors covering the same XZ a position belongs to. This is
// what makes overlapping geometry inside one chunk work at all: before it,
// every lookup here took the first polygon that contained the point, so a
// second sector stacked over the first was unreachable and authors had to split
// the two into separate chunks to be told apart by chunkContainsY.
//
// The rule is about FLOORS, not vertical spans, and that is deliberate. A
// sector's top is not a reliable bound — an outdoor sector has no ceiling at
// all, so requiring the position to sit under one would drop a jumping body out
// of the world at the top of its arc. Floors are always real:
//
//   - A candidate whose floor is at or below the position is one the position
//     could be standing on. Those beat every candidate whose floor is above it.
//   - Among those, the highest floor wins: the surface nearest under the body
//     is the one it is standing on, so walking onto an overhang picks the
//     overhang and walking off it picks the ground again, with no state to
//     maintain and nothing to author.
//   - When no candidate is below the position — a body under every floor in the
//     stack, mid-fall through a hole — the lowest floor wins, so the answer is
//     the sector it is falling toward.
//
// The one case this cannot get right: an overhang whose floor is less than a
// body's half-height above the floor beneath it. Positions here are body
// centres, not feet (see ResolveFloorCollision), so a floor that close reads as
// "below the body" while the body is really standing on the one under it. Such
// an overhang is too low to walk under, so a level containing one is already
// broken in a more visible way.

// sectorFit is how well one candidate sector matches a query height. Compare
// with beats; build with sectorFitAt.
type sectorFit struct {
	floorY float32 // the candidate's floor directly under the query point
	below  bool    // that floor is at or under the query height
}

// sectorFitAt scores s against a CHUNK-LOCAL query point and height.
//
// floorY comes back chunk-local too, so a caller ranking candidates from more
// than one chunk must carry it out to world space first — see toWorld.
func sectorFitAt(ch *Chunk, s *Sector, xz mgl32.Vec2, y float32) sectorFit {
	floorY, _, _ := InterpolateSurfaceHeight(ch, s, xz, true)
	return sectorFit{
		floorY: floorY,
		// The same tolerance chunkContainsY needs, for the same reason: a body
		// at rest sits exactly on the floor it is standing on, and which side of
		// the comparison it lands on is decided by float rounding.
		below: floorY <= y+chunkYTolerance(floorY),
	}
}

// toWorld carries a fit's floor out of ch's local space, making it comparable
// against a fit from a differently-placed chunk. `below` was already decided in
// the chunk's own space, where the query height and the floor agree.
func (f sectorFit) toWorld(ch *Chunk, xz mgl32.Vec2) sectorFit {
	f.floorY = ch.LocalToWorld(mgl32.Vec3{xz.X(), f.floorY, xz.Y()}).Y()
	return f
}

// beats reports whether f is a better match for the query height than other.
func (f sectorFit) beats(other sectorFit) bool {
	if f.below != other.below {
		return f.below
	}
	if f.below {
		return f.floorY > other.floorY // nearest surface under the body
	}
	return f.floorY < other.floorY // nearest surface it is falling toward
}

// sectorAtHeight returns the sector in ch covering chunk-local xz that best
// fits chunk-local height y, or nil when the point is in none of them.
//
// Not to be confused with drape.go's bestSectorAt, which answers a different
// question with a different rule: which surface a painted biome polygon belongs
// to, decided by its recorded binding rather than by anything's position.
func sectorAtHeight(ch *Chunk, xz mgl32.Vec2, y float32) *Sector {
	var best sectorFit
	var found *Sector
	for i := range ch.Sectors {
		s := &ch.Sectors[i]
		if !PointInSector(ch, s, xz) {
			continue
		}
		fit := sectorFitAt(ch, s, xz, y)
		if found == nil || fit.beats(best) {
			best, found = fit, s
		}
	}
	return found
}

// ---------- linear lookups ----------
//
// The spatial hash above is the fast path; these are the exhaustive scans it
// falls back to, and the ones the editor uses when the hash has not been built.
// They live here beside it rather than in collision.go: locating the sector
// under a point is a world query, and plenty of callers that need one are not
// resolving a collision.

// FindChunk returns the chunk whose 3D AABB contains pos, or nil (O(N) scan).
// Prefer FindChunkAndSector when you need both, as it uses PIP for correctness
// when multiple chunk AABBs overlap.
func FindChunk(chunks []Chunk, pos mgl32.Vec3) *Chunk {
	for i := range chunks {
		c := &chunks[i]
		if c.InFrame() {
			continue // not in world space; see Chunk.FrameOwner
		}
		// pos carried into this chunk's own local space, where MinBound/MaxBound
		// live — exact for a rotated chunk too, unlike testing against its
		// (necessarily looser) axis-aligned world bounds would be.
		local := c.WorldToLocal(pos)
		if local.X() >= c.MinBound.X() && local.X() <= c.MaxBound.X() &&
			chunkContainsY(c, local.Y()) &&
			local.Z() >= c.MinBound.Z() && local.Z() <= c.MaxBound.Z() {
			return c
		}
	}
	return nil
}

// FindSector returns the sector in chunk whose XZ polygon contains xz
// (ray-casting PIP).
//
// xz must be CHUNK-LOCAL. This is the one lookup in this file that does no
// conversion of its own — the others take a world position and carry it in
// via WorldToLocal first — so a caller holding a world position must convert
// before calling, and a caller sweeping several chunks must convert per
// candidate.
//
// It is also the one lookup with no height to resolve stacked sectors by, so
// where several cover xz it answers with the lowest floor. Callers that do have
// a height want FindSectorAt instead; this one is for the queries that are
// genuinely 2D, and the lowest-floor tiebreak at least makes it deterministic
// rather than dependent on slice order.
func FindSector(chunk *Chunk, xz mgl32.Vec2) *Sector {
	if chunk == nil {
		return nil
	}
	// -inf leaves no candidate "below" the query, which is exactly the branch of
	// sectorFit.beats that prefers the lowest floor.
	return sectorAtHeight(chunk, xz, float32(math.Inf(-1)))
}

// FindSectorAt is FindSector with a height to resolve stacked sectors by: the
// sector at chunk-local xz whose floor the given chunk-local y is standing on.
// See the vertical sector choice section above for the rule.
func FindSectorAt(chunk *Chunk, xz mgl32.Vec2, y float32) *Sector {
	if chunk == nil {
		return nil
	}
	return sectorAtHeight(chunk, xz, y)
}

// FindChunkAndSector scans all chunks and returns the (chunk, sector) pair
// whose sector best contains pos.  Unlike calling FindChunk+FindSector
// separately this is correct even when chunk AABBs overlap, because it uses the
// authoritative point-in-polygon test rather than the approximate AABB test —
// and, for sectors stacked at the same XZ, the vertical ranking above.
func FindChunkAndSector(chunks []Chunk, pos mgl32.Vec3) (*Chunk, *Sector) {
	var best sectorFit
	var bestChunk *Chunk
	var bestSector *Sector
	for i := range chunks {
		c := &chunks[i]
		if c.InFrame() {
			continue // not in world space; see Chunk.FrameOwner
		}
		// pos carried into this chunk's own local space, the same space
		// MinBound/MaxBound and SectorPoints are expressed in.
		local := c.WorldToLocal(pos)
		// Quick AABB reject (XZ + Y) before the more expensive PIP scan.
		// The Y check enables vertically stacked chunks (rooms-over-rooms).
		if local.X() < c.MinBound.X() || local.X() > c.MaxBound.X() ||
			!chunkContainsY(c, local.Y()) ||
			local.Z() < c.MinBound.Z() || local.Z() > c.MaxBound.Z() {
			continue
		}
		xz := mgl32.Vec2{local.X(), local.Z()}
		for j := range c.Sectors {
			s := &c.Sectors[j]
			if !PointInSector(c, s, xz) {
				continue
			}
			// World-space floors, so a candidate here is comparable against one
			// from the next chunk — see FindChunkAndSectorSH.
			fit := sectorFitAt(c, s, xz, local.Y()).toWorld(c, xz)
			if bestSector == nil || fit.beats(best) {
				best, bestChunk, bestSector = fit, c, s
			}
		}
	}
	return bestChunk, bestSector
}

// FindChunkAndSectorAmong locates pos among a named subset of chunks, in
// whatever coordinate system those chunks are authored in.
//
// This is how a frame is searched: the caller (Frame.ChunkAndSectorAt) has
// already carried pos out of the frame via Frame.FromWorld, so pos arrives
// here already expressed in the frame's own local space. Each chunk in idx
// can still carry its own placement within that frame — the same
// LocalPlacementMatrix ModelMatrix composes under the frame's Xform — so pos
// is carried one step further, into that chunk's own local space, before
// testing against its geometry.
//
// There is no spatial hash: a frame owns a handful of chunks — usually one —
// so the scan is shorter than the hash lookup it would replace, and building a
// hash per frame per structural change would cost more than it saved.
func FindChunkAndSectorAmong(chunks []Chunk, idx []int, pos mgl32.Vec3) (*Chunk, *Sector) {
	var best sectorFit
	var bestChunk *Chunk
	var bestSector *Sector
	for _, ci := range idx {
		if ci < 0 || ci >= len(chunks) {
			continue
		}
		c := &chunks[ci]
		local := pos
		lm := c.LocalPlacementMatrix()
		if lm != mgl32.Ident4() {
			r := lm.Inv().Mul4x1(pos.Vec4(1))
			local = mgl32.Vec3{r.X(), r.Y(), r.Z()}
		}
		if local.X() < c.MinBound.X() || local.X() > c.MaxBound.X() ||
			!chunkContainsY(c, local.Y()) ||
			local.Z() < c.MinBound.Z() || local.Z() > c.MaxBound.Z() {
			continue
		}
		xz := mgl32.Vec2{local.X(), local.Z()}
		for j := range c.Sectors {
			s := &c.Sectors[j]
			if !PointInSector(c, s, xz) {
				continue
			}
			// Ranked in the FRAME's space, not the world's: pos arrived here
			// already carried out of the frame, and the only transform between
			// these chunks is their placement within it.
			fit := sectorFitAt(c, s, xz, local.Y())
			if lm != mgl32.Ident4() {
				r := lm.Mul4x1(mgl32.Vec4{xz.X(), fit.floorY, xz.Y(), 1})
				fit.floorY = r.Y()
			}
			if bestSector == nil || fit.beats(best) {
				best, bestChunk, bestSector = fit, c, s
			}
		}
	}
	return bestChunk, bestSector
}

// ---------- vertical containment ----------

// chunkContainsY reports whether a height falls inside a chunk's vertical
// extent, with a tolerance.
//
// The tolerance is not defensive padding, it is the whole point. A chunk's
// bounds are the extent of its geometry, so its lowest floor *is* MinBound.Y —
// and a body resting on that floor therefore sits exactly on the boundary of
// this test. Whether it lands a hair above or a hair below is decided by how the
// last float rounded, which follows from the level's coordinates rather than
// from anything an author did. Without the tolerance, standing still on the
// ground puts a body in no chunk and no sector roughly half the time, and a
// script's `:enter:` and `:exit:` on a sector misfire for anything at rest.
//
// Only Y is given the tolerance. Nothing in XZ comes to rest exactly on a
// bound — the wall resolver pushes a body a radius clear of every wall — and
// widening the horizontal test would make two adjacent chunks overlap, changing
// which of them a point between them belongs to. Gravity is what makes the
// vertical case special.
func chunkContainsY(c *Chunk, y float32) bool {
	return y >= c.MinBound.Y()-chunkYTolerance(c.MinBound.Y()) &&
		y <= c.MaxBound.Y()+chunkYTolerance(c.MaxBound.Y())
}

// chunkYTolerance is how far outside a bound still counts as on it.
//
// Scaled by the magnitude of the bound because float32 resolution is: an
// absolute millimetre is generous at the origin and below the noise floor ten
// thousand units out, where one unit in the last place is already about a
// millimetre. The floor keeps it meaningful near zero, and both are orders of
// magnitude smaller than any chunk is tall — see TestStackedChunksStayDistinct.
func chunkYTolerance(bound float32) float32 {
	const (
		atOrigin = float32(1e-3) // a millimetre
		relative = float32(1e-6)
	)
	if t := geom.Abs32(bound) * relative; t > atOrigin {
		return t
	}
	return atOrigin
}
