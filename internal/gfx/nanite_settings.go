package gfx

// nanite_settings.go — the player's Nanite level-of-detail setting: how many
// pixels of error a cooked mesh's simplified clusters may show before the
// finer ones are drawn instead (render/nanite_cull.go). A player setting like
// FoliageDistance, saved to graphics.yaml (app/graphics_settings.go) and set
// through the setter so the save hears about it. Phase 2 of the Nanite plan
// (docs/nanite/PHASE_2.md).

// NaniteLODError is the largest error, in pixels of the frame's height, a
// drawn cluster may have: 1 — a simplification never moves the surface by
// more than a pixel — is the default and the picture; 2 to 4 trade a little
// shimmer at silhouettes for fewer triangles. Clamped to
// [NaniteLODErrorMin, NaniteLODErrorMax].
var NaniteLODError float32 = 1

const (
	NaniteLODErrorMin float32 = 0.25
	NaniteLODErrorMax float32 = 8
)

// SetNaniteLODError changes NaniteLODError.
func SetNaniteLODError(v float32) {
	v = min(max(v, NaniteLODErrorMin), NaniteLODErrorMax)
	if NaniteLODError == v {
		return
	}
	NaniteLODError = v
	GraphicsSettingsChanged = true
}
