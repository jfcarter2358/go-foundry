package tracker

// render.go — playing a song with no device: WAV export and tests.
//
// The player is deterministic given a song, a start and a command log, so an
// offline render is the same audio a game would hear. That is what makes a
// golden-hash test of the whole sequencer possible.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"os"
)

// RenderOptions controls an offline render.
type RenderOptions struct {
	Rate int // default 48000
	// Section starts there; "" is the top.
	Section string
	// Seconds caps the length. 0 renders one pass of the order list (or
	// until a non-looping song ends) plus Tail.
	Seconds float64
	// Tail is extra time for reverb and releases after the end, seconds.
	Tail float64
	// Params are set before playback starts.
	Params map[string]float32
}

// Render plays a song offline and returns interleaved stereo.
func Render(s *Song, o RenderOptions) []float32 {
	if o.Rate <= 0 {
		o.Rate = 48000
	}
	p := NewPlayer(s)
	for k, v := range o.Params {
		p.SetParamNow(k, v)
	}
	p.Start(o.Section)
	secs := o.Seconds
	if secs <= 0 {
		secs = s.Duration() + o.Tail
		if o.Section != "" {
			if i := s.SectionIndex(o.Section); i >= 0 {
				sec := s.Sections[i]
				rows := 0
				for _, ord := range s.Order[sec.Start:sec.End] {
					rows += s.Patterns[ord].Rows()
				}
				secs = float64(rows)*s.SecondsPerRow() + o.Tail
			}
		}
	}
	total := int(math.Ceil(secs * float64(o.Rate)))
	out := make([]float32, total*2)
	const block = 480
	for i := 0; i < total; i += block {
		n := min(block, total-i)
		more := p.Render(out[i*2:(i+n)*2], n, o.Rate)
		if !more {
			out = out[:(i+n)*2]
			break
		}
	}
	return out
}

// RenderToWAV renders a song to a 24-bit stereo WAV file.
func RenderToWAV(s *Song, path string, o RenderOptions) error {
	if o.Rate <= 0 {
		o.Rate = 48000
	}
	data := Render(s, o)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := WriteWAV(f, data, 2, o.Rate, 24); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Hash is a short digest of rendered audio, for golden tests.
func Hash(data []float32) string {
	h := sha256.New()
	var b [4]byte
	for _, v := range data {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(v))
		h.Write(b[:])
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Peak is the largest absolute sample.
func Peak(data []float32) float32 {
	var m float32
	for _, v := range data {
		if a := absf(v); a > m {
			m = a
		}
	}
	return m
}
