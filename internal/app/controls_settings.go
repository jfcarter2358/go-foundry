package app

// controls_settings.go — the player's mouse settings and key bindings, on disk.
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
// range is one write, not one a frame. A script's change to the audio gains is
// saved the same way, to audio.yaml.

import (
	"log"

	"foundry/internal/audio"
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
	// Bindings maps an action's name to its slots' input names
	// (platform.Input.Name), "" for an empty slot. An action the file does
	// not name keeps its default, so a new action reaches an old file.
	Bindings map[string][]string `yaml:"bindings,omitempty"`
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
	for name, slots := range raw.Bindings {
		a, ok := platform.ActionByName(name)
		if !ok {
			log.Printf("controls: %s: no action named %q (ignored)", controlsFileName, name)
			continue
		}
		var row [platform.BindingSlots]platform.Input
		for i, n := range slots {
			if i >= platform.BindingSlots {
				break
			}
			in, ok := platform.ParseInput(n)
			if !ok {
				log.Printf("controls: %s: %s: no key or button named %q (left unbound)", controlsFileName, name, n)
				continue
			}
			row[i] = in
		}
		platform.Bindings[a] = row
	}
	return nil
}

func marshalControlSettings() ([]byte, error) {
	sens, smooth, inv := platform.MouseSensitivity, platform.MouseSmoothing, platform.MouseInvertY
	bindings := make(map[string][]string, platform.ActionCount)
	for a, row := range platform.Bindings {
		names := make([]string, len(row))
		for i, in := range row {
			names[i] = in.Name()
		}
		bindings[platform.Actions[a].Name] = names
	}
	return yaml.Marshal(rawControls{MouseSensitivity: &sens, MouseSmoothing: &smooth, MouseInvertY: &inv, Bindings: bindings})
}

// settledSave writes a settings file once a change to it has been left alone
// for controlsSaveDelay. One per file.
type settledSave struct {
	unsavedFor float32 // how long the current unsaved change has been still
}

// step is called once a frame with the file's changed flag, and reports whether
// it is time to save — clearing the flag when it is.
func (s *settledSave) step(changed *bool, dt float32) bool {
	if !*changed {
		s.unsavedFor = 0
		return false
	}
	// A setter raising the flag again restarts the wait, so the file is written
	// after the last move of a drag.
	if s.unsavedFor == 0 {
		s.unsavedFor = dt
		return false
	}
	s.unsavedFor += dt
	if s.unsavedFor < controlsSaveDelay {
		return false
	}
	*changed = false
	s.unsavedFor = 0
	return true
}

var controlsSave, audioSave settledSave

// saveSettingsWhenSettled writes controls.yaml and audio.yaml once a script's
// change to them has settled. Called once a frame from the frame loop.
func saveSettingsWhenSettled(dt float32) {
	if controlsSave.step(&platform.ControlSettingsChanged, dt) {
		data, err := marshalControlSettings()
		if err == nil {
			err = fileutil.WriteFileAtomic(fileutil.StatePath(controlsFileName), data)
		}
		if err != nil {
			log.Printf("controls: saving %s: %v", controlsFileName, err)
		}
	}
	if audioSave.step(&audio.SettingsChanged, dt) {
		if err := audio.SaveSettings(); err != nil {
			log.Printf("audio: saving settings: %v", err)
		}
	}
}
