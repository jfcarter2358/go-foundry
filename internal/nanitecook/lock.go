package nanitecook

// lock.go — border locking, so tiles cut from one surface do not crack.
//
// A Nanite cook simplifies a mesh into a DAG of coarser and coarser cluster
// groups. Each group simplifies alone, which is safe INSIDE one mesh because
// the DAG locks the edges between groups. Between two separate meshes —
// adjacent terrain tiles — nothing does: tile A's simplifier moves its edge
// vertices one way, tile B's another, and at any LOD where they differ a crack
// opens along the seam. The procedural generators tile everything (a
// kilometre of canyon is dozens of tiles), so they need the seam pinned.
//
// Two ways to say which vertices are the seam:
//
//   - LockBorder: every vertex on an OPEN edge, one used by a single
//     triangle. Edges are counted by POSITION, not by pool vertex: a UV seam
//     splits a vertex in the welded pool without opening the surface, and
//     must not lock.
//   - LockRect: every vertex within a tolerance of the edge of an XZ
//     rectangle. For a closed tile (top, skirts, bottom — which nav needs to
//     treat it as one parity solid) there is no open edge, but the top
//     surface's rim is exactly on the tile's rectangle.
//
// A locked vertex is kept at full resolution at every LOD, which is the
// price: a 128 m tile at 0.5 m keeps ~1000 border vertices in its coarsest
// level. Against the tile's interior that is small, and it is what Nanite's
// own tiled landscapes pay too.

import (
	"encoding/binary"
	"hash"
	"math"

	"foundry/internal/gfx"
)

// borderLocks is the pool vertices the options lock beyond the slot seams,
// or nil when they lock none.
func borderLocks(pool *gfx.IndexedMesh, opt Options) []bool {
	if !opt.LockBorder && opt.LockRect == nil {
		return nil
	}
	nv := pool.VertexCount()
	// One ID per distinct position.
	posID := make([]int32, nv)
	ids := map[[3]uint32]int32{}
	for v := 0; v < nv; v++ {
		k := [3]uint32{math.Float32bits(pool.Vertices[5*v]), math.Float32bits(pool.Vertices[5*v+1]), math.Float32bits(pool.Vertices[5*v+2])}
		id, ok := ids[k]
		if !ok {
			id = int32(len(ids))
			ids[k] = id
		}
		posID[v] = id
	}
	lockPos := make([]bool, len(ids))
	anyLocked := false

	if opt.LockBorder {
		type edge struct{ a, b int32 }
		count := map[edge]int32{}
		tri := pool.Indices
		for t := 0; t+2 < len(tri); t += 3 {
			for k := 0; k < 3; k++ {
				a, b := posID[tri[t+k]], posID[tri[t+(k+1)%3]]
				if a == b {
					continue
				}
				if a > b {
					a, b = b, a
				}
				count[edge{a, b}]++
			}
		}
		for e, n := range count {
			if n == 1 {
				lockPos[e.a], lockPos[e.b] = true, true
				anyLocked = true
			}
		}
	}
	if r := opt.LockRect; r != nil {
		tol := opt.LockRectTolerance
		if tol <= 0 {
			tol = 1e-4
		}
		near := func(x, edge float32) bool { return float32(math.Abs(float64(x-edge))) <= tol }
		for v := 0; v < nv; v++ {
			x, z := pool.Vertices[5*v], pool.Vertices[5*v+2]
			inX := x >= r[0]-tol && x <= r[2]+tol
			inZ := z >= r[1]-tol && z <= r[3]+tol
			if (inZ && (near(x, r[0]) || near(x, r[2]))) || (inX && (near(z, r[1]) || near(z, r[3]))) {
				lockPos[posID[v]] = true
				anyLocked = true
			}
		}
	}
	if !anyLocked {
		return nil
	}
	out := make([]bool, nv)
	for v := range out {
		out[v] = lockPos[posID[v]]
	}
	return out
}

// hashLocks folds the lock options into a cook's source hash — nothing at
// all when neither is set, so the hashes of every cook made before they
// existed are unchanged.
func hashLocks(h hash.Hash, opt Options) {
	if !opt.LockBorder && opt.LockRect == nil {
		return
	}
	var b [4]byte
	h.Write([]byte("locks\x00"))
	if opt.LockBorder {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	if r := opt.LockRect; r != nil {
		tol := opt.LockRectTolerance
		if tol <= 0 {
			tol = 1e-4
		}
		for _, f := range []float32{r[0], r[1], r[2], r[3], tol} {
			binary.LittleEndian.PutUint32(b[:], math.Float32bits(f))
			h.Write(b[:])
		}
	}
}
