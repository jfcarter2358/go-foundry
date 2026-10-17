package sfz

// wav.go — a sample file's own pitch and loop.
//
// A WAV made for a sampler carries a 'smpl' chunk: the note it was
// recorded at (with a fraction of a semitone) and its loop points. An SFZ
// region that gives no pitch_keycenter=sample or no loop_start uses them,
// so they are read here beside the audio internal/audio decodes (and FLAC,
// which it does not, by internal/score/io/audioenc's decoder).

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"

	"foundry/internal/audio"
	"foundry/internal/score/io/audioenc"
)

// loadAudio decodes a sample file with its smpl chunk's root and loop. A
// FLAC sample is read by the score's own FLAC decoder (the game's audio
// reads WAV, OGG and MP3 only); the instrument keeps it as WAV.
func loadAudio(path string) (*Audio, error) {
	if strings.EqualFold(pathExt(path), ".flac") {
		return loadFLAC(path)
	}
	clip, err := audio.LoadClip(path, path)
	if err != nil {
		return nil, err
	}
	a := &Audio{Name: path, Data: clip.Samples, Channels: clip.Channels, Rate: clip.Rate, Root: -1}
	if strings.EqualFold(pathExt(path), ".wav") {
		if sm, ok := readSmpl(path); ok {
			a.Root, a.Tune = sm.root, sm.tune
			if sm.loopEnd > sm.loopStart && sm.loopEnd <= a.Frames() {
				a.LoopStart, a.LoopEnd = sm.loopStart, sm.loopEnd
			}
		}
	}
	return a, nil
}

// loadFLAC decodes a FLAC sample to floats.
func loadFLAC(path string) (*Audio, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	st, err := audioenc.DecodeFLAC(b)
	if err != nil {
		return nil, err
	}
	if st.Channels < 1 || st.Bits < 1 || st.Bits > 32 {
		return nil, fmt.Errorf("FLAC: %d channels of %d bits", st.Channels, st.Bits)
	}
	scale := 1 / float32(int64(1)<<(st.Bits-1))
	data := make([]float32, len(st.Samples))
	for i, v := range st.Samples {
		data[i] = float32(v) * scale
	}
	return &Audio{Name: path, Data: data, Channels: st.Channels, Rate: st.Rate, Root: -1}, nil
}

func pathExt(p string) string {
	if k := strings.LastIndexByte(p, '.'); k >= 0 {
		return p[k:]
	}
	return ""
}

// smpl is what a WAV's sampler chunk says.
type smpl struct {
	root               int
	tune               float64
	loopStart, loopEnd int64 // End exclusive
}

// readSmpl finds a WAV's 'smpl' chunk. Its loop end is inclusive in the
// file; here it is exclusive.
func readSmpl(path string) (smpl, bool) {
	f, err := os.Open(path)
	if err != nil {
		return smpl{}, false
	}
	defer f.Close()
	var hdr [12]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil || string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return smpl{}, false
	}
	le := binary.LittleEndian
	for {
		var ch [8]byte
		if _, err := io.ReadFull(f, ch[:]); err != nil {
			return smpl{}, false
		}
		size := int64(le.Uint32(ch[4:]))
		if string(ch[0:4]) != "smpl" {
			if _, err := f.Seek(size+size&1, io.SeekCurrent); err != nil {
				return smpl{}, false
			}
			continue
		}
		if size < 36 || size > 1<<20 {
			return smpl{}, false
		}
		b := make([]byte, size)
		if _, err := io.ReadFull(f, b); err != nil {
			return smpl{}, false
		}
		sm := smpl{root: int(le.Uint32(b[12:]))}
		if sm.root > 127 {
			sm.root = -1
		}
		// The recording is this far above its root: play it that much lower.
		sm.tune = -float64(le.Uint32(b[16:])) / 4294967296 * 100
		if n := le.Uint32(b[28:]); n > 0 && size >= 36+24 {
			l := b[36:]
			sm.loopStart, sm.loopEnd = int64(le.Uint32(l[8:])), int64(le.Uint32(l[12:]))+1
		}
		return sm, true
	}
}
