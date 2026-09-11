package uifont

// Package uifont bakes TrueType fonts into glyph atlases and lays text out
// against them.
//
// It is pure CPU: no GL, no level, no editor. That matters because the UI
// layout solve in internal/ui measures text — a label auto-sizes, and a wrapped
// paragraph's height depends on the font — so font metrics have to sit *below*
// the solver. Metrics are data, not an effect, so they live with the data
// rather than arriving through a function variable. It is the same split
// texdoc/texture makes, and it is what lets the UI editor's overlays and the
// game's draw path share one set of measurements with no plumbing at all.
//
// The GPU half stays up in internal/uidraw, which uploads Atlas.Pix and turns
// Layout's quads into vertices.
//
// uifont.go holds the asset directory, the (font, size) cache, and the
// missing-font policy.

import (
	"os"
	"path/filepath"
	"sync"

	"foundry/internal/fileutil"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// FontsDirName is the folder .ttf files live in, alongside textures/, models/
// and particles/.
const FontsDirName = "fonts"

// FontExt is the extension a font asset carries.
const FontExt = ".ttf"

// FontsDir is the absolute fonts folder, resolved against the executable rather
// than the working directory for the reason fileutil.ResolveAssetPath gives.
func FontsDir() string { return filepath.Join(fileutil.ResourcesPath, FontsDirName) }

// FontPath is the absolute path of a font named without its extension.
func FontPath(name string) string { return filepath.Join(FontsDir(), name+FontExt) }

// ScanFontFiles returns the sorted font filenames on disk, for the editor's
// font dropdown.
func ScanFontFiles() []string { return fileutil.ScanFilesByExt(FontsDir(), FontExt) }

// MinSize and MaxSize bound the pixel sizes an atlas will be baked at.
//
// Sizes are rounded to whole pixels before they reach the cache, because the
// cache key is (font, size) and a size that came out of a layout calculation
// can otherwise differ in the last bit from frame to frame — which would bake a
// fresh 512x512 page every frame and never hit the cache once.
const (
	MinSize = 6
	MaxSize = 256
)

type faceKey struct {
	font string
	px   int
}

var (
	facesMu sync.Mutex
	faces   = map[faceKey]*Atlas{}
	fonts   = map[string]*sfnt.Font{} // parsed once per name; nil means "missing"
	warned  = map[string]bool{}       // one log per missing name, not one per frame
)

// LogMissing is called once per font name that cannot be loaded. internal/ui
// installs it so the message reaches the editor's log window; nil is correct
// for a headless test, which is why a missing font is never an error return.
var LogMissing func(name string, err error)

// Face returns the atlas for a font at a pixel size, baking ASCII on first use.
//
// It never fails. A name that does not resolve to a readable .ttf falls back to
// the compiled-in bitmap face (see uifont_fallback.go), which is deliberately
// crude so that a missing font reads as a missing font rather than as a
// slightly-off one. Refusing to lay out text at all would take a whole screen
// down over one bad name, which is the log-and-keep rule levelio already
// follows for a broken asset reference.
func Face(name string, px float32) *Atlas {
	size := int(px + 0.5)
	if size < MinSize {
		size = MinSize
	}
	if size > MaxSize {
		size = MaxSize
	}

	facesMu.Lock()
	defer facesMu.Unlock()

	key := faceKey{name, size}
	if a := faces[key]; a != nil {
		return a
	}

	a := newAtlas(name, float32(size), loadFontLocked(name))
	faces[key] = a
	return a
}

// Faces returns every atlas baked so far, in a deterministic order. The
// uploader in internal/uidraw walks these once a frame looking for pages whose
// Rev has outrun their UploadedRev.
func Faces() []*Atlas {
	facesMu.Lock()
	defer facesMu.Unlock()

	keys := make([]faceKey, 0, len(faces))
	for k := range faces {
		keys = append(keys, k)
	}
	sortFaceKeys(keys)

	out := make([]*Atlas, 0, len(keys))
	for _, k := range keys {
		out = append(out, faces[k])
	}
	return out
}

// InvalidateFont drops every cached atlas for a font, so the next Face call
// re-reads it from disk. The editor calls this when a .ttf is replaced, and it
// also clears the missing-font warning so a font that has just been added is
// reported again if it is still broken.
func InvalidateFont(name string) {
	facesMu.Lock()
	defer facesMu.Unlock()

	delete(fonts, name)
	delete(warned, name)
	for k := range faces {
		if k.font == name {
			delete(faces, k)
		}
	}
}

// Reset drops the whole cache. Tests use it to isolate from each other; nothing
// in the engine calls it.
func Reset() {
	facesMu.Lock()
	defer facesMu.Unlock()

	faces = map[faceKey]*Atlas{}
	fonts = map[string]*sfnt.Font{}
	warned = map[string]bool{}
}

// loadFontLocked parses a font, memoising both the success and the failure.
// Caller holds facesMu.
func loadFontLocked(name string) *sfnt.Font {
	if f, seen := fonts[name]; seen {
		return f
	}

	var f *sfnt.Font
	err := error(nil)
	if name == "" {
		err = os.ErrNotExist
	} else {
		var data []byte
		if data, err = os.ReadFile(FontPath(name)); err == nil {
			f, err = parseFontBytes(data)
		}
	}

	if err != nil {
		f = nil
		if !warned[name] {
			warned[name] = true
			if LogMissing != nil {
				LogMissing(name, err)
			}
		}
	}
	fonts[name] = f
	return f
}

// parseFontBytes is sfnt.Parse, named so the one place that knows which parser
// this package uses is not spread across the loader and its tests.
func parseFontBytes(data []byte) (*sfnt.Font, error) { return sfnt.Parse(data) }

// newFace turns a parsed font into a rasterizer at a pixel size. DPI is 72 so
// that one "point" is one pixel and the caller's size means what it says.
func newFace(f *sfnt.Font, px float32) font.Face {
	if f == nil {
		return nil
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    float64(px),
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil
	}
	return face
}

// sortFaceKeys orders by name then size. A hand-rolled insertion sort rather
// than sort.Slice because the slice is a handful of entries and this keeps the
// package's imports to what it actually needs.
func sortFaceKeys(keys []faceKey) {
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0; j-- {
			a, b := keys[j-1], keys[j]
			if a.font < b.font || (a.font == b.font && a.px <= b.px) {
				break
			}
			keys[j-1], keys[j] = b, a
		}
	}
}
