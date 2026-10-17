package audioenc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"
)

// music is a few seconds of stereo that exercises a coder: a chord with
// a sweep, a burst of noise, silence, a constant and a clipped stretch.
func music(rate int, secs float64) []float32 {
	n := int(float64(rate) * secs)
	out := make([]float32, 2*n)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < n; i++ {
		t := float64(i) / float64(rate)
		var l, r float64
		switch {
		case t < secs*0.5:
			f := 220 + 440*t/secs
			l = 0.3*math.Sin(2*math.Pi*f*t) + 0.2*math.Sin(2*math.Pi*f*1.5*t)
			r = 0.3*math.Sin(2*math.Pi*f*1.25*t+0.3) + 0.1*math.Sin(2*math.Pi*55*t)
		case t < secs*0.6:
			l, r = rng.Float64()*0.6-0.3, rng.Float64()*0.6-0.3
		case t < secs*0.7:
			l, r = 0, 0
		case t < secs*0.8:
			l, r = 0.25, -0.25
		default:
			l = 1.4 * math.Sin(2*math.Pi*330*t)
			r = l
		}
		out[2*i], out[2*i+1] = float32(l), float32(r)
	}
	return out
}

func TestFLACIsLossless(t *testing.T) {
	for _, c := range []struct {
		rate, bits, channels int
		secs                 float64
	}{
		{48000, 24, 2, 1.3}, {44100, 16, 2, 1.1}, {48000, 16, 1, 0.4},
		{44100, 24, 2, 0.01},           // one short block
		{32000, 16, 2, 0},              // no audio at all
		{37800, 16, 2, 0.2},            // a rate the frame header cannot name
		{48000, 24, 2, 4096.0 / 48000}, // exactly one block
	} {
		data := music(c.rate, c.secs)
		if c.channels == 1 {
			mono := make([]float32, len(data)/2)
			for i := range mono {
				mono[i] = data[2*i]
			}
			data = mono
		}
		b, err := EncodeFLAC(data, c.channels, c.rate, c.bits, Tags{Title: "Hangar Theme", Artist: "Ünïcode"})
		if err != nil {
			t.Fatal(err)
		}
		st, err := DecodeFLAC(b)
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		if st.Rate != c.rate || st.Channels != c.channels || st.Bits != c.bits {
			t.Errorf("%+v: decoded %d Hz, %d channels, %d bits", c, st.Rate, st.Channels, st.Bits)
		}
		want := quantize(data, c.bits)
		if len(st.Samples) != len(want) {
			t.Fatalf("%+v: %d samples back, %d in", c, len(st.Samples), len(want))
		}
		for i := range want {
			if st.Samples[i] != want[i] {
				t.Fatalf("%+v: sample %d is %d, went in as %d", c, i, st.Samples[i], want[i])
			}
		}
		if !strings.Contains(strings.Join(st.Tags, "|"), "TITLE=Hangar Theme") || !strings.Contains(strings.Join(st.Tags, "|"), "ARTIST=Ünïcode") || st.Vendor != Vendor {
			t.Errorf("tags %q vendor %q", st.Tags, st.Vendor)
		}
	}
}

// Prediction earns its keep: a FLAC of music is well under the WAV's size,
// and silence costs next to nothing.
func TestFLACCompresses(t *testing.T) {
	data := music(48000, 2)[:48000*2] // the tonal first second
	b, err := EncodeFLAC(data, 2, 48000, 16, Tags{})
	if err != nil {
		t.Fatal(err)
	}
	wav := len(data) * 2
	if len(b)*2 > wav {
		t.Errorf("FLAC of a chord is %d bytes, the WAV %d", len(b), wav)
	}
	silence := make([]float32, 2*48000*5)
	s, _ := EncodeFLAC(silence, 2, 48000, 24, Tags{})
	if len(s) > 4096 {
		t.Errorf("five seconds of silence take %d bytes", len(s))
	}
}

// A damaged file is refused, not decoded wrong.
func TestFLACDecoderChecks(t *testing.T) {
	b, _ := EncodeFLAC(music(44100, 0.3), 2, 44100, 16, Tags{})
	for name, f := range map[string]func([]byte){
		"a flipped bit in the audio": func(x []byte) { x[len(x)/2] ^= 0x10 },
		"a cut-off end":              func(x []byte) {},
		"no magic":                   func(x []byte) { x[0] = 'X' },
	} {
		x := append([]byte(nil), b...)
		f(x)
		if name == "a cut-off end" {
			x = x[:len(x)-100]
		}
		if _, err := DecodeFLAC(x); err == nil {
			t.Errorf("%s decoded", name)
		}
	}
}

