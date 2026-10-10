package video

// video_avi_test.go — the demuxer, against files built here.
//
// Building the AVI in the test rather than checking a binary fixture in is
// deliberate: the thing under test is an agreement about byte offsets, and a
// fixture would let that agreement drift without anybody being able to read the
// diff. A writer next to the reader means a change to one that the other does
// not expect fails loudly.

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// ---------- a very small AVI writer ----------

type aviBuilder struct {
	width, height int
	fps           int
	frames        [][]byte

	audioRate     int
	audioChannels int
	audioBits     int
	audio         []byte

	// codec overrides the four-character compression code, so a test can build
	// a file this package must refuse.
	codec string
}

func chunk(id string, body []byte) []byte {
	var b bytes.Buffer
	b.WriteString(id)
	binary.Write(&b, binary.LittleEndian, uint32(len(body)))
	b.Write(body)
	if len(body)%2 == 1 {
		b.WriteByte(0) // chunks are word-aligned
	}
	return b.Bytes()
}

func list(form string, body []byte) []byte {
	return chunk("LIST", append([]byte(form), body...))
}

func u32(v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return b[:]
}

func u16(v uint16) []byte {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	return b[:]
}

func (a *aviBuilder) bytes() []byte {
	codec := a.codec
	if codec == "" {
		codec = "MJPG"
	}

	avih := make([]byte, 56)
	binary.LittleEndian.PutUint32(avih[0:], uint32(1000000/a.fps))
	binary.LittleEndian.PutUint32(avih[16:], uint32(len(a.frames)))
	binary.LittleEndian.PutUint32(avih[32:], uint32(a.width))
	binary.LittleEndian.PutUint32(avih[36:], uint32(a.height))

	vstrh := make([]byte, 56)
	copy(vstrh[0:], "vids")
	copy(vstrh[4:], codec)
	binary.LittleEndian.PutUint32(vstrh[20:], 1)                     // scale
	binary.LittleEndian.PutUint32(vstrh[24:], uint32(a.fps))         // rate
	binary.LittleEndian.PutUint32(vstrh[32:], uint32(len(a.frames))) // length

	vstrf := make([]byte, 40)
	binary.LittleEndian.PutUint32(vstrf[0:], 40)
	binary.LittleEndian.PutUint32(vstrf[4:], uint32(a.width))
	binary.LittleEndian.PutUint32(vstrf[8:], uint32(a.height))
	binary.LittleEndian.PutUint16(vstrf[14:], 24)
	copy(vstrf[16:], codec)

	hdrl := append(chunk("avih", avih), list("strl", append(chunk("strh", vstrh), chunk("strf", vstrf)...))...)

	if len(a.audio) > 0 {
		astrh := make([]byte, 56)
		copy(astrh[0:], "auds")
		binary.LittleEndian.PutUint32(astrh[20:], 1)
		binary.LittleEndian.PutUint32(astrh[24:], uint32(a.audioRate))

		var astrf bytes.Buffer
		astrf.Write(u16(1)) // PCM
		astrf.Write(u16(uint16(a.audioChannels)))
		astrf.Write(u32(uint32(a.audioRate)))
		astrf.Write(u32(uint32(a.audioRate * a.audioChannels * a.audioBits / 8)))
		astrf.Write(u16(uint16(a.audioChannels * a.audioBits / 8)))
		astrf.Write(u16(uint16(a.audioBits)))
		astrf.Write(u16(0)) // cbSize
		hdrl = append(hdrl, list("strl", append(chunk("strh", astrh), chunk("strf", astrf.Bytes())...))...)
	}

	var movi []byte
	for i, f := range a.frames {
		movi = append(movi, chunk("00dc", f)...)
		if len(a.audio) > 0 {
			// One slice of audio per frame, the way a muxer interleaves it.
			per := len(a.audio) / len(a.frames)
			lo, hi := i*per, (i+1)*per
			if i == len(a.frames)-1 {
				hi = len(a.audio)
			}
			movi = append(movi, chunk("01wb", a.audio[lo:hi])...)
		}
	}

	body := append([]byte("AVI "), list("hdrl", hdrl)...)
	body = append(body, list("movi", movi)...)
	return chunk("RIFF", body)
}

