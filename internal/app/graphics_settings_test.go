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
