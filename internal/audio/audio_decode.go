package audio

// audio_decode.go — turning a file on disk into interleaved float32 frames.
//
// Three formats, one interface. WAV is parsed here rather than handed to
// sdl.LoadWAV: go-sdl2's LoadWAV wraps SDL-allocated C memory in a hand-built
// slice header and never calls SDL_FreeWAV, so every clip loaded through it
// leaks and the slice aliases memory the Go collector knows nothing about.
// A few hundred lines of RIFF parsing buys back ordinary Go memory and, more
// usefully, a decode path that tests can exercise with no SDL at all.
//
// Everything normalises to float32 in [-1,1], interleaved, at the file's own
// sample rate. Rate conversion is deliberately *not* done here: the mixer
// already steps through source frames at a per-voice ratio to get pitch and
// doppler, so making it also absorb the device/file rate difference costs one
// multiply and saves a whole resampling stage.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"
)

// decoder yields interleaved float32 frames at a fixed rate and channel count.
type decoder interface {
	// ReadFrames fills dst with whole interleaved frames and returns how many
	// it wrote. A short read is not an error; io.EOF with n > 0 means "this
	// much, and that was the last of it".
	ReadFrames(dst []float32) (int, error)
	Rate() int
	Channels() int
	Close() error
}

// errUnsupportedFormat names an extension nothing here can read.
var errUnsupportedFormat = errors.New("unsupported audio format")

// openDecoder picks a decoder by extension. The extension is the only signal
// used — sniffing magic bytes would let a file called .wav decode as MP3,
// which is a confusing way to be helpful.
func openDecoder(path string) (decoder, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	var d decoder
	switch strings.ToLower(filepath.Ext(path)) {
	case ".wav":
		d, err = newWAVDecoder(f)
	case ".ogg":
		d, err = newOggDecoder(f)
	case ".mp3":
		d, err = newMP3Decoder(f)
	default:
		f.Close()
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, filepath.Ext(path))
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return d, nil
}

// ---------- WAV ----------

type wavDecoder struct {
	src      io.ReadCloser
	rate     int
	channels int
	bits     int
	isFloat  bool
	// remaining counts bytes left in the data chunk, so trailing chunks after
	// the samples (LIST, id3) are not decoded as audio.
	remaining int64
	buf       []byte
}

func newWAVDecoder(f io.ReadCloser) (*wavDecoder, error) {
	var hdr [12]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return nil, fmt.Errorf("wav: short header: %w", err)
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return nil, errors.New("wav: not a RIFF/WAVE file")
	}

	d := &wavDecoder{src: f}
	seenFmt := false
	for {
		var ch [8]byte
		if _, err := io.ReadFull(f, ch[:]); err != nil {
			if seenFmt {
				return nil, errors.New("wav: no data chunk")
			}
			return nil, fmt.Errorf("wav: truncated: %w", err)
		}
		id := string(ch[0:4])
		size := int64(binary.LittleEndian.Uint32(ch[4:8]))

		switch id {
		case "fmt ":
			if size < 16 {
				return nil, errors.New("wav: fmt chunk too small")
			}
			body := make([]byte, size)
			if _, err := io.ReadFull(f, body); err != nil {
				return nil, fmt.Errorf("wav: truncated fmt: %w", err)
			}
			format := binary.LittleEndian.Uint16(body[0:2])
			d.channels = int(binary.LittleEndian.Uint16(body[2:4]))
			d.rate = int(binary.LittleEndian.Uint32(body[4:8]))
			d.bits = int(binary.LittleEndian.Uint16(body[14:16]))
			// WAVE_FORMAT_EXTENSIBLE keeps the real format in a subformat GUID
			// whose first two bytes are the plain tag.
			if format == 0xFFFE && size >= 40 {
				format = binary.LittleEndian.Uint16(body[24:26])
			}
			switch format {
			case 1: // PCM
			case 3: // IEEE float
				d.isFloat = true
			default:
				return nil, fmt.Errorf("wav: unsupported encoding %d (only PCM and float are read; compressed WAV is not)", format)
			}
			seenFmt = true
		case "data":
			if !seenFmt {
				return nil, errors.New("wav: data chunk before fmt")
			}
			d.remaining = size
			if err := d.validate(); err != nil {
				return nil, err
			}
			return d, nil
		default:
			// Chunks are word-aligned; an odd size carries a pad byte.
			skip := size + size%2
			if _, err := io.CopyN(io.Discard, f, skip); err != nil {
				return nil, fmt.Errorf("wav: truncated at chunk %q: %w", id, err)
			}
		}
	}
}

func (d *wavDecoder) validate() error {
	if d.channels < 1 || d.channels > 8 {
		return fmt.Errorf("wav: %d channels is not a sound", d.channels)
	}
	if d.rate < 1000 || d.rate > 384000 {
		return fmt.Errorf("wav: implausible sample rate %d", d.rate)
	}
	switch {
	case d.isFloat && d.bits == 32:
	case !d.isFloat && (d.bits == 8 || d.bits == 16 || d.bits == 24 || d.bits == 32):
	default:
		return fmt.Errorf("wav: unsupported bit depth %d", d.bits)
	}
	return nil
}

func (d *wavDecoder) Rate() int       { return d.rate }
func (d *wavDecoder) Channels() int   { return d.channels }
func (d *wavDecoder) Close() error    { return d.src.Close() }
func (d *wavDecoder) frameBytes() int { return d.bits / 8 * d.channels }

