package app

import (
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
