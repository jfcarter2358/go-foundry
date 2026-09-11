package audio

// audio_shipped_test.go — every clip that ships actually decodes.
//
// Mirrors particle_shipped_test.go, and exists for its reason: a worked
// example is documentation, and documentation rots. The symptom of a shipped
// clip that no longer loads is silence in the running game, which nothing else
// in the build can see.

import (
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
)

func TestEveryShippedClipDecodes(t *testing.T) {
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join("..", "..", "resources")
	ReloadClips()
	t.Cleanup(func() {
		fileutil.ResourcesPath = prev
		ReloadClips()
	})

	names := ScanSoundNames()
	if len(names) == 0 {
		t.Skip("no clips are shipped yet")
	}
	for _, n := range names {
		c, err := ClipFor(n)
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		if c.Frames() == 0 {
			t.Errorf("%s decoded to no audio", n)
		}
		if c.Channels != 1 && c.Channels != 2 {
			t.Errorf("%s has %d channels", n, c.Channels)
		}
		// A positional sound has to be mono: a stereo clip carries its own
		// image and is downmixed rather than panned, so shipping a stereo
		// example would be shipping a misleading one.
		if c.Channels != 1 {
			t.Errorf("%s is stereo; the shipped examples are meant to be placeable positionally", n)
		}
		t.Logf("%s: %.2fs, %d Hz, %d ch", n, c.Duration().Seconds(), c.Rate, c.Channels)
	}
}
