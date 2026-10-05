package tracker

// library.go — songs by name.
//
// Songs live beside every other sound, under sounds/, with the .ftrk
// extension: `play_music("combat")` finds sounds/combat.ftrk the same way it
// would find sounds/combat.ogg. Loaded songs are cached with their samples
// resolved; a player is given a clone, so an editor reload never pulls a song
// out from under one that is playing.

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"foundry/internal/audio"
)

// SongPath resolves a song name (a path under sounds/, no extension) to its
// file, or "".
func SongPath(name string) string {
	if name == "" {
		return ""
	}
	name = strings.TrimSuffix(name, FileExt)
	p := filepath.Join(audio.SoundsDir(), filepath.FromSlash(name)+FileExt)
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p
	}
	return ""
}

// IsSong reports whether a name resolves to a song rather than a clip.
func IsSong(name string) bool { return SongPath(name) != "" }

type songEntry struct {
	s   *Song
	err error
}

var (
	songMu    sync.Mutex
	songCache = map[string]songEntry{}
)

// Load returns a song by name with its samples resolved. The result is
// shared: clone it before changing it.
func Load(name string) (*Song, error) {
	name = strings.TrimSuffix(name, FileExt)
	songMu.Lock()
	defer songMu.Unlock()
	if e, ok := songCache[name]; ok {
		return e.s, e.err
	}
	var e songEntry
	path := SongPath(name)
	if path == "" {
		e.err = fmt.Errorf("no song %q under %s", name, audio.SoundsDir())
	} else if e.s, e.err = LoadFile(path); e.err == nil {
		for _, err := range ResolveSamples(e.s) {
			log.Printf("tracker: %s: %v", name, err)
		}
	}
	if e.err != nil {
		log.Printf("tracker: %v", e.err)
	}
	songCache[name] = e
	return e.s, e.err
}

// ResolveSamples loads every instrument's samples from the library.
func ResolveSamples(s *Song) []error {
	var errs []error
	for _, in := range s.Instruments {
		errs = append(errs, in.Resolve(SampleFor)...)
	}
	return errs
}

// ReloadSongs drops the song cache (and the sample cache with it).
func ReloadSongs() {
	songMu.Lock()
	songCache = map[string]songEntry{}
	songMu.Unlock()
	ReloadSamples()
}

// ScanSongNames lists every song under sounds/, sorted.
func ScanSongNames() []string {
	root := audio.SoundsDir()
	var out []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.EqualFold(filepath.Ext(p), FileExt) {
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

// SongSamples lists the library samples a song file references, for the
// release packer and the unused-asset scan.
func SongSamples(path string) ([]string, error) {
	s, err := LoadFile(path)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, in := range s.Instruments {
		for _, n := range in.Samples {
			if n != "" && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// PlayOptions starts a song as a music voice.
type PlayOptions struct {
	Section string
	Gain    float32
	FadeIn  float32
	Params  map[string]float32
	// Resume starts at Order and Row instead of Section (save games).
	Resume     bool
	Order, Row int
}

// Play loads a song and starts it on the music bus. The player is the handle
// scripts steer; the voice is the mixer's. Both are zero when the song will
// not load or there is no audio device.
func Play(name string, o PlayOptions) (*Player, audio.VoiceID, error) {
	s, err := Load(name)
	if err != nil {
		return nil, 0, err
	}
	p := NewPlayer(s.Clone())
	if o.Resume {
		p.StartAt(o.Order, o.Row)
	} else {
		p.Start(o.Section)
	}
	for k, v := range o.Params {
		p.SetParamNow(k, v)
	}
	id := audio.PlayGenerator(p, audio.PlayOptions{Bus: audio.BusMusic, Gain: o.Gain, FadeIn: o.FadeIn, Priority: audio.PriorityHigh})
	return p, id, nil
}
