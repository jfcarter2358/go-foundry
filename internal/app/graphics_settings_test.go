package app

import (
	"strings"
	"testing"

	"foundry/internal/gfx"
)

// Smooth Lighting survives the file, and a file that does not mention it
// leaves it on — the default.
func TestSmoothLightingRoundTripsThroughGraphicsFile(t *testing.T) {
	t.Cleanup(func() { gfx.SmoothLighting, gfx.GraphicsSettingsChanged = true, false })

	gfx.SetSmoothLighting(false)
	if !gfx.GraphicsSettingsChanged {
		t.Error("turning Smooth Lighting off did not mark the settings for saving")
	}
	data, err := marshalGraphicsSettings()
	if err != nil {
		t.Fatal(err)
	}
	gfx.SmoothLighting = true
	if err := applyGraphicsSettings(data); err != nil {
		t.Fatal(err)
	}
	if gfx.SmoothLighting {
		t.Errorf("Smooth Lighting came back on from:\n%s", data)
	}

	gfx.SmoothLighting = true
	if err := applyGraphicsSettings([]byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if !gfx.SmoothLighting {
		t.Error("a file without smooth_lighting turned it off")
	}
}

// The GI tier survives the file once chosen; one the engine picked by GPU is
// not written, so it is picked again on the next machine.
func TestGIQualityRoundTripsOnlyOnceChosen(t *testing.T) {
	prevQ, prevChosen := gfx.GIQuality, gfx.GIQualityChosen
	t.Cleanup(func() {
		gfx.GIQuality, gfx.GIQualityChosen, gfx.GraphicsSettingsChanged = prevQ, prevChosen, false
	})

	gfx.GIQuality, gfx.GIQualityChosen = gfx.GIMedium, false
	data, err := marshalGraphicsSettings()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "gi_quality") {
		t.Errorf("a tier nobody chose was saved:\n%s", data)
	}

	gfx.SetGIQuality(gfx.GILow)
	data, err = marshalGraphicsSettings()
	if err != nil {
		t.Fatal(err)
	}
	gfx.GIQuality, gfx.GIQualityChosen = gfx.GIHigh, false
	if err := applyGraphicsSettings(data); err != nil {
		t.Fatal(err)
	}
	if gfx.GIQuality != gfx.GILow || !gfx.GIQualityChosen {
		t.Errorf("came back as %v (chosen %v) from:\n%s", gfx.GIQuality, gfx.GIQualityChosen, data)
	}
	gfx.PickDefaultGIQuality("llvmpipe (LLVM 21.1.8, 256 bits)")
	if gfx.GIQuality != gfx.GILow {
		t.Error("the GPU pick overrode a saved choice")
	}
}

