package gfx

// known_gl_state.go — the GL state the screen-space passes find after the
// world, said rather than asked for.
//
// The HUD (uidraw), ImGui and the developer overlay each put back every piece
// of GL state they change, and used to learn what to put back by asking GL.
// Asking is not free: a driver with a worker thread of its own (NVIDIA's) has
// to finish everything queued before it can answer, so the first question
// after a swap waits for the GPU to finish the last frame — and once the world
// pass stopped asking (render/frame_target.go), the HUD's question was where
// that wait landed. The frame loop knows the state the world leaves, so it
// says so here for the passes drawn after it, and they restore to that.

import (
	"fmt"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// GLState is the GL state a screen-space pass changes and hands back.
type GLState struct {
	Program, Texture2D, VAO, ArrayBuffer, ElementBuffer uint32
	Viewport, Scissor                                   [4]int32
	Blend, Cull, DepthTest, ScissorTest, DepthMask      bool
}

// KnownGLState is the state the passes drawing now will find, as the frame
// loop declared it; nil when it declared nothing, and then they ask.
var KnownGLState *GLState

// AfterWorldGLState is the state render.DrawWorld leaves the frame in, on a
// target of the given viewport: the world's depth-tested, unblended,
// unculled convention with the world program bound and nothing else.
func AfterWorldGLState(viewport [4]int32) *GLState {
	return &GLState{
		Program: Program, Viewport: viewport, Scissor: viewport,
		DepthTest: true, DepthMask: true,
	}
}

// CurrentGLState is KnownGLState when there is one, and GL's own answer when
// there is not.
func CurrentGLState() GLState {
	if KnownGLState != nil {
		checkKnownGLState()
		return *KnownGLState
	}
	return QueryGLState()
}

// QueryGLState asks GL — the slow way, for when nothing is known.
func QueryGLState() GLState {
	var s GLState
	var v int32
	gl.GetIntegerv(gl.CURRENT_PROGRAM, &v)
	s.Program = uint32(v)
	gl.GetIntegerv(gl.TEXTURE_BINDING_2D, &v)
	s.Texture2D = uint32(v)
	gl.GetIntegerv(gl.VERTEX_ARRAY_BINDING, &v)
	s.VAO = uint32(v)
	gl.GetIntegerv(gl.ARRAY_BUFFER_BINDING, &v)
	s.ArrayBuffer = uint32(v)
	gl.GetIntegerv(gl.ELEMENT_ARRAY_BUFFER_BINDING, &v)
	s.ElementBuffer = uint32(v)
	gl.GetIntegerv(gl.VIEWPORT, &s.Viewport[0])
	gl.GetIntegerv(gl.SCISSOR_BOX, &s.Scissor[0])
	s.Blend = gl.IsEnabled(gl.BLEND)
	s.Cull = gl.IsEnabled(gl.CULL_FACE)
	s.DepthTest = gl.IsEnabled(gl.DEPTH_TEST)
	s.ScissorTest = gl.IsEnabled(gl.SCISSOR_TEST)
	gl.GetBooleanv(gl.DEPTH_WRITEMASK, &s.DepthMask)
	return s
}

// Restore puts s back. The VAO goes back before the buffers, so the element
// buffer binding (which a VAO owns) lands on the VAO it belongs to.
func (s GLState) Restore() {
	gl.UseProgram(s.Program)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, s.Texture2D)
	gl.BindVertexArray(s.VAO)
	gl.BindBuffer(gl.ARRAY_BUFFER, s.ArrayBuffer)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, s.ElementBuffer)
	gl.Scissor(s.Scissor[0], s.Scissor[1], s.Scissor[2], s.Scissor[3])
	gl.Viewport(s.Viewport[0], s.Viewport[1], s.Viewport[2], s.Viewport[3])
	setCap(gl.BLEND, s.Blend)
	setCap(gl.CULL_FACE, s.Cull)
	setCap(gl.DEPTH_TEST, s.DepthTest)
	setCap(gl.SCISSOR_TEST, s.ScissorTest)
	gl.DepthMask(s.DepthMask)
}

func setCap(c uint32, on bool) {
	if on {
		gl.Enable(c)
	} else {
		gl.Disable(c)
	}
}

// CheckKnownGLState makes CurrentGLState also ask GL and note where the
// declared state is wrong — for tests, since asking is the cost this exists
// to avoid.
var CheckKnownGLState bool

var knownGLStateMismatches []string

// KnownGLStateMismatches returns, and forgets, what CheckKnownGLState found.
func KnownGLStateMismatches() []string {
	m := knownGLStateMismatches
	knownGLStateMismatches = nil
	return m
}

func checkKnownGLState() {
	if !CheckKnownGLState {
		return
	}
	want, got := *KnownGLState, QueryGLState()
	note := func(what string, w, g any) {
		if w != g {
			knownGLStateMismatches = append(knownGLStateMismatches,
				fmt.Sprintf("%s is %v, KnownGLState says %v", what, g, w))
		}
	}
	// What a pass restores and nothing reads before binding its own —
	// program, texture, VAO, buffers, scissor box — is put back rather than
	// relied on, so only what a pass DOES rely on is compared.
	note("viewport", want.Viewport, got.Viewport)
	note("blend", want.Blend, got.Blend)
	note("cull", want.Cull, got.Cull)
	note("depth test", want.DepthTest, got.DepthTest)
	note("scissor test", want.ScissorTest, got.ScissorTest)
	note("depth mask", want.DepthMask, got.DepthMask)
}
