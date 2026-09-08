package video

// video_avi.go — the RIFF/AVI demuxer, and motion-JPEG out of it.
//
// AVI is a container of chunks, and this reads it the honest way: walk the
// chunk tree, note where every frame's bytes are, and decode one when it is
// asked for. It deliberately ignores the `idx1` index at the end of the file
// even though that is what it is for. Two different generations of muxer wrote
// idx1 offsets relative to two different bases — the file, and the `movi` list
// — and a file written against the other convention indexes to garbage. Walking
// `movi` costs one pass at open and cannot be wrong.
//
// The video stream must be motion-JPEG. Anything else is refused by name at
// open, with the ffmpeg line that would fix it, rather than decoded into
// nonsense: a codec mismatch that produces a green smear is a much worse
// morning than one that produces a sentence.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"os"
	"strings"

	"foundry/internal/audio"
)

// MaxSoundtrackSeconds caps how much of a video's audio is held.
//
// A soundtrack is decoded whole, because an audio.Clip is a whole decoded sound
// and the mixer reads it with no locking on that basis. Three minutes of 44.1
// kHz stereo is about sixty megabytes resident, which is already generous for a
// cutscene; past it the audio is dropped and the video plays silently rather
// than the load failing, and the log says so. A longer film wants to be two
// files.
const MaxSoundtrackSeconds = 180

// maxChunkBytes rejects a length field that cannot be real before it is used to
// size a slice. A corrupt or truncated AVI otherwise asks for gigabytes on the
// strength of four bytes.
const maxChunkBytes = 256 << 20

// frameRef is where one compressed frame lives in the file.
type frameRef struct {
	off  int64
	size int64
}

type aviDecoder struct {
	name string
	f    *os.File
	trk  Track

	frames []frameRef

	// buf is the compressed bytes of the frame being decoded, and img the
	// decoded picture. Both are reused across calls: playback decodes one
	// frame per display frame forever, and allocating a megabyte each time is
	// a garbage collector's whole afternoon.
	buf []byte
	img *image.RGBA
}

func openAVI(name, path string) (decoder, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	d := &aviDecoder{name: name, f: f}
	if err := d.parse(st.Size()); err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return d, nil
}

func (d *aviDecoder) Track() *Track { return &d.trk }
func (d *aviDecoder) Close() error  { return d.f.Close() }

// ---------- the chunk walk ----------

// chunkHeader reads the eight-byte id and length at off.
func chunkHeader(r io.ReaderAt, off int64) (id string, size int64, err error) {
	var h [8]byte
	if _, err := r.ReadAt(h[:], off); err != nil {
		return "", 0, err
	}
	size = int64(binary.LittleEndian.Uint32(h[4:8]))
	if size < 0 || size > maxChunkBytes {
		return "", 0, fmt.Errorf("chunk %q claims %d bytes", string(h[0:4]), size)
	}
	return string(h[0:4]), size, nil
}

// walkChunks calls fn for every chunk between off and end. fn is given the
// chunk's id, where its body starts and how long it is; returning an error
// stops the walk.
//
// Chunk bodies are word-aligned, so an odd length carries a pad byte that is
// not part of the body and must still be stepped over. Getting that wrong
// desynchronises the whole rest of the file, which is why it is in one place.
func walkChunks(r io.ReaderAt, off, end int64, fn func(id string, body, size int64) error) error {
	for off+8 <= end {
		id, size, err := chunkHeader(r, off)
		if err != nil {
			// A truncated tail is survivable: everything already indexed still
			// plays. A truncated *header* is where the walk simply stops.
			return nil
		}
		body := off + 8
		if body+size > end {
			size = end - body
		}
		if err := fn(id, body, size); err != nil {
			return err
		}
		off = body + size + size%2
	}
	return nil
}

// listForm is the four-character type of a LIST chunk — "hdrl", "movi", "strl".
func listForm(r io.ReaderAt, body, size int64) string {
	if size < 4 {
		return ""
	}
	var t [4]byte
	if _, err := r.ReadAt(t[:], body); err != nil {
		return ""
	}
	return string(t[:])
}

func readAll(r io.ReaderAt, off, size int64) ([]byte, error) {
	if size <= 0 || size > maxChunkBytes {
		return nil, fmt.Errorf("implausible chunk length %d", size)
	}
	b := make([]byte, size)
	if _, err := r.ReadAt(b, off); err != nil && err != io.EOF {
		return nil, err
	}
	return b, nil
}

