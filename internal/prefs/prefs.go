// Package prefs is the player's own game settings — the ones a game defines
// for itself, where the engine's are the mouse, the keys and the volume.
//
// A settings screen that offers "weapon bar on the left or the right" needs
// that choice to outlast the session, and the engine has no business knowing
// what a weapon bar is. So this is a flat name → value store a script reads
// with `setting(name, default)` and writes with `set_setting(name, value)`,
// saved to settings.yaml in the writable state directory by the frame loop
// once a change has settled (app/controls_settings.go), exactly as
// controls.yaml is. A tick must not do file I/O, so Set only raises Changed.
//
// Values are strings, numbers and bools — what a settings screen chooses
// between. Numbers are held as float64; the script verb hands an int back to a
// caller whose default was an int.
package prefs

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v2"
)

// FileName is the store's name in the state directory.
const FileName = "settings.yaml"

var values = map[string]any{}

// Changed is raised by Set and cleared by the frame loop when it saves.
var Changed bool

// Get returns the named value: a string, a float64 or a bool.
func Get(name string) (any, bool) {
	v, ok := values[name]
	return v, ok
}

// Set stores a value, which must be a string, a number or a bool.
func Set(name string, v any) error {
	switch x := v.(type) {
	case string, bool, float64:
	case int:
		v = float64(x)
	case int64:
		v = float64(x)
	case float32:
		v = float64(x)
	default:
		return fmt.Errorf("a setting is a string, a number or a bool, not a %T", v)
	}
	if old, ok := values[name]; ok && old == v {
		return nil
	}
	values[name] = v
	Changed = true
	return nil
}

// Reset forgets every setting, without marking the store changed. For tests.
func Reset() {
	values = map[string]any{}
	Changed = false
}

// Load replaces the store with a saved file's contents. An entry of a kind no
// setting can be (a list, a map) is skipped with the rest kept.
func Load(data []byte) error {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	loaded := make(map[string]any, len(raw))
	var bad []string
	for k, v := range raw {
		switch x := v.(type) {
		case string, bool, float64:
			loaded[k] = x
		case int:
			loaded[k] = float64(x)
		default:
			bad = append(bad, k)
		}
	}
	values = loaded
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("skipped %v: a setting is a string, a number or a bool", bad)
	}
	return nil
}

// Marshal is the store as settings.yaml holds it.
func Marshal() ([]byte, error) {
	return yaml.Marshal(values)
}
