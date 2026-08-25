package world

import (
	"foundry/internal/geom"

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

// BuildSpatialHash creates a spatial hash from all chunks and their sectors.
func BuildSpatialHash(chunks []Chunk, cellSize float32) *SpatialHash {
	if cellSize <= 0 {
		cellSize = 8.0
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
			// Use sector 2D AABB to determine which cells it overlaps
			minCellX := int(geom.Floor32(s.MinBound.X() * sh.InvCell))
			minCellZ := int(geom.Floor32(s.MinBound.Y() * sh.InvCell)) // MinBound.Y() is Z in 2D
			maxCellX := int(geom.Floor32(s.MaxBound.X() * sh.InvCell))
			maxCellZ := int(geom.Floor32(s.MaxBound.Y() * sh.InvCell))
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
// then runs the authoritative point-in-polygon test on candidates.
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
	xz := mgl32.Vec2{pos.X(), pos.Z()}
	for _, ref := range refs {
		c := &chunks[ref.ChunkIdx]
		// Y-bounds check enables vertically stacked chunks (rooms-over-rooms).
		// Chunks that overlap in XZ but occupy different Y ranges are
		// disambiguated by the entity's vertical position.
		if !chunkContainsY(c, pos.Y()) {
			continue
		}
		s := &c.Sectors[ref.SectorIdx]
		if geom.PointInPolygon(xz, SectorPoints(c, s)) {
			return c, s
		}
	}
	return nil, nil
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
		if pos.X() >= c.MinBound.X() && pos.X() <= c.MaxBound.X() &&
			chunkContainsY(c, pos.Y()) &&
			pos.Z() >= c.MinBound.Z() && pos.Z() <= c.MaxBound.Z() {
			return c
		}
	}
	return nil
}

// FindSector returns the sector in chunk whose XZ polygon contains xz (ray-casting PIP).
func FindSector(chunk *Chunk, xz mgl32.Vec2) *Sector {
	if chunk == nil {
		return nil
	}
	for i := range chunk.Sectors {
		s := &chunk.Sectors[i]
		if geom.PointInPolygon(xz, SectorPoints(chunk, s)) {
			return s
		}
	}
	return nil
}

// FindChunkAndSector scans all chunks and returns the first (chunk, sector) pair
// whose sector XZ polygon contains pos.  Unlike calling FindChunk+FindSector
// separately this is correct even when chunk AABBs overlap, because it uses the
// authoritative point-in-polygon test rather than the approximate AABB test.
func FindChunkAndSector(chunks []Chunk, pos mgl32.Vec3) (*Chunk, *Sector) {
	xz := mgl32.Vec2{pos.X(), pos.Z()}
	for i := range chunks {
		c := &chunks[i]
		if c.InFrame() {
			continue // not in world space; see Chunk.FrameOwner
		}
		// Quick AABB reject (XZ + Y) before the more expensive PIP scan.
		// The Y check enables vertically stacked chunks (rooms-over-rooms).
		if pos.X() < c.MinBound.X() || pos.X() > c.MaxBound.X() ||
			!chunkContainsY(c, pos.Y()) ||
			pos.Z() < c.MinBound.Z() || pos.Z() > c.MaxBound.Z() {
			continue
		}
		for j := range c.Sectors {
			s := &c.Sectors[j]
			if geom.PointInPolygon(xz, SectorPoints(c, s)) {
				return c, s
			}
		}
	}
	return nil, nil
}

// FindChunkAndSectorAmong locates pos among a named subset of chunks, in
// whatever coordinate system those chunks are authored in.
//
// This is how a frame is searched. There is no spatial hash: a frame owns a
// handful of chunks — usually one — so the scan is shorter than the hash lookup
// it would replace, and building a hash per frame per structural change would
// cost more than it saved.
func FindChunkAndSectorAmong(chunks []Chunk, idx []int, pos mgl32.Vec3) (*Chunk, *Sector) {
	xz := mgl32.Vec2{pos.X(), pos.Z()}
	for _, ci := range idx {
		if ci < 0 || ci >= len(chunks) {
			continue
		}
		c := &chunks[ci]
		if pos.X() < c.MinBound.X() || pos.X() > c.MaxBound.X() ||
			!chunkContainsY(c, pos.Y()) ||
			pos.Z() < c.MinBound.Z() || pos.Z() > c.MaxBound.Z() {
			continue
		}
		for j := range c.Sectors {
			s := &c.Sectors[j]
			if geom.PointInPolygon(xz, SectorPoints(c, s)) {
				return c, s
			}
		}
	}
	return nil, nil
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
