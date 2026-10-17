package gfx

// texture_opaque.go — whether a texture's every texel is fully opaque, read
// from the image as it was decoded, before any block compression.
//
// The compute rasteriser takes only opaque surfaces (render's swEligible),
// and used to decide by reading the texture's top level back from the GPU.
// A BC7 upload (TextureCompression) does not give back exactly 255 in every
// opaque block, so with compression on every textured cooked mesh was
// refused and the canyon's small clusters went back to the hardware
// rasteriser (docs/canyon/PERF.md). The decoded image is the truth; the
// readback stays for a texture this never saw (one painted in the editor).

import (
	"image"
	"sync"
)

var (
	textureOpaqueMu sync.Mutex
	textureOpaque   = map[string]bool{}
)

// noteTextureOpaque records whether img, name's decoded level 0, has full
// alpha everywhere.
func noteTextureOpaque(name string, img *image.RGBA) {
	opaque := true
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 255 {
			opaque = false
			break
		}
	}
	textureOpaqueMu.Lock()
	textureOpaque[name] = opaque
	textureOpaqueMu.Unlock()
}

// TextureFullyOpaque reports whether texture name, as decoded at its last
// load, has full alpha in every texel, and whether that is known.
func TextureFullyOpaque(name string) (opaque, known bool) {
	textureOpaqueMu.Lock()
	defer textureOpaqueMu.Unlock()
	opaque, known = textureOpaque[name]
	return
}
