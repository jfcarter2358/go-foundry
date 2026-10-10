package gfx

// antialias_settings.go — the player's anti-aliasing setting, and the
// sub-pixel jitter the temporal kind draws with. Phase 4 of the Nanite plan
// (docs/nanite/PHASE_4.md; the renderer's side is render/taa.go).
//
// Three modes:
//
//	taa   the world is drawn into the renderer's own scene target, one
//	      sample a pixel, from a camera nudged a fraction of a pixel each
//	      frame; a resolve pass blends each frame into a history reprojected
//	      along the frame's motion vectors, clamped to what the pixel's
//	      neighbours say is plausible, and sharpened. The visibility buffer
//	      (render/nanite_vis.go) needs this target, so this is also the
//	      mode cooked meshes and compiled levels shade once per pixel in.
//	msaa  the fallback: the world straight into the window's multisampled
//	      framebuffer, as it always was (render/antialias.go), alpha
//	      cutouts antialiased by alpha-to-coverage.
//	off   the window's framebuffer, multisampling off.
//
// Saved to graphics.yaml as `anti_aliasing` (app/graphics_settings.go), with
// the sharpening as `taa_sharpen`; scripts reach both through
// @level.prop.anti_aliasing and @level.prop.taa_sharpen. FOUNDRY_AA=taa|msaa|off
// overrides the saved choice for a run (tests, the benchmark).

import (
	"os"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

// AAMode is an anti-aliasing mode.
type AAMode int

const (
	AAModeTAA AAMode = iota
	AAModeMSAA
	AAModeOff
)

// String is the mode's name, as graphics.yaml and scripts spell it.
func (m AAMode) String() string {
	switch m {
	case AAModeMSAA:
		return "msaa"
	case AAModeOff:
		return "off"
	}
	return "taa"
}

// ParseAAMode reads a mode by name.
func ParseAAMode(s string) (AAMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "taa", "temporal":
		return AAModeTAA, true
	case "msaa", "multisample":
		return AAModeMSAA, true
	case "off", "none":
		return AAModeOff, true
	}
	return AAModeTAA, false
}

// DefaultAntiAliasMode is the mode a player who has never chosen one gets.
var DefaultAntiAliasMode = AAModeTAA

// AntiAliasMode is the mode the frame draws in. Set through SetAntiAliasMode,
// which marks the change for saving; taken at the next frame (the window's
// own samples, which MSAA needs, are fixed when the window is made — see
// render.CreateAntiAliasedWindow).
var AntiAliasMode = initialAAMode()

// AntiAliasModeForced is set when FOUNDRY_AA chose the mode, so a saved
// setting does not override the environment.
var AntiAliasModeForced bool

func initialAAMode() AAMode {
	if v := os.Getenv("FOUNDRY_AA"); v != "" {
		if m, ok := ParseAAMode(v); ok {
			AntiAliasModeForced = true
			return m
		}
	}
	return DefaultAntiAliasMode
}

// AntiAliasModeChosen is whether the player (or a saved file) chose the mode:
// only a chosen mode is saved, so a player who never picked one follows the
// engine's default as it changes.
var AntiAliasModeChosen bool

// SetAntiAliasMode changes AntiAliasMode, as the player's choice.
func SetAntiAliasMode(m AAMode) {
	if AntiAliasMode == m && AntiAliasModeChosen {
		return
	}
	AntiAliasMode, AntiAliasModeChosen = m, true
	GraphicsSettingsChanged = true
}

// TAASharpen is how much the TAA output is sharpened, 0 (none) to 1: the
// history's reprojection resamples every frame through a filter, which
// softens fine texture a little, and an unsharp mask of about this strength
// gives it back. Clamped to [0, 1].
var TAASharpen float32 = 0.25

// SetTAASharpen changes TAASharpen.
func SetTAASharpen(v float32) {
	v = min(max(v, 0), 1)
	if TAASharpen == v {
		return
	}
	TAASharpen = v
	GraphicsSettingsChanged = true
}

// ProjectionJitter is the sub-pixel offset, in normalised device units, the
// frame being drawn adds to every projection: zero outside a TAA frame.
// render.DrawWorld sets it for the length of the frame; ViewmodelProjection
// reads it, so the hands are jittered with the world they are resolved with.
var ProjectionJitter mgl32.Vec2

// JitterProjection is p moved by j in normalised device units: the offset is
// added in clip space before the divide, so a point lands j away on screen at
// any depth (the third column carries -z into w; for an orthographic
// projection the fourth does).
func JitterProjection(p mgl32.Mat4, j mgl32.Vec2) mgl32.Mat4 {
	if j == (mgl32.Vec2{}) {
		return p
	}
	if p[11] != 0 {
		// Perspective: clip.x += j.x * clip.w, and clip.w = p[11]*z (-z),
		// so the jitter rides on the z column.
		p[8] += j[0] * p[11]
		p[9] += j[1] * p[11]
		return p
	}
	p[12] += j[0]
	p[13] += j[1]
	return p
}
