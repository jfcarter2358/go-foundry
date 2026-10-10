package world

import (
	"unsafe"

	"foundry/internal/geom"

	"github.com/go-gl/mathgl/mgl32"
)

// SpatialHash provides O(1) lookup for the chunk under an XZ position. Each
// cell in the grid stores the indices of every chunk whose world AABB
// overlaps that cell.
//
// A sector needed a second level of bucketing within a chunk — which of
// several sectors stacked at the same XZ a position belonged to. A brush
// carries no such ranking: "what's under this point" is a collision/raycast
// question (internal/collision), not a spatial-lookup one, so this hash only
// ever answers "which chunk".
type SpatialHash struct {
	CellSize float32
	InvCell  float32 // 1.0 / CellSize
	Cells    map[[2]int][]int
}

// SpatialCellSize is the hash's cell size in world units. One constant so that
// every rebuild buckets into the same grid — a hash built at one size and
// queried against another silently finds nothing.
const SpatialCellSize = float32(8.0)

// RebuildSpatialHash re-buckets lvl's chunks into a fresh hash.
//
// MUST be called whenever a chunk's Origin, Rotation or Scale changes. The hash
// keys are WORLD cells derived from ch.ModelMatrix (see BuildSpatialHash), so
// moving a chunk invalidates every cell it was filed under: a query at the
// chunk's new position finds no refs, and one at its old position finds a
// stale ref that then fails the exact test (which uses the *new* matrix). The
// chunk simply stops existing as far as collision, entity location and the
// script location verbs are concerned, until something unrelated happens to
// rebuild.
func RebuildSpatialHash(lvl *Level) {
	if lvl == nil {
		return
	}
	lvl.Spatial = BuildSpatialHash(lvl.Chunks, SpatialCellSize)
	// The hash now reflects every chunk's enabled flag, so none of them has a
	// toggle left for SyncChunkGeometry to report. See Chunk.indexedDisabled.
	for ci := range lvl.Chunks {
		lvl.Chunks[ci].indexedDisabled = lvl.Chunks[ci].Disabled
	}
}

// BuildSpatialHash creates a spatial hash from all chunks.
//
// Each chunk is bucketed by its WORLD-space footprint (WorldBounds) rather
// than its chunk-local MinBound/MaxBound, so a chunk that has been moved,
// rotated or scaled still lands in the cells a world-space query will
// actually look in. Bucketing is necessarily conservative for a rotated
// chunk (its axis-aligned world box is bigger than the chunk itself);
// FindChunkSH resolves each candidate exactly, in the chunk's own local
// space.
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
		Cells:    make(map[[2]int][]int),
	}
	for ci := range chunks {
		c := &chunks[ci]
		// A frame-owned chunk's geometry is in its frame's coordinates, not the
		// world's. Indexing it here would answer world-space queries with
		// brushes from inside a moving ship — see Chunk.FrameOwner.
		if c.InFrame() {
			continue
		}
		// A disabled chunk is not in the world: leaving it out here is what
		// makes collision, entity location and the script location verbs all
		// walk straight through it, since every one of them resolves through
		// this hash. See chunk_enable.go.
		if !c.IsEnabled() {
			continue
		}
		wmin, wmax := c.WorldBounds()
		minCellX := int(geom.Floor32(wmin.X() * sh.InvCell))
		minCellZ := int(geom.Floor32(wmin.Z() * sh.InvCell))
		maxCellX := int(geom.Floor32(wmax.X() * sh.InvCell))
		maxCellZ := int(geom.Floor32(wmax.Z() * sh.InvCell))
		for cx := minCellX; cx <= maxCellX; cx++ {
			for cz := minCellZ; cz <= maxCellZ; cz++ {
				key := [2]int{cx, cz}
				sh.Cells[key] = append(sh.Cells[key], ci)
			}
		}
	}
	return sh
}

