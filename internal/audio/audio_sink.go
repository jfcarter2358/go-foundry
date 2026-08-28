package audio

// audio_sink.go — the one place SDL is spoken to, and the seam a test replaces.
//
// Everything above this file deals in float32 frames. A Sink is what turns
// those into whatever the device actually accepted, which may not be what was
// asked for. Keeping that conversion here means the mixer never has to know
// the device's format, and a test can drive the entire package with a Sink
// that just remembers what it was handed.

import (
	"fmt"
	"sync"

	"github.com/veandco/go-sdl2/sdl"
)

// Sink is an opened output.
type Sink interface {
	// Write hands over one block of interleaved stereo float32 frames.
	Write(block []float32) error
	// QueuedFrames reports how much is buffered ahead, in stereo frames. The
	// mixer uses it to decide whether to produce another block.
	QueuedFrames() int
	// Rate is the device's actual sample rate, which may not be the one asked
	// for.
	Rate() int
	Close() error
}

// OpenSink is the constructor the device uses. It is a variable so a test can
// install a recording sink and drive the real Init/Play/Shutdown path with no
// hardware, the same way particle.Collide is installed from above.
var OpenSink = openSDLSink

// ---------- SDL ----------

type sdlSink struct {
	dev  sdl.AudioDeviceID
	rate int
	// s16 is set when the driver refused float output. Some ALSA and dmix
	// configurations do.
	s16 bool
	buf []byte
}

func openSDLSink(cfg Config) (Sink, error) {
	want := sdl.AudioSpec{
		Freq:     int32(cfg.Rate),
		Format:   sdl.AUDIO_F32SYS,
		Channels: 2,
		Samples:  uint16(pow2AtLeast(cfg.Rate * cfg.ChunkMS / 1000)),
	}

	// Frequency and format may be renegotiated; the channel count may not.
	//
	// AUDIO_ALLOW_CHANNELS_CHANGE is deliberately absent. A driver handing
	// back a mono device would take the pan silently with it, and every
	// positional sound in the game would be centred with nothing anywhere
	// reporting a problem. Better to fail the open and fall back explicitly.
	const allowed = sdl.AUDIO_ALLOW_FREQUENCY_CHANGE | sdl.AUDIO_ALLOW_FORMAT_CHANGE

	attempts := []sdl.AudioSpec{want, want, want}
	attempts[1].Format = sdl.AUDIO_S16SYS
	attempts[2].Format = sdl.AUDIO_S16SYS
	attempts[2].Freq = 44100

	var lastErr error
	for _, spec := range attempts {
		desired := spec
		var got sdl.AudioSpec
		dev, err := sdl.OpenAudioDevice(cfg.DeviceName, false, &desired, &got, allowed)
		if err != nil {
			lastErr = err
			continue
		}
		if got.Channels != 2 {
			sdl.CloseAudioDevice(dev)
			lastErr = fmt.Errorf("device offered %d channels, not stereo", got.Channels)
			continue
		}
		s := &sdlSink{dev: dev, rate: int(got.Freq), s16: got.Format != sdl.AUDIO_F32SYS}
		sdl.PauseAudioDevice(dev, false)
		return s, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable audio device")
	}
	return nil, lastErr
}

func (s *sdlSink) Rate() int { return s.rate }

func (s *sdlSink) Write(block []float32) error {
	if s.s16 {
		need := len(block) * 2
		if cap(s.buf) < need {
			s.buf = make([]byte, need)
		}
		b := s.buf[:need]
		for i, v := range block {
			// 32767, not 32768: scaling by 32768 lets +1.0 wrap to -32768,
			// which is the loudest possible click.
			x := int16(clampUnit(v) * 32767)
			b[i*2] = byte(uint16(x))
			b[i*2+1] = byte(uint16(x) >> 8)
		}
		return sdl.QueueAudio(s.dev, b)
	}

	need := len(block) * 4
	if cap(s.buf) < need {
		s.buf = make([]byte, need)
	}
	b := s.buf[:need]
	for i, v := range block {
		putFloat32LE(b[i*4:], clampUnit(v))
	}
	return sdl.QueueAudio(s.dev, b)
}

func (s *sdlSink) QueuedFrames() int {
	bytesPerFrame := 8 // stereo float32
	if s.s16 {
		bytesPerFrame = 4
	}
	return int(sdl.GetQueuedAudioSize(s.dev)) / bytesPerFrame
}

func (s *sdlSink) Close() error {
	sdl.PauseAudioDevice(s.dev, true)
	sdl.ClearQueuedAudio(s.dev)
	sdl.CloseAudioDevice(s.dev)
	return nil
}

func pow2AtLeast(n int) int {
	p := 256
	for p < n && p < 8192 {
		p *= 2
	}
	return p
}

func putFloat32LE(b []byte, v float32) {
	u := float32bits(v)
	b[0] = byte(u)
	b[1] = byte(u >> 8)
	b[2] = byte(u >> 16)
	b[3] = byte(u >> 24)
}

// ---------- the recording sink, for tests ----------

// RecordingSink accepts blocks and keeps them, reporting a queue depth that
// drains at whatever rate the test asks for. It is exported so tests in other
// packages — world's and script's — can install it and assert on what a frame
// actually produced without opening a device.
type RecordingSink struct {
	mu     sync.Mutex
	Blocks [][]float32
	// Drain is how many frames to pretend have played between reads. Zero
	// means the queue never drains, which is what a test wants when it is
	// asserting on one block rather than driving the loop.
	Drain    int
	rate     int
	queued   int
	closed   bool
	WriteErr error
}

// NewRecordingSink returns a sink at the given rate.
func NewRecordingSink(rate int) *RecordingSink { return &RecordingSink{rate: rate} }

func (r *RecordingSink) Rate() int { return r.rate }

func (r *RecordingSink) Write(block []float32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		// Writing after Close is the sdl.Quit use-after-free, in Go form.
		return fmt.Errorf("write after close")
	}
	if r.WriteErr != nil {
		return r.WriteErr
	}
	cp := make([]float32, len(block))
	copy(cp, block)
	r.Blocks = append(r.Blocks, cp)
	r.queued += len(block) / 2
	return nil
}

func (r *RecordingSink) QueuedFrames() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queued -= r.Drain
	if r.queued < 0 {
		r.queued = 0
	}
	return r.queued
}

func (r *RecordingSink) Close() error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	return nil
}

// Frames returns every block concatenated, for assertions.
func (r *RecordingSink) Frames() []float32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []float32
	for _, b := range r.Blocks {
		out = append(out, b...)
	}
	return out
}
