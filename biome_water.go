package main

// biome_water.go — auto-manages a companion water-plane Primitive for every
// Water-kind BiomeSector, filling the painted polygon.
//
// Water paints differently from every other biome kind: instead of GPU
// scatter or a shaded ground patch, painting Water gets you an actual water
// *surface* — reusing the engine's existing animated-water primitive
// pipeline (waves, fresnel reflection, alpha-blended pass, shadows-off — see
// primitive.go/wall.go/main.go) rather than reinventing it. The companion
// primitive lives in Chunk.BiomeWaterPrims, a runtime-only slice kept in sync
// with BiomeSectors here — never in Chunk.Primitives, so it never shows up in
// the scenery/primitive editor or gets serialized (it's fully derived from
// the owning BiomeSector and rebuilt after load).
//
// The mesh is a grid over the sector's polygon AABB, clipped to the polygon
// the same way biome_ground.go's ground patch is (all four corners of a quad
// must fall inside), flat at local y=0 — wave displacement happens in the
// shared water vertex/fragment shader, same as a hand-placed water
// primitive. The sector's own ground patch (rendered separately, see
// biome_ground.go) acts as the lakebed beneath it.

import (
	"github.com/go-gl/mathgl/mgl32"
)

// SyncBiomeWaterPrimitives keeps every chunk's BiomeWaterPrims in step with
// its Water-kind BiomeSectors: adds a companion primitive for a newly-painted
// or newly-switched-to-Water sector, rebuilds one whose polygon or water
// params changed, and drops one whose sector was deleted or switched to a
// different kind.
//
// Must run early in the frame, before the main primitive draw loop picks up
// chunk.BiomeWaterPrims (main.go) — and before RenderBiomeGround, which is
// what actually clears BiomeSector.GroundDirty once every mesh derived from
// it this frame (this sync and the ground patch) has had a chance to see it
// dirty.
func SyncBiomeWaterPrimitives(level *Level) {
	if level == nil {
		return
	}
	for ci := range level.Chunks {
		syncChunkWaterPrimitives(level, &level.Chunks[ci])
	}
}

func syncChunkWaterPrimitives(level *Level, ch *Chunk) {
	if len(ch.BiomeSectors) == 0 && len(ch.BiomeWaterPrims) == 0 {
		return
	}

	want := map[string]*BiomeSector{}
	for si := range ch.BiomeSectors {
		s := &ch.BiomeSectors[si]
		if s.Brush.Kind == BiomeWater && len(s.Points) >= 3 {
			want[s.ID] = s
		}
	}

	// Drop companion prims whose sector is gone or no longer Water.
	kept := ch.BiomeWaterPrims[:0]
	for i := range ch.BiomeWaterPrims {
		p := &ch.BiomeWaterPrims[i]
		if _, ok := want[p.ID]; !ok {
			p.Geometry.Delete()
			continue
		}
		kept = append(kept, *p)
	}
	ch.BiomeWaterPrims = kept

	// Add missing / rebuild dirty.
	for id, s := range want {
		idx := -1
		for i := range ch.BiomeWaterPrims {
			if ch.BiomeWaterPrims[i].ID == id {
				idx = i
				break
			}
		}
		isNew := idx < 0
		if isNew {
			ch.BiomeWaterPrims = append(ch.BiomeWaterPrims, Primitive{ID: id, ChunkID: ch.ID})
			idx = len(ch.BiomeWaterPrims) - 1
		}
		p := &ch.BiomeWaterPrims[idx]
		if isNew || s.GroundDirty || p.Geometry.Vao == 0 {
			rebuildBiomeWaterPrimitive(level, s, p)
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
func biomeWaterCentroidAndFloor(level *Level, s *BiomeSector) (mgl32.Vec2, float32) {
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
func buildBiomeWaterMesh(s *BiomeSector, centroid mgl32.Vec2) ([][]float32, []int) {
	boundary, allPts := biomeMeshPoints(s)
	if len(allPts) == 0 {
		return nil, nil
	}
	triIdx := delaunayTriangulate(allPts, boundary)
	if len(triIdx) == 0 {
		return nil, nil
	}

	const uvTile = 0.15
	verts := make([][]float32, len(allPts))
	for i, p := range allPts {
		verts[i] = []float32{p.X() - centroid.X(), 0, p.Y() - centroid.Y(), p.X() * uvTile, p.Y() * uvTile}
	}

	// Double-sided: both windings, so the surface is visible from below too
	// (matches the old grid mesh's behavior, and the primitive draw loop's
	// DoubleSided transparent sub-pass expects it).
	indices := make([]int, 0, len(triIdx)*2)
	indices = append(indices, triIdx...)
	for i := 0; i < len(triIdx); i += 3 {
		indices = append(indices, triIdx[i], triIdx[i+2], triIdx[i+1])
	}
	return verts, indices
}

// rebuildBiomeWaterPrimitive (re)generates p's geometry and water params from
// sector s, in place — mirrors the tail of Primitive.Load() since a polygon-
// shaped water plane needs custom geometry generatePlane/generateWaterPlane
// can't produce.
func rebuildBiomeWaterPrimitive(level *Level, s *BiomeSector, p *Primitive) {
	centroid, floorY := biomeWaterCentroidAndFloor(level, s)
	depth := s.Brush.WaterDepth
	if depth <= 0 {
		depth = defaultWaterDepth
	}
	verts, indices := buildBiomeWaterMesh(s, centroid)

	p.Kind = PrimitiveWater
	p.Texture = "water"
	p.Position = mgl32.Vec3{centroid.X(), floorY - depth, centroid.Y()}
	p.Rotation = mgl32.Vec3{}
	p.Scale = mgl32.Vec3{1, 1, 1}
	p.Alpha = 0.75
	p.Collision = false
	p.Water = s.Brush.Water
	if !p.Water.Enabled {
		p.Water = DefaultWaterParams()
	}

	min, max := s.Bounds()
	p.BoundingKind = BoundingAABB
	p.BoundHalfX = (max.X()-min.X())/2 + 1
	p.BoundHalfY = depth + 0.5
	p.BoundHalfZ = (max.Y()-min.Y())/2 + 1

	p.Geometry.Delete()
	_ = ensureTextureLoaded(p.Texture)
	if len(verts) == 0 || len(indices) == 0 {
		p.Geometry = Geometry{}
		return
	}
	p.Geometry.Smooth = false
	_ = p.Geometry.Load(verts, indices, p.Texture, p.Position)
	p.Geometry.Alpha = p.Alpha
	p.Geometry.Water = p.Water
}
