package world

// tonemap.go — a level's HDR shading and tone curve: the `tonemap:` block
// (gfx/hdr.go has the method and why; docs/canyon/PHASE_7.md the canyon's
// numbers).
//
//	tonemap:
//	  exposure: 1.1     # scales the scene's light before the curve; default 1
//	  auto: true        # meter the scene and adapt; exposure is then a compensation
//	  curve: aces       # the filmic curve; aces is the only one today
//	  disabled: true    # keeps the numbers, shades LDR as before
//
// Since the lighting pass (docs/lighting/PLAN.md, phase 4) every level shades
// in HDR in a frame drawn into the renderer's scene target (a TAA frame: the
// RGBA16F target): light above 1 is kept and the result mapped through
// exposure and the curve at the end; a frame drawn any other way (the
// editor's own view) stays LDR. A level without the block takes AUTOMATIC
// exposure (render/exposure.go: the scene metered, the eye adapting); a block
// that sets an exposure and says nothing of auto keeps that exposure fixed,
// as every level that set one was tuned for; `disabled: true` is the old LDR
// shading — the light clamped to [0, 1], no curve. gfx.HDRByDefault false
// (FOUNDRY_HDR=0) puts a level without the block back to LDR.

import "foundry/internal/gfx"

// Tonemap is a level's HDR setting.
type Tonemap struct {
	// Enabled is false for a block switched off with its numbers kept.
	Enabled bool
	// Exposure multiplies the scene's light before the curve (> 0).
	Exposure float32
	// Curve is the curve's name: TonemapACES.
	Curve string
	// Auto meters the scene and adapts the exposure (gfx.AutoExposure);
	// Exposure is then a compensation on top.
	Auto bool
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
	// Auto meters the scene and adapts the exposure, which is then a
	// compensation. Absent: on unless the block sets an exposure.
	Auto *bool `yaml:"auto,omitempty"`
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
	t.Auto = r.Exposure == nil
	if r.Auto != nil {
		t.Auto = *r.Auto
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
	// Auto is written when it differs from what its absence reads as.
	if t.Auto != (r.Exposure == nil) {
		a := t.Auto
		r.Auto = &a
	}
	if t.Curve != "" && t.Curve != TonemapACES {
		r.Curve = t.Curve
	}
	return r
}

// TonemapCurves is the curves a block may name.
var TonemapCurves = []string{TonemapACES}

// LevelHDR reports whether lvl shades in HDR in a scene-target frame, its
// exposure, and whether that is a compensation on the metered exposure
// (auto) rather than the exposure itself.
func LevelHDR(lvl *Level) (on bool, exposure float32, auto bool) {
	if lvl == nil {
		return false, 1, false
	}
	if lvl.Tonemap == nil {
		return gfx.HDRByDefault, 1, true
	}
	if !lvl.Tonemap.Enabled {
		return false, 1, false
	}
	e := lvl.Tonemap.Exposure
	if e <= 0 {
		e = DefaultTonemapExposure
	}
	return true, e, lvl.Tonemap.Auto
}

// packTonemap writes the HDR switch into the scene block: on only for a
// level that shades HDR and a frame that can hold it (gfx.HDRFrame), with
// the metered exposure (gfx.AutoExposure) under an auto level's compensation.
func packTonemap(b *gfx.SceneBlock, lvl *Level) {
	on, e, auto := LevelHDR(lvl)
	if !on || !gfx.HDRFrame {
		b.HdrMode, b.HdrExposure = 0, 1
		return
	}
	if auto {
		e *= gfx.AutoExposure
	}
	b.HdrMode, b.HdrExposure = 1, e
}
