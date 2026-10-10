package gfx

// texture_mean.go — a texture's average colour, for the light bake.
//
// Light bounced off a surface takes on the surface's colour, and the bake works
// per vertex, far coarser than any texel: what a wall reflects is, to it, the
// wall's texture averaged down to one colour. So that one colour is recorded as
// the texture is decoded, which is the only moment its pixels are in hand — the
// GPU keeps the rest, and a headless compile never uploads at all.

import (
	"image"
	"path/filepath"
	"sync"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/fileutil"
)

var (
	textureMeansMu sync.Mutex
	// textureMeans is keyed like Textures. Guarded because PrefetchTextures
	// decodes off the main thread and a compile may run beside the editor.
	textureMeans = map[string]mgl32.Vec3{}
)

// meanSampleBudget caps how many texels an average reads. A 4k texture is
// sixteen million of them, and the average of a quarter-million evenly spread
// ones is the same colour to three decimal places.
const meanSampleBudget = 1 << 18

// meanColorOf is img's colour averaged over its texels, each weighted by its
// alpha — a cutout's clear texels are not part of the surface — and false when
// the image is entirely clear. Channels are 0..1, the values the shader
// multiplies the lighting by.
func meanColorOf(img *image.RGBA) (mgl32.Vec3, bool) {
	n := len(img.Pix) / 4
	if n == 0 {
		return mgl32.Vec3{}, false
	}
	stride := 1
	for n/stride > meanSampleBudget {
		stride *= 2
	}
	var r, g, b, a float64
	for i := 0; i < n; i += stride {
		px := img.Pix[i*4 : i*4+4]
		w := float64(px[3])
		r += float64(px[0]) * w
		g += float64(px[1]) * w
		b += float64(px[2]) * w
		a += w
	}
	if a == 0 {
		return mgl32.Vec3{}, false
	}
	a *= 255
	return mgl32.Vec3{float32(r / a), float32(g / a), float32(b / a)}, true
}

// noteTextureMean records name's average colour from its decoded level 0.
func noteTextureMean(name string, img *image.RGBA) {
	mean, ok := meanColorOf(img)
	textureMeansMu.Lock()
	defer textureMeansMu.Unlock()
	if ok {
		textureMeans[name] = mean
	} else {
		delete(textureMeans, name)
	}
}

func forgetTextureMean(name string) {
	textureMeansMu.Lock()
	delete(textureMeans, name)
	textureMeansMu.Unlock()
}

// TextureMeanColor is a texture's average colour, 0..1 per channel, and whether
// there is one to give. A texture that was never loaded is decoded from disk
// for the answer (and remembered), so a caller does not need the level's
// textures on the GPU — only on disk, under TextureNames.
func TextureMeanColor(name string) (mgl32.Vec3, bool) {
	if name == "" {
		return mgl32.Vec3{}, false
	}
	textureMeansMu.Lock()
	mean, ok := textureMeans[name]
	textureMeansMu.Unlock()
	if ok {
		return mean, true
	}
	path, known := TextureNames[name]
	if !known {
		return mgl32.Vec3{}, false
	}
	img, err := decodeTextureFile(filepath.Join(fileutil.ResourcesPath, path))
	if err != nil {
		return mgl32.Vec3{}, false
	}
	noteTextureMean(name, img)
	return meanColorOf(img)
}