func TestWAV(t *testing.T) {
	data := music(44100, 0.2)
	for _, bits := range []int{16, 24} {
		b, err := Encode(data, 2, 44100, Options{Format: WAV, Bits: bits})
		if err != nil {
			t.Fatal(err)
		}
		if string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
			t.Fatal("not a WAV")
		}
		if ch, rate, bps := binary.LittleEndian.Uint16(b[22:]), binary.LittleEndian.Uint32(b[24:]), binary.LittleEndian.Uint16(b[34:]); ch != 2 || rate != 44100 || int(bps) != bits {
			t.Errorf("%d bits: header says %d channels, %d Hz, %d bits", bits, ch, rate, bps)
		}
		if got, want := len(b)-44, len(data)*bits/8; got != want {
			t.Errorf("%d bits: %d bytes of audio, want %d", bits, got, want)
		}
	}
}

// correlation is how alike two signals are (1 the same shape), b shifted
// by lag samples.
func correlation(a, b []float64, lag int) float64 {
	var ab, aa, bb float64
	for i := range a {
		j := i + lag
		if j < 0 || j >= len(b) {
			continue
		}
		ab += a[i] * b[j]
		aa += a[i] * a[i]
		bb += b[j] * b[j]
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return ab / math.Sqrt(aa*bb)
}

// channel is one channel of interleaved audio as float64.
func channel(data []float32, ch, chans int) []float64 {
	out := make([]float64, len(data)/chans)
	for i := range out {
		out[i] = float64(data[i*chans+ch])
	}
	return out
}

// tonal is two seconds of a chord: what lossy coders are judged on.
func tonal(rate int) []float32 {
	n := 2 * rate
	out := make([]float32, 2*n)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(rate)
		env := math.Min(1, t*8) * math.Min(1, (2-t)*8)
		l := env * (0.3*math.Sin(2*math.Pi*261.63*t) + 0.2*math.Sin(2*math.Pi*329.63*t))
		r := env * (0.3*math.Sin(2*math.Pi*392*t) + 0.15*math.Sin(2*math.Pi*523.25*t))
		out[2*i], out[2*i+1] = float32(l), float32(r)
	}
	return out
}

