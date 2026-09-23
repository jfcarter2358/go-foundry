package glctx

// target.go — an offscreen colour+depth target and the readback off it.
//
// Every headless render in the engine's history has written this same twenty
// lines: a framebuffer, a colour texture, a depth attachment, a draw, a
// glReadPixels and a flip. Here once, with the flip in it — GL reads rows from
// the bottom and every image format writes them from the top, and a picture that
// comes out upside down is the one mistake nobody notices in a thumbnail.

import (
	"fmt"
	"image"
	"image/png"
	"os"

	"github.com/go-gl/gl/v4.1-core/gl"
)

// Target is somewhere to draw that is not a window.
type Target struct {
	FBO           uint32
	Color         uint32
	Depth         uint32
	Width, Height int32
}

// NewTarget makes a target of this size. A context must be current.
func NewTarget(w, h int32) (*Target, error) {
	t := &Target{Width: w, Height: h}
	gl.GenFramebuffers(1, &t.FBO)
	gl.BindFramebuffer(gl.FRAMEBUFFER, t.FBO)

	gl.GenTextures(1, &t.Color)
	gl.BindTexture(gl.TEXTURE_2D, t.Color)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, w, h, 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, t.Color, 0)

	// A depth buffer, because the passes that draw the world expect one: without
	// it the depth test silently discards nothing and the picture is whatever
	// was drawn last rather than what is nearest.
	gl.GenRenderbuffers(1, &t.Depth)
	gl.BindRenderbuffer(gl.RENDERBUFFER, t.Depth)
	gl.RenderbufferStorage(gl.RENDERBUFFER, gl.DEPTH24_STENCIL8, w, h)
	gl.FramebufferRenderbuffer(gl.FRAMEBUFFER, gl.DEPTH_STENCIL_ATTACHMENT, gl.RENDERBUFFER, t.Depth)

	if s := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); s != gl.FRAMEBUFFER_COMPLETE {
		gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
		return nil, fmt.Errorf("offscreen target incomplete (0x%x)", s)
	}
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	return t, nil
}

// Bind makes the target current and sets the viewport to it.
func (t *Target) Bind() {
	gl.BindFramebuffer(gl.FRAMEBUFFER, t.FBO)
	gl.Viewport(0, 0, t.Width, t.Height)
}

// Unbind goes back to the window (or to nothing, headless).
func (t *Target) Unbind() { gl.BindFramebuffer(gl.FRAMEBUFFER, 0) }

// Image reads the target back, top row first.
func (t *Target) Image() *image.NRGBA {
	gl.BindFramebuffer(gl.FRAMEBUFFER, t.FBO)
	gl.PixelStorei(gl.PACK_ALIGNMENT, 1)
	buf := make([]byte, int(t.Width)*int(t.Height)*4)
	gl.ReadPixels(0, 0, t.Width, t.Height, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(buf))

	img := image.NewNRGBA(image.Rect(0, 0, int(t.Width), int(t.Height)))
	stride := int(t.Width) * 4
	for y := 0; y < int(t.Height); y++ {
		// GL's first row is the bottom one.
		src := (int(t.Height) - 1 - y) * stride
		copy(img.Pix[y*img.Stride:y*img.Stride+stride], buf[src:src+stride])
	}
	return img
}

// Delete frees the target's GL objects.
func (t *Target) Delete() {
	gl.DeleteFramebuffers(1, &t.FBO)
	gl.DeleteTextures(1, &t.Color)
	gl.DeleteRenderbuffers(1, &t.Depth)
}

// SavePNG writes an image to a file.
func SavePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
