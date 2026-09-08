package gfx

import (
	"fmt"
	"foundry/internal/fileutil"
	"image"
	"image/draw"
	// The PNG decoder, registered for image.Decode above. It belongs HERE, in
	// the package that decodes, rather than in whichever main happens to be
	// running: a tool that reads a level without going through internal/app —
	// the level compiler — otherwise gets "image: unknown format" from a file
	// that is a perfectly good PNG.
	_ "image/png"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-gl/gl/v4.1-core/gl"
)

// TextureUploadsEnabled gates the GPU half of LoadTexture, exactly as
// GeometryUploadsEnabled gates Geometry.Load's. True in the running engine;
// turned off by headless tooling — the level compiler's command-line entry
// point, and the tests — so that a level can be read all the way through
// without a window.
//
// The name and the decoded size are still recorded when it is off, because they
// are what a caller with no context actually wants: which textures a level
// references, and how big they are.
var TextureUploadsEnabled = true

func LoadTexture(name, file string) error {
	imgFile, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("texture %q not found on disk: %v", file, err)
	}
	img, _, err := image.Decode(imgFile)
	if err != nil {
		return err
	}

	rgba := image.NewRGBA(img.Bounds())
	if rgba.Stride != rgba.Rect.Size().X*4 {
		return fmt.Errorf("unsupported stride")
	}
	draw.Draw(rgba, rgba.Bounds(), img, image.Point{0, 0}, draw.Src)

	// Everything above is the decode, which any tool that only wants to know a
	// texture's size can do; everything below hands it to the GPU. The split
	// mirrors Geometry.Load's — see TextureUploadsEnabled.
	var texture uint32
	if TextureUploadsEnabled {
		gl.GenTextures(1, &texture)
		gl.ActiveTexture(gl.TEXTURE0)
		gl.BindTexture(gl.TEXTURE_2D, texture)
		applyWorldTextureFilter()
		gl.TexImage2D(
			gl.TEXTURE_2D,
			0,
			gl.RGBA,
			int32(rgba.Rect.Size().X),
			int32(rgba.Rect.Size().Y),
			0,
			gl.RGBA,
			gl.UNSIGNED_BYTE,
			gl.Ptr(rgba.Pix))
		generateWorldMipmaps()
	}

	Textures[name] = texture
	noteTextureSize(name, int32(rgba.Rect.Size().X), int32(rgba.Rect.Size().Y))

	if !TextureUploadsEnabled {
		// The normal map's own load would upload too, and a headless caller has
		// no use for one.
		return nil
	}

	// Every path that puts a texture on the GPU comes through here, so this is
	// the one place a texture's normal map has to be picked up — see
	// ensureNormalMapLoaded. A decode failure is worth saying out loud but not
	// worth failing the albedo over: the surface draws, just flat.
	if err := ensureNormalMapLoaded(name); err != nil {
		log.Printf("WARNING: %v", err)
	}
	return nil
}

// EnsureTextureLoaded uploads the named texture to the GPU if it isn't
// already resident. Level loading (loadRawChunk) walks every referenced
// texture and loads it up front, but editor operations that build Wall/
// Floor/Ceiling structs directly (e.g. createChunkAtCamera,
// createSectorFromPolygon) bypass that pipeline. Without this, drawing with
// an unloaded name binds texture 0 (gl.BindTexture(TEXTURE_2D, 0)), which
// samples as black — the geometry renders, but pure black, regardless of
// how much light is in the scene.
func EnsureTextureLoaded(name string) error {
	if name == "" {
		return nil
	}
	if _, ok := Textures[name]; ok {
		return nil
	}
	path, ok := TextureNames[name]
	if !ok {
		return fmt.Errorf("unknown texture %q — ensure a matching .png exists in textures/", name)
	}
	return LoadTexture(name, filepath.Join(fileutil.ExecutablePath, path))
}

// ---------- texture editor GPU sync ----------
//
// Nothing in the renderer caches a texture handle beyond the Textures map —
// every draw call does gl.BindTexture(gl.TEXTURE_2D, Textures[g.Texture]) at
// draw time. So updating a texture in place is just re-specifying the same GL
// texture object: every wall, floor, water surface and detail-
// scatter instance shows the new image on the next frame with no level walk
// and no geometry rebuild.

// pendingUpload is one queued GPU upload from the texture editor.
type pendingUpload struct {
	Name string
	Img  *image.RGBA
}

