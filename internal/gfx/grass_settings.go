package gfx

// grass_settings.go — the player's settings for the Poly Haven grass
// instancer (render/grass*.go): how much of it is drawn, how far, and how
// far from the camera it casts shadows. Player settings like
// FoliageDistance, saved to graphics.yaml (app/graphics_settings.go) and set
// through the setters so the save hears about them.

// GrassDensity is the fraction of grass clumps drawn, 0.25..1: every field
// is thinned evenly (in its fixed hashed order), and the survivors grow to
// keep the cover, so lower is faster and reads a little coarser rather than
// balder.
var GrassDensity float32 = 1

// GrassDistance scales how far grass is drawn (and where its meshes thin and
// it fades out), 0.25..2.
var GrassDistance float32 = 1

// GrassShadowDistance is how far from the camera grass casts shadows,
// metres; 0 turns its shadows off.
var GrassShadowDistance float32 = 14

const (
	GrassDensityMin        float32 = 0.25
	GrassDensityMax        float32 = 1
	GrassDistanceMin       float32 = 0.25
	GrassDistanceMax       float32 = 2
	GrassShadowDistanceMax float32 = 60
)

// ClampGrassDensity, ClampGrassDistance and ClampGrassShadowDistance bound a
// value to its setting's range.
func ClampGrassDensity(v float32) float32  { return min(max(v, GrassDensityMin), GrassDensityMax) }
func ClampGrassDistance(v float32) float32 { return min(max(v, GrassDistanceMin), GrassDistanceMax) }
func ClampGrassShadowDistance(v float32) float32 {
	return min(max(v, 0), GrassShadowDistanceMax)
}

// SetGrassDensity changes GrassDensity.
func SetGrassDensity(v float32) {
	if v = ClampGrassDensity(v); GrassDensity != v {
		GrassDensity = v
		GraphicsSettingsChanged = true
	}
}

// SetGrassDistance changes GrassDistance.
func SetGrassDistance(v float32) {
	if v = ClampGrassDistance(v); GrassDistance != v {
		GrassDistance = v
		GraphicsSettingsChanged = true
	}
}

// SetGrassShadowDistance changes GrassShadowDistance.
func SetGrassShadowDistance(v float32) {
	if v = ClampGrassShadowDistance(v); GrassShadowDistance != v {
		GrassShadowDistance = v
		GraphicsSettingsChanged = true
	}
}