// jpegFrame is a solid-colour JPEG, so a decoded pixel identifies which frame
// it came from.
func jpegFrame(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b.Bytes()
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// ---------- the tests ----------

func TestAVIIndexesEveryFrameAndTimesThem(t *testing.T) {
	colors := []color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 0, 255}}
	b := &aviBuilder{width: 16, height: 8, fps: 4}
	for _, c := range colors {
		b.frames = append(b.frames, jpegFrame(t, 16, 8, c))
	}

	d, err := openAVI("clip", writeTemp(t, "clip.avi", b.bytes()))
	if err != nil {
		t.Fatalf("openAVI: %v", err)
	}
	defer d.Close()

	trk := d.Track()
	if trk.Width != 16 || trk.Height != 8 {
		t.Errorf("size = %dx%d, want 16x8", trk.Width, trk.Height)
	}
	if trk.Count() != 4 {
		t.Fatalf("frame count = %d, want 4", trk.Count())
	}
	if trk.Duration != 1 {
		t.Errorf("duration = %v, want 1s for four frames at 4 fps", trk.Duration)
	}
	for i, want := range []float64{0, 0.25, 0.5, 0.75} {
		if trk.Starts[i] != want {
			t.Errorf("frame %d starts at %v, want %v", i, trk.Starts[i], want)
		}
	}
}

func TestAVIDecodesTheFrameItWasAskedFor(t *testing.T) {
	colors := []color.RGBA{{200, 30, 30, 255}, {30, 200, 30, 255}, {30, 30, 200, 255}}
	b := &aviBuilder{width: 16, height: 16, fps: 10}
	for _, c := range colors {
		b.frames = append(b.frames, jpegFrame(t, 16, 16, c))
	}
	d, err := openAVI("clip", writeTemp(t, "clip.avi", b.bytes()))
	if err != nil {
		t.Fatalf("openAVI: %v", err)
	}
	defer d.Close()

	// Out of order, because random access is the whole reason frames are
	// addressed by index rather than pulled from a stream.
	for _, i := range []int{2, 0, 1, 2} {
		img, err := d.Frame(i)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		got := img.RGBAAt(8, 8)
		want := colors[i]
		if !closeEnough(got, want) {
			t.Errorf("frame %d is %v, want about %v", i, got, want)
		}
	}
}

// closeEnough allows for JPEG being lossy. A frame that came back as the wrong
// one is off by a hundred and seventy, not by six.
func closeEnough(got, want color.RGBA) bool {
	d := func(a, b uint8) int {
		if a > b {
			return int(a - b)
		}
		return int(b - a)
	}
	return d(got.R, want.R) < 24 && d(got.G, want.G) < 24 && d(got.B, want.B) < 24
}

func TestAVIRefusesACodecItCannotRead(t *testing.T) {
	b := &aviBuilder{width: 8, height: 8, fps: 10, codec: "H264"}
	b.frames = append(b.frames, jpegFrame(t, 8, 8, color.RGBA{1, 2, 3, 255}))

	_, err := openAVI("clip", writeTemp(t, "clip.avi", b.bytes()))
	if err == nil {
		t.Fatal("an H.264 stream opened; it should be refused")
	}
	// The message has to carry the way out, not just the complaint: someone
	// hitting this needs the ffmpeg line, not a codec name they already knew.
	if !bytes.Contains([]byte(err.Error()), []byte("ffmpeg")) {
		t.Errorf("error does not say how to fix it: %v", err)
	}
}

