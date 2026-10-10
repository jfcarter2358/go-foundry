package audio

// audio_registry.go — the clip cache.
//
// Modelled on world.LoadEntityDef's cache, including the part that looks like
// a bug and is not: failures are cached too. A level with forty torches naming
// a clip that is not there should say so once, not forty times a frame for as
// long as the level is open.
//
// Guarded by a mutex because a script can start a sound during the tick while
// the mixer goroutine is reading, and because the editor's asset picker
// rescans from the UI thread.

import (
	"fmt"
	"log"
	"sync"
)

type clipEntry struct {
	clip *Clip
	err  error
}

var (
	clipMu    sync.Mutex
	clipCache = map[string]clipEntry{}
)

// ClipFor returns the named clip, decoding it on first use.
//
// The error is worth reporting once and then ignoring: a missing sound is a
// missing asset, not a broken level, and the placement stays selectable in the
// editor so the author can see what to fix. That is the same policy the level
// loader applies to an emitter whose .fpar will not read.
func ClipFor(name string) (*Clip, error) {
	if name == "" {
		return nil, fmt.Errorf("no clip named")
	}

	clipMu.Lock()
	defer clipMu.Unlock()

	if e, ok := clipCache[name]; ok {
		return e.clip, e.err
	}

	path := SoundPath(name)
	if path == "" {
		err := fmt.Errorf("no file for sound %q under %s (looked for %v)", name, SoundsDirName, SoundExts)
		clipCache[name] = clipEntry{err: err}
		log.Printf("audio: %v", err)
		return nil, err
	}

	c, err := LoadClip(name, path)
	clipCache[name] = clipEntry{clip: c, err: err}
	if err != nil {
		log.Printf("audio: %s: %v", name, err)
	}
	return c, err
}

// ReloadClips drops the cache, so an asset re-exported while the editor is
// open is picked up without a restart. This is what the editor's Reload button
// calls, mirroring world.ReloadEntityDefs.
//
// Voices already playing keep the Clip they hold — it is immutable and still
// valid — so a reload never cuts a sound off mid-play.
func ReloadClips() {
	clipMu.Lock()
	clipCache = map[string]clipEntry{}
	clipMu.Unlock()
}

// CachedClipCount reports how many entries the cache holds, successes and
// failures alike. For the debug overlay and for tests.
func CachedClipCount() int {
	clipMu.Lock()
	defer clipMu.Unlock()
	return len(clipCache)
}
