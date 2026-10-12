package engrave

// textfont.go — the faces text is set in.
//
// A text item names a style (TextFont), not a file; every backend sets it
// in the Go fonts x/image carries (a humanist sans and its italic, bold and
// bold italic), so the layout can measure text exactly as it will be
// drawn: the raster backend draws with these faces, the editor's imgui
// backend rasterises its text runs with them, and the PDF writer (phase 5)
// maps the four styles onto its base fonts.

import (
	"math"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

var (
	textOnce  sync.Once
	textFonts [4]*sfnt.Font
	faceMu    sync.Mutex
	faces     = map[faceKey]font.Face{}
)

type faceKey struct {
	f  TextFont
	px int // quarter pixels
}

// TextFace is a style's face at a pixel size (an em of px pixels), shared
// and cached by quarter pixels; nil when the font will not load. A face is
// not safe for concurrent drawing: callers on several goroutines take
// their own.
func TextFace(f TextFont, px float64) font.Face {
	textOnce.Do(func() {
		for i, ttf := range [][]byte{goregular.TTF, goitalic.TTF, gobold.TTF, gobolditalic.TTF} {
			textFonts[i], _ = opentype.Parse(ttf)
		}
	})
	k := faceKey{f, int(math.Round(px * 4))}
	faceMu.Lock()
	defer faceMu.Unlock()
	if fc, ok := faces[k]; ok {
		return fc
	}
	src := textFonts[int(f)%len(textFonts)]
	if src == nil {
		return nil
	}
	fc, err := opentype.NewFace(src, &opentype.FaceOptions{Size: float64(k.px) / 4, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil
	}
	faces[k] = fc
	return fc
}

// measurePx is the em text is measured at; widths scale linearly from it
// (the faces are unhinted).
const measurePx = 64

var measureMu sync.Mutex

// TextWidthFont is a run of text's width in a style at a size (staff
// spaces, a size of 1 being a 1-space em), as the backends set it.
func TextWidthFont(s string, f TextFont, size float64) float64 {
	measureMu.Lock()
	defer measureMu.Unlock()
	fc := TextFace(f, measurePx)
	if fc == nil {
		return estimateWidth(s, size)
	}
	return float64(font.MeasureString(fc, s)) / 64 / measurePx * size
}

// TextWidth is a run's width in the plain style.
func TextWidth(s string, size float64) float64 { return TextWidthFont(s, TextPlain, size) }

// estimateWidth guesses a width from a serif's average advances, for a
// build whose fonts will not load.
func estimateWidth(s string, size float64) float64 {
	w := 0.0
	for _, r := range s {
		switch {
		case r == ' ' || r == '.' || r == ',' || r == 'i' || r == 'l' || r == 'j' || r == 't' || r == 'f' || r == 'r':
			w += 0.3
		case r == 'm' || r == 'w' || r == 'M' || r == 'W':
			w += 0.8
		case r >= 'A' && r <= 'Z':
			w += 0.68
		default:
			w += 0.52
		}
	}
	return w * size
}
