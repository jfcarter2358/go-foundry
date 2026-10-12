package gfx

// texture_normal.go — normal maps.
//
// A normal map is not a separate kind of asset here: it is an ordinary
// texture in the Textures map, uploaded and filtered exactly like an albedo,
// registered under a derived name (NormalMapName). That is what lets every
// piece of machinery this package already has work on it unchanged — the
// upload queue, the animated-texture clock, live edits from the texture
// editor. What this file adds is only the two things that are actually
// specific to normal maps: the derived name, and the set of albedo names that
// have one.
//
// The map itself is authored in the texture editor as a *height* field on a
// document's bump channel and derived from it on save (see
// texdoc/bump.go), so a normal map is never something an artist is asked to
// paint three channels of by hand.
//
// A texture is normal-mapped if, and only if, <name>_normal.png sits beside
// <name>.png in textures/ — or a live edit has just handed one over. Nothing
// in a level file says so, which is deliberate: relief belongs to the
// material, not to the wall wearing it, so painting one bump map lights every
// surface in the game that uses that texture without touching a single level.

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"

	"github.com/go-gl/gl/v4.3-core/gl"

	"foundry/internal/fileutil"
)

// NormalMapSuffix, NormalMapName and IsNormalMapName are re-exported from
// fileutil, which owns the on-disk texture naming rules (the release packer
// needs them too, and cannot import this package). ScanTextures filters on the
// suffix, so a normal map never shows up as a texture in its own right in the
// editor's pickers — picking "brick_normal" for a wall would draw the raw
// normal-encoded colours.
const NormalMapSuffix = fileutil.NormalMapSuffix

// NormalMapUnit is the texture image unit a normal map binds to. Unit 0 is
// the albedo, 1 the shadow cascade array, 2..9 the point-light cubemaps, and
// 10/11 the blend pair — see the note above BlendTexUnit in graphics.go. 12 is
// the next one free, still inside GL 4.3's guaranteed 16.
const NormalMapUnit = 12

// normalMapped is the set of albedo names known to have a normal map loaded
// under NormalMapName(name).
var normalMapped = map[string]bool{}

// NormalMapName is the Textures/TextureNames key a texture's normal map lives
// under.
func NormalMapName(name string) string { return fileutil.NormalMapName(name) }

// IsNormalMapName reports whether name is itself a normal map's name rather
// than an albedo's. Used to keep the companion-loading below from recursing,
// and to keep normal maps out of the scanned texture list.
func IsNormalMapName(name string) bool { return fileutil.IsNormalMapName(name) }

// HasNormalMap reports whether the named texture has a normal map resident on
// the GPU.
func HasNormalMap(name string) bool {
	if !normalMapped[name] {
		return false
	}
	h, ok := Textures[NormalMapName(name)]
	return ok && h != 0
}

// SetNormalMap installs img as the named texture's normal map, queuing it for
// upload the same way any live texture edit is. Used by the texture editor,
// which derives the map from the document being painted rather than reading
// it back off disk.
func SetNormalMap(name string, img *image.RGBA) {
	normalMapped[name] = true
	QueueTextureUpload(NormalMapName(name), img)
}

// ClearNormalMap marks the named texture as no longer normal-mapped — what
// deleting a document's bump channel does. The GL object is left alone: it
// costs one texture's worth of memory until the process exits, and keeping it
// means turning the bump channel back on doesn't have to reallocate.
func ClearNormalMap(name string) {
	delete(normalMapped, name)
	UnregisterAnimatedTexture(NormalMapName(name))
}

// normalMapPath is where a texture's normal map would be on disk. Resolved
// directly rather than through TextureNames, because ScanTextures deliberately
// keeps normal maps out of that map — see NormalMapSuffix.
func normalMapPath(name string) string {
	return filepath.Join(fileutil.TexturesDir(), NormalMapName(name)+".png")
}

// ensureNormalMapLoaded uploads the named texture's normal map if one exists
// on disk and isn't already resident.
//
// Called from LoadTexture, which is the single point every path that puts a
// texture on the GPU goes through — level load, chunk streaming, the editor's
// texture picker. Hanging it there rather than on each of those is what makes
// "the .png is there, so the surface is normal-mapped" true everywhere
// without a caller having to remember.
//
// A missing map is the overwhelmingly common case and not an error; a map
// that exists but fails to decode is, and is logged by the caller.
func ensureNormalMapLoaded(name string) error {
	if name == "" || IsNormalMapName(name) {
		return nil
	}
	if HasNormalMap(name) {
		return nil
	}
	path := normalMapPath(name)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	if err := LoadTexture(NormalMapName(name), path); err != nil {
		return fmt.Errorf("normal map for %q: %w", name, err)
	}
	normalMapped[name] = true
	return nil
}

// BindNormalMap sets the hasNormalMap/normalMap uniform pair for a draw of
// the named texture, binding the map to NormalMapUnit when there is one.
// hasNormalMap is 0 (none), 1 (an RGB normal map) or 2 (a BC5 map holding
// only x and y).
//
// Takes the locations rather than reading the globals so the detail
// tessellation and scatter programs — which link the same fragment shader
// under their own program objects, and so have their own locations for its
// uniforms — get normal-mapped surfaces on exactly the same terms as the
// world program.
func BindNormalMap(texName string, hasLoc, samplerLoc int32) {
	if !HasNormalMap(texName) {
		gl.Uniform1i(hasLoc, 0)
		return
	}
	// 2 rather than 1 says the map is two-channel BC5 and its z must be
	// rebuilt from x and y (fragment.glsl; texture_compress.go).
	if TextureFormatOf(NormalMapName(texName)) == TextureBC5 {
		gl.Uniform1i(hasLoc, 2)
	} else {
		gl.Uniform1i(hasLoc, 1)
	}
	gl.ActiveTexture(gl.TEXTURE0 + NormalMapUnit)
	gl.BindTexture(gl.TEXTURE_2D, Textures[NormalMapName(texName)])
	gl.Uniform1i(samplerLoc, NormalMapUnit)
	gl.ActiveTexture(gl.TEXTURE0)
}

// FlatNormalRGBA is the byte encoding of a tangent-space normal pointing
// straight out of the surface — what an unpainted height field derives to.
// Exported for tests that need a valid normal map without building one.
func FlatNormalRGBA() color.RGBA { return color.RGBA{R: 128, G: 128, B: 255, A: 255} }
