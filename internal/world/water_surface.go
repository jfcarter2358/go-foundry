package world

// water_surface.go — the wave/reflection parameters shared by anything that
// draws an animated water plane.
//
// Water used to be one kind of placeable Primitive, and later something a
// Water-kind BiomeSector derived automatically. Both are gone; what survives
// here is the part that was never about placement — the parameters
// themselves, and their YAML conversion.
//
// The runtime WaterSurface type that used to live beside this — a mesh
// derived from a BiomeSector's polygon — goes with it. A brush-scattering
// Volume will need its own water-plane derivation in its place; see
// VolumeBiome (volume.go, planned).

import (
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// DefaultWaterDepth is how far below the surrounding ground a water surface
// sits, when nothing overrides it.
const DefaultWaterDepth = float32(0.6)

// WaterDepthLimit bounds how far a water surface can be moved from the ground
// it was sampled against, in either direction. It is a sanity rail on the
// depth slider and the height gizmo, not a physical constraint.
const WaterDepthLimit = float32(100)

// waterDepthEpsilon is the smallest magnitude NormalizeWaterDepth will hand
// back for a level that was asked for exactly at the ground. See its doc.
const waterDepthEpsilon = float32(0.0005)

// ResolvedWaterDepth applies the "0 means the default" convention (shared with
// every other zero-value-means-default field in this engine) to a water
// depth.
//
// Unlike most of those, a water depth is meaningful when NEGATIVE: depth is
// measured downward from whatever ground it was sampled against, so a
// negative one lifts the surface ABOVE that ground — flooding flat ground
// rather than filling a dug basin. Only exactly 0 is the sentinel, which is
// why this tests == 0 rather than the <= 0 other resolvers use.
func ResolvedWaterDepth(v float32) float32 {
	if v == 0 {
		return DefaultWaterDepth
	}
	return v
}

// NormalizeWaterDepth prepares a depth for storing: it clamps to
// +/-WaterDepthLimit and nudges an exact 0 aside, since 0 is
// ResolvedWaterDepth's "use the default" sentinel and would otherwise make a
// slider or gizmo drag jump to DefaultWaterDepth as it passed the ground. The
// nudge is far below what any "%.2f" readout can show.
func NormalizeWaterDepth(v float32) float32 {
	if v > WaterDepthLimit {
		v = WaterDepthLimit
	} else if v < -WaterDepthLimit {
		v = -WaterDepthLimit
	}
	if v == 0 {
		return waterDepthEpsilon
	}
	return v
}

// WaterParamsFromRaw converts the YAML form, falling back to defaults when the
// block is absent.
func WaterParamsFromRaw(rw *RawWater) gfx.WaterParams {
	if rw == nil {
		return gfx.DefaultWaterParams()
	}
	// The block carries the five original numbers; the rest of the look is
	// the default water's (material.DefaultWater).
	w := gfx.DefaultWaterParams()
	w.WaveAmplitude = rw.WaveAmplitude
	w.WaveLength = rw.WaveLength
	w.WaveSpeed = rw.WaveSpeed
	w.Reflectivity = rw.Reflectivity
	w.ReflectColor = mgl32.Vec3{rw.ReflectColor.R, rw.ReflectColor.G, rw.ReflectColor.B}
	return w
}
