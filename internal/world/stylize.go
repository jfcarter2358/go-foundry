package world

// stylize.go — a level's painted-miniature look: the `stylize:` block
// (experimental; docs/guide/38-stylized-lighting.md).
//
//	stylize:
//	  shadow_color: {r: 0.35, g: 0.42, b: 0.78}  # what the dark side turns
//	  shadow_tint: 0.55                         # how far it turns, 0..1
//	  shadow_fill: 0.06                         # cool light lifted into shadow
//	  highlight_color: {r: 1, g: 0.85, b: 0.62} # what the lit side warms to
//	  highlight_tint: 0.4
//	  shadow_point: 0.05                        # light below this is shadow
//	  light_point: 0.6                          # light above this is highlight
//	  edge_color: {r: 1, g: 0.93, b: 0.8}       # convex edges, picked out
//	  edge: 0.8
//	  edge_width: 1.5                           # pixels at 1080 lines
//	  wash_color: {r: 0.32, g: 0.24, b: 0.18}   # recesses, darkened
//	  wash: 0.5
//	  drybrush_color: {r: 0.95, g: 0.9, b: 0.8} # raised detail, scuffed light
//	  drybrush: 0.35
//	  drybrush_scale: 6                         # strokes per world unit
//	  drybrush_threshold: 0.3                   # how raised before it catches
//	  fade_distance: 80                         # the paint effects end here
//	  disabled: true                            # keeps the numbers, draws plain
//
// It is two halves, and they live in two places:
//
//   - The LIGHT RAMP (shadow_*, highlight_*, the two points) bends the light
//     every lit surface takes before its colour multiplies it: dark turns
//     cool rather than black, bright turns warm. It is light_model.glsl's
//     stylizeLight, packed into the scene block (packStylize), so every pass
//     that lights a surface — the world program, the visibility buffer's
//     material pass, terrain, foliage, grass, sprites — takes it the same way,
//     on either frame path.
//   - The PAINT (edge, wash, drybrush) is a screen pass over the frame's
//     normals (render/stylize.go, stylize_frag.glsl): the surfaces' normals
//     tell convex from concave, so a ridge is highlighted, a crease washed and
//     raised detail dry-brushed. It needs the scene target's normal buffer,
//     so it runs only in a scene-target frame (TAA mode, which the editor's
//     filled view also takes).
//
// A level without the block draws exactly as before; the editor's View menu
// and the Environment section are what create and switch it.

