package gfx

// gi_settings.go — the player's live GI quality (render/gi.go). A player
// setting like SmoothLighting, saved to graphics.yaml (app/graphics_settings.go)
// and set through SetGIQuality so the save hears about it.

import "strings"

// GIQualityLevel is one of the GI tiers. Off is exactly the lighting the
// engine had before GI: the flat ambient live, the bake's own bounce on a
// compiled level.
type GIQualityLevel int

const (
	GIOff GIQualityLevel = iota
	GILow
	GIMedium
	GIHigh
	giQualityCount
)

// GIQuality is the player's tier. Until something sets it — a saved
// graphics.yaml, a script, PickDefaultGIQuality — it is Medium.
var GIQuality = GIMedium

// GIQualityChosen is whether GIQuality came from somewhere that should stand —
// the player's saved setting or a script — rather than the built-in default,
// which PickDefaultGIQuality is free to replace once it knows the GPU.
var GIQualityChosen bool

var giQualityNames = [giQualityCount]string{"off", "low", "medium", "high"}

// String is the tier's name as graphics.yaml and scripts spell it.
func (q GIQualityLevel) String() string {
	if q < 0 || q >= giQualityCount {
		return giQualityNames[GIOff]
	}
	return giQualityNames[q]
}

// ParseGIQuality reads a tier's name, case-insensitively.
func ParseGIQuality(s string) (GIQualityLevel, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for i, n := range giQualityNames {
		if n == s {
			return GIQualityLevel(i), true
		}
	}
	return GIOff, false
}

// GIQualityNames lists the tiers, lowest first.
func GIQualityNames() []string { return giQualityNames[:] }

// SetGIQuality changes the tier, as the player's choice.
func SetGIQuality(q GIQualityLevel) {
	q = min(max(q, GIOff), GIHigh)
	GIQualityChosen = true
	if GIQuality == q {
		return
	}
	GIQuality = q
	GraphicsSettingsChanged = true
}

// PickDefaultGIQuality chooses the tier for a player who has never chosen one,
// from the GPU's name (gl.RENDERER): Medium on a discrete NVIDIA or AMD card,
// Off on anything else — an integrated GPU, a software renderer — where even
// Low costs more frame than it is worth. A choice already made is left alone.
func PickDefaultGIQuality(renderer string) {
	if GIQualityChosen {
		return
	}
	if DiscreteGPU(renderer) {
		GIQuality = GIMedium
	} else {
		GIQuality = GIOff
	}
}

// DiscreteGPU reports whether a gl.RENDERER string names a discrete card.
// WSL's D3D12 layer reports the card in brackets ("D3D12 (NVIDIA GeForce GTX
// 1080)"), which matches the same way.
func DiscreteGPU(renderer string) bool {
	r := strings.ToLower(renderer)
	for _, s := range []string{"nvidia", "geforce", "quadro", "radeon rx", "radeon pro", "radeon r9", "radeon vii", "arc a"} {
		if strings.Contains(r, s) {
			return true
		}
	}
	return false
}
