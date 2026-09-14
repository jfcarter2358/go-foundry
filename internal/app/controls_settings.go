package app

// controls_settings.go — the player's mouse settings, on disk.
//
// The same arrangement as audio/audio_settings.go, for the same reason: a
// player who turns their sensitivity down expects it to stay down. The file is
// controls.yaml in the writable state directory (fileutil.StatePath), a
// missing file means defaults, and a malformed one is logged and ignored.
//
// Unlike the audio sliders, these are written by a script — the game's own
// settings screen — and a tick must not do file I/O. So the setters only raise
// platform.ControlSettingsChanged, and the frame loop saves once the value has
// stopped moving for controlsSaveDelay: a slider dragged across its whole
// range is one write, not one a frame.

import (
	"log"

	"foundry/internal/fileutil"
	"foundry/internal/platform"

	"gopkg.in/yaml.v2"
)

const controlsFileName = "controls.yaml"

// controlsSaveDelay is how long a change has to sit still before it is saved.
const controlsSaveDelay = float32(0.5)

// rawControls is the on-disk shape; pointers so a field missing from an older
// file keeps its default.
type rawControls struct {
	MouseSensitivity *float32 `yaml:"mouse_sensitivity,omitempty"`
	MouseSmoothing   *float32 `yaml:"mouse_smoothing,omitempty"`
	MouseInvertY     *bool    `yaml:"mouse_invert_y,omitempty"`
}

// loadControlSettings reads the saved mouse settings over the defaults.
func loadControlSettings() {
	data, err := fileutil.ReadState(controlsFileName)
	if err != nil {
		return
	}
	if err := applyControlSettings(data); err != nil {
		log.Printf("controls: %s: %v (using defaults)", controlsFileName, err)
	}
}

func applyControlSettings(data []byte) error {
	var raw rawControls
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.MouseSensitivity != nil && *raw.MouseSensitivity > 0 {
		platform.MouseSensitivity = *raw.MouseSensitivity
	}
	if raw.MouseSmoothing != nil {
		platform.MouseSmoothing = clamp01(*raw.MouseSmoothing)
	}
	if raw.MouseInvertY != nil {
		platform.MouseInvertY = *raw.MouseInvertY
	}
	return nil
}

func marshalControlSettings() ([]byte, error) {
	sens, smooth, inv := platform.MouseSensitivity, platform.MouseSmoothing, platform.MouseInvertY
	return yaml.Marshal(rawControls{MouseSensitivity: &sens, MouseSmoothing: &smooth, MouseInvertY: &inv})
}

// controlsUnsavedFor is how long the current unsaved change has been still.
var controlsUnsavedFor float32

// saveControlSettingsWhenSettled writes controls.yaml once a change has been
// left alone for controlsSaveDelay. Called once a frame from the frame loop.
func saveControlSettingsWhenSettled(dt float32) {
	if !platform.ControlSettingsChanged {
		controlsUnsavedFor = 0
		return
	}
	// A setter raising the flag again restarts the wait, so the file is written
	// after the last move of a drag.
	if controlsUnsavedFor == 0 {
		controlsUnsavedFor = dt
		return
	}
	controlsUnsavedFor += dt
	if controlsUnsavedFor < controlsSaveDelay {
		return
	}
	platform.ControlSettingsChanged = false
	controlsUnsavedFor = 0
	data, err := marshalControlSettings()
	if err == nil {
		err = fileutil.WriteFileAtomic(fileutil.StatePath(controlsFileName), data)
	}
	if err != nil {
		log.Printf("controls: saving %s: %v", controlsFileName, err)
	}
}
