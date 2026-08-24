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
// Sample count is fixed at window-creation time (CreateAntiAliasedWindow) —
// the platform picks the backbuffer's pixel format then, and that can't
// change without recreating the window. What IS a genuine runtime toggle is
// whether the GPU resolves those samples at all: gl.Enable/Disable(
// gl.MULTISAMPLE) (ApplyAntiAliasGLState / ToggleAntiAlias) costs nothing to
// flip and doesn't touch the framebuffer, which is what lets AntiAliasEnabled
// be a live F10 toggle instead of a restart-required setting.
//
// MSAA only antialiases *geometry* edges — it multiplies coverage samples,
// not shading. Two things it therefore cannot fix are handled elsewhere and
// are the other half of the anti-aliasing story:
//
//   - Texture crawl on distant/grazing surfaces: prefiltering, not coverage.
//     See texture_filter.go (mipmaps + anisotropic filtering).
//   - Alpha-cutout edges: a discard is a per-fragment, all-or-nothing
//     decision, so a foliage silhouette stays hard-edged no matter how many
//     coverage samples the pixel has. SetAlphaToCoverage below turns the
//     shader's alpha into a per-sample coverage mask, which is what lets MSAA
//     reach those edges too; fragment.glsl's alphaSharpen path feeds it an
//     edge ramp of the right width.

import (
	"fmt"
	"log"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/veandco/go-sdl2/sdl"
)

// AntiAliasEnabled toggles MSAA resolving at runtime (F10, see input.go) —
// see ApplyAntiAliasGLState/ToggleAntiAlias. Defaults on: jagged edges are
// the more jarring default for a 3D game, and disabling costs nothing to try
// if a comparison is wanted.
var AntiAliasEnabled = true

// AntiAliasSamples is how many samples per pixel we'd like the window's own
// framebuffer to have. Requested unconditionally at window creation
// (CreateAntiAliasedWindow) regardless of AntiAliasEnabled's starting
// value — that field only ever gates gl.MULTISAMPLE afterward — so F10 has a
// multisampled buffer to resolve into from the very first frame rather than
// needing a restart the first time AA is turned on.
const AntiAliasSamples int32 = 8

// antiAliasSampleTiers is what CreateAntiAliasedWindow walks, in order, until
// a window and GL context actually come back.
//
// Asking for a pixel format the driver can't provide is not always the soft
// failure it looks like. Most drivers quietly downgrade to the nearest
// supported sample count, and then the readback in ApplyAntiAliasGLState is
// the whole story — but some (notably older Intel/Mesa paths, and
// remote/virtualized GL such as the WSLg setup this builds on) instead fail
// window or context creation outright. At 4x that risk was low enough to
// ignore; at 8x it isn't, and a hard-failed CreateWindow is the difference
// between "slightly softer edges" and "the engine doesn't start". So each
// tier is tried for real, ending at 0 — no multisampling at all — which is
// the plain single-sampled window the engine had before any of this and
// always succeeds if a GL window can be created at all.
var antiAliasSampleTiers = []int32{AntiAliasSamples, 4, 2, 0}

// AntiAliasRequestedSamples is the tier CreateAntiAliasedWindow actually got
// a window out of — what was asked for, as opposed to
// AntiAliasGrantedSamples, which is what the driver then reported giving.
// They differ when a driver accepts the request and silently downgrades.
var AntiAliasRequestedSamples int32

// AntiAliasGrantedSamples is what the driver actually granted, read back
// once a GL context exists (ApplyAntiAliasGLState) — can be less than
// AntiAliasSamples, or 0 on a driver/GPU that can't honor the request at
// all, in which case AntiAliasEnabled has nothing to toggle. Shown in the F3
// overlay so a shortfall is visible rather than silently assumed.
var AntiAliasGrantedSamples int32

// CreateAntiAliasedWindow creates the main window and its GL context with
// the highest sample count from antiAliasSampleTiers that the platform will
// actually give us, dropping a tier at a time on failure.
//
// The window and the context are created together here rather than left to
// the caller because the pixel format is only really proven when a context
// binds to it: plenty of drivers hand back a window for a multisample format
// they can't honour and then fail at GLCreateContext. Retrying that pair as a
// unit is the only way a tier is genuinely ruled out.
//
// Returns the surviving window and context, or an error if even the
// no-multisampling tier failed — at which point the problem isn't AA.
func CreateAntiAliasedWindow(title string, w, h int32, flags sdl.WindowFlags) (*sdl.Window, sdl.GLContext, error) {
	var lastErr error
	for _, samples := range antiAliasSampleTiers {
		requestAntiAliasSamples(samples)

		win, err := sdl.CreateWindow(title, sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED, w, h, flags)
		if err != nil {
			lastErr = fmt.Errorf("sdl.CreateWindow at %dx MSAA: %w", samples, err)
			log.Printf("WARNING: %v — retrying with fewer samples", lastErr)
			continue
		}

		ctx, err := win.GLCreateContext()
		if err != nil {
			lastErr = fmt.Errorf("GLCreateContext at %dx MSAA: %w", samples, err)
			log.Printf("WARNING: %v — retrying with fewer samples", lastErr)
			win.Destroy()
			continue
		}

		AntiAliasRequestedSamples = samples
		return win, ctx, nil
	}
	return nil, nil, fmt.Errorf("no usable window pixel format, including with multisampling off: %w", lastErr)
}

