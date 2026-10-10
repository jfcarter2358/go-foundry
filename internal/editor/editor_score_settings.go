package editor

// editor_score_settings.go — what the Score editor remembers between
// sessions: the keymap (a preset and the rebindings laid over it), the
// recently opened scores, and whether the keypad panel is up.
//
// Kept in score.yaml beside claude.yaml (fileutil.StatePath), on that file's
// rule: it is the editor's own, it never ships (pack.developmentFiles knows
// its name), and a failure to read or write it is logged, not shown — losing
// a recent-files list is not worth interrupting anyone over.

import (
	"log"
	"os"
	"sync"

	"foundry/internal/fileutil"

	"gopkg.in/yaml.v2"
)

// scoreSettingsFile is the name pack.IsDevelopmentFile must also know.
const scoreSettingsFile = "score.yaml"

// scoreRecentMax is how many recent scores File > Open Recent keeps.
const scoreRecentMax = 10

type scoreConfig struct {
	// Keymap is the preset's name ("Sibelius", "MuseScore").
	Keymap string `yaml:"keymap,omitempty"`
	// Bindings are the commands rebound away from the preset: a command
	// listed has exactly these chords.
	Bindings map[string][]string `yaml:"bindings,omitempty"`
	Recent   []string            `yaml:"recent,omitempty"`
	Keypad   *bool               `yaml:"keypad,omitempty"`
}

var (
	scoreCfg     scoreConfig
	scoreCfgOnce sync.Once
	// scoreKeys is the active keymap, built from the settings once.
	scoreKeys *scoreKeymap
)

// scoreSettings reads the settings the first time they are asked for.
func scoreSettings() *scoreConfig {
	scoreCfgOnce.Do(func() {
		data, err := os.ReadFile(fileutil.StatePath(scoreSettingsFile))
		if err != nil {
			return
		}
		if err := yaml.Unmarshal(data, &scoreCfg); err != nil {
			log.Printf("score settings: %v", err)
		}
	})
	return &scoreCfg
}

// scoreSaveSettings writes the settings back.
func scoreSaveSettings() {
	data, err := yaml.Marshal(scoreSettings())
	if err != nil {
		log.Printf("score settings: %v", err)
		return
	}
	if err := os.WriteFile(fileutil.StatePath(scoreSettingsFile), data, 0o644); err != nil {
		log.Printf("score settings: %v", err)
	}
}

// scoreKeymapActive is the keymap in use: the saved preset with the saved
// rebindings over it.
func scoreKeymapActive() *scoreKeymap {
	if scoreKeys == nil {
		cfg := scoreSettings()
		k := scorePreset(cfg.Keymap)
		for _, p := range k.applyOverrides(cfg.Bindings) {
			log.Printf("score settings: %s", p)
		}
		scoreKeys = k
	}
	return scoreKeys
}

// scoreSetKeymap switches preset, dropping every rebinding.
func scoreSetKeymap(name string) {
	cfg := scoreSettings()
	k := scorePreset(name)
	cfg.Keymap, cfg.Bindings = k.Name, nil
	scoreKeys = k
	scoreSaveSettings()
}

// scoreSaveBindings stores the active keymap's differences from its preset.
func scoreSaveBindings() {
	k := scoreKeymapActive()
	cfg := scoreSettings()
	cfg.Keymap = k.Name
	cfg.Bindings = k.overrides(scorePreset(k.Name))
	if len(cfg.Bindings) == 0 {
		cfg.Bindings = nil
	}
	scoreSaveSettings()
}

// scoreNoteRecent puts a score at the top of the recent list.
func scoreNoteRecent(name string) {
	cfg := scoreSettings()
	cfg.Recent = scorePushRecent(cfg.Recent, name)
	scoreSaveSettings()
}

// scorePushRecent is the list with name first, once, at most scoreRecentMax long.
func scorePushRecent(list []string, name string) []string {
	out := []string{name}
	for _, n := range list {
		if n != name && len(out) < scoreRecentMax {
			out = append(out, n)
		}
	}
	return out
}

// scoreKeypadWanted is whether the keypad panel shows; it starts up.
func scoreKeypadWanted() bool {
	if k := scoreSettings().Keypad; k != nil {
		return *k
	}
	return true
}

func scoreSetKeypadWanted(on bool) {
	scoreSettings().Keypad = &on
	scoreSaveSettings()
}
