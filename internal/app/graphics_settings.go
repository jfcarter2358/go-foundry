package app

// graphics_settings.go — the player's graphics settings, on disk.
//
// controls_settings.go's arrangement, for the one graphics choice a game's
// settings screen offers so far: Smooth Lighting (gfx.SmoothLighting). The
// file is graphics.yaml in the writable state directory, a missing file means
// defaults, a malformed one is logged and ignored, and a script's change is
// saved once it has settled (saveSettingsWhenSettled).

import (
	"log"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"

	"gopkg.in/yaml.v2"
)

const graphicsFileName = "graphics.yaml"

// rawGraphics is the on-disk shape; pointers so a field missing from an older
// file keeps its default.
type rawGraphics struct {
	SmoothLighting *bool `yaml:"smooth_lighting,omitempty"`
}

// loadGraphicsSettings reads the saved graphics settings over the defaults.
// Before the first level loads, so the first batches are built with the
// player's choice rather than rebuilt a frame later.
func loadGraphicsSettings() {
	data, err := fileutil.ReadState(graphicsFileName)
	if err != nil {
		return
	}
	if err := applyGraphicsSettings(data); err != nil {
		log.Printf("graphics: %s: %v (using defaults)", graphicsFileName, err)
	}
}

func applyGraphicsSettings(data []byte) error {
	var raw rawGraphics
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.SmoothLighting != nil {
		gfx.SmoothLighting = *raw.SmoothLighting
	}
	return nil
}

func marshalGraphicsSettings() ([]byte, error) {
	smooth := gfx.SmoothLighting
	return yaml.Marshal(rawGraphics{SmoothLighting: &smooth})
}

var graphicsSave settledSave

// saveGraphicsWhenSettled writes graphics.yaml once a change has settled.
func saveGraphicsWhenSettled(dt float32) {
	if !graphicsSave.step(&gfx.GraphicsSettingsChanged, dt) {
		return
	}
	data, err := marshalGraphicsSettings()
	if err == nil {
		err = fileutil.WriteFileAtomic(fileutil.StatePath(graphicsFileName), data)
	}
	if err != nil {
		log.Printf("graphics: saving %s: %v", graphicsFileName, err)
	}
}
