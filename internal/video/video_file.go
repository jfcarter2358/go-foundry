package video

// video_file.go — where videos live on disk.
//
// Spelled exactly like audio_file.go, which is spelled exactly like
// particle_file.go: a directory name beside sounds/ and textures/, a resolver
// from a bare name to a full path, and a scan for the editor's dropdown. A
// widget stores "intro", not "intro.avi", so re-encoding an asset does not
// mean editing every screen that plays it.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/fileutil"
)

// VideoDirName is where clips live, beside sounds/ and particles/.
const VideoDirName = "video"

// VideoExts are the extensions understood, in the order VideoPath tries them.
//
// Two, and both are decoded by the standard library: an AVI holding
// motion-JPEG video and PCM audio, and an animated GIF. That is the whole
// format list, and it is a deliberate stopping point rather than a first
// instalment — see video_decode.go for what a third one would cost.
var VideoExts = []string{".avi", ".gif"}

// VideoDir is where the editor's picker looks.
func VideoDir() string { return filepath.Join(fileutil.ExecutablePath, VideoDirName) }

// VideoPath resolves a bare name to a full path, trying each known extension.
// It returns "" when nothing matches, which the caller reports as a missing
// asset rather than as an error worth failing a screen over.
//
// A name that already carries a known extension is honoured as given, so a
// hand-written .fui naming the exact file still works.
func VideoPath(name string) string {
	if name == "" {
		return ""
	}
	if ext := strings.ToLower(filepath.Ext(name)); knownExt(ext) {
		p := filepath.Join(VideoDir(), name)
		if fileExists(p) {
			return p
		}
		return ""
	}
	for _, ext := range VideoExts {
		p := filepath.Join(VideoDir(), name+ext)
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func knownExt(ext string) bool {
	for _, e := range VideoExts {
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

// ScanVideoNames lists the bare names under video/, extension stripped and
// deduplicated, for the editor's dropdown. Deduplication matters because
// re-encoding commonly leaves both intro.gif and intro.avi behind, and
// VideoPath would only ever reach the first.
func ScanVideoNames() []string {
	seen := map[string]bool{}
	for _, ext := range VideoExts {
		for _, f := range fileutil.ScanFilesByExt(VideoDir(), ext) {
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
