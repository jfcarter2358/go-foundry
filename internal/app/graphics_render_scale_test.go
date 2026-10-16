package app

import (
	"testing"

	"foundry/internal/gfx"
)

// TestRenderScaleSettingsRoundTrip: render_scale, dynamic resolution and
// light_shafts survive graphics.yaml, and an out-of-range scale is clamped
// (docs/adventure/PHASE_F.md, Phase F2).
func TestRenderScaleSettingsRoundTrip(t *testing.T) {
	prevS, prevD, prevT, prevM, prevQ := gfx.RenderScale, gfx.DynamicResolution, gfx.DynamicResolutionTargetMs,
		gfx.DynamicResolutionMin, gfx.LightShaftsQuality
	prevSF, prevDF := gfx.RenderScaleForced, gfx.DynamicResolutionForced
	t.Cleanup(func() {
		gfx.RenderScale, gfx.DynamicResolution, gfx.DynamicResolutionTargetMs = prevS, prevD, prevT
		gfx.DynamicResolutionMin, gfx.LightShaftsQuality = prevM, prevQ
		gfx.RenderScaleForced, gfx.DynamicResolutionForced = prevSF, prevDF
		gfx.GraphicsSettingsChanged = false
	})
	gfx.RenderScaleForced, gfx.DynamicResolutionForced = false, false
	if err := applyGraphicsSettings([]byte("render_scale: 0.2\ndynamic_resolution: true\ndynamic_resolution_target_ms: 14\n" +
		"dynamic_resolution_min: 0.6\nlight_shafts: low\n")); err != nil {
		t.Fatal(err)
	}
	if gfx.RenderScale != gfx.RenderScaleMin || !gfx.DynamicResolution || gfx.DynamicResolutionTargetMs != 14 ||
		gfx.DynamicResolutionMin != 0.6 || gfx.LightShaftsQuality != gfx.ShaftsLow {
		t.Fatalf("read back scale %g dynamic %v target %g min %g shafts %v", gfx.RenderScale, gfx.DynamicResolution,
			gfx.DynamicResolutionTargetMs, gfx.DynamicResolutionMin, gfx.LightShaftsQuality)
	}
	gfx.RenderScale = 0.75
	data, err := marshalGraphicsSettings()
	if err != nil {
		t.Fatal(err)
	}
	gfx.RenderScale, gfx.DynamicResolution, gfx.LightShaftsQuality = 1, false, gfx.ShaftsHigh
	if err := applyGraphicsSettings(data); err != nil {
		t.Fatal(err)
	}
	if gfx.RenderScale != 0.75 || !gfx.DynamicResolution || gfx.LightShaftsQuality != gfx.ShaftsLow {
		t.Errorf("came back scale %g dynamic %v shafts %v from:\n%s", gfx.RenderScale, gfx.DynamicResolution,
			gfx.LightShaftsQuality, data)
	}
}
