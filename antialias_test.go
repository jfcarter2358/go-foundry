package main

import (
	"strings"
	"testing"
)

// TestAntiAliasSampleTiersTerminate pins the property that makes an 8x MSAA
// request safe to ship: the fallback walk always ends somewhere that works.
// A tier list that stopped at, say, 2 would leave a driver with no
// multisampling at all — and CreateAntiAliasedWindow tries each tier for
// real, so "no tier succeeded" is a failed startup, not a downgrade.
func TestAntiAliasSampleTiersTerminate(t *testing.T) {
	if len(antiAliasSampleTiers) == 0 {
		t.Fatal("antiAliasSampleTiers is empty — CreateAntiAliasedWindow would never create a window")
	}
	if got := antiAliasSampleTiers[0]; got != AntiAliasSamples {
		t.Errorf("first tier is %dx, want AntiAliasSamples (%dx) — the preferred count must be tried first", got, AntiAliasSamples)
	}
	if got := antiAliasSampleTiers[len(antiAliasSampleTiers)-1]; got != 0 {
		t.Errorf("last tier is %dx, want 0 — the walk must end at a plain single-sampled window", got)
	}
	for i := 1; i < len(antiAliasSampleTiers); i++ {
		if antiAliasSampleTiers[i] >= antiAliasSampleTiers[i-1] {
			t.Errorf("tier %d (%dx) does not drop below tier %d (%dx) — the walk must strictly weaken its request",
				i, antiAliasSampleTiers[i], i-1, antiAliasSampleTiers[i-1])
		}
	}
}

// TestFragmentShaderSharpensCutoutsBehindUniform keeps the shader side of the
// cutout path in step with the Go side. AlphaSharpenUniform (main.go) is
// looked up by this exact name and is what scopes sharpening to the opaque
// pass; if the shader dropped the uniform, GetUniformLocation would quietly
// return -1 and every cutout would fade out at distance under mipmapping with
// nothing to point at.
func TestFragmentShaderSharpensCutoutsBehindUniform(t *testing.T) {
	src := readBiomeShader(t, "fragment.glsl")
	if !strings.Contains(src, "uniform int alphaSharpen;") {
		t.Error("fragment.glsl does not declare `uniform int alphaSharpen` — AlphaSharpenUniform in main.go has nothing to bind to")
	}
	if !strings.Contains(src, "if (alphaSharpen != 0) {") {
		t.Error("fragment.glsl does not gate its alpha sharpening on alphaSharpen — the transparent pass would have its blending quantised")
	}
	if !strings.Contains(src, "fwidth(finalAlpha)") {
		t.Error("fragment.glsl's cutout edge is not rescaled by fwidth — the edge would be a fixed width in texture space, so cutouts still thin out with distance")
	}
}

// TestGroundShaderResolvesSlopeCutoffToCoverage guards the biome patch's
// slope contour against sliding back to a bare discard. The contour runs
// through the interior of a triangle, so MSAA cannot see it on its own —
// per-sample coverage is the only thing antialiasing it.
func TestGroundShaderResolvesSlopeCutoffToCoverage(t *testing.T) {
	src := readBiomeShader(t, "ground_frag.glsl")
	if !strings.Contains(src, "fwidth(vCoverage)") {
		t.Error("ground_frag.glsl does not rescale vCoverage by fwidth — the slope cutoff is back to a hard, aliased edge")
	}
	if strings.Contains(src, "if (vCoverage < 0.5) {") {
		t.Error("ground_frag.glsl still discards on the raw vCoverage threshold — that edge writes whole pixels and cannot be antialiased")
	}
	if !strings.Contains(src, "outputColor = vec4(color, cov);") {
		t.Error("ground_frag.glsl does not emit the slope coverage as alpha — alpha-to-coverage has nothing to spend the pixel's samples on")
	}
}
