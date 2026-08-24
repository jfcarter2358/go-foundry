package main

// biome_water.go — auto-manages a companion water plane for every Water-kind
// BiomeSector, filling the painted polygon.
//
// Water paints differently from every other biome kind: instead of GPU
// scatter or a shaded ground patch, painting Water gets you an actual water
// *surface* — waves, fresnel reflection, alpha-blended pass, shadows off (see
// water_surface.go/wall.go/main.go). The companion surface lives in
// Chunk.WaterSurfaces, a runtime-only slice kept in sync with BiomeSectors
// here, so it never gets serialized: it is fully derived from the owning
// BiomeSector and rebuilt after load.
//
// The mesh follows the painted polygon exactly (the same Delaunay-triangulated
// point cloud the ground patch uses), flat at local y=0 — wave displacement
// happens in the shared water vertex/fragment shader. The sector's own ground
// patch (rendered separately, see biome_ground.go) acts as the lakebed
// beneath it.

import (
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// SyncBiomeWaterSurfaces keeps every chunk's WaterSurfaces in step with its
// Water-kind BiomeSectors: adds a companion surface for a newly-painted or
// newly-switched-to-Water sector, rebuilds one whose polygon or water params
// changed, and drops one whose sector was deleted or switched to a different
// kind.
//
// Must run early in the frame, before the water draw loop picks up
// chunk.WaterSurfaces (main.go) — and before RenderBiomeGround, which is what
// actually clears BiomeSector.GroundDirty once every mesh derived from it this
// frame (this sync and the ground patch) has had a chance to see it dirty.
func SyncBiomeWaterSurfaces(level *world.Level) {
	if level == nil {
		return
	}
	for ci := range level.Chunks {
		syncChunkWaterSurfaces(level, &level.Chunks[ci])
	}
}

func syncChunkWaterSurfaces(level *world.Level, ch *world.Chunk) {
	if len(ch.BiomeSectors) == 0 && len(ch.WaterSurfaces) == 0 {
		return
	}

	want := map[string]*world.BiomeSector{}
	for si := range ch.BiomeSectors {
		s := &ch.BiomeSectors[si]
		if s.Brush.Kind == world.BiomeWater && len(s.Points) >= 3 {
			want[s.ID] = s
		}
	}

	// Drop companion surfaces whose sector is gone or no longer Water.
	kept := ch.WaterSurfaces[:0]
	for i := range ch.WaterSurfaces {
		w := &ch.WaterSurfaces[i]
		if _, ok := want[w.ID]; !ok {
			w.Geometry.Delete()
			continue
		}
		kept = append(kept, *w)
	}
	ch.WaterSurfaces = kept

	// Add missing / rebuild dirty.
	for id, s := range want {
		idx := -1
		for i := range ch.WaterSurfaces {
			if ch.WaterSurfaces[i].ID == id {
				idx = i
				break
			}
		}
		isNew := idx < 0
		if isNew {
			ch.WaterSurfaces = append(ch.WaterSurfaces, world.WaterSurface{ID: id, ChunkID: ch.ID})
			idx = len(ch.WaterSurfaces) - 1
		}
		w := &ch.WaterSurfaces[idx]
		if isNew || s.GroundDirty || w.Geometry.Vao == 0 {
			rebuildBiomeWaterSurface(level, s, w)
			// Fold the new/changed plane into the chunk's cull bounds right
			// away — outside the editor nothing else recomputes them
			// per-frame (see main.go), and a lake at a chunk's edge could
			// otherwise sit outside stale bounds and never draw.
			ch.RecomputeBounds()
		}
	}
}

// biomeWaterCentroidAndFloor returns the sector's polygon-AABB center and the
// structural floor height sampled there — the water plane's reference
// position (its surface then sits WaterDepth below that floor height).
func biomeWaterCentroidAndFloor(level *world.Level, s *world.BiomeSector) (mgl32.Vec2, float32) {
	min, max := s.Bounds()
	c := mgl32.Vec2{(min.X() + max.X()) / 2, (min.Y() + max.Y()) / 2}
	return c, floorHeightUnderLevel(level, c.X(), c.Y())
}

// buildBiomeWaterMesh returns an indexed, double-sided, flat XZ grid mesh
// (vertex format [X,Y,Z,U,V], local to centroid) covering sector s's polygon,
// using the same Delaunay-triangulated point cloud as the ground patch
// (biome_ground.go's biomeMeshPoints) so the water's footprint follows the
// painted polygon exactly (no grid-diagonal staircase) and lines up with its
// lakebed. UV tiles from world position so the texture doesn't re-stretch as
// the sector is edited.
func buildBiomeWaterMesh(s *world.BiomeSector, centroid mgl32.Vec2) ([][]float32, []int) {
	boundary, allPts := biomeMeshPoints(s)
	if len(allPts) == 0 {
		return nil, nil
	}
	triIdx := geom.DelaunayTriangulate(allPts, boundary)
	if len(triIdx) == 0 {
		return nil, nil
	}

	const uvTile = 0.15
	verts := make([][]float32, len(allPts))
	for i, p := range allPts {
		verts[i] = []float32{p.X() - centroid.X(), 0, p.Y() - centroid.Y(), p.X() * uvTile, p.Y() * uvTile}
	}

	// Double-sided: both windings, so the surface is visible from below too
	// (matches the old grid mesh's behavior, and the transparent sub-pass the
	// water draw loop hands it to expects it).
	indices := make([]int, 0, len(triIdx)*2)
	indices = append(indices, triIdx...)
	for i := 0; i < len(triIdx); i += 3 {
		indices = append(indices, triIdx[i], triIdx[i+2], triIdx[i+1])
	}
	return verts, indices
}

// biomeWaterTexture is what every derived water plane is drawn with. There is
// no per-sector choice: the look is carried by the wave and reflection params,
// not by swapping the base map.
const biomeWaterTexture = "water"

// biomeWaterAlpha is how much of the lakebed shows through the surface.
const biomeWaterAlpha = float32(0.75)

// rebuildBiomeWaterSurface (re)generates w's geometry and water params from
// sector s, in place.
func rebuildBiomeWaterSurface(level *world.Level, s *world.BiomeSector, w *world.WaterSurface) {
	centroid, floorY := biomeWaterCentroidAndFloor(level, s)
	depth := s.Brush.WaterDepth
	if depth <= 0 {
		depth = world.DefaultWaterDepth
	}
	verts, indices := buildBiomeWaterMesh(s, centroid)

	w.Position = mgl32.Vec3{centroid.X(), floorY - depth, centroid.Y()}
	w.Alpha = biomeWaterAlpha
	w.Water = s.Brush.Water
	if !w.Water.Enabled {
		w.Water = gfx.DefaultWaterParams()
	}

	min, max := s.Bounds()
	w.BoundHalfX = (max.X()-min.X())/2 + 1
	w.BoundHalfY = depth + 0.5
	w.BoundHalfZ = (max.Y()-min.Y())/2 + 1

	w.Geometry.Delete()
	_ = gfx.EnsureTextureLoaded(biomeWaterTexture)
	if len(verts) == 0 || len(indices) == 0 {
		w.Geometry = gfx.Geometry{}
		return
	}
	w.Geometry.Smooth = false
	_ = w.Geometry.Load(verts, indices, biomeWaterTexture, w.Position)
	w.Geometry.Alpha = w.Alpha
	w.Geometry.Water = w.Water
}
