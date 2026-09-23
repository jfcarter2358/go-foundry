package devui

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/render"
	"foundry/internal/uidraw"
	"foundry/internal/uifont"

	"github.com/go-gl/gl/v4.1-core/gl"
)

// devui_gl_test.go — the console and the overlay drawn through a real context.
//
// The headless tests in draw_test.go prove the arithmetic; this proves the
// thing they cannot, which is that what comes out actually rasterises. A debug
// tool whose panel is there but whose text is not would pass every test above
// and be useless in exactly the situation it exists for.
//
// Opt-in, like every other GL test: set FOUNDRY_GL_TEST=1.

func TestTheToolsRasterise(t *testing.T) {
	gltest.Context(t)

	if err := uidraw.Init(); err != nil {
		t.Fatalf("uidraw.Init: %v", err)
	}
	t.Cleanup(uidraw.Delete)

	uifont.Reset()
	t.Cleanup(uifont.Reset)

	reset(t)
	Toggle()
	insert("spawn grenade")
	printf(kindOut, "a line of scrollback")

	was := render.DebugOverlayVisible
	render.DebugOverlayVisible = true
	t.Cleanup(func() { render.DebugOverlayVisible = was })

	// The glyph pages have to reach the GPU before they are sampled — the
	// ordering app.go's frame relies on, and the reason Toggle bakes the
	// printable set on the way in.
	uidraw.SyncFontAtlases()
	gfx.DrainTextureUploads()
	if fontTex() == 0 {
		t.Fatal("the console's glyph page never reached the GPU")
	}

	// Into an offscreen target rather than the test window, which is 64x64 —
	// smaller than the console's own minimum and far too small to say anything
	// about where the two panels land.
	const w, h = 800, 600
	fbo, rbo := offscreen(t, w, h)
	defer release(fbo, rbo)

	gl.Viewport(0, 0, w, h)
	gl.ClearColor(0, 0, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT)

	var l uidraw.List
	Render(&l, w, h)

	if l.Empty() {
		t.Fatal("the tools issued no geometry")
	}
	if err := gl.GetError(); err != gl.NO_ERROR {
		t.Fatalf("GL error 0x%x drawing the developer tools", err)
	}

	// Something was painted where the console's panel is, and something else
	// where the overlay is. Read back rather than trusted, because a list that
	// builds and a list that draws are two different claims.
	if !painted(t, w, h, int32(margin+2), int32(h-margin-4)) {
		t.Error("the console's panel did not rasterise")
	}
	if !painted(t, w, h, int32(margin+2), int32(margin+2)) {
		t.Error("the overlay's panel did not rasterise")
	}

	// And the text on them, which is the claim that matters: a panel with no
	// glyphs on it looks exactly like a working console until you try to read
	// one. The ink is near-white, so a bright pixel anywhere along the prompt's
	// row is the whole of the evidence needed.
	r := consoleRect(w, h)
	promptRow := int32(r.Y + r.H - padding - lineHeight() + lineHeight()/2)
	if !brightPixelInRow(t, w, h, promptRow, int32(r.X+padding), int32(r.X+padding+300)) {
		t.Error("the prompt and the line being typed did not rasterise")
	}
	if !brightPixelInRow(t, w, h, int32(margin+padding+lineHeight()/2), margin, margin+300) {
		t.Error("the overlay's first row of text did not rasterise")
	}

	// The negative half, so that a probe which cannot tell ink from panel does
	// not pass this test by finding everything bright: the far right of the
	// prompt row has nothing on it.
	if brightPixelInRow(t, w, h, promptRow, int32(r.X+r.W-200), int32(r.X+r.W-4)) {
		t.Error("the empty end of the prompt row read as ink — the probe proves nothing")
	}
}

// brightPixelInRow reports whether any pixel between x0 and x1 on row y is
// bright enough to be ink rather than panel.
func brightPixelInRow(t *testing.T, w, h, y, x0, x1 int32) bool {
	t.Helper()
	if x1 > w {
		x1 = w
	}
	row := make([]uint8, 4*(x1-x0))
	gl.ReadPixels(x0, h-1-y, x1-x0, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&row[0]))
	for i := 0; i < len(row); i += 4 {
		if row[i] > 150 {
			return true
		}
	}
	return false
}

// offscreen makes a framebuffer of the given size and binds it.
func offscreen(t *testing.T, w, h int32) (fbo, rbo uint32) {
	t.Helper()
	gl.GenFramebuffers(1, &fbo)
	gl.GenRenderbuffers(1, &rbo)
	gl.BindRenderbuffer(gl.RENDERBUFFER, rbo)
	gl.RenderbufferStorage(gl.RENDERBUFFER, gl.RGBA8, w, h)
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	gl.FramebufferRenderbuffer(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.RENDERBUFFER, rbo)
	if st := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); st != gl.FRAMEBUFFER_COMPLETE {
		t.Fatalf("offscreen framebuffer is incomplete: 0x%x", st)
	}
	return fbo, rbo
}

func release(fbo, rbo uint32) {
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
	gl.DeleteFramebuffers(1, &fbo)
	gl.DeleteRenderbuffers(1, &rbo)
}

// painted reports whether the pixel at x, y (top-left origin, as the tools are
// laid out) is no longer the black the framebuffer was cleared to.
func painted(t *testing.T, w, h, x, y int32) bool {
	t.Helper()
	var px [4]uint8
	// GL reads from the bottom left; the layout above measures from the top.
	gl.ReadPixels(x, h-1-y, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return px[0] != 0 || px[1] != 0 || px[2] != 0
}