func needCodec(t *testing.T, f Format) {
	t.Helper()
	if err := Available(f); err != nil {
		if os.Getenv("FOUNDRY_REQUIRE_CODECS") != "" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
}

// An OGG this writes is a Vorbis stream another decoder plays back as
// what went in: the same length to the sample, the same sound.
func TestOGGDecodesBack(t *testing.T) {
	needCodec(t, OGG)
	for _, rate := range []int{48000, 44100} {
		data := tonal(rate)
		b, err := Encode(data, 2, rate, Options{Format: OGG, Quality: 6, Tags: Tags{Title: "Hangar Theme", Artist: "Somebody"}})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(b, []byte("OggS")) {
			t.Fatal("not an Ogg stream")
		}
		r, err := oggvorbis.NewReader(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if r.SampleRate() != rate || r.Channels() != 2 {
			t.Errorf("decoded %d Hz %d channels", r.SampleRate(), r.Channels())
		}
		var got []float32
		buf := make([]float32, 8192)
		for {
			n, err := r.Read(buf)
			got = append(got, buf[:n]...)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if len(got) != len(data) {
			t.Errorf("%d Hz: %d samples back, %d in", rate, len(got), len(data))
		}
		for ch := 0; ch < 2; ch++ {
			if c := correlation(channel(data, ch, 2), channel(got, ch, 2), 0); c < 0.99 {
				t.Errorf("%d Hz channel %d: correlation %.4f with what went in", rate, ch, c)
			}
		}
		comments := strings.Join(r.CommentHeader().Comments, "|")
		if !strings.Contains(comments, "TITLE=Hangar Theme") || !strings.Contains(comments, "ENCODER="+Vendor) {
			t.Errorf("comments %q", comments)
		}
		// The quality setting means something: a lower one is smaller.
		small, _ := Encode(data, 2, rate, Options{Format: OGG, Quality: 0})
		if len(small) >= len(b) {
			t.Errorf("quality 0 is %d bytes, quality 6 %d", len(small), len(b))
		}
	}
	// The same audio makes the same file.
	a, _ := Encode(tonal(48000), 2, 48000, DefaultOptions(OGG))
	b, _ := Encode(tonal(48000), 2, 48000, DefaultOptions(OGG))
	if !bytes.Equal(a, b) {
		t.Error("two encodes of the same audio differ")
	}
	if _, err := Encode(tonal(48000), 2, 48000, Options{Format: OGG, Quality: 11}); err == nil {
		t.Error("quality 11 accepted")
	}
}

// An MP3 this writes decodes with another decoder to what went in, behind
// the coder's usual delay; VBR has its header, CBR its rate.
func TestMP3DecodesBack(t *testing.T) {
	needCodec(t, MP3)
	for _, o := range []Options{
		{Format: MP3, Quality: 2, Tags: Tags{Title: "Hangar Theme", Artist: "Somebody"}},
		{Format: MP3, Bitrate: 192},
	} {
		for _, rate := range []int{48000, 44100} {
			data := tonal(rate)
			b, err := Encode(data, 2, rate, o)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(b, []byte("ID3")) {
				t.Fatal("no ID3 tag")
			}
			if o.Tags.Title != "" && !bytes.Contains(b, []byte{'H', 0, 'a', 0, 'n', 0}) {
				t.Error("the title is not in the tag")
			}
			if o.Bitrate == 0 && !bytes.Contains(b[:4096], []byte("Xing")) && !bytes.Contains(b[:4096], []byte("Info")) {
				t.Error("a VBR file without its Xing header")
			}
			d, err := mp3.NewDecoder(bytes.NewReader(b))
			if err != nil {
				t.Fatal(err)
			}
			if d.SampleRate() != rate {
				t.Errorf("decoded at %d Hz", d.SampleRate())
			}
			raw, err := io.ReadAll(d)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]float32, len(raw)/2)
			for i := range got {
				got[i] = float32(int16(binary.LittleEndian.Uint16(raw[2*i:]))) / 32768
			}
			frames := len(got) / 2
			if frames < len(data)/2 || frames > len(data)/2+3*1152 {
				t.Errorf("%+v %d Hz: %d frames back, %d in", o, rate, frames, len(data)/2)
			}
			// Find the coder's delay, then compare.
			l, gl := channel(data, 0, 2), channel(got, 0, 2)
			best, lag := 0.0, 0
			for k := 0; k < 3000; k++ {
				if c := correlation(l[:rate/2], gl, k); c > best {
					best, lag = c, k
				}
			}
			for ch := 0; ch < 2; ch++ {
				if c := correlation(channel(data, ch, 2), channel(got, ch, 2), lag); c < 0.98 {
					t.Errorf("%+v %d Hz channel %d: correlation %.4f (delay %d)", o, rate, ch, c, lag)
				}
			}
		}
	}
	if _, err := Encode(tonal(48000), 2, 47000, Options{Format: MP3}); err == nil {
		t.Error("47 kHz accepted for MP3")
	}
	if _, err := Encode(make([]float32, 600), 3, 48000, Options{Format: MP3}); err == nil {
		t.Error("three channels accepted for MP3")
	}
}

// withCodecDir makes the codec search see only a directory (and not the
// system), for as long as the test runs.
func withCodecDir(t *testing.T, dir string) {
	t.Helper()
	prev, had := os.LookupEnv("FOUNDRY_CODECS")
	os.Setenv("FOUNDRY_CODECS", dir)
	searchSystem = false
	resetLibraries()
	t.Cleanup(func() {
		if had {
			os.Setenv("FOUNDRY_CODECS", prev)
		} else {
			os.Unsetenv("FOUNDRY_CODECS")
		}
		searchSystem = true
		resetLibraries()
	})
}

// An editor without the libraries says so, and says how to get them; WAV
// and FLAC do not need them.
func TestMissingLibrariesAreSaid(t *testing.T) {
	withCodecDir(t, t.TempDir())
	for _, f := range []Format{OGG, MP3} {
		err := Available(f)
		var miss *MissingLibraryError
		if !errors.As(err, &miss) {
			t.Fatalf("%s with no library: %v", f, err)
		}
		msg := err.Error()
		want := map[Format]string{OGG: "libvorbis", MP3: "libmp3lame"}[f]
		if !strings.Contains(msg, want) || !strings.Contains(msg, "codecs") && !strings.Contains(msg, os.Getenv("FOUNDRY_CODECS")) {
			t.Errorf("%s: %q", f, msg)
		}
		if _, err := Encode(tonal(48000)[:4800], 2, 48000, DefaultOptions(f)); !errors.As(err, &miss) {
			t.Errorf("%s encode: %v", f, err)
		}
	}
	for _, f := range []Format{WAV, FLAC} {
		if err := Available(f); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

// Libraries put in the codecs folder are the ones used, before the
// system's: how the editor's distribution carries them.
func TestLibrariesLoadFromTheCodecsFolder(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("copies the system's libraries, found where Linux keeps them")
	}
	var found map[string]string
	resetLibraries()
	found = Libraries()
	resetLibraries()
	dir := t.TempDir()
	copied := 0
	for _, lib := range []string{"libogg.so.0", "libvorbis.so.0", "libvorbisenc.so.2", "libmp3lame.so.0"} {
		for _, sys := range []string{"/lib/x86_64-linux-gnu", "/usr/lib/x86_64-linux-gnu", "/usr/lib64", "/usr/lib"} {
			b, err := os.ReadFile(filepath.Join(sys, lib))
			if err == nil {
				os.WriteFile(filepath.Join(dir, lib), b, 0o755)
				copied++
				break
			}
		}
	}
	if copied < 4 {
		if os.Getenv("FOUNDRY_REQUIRE_CODECS") != "" {
			t.Fatalf("found %d of the four libraries to copy (system: %v)", copied, found)
		}
		t.Skipf("found %d of the four libraries to copy", copied)
	}
	withCodecDir(t, dir)
	libs := Libraries()
	for name, path := range libs {
		if !strings.HasPrefix(path, dir) {
			t.Errorf("%s came from %q, not the codecs folder", name, path)
		}
	}
	if _, err := Encode(tonal(44100)[:44100], 2, 44100, DefaultOptions(MP3)); err != nil {
		t.Error(err)
	}
	if _, err := Encode(tonal(44100)[:44100], 2, 44100, DefaultOptions(OGG)); err != nil {
		t.Error(err)
	}
}

func TestParseFormatAndDescribe(t *testing.T) {
	for in, want := range map[string]Format{"wav": WAV, ".FLAC": FLAC, "vorbis": OGG, "Mp3": MP3, "ogg": OGG} {
		if f, err := ParseFormat(in); err != nil || f != want {
			t.Errorf("%q: %v %v", in, f, err)
		}
	}
	if _, err := ParseFormat("aiff"); err == nil {
		t.Error("aiff parsed")
	}
	for o, want := range map[Options]string{
		DefaultOptions(OGG):         "48 kHz · quality 6",
		DefaultOptions(MP3):         "48 kHz · VBR V2",
		{Format: MP3, Bitrate: 320}: "48 kHz · 320 kb/s",
		{Format: FLAC, Bits: 16}:    "48 kHz · 16-bit",
		DefaultOptions(WAV):         "48 kHz · 24-bit",
	} {
		if got := o.Describe(48000); got != want {
			t.Errorf("%+v: %q, want %q", o, got, want)
		}
	}
}

func BenchmarkFLAC(b *testing.B) {
	data := music(48000, 10)
	b.SetBytes(int64(len(data) * 2))
	for i := 0; i < b.N; i++ {
		if _, err := EncodeFLAC(data, 2, 48000, 24, Tags{}); err != nil {
			b.Fatal(err)
		}
	}
}

// AUDIOENC_DUMP=dir writes one file of each format there, to open with
// another program.
func TestDumpForOtherPrograms(t *testing.T) {
	dir := os.Getenv("AUDIOENC_DUMP")
	if dir == "" {
		t.Skip("set AUDIOENC_DUMP to a directory")
	}
	data := tonal(48000)
	for _, o := range []Options{{Format: WAV, Bits: 16}, {Format: FLAC, Bits: 24}, {Format: FLAC, Bits: 16}, DefaultOptions(OGG), DefaultOptions(MP3)} {
		o.Tags = Tags{Title: "Hangar Theme", Artist: "Somebody"}
		name := filepath.Join(dir, "tonal"+string(rune('0'+o.Bits/8))+o.Format.Ext())
		if err := WriteFile(name, data, 2, 48000, o); err != nil {
			t.Error(err)
		}
	}
	for _, rate := range []int{44100, 37800} {
		for _, bits := range []int{16, 24} {
			name := filepath.Join(dir, "music"+string(rune('0'+bits/8))+"_"+itoa(rate)+".flac")
			if err := WriteFile(name, music(rate, 3.7), 2, rate, Options{Format: FLAC, Bits: bits}); err != nil {
				t.Error(err)
			}
		}
	}
}

func itoa(v int) string { return strconv.Itoa(v) }
