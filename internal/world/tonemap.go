package world

// tonemap.go — a level's HDR shading and tone curve: the `tonemap:` block
// (gfx/hdr.go has the method and why; docs/canyon/PHASE_7.md the canyon's
// numbers).
//
//	tonemap:
//	  exposure: 1.1     # scales the scene's light before the curve; default 1
//	  curve: aces       # the filmic curve; aces is the only one today
//	  disabled: true    # keeps the numbers, shades LDR as before
//
// A level without the block — every level made before it — shades exactly as
// it always has: the light clamped to [0, 1], no curve. With it, a frame
// drawn into the renderer's scene target (a TAA frame: the RGBA16F target)
// keeps light above 1 and maps the result through exposure and the curve at
// the end; a frame drawn any other way (the editor's own view) stays LDR. So
// the block is how a level allows suns brighter than 1 and gets highlights
// that roll off instead of clipping.

import "foundry/internal/gfx"

// Tonemap is a level's HDR setting.
type Tonemap struct {
	// Enabled is false for a block switched off with its numbers kept.
	Enabled bool
	// Exposure multiplies the scene's light before the curve (> 0).
	Exposure float32
	// Curve is the curve's name: TonemapACES.
	Curve string
}

// TonemapACES is the ACES filmic fit (gfx.ACESFilm).
const TonemapACES = "aces"

// DefaultTonemapExposure is an absent exposure's value.
const DefaultTonemapExposure = 1

// RawTonemap is the YAML block. Pointers so a deliberate value survives.
type RawTonemap struct {
	// Disabled keeps the block's numbers but shades LDR, as without it.
	Disabled bool `yaml:"disabled,omitempty"`
	// Exposure multiplies the scene's light before the curve. Default 1.
	Exposure *float32 `yaml:"exposure,omitempty"`
	// Curve is the tone curve: aces (the default and, today, the only one).
	Curve string `yaml:"curve,omitempty"`
}

// TonemapFromRaw is the block's Tonemap, or nil for no block.
func TonemapFromRaw(r *RawTonemap) *Tonemap {
	if r == nil {
		return nil
	}
	t := &Tonemap{Enabled: !r.Disabled, Exposure: DefaultTonemapExposure, Curve: TonemapACES}
	if r.Exposure != nil && *r.Exposure > 0 {
		t.Exposure = *r.Exposure
	}
	if r.Curve != "" {
		t.Curve = r.Curve
	}
	return t
}

// TonemapToRaw is t's block, nil for nil: defaults are left out.
func TonemapToRaw(t *Tonemap) *RawTonemap {
	if t == nil {
		return nil
	}
	r := &RawTonemap{Disabled: !t.Enabled}
	if t.Exposure != DefaultTonemapExposure && t.Exposure > 0 {
		e := t.Exposure
		r.Exposure = &e
	}
	if t.Curve != "" && t.Curve != TonemapACES {
		r.Curve = t.Curve
	}
	return r
}

// TonemapCurves is the curves a block may name.
var TonemapCurves = []string{TonemapACES}

// LevelHDR reports whether lvl shades in HDR in a scene-target frame, and its
// exposure.
func LevelHDR(lvl *Level) (bool, float32) {
	if lvl == nil || lvl.Tonemap == nil || !lvl.Tonemap.Enabled {
		return false, 1
	}
	e := lvl.Tonemap.Exposure
	if e <= 0 {
		e = DefaultTonemapExposure
	}
	return true, e
}

// packTonemap writes the HDR switch into the scene block: on only for a
// level that asks and a frame that can hold it (gfx.HDRFrame).
func packTonemap(b *gfx.SceneBlock, lvl *Level) {
	on, e := LevelHDR(lvl)
	if !on || !gfx.HDRFrame {
		b.HdrMode, b.HdrExposure = 0, 1
		return
	}
	b.HdrMode, b.HdrExposure = 1, e
}
