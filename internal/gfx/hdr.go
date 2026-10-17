package gfx

// hdr.go — high dynamic range shading and the filmic tone curve
// (docs/canyon/PHASE_7.md, "Lighting").
//
// The engine has always shaded in low dynamic range: fragment.glsl clamps the
// summed light to [0, 1] before it multiplies the albedo, and what a pixel
// holds is what the screen shows. That is why a sunlit cliff can never be
// brighter than its texture: there is nowhere for "brighter than white" to
// go, so a sun strong enough to make the rock glow simply flattens it.
//
// A level may opt into HDR (world.Tonemap, the level's `tonemap:` block).
// Then, in a frame drawn into the renderer's scene target (render/taa.go's
// scene frame — the RGBA16F target TAA runs in), the light is clamped at
// HDRLightMax instead of 1, so a strong sun and a dim ambient keep their
// ratio, and the frame's last pass (taa_output_frag.glsl) maps the result to
// the screen through EXPOSURE and a FILMIC CURVE: Narkowicz's fit of the ACES
// reference rendering transform, which rolls highlights off smoothly instead
// of clipping them and lifts the mid-tones' contrast. Anything not in a scene
// frame (the editor's own view, a preview) stays LDR, as before.
//
// DISPLAY-REFERRED INPUTS. Much of a frame was authored as screen colour,
// not as light: the skybox, the fog's colours, unlit surfaces, and every
// shading pass that does not know about HDR (biome scatter, sprites, block
// terrain, foliage). Run through the curve as they are, they would come out
// darker and shifted. So in an HDR frame each is first carried into the
// scene's units by the curve's INVERSE (fog.glsl's hdrFromDisplay):
// tonemap(inverse(c) / exposure · exposure) = c, and they come out on screen
// exactly as authored. Only fragment.glsl's lit surfaces are shaded in scene
// units. Additive blends (sparks) are left as they are and so read a little
// dimmer under the curve.
//
// BAKED LIGHT. The light baker writes LDR light, clamped to 1. In an HDR
// frame a baked surface's light is taken as scene-linear at exposure 1, as
// it stands — not inverted, because it is light, not screen colour. So a
// baked level that opts in sees its brightest baked light at the curve's
// value for 1 (about 0.8) times the exposure, and its live lights may run
// above 1 beside it; raise exposure or the bake's light to match. The canyon
// template's scenery is all lit live, so it is not affected.
//
// HDRFrame is set by the renderer at the start of every frame: true when the
// frame draws into the scene target, so world.PackFog only switches the
// shaders to HDR where the target can hold it.

import "math"

// HDRFrame is whether the frame being drawn is a scene-target frame
// (render.sceneFrameBegin sets and clears it).
var HDRFrame bool

// HDRByDefault is whether a level without a `tonemap:` block shades in HDR,
// with automatic exposure (docs/lighting/PLAN.md, phase 4). FOUNDRY_HDR=0
// turns it off: every such level back to the LDR shading of before.
var HDRByDefault = EnvFlag("FOUNDRY_HDR", true)

// AutoExposure is the CPU's side of an auto level's exposure: what its
// compensation multiplies in the scene block's hdrExposure, which carries the
// display-referred colours into the scene's units. The adapted exposure
// itself lives on the GPU (render/exposure.go) and is never read back; this
// stays at the reference, DefaultAutoExposure, which the output divides the
// adapted value by.
var AutoExposure float32 = DefaultAutoExposure

// DefaultAutoExposure is the reference exposure: about what the adaptation
// settles at in an ordinary daylit level, where its mean lands where the LDR
// shading had it under the ACES curve (render/exposure.go's key).
const DefaultAutoExposure = 0.75

// HDRLightMax is the brightest summed light an HDR frame keeps (the LDR
// frame's is 1): far past any sun a level sets, short of overflowing the
// RGBA16F target once multiplied by an albedo.
const HDRLightMax = 64

// The ACES fit's coefficients: (x(Ax + B)) / (x(Cx + D) + E).
const (
	acesA = 2.51
	acesB = 0.03
	acesC = 2.43
	acesD = 0.59
	acesE = 0.14
)

// ACESFilm is the filmic curve taa_output_frag.glsl applies per channel to
// exposure × the scene's value, clamped to [0, 1]. The Go copy is for tests
// and for anything that must agree with the shader.
func ACESFilm(x float64) float64 {
	x = math.Max(x, 0)
	y := (x * (acesA*x + acesB)) / (x*(acesC*x+acesD) + acesE)
	return math.Min(math.Max(y, 0), 1)
}

// ACESFilmInverse is the scene value the curve maps to y (0 ≤ y < 1): the
// positive root of (Cy − A)x² + (Dy − B)x + Ey = 0. fog.glsl's
// hdrFromDisplay is this divided by the exposure. y is held under the
// curve's limit (A/C ≈ 1.03, and 1 is reached at about 7.2).
func ACESFilmInverse(y float64) float64 {
	y = math.Min(math.Max(y, 0), HDRDisplayMax)
	a := acesC*y - acesA
	b := acesD*y - acesB
	c := acesE * y
	disc := math.Max(b*b-4*a*c, 0)
	return (-b - math.Sqrt(disc)) / (2 * a)
}

// HDRDisplayMax is the brightest display value hdrFromDisplay inverts: just
// under white, whose inverse is finite (about 7.2) but steep.
const HDRDisplayMax = 0.999