// pendingTextureUploads is drained once per frame by DrainTextureUploads,
// while the main GL context is current — see the frame-order note in
// window.go. Queuing (rather than uploading directly from the editor window)
// throttles a brush drag to one upload per frame and keeps all world-GL
// mutation in one place, even though shared GL contexts make a direct upload
// legal.
var pendingTextureUploads []pendingUpload

// QueueTextureUpload schedules img to be uploaded to the GL texture named
// name at the start of the next frame. A second call for the same name before
// the queue drains replaces the pending image rather than growing the queue.
func QueueTextureUpload(name string, img *image.RGBA) {
	for i := range pendingTextureUploads {
		if pendingTextureUploads[i].Name == name {
			pendingTextureUploads[i].Img = img
			return
		}
	}
	pendingTextureUploads = append(pendingTextureUploads, pendingUpload{Name: name, Img: img})
}

// DrainTextureUploads uploads every queued image and clears the queue. Must
// be called with the main GL context current.
func DrainTextureUploads() {
	// The two queues drain independently. An early return on an empty world
	// queue used to take the UI's with it, which starved the glyph pages in
	// every frame that happened not to edit a texture — and an atlas that
	// never reaches the GPU draws as gfx.Textures' zero value, which is the
	// default texture object: a black box per glyph.
	for _, u := range pendingTextureUploads {
		if err := UpdateTextureFromRGBA(u.Name, u.Img); err != nil {
			log.Printf("texture upload %q: %v", u.Name, err)
		}
	}
	pendingTextureUploads = pendingTextureUploads[:0]
	drainUITextureUploads()
}

// UpdateTextureFromRGBA uploads img into the GL texture object already
// registered under name, or creates one if this is a brand-new texture.
// Re-specifying the same handle (rather than allocating a new one) is what
// makes every existing instance of the texture update live — nothing else
// needs to change.
func UpdateTextureFromRGBA(name string, img *image.RGBA) error {
	w := int32(img.Rect.Dx())
	h := int32(img.Rect.Dy())
	if w <= 0 || h <= 0 {
		return errEmptyImage(name)
	}
	if img.Stride != img.Rect.Size().X*4 {
		return errBadStride(name)
	}

	tex, ok := Textures[name]
	if !ok {
		gl.GenTextures(1, &tex)
		gl.ActiveTexture(gl.TEXTURE0)
		gl.BindTexture(gl.TEXTURE_2D, tex)
		applyWorldTextureFilter()
		Textures[name] = tex
	} else {
		gl.ActiveTexture(gl.TEXTURE0)
		gl.BindTexture(gl.TEXTURE_2D, tex)
	}
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, w, h, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(img.Pix))
	// Rebuild the mip chain from the new level 0 — see generateWorldMipmaps.
	// This is on the texture editor's live-edit path (a brush drag re-uploads
	// once per frame), but it is a GPU-side downsample of one texture, which
	// is well under the cost of the flatten that produced img in the first
	// place.
	generateWorldMipmaps()
	noteTextureSize(name, w, h)
	return nil
}

// RegisterTexture makes name resolve to <exe>/textures/<name>.png for every
// consumer that reads through TextureNames — textureCombo's dropdown
// (editor_ui.go) and EnsureTextureLoaded both rebuild/resolve from this map
// on every use, so a single insert is all requirement 7 needs.
func RegisterTexture(name string) {
	TextureNames[name] = fileutil.TexturesDirName + "/" + name + ".png"
}

// TextureNames maps logical texture names to paths relative to ExecutablePath.
// Populated at startup by ScanTextures.
var TextureNames map[string]string

// ScanTextures walks dir and returns a map from logical name (filename without
// extension) to the relative path "textures/<file>".
func ScanTextures(dir string) map[string]string {
	result := make(map[string]string)
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("WARNING: could not scan textures directory %s: %v", dir, err)
		return result
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".png") {
			continue
		}
		logical := strings.TrimSuffix(name, filepath.Ext(name))
		if IsNormalMapName(logical) {
			// A normal map is reached through its albedo (ensureNormalMapLoaded),
			// never named on its own. Leaving it out of this map keeps it out of
			// every texture picker and every "unknown texture" check that reads
			// through it.
			continue
		}
		result[logical] = "textures/" + name
	}
	return result
}

// errEmptyImage and errBadStride are the two ways an RGBA upload can be
// rejected, shared by the world and UI paths so both refuse the same things in
// the same words.
func errEmptyImage(name string) error {
	return fmt.Errorf("texture upload %q: empty image", name)
}

func errBadStride(name string) error {
	return fmt.Errorf("texture upload %q: unsupported stride, want tightly packed RGBA", name)
}
