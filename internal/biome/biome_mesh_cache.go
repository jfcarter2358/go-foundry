package biome

// biome_mesh_cache.go — the painted polygon's triangulation, kept between
// rebuilds.
//
// A biome sector's ground patch and its companion water plane are both meshed
// over the same Delaunay triangulation of the same point cloud, and both are
// rebuilt whenever the sector is marked dirty. But "dirty" mostly means the
// FLOOR under the paint moved, not the paint itself — and the triangulation
// depends only on the polygon (BiomeMeshPoints reads nothing but s.Points).
// Re-running it for a height change is pure waste, and not a small one:
// profiling a vertex drag over a modest painted polygon put ~90% of a 134ms
// per-frame rebuild inside geom.DelaunayTriangulate.
//
// So it is cached, keyed on the polygon's own contents. That makes the cache
// self-invalidating: any edit to the points changes the hash and the entry is
// rebuilt, with no dirty flag to keep in step and nothing to forget to call.
// The cost is one hash of the point list per rebuild, which is nothing beside
// the triangulation it skips.

import (
	"encoding/binary"
	"hash/fnv"
	"math"

	"foundry/internal/geom"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

type meshCacheEntry struct {
	key    uint64
	allPts []mgl32.Vec2
	triIdx []int
}

// meshCache is keyed by sector ID; the entry's own key guards the contents.
// Single-threaded like the rest of the editor/render path.
var meshCache = map[string]meshCacheEntry{}

// polygonKey hashes a point list. Bit patterns rather than values, so it does
// not have to reason about float equality — two polygons that hash the same
// are byte-identical.
func polygonKey(pts []mgl32.Vec2) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	for _, p := range pts {
		binary.LittleEndian.PutUint32(buf[0:4], math.Float32bits(p.X()))
		binary.LittleEndian.PutUint32(buf[4:8], math.Float32bits(p.Y()))
		_, _ = h.Write(buf[:])
	}
	return h.Sum64()
}

// BiomeMeshTriangulation returns the mesh point cloud for s (its densified
// boundary plus an interior sample grid) and the Delaunay triangulation over
// it, reusing the previous result when the polygon has not changed.
//
// Returns (nil, nil) for a polygon too small or too degenerate to triangulate,
// which callers already handle as "no mesh".
func BiomeMeshTriangulation(s *world.BiomeSector) (allPts []mgl32.Vec2, triIdx []int) {
	key := polygonKey(s.Points)
	if e, ok := meshCache[s.ID]; ok && e.key == key {
		return e.allPts, e.triIdx
	}

	boundary, pts := BiomeMeshPoints(s)
	if len(pts) == 0 {
		return nil, nil
	}
	tri := geom.DelaunayTriangulate(pts, boundary)
	if len(tri) == 0 {
		return nil, nil
	}
	meshCache[s.ID] = meshCacheEntry{key: key, allPts: pts, triIdx: tri}
	return pts, tri
}

// EvictMeshCache drops the cached triangulation for a deleted biome sector, so
// a level edited for a long time does not accumulate entries for paint that no
// longer exists. Not required for correctness — a stale entry can only be
// reached by a sector reusing the ID, and the key check would reject it.
func EvictMeshCache(id string) { delete(meshCache, id) }