// requestAntiAliasSamples sets the SDL GL attributes describing the
// multisample pixel format to ask for next. Only affects windows created
// after it — once a window exists its format is fixed — so
// CreateAntiAliasedWindow calls this once per tier, immediately before the
// attempt it applies to. samples == 0 asks for no multisample buffer at all,
// which is how the last tier stops being a multisampling request rather than
// becoming a request for a 0-sample multisampled format (which is not the
// same thing, and not universally accepted).
func requestAntiAliasSamples(samples int32) {
	buffers := 1
	if samples <= 0 {
		buffers, samples = 0, 0
	}
	if err := sdl.GLSetAttribute(sdl.GL_MULTISAMPLEBUFFERS, buffers); err != nil {
		log.Printf("WARNING: sdl.GLSetAttribute(GL_MULTISAMPLEBUFFERS, %d): %v", buffers, err)
	}
	if err := sdl.GLSetAttribute(sdl.GL_MULTISAMPLESAMPLES, int(samples)); err != nil {
		log.Printf("WARNING: sdl.GLSetAttribute(GL_MULTISAMPLESAMPLES, %d): %v", samples, err)
	}
}

// ApplyAntiAliasGLState reads back how many samples the driver actually
// granted the window's framebuffer and applies AntiAliasEnabled's starting
// value. Call once, after gl.Init() (a GL context must be current).
func ApplyAntiAliasGLState() {
	var got int32
	gl.GetIntegerv(gl.SAMPLES, &got)
	AntiAliasGrantedSamples = got
	switch {
	case got == 0:
		log.Printf("WARNING: multisampling unavailable (driver granted 0 of %d requested samples) — F10 will have no effect", AntiAliasRequestedSamples)
	case got < AntiAliasSamples:
		// Either a lower tier won in CreateAntiAliasedWindow, or the driver
		// took the request and quietly downgraded it. Worth a distinct line:
		// this is the case where the F3 overlay shows fewer samples than the
		// build asks for and nothing looks broken.
		log.Printf("multisampling: %d samples granted (requested %d, preferred %d)", got, AntiAliasRequestedSamples, AntiAliasSamples)
	default:
		log.Printf("multisampling: %d samples granted (of %d requested)", got, AntiAliasRequestedSamples)
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

// SetAlphaToCoverage turns the fragment shader's output alpha into a
// per-sample coverage mask for the draws that follow.
//
// This is what extends MSAA to alpha-cutout surfaces. A cutout fragment
// either discards or writes the pixel whole, so a foliage silhouette is a
// hard staircase however many coverage samples the pixel has. With
// alpha-to-coverage the GPU instead keeps a number of the pixel's samples
// proportional to alpha, and the MSAA resolve averages them — the same
// mechanism that antialiases a triangle edge, driven by the shader instead of
// by geometry. fragment.glsl's alphaSharpen path exists to hand it an edge
// ramp roughly one pixel wide, which is the width this can actually resolve.
//
// Only meaningful with a multisampled framebuffer: on a single-sampled one
// there is exactly one sample to keep or drop, so partial coverage rounds to
// all-or-nothing and cutouts would *dither* between frames instead of
// antialiasing. Hence the same guard as gl.MULTISAMPLE, which also means F10
// turns this off along with everything else.
//
// Scoped to the opaque pass by its caller (main.go). It must not be left on
// for the sorted transparent pass, where alpha means "blend this much", not
// "cover this many samples" — the two interpretations compound, and a
// half-transparent surface would come out at a quarter strength.
func SetAlphaToCoverage(on bool) {
	if on && AntiAliasEnabled && AntiAliasGrantedSamples > 0 {
		gl.Enable(gl.SAMPLE_ALPHA_TO_COVERAGE)
	} else {
		gl.Disable(gl.SAMPLE_ALPHA_TO_COVERAGE)
	}
}