// FindChunkSH looks up the spatial hash for the cell containing pos, then
// runs the authoritative bounds test on candidates — each in its own chunk's
// local space, since a bucketed candidate may be placed by a different
// transform than the next one in the same cell.
//
// Where more than one candidate's bounds contain pos (vertically stacked
// chunks — rooms over rooms), the one whose local Y sits closest to its own
// vertical centre wins. That is a coarse tiebreak compared to the old
// per-sector floor ranking, and deliberately so: which brush a body is
// actually resting on is what collision (internal/collision) resolves, not
// this lookup — this only ever needs to pick a plausible chunk to search.
func FindChunkSH(sh *SpatialHash, chunks []Chunk, pos mgl32.Vec3) *Chunk {
	if sh == nil {
		return FindChunk(chunks, pos)
	}
	cx := int(geom.Floor32(pos.X() * sh.InvCell))
	cz := int(geom.Floor32(pos.Z() * sh.InvCell))
	refs := sh.Cells[[2]int{cx, cz}]
	if len(refs) == 0 {
		return nil
	}

	var best *Chunk
	bestDist := float32(0)
	for _, ci := range refs {
		c := &chunks[ci]
		local := c.WorldToLocal(pos)
		if local.X() < c.MinBound.X() || local.X() > c.MaxBound.X() ||
			!chunkContainsY(c, local.Y()) ||
			local.Z() < c.MinBound.Z() || local.Z() > c.MaxBound.Z() {
			continue
		}
		mid := (c.MinBound.Y() + c.MaxBound.Y()) / 2
		dist := geom.Abs32(local.Y() - mid)
		if best == nil || dist < bestDist {
			best, bestDist = c, dist
		}
	}
	return best
}

// ChunksOverlapping appends to out every enabled world-space chunk whose world
// bounds overlap the box [lo, hi], in level order, and returns it.
//
// FindChunkSH answers "which one chunk is this point in", which is the right
// question for where a body is and the wrong one for what it is solid against:
// a body on the seam between two terrain chunks touches both, a canyon's chunk
// boxes overlap each other, and a body standing on a chunk whose top is its
// floor is not inside that chunk at all. Collision asks this instead and
// resolves against all of them. Every hash cell the box covers is walked, so a
// wide box finds a chunk whose footprint only reaches one of its corners.
func ChunksOverlapping(lvl *Level, lo, hi mgl32.Vec3, out []*Chunk) []*Chunk {
	if lvl == nil {
		return out
	}
	overlaps := func(c *Chunk) bool {
		if c.InFrame() || !c.IsEnabled() {
			return false
		}
		wmin, wmax := c.WorldBounds()
		return wmin.X() <= hi.X() && wmax.X() >= lo.X() &&
			wmin.Y() <= hi.Y() && wmax.Y() >= lo.Y() &&
			wmin.Z() <= hi.Z() && wmax.Z() >= lo.Z()
	}
	sh := lvl.Spatial
	if sh == nil {
		for ci := range lvl.Chunks {
			if overlaps(&lvl.Chunks[ci]) {
				out = append(out, &lvl.Chunks[ci])
			}
		}
		return out
	}
	minX := int(geom.Floor32(lo.X() * sh.InvCell))
	maxX := int(geom.Floor32(hi.X() * sh.InvCell))
	minZ := int(geom.Floor32(lo.Z() * sh.InvCell))
	maxZ := int(geom.Floor32(hi.Z() * sh.InvCell))
	start := len(out)
	for cx := minX; cx <= maxX; cx++ {
		for cz := minZ; cz <= maxZ; cz++ {
			for _, ci := range sh.Cells[[2]int{cx, cz}] {
				if ci >= len(lvl.Chunks) {
					continue // a hash older than the slice; the next rebuild fixes it
				}
				c := &lvl.Chunks[ci]
				dup := false
				for _, have := range out[start:] {
					if have == c {
						dup = true
						break
					}
				}
				if !dup && overlaps(c) {
					out = append(out, c)
				}
			}
		}
	}
	// Level order, whatever order the cells were walked in, so a body resolves
	// against the same chunks in the same order wherever it stands.
	found := out[start:]
	for i := 1; i < len(found); i++ {
		for j := i; j > 0 && chunkIndex(lvl, found[j]) < chunkIndex(lvl, found[j-1]); j-- {
			found[j], found[j-1] = found[j-1], found[j]
		}
	}
	return out
}

