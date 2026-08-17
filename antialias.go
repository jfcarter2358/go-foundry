package main

// antialias.go — window-level multisample anti-aliasing (MSAA).
//
// The renderer has no offscreen main-pass FBO/post-process pipeline to hook
// a resolve step into (shadow maps and the biome height/ground textures are
// separate, unrelated FBOs) — everything draws straight to the window's own
// backbuffer, editor views and gameplay alike. MSAA at the window level is
// the natural fit: request a multisampled framebuffer from SDL/GLX/WGL when
// the window is created, and every pass that draws to the default
// framebuffer (the main scene, the biome passes, gizmos, the 2D editor,
// ImGui) gets antialiased for free, with no changes to any of them.
//
// Sample count is fixed at window-creation time (RequestAntiAliasWindow,
// called before sdl.CreateWindow) — the platform picks the backbuffer's
// pixel format then, and that can't change without recreating the window.
// What IS a genuine runtime toggle is whether the GPU resolves those samples
// at all: gl.Enable/Disable(gl.MULTISAMPLE) (ApplyAntiAliasGLState /
// ToggleAntiAlias) costs nothing to flip and doesn't touch the framebuffer,
// which is what lets AntiAliasEnabled be a live F10 toggle instead of a
// restart-required setting.

import (
	"log"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/veandco/go-sdl2/sdl"
)

// AntiAliasEnabled toggles MSAA resolving at runtime (F10, see input.go) —
// see ApplyAntiAliasGLState/ToggleAntiAlias. Defaults on: jagged edges are
// the more jarring default for a 3D game, and disabling costs nothing to try
// if a comparison is wanted.
var AntiAliasEnabled = true

// AntiAliasSamples is how many samples per pixel are requested for the
// window's own framebuffer. Requested unconditionally at window creation
// (RequestAntiAliasWindow) regardless of AntiAliasEnabled's starting
// value — that field only ever gates gl.MULTISAMPLE afterward — so F10 has a
// multisampled buffer to resolve into from the very first frame rather than
// needing a restart the first time AA is turned on.
const AntiAliasSamples = 4

// AntiAliasGrantedSamples is what the driver actually granted, read back
// once a GL context exists (ApplyAntiAliasGLState) — can be less than
// AntiAliasSamples, or 0 on a driver/GPU that can't honor the request at
// all, in which case AntiAliasEnabled has nothing to toggle. Shown in the F3
// overlay so a shortfall is visible rather than silently assumed.
var AntiAliasGrantedSamples int32

// RequestAntiAliasWindow sets the SDL GL attributes that request a
// multisampled window framebuffer. MUST be called before sdl.CreateWindow —
// once the window exists the pixel format is fixed.
func RequestAntiAliasWindow() {
	if err := sdl.GLSetAttribute(sdl.GL_MULTISAMPLEBUFFERS, 1); err != nil {
		log.Printf("WARNING: sdl.GLSetAttribute(GL_MULTISAMPLEBUFFERS): %v", err)
	}
	if err := sdl.GLSetAttribute(sdl.GL_MULTISAMPLESAMPLES, AntiAliasSamples); err != nil {
		log.Printf("WARNING: sdl.GLSetAttribute(GL_MULTISAMPLESAMPLES): %v", err)
	}
}

// ApplyAntiAliasGLState reads back how many samples the driver actually
// granted the window's framebuffer and applies AntiAliasEnabled's starting
// value. Call once, after gl.Init() (a GL context must be current).
func ApplyAntiAliasGLState() {
	var got int32
	gl.GetIntegerv(gl.SAMPLES, &got)
	AntiAliasGrantedSamples = got
	if got == 0 {
		log.Printf("WARNING: multisampling unavailable (driver granted 0 of %d requested samples) — F10 will have no effect", AntiAliasSamples)
	} else {
		log.Printf("multisampling: %d samples granted (of %d requested)", got, AntiAliasSamples)
	}
	setMultisampleGLState()
}

// ToggleAntiAlias flips AntiAliasEnabled and applies it immediately (F10).
func ToggleAntiAlias() {
	AntiAliasEnabled = !AntiAliasEnabled
	setMultisampleGLState()
}

// setMultisampleGLState applies AntiAliasEnabled to the GL context.
// Disabling GL_MULTISAMPLE doesn't shrink the window's framebuffer back down
// (fixed at the pixel format RequestAntiAliasWindow asked for) — it just
// stops the rasterizer computing per-sample coverage, so every sample in a
// covered pixel gets the same value and edges alias again. That's what makes
// this a real zero-cost runtime toggle instead of something that needs the
// window recreated.
func setMultisampleGLState() {
	if AntiAliasEnabled && AntiAliasGrantedSamples > 0 {
		gl.Enable(gl.MULTISAMPLE)
	} else {
		gl.Disable(gl.MULTISAMPLE)
	}
}
