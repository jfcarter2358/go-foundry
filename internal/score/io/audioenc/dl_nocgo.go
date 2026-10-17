//go:build !cgo

package audioenc

// dl_nocgo.go — a build without cgo cannot load a C library: OGG and MP3
// say so, and WAV and FLAC (this package's own) still work.

import (
	"os"
	"path/filepath"
)

var searchSystem = true

// CodecDirs are the directories searched for a codec library.
func CodecDirs() []string {
	var out []string
	if d := os.Getenv("FOUNDRY_CODECS"); d != "" {
		out = append(out, d)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		out = append(out, filepath.Join(dir, "codecs"), dir)
	}
	return out
}

// Libraries reports where each codec library was loaded from: nowhere.
func Libraries() map[string]string { return map[string]string{"libvorbisenc": "", "libmp3lame": ""} }

func resetLibraries() {}

const noCgo = "this build was made without cgo, which loading a library needs"

func loadVorbis() (struct{}, error) {
	return struct{}{}, &MissingLibraryError{Format: OGG, Library: "libvorbisenc", Reason: noCgo}
}

func loadLAME() (struct{}, error) {
	return struct{}{}, &MissingLibraryError{Format: MP3, Library: "libmp3lame", Reason: noCgo}
}

// EncodeVorbis needs libvorbisenc, which this build cannot load.
func EncodeVorbis(data []float32, channels, rate int, quality float32, tags Tags) ([]byte, error) {
	_, err := loadVorbis()
	return nil, err
}

// EncodeMP3 needs libmp3lame, which this build cannot load.
func EncodeMP3(data []float32, channels, rate int, o Options) ([]byte, error) {
	_, err := loadLAME()
	return nil, err
}

func mp3Quality(q float32) int { return max(0, min(9, int(q+0.5))) }