// chunkIndex is c's index in lvl.Chunks; c must point into that slice.
func chunkIndex(lvl *Level, c *Chunk) int {
	return int((uintptr(unsafe.Pointer(c)) - uintptr(unsafe.Pointer(&lvl.Chunks[0]))) / unsafe.Sizeof(lvl.Chunks[0]))
}

// ---------- linear lookups ----------
//
// The spatial hash above is the fast path; this is the exhaustive scan it
// falls back to, and the one the editor uses when the hash has not been
// built.

// FindChunk returns the chunk whose 3D AABB contains pos, or nil (O(N) scan).
func FindChunk(chunks []Chunk, pos mgl32.Vec3) *Chunk {
	for i := range chunks {
		c := &chunks[i]
		if c.InFrame() {
			continue // not in world space; see Chunk.FrameOwner
		}
		if !c.IsEnabled() {
			continue // switched off; see chunk_enable.go
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

// FindChunkAmong locates pos among a named subset of chunks, in whatever
// coordinate system those chunks are authored in.
//
// This is how a frame is searched: the caller (Frame.ChunkAt) has already
// carried pos out of the frame via Frame.FromWorld, so pos arrives here
// already expressed in the frame's own local space. Each chunk in idx can
// still carry its own placement within that frame — the same
// LocalPlacementMatrix ModelMatrix composes under the frame's Xform — so pos
// is carried one step further, into that chunk's own local space, before
// testing against its bounds.
//
// There is no spatial hash: a frame owns a handful of chunks — usually one —
// so the scan is shorter than the hash lookup it would replace, and building
// a hash per frame per structural change would cost more than it saved.
func FindChunkAmong(chunks []Chunk, idx []int, pos mgl32.Vec3) *Chunk {
	for _, ci := range idx {
		if ci < 0 || ci >= len(chunks) {
			continue
		}
		c := &chunks[ci]
		if !c.IsEnabled() {
			continue // switched off; see chunk_enable.go
		}
		if ChunkContains(c, pos) {
			return c
		}
	}
	return nil
}

// ChunkContains reports whether pos — expressed in the space the chunk is
// PLACED in, which is its frame's for a frame-owned chunk and the world's
// otherwise — falls inside the chunk's bounds.
//
// Exported because it is the predicate that decides Entity.CurrentChunk, and
// the frame-entry rule for an entity's own geometry has to be exactly the same
// question asked the other way round: a body is aboard an entity when it is
// inside that entity's geometry chunk, and steps off when it is inside none of
// its frame's chunks. Two nearly-identical containment tests would give the two
// halves of that a sliver of ground they both claimed, which is the ping-pong
// the whole doorway design exists to avoid. See collision/entity_brushes.go.
func ChunkContains(c *Chunk, pos mgl32.Vec3) bool {
	if c == nil {
		return false
	}
	local := pos
	lm := c.LocalPlacementMatrix()
	if lm != mgl32.Ident4() {
		r := lm.Inv().Mul4x1(pos.Vec4(1))
		local = mgl32.Vec3{r.X(), r.Y(), r.Z()}
	}
	return local.X() >= c.MinBound.X() && local.X() <= c.MaxBound.X() &&
		chunkContainsY(c, local.Y()) &&
		local.Z() >= c.MinBound.Z() && local.Z() <= c.MaxBound.Z()
}

// ---------- vertical containment ----------

// chunkContainsY reports whether a height falls inside a chunk's vertical
// extent, with a tolerance.
//
// The tolerance is not defensive padding, it is the whole point. A chunk's
// bounds are the extent of its geometry, so its lowest point *is* MinBound.Y —
// and a body resting there therefore sits exactly on the boundary of this
// test. Whether it lands a hair above or a hair below is decided by how the
// last float rounded, which follows from the level's coordinates rather than
// from anything an author did. Without the tolerance, standing still on the
// ground puts a body in no chunk roughly half the time.
//
// Only Y is given the tolerance. Nothing in XZ comes to rest exactly on a
// bound — the wall resolver pushes a body a radius clear of every wall — and
// widening the horizontal test would make two adjacent chunks overlap,
// changing which of them a point between them belongs to. Gravity is what
// makes the vertical case special.
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
