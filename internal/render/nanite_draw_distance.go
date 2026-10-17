package render

// nanite_draw_distance.go — draw distances and the impostor tier on the
// GPU-driven cull (docs/regions/PRESETS_5.md; the windows themselves, and
// why they are what they are, are world/scenery_draw.go's).
//
// A cooked placement with a draw distance, or an impostor, is up to two
// INSTANCES of the batch: its mesh, and its impostor's cards (a second mesh
// in the same pool, nanite_scenery.go's members). Each carries its window in
// gpuInstance.Draw — from, to and the placement's hash — and three things
// read it:
//
//  1. The instance stage (nanite_instance_cull_comp.glsl, drawKeeps) drops an
//     instance outside its window before anything else looks at it: no LOD
//     walk, no tiles (the live-tile list keeps only kept instances'), no
//     commands, no records. That is the saving — a far bush costs one thread.
//  2. The visibility buffer's ID pass dithers the fade bands per pixel: the
//     vertex stage works out the instance's noise band (drawFadeWindow, which
//     is world.DrawWindow.DrawFade), the fragment stage keeps a pixel whose
//     noise, offset by the hash, is in it (nanite_vis_frag.glsl); the
//     compute rasteriser does the same per pixel it writes
//     (nanite_swraster_comp.glsl). The noise is fragment.glsl's
//     coverageNoise, which steps with TAA's jitter, so the dissolve resolves
//     into a fade; without TAA it is a fixed dither.
//  3. Everything that cannot dither takes the per-instance cut instead: a
//     camera pass drawn the forward way (DRAW_HASHED, on the GPU), and every
//     shadow pass (DRAW_SHADOW: the CPU decides, per instance per frame,
//     instShadowHidden — the answer the virtual shadow map's tracker hashes
//     too, vsm_track.go, so a page is redrawn when a caster crosses its
//     shadow distance).
//
// The CPU's look ahead of a pass (inView, for the draw bound) passes a
// superset of what the GPU keeps (world.DrawWindow.MayDraw), as it must: a
// command packed past the bound is never drawn.
//
// The classic path (a placement the GPU cull does not draw) takes the
// per-instance cut on the CPU and in scenery_cull_comp.glsl; it draws no
// impostors.
//
// FOUNDRY_DRAW_DISTANCE=0 ignores every window (and every impostor): the
// A/B switch, and the picture of a level before it had any.

import (
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// DrawDistances switches the windows; see the file comment.
var DrawDistances = gfx.EnvFlag("FOUNDRY_DRAW_DISTANCE", true)

// The instance stage's modes (nanite_instance_cull_comp.glsl).
const (
	drawOff    = 0
	drawDither = 1
	drawHashed = 2
	drawShadow = 3
)

// drawModeFor is the instance stage's mode for a pass of view v.
func drawModeFor(v cullView) int32 {
	if !DrawDistances || naniteDebugActive() {
		return drawOff
	}
	if !v.camera {
		return drawShadow
	}
	if naniteVisActive() {
		return drawDither
	}
	return drawHashed
}

// instanceDraw is an instance's Draw record for window w of a placement
// hashing to hash: zero for an open window (drawn everywhere).
func instanceDraw(w world.DrawWindow, hash float32) [4]float32 {
	if !DrawDistances || w.Open() {
		return [4]float32{}
	}
	if w.None {
		// Nothing: a window no distance is inside (from past to).
		return [4]float32{1e30, 1, hash, 0}
	}
	return [4]float32{w.From, w.To, hash, 0}
}

// drawFadeWindowGLSL is world.DrawWindow.DrawFade for the shaders: the noise
// band [lo, hi) of the pixels kept at distance d of window dr (from, to).
// The visibility buffer's vertex stage and the compute rasteriser both take
// it; they must agree with each other and with the Go.
const drawFadeWindowGLSL = `
vec2 drawFadeWindow(float d, vec4 dr) {
    float hi = dr.y > 0.0 ? clamp((dr.y - d) / (0.1 * dr.y), 0.0, 1.0) : 1.0;
    float lo = dr.x > 0.0 ? clamp((dr.x - d) / (0.1 * dr.x), 0.0, 1.0) : 0.0;
    return vec2(lo, hi);
}
`

// drawFadeKeepsGLSL is the per-pixel test: pixel p of frame (coverageDither:
// 0 for a fixed pattern) is kept in the band fade (lo, hi, hash). The noise
// is fragment.glsl's coverageNoise, and MUST stay so.
const drawFadeKeepsGLSL = `
bool drawFadeKeeps(vec2 p, int frame, vec3 fade) {
    if (fade.x <= 0.0 && fade.y >= 1.0) {
        return true;
    }
    float ign = fract(52.9829189 * fract(dot(p, vec2(0.06711056, 0.00583715))));
    float n = fract(fract(ign + float(frame) * 0.125) + fade.z);
    return n >= fade.x && n < fade.y;
}
`

// placementDraw is the Draw record and shadow verdict of a member: its mesh
// (impostor false) or its impostor's cards, at the camera eye.
func placementDraw(s *world.Scenery, impostor bool, eye mgl32.Vec3) (draw [4]float32, castsNow bool) {
	w, sw := s.MeshWindow(), s.MeshShadowWindow()
	if impostor {
		w, sw = s.ImpostorWindow(), s.ImpostorShadowWindow()
	}
	h := s.DrawHash()
	draw = instanceDraw(w, h)
	castsNow = !DrawDistances || sw.Open() || sw.Drawn(s.DrawDistanceFrom(eye), h)
	return draw, castsNow
}

// placementMayDraw is the CPU's loose test (world.DrawWindow.MayDraw) of a
// member for a camera pass at eye.
func placementMayDraw(s *world.Scenery, impostor bool, eye mgl32.Vec3) bool {
	if !DrawDistances {
		return !impostor
	}
	w := s.MeshWindow()
	if impostor {
		w = s.ImpostorWindow()
	}
	if w.Open() {
		return true
	}
	// A little slack each way against the GPU's float distance.
	d := s.DrawDistanceFrom(eye)
	if w.None || (w.From > 0 && d*1.001+0.05 < w.From*(1-world.DrawFadeBand)) {
		return false
	}
	return w.To <= 0 || d*0.999-0.05 < w.To
}