func (d *wavDecoder) ReadFrames(dst []float32) (int, error) {
	fb := d.frameBytes()
	want := len(dst) / d.channels
	if want == 0 || d.remaining < int64(fb) {
		return 0, io.EOF
	}
	if avail := int(d.remaining / int64(fb)); want > avail {
		want = avail
	}

	need := want * fb
	if cap(d.buf) < need {
		d.buf = make([]byte, need)
	}
	buf := d.buf[:need]

	n, err := io.ReadFull(d.src, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return 0, err
	}
	// A truncated file is playable up to the truncation; only whole frames.
	frames := n / fb
	d.remaining -= int64(frames * fb)

	bps := d.bits / 8
	for i := 0; i < frames*d.channels; i++ {
		dst[i] = decodeSample(buf[i*bps:], d.bits, d.isFloat)
	}
	if frames == 0 {
		return 0, io.EOF
	}
	return frames, nil
}

// decodeSample converts one little-endian sample to [-1,1].
func decodeSample(b []byte, bits int, isFloat bool) float32 {
	if isFloat {
		return math.Float32frombits(binary.LittleEndian.Uint32(b))
	}
	switch bits {
	case 8:
		// 8-bit WAV is unsigned, every other depth is signed. Getting this
		// wrong yields a loud DC offset rather than silence, so it is at
		// least easy to notice.
		return (float32(b[0]) - 128) / 128
	case 16:
		return float32(int16(binary.LittleEndian.Uint16(b))) / 32768
	case 24:
		v := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
		if v&0x800000 != 0 {
			v |= ^0xFFFFFF // sign-extend
		}
		return float32(v) / 8388608
	case 32:
		return float32(int32(binary.LittleEndian.Uint32(b))) / 2147483648
	}
	return 0
}

// ---------- OGG Vorbis ----------

type oggDecoder struct {
	src io.Closer
	r   *oggvorbis.Reader
	// partial holds samples of a frame split across two reads. oggvorbis
	// counts in samples, not frames, and makes no promise about landing on a
	// frame boundary.
	partial []float32
}

func newOggDecoder(f io.ReadCloser) (*oggDecoder, error) {
	r, err := oggvorbis.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("ogg: %w", err)
	}
	if r.Channels() < 1 {
		return nil, errors.New("ogg: no channels")
	}
	return &oggDecoder{src: f, r: r}, nil
}

func (d *oggDecoder) Rate() int     { return d.r.SampleRate() }
func (d *oggDecoder) Channels() int { return d.r.Channels() }
func (d *oggDecoder) Close() error  { return d.src.Close() }

func (d *oggDecoder) ReadFrames(dst []float32) (int, error) {
	ch := d.r.Channels()
	n := copy(dst, d.partial)
	d.partial = d.partial[n:]
	if len(d.partial) > 0 {
		return n / ch, nil
	}

	for n+ch <= len(dst) {
		got, err := d.r.Read(dst[n:])
		n += got
		if err == io.EOF || (got == 0 && err == nil) {
			break
		}
		if err != nil {
			return n / ch, err
		}
	}

	// Hold back a trailing part-frame for the next call rather than emitting
	// a half frame, which would swap the channels for the rest of the file.
	if rem := n % ch; rem != 0 {
		d.partial = append(d.partial[:0], dst[n-rem:n]...)
		n -= rem
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n / ch, nil
}

// ---------- MP3 ----------

// mp3Decoder wraps go-mp3, which always yields 16-bit stereo regardless of
// what the file holds.
type mp3Decoder struct {
	src io.Closer
	d   *mp3.Decoder
	buf []byte
}

func newMP3Decoder(f io.ReadCloser) (*mp3Decoder, error) {
	d, err := mp3.NewDecoder(f)
	if err != nil {
		return nil, fmt.Errorf("mp3: %w", err)
	}
	return &mp3Decoder{src: f, d: d}, nil
}

func (d *mp3Decoder) Rate() int     { return d.d.SampleRate() }
func (d *mp3Decoder) Channels() int { return 2 }
func (d *mp3Decoder) Close() error  { return d.src.Close() }

func (d *mp3Decoder) ReadFrames(dst []float32) (int, error) {
	frames := len(dst) / 2
	if frames == 0 {
		return 0, io.EOF
	}
	need := frames * 4 // 2 channels * int16
	if cap(d.buf) < need {
		d.buf = make([]byte, need)
	}
	buf := d.buf[:need]

	n, err := io.ReadFull(d.src4(), buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return 0, err
	}
	got := n / 4
	for i := 0; i < got*2; i++ {
		dst[i] = float32(int16(binary.LittleEndian.Uint16(buf[i*2:]))) / 32768
	}
	if got == 0 {
		return 0, io.EOF
	}
	return got, nil
}

// src4 exists only so ReadFrames can use io.ReadFull against the decoder,
// which reads whole MP3 frames internally and so returns short reads freely.
func (d *mp3Decoder) src4() io.Reader { return d.d }

// ---------- shared ----------

// decodeAll drains a decoder into one slice. Used for clips, never for music.
func decodeAll(d decoder, limitFrames int) ([]float32, error) {
	ch := d.Channels()
	out := make([]float32, 0, 64*1024)
	block := make([]float32, 4096*ch)
	total := 0
	for {
		n, err := d.ReadFrames(block)
		if n > 0 {
			out = append(out, block[:n*ch]...)
			total += n
			if limitFrames > 0 && total > limitFrames {
				return nil, fmt.Errorf("clip is longer than %d frames; load it as music instead", limitFrames)
			}
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return out, nil
		}
	}
}
