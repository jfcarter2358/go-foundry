package video

// Package video is a video file, decoded: a track of frames with presentation
// times, an optional soundtrack, and a player that walks one against a clock.
//
// It sits where internal/audio sits, and under the same rule — no GL, no level,
// no window. A frame comes out as an *image.RGBA and it is the caller's job to
// get that onto a texture, which is what keeps every decision in here testable
// with no context current and what lets the UI editor's preview and the running
// game drive one decoder.
//
// Two formats, both decoded by the standard library, and no new native
// dependency — the constraint internal/audio already works under. An AVI
// holding motion-JPEG frames is `image/jpeg` plus a RIFF walk; an animated GIF
// is `image/gif` plus a disposal-method composite. Between them they cover what
// a game actually plays on a screen: a pre-rendered cutscene, a logo sting, an
// animated background.
//
// What they deliberately do not cover is a modern inter-frame codec. H.264 or
// VP9 means either a cgo dependency — which this engine has spent real effort
// not having — or a decoder that is a larger program than the renderer it would
// live next to. Motion-JPEG pays for that in file size and in nothing else:
// every frame is a keyframe, so seeking is a division rather than a hunt
// backwards for the last I-frame, and the encode is one ffmpeg line:
//
//	ffmpeg -i in.mp4 -c:v mjpeg -q:v 3 -c:a pcm_s16le out.avi
//
// video_decode.go holds what every format has in common: the track, and the
// dispatch that picks a reader for a file.

import (
	"fmt"
	"image"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/audio"
)

// Track is the shape of what a decoder found: how big the picture is, when each
// frame is meant to be on screen, and the soundtrack if there is one.
type Track struct {
	Width, Height int

	// Starts is the presentation time of each frame in seconds, ascending, one
	// entry per frame. A list rather than a frame rate because a GIF's frames
	// have individual delays — a title card held for two seconds followed by
	// twelve frames of animation is an ordinary GIF and not expressible as a
	// rate. A constant-rate AVI simply fills it with i/fps.
	Starts []float64

	// Duration is where the last frame ends, which is what a loop wraps at.
	Duration float64

	// Sound is the demuxed soundtrack, or nil. It is an audio.Clip rather than
	// anything of this package's own so that it plays through the one mixer
	// with the one set of gain sliders — a video whose audio ignored the
	// master volume would be a bug reported as "the settings do nothing".
	Sound *audio.Clip
}

// Count is how many frames the track has.
func (t *Track) Count() int { return len(t.Starts) }

// FrameAt is the index of the frame that should be on screen at a time in
// seconds. Times before the first frame and after the last both clamp, so a
// player never has to special-case either end.
func (t *Track) FrameAt(sec float64) int {
	n := len(t.Starts)
	if n == 0 {
		return 0
	}
	if sec <= t.Starts[0] {
		return 0
	}
	// The first frame that starts *after* sec, minus one — the frame in force.
	i := sort.SearchFloat64s(t.Starts, sec)
	if i >= n || t.Starts[i] > sec {
		i--
	}
	if i < 0 {
		i = 0
	}
	if i >= n {
		i = n - 1
	}
	return i
}

// decoder turns a file into frames on demand.
//
// Frames are addressed by index rather than pulled from a stream, because
// seeking a stream is the whole difficulty of video playback and indexing is
// how motion-JPEG makes it go away. It also means a paused player costs
// nothing: it is not holding a decode half-finished, it simply is not asking.
type decoder interface {
	// Track is the file's shape. Fixed once open succeeds.
	Track() *Track
	// Frame decodes one frame. The returned image is owned by the decoder and
	// is overwritten by the next call, so a caller that needs to keep it must
	// upload or copy first.
	Frame(i int) (*image.RGBA, error)
	Close() error
}

// open picks a decoder by extension.
//
// The extension is the only signal, which is openDecoder's rule in
// internal/audio and for its reason: sniffing magic bytes would let a file
// called .gif decode as an AVI, which is a confusing way to be helpful.
func open(name, path string) (decoder, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".avi":
		return openAVI(name, path)
	case ".gif":
		return openGIF(name, path)
	}
	return nil, fmt.Errorf("%s: unsupported video format %q (this engine reads %v)",
		name, filepath.Ext(path), VideoExts)
}