// The Nanite LOD threshold survives the file, clamped on the way in; a file
// without it keeps the default.
func TestNaniteLODErrorRoundTripsThroughGraphicsFile(t *testing.T) {
	prev := gfx.NaniteLODError
	t.Cleanup(func() { gfx.NaniteLODError, gfx.GraphicsSettingsChanged = prev, false })

	gfx.SetNaniteLODError(3)
	if !gfx.GraphicsSettingsChanged {
		t.Error("changing the LOD threshold did not mark the settings for saving")
	}
	data, err := marshalGraphicsSettings()
	if err != nil {
		t.Fatal(err)
	}
	gfx.NaniteLODError = 1
	if err := applyGraphicsSettings(data); err != nil {
		t.Fatal(err)
	}
	if gfx.NaniteLODError != 3 {
		t.Errorf("came back as %g from:\n%s", gfx.NaniteLODError, data)
	}
	if err := applyGraphicsSettings([]byte("nanite_lod_error: 100\n")); err != nil {
		t.Fatal(err)
	}
	if gfx.NaniteLODError != gfx.NaniteLODErrorMax {
		t.Errorf("an out-of-range threshold came in as %g", gfx.NaniteLODError)
	}
	gfx.NaniteLODError = 1
	if err := applyGraphicsSettings([]byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if gfx.NaniteLODError != 1 {
		t.Error("a file without nanite_lod_error changed it")
	}
}

// The Nanite page pool's budget round-trips, is clamped, and an older file
// without it keeps the default (Phase 3).
func TestNaniteStreamPoolRoundTripsThroughGraphicsFile(t *testing.T) {
	prev := gfx.NaniteStreamPoolMB
	t.Cleanup(func() { gfx.NaniteStreamPoolMB, gfx.GraphicsSettingsChanged = prev, false })

	gfx.SetNaniteStreamPoolMB(96)
	if !gfx.GraphicsSettingsChanged {
		t.Error("changing the pool did not mark the settings for saving")
	}
	data, err := marshalGraphicsSettings()
	if err != nil {
		t.Fatal(err)
	}
	gfx.NaniteStreamPoolMB = 256
	if err := applyGraphicsSettings(data); err != nil {
		t.Fatal(err)
	}
	if gfx.NaniteStreamPoolMB != 96 {
		t.Errorf("came back as %d from:\n%s", gfx.NaniteStreamPoolMB, data)
	}
	if err := applyGraphicsSettings([]byte("nanite_stream_pool_mb: 1\n")); err != nil {
		t.Fatal(err)
	}
	if gfx.NaniteStreamPoolMB != gfx.NaniteStreamPoolMBMin {
		t.Errorf("an out-of-range pool came in as %d", gfx.NaniteStreamPoolMB)
	}
	gfx.NaniteStreamPoolMB = 256
	if err := applyGraphicsSettings([]byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if gfx.NaniteStreamPoolMB != 256 {
		t.Error("a file without nanite_stream_pool_mb changed it")
	}
}

// The anti-aliasing mode survives the file once chosen (one never chosen
// follows the engine's default), an unknown name is ignored, and the TAA
// sharpening round-trips clamped.
func TestAntiAliasingRoundTripsOnceChosen(t *testing.T) {
	prevM, prevChosen, prevS := gfx.AntiAliasMode, gfx.AntiAliasModeChosen, gfx.TAASharpen
	t.Cleanup(func() {
		gfx.AntiAliasMode, gfx.AntiAliasModeChosen, gfx.TAASharpen, gfx.GraphicsSettingsChanged = prevM, prevChosen, prevS, false
	})
	if gfx.AntiAliasModeForced {
		t.Skip("FOUNDRY_AA is set; the saved mode does not apply")
	}
	gfx.AntiAliasMode, gfx.AntiAliasModeChosen = gfx.DefaultAntiAliasMode, false
	data, err := marshalGraphicsSettings()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "anti_aliasing") {
		t.Errorf("a mode nobody chose was saved:\n%s", data)
	}
	gfx.SetAntiAliasMode(gfx.AAModeMSAA)
	gfx.SetTAASharpen(3)
	if !gfx.GraphicsSettingsChanged || gfx.TAASharpen != 1 {
		t.Fatalf("changed %v, sharpen %g", gfx.GraphicsSettingsChanged, gfx.TAASharpen)
	}
	gfx.TAASharpen = 0.5
	data, err = marshalGraphicsSettings()
	if err != nil {
		t.Fatal(err)
	}
	gfx.AntiAliasMode, gfx.AntiAliasModeChosen, gfx.TAASharpen = gfx.AAModeTAA, false, 0
	if err := applyGraphicsSettings(data); err != nil {
		t.Fatal(err)
	}
	if gfx.AntiAliasMode != gfx.AAModeMSAA || !gfx.AntiAliasModeChosen || gfx.TAASharpen != 0.5 {
		t.Errorf("came back %v (chosen %v), sharpen %g from:\n%s", gfx.AntiAliasMode, gfx.AntiAliasModeChosen, gfx.TAASharpen, data)
	}
	if err := applyGraphicsSettings([]byte("anti_aliasing: supersample\n")); err != nil {
		t.Fatal(err)
	}
	if gfx.AntiAliasMode != gfx.AAModeMSAA {
		t.Errorf("an unknown name changed the mode to %v", gfx.AntiAliasMode)
	}
	for _, name := range []string{"taa", "msaa", "off"} {
		m, ok := gfx.ParseAAMode(name)
		if !ok || m.String() != name {
			t.Errorf("%q parses as %v (%v)", name, m, ok)
		}
	}
}