// ---------- the header block ----------

// stream is one `strl` list's headers, kept while the file is walked because
// the movi chunks name streams by index and nothing else says what a stream is.
type stream struct {
	kind    string // "vids" or "auds"
	handler string
	scale   uint32
	rate    uint32
	length  uint32

	// video
	width, height int
	compression   string

	// audio
	channels, sampleRate, bits int
	isFloat                    bool
	// audioCodec is the WAVEFORMATEX tag, kept so that a soundtrack this
	// cannot read is reported as the thing it actually is rather than as
	// "zero channels".
	audioCodec uint16
}

func (d *aviDecoder) parse(fileSize int64) error {
	var riff [12]byte
	if _, err := d.f.ReadAt(riff[:], 0); err != nil {
		return fmt.Errorf("short header: %w", err)
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "AVI " {
		return fmt.Errorf("not a RIFF/AVI file")
	}
	end := int64(binary.LittleEndian.Uint32(riff[4:8])) + 8
	if end > fileSize || end <= 12 {
		// A muxer that never went back to patch the RIFF length — common on a
		// stream that was piped — leaves it at zero or at a placeholder. The
		// file on disk is the truth.
		end = fileSize
	}

	var streams []stream
	var movi [2]int64 // body, size
	err := walkChunks(d.f, 12, end, func(id string, body, size int64) error {
		if id != "LIST" {
			return nil
		}
		switch listForm(d.f, body, size) {
		case "hdrl":
			var err error
			streams, err = d.parseHeaderList(body+4, body+size)
			return err
		case "movi":
			movi = [2]int64{body + 4, size - 4}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if movi[1] <= 0 {
		return fmt.Errorf("no movi list: there is no picture data in this file")
	}

	vid, vidIdx := pickStream(streams, "vids")
	if vid == nil {
		return fmt.Errorf("no video stream")
	}
	if !isMotionJPEG(vid) {
		return fmt.Errorf("video stream is %q, and only motion-JPEG is read. "+
			"Re-encode with: ffmpeg -i in.mp4 -c:v mjpeg -q:v 3 -c:a pcm_s16le out.avi",
			describeCodec(vid))
	}
	aud, audIdx := pickStream(streams, "auds")

	var audioBytes []byte
	audioCap := 0
	if aud != nil && readableAudio(aud) {
		audioCap = MaxSoundtrackSeconds * aud.sampleRate * aud.channels * aud.bits / 8
	} else if aud != nil {
		// Not worth reading a byte of: the picture still plays, and the log
		// says which of the several possible reasons it was.
		logf("%s: %s — the video plays without sound", d.name, describeAudio(aud))
		aud = nil
	}

	// One pass over movi: video chunks become index entries, audio chunks are
	// concatenated. `rec ` lists group one frame's streams together in files
	// written for playback off a spinning disc, so they have to be descended
	// into rather than skipped.
	var walk func(from, to int64) error
	walk = func(from, to int64) error {
		return walkChunks(d.f, from, to, func(id string, body, size int64) error {
			if id == "LIST" {
				if listForm(d.f, body, size) == "rec " {
					return walk(body+4, body+size)
				}
				return nil
			}
			idx, kind, ok := parseStreamChunkID(id)
			if !ok || size <= 0 {
				return nil
			}
			switch {
			case idx == vidIdx && (kind == "dc" || kind == "db"):
				d.frames = append(d.frames, frameRef{off: body, size: size})
			case aud != nil && idx == audIdx && kind == "wb":
				if audioCap > 0 && len(audioBytes) >= audioCap {
					return nil
				}
				b, err := readAll(d.f, body, size)
				if err != nil {
					return nil
				}
				audioBytes = append(audioBytes, b...)
			}
			return nil
		})
	}
	if err := walk(movi[0], movi[0]+movi[1]); err != nil {
		return err
	}
	if len(d.frames) == 0 {
		return fmt.Errorf("movi list holds no frames for stream %d", vidIdx)
	}

	d.trk.Width, d.trk.Height = vid.width, vid.height
	if d.trk.Width <= 0 || d.trk.Height <= 0 {
		// The stream header lied or was absent; the first frame's JPEG knows.
		if err := d.sizeFromFirstFrame(); err != nil {
			return err
		}
	}
	d.fillStarts(fps(vid))
	d.trk.Sound = d.decodeSoundtrack(aud, audioBytes, audioCap)
	d.img = image.NewRGBA(image.Rect(0, 0, d.trk.Width, d.trk.Height))
	return nil
}

// parseHeaderList reads the `hdrl` block: the main header, then one `strl` per
// stream, in the order the movi chunk ids number them.
func (d *aviDecoder) parseHeaderList(from, to int64) ([]stream, error) {
	var out []stream
	err := walkChunks(d.f, from, to, func(id string, body, size int64) error {
		if id != "LIST" || listForm(d.f, body, size) != "strl" {
			return nil
		}
		var s stream
		err := walkChunks(d.f, body+4, body+size, func(id string, body, size int64) error {
			b, err := readAll(d.f, body, size)
			if err != nil {
				return nil
			}
			switch id {
			case "strh":
				parseStreamHeader(&s, b)
			case "strf":
				parseStreamFormat(&s, b)
			}
			return nil
		})
		if err != nil {
			return err
		}
		out = append(out, s)
		return nil
	})
	return out, err
}

func parseStreamHeader(s *stream, b []byte) {
	if len(b) < 48 {
		return
	}
	s.kind = string(b[0:4])
	s.handler = string(b[4:8])
	s.scale = binary.LittleEndian.Uint32(b[20:24])
	s.rate = binary.LittleEndian.Uint32(b[24:28])
	s.length = binary.LittleEndian.Uint32(b[32:36])
}

func parseStreamFormat(s *stream, b []byte) {
	switch s.kind {
	case "vids":
		// BITMAPINFOHEADER. Height is signed and negative for a top-down
		// bitmap; for motion-JPEG it says nothing about the picture, which is
		// oriented by the JPEG itself, so the sign is simply dropped.
		if len(b) < 20 {
			return
		}
		s.width = int(int32(binary.LittleEndian.Uint32(b[4:8])))
		h := int(int32(binary.LittleEndian.Uint32(b[8:12])))
		if h < 0 {
			h = -h
		}
		s.height = h
		s.compression = string(b[16:20])
	case "auds":
		// WAVEFORMATEX.
		if len(b) < 16 {
			return
		}
		format := binary.LittleEndian.Uint16(b[0:2])
		s.channels = int(binary.LittleEndian.Uint16(b[2:4]))
		s.sampleRate = int(binary.LittleEndian.Uint32(b[4:8]))
		s.bits = int(binary.LittleEndian.Uint16(b[14:16]))
		if format == 0xFFFE && len(b) >= 40 {
			// WAVE_FORMAT_EXTENSIBLE keeps the real tag in the first two bytes
			// of its subformat GUID — the same unwrapping the WAV reader does.
			format = binary.LittleEndian.Uint16(b[24:26])
		}
		s.audioCodec = format
		if format == 3 {
			s.isFloat = true
		}
	}
}

// pickStream returns the first stream of a kind and its index, which is the
// number the movi chunk ids use.
func pickStream(streams []stream, kind string) (*stream, int) {
	for i := range streams {
		if streams[i].kind == kind {
			return &streams[i], i
		}
	}
	return nil, -1
}

// parseStreamChunkID splits "00dc" into the stream number and the two-letter
// data type. Anything that is not two digits and two letters is not a stream
// chunk — "idx1" and "JUNK" both land here.
func parseStreamChunkID(id string) (idx int, kind string, ok bool) {
	if len(id) != 4 || id[0] < '0' || id[0] > '9' || id[1] < '0' || id[1] > '9' {
		return 0, "", false
	}
	return int(id[0]-'0')*10 + int(id[1]-'0'), strings.ToLower(id[2:4]), true
}

func isMotionJPEG(s *stream) bool {
	for _, c := range []string{s.compression, s.handler} {
		switch strings.ToUpper(strings.TrimSpace(c)) {
		case "MJPG", "JPEG", "MJPA", "MJPB", "DMB1", "AVRN":
			return true
		}
	}
	return false
}

func describeCodec(s *stream) string {
	c := strings.TrimSpace(s.compression)
	if c == "" || c == "\x00\x00\x00\x00" {
		c = strings.TrimSpace(s.handler)
	}
	if c == "" {
		return "an unnamed codec"
	}
	return c
}

// fps is the stream header's rate over its scale, defaulted to something
// playable when either is missing. A file with no usable rate still plays; it
// just plays at fifteen frames a second, which is visibly wrong in a way that
// points at the file rather than at the engine.
func fps(s *stream) float64 {
	if s.scale > 0 && s.rate > 0 {
		f := float64(s.rate) / float64(s.scale)
		if f > 0.1 && f < 1000 {
			return f
		}
	}
	return 15
}

func (d *aviDecoder) fillStarts(f float64) {
	d.trk.Starts = make([]float64, len(d.frames))
	for i := range d.frames {
		d.trk.Starts[i] = float64(i) / f
	}
	d.trk.Duration = float64(len(d.frames)) / f
}

func (d *aviDecoder) sizeFromFirstFrame() error {
	b, err := readAll(d.f, d.frames[0].off, d.frames[0].size)
	if err != nil {
		return err
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("first frame is not a JPEG: %w", err)
	}
	d.trk.Width, d.trk.Height = cfg.Width, cfg.Height
	return nil
}

// decodeSoundtrack turns the concatenated `wb` bytes into a clip, or nil.
//
// Every failure here is silent picture rather than a refused load, and each one
// logs the reason once: a cutscene that plays without its dialogue is a bug
// worth seeing on screen, where somebody will report it, rather than one that
// stops the menu from opening.
func (d *aviDecoder) decodeSoundtrack(s *stream, raw []byte, limit int) *audio.Clip {
	if s == nil || len(raw) == 0 {
		return nil
	}
	if limit > 0 && len(raw) >= limit {
		logf("%s: soundtrack truncated at %d seconds — split a longer film into two files",
			d.name, MaxSoundtrackSeconds)
		raw = raw[:limit]
	}
	samples := audio.DecodePCMSamples(raw, s.bits, s.isFloat)
	if len(samples) == 0 {
		return nil
	}
	return &audio.Clip{
		Name:     d.name + " (soundtrack)",
		Samples:  samples,
		Rate:     s.sampleRate,
		Channels: s.channels,
	}
}

// readableAudio reports whether a soundtrack is in a form this can turn into a
// clip: uncompressed, mono or stereo, at a plausible rate and depth.
func readableAudio(s *stream) bool {
	if s.audioCodec != 1 && s.audioCodec != 3 {
		return false
	}
	if s.channels < 1 || s.channels > 2 {
		return false
	}
	if s.sampleRate < 1000 || s.sampleRate > 384000 {
		return false
	}
	switch {
	case s.isFloat && s.bits == 32:
		return true
	case !s.isFloat && (s.bits == 8 || s.bits == 16 || s.bits == 24 || s.bits == 32):
		return true
	}
	return false
}

// describeAudio names the one thing wrong with a soundtrack, so the log says
// what to re-encode rather than that something was unsatisfactory.
func describeAudio(s *stream) string {
	switch {
	case s.audioCodec != 1 && s.audioCodec != 3:
		return fmt.Sprintf("the soundtrack is compressed (WAVE format %d); re-encode it with -c:a pcm_s16le", s.audioCodec)
	case s.channels < 1 || s.channels > 2:
		return fmt.Sprintf("the soundtrack has %d channels; only mono and stereo play", s.channels)
	case s.sampleRate < 1000 || s.sampleRate > 384000:
		return fmt.Sprintf("the soundtrack claims %d Hz, which is not a sample rate", s.sampleRate)
	}
	return fmt.Sprintf("the soundtrack is %d-bit, which is not a PCM depth this reads", s.bits)
}

// ---------- decoding a frame ----------

func (d *aviDecoder) Frame(i int) (*image.RGBA, error) {
	if i < 0 || i >= len(d.frames) {
		return nil, fmt.Errorf("frame %d of %d", i, len(d.frames))
	}
	ref := d.frames[i]
	if int64(cap(d.buf)) < ref.size {
		d.buf = make([]byte, ref.size)
	}
	b := d.buf[:ref.size]
	if _, err := d.f.ReadAt(b, ref.off); err != nil && err != io.EOF {
		return nil, err
	}

	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("frame %d: %w", i, err)
	}
	// Straight into the reused buffer at the track's size. A frame that decodes
	// to a different size than the header promised is drawn where it lands
	// rather than resized: it is a broken file either way, and a black band is
	// a clearer symptom than a stretch. The clear only happens in that case —
	// a well-formed frame covers every pixel, and wiping first would be a
	// full-frame write per displayed frame for nothing.
	if !img.Bounds().Sub(img.Bounds().Min).Eq(d.img.Bounds()) {
		draw.Draw(d.img, d.img.Bounds(), image.Transparent, image.Point{}, draw.Src)
	}
	draw.Draw(d.img, d.img.Bounds(), img, img.Bounds().Min, draw.Src)
	return d.img, nil
}
