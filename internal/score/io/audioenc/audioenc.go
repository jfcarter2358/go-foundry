// Package audioenc writes rendered audio as WAV, FLAC, OGG Vorbis and MP3:
// what the Score editor's Export dialog and `foundry score export-audio`
// write a mix and its stems with.
//
// WAV and FLAC are this package's own (FLAC in flac.go, a lossless
// encoder with fixed and LPC predictors and Rice coding; flacdec.go reads
// it back). OGG Vorbis and MP3 are the reference encoders, libvorbisenc
// (with libvorbis and libogg) and LAME's libmp3lame, loaded when first
// asked for from beside the editor (codecs/) or the system's own copy
// (dl.go) — never linked: the release game never contains them, and an
// editor without them still runs, and says what is missing when an
// export needs one (docs/score/CODECS.md has how they ship and their
// licences).
//
// Audio comes in as the renderer makes it: interleaved float32 frames,
// any channel count, -1..1 (clipped beyond).
package audioenc

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/tracker"
)

// Vendor is what the files say wrote them.
const Vendor = "Foundry Score"

// Format is an audio file format.
type Format string

const (
	WAV  Format = "wav"
	FLAC Format = "flac"
	OGG  Format = "ogg"
	MP3  Format = "mp3"
)

// Formats are every format, in the order a dialog lists them.
var Formats = []Format{WAV, FLAC, OGG, MP3}

// Ext is the format's file extension.
func (f Format) Ext() string { return "." + string(f) }

// Lossless reports a format that keeps every sample (and so takes a bit
// depth rather than a quality).
func (f Format) Lossless() bool { return f == WAV || f == FLAC }

// ParseFormat reads a format's name or extension.
func ParseFormat(s string) (Format, error) {
	t := Format(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "."))
	switch t {
	case "vorbis", "oga":
		t = OGG
	case "wave":
		t = WAV
	}
	for _, f := range Formats {
		if f == t {
			return f, nil
		}
	}
	return "", fmt.Errorf("%q is not an audio format (wav, flac, ogg, mp3)", s)
}

// Tags are the file's metadata.
type Tags struct {
	Title, Artist, Album, Comment string
}

func (t Tags) pairs() [][2]string {
	var out [][2]string
	for _, p := range [][2]string{{"TITLE", t.Title}, {"ARTIST", t.Artist}, {"ALBUM", t.Album}, {"COMMENT", t.Comment}} {
		if p[1] != "" {
			out = append(out, p)
		}
	}
	return out
}

// Options shape an encoding.
type Options struct {
	Format Format
	// Bits is a lossless format's sample size: 16 or 24 (0 is 24).
	Bits int
	// Quality is OGG Vorbis's quality, -1..10, and for MP3 the VBR
	// quality, 0 (best) .. 9 (smallest), when Bitrate is 0. DefaultOptions
	// has the usual ones.
	Quality float32
	// Bitrate, for MP3, asks for constant bit rate at this many kb/s
	// (32..320) instead of VBR.
	Bitrate int
	Tags    Tags
}

// DefaultOGGQuality and DefaultMP3Quality are what the dialog starts at.
const (
	DefaultOGGQuality = 6
	DefaultMP3Quality = 2
)

// DefaultOptions are a format's usual settings: 24-bit lossless, Vorbis
// quality 6, MP3 VBR V2.
func DefaultOptions(f Format) Options {
	o := Options{Format: f, Bits: 24}
	switch f {
	case OGG:
		o.Quality = DefaultOGGQuality
	case MP3:
		o.Quality = DefaultMP3Quality
	}
	return o
}

func (o Options) bits() int {
	if o.Bits == 0 {
		return 24
	}
	return o.Bits
}

// Describe is the options as a dialog says them: "48 kHz · 24-bit",
// "quality 6", "VBR V2", "192 kb/s".
func (o Options) Describe(rate int) string {
	khz := fmt.Sprintf("%g kHz", float64(rate)/1000)
	switch o.Format {
	case WAV, FLAC:
		return fmt.Sprintf("%s · %d-bit", khz, o.bits())
	case OGG:
		return fmt.Sprintf("%s · quality %g", khz, o.Quality)
	case MP3:
		if o.Bitrate > 0 {
			return fmt.Sprintf("%s · %d kb/s", khz, o.Bitrate)
		}
		return fmt.Sprintf("%s · VBR V%d", khz, mp3Quality(o.Quality))
	}
	return khz
}

// Encode writes interleaved audio in a format.
func Encode(data []float32, channels, rate int, o Options) ([]byte, error) {
	switch o.Format {
	case WAV:
		var b bytes.Buffer
		if err := tracker.WriteWAV(&b, data, channels, rate, o.bits()); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	case FLAC:
		return EncodeFLAC(data, channels, rate, o.bits(), o.Tags)
	case OGG:
		return EncodeVorbis(data, channels, rate, o.Quality, o.Tags)
	case MP3:
		return EncodeMP3(data, channels, rate, o)
	}
	return nil, fmt.Errorf("audioenc: no format %q", o.Format)
}

// WriteFile encodes audio into a file, making its directory.
func WriteFile(path string, data []float32, channels, rate int, o Options) error {
	b, err := Encode(data, channels, rate, o)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// Available reports whether a format can be written here, and if not why:
// WAV and FLAC always; OGG and MP3 when their libraries load.
func Available(f Format) error {
	switch f {
	case WAV, FLAC:
		return nil
	case OGG:
		_, err := loadVorbis()
		return err
	case MP3:
		_, err := loadLAME()
		return err
	}
	return fmt.Errorf("audioenc: no format %q", f)
}

// MissingLibraryError is an OGG or MP3 export with its library not found:
// which one, and everywhere it was looked for.
type MissingLibraryError struct {
	Format  Format
	Library string // "libvorbisenc", "libmp3lame"
	Tried   []string
	Reason  string
}

func (e *MissingLibraryError) Error() string {
	what := map[Format]string{OGG: "OGG Vorbis", MP3: "MP3"}[e.Format]
	msg := fmt.Sprintf("%s export needs %s, which was not found", what, e.Library)
	if e.Reason != "" {
		msg += " (" + e.Reason + ")"
	}
	return msg + ". " + installHint(e.Format)
}

// installHint says how to get a codec where this build looks.
func installHint(f Format) string {
	dirs := CodecDirs()
	sort.Strings(dirs)
	where := "the codecs folder beside the editor"
	if len(dirs) > 0 {
		where = dirs[0]
	}
	switch f {
	case OGG:
		return "Install libvorbis (on Debian and Ubuntu: apt install libvorbisenc2), or put libogg, libvorbis and libvorbisenc in " + where + "."
	case MP3:
		return "Install LAME's library (on Debian and Ubuntu: apt install libmp3lame0), or put libmp3lame in " + where + "."
	}
	return ""
}
