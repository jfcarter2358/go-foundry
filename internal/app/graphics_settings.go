package app

// graphics_settings.go — the player's graphics settings, on disk.
//
// controls_settings.go's arrangement, for the graphics choices a game's
// settings screen can offer: Smooth Lighting (gfx.SmoothLighting), the
// foliage distance and shadows (gfx.FoliageDistance, gfx.FoliageShadows), the
// Nanite LOD threshold (gfx.NaniteLODError) and the live GI tier
// (gfx.GIQuality). The
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
	SmoothLighting  *bool    `yaml:"smooth_lighting,omitempty"`
	FoliageDistance *float32 `yaml:"foliage_distance,omitempty"`
	FoliageShadows  *bool    `yaml:"foliage_shadows,omitempty"`
	// NaniteLODError is gfx.NaniteLODError, pixels.
	NaniteLODError *float32 `yaml:"nanite_lod_error,omitempty"`
	// GIQuality is gfx.GIQuality by name: off, low, medium, high. Absent lets
	// the engine pick by GPU (gfx.PickDefaultGIQuality).
	GIQuality *string `yaml:"gi_quality,omitempty"`
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
	if raw.FoliageDistance != nil {
		gfx.FoliageDistance = min(max(*raw.FoliageDistance, gfx.FoliageDistanceMin), gfx.FoliageDistanceMax)
	}
	if raw.FoliageShadows != nil {
		gfx.FoliageShadows = *raw.FoliageShadows
	}
	if raw.NaniteLODError != nil {
		gfx.NaniteLODError = min(max(*raw.NaniteLODError, gfx.NaniteLODErrorMin), gfx.NaniteLODErrorMax)
	}
	if raw.GIQuality != nil {
		if q, ok := gfx.ParseGIQuality(*raw.GIQuality); ok {
			gfx.GIQuality, gfx.GIQualityChosen = q, true
		} else {
			log.Printf("graphics: unknown gi_quality %q (want off, low, medium or high)", *raw.GIQuality)
		}
	}
	return nil
}

func marshalGraphicsSettings() ([]byte, error) {
	smooth := gfx.SmoothLighting
	dist := gfx.FoliageDistance
	shadows := gfx.FoliageShadows
	lod := gfx.NaniteLODError
	raw := rawGraphics{SmoothLighting: &smooth, FoliageDistance: &dist, FoliageShadows: &shadows, NaniteLODError: &lod}
	// Only a tier someone chose is written: one the engine picked by GPU
	// stays a pick, and is picked again on whatever GPU runs it next.
	if gfx.GIQualityChosen {
		gi := gfx.GIQuality.String()
		raw.GIQuality = &gi
	}
	return yaml.Marshal(raw)
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
