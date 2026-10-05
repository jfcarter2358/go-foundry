package tracker

// sample.go — the sound an instrument plays.
//
// Samples live in the shared library under sounds/samples/, so twenty songs
// can share one piano and the release packer can prune what nothing plays:
//
//	resources/sounds/samples/drums/kick_808.wav
//	resources/sounds/samples/drums/kick_808.fsmp   <- base note, loop, tags
//
// A song names a sample by its path under samples/, without the extension
// ("drums/kick_808"). The audio is decoded with internal/audio's decoders, so
// anything a level can play, a song can too.

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

// SamplesDirName is the sample library's folder under sounds/.
const SamplesDirName = "samples"

// SampleExt is the sidecar's extension.
const SampleExt = ".fsmp"

// LoopMode is how a sample repeats while a note is held.
type LoopMode uint8

const (
	LoopNone LoopMode = iota
	LoopForward
	LoopPingPong
)

func (m LoopMode) String() string {
	switch m {
	case LoopForward:
		return "forward"
	case LoopPingPong:
		return "pingpong"
	}
	return "none"
}

func parseLoopMode(s string) LoopMode {
	switch strings.ToLower(s) {
	case "forward", "loop", "on":
		return LoopForward
	case "pingpong", "ping-pong", "bidi":
		return LoopPingPong
	}
	return LoopNone
}

// Sample is decoded audio plus how to play it. Immutable once loaded: the
// mixer goroutine reads Data with no lock.
type Sample struct {
	Name     string
	Data     []float32 // interleaved, Channels wide
	Channels int
	Rate     int
	// BaseNote is the note at which the sample plays at its own pitch.
	BaseNote uint8
	FineTune float32 // cents
	Loop     LoopMode
	// LoopStart and LoopEnd are frames, End exclusive.
	LoopStart, LoopEnd int
	Volume             float32
	Tags               []string
	Description        string
}

// Frames is the sample's length.
func (s *Sample) Frames() int {
	if s == nil || s.Channels == 0 {
		return 0
	}
	return len(s.Data) / s.Channels
}

// Looped reports a usable loop.
func (s *Sample) Looped() bool {
	return s.Loop != LoopNone && s.LoopEnd > s.LoopStart+1 && s.LoopEnd <= s.Frames()
}

