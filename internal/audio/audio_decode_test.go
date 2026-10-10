package audio

// audio_decode_test.go — the RIFF parser, mostly.
//
// WAV is the one format decoded by hand here, so it is the one that can be
// wrong in this package rather than in a dependency. The bit-depth conversions
// are the interesting part: each has its own sign convention (8-bit WAV is
// unsigned and every other depth is signed, 24-bit needs manual sign
// extension), and getting one wrong produces a loud DC offset or inverted
// audio rather than an error. Every depth is therefore round-tripped against
// a known ramp.
//
// There are no .ogg/.mp3 fixtures because nothing on this machine can encode
// one — no ffmpeg, oggenc, lame or sox. Those two paths are thin wrappers over
// libraries with their own test suites; what is tested here is the framing
// logic this package puts around them, which is where a channel swap would
// come from.

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// buildWAV writes a well-formed WAV around the given interleaved samples.
func buildWAV(t *testing.T, rate, channels, bits int, isFloat bool, samples []float32, extraChunks bool) []byte {
	t.Helper()
	var data bytes.Buffer
	for _, s := range samples {
		switch {
		case isFloat:
			binary.Write(&data, binary.LittleEndian, math.Float32bits(s))
		case bits == 8:
			data.WriteByte(byte(s*128 + 128))
		case bits == 16:
			binary.Write(&data, binary.LittleEndian, int16(s*32767))
		case bits == 24:
			v := int32(s * 8388607)
			data.Write([]byte{byte(v), byte(v >> 8), byte(v >> 16)})
		case bits == 32:
			binary.Write(&data, binary.LittleEndian, int32(s*2147483647))
		}
	}

	format := uint16(1)
	if isFloat {
		format = 3
	}
	var fmtChunk bytes.Buffer
	binary.Write(&fmtChunk, binary.LittleEndian, format)
	binary.Write(&fmtChunk, binary.LittleEndian, uint16(channels))
	binary.Write(&fmtChunk, binary.LittleEndian, uint32(rate))
	binary.Write(&fmtChunk, binary.LittleEndian, uint32(rate*channels*bits/8)) // byte rate
	binary.Write(&fmtChunk, binary.LittleEndian, uint16(channels*bits/8))      // block align
	binary.Write(&fmtChunk, binary.LittleEndian, uint16(bits))

	var body bytes.Buffer
	body.WriteString("WAVE")
	body.WriteString("fmt ")
	binary.Write(&body, binary.LittleEndian, uint32(fmtChunk.Len()))
	body.Write(fmtChunk.Bytes())

	if extraChunks {
		// An odd-sized chunk, to exercise the word-alignment pad byte. A real
		// file from a real editor is full of these.
		body.WriteString("LIST")
		binary.Write(&body, binary.LittleEndian, uint32(5))
		body.WriteString("INFO\x00")
		body.WriteByte(0) // pad
	}

	body.WriteString("data")
	binary.Write(&body, binary.LittleEndian, uint32(data.Len()))
	body.Write(data.Bytes())

	var out bytes.Buffer
	out.WriteString("RIFF")
	binary.Write(&out, binary.LittleEndian, uint32(body.Len()))
	out.Write(body.Bytes())
	return out.Bytes()
}

type nopCloser struct{ io.Reader }

func (nopCloser) Close() error { return nil }

func decodeWAVBytes(t *testing.T, b []byte) (*wavDecoder, []float32) {
	t.Helper()
	d, err := newWAVDecoder(nopCloser{bytes.NewReader(b)})
	if err != nil {
		t.Fatalf("newWAVDecoder: %v", err)
	}
	got, err := decodeAll(d, 0)
	if err != nil {
		t.Fatalf("decodeAll: %v", err)
	}
	return d, got
}

func TestEveryWAVBitDepthDecodesToTheSameWaveform(t *testing.T) {
	// A ramp through the whole range, including both extremes and zero, so a
	// sign error or an off-by-one in the divisor shows up rather than hiding
	// in the middle of the range.
	want := []float32{-1, -0.5, -0.25, 0, 0.25, 0.5, 0.9}

	for _, c := range []struct {
		name    string
		bits    int
		isFloat bool
		tol     float32
	}{
		{"8-bit unsigned", 8, false, 1.0 / 100},
		{"16-bit signed", 16, false, 1.0 / 20000},
		{"24-bit signed", 24, false, 1.0 / 1000000},
		{"32-bit signed", 32, false, 1.0 / 1000000},
		{"32-bit float", 32, true, 1e-6},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw := buildWAV(t, 44100, 1, c.bits, c.isFloat, want, false)
			d, got := decodeWAVBytes(t, raw)

			if d.Rate() != 44100 || d.Channels() != 1 {
				t.Fatalf("read back %d Hz / %d ch, wrote 44100/1", d.Rate(), d.Channels())
			}
			if len(got) != len(want) {
				t.Fatalf("got %d samples, wrote %d", len(got), len(want))
			}
			for i := range want {
				if diff := float32(math.Abs(float64(got[i] - want[i]))); diff > c.tol {
					t.Errorf("sample %d: got %.6f want %.6f (off by %.6f)", i, got[i], want[i], diff)
				}
			}
		})
	}
}

