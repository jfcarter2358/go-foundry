package main

import "github.com/go-gl/mathgl/mgl32"

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
		for si := range c.Sectors {
			s := &c.Sectors[si]
			// Use sector 2D AABB to determine which cells it overlaps
			minCellX := int(floor32(s.MinBound.X() * sh.InvCell))
			minCellZ := int(floor32(s.MinBound.Y() * sh.InvCell)) // MinBound.Y() is Z in 2D
			maxCellX := int(floor32(s.MaxBound.X() * sh.InvCell))
			maxCellZ := int(floor32(s.MaxBound.Y() * sh.InvCell))
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
	cx := int(floor32(pos.X() * sh.InvCell))
	cz := int(floor32(pos.Z() * sh.InvCell))
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
		if pos.Y() < c.MinBound.Y() || pos.Y() > c.MaxBound.Y() {
			continue
		}
		s := &c.Sectors[ref.SectorIdx]
		if pointInPolygon(xz, SectorPoints(c, s)) {
			return c, s
		}
	}
	return nil, nil
}
