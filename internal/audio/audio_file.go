package audio

// audio_file.go — where sounds live on disk.
//
// Follows particle_file.go's spelling exactly: a directory name beside
// textures/ and particles/, a resolver from a bare name to a full path, and a
// scan for the editor's dropdown. The one difference is that a sound has three
// possible extensions rather than one, so SoundPath has to look rather than
// just concatenate — a level stores "footstep", not "footstep.ogg", so that
// re-encoding an asset does not require editing every level that plays it.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/fileutil"
)

// SoundsDirName is where clips and music live, beside particles/ and textures/.
const SoundsDirName = "sounds"

// SoundExts are the extensions understood, in the order SoundPath tries them.
// WAV first because it is what a short effect should be; the compressed
// formats are for music.
var SoundExts = []string{".wav", ".ogg", ".mp3"}

// SoundsDir is where the editor's picker looks.
func SoundsDir() string { return filepath.Join(fileutil.ResourcesPath, SoundsDirName) }

// SoundPath resolves a bare name to a full path, trying each known extension.
// It returns "" when nothing matches, which the caller reports as a missing
// asset rather than treating as an error worth failing a level load over.
//
// A name that already carries a known extension is honoured as given, so a
// hand-written level entry naming the exact file still works.
func SoundPath(name string) string {
	if name == "" {
		return ""
	}
	if ext := strings.ToLower(filepath.Ext(name)); knownExt(ext) {
		p := filepath.Join(SoundsDir(), name)
		if fileExists(p) {
			return p
		}
		return ""
	}
	for _, ext := range SoundExts {
		p := filepath.Join(SoundsDir(), name+ext)
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func knownExt(ext string) bool {
	for _, e := range SoundExts {
		if e == ext {
			return true
		}
	}
	return false
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// ScanSoundNames lists the bare names under sounds/, extension stripped and
// deduplicated, for the editor's dropdown. Deduplication matters because
// re-encoding an asset commonly leaves both foo.wav and foo.ogg behind, and
// SoundPath would only ever reach the first.
func ScanSoundNames() []string {
	seen := map[string]bool{}
	for _, ext := range SoundExts {
		for _, f := range fileutil.ScanFilesByExt(SoundsDir(), ext) {
			seen[strings.TrimSuffix(f, filepath.Ext(f))] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