import (
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// Stylize is a level's painted look.
type Stylize struct {
	// Enabled is false for a block switched off with its numbers kept.
	Enabled bool

	// The light ramp.
	ShadowColor    mgl32.Vec3
	ShadowTint     float32
	ShadowFill     float32
	HighlightColor mgl32.Vec3
	HighlightTint  float32
	ShadowPoint    float32
	LightPoint     float32

	// The paint.
	EdgeColor         mgl32.Vec3
	Edge              float32
	EdgeWidth         float32
	WashColor         mgl32.Vec3
	Wash              float32
	DrybrushColor     mgl32.Vec3
	Drybrush          float32
	DrybrushScale     float32
	DrybrushThreshold float32
	FadeDistance      float32
}

// DefaultStylize is a fresh block's look: blue-violet shadow, warm light,
// bone highlights on the edges and a brown wash in the recesses.
func DefaultStylize() Stylize {
	return Stylize{
		Enabled:           true,
		ShadowColor:       mgl32.Vec3{0.35, 0.42, 0.78},
		ShadowTint:        0.55,
		ShadowFill:        0.06,
		HighlightColor:    mgl32.Vec3{1, 0.85, 0.62},
		HighlightTint:     0.4,
		ShadowPoint:       0.05,
		LightPoint:        0.6,
		EdgeColor:         mgl32.Vec3{1, 0.93, 0.8},
		Edge:              0.8,
		EdgeWidth:         1.5,
		WashColor:         mgl32.Vec3{0.32, 0.24, 0.18},
		Wash:              0.5,
		DrybrushColor:     mgl32.Vec3{0.95, 0.9, 0.8},
		Drybrush:          0.35,
		DrybrushScale:     6,
		DrybrushThreshold: 0.3,
		FadeDistance:      80,
	}
}

// RawStylize is the YAML block. Every number a pointer, so an absent key is
// the default and a deliberate 0 (no wash at all) survives.
type RawStylize struct {
	// Disabled keeps the block's numbers but draws the level plain.
	Disabled bool `yaml:"disabled,omitempty"`
	// ShadowColor is the hue the dark side of a surface turns toward.
	ShadowColor *RawColor `yaml:"shadow_color,omitempty"`
	// ShadowTint is how far shadow turns toward ShadowColor, 0..1. Default 0.55.
	ShadowTint *float32 `yaml:"shadow_tint,omitempty"`
	// ShadowFill is ShadowColor light added where a surface is in shadow, so
	// it never goes black. Default 0.06.
	ShadowFill *float32 `yaml:"shadow_fill,omitempty"`
	// HighlightColor is the hue the lit side of a surface warms toward.
	HighlightColor *RawColor `yaml:"highlight_color,omitempty"`
	// HighlightTint is how far bright light turns toward HighlightColor, 0..1. Default 0.4.
	HighlightTint *float32 `yaml:"highlight_tint,omitempty"`
	// ShadowPoint is the light, as shown on screen (0..1), at and below which a surface counts as fully in shadow. Default 0.05.
	ShadowPoint *float32 `yaml:"shadow_point,omitempty"`
	// LightPoint is the light at and above which a surface takes the full highlight. Default 0.6.
	LightPoint *float32 `yaml:"light_point,omitempty"`
	// EdgeColor is the paint picked out along convex edges.
	EdgeColor *RawColor `yaml:"edge_color,omitempty"`
	// Edge is the edge highlight's strength, 0 for none. Default 0.8.
	Edge *float32 `yaml:"edge,omitempty"`
	// EdgeWidth is how wide an edge highlight is, in pixels of a 1080-line frame. Default 1.5.
	EdgeWidth *float32 `yaml:"edge_width,omitempty"`
	// WashColor is the shade pooled into creases and recesses.
	WashColor *RawColor `yaml:"wash_color,omitempty"`
	// Wash is the recess shade's strength, 0 for none. Default 0.5.
	Wash *float32 `yaml:"wash,omitempty"`
	// DrybrushColor is the light paint scuffed over raised detail.
	DrybrushColor *RawColor `yaml:"drybrush_color,omitempty"`
	// Drybrush is the dry-brush strength, 0 for none. Default 0.35.
	Drybrush *float32 `yaml:"drybrush,omitempty"`
	// DrybrushScale is how many brush strokes fit in a world unit. Default 6.
	DrybrushScale *float32 `yaml:"drybrush_scale,omitempty"`
	// DrybrushThreshold is how raised detail must be before the brush catches it, 0..1. Default 0.3.
	DrybrushThreshold *float32 `yaml:"drybrush_threshold,omitempty"`
	// FadeDistance is how far from the camera, in world units, the edge, wash and dry-brush fade out. Default 80.
	FadeDistance *float32 `yaml:"fade_distance,omitempty"`
}

// StylizeFromRaw is the block's Stylize, or nil for no block.
func StylizeFromRaw(r *RawStylize) *Stylize {
	if r == nil {
		return nil
	}
	s := DefaultStylize()
	s.Enabled = !r.Disabled
	col := func(dst *mgl32.Vec3, c *RawColor) {
		if c != nil {
			*dst = mgl32.Vec3{c.R, c.G, c.B}
		}
	}
	num := func(dst *float32, v *float32) {
		if v != nil {
			*dst = *v
		}
	}
	col(&s.ShadowColor, r.ShadowColor)
	num(&s.ShadowTint, r.ShadowTint)
	num(&s.ShadowFill, r.ShadowFill)
	col(&s.HighlightColor, r.HighlightColor)
	num(&s.HighlightTint, r.HighlightTint)
	num(&s.ShadowPoint, r.ShadowPoint)
	num(&s.LightPoint, r.LightPoint)
	col(&s.EdgeColor, r.EdgeColor)
	num(&s.Edge, r.Edge)
	num(&s.EdgeWidth, r.EdgeWidth)
	col(&s.WashColor, r.WashColor)
	num(&s.Wash, r.Wash)
	col(&s.DrybrushColor, r.DrybrushColor)
	num(&s.Drybrush, r.Drybrush)
	num(&s.DrybrushScale, r.DrybrushScale)
	num(&s.DrybrushThreshold, r.DrybrushThreshold)
	num(&s.FadeDistance, r.FadeDistance)
	s.Clamp()
	return &s
}

// StylizeToRaw is s's block, nil for nil: defaults are left out.
func StylizeToRaw(s *Stylize) *RawStylize {
	if s == nil {
		return nil
	}
	d := DefaultStylize()
	r := &RawStylize{Disabled: !s.Enabled}
	col := func(v, def mgl32.Vec3) *RawColor {
		if v == def {
			return nil
		}
		return &RawColor{R: v.X(), G: v.Y(), B: v.Z()}
	}
	num := func(v, def float32) *float32 {
		if v == def {
			return nil
		}
		return &v
	}
	r.ShadowColor = col(s.ShadowColor, d.ShadowColor)
	r.ShadowTint = num(s.ShadowTint, d.ShadowTint)
	r.ShadowFill = num(s.ShadowFill, d.ShadowFill)
	r.HighlightColor = col(s.HighlightColor, d.HighlightColor)
	r.HighlightTint = num(s.HighlightTint, d.HighlightTint)
	r.ShadowPoint = num(s.ShadowPoint, d.ShadowPoint)
	r.LightPoint = num(s.LightPoint, d.LightPoint)
	r.EdgeColor = col(s.EdgeColor, d.EdgeColor)
	r.Edge = num(s.Edge, d.Edge)
	r.EdgeWidth = num(s.EdgeWidth, d.EdgeWidth)
	r.WashColor = col(s.WashColor, d.WashColor)
	r.Wash = num(s.Wash, d.Wash)
	r.DrybrushColor = col(s.DrybrushColor, d.DrybrushColor)
	r.Drybrush = num(s.Drybrush, d.Drybrush)
	r.DrybrushScale = num(s.DrybrushScale, d.DrybrushScale)
	r.DrybrushThreshold = num(s.DrybrushThreshold, d.DrybrushThreshold)
	r.FadeDistance = num(s.FadeDistance, d.FadeDistance)
	return r
}

// Clamp holds every number inside what the shaders make sense of: the
// strengths and tints 0..1 (the edge and dry-brush up to 2), the two points
// in order with room between them, a width of at least half a pixel.
func (s *Stylize) Clamp() {
	c := func(v *float32, lo, hi float32) { *v = min(max(*v, lo), hi) }
	c(&s.ShadowTint, 0, 1)
	c(&s.ShadowFill, 0, 1)
	c(&s.HighlightTint, 0, 1)
	c(&s.ShadowPoint, 0, 4)
	c(&s.LightPoint, s.ShadowPoint+0.01, 8)
	c(&s.Edge, 0, 2)
	c(&s.EdgeWidth, 0.5, 8)
	c(&s.Wash, 0, 1)
	c(&s.Drybrush, 0, 2)
	c(&s.DrybrushScale, 0.01, 200)
	c(&s.DrybrushThreshold, 0, 1)
	c(&s.FadeDistance, 1, 100000)
}

// LevelStylize is lvl's look when it is on, nil otherwise.
func LevelStylize(lvl *Level) *Stylize {
	if lvl == nil || lvl.Stylize == nil || !lvl.Stylize.Enabled {
		return nil
	}
	return lvl.Stylize
}

// packStylize writes the light ramp into the scene block: zero (off) for a
// level without the look.
func packStylize(b *gfx.SceneBlock, lvl *Level) {
	s := LevelStylize(lvl)
	if s == nil {
		b.StyShadow, b.StyHighlight, b.StyRamp = [4]float32{}, [4]float32{}, [4]float32{}
		return
	}
	b.StyShadow = [4]float32{s.ShadowColor[0], s.ShadowColor[1], s.ShadowColor[2], s.ShadowTint}
	b.StyHighlight = [4]float32{s.HighlightColor[0], s.HighlightColor[1], s.HighlightColor[2], s.HighlightTint}
	b.StyRamp = [4]float32{s.ShadowPoint, s.LightPoint, s.ShadowFill, 1}
}
