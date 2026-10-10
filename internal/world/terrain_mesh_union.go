package world

// terrain_mesh_union.go — rebuilding neighbouring smooth sections together.
//
// A smooth section is built from a padded neighbourhood (prepareSmooth): the
// blurred field reaches pad = blur radius + 4 + halo (11) blocks past the
// section each way, and the dual cells it places and relaxes reach the halo
// (5) past it. For a 16^3 section that is a 38^3 field — over thirteen times
// the section — and 28^3 cells, five times it. An edit dirties every section
// within smoothReach (13 blocks at the default smoothing), which is two or
// three sections along each axis, so building each one alone computes the
// field and cells of their shared borders up to eight times over.
//
// Here the dirty sections that have a surface are grouped into boxes of up
// to terrainUnionTile sections a side, and each box is prepared ONCE —
// field and cells over the box grown by the same pad and halo — and then
// every section in it emits its faces from that one preparation.
//
// # Why the faces are the same bytes
//
// Every value a section's faces read is a pure function of the block grid
// and its position, as long as it is far enough from the edge of the
// scratch region it was computed in:
//
//   - The X pass of the blur reads the grid itself past the region's edge,
//     so it is exact everywhere; the Y and Z passes clamp at the edge and
//     are exact at least the kernel radius inside it. Each sample's sum is
//     taken in the same order whatever the region, so equal inputs give
//     equal bits.
//   - A dual cell's start (dual contouring, then floorSmooth) reads the
//     field within 2 blocks of the cell and the grid's columns; its
//     relaxation is truncated at the cell region's edge and the error walks
//     in one cell per round, which is what the halo of rounds+1 cells
//     absorbs; its normal reads the cells beside it.
//
// A union box keeps the same pad and halo past ITS edge, and every section
// inside it is at least as far from that edge as it was from its own, so
// each face reads exactly the values it read before. TestTerrainUnionBuild*
// compares every section of union and per-section builds after random edit
// sequences, field for field.
//
// # Parallelism
//
// A box's stages (occupancy, three blur passes, the hold, placement,
// floors, each relaxation round, normals, face emission) each write every
// output from inputs the previous stage finished, so each splits across
// workers by slabs with nothing to merge, and the result is the same however
// it is split. A rebuild of a few boxes (an edit) runs the boxes one after
// another with every stage split across all CPUs; a rebuild of many (a
// load, a Touch) gives each worker whole boxes and runs their stages
// serially, which needs no barriers.
//
// # Not asynchronous
//
// The rebuild stays synchronous on the main thread. It is tempting to keep
// drawing a section's old faces while the new ones build on a goroutine,
// but the faces are not only drawn: collision (collision/terrain.go) walks
// and stands on them through Raycast, ray picks and hitscan go through the
// same bins, nav gathers from them and foliage places on them. A mined
// block has to stop being solid the frame it is mined, or the player stands
// on air, a second pick hits the block already gone and the shot that
// should pass through the hole stops on it. The expensive part of a rebuild
// IS those faces, so a split that kept collision synchronous and made only
// the drawing asynchronous would save nothing but the upload, which is the
// renderer's (render/terrain.go) to defer if it ever matters.

import (
	"runtime"
	"slices"
	"sync"
)

// terrainUnionBuild switches the smooth rebuild between preparing whole
// boxes of sections at once (true) and the original one-section-at-a-time
// path. Tests flip it to prove the two give the same faces.
var terrainUnionBuild = true

// terrainBuildWorkers caps how many goroutines a rebuild uses; tests set it
// to 1 to prove the split changes nothing.
var terrainBuildWorkers = runtime.NumCPU()

// terrainUnionTile is the most sections along each axis one union box
// holds when few boxes are dirty — every section an edit at the default
// smoothing can reach — and terrainUnionTileMany when there are enough
// boxes to keep every worker busy with whole ones, which keeps each
// worker's scratch small (a 2^3 box's field is 54^3 floats).
const (
	terrainUnionTile     = 3
	terrainUnionTileMany = 2
)

// terrainBuilderPool keeps builders' scratch between rebuilds: an edit
// rebuilds every few frames while a pick is held down, and the field and
// cells of a union box are megabytes.
var terrainBuilderPool sync.Pool

