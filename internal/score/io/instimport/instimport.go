// Package instimport imports a third-party sample library file — an .sfz
// or a SoundFont .sf2 — into the instrument library, choosing the reader
// by the file's extension. It is what `foundry tracker import-instrument`
// and the Score editor's instrument library dialog call.
package instimport

import (
	"fmt"
	"path/filepath"
	"strings"

	"foundry/internal/score/io/sf2"
	"foundry/internal/score/io/sfz"
)

// Exts are the extensions Import reads.
var Exts = []string{".sfz", ".sf2"}

// Supported reports whether a path has an extension Import reads.
func Supported(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range Exts {
		if ext == e {
			return true
		}
	}
	return false
}

// Options say what an import is called and where it goes.
type Options struct {
	// Name is the library name: the instrument's, or for a SoundFont of
	// several presets the folder holding one instrument each (whichever of
	// them are imported). "" takes "imported/" + the file's name.
	Name string
	// Presets picks a SoundFont's presets (sf2.Preset.Index); nil is all.
	Presets []int
	// SoundsDir is the sounds folder written into; "" is the project's.
	SoundsDir string
}

// Import reads path and writes what it holds into the library: one
// instrument for an .sfz, one a preset for an .sf2.
func Import(path string, o Options) ([]*sfz.Built, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sfz":
		b, err := sfz.Import(path, sfz.Options{Name: o.Name, SoundsDir: o.SoundsDir})
		if err != nil {
			return nil, err
		}
		return []*sfz.Built{b}, nil
	case ".sf2":
		return sf2.Import(path, sf2.Options{Name: o.Name, Presets: o.Presets, SoundsDir: o.SoundsDir})
	}
	return nil, fmt.Errorf("%s: not an .sfz or .sf2 file", filepath.Base(path))
}
