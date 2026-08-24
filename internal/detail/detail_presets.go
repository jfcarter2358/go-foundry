package detail

// detail_presets.go — user-authored procedural-detail presets.
//
// The presets in go are compiled in. This file adds presets the user
// creates from the editor's Detail panel: the face's current wave, noise, taper
// and scatter settings are written to <exe>/detail_presets/<name>.yaml and
// merged into DetailPresets at startup, so they show up in the preset combo
// beside the built-ins and can be applied to any other face.
//
// A preset never carries a Seed — that stays per-face, exactly as
// ApplyDetailPreset already treats it.
//
// A user preset may reuse a built-in's name, in which case it shadows the
// built-in; deleting it restores the original. The on-disk format is the same
// RawDetail used for a face inside a level file, so a preset can be lifted out
// of a level's YAML by hand and vice versa.

import (
	"fmt"
	"foundry/internal/fileutil"
	"foundry/internal/world"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v2"
)

// detailPresetsDirName is the directory (next to the executable) that user
// presets live in. Like levels/, it is not created until something
// is saved into it.
const detailPresetsDirName = "detail_presets"

// DetailBuiltinPresets snapshots the compiled-in presets before a user preset
// can shadow one, so deleting the shadowing preset restores the original.
var DetailBuiltinPresets = snapshotDetailPresets()

// DetailUserPresets is the set of preset names backed by a file on disk. Only
// these can be deleted; the built-ins are code.
var DetailUserPresets = map[string]bool{}

// DetailUserPresetNames is DetailUserPresets sorted, for a stable UI listing.
var DetailUserPresetNames []string

func snapshotDetailPresets() map[string]world.DetailSpec {
	m := make(map[string]world.DetailSpec, len(world.DetailPresets))
	for name, spec := range world.DetailPresets {
		m[name] = spec // Waves is an array, so this copy is deep
	}
	return m
}

// detailPresetsDir is the on-disk home of user presets.
func detailPresetsDir() string {
	return filepath.Join(fileutil.ExecutablePath, detailPresetsDirName)
}

// LoadDetailPresets merges every preset file in <exe>/detail_presets into
// DetailPresets. A missing directory just means no user presets yet; a bad file
// is logged and skipped rather than taken down the whole startup.
func LoadDetailPresets() {
	dir := detailPresetsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		name := DetailPresetName(strings.TrimSuffix(e.Name(), ".yaml"))
		if name == "" {
			continue
		}
		spec, err := loadDetailPresetFile(filepath.Join(dir, e.Name()))
		if err != nil {
			log.Printf("detail preset %s: %v", e.Name(), err)
			continue
		}
		spec.Preset = name
		world.DetailPresets[name] = spec
		markDetailUserPreset(name)
	}
	if N := len(DetailUserPresetNames); N > 0 {
		log.Printf("loaded %d user detail preset(s) from %s", N, dir)
	}
}

// loadDetailPresetFile reads one preset file. The seed is dropped: applying a
// preset preserves the target face's own seed.
func loadDetailPresetFile(path string) (world.DetailSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return world.DetailSpec{}, err
	}
	var raw world.RawDetail
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return world.DetailSpec{}, fmt.Errorf("yaml: %w", err)
	}
	spec := world.DetailFromRaw(&raw)
	spec.Seed = 0
	return spec, nil
}

// SaveDetailPreset writes d's parameters as the named preset and registers it,
// creating the preset directory on first use. An existing preset of that name
// (user or built-in) is replaced.
func SaveDetailPreset(name string, d world.DetailSpec) error {
	name = DetailPresetName(name)
	if name == "" {
		return fmt.Errorf("preset name is empty")
	}
	dir := detailPresetsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	spec := d
	spec.Enabled = true // DetailToRaw omits a disabled spec entirely
	spec.Preset = name
	spec.Seed = 0
	spec.Clamp()
	spec.Seed = 0 // clamp() forces a non-zero seed; a preset carries none

	data, err := yaml.Marshal(world.DetailToRaw(spec))
	if err != nil {
		return fmt.Errorf("yaml marshal: %w", err)
	}
	if err := fileutil.WriteFileAtomic(filepath.Join(dir, name+".yaml"), data); err != nil {
		return err
	}

	world.DetailPresets[name] = spec
	markDetailUserPreset(name)
	return nil
}

// DeleteDetailPreset removes a user preset's file and unregisters it. If it was
// shadowing a built-in of the same name, the built-in comes back.
func DeleteDetailPreset(name string) error {
	name = DetailPresetName(name)
	if !DetailUserPresets[name] {
		return fmt.Errorf("%q is not a user preset", name)
	}
	if err := os.Remove(filepath.Join(detailPresetsDir(), name+".yaml")); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(DetailUserPresets, name)
	for i, N := range DetailUserPresetNames {
		if N == name {
			DetailUserPresetNames = append(DetailUserPresetNames[:i], DetailUserPresetNames[i+1:]...)
			break
		}
	}
	if builtin, Ok := DetailBuiltinPresets[name]; Ok {
		world.DetailPresets[name] = builtin
	} else {
		delete(world.DetailPresets, name)
	}
	return nil
}

// markDetailUserPreset records name as disk-backed, keeping the name list sorted.
func markDetailUserPreset(name string) {
	if DetailUserPresets[name] {
		return
	}
	DetailUserPresets[name] = true
	DetailUserPresetNames = append(DetailUserPresetNames, name)
	sort.Strings(DetailUserPresetNames)
}

// DetailPresetName normalises a typed preset name into something safe to use as
// a filename: surrounding space trimmed, a typed ".yaml" suffix dropped, and
// anything outside [A-Za-z0-9 _-] removed so it can never escape the presets
// directory.
func DetailPresetName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".yaml")
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == ' ':
			return r
		}
		return -1
	}, s)
	return strings.TrimSpace(s)
}
