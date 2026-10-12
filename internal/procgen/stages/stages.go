// Package stages holds the built-in generator stage kinds. Importing it
// (usually blank: `import _ "foundry/internal/procgen/stages"`) registers
// every one with internal/procgen, which is what the Generator editor and
// `foundry gen` do.
//
// One file per kind, each registering itself from init and touching nothing
// shared: a new stage is a new file here (or a package of its own, imported
// from a file here), and no switch anywhere else grows a case. Each kind's
// test sits beside it.
//
// Phase 0 shipped noise_preview (noise_preview.go), a debugging aid that
// exercises every part of the machinery — the schema's every widget type, a
// fast preview with image, point, line and mesh layers, a real run that
// writes PNGs, cooks border-locked Nanite tiles in parallel and places them,
// and a Load that reads its heightmap back for a downstream stage.
//
// Phase 1 adds terrain (terrain.go, terrain_run.go, terrain_preview.go):
// eroded canyon country from internal/procgen/terrain, its masks and
// metadata, and closed tiles cooked on one quantisation grid.
package stages

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
)

// encodePNG16 is a 16-bit greyscale PNG of values mapped lo..hi to 0..65535.
// The encoder is deterministic, so the same values make the same bytes.
func encodePNG16(w, h int, vals []float32, lo, hi float32) ([]byte, error) {
	img := image.NewGray16(image.Rect(0, 0, w, h))
	span := hi - lo
	if span == 0 {
		span = 1
	}
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			t := (vals[j*w+i] - lo) / span
			t = min(max(t, 0), 1)
			img.SetGray16(i, j, color.Gray16{Y: uint16(t*65535 + 0.5)})
		}
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodePNG8 is an 8-bit greyscale PNG of values in 0..1.
func encodePNG8(w, h int, vals []float32) ([]byte, error) {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			t := min(max(vals[j*w+i], 0), 1)
			img.SetGray(i, j, color.Gray{Y: uint8(t*255 + 0.5)})
		}
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodePNG16 reads a greyscale PNG back as 0..1 values.
func decodePNG16(data []byte) (w, h int, vals []float32, err error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return 0, 0, nil, err
	}
	b := img.Bounds()
	w, h = b.Dx(), b.Dy()
	vals = make([]float32, w*h)
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			g := color.Gray16Model.Convert(img.At(b.Min.X+i, b.Min.Y+j)).(color.Gray16)
			vals[j*w+i] = float32(g.Y) / 65535
		}
	}
	return w, h, vals, nil
}

// readFile is os.ReadFile, named for the Load hooks that read their outputs.
func readFile(path string) ([]byte, error) { return os.ReadFile(path) }
