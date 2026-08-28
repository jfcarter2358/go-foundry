package gfx

// sprite_mode.go — how a billboard is oriented and how it blends.
//
// These live here, below everything that shares them, rather than beside
// world.Sprite where they started. Two things need them and neither can import
// the other: a level sprite (internal/world) authors them directly, and a
// particle effect layer (internal/particle) carries them in its .fpar document —
// and internal/world imports internal/particle, so the enums cannot live in
// world without dragging the level model down into the effect format.
//
// Both are rendering concerns anyway, which is what this package is for. The
// name tables are the single point of truth for the YAML and UI spelling of each
// value: adding a mode means adding its name here and nowhere else, so an
// unknown name cannot quietly load as the wrong one through some per-file
// switch that was never updated.

// BillboardMode is how a quad is oriented against the viewer.
type BillboardMode int

const (
	// BillboardFull faces the viewer outright, on both axes. Right for anything
	// with no up of its own: a glow, a puff, a spark.
	BillboardFull BillboardMode = iota
	// BillboardYAxis keeps world up as the quad's up and only yaws toward the
	// viewer. Right for anything standing on the ground — a bush leaning back as
	// the camera rises is the artifact this exists to avoid.
	BillboardYAxis
	// BillboardFixed does not billboard at all, leaving a quad facing wherever
	// its authored rotation puts it. Right for a poster on a wall.
	BillboardFixed
)

// BillboardModeNames is the YAML/UI name of each mode, indexed by the mode.
var BillboardModeNames = []string{
	"full",
	"y-axis",
	"fixed",
}

// BillboardModeName returns a mode's YAML/UI name, falling back to the default
// for anything out of range.
func BillboardModeName(m BillboardMode) string {
	if int(m) < 0 || int(m) >= len(BillboardModeNames) {
		return BillboardModeNames[BillboardFull]
	}
	return BillboardModeNames[m]
}

// BillboardModeFromName is the inverse. An unknown name loads as the default
// rather than failing, so a document written by a newer build still opens.
func BillboardModeFromName(name string) BillboardMode {
	for i, n := range BillboardModeNames {
		if n == name {
			return BillboardMode(i)
		}
	}
	return BillboardFull
}

// SpriteBlend is how a quad's texture is combined with what is behind it. It
// decides which pass the quad draws in, not merely how it is sorted — see
// render/sprite_render.go.
type SpriteBlend int

const (
	// SpriteBlendCutout is opaque with a hard alpha edge. It draws in the
	// opaque pass, writes depth, needs no sorting, and gets its silhouette
	// antialiased by the alpha-to-coverage that pass already runs. The right
	// default for foliage and any solid-looking sprite.
	SpriteBlendCutout SpriteBlend = iota
	// SpriteBlendAlpha is ordinary translucency: sorted back-to-front, no depth
	// write. For glass, ghosts, and anything that has to blend rather than cut.
	SpriteBlendAlpha
	// SpriteBlendAdditive adds light to the scene and never occludes. It is
	// order-independent, so it needs no sorting at all. For fire, glows, sparks.
	SpriteBlendAdditive
)

// SpriteBlendNames is the YAML/UI name of each blend mode, indexed by the mode.
var SpriteBlendNames = []string{
	"cutout",
	"alpha",
	"additive",
}

// SpriteBlendName returns a blend mode's YAML/UI name.
func SpriteBlendName(b SpriteBlend) string {
	if int(b) < 0 || int(b) >= len(SpriteBlendNames) {
		return SpriteBlendNames[SpriteBlendCutout]
	}
	return SpriteBlendNames[b]
}

// SpriteBlendFromName is the inverse, defaulting to cutout.
func SpriteBlendFromName(name string) SpriteBlend {
	for i, n := range SpriteBlendNames {
		if n == name {
			return SpriteBlend(i)
		}
	}
	return SpriteBlendCutout
}