func TestStereoWAVKeepsItsChannelsInOrder(t *testing.T) {
	// Left is positive throughout, right negative. A frame-splitting bug
	// swaps them, and every subsequent sample stays swapped.
	in := []float32{0.5, -0.5, 0.25, -0.25, 0.75, -0.75}
	_, got := decodeWAVBytes(t, buildWAV(t, 22050, 2, 16, false, in, false))

	if len(got) != len(in) {
		t.Fatalf("got %d samples, wrote %d", len(got), len(in))
	}
	for i := 0; i < len(got); i += 2 {
		if got[i] <= 0 || got[i+1] >= 0 {
			t.Fatalf("frame %d came back as (%.3f, %.3f); the channels are swapped", i/2, got[i], got[i+1])
		}
	}
}

func TestWAVSkipsChunksItDoesNotUnderstand(t *testing.T) {
	// Real files carry LIST/INFO/id3 chunks between fmt and data, some of
	// them odd-sized. Treating one as samples produces a burst of noise at
	// the head of the clip.
	want := []float32{0.5, -0.5, 0.25}
	_, got := decodeWAVBytes(t, buildWAV(t, 44100, 1, 16, false, want, true))
	if len(got) != len(want) {
		t.Fatalf("got %d samples past the extra chunk, want %d", len(got), len(want))
	}
	if got[0] < 0.4 {
		t.Errorf("first sample is %.3f; the LIST chunk was decoded as audio", got[0])
	}
}

func TestATruncatedWAVPlaysWhatItHasRatherThanFailing(t *testing.T) {
	// A file cut short mid-download is still worth hearing, and the header
	// says more samples are coming than the file holds.
	full := buildWAV(t, 44100, 1, 16, false, []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6}, false)
	cut := full[:len(full)-5] // drops two and a half samples

	d, err := newWAVDecoder(nopCloser{bytes.NewReader(cut)})
	if err != nil {
		t.Fatalf("a truncated file should still open: %v", err)
	}
	got, err := decodeAll(d, 0)
	if err != nil {
		t.Fatalf("decodeAll on a truncated file: %v", err)
	}
	if len(got) == 0 || len(got) > 6 {
		t.Errorf("got %d samples from a file truncated to about 3", len(got))
	}
}

func TestWAVRefusesWhatItCannotDecodeWithAUsefulMessage(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []byte
	}{
		{"not a RIFF file at all", []byte("this is a text file, honestly")},
		{"RIFF but not WAVE", append([]byte("RIFF\x10\x00\x00\x00AVI "), make([]byte, 16)...)},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := newWAVDecoder(nopCloser{bytes.NewReader(c.in)}); err == nil {
				t.Error("no error; a bad file must say so rather than decode as noise")
			}
		})
	}
}

func TestACompressedWAVIsRefusedRatherThanDecodedAsNoise(t *testing.T) {
	// SDL ships an ADPCM sample. Only PCM and IEEE float are read, and the
	// failure mode for guessing wrong is a very loud noise burst.
	path := filepath.Join("..", "..", "bak", "SDL2-2.30.10", "test", "sample.wav")
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("no ADPCM fixture at %s", path)
	}
	defer f.Close()

	_, err = newWAVDecoder(f)
	if err == nil {
		t.Fatal("an ADPCM file decoded as PCM; it would come out as noise")
	}
	t.Logf("refused as expected: %v", err)
}

func TestOpenDecoderRefusesAnExtensionItDoesNotKnow(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "music.flac")
	if err := os.WriteFile(p, []byte("fLaC"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openDecoder(p); err == nil {
		t.Error("a .flac opened; the format list must be honest about what it reads")
	}
}

func TestOpenDecoderRoutesByExtension(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "beep.wav")
	raw := buildWAV(t, 48000, 2, 16, false, []float32{0.1, -0.1, 0.2, -0.2}, false)
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := openDecoder(p)
	if err != nil {
		t.Fatalf("openDecoder: %v", err)
	}
	defer d.Close()
	if d.Rate() != 48000 || d.Channels() != 2 {
		t.Errorf("got %d Hz / %d ch, want 48000/2", d.Rate(), d.Channels())
	}
}

func TestDecodeAllRefusesAClipLongerThanTheLimit(t *testing.T) {
	// The limit is what stops someone loading a ten-minute track as an effect
	// and holding the whole thing in RAM as float32.
	raw := buildWAV(t, 8000, 1, 16, false, make([]float32, 20000), false)
	d, err := newWAVDecoder(nopCloser{bytes.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeAll(d, 1000); err == nil {
		t.Error("a clip past the frame limit loaded anyway")
	}
}
