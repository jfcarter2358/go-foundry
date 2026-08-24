package main

// water_surface.go — the animated water plane.
//
// Water used to be one kind of placeable Primitive. Primitives are gone from
// levels, and the only thing left that wants a water surface is a Water-kind
// BiomeSector, which derives one from the polygon it paints (biome_water.go).
// What survives here is the part that was never about placement: the wave and
// reflection parameters the shaders read, and the runtime mesh that carries
// them.

import (
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// waterParamsFromRaw converts the YAML form, falling back to defaults when the
// block is absent (sectors painted before these settings existed).
func waterParamsFromRaw(rw *RawWater) gfx.WaterParams {
	if rw == nil {
		return gfx.DefaultWaterParams()
	}
	return gfx.WaterParams{
		Enabled:       true,
		WaveAmplitude: rw.WaveAmplitude,
		WaveLength:    rw.WaveLength,
		WaveSpeed:     rw.WaveSpeed,
		Reflectivity:  rw.Reflectivity,
		ReflectColor:  mgl32.Vec3{rw.ReflectColor.R, rw.ReflectColor.G, rw.ReflectColor.B},
	}
}

// WaterSurface is one water plane derived from a Water-kind BiomeSector: a
// runtime-only mesh with the wave parameters that drive it.
//
// It is deliberately not placeable and never serialized — everything about it
// follows from the sector it belongs to, and biome_water.go rebuilds it from
// there whenever that sector changes. ID matches the owning sector's, which is
// how the sync finds the one to rebuild or drop.
//
// There is no rotation or scale: the mesh is generated in world proportions
// around Position, so a transform would only be a second way to say something
// the polygon already says.
type WaterSurface struct {
	ID       string
	ChunkID  string
	Position mgl32.Vec3
	Water    gfx.WaterParams
	Alpha    float32

	// Half-extents of the plane, used for frustum culling. Culling tests the
	// surface's own bounds rather than its owning sector's — a lake routinely
	// extends past the structural sector its centre happens to land in.
	BoundHalfX float32
	BoundHalfY float32
	BoundHalfZ float32

	Geometry gfx.Geometry
}

// ModelMatrix builds the model matrix for this surface — a translation, since
// the mesh already carries its own shape.
func (w *WaterSurface) ModelMatrix() mgl32.Mat4 {
	return mgl32.Translate3D(w.Position.X(), w.Position.Y(), w.Position.Z())
}

// WorldAABB returns the surface's world-space axis-aligned bounds, grown by the
// wave amplitude so a crest cannot poke outside the box it is culled against.
func (w *WaterSurface) WorldAABB() (mgl32.Vec3, mgl32.Vec3) {
	h := mgl32.Vec3{w.BoundHalfX, w.BoundHalfY, w.BoundHalfZ}
	if w.Water.Enabled {
		h[1] += w.Water.WaveAmplitude
	}
	return w.Position.Sub(h), w.Position.Add(h)
}
