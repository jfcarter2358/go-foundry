package render

// nanite_lod.go — the camera's level-of-detail parameters for the GPU-driven
// cull's LOD selection (nanite_cull.go, "Level of detail"). Phase 2 of the
// Nanite plan (docs/nanite/PHASE_2.md), component 5.
//
// A cluster is fine enough when its error, seen from the camera, covers no
// more than gfx.NaniteLODError pixels: error·H / (2·tan(fovy/2)·d) <= τ, with
// H the frame's height in pixels and d the distance to the nearest point of
// its LOD sphere. The shaders test it as error·k <= d (cluster.LODWithin),
// with everything but the distance folded into
//
//	k = H / (2·tan(fovy/2)) / τ = H · Projection[1][1] / 2 / τ
//
// Taken once per frame, at the top of DrawWorld, from the projection and
// camera the frame was handed — before the shadow passes. A point light's
// cube faces choose their cut from the camera too. A sun's orthographic views
// (each cascade, each virtual-shadow-map rectangle) choose one by their own
// texel instead (shadowOrthoLOD): the camera's cut is centimetre detail round
// the player, and an orthographic view takes all of it — no occlusion, no
// fall-off with distance — however coarse its texels are. A cascade or a
// coarse page over a city drew every building near the camera at full detail
// into texels a metre across. A caller drawing outside DrawWorld (a GL test,
// a preview) gets the camera and projection as they stand at each pass.

import (
	"foundry/internal/gfx"
	"foundry/internal/platform"

	"github.com/go-gl/mathgl/mgl32"
)

// NaniteLOD switches the LOD selection: off draws every cooked mesh at its
// full detail, as Phase 1 did.
var NaniteLOD = gfx.EnvFlag("FOUNDRY_NANITE_LOD", true)

// NaniteShadowLODScale is how many of its own texels a cluster's error may
// cover in a sun shadow view, as a multiple of gfx.NaniteLODError: 0 draws
// the camera's cut there, as before shadowOrthoLOD (FOUNDRY_NANITE_SHADOW_LOD).
var NaniteShadowLODScale = float32(envFloat("FOUNDRY_NANITE_SHADOW_LOD", 1))

// shadowLODTexel is the world size of a texel of the orthographic shadow view
// being drawn, set round its walk by the pass that draws it (the cascades,
// the virtual map's rectangles); 0 outside one.
var shadowLODTexel float32

// shadowOrthoDistance is how far back along the light shadowOrthoLOD puts its
// eye. An orthographic view is a perspective one from infinitely far; from
// here every cluster in a view is the same distance to a few parts in ten
// thousand (sideways is second order), which is the error test's own slack.
const shadowOrthoDistance = 1e5

// shadowOrthoLOD is the LOD parameters for an orthographic view looking along
// dir (unit, from the light into the scene) with texels texel across: a
// cluster is fine enough when its error covers no more than τ texels. The
// test stays error·k <= d — the eye far back along the light makes d the same
// everywhere — so the shaders need nothing new and the cut is a cut of the
// DAG like any other.
func shadowOrthoLOD(dir mgl32.Vec3, texel float32) naniteLODView {
	tau := min(max(gfx.NaniteLODError, gfx.NaniteLODErrorMin), gfx.NaniteLODErrorMax) * NaniteShadowLODScale
	return naniteLODView{
		eye: gfx.Cam.Position.Sub(dir.Mul(shadowOrthoDistance)),
		k:   shadowOrthoDistance / (tau * texel),
	}
}

// naniteLODWalk runs the tree walk ahead of the per-cluster LOD test. It
// changes nothing but cost — the walk only skips what the test rejects — and
// is a switch so the tests can hold it to that.
var naniteLODWalk = true

// naniteLODView is what the LOD test needs of the camera.
type naniteLODView struct {
	eye mgl32.Vec3
	k   float32
}

// naniteLODFrame is this frame's, while DrawWorld runs.
var naniteLODFrame struct {
	set  bool
	view naniteLODView
}

// beginNaniteLODFrame fixes the frame's LOD parameters; endNaniteLODFrame
// lets them go.
func beginNaniteLODFrame() {
	naniteLODFrame.view = liveNaniteLOD()
	naniteLODFrame.set = true
	// A new frame for the page pool too (nanite_stream.go): it updates once,
	// before the frame's first cull, so every pass sees one residency.
	naniteStreamFrame++
}

func endNaniteLODFrame() { naniteLODFrame.set = false }

// currentNaniteLOD is the parameters a pass culls with: the frame's, or the
// camera as it stands.
func currentNaniteLOD() naniteLODView {
	if naniteLODFrame.set {
		return naniteLODFrame.view
	}
	return liveNaniteLOD()
}

// liveNaniteLOD reads them off gfx.Cam, gfx.Projection and the frame's
// height.
func liveNaniteLOD() naniteLODView {
	h := float32(platform.WinHeight)
	if drawTarget.set && drawTarget.t.Viewport[3] > 0 {
		h = float32(drawTarget.t.Viewport[3])
	}
	if h < 1 {
		h = 1
	}
	tau := min(max(gfx.NaniteLODError, gfx.NaniteLODErrorMin), gfx.NaniteLODErrorMax)
	cot := gfx.Projection[5] // 1 / tan(fovy/2)
	if cot <= 0 {
		cot = 1
	}
	return naniteLODView{eye: gfx.Cam.Position, k: h * cot / 2 / tau}
}
