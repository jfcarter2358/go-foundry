package editor

// editor_claude_settings.go — what the panel remembers between sessions.
//
// Kept beside the Release window's release.yaml, in the project directory and
// under the same rule: it is the editor's own file, it never ships, and
// pack.developmentFiles knows its name.
//
// Deliberately small. The model, the permission mode and the conversation to
// resume are the three things that are annoying to set again every morning; the
// binary path is here because a machine with the CLI somewhere unusual would
// otherwise have to be re-found through the UI every time. Everything else about
// how Claude behaves belongs in the project's CLAUDE.md, where it also applies
// when the same project is opened from a terminal.

import (
	"log"
	"os"
	"sync"

	"foundry/internal/fileutil"

	"gopkg.in/yaml.v2"
)

// claudeSettingsFile is the name pack.IsDevelopmentFile must also know.
const claudeSettingsFile = "claude.yaml"

type claudeConfig struct {
	// Binary overrides the search in internal/claude/locate.go.
	Binary string `yaml:"binary,omitempty"`
	Model  string `yaml:"model,omitempty"`
	Mode   string `yaml:"mode,omitempty"`
	// LastSession is the conversation to offer to resume.
	LastSession string `yaml:"last_session,omitempty"`
}

var (
	claudeCfg     claudeConfig
	claudeCfgOnce sync.Once
)

// claudeSettings is the settings, read from disk the first time they are asked
// for.
func claudeSettings() *claudeConfig {
	claudeCfgOnce.Do(func() {
		data, err := os.ReadFile(fileutil.StatePath(claudeSettingsFile))
		if err != nil {
			return
		}
		if err := yaml.Unmarshal(data, &claudeCfg); err != nil {
			log.Printf("claude settings: %v", err)
		}
	})
	return &claudeCfg
}

// claudeSaveSettings writes the panel's current choices back. Failures are
// logged and otherwise ignored: losing the last model picked is not worth
// interrupting anyone over.
func claudeSaveSettings() {
	cfg := claudeSettings()
	cfg.Model, cfg.Mode = claudeState.model, claudeState.mode
	if claudeState.sess != nil {
		if id := claudeState.sess.Snapshot().SessionID; id != "" {
			cfg.LastSession = id
		}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		log.Printf("claude settings: %v", err)
		return
	}
	if err := os.WriteFile(fileutil.StatePath(claudeSettingsFile), data, 0o644); err != nil {
		log.Printf("claude settings: %v", err)
	}
}

// claudeLoadSettings seeds the panel's controls from the file. Called as the
// window opens.
func claudeLoadSettings() {
	cfg := claudeSettings()
	if claudeState.model == "" {
		claudeState.model = cfg.Model
	}
	if claudeState.mode == "" {
		claudeState.mode = cfg.Mode
		// A mode saved against a newer CLI than this machine has would sit in the
		// picker as a label for something that cannot run. Asking is the mode to
		// fall back to.
		for _, m := range claudeModes {
			if m.Mode == claudeState.mode && !claudeModeAvailable(m.Min) {
				claudeState.mode = ""
			}
		}
	}
}
