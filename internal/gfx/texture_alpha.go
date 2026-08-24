package gfx

// texture_alpha.go — semi-transparent-texel detection.
//
// Cutout (fully transparent texels, alpha < 0.001 in the shader) already
// works: fragment.glsl discards below that threshold regardless of this file.
// What doesn't work without it is a texel that's partially see-through — that
// currently renders opaque because a surface only enters the sorted,
// back-to-front blended pass (main.go) when Geometry.IsTransparent() is true,
// and that was purely the per-geometry Alpha scalar. See IsTransparent in
// wall.go for the routing this feeds.

import (
	"image"

	"foundry/internal/texdoc"
)

// textureHasAlpha records which uploaded textures contain semi-transparent
// texels, keyed by texture name. Populated by UpdateTextureHasAlpha whenever
// a texture is flattened (on save, or on any live edit in the texture
// editor), so Geometry.IsTransparent can route those surfaces into the
// blended pass with a map lookup instead of scanning texels on every draw.
var textureHasAlpha = map[string]bool{}

// alphaCutoffLow/High bound the "semi-transparent" band that needs the sorted
// blended pass. A slot at or below Low is cutout territory, already handled
// by the shader's discard; at or above High it's opaque enough to draw in the
// ordinary opaque pass.
const (
	alphaCutoffLow  = 0
	alphaCutoffHigh = 250
)

// ComputeHasAlpha reports whether img (indexed against pal) contains any
// texel whose palette slot is semi-transparent. It builds a used-index
// histogram first so a 1024x1024 image with only a handful of distinct
// colors costs a 256-entry palette scan, not a million-texel one.
func ComputeHasAlpha(pal *texdoc.TexPalette, img *image.Paletted) bool {
	var used [texdoc.TexPaletteSz]bool
	for _, idx := range img.Pix {
		used[idx] = true
	}
	for i, u := range used {
		if !u {
			continue
		}
		a := pal[i].A
		if a > alphaCutoffLow && a < alphaCutoffHigh {
			return true
		}
	}
	return false
}

// UpdateTextureHasAlpha recomputes and records the has-alpha flag for name
// from its flattened image and the palette it was flattened against.
func UpdateTextureHasAlpha(name string, pal *texdoc.TexPalette, img *image.Paletted) {
	textureHasAlpha[name] = ComputeHasAlpha(pal, img)
}
