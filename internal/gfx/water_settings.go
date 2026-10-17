package gfx

// water_settings.go — the player's water quality (render/water_pass.go,
// resources/shaders/water.glsl; docs/terrain-paint/PHASE_W1.md). A player
// setting like GIQuality: saved to graphics.yaml (app/graphics_settings.go),
// set by a script through @level.prop.water_quality, and set through
// SetWaterQuality so the save hears about it.
//
//	low     the sky reflection and the depth colour: the scene's depth is
//	        copied, the water blends over the bed with Beer–Lambert alpha
//	medium  + refraction: the scene's colour is copied too, and the bed is
//	        seen bent through the ripples and tinted by the water
//	high    + screen-space reflection and caustics on the bed

import "strings"

// WaterQualityLevel is one of the water tiers.
type WaterQualityLevel int

const (
	WaterLow WaterQualityLevel = iota
	WaterMedium
	WaterHigh
	waterQualityCount
)

// WaterQuality is the player's tier. High until something sets it.
var WaterQuality = WaterHigh

// WaterQualityChosen is whether WaterQuality came from the player's saved
// setting or a script, so it is written back to graphics.yaml.
var WaterQualityChosen bool

var waterQualityNames = [waterQualityCount]string{"low", "medium", "high"}

// String is the tier's name as graphics.yaml and scripts spell it.
func (q WaterQualityLevel) String() string {
	if q < 0 || q >= waterQualityCount {
		return waterQualityNames[WaterHigh]
	}
	return waterQualityNames[q]
}

// ParseWaterQuality reads a tier's name, case-insensitively.
func ParseWaterQuality(s string) (WaterQualityLevel, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for i, n := range waterQualityNames {
		if n == s {
			return WaterQualityLevel(i), true
		}
	}
	return WaterHigh, false
}

// WaterQualityNames lists the tiers, lowest first.
func WaterQualityNames() []string { return waterQualityNames[:] }

// SetWaterQuality changes the tier, as the player's choice.
func SetWaterQuality(q WaterQualityLevel) {
	q = min(max(q, WaterLow), WaterHigh)
	WaterQualityChosen = true
	if WaterQuality == q {
		return
	}
	WaterQuality = q
	GraphicsSettingsChanged = true
}