// sidecar is the .fsmp file.
type sidecar struct {
	BaseNote    string   `yaml:"base_note,omitempty"`
	FineTune    float32  `yaml:"fine_tune,omitempty"`
	Loop        string   `yaml:"loop,omitempty"`
	LoopStart   int      `yaml:"loop_start,omitempty"`
	LoopEnd     int      `yaml:"loop_end,omitempty"`
	Volume      *float32 `yaml:"volume,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
	Description string   `yaml:"description,omitempty"`
}

// SamplesDir is the library root.
func SamplesDir() string { return filepath.Join(audio.SoundsDir(), SamplesDirName) }

// SamplePath resolves a library name to its audio file, or "".
func SamplePath(name string) string {
	for _, ext := range audio.SoundExts {
		p := filepath.Join(SamplesDir(), filepath.FromSlash(name)+ext)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// LoadSampleFile decodes an audio file and reads the sidecar beside it.
func LoadSampleFile(name, path string) (*Sample, error) {
	clip, err := audio.LoadClip(name, path)
	if err != nil {
		return nil, err
	}
	s := &Sample{
		Name: name, Data: clip.Samples, Channels: clip.Channels, Rate: clip.Rate,
		BaseNote: NoteC4, Volume: 1,
	}
	side := strings.TrimSuffix(path, filepath.Ext(path)) + SampleExt
	if b, err := os.ReadFile(side); err == nil {
		var sc sidecar
		if err := yaml.Unmarshal(b, &sc); err != nil {
			return nil, fmt.Errorf("%s: %w", side, err)
		}
		if sc.BaseNote != "" {
			n, err := ParseNote(sc.BaseNote)
			if err != nil || n == NoteNone || n > NoteMax {
				return nil, fmt.Errorf("%s: base_note %q", side, sc.BaseNote)
			}
			s.BaseNote = n
		}
		s.FineTune = sc.FineTune
		s.Loop = parseLoopMode(sc.Loop)
		s.LoopStart, s.LoopEnd = sc.LoopStart, sc.LoopEnd
		if s.Loop != LoopNone && s.LoopEnd == 0 {
			s.LoopEnd = s.Frames()
		}
		if sc.Volume != nil {
			s.Volume = *sc.Volume
		}
		s.Tags = sc.Tags
		s.Description = sc.Description
	}
	if s.LoopEnd > s.Frames() {
		s.LoopEnd = s.Frames()
	}
	return s, nil
}

// SaveSample writes a sample into the library: 24-bit WAV and its sidecar.
func SaveSample(s *Sample) error {
	base := filepath.Join(SamplesDir(), filepath.FromSlash(s.Name))
	return SaveSampleTo(base, s)
}

// SaveSampleTo writes base+".wav" and base+".fsmp".
func SaveSampleTo(base string, s *Sample) error {
	if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
		return err
	}
	f, err := os.Create(base + ".wav")
	if err != nil {
		return err
	}
	if err := WriteWAV(f, s.Data, s.Channels, s.Rate, 24); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	sc := sidecar{
		BaseNote: NoteName(s.BaseNote), FineTune: s.FineTune,
		Tags: s.Tags, Description: s.Description,
	}
	if s.Loop != LoopNone {
		sc.Loop = s.Loop.String()
		sc.LoopStart, sc.LoopEnd = s.LoopStart, s.LoopEnd
	}
	if s.Volume != 1 && s.Volume != 0 {
		v := s.Volume
		sc.Volume = &v
	}
	b, err := yaml.Marshal(sc)
	if err != nil {
		return err
	}
	return os.WriteFile(base+SampleExt, b, 0o644)
}

// ---------- the cache ----------

type sampleEntry struct {
	s   *Sample
	err error
}

var (
	sampleMu    sync.Mutex
	sampleCache = map[string]sampleEntry{}
)

// SampleFor returns a library sample, decoding it on first use. Failures are
// cached and logged once, the policy audio.ClipFor follows.
func SampleFor(name string) (*Sample, error) {
	if name == "" {
		return nil, fmt.Errorf("no sample named")
	}
	sampleMu.Lock()
	defer sampleMu.Unlock()
	if e, ok := sampleCache[name]; ok {
		return e.s, e.err
	}
	path := SamplePath(name)
	var e sampleEntry
	if path == "" {
		e.err = fmt.Errorf("no file for sample %q under %s", name, SamplesDir())
	} else {
		e.s, e.err = LoadSampleFile(name, path)
	}
	if e.err != nil {
		log.Printf("tracker: %v", e.err)
	}
	sampleCache[name] = e
	return e.s, e.err
}

// PutSample replaces a cached sample — the sample editor's way of letting
// songs hear an edit before it is saved.
func PutSample(s *Sample) {
	sampleMu.Lock()
	sampleCache[s.Name] = sampleEntry{s: s}
	sampleMu.Unlock()
}

// ReloadSamples drops the cache. Voices already playing keep their data.
func ReloadSamples() {
	sampleMu.Lock()
	sampleCache = map[string]sampleEntry{}
	sampleMu.Unlock()
}

// ScanSampleNames lists the library, sorted, as names songs use.
func ScanSampleNames() []string {
	root := SamplesDir()
	seen := map[string]bool{}
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		for _, e := range audio.SoundExts {
			if ext == e {
				rel, err := filepath.Rel(root, p)
				if err == nil {
					seen[filepath.ToSlash(strings.TrimSuffix(rel, filepath.Ext(rel)))] = true
				}
			}
		}
		return nil
	})
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
