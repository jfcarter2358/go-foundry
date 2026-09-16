package nav

// tiles.go — rasterising a level's heightfield a tile at a time, and keeping
// the tiles.
//
// Rasterising is most of what building navigation costs — level1's ground took
// 590 ms of it against 95 ms for the grid — and a streamed level changes a
// small part of itself at a time. So the heightfield is cut into fixed,
// world-aligned tiles of tileColumns columns, each tile rasterised from every
// solid whose box overlaps it (not just its "own" chunk's: chunks overlap, and
// a column's spans merge across solids) and cached under a hash of exactly
// those solids. Switching a zone on or off re-rasterises only the tiles its
// solids touch; every other tile is copied. The tiles are then assembled into
// one heightfield and the grid is built over the whole of it, as it always was,
// so nothing about links, erosion or regions has to know tiles exist.
//
// World-aligned is what makes a tile reusable: a level growing to the west
// moves the heightfield's origin by whole tiles, and a tile's columns are the
// same columns wherever the level's edge happens to be.

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"sync"

	"github.com/go-gl/mathgl/mgl32"
)

// tileColumns is a tile's width in columns: 32 m at DefaultCellSize.
const tileColumns = 128

type tileKey struct {
	tx, tz int32
	cell   float32
}

type tileEntry struct {
	hash uint64
	hf   *Heightfield
}

// tileCache holds every tile built since the last Invalidate. Guarded, because
// a background build (SyncAsync) and an on-the-spot one (For) can both be
// rasterising at once; a tile is immutable once stored, so readers share it.
var tileCache struct {
	sync.Mutex
	m map[tileKey]tileEntry
	// built counts tiles rasterised rather than reused, for the tests.
	built int
}

// resetTiles drops the tile cache — a different level shares no tiles.
func resetTiles() {
	tileCache.Lock()
	tileCache.m = nil
	tileCache.Unlock()
}

// hashSolid hashes a solid's world-space faces.
func hashSolid(s *solid) uint64 {
	h := fnv.New64a()
	var buf [4]byte
	f32 := func(v float32) {
		binary.LittleEndian.PutUint32(buf[:], math.Float32bits(v))
		_, _ = h.Write(buf[:])
	}
	for _, face := range s.faces {
		binary.LittleEndian.PutUint32(buf[:], uint32(len(face)))
		_, _ = h.Write(buf[:])
		for _, p := range face {
			f32(p.X())
			f32(p.Y())
			f32(p.Z())
		}
	}
	return h.Sum64()
}

// buildHeightfield rasterises every solid into columns of cell (or coarser, see
// maxColumns), reusing whatever tiles are cached.
func buildHeightfield(solids []solid, cell, maxSlopeCos float32) *Heightfield {
	h := &Heightfield{Cell: cell, MaxSlopeCos: maxSlopeCos}
	if len(solids) == 0 {
		h.colStart = []int32{0}
		return h
	}
	lo, hi := solids[0].min, solids[0].max
	for _, s := range solids[1:] {
		lo = mgl32.Vec3{min32(lo.X(), s.min.X()), min32(lo.Y(), s.min.Y()), min32(lo.Z(), s.min.Z())}
		hi = mgl32.Vec3{max32(hi.X(), s.max.X()), max32(hi.Y(), s.max.Y()), max32(hi.Z(), s.max.Z())}
	}

	// The tiles covering the level with a column to spare on every side, at
	// the finest cell that keeps the whole within maxColumns. The cell only
	// ever doubles, so it stays put while a level's size changes a little.
	var tw float32
	var tx0, tz0, tilesW, tilesD int
	for {
		tw = float32(tileColumns) * cell
		tx0 = int(math.Floor(float64((lo.X() - cell) / tw)))
		tz0 = int(math.Floor(float64((lo.Z() - cell) / tw)))
		tilesW = int(math.Floor(float64((hi.X()+cell)/tw))) - tx0 + 1
		tilesD = int(math.Floor(float64((hi.Z()+cell)/tw))) - tz0 + 1
		if tilesW*tilesD*tileColumns*tileColumns <= maxColumns {
			break
		}
		cell *= 2
	}
	h.Cell = cell
	h.Origin = mgl32.Vec2{float32(tx0) * tw, float32(tz0) * tw}
	h.W, h.D = tilesW*tileColumns, tilesD*tileColumns

	// Which solids reach each tile, in level order — the order the hash reads
	// them in and the order rasterise was always given them in.
	members := make([][]int32, tilesW*tilesD)
	tileOf := func(v float32, t0, n int) int {
		t := int(math.Floor(float64(v/tw))) - t0
		return max(0, min(n-1, t))
	}
	for si := range solids {
		s := &solids[si]
		i0, i1 := tileOf(s.min.X(), tx0, tilesW), tileOf(s.max.X(), tx0, tilesW)
		j0, j1 := tileOf(s.min.Z(), tz0, tilesD), tileOf(s.max.Z(), tz0, tilesD)
		for j := j0; j <= j1; j++ {
			for i := i0; i <= i1; i++ {
				members[i+j*tilesW] = append(members[i+j*tilesW], int32(si))
			}
		}
	}

	tiles := make([]*Heightfield, tilesW*tilesD)
	var subset []solid
	for t, ids := range members {
		if len(ids) == 0 {
			continue
		}
		key := tileKey{tx: int32(tx0 + t%tilesW), tz: int32(tz0 + t/tilesW), cell: cell}
		hh := fnv.New64a()
		var buf [8]byte
		binary.LittleEndian.PutUint32(buf[:4], math.Float32bits(maxSlopeCos))
		_, _ = hh.Write(buf[:4])
		for _, si := range ids {
			binary.LittleEndian.PutUint64(buf[:], solids[si].hash)
			_, _ = hh.Write(buf[:])
		}
		sum := hh.Sum64()

		tileCache.Lock()
		e, ok := tileCache.m[key]
		tileCache.Unlock()
		if ok && e.hash == sum {
			tiles[t] = e.hf
			continue
		}

		subset = subset[:0]
		for _, si := range ids {
			subset = append(subset, solids[si])
		}
		origin := mgl32.Vec2{float32(key.tx) * tw, float32(key.tz) * tw}
		thf := rasterise(subset, origin, cell, tileColumns, tileColumns, maxSlopeCos)
		tiles[t] = thf
		tileCache.Lock()
		if tileCache.m == nil {
			tileCache.m = map[tileKey]tileEntry{}
		}
		tileCache.m[key] = tileEntry{hash: sum, hf: thf}
		tileCache.built++
		tileCache.Unlock()
	}

	// Assemble: every column copied out of the tile it falls in.
	total := 0
	for _, thf := range tiles {
		if thf != nil {
			total += len(thf.spans)
		}
	}
	cols := h.W * h.D
	h.colStart = make([]int32, cols+1)
	h.spans = make([]span, 0, total)
	for j := 0; j < h.D; j++ {
		tj, lj := j/tileColumns, j%tileColumns
		for i := 0; i < h.W; i++ {
			c := i + j*h.W
			h.colStart[c] = int32(len(h.spans))
			thf := tiles[i/tileColumns+tj*tilesW]
			if thf == nil {
				continue
			}
			lc := i%tileColumns + lj*tileColumns
			h.spans = append(h.spans, thf.spans[thf.colStart[lc]:thf.colStart[lc+1]]...)
		}
	}
	h.colStart[cols] = int32(len(h.spans))
	return h
}
