package app

// controls_settings.go — the player's mouse and pad settings and their
// bindings, on disk.
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
	"foundry/internal/prefs"

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

	PadSensitivityX *float32 `yaml:"pad_sensitivity_x,omitempty"`
	PadSensitivityY *float32 `yaml:"pad_sensitivity_y,omitempty"`
	PadInvertY      *bool    `yaml:"pad_invert_y,omitempty"`
	PadDeadZone     *float32 `yaml:"pad_dead_zone,omitempty"`
	PadLookCurve    *float32 `yaml:"pad_look_curve,omitempty"`
	PadVibration    *float32 `yaml:"pad_vibration,omitempty"`
	PadHoldTime     *float32 `yaml:"pad_hold_time,omitempty"`
	AimAssist       *bool    `yaml:"aim_assist,omitempty"`

	// Bindings maps an action's name to its slots' input names
	// (platform.Input.Name), "" for an empty slot: the two key slots, then the
	// pad's. An action the file does not name keeps its default, so a new
	// action reaches an old file — and a slot it does not list keeps its
	// default too, so a file from before the pad column gets the default pad
	// layout rather than none.
	Bindings map[string][]string `yaml:"bindings,omitempty"`
}

// loadControlSettings reads the saved mouse settings over the defaults, and the
// game's own settings (settings.yaml, internal/prefs) beside them.
func loadControlSettings() {
	if data, err := fileutil.ReadState(controlsFileName); err == nil {
		if err := applyControlSettings(data); err != nil {
			log.Printf("controls: %s: %v (using defaults)", controlsFileName, err)
		}
	}
	if data, err := fileutil.ReadState(prefs.FileName); err == nil {
		if err := prefs.Load(data); err != nil {
			log.Printf("settings: %s: %v", prefs.FileName, err)
		}
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
	if raw.PadSensitivityX != nil {
		platform.PadLookSensitivityX = clampF(*raw.PadSensitivityX, platform.MinPadSensitivity, platform.MaxPadSensitivity)
	}
	if raw.PadSensitivityY != nil {
		platform.PadLookSensitivityY = clampF(*raw.PadSensitivityY, platform.MinPadSensitivity, platform.MaxPadSensitivity)
	}
	if raw.PadInvertY != nil {
		platform.PadInvertY = *raw.PadInvertY
	}
	if raw.PadDeadZone != nil {
		platform.PadDeadZone = clampF(*raw.PadDeadZone, 0, platform.MaxPadDeadZone)
	}
	if raw.PadLookCurve != nil {
		platform.PadLookCurve = clampF(*raw.PadLookCurve, platform.MinPadLookCurve, platform.MaxPadLookCurve)
	}
	if raw.PadVibration != nil {
		platform.PadVibration = clamp01(*raw.PadVibration)
	}
	if raw.PadHoldTime != nil {
		platform.PadHoldTime = clampF(*raw.PadHoldTime, platform.MinPadHoldTime, platform.MaxPadHoldTime)
	}
	if raw.AimAssist != nil {
		platform.AimAssist = *raw.AimAssist
	}
	for name, slots := range raw.Bindings {
		a, ok := platform.ActionByName(name)
		if !ok {
			log.Printf("controls: %s: no action named %q (ignored)", controlsFileName, name)
			continue
		}
		row := platform.Bindings[a]
		for i, n := range slots {
			if i >= platform.BindingSlots {
				break
			}
			in, ok := platform.ParseInput(n)
			if !ok {
				log.Printf("controls: %s: %s: no key or button named %q (left unbound)", controlsFileName, name, n)
				row[i] = platform.Input{}
				continue
			}
			if in.Bound() && in.IsPad() != (i == platform.PadSlot) {
				log.Printf("controls: %s: %s: %q cannot go in slot %d (left unbound)", controlsFileName, name, n, i+1)
				row[i] = platform.Input{}
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
	psx, psy, pinv := platform.PadLookSensitivityX, platform.PadLookSensitivityY, platform.PadInvertY
	dead, curve, vib, assist := platform.PadDeadZone, platform.PadLookCurve, platform.PadVibration, platform.AimAssist
	hold := platform.PadHoldTime
	bindings := make(map[string][]string, platform.ActionCount)
	for a, row := range platform.Bindings {
		names := make([]string, len(row))
		for i, in := range row {
			names[i] = in.Name()
		}
		bindings[platform.Actions[a].Name] = names
	}
	return yaml.Marshal(rawControls{
		MouseSensitivity: &sens, MouseSmoothing: &smooth, MouseInvertY: &inv,
		PadSensitivityX: &psx, PadSensitivityY: &psy, PadInvertY: &pinv,
		PadDeadZone: &dead, PadLookCurve: &curve, PadVibration: &vib, PadHoldTime: &hold, AimAssist: &assist,
		Bindings: bindings,
	})
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

var controlsSave, audioSave, prefsSave settledSave

// saveSettingsWhenSettled writes controls.yaml, audio.yaml, graphics.yaml and
// settings.yaml once a script's change to them has settled. Called once a frame from the
// frame loop.
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
	if prefsSave.step(&prefs.Changed, dt) {
		data, err := prefs.Marshal()
		if err == nil {
			err = fileutil.WriteFileAtomic(fileutil.StatePath(prefs.FileName), data)
		}
		if err != nil {
			log.Printf("settings: saving %s: %v", prefs.FileName, err)
		}
	}
	saveGraphicsWhenSettled(dt)
	if audioSave.step(&audio.SettingsChanged, dt) {
		if err := audio.SaveSettings(); err != nil {
			log.Printf("audio: saving settings: %v", err)
		}
	}
}

func clampF(v, lo, hi float32) float32 { return min(max(v, lo), hi) }
