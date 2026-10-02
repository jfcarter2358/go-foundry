package editor

// editor_claude_shot.go — letting Claude look at the viewport.
//
// This is the tool that changes what the panel is for. Without it Claude is
// working blind on a 3D editor: it can write a level file that is correct in
// every field and have no idea that the light it placed is inside the wall. With
// it, "add a lamp above the door" can be followed by looking at the result.
//
// The capture has to happen at one exact moment in the frame — after everything
// has been drawn and before the buffers are swapped, because after the swap the
// back buffer's contents are undefined. That is the same constraint
// benchCaptureShot works under, and the same readback. So a tool call cannot
// simply take a screenshot: it raises a request, the frame loop fills it at the
// right instant, and the tool's goroutine waits.

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"sync"
	"time"

	"foundry/internal/claude"
	"foundry/internal/glext"
	"foundry/internal/platform"
	"foundry/internal/render"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// claudeShotTimeout is how long a tool waits for a frame to be drawn. The editor
// draws continuously, so this only expires when it is blocked or minimised.
const claudeShotTimeout = 10 * time.Second

// claudeShotMaxWidth bounds what is sent. A 4K viewport is several megabytes of
// PNG and costs a great many tokens to look at; a half-scale image answers
// "is the lamp inside the wall" just as well.
const claudeShotMaxWidth = 1600

var claudeShot struct {
	mu      sync.Mutex
	waiting chan []byte
}

// claudeRequestShot asks the frame loop for the next frame's pixels. Called from
// a tool's goroutine.
func claudeRequestShot() ([]byte, error) {
	ch := make(chan []byte, 1)
	claudeShot.mu.Lock()
	if claudeShot.waiting != nil {
		claudeShot.mu.Unlock()
		return nil, errors.New("a screenshot is already being taken")
	}
	claudeShot.waiting = ch
	claudeShot.mu.Unlock()

	select {
	case png := <-ch:
		if len(png) == 0 {
			return nil, errors.New("the viewport could not be read")
		}
		return png, nil
	case <-time.After(claudeShotTimeout):
		claudeShot.mu.Lock()
		claudeShot.waiting = nil
		claudeShot.mu.Unlock()
		return nil, errors.New("the editor did not draw a frame in time")
	}
}

// ClaudeCaptureIfRequested fills a pending screenshot request. Called from the
// frame loop with the main GL context current, after everything has been drawn
// and before the swap.
func ClaudeCaptureIfRequested() {
	claudeShot.mu.Lock()
	ch := claudeShot.waiting
	claudeShot.waiting = nil
	claudeShot.mu.Unlock()
	if ch == nil {
		return
	}
	ch <- claudeCaptureBackBuffer()
}

// claudeCaptureBackBuffer reads the window and encodes it. Returns nil on any
// failure; the caller turns that into a message for Claude.
func claudeCaptureBackBuffer() []byte {
	w, h := int(platform.WinWidth), int(platform.WinHeight)
	if w <= 0 || h <= 0 {
		return nil
	}
	px := make([]uint8, w*h*4)
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, 0)
	gl.ReadBuffer(gl.BACK)
	gl.PixelStorei(gl.PACK_ALIGNMENT, 1)
	glext.ReadPixels(0, 0, int32(w), int32(h), gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(px))

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		// GL's rows run bottom-up.
		src := px[(h-1-y)*w*4 : (h-y)*w*4]
		dst := img.Pix[y*img.Stride : y*img.Stride+w*4]
		copy(dst, src)
		for x := 3; x < len(dst); x += 4 {
			dst[x] = 255
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, claudeDownscale(img, claudeShotMaxWidth)); err != nil {
		return nil
	}
	return buf.Bytes()
}

// claudeDownscale halves the image until it fits, by point sampling. Nothing
// here is trying to be pretty: it is a picture to be looked at once, and a
// box filter would cost more than it is worth on a 4K readback every time.
func claudeDownscale(src *image.NRGBA, maxW int) image.Image {
	step := 1
	for src.Rect.Dx()/step > maxW {
		step *= 2
	}
	if step == 1 {
		return src
	}
	w, h := src.Rect.Dx()/step, src.Rect.Dy()/step
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			si := (y*step)*src.Stride + (x*step)*4
			di := y*dst.Stride + x*4
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

// claudeViewportShot is the viewport tool: the next frame's pixels, drawn in
// a debug view if one is named. The view is switched for that one frame and
// put back after, whatever happened in between — a tool call that left the
// author's editor in lighting-only would be a change nobody asked for.
func claudeViewportShot(view string) (claude.ToolResult, error) {
	var note string
	if view != "" {
		type set struct {
			prev, note string
			err        error
		}
		r, err := onMainThread(func() set {
			prev := render.ViewName()
			n, err := render.SetView(view)
			return set{prev, n, err}
		})
		if err != nil {
			return claude.ToolResult{}, err
		}
		if r.err != nil {
			return claude.ToolResult{}, r.err
		}
		note = r.note
		defer onMainThread(func() bool { _, _ = render.SetView(r.prev); return true })
	}
	png, err := claudeRequestShot()
	if err != nil {
		return claude.ToolResult{}, err
	}
	return claude.ToolResult{Text: note, Image: png, ImageMIME: "image/png"}, nil
}

// anySlice is a []string as the []any a JSON schema's enum is written in.
func anySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