func TestAVIDemuxesItsSoundtrack(t *testing.T) {
	b := &aviBuilder{
		width: 8, height: 8, fps: 2,
		audioRate: 8000, audioChannels: 1, audioBits: 16,
	}
	for i := 0; i < 4; i++ {
		b.frames = append(b.frames, jpegFrame(t, 8, 8, color.RGBA{uint8(i * 60), 0, 0, 255}))
	}
	// A second of 8 kHz mono: 8000 frames of 16-bit silence, with one sample
	// set so a dropped or reordered chunk is visible.
	pcm := make([]byte, 8000*2)
	binary.LittleEndian.PutUint16(pcm[100:], uint16(int16(16384)))
	b.audio = pcm

	d, err := openAVI("clip", writeTemp(t, "clip.avi", b.bytes()))
	if err != nil {
		t.Fatalf("openAVI: %v", err)
	}
	defer d.Close()

	snd := d.Track().Sound
	if snd == nil {
		t.Fatal("no soundtrack was demuxed")
	}
	if snd.Rate != 8000 || snd.Channels != 1 {
		t.Errorf("soundtrack is %d Hz %d channel, want 8000 Hz mono", snd.Rate, snd.Channels)
	}
	if snd.Frames() != 8000 {
		t.Errorf("soundtrack is %d frames, want 8000 — the wb chunks did not all arrive", snd.Frames())
	}
	if got := snd.Samples[50]; got < 0.4 || got > 0.6 {
		t.Errorf("sample 50 is %v, want about 0.5 — the audio came back scrambled", got)
	}
}

func TestAVIWithNoAudioIsSilentRatherThanBroken(t *testing.T) {
	b := &aviBuilder{width: 8, height: 8, fps: 5}
	b.frames = append(b.frames, jpegFrame(t, 8, 8, color.RGBA{9, 9, 9, 255}))

	d, err := openAVI("clip", writeTemp(t, "clip.avi", b.bytes()))
	if err != nil {
		t.Fatalf("openAVI: %v", err)
	}
	defer d.Close()
	if d.Track().Sound != nil {
		t.Error("a video with no audio stream produced a soundtrack")
	}
}

func TestParseStreamChunkIDIgnoresWhatIsNotAStream(t *testing.T) {
	if _, _, ok := parseStreamChunkID("idx1"); ok {
		t.Error("idx1 read as a stream chunk")
	}
	if _, _, ok := parseStreamChunkID("JUNK"); ok {
		t.Error("JUNK read as a stream chunk")
	}
	idx, kind, ok := parseStreamChunkID("01wb")
	if !ok || idx != 1 || kind != "wb" {
		t.Errorf("01wb parsed as (%d, %q, %v)", idx, kind, ok)
	}
}

func TestACompressedSoundtrackLeavesThePictureAlone(t *testing.T) {
	var logged []string
	old := Logf
	Logf = func(format string, args ...any) { logged = append(logged, format) }
	t.Cleanup(func() { Logf = old })

	b := &aviBuilder{
		width: 8, height: 8, fps: 5,
		audioRate: 44100, audioChannels: 2, audioBits: 16,
		audio: make([]byte, 4096),
	}
	b.frames = append(b.frames, jpegFrame(t, 8, 8, color.RGBA{5, 5, 5, 255}))

	// MP3 inside the AVI: nothing here decodes it, and the picture must still
	// open. Patched into the WAVEFORMATEX tag after the fact, which is the one
	// field the builder does not parameterise.
	raw := b.bytes()
	i := bytes.Index(raw, []byte("auds"))
	if i < 0 {
		t.Fatal("no audio stream in the built file")
	}
	j := bytes.Index(raw[i:], []byte("strf"))
	binary.LittleEndian.PutUint16(raw[i+j+8:], 0x0055) // WAVE_FORMAT_MPEGLAYER3

	d, err := openAVI("clip", writeTemp(t, "clip.avi", raw))
	if err != nil {
		t.Fatalf("a compressed soundtrack stopped the picture opening: %v", err)
	}
	defer d.Close()
	if d.Track().Sound != nil {
		t.Error("an MP3 soundtrack was decoded as PCM")
	}
	if len(logged) == 0 {
		t.Error("nothing was logged; a silent cutscene with no explanation is unfixable")
	}
}
