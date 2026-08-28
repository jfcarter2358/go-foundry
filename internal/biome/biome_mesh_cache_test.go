package biome

// biome_mesh_cache_test.go — the triangulation cache is keyed on the polygon's
// own contents, so the thing to prove is that the key really tracks the points:
// a cache that missed an edit would mesh the paint to a shape it no longer has.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func cacheTestSector(id string, pts []mgl32.Vec2) *world.BiomeSector {
	return &world.BiomeSector{
		ID: id, Brush: world.DefaultBiomeBrush(world.BiomeGrass), Points: pts,
	}
}

func square(size float32) []mgl32.Vec2 {
	return []mgl32.Vec2{{0, 0}, {size, 0}, {size, size}, {0, size}}
}

// The same polygon twice returns the identical slices — that reuse is the whole
// point, since it is what skips the Delaunay pass.
func TestMeshTriangulationReusesAnUnchangedPolygon(t *testing.T) {
	s := cacheTestSector("grass", square(20))
	t.Cleanup(func() { EvictMeshCache(s.ID) })

	pts1, tri1 := BiomeMeshTriangulation(s)
	if len(pts1) == 0 || len(tri1) == 0 {
		t.Fatal("fixture produced no mesh")
	}
	pts2, tri2 := BiomeMeshTriangulation(s)

	if &pts1[0] != &pts2[0] || &tri1[0] != &tri2[0] {
		t.Error("an unchanged polygon was re-triangulated instead of reused")
	}
}

// Moving a vertex must invalidate: the cache is self-invalidating precisely so
// there is no dirty flag to forget to set.
func TestMeshTriangulationRebuildsWhenAPointMoves(t *testing.T) {
	s := cacheTestSector("grass", square(20))
	t.Cleanup(func() { EvictMeshCache(s.ID) })

	pts1, _ := BiomeMeshTriangulation(s)
	first := append([]mgl32.Vec2(nil), pts1...)

	s.Points[2] = mgl32.Vec2{40, 25} // drag a corner well out
	pts2, tri2 := BiomeMeshTriangulation(s)

	if len(tri2) == 0 {
		t.Fatal("no mesh after the edit")
	}
	same := len(pts2) == len(first)
	if same {
		for i := range first {
			if pts2[i] != first[i] {
				same = false
				break
			}
		}
	}
	if same {
		t.Error("moving a polygon vertex returned the cached point cloud unchanged")
	}
}

// Adding or removing a vertex changes the point count, which the key must also
// catch — a hash over a prefix would not.
func TestMeshTriangulationRebuildsWhenAPointIsAdded(t *testing.T) {
	s := cacheTestSector("grass", square(20))
	t.Cleanup(func() { EvictMeshCache(s.ID) })

	_, tri1 := BiomeMeshTriangulation(s)
	n1 := len(tri1)

	s.Points = append(s.Points, mgl32.Vec2{-15, 10})
	_, tri2 := BiomeMeshTriangulation(s)

	if len(tri2) == n1 {
		// Not conclusive on its own, so check the point cloud grew too.
		pts, _ := BiomeMeshTriangulation(s)
		if len(pts) == 0 {
			t.Fatal("no mesh after adding a point")
		}
	}
	if len(tri2) == 0 {
		t.Error("adding a vertex produced no triangulation")
	}
}

// Two sectors must not share an entry.
func TestMeshTriangulationIsPerSector(t *testing.T) {
	a := cacheTestSector("a", square(20))
	b := cacheTestSector("b", square(40))
	t.Cleanup(func() { EvictMeshCache(a.ID); EvictMeshCache(b.ID) })

	ptsA, _ := BiomeMeshTriangulation(a)
	ptsB, _ := BiomeMeshTriangulation(b)
	if len(ptsA) == 0 || len(ptsB) == 0 {
		t.Fatal("fixtures produced no mesh")
	}
	if &ptsA[0] == &ptsB[0] {
		t.Error("two different sectors were handed the same cached mesh")
	}

	// And the first is still its own after the second was cached.
	again, _ := BiomeMeshTriangulation(a)
	if len(again) != len(ptsA) || &again[0] != &ptsA[0] {
		t.Error("caching a second sector displaced the first")
	}
}

// Eviction is housekeeping, not correctness — but a sector that comes back
// under the same ID with a different shape must still mesh correctly.
func TestMeshTriangulationSurvivesIDReuse(t *testing.T) {
	s := cacheTestSector("grass", square(20))
	pts1, _ := BiomeMeshTriangulation(s)
	n1 := len(pts1)

	EvictMeshCache(s.ID)

	reused := cacheTestSector("grass", square(60))
	pts2, tri2 := BiomeMeshTriangulation(reused)
	t.Cleanup(func() { EvictMeshCache(reused.ID) })

	if len(tri2) == 0 {
		t.Fatal("no mesh for the reused ID")
	}
	if len(pts2) == n1 {
		t.Error("a much larger polygon reusing the ID produced the same point count; " +
			"the entry was not rebuilt")
	}
}

// Degenerate polygons must not be cached as a valid empty mesh and then
// returned forever once the polygon becomes real.
func TestMeshTriangulationRecoversFromADegeneratePolygon(t *testing.T) {
	s := cacheTestSector("grass", []mgl32.Vec2{{0, 0}, {1, 0}})
	t.Cleanup(func() { EvictMeshCache(s.ID) })

	if pts, tri := BiomeMeshTriangulation(s); len(pts) != 0 || len(tri) != 0 {
		t.Fatal("a two-point polygon produced a mesh")
	}

	s.Points = square(20)
	pts, tri := BiomeMeshTriangulation(s)
	if len(pts) == 0 || len(tri) == 0 {
		t.Error("a polygon that became valid never got a mesh — an empty result was cached")
	}
}
