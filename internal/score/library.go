package score

// library.go — scores by name.
//
// Scores live beside every other sound, under sounds/, with the .fscore
// extension, exactly as tracker songs do: "hangar_theme" is
// sounds/hangar_theme.fscore. Loaded scores are cached; the result is
// shared, so anything that changes one clones it first.

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"foundry/internal/audio"
	"foundry/internal/tracker"
)

// ScorePath resolves a score name (a path under sounds/, no extension) to
// its file, or "".
func ScorePath(name string) string {
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

// IsScore reports whether a name resolves to a score.
func IsScore(name string) bool { return ScorePath(name) != "" }

// nameFor is a file's library name, or its base name outside sounds/.
func nameFor(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		if root, err := filepath.Abs(audio.SoundsDir()); err == nil {
			if rel, err := filepath.Rel(root, abs); err == nil && !strings.HasPrefix(rel, "..") {
				return filepath.ToSlash(strings.TrimSuffix(rel, filepath.Ext(rel)))
			}
		}
	}
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

type scoreEntry struct {
	s   *Score
	err error
}

var (
	scoreMu    sync.Mutex
	scoreCache = map[string]scoreEntry{}
)

// Load returns a score by name. The result is shared: clone it before
// changing it.
func Load(name string) (*Score, error) {
	name = strings.TrimSuffix(name, FileExt)
	scoreMu.Lock()
	defer scoreMu.Unlock()
	if e, ok := scoreCache[name]; ok {
		return e.s, e.err
	}
	var e scoreEntry
	path := ScorePath(name)
	if path == "" {
		e.err = fmt.Errorf("no score %q under %s", name, audio.SoundsDir())
	} else {
		e.s, e.err = LoadFile(path)
	}
	if e.err != nil {
		log.Printf("score: %v", e.err)
	}
	scoreCache[name] = e
	return e.s, e.err
}

// Reload drops the score cache (an editor save, a file that changed).
func Reload() {
	scoreMu.Lock()
	scoreCache = map[string]scoreEntry{}
	scoreMu.Unlock()
}

// Save writes a score into the library under name and drops it from the
// cache.
func Save(name string, s *Score) error {
	name = strings.TrimSuffix(name, FileExt)
	path := filepath.Join(audio.SoundsDir(), filepath.FromSlash(name)+FileExt)
	if err := SaveFile(path, s); err != nil {
		return err
	}
	scoreMu.Lock()
	delete(scoreCache, name)
	scoreMu.Unlock()
	return nil
}

// ScanScoreNames lists every score under sounds/, sorted.
func ScanScoreNames() []string {
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

// InstrumentRefs lists the library instruments a score's lines name.
func InstrumentRefs(s *Score) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range s.Lines {
		if l.Instrument != "" && !seen[l.Instrument] {
			seen[l.Instrument] = true
			out = append(out, l.Instrument)
		}
	}
	sort.Strings(out)
	return out
}

// ScoreSamples lists the library samples a score file's instruments play,
// for the release packer and the unused-asset scan.
func ScoreSamples(path string) ([]string, error) {
	s, err := LoadFile(path)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, ref := range InstrumentRefs(s) {
		p := tracker.InstrumentPath(ref)
		if p == "" {
			continue
		}
		in, err := tracker.LoadInstrumentFile(p)
		if err != nil {
			continue
		}
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
