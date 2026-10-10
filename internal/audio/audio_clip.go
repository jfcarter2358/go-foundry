package audio

// audio_clip.go — a short sound, decoded once and kept.
//
// A Clip is immutable after LoadClip returns. That is what lets the mixer
// goroutine read one while the game thread is starting another voice on it
// with no locking at all: the only mutable thing a voice holds is its own
// playback cursor.

import (
	"fmt"
	"time"
)

// maxClipFrames caps what will be held fully decoded, at about 30 seconds of
// 48 kHz stereo. Past that the memory is better spent streaming: a clip is
// stored as float32, so a three-minute track would be ~70 MB resident.
const maxClipFrames = 48000 * 30

// Clip is fully decoded audio, interleaved float32 in [-1,1].
type Clip struct {
	// Name is the bare asset name, for log messages.
	Name string
	// Samples is Frames*Channels interleaved values. Never mutated after load.
	Samples []float32
	// Rate is the clip's own sample rate, not the device's. The mixer steps
	// through the clip at a ratio that folds this together with pitch and
	// doppler, so no resampling pass happens at load.
	Rate int
	// Channels is 1 or 2. Only mono clips spatialize meaningfully — a stereo
	// clip already carries its own image, so panning it would fight the
	// author. Positional playback of a stereo clip downmixes; see mixVoice.
	Channels int
}

// Frames is the clip's length in sample frames.
func (c *Clip) Frames() int {
	if c == nil || c.Channels == 0 {
		return 0
	}
	return len(c.Samples) / c.Channels
}

// Duration is how long the clip runs at its own rate.
func (c *Clip) Duration() time.Duration {
	if c == nil || c.Rate == 0 {
		return 0
	}
	return time.Duration(float64(c.Frames()) / float64(c.Rate) * float64(time.Second))
}

// LoadClip decodes a file fully into memory. It does no caching; callers go
// through Clip(name) in audio_registry.go.
func LoadClip(name, path string) (*Clip, error) {
	d, err := openDecoder(path)
	if err != nil {
		return nil, err
	}
	defer d.Close()

	samples, err := decodeAll(d, maxClipFrames)
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("%s decoded to no audio", path)
	}
	ch := d.Channels()
	if ch > 2 {
		// Nothing downstream knows what to do with 5.1, and silently dropping
		// channels would be worse than saying so.
		return nil, fmt.Errorf("%s has %d channels; only mono and stereo are supported", path, ch)
	}
	return &Clip{Name: name, Samples: samples, Rate: d.Rate(), Channels: ch}, nil
}
