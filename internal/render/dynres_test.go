package render

import (
	"testing"

	"foundry/internal/gfx"
)

// TestDynamicResolutionSteps: frames over the target step the scale down
// toward the one that would meet it; frames well under it step it back up a
// step at a time, never past render_scale or below the minimum.
func TestDynamicResolutionSteps(t *testing.T) {
	prev := dynRes
	prevS, prevD, prevT, prevM := gfx.RenderScale, gfx.DynamicResolution, gfx.DynamicResolutionTargetMs, gfx.DynamicResolutionMin
	t.Cleanup(func() {
		dynRes = prev
		gfx.RenderScale, gfx.DynamicResolution, gfx.DynamicResolutionTargetMs, gfx.DynamicResolutionMin = prevS, prevD, prevT, prevM
	})
	gfx.RenderScale, gfx.DynamicResolution, gfx.DynamicResolutionTargetMs, gfx.DynamicResolutionMin = 1, true, 15, 0.5
	dynRes = dynResState{scale: 1}
	// A frame of 30 ms, all per pixel: half the time is scale 0.707.
	for i := 0; i < 20 && dynRes.scale == 1; i++ {
		dynResUpdate(30, 1)
	}
	if dynRes.scale > 0.7 || dynRes.scale < 0.6 {
		t.Fatalf("after 30 ms frames at 1.0 the scale is %.2f, want 0.6–0.7", dynRes.scale)
	}
	// Then cheap frames: up a step at a time, to the top and no further.
	for i := 0; i < 2000; i++ {
		s := dynRes.scale
		dynResUpdate(5, s)
	}
	if dynRes.scale != 1 {
		t.Errorf("after cheap frames the scale is %.2f, want 1", dynRes.scale)
	}
	// Ruinous frames: never below the minimum.
	for i := 0; i < 2000; i++ {
		dynResUpdate(200, dynRes.scale)
	}
	if dynRes.scale < 0.5-1e-4 {
		t.Errorf("the scale went to %.2f, under the minimum 0.5", dynRes.scale)
	}
	if got := frameRenderScale(WorldDraw{Editor: true}, true); got != 1 {
		t.Errorf("the editor's view drew at %.2f", got)
	}
}
