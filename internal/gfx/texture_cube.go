package gfx

// texture_cube.go — cubemap textures.
//
// The only consumer today is the skybox (render/sky.go), and cubemaps live here
// rather than beside it for the reason every other texture does: what a draw
// call binds belongs to gfx, and the pass that binds it belongs to render.
//
// Deliberately NOT part of the Textures map. A name in there is a sampler2D as
// far as every draw call in the engine is concerned — Geometry.Draw binds
// whatever it finds under g.Texture to GL_TEXTURE_2D — so a cubemap handle
// sitting in it would be bound to the wrong target the first time a wall
// happened to name it, which is invalid and silently blacks out the draw.

import (
	"fmt"
	"foundry/internal/fileutil"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// Cubemaps caches uploaded cubemaps by the key EnsureCubemapLoaded derives from
// their six face names, so re-selecting a sky in the editor costs a map lookup
// rather than six PNG decodes.
var Cubemaps = map[string]uint32{}

// CubemapKey is the cache key for a set of faces: the names joined, so changing
// any one face is a different cubemap and changing none is the same one.
func CubemapKey(faces [6]string) string { return strings.Join(faces[:], "|") }

// EnsureCubemapLoaded returns the GL cubemap for these six faces, uploading it
// on first use. Every face must name a texture ScanTextures found; a missing one
// is an error rather than a black face, because the caller has a sky it can fall
// back to and a half-loaded cubemap is worse than none.
func EnsureCubemapLoaded(faces [6]string) (uint32, error) {
	key := CubemapKey(faces)
	if tex, ok := Cubemaps[key]; ok && tex != 0 {
		return tex, nil
	}

	paths := [6]string{}
	for i, name := range faces {
		if name == "" {
			return 0, fmt.Errorf("cubemap face %d is unset", i)
		}
		rel, ok := TextureNames[name]
		if !ok {
			return 0, fmt.Errorf("cubemap face %d: unknown texture %q — ensure a matching .png exists in textures/", i, name)
		}
		paths[i] = filepath.Join(fileutil.ResourcesPath, rel)
	}

	tex, err := uploadCubemap(paths)
	if err != nil {
		return 0, err
	}
	Cubemaps[key] = tex
	return tex, nil
}

// ReleaseCubemap drops a cached cubemap and its GL object. Used when a sky's
// faces change under an author in the editor, so a session spent trying skies
// does not accumulate one texture set per attempt.
func ReleaseCubemap(faces [6]string) {
	key := CubemapKey(faces)
	tex, ok := Cubemaps[key]
	if !ok {
		return
	}
	delete(Cubemaps, key)
	if tex != 0 {
		gl.DeleteTextures(1, &tex)
	}
}

// uploadCubemap decodes the six images and uploads them as one cubemap.
//
// The rows are NOT flipped, unlike the 2D path's implicit convention: GL
// specifies cube faces in a left-handed space, as seen from the centre looking
// out, which is exactly how a skybox face is painted and exported. Flipping
// here is the classic way to get a sky that is upside down on four sides and
// mirrored on the other two.
func uploadCubemap(paths [6]string) (uint32, error) {
	var tex uint32
	gl.GenTextures(1, &tex)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, tex)

	for i, path := range paths {
		rgba, err := decodeRGBA(path)
		if err != nil {
			gl.DeleteTextures(1, &tex)
			return 0, err
		}
		gl.TexImage2D(
			uint32(gl.TEXTURE_CUBE_MAP_POSITIVE_X+uint32(i)),
			0,
			gl.RGBA,
			int32(rgba.Rect.Size().X),
			int32(rgba.Rect.Size().Y),
			0,
			gl.RGBA,
			gl.UNSIGNED_BYTE,
			gl.Ptr(rgba.Pix))
	}

	// No mipmaps: the sky is drawn at a fixed angular size per pixel, so there
	// is no minification to filter, and a mip chain over a cube face pulls the
	// edge texels of neighbouring faces together at the seams.
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	// CLAMP_TO_EDGE on all three axes is what stops the bilinear filter at a
	// face's border from wrapping round to the far side of that same face,
	// which draws as a thin bright line down each of the cube's twelve edges.
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_R, gl.CLAMP_TO_EDGE)

	gl.BindTexture(gl.TEXTURE_CUBE_MAP, 0)
	return tex, nil
}

// decodeRGBA reads an image file into a tightly packed RGBA buffer, the same
// shape LoadTexture builds and for the same reason: gl.Ptr wants one.
func decodeRGBA(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("texture %q not found on disk: %v", path, err)
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("texture %q: %v", path, err)
	}
	rgba := image.NewRGBA(img.Bounds())
	if rgba.Stride != rgba.Rect.Size().X*4 {
		return nil, fmt.Errorf("texture %q: unsupported stride", path)
	}
	draw.Draw(rgba, rgba.Bounds(), img, image.Point{0, 0}, draw.Src)
	return rgba, nil
}
