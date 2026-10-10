package gfx

// foliage_settings.go — the player's foliage settings: how far painted
// foliage is drawn, and whether it casts shadows. Player settings like
// SmoothLighting, saved to graphics.yaml (app/graphics_settings.go) and set
// through the setters so the save hears about them.

// FoliageDistance scales every foliage draw distance (render/foliage.go):
// 1 is the default, 0.5 halves how far grass and trees reach — and with the
// area drawn, roughly quarters their cost. Clamped to
// [FoliageDistanceMin, FoliageDistanceMax].
var FoliageDistance float32 = 1

// FoliageShadows is whether trees, shrubs, rocks and ferns cast shadows.
var FoliageShadows = true

const (
	FoliageDistanceMin float32 = 0.25
	FoliageDistanceMax float32 = 2
)

// SetFoliageDistance changes FoliageDistance.
func SetFoliageDistance(v float32) {
	v = min(max(v, FoliageDistanceMin), FoliageDistanceMax)
	if FoliageDistance == v {
		return
	}
	FoliageDistance = v
	GraphicsSettingsChanged = true
}

// SetFoliageShadows changes FoliageShadows.
func SetFoliageShadows(on bool) {
	if FoliageShadows == on {
		return
	}
	FoliageShadows = on
	GraphicsSettingsChanged = true
}
