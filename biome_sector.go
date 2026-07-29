package main

// biome_sector.go — biome-paint data model.
//
// A BiomeSector is a user-drawn polygon on its own layer, independent of the
// structural sector geometry, painted with exactly one BiomeBrush (kind,
// density, draw range, color, and kind-specific appearance). Because a
// sector owns its brush outright (not a shared/frozen snapshot referenced by
// cells), editing a sector's brush only ever affects that one sector — there
// is no dedup/freeze bookkeeping needed. Sectors may overlap in world space;
// each renders independently, which is how multiple biome looks (e.g. tall +
// short grass) get layered together.

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"

	"github.com/go-gl/mathgl/mgl32"
)

// Biome kinds a brush can scatter.
const (
	BiomeGrass int32 = iota
	BiomeRock
)

// Default camera-ring draw radii for the GPU-scattered biomes (world units).
const (
	defaultGrassRange = float32(45)
	defaultRockRange  = float32(90)
)

// Default appearance of the GPU-scattered biomes.
const (
	defaultGrassHeight  = float32(0.55)
	defaultGrassWidth   = float32(0.045)
	defaultRockSides    = int32(4) // octahedron (8 faces)
	defaultRockScale    = float32(0.35)
	defaultBiomeDensity = float32(0.8)
)

// biomeMaxPolyVerts caps how many points a single BiomeSector polygon can
// have — generous for a hand-drawn shape, and mirrored as MAX_POLY in
// biome_vert.glsl (the uniform array the polygon is uploaded into).
const biomeMaxPolyVerts = 64

// BiomeBrush is the paint configuration assigned to a BiomeSector. Unlike the
// old frozen-layer model, this lives directly on the sector it paints, so
// editing it only ever affects that sector.
type BiomeBrush struct {
	Kind        int32      // BiomeGrass / BiomeRock
	Density     float32    // 0..1 survival probability, uniform across the polygon
	Range       float32    // camera-ring draw distance (world units)
	Color       mgl32.Vec3 // paint color: becomes the rendered blade/rock color directly
	GrassHeight float32    // grass blade height (world units)
	GrassWidth  float32    // grass blade base half-width (world units)
	RockSides   int32      // rock equatorial vertex count (faces == 2*RockSides)
	RockScale   float32    // rock world scale (unit blob size)
}

// BiomeSector is a user-drawn polygon painted with one brush. Multiple
// sectors may overlap; each is rendered as its own instanced pass.
type BiomeSector struct {
	ID     string
	Points []mgl32.Vec2 // world-space polygon (CCW), independent of structural geometry
	Brush  BiomeBrush

	// HeightDirty is runtime-only (never serialized): true whenever Points
	// has changed since the last render, so the renderer knows to rebuild
	// this sector's cached height texture from current floor geometry. Also
	// force-set true to manually resync after unrelated structural edits.
	HeightDirty bool
}

// Bounds returns the world-XZ AABB of the sector's polygon.
func (s *BiomeSector) Bounds() (min, max mgl32.Vec2) {
	if len(s.Points) == 0 {
		return
	}
	min, max = s.Points[0], s.Points[0]
	for _, p := range s.Points[1:] {
		if p.X() < min.X() {
			min[0] = p.X()
		}
		if p.Y() < min.Y() {
			min[1] = p.Y()
		}
		if p.X() > max.X() {
			max[0] = p.X()
		}
		if p.Y() > max.Y() {
			max[1] = p.Y()
		}
	}
	return
}

// biomeSeed derives a deterministic per-sector jitter/survival-pick seed from
// the sector's ID and its brush settings. Two sectors with different brush
// configs (or just different IDs) decorrelate their scatter lattices, so
// e.g. tall grass and short grass painted over the same area don't land on
// identical spots.
func biomeSeed(s *BiomeSector) mgl32.Vec2 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s.ID))
	_ = binary.Write(h, binary.LittleEndian, s.Brush)
	v := h.Sum64()
	a := float32(v%1_000_003) / 1_000_003.0
	b := float32((v/1_000_003)%1_000_003) / 1_000_003.0
	return mgl32.Vec2{a * 1000, b * 1000}
}

// nextBiomeSectorID returns a default ID unique within ch's existing biome
// sectors (IDs only need to be unique per chunk, not level-wide).
func nextBiomeSectorID(ch *Chunk) string {
	for n := len(ch.BiomeSectors); ; n++ {
		id := fmt.Sprintf("biome_%d", n)
		if !biomeSectorIDTaken(ch, id) {
			return id
		}
	}
}

func biomeSectorIDTaken(ch *Chunk, id string) bool {
	for i := range ch.BiomeSectors {
		if ch.BiomeSectors[i].ID == id {
			return true
		}
	}
	return false
}
