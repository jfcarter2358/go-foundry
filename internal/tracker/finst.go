package tracker

// finst.go — instruments on their own: the .finst file and its library.
//
// A song can carry its instruments inside it, the way the Tracker always
// has, or name one from the shared library:
//
//	resources/sounds/instruments/strings/violin_section.finst
//
// is "strings/violin_section" to a song's slot (ref:) and to a Score line.
// The file is the same YAML a song's instrument is — samples by library
// name, zones, envelopes, filter, vibrato — plus what the Score needs:
// velocity layers, named articulations, round robin, and the playback
// defaults that say how the instrument is written (range, transposition,
// clef) and how its marks play (glissando style, staccato length,
// release). One violin, tuned once, then plays the same in every song and
// every score that names it.

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"foundry/internal/audio"

	"gopkg.in/yaml.v2"
)

// InstrumentExt is a library instrument's extension.
const InstrumentExt = ".finst"

// InstrumentsDirName is the instrument library's folder under sounds/.
const InstrumentsDirName = "instruments"

// InstrumentFormatVersion is written into every .finst.
const InstrumentFormatVersion = 1

type rawFinst struct {
	Version       int `yaml:"version"`
	rawInstrument `yaml:",inline"`
	Description   string `yaml:"description,omitempty"`
}

// InstrumentsDir is the library root.
func InstrumentsDir() string { return filepath.Join(audio.SoundsDir(), InstrumentsDirName) }

// InstrumentPath resolves a library name ("strings/violin_section") to its
// file, or "".
func InstrumentPath(name string) string {
	if name == "" {
		return ""
	}
	name = strings.TrimSuffix(name, InstrumentExt)
	p := filepath.Join(InstrumentsDir(), filepath.FromSlash(name)+InstrumentExt)
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p
	}
	return ""
}

// ParseInstrument decodes a .finst. Samples are not resolved.
func ParseInstrument(b []byte) (*Instrument, error) {
	var r rawFinst
	if err := yaml.UnmarshalStrict(b, &r); err != nil {
		return nil, err
	}
	if r.Version > InstrumentFormatVersion {
		return nil, fmt.Errorf("instrument is format version %d; this build reads up to %d", r.Version, InstrumentFormatVersion)
	}
	if r.Ref != "" {
		return nil, fmt.Errorf("a library instrument cannot itself be a ref (%q)", r.Ref)
	}
	in, err := instrumentFromRaw(r.rawInstrument)
	if err != nil {
		return nil, err
	}
	in.Description = r.Description
	return in, nil
}

// MarshalInstrument encodes an instrument as a .finst. A slot's Ref is not
// written: the library holds instruments, not pointers to them.
func MarshalInstrument(in *Instrument) ([]byte, error) {
	c := *in
	c.Ref = ""
	return yaml.Marshal(rawFinst{Version: InstrumentFormatVersion, rawInstrument: instrumentToRaw(&c), Description: in.Description})
}

// LoadInstrumentFile reads a .finst from disk, its category taken from the
// folder it is in. Samples are not resolved.
func LoadInstrumentFile(path string) (*Instrument, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	in, err := ParseInstrument(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if rel, err := filepath.Rel(InstrumentsDir(), path); err == nil && !strings.HasPrefix(rel, "..") {
		if dir := filepath.Dir(filepath.ToSlash(rel)); dir != "." {
			in.Category = strings.SplitN(dir, "/", 2)[0]
		}
	}
	return in, nil
}

// SaveInstrumentFile writes an instrument to a .finst.
func SaveInstrumentFile(path string, in *Instrument) error {
	b, err := MarshalInstrument(in)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// SaveInstrument writes an instrument into the library under name.
func SaveInstrument(name string, in *Instrument) error {
	path := filepath.Join(InstrumentsDir(), filepath.FromSlash(strings.TrimSuffix(name, InstrumentExt))+InstrumentExt)
	if err := SaveInstrumentFile(path, in); err != nil {
		return err
	}
	ReloadInstruments()
	return nil
}

// ---------- the library ----------

type instEntry struct {
	in  *Instrument
	err error
}

var (
	instMu    sync.Mutex
	instCache = map[string]instEntry{}
)

// LoadInstrument returns a library instrument by name with its samples
// resolved. The result is shared: clone it before changing it. A missing
// sample is logged and leaves a silent slot, as in a song.
func LoadInstrument(name string) (*Instrument, error) {
	name = strings.TrimSuffix(name, InstrumentExt)
	instMu.Lock()
	defer instMu.Unlock()
	if e, ok := instCache[name]; ok {
		return e.in, e.err
	}
	var e instEntry
	path := InstrumentPath(name)
	if path == "" {
		e.err = fmt.Errorf("no instrument %q under %s", name, InstrumentsDir())
	} else if e.in, e.err = LoadInstrumentFile(path); e.err == nil {
		for _, err := range e.in.Resolve(SampleFor) {
			log.Printf("tracker: instrument %s: %v", name, err)
		}
	}
	if e.err != nil {
		log.Printf("tracker: %v", e.err)
	}
	instCache[name] = e
	return e.in, e.err
}

// ReloadInstruments drops the instrument cache.
func ReloadInstruments() {
	instMu.Lock()
	instCache = map[string]instEntry{}
	instMu.Unlock()
}

// ScanInstrumentNames lists the library, sorted, as songs name them.
func ScanInstrumentNames() []string {
	root := InstrumentsDir()
	var out []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.EqualFold(filepath.Ext(p), InstrumentExt) {
			return nil
		}
		if rel, err := filepath.Rel(root, p); err == nil {
			out = append(out, filepath.ToSlash(strings.TrimSuffix(rel, filepath.Ext(rel))))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// InstrumentRefs lists the library instruments a song file's slots name,
// for the release packer and the unused-asset scan.
func InstrumentRefs(s *Song) []string {
	seen := map[string]bool{}
	var out []string
	for _, in := range s.Instruments {
		if in.Ref != "" && !seen[in.Ref] {
			seen[in.Ref] = true
			out = append(out, in.Ref)
		}
	}
	sort.Strings(out)
	return out
}

// resolveRef fills a library slot from its .finst, keeping the slot's name
// (or taking the instrument's when the slot has none) and its Ref.
func (in *Instrument) resolveRef() error {
	lib, err := LoadInstrument(in.Ref)
	if err != nil {
		return err
	}
	in.adopt(lib)
	return nil
}

// adopt makes a library slot play lib: everything but the slot's name and
// ref is lib's. Decoded samples are shared.
func (in *Instrument) adopt(lib *Instrument) {
	name, ref := in.Name, in.Ref
	*in = *lib.Clone()
	if name != "" {
		in.Name = name
	}
	in.Ref = ref
}

// UseLibrary makes the slot play library instrument name, resolved now. The
// slot keeps its own name unless it has none. On an error the slot is left
// as it was.
func (in *Instrument) UseLibrary(name string) error {
	name = strings.TrimSuffix(name, InstrumentExt)
	lib, err := LoadInstrument(name)
	if err != nil {
		return err
	}
	in.Ref = name
	in.adopt(lib)
	return nil
}

// Embed turns a library slot into an embedded copy of what it plays, so the
// song can change it without changing the library.
func (in *Instrument) Embed() {
	in.Ref = ""
}
