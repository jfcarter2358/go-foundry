package render

// frame_target.go — the frame's framebuffer, viewport and GL state, known
// rather than asked for.
//
// Asking GL what is bound (glGetIntegerv, glIsEnabled, glGetError) is not a
// read of a variable. A driver that runs a worker thread of its own (NVIDIA's,
// by default) has to finish every command queued ahead of the question before
// it can answer, and the first question after a swap waits out the whole of
// the GPU's previous frame. The sun's cascade pass asked twice at its top, so
// on a GPU-bound frame "shadows: sun" in the F3 profile was really the time
// the GPU spent on live GI — and none of the CPU's work after it could overlap
// the GPU's.
//
// So DrawWorld's caller says where the frame goes (WorldDraw.Target), and the
// passes inside it read that and frameState instead of asking. Outside
// DrawWorld — the editor's preview windows, a GL test drawing one pass —
// nothing is declared and they ask, as they always did.

import (
	"fmt"
	"path/filepath"
	"runtime"

	"foundry/internal/gfx"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// FrameTarget is the framebuffer and viewport a frame draws into.
type FrameTarget struct {
	FBO      uint32
	Viewport [4]int32
}

// drawTarget is the declared target of the DrawWorld running now; set false
// outside one, and inside one whose caller declared nothing.
var drawTarget struct {
	set bool
	t   FrameTarget
}

// beginFrameTarget declares t for the frame about to be drawn and puts GL in
// the state the passes hand each other (frameState), so that state is a fact
// rather than a habit: whatever ran between two frames — the HUD, ImGui, an
// editor window — the first pass starts from it. Setting state costs nothing
// the way asking for it does.
func beginFrameTarget(t *FrameTarget) {
	if t == nil {
		drawTarget.set = false
		return
	}
	drawTarget.set, drawTarget.t = true, *t
	gl.BindFramebuffer(gl.FRAMEBUFFER, t.FBO)
	gl.Viewport(t.Viewport[0], t.Viewport[1], t.Viewport[2], t.Viewport[3])
	frameState().restore(t.FBO, t.Viewport)
}

// endFrameTarget closes the declared frame — checking, under
// CheckFrameState, that the last pass handed the convention back too.
func endFrameTarget() {
	if drawTarget.set {
		checkFrameState("end of frame")
	}
	drawTarget.set = false
}

// boundFramebuffer is the framebuffer being drawn into right now.
//
// It exists because every pass here draws into a depth map of its own and then
// has to put the frame's own target back — and "the frame's own target" is not
// always the window. A headless renderer (`foundry render`, a play session's
// screenshot, a GL test) draws into an FBO, and a pass that restored 0 instead
// sent the entire world pass after it to the window nobody is looking at,
// leaving the caller's target exactly as empty as it started.
func boundFramebuffer() uint32 {
	if drawTarget.set {
		checkFrameState("framebuffer")
		return drawTarget.t.FBO
	}
	var fb int32
	gl.GetIntegerv(gl.DRAW_FRAMEBUFFER_BINDING, &fb)
	return uint32(fb)
}

// boundViewport is the viewport being drawn into right now.
func boundViewport() [4]int32 {
	if drawTarget.set {
		return drawTarget.t.Viewport
	}
	var vp [4]int32
	gl.GetIntegerv(gl.VIEWPORT, &vp[0])
	return vp
}

// alphaToCoverageOn is what SetAlphaToCoverage last left GL_SAMPLE_ALPHA_TO_COVERAGE
// at, so a pass can tell its shader without asking GL.
var alphaToCoverageOn bool

// frameState is the GL state DrawWorld's passes hand one another: depth test
// and depth writes on, blending, culling and depth clamping off, the world
// program bound. A pass that changes any of it puts it back, and the opaque
// block's alpha-to-coverage is tracked by SetAlphaToCoverage.
func frameState() volumeGLState {
	return volumeGLState{
		depthTest: true, depthMask: true,
		a2c:         alphaToCoverageOn,
		cullMode:    gl.BACK,
		blendSrcRGB: gl.SRC_ALPHA, blendDstRGB: gl.ONE_MINUS_SRC_ALPHA,
		blendSrcA: gl.SRC_ALPHA, blendDstA: gl.ONE_MINUS_SRC_ALPHA,
		program: int32(gfx.Program),
	}
}

// currentGLState is the state a pass changes and must hand back: frameState
// inside a declared frame, GL's own answer outside one.
func currentGLState() volumeGLState {
	if drawTarget.set {
		checkFrameState("state")
		return frameState()
	}
	return saveVolumeGLState()
}

// CheckFrameState makes every pass that trusts frameState and the declared
// target also ask GL, and note where the two disagree — for tests, since the
// asking is exactly the cost this file exists to avoid.
var CheckFrameState bool

var frameStateMismatches []string

// FrameStateMismatches returns, and forgets, what CheckFrameState found.
func FrameStateMismatches() []string {
	m := frameStateMismatches
	frameStateMismatches = nil
	return m
}

func checkFrameState(site string) {
	if !CheckFrameState {
		return
	}
	note := func(what string, want, got any) {
		if want != got {
			frameStateMismatches = append(frameStateMismatches,
				fmt.Sprintf("%s: %s is %v, frameState says %v (%s)", site, what, got, want, callerChain()))
		}
	}
	var fb, rb int32
	gl.GetIntegerv(gl.DRAW_FRAMEBUFFER_BINDING, &fb)
	gl.GetIntegerv(gl.READ_FRAMEBUFFER_BINDING, &rb)
	note("draw framebuffer", int32(drawTarget.t.FBO), fb)
	note("read framebuffer", int32(drawTarget.t.FBO), rb)
	var vp [4]int32
	gl.GetIntegerv(gl.VIEWPORT, &vp[0])
	note("viewport", drawTarget.t.Viewport, vp)

	want, got := frameState(), saveVolumeGLState()
	note("depth test", want.depthTest, got.depthTest)
	note("blend", want.blend, got.blend)
	note("cull", want.cull, got.cull)
	note("depth clamp", want.depthClamp, got.depthClamp)
	note("alpha to coverage", want.a2c, got.a2c)
	note("depth mask", want.depthMask, got.depthMask)
	// The program, VAO, cull face and blend function are put back rather
	// than relied on: every pass binds its own before it draws.
}

// alphaToCoverage is whether GL_SAMPLE_ALPHA_TO_COVERAGE is on, for a shader
// that has to know.
func alphaToCoverage() bool {
	if drawTarget.set {
		return alphaToCoverageOn
	}
	return gl.IsEnabled(gl.SAMPLE_ALPHA_TO_COVERAGE)
}

// callerChain is where a check was made from, a few frames up: the site
// name says which question, this says which pass asked it.
func callerChain() string {
	out := ""
	for i := 2; i < 7; i++ {
		_, f, l, ok := runtime.Caller(i)
		if ok {
			out += fmt.Sprintf("<- %s:%d ", filepath.Base(f), l)
		}
	}
	return out
}
