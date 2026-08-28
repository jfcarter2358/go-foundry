package audio

// audio_settings.go — the three gains, on disk.
//
// The engine keeps every other user setting as a package global written
// straight by an ImGui widget, and none of them survive a restart. Volume is
// the first setting where that is genuinely wrong: a player who turns the
// music down expects it to stay down, and re-muting the game at every launch
// is the kind of small insult people remember.
//
// Rather than invent a settings system for one feature, this follows the one
// existing precedent — detail.LoadDetailPresets / SaveDetailPreset — with a
// YAML file beside the executable, an atomic write, and a missing file meaning
// "defaults" rather than an error.

import (
	"log"
	"os"
	"path/filepath"

	"foundry/internal/fileutil"

	"gopkg.in/yaml.v2"
)

// settingsFileName is the file beside the executable.
const settingsFileName = "audio.yaml"

// rawSettings is the on-disk shape. Pointers so that a file written before a
// field existed leaves that field at its default rather than at zero — which
// for a gain means silence, and would be a confusing thing to inherit from an
// upgrade.
type rawSettings struct {
	Master  *float32 `yaml:"master,omitempty"`
	Effects *float32 `yaml:"effects,omitempty"`
	Music   *float32 `yaml:"music,omitempty"`
}

func settingsPath() string {
	return filepath.Join(fileutil.ExecutablePath, settingsFileName)
}

// LoadSettings reads the saved gains. A missing file is the normal first-run
// case and is silent; a malformed one is logged and ignored, because starting
// the game at default volume is a better outcome than refusing to start.
func LoadSettings() {
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		return
	}
	var raw rawSettings
	if err := yaml.Unmarshal(data, &raw); err != nil {
		log.Printf("audio: %s: %v (using defaults)", settingsFileName, err)
		return
	}
	if raw.Master != nil {
		SetMasterGain(*raw.Master)
	}
	if raw.Effects != nil {
		SetEffectsGain(*raw.Effects)
	}
	if raw.Music != nil {
		SetMusicGain(*raw.Music)
	}
}

// SaveSettings writes the current gains. Called when a slider is released
// rather than on every frame of a drag, so a drag is one write and not two
// hundred.
func SaveSettings() error {
	m, e, mu := MasterGain(), EffectsGain(), MusicGain()
	data, err := yaml.Marshal(rawSettings{Master: &m, Effects: &e, Music: &mu})
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(settingsPath(), data)
}
