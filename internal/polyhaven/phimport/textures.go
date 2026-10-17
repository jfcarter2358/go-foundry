package phimport

// textures.go — Poly Haven's JPGs (and PNGs) as engine PNGs.
//
//   - Colour maps (diffuse, normal, ARM) keep their RGB; a diffuse whose
//     material is a cutout (glTF alphaMode MASK, or BLEND with an alpha map)
//     takes the asset's alpha map as its alpha, so the engine's alpha-tested
//     opaque pass — and the Nanite visibility buffer's coverage test
//     (nanite_vis_frag.glsl) — cut the leaves out.
//   - One channel of the ARM (R = ambient occlusion, G = roughness) can be
//     written alone as a grey PNG, and the displacement is written grey.
//   - MaxSize, when set, halves the image (box filter) until it fits: a
//     lower resolution than the API offers.
//
// PNGs are written at png.BestSpeed: a 2k colour map encodes in a fraction
// of a second rather than a couple, at ~10-15% more disk.

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
)

// channel picks what a conversion writes.
type channel int

const (
	chRGB   channel = iota // the colour, opaque (or with the alpha map)
	chGrayR                // R alone, grey
	chGrayG                // G alone, grey
	chLuma                 // the luminance, grey
	// chNormal is an OpenGL-convention (nor_gl) normal map with its green
	// turned over: the engine's +V runs DOWN the image (a texture is
	// uploaded top row first, and fragment.glsl's cotangent frame takes
	// its bitangent along +V), so the engine's normal maps are what DCC
	// tools call DirectX-convention — texdoc's NormalMapFromHeight authors
	// them that way. Poly Haven's nor_gl taken as it is lights every bump
	// from the wrong side vertically.
	chNormal
)

// convertSpec is one texture to write.
type convertSpec struct {
	src, alpha, dst string
	ch              channel
	maxSize         int
}

// decodeFile reads an image file of any registered format.
func decodeFile(path string) (image.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return img, nil
}

// toNRGBA is img as tightly packed NRGBA.
func toNRGBA(img image.Image) *image.NRGBA {
	if n, ok := img.(*image.NRGBA); ok && n.Rect.Min == (image.Point{}) {
		return n
	}
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Rect, img, b.Min, draw.Src)
	return out
}

// halve box-filters img to half size (odd sizes round down, at least 1).
func halve(img *image.NRGBA) *image.NRGBA {
	w, h := max(img.Rect.Dx()/2, 1), max(img.Rect.Dy()/2, 1)
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	sw, sh := img.Rect.Dx(), img.Rect.Dy()
	for y := 0; y < h; y++ {
		y0, y1 := min(2*y, sh-1), min(2*y+1, sh-1)
		for x := 0; x < w; x++ {
			x0, x1 := min(2*x, sw-1), min(2*x+1, sw-1)
			for c := 0; c < 4; c++ {
				s := int(img.Pix[y0*img.Stride+x0*4+c]) + int(img.Pix[y0*img.Stride+x1*4+c]) +
					int(img.Pix[y1*img.Stride+x0*4+c]) + int(img.Pix[y1*img.Stride+x1*4+c])
				out.Pix[y*out.Stride+x*4+c] = uint8((s + 2) / 4)
			}
		}
	}
	return out
}

// fit halves img until its larger side is at most maxSize (0: unchanged).
func fit(img *image.NRGBA, maxSize int) *image.NRGBA {
	for maxSize > 0 && max(img.Rect.Dx(), img.Rect.Dy()) > maxSize {
		img = halve(img)
	}
	return img
}

// sampleGray is the alpha map's value at (x, y) of a w×h target (nearest
// texel; the maps are normally the same size).
func sampleGray(a *image.NRGBA, x, y, w, h int) uint8 {
	ax := min(x*a.Rect.Dx()/w, a.Rect.Dx()-1)
	ay := min(y*a.Rect.Dy()/h, a.Rect.Dy()-1)
	p := a.Pix[ay*a.Stride+ax*4:]
	return uint8((299*int(p[0]) + 587*int(p[1]) + 114*int(p[2]) + 500) / 1000)
}

// convert writes one texture.
func convert(s convertSpec) error {
	src, err := decodeFile(s.src)
	if err != nil {
		return err
	}
	img := toNRGBA(src)
	var out image.Image
	switch s.ch {
	case chNormal:
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i+1] = 255 - img.Pix[i+1] // green: nor_gl's +Y up the image → the engine's +V down it
			img.Pix[i+3] = 255
		}
		out = fit(img, s.maxSize)
	case chRGB:
		if s.alpha != "" {
			ai, err := decodeFile(s.alpha)
			if err != nil {
				return err
			}
			a := toNRGBA(ai)
			w, h := img.Rect.Dx(), img.Rect.Dy()
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					img.Pix[y*img.Stride+x*4+3] = sampleGray(a, x, y, w, h)
				}
			}
		} else {
			for i := 3; i < len(img.Pix); i += 4 {
				img.Pix[i] = 255
			}
		}
		out = fit(img, s.maxSize)
	default:
		img = fit(img, s.maxSize)
		g := image.NewGray(img.Rect)
		for i, j := 0, 0; i < len(img.Pix); i, j = i+4, j+1 {
			switch s.ch {
			case chGrayR:
				g.Pix[j] = img.Pix[i]
			case chGrayG:
				g.Pix[j] = img.Pix[i+1]
			default:
				g.Pix[j] = uint8((299*int(img.Pix[i]) + 587*int(img.Pix[i+1]) + 114*int(img.Pix[i+2]) + 500) / 1000)
			}
		}
		out = g
	}
	return writePNG(s.dst, out)
}

// writePNG encodes img to path atomically.
func writePNG(path string, img image.Image) error {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// alphaCoverage is the fraction of a texture's texels under half alpha — for
// the import's log ("the leaves' cutout is 61% empty").
func alphaCoverage(path string) (float64, error) {
	img, err := decodeFile(path)
	if err != nil {
		return 0, err
	}
	b := img.Bounds()
	n, empty := 0, 0
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			_, _, _, a := img.At(x, y).RGBA()
			n++
			if a < 0x8000 {
				empty++
			}
		}
	}
	if n == 0 {
		return 0, nil
	}
	return float64(empty) / float64(n), nil
}
