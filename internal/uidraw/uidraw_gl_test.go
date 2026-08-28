package uidraw

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/ui"
	"foundry/internal/uifont"

	"github.com/go-gl/gl/v4.1-core/gl"
)

// TestProgramLinksAndDraws is the smoke test go build cannot be: the shader
// pair is compiled at first use, so a typo in it would otherwise surface as a
// blank screen rather than as a build failure.
func TestProgramLinksAndDraws(t *testing.T) {
	gltest.Context(t)

	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(Delete)

	if White() == 0 {
		t.Fatal("the white pixel was not created")
	}

	uifont.Reset()
	t.Cleanup(uifont.Reset)

	s := ui.NewScreen("smoke")
	panel := pinned("panel", ui.KindPanel, 10, 10, 200, 100)
	panel.Clip = true
	label := pinned("label", ui.KindText, 20, 20, 180, 40)
	label.Text.Value = "Hello"
	bar := pinned("bar", ui.KindBar, 20, 70, 180, 20)
	bar.Value.Value = 0.5
	panel.Children = []ui.Node{label, bar}
	s.Root.Children = []ui.Node{panel}

	// The glyph page has to reach the GPU before it is sampled. This is exactly
	// the ordering app.go's frame relies on.
	uifont.Face("", 22)
	SyncFontAtlases()
	gfx.DrainTextureUploads()

	var l List
	RenderScreen(&l, s, nil, 640, 480)

	if l.Empty() {
		t.Fatal("nothing was built")
	}
	assertNoGLError(t, "the ui pass")
}

// TestRenderRestoresGLState is what stops the UI pass from breaking the world
// pass that draws before it on the next frame. Every one of these was set
// deliberately by something else and has to come back the way it was found.
func TestRenderRestoresGLState(t *testing.T) {
	gltest.Context(t)
	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(Delete)

	// Put the context into a state that is not the pass's own, so that a
	// forgotten restore shows up rather than happening to match the default.
	gl.Enable(gl.DEPTH_TEST)
	gl.Enable(gl.CULL_FACE)
	gl.Disable(gl.BLEND)
	gl.Disable(gl.SCISSOR_TEST)
	gl.DepthMask(true)
	gl.Viewport(0, 0, 64, 64)

	s := ui.NewScreen("smoke")
	s.Root.Children = []ui.Node{pinned("p", ui.KindPanel, 0, 0, 100, 100)}

	before := captureGLState()
	var l List
	RenderScreen(&l, s, nil, 640, 480)
	after := captureGLState()

	if before != after {
		t.Errorf("the ui pass leaked GL state:\nbefore %+v\n after %+v", before, after)
	}
	assertNoGLError(t, "the state restore")
}

// glState is the set of things Render touches and promises to put back.
type glState struct {
	Program, Texture, VAO, ArrayBuf, ElemBuf int32
	Scissor, Viewport                        [4]int32
	Cull, Blend, Depth, ScissorTest          bool
	DepthMask                                bool
}

func captureGLState() glState {
	var s glState
	gl.GetIntegerv(gl.CURRENT_PROGRAM, &s.Program)
	gl.GetIntegerv(gl.TEXTURE_BINDING_2D, &s.Texture)
	gl.GetIntegerv(gl.VERTEX_ARRAY_BINDING, &s.VAO)
	gl.GetIntegerv(gl.ARRAY_BUFFER_BINDING, &s.ArrayBuf)
	gl.GetIntegerv(gl.ELEMENT_ARRAY_BUFFER_BINDING, &s.ElemBuf)
	gl.GetIntegerv(gl.SCISSOR_BOX, &s.Scissor[0])
	gl.GetIntegerv(gl.VIEWPORT, &s.Viewport[0])
	s.Cull = gl.IsEnabled(gl.CULL_FACE)
	s.Blend = gl.IsEnabled(gl.BLEND)
	s.Depth = gl.IsEnabled(gl.DEPTH_TEST)
	s.ScissorTest = gl.IsEnabled(gl.SCISSOR_TEST)
	gl.GetBooleanv(gl.DEPTH_WRITEMASK, &s.DepthMask)
	return s
}

func assertNoGLError(t *testing.T, what string) {
	t.Helper()
	if err := gl.GetError(); err != gl.NO_ERROR {
		t.Errorf("%s raised GL error 0x%04x", what, err)
	}
}
