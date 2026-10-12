package gfx

// texture_ui.go — what the screen-space UI pass needs from the texture
// registry that the world pass does not.
//
// Two things, and they are here rather than in internal/uidraw because they are
// both about the registry rather than about the UI: the size of a texture, and
// an upload that does not build a mip chain.

import (
	"image"
	"log"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// TextureSizes is the pixel size of every texture that has been uploaded,
// keyed like Textures.
//
// The UI needs it for nine-slicing, where the insets an author types are
// measured in texels of the source image — which is the unit they can see in an
// image editor. Converting those to normalised UVs needs the size, and asking
// GL for it means a round trip and a bind per draw.
var TextureSizes = map[string][2]float32{}

// TextureSize is the pixel size of a texture, and whether it is known at all.
func TextureSize(name string) (w, h float32, ok bool) {
	s, ok := TextureSizes[name]
	return s[0], s[1], ok
}

// noteTextureSize records a size at upload. Called from every path that
// specifies a level 0, so nothing can add a texture without its size.
func noteTextureSize(name string, w, h int32) {
	TextureSizes[name] = [2]float32{float32(w), float32(h)}
}

// ReleaseTexture deletes a texture object and forgets its name and size.
//
// Nothing that comes off disk needs this — a texture loaded from textures/ is
// resident for the life of the process, which is why nothing else in here
// deletes. A video's frame buffer is the exception: it is created when a screen
// showing that widget goes up and is dead the moment the widget does, and a
// process that opened a dozen cutscenes over an evening would otherwise be
// holding a dozen full-resolution textures nothing can ever draw again.
//
// Must be called with a GL context current. Texture objects are shared between
// this engine's contexts, so any of them will do.
func ReleaseTexture(name string) {
	tex, ok := Textures[name]
	if !ok {
		return
	}
	gl.DeleteTextures(1, &tex)
	delete(Textures, name)
	delete(TextureSizes, name)
	forgetTextureMean(name)
	forgetTextureVRAM(name)
	for i := range pendingUITextureUploads {
		if pendingUITextureUploads[i].Name == name {
			// A queued upload would otherwise recreate the texture on the next
			// drain, under a name nothing is looking for any more.
			pendingUITextureUploads = append(pendingUITextureUploads[:i], pendingUITextureUploads[i+1:]...)
			break
		}
	}
}

// pendingUITextureUploads is the UI's own queue, drained by the same
// DrainTextureUploads the world's is. Separate from that queue only because the
// filter state differs; see QueueUITextureUpload.
var pendingUITextureUploads []pendingUpload

// QueueUITextureUpload schedules a UI image — a glyph atlas page — for upload
// with linear filtering, no mipmaps and clamped wrapping.
//
// It cannot go through QueueTextureUpload, because that path applies the
// world's filter: a mip chain over a glyph atlas averages neighbouring glyphs
// together, so small text on a distant mip level renders as grey mush with
// pieces of the next letter in it. REPEAT wrapping has the same problem at the
// page edges.
//
// Drained by DrainTextureUploads with the main GL context current, like the
// world's queue.
func QueueUITextureUpload(name string, img *image.RGBA) {
	for i := range pendingUITextureUploads {
		if pendingUITextureUploads[i].Name == name {
			pendingUITextureUploads[i].Img = img
			return
		}
	}
	pendingUITextureUploads = append(pendingUITextureUploads, pendingUpload{Name: name, Img: img})
}

// drainUITextureUploads uploads every queued UI image and clears the queue.
func drainUITextureUploads() {
	if len(pendingUITextureUploads) == 0 {
		return
	}
	for _, u := range pendingUITextureUploads {
		if err := uploadUITexture(u.Name, u.Img); err != nil {
			log.Printf("ui texture upload %q: %v", u.Name, err)
		}
	}
	pendingUITextureUploads = pendingUITextureUploads[:0]
}

func uploadUITexture(name string, img *image.RGBA) error {
	w, h := int32(img.Rect.Dx()), int32(img.Rect.Dy())
	if w <= 0 || h <= 0 {
		return errEmptyImage(name)
	}
	if img.Stride != img.Rect.Size().X*4 {
		return errBadStride(name)
	}

	tex, ok := Textures[name]
	if !ok {
		gl.GenTextures(1, &tex)
		Textures[name] = tex
	}
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, w, h, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(img.Pix))
	noteTextureSize(name, w, h)
	return nil
}
