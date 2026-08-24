package main

// texture_filter.go — mipmapping and anisotropic filtering for world textures.
//
// The other half of anti-aliasing, and the half MSAA (antialias.go) cannot
// touch. MSAA antialiases *geometry* edges: it takes extra coverage samples
// per pixel, but still shades each covered pixel once, from one texture
// sample. So a wall or a ground patch receding into the distance — where one
// pixel spans dozens of texels — samples that whole footprint at a single
// point, and which texel it lands on changes as the camera moves. That is the
// crawl/shimmer on distant floors, and no number of coverage samples fixes it.
//
// The fix is prefiltering: mip levels are progressively box-filtered copies of
// the texture, and LINEAR_MIPMAP_LINEAR picks the pair of levels whose texel
// size brackets the pixel's footprint and blends them, so a distant pixel gets
// the *average* of the texels it covers instead of a lottery ticket. Every
// world texture was previously uploaded with a plain LINEAR min filter and no
// mip chain at all, which is why the shimmer was there to begin with.
//
// Mipmapping alone over-blurs at grazing angles — the standard mip selection
// assumes a square footprint, but a floor seen edge-on covers a footprint that
// is many texels long and one texel wide, so it picks the level that suits the
// long axis and smears the short one. Anisotropic filtering takes several
// samples along that long axis at a sharper level instead. That is the single
// biggest visual difference on ground planes, which is most of what this level
// geometry is.
//
// Both are pure sampler state, applied at upload time by
// applyWorldTextureFilter — nothing in the renderer or any shader has to know.
// The one thing that does need to know is alpha-cutout shading: mip levels
// average alpha along with color, so a cutout's hard 0/255 alpha edge becomes a
// soft ramp at distance and the shape thins away. fragment.glsl's alphaSharpen
// path (see AlphaSharpenUniform in main.go) re-derives a crisp edge from that
// ramp, which is what makes mipmapping safe for cutout textures here.

import (
	"log"
	"strings"

	"github.com/go-gl/gl/v4.1-core/gl"
)

// TextureAnisotropyCap bounds how many anisotropic samples we ask for even on
// hardware that offers more. 16x is where the returns flatten out visually and
// is the maximum every GPU that supports the extension at all exposes, so the
// cap is really just a guard against a driver reporting something absurd.
const TextureAnisotropyCap float32 = 16

// TextureAnisotropy is how many anisotropic samples world textures are
// filtered with, resolved once by InitTextureFiltering against what the driver
// supports. 1 means plain isotropic mipmapping — either the extension is
// missing or the driver granted nothing more — and is also the value used
// before InitTextureFiltering runs, so a texture uploaded early is still
// filtered legally, just without the anisotropic pass.
var TextureAnisotropy float32 = 1

// anisotropySupported records whether EXT_texture_filter_anisotropic is
// present. Kept separate from TextureAnisotropy so the F3 overlay can tell
// "driver doesn't have it" apart from "has it, granted 1x".
var anisotropySupported bool

// InitTextureFiltering resolves the anisotropy level to use from the driver's
// advertised maximum. Call once after gl.Init() and before any texture is
// uploaded — LoadTexture/UpdateTextureFromRGBA read TextureAnisotropy at
// upload time and never revisit it.
func InitTextureFiltering() {
	anisotropySupported = hasGLExtension("GL_EXT_texture_filter_anisotropic")
	if !anisotropySupported {
		log.Printf("texture filtering: mipmaps on, anisotropic filtering unavailable (no GL_EXT_texture_filter_anisotropic)")
		return
	}
	var max float32
	gl.GetFloatv(gl.MAX_TEXTURE_MAX_ANISOTROPY, &max)
	if max < 1 {
		// A driver that advertises the extension but reports a nonsense
		// maximum: clamp to the isotropic case rather than passing a value
		// below 1.0 to glTexParameterf, which is an INVALID_VALUE error.
		max = 1
	}
	TextureAnisotropy = min(max, TextureAnisotropyCap)
	log.Printf("texture filtering: mipmaps on, anisotropic %.0fx (driver max %.0fx)", TextureAnisotropy, max)
}

// applyWorldTextureFilter sets the sampler state every world texture shares,
// on whatever texture is currently bound to GL_TEXTURE_2D. The caller is
// responsible for uploading level 0 and then calling generateWorldMipmaps —
// the LINEAR_MIPMAP_LINEAR min filter set here samples a mip chain that does
// not exist yet, and a texture whose chain is never built is *incomplete* and
// samples as black.
func applyWorldTextureFilter() {
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.REPEAT)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.REPEAT)
	if anisotropySupported && TextureAnisotropy > 1 {
		gl.TexParameterf(gl.TEXTURE_2D, gl.TEXTURE_MAX_ANISOTROPY, TextureAnisotropy)
	}
}

// generateWorldMipmaps builds the mip chain for the currently bound
// GL_TEXTURE_2D. Must follow every level-0 upload, including the re-uploads
// the texture editor queues on a live edit — a stale chain would keep showing
// the pre-edit image at distance while the near view updated.
func generateWorldMipmaps() {
	gl.GenerateMipmap(gl.TEXTURE_2D)
}

// hasGLExtension reports whether the current context advertises name. The
// core profile retires the single space-separated glGetString(GL_EXTENSIONS)
// string — it returns INVALID_ENUM here — so the list has to be walked entry
// by entry with glGetStringi.
func hasGLExtension(name string) bool {
	var n int32
	gl.GetIntegerv(gl.NUM_EXTENSIONS, &n)
	for i := int32(0); i < n; i++ {
		if strings.EqualFold(gl.GoStr(gl.GetStringi(gl.EXTENSIONS, uint32(i))), name) {
			return true
		}
	}
	return false
}
