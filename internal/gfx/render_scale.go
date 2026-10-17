package gfx

// render_scale.go — the player's render scale and dynamic resolution
// (docs/adventure/PHASE_F.md, Phase F2).
//
// A TAA frame can draw the world at a fraction of the output's size and let
// the TAA resolve reconstruct the output from the jittered samples of several
// frames (render/taa.go's upsampling resolve) — what Unreal's TSR does for
// its Nanite scenes. The HUD and every UI layer are drawn after, at the
// output's own size.
//
//	RenderScale            graphics.yaml render_scale, 0.5–1.0 (1: off)
//	DynamicResolution      graphics.yaml dynamic_resolution: the scale moves
//	                       each frame, between DynamicResolutionMin and
//	                       RenderScale, to hold the world's GPU time at
//	                       DynamicResolutionTargetMs
//	FOUNDRY_RENDER_SCALE   outranks render_scale for the run
//	FOUNDRY_DYNAMIC_RES    1/0 outranks dynamic_resolution for the run
//
// Only TAA frames scale (gfx.AntiAliasMode taa); MSAA and the editor's own
// views always draw at the output's size.

import (
	"os"
	"strconv"
)

// The render scale's bounds.
const (
	RenderScaleMin = 0.5
	RenderScaleMax = 1.0
)

// RenderScale is the share of the output's width (and height) the world is
// drawn at in a TAA frame. 1 draws at the output's size, as always.
var RenderScale float32 = 1

// DynamicResolution moves the scale each frame (render/dynres.go) to hold
// the world's GPU time at DynamicResolutionTargetMs.
var DynamicResolution bool

// DynamicResolutionMin is the least scale dynamic resolution goes to; the
// most is RenderScale.
var DynamicResolutionMin float32 = 0.5

// DynamicResolutionTargetMs is the world's GPU time per frame dynamic
// resolution aims at, in milliseconds (a little under 16.6 for 60 fps).
var DynamicResolutionTargetMs float32 = 15

// RenderScaleForced / DynamicResolutionForced: the environment set them for
// the run, and graphics.yaml does not override them.
var RenderScaleForced, DynamicResolutionForced bool

func init() {
	if v := os.Getenv("FOUNDRY_RENDER_SCALE"); v != "" {
		if f, err := strconv.ParseFloat(v, 32); err == nil {
			RenderScale, RenderScaleForced = ClampRenderScale(float32(f)), true
		}
	}
	if v := os.Getenv("FOUNDRY_DYNAMIC_RES"); v != "" {
		DynamicResolution, DynamicResolutionForced = v == "1" || v == "true", true
	}
	if v := os.Getenv("FOUNDRY_DYNAMIC_RES_TARGET_MS"); v != "" {
		if f, err := strconv.ParseFloat(v, 32); err == nil {
			DynamicResolutionTargetMs = ClampDynamicResolutionTarget(float32(f))
		}
	}
	if v := os.Getenv("FOUNDRY_DYNAMIC_RES_MIN"); v != "" {
		if f, err := strconv.ParseFloat(v, 32); err == nil {
			DynamicResolutionMin = ClampRenderScale(float32(f))
		}
	}
}

// ClampRenderScale holds a scale to [RenderScaleMin, RenderScaleMax].
func ClampRenderScale(v float32) float32 {
	if v != v { // NaN
		return RenderScaleMax
	}
	return min(max(v, RenderScaleMin), RenderScaleMax)
}

// ClampDynamicResolutionTarget holds a target to 4–100 ms.
func ClampDynamicResolutionTarget(v float32) float32 {
	if v != v {
		return 15
	}
	return min(max(v, 4), 100)
}

// SetRenderScale changes RenderScale.
func SetRenderScale(v float32) {
	v = ClampRenderScale(v)
	if RenderScale == v {
		return
	}
	RenderScale = v
	GraphicsSettingsChanged = true
}

// SetDynamicResolution changes DynamicResolution.
func SetDynamicResolution(on bool) {
	if DynamicResolution == on {
		return
	}
	DynamicResolution = on
	GraphicsSettingsChanged = true
}

// SetDynamicResolutionTargetMs changes DynamicResolutionTargetMs.
func SetDynamicResolutionTargetMs(v float32) {
	v = ClampDynamicResolutionTarget(v)
	if DynamicResolutionTargetMs == v {
		return
	}
	DynamicResolutionTargetMs = v
	GraphicsSettingsChanged = true
}

// SetDynamicResolutionMin changes DynamicResolutionMin.
func SetDynamicResolutionMin(v float32) {
	v = ClampRenderScale(v)
	if DynamicResolutionMin == v {
		return
	}
	DynamicResolutionMin = v
	GraphicsSettingsChanged = true
}

// ShaftsQuality is the player's light shafts tier (graphics.yaml
// light_shafts; render/shafts.go): high marches the level's steps (halved
// under TAA below the High GI tier, as always), low half of that again, off
// none at all. A level still has to ask for shafts (fog: shafts:).
type ShaftsQuality int

const (
	ShaftsOff ShaftsQuality = iota
	ShaftsLow
	ShaftsHigh
)

var shaftsQualityNames = [...]string{"off", "low", "high"}

func (q ShaftsQuality) String() string {
	if q < ShaftsOff || q > ShaftsHigh {
		return shaftsQualityNames[ShaftsHigh]
	}
	return shaftsQualityNames[q]
}

// ParseShaftsQuality reads a tier by name.
func ParseShaftsQuality(s string) (ShaftsQuality, bool) {
	for i, n := range shaftsQualityNames {
		if n == s {
			return ShaftsQuality(i), true
		}
	}
	return ShaftsHigh, false
}

// LightShaftsQuality is the tier in force; high (the old behaviour) by
// default.
var LightShaftsQuality = ShaftsHigh

// SetLightShaftsQuality changes LightShaftsQuality.
func SetLightShaftsQuality(q ShaftsQuality) {
	q = min(max(q, ShaftsOff), ShaftsHigh)
	if LightShaftsQuality == q {
		return
	}
	LightShaftsQuality = q
	GraphicsSettingsChanged = true
}
