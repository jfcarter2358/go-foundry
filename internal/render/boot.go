package render

// boot.go — standing up everything that draws the world.
//
// The shaders, the shadow pipeline, the passes that draw level content and the
// uniform locations they all read: one sequence, in one place, because there is
// more than one thing that needs it. app.Run needs it after it has made its
// window; the headless renderer behind `foundry render` needs it after it has
// made an offscreen context; a GL test that draws through the world program
// needs it too, and the ones that stood up their own half of it are exactly how
// the "nothing rasterises, every draw is GL_INVALID_OPERATION" class of bug
// keeps being rediscovered.
//
// It assumes a current GL context and nothing else. It does not make a window,
// does not touch the swap interval, and does not load a level.

import (
	"fmt"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// The depth-only programs behind the shadow maps. Vars rather than constants
// because that is how they have always been, and a tool that wants to point at
// a different set has one place to say so.
var (
	ShadowVertShaderPath = "shaders/shadow_vert.glsl"
	ShadowFragShaderPath = "shaders/shadow_frag.glsl"
	ShadowCubeVertPath   = "shaders/shadow_cube_vert.glsl"
	ShadowCubeFragPath   = "shaders/shadow_cube_frag.glsl"
)

// StandUpWorld loads the world program and every pass that draws through it,
// and leaves the GL state the engine draws in: depth testing on, the viewport
// filling w by h, and each of the world program's uniforms at its documented
// default.
func StandUpWorld(w, h int32) error {
	// Resolves the anisotropic filtering level for world textures. Must come
	// before the first texture upload — LoadTexture reads it at upload time.
	// See texture_filter.go for why mipmapping is the half of anti-aliasing
	// MSAA cannot do.
	gfx.InitTextureFiltering()
	// Live GI's tier for a player who has never chosen one, by GPU: on a
	// discrete card, off on an integrated one (gfx.PickDefaultGIQuality).
	renderer := gl.GoStr(gl.GetString(gl.RENDERER))
	gfx.PickDefaultGIQuality(renderer)
	// And the lighting pass's tiers on the same terms (gfx/lighting_settings.go).
	gfx.PickDefaultLighting(renderer)
	// And the Nanite page pool's budget, by the card's memory
	// (gfx/nanite_pool_default.go).
	gfx.PickDefaultNaniteStreamPool()

	gl.Enable(gl.DEPTH_TEST)
	gl.ClearColor(0.2, 0.2, 0.3, 1.0)
	gl.ClearDepth(1)
	gl.DepthFunc(gl.LEQUAL)
	gl.Viewport(0, 0, w, h)

	vert := fileutil.ShaderPath(gfx.VertexShaderPath)
	frag := fileutil.ShaderPath(gfx.FragmentShaderPath)
	if err := gfx.LoadProgram(vert, frag); err != nil {
		return fmt.Errorf("LoadProgram: %w", err)
	}
	gl.UseProgram(gfx.Program)

	// The shadow pipeline: depth-only shaders and their FBOs. A draw through the
	// world program is invalid without it even in a scene with no shadow-casting
	// light, because fragment.glsl declares a sampler2DArrayShadow and a
	// samplerCube array, and an unassigned sampler defaults to unit 0 where the
	// albedo sampler2D already lives. Three sampler types on one unit makes
	// every draw GL_INVALID_OPERATION and nothing rasterises.
	if err := InitShadows(
		fileutil.ShaderPath(ShadowVertShaderPath),
		fileutil.ShaderPath(ShadowFragShaderPath),
		fileutil.ShaderPath(ShadowCubeVertPath),
		fileutil.ShaderPath(ShadowCubeFragPath),
	); err != nil {
		return fmt.Errorf("InitShadows: %w", err)
	}

	// Every pass that draws level content: biome scatter and ground patches,
	// procedural face detail, sprites and particles, and the sky. Here rather
	// than in the editor's init because none of them is an authoring aid, and a
	// release build has no editor to be stood up from — see InitWorldPasses,
	// which exists because that is exactly how they came to be missing from
	// shipped games.
	InitWorldPasses()

	StandUpWorldUniforms()
	return nil
}

// StandUpWorldUniforms resolves the world program's uniform locations and sets
// each one to the default the engine expects to find it at.
//
// Split out from StandUpWorld because it is what a re-link needs: every location
// is invalidated by one, and gfx.ForgetUniformLocations alone only clears the
// memo — the handles cached in gfx's own variables are still the old program's.
func StandUpWorldUniforms() {
	// Every gl.Uniform* below writes the program in use, and StandUpWorld
	// reaches here after InitShadows and InitWorldPasses have each linked and
	// bound programs of their own. Without this the projection — and every
	// default after it — went into whichever of those was bound last, and the
	// world program kept a zero projection until something else re-sent it:
	// on a level with a viewmodel, that pass's restore at the end of the first
	// frame; on one without (the Nanite bench levels), never, so every world
	// draw collapsed to nothing and only the clear colour and the sky (own
	// program) were ever captured (docs/nanite/PHASE_4.md).
	gl.UseProgram(gfx.Program)
	gfx.ProjectionUniform = gl.GetUniformLocation(gfx.Program, gl.Str("projection\x00"))
	gfx.RebuildProjection()

	// The view matrix's location, and the matrix itself from wherever the
	// camera currently is.
	//
	// Here rather than only in gfx.LoadCamera, which is where it used to live
	// alone: that function belongs to a window — it grabs the mouse — so
	// anything that stood the world up without one (a headless render, a play
	// session's screenshot, a GL test) left this at 0. Zero is a perfectly
	// valid location for a DIFFERENT uniform, so the symptom was not an error
	// but a world drawn through a view matrix that was never uploaded: every
	// brush collapsed, the sky and the models (which have their own program)
	// came out fine, and the picture looked like a level with no geometry in
	// it. Resolving it with the rest of the program's uniforms also means a
	// re-link cannot leave it stale.
	gfx.Cam.Uniform = gl.GetUniformLocation(gfx.Program, gl.Str("camera\x00"))
	gfx.UpdateCamMatrix()

	gfx.AlphaUniform = gl.GetUniformLocation(gfx.Program, gl.Str("alpha\x00"))
	gl.Uniform1f(gfx.AlphaUniform, 1.0) // default: fully opaque

	gfx.SmoothUniform = gl.GetUniformLocation(gfx.Program, gl.Str("smoothShading\x00"))
	gl.Uniform1i(gfx.SmoothUniform, 0) // default: flat facet normals

	gfx.BakedLightUniform = gl.GetUniformLocation(gfx.Program, gl.Str("bakedLight\x00"))
	gl.Uniform1i(gfx.BakedLightUniform, 0) // default: light the fragment the ordinary way

	gfx.NormalColorUniform = gl.GetUniformLocation(gfx.Program, gl.Str("normalColor\x00"))
	gl.Uniform1i(gfx.NormalColorUniform, 0) // default: sample tex, as a textured surface does

	gfx.UnlitUniform = gl.GetUniformLocation(gfx.Program, gl.Str("unlit\x00"))
	gl.Uniform1i(gfx.UnlitUniform, 0) // default: lit

	gfx.UVOffsetUniform = gl.GetUniformLocation(gfx.Program, gl.Str("uvOffset\x00"))
	gl.Uniform2f(gfx.UVOffsetUniform, 0, 0) // default: the texture where the UVs put it
	// The rest of a material's UV transform and the cutout threshold. All
	// three are biases from the identity, so zero is what the engine did
	// before materials existed — see gfx.ApplyUVUniforms.
	gfx.UVScaleBiasUniform = gl.GetUniformLocation(gfx.Program, gl.Str("uvScaleBias\x00"))
	gl.Uniform2f(gfx.UVScaleBiasUniform, 0, 0)
	gfx.UVRotationUniform = gl.GetUniformLocation(gfx.Program, gl.Str("uvRotation\x00"))
	gl.Uniform1f(gfx.UVRotationUniform, 0)
	gfx.AlphaCutoffBiasUniform = gl.GetUniformLocation(gfx.Program, gl.Str("alphaCutoffBias\x00"))
	gl.Uniform1f(gfx.AlphaCutoffBiasUniform, 0)
	// The albedo tint, a bias from white on the same terms — zero is no tint.
	gfx.TintBiasUniform = gl.GetUniformLocation(gfx.Program, gl.Str("tintBias\x00"))
	gl.Uniform3f(gfx.TintBiasUniform, 0, 0, 0)
	// How the framebuffer blend will combine a fragment. Zero is the ordinary
	// case, so nothing that does not set it changes.
	gfx.SurfaceBlendUniform = gl.GetUniformLocation(gfx.Program, gl.Str("surfaceBlend\x00"))
	gl.Uniform1i(gfx.SurfaceBlendUniform, 0)
	// Per-vertex colour and cavity (docs/canyon/PHASE_2.md). Zero is every
	// surface without colours.
	gfx.VertexColorsUniform = gl.GetUniformLocation(gfx.Program, gl.Str("vertexColors\x00"))
	gl.Uniform1i(gfx.VertexColorsUniform, 0)

	gfx.InstancedUniform = gl.GetUniformLocation(gfx.Program, gl.Str("instanced\x00"))
	gl.Uniform1i(gfx.InstancedUniform, 0) // default: the model matrix is the `model` uniform

	gfx.AlphaSharpenUniform = gl.GetUniformLocation(gfx.Program, gl.Str("alphaSharpen\x00"))
	gl.Uniform1i(gfx.AlphaSharpenUniform, 0) // default: off; the opaque pass turns it on

	gfx.TimeUniform = gl.GetUniformLocation(gfx.Program, gl.Str("time\x00"))
	gfx.CamPosUniform = gl.GetUniformLocation(gfx.Program, gl.Str("camPos\x00"))
	gfx.IsWaterUniform = gl.GetUniformLocation(gfx.Program, gl.Str("isWater\x00"))
	gfx.WaveAmplitudeUniform = gl.GetUniformLocation(gfx.Program, gl.Str("waveAmplitude\x00"))
	gfx.WaveLengthUniform = gl.GetUniformLocation(gfx.Program, gl.Str("waveLength\x00"))
	gfx.WaveSpeedUniform = gl.GetUniformLocation(gfx.Program, gl.Str("waveSpeed\x00"))
	gfx.ReflectivityUniform = gl.GetUniformLocation(gfx.Program, gl.Str("reflectivity\x00"))
	gfx.ReflectColorUniform = gl.GetUniformLocation(gfx.Program, gl.Str("reflectColor\x00"))
	gl.Uniform1i(gfx.IsWaterUniform, 0)

	gfx.HasBlendUniform = gl.GetUniformLocation(gfx.Program, gl.Str("hasBlend\x00"))
	gfx.BlendTexUniform = gl.GetUniformLocation(gfx.Program, gl.Str("blendTex\x00"))
	gfx.BlendMaskUniform = gl.GetUniformLocation(gfx.Program, gl.Str("blendMask\x00"))
	gl.Uniform1i(gfx.HasBlendUniform, 0) // default: no second texture

	gfx.HasNormalMapUniform = gl.GetUniformLocation(gfx.Program, gl.Str("hasNormalMap\x00"))
	gfx.NormalMapUniform = gl.GetUniformLocation(gfx.Program, gl.Str("normalMap\x00"))
	gl.Uniform1i(gfx.HasNormalMapUniform, 0) // default: flat, no relief
}
