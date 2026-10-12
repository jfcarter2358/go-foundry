package gfx

// texture_compress_settings.go — the player's texture memory settings.
//
// Two player settings like SmoothLighting, saved to graphics.yaml
// (app/graphics_settings.go: texture_compression, texture_skip_mips) and set
// from a game's settings screen through @level.prop.texture_compression and
// @level.prop.texture_skip_mips (script_props_level.go):
//
//   - TextureCompression: world textures read from textures/*.png and at
//     least TextureCompressMinSize on both sides upload block-compressed —
//     BC7 for albedos, BC5 for <name>_normal maps — at a quarter of RGBA8's
//     memory. OFF by default, so existing content renders exactly as it did
//     until a player (or a game's default screen) turns it on. See
//     texture_compress.go.
//   - TextureSkipMips (0..3): textures of at least TextureSkipMipsMinSize on
//     both sides leave out their N largest mip levels — the texture is
//     uploaded at half, quarter or eighth size and those levels are never
//     allocated — but never below TextureSkipMipsFloor on the short side.
//     Each level dropped is roughly 75% less memory for that texture.
//
// Both are read when a texture is loaded. A texture already resident keeps
// the form it was uploaded in until it is next loaded, so a change takes full
// effect at the next start; the setters say as much in their script docs.
//
// The environment outranks the saved file for headless runs and tests, as
// FOUNDRY_AA does: FOUNDRY_TEXTURE_COMPRESSION=1|0 and
// FOUNDRY_TEXTURE_SKIP_MIPS=0..3.

import (
	"os"
	"strconv"
)

const (
	// TextureCompressMinSize is the smallest side a texture may have and be
	// compressed: UI art, tiny tiles and the like stay RGBA8, where the
	// saving is nothing and the block artefacts would show.
	TextureCompressMinSize = 256
	// TextureSkipMipsMinSize is the smallest side a texture may have and
	// have its top levels skipped.
	TextureSkipMipsMinSize = 512
	// TextureSkipMipsFloor is the short side skipping stops at: a 512²
	// texture under skip 3 is uploaded at 256², not 64².
	TextureSkipMipsFloor = 256
	// TextureSkipMipsMax bounds the setting.
	TextureSkipMipsMax = 3
)

// TextureCompression is whether textures loaded from disk upload
// block-compressed. Off by default; see the file comment.
var TextureCompression = envBool("FOUNDRY_TEXTURE_COMPRESSION", false)

// TextureCompressionForced is set when the environment chose it, so the
// saved setting does not override the run.
var TextureCompressionForced = os.Getenv("FOUNDRY_TEXTURE_COMPRESSION") != ""

// TextureSkipMips is how many of the largest mip levels big textures leave
// out, 0..TextureSkipMipsMax.
var TextureSkipMips = envSkipMips()

// TextureSkipMipsForced is set when the environment chose it.
var TextureSkipMipsForced = os.Getenv("FOUNDRY_TEXTURE_SKIP_MIPS") != ""

// SetTextureCompression changes TextureCompression, marking it for saving.
func SetTextureCompression(on bool) {
	if TextureCompression == on {
		return
	}
	TextureCompression = on
	GraphicsSettingsChanged = true
}

// SetTextureSkipMips changes TextureSkipMips, clamped, marking it for saving.
func SetTextureSkipMips(n int) {
	n = ClampTextureSkipMips(n)
	if TextureSkipMips == n {
		return
	}
	TextureSkipMips = n
	GraphicsSettingsChanged = true
}

// ClampTextureSkipMips is n within 0..TextureSkipMipsMax.
func ClampTextureSkipMips(n int) int { return min(max(n, 0), TextureSkipMipsMax) }

// textureSkipLevels is how many top levels a w x h texture drops under the
// current setting.
func textureSkipLevels(w, h int) int {
	short := min(w, h)
	if TextureSkipMips <= 0 || short < TextureSkipMipsMinSize {
		return 0
	}
	n := 0
	for n < TextureSkipMips && short/2 >= TextureSkipMipsFloor {
		short /= 2
		n++
	}
	return n
}

func envBool(name string, def bool) bool {
	switch os.Getenv(name) {
	case "1", "true", "on", "yes":
		return true
	case "0", "false", "off", "no":
		return false
	}
	return def
}

func envSkipMips() int {
	n, err := strconv.Atoi(os.Getenv("FOUNDRY_TEXTURE_SKIP_MIPS"))
	if err != nil {
		return 0
	}
	return ClampTextureSkipMips(n)
}