// getTerrainBuilder is a builder for t, its stages split across workers,
// with whatever scratch an earlier rebuild left it.
func getTerrainBuilder(t *Terrain, workers int) *terrainBuilder {
	fresh := newTerrainBuilder(t)
	fresh.workers = workers
	if old, ok := terrainBuilderPool.Get().(*terrainBuilder); ok {
		fresh.occ, fresh.blur, fresh.tmp, fresh.fld = old.occ, old.blur, old.tmp, old.fld
		fresh.cells, fresh.pa, fresh.pb = old.cells, old.pa, old.pb
		fresh.surf, fresh.nbr, fresh.floors, fresh.fw, fresh.fd = old.surf, old.nbr, old.floors, old.fw, old.fd
	}
	return fresh
}

func putTerrainBuilder(b *terrainBuilder) {
	b.t = nil
	terrainBuilderPool.Put(b)
}

// par runs fn over [0, n) split into contiguous ranges across b.workers
// goroutines, and returns when every range is done.
func (b *terrainBuilder) par(n int, fn func(lo, hi int)) {
	terrainParallel(n, b.workers, fn)
}

func terrainParallel(n, workers int, fn func(lo, hi int)) {
	workers = min(workers, n)
	if workers <= 1 {
		fn(0, n)
		return
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		lo, hi := n*w/workers, n*(w+1)/workers
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn(lo, hi)
		}()
	}
	wg.Wait()
}

// buildSmoothUnion rebuilds the smooth sections todo. See the file comment.
func buildSmoothUnion(t *Terrain, todo []*TerrainSection) {
	workers := max(1, terrainBuildWorkers)
	// Most sections of a terrain — rock under the ground, air over it — have
	// no surface; they are emptied here and take no part in a box, so a box
	// shrinks to the sections that do.
	has := make([]bool, len(todo))
	terrainParallel(len(todo), workers, func(lo, hi int) {
		b := &terrainBuilder{t: t}
		for i := lo; i < hi; i++ {
			s := todo[i]
			if b.hasFace(t.sectionBlocks(s)) {
				has[i] = true
				continue
			}
			s.Faces = s.Faces[:0]
			b.finish(s)
		}
	})
	var need []*TerrainSection
	for i, s := range todo {
		if has[i] {
			need = append(need, s)
		}
	}
	if len(need) == 0 {
		return
	}
	tile := terrainUnionTile
	many := len(need) > tile*tile*tile
	if many {
		tile = terrainUnionTileMany
	}
	byTile := map[[3]int][]*TerrainSection{}
	for _, s := range need {
		k := [3]int{s.X / tile, s.Y / tile, s.Z / tile}
		byTile[k] = append(byTile[k], s)
	}
	keys := make([][3]int, 0, len(byTile))
	for k := range byTile {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b [3]int) int {
		for i := 2; i >= 0; i-- {
			if a[i] != b[i] {
				return a[i] - b[i]
			}
		}
		return 0
	})
	if many && workers > 1 {
		var mu sync.Mutex
		next := 0
		terrainParallel(workers, workers, func(_, _ int) {
			b := getTerrainBuilder(t, 1)
			defer putTerrainBuilder(b)
			for {
				mu.Lock()
				i := next
				next++
				mu.Unlock()
				if i >= len(keys) {
					return
				}
				b.buildBox(byTile[keys[i]])
			}
		})
		return
	}
	b := getTerrainBuilder(t, workers)
	defer putTerrainBuilder(b)
	for _, k := range keys {
		b.buildBox(byTile[k])
	}
}

// buildBox prepares the field and cells over the box round secs once and
// emits every one of them from it.
func (b *terrainBuilder) buildBox(secs []*TerrainSection) {
	t := b.t
	x0, y0, z0, x1, y1, z1 := t.sectionBlocks(secs[0])
	for _, s := range secs[1:] {
		sx0, sy0, sz0, sx1, sy1, sz1 := t.sectionBlocks(s)
		x0, y0, z0 = min(x0, sx0), min(y0, sy0), min(z0, sz0)
		x1, y1, z1 = max(x1, sx1), max(y1, sy1), max(z1, sz1)
	}
	b.prepareSmooth(x0, y0, z0, x1, y1, z1)
	// Emitting reads the cells and writes only its own section.
	b.par(len(secs), func(lo, hi int) {
		for _, s := range secs[lo:hi] {
			b.emit(s)
		}
	})
}
